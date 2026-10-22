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
	"strings"
	"sync"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	"gorm.io/gorm"
)

// QoS 聚合常量
const (
	// qosScanWindow 聚合窗口：最近 7 天（基准 + 周视图 + 热力图共用一次扫描）。
	qosScanWindow = 7 * 24 * time.Hour
	// qosScanBatch 单批扫描行数（keyset 分页）。
	qosScanBatch = 5000
	// qosMinuteBuckets 分钟桶数量（近 1 小时折线视图）。
	qosMinuteBuckets = 60
	// qosCongestionCap 单请求拥堵系数上限截断（防脏数据拉爆均值）。
	qosCongestionCap = 100.0
	// qosCacheTTL 报告缓存时长（与前端 30s 刷新对齐，最坏陈旧度 60s）。
	qosCacheTTL = 30 * time.Second
	// qosTokenBandCount Token 分档数量 = len(qosTokenBandEdges)+1。
	qosTokenBandCount = 11
	// qosBandMinSamples 单档最小校准样本，不足则回退全局基准。
	qosBandMinSamples = 30
	// qosLiveMinSamples 实时窗口阶梯选择的最小样本数。
	qosLiveMinSamples = 5
	// qosMinCoefficientSamples 系数类指标的低样本提示阈值（前端同步展示）。
	qosMinCoefficientSamples = 5
)

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

// QoSModelReport 单个目标源站模型的完整 QoS 报告。
type QoSModelReport struct {
	DstModelName       string           `json:"dst_model_name"`
	BaselineMsPerToken float64          `json:"baseline_ms_per_token"` // 全局 Σelapsed/ΣTokens（展示口径）
	BaselineBands      int              `json:"baseline_bands"`        // 已校准的 Token 分档数
	BaselineSamples    int64            `json:"baseline_samples"`
	Status             string           `json:"status"` // ok|light|moderate|severe|idle
	Alert              bool             `json:"alert"`
	Live               QoSLiveStats     `json:"live"`
	Minutes            []QoSBucketPoint `json:"minutes"` // 近 60 分钟（升序）
	Hours              []QoSBucketPoint `json:"hours"`   // 近 24 小时（升序）
	Days               []QoSBucketPoint `json:"days"`    // 近 7 天（升序）
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
	LastRequestAtMs    int64            `json:"last_request_at_ms"`
}

// QoSMonitorReport 服务质量监控完整响应。
type QoSMonitorReport struct {
	GeneratedAtMs int64            `json:"generated_at_ms"`
	ModelCount    int              `json:"model_count"`
	TotalCalls7d  int64            `json:"total_calls_7d"`
	Models        []QoSModelReport `json:"models"`
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
// 结果按 (userName, subTableNum) 缓存 qosCacheTTL（30s），
// 配合前端 30s 自动刷新达到「分钟级准实时」新鲜度。
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

func buildQoSMonitorReport(userName string, subTableNum int) (*QoSMonitorReport, error) {
	now := time.Now()
	cutoff := now.Add(-qosScanWindow)

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
						m.calls7d++
						if cls == 0 {
							m.okCalls7d++
						}
						if ts.After(m.lastRequestAt) {
							m.lastRequestAt = ts
						}

						// 基准：7 天成功请求（2xx 且 output>0 且 elapsed>0），
						// 同时累计全局与 Token 分档两边。
						if cls == 0 && r.TokensOutput > 0 && r.ElapsedMs > 0 {
							m.baseElapsed += r.ElapsedMs
							m.baseTokens += r.TokensOutput
							m.baseSamples++

							band := qosTokenBandIndex(r.TokensOutput)
							m.bandElapsed[band] += r.ElapsedMs
							m.bandTokens[band] += r.TokensOutput
							m.bandSamples[band]++
						}

						// 小时桶（7 天窗口内全部记录）
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
			LastRequestAtMs:    m.lastRequestAt.UnixMilli(),
		}

		model.Minutes = buildQoSMinuteSeries(m, base, now)
		model.Hours = buildQoSHourSeries(m, base, now)
		model.Days = buildQoSDaySeries(m, base, now)
		model.Heatmap, model.HeatmapCounts = buildQoSHeatmap(m, base, now)
		model.WeekP50Ms, model.WeekP95Ms, model.WeekOkP50Ms, model.WeekOkP95Ms = qosWeekPercentiles(m)
		model.Live = buildQoSLive(m, base, now)
		model.Live.LastRequestAtMs = m.lastRequestAt.UnixMilli()
		model.Status, model.Alert = qosStatusOf(model.Live)

		// 近 24 小时调用量（小时桶尾部求和）
		for i := len(model.Hours) - 24; i < len(model.Hours); i++ {
			if i >= 0 {
				model.Calls24h += model.Hours[i].Count
			}
		}
		report.TotalCalls7d += m.calls7d
		report.Models = append(report.Models, model)
	}

	// 排序：7 天调用量降序 → 模型名升序（状态卡与表格默认序）
	sort.Slice(report.Models, func(i, j int) bool {
		if report.Models[i].Calls7d != report.Models[j].Calls7d {
			return report.Models[i].Calls7d > report.Models[j].Calls7d
		}
		return report.Models[i].DstModelName < report.Models[j].DstModelName
	})
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
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	keys := make([]string, 0, 7)
	index := make(map[string]int, 7)
	for i := 0; i < 7; i++ {
		key := today.AddDate(0, 0, i-6).Format("2006-01-02")
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

// buildQoSDaySeries 近 7 天连续天桶。天桶由小时桶 pairs 直接合并，
// 保证分位数/均值与逐行重算完全一致，无需二次扫描。
func buildQoSDaySeries(m *qosModelAcc, base qosBaseline, now time.Time) []QoSBucketPoint {
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

	keys, _ := qosDayKeys(now)
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
func qosWeekPercentiles(m *qosModelAcc) (p50, p95, okP50, okP95 int64) {
	all := make([]int64, 0, 1024)
	ok := make([]int64, 0, 1024)
	for _, b := range m.hours {
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
