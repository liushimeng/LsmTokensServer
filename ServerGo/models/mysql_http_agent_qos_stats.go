// v2.0.83: 源站大模型服务质量监控（QoS）聚合函数
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
//
// 核心思路 —— Token 归一化 + 分位统计：
//   - 基准推理速度 baseline = 近 7 天成功请求 Σ elapsed_ms / Σ tokens_output（ms/Token）
//   - 拥堵系数 = elapsed_ms / (baseline × tokens_output)，分档
//     <1.2 正常 / 1.2~2 轻度排队 / 2~5 中度排队 / >5 严重排队
//   - 卡顿率 = 窗口内系数 > 2.0 的请求占比；网络抖动率 = elapsed 标准差/均值（变异系数）
//   - 429 / 5xx / 其他错误率按状态码分类统计
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
	// qosLiveWindow 实时窗口：最近 5 分钟滚动（状态卡实时指标）。
	qosLiveWindow = 5 * time.Minute
	// qosCongestionCap 单请求拥堵系数上限截断（防脏数据拉爆均值）。
	qosCongestionCap = 100.0
	// qosCacheTTL 报告缓存时长（保证实时视图新鲜度，独立于 5 分钟 statsCache）。
	qosCacheTTL = 60 * time.Second
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

// QoSBucketPoint 单时间桶的服务质量聚合结果。
// Count 为桶内总请求数；Error* 为分类错误计数；率为 0~100 百分比。
type QoSBucketPoint struct {
	Bucket        string  `json:"bucket"`
	Count         int64   `json:"count"`
	AvgElapsedMs  int64   `json:"avg_elapsed_ms"`
	P95ElapsedMs  int64   `json:"p95_elapsed_ms"`
	AvgCongestion float64 `json:"avg_congestion"`
	StallRate     float64 `json:"stall_rate"`
	Jitter        float64 `json:"jitter"`
	Error429      int64   `json:"error_429"`
	Error5xx      int64   `json:"error_5xx"`
	ErrorOther    int64   `json:"error_other"`
	ErrorRate     float64 `json:"error_rate"`
	TokensOutput  uint64  `json:"tokens_output"`
}

// QoSLiveStats 实时窗口（最近 5 分钟滚动）指标，用于状态卡。
// Coefficient = 0 表示窗口内无可算拥堵样本（如只有错误请求或无流量）。
type QoSLiveStats struct {
	Coefficient  float64 `json:"coefficient"`
	AvgElapsedMs int64   `json:"avg_elapsed_ms"`
	ErrorRate    float64 `json:"error_rate"`
	ReqCount     int64   `json:"req_count"`
}

// QoSModelReport 单个目标源站模型的完整 QoS 报告。
type QoSModelReport struct {
	DstModelName       string           `json:"dst_model_name"`
	BaselineMsPerToken float64          `json:"baseline_ms_per_token"`
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
	Calls24h           int64            `json:"calls_24h"`
	Calls7d            int64            `json:"calls_7d"`
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
	ResponseStatus string    `gorm:"column:response_status"`
	TokensOutput   uint64    `gorm:"column:tokens_output_size"`
	ElapsedMs      int64     `gorm:"column:elapsed_ms"`
}

// qosRowPair 桶内原始请求对（elapsed × output tokens）。
// 拥堵系数依赖基准终值（全窗口 Σ），扫描阶段只记原始对，终态统一重算。
type qosRowPair struct {
	elapsed int64
	tokens  uint64
}

// qosBucketAcc 单桶增量累积器。pairs 保留原始值，
// 终态计算均值、P95、拥堵系数与变异系数（抖动率）。
type qosBucketAcc struct {
	count     int64
	pairs     []qosRowPair
	err429    int64
	err5xx    int64
	errOther  int64
	tokensOut uint64
}

