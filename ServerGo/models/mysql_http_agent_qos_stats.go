// v2.0.84: 源站大模型服务质量监控（QoS）聚合函数 —— 时间维度数据质量优化
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控时间维度优化方案_20261022_124609.md
// 前置：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md（v2.0.83）
//
// 核心思路 —— Token 归一化 + 分位统计：
//   - 基准推理速度按**输出 Token 分档**计算（10 档对数分箱），
//     每档 bandBaseline = Σ elapsed_ms / Σ tokens_output（仅 2xx 且 tokens>0），
//     消除「固定开销摊到 Token 上」导致的小请求拥堵系数虚高；
//     样本 < qosBandMinSamples 的档回退全局基准。
//   - 拥堵系数 = elapsed_ms / (bandBaseline × tokens_output)，
//     分档 <1.2 正常 / 1.2~2 轻度排队 / 2~5 中度排队 / >5 严重排队。
//   - 时延统计分离成功/失败：ok_* 仅统计 2xx（429/5xx 快速失败会拉低时延，
//     混入会让「故障越多、时延越好看」）；avg/p95 保留全量口径供兼容。
//   - 抖动率 = 拥堵系数的变异系数（无量纲），排除请求体量影响。
//   - 卡顿率 = 窗口内系数 > 2.0 的请求占比（coefficient_samples 供低样本提示）。
//   - 429 / 5xx / 其他错误率按状态码分类统计。
//   - 进行中的分钟/小时/天桶标记 partial，避免与完整桶直接比较。
//
// v2.0.85（阶段CZ）：模型状态总览统计化 + 运行经验预测，方案见
// docs/项目迁移解决方案/源站模型服务质量监控统计与预测方案_20261022_132957.md
//   - stats_24h / stats_7d：固定窗口统计（小时桶合并后一次 finalize，口径与桶一致）；
//   - prediction：同时段历史加权均值（next_hour / today_remaining）、
//     星期模式加权（tomorrow，同星期 ×2）、趋势研判（近24h vs 前24h、今日 vs 周均值）。
//
// v2.0.86（阶段DA）：统计化重构 + 全模型对比 + 高峰低谷预测，方案见
// docs/项目迁移解决方案/源站模型服务质量监控统计化与峰谷预测方案_20261022_143602.md
//   - hour_profile：近 7 天「同时段」小时画像（24 点），复用 m.hours 无二次扫描；
//   - bands_24h / bands_7d：业务时段带（凌晨/早间/上午/午间/下午/晚间）统计；
//   - peak_valley：峰/谷小时识别（调用量口径）+ 今日剩余预测峰 + 明日高峰时段带；
//   - 报告级 hour_profile / bands / peak_valley：全站汇总（跨模型样本加权，不混算耗时）；
//   - QoSWindowStats 增 status/alert：窗口口径状态分档（替代实时窗口的抖动状态）。
//
// v2.0.87（阶段DB）：多窗口统计（近 1 天 / 1 周 / 1 月）+ 源站横向差异，方案见
// docs/项目迁移解决方案/源站模型服务质量监控多窗口统计与源站横向对比方案_20261022_150702.md
//   - 扫描窗口 7d → 30d；基准 / 预测 / 周分位显式固定为近 7 天（防止口径随扫描漂移）；
//   - 每模型新增 windows{24h,7d,30d}（stats/hour_profile/bands/peak_valley/trend）
//     与 days_30d / stats_30d / calls_30d，前端一次取数、三窗口切换；
//   - 报告级新增 windows 全站汇总（跨模型样本加权）与 total_calls_30d；
//   - 既有字段（stats_24h/stats_7d/hour_profile/bands_*/days）语义不变，保证兼容。
//
// 表定义研读结论（TAgentHttpTransactionDataItem，v2.0.86）：
//   - request_start_at 才是「用户发起调用」的时刻 → 时段/峰谷分析必须用它；
//     created_at ≈ request_start_at + 响应耗时，用它会让峰谷失真。
//   - request_end_at / response_start_at 在代理路径落库时被写成 request_start_at 的副本
//     （proxy/server_http_ai_proxy.go），TTFB / 流式生成耗时不可用 → 本模块不引入该指标。
//   - 排队拥堵无直接度量，只能由「输出 Token 归一化拥堵系数」反推。
//
// 实现约束（与既有 All 系列统计一致）：
//   - 遍历 8 张 TAgentHttpTransactionDataItem 分表，keyset 分页扫描（v2.0.58 模式），
//     每批 qosScanBatch 行，内存有界
//   - SELECT 仅小字段，禁含 8 个 longtext（v2.0.42 白名单契约）
//   - 全部走 database.StatsDB() 25s context（v2.0.54 强制规则）
//   - Go 端跨分表合并聚合；NilDB / ctx 取消安全
//   - 时间桶键复用 statsTimeBucketKey 整点对齐语义（minute/hour 截断）
package models

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	"gorm.io/gorm"
)

// QoS 聚合常量
const (
	// qosScanWindow 扫描窗口：最近 30 天（v2.0.87 阶段DB 起覆盖「近 1 个月」统计）。
	// 基准 / 预测 / 周分位仍固定取近 7 天（见 qosBaselineWindow / qosForecastWindow），
	// 否则同一请求的拥堵系数会随扫描窗口变化，历史对比与 1.2/2/5 阈值全部失真。
	qosScanWindow = 30 * 24 * time.Hour
	// qosBaselineWindow 拥堵系数基准窗口：固定 7 天（不受扫描窗口影响）。
	qosBaselineWindow = 7 * 24 * time.Hour
	// qosForecastWindow 运行经验预测的历史窗口：固定 7 天同时段。
	qosForecastWindow = 7 * 24 * time.Hour
	// qosScanBatch 单批扫描行数（keyset 分页）。
	qosScanBatch = 5000
	// qosMinuteBuckets 分钟桶数量（近 1 小时折线视图）。
	qosMinuteBuckets = 60
	// qosCongestionCap 单请求拥堵系数上限截断（防脏数据拉爆均值）。
	qosCongestionCap = 100.0
	// qosCacheTTL 报告缓存时长（v2.0.86：前端已移除 30s 自动刷新，
	// 缓存仅用于保护服务端免受高频全表扫描；手动刷新走 refresh=true 旁路）。
	qosCacheTTL = 60 * time.Second
	// qosTokenBandCount Token 分档数量 = len(qosTokenBandEdges)+1。
	qosTokenBandCount = 11
	// qosBandMinSamples 单档最小校准样本，不足则回退全局基准。
	qosBandMinSamples = 30
	// qosLiveMinSamples 实时窗口阶梯选择的最小样本数。
	qosLiveMinSamples = 5
	// qosMinCoefficientSamples 系数类指标的低样本提示阈值（前端同步展示）。
	qosMinCoefficientSamples = 5
	// qosForecastHighSamples / qosForecastMediumSamples 预测可信度分档
	// （历史同时段样本数）：≥30 高、≥10 中、否则低（仅供参考）。
	qosForecastHighSamples   = 30
	qosForecastMediumSamples = 10
	// qosProfileDays 小时画像的归一化天数（近 7 天窗口；30 天画像按实际有数据天数归一）。
	qosProfileDays = 7
)

// qosTimeBandDef 业务时段带定义（v2.0.86）。
//
// 按运维经验把一天切成 6 段，回答「早上 9 点到 12 点、下午 2 点到 6 点」这类
// 使用强度差异问题。区间为左闭右开 [StartHour, EndHour)。
// 注意各带时长不同（凌晨 6h、午间 2h…），横向比较必须看「日均调用」或「占比」，
// 不能直接比「总调用数」。
type qosTimeBandDef struct {
	Key       string
	StartHour int
	EndHour   int
}

// qosTimeBands 业务时段带（顺序即前端展示顺序）。
var qosTimeBands = []qosTimeBandDef{
	{Key: "dawn", StartHour: 0, EndHour: 6},        // 凌晨 00:00-06:00
	{Key: "morning", StartHour: 6, EndHour: 9},     // 早间 06:00-09:00
	{Key: "forenoon", StartHour: 9, EndHour: 12},   // 上午 09:00-12:00
	{Key: "noon", StartHour: 12, EndHour: 14},      // 午间 12:00-14:00
	{Key: "afternoon", StartHour: 14, EndHour: 18}, // 下午 14:00-18:00
	{Key: "evening", StartHour: 18, EndHour: 24},   // 晚间 18:00-24:00
}

// 拥堵分档阈值（方案 §3.2）
const (
	qosTierNormal   = 1.2 // < 1.2 正常
	qosTierLight    = 2.0 // 1.2 ~ 2.0 轻度排队
	qosTierModerate = 5.0 // 2.0 ~ 5.0 中度排队；> 5.0 严重排队
	// qosStallThreshold 卡顿判定：系数 > 2.0
	qosStallThreshold = 2.0
	// qosAlertErrorRate 卡片告警错误率阈值（%）
	qosAlertErrorRate = 5.0
)

// qosTokenBandEdges 输出 Token 分档上界（对数分布，覆盖 1 ~ 32768+）。
var qosTokenBandEdges = []uint64{64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768}

// qosLiveWindowLadder 实时窗口阶梯（分钟）：取第一个样本数达标的窗口。
var qosLiveWindowLadder = []int{5, 15, 60}

// QoSBucketPoint 单时间桶的服务质量聚合结果。
//   - Count 为桶内总请求数；SuccessCount 为 2xx 请求数；Error* 为分类错误计数。
//   - Ok* 为成功请求（2xx）时延口径，前端趋势图/时段表主用；
//     AvgElapsedMs / P95ElapsedMs 为全量口径（阶段 CX 兼容字段）。
//   - Jitter 为拥堵系数变异系数（%）；CoefficientSamples 为可算系数样本数。
//   - Partial 为进行中时间桶（当前分钟/小时/天），不应与完整桶直接比较。
type QoSBucketPoint struct {
	Bucket             string  `json:"bucket"`
	Partial            bool    `json:"partial"`
	Count              int64   `json:"count"`
	SuccessCount       int64   `json:"success_count"`
	AvgElapsedMs       int64   `json:"avg_elapsed_ms"`
	P95ElapsedMs       int64   `json:"p95_elapsed_ms"`
	OkAvgElapsedMs     int64   `json:"ok_avg_elapsed_ms"`
	OkP50Ms            int64   `json:"ok_p50_ms"`
	OkP95Ms            int64   `json:"ok_p95_ms"`
	OkMsPerToken       float64 `json:"ok_ms_per_token"`
	AvgCongestion      float64 `json:"avg_congestion"`
	CoefficientSamples int64   `json:"coefficient_samples"`
	StallRate          float64 `json:"stall_rate"`
	Jitter             float64 `json:"jitter"`
	Error429           int64   `json:"error_429"`
	Error5xx           int64   `json:"error_5xx"`
	ErrorOther         int64   `json:"error_other"`
	ErrorRate          float64 `json:"error_rate"`
	TokensOutput       uint64  `json:"tokens_output"`
}

