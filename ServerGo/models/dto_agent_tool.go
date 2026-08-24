package models

// AgentToolStat 单个 Agent 工具的统计数据
// （原定义于旧工程 server_api_manager_chat_total.go，因 models 层统计函数返回该结构，提前迁入本包；
//
//	阶段3 迁移 API 层时直接复用，勿重复定义）
type AgentToolStat struct {
	AgentToolName string  `json:"agent_tool_name"`
	Count         int64   `json:"count"`
	FirstSeenAt   string  `json:"first_seen_at"`
	LastSeenAt    string  `json:"last_seen_at"`
	Percentage    float64 `json:"percentage"`
}

// AgentToolStatsResponse Agent 工具统计响应
type AgentToolStatsResponse struct {
	TotalAgentCount int64           `json:"total_agent_count"`
	UniqueTools     int             `json:"unique_tools"`
	ToolStats       []AgentToolStat `json:"tool_stats"`
}
