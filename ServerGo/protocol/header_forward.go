package protocol

import (
	"strings"
)

// shouldForwardProxyHeader 判定请求头是否应转发（自旧工程 proxy security 提取）
func ShouldForwardProxyHeader(key string, protocolType int) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "host", "cookie",
		"authorization", "x-api-key", "x-forwarded-for", "x-forwarded-host",
		"x-forwarded-proto", "x-real-ip", "content-length":
		return false
	case "content-type", "accept", "user-agent", "x-request-id",
		"anthropic-version", "anthropic-beta", "openai-beta":
		return true
	}
	return strings.HasPrefix(strings.ToLower(key), "x-stainless-") || protocolType == AgentProtocolType_OpenAI && strings.HasPrefix(strings.ToLower(key), "openai-")
}