// QoSLiveStats 实时窗口指标，用于状态卡。
//   - WindowMinutes 为实际使用的窗口（5/15/60）：低流量模型逐级回退，
//     不再因为固定 5 分钟窗口而误标「无流量」。
//   - Coefficient = 0 表示窗口内无可算拥堵样本（如只有错误请求或无流量）。
type QoSLiveStats struct {
	Coefficient        float64 `json:"coefficient"`
	AvgElapsedMs       int64   `json:"avg_elapsed_ms"`
	OkAvgElapsedMs     int64   `json:"ok_avg_elapsed_ms"`
	ErrorRate          float64 `json:"error_rate"`
	ReqCount           int64   `json:"req_count"`
	SuccessCount       int64   `json:"success_count"`
	WindowMinutes      int     `json:"window_minutes"`
	CoefficientSamples int64   `json:"coefficient_samples"`
	StallRate          float64 `json:"stall_rate"`
	Jitter             float64 `json:"jitter"`
	LastRequestAtMs    int64   `json:"last_request_at_ms"`
}

// QoSWindowStats 固定窗口（24h/7d）统计：由窗口内小时桶合并后经 finalizeQoSBucket
// 一次算出，与小时桶/天桶完全同一套口径（分档基准、成功时延、低样本语义一致）。
type QoSWindowStats struct {
	Window             string  `json:"window"` // "24h" | "7d"
	Calls              int64   `json:"calls"`
	SuccessRate        float64 `json:"success_rate"` // %
	AvgCongestion      float64 `json:"avg_congestion"`
	CoefficientSamples int64   `json:"coefficient_samples"`
	StallRate          float64 `json:"stall_rate"` // %
	Jitter             float64 `json:"jitter"`     // %
	ErrorRate          float64 `json:"error_rate"` // %
	OkAvgElapsedMs     int64   `json:"ok_avg_elapsed_ms"`
	OkP95Ms            int64   `json:"ok_p95_ms"`
	OkMsPerToken       float64 `json:"ok_ms_per_token"`
	// Status/Alert（v2.0.86）：窗口口径的状态分档与告警标记。
	// 状态卡改用本字段（替代实时窗口状态），避免 5 分钟窗口抖动导致卡片忽绿忽红。
	Status string `json:"status"` // ok|light|moderate|severe|idle
	Alert  bool   `json:"alert"`
}

// QoSWindowView 单个统计窗口（24h/7d/30d）的完整视图（v2.0.87 阶段DB）。
//
// 前端按窗口一次取用，避免在三组并列字段间自行拼装：
//   - Stats：固定窗口统计（与小时桶同一套口径）；
//   - HourProfile：窗口内「同时段」24 点画像（按该小时有数据的天数归一）；
//   - Bands：6 个业务时段带（由 HourProfile 归并）；
//   - PeakValley：该窗口的高峰/低谷（峰谷会随窗口变化，20 天前的峰不应冒充近 7 天峰）；
//   - Trend：趋势序列（24h → 24 小时桶；7d/30d → 7/30 天桶）。
type QoSWindowView struct {
	Window      string           `json:"window"`
	Stats       QoSWindowStats   `json:"stats"`
	HourProfile []QoSHourProfile `json:"hour_profile"`
	Bands       []QoSTimeBand    `json:"bands"`
	PeakValley  QoSPeakValley    `json:"peak_valley"`
	Trend       []QoSBucketPoint `json:"trend"`
}

// QoSHourProfile 单小时画像（v2.0.86）：近 7 天「同时段」汇总，24 点。
//
// 回答「哪个小时是高峰/低谷」「源站在某个时段的典型表现」。字段口径：
//   - Calls：近 7 天该小时的总调用数；
//   - AvgCallsPerDay：日均调用 = Calls / Days（Days = 该小时有数据的天数，
//     用有数据的天数而非固定 7，避免"某模型只在一天有流量"时被摊薄成假低谷）；
//   - AvgCongestion：样本加权的平均拥堵系数；
//   - OkP95Ms：该小时 7 天全部成功请求的 P95（跨天分位，代表该时段的典型 P95）。
type QoSHourProfile struct {
	Hour               int     `json:"hour"` // 0~23
	Calls              int64   `json:"calls"`
	SuccessCount       int64   `json:"success_count"`
	Days               int64   `json:"days"` // 该小时有数据的天数
	AvgCallsPerDay     float64 `json:"avg_calls_per_day"`
	AvgCongestion      float64 `json:"avg_congestion"`
	ErrorRate          float64 `json:"error_rate"` // %
	StallRate          float64 `json:"stall_rate"` // %
	OkAvgElapsedMs     int64   `json:"ok_avg_elapsed_ms"`
	OkP95Ms            int64   `json:"ok_p95_ms"`
	CoefficientSamples int64   `json:"coefficient_samples"`
	Confidence         string  `json:"confidence"` // high | medium | low
}

// QoSTimeBand 业务时段带统计（v2.0.86）：由小时画像按时段带归并。
//
// 关键口径：各带时长不同（凌晨 6h、午间 2h…），横向比较必须看归一化指标：
//   - AvgCallsPerDay  = 该带每天的调用总量（贡献度）；
//   - AvgCallsPerHour = 该带平均每小时的调用强度（**跨带可比**）；
//   - Share           = 调用量占比 %。
//
// 直接比 Calls 会被"带越长数越大"误导。
type QoSTimeBand struct {
	Key                string  `json:"key"` // dawn|morning|forenoon|noon|afternoon|evening
	StartHour          int     `json:"start_hour"`
	EndHour            int     `json:"end_hour"` // 不含
	Calls              int64   `json:"calls"`
	Days               int64   `json:"days"`
	AvgCallsPerDay     float64 `json:"avg_calls_per_day"`
	AvgCallsPerHour    float64 `json:"avg_calls_per_hour"`
	AvgCongestion      float64 `json:"avg_congestion"`
	ErrorRate          float64 `json:"error_rate"` // %
	StallRate          float64 `json:"stall_rate"` // %
	OkAvgElapsedMs     int64   `json:"ok_avg_elapsed_ms"`
	OkP95Ms            int64   `json:"ok_p95_ms"` // 带内最差小时的 P95（保守口径）
	CoefficientSamples int64   `json:"coefficient_samples"`
	Share              float64 `json:"share"` // 调用量占比 %
	Confidence         string  `json:"confidence"`
}

// QoSPeakValley 高峰 / 低谷识别（v2.0.86）。
//
//   - PeakHour / ValleyHour：按「日均调用量」取极值的小时；
//     谷值只在有数据（Calls>0）的小时中取 —— 否则"凌晨完全没流量"会被误判为谷底。
//   - ForecastPeakHour：今日剩余时段中预测拥堵系数最高的小时（运行经验外推）。
//   - TomorrowPeakBandKey：明日预测拥堵最高的时段带。
//   - TopBands：按调用占比降序的时段带。
type QoSPeakValley struct {
	PeakHour               int           `json:"peak_hour"`
	PeakCallsPerDay        float64       `json:"peak_calls_per_day"`
	PeakCongestion         float64       `json:"peak_congestion"`
	ValleyHour             int           `json:"valley_hour"`
	ValleyCallsPerDay      float64       `json:"valley_calls_per_day"`
	ValleyCongestion       float64       `json:"valley_congestion"`
	PeakBandKey            string        `json:"peak_band_key"`
	ValleyBandKey          string        `json:"valley_band_key"`
	ForecastPeakHour       int           `json:"forecast_peak_hour"`
	ForecastPeakCongestion float64       `json:"forecast_peak_congestion"`
	ForecastPeakBandKey    string        `json:"forecast_peak_band_key"`
	TomorrowPeakBandKey    string        `json:"tomorrow_peak_band_key"`
	TomorrowPeakCongestion float64       `json:"tomorrow_peak_congestion"`
	TopBands               []QoSTimeBand `json:"top_bands"`
}

// QoSHourForecast 未来某小时的预测（源站运行经验：近 7 天同时段加权均值）。
type QoSHourForecast struct {
	Hour          int     `json:"hour"` // 0~23 目标小时
	AvgCongestion float64 `json:"avg_congestion"`
	ErrorRate     float64 `json:"error_rate"` // %
	Samples       int64   `json:"samples"`    // 历史同小时系数样本总数（可信度依据）
	Confidence    string  `json:"confidence"` // high | medium | low
}

// QoSDayForecast 明日预测（星期模式加权：同星期样本权重 ×2）。
type QoSDayForecast struct {
	Date          string  `json:"date"` // YYYY-MM-DD
	Weekday       int     `json:"weekday"`
	AvgCongestion float64 `json:"avg_congestion"`
	ErrorRate     float64 `json:"error_rate"` // %
	Calls         int64   `json:"calls"`      // 预计调用量
	Samples       int64   `json:"samples"`
	Confidence    string  `json:"confidence"` // high | medium | low
}

// QoSPrediction 运行经验预测：下一时段 / 今日剩余 / 明日 / 趋势研判。
type QoSPrediction struct {
	NextHour       QoSHourForecast   `json:"next_hour"`
	TodayRemaining []QoSHourForecast `json:"today_remaining"` // 当前小时+1 ~ 23 时
	Tomorrow       QoSDayForecast    `json:"tomorrow"`
	Trend24h       string            `json:"trend_24h"`     // up | down | flat | unknown
	TodayVsWeek    float64           `json:"today_vs_week"` // 今日(部分)相对周均值偏差 %
}

