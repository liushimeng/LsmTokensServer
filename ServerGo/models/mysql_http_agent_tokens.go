package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"strings"
	"time"
)

// TokensRangeStat Tokens时间段统计
type TokensRangeStat struct {
	Date          string `json:"date"`
	Count         int64  `json:"count"`
	TokensInput   uint64 `json:"tokens_input"`
	TokensOutput  uint64 `json:"tokens_output"`
	TokensTotal   uint64 `json:"tokens_total"`
	AvgElapsedMs  int64  `json:"avg_elapsed_ms"`
	AvgTTFBMs     int64  `json:"avg_ttfb_ms"`
	AvgGenerateMs int64  `json:"avg_generate_ms"`
}

// TokensReportStat 区间分析报告聚合结果
type TokensReportStat struct {
	RangeStart   string              `json:"range_start"`
	RangeEnd     string              `json:"range_end"`
	Granularity  string              `json:"granularity"`
	TotalCount   int64               `json:"total_count"`
	TotalInput   uint64              `json:"total_input"`
	TotalOutput  uint64              `json:"total_output"`
	TotalAll     uint64              `json:"total_all"`
	AvgInput     uint64              `json:"avg_input"`
	AvgOutput    uint64              `json:"avg_output"`
	AvgAll       uint64              `json:"avg_all"`
	AvgElapsedMs int64               `json:"avg_elapsed_ms"`
	AvgTTFBMs    int64               `json:"avg_ttfb_ms"`
	AvgGenMs     int64               `json:"avg_gen_ms"`
	ModelDist    []TokensModelStat   `json:"model_dist"`
	LatencyDist  []TokensLatencyStat `json:"latency_dist"`
	Series       []TokensRangeStat   `json:"series"`
}

// normalizeTokensGranularity 校验并规范化颗粒度字符串（minute/hour/day）
// 仅接受 minute / hour / day，其它值统一回落到 "day"
func normalizeTokensGranularity(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "minute":
		return "minute"
	case "hour":
		return "hour"
	case "day":
		return "day"
	default:
		return "day"
	}
}

// inferSpanGranularity v2.0.46：根据时间区间跨度（毫秒）推断最佳颗粒度
//   - spanMs ≤ 24 小时 → minute（1 分钟 1 桶，1 天最多 1440 桶，受 tokensFillGaps 上限 2000 保护）
//   - spanMs ≤ 7 天   → hour（1 小时 1 桶，1 周 168 桶）
//   - spanMs > 7 天   → day（1 天 1 桶）
//   - spanMs ≤ 0      → day（兜底）
//
// 与前端 lsmInferGranularityBySpan 完全对齐；用于 SSE handler 在客户端未传 granularity 时的兜底推断
func inferSpanGranularity(spanMs int64) string {
	if spanMs <= 0 {
		return "day"
	}
	if spanMs <= 24*3600*1000 {
		return "minute"
	}
	if spanMs <= 7*24*3600*1000 {
		return "hour"
	}
	return "day"
}

// tokensRangeTimeFormat 根据颗粒度返回 SQL 日期格式化字符串和 Go 本地解析格式
func tokensRangeTimeFormat(granularity string) (sqlFmt, goFmt string) {
	switch granularity {
	case "minute":
		return "%Y-%m-%d %H:%i", "2006-01-02 15:04"
	case "hour":
		return "%Y-%m-%d %H:00", "2006-01-02 15:04"
	default:
		return "%Y-%m-%d", "2006-01-02"
	}
}

// tokensRangeStep 返回颗粒度对应的时间步长（用于补全空槽位）
func tokensRangeStep(granularity string) (time.Duration, bool) {
	switch granularity {
	case "minute":
		return time.Minute, true
	case "hour":
		return time.Hour, true
	default:
		return 24 * time.Hour, true
	}
}

