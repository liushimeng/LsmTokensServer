package protocol

import (
	"strings"
)

// ShouldForwardProxyHeader 判定请求头是否应转发到上游源站（自旧工程 proxy security 提取）
//
// 客户端协议（protocolType）感知：
//   - Anthropic 客户端：丢弃 OpenAI 私有头（OpenAI-Organization / OpenAI-Project 等）
//   - OpenAI 客户端：丢弃 Anthropic 私有头（Anthropic-Version / Anthropic-Beta 等）
//   - 双向均丢弃：hop-by-hop 头、代理转发链、Cookie 与客户端密钥头
//
// 这样可以避免协议转换时上游响应头里混入对方协议私有头。
func ShouldForwardProxyHeader(key string, protocolType int) bool {
	lower := strings.ToLower(key)
	// 协议感知必须在通用判定之前——否则 anthropic-version / openai-beta 这类
	// 在通用白名单中的头会被无条件转发，丢掉协议感知能力。
	if protocolType == AgentProtocolType_Anthropic && strings.HasPrefix(lower, "openai-") {
		return false
	}
	if protocolType == AgentProtocolType_OpenAI && strings.HasPrefix(lower, "anthropic-") {
		return false
	}

	switch lower {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "host", "cookie",
		"authorization", "x-api-key", "x-forwarded-for", "x-forwarded-host",
		"x-forwarded-proto", "x-real-ip", "content-length":
		return false
	case "content-type", "accept", "user-agent", "x-request-id",
		"anthropic-version", "anthropic-beta", "openai-beta":
		return true
	}
	return strings.HasPrefix(lower, "x-stainless-") ||
		(protocolType == AgentProtocolType_OpenAI && strings.HasPrefix(lower, "openai-"))
}