func (b *qosBucketAcc) add(r qosScanRow, statusClass int) {
	b.count++
	b.pairs = append(b.pairs, qosRowPair{elapsed: r.ElapsedMs, tokens: r.TokensOutput})
	switch statusClass {
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
	baseElapsed int64  // 基准分子：7 天成功请求 Σ elapsed
	baseTokens  uint64 // 基准分母：7 天成功请求 Σ output tokens
	baseSamples int64
	minutes     map[string]*qosBucketAcc
	hours       map[string]*qosBucketAcc
	weekElapsed []int64 // 7 天全量 elapsed（周 P50/P95）
	calls7d     int64
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
			Select("id, created_at, request_start_at, dst_model_name, response_status, tokens_output_size, elapsed_ms").
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
// 结果按 (userName, subTableNum) 缓存 qosCacheTTL（60s），
// 配合前端 30s 自动刷新达到「分钟级准实时」新鲜度（方案 §3.4）。
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

						// 基准：7 天成功请求（2xx 且 output>0 且 elapsed>0）
						if cls == 0 && r.TokensOutput > 0 && r.ElapsedMs > 0 {
							m.baseElapsed += r.ElapsedMs
							m.baseTokens += r.TokensOutput
							m.baseSamples++
						}

						m.weekElapsed = append(m.weekElapsed, r.ElapsedMs)
						m.calls7d++

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
		baseline := 0.0
		if m.baseTokens > 0 && m.baseElapsed > 0 {
			baseline = float64(m.baseElapsed) / float64(m.baseTokens)
		}
		model := QoSModelReport{
			DstModelName:       m.name,
			BaselineMsPerToken: qosRound2(baseline),
			BaselineSamples:    m.baseSamples,
			Calls7d:            m.calls7d,
		}

		model.Minutes = buildQoSMinuteSeries(m, baseline, now)
		model.Hours = buildQoSHourSeries(m, baseline, now)
		model.Days = buildQoSDaySeries(m, baseline, now)
		model.Heatmap, model.HeatmapCounts = buildQoSHeatmap(m, baseline, now)
		model.WeekP50Ms, model.WeekP95Ms = qosPercentiles(m.weekElapsed, 0.5, 0.95)
		model.Live = buildQoSLive(m, baseline, now)
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

// finalizeQoSBucket 把累积器转成响应点（基准参与拥堵系数计算）。
func finalizeQoSBucket(key string, b *qosBucketAcc, baseline float64) QoSBucketPoint {
	p := QoSBucketPoint{
		Bucket:       key,
		Count:        b.count,
		Error429:     b.err429,
		Error5xx:     b.err5xx,
		ErrorOther:   b.errOther,
		TokensOutput: b.tokensOut,
	}
	if b.count == 0 {
		return p
	}

	var sumElapsed int64
	elapsed := make([]int64, 0, len(b.pairs))
	congestion := make([]float64, 0, len(b.pairs))
	for _, pair := range b.pairs {
		sumElapsed += pair.elapsed
		elapsed = append(elapsed, pair.elapsed)
		// 拥堵系数：基准可用且 output>0 才有定义（429/5xx 通常 output=0，天然排除）
		if baseline > 0 && pair.tokens > 0 {
			coeff := float64(pair.elapsed) / (baseline * float64(pair.tokens))
			if coeff > qosCongestionCap {
				coeff = qosCongestionCap
			}
			congestion = append(congestion, coeff)
		}
	}

	p.AvgElapsedMs = sumElapsed / b.count
	p.P95ElapsedMs = qosPercentile(elapsed, 0.95)
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
	}
	p.Jitter = qosRound2(qosJitter(elapsed))
	errCount := b.err429 + b.err5xx + b.errOther
	if errCount > 0 {
		p.ErrorRate = qosRound2(float64(errCount) / float64(b.count) * 100)
	}
	return p
}

// buildQoSMinuteSeries 近 60 分钟连续分钟桶（缺数据补零）。
func buildQoSMinuteSeries(m *qosModelAcc, baseline float64, now time.Time) []QoSBucketPoint {
	points := make([]QoSBucketPoint, 0, qosMinuteBuckets)
	start := now.Add(-time.Duration(qosMinuteBuckets-1) * time.Minute).Truncate(time.Minute)
	goFmt := "2006-01-02 15:04"
	for t := start; !t.After(now); t = t.Add(time.Minute) {
		key := t.Format(goFmt)
		if b, ok := m.minutes[key]; ok {
			points = append(points, finalizeQoSBucket(key, b, baseline))
		} else {
			points = append(points, QoSBucketPoint{Bucket: key})
		}
	}
	return points
}

// buildQoSHourSeries 近 24 小时连续小时桶（缺数据补零）。
func buildQoSHourSeries(m *qosModelAcc, baseline float64, now time.Time) []QoSBucketPoint {
	points := make([]QoSBucketPoint, 0, 24)
	start := now.Add(-23 * time.Hour).Truncate(time.Hour)
	goFmt := "2006-01-02 15:04"
	for t := start; !t.After(now); t = t.Add(time.Hour) {
		key := t.Format(goFmt)
		if b, ok := m.hours[key]; ok {
			points = append(points, finalizeQoSBucket(key, b, baseline))
		} else {
			points = append(points, QoSBucketPoint{Bucket: key})
		}
	}
	return points
}

// buildQoSDaySeries 近 7 天连续天桶。天桶由小时桶 pairs 直接合并，
// 保证分位数/均值与逐行重算完全一致，无需二次扫描。
func buildQoSDaySeries(m *qosModelAcc, baseline float64, now time.Time) []QoSBucketPoint {
	// 先按日期归并所有窗口内小时桶（不止 24 小时，覆盖完整 7 天）
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
		db.count += b.count
		db.pairs = append(db.pairs, b.pairs...)
		db.err429 += b.err429
		db.err5xx += b.err5xx
		db.errOther += b.errOther
		db.tokensOut += b.tokensOut
	}

	points := make([]QoSBucketPoint, 0, 7)
	goFmt := "2006-01-02"
	for i := 6; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format(goFmt)
		if b, ok := dayBuckets[key]; ok {
			points = append(points, finalizeQoSBucket(key, b, baseline))
		} else {
			points = append(points, QoSBucketPoint{Bucket: key})
		}
	}
	return points
}