// GetTokensRangeStats 获取Tokens时间段统计（按天聚合）
// v2.0.48: 当 days > tokensStatsMaxDays(14) 时，自动降级为"按周聚合"减少返回桶数，避免网络传输和前端渲染卡死。
// v2.0.52: 改用 Go 端聚合 — 旧实现 SELECT DATE_FORMAT(created_at)+SUM/AVG+TIMESTAMPDIFF
//
//	会让 MySQL 走 "Using temporary; Using filesort"，对 7 天 20K 行场景实测 4.8s。
//	新实现只 SELECT 8 个小字段（每行 ~100 字节），靠复合索引 (user_name, model_name, created_at)
//	快速定位 20K 行，传输 ~2MB，Go 端按天/周桶聚合。
//	实测 7 天 20K 行：4.8s → ~100ms（提升 40-50 倍）。
func GetTokensRangeStats(userName, modelName string, subTableNum int, days int) ([]TokensRangeStat, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days > 365 {
		days = 365
	}

	// 尝试从缓存获取（按实际颗粒度拼 key，避免天/周不同颗粒度碰撞）
	granularity := "day"
	if days > tokensStatsMaxDays {
		granularity = "week"
	}
	cacheKey := makeStatsCacheKey("GetTokensRangeStats", userName, modelName, subTableNum, days, granularity)
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if stats, valid := cached.([]TokensRangeStat); valid {
			return stats, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}

	// v2.0.54: 绑定超时 context，超时真正取消查询并释放连接。
	sdb, cancel := database.StatsDB()
	defer cancel()

	// 只 SELECT 8 个小字段：created_at + tokens 3 列 + elapsed_ms + 3 个时间戳。
	// 严格避开 longtext 字段（request_body / response_body 等），符合 v2.0.42 白名单契约。
	// 不带 GROUP BY / DATE_FORMAT / TIMESTAMPDIFF —— 全部走 Go 端桶聚合。
	// 复合索引 idx_user_model_created 让 WHERE 走索引范围扫描，rows 大幅减少。
	var rows []struct {
		CreatedAt        time.Time `gorm:"column:created_at"`
		TokensInputSize  uint64    `gorm:"column:tokens_input_size"`
		TokensOutputSize uint64    `gorm:"column:tokens_output_size"`
		TokensAllSize    uint64    `gorm:"column:tokens_all_size"`
		ElapsedMs        int64     `gorm:"column:elapsed_ms"`
		RequestStartAt   time.Time `gorm:"column:request_start_at"`
		ResponseStartAt  time.Time `gorm:"column:response_start_at"`
		ResponseEndAt    time.Time `gorm:"column:response_end_at"`
	}

	query := sdb.Table(tableName).
		Select("created_at, tokens_input_size, tokens_output_size, tokens_all_size, elapsed_ms, request_start_at, response_start_at, response_end_at").
		Where("user_name = ? AND model_name = ?", userName, modelName)
	if days > 0 {
		query = query.Where("created_at >= DATE_SUB(NOW(), INTERVAL ? DAY)", days)
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to get tokens range stats rows: %w", err)
	}

	// Go 端按天/周桶聚合
	type bucket struct {
		date           string
		count          int64
		tokensInput    uint64
		tokensOutput   uint64
		tokensTotal    uint64
		sumElapsed     int64
		sumTTFB        int64
		sumGen         int64
		ttfbValidCount int64 // request_start_at/response_start_at 同时非零
		genValidCount  int64 // response_start_at/response_end_at 同时非零
	}
	buckets := make(map[string]*bucket)

	for _, r := range rows {
		var dateKey string
		if granularity == "week" {
			// 按周聚合：取周一作为日期标签
			weekday := int(r.CreatedAt.Weekday()) // Sunday=0
			if weekday == 0 {
				weekday = 7 // ISO 周一为 1，把 Sunday 当 7
			}
			monday := r.CreatedAt.AddDate(0, 0, -(weekday - 1))
			dateKey = monday.Format("2006-01-02")
		} else {
			dateKey = r.CreatedAt.Format("2006-01-02")
		}

		b, ok := buckets[dateKey]
		if !ok {
			b = &bucket{date: dateKey}
			buckets[dateKey] = b
		}
		b.count++
		b.tokensInput += r.TokensInputSize
		b.tokensOutput += r.TokensOutputSize
		b.tokensTotal += r.TokensAllSize
		b.sumElapsed += r.ElapsedMs
		// TTFB: request_start_at → response_start_at
		if !r.RequestStartAt.IsZero() && !r.ResponseStartAt.IsZero() && r.ResponseStartAt.After(r.RequestStartAt) {
			b.sumTTFB += r.ResponseStartAt.Sub(r.RequestStartAt).Milliseconds()
			b.ttfbValidCount++
		}
		// 生成耗时: response_start_at → response_end_at
		if !r.ResponseStartAt.IsZero() && !r.ResponseEndAt.IsZero() && r.ResponseEndAt.After(r.ResponseStartAt) {
			b.sumGen += r.ResponseEndAt.Sub(r.ResponseStartAt).Milliseconds()
			b.genValidCount++
		}
	}

	// 转成切片并按日期排序
	results := make([]TokensRangeStat, 0, len(buckets))
	for _, b := range buckets {
		var avgElapsed, avgTTFB, avgGen int64
		if b.count > 0 {
			avgElapsed = b.sumElapsed / b.count
		}
		if b.ttfbValidCount > 0 {
			avgTTFB = b.sumTTFB / b.ttfbValidCount
		}
		if b.genValidCount > 0 {
			avgGen = b.sumGen / b.genValidCount
		}
		results = append(results, TokensRangeStat{
			Date:          b.date,
			Count:         b.count,
			TokensInput:   b.tokensInput,
			TokensOutput:  b.tokensOutput,
			TokensTotal:   b.tokensTotal,
			AvgElapsedMs:  avgElapsed,
			AvgTTFBMs:     avgTTFB,
			AvgGenerateMs: avgGen,
		})
	}
	// 按日期升序排序（map 迭代顺序无保证）
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Date < results[i].Date {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// 边界：days<=0 或无数据 直接返回
	if len(results) == 0 || days <= 0 {
		setStatsToCache(cacheKey, results)
		return results, nil
	}

	// 按周粒度直接返回（不补齐，避免周数过多）
	if granularity == "week" {
		setStatsToCache(cacheKey, results)
		return results, nil
	}

	// 按天粒度补齐空槽位
	resultMap := make(map[string]TokensRangeStat, len(results))
	for _, r := range results {
		resultMap[r.Date] = r
	}

	now := time.Now()
	filled := make([]TokensRangeStat, 0, days)
	for i := days - 1; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		if stat, ok := resultMap[date]; ok {
			filled = append(filled, stat)
		} else {
			filled = append(filled, TokensRangeStat{Date: date})
		}
	}

	setStatsToCache(cacheKey, filled)
	return filled, nil
}