// QoSModelReport 单个目标源站模型的完整 QoS 报告。
type QoSModelReport struct {
	DstModelName       string           `json:"dst_model_name"`
	BaselineMsPerToken float64          `json:"baseline_ms_per_token"` // 全局 Σelapsed/ΣTokens（展示口径）
	BaselineBands      int              `json:"baseline_bands"`        // 已校准的 Token 分档数
	BaselineSamples    int64            `json:"baseline_samples"`
	Status             string           `json:"status"` // ok|light|moderate|severe|idle
	Alert              bool             `json:"alert"`
	Live               QoSLiveStats     `json:"live"`
	Minutes            []QoSBucketPoint `json:"minutes"`        // 近 60 分钟（升序）
	Hours              []QoSBucketPoint `json:"hours"`          // 近 24 小时（升序）
	Days               []QoSBucketPoint `json:"days"`           // 近 7 天（升序）
	Heatmap            [][]float64      `json:"heatmap"`        // [7][24] 桶均拥堵系数（-1 = 无数据）
	HeatmapCounts      [][]int64        `json:"heatmap_counts"` // [7][24] 桶请求数
	WeekP50Ms          int64            `json:"week_p50_ms"`
	WeekP95Ms          int64            `json:"week_p95_ms"`
	WeekOkP50Ms        int64            `json:"week_ok_p50_ms"`
	WeekOkP95Ms        int64            `json:"week_ok_p95_ms"`
	Calls24h           int64            `json:"calls_24h"`
	Calls7d            int64            `json:"calls_7d"`
	OkCalls7d          int64            `json:"ok_calls_7d"`
	ErrCalls7d         int64            `json:"err_calls_7d"`
	Calls30d           int64            `json:"calls_30d"`
	OkCalls30d         int64            `json:"ok_calls_30d"`
	LastRequestAtMs    int64            `json:"last_request_at_ms"`
	Stats24h           QoSWindowStats   `json:"stats_24h"`
	Stats7d            QoSWindowStats   `json:"stats_7d"`
	Stats30d           QoSWindowStats   `json:"stats_30d"`
	Prediction         QoSPrediction    `json:"prediction"`
	// 时段画像与峰谷（v2.0.86；v2.0.87 增加 24h/30d 两套）
	HourProfile    []QoSHourProfile `json:"hour_profile"`     // 24 点（近 7 天同时段汇总）
	HourProfile24h []QoSHourProfile `json:"hour_profile_24h"` // 24 点（近 24h 口径）
	HourProfile30d []QoSHourProfile `json:"hour_profile_30d"` // 24 点（近 30 天口径）
	Bands24h       []QoSTimeBand    `json:"bands_24h"`        // 6 个业务时段带（近 24h 口径）
	Bands7d        []QoSTimeBand    `json:"bands_7d"`         // 6 个业务时段带（近 7 天口径）
	Bands30d       []QoSTimeBand    `json:"bands_30d"`        // 6 个业务时段带（近 30 天口径）
	PeakValley     QoSPeakValley    `json:"peak_valley"`      // 近 7 天峰谷（兼容字段）
	PeakValley24h  QoSPeakValley    `json:"peak_valley_24h"`
	PeakValley30d  QoSPeakValley    `json:"peak_valley_30d"`
	Days30d        []QoSBucketPoint `json:"days_30d"` // 近 30 天逐日桶
	// Windows 按窗口打包（v2.0.87）：24h / 7d / 30d，前端直接按 key 取用。
	Windows map[string]QoSWindowView `json:"windows"`
}

// QoSMonitorReport 服务质量监控完整响应。
type QoSMonitorReport struct {
	GeneratedAtMs int64            `json:"generated_at_ms"`
	ModelCount    int              `json:"model_count"`
	TotalCalls7d  int64            `json:"total_calls_7d"`
	TotalCalls30d int64            `json:"total_calls_30d"`
	Models        []QoSModelReport `json:"models"`
	// 全站汇总（v2.0.86）：跨模型聚合。
	// 口径：调用量/错误数求和；拥堵系数按各模型系数样本数加权（不做耗时混算，
	// 各源站基准不同，混算无意义）；P95 取各模型中最差者（保守：全站最差体验）。
	HourProfile []QoSHourProfile `json:"hour_profile"` // 24 点
	Bands       []QoSTimeBand    `json:"bands"`        // 6 个时段带（近 7 天）
	BandsToday  []QoSTimeBand    `json:"bands_today"`  // 6 个时段带（今日）
	PeakValley  QoSPeakValley    `json:"peak_valley"`
	// Windows 全站口径三窗口视图（v2.0.87）：24h / 7d / 30d。
	Windows map[string]QoSWindowView `json:"windows"`
}

// ---- 扫描与聚合 ----