// buildQoSHeatmap 拥堵热力图：[7 天][24 小时]，行 0 = 6 天前，行 6 = 今天；
// 单元格 = 该小时桶平均拥堵系数，无数据为 -1（前端渲染灰色）。
func buildQoSHeatmap(m *qosModelAcc, baseline float64, now time.Time) ([][]float64, [][]int64) {
	values := make([][]float64, 7)
	counts := make([][]int64, 7)
	for i := range values {
		values[i] = make([]float64, 24)
		counts[i] = make([]int64, 24)
		for j := range values[i] {
			values[i][j] = -1
		}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for key, b := range m.hours {
		t, err := time.ParseInLocation("2006-01-02 15:04", key, now.Location())
		if err != nil {
			continue
		}
		dayIdx := 6 - int(today.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())).Hours()/24)
		if dayIdx < 0 || dayIdx > 6 {
			continue // 理论上不会出现（扫描窗口 7 天），防御性跳过
		}
		point := finalizeQoSBucket(key, b, baseline)
		values[dayIdx][t.Hour()] = point.AvgCongestion
		counts[dayIdx][t.Hour()] = point.Count
	}
	return values, counts
}

// buildQoSLive 实时窗口（最近 5 分钟）指标：合并窗口内分钟桶。
// 分钟键 "2006-01-02 15:04" 字典序 == 时间序，直接字符串比较。
func buildQoSLive(m *qosModelAcc, baseline float64, now time.Time) QoSLiveStats {
	liveKey := now.Add(-qosLiveWindow).Truncate(time.Minute).Format("2006-01-02 15:04")
	merged := &qosBucketAcc{}
	for key, b := range m.minutes {
		if key >= liveKey {
			merged.count += b.count
			merged.pairs = append(merged.pairs, b.pairs...)
			merged.err429 += b.err429
			merged.err5xx += b.err5xx
			merged.errOther += b.errOther
			merged.tokensOut += b.tokensOut
		}
	}
	if merged.count == 0 {
		return QoSLiveStats{}
	}
	point := finalizeQoSBucket("live", merged, baseline)
	live := QoSLiveStats{
		Coefficient:  point.AvgCongestion,
		AvgElapsedMs: point.AvgElapsedMs,
		ErrorRate:    point.ErrorRate,
		ReqCount:     point.Count,
	}
	return live
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

// qosPercentiles 一次排序取双分位（周 P50/P95）。
func qosPercentiles(values []int64, q1, q2 float64) (int64, int64) {
	if len(values) == 0 {
		return 0, 0
	}
	vals := make([]int64, len(values))
	copy(vals, values)
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	pick := func(q float64) int64 {
		idx := int(math.Ceil(q*float64(len(vals)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(vals) {
			idx = len(vals) - 1
		}
		return vals[idx]
	}
	return pick(q1), pick(q2)
}

// qosJitter 变异系数（标准差/均值，%）；均值 0 时返回 0。
func qosJitter(values []int64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += float64(v)
	}
	mean := sum / float64(n)
	if mean == 0 {
		return 0
	}
	var sq float64
	for _, v := range values {
		d := float64(v) - mean
		sq += d * d
	}
	stddev := math.Sqrt(sq / float64(n))
	return stddev / mean * 100
}

// qosRound2 保留两位小数。
func qosRound2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ---- 报告缓存（60s，独立于 statsCache 的 5 分钟 TTL）----

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