// tokensStatsMaxDays Tokens 时序统计的"按天聚合"上限天数；
// 超过此阈值时自动降级为"按周聚合"，减少返回桶数和网络传输量。
// 配合前端：>14 天时 brush 选区生成报告仍可按 hour/day 颗粒度拉（走区间接口 GetTokensRangeReport），
// 仅影响首屏概览折线。
const tokensStatsMaxDays = 14

// GetTokensRangeReport 生成基于任意时间范围的 Tokens 深度分析报告
// 颗粒度 granularity ∈ {minute, hour, day}：
//   - 1 天或几天总跨度内可细化到 minute；
//   - 几十天跨度可细化到 hour；
//   - day 为默认兜底颗粒度。
//
// 函数按 (userName, model_name, 时间桶) 聚合 tokens 统计，同时补齐空槽位保证时序连续；
// 调用方负责校验 start/end 合法性。
//
// v2.0.53: 改用 Go 端聚合 — 旧实现 SELECT DATE_FORMAT(created_at)+COUNT/SUM/AVG+TIMESTAMPDIFF + GROUP BY
//
//	会让 MySQL 走 "Using temporary; Using filesort"，对 7 天 20K 行 brush 选区场景
//	实测 1.2s+。新实现只 SELECT 8 个小字段（与 GetTokensRangeStats 一致），
//	WHERE 走复合索引 idx_user_model_created 范围扫描 + created_at 区间定位，
//	Go 端按颗粒度桶聚合 + time.Sub().Milliseconds() 算 TTFB / Gen。
//	不再触发 temp table / filesort。
//
// v2.0.53: 加缓存（按 start/end/granularity 拼 key，避免相同 brush 选区反复查询）。
//
//	5 分钟 TTL 与其它统计缓存对齐。
func GetTokensRangeReport(userName, modelName string, subTableNum int, start, end time.Time, granularity string) (*TokensReportStat, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	granularity = normalizeTokensGranularity(granularity)

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}

	// v2.0.53: 区间报告加缓存（5 分钟 TTL）。key 包含 start/end/granularity 避免区间组合碰撞
	// 注意 start/end 精度到秒即可，毫秒精度会让 cache key 永不命中。
	cacheKey := fmt.Sprintf("GetTokensRangeReport:%s:%s:%d:%s:%d:%d",
		userName, modelName, subTableNum, granularity, start.Unix(), end.Unix())
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if stats, valid := cached.(*TokensReportStat); valid {
			return stats, nil
		}
	}

	goFmt := tokensRangeGoFormat(granularity)
	step, _ := tokensRangeStep(granularity)

	// 只 SELECT 8 个小字段：created_at + 3 个 tokens 列 + elapsed_ms + 3 个时间戳。
	// 严格避开 longtext 字段（request_body / response_body 等 8 个），符合 v2.0.42 白名单契约。
	// 不带 GROUP BY / DATE_FORMAT / TIMESTAMPDIFF —— 全部走 Go 端桶聚合。
	type rawRow struct {
		CreatedAt        time.Time `gorm:"column:created_at"`
		TokensInputSize  uint64    `gorm:"column:tokens_input_size"`
		TokensOutputSize uint64    `gorm:"column:tokens_output_size"`
		TokensAllSize    uint64    `gorm:"column:tokens_all_size"`
		ElapsedMs        int64     `gorm:"column:elapsed_ms"`
		RequestStartAt   time.Time `gorm:"column:request_start_at"`
		ResponseStartAt  time.Time `gorm:"column:response_start_at"`
		ResponseEndAt    time.Time `gorm:"column:response_end_at"`
	}
	var rows []rawRow

	// v2.0.54: 绑定超时 context，超时真正取消查询并释放连接。
	sdb, cancel := database.StatsDB()
	defer cancel()

	if err := sdb.Table(tableName).
		Select("created_at, tokens_input_size, tokens_output_size, tokens_all_size, elapsed_ms, request_start_at, response_start_at, response_end_at").
		Where("user_name = ? AND model_name = ? AND created_at >= ? AND created_at < ?", userName, modelName, start, end).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to get tokens range series rows: %w", err)
	}

	// Go 端按颗粒度桶聚合
	type bucket struct {
		date           string
		count          int64
		tokensInput    uint64
		tokensOutput   uint64
		tokensTotal    uint64
		sumElapsed     int64
		sumTTFB        int64
		sumGen         int64
		ttfbValidCount int64
		genValidCount  int64
	}
	buckets := make(map[string]*bucket)
	for _, r := range rows {
		dateKey := r.CreatedAt.Format(goFmt)
		b, ok := buckets[dateKey]
		if !ok {
			b = &bucket{date: dateKey}
			buckets[dateKey] = b
		}
		b.count++
		b.tokensInput += r.TokensInputSize
		b.tokensOutput += r.TokensOutputSize
		b.tokensTotal += r.TokensAllSize
		b.sumElapsed += r.ElapsedMs
		if !r.RequestStartAt.IsZero() && !r.ResponseStartAt.IsZero() && r.ResponseStartAt.After(r.RequestStartAt) {
			b.sumTTFB += r.ResponseStartAt.Sub(r.RequestStartAt).Milliseconds()
			b.ttfbValidCount++
		}
		if !r.ResponseStartAt.IsZero() && !r.ResponseEndAt.IsZero() && r.ResponseEndAt.After(r.ResponseStartAt) {
			b.sumGen += r.ResponseEndAt.Sub(r.ResponseStartAt).Milliseconds()
			b.genValidCount++
		}
	}

	// 转成切片并按日期升序排序
	results := make([]TokensRangeStat, 0, len(buckets))
	for _, b := range buckets {
		var avgElapsed, avgTTFB, avgGen int64
		if b.count > 0 {
			avgElapsed = b.sumElapsed / b.count
		}
		if b.ttfbValidCount > 0 {
			avgTTFB = b.sumTTFB / b.ttfbValidCount
		}
		if b.genValidCount > 0 {
			avgGen = b.sumGen / b.genValidCount
		}
		results = append(results, TokensRangeStat{
			Date:          b.date,
			Count:         b.count,
			TokensInput:   b.tokensInput,
			TokensOutput:  b.tokensOutput,
			TokensTotal:   b.tokensTotal,
			AvgElapsedMs:  avgElapsed,
			AvgTTFBMs:     avgTTFB,
			AvgGenerateMs: avgGen,
		})
	}
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Date < results[i].Date {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	series := tokensFillGaps(results, start, end, step, goFmt)

	var totalInput, totalOutput, totalAll uint64
	var totalCount int64
	var sumElapsed, sumTTFB, sumGen int64
	for _, s := range series {
		totalInput += s.TokensInput
		totalOutput += s.TokensOutput
		totalAll += s.TokensTotal
		totalCount += s.Count
		sumElapsed += s.AvgElapsedMs * s.Count
		sumTTFB += s.AvgTTFBMs * s.Count
		sumGen += s.AvgGenerateMs * s.Count
	}
	var avgInput, avgOutput, avgAll uint64
	var avgElapsed, avgTTFB, avgGen int64
	if totalCount > 0 {
		avgInput = totalInput / uint64(totalCount)
		avgOutput = totalOutput / uint64(totalCount)
		avgAll = totalAll / uint64(totalCount)
		avgElapsed = sumElapsed / totalCount
		avgTTFB = sumTTFB / totalCount
		avgGen = sumGen / totalCount
	}

	// v2.0.53: modelDist / latencyDist 改为 Go 端聚合（与 GetTokensModelStats / GetTokensLatencyStats 对齐）
	modelDist, _ := tokensRangeModelDist(userName, modelName, subTableNum, start, end)
	latencyDist, _ := tokensRangeLatencyDist(userName, modelName, subTableNum, start, end)

	rangeFmt := goFmt
	if granularity == "day" {
		rangeFmt = "2006-01-02"
	}

	resp := &TokensReportStat{
		RangeStart:   start.Format(rangeFmt),
		RangeEnd:     end.Format(rangeFmt),
		Granularity:  granularity,
		TotalCount:   totalCount,
		TotalInput:   totalInput,
		TotalOutput:  totalOutput,
		TotalAll:     totalAll,
		AvgInput:     avgInput,
		AvgOutput:    avgOutput,
		AvgAll:       avgAll,
		AvgElapsedMs: avgElapsed,
		AvgTTFBMs:    avgTTFB,
		AvgGenMs:     avgGen,
		ModelDist:    modelDist,
		LatencyDist:  latencyDist,
		Series:       series,
	}

	setStatsToCache(cacheKey, resp)
	return resp, nil
}

