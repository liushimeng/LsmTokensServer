// v2.0.56: /ChatAnalysisTotalWS 全站统计变体函数
//
// 背景：v2.0.55 WS handler 直接调用 GetTimeRangeStats / GetTokensRangeStats /
// GetProtocolAnalysisStats / GetAgentToolStatsByRange，这 4 个函数都强制带
// `Where("user_name = ? AND model_name = ?")` 过滤 + 按 (user_name, model_name)
// 哈希定位单张分表。在「管理员全站视图」场景传 (\"\", \"\") 会拼出
// `TAgentHttpTransactionDataItem_00` + `WHERE user_name=” AND model_name=”`，
// 必然返回 0 行 —— 这就是 v2.0.55 发布后 KPI / 时间分布 / Tokens / 协议 / Agent
// 全为 0 但本平台模型分布有数据的根因。
//
// 本文件提供 5 个「All」后缀变体函数：
//   - 遍历 8 张 TAgentHttpTransactionDataItem 分表
//   - 不带 user/model WHERE（全站聚合）
//   - SELECT 列表仅小字段（符合 v2.0.42 longtext 白名单契约）
//   - 全部走 database.StatsDB() 25s context（v2.0.54 强制规则）
//   - Go 端跨分表合并聚合
//   - NilDB / ctx 取消安全
package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ============================================================
// v2.0.58: keyset 分页扫描 helper（分布式分页数据库查询）
// ============================================================
//
// 背景：v2.0.56/57 的「All」聚合函数对 8 张分表逐张 Find(&rows) 一次性拉全表
// （生产 160K+ 行），造成内存尖峰 + 单条语句长停顿 + 最坏被 25s ctx 取消返回空。
// 本 helper 把「单张分表扫描」改为按主键 id 的 keyset 分页批量扫描，每批固定
// StatsShardScanBatch 行，回调 fn 增量聚合，内存有界。
//
// 为什么用 keyset(id > lastID) 而非 LIMIT/OFFSET：
//   - 并发插入不重不漏（新行 id 更大，落在后续批次；旧行只读一次）
//   - O(log N) 主键 B-tree seek，而非 OFFSET 的 O(K) 跳过扫描
//   - id 唯一，ORDER BY id ASC 严格递增无需 tiebreaker
//
// 一致性语义：扫描过程中新插入的行会被后续批次纳入（结果为「近似当前」）。
// 对统计看板可接受（无需快照事务）。

// StatsShardScanBatch 单批扫描行数。Plan 校验推荐 5000（内存 ~320KB/批，
// 20K 行/分表约 4 批）；范围 [2000, 10000]。
const StatsShardScanBatch = 5000

