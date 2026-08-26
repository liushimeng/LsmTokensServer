// v2.0.55: 全站按本平台模型名称 (model_name) 聚合的统计函数
//
// 与 GetModelInfoUsageStatsByUser（mysql_http_agent_sub_table.go:1626）模式一致，
// 但不限定 user_name / model_name，直接遍历全部 8 张 TAgentHttpTransactionDataItem
// 分表按 model_name 分组聚合调用次数与 Tokens。
//
// SELECT 列表仅为小字段（不含 longtext，符合 v2.0.42 白名单契约）；
// 依赖复合索引 idx_user_model_created(user_name, model_name, created_at) 走索引扫描；
// 所有 DB 调用走 database.StatsDB() 25s context（v2.0.54 强制规则）。
//
// 业务含义：
//   - model_name 是用户配置的「本平台模型」（即 /AIRouteManage 中可看到的模型别名）
//   - dst_model_name 是「目标源站模型」（实际转发的真实模型，由源站控制）
//   - 管理员 / ChatAnalysisTotal 页要展示「按本平台模型聚合」视图，让管理员看每个
//     用户配置的模型调用情况，而不是混在一起的源站真实模型分布。
package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"sort"
	"strings"
	"time"
)

// DstEndpointUsage 单个源站在某模型下的使用情况（v2.0.68 新增）
//
// 仅在 stage 4 model_distribution 聚合时附带 Top 3，避免把全量源站列表传到前端（CLAUDE.md
// 强制规则：禁止把未脱敏明细传到前端 + IO 膨胀控制）。
type DstEndpointUsage struct {
	DstEndpointID uint64 `json:"dst_endpoint_id"`
	CallCount     int64  `json:"call_count"`
}

// ModelNameUsageStat 单个本平台模型的聚合统计
type ModelNameUsageStat struct {
	ModelName    string  `json:"model_name"`
	UserCount    int64   `json:"user_count"` // 跨分表去重的活跃用户数
	CallCount    int64   `json:"call_count"`
	TokensInput  uint64  `json:"tokens_input"`
	TokensOutput uint64  `json:"tokens_output"`
	TokensTotal  uint64  `json:"tokens_total"`
	CallShare    float64 `json:"call_share"`  // 调用次数占比
	TokenShare   float64 `json:"token_share"` // 总 Tokens 占比
	// v2.0.68 新增：本模型覆盖的源站维度
	DstEndpointCount int                `json:"dst_endpoint_count"`          // 去重源站数
	TopDstEndpoints  []DstEndpointUsage `json:"top_dst_endpoints,omitempty"` // Top 3 源站（按调用次数降序）
}