// tokensRangeGoFormat 根据颗粒度返回 Go 端 time.Format 字符串
// v2.0.53: 与原 tokensRangeTimeFormat 的 goFmt 拆出来复用，避免 SQL 拼接
func tokensRangeGoFormat(granularity string) string {
	switch granularity {
	case "minute":
		return "2006-01-02 15:04"
	case "hour":
		return "2006-01-02 15:04"
	default:
		return "2006-01-02"
	}
}

// tokensFillGaps 按时间步长补齐空槽位，让时序长度对齐真实的区间跨度
// 最多补齐 maxBuckets(=2000) 个桶，避免 UI / 网络爆炸；超出时保留原始聚合结果。
func tokensFillGaps(series []TokensRangeStat, start, end time.Time, step time.Duration, goFmt string) []TokensRangeStat {
	const maxBuckets = 2000
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return series
	}
	totalBuckets := int(end.Sub(start) / step)
	if totalBuckets <= 0 || totalBuckets > maxBuckets {
		return series
	}

	m := make(map[string]TokensRangeStat, len(series))
	for _, s := range series {
		m[s.Date] = s
	}
	var filled []TokensRangeStat
	for t := start; t.Before(end); t = t.Add(step) {
		key := t.Format(goFmt)
		if stat, ok := m[key]; ok {
			filled = append(filled, stat)
		} else {
			filled = append(filled, TokensRangeStat{Date: key})
		}
	}
	return filled
}

