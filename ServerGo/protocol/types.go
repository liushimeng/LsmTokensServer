// Package protocol - 协议转换与公共协议类型定义
package protocol

// Agent 协议类型常量（原 mysql_http_agent_model.go 定义，提取为公共叶子包避免循环依赖）
const (
	AgentProtocolType_Anthropic = 1
	AgentProtocolType_OpenAI    = 2
)