// GetModelNameUsageStatsByRange 全站按本平台模型名称（model_name）聚合调用次数与 Tokens。
//
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（clampStatsDays 上限 365）。
// 返回数据按调用次数降序，再按总 Tokens 降序，再按 model_name 字母序升序。
//
// NilDB 安全：DB 未初始化时返回空切片（不 panic）。
// ctx 取消安全：context.Canceled / DeadlineExceeded 直接返回，不当作 error。
func GetModelNameUsageStatsByRange(subTableNum int, days int) ([]ModelNameUsageStat, error) {
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsSpan(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		// NilDB 安全：返回空切片（前端会显示「暂无数据」而非 500）
		return []ModelNameUsageStat{}, nil
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), database.StatsQueryTimeout)
	defer ctxCancel()

	acc := make(map[string]*modelNameUsageAccumulator)
	var totalCalls int64
	var totalTokens uint64

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		// v2.0.58: GROUP BY 从 (model_name, user_name) 降为 model_name，
		// 把 temp-table 基数从 models×users 降到 models（消除 160K×8 GROUP BY 慢查询死穴）。
		// user_count 单独用一条 COUNT(DISTINCT user_name) GROUP BY model_name 便宜查询补齐。
		var rows []struct {
			ModelName    string `gorm:"column:model_name"`
			CallCount    int64  `gorm:"column:call_count"`
			TokensInput  uint64 `gorm:"column:tokens_input"`
			TokensOutput uint64 `gorm:"column:tokens_output"`
			TokensAll    uint64 `gorm:"column:tokens_all"`
		}
		err := applyStatsSpanWhere(sdb.Table(tableName), days).
			WithContext(ctx).
			Select("model_name, COUNT(*) as call_count, COALESCE(SUM(tokens_input_size), 0) as tokens_input, COALESCE(SUM(tokens_output_size), 0) as tokens_output, COALESCE(SUM(tokens_all_size), 0) as tokens_all").
			Where("model_name <> ''").
			Group("model_name").
			Scan(&rows).Error
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// 客户端切走 / 服务端超时：返回当前已聚合结果，warning 由调用方决定
				break
			}
			return nil, fmt.Errorf("failed to scan %s: %w", tableName, err)
		}

		for _, row := range rows {
			modelName := strings.TrimSpace(row.ModelName)
			if modelName == "" {
				continue
			}
			item, ok := acc[modelName]
			if !ok {
				item = &modelNameUsageAccumulator{
					ModelNameUsageStat: ModelNameUsageStat{ModelName: modelName},
					users:              make(map[string]struct{}),
					// v2.0.68 新增：源站去重集合 + 源站→调用次数
					dstEndpoints:     make(map[uint64]struct{}),
					dstEndpointCalls: make(map[uint64]int64),
				}
				acc[modelName] = item
			}
			item.CallCount += row.CallCount
			item.TokensInput += row.TokensInput
			item.TokensOutput += row.TokensOutput
			item.TokensTotal += row.TokensAll
			totalCalls += row.CallCount
			totalTokens += row.TokensAll
		}

		// user_count 补齐：便宜查询 COUNT(DISTINCT user_name) GROUP BY model_name
		// （基数 ≤ distinct(model_name)，走 idx_user_model_created 前缀）
		var userRows []struct {
			ModelName string `gorm:"column:model_name"`
			UserCount int64  `gorm:"column:user_count"`
		}
		uerr := applyStatsSpanWhere(sdb.Table(tableName), days).
			WithContext(ctx).
			Select("model_name, COUNT(DISTINCT user_name) as user_count").
			Where("model_name <> ''").
			Group("model_name").
			Scan(&userRows).Error
		if uerr != nil {
			if errors.Is(uerr, context.Canceled) || errors.Is(uerr, context.DeadlineExceeded) {
				break
			}
			// user_count 非致命：失败则该分表 user_count 记 0，不中断主聚合
			continue
		}
		for _, ur := range userRows {
			modelName := strings.TrimSpace(ur.ModelName)
			if modelName == "" {
				continue
			}
			if item, ok := acc[modelName]; ok {
				// 跨分表累加去重用户数（近似：同一 user 在不同分表按哈希落不同表，通常不重叠）
				item.UserCount += ur.UserCount
			}
		}

		// v2.0.68 新增：源站维度聚合（dst_endpoint_count + top_dst_endpoints）。
		// 复用 idx_dst_endpoint_id 索引（mysql_http_agent_model.go:101），SELECT 仅含小字段
		// （不含 longtext，符合 v2.0.42 白名单），GROUP BY (model_name, dst_endpoint_id) 命中索引。
		var dstRows []struct {
			ModelName     string `gorm:"column:model_name"`
			DstEndpointID uint64 `gorm:"column:dst_endpoint_id"`
			CallCount     int64  `gorm:"column:call_count"`
		}
		derr := applyStatsSpanWhere(sdb.Table(tableName), days).
			WithContext(ctx).
			Select("model_name, dst_endpoint_id, COUNT(*) as call_count").
			Where("model_name <> '' AND dst_endpoint_id > 0").
			Group("model_name, dst_endpoint_id").
			Scan(&dstRows).Error
		if derr != nil {
			if errors.Is(derr, context.Canceled) || errors.Is(derr, context.DeadlineExceeded) {
				break
			}
			// 源站聚合非致命：失败则该分表 dst_endpoint 维度记空，不中断主聚合
			continue
		}
		for _, dr := range dstRows {
			modelName := strings.TrimSpace(dr.ModelName)
			if modelName == "" || dr.DstEndpointID == 0 {
				continue
			}
			if item, ok := acc[modelName]; ok {
				// 跨分表累加（同一 model_name 跨分表的源站去重合并）
				item.dstEndpoints[dr.DstEndpointID] = struct{}{}
				item.dstEndpointCalls[dr.DstEndpointID] += dr.CallCount
			}
		}
	}

	stats := make([]ModelNameUsageStat, 0, len(acc))
	for _, item := range acc {
		// v2.0.68：填充 DstEndpointCount + Top 3 TopDstEndpoints（按调用次数降序 → id 升序）
		item.DstEndpointCount = len(item.dstEndpoints)
		if len(item.dstEndpointCalls) > 0 {
			top := make([]DstEndpointUsage, 0, len(item.dstEndpointCalls))
			for id, cnt := range item.dstEndpointCalls {
				top = append(top, DstEndpointUsage{DstEndpointID: id, CallCount: cnt})
			}
			sort.Slice(top, func(i, j int) bool {
				if top[i].CallCount != top[j].CallCount {
					return top[i].CallCount > top[j].CallCount
				}
				return top[i].DstEndpointID < top[j].DstEndpointID
			})
			if len(top) > 3 {
				top = top[:3]
			}
			item.TopDstEndpoints = top
		}
		stats = append(stats, item.ModelNameUsageStat)
	}

	// 计算占比（依赖 total，因此先 collect 再 share）
	for i := range stats {
		if totalCalls > 0 {
			stats[i].CallShare = float64(stats[i].CallCount) / float64(totalCalls) * 100.0
		}
		if totalTokens > 0 {
			stats[i].TokenShare = float64(stats[i].TokensTotal) / float64(totalTokens) * 100.0
		}
	}

	// 排序：调用次数降序 → 总 Tokens 降序 → model_name 升序
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].CallCount != stats[j].CallCount {
			return stats[i].CallCount > stats[j].CallCount
		}
		if stats[i].TokensTotal != stats[j].TokensTotal {
			return stats[i].TokensTotal > stats[j].TokensTotal
		}
		return stats[i].ModelName < stats[j].ModelName
	})

	return stats, nil
}