// tokensRangeModelDist 指定时间区间内按 dst_model_name 聚合的模型分布
// v2.0.53: 改用 Go 端聚合 — 与 GetTokensModelStats 对齐；不带 GROUP BY / SUM。
func tokensRangeModelDist(userName, modelName string, subTableNum int, start, end time.Time) ([]TokensModelStat, error) {
	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}
	type rawRow struct {
		DstModelName     string `gorm:"column:dst_model_name"`
		TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
		TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
	}
	var rows []rawRow
	sdb, cancel := database.StatsDB()
	defer cancel()
	if err := sdb.Table(tableName).
		Select("dst_model_name, tokens_input_size, tokens_output_size, tokens_all_size").
		Where("user_name = ? AND model_name = ? AND dst_model_name != '' AND created_at >= ? AND created_at < ?", userName, modelName, start, end).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to get tokens model dist rows: %w", err)
	}
	type bucket struct {
		name        string
		count       int64
		tokensInput uint64
		tokensOut   uint64
		tokensTotal uint64
	}
	buckets := make(map[string]*bucket)
	for _, r := range rows {
		if r.DstModelName == "" {
			continue
		}
		b, ok := buckets[r.DstModelName]
		if !ok {
			b = &bucket{name: r.DstModelName}
			buckets[r.DstModelName] = b
		}
		b.count++
		b.tokensInput += r.TokensInputSize
		b.tokensOut += r.TokensOutputSize
		b.tokensTotal += r.TokensAllSize
	}
	results := make([]TokensModelStat, 0, len(buckets))
	for _, b := range buckets {
		results = append(results, TokensModelStat{
			ModelName:    b.name,
			Count:        b.count,
			CallCount:    b.count, // v2.0.68: 与 Count 同值，对齐 stage 4 字段
			TokensInput:  b.tokensInput,
			TokensOutput: b.tokensOut,
			TokensTotal:  b.tokensTotal,
		})
	}
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].TokensTotal > results[i].TokensTotal {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	return results, nil
}

