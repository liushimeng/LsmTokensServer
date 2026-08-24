// Package protocol - 协议类型公共定义（阶段3 迁入完整协议转换实现）
package protocol

// Agent 协议类型常量（原 mysql_http_agent_model.go 定义，提取为公共叶子包避免循环依赖）
const (
	AgentProtocolType_Anthropic = 1
	AgentProtocolType_OpenAI    = 2
)