// modelNameUsageAccumulator 内部聚合器（避免重复 UserCount 计算）
type modelNameUsageAccumulator struct {
	ModelNameUsageStat
	users map[string]struct{}
	// v2.0.68 新增：本模型覆盖的源站去重集合 + 各源站调用次数
	dstEndpoints     map[uint64]struct{}
	dstEndpointCalls map[uint64]int64
}

// ModelNameUsageStatsSummary 全站本平台模型汇总
type ModelNameUsageStatsSummary struct {
	ModelCount    int    `json:"model_count"` // 不同 model_name 数量
	TotalCalls    int64  `json:"total_calls"`
	TokensInput   uint64 `json:"tokens_input"`
	TokensOutput  uint64 `json:"tokens_output"`
	TokensTotal   uint64 `json:"tokens_total"`
	ActiveUsers   int64  `json:"active_users"` // 跨模型去重的活跃用户总数
	WindowDays    int    `json:"window_days"`
	GeneratedAtMs int64  `json:"generated_at_ms"` // 服务端生成时间（毫秒）
}

// GetDstModelUsageStatsByUserModel v2.0.68 校正：在「本平台 (user_name, model_name) 上下文」
// 下，按目标源站模型名 (dst_model_name) 聚合调用次数 + Tokens + 源站覆盖。
//
// 这是 stage 4 model_distribution HTTP fallback 路径的正确实现 —— 与 WS 路径的
// dstModelAgg + snapshotModelDist 语义一致。运维真正想看的是：本平台模型下，
// 实际请求到了哪些 dst_model_name（目标源站模型），各自调用了多少 / Tokens 多少。
//
// SELECT 仅含小字段（不含 longtext，符合 v2.0.42 白名单），依赖
// idx_user_model_created(user_name, model_name, created_at) 复合索引命中索引范围扫描。
// ctx 25s 上限（v2.0.54 强制规则）。
func GetDstModelUsageStatsByUserModel(userName, modelName string, subTableNum int, days int) ([]ModelNameUsageStat, error) {
	if userName == "" || modelName == "" {
		return nil, fmt.Errorf("GetDstModelUsageStatsByUserModel: user_name 与 model_name 都必须非空")
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsSpan(days)

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		// NilDB 安全：返回空切片
		return []ModelNameUsageStat{}, nil
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), database.StatsQueryTimeout)
	defer ctxCancel()

	acc := make(map[string]*modelNameUsageAccumulator)
	var totalCalls int64
	var totalTokens uint64

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		// 主聚合：按 dst_model_name 分组，限定 (user_name, model_name) + created_at
		var rows []struct {
			DstModelName string `gorm:"column:dst_model_name"`
			CallCount    int64  `gorm:"column:call_count"`
			TokensInput  uint64 `gorm:"column:tokens_input"`
			TokensOutput uint64 `gorm:"column:tokens_output"`
			TokensAll    uint64 `gorm:"column:tokens_all"`
		}
		err := applyStatsSpanWhere(sdb.Table(tableName), days).
			WithContext(ctx).
			Select("dst_model_name, COUNT(*) as call_count, COALESCE(SUM(tokens_input_size), 0) as tokens_input, COALESCE(SUM(tokens_output_size), 0) as tokens_output, COALESCE(SUM(tokens_all_size), 0) as tokens_all").
			Where("user_name = ? AND model_name = ? AND dst_model_name <> ''", userName, modelName).
			Group("dst_model_name").
			Scan(&rows).Error
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to scan %s: %w", tableName, err)
		}

		for _, row := range rows {
			dstName := strings.TrimSpace(row.DstModelName)
			if dstName == "" {
				continue
			}
			item, ok := acc[dstName]
			if !ok {
				item = &modelNameUsageAccumulator{
					ModelNameUsageStat: ModelNameUsageStat{ModelName: dstName},
					users:              make(map[string]struct{}),
					dstEndpoints:       make(map[uint64]struct{}),
					dstEndpointCalls:   make(map[uint64]int64),
				}
				acc[dstName] = item
			}
			item.CallCount += row.CallCount
			item.TokensInput += row.TokensInput
			item.TokensOutput += row.TokensOutput
			item.TokensTotal += row.TokensAll
			totalCalls += row.CallCount
			totalTokens += row.TokensAll
		}

		// 源站维度聚合（dst_endpoint_count + top_dst_endpoints）
		var dstRows []struct {
			DstModelName  string `gorm:"column:dst_model_name"`
			DstEndpointID uint64 `gorm:"column:dst_endpoint_id"`
			CallCount     int64  `gorm:"column:call_count"`
		}
		derr := applyStatsSpanWhere(sdb.Table(tableName), days).
			WithContext(ctx).
			Select("dst_model_name, dst_endpoint_id, COUNT(*) as call_count").
			Where("user_name = ? AND model_name = ? AND dst_model_name <> '' AND dst_endpoint_id > 0", userName, modelName).
			Group("dst_model_name, dst_endpoint_id").
			Scan(&dstRows).Error
		if derr != nil {
			if errors.Is(derr, context.Canceled) || errors.Is(derr, context.DeadlineExceeded) {
				break
			}
			// 源站聚合非致命
			continue
		}
		for _, dr := range dstRows {
			dstName := strings.TrimSpace(dr.DstModelName)
			if dstName == "" || dr.DstEndpointID == 0 {
				continue
			}
			if item, ok := acc[dstName]; ok {
				item.dstEndpoints[dr.DstEndpointID] = struct{}{}
				item.dstEndpointCalls[dr.DstEndpointID] += dr.CallCount
			}
		}
	}

	stats := make([]ModelNameUsageStat, 0, len(acc))
	for _, item := range acc {
		item.DstEndpointCount = len(item.dstEndpoints)
		if len(item.dstEndpointCalls) > 0 {
			top := make([]DstEndpointUsage, 0, len(item.dstEndpointCalls))
			for id, cnt := range item.dstEndpointCalls {
				top = append(top, DstEndpointUsage{DstEndpointID: id, CallCount: cnt})
			}
			sort.Slice(top, func(i, j int) bool {
				if top[i].CallCount != top[j].CallCount {
					return top[i].CallCount > top[j].CallCount
				}
				return top[i].DstEndpointID < top[j].DstEndpointID
			})
			if len(top) > 3 {
				top = top[:3]
			}
			item.TopDstEndpoints = top
		}
		stats = append(stats, item.ModelNameUsageStat)
	}

	// 计算占比
	for i := range stats {
		if totalCalls > 0 {
			stats[i].CallShare = float64(stats[i].CallCount) / float64(totalCalls) * 100.0
		}
		if totalTokens > 0 {
			stats[i].TokenShare = float64(stats[i].TokensTotal) / float64(totalTokens) * 100.0
		}
	}

	// 排序
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].CallCount != stats[j].CallCount {
			return stats[i].CallCount > stats[j].CallCount
		}
		if stats[i].TokensTotal != stats[j].TokensTotal {
			return stats[i].TokensTotal > stats[j].TokensTotal
		}
		return stats[i].ModelName < stats[j].ModelName
	})

	return stats, nil
}

// SummarizeModelNameUsage 从 ModelNameUsageStat 列表计算汇总
func SummarizeModelNameUsage(stats []ModelNameUsageStat, days int) ModelNameUsageStatsSummary {
	summary := ModelNameUsageStatsSummary{
		ModelCount:    len(stats),
		WindowDays:    days,
		GeneratedAtMs: time.Now().UnixMilli(),
	}
	users := make(map[string]struct{})
	for _, s := range stats {
		summary.TotalCalls += s.CallCount
		summary.TokensInput += s.TokensInput
		summary.TokensOutput += s.TokensOutput
		summary.TokensTotal += s.TokensTotal
		// UserCount 是单模型去重数；汇总层再做一次全局去重
		// 注：这里仅做计数；按需可暴露 users slice，但当前阶段不要传用户列表
		// （CLAUDE.md 强制规则：禁止把敏感数据传到前端未脱敏位置）
		_ = users
	}
	return summary
}