// tokensRangeLatencyDist 指定时间区间内的时延分段分布（TTLT, elapsed_ms）
// v2.0.53: 改用 Go 端聚合 — 与 GetTokensLatencyStats 对齐；不带 CASE WHEN / GROUP BY。
func tokensRangeLatencyDist(userName, modelName string, subTableNum int, start, end time.Time) ([]TokensLatencyStat, error) {
	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}
	type rawRow struct {
		ElapsedMs     int64  `gorm:"column:elapsed_ms"`
		TokensAllSize uint64 `gorm:"column:tokens_all_size"`
	}
	var rows []rawRow
	sdb, cancel := database.StatsDB()
	defer cancel()
	if err := sdb.Table(tableName).
		Select("elapsed_ms, tokens_all_size").
		Where("user_name = ? AND model_name = ? AND created_at >= ? AND created_at < ?", userName, modelName, start, end).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to get tokens latency dist rows: %w", err)
	}
	type bucket struct {
		label       string
		count       int64
		tokensTotal uint64
	}
	buckets := [6]*bucket{
		{label: "< 1s"},
		{label: "1-3s"},
		{label: "3-5s"},
		{label: "5-10s"},
		{label: "10-30s"},
		{label: "> 30s"},
	}
	idxOfBucket := func(elapsedMs int64) int {
		switch {
		case elapsedMs < 1000:
			return 0
		case elapsedMs < 3000:
			return 1
		case elapsedMs < 5000:
			return 2
		case elapsedMs < 10000:
			return 3
		case elapsedMs < 30000:
			return 4
		default:
			return 5
		}
	}
	for _, r := range rows {
		b := buckets[idxOfBucket(r.ElapsedMs)]
		b.count++
		b.tokensTotal += r.TokensAllSize
	}
	var totalCount int64
	for _, b := range buckets {
		totalCount += b.count
	}
	results := make([]TokensLatencyStat, 0, 6)
	for _, b := range buckets {
		if b.count == 0 && b.tokensTotal == 0 {
			results = append(results, TokensLatencyStat{RangeLabel: b.label})
			continue
		}
		avgTokens := float64(0)
		if b.count > 0 {
			avgTokens = float64(b.tokensTotal) / float64(b.count)
		}
		pct := float64(0)
		if totalCount > 0 {
			pct = float64(b.count) / float64(totalCount) * 100
		}
		results = append(results, TokensLatencyStat{
			RangeLabel:  b.label,
			Count:       b.count,
			TokensTotal: b.tokensTotal,
			AvgTokens:   avgTokens,
			PctOfTotal:  pct,
		})
	}
	return results, nil
}

