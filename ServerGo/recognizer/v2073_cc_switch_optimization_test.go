package recognizer

import (
	"net/http"
	"testing"

	"github.com/lishimeng/LsmTokensServer/protocol"
)

// ============================================================================
// v2.0.73: 借鉴 cc-switch 优化测试
// 覆盖：新增 Agent 前缀 / Grok Build Session 头 / 长度校验 / 头别名 / OpenCode 头
// ============================================================================

// ---- 1. 新增 Agent 前缀识别 ----

func TestRecognizeAgentTool_NewPrefixes_v2073(t *testing.T) {
	tests := []struct {
		ua       string
		wantName string
		wantInfo string
	}{
		{"grok-build/1.0.0", "grok-build", "1.0.0"},
		{"grok/1.0", "grok-build", "1.0"},
		{"Grok-Build/2.0", "grok-build", "2.0"},
		{"opencode/0.5.0", "opencode", "0.5.0"},
		{"rovo/1.0.0", "rovo", "1.0.0"},
		{"longcat/1.0", "longcat", "1.0"},
		{"kilo-code/1.0.0", "kilo-code", "1.0.0"},
		{"kilo/1.0", "kilo-code", "1.0"},
		{"amp/0.1.0", "amp", "0.1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.ua, func(t *testing.T) {
			got := RecognizeAgentTool(tt.ua)
			if got.AgentToolName != tt.wantName {
				t.Errorf("RecognizeAgentTool(%q).AgentToolName = %q, want %q", tt.ua, got.AgentToolName, tt.wantName)
			}
			if got.AgentToolInfo != tt.wantInfo {
				t.Errorf("RecognizeAgentTool(%q).AgentToolInfo = %q, want %q", tt.ua, got.AgentToolInfo, tt.wantInfo)
			}
		})
	}
}

// ---- 2. 新增前缀的 lookupKnownAgentPrefix 测试 ----

func TestLookupKnownAgentPrefix_v2073(t *testing.T) {
	tests := []struct {
		input     string
		wantCanon string
		wantOK    bool
	}{
		{"grok-build", "grok-build", true},
		{"GROK", "grok-build", true},
		{"opencode", "opencode", true},
		{"rovo", "rovo", true},
		{"longcat", "longcat", true},
		{"kilo-code", "kilo-code", true},
		{"KILO", "kilo-code", true},
		{"amp", "amp", true},
		{"unknown-tool", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := lookupKnownAgentPrefix(tt.input)
			if ok != tt.wantOK || got != tt.wantCanon {
				t.Errorf("lookupKnownAgentPrefix(%q) = (%q, %v), want (%q, %v)",
					tt.input, got, ok, tt.wantCanon, tt.wantOK)
			}
		})
	}
}

// ---- 3. Grok Build Session 头识别 ----

func TestParseSessionIDFromGrokHeaders_ConvId(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Grok-Conv-Id", "conv-724f4275-584e-43af-ad46-b5e7509a3ca2")
	headers.Set("X-Grok-Session-Id", "session-d937243f-2702-4f20-97b6-c9682235ab81")
	got := parseSessionIDFromGrokHeaders(headers)
	want := "conv-724f4275-584e-43af-ad46-b5e7509a3ca2"
	if got != want {
		t.Errorf("conv-id should win, got %q want %q", got, want)
	}
}

func TestParseSessionIDFromGrokHeaders_SessionIdFallback(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Grok-Session-Id", "session-d937243f-2702-4f20-97b6-c9682235ab81")
	got := parseSessionIDFromGrokHeaders(headers)
	want := "session-d937243f-2702-4f20-97b6-c9682235ab81"
	if got != want {
		t.Errorf("session-id fallback, got %q want %q", got, want)
	}
}

func TestParseSessionIDFromGrokHeaders_ReqIdIgnored(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Grok-Req-Id", "request-724f4275-584e-43af-ad46-b5e7509a3ca2")
	got := parseSessionIDFromGrokHeaders(headers)
	if got != "" {
		t.Errorf("req-id should be ignored, got %q", got)
	}
}

func TestParseSessionIDFromGrokHeaders_ShortValueFiltered(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Grok-Conv-Id", "short")
	got := parseSessionIDFromGrokHeaders(headers)
	if got != "" {
		t.Errorf("short value should be filtered, got %q", got)
	}
}

func TestParseSessionIDFromGrokHeaders_NilHeaders(t *testing.T) {
	got := parseSessionIDFromGrokHeaders(nil)
	if got != "" {
		t.Errorf("nil headers should return empty, got %q", got)
	}
}

// ---- 4. Session ID 长度校验 ----

func TestSessionIDMinHeaderLen_Constant(t *testing.T) {
	if sessionIDMinHeaderLen != 10 {
		t.Errorf("sessionIDMinHeaderLen = %d, want 10", sessionIDMinHeaderLen)
	}
}

// ---- 5. Anthropic 头别名（Claude-Code-Session-Id 无 X- 前缀）----

func TestParseSessionIDFromAnthropicHeaders_NoXPrefix(t *testing.T) {
	headers := http.Header{}
	headers.Set("Claude-Code-Session-Id", "d937243f-2702-4f20-97b6-c9682235ab81")
	got := parseSessionIDFromAnthropicHeaders(headers)
	want := "d937243f-2702-4f20-97b6-c9682235ab81"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---- 6. OpenCode Session 头识别增强 ----

func TestRecognizeSessionID_OpenAI_OpenCodeSessionHeader(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-OpenCode-Session-Id", "opencode-sess-abc1234567")
	body := []byte(`{"model":"gpt-4","messages":[]}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, headers)
	if sid != "opencode-sess-abc1234567" {
		t.Errorf("expected OpenCode session header, got %q", sid)
	}
}

// ---- 7. Grok Build 头在 OpenAI 协议中的集成 ----

func TestRecognizeSessionID_OpenAI_GrokHeader(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Grok-Conv-Id", "conv-724f4275-584e-43af-ad46-b5e7509a3ca2")
	body := []byte(`{"input":"Write code"}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, headers)
	if sid != "conv-724f4275-584e-43af-ad46-b5e7509a3ca2" {
		t.Errorf("expected Grok conv-id, got %q", sid)
	}
}

// ---- 8. 短值长度校验在各路径生效 ----

func TestSessionIDMinHeaderLen_FilterShortValues(t *testing.T) {
	// Anthropic 头短值
	headers := http.Header{}
	headers.Set("X-Claude-Code-Session-Id", "short")
	got := parseSessionIDFromAnthropicHeaders(headers)
	if got != "" {
		t.Errorf("Anthropic short header should be filtered, got %q", got)
	}

	// Codex Turn Metadata 短值
	headers2 := http.Header{}
	headers2.Set("X-Codex-Turn-Metadata", `{"session_id":"abc"}`)
	got2 := parseSessionIDFromCodexTurnMetadata(headers2)
	if got2 != "" {
		t.Errorf("Codex short session_id should be filtered, got %q", got2)
	}
}