// qosScanRow QoS 聚合扫描行结构（仅小字段）。
type qosScanRow struct {
	ID             uint64    `gorm:"column:id"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	RequestStartAt time.Time `gorm:"column:request_start_at"`
	DstModelName   string    `gorm:"column:dst_model_name"`
	DstEndPointID  uint64    `gorm:"column:dst_endpoint_id"`
	ResponseStatus string    `gorm:"column:response_status"`
	TokensOutput   uint64    `gorm:"column:tokens_output_size"`
	ElapsedMs      int64     `gorm:"column:elapsed_ms"`
}

// qosRowPair 桶内原始请求（elapsed × output tokens × 是否成功）。
// 拥堵系数依赖分档基准终值（全窗口 Σ），扫描阶段只记原始值，终态统一重算。
type qosRowPair struct {
	elapsed int64
	tokens  uint64
	ok      bool
}

// qosBucketAcc 单桶增量累积器。
type qosBucketAcc struct {
	count     int64
	okCount   int64
	pairs     []qosRowPair
	err429    int64
	err5xx    int64
	errOther  int64
	tokensOut uint64 // 全量输出 Tokens
	okTokens  uint64 // 成功请求输出 Tokens（ms/Token 绝对速度）
}

func (b *qosBucketAcc) add(r qosScanRow, statusClass int) {
	b.count++
	b.pairs = append(b.pairs, qosRowPair{elapsed: r.ElapsedMs, tokens: r.TokensOutput, ok: statusClass == 0})
	switch statusClass {
	case 0:
		b.okCount++
		b.okTokens += r.TokensOutput
	case 1:
		b.err429++
	case 2:
		b.err5xx++
	case 3:
		b.errOther++
	}
	b.tokensOut += r.TokensOutput
}

// qosModelAcc 单模型累积器。
type qosModelAcc struct {
	name        string
	baseElapsed int64  // 全局基准分子：7 天成功请求 Σ elapsed
	baseTokens  uint64 // 全局基准分母：7 天成功请求 Σ output tokens
	baseSamples int64
	// Token 分档基准（消除固定开销导致的小请求系数虚高）
	bandElapsed [qosTokenBandCount]int64
	bandTokens  [qosTokenBandCount]uint64
	bandSamples [qosTokenBandCount]int64

	minutes       map[string]*qosBucketAcc
	hours         map[string]*qosBucketAcc
	calls7d       int64
	okCalls7d     int64
	calls30d      int64
	okCalls30d    int64
	lastRequestAt time.Time
}

// qosBaseline 模型基准（全局 + 分档），计算拥堵系数时的统一入口。
type qosBaseline struct {
	global     float64
	bands      [qosTokenBandCount]float64
	calibrated int
}

// qosTokenBandIndex 输出 Token → 分档下标（0..10）。边界值归入上档。
func qosTokenBandIndex(tokens uint64) int {
	return sort.Search(len(qosTokenBandEdges), func(i int) bool { return qosTokenBandEdges[i] > tokens })
}

// baseline 计算全局与分档基准。分档样本不足时回退全局值。
func (m *qosModelAcc) baseline() qosBaseline {
	b := qosBaseline{}
	if m.baseTokens > 0 && m.baseElapsed > 0 {
		b.global = float64(m.baseElapsed) / float64(m.baseTokens)
	}
	for i := 0; i < qosTokenBandCount; i++ {
		b.bands[i] = b.global
		if m.bandSamples[i] >= qosBandMinSamples && m.bandTokens[i] > 0 && m.bandElapsed[i] > 0 {
			b.bands[i] = float64(m.bandElapsed[i]) / float64(m.bandTokens[i])
			b.calibrated++
		}
	}
	return b
}

// expectedMs 返回该请求在无排队时的预期耗时；基准不可用返回 0。
func (b qosBaseline) expectedMs(tokens uint64) float64 {
	if tokens == 0 {
		return 0
	}
	perToken := b.bands[qosTokenBandIndex(tokens)]
	if perToken <= 0 {
		return 0
	}
	return perToken * float64(tokens)
}

// qosStatusClass 响应状态分类：0 成功 / 1 限流 429 / 2 服务故障 5xx / 3 其他错误。
// ParseResponseStatusCode 兼容 "200 OK"/"529 " 等完整状态文本。
func qosStatusClass(statusText string) int {
	code := ParseResponseStatusCode(statusText)
	switch {
	case code == 0:
		return 3 // 不可解析视为其他错误（与 isResponseSuccess 的 0=失败语义一致）
	case code == 429:
		return 1
	case code >= 500 && code <= 599:
		return 2
	case code >= 200 && code <= 299:
		return 0
	default:
		return 3
	}
}

// qosBucketTs 选择时间桶基准：request_start_at（请求开始）优先，
// 零值或晚于 created_at 的异常数据兜底 created_at。
func qosBucketTs(r qosScanRow) time.Time {
	if !r.RequestStartAt.IsZero() && !r.RequestStartAt.After(r.CreatedAt) {
		return r.RequestStartAt
	}
	return r.CreatedAt
}

// scanQoSShard 对单张分表按主键 keyset 分页扫描（v2.0.58 scanShardPaged 同构，
// 行结构不同故独立实现）。userName 非空时追加用户过滤（用户端数据隔离）。
// 每批新建 GORM 链（避免 .Where 累积污染，v2.0.58 已知陷阱）。
func scanQoSShard(sdb *gorm.DB, tableName, userName string, cutoff time.Time, fn func(rows []qosScanRow)) error {
	if sdb == nil {
		return nil
	}
	var lastID uint64
	for {
		var rows []qosScanRow
		q := sdb.Table(tableName).
			Select("id, created_at, request_start_at, dst_model_name, dst_endpoint_id, response_status, tokens_output_size, elapsed_ms").
			Where("id > ? AND created_at >= ?", lastID, cutoff)
		if userName != "" {
			q = q.Where("user_name = ?", userName)
		}
		if err := q.Order("id ASC").Limit(qosScanBatch).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			fn(rows)
			lastID = rows[len(rows)-1].ID
		}
		if len(rows) < qosScanBatch {
			return nil
		}
	}
}

// GetQoSMonitorReport 计算服务质量监控报告。
//   - userName == ""：全站维度（管理端）
//   - userName != ""：仅统计该用户的请求（用户端，JWT claims 强制传入）
//
// 结果按 (userName, subTableNum) 缓存 qosCacheTTL（60s）。页面已移除自动刷新，
// 缓存用于保护服务端免受高频 8 分表全量扫描；手动刷新走 GetQoSMonitorReportFresh 旁路。
func GetQoSMonitorReport(userName string, subTableNum int) (*QoSMonitorReport, error) {
	userName = strings.TrimSpace(userName)
	subTableNum = normalizeSubTableNum(subTableNum)

	cacheKey := fmt.Sprintf("QoSMonitorReport:%s:%d", userName, subTableNum)
	if cached, ok := getQoSCache(cacheKey); ok {
		return cached, nil
	}

	report, err := buildQoSMonitorReport(userName, subTableNum)
	if err != nil {
		return nil, err
	}
	setQoSCache(cacheKey, report)
	return report, nil
}

// GetQoSMonitorReportFresh 强制重算并刷新缓存（v2.0.86 手动刷新旁路）。
//
// 页面已移除 30s 自动刷新，用户点「刷新」时必须拿到新快照 —— 走缓存会让
// 按钮看起来"点了没反应"。仍然写回缓存，避免紧接着的普通请求重复全表扫描。
func GetQoSMonitorReportFresh(userName string, subTableNum int) (*QoSMonitorReport, error) {
	userName = strings.TrimSpace(userName)
	subTableNum = normalizeSubTableNum(subTableNum)

	report, err := buildQoSMonitorReport(userName, subTableNum)
	if err != nil {
		return nil, err
	}
	setQoSCache(fmt.Sprintf("QoSMonitorReport:%s:%d", userName, subTableNum), report)
	return report, nil
}

func buildQoSMonitorReport(userName string, subTableNum int) (*QoSMonitorReport, error) {
	now := time.Now()
	cutoff := now.Add(-qosScanWindow)
	// 基准 / 7 天指标固定以 weekStart 为界：扫描窗口 30 天，但「近 7 天」口径不得被摊大。
	weekStart := now.Add(-qosBaselineWindow)

	acc := make(map[string]*qosModelAcc)

	if database.DB != nil {
		sdb, cancel := database.StatsDB()
		defer cancel()
		if sdb != nil {
			// 分钟桶只覆盖最近 60 分钟；更早的行不入分钟桶
			minuteStart := now.Add(-time.Duration(qosMinuteBuckets-1) * time.Minute).Truncate(time.Minute)

			for i := 0; i < subTableNum; i++ {
				tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
				if !IsTableExists(tableName) {
					continue
				}
				err := scanQoSShard(sdb, tableName, userName, cutoff, func(rows []qosScanRow) {
					for _, r := range rows {
						name := strings.TrimSpace(r.DstModelName)
						if name == "" {
							continue // 无源站归属的行不参与（直连失败等）
						}
						m := acc[name]
						if m == nil {
							m = &qosModelAcc{
								name:    name,
								minutes: make(map[string]*qosBucketAcc),
								hours:   make(map[string]*qosBucketAcc),
							}
							acc[name] = m
						}

						cls := qosStatusClass(r.ResponseStatus)
						ts := qosBucketTs(r)
						m.calls30d++
						if cls == 0 {
							m.okCalls30d++
						}
						inWeek := !ts.Before(weekStart)
						if inWeek {
							m.calls7d++
							if cls == 0 {
								m.okCalls7d++
							}
						}
						if ts.After(m.lastRequestAt) {
							m.lastRequestAt = ts
						}

						// 基准：仅近 7 天成功请求（2xx 且 output>0 且 elapsed>0），
						// 同时累计全局与 Token 分档两边。30 天前的样本只进月统计，
						// 不得改变基准 —— 否则同一请求的拥堵系数会随扫描窗口漂移。
						if inWeek && cls == 0 && r.TokensOutput > 0 && r.ElapsedMs > 0 {
							m.baseElapsed += r.ElapsedMs
							m.baseTokens += r.TokensOutput
							m.baseSamples++

							band := qosTokenBandIndex(r.TokensOutput)
							m.bandElapsed[band] += r.ElapsedMs
							m.bandTokens[band] += r.TokensOutput
							m.bandSamples[band]++
						}

						// 小时桶（30 天扫描窗口内全部记录，供 24h/7d/30d 三套视图复用）
						hourKey := statsTimeBucketKey(ts, "hour")
						hb := m.hours[hourKey]
						if hb == nil {
							hb = &qosBucketAcc{}
							m.hours[hourKey] = hb
						}
						hb.add(r, cls)

						// 分钟桶（仅最近 60 分钟）
						if !ts.Before(minuteStart) {
							minKey := statsTimeBucketKey(ts, "minute")
							mb := m.minutes[minKey]
							if mb == nil {
								mb = &qosBucketAcc{}
								m.minutes[minKey] = mb
							}
							mb.add(r, cls)
						}
					}
				})
				if err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						break // 超时：返回已聚合部分（统计看板可接受的近似语义）
					}
					return nil, fmt.Errorf("failed to query %s: %w", tableName, err)
				}
			}
		}
	}

	report := &QoSMonitorReport{
		GeneratedAtMs: now.UnixMilli(),
		ModelCount:    len(acc),
		Models:        make([]QoSModelReport, 0, len(acc)),
	}

	for _, m := range acc {
		base := m.baseline()
		model := QoSModelReport{
			DstModelName:       m.name,
			BaselineMsPerToken: qosRound2(base.global),
			BaselineBands:      base.calibrated,
			BaselineSamples:    m.baseSamples,
			Calls7d:            m.calls7d,
			OkCalls7d:          m.okCalls7d,
			ErrCalls7d:         m.calls7d - m.okCalls7d,
			Calls30d:           m.calls30d,
			OkCalls30d:         m.okCalls30d,
			LastRequestAtMs:    m.lastRequestAt.UnixMilli(),
		}

		model.Minutes = buildQoSMinuteSeries(m, base, now)
		model.Hours = buildQoSHourSeries(m, base, now)
		model.Days = buildQoSDaySeriesN(m, base, now, 7)
		model.Days30d = buildQoSDaySeriesN(m, base, now, 30)
		model.Heatmap, model.HeatmapCounts = buildQoSHeatmap(m, base, now)
		model.WeekP50Ms, model.WeekP95Ms, model.WeekOkP50Ms, model.WeekOkP95Ms = qosWeekPercentiles(m, now)
		model.Live = buildQoSLive(m, base, now)
		model.Live.LastRequestAtMs = m.lastRequestAt.UnixMilli()
		model.Status, model.Alert = qosStatusOf(model.Live)
		model.Stats24h = buildQoSWindowStats(m, base, now, "24h", now.Add(-24*time.Hour))
		model.Stats7d = buildQoSWindowStats(m, base, now, "7d", now.Add(-qosBaselineWindow))
		model.Stats30d = buildQoSWindowStats(m, base, now, "30d", now.Add(-qosScanWindow))
		model.Prediction = buildQoSPrediction(m, base, now)

		// 时段画像 / 时段带 / 峰谷（v2.0.86 → v2.0.87 三窗口）
		//   - 小时画像：窗口内同时段汇总（复用 m.hours，无二次扫描）
		//   - 时段带：由各窗口画像归并；峰谷：各窗口独立识别（20 天前的峰不得冒充近 7 天峰）
		model.HourProfile24h = buildQoSHourProfileRange(m, base, now, now.Add(-24*time.Hour))
		model.HourProfile = buildQoSHourProfileRange(m, base, now, now.Add(-qosBaselineWindow))
		model.HourProfile30d = buildQoSHourProfileRange(m, base, now, now.Add(-qosScanWindow))
		model.Bands24h = buildQoSTimeBands(qosHourAggsFromProfile(model.HourProfile24h))
		model.Bands7d = buildQoSTimeBands(qosHourAggsFromProfile(model.HourProfile))
		model.Bands30d = buildQoSTimeBands(qosHourAggsFromProfile(model.HourProfile30d))
		model.PeakValley24h = buildQoSPeakValley(model.HourProfile24h, model.Bands24h, nil)
		model.PeakValley = buildQoSPeakValley(model.HourProfile, model.Bands7d, model.Prediction.TodayRemaining)
		model.PeakValley30d = buildQoSPeakValley(model.HourProfile30d, model.Bands30d, nil)
		model.Windows = map[string]QoSWindowView{
			"24h": {
				Window: "24h", Stats: model.Stats24h, HourProfile: model.HourProfile24h,
				Bands: model.Bands24h, PeakValley: model.PeakValley24h, Trend: model.Hours,
			},
			"7d": {
				Window: "7d", Stats: model.Stats7d, HourProfile: model.HourProfile,
				Bands: model.Bands7d, PeakValley: model.PeakValley, Trend: model.Days,
			},
			"30d": {
				Window: "30d", Stats: model.Stats30d, HourProfile: model.HourProfile30d,
				Bands: model.Bands30d, PeakValley: model.PeakValley30d, Trend: model.Days30d,
			},
		}

		// 近 24 小时调用量（小时桶尾部求和）
		for i := len(model.Hours) - 24; i < len(model.Hours); i++ {
			if i >= 0 {
				model.Calls24h += model.Hours[i].Count
			}
		}
		report.TotalCalls7d += m.calls7d
		report.TotalCalls30d += m.calls30d
		report.Models = append(report.Models, model)
	}

	// 排序：7 天调用量降序 → 模型名升序（状态卡与表格默认序）
	sort.Slice(report.Models, func(i, j int) bool {
		if report.Models[i].Calls7d != report.Models[j].Calls7d {
			return report.Models[i].Calls7d > report.Models[j].Calls7d
		}
		return report.Models[i].DstModelName < report.Models[j].DstModelName
	})

	// 全站汇总（v2.0.86）：跨模型指标级再聚合（耗时不做混算）
	report.HourProfile, report.Bands, report.BandsToday, report.PeakValley =
		aggregateQoSReport(report.Models, now)
	// 全站三窗口汇总（v2.0.87）：24h / 7d / 30d
	report.Windows = aggregateQoSWindows(report.Models)
	return report, nil
}

// ---- 终态计算 ----

// finalizeQoSBucket 把累积器转成响应点（分档基准参与拥堵系数计算）。
// partial 表示该桶尚未结束（当前分钟/小时/天）。
func finalizeQoSBucket(key string, b *qosBucketAcc, base qosBaseline, partial bool) QoSBucketPoint {
	p := QoSBucketPoint{
		Bucket:       key,
		Partial:      partial,
		Count:        b.count,
		SuccessCount: b.okCount,
		Error429:     b.err429,
		Error5xx:     b.err5xx,
		ErrorOther:   b.errOther,
		TokensOutput: b.tokensOut,
	}
	if b.count == 0 {
		return p
	}

	allElapsed := make([]int64, 0, len(b.pairs))
	okElapsed := make([]int64, 0, len(b.pairs))
	congestion := make([]float64, 0, len(b.pairs))
	var sumAll, sumOk int64
	for _, pair := range b.pairs {
		sumAll += pair.elapsed
		allElapsed = append(allElapsed, pair.elapsed)
		if pair.ok {
			sumOk += pair.elapsed
			okElapsed = append(okElapsed, pair.elapsed)
		}
		// 拥堵系数：仅成功且基准可用、output>0（429/5xx 通常 output=0，天然排除）
		if pair.ok && pair.tokens > 0 {
			expected := base.expectedMs(pair.tokens)
			if expected > 0 {
				coeff := float64(pair.elapsed) / expected
				if coeff > qosCongestionCap {
					coeff = qosCongestionCap
				}
				congestion = append(congestion, coeff)
			}
		}
	}

	p.AvgElapsedMs = sumAll / b.count
	p.P95ElapsedMs = qosPercentile(allElapsed, 0.95)
	if b.okCount > 0 {
		p.OkAvgElapsedMs = sumOk / b.okCount
		p.OkP50Ms = qosPercentile(okElapsed, 0.50)
		p.OkP95Ms = qosPercentile(okElapsed, 0.95)
	}
	if b.okTokens > 0 {
		p.OkMsPerToken = qosRound2(float64(sumOk) / float64(b.okTokens))
	}
	p.CoefficientSamples = int64(len(congestion))
	if len(congestion) > 0 {
		var sum float64
		stall := 0
		for _, c := range congestion {
			sum += c
			if c > qosStallThreshold {
				stall++
			}
		}
		p.AvgCongestion = qosRound2(sum / float64(len(congestion)))
		p.StallRate = qosRound2(float64(stall) / float64(len(congestion)) * 100)
		p.Jitter = qosRound2(qosCV(congestion) * 100)
	}
	errCount := b.err429 + b.err5xx + b.errOther
	if errCount > 0 {
		p.ErrorRate = qosRound2(float64(errCount) / float64(b.count) * 100)
	}
	return p
}

// qosBucketPartial 判断桶是否进行中：当前分钟/小时/天桶为 true。
func qosBucketPartial(key, granularity string, now time.Time) bool {
	switch granularity {
	case "minute":
		return key == now.Format("2006-01-02 15:04")
	case "hour":
		return key == truncateToHour(now).Format("2006-01-02 15:04")
	default:
		return key == now.Format("2006-01-02")
	}
}

// buildQoSMinuteSeries 近 60 分钟连续分钟桶（缺数据补零）。
func buildQoSMinuteSeries(m *qosModelAcc, base qosBaseline, now time.Time) []QoSBucketPoint {
	points := make([]QoSBucketPoint, 0, qosMinuteBuckets)
	start := now.Add(-time.Duration(qosMinuteBuckets-1) * time.Minute).Truncate(time.Minute)
	goFmt := "2006-01-02 15:04"
	for t := start; !t.After(now); t = t.Add(time.Minute) {
		key := t.Format(goFmt)
		if b, ok := m.minutes[key]; ok {
			points = append(points, finalizeQoSBucket(key, b, base, qosBucketPartial(key, "minute", now)))
		} else {
			points = append(points, QoSBucketPoint{Bucket: key, Partial: qosBucketPartial(key, "minute", now)})
		}
	}
	return points
}

// buildQoSHourSeries 近 24 小时连续小时桶（缺数据补零）。
func buildQoSHourSeries(m *qosModelAcc, base qosBaseline, now time.Time) []QoSBucketPoint {
	points := make([]QoSBucketPoint, 0, 24)
	start := now.Add(-23 * time.Hour).Truncate(time.Hour)
	goFmt := "2006-01-02 15:04"
	for t := start; !t.After(now); t = t.Add(time.Hour) {
		key := t.Format(goFmt)
		if b, ok := m.hours[key]; ok {
			points = append(points, finalizeQoSBucket(key, b, base, qosBucketPartial(key, "hour", now)))
		} else {
			points = append(points, QoSBucketPoint{Bucket: key, Partial: qosBucketPartial(key, "hour", now)})
		}
	}
	return points
}

// qosDayKeys 返回近 7 天日期键（升序，末位为今天）与日期→行下标映射。
// 用日期算术（AddDate）而非 Hours()/24 推导，避免跨月/夏令时误差。
func qosDayKeys(now time.Time) ([]string, map[string]int) {
	return qosDayKeysN(now, 7)
}

// qosDayKeysN 返回近 days 天日期键（升序，末位为今天）与日期→行下标映射。
func qosDayKeysN(now time.Time, days int) ([]string, map[string]int) {
	if days <= 0 {
		days = 7
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	keys := make([]string, 0, days)
	index := make(map[string]int, days)
	for i := 0; i < days; i++ {
		key := today.AddDate(0, 0, i-(days-1)).Format("2006-01-02")
		keys = append(keys, key)
		index[key] = i
	}
	return keys, index
}

// mergeQoSBuckets 把多个桶合并为一个累积器（天桶由小时桶归并）。
func mergeQoSBuckets(dst *qosBucketAcc, src *qosBucketAcc) {
	dst.count += src.count
	dst.okCount += src.okCount
	dst.pairs = append(dst.pairs, src.pairs...)
	dst.err429 += src.err429
	dst.err5xx += src.err5xx
	dst.errOther += src.errOther
	dst.tokensOut += src.tokensOut
	dst.okTokens += src.okTokens
}

// buildQoSDaySeriesN 近 days 天连续天桶。天桶由小时桶 pairs 直接合并，
// 保证分位数/均值与逐行重算完全一致，无需二次扫描。
func buildQoSDaySeriesN(m *qosModelAcc, base qosBaseline, now time.Time, days int) []QoSBucketPoint {
	dayBuckets := make(map[string]*qosBucketAcc)
	for key, b := range m.hours {
		t, err := time.ParseInLocation("2006-01-02 15:04", key, now.Location())
		if err != nil {
			continue
		}
		dayKey := t.Format("2006-01-02")
		db := dayBuckets[dayKey]
		if db == nil {
			db = &qosBucketAcc{}
			dayBuckets[dayKey] = db
		}
		mergeQoSBuckets(db, b)
	}

	keys, _ := qosDayKeysN(now, days)
	points := make([]QoSBucketPoint, 0, len(keys))
	for _, key := range keys {
		if b, ok := dayBuckets[key]; ok {
			points = append(points, finalizeQoSBucket(key, b, base, qosBucketPartial(key, "day", now)))
		} else {
			points = append(points, QoSBucketPoint{Bucket: key, Partial: qosBucketPartial(key, "day", now)})
		}
	}
	return points
}

// buildQoSHeatmap 拥堵热力图：[7 天][24 小时]，行 0 = 6 天前，行 6 = 今天；
// 单元格 = 该小时桶平均拥堵系数，无数据为 -1（前端渲染灰色）。
func buildQoSHeatmap(m *qosModelAcc, base qosBaseline, now time.Time) ([][]float64, [][]int64) {
	values := make([][]float64, 7)
	counts := make([][]int64, 7)
	for i := range values {
		values[i] = make([]float64, 24)
		counts[i] = make([]int64, 24)
		for j := range values[i] {
			values[i][j] = -1
		}
	}
	_, dayIndex := qosDayKeys(now)
	for key, b := range m.hours {
		t, err := time.ParseInLocation("2006-01-02 15:04", key, now.Location())
		if err != nil {
			continue
		}
		dayIdx, ok := dayIndex[t.Format("2006-01-02")]
		if !ok {
			continue // 理论上不会出现（扫描窗口 7 天），防御性跳过
		}
		point := finalizeQoSBucket(key, b, base, false)
		values[dayIdx][t.Hour()] = point.AvgCongestion
		counts[dayIdx][t.Hour()] = point.Count
	}
	return values, counts
}

// qosWeekPercentiles 周级 P50/P95（全量口径 + 成功口径）。
// 从小时桶 pairs 汇总，不额外保留全量 elapsed 切片（降低内存占用）。
func qosWeekPercentiles(m *qosModelAcc, now time.Time) (p50, p95, okP50, okP95 int64) {
	all := make([]int64, 0, 1024)
	ok := make([]int64, 0, 1024)
	// 扫描窗口已扩到 30 天，但「周分位」必须只统计近 7 天（v2.0.87）。
	cutKey := now.Add(-qosBaselineWindow).Format("2006-01-02 15:04")
	for key, b := range m.hours {
		if key < cutKey {
			continue
		}
		for _, pair := range b.pairs {
			all = append(all, pair.elapsed)
			if pair.ok {
				ok = append(ok, pair.elapsed)
			}
		}
	}
	p50 = qosPercentile(all, 0.50)
	p95 = qosPercentile(all, 0.95)
	okP50 = qosPercentile(ok, 0.50)
	okP95 = qosPercentile(ok, 0.95)
	return
}

// buildQoSLive 实时窗口指标：按 5 → 15 → 60 分钟阶梯选取第一个样本达标的窗口。
// 全部窗口都不达标时取 60 分钟（有数据即展示，配合前端低样本提示）。
// 分钟键 "2006-01-02 15:04" 字典序 == 时间序，直接字符串比较。
func buildQoSLive(m *qosModelAcc, base qosBaseline, now time.Time) QoSLiveStats {
	for _, minutes := range qosLiveWindowLadder {
		cutKey := now.Add(-time.Duration(minutes) * time.Minute).Truncate(time.Minute).Format("2006-01-02 15:04")
		merged := &qosBucketAcc{}
		for key, b := range m.minutes {
			if key >= cutKey {
				mergeQoSBuckets(merged, b)
			}
		}
		if merged.count == 0 {
			continue
		}
		if merged.count >= qosLiveMinSamples || minutes == qosLiveWindowLadder[len(qosLiveWindowLadder)-1] {
			point := finalizeQoSBucket("live", merged, base, true)
			return QoSLiveStats{
				Coefficient:        point.AvgCongestion,
				AvgElapsedMs:       point.AvgElapsedMs,
				OkAvgElapsedMs:     point.OkAvgElapsedMs,
				ErrorRate:          point.ErrorRate,
				ReqCount:           point.Count,
				SuccessCount:       point.SuccessCount,
				WindowMinutes:      minutes,
				CoefficientSamples: point.CoefficientSamples,
				StallRate:          point.StallRate,
				Jitter:             point.Jitter,
			}
		}
	}
	return QoSLiveStats{}
}

// qosStatusOf 状态卡分档 + 告警判定。
//   - 无流量 → idle；有流量但无可算拥堵样本（只有错误请求等）→ 按错误率兜底
//   - 告警：拥堵系数 > 2.0 或错误率 > 5%（方案 §三告警阈值）
func qosStatusOf(live QoSLiveStats) (status string, alert bool) {
	alert = live.Coefficient > qosStallThreshold || live.ErrorRate > qosAlertErrorRate
	if live.ReqCount == 0 {
		return "idle", false
	}
	switch {
	case live.Coefficient > qosTierModerate:
		return "severe", alert // > 5.0 严重排队
	case live.Coefficient >= qosTierLight:
		return "moderate", alert // 2.0 ~ 5.0 中度排队
	case live.Coefficient >= qosTierNormal:
		return "light", alert // 1.2 ~ 2.0 轻度排队
	default:
		// 系数正常或无样本：错误率高（>5%）时直接标红（服务故障主导）
		if live.ErrorRate > qosAlertErrorRate {
			return "severe", true
		}
		return "ok", alert
	}
}

// ---- 固定窗口统计与运行经验预测（阶段CZ v2.0.85） ----

// buildQoSWindowStats 合并 since 之后的小时桶，经 finalizeQoSBucket 一次算出
// 固定窗口（24h/7d）统计 —— 与小时桶/天桶完全同一套口径，无二次扫描。
func buildQoSWindowStats(m *qosModelAcc, base qosBaseline, now time.Time, window string, since time.Time) QoSWindowStats {
	cutKey := since.Format("2006-01-02 15:04")
	merged := &qosBucketAcc{}
	for key, b := range m.hours {
		if key >= cutKey {
			mergeQoSBuckets(merged, b)
		}
	}
	s := QoSWindowStats{Window: window}
	if merged.count == 0 {
		return s
	}
	p := finalizeQoSBucket("stats", merged, base, true)
	s.Calls = p.Count
	if p.Count > 0 {
		s.SuccessRate = qosRound2(float64(p.SuccessCount) / float64(p.Count) * 100)
	}
	s.AvgCongestion = p.AvgCongestion
	s.CoefficientSamples = p.CoefficientSamples
	s.StallRate = p.StallRate
	s.Jitter = p.Jitter
	s.ErrorRate = p.ErrorRate
	s.OkAvgElapsedMs = p.OkAvgElapsedMs
	s.OkP95Ms = p.OkP95Ms
	s.OkMsPerToken = p.OkMsPerToken
	// 窗口口径状态分档（v2.0.86）：状态卡改用窗口状态，避免实时窗口抖动
	s.Status, s.Alert = qosStatusOfWindow(s.AvgCongestion, s.ErrorRate, s.Calls)
	return s
}

// qosHourPattern 「同时段历史」加权累积：对小时 H，pred = Σ(avg_i × n_i) / Σn_i。
type qosHourPattern struct {
	congW float64 // Σ(avg_congestion × coefficient_samples)
	congN int64
	errW  float64 // Σ(error_rate × count)
	errN  int64
}

// qosForecastConfidence 预测可信度三档：样本 ≥30 高、≥10 中、否则低。
func qosForecastConfidence(samples int64) string {
	switch {
	case samples >= qosForecastHighSamples:
		return "high"
	case samples >= qosForecastMediumSamples:
		return "medium"
	default:
		return "low"
	}
}

// qosWeightedCongestionRange 汇总 [start, end) 内小时桶的加权平均拥堵分子/分母。
// 小时键字典序 == 时间序，直接字符串裁剪。
func qosWeightedCongestionRange(hours map[string]*qosBucketAcc, base qosBaseline, start, end time.Time) (sumW float64, sumN int64) {
	startKey := start.Format("2006-01-02 15:04")
	endKey := end.Format("2006-01-02 15:04")
	for key, b := range hours {
		if key < startKey || key >= endKey {
			continue
		}
		pt := finalizeQoSBucket(key, b, base, false)
		sumW += pt.AvgCongestion * float64(pt.CoefficientSamples)
		sumN += pt.CoefficientSamples
	}
	return
}

// buildQoSPrediction 运行经验预测（方案 §3.2）：复用同一份 m.hours 桶，无二次扫描。
func buildQoSPrediction(m *qosModelAcc, base qosBaseline, now time.Time) QoSPrediction {
	pred := QoSPrediction{
		Trend24h:       "unknown",
		TodayRemaining: []QoSHourForecast{},
	}

	// 第一遍：逐小时模式 + 按天聚合（供明日预测与今日 vs 周均值）
	// v2.0.87：扫描窗口已扩到 30 天，运行经验预测必须仍只取近 7 天同时段，
	// 否则「经验」会被 20 天前的旧模式稀释，可信度样本也会虚高。
	forecastCutKey := now.Add(-qosForecastWindow).Format("2006-01-02 15:04")
	hourly := make(map[int]*qosHourPattern, 24)
	dayAcc := make(map[string]*qosBucketAcc, 7)
	for key, b := range m.hours {
		if key < forecastCutKey {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02 15:04", key, now.Location())
		if err != nil {
			continue
		}
		pt := finalizeQoSBucket(key, b, base, false)
		hp := hourly[t.Hour()]
		if hp == nil {
			hp = &qosHourPattern{}
			hourly[t.Hour()] = hp
		}
		hp.congW += pt.AvgCongestion * float64(pt.CoefficientSamples)
		hp.congN += pt.CoefficientSamples
		hp.errW += pt.ErrorRate * float64(pt.Count)
		hp.errN += pt.Count

		dayKey := t.Format("2006-01-02")
		db := dayAcc[dayKey]
		if db == nil {
			db = &qosBucketAcc{}
			dayAcc[dayKey] = db
		}
		mergeQoSBuckets(db, b)
	}

	hourForecast := func(h int) QoSHourForecast {
		f := QoSHourForecast{Hour: h, Confidence: "low"}
		hp := hourly[h]
		if hp == nil || hp.congN == 0 {
			return f
		}
		f.AvgCongestion = qosRound2(hp.congW / float64(hp.congN))
		if hp.errN > 0 {
			f.ErrorRate = qosRound2(hp.errW / float64(hp.errN))
		}
		f.Samples = hp.congN
		f.Confidence = qosForecastConfidence(hp.congN)
		return f
	}

	// 下一时段（当前小时 +1；跨天回到 0 时仍按「同时段」模式预测）
	pred.NextHour = hourForecast((now.Hour() + 1) % 24)
	// 今日剩余时段逐小时预测
	for h := now.Hour() + 1; h < 24; h++ {
		pred.TodayRemaining = append(pred.TodayRemaining, hourForecast(h))
	}

	// 第二遍（按天）：明日预测（星期模式，同星期权重 ×2）+ 今日 vs 周均值
	todayKey := now.Format("2006-01-02")
	tomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	pred.Tomorrow.Date = tomorrow.Format("2006-01-02")
	pred.Tomorrow.Weekday = int(tomorrow.Weekday())
	pred.Tomorrow.Confidence = "low" // 无完整天数据时保持 low（可信度兜底）
	var congW, errW, callsW float64
	var congN, errN, callsN, sampleN int64
	var fullW float64 // 近 6 个完整天加权拥堵分子
	var fullN int64
	var todayAvg float64
	var todayN int64
	for dayKey, db := range dayAcc {
		dp := finalizeQoSBucket(dayKey, db, base, dayKey == todayKey)
		if dayKey == todayKey {
			todayAvg = dp.AvgCongestion
			todayN = dp.CoefficientSamples
			continue
		}
		w := int64(1)
		if t, err := time.ParseInLocation("2006-01-02", dayKey, now.Location()); err == nil && int(t.Weekday()) == pred.Tomorrow.Weekday {
			w = 2 // 星期模式：与明天同星期的历史天权重 ×2
		}
		congW += dp.AvgCongestion * float64(dp.CoefficientSamples) * float64(w)
		congN += dp.CoefficientSamples * w
		errW += dp.ErrorRate * float64(dp.Count) * float64(w)
		errN += dp.Count * w
		callsW += float64(dp.Count) * float64(w)
		callsN += w
		sampleN += dp.CoefficientSamples
		fullW += dp.AvgCongestion * float64(dp.CoefficientSamples)
		fullN += dp.CoefficientSamples
	}
	if congN > 0 {
		pred.Tomorrow.AvgCongestion = qosRound2(congW / float64(congN))
		pred.Tomorrow.Samples = sampleN
		pred.Tomorrow.Confidence = qosForecastConfidence(sampleN)
	}
	if errN > 0 {
		pred.Tomorrow.ErrorRate = qosRound2(errW / float64(errN))
	}
	if callsN > 0 {
		pred.Tomorrow.Calls = int64(math.Round(callsW / float64(callsN)))
	}
	if todayN > 0 && fullN > 0 {
		pred.TodayVsWeek = qosRound2((todayAvg/(fullW/float64(fullN)) - 1) * 100)
	}

	// 趋势研判：近 24h vs 前 24h 加权平均拥堵系数（比值 >1.1 升、<0.9 降）
	lastW, lastN := qosWeightedCongestionRange(m.hours, base, now.Add(-24*time.Hour), now)
	prevW, prevN := qosWeightedCongestionRange(m.hours, base, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if lastN > 0 && prevN > 0 && prevW > 0 {
		ratio := (lastW / float64(lastN)) / (prevW / float64(prevN))
		switch {
		case ratio > 1.1:
			pred.Trend24h = "up"
		case ratio < 0.9:
			pred.Trend24h = "down"
		default:
			pred.Trend24h = "flat"
		}
	}
	return pred
}

// ---- 时段画像 / 时段带 / 峰谷（v2.0.86 阶段DA） ----

// qosHourAgg 小时级聚合中间结构：时段带归并与全站汇总的统一输入。
type qosHourAgg struct {
	hour    int
	calls   int64
	days    int64
	cong    float64
	samples int64
	errRate float64
	okAvgMs int64
	okCount int64
	p95Ms   int64
}

// buildQoSHourProfileRange 窗口内「同时段」小时画像（24 点，v2.0.87 泛化）。
//
// 复用 m.hours（扫描窗口 ≤30 天 × 24 = ≤720 个小时桶，**无二次扫描**）：按小时归并
// [since, now] 内的桶 → mergeQoSBuckets → 一次 finalizeQoSBucket，与小时桶/天桶/
// 窗口统计完全同一套口径（分档基准、成功口径、低样本语义一致）。
// 24h / 7d / 30d 三套画像只是 since 不同，算法完全一致。
//
// Days 统计「该小时有数据的天数」：日均调用用 Days 归一而非固定天数，
// 避免"某模型只在一天有流量"时被摊薄成假低谷。
func buildQoSHourProfileRange(m *qosModelAcc, base qosBaseline, now, since time.Time) []QoSHourProfile {
	accs := make([]*qosBucketAcc, 24)
	daySets := make([]map[string]struct{}, 24)
	for i := 0; i < 24; i++ {
		accs[i] = &qosBucketAcc{}
		daySets[i] = make(map[string]struct{})
	}
	cutKey := since.Format("2006-01-02 15:04")
	for key, b := range m.hours {
		if key < cutKey {
			continue // 窗口外（30 天扫描时，7d/24h 画像必须只取窗口内）
		}
		t, err := time.ParseInLocation("2006-01-02 15:04", key, now.Location())
		if err != nil {
			continue
		}
		h := t.Hour()
		mergeQoSBuckets(accs[h], b)
		daySets[h][t.Format("2006-01-02")] = struct{}{}
	}

	profiles := make([]QoSHourProfile, 0, 24)
	for h := 0; h < 24; h++ {
		p := QoSHourProfile{Hour: h, Days: int64(len(daySets[h]))}
		if accs[h].count > 0 {
			pt := finalizeQoSBucket("profile", accs[h], base, false)
			p.Calls = pt.Count
			p.SuccessCount = pt.SuccessCount
			if p.Days > 0 {
				p.AvgCallsPerDay = qosRound2(float64(pt.Count) / float64(p.Days))
			}
			p.AvgCongestion = pt.AvgCongestion
			p.ErrorRate = pt.ErrorRate
			p.StallRate = pt.StallRate
			p.OkAvgElapsedMs = pt.OkAvgElapsedMs
			p.OkP95Ms = pt.OkP95Ms
			p.CoefficientSamples = pt.CoefficientSamples
			p.Confidence = qosForecastConfidence(pt.CoefficientSamples)
		}
		profiles = append(profiles, p)
	}
	return profiles
}

// qosHourAggsFromProfile 小时画像 → 小时聚合中间结构。
func qosHourAggsFromProfile(profiles []QoSHourProfile) []qosHourAgg {
	out := make([]qosHourAgg, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, qosHourAgg{
			hour:    p.Hour,
			calls:   p.Calls,
			days:    p.Days,
			cong:    p.AvgCongestion,
			samples: p.CoefficientSamples,
			errRate: p.ErrorRate,
			okAvgMs: p.OkAvgElapsedMs,
			okCount: p.SuccessCount,
			p95Ms:   p.OkP95Ms,
		})
	}
	return out
}

// qosHourAggsFromPoints 24 小时桶序列 → 小时聚合中间结构（今日口径，days=1）。
func qosHourAggsFromPoints(points []QoSBucketPoint) []qosHourAgg {
	out := make([]qosHourAgg, 0, len(points))
	for _, p := range points {
		agg := qosHourAgg{
			hour:    qosHourFromBucketKey(p.Bucket),
			calls:   p.Count,
			days:    1,
			cong:    p.AvgCongestion,
			samples: p.CoefficientSamples,
			errRate: p.ErrorRate,
			okAvgMs: p.OkAvgElapsedMs,
			okCount: p.SuccessCount,
			p95Ms:   p.OkP95Ms,
		}
		if agg.hour < 0 {
			continue
		}
		out = append(out, agg)
	}
	return out
}

// qosHourFromBucketKey 从小时桶键 "2006-01-02 15:04" 取小时（0~23）；失败返回 -1。
func qosHourFromBucketKey(key string) int {
	parts := strings.SplitN(key, " ", 2)
	if len(parts) != 2 || len(parts[1]) < 2 {
		return -1
	}
	h, err := strconv.Atoi(parts[1][:2])
	if err != nil || h < 0 || h > 23 {
		return -1
	}
	return h
}

// buildQoSTimeBands 由小时聚合归并业务时段带统计。
//
// 权重口径：
//   - 拥堵系数：样本加权（Σ(系数×样本)/Σ样本）—— 小样本时段不主导结论；
//   - 错误率：调用加权（Σ(错误率×调用)/Σ调用）；
//   - 成功平均时延：成功数加权；
//   - P95：取带内最差小时（保守口径，代表"该时段带最差体验"）。
//
// Share = 该带调用 / 全部小时调用 × 100。
func buildQoSTimeBands(aggs []qosHourAgg) []QoSTimeBand {
	bands := make([]QoSTimeBand, 0, len(qosTimeBands))
	var totalCalls int64
	for _, a := range aggs {
		totalCalls += a.calls
	}
	for _, def := range qosTimeBands {
		b := QoSTimeBand{Key: def.Key, StartHour: def.StartHour, EndHour: def.EndHour}
		var congW, errW, okW float64
		var congN, errN, okN int64
		for _, a := range aggs {
			if a.hour < def.StartHour || a.hour >= def.EndHour {
				continue
			}
			b.Calls += a.calls
			if a.days > b.Days {
				b.Days = a.days
			}
			congW += a.cong * float64(a.samples)
			congN += a.samples
			errW += a.errRate * float64(a.calls)
			errN += a.calls
			okW += float64(a.okAvgMs) * float64(a.okCount)
			okN += a.okCount
			if a.p95Ms > b.OkP95Ms {
				b.OkP95Ms = a.p95Ms
			}
		}
		if congN > 0 {
			b.AvgCongestion = qosRound2(congW / float64(congN))
		}
		if errN > 0 {
			b.ErrorRate = qosRound2(errW / float64(errN))
		}
		if okN > 0 {
			b.OkAvgElapsedMs = int64(okW / float64(okN))
		}
		b.CoefficientSamples = congN
		b.Confidence = qosForecastConfidence(congN)
		if b.Days > 0 {
			b.AvgCallsPerDay = qosRound2(float64(b.Calls) / float64(b.Days))
			// 每小时强度：跨时段带可比的归一化口径（消除"带越长数越大"的误导）
			span := b.EndHour - b.StartHour
			if span > 0 {
				b.AvgCallsPerHour = qosRound2(float64(b.Calls) / float64(b.Days) / float64(span))
			}
		}
		if totalCalls > 0 {
			b.Share = qosRound2(float64(b.Calls) / float64(totalCalls) * 100)
		}
		bands = append(bands, b)
	}
	return bands
}

// qosBandOfHour 返回小时归属的时段带 key；无归属返回 ""。
func qosBandOfHour(hour int) string {
	for _, def := range qosTimeBands {
		if hour >= def.StartHour && hour < def.EndHour {
			return def.Key
		}
	}
	return ""
}

// buildQoSPeakValley 高峰 / 低谷识别。
//
// 峰/谷口径 = 日均调用量（AvgCallsPerDay）的极值；谷值只在有数据的小时中取
// （Calls>0），否则"凌晨完全没流量"会被误判为谷底。
// ForecastPeakHour = 今日剩余时段中预测拥堵系数最高的小时。
// TomorrowPeakBandKey = 7 天口径中拥堵最高且样本达标的时段带。
func buildQoSPeakValley(profiles []QoSHourProfile, bands []QoSTimeBand, todayRemaining []QoSHourForecast) QoSPeakValley {
	pv := QoSPeakValley{PeakHour: -1, ValleyHour: -1, ForecastPeakHour: -1}

	for _, p := range profiles {
		if p.Calls <= 0 {
			continue
		}
		if pv.PeakHour < 0 || p.AvgCallsPerDay > pv.PeakCallsPerDay {
			pv.PeakHour = p.Hour
			pv.PeakCallsPerDay = p.AvgCallsPerDay
			pv.PeakCongestion = p.AvgCongestion
		}
		if pv.ValleyHour < 0 || p.AvgCallsPerDay < pv.ValleyCallsPerDay {
			pv.ValleyHour = p.Hour
			pv.ValleyCallsPerDay = p.AvgCallsPerDay
			pv.ValleyCongestion = p.AvgCongestion
		}
	}
	if pv.PeakHour >= 0 {
		pv.PeakBandKey = qosBandOfHour(pv.PeakHour)
	}
	if pv.ValleyHour >= 0 {
		pv.ValleyBandKey = qosBandOfHour(pv.ValleyHour)
	}

	// 今日剩余时段预测峰（样本达标者才参与）
	for _, f := range todayRemaining {
		if f.Samples <= 0 {
			continue
		}
		if pv.ForecastPeakHour < 0 || f.AvgCongestion > pv.ForecastPeakCongestion {
			pv.ForecastPeakHour = f.Hour
			pv.ForecastPeakCongestion = f.AvgCongestion
			pv.ForecastPeakBandKey = qosBandOfHour(f.Hour)
		}
	}

	// 明日高峰时段带：拥堵最高且样本达标的带
	for _, b := range bands {
		if b.CoefficientSamples < qosForecastMediumSamples {
			continue
		}
		if pv.TomorrowPeakBandKey == "" || b.AvgCongestion > pv.TomorrowPeakCongestion {
			pv.TomorrowPeakBandKey = b.Key
			pv.TomorrowPeakCongestion = b.AvgCongestion
		}
	}
	if pv.TomorrowPeakBandKey == "" && len(bands) > 0 {
		// 无达标样本时退化为调用占比最高的时段带
		best := bands[0]
		for _, b := range bands[1:] {
			if b.Share > best.Share {
				best = b
			}
		}
		pv.TomorrowPeakBandKey = best.Key
		pv.TomorrowPeakCongestion = best.AvgCongestion
	}

	// TopBands：按调用占比降序
	top := make([]QoSTimeBand, len(bands))
	copy(top, bands)
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].Share != top[j].Share {
			return top[i].Share > top[j].Share
		}
		return top[i].StartHour < top[j].StartHour
	})
	pv.TopBands = top
	return pv
}

// aggregateQoSReport 全站汇总（v2.0.86）。
//
// 跨模型聚合口径（各源站基准不同，耗时不可混算）：
//   - 调用量 / 成功数 / 样本数：求和；
//   - 拥堵系数：Σ(模型系数 × 模型样本) / Σ 模型样本；
//   - 错误率：Σ(模型错误率 × 模型调用) / Σ 模型调用；
//   - 成功平均时延：成功数加权；
//   - P95：取各模型最差者（全站最差体验，保守口径）。
func aggregateQoSReport(models []QoSModelReport, now time.Time) ([]QoSHourProfile, []QoSTimeBand, []QoSTimeBand, QoSPeakValley) {
	// 7 天口径：按小时汇总
	profileAgg := make([]qosHourAgg, 24)
	for h := 0; h < 24; h++ {
		profileAgg[h].hour = h
	}
	// 今日口径：按小时汇总（各模型的 24 小时桶）
	todayAgg := make([]qosHourAgg, 24)
	for h := 0; h < 24; h++ {
		todayAgg[h].hour = h
	}

	for i := range models {
		m := &models[i]
		for _, p := range m.HourProfile {
			if p.Hour < 0 || p.Hour > 23 {
				continue
			}
			a := &profileAgg[p.Hour]
			a.calls += p.Calls
			a.cong += p.AvgCongestion * float64(p.CoefficientSamples)
			a.samples += p.CoefficientSamples
			a.errRate += p.ErrorRate * float64(p.Calls)
			a.okAvgMs += int64(float64(p.OkAvgElapsedMs) * float64(p.SuccessCount))
			a.okCount += p.SuccessCount
			if p.Days > a.days {
				a.days = p.Days
			}
			if p.OkP95Ms > a.p95Ms {
				a.p95Ms = p.OkP95Ms
			}
		}
		for _, p := range m.Hours {
			h := qosHourFromBucketKey(p.Bucket)
			if h < 0 {
				continue
			}
			a := &todayAgg[h]
			a.calls += p.Count
			a.cong += p.AvgCongestion * float64(p.CoefficientSamples)
			a.samples += p.CoefficientSamples
			a.errRate += p.ErrorRate * float64(p.Count)
			a.okAvgMs += int64(float64(p.OkAvgElapsedMs) * float64(p.SuccessCount))
			a.okCount += p.SuccessCount
			if p.OkP95Ms > a.p95Ms {
				a.p95Ms = p.OkP95Ms
			}
			a.days = 1
		}
	}

	// 归一化：系数 / 错误率 / 时延的加权和 → 均值
	normalize := func(aggs []qosHourAgg) {
		for i := range aggs {
			a := &aggs[i]
			if a.samples > 0 {
				a.cong = a.cong / float64(a.samples)
			} else {
				a.cong = 0
			}
			if a.calls > 0 {
				a.errRate = a.errRate / float64(a.calls)
			} else {
				a.errRate = 0
			}
			if a.okCount > 0 {
				a.okAvgMs = a.okAvgMs / a.okCount
			} else {
				a.okAvgMs = 0
			}
		}
	}
	normalize(profileAgg)
	normalize(todayAgg)

	profiles := make([]QoSHourProfile, 0, 24)
	for h := 0; h < 24; h++ {
		a := profileAgg[h]
		p := QoSHourProfile{
			Hour:               h,
			Calls:              a.calls,
			SuccessCount:       a.okCount,
			Days:               a.days,
			AvgCongestion:      qosRound2(a.cong),
			ErrorRate:          qosRound2(a.errRate),
			OkAvgElapsedMs:     a.okAvgMs,
			OkP95Ms:            a.p95Ms,
			CoefficientSamples: a.samples,
			Confidence:         qosForecastConfidence(a.samples),
		}
		if a.days > 0 {
			p.AvgCallsPerDay = qosRound2(float64(a.calls) / float64(a.days))
		}
		profiles = append(profiles, p)
	}

	bands7d := buildQoSTimeBands(profileAgg)
	bandsToday := buildQoSTimeBands(todayAgg)

	// 全站峰谷：以全站小时画像的日均调用量取极值
	pv := buildQoSPeakValley(profiles, bands7d, nil)
	return profiles, bands7d, bandsToday, pv
}

// aggregateQoSWindows 全站三窗口汇总（v2.0.87）。
//
// 以各模型 windows[win] 为输入做指标级再聚合（跨源站耗时不可混算）：
//   - 调用量 / 成功数 / 错误数：求和；
//   - 拥堵系数：Σ(模型系数 × 模型样本) / Σ 模型样本；
//   - 错误率：Σ(模型错误率 × 模型调用) / Σ 模型调用；
//   - 成功平均时延：成功数加权；P95 取各模型最大（最差体验，保守口径）；
//   - 小时画像 / 时段带 / 峰谷：由聚合后画像重算；
//   - 趋势：按桶做同样的指标级加权合并。
func aggregateQoSWindows(models []QoSModelReport) map[string]QoSWindowView {
	out := make(map[string]QoSWindowView, 3)
	for _, win := range []string{"24h", "7d", "30d"} {
		profileAgg := make([]qosHourAgg, 24)
		for h := 0; h < 24; h++ {
			profileAgg[h].hour = h
		}

		var calls, samples int64
		var congW, errW, okW float64
		var okN int64
		var p95 int64

		type pointAgg struct {
			count, success           int64
			err429, err5xx, errOther int64
			tokens                   uint64
			congW                    float64
			congN                    int64
			errW                     float64
			errN                     int64
			okW                      float64
			okN                      int64
			p95                      int64
			partial                  bool
		}
		trend := make(map[string]*pointAgg)

		for i := range models {
			v, ok := models[i].Windows[win]
			if !ok {
				continue
			}
			for _, p := range v.HourProfile {
				if p.Hour < 0 || p.Hour > 23 {
					continue
				}
				a := &profileAgg[p.Hour]
				a.calls += p.Calls
				a.cong += p.AvgCongestion * float64(p.CoefficientSamples)
				a.samples += p.CoefficientSamples
				a.errRate += p.ErrorRate * float64(p.Calls)
				a.okAvgMs += int64(float64(p.OkAvgElapsedMs) * float64(p.SuccessCount))
				a.okCount += p.SuccessCount
				if p.Days > a.days {
					a.days = p.Days
				}
				if p.OkP95Ms > a.p95Ms {
					a.p95Ms = p.OkP95Ms
				}
			}

			s := v.Stats
			calls += s.Calls
			samples += s.CoefficientSamples
			congW += s.AvgCongestion * float64(s.CoefficientSamples)
			errW += s.ErrorRate * float64(s.Calls)
			okCalls := int64(math.Round(s.SuccessRate / 100 * float64(s.Calls)))
			okW += float64(s.OkAvgElapsedMs) * float64(okCalls)
			okN += okCalls
			if s.OkP95Ms > p95 {
				p95 = s.OkP95Ms
			}

			for _, tp := range v.Trend {
				pa := trend[tp.Bucket]
				if pa == nil {
					pa = &pointAgg{}
					trend[tp.Bucket] = pa
				}
				pa.count += tp.Count
				pa.success += tp.SuccessCount
				pa.err429 += tp.Error429
				pa.err5xx += tp.Error5xx
				pa.errOther += tp.ErrorOther
				pa.tokens += tp.TokensOutput
				pa.congW += tp.AvgCongestion * float64(tp.CoefficientSamples)
				pa.congN += tp.CoefficientSamples
				pa.errW += tp.ErrorRate * float64(tp.Count)
				pa.errN += tp.Count
				pa.okW += float64(tp.OkAvgElapsedMs) * float64(tp.SuccessCount)
				pa.okN += tp.SuccessCount
				if tp.OkP95Ms > pa.p95 {
					pa.p95 = tp.OkP95Ms
				}
				pa.partial = pa.partial || tp.Partial
			}
		}

		// 归一化小时聚合
		for i := range profileAgg {
			a := &profileAgg[i]
			if a.samples > 0 {
				a.cong /= float64(a.samples)
			} else {
				a.cong = 0
			}
			if a.calls > 0 {
				a.errRate /= float64(a.calls)
			} else {
				a.errRate = 0
			}
			if a.okCount > 0 {
				a.okAvgMs /= a.okCount
			} else {
				a.okAvgMs = 0
			}
		}

		profiles := make([]QoSHourProfile, 0, 24)
		for h := 0; h < 24; h++ {
			a := profileAgg[h]
			p := QoSHourProfile{
				Hour:               h,
				Calls:              a.calls,
				SuccessCount:       a.okCount,
				Days:               a.days,
				AvgCongestion:      qosRound2(a.cong),
				ErrorRate:          qosRound2(a.errRate),
				OkAvgElapsedMs:     a.okAvgMs,
				OkP95Ms:            a.p95Ms,
				CoefficientSamples: a.samples,
				Confidence:         qosForecastConfidence(a.samples),
			}
			if a.days > 0 {
				p.AvgCallsPerDay = qosRound2(float64(a.calls) / float64(a.days))
			}
			profiles = append(profiles, p)
		}

		bands := buildQoSTimeBands(profileAgg)
		pv := buildQoSPeakValley(profiles, bands, nil)

		stats := QoSWindowStats{Window: win, Calls: calls, CoefficientSamples: samples, OkP95Ms: p95}
		if calls > 0 {
			stats.ErrorRate = qosRound2(errW / float64(calls))
			errCount := int64(math.Round(errW / 100))
			stats.SuccessRate = qosRound2(float64(calls-errCount) / float64(calls) * 100)
		}
		if samples > 0 {
			stats.AvgCongestion = qosRound2(congW / float64(samples))
		}
		if okN > 0 {
			stats.OkAvgElapsedMs = int64(okW / float64(okN))
		}
		stats.Status, stats.Alert = qosStatusOfWindow(stats.AvgCongestion, stats.ErrorRate, stats.Calls)

		// 趋势桶排序（键字典序 == 时间序）
		keys := make([]string, 0, len(trend))
		for k := range trend {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		points := make([]QoSBucketPoint, 0, len(keys))
		for _, k := range keys {
			pa := trend[k]
			p := QoSBucketPoint{
				Bucket: k, Partial: pa.partial,
				Count: pa.count, SuccessCount: pa.success,
				Error429: pa.err429, Error5xx: pa.err5xx, ErrorOther: pa.errOther,
				TokensOutput: pa.tokens, OkP95Ms: pa.p95,
			}
			if pa.count > 0 {
				p.ErrorRate = qosRound2(pa.errW / float64(pa.count))
			}
			if pa.congN > 0 {
				p.AvgCongestion = qosRound2(pa.congW / float64(pa.congN))
				p.CoefficientSamples = pa.congN
			}
			if pa.okN > 0 {
				p.OkAvgElapsedMs = int64(pa.okW / float64(pa.okN))
			}
			points = append(points, p)
		}

		out[win] = QoSWindowView{
			Window: win, Stats: stats, HourProfile: profiles,
			Bands: bands, PeakValley: pv, Trend: points,
		}
	}
	return out
}

// qosStatusOfWindow 按窗口统计口径给出状态分档与告警（阈值与实时窗口一致）。
// 无调用 → idle；系数 >5 严重 / ≥2 中度 / ≥1.2 轻度；系数正常但错误率 >5% → 严重（故障主导）。
func qosStatusOfWindow(cong, errRate float64, calls int64) (string, bool) {
	if calls == 0 {
		return "idle", false
	}
	alert := cong > qosStallThreshold || errRate > qosAlertErrorRate
	switch {
	case cong > qosTierModerate:
		return "severe", alert
	case cong >= qosTierLight:
		return "moderate", alert
	case cong >= qosTierNormal:
		return "light", alert
	default:
		if errRate > qosAlertErrorRate {
			return "severe", true
		}
		return "ok", alert
	}
}

// ---- 数值工具 ----

// qosPercentile 升序分位值（q ∈ (0,1]），n=0 返回 0。索引 = ceil(q×n)-1。
func qosPercentile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	vals := make([]int64, len(sorted))
	copy(vals, sorted)
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	idx := int(math.Ceil(q*float64(len(vals)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

// qosCV 变异系数（标准差/均值）；n<2 或均值 0 时返回 0。
// v2.0.84 起用于「拥堵系数变异系数」= 网络/排队抖动率。
func qosCV(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)
	if mean == 0 {
		return 0
	}
	var sq float64
	for _, v := range values {
		d := v - mean
		sq += d * d
	}
	stddev := math.Sqrt(sq / float64(n))
	return stddev / mean
}

// qosRound2 保留两位小数。
func qosRound2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ---- 报告缓存（30s，独立于 statsCache 的 5 分钟 TTL）----

type qosCacheEntry struct {
	report *QoSMonitorReport
	at     time.Time
}

var (
	qosCacheMu sync.RWMutex
	qosCache   = make(map[string]qosCacheEntry)
)

func getQoSCache(key string) (*QoSMonitorReport, bool) {
	qosCacheMu.RLock()
	entry, ok := qosCache[key]
	qosCacheMu.RUnlock()
	if !ok || time.Since(entry.at) > qosCacheTTL {
		return nil, false
	}
	return entry.report, true
}

func setQoSCache(key string, report *QoSMonitorReport) {
	qosCacheMu.Lock()
	qosCache[key] = qosCacheEntry{report: report, at: time.Now()}
	qosCacheMu.Unlock()
}