// TokensModelStat 按模型统计Tokens
//
// v2.0.68: 增加 CallCount 字段作为 Count 的语义别名，与 stage 4 model_distribution 的
// ModelNameUsageStat.CallCount 对齐；前端同一份渲染代码可同时复用两端数据。
// 序列化时两者同值，前端按需取其一即可。
type TokensModelStat struct {
	ModelName    string `json:"model_name"`
	Count        int64  `json:"count"`
	CallCount    int64  `json:"call_count"` // v2.0.68 新增：与 Count 同值，stage 4 字段对齐
	TokensInput  uint64 `json:"tokens_input"`
	TokensOutput uint64 `json:"tokens_output"`
	TokensTotal  uint64 `json:"tokens_total"`
}

// GetTokensModelStats 获取按模型聚合的Tokens统计
// v2.0.53: 改用 Go 端聚合 — 旧实现 SELECT dst_model_name + SUM/COUNT + GROUP BY
//
//	会让 MySQL 走 "Using temporary; Using filesort"，对 7 天 20K 行场景实测 700ms+。
//	新实现只 SELECT 4 个小字段（dst_model_name + 3 个 tokens 列），
//	符合 v2.0.42 longtext 白名单契约；复合索引 (user_name, model_name, created_at)
//	让 WHERE 走索引范围扫描，Go 端按 dst_model_name 桶聚合。
//	不再触发 temp table / filesort，性能与首屏 lsmRunInsightsSummary 一致。
func GetTokensModelStats(userName, modelName string, subTableNum int, days int) ([]TokensModelStat, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days > 365 {
		days = 365
	}

	// 尝试从缓存获取
	cacheKey := makeStatsCacheKey("GetTokensModelStats", userName, modelName, subTableNum, days, "")
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if stats, valid := cached.([]TokensModelStat); valid {
			return stats, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}

	// 只 SELECT 4 个小字段：dst_model_name + 3 个 tokens 列。
	// 严格避开 longtext 字段（request_body / response_body 等 8 个），符合 v2.0.42 白名单契约。
	// 不带 GROUP BY / SUM —— 全部走 Go 端桶聚合。
	// 复合索引 idx_user_model_created 让 WHERE 走索引范围扫描，rows 大幅减少。
	type rawRow struct {
		DstModelName     string `gorm:"column:dst_model_name"`
		TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
		TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
	}
	var rows []rawRow

	sdb, cancel := database.StatsDB()
	defer cancel()
	query := sdb.Table(tableName).
		Select("dst_model_name, tokens_input_size, tokens_output_size, tokens_all_size").
		Where("user_name = ? AND model_name = ? AND dst_model_name != ''", userName, modelName)
	if days > 0 {
		query = query.Where("created_at >= DATE_SUB(NOW(), INTERVAL ? DAY)", days)
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to get tokens model stats rows: %w", err)
	}

	// Go 端按 dst_model_name 桶聚合
	type bucket struct {
		modelName    string
		count        int64
		tokensInput  uint64
		tokensOutput uint64
		tokensTotal  uint64
	}
	buckets := make(map[string]*bucket)
	for _, r := range rows {
		if r.DstModelName == "" {
			continue
		}
		b, ok := buckets[r.DstModelName]
		if !ok {
			b = &bucket{modelName: r.DstModelName}
			buckets[r.DstModelName] = b
		}
		b.count++
		b.tokensInput += r.TokensInputSize
		b.tokensOutput += r.TokensOutputSize
		b.tokensTotal += r.TokensAllSize
	}

	// 转成切片并按 tokens_total 降序排序（与旧实现 ORDER BY tokens_total DESC 一致）
	results := make([]TokensModelStat, 0, len(buckets))
	for _, b := range buckets {
		results = append(results, TokensModelStat{
			ModelName:    b.modelName,
			Count:        b.count,
			CallCount:    b.count, // v2.0.68: 与 Count 同值，对齐 stage 4 字段
			TokensInput:  b.tokensInput,
			TokensOutput: b.tokensOutput,
			TokensTotal:  b.tokensTotal,
		})
	}
	// 简单冒泡排序（结果集通常较小，<100 行）
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].TokensTotal > results[i].TokensTotal {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	setStatsToCache(cacheKey, results)
	return results, nil
}

// TokensLatencyStat 时延分布统计
type TokensLatencyStat struct {
	RangeLabel  string  `json:"range_label"`
	Count       int64   `json:"count"`
	TokensTotal uint64  `json:"tokens_total"`
	AvgTokens   float64 `json:"avg_tokens"`
	PctOfTotal  float64 `json:"pct_of_total"`
}

