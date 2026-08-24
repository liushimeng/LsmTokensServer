package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"sort"
	"strings"
)

// AgentInfoUsageStat Agent 信息页使用统计明细（按 AgentToolName 维度）
type AgentInfoUsageStat struct {
	AgentToolName    string  `json:"agent_tool_name"`
	CallCount        int64   `json:"call_count"`
	TokensAllSize    uint64  `json:"tokens_all_size"`
	TokensInputSize  uint64  `json:"tokens_input_size"`
	TokensOutputSize uint64  `json:"tokens_output_size"`
	CallShare        float64 `json:"call_share"`
	TokenShare       float64 `json:"token_share"`
	UserCount        int64   `json:"user_count,omitempty"`
}

// AgentInfoUsageSummary Agent 信息页使用统计汇总
type AgentInfoUsageSummary struct {
	AgentCount       int    `json:"agent_count"`
	TotalCallCount   int64  `json:"total_call_count"`
	TokensAllSize    uint64 `json:"tokens_all_size"`
	TokensInputSize  uint64 `json:"tokens_input_size"`
	TokensOutputSize uint64 `json:"tokens_output_size"`
}

type agentInfoUsageAccumulator struct {
	AgentInfoUsageStat
	users map[string]struct{}
}

// normalizeAgentToolName 归一化 Agent 名称，空值统一显示为 unknown，便于聚合不丢数据。
func normalizeAgentToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}
	return name
}

func finalizeAgentInfoUsageStats(acc map[string]*agentInfoUsageAccumulator) (*AgentInfoUsageSummary, []AgentInfoUsageStat) {
	summary := &AgentInfoUsageSummary{}
	stats := make([]AgentInfoUsageStat, 0, len(acc))
	for _, item := range acc {
		if item.AgentToolName == "" {
			continue
		}
		item.UserCount = int64(len(item.users))
		summary.TotalCallCount += item.CallCount
		summary.TokensAllSize += item.TokensAllSize
		summary.TokensInputSize += item.TokensInputSize
		summary.TokensOutputSize += item.TokensOutputSize
	}

	for _, item := range acc {
		if item.AgentToolName == "" {
			continue
		}
		stat := item.AgentInfoUsageStat
		if summary.TotalCallCount > 0 {
			stat.CallShare = float64(stat.CallCount) * 100 / float64(summary.TotalCallCount)
		}
		if summary.TokensAllSize > 0 {
			stat.TokenShare = float64(stat.TokensAllSize) * 100 / float64(summary.TokensAllSize)
		}
		stats = append(stats, stat)
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].CallCount != stats[j].CallCount {
			return stats[i].CallCount > stats[j].CallCount
		}
		if stats[i].TokensAllSize != stats[j].TokensAllSize {
			return stats[i].TokensAllSize > stats[j].TokensAllSize
		}
		return stats[i].AgentToolName < stats[j].AgentToolName
	})
	summary.AgentCount = len(stats)
	return summary, stats
}

// GetAgentInfoUsageStatsAll 管理员 Agent 信息页统计：扫描所有分表，按 agent_tool_name 聚合
// 调用次数和 Tokens（全站维度）。数据源为交易分表 agent_tool_name 列，确保不丢失
// TAgentHttpAgentInfo 未记录（如 unknown）或未含 Tokens 的统计信息。
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（最大 365）。
func GetAgentInfoUsageStatsAll(subTableNum int, days int) (*AgentInfoUsageSummary, []AgentInfoUsageStat, error) {
	if database.DB == nil {
		return nil, nil, fmt.Errorf("database not initialized")
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsDays(days)

	acc := make(map[string]*agentInfoUsageAccumulator)
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var rows []struct {
			AgentToolName    string `gorm:"column:agent_tool_name"`
			UserName         string `gorm:"column:user_name"`
			CallCount        int64  `gorm:"column:call_count"`
			TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
			TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
			TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		}

		err := applyStatsDaysWhere(database.DB.Table(tableName), days).
			Select("agent_tool_name, user_name, COUNT(*) as call_count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
			Group("agent_tool_name, user_name").
			Scan(&rows).Error
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get agent info usage stats from %s: %w", tableName, err)
		}

		for _, row := range rows {
			agentToolName := normalizeAgentToolName(row.AgentToolName)
			item := acc[agentToolName]
			if item == nil {
				item = &agentInfoUsageAccumulator{
					AgentInfoUsageStat: AgentInfoUsageStat{AgentToolName: agentToolName},
					users:              make(map[string]struct{}),
				}
				acc[agentToolName] = item
			}
			item.CallCount += row.CallCount
			item.TokensAllSize += row.TokensAllSize
			item.TokensInputSize += row.TokensInputSize
			item.TokensOutputSize += row.TokensOutputSize
			if row.UserName != "" {
				item.users[row.UserName] = struct{}{}
			}
		}
	}

	summary, stats := finalizeAgentInfoUsageStats(acc)
	return summary, stats, nil
}

// GetAgentInfoUsageStatsByUser 用户 Agent 信息页统计：仅统计当前用户的平台模型分表，
// 按 agent_tool_name 聚合调用次数和 Tokens（用户维度）。
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（最大 365）。
func GetAgentInfoUsageStatsByUser(userName string, modelNames []string, subTableNum int, days int) (*AgentInfoUsageSummary, []AgentInfoUsageStat, error) {
	if database.DB == nil {
		return nil, nil, fmt.Errorf("database not initialized")
	}
	userName = strings.TrimSpace(userName)
	if userName == "" {
		return nil, nil, fmt.Errorf("user_name is required")
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsDays(days)

	// 一个用户的多个模型可能落在同一张分表，需对 (表名) 去重，避免重复累加。
	seenTable := make(map[string]struct{})
	acc := make(map[string]*agentInfoUsageAccumulator)
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}

		tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
		if _, ok := seenTable[tableName]; ok {
			continue
		}
		seenTable[tableName] = struct{}{}
		if !IsTableExists(tableName) {
			continue
		}

		var rows []struct {
			AgentToolName    string `gorm:"column:agent_tool_name"`
			CallCount        int64  `gorm:"column:call_count"`
			TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
			TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
			TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		}
		err := applyStatsDaysWhere(database.DB.Table(tableName), days).
			Select("agent_tool_name, COUNT(*) as call_count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
			Where("user_name = ?", userName).
			Group("agent_tool_name").
			Scan(&rows).Error
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get agent info usage stats for %s/%s: %w", userName, modelName, err)
		}

		for _, row := range rows {
			agentToolName := normalizeAgentToolName(row.AgentToolName)
			item := acc[agentToolName]
			if item == nil {
				item = &agentInfoUsageAccumulator{
					AgentInfoUsageStat: AgentInfoUsageStat{AgentToolName: agentToolName},
					users:              make(map[string]struct{}),
				}
				acc[agentToolName] = item
			}
			item.CallCount += row.CallCount
			item.TokensAllSize += row.TokensAllSize
			item.TokensInputSize += row.TokensInputSize
			item.TokensOutputSize += row.TokensOutputSize
			item.users[userName] = struct{}{}
		}
	}

	summary, stats := finalizeAgentInfoUsageStats(acc)
	return summary, stats, nil
}