// shardScanRow 分页扫描通用行结构。仅小字段（禁含 8 个 longtext，v2.0.42 白名单）。
// 各聚合函数按需读取所需列（未 SELECT 的列保持零值）。
type shardScanRow struct {
	ID               uint64    `gorm:"column:id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	TokensInputSize  uint64    `gorm:"column:tokens_input_size"`
	TokensOutputSize uint64    `gorm:"column:tokens_output_size"`
	TokensAllSize    uint64    `gorm:"column:tokens_all_size"`
	ElapsedMs        int64     `gorm:"column:elapsed_ms"`
	AgentToolName    string    `gorm:"column:agent_tool_name"`
}

// scanShardPaged 对单张分表按主键 id keyset 分页扫描，每批回调 fn 做增量聚合。
//
// selectCols：本次需要读取的列（逗号分隔，必须含 id 供 keyset 游标）。
//   - 禁含 8 个 longtext 字段（v2.0.42 白名单契约）。
//
// days 参数为统一 span 编码（20260826）：0 省略 created_at 过滤；>0 最近 N 天；<0 最近 |N| 小时。
// 过滤统一加 created_at >= cutoff（Go 端计算，避免 SQL 方言差异）。
//
// 终止条件：某批返回行数 < StatsShardScanBatch，或 ctx 取消。
// ctx.Canceled / DeadlineExceeded 直接返回该 err（调用方按现有约定 break 返回部分结果）。
//
// GORM 链复用陷阱：每批必须从 sdb.Table(tableName) 新建链，禁止跨批复用同一 *gorm.DB
// （.Where 累积会污染下一批的 WHERE 条件）。
// sdb 已由 database.StatsDB() 绑定 25s ctx，helper 内不再 WithContext。
func scanShardPaged(sdb *gorm.DB, tableName, selectCols string, days int, fn func(rows []shardScanRow)) error {
	if sdb == nil {
		return nil
	}
	cutoff, filterTime := resolveStatsSpanCutoff(ClampStatsSpan(days))

	var lastID uint64
	for {
		var rows []shardScanRow
		// 每批新建链（避免 .Where 累积污染）
		q := sdb.Table(tableName).Select(selectCols).Where("id > ?", lastID)
		if filterTime {
			q = q.Where("created_at >= ?", cutoff)
		}
		if err := q.Order("id ASC").Limit(StatsShardScanBatch).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			fn(rows)
			lastID = rows[len(rows)-1].ID
		}
		if len(rows) < StatsShardScanBatch {
			// 短批 → 该分表扫描结束
			break
		}
	}
	return nil
}

// ============================================================
// GetTimeRangeStatsAll 全站按小时/天调用次数分布
// ============================================================

// GetTimeRangeStatsAll 全站遍历 8 张分表按 created_at 桶聚合调用次数。
// days<=7 按小时桶（"2006-01-02 15:04"）；days>7 按天桶（"2006-01-02"）。
// days<=0 时按天桶且使用全部历史记录。
func GetTimeRangeStatsAll(subTableNum int, days int) ([]TimeRangeStat, error) {
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	// 20260826：days 参数升级为统一 span 编码（负值=最近 N 小时）
	days = ClampStatsSpan(days)
	spanHours := SpanHours(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return []TimeRangeStat{}, nil
	}

	granularity := "hour"
	if spanHours > TimeStatsMaxDays*24 {
		granularity = "day"
	}
	goFmt := "2006-01-02 15:04"
	if granularity == "day" {
		goFmt = "2006-01-02"
	}

	bucketCounts := make(map[string]int64)
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		// v2.0.58: keyset 分页扫描，避免一次性拉全表 created_at
		err := scanShardPaged(sdb, tableName, "id, created_at", days, func(rows []shardScanRow) {
			for _, r := range rows {
				bucketCounts[r.CreatedAt.Format(goFmt)]++
			}
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to query %s: %w", tableName, err)
		}
	}

	// 补齐空槽位（与原 GetTimeRangeStats 行为一致；span=0 沿用旧默认窗口）
	var stats []TimeRangeStat
	now := time.Now()
	if granularity == "hour" {
		fillHours := spanHours
		if fillHours <= 0 {
			fillHours = 24 // span=0 时按当天 24 小时
		}
		startTime := now.Add(-time.Duration(fillHours) * time.Hour).Truncate(time.Hour)
		for t := startTime; !t.After(now); t = t.Add(time.Hour) {
			key := t.Format(goFmt)
			stats = append(stats, TimeRangeStat{Date: key, Count: bucketCounts[key]})
		}
	} else {
		spanDays := (spanHours + 23) / 24
		if spanDays <= 0 {
			spanDays = 30 // span=0 时按最近 30 天
		}
		for i := spanDays - 1; i >= 0; i-- {
			key := now.AddDate(0, 0, -i).Format(goFmt)
			stats = append(stats, TimeRangeStat{Date: key, Count: bucketCounts[key]})
		}
	}

	return stats, nil
}

// ============================================================
// GetTokensRangeStatsAll 全站 Tokens 概览
// ============================================================

// GetTokensRangeStatsAll 全站遍历 8 张分表按天桶聚合调用次数与 Tokens。
// 与 GetTokensRangeStats 同结构，但不带 user/model WHERE。
func GetTokensRangeStatsAll(subTableNum int, days int) ([]TokensRangeStat, error) {
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	// 20260826：days 参数升级为统一 span 编码（负值=最近 N 小时）
	days = ClampStatsSpan(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return []TokensRangeStat{}, nil
	}

	type bucket struct {
		count     int64
		inTokens  uint64
		outTokens uint64
		allTokens uint64
		elapsedMs int64
	}
	buckets := make(map[string]*bucket)

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		// v2.0.58: keyset 分页扫描，避免一次性拉全表（created_at + 4 列）
		err := scanShardPaged(sdb, tableName,
			"id, created_at, tokens_input_size, tokens_output_size, tokens_all_size, elapsed_ms",
			days, func(rows []shardScanRow) {
				for _, r := range rows {
					key := r.CreatedAt.Format("2006-01-02")
					b, ok := buckets[key]
					if !ok {
						b = &bucket{}
						buckets[key] = b
					}
					b.count++
					b.inTokens += r.TokensInputSize
					b.outTokens += r.TokensOutputSize
					b.allTokens += r.TokensAllSize
					b.elapsedMs += r.ElapsedMs
				}
			})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to query %s: %w", tableName, err)
		}
	}

	// 补齐空槽位（最近 N 天；小时窗口按覆盖天数补槽，span=0 默认 30 天）
	spanDays := (SpanHours(days) + 23) / 24
	if spanDays <= 0 {
		spanDays = 30
	}
	now := time.Now()
	stats := make([]TokensRangeStat, 0, spanDays)
	for i := spanDays - 1; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format("2006-01-02")
		b := buckets[key]
		if b == nil {
			stats = append(stats, TokensRangeStat{Date: key})
			continue
		}
		stats = append(stats, TokensRangeStat{
			Date:         key,
			Count:        b.count,
			TokensInput:  b.inTokens,
			TokensOutput: b.outTokens,
			TokensTotal:  b.allTokens,
			AvgElapsedMs: b.elapsedMs,
		})
	}

	return stats, nil
}

// ============================================================
// GetProtocolAnalysisStatsAll 全站协议分析
// ============================================================

// GetProtocolAnalysisStatsAll 全站遍历 8 张分表聚合协议分析。
// 与原 GetProtocolAnalysisStats 不同：不带 user/model WHERE；
// 每张分表独立取最近 limit 条（总共可能 N*limit 条，跨分表合并后再计算）。
func GetProtocolAnalysisStatsAll(subTableNum int, limit int) (*ProtocolAnalysisStats, error) {
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return &ProtocolAnalysisStats{
			MethodStats: make(map[string]int64), URLPatternStats: make(map[string]int64),
			StatusStats: make(map[string]int64), ModelStats: make(map[string]int64),
		}, nil
	}

	type statsRecord struct {
		RequestMethod         string
		RequestURL            string
		ResponseStatus        string
		ElapsedMs             int64
		RequestContentLength  uint64
		ResponseContentLength uint64
		IsTask                bool
		TaskModel             string
		IsStream              bool
		HasSystemPrompt       bool
		HasToolCall           bool
		MessageCount          int
		UserMessageCount      int
	}

	merged := &ProtocolAnalysisStats{
		MethodStats:     make(map[string]int64),
		URLPatternStats: make(map[string]int64),
		StatusStats:     make(map[string]int64),
		ModelStats:      make(map[string]int64),
		MinElapsedMs:    -1,
		MaxElapsedMs:    -1,
		SampleLimit:     limit,
	}

	var totalReqSize, totalRespSize, totalElapsed, totalCount int64

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var records []statsRecord
		err := sdb.Table(tableName).
			Select("request_method", "request_url", "response_status", "elapsed_ms",
				"request_content_length", "response_content_length",
				"is_task", "task_model", "is_stream", "has_system_prompt",
				"has_tool_call", "message_count", "user_message_count").
			Order("id DESC").
			Limit(limit).
			Find(&records).Error
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to query %s: %w", tableName, err)
		}

		for _, rec := range records {
			merged.MethodStats[rec.RequestMethod]++
			urlPath := rec.RequestURL
			if idx := strings.LastIndex(urlPath, "?"); idx > 0 {
				urlPath = urlPath[:idx]
			}
			merged.URLPatternStats[urlPath]++
			merged.StatusStats[rec.ResponseStatus]++

			totalElapsed += rec.ElapsedMs
			if merged.MinElapsedMs < 0 || rec.ElapsedMs < merged.MinElapsedMs {
				merged.MinElapsedMs = rec.ElapsedMs
			}
			if merged.MaxElapsedMs < 0 || rec.ElapsedMs > merged.MaxElapsedMs {
				merged.MaxElapsedMs = rec.ElapsedMs
			}
			totalReqSize += int64(rec.RequestContentLength)
			totalRespSize += int64(rec.ResponseContentLength)

			if rec.TaskModel != "" {
				merged.ModelStats[rec.TaskModel]++
			}
			if rec.IsStream {
				merged.StreamCount++
			} else {
				merged.NonStreamCount++
			}
			if rec.HasSystemPrompt {
				merged.HasSystemPrompt++
			}
			if rec.HasToolCall {
				merged.HasToolCall++
			}
			if rec.UserMessageCount > 1 {
				merged.MultiTurnCount++
			} else {
				merged.SingleTurnCount++
			}
			totalCount++
		}
	}

	merged.SampleCount = int(totalCount)
	if totalCount > 0 {
		merged.AvgElapsedMs = totalElapsed / totalCount
		merged.AvgReqSize = totalReqSize / totalCount
		merged.AvgRespSize = totalRespSize / totalCount
	} else {
		merged.MinElapsedMs = 0
		merged.MaxElapsedMs = 0
	}

	return merged, nil
}

// ============================================================
// GetAgentToolStatsByRangeAll 全站 Agent 工具统计
// ============================================================

// GetAgentToolStatsByRangeAll 全站遍历 8 张分表按 agent_tool_name 聚合。
// 与原 GetAgentToolStatsByRange 同结构，但不带 user/model WHERE。
// 20260826：days 参数升级为统一 span 编码（负值=最近 N 小时）。
func GetAgentToolStatsByRangeAll(subTableNum int, days int) (*AgentToolStatsResponse, error) {
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	days = ClampStatsSpan(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return &AgentToolStatsResponse{ToolStats: []AgentToolStat{}}, nil
	}

	type bucket struct {
		name      string
		count     int64
		firstSeen time.Time
		lastSeen  time.Time
	}
	buckets := make(map[string]*bucket)

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		// v2.0.59: keyset 分页扫描（与 v2.0.58 其余 All 函数对齐），
		// 避免一次性 Find(&rows) 拉全表 agent_tool_name+created_at。
		// agent_tool_name 过滤在回调内做（Go 端），保持 scanShardPaged 通用签名不变。
		err := scanShardPaged(sdb, tableName, "id, created_at, agent_tool_name", days, func(rows []shardScanRow) {
			for _, r := range rows {
				if r.AgentToolName == "" || r.AgentToolName == "unknown" {
					continue
				}
				b, ok := buckets[r.AgentToolName]
				if !ok {
					b = &bucket{name: r.AgentToolName, firstSeen: r.CreatedAt, lastSeen: r.CreatedAt}
					buckets[r.AgentToolName] = b
				}
				b.count++
				if r.CreatedAt.Before(b.firstSeen) {
					b.firstSeen = r.CreatedAt
				}
				if r.CreatedAt.After(b.lastSeen) {
					b.lastSeen = r.CreatedAt
				}
			}
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to query %s: %w", tableName, err)
		}
	}

	// 计算总数
	var totalCount int64
	for _, b := range buckets {
		totalCount += b.count
	}

	// 转 sorted slice
	stats := make([]AgentToolStat, 0, len(buckets))
	for _, b := range buckets {
		var pct float64
		if totalCount > 0 {
			pct = float64(b.count) / float64(totalCount) * 100.0
		}
		stats = append(stats, AgentToolStat{
			AgentToolName: b.name,
			Count:         b.count,
			FirstSeenAt:   b.firstSeen.Format("2006-01-02 15:04:05"),
			LastSeenAt:    b.lastSeen.Format("2006-01-02 15:04:05"),
			Percentage:    pct,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Count != stats[j].Count {
			return stats[i].Count > stats[j].Count
		}
		return stats[i].AgentToolName < stats[j].AgentToolName
	})

	return &AgentToolStatsResponse{
		TotalAgentCount: totalCount,
		UniqueTools:     len(buckets),
		ToolStats:       stats,
	}, nil
}

// ============================================================
// CountAgentHttpTransactionsAll 全站调用总数
// ============================================================

// CountAgentHttpTransactionsAll 全站遍历 8 张分表返回总调用次数。
// 与原 CountAgentHttpTransactionsByDays 不同：不带 user/model WHERE。
func CountAgentHttpTransactionsAll(subTableNum int, days int) (int64, error) {
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	// 20260826：days 参数升级为统一 span 编码（负值=最近 N 小时）
	days = ClampStatsSpan(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return 0, nil
	}

	var total int64
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var count int64
		query := applyStatsSpanWhere(sdb.Table(tableName), days)
		if err := query.Count(&count).Error; err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return 0, fmt.Errorf("failed to count %s: %w", tableName, err)
		}
		total += count
	}

	return total, nil
}

// ============================================================
// GetAllStatsKPISummary 全站 KPI 轻量聚合
// ============================================================

// GetAllStatsKPISummary 全站遍历 8 张分表做 KPI 轻量聚合。
// 仅 SELECT `COUNT(*)` + `SUM(tokens_all_size)`（不 GROUP BY），跨分表累加。
// v2.0.57 引入：替代 lsmBuildChatStatsKPI 内对 GetModelNameUsageStatsByRange 的依赖，
// 避免 160K 行 × GROUP BY model_name,user_name 的慢查询（GROUP BY 是死穴）。
// 返回 (total_calls, total_tokens, active_days, error)。
//
// active_days：跨分表出现的有数据 created_at 日期数（基于 created_at 去重）；
// 注意当 days=0 时使用全量数据，可能非常大。
func GetAllStatsKPISummary(subTableNum int, days int) (int64, uint64, int, error) {
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	// 20260826：days 参数升级为统一 span 编码（负值=最近 N 小时）
	days = ClampStatsSpan(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return 0, 0, 0, nil
	}

	var totalCalls int64
	var totalTokens uint64
	activeDays := make(map[string]struct{})

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		type aggRow struct {
			Count int64  `gorm:"column:agg_count"`
			Sum   uint64 `gorm:"column:agg_sum"`
		}
		var row aggRow
		query := applyStatsSpanWhere(sdb.Table(tableName).
			Select("COUNT(*) as agg_count, COALESCE(SUM(tokens_all_size), 0) as agg_sum"), days)
		if err := query.Scan(&row).Error; err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return 0, 0, 0, fmt.Errorf("failed to kpi-aggregate %s: %w", tableName, err)
		}
		totalCalls += row.Count
		totalTokens += row.Sum
	}

	// 单独计算 active_days（SQL 端 DISTINCT DATE，每分表 ≤ days 行，走 created_at 单列索引）
	// v2.0.58: 原实现 SELECT created_at 把全表拉进 Go 数去重日期（160K 行），改为 SQL 去重。
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}
		var dateStrs []string
		query := applyStatsSpanWhere(sdb.Table(tableName).Select("DISTINCT DATE(created_at) as d"), days)
		if err := query.Pluck("d", &dateStrs).Error; err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			// active_days 失败不致命；返回已聚合的 KPI 即可
			break
		}
		for _, d := range dateStrs {
			if d != "" {
				activeDays[d] = struct{}{}
			}
		}
	}

	return totalCalls, totalTokens, len(activeDays), nil
}

// ============================================================
// 调用次数和 Token 数趋势（小时级 K 线图）
// ============================================================

// maxHourlyTrendHours 趋势接口的最大小时数（30 天等价小时数），超出强制按天桶。
// 与 maxStatsSpanHours（720）一致——K 线图需要按小时粒度时最大跨度 720，
// 大于 720 强制按天桶（每桶表示一天的总调用次数与 Tokens）。
const maxHourlyTrendHours = 720

// hourlyTrendHourBucketThreshold 7 天内按小时桶，更大跨度按天桶。
// 与 TimeStatsMaxDays=7 对齐，保证 /ModelInfo /AgentInfo 趋势模块的「小时级精度」
// 语义与 /ChatAnalysisTotal 一致。
const hourlyTrendHourBucketThreshold = 168 // 7 * 24

// HourlyTrendPoint 单时间桶内的调用次数与 Tokens 汇总。
// 桶格式："YYYY-MM-DD HH:00"（小时桶）或 "YYYY-MM-DD"（天桶）。
// 与 DailyStat 共用 JSON 字段名以降低前端解析分支。
type HourlyTrendPoint struct {
	Date         string `json:"date"`
	Count        int64  `json:"count"`
	TokensInput  uint64 `json:"tokens_input"`
	TokensOutput uint64 `json:"tokens_output"`
	TokensTotal  uint64 `json:"tokens_total"`
}

// HourlyTrendResult 趋势响应包装（含粒度 + 时间窗元数据）。
// Granularity 取值 "hour" 或 "day"，Hours 为规范化后的请求窗口（1~720）。
// From/To 为 ISO 时间，前端用于坐标轴对齐与工具提示。
type HourlyTrendResult struct {
	Points      []HourlyTrendPoint `json:"points"`
	Granularity string             `json:"granularity"` // "hour" | "day"
	Hours       int                `json:"hours"`
	From        string             `json:"from"`
	To          string             `json:"to"`
}

// hourlyTrendBucket 单桶聚合计数器（包级以便复用：GetHourlyTrendAll 与
// GetHourlyTrendByUser 共享同一内存布局；buildHourlyTrendPoints 通过 map value 接收）。
type hourlyTrendBucket struct {
	count     int64
	inTokens  uint64
	outTokens uint64
	allTokens uint64
}

// normalizeHourlyTrendHours 规范化 hours：<=0 视为 24；>720 截断到 720。
// 返回规范化值。
func normalizeHourlyTrendHours(hours int) int {
	if hours <= 0 {
		return 24
	}
	if hours > maxHourlyTrendHours {
		return maxHourlyTrendHours
	}
	return hours
}

// GetHourlyTrendAll 全站维度按小时/天桶聚合调用次数与 Tokens，用于 ModelInfo / AgentInfo
// 趋势模块的 K 线图。
//
// 参数 hours 语义：
//   - hours<=0 → 视为 24（最近一天）
//   - 1~168（7 天内） → granularity="hour"，桶格式 "YYYY-MM-DD HH:00"
//   - 169~720（>7 天） → granularity="day"，桶格式 "YYYY-MM-DD"
//
// 复用 scanShardPaged keyset 分页 + database.StatsDB() 25s context：
//   - 每批 5000 行 + created_at >= cutoff 预过滤，避免全表扫描
//   - ctx 超时驱动向 MySQL 发 KILL 并归还连接
//
// 补齐空桶：从 (now - span) 到 now 逐桶 append，缺数据填零值 HourlyTrendPoint。
// 调用方：管理员 /ModelInfoInterface 与 /AgentInfoInterface 的 action="trend"。
func GetHourlyTrendAll(subTableNum int, hours int) (*HourlyTrendResult, error) {
	if database.DB == nil {
		// DB 未就绪：返回零值桶（与 GetTimeRangeStatsAll 的行为一致）
		hours = normalizeHourlyTrendHours(hours)
		granularity := "hour"
		if hours > hourlyTrendHourBucketThreshold {
			granularity = "day"
		}
		return buildEmptyHourlyTrend(hours, granularity), nil
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	hours = normalizeHourlyTrendHours(hours)

	granularity := "hour"
	if hours > hourlyTrendHourBucketThreshold {
		granularity = "day"
	}
	goFmt := "2006-01-02 15:04"
	if granularity == "day" {
		goFmt = "2006-01-02"
	}

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		// sdb 不可用：返回零值桶
		return buildEmptyHourlyTrend(hours, granularity), nil
	}

	buckets := make(map[string]*hourlyTrendBucket)

	// scanShardPaged 的 cutoff 用「天」为单位；小时窗口用同等跨度的天数兜底。
	// hours<=168 → cutoff = now - hours/24 + 1 天（保守向上取整，保证不漏最近一小时）
	// hours>168  → cutoff = now - hours/24 天
	daysForFilter := hours / 24
	if hours%24 != 0 {
		daysForFilter++ // 向上取整
	}
	if daysForFilter < 1 {
		daysForFilter = 1
	}

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		err := scanShardPaged(sdb, tableName,
			"id, created_at, tokens_input_size, tokens_output_size, tokens_all_size",
			daysForFilter, func(rows []shardScanRow) {
				for _, r := range rows {
					key := r.CreatedAt.Format(goFmt)
					b, ok := buckets[key]
					if !ok {
						b = &hourlyTrendBucket{}
						buckets[key] = b
					}
					b.count++
					b.inTokens += r.TokensInputSize
					b.outTokens += r.TokensOutputSize
					b.allTokens += r.TokensAllSize
				}
			})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to query %s: %w", tableName, err)
		}
	}

	points := buildHourlyTrendPoints(buckets, hours, granularity)
	now := time.Now()
	from := now.Add(-time.Duration(hours) * time.Hour)
	return &HourlyTrendResult{
		Points:      points,
		Granularity: granularity,
		Hours:       hours,
		From:        from.Format(time.RFC3339),
		To:          now.Format(time.RFC3339),
	}, nil
}

// buildHourlyTrendPoints 把 buckets map 按时间窗展平成连续桶序列，缺数据填零。
// span 单位（小时桶 = hours 个，天桶 = ceil(hours/24) 个）。
func buildHourlyTrendPoints(buckets map[string]*hourlyTrendBucket, hours int, granularity string) []HourlyTrendPoint {
	now := time.Now()
	points := make([]HourlyTrendPoint, 0)

	if granularity == "hour" {
		startTime := now.Add(-time.Duration(hours-1) * time.Hour).Truncate(time.Hour)
		goFmt := "2006-01-02 15:04"
		for t := startTime; !t.After(now); t = t.Add(time.Hour) {
			key := t.Format(goFmt)
			if b, ok := buckets[key]; ok {
				points = append(points, HourlyTrendPoint{
					Date:        key,
					Count:       b.count,
					TokensInput: b.inTokens,
					TokensOutput: b.outTokens,
					TokensTotal: b.allTokens,
				})
			} else {
				points = append(points, HourlyTrendPoint{Date: key})
			}
		}
	} else {
		spanDays := hours / 24
		if hours%24 != 0 {
			spanDays++ // 向上取整
		}
		if spanDays < 1 {
			spanDays = 1
		}
		goFmt := "2006-01-02"
		for i := spanDays - 1; i >= 0; i-- {
			key := now.AddDate(0, 0, -i).Format(goFmt)
			if b, ok := buckets[key]; ok {
				points = append(points, HourlyTrendPoint{
					Date:        key,
					Count:       b.count,
					TokensInput: b.inTokens,
					TokensOutput: b.outTokens,
					TokensTotal: b.allTokens,
				})
			} else {
				points = append(points, HourlyTrendPoint{Date: key})
			}
		}
	}
	return points
}

// buildEmptyHourlyTrend DB 未就绪时返回连续零值桶（保持前端渲染坐标轴对齐）。
func buildEmptyHourlyTrend(hours int, granularity string) *HourlyTrendResult {
	points := buildHourlyTrendPoints(make(map[string]*hourlyTrendBucket), hours, granularity)
	now := time.Now()
	from := now.Add(-time.Duration(hours) * time.Hour)
	return &HourlyTrendResult{
		Points:      points,
		Granularity: granularity,
		Hours:       hours,
		From:        from.Format(time.RFC3339),
		To:          now.Format(time.RFC3339),
	}
}