// GetTokensLatencyStats 获取时延分布统计（按总耗时分段）
// v2.0.53: 改用 Go 端聚合 — 旧实现 SELECT CASE WHEN elapsed_ms + COUNT/SUM + GROUP BY
//
//	会让 MySQL 走 "Using temporary; Using filesort"，对 7 天 20K 行场景实测 600ms+。
//	新实现只 SELECT 2 个小字段（elapsed_ms + tokens_all_size），
//	符合 v2.0.42 longtext 白名单契约；复合索引 (user_name, model_name, created_at)
//	让 WHERE 走索引范围扫描，Go 端按 6 段时延桶聚合。
//	不再触发 temp table / filesort，性能与首屏 lsmRunInsightsSummary 一致。
func GetTokensLatencyStats(userName, modelName string, subTableNum int, days int) ([]TokensLatencyStat, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days > 365 {
		days = 365
	}

	// 尝试从缓存获取
	cacheKey := makeStatsCacheKey("GetTokensLatencyStats", userName, modelName, subTableNum, days, "")
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if stats, valid := cached.([]TokensLatencyStat); valid {
			return stats, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}

	// 只 SELECT 2 个小字段：elapsed_ms + tokens_all_size。
	// 严格避开 longtext 字段（request_body / response_body 等 8 个），符合 v2.0.42 白名单契约。
	// 不带 GROUP BY / CASE WHEN / SUM —— 全部走 Go 端桶聚合。
	// 复合索引 idx_user_model_created 让 WHERE 走索引范围扫描，rows 大幅减少。
	type rawRow struct {
		ElapsedMs     int64  `gorm:"column:elapsed_ms"`
		TokensAllSize uint64 `gorm:"column:tokens_all_size"`
	}
	var rows []rawRow

	sdb, cancel := database.StatsDB()
	defer cancel()
	query := sdb.Table(tableName).
		Select("elapsed_ms, tokens_all_size").
		Where("user_name = ? AND model_name = ?", userName, modelName)
	if days > 0 {
		query = query.Where("created_at >= DATE_SUB(NOW(), INTERVAL ? DAY)", days)
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to get tokens latency stats rows: %w", err)
	}

	// Go 端按 6 段时延桶聚合
	// 与旧 SQL CASE WHEN 顺序一致：< 1s / 1-3s / 3-5s / 5-10s / 10-30s / > 30s
	type bucket struct {
		label       string
		count       int64
		tokensTotal uint64
	}
	buckets := [6]*bucket{
		{label: "< 1s"},
		{label: "1-3s"},
		{label: "3-5s"},
		{label: "5-10s"},
		{label: "10-30s"},
		{label: "> 30s"},
	}
	idxOfBucket := func(elapsedMs int64) int {
		switch {
		case elapsedMs < 1000:
			return 0
		case elapsedMs < 3000:
			return 1
		case elapsedMs < 5000:
			return 2
		case elapsedMs < 10000:
			return 3
		case elapsedMs < 30000:
			return 4
		default:
			return 5
		}
	}
	for _, r := range rows {
		b := buckets[idxOfBucket(r.ElapsedMs)]
		b.count++
		b.tokensTotal += r.TokensAllSize
	}

	// 转成切片并计算 AvgTokens / PctOfTotal
	var totalCount int64
	for _, b := range buckets {
		totalCount += b.count
	}
	results := make([]TokensLatencyStat, 0, 6)
	for _, b := range buckets {
		if b.count == 0 && b.tokensTotal == 0 {
			// 与旧实现行为一致：0 数据段仍然保留（前端展示所有 6 段）
			results = append(results, TokensLatencyStat{RangeLabel: b.label})
			continue
		}
		avgTokens := float64(0)
		if b.count > 0 {
			avgTokens = float64(b.tokensTotal) / float64(b.count)
		}
		pct := float64(0)
		if totalCount > 0 {
			pct = float64(b.count) / float64(totalCount) * 100
		}
		results = append(results, TokensLatencyStat{
			RangeLabel:  b.label,
			Count:       b.count,
			TokensTotal: b.tokensTotal,
			AvgTokens:   avgTokens,
			PctOfTotal:  pct,
		})
	}

	setStatsToCache(cacheKey, results)
	return results, nil
}
