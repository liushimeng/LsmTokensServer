package protocol

import "testing"

// TestShouldForwardProxyHeader 验证请求头转发白名单 / 黑名单 + 协议感知
func TestShouldForwardProxyHeader(t *testing.T) {
	cases := []struct {
		key          string
		protocolType int
		want         bool
	}{
		// 通用黑名单（hop-by-hop + 代理转发链 + 客户端密钥）
		{"Authorization", AgentProtocolType_Anthropic, false},
		{"authorization", AgentProtocolType_OpenAI, false},
		{"X-Api-Key", AgentProtocolType_Anthropic, false},
		{"Cookie", AgentProtocolType_OpenAI, false},
		{"Host", AgentProtocolType_Anthropic, false},
		{"Content-Length", AgentProtocolType_Anthropic, false},
		{"Transfer-Encoding", AgentProtocolType_Anthropic, false},
		{"X-Forwarded-For", AgentProtocolType_Anthropic, false},

		// 通用白名单
		{"Content-Type", AgentProtocolType_Anthropic, true},
		{"User-Agent", AgentProtocolType_Anthropic, true},
		{"Accept", AgentProtocolType_Anthropic, true},
		{"X-Request-Id", AgentProtocolType_Anthropic, true},
		{"anthropic-version", AgentProtocolType_Anthropic, true},
		{"anthropic-beta", AgentProtocolType_Anthropic, true},
		{"openai-beta", AgentProtocolType_OpenAI, true},

		// 协议感知：客户端是 Anthropic 时，OpenAI 私有头不转发
		{"OpenAI-Organization", AgentProtocolType_Anthropic, false},
		{"OpenAI-Project", AgentProtocolType_Anthropic, false},
		{"openai-encoding-format", AgentProtocolType_Anthropic, false},
		// OpenAI 私有头对 OpenAI 客户端应转发
		{"OpenAI-Organization", AgentProtocolType_OpenAI, true},
		{"openai-encoding-format", AgentProtocolType_OpenAI, true},

		// 协议感知：客户端是 OpenAI 时，Anthropic 私有头不转发
		{"Anthropic-Version", AgentProtocolType_OpenAI, false},
		{"anthropic-beta", AgentProtocolType_OpenAI, false},
		// Anthropic 私有头对 Anthropic 客户端应转发
		{"Anthropic-Version", AgentProtocolType_Anthropic, true},
		{"anthropic-beta", AgentProtocolType_Anthropic, true},

		// x-stainless-* 对双方都转发
		{"X-Stainless-Raw-Response", AgentProtocolType_Anthropic, true},
		{"X-Stainless-Read-Timeout", AgentProtocolType_OpenAI, true},

		// 未知头：默认不转发（保守策略）
		{"X-Custom-Thing", AgentProtocolType_Anthropic, false},
	}
	for _, tc := range cases {
		got := ShouldForwardProxyHeader(tc.key, tc.protocolType)
		if got != tc.want {
			t.Errorf("ShouldForwardProxyHeader(%q, %d) = %v, want %v", tc.key, tc.protocolType, got, tc.want)
		}
	}
}
