package recognizer

import (
	"net/http"
	"testing"

	"github.com/lishimeng/LsmTokensServer/protocol"
)

// ============================================================================
// v2.0.73: Agent 工具检测与 Session 识别增强测试
// 覆盖：
//   1. 新增 Agent UA 解析（codex-cli/pi/hermes/aider/continue/cline/windsurf/cursor/copilot）
//   2. 高阶 Agent 白名单扩展
//   3. 合成 Session 白名单扩展
//   4. Header-based Session 识别（Codex Turn Metadata JSON 头）
//   5. 已知前缀映射（knownAgentPrefixes + lookupKnownAgentPrefix）
// ============================================================================

// ---- 1. 新增 Agent UA 解析 ----

func TestRecognizeAgentTool_NewAgents(t *testing.T) {
	tests := []struct {
		ua       string
		wantName string
		wantInfo string
	}{
		{"codex-cli/0.1.0", "codex-cli", "0.1.0"},
		{"pi/2.0", "pi", "2.0"},
		{"hermes/1.5.0", "hermes", "1.5.0"},
		{"aider/0.60.0", "aider", "0.60.0"},
		{"continue/0.9.0", "continue", "0.9.0"},
		{"cline/3.0.0", "cline", "3.0.0"},
		{"windsurf/1.0", "windsurf", "1.0"},
		{"cursor/0.40.0", "cursor", "0.40.0"},
		{"copilot/1.0", "copilot", "1.0"},
		// 大小写变体
		{"Aider/0.60.0", "aider", "0.60.0"},
		{"CURSOR/0.40.0", "cursor", "0.40.0"},
		{"Claude-Code/1.0.5", "claude-code", "1.0.5"},
		// 无版本号
		{"aider", "aider", ""},
		{"cline", "cline", ""},
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

func TestRecognizeAgentTool_ClaudeCodeUA(t *testing.T) {
	tests := []struct {
		ua       string
		wantName string
		wantInfo string
	}{
		{"claude-code/1.0.5", "claude-code", "1.0.5"},
		{"Claude-Code/2.0.0", "claude-code", "2.0.0"},
		{"CLAUDE-CODE/1.0", "claude-code", "1.0"},
		{"claude-code", "claude-code", ""},
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

// ---- 4. Header-based Session 识别 ----

func TestParseSessionIDFromCodexTurnMetadata(t *testing.T) {
	// 正常 JSON 头
	headers := http.Header{}
	headers.Set("X-Codex-Turn-Metadata", `{"session_id":"sess-abc-123","thread_id":"thread-xyz","turn_id":"turn-1"}`)
	got := parseSessionIDFromCodexTurnMetadata(headers)
	if got != "sess-abc-123" {
		t.Errorf("parseSessionIDFromCodexTurnMetadata with session_id = %q, want %q", got, "sess-abc-123")
	}

	// 只有 thread_id（无 session_id）
	headers2 := http.Header{}
	headers2.Set("X-Codex-Turn-Metadata", `{"thread_id":"thread-xyz"}`)
	got2 := parseSessionIDFromCodexTurnMetadata(headers2)
	if got2 != "thread-xyz" {
		t.Errorf("parseSessionIDFromCodexTurnMetadata with thread_id only = %q, want %q", got2, "thread-xyz")
	}
}

func TestParseSessionIDFromCodexTurnMetadata_Empty(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
	}{
		{"nil headers", nil},
		{"no header", http.Header{}},
		{"empty value", func() http.Header { h := http.Header{}; h.Set("X-Codex-Turn-Metadata", ""); return h }()},
		{"invalid json", func() http.Header { h := http.Header{}; h.Set("X-Codex-Turn-Metadata", "not-json"); return h }()},
		{"empty json", func() http.Header { h := http.Header{}; h.Set("X-Codex-Turn-Metadata", "{}"); return h }()},
		{"no session fields", func() http.Header {
			h := http.Header{}
			h.Set("X-Codex-Turn-Metadata", `{"turn_id":"turn-1"}`)
			return h
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSessionIDFromCodexTurnMetadata(tt.headers)
			if got != "" {
				t.Errorf("parseSessionIDFromCodexTurnMetadata(%s) = %q, want empty", tt.name, got)
			}
		})
	}
}

func TestParseSessionIDFromCodexTurnMetadata_NonStringValues(t *testing.T) {
	// 非字符串值应被忽略
	headers := http.Header{}
	headers.Set("X-Codex-Turn-Metadata", `{"session_id":12345,"thread_id":true}`)
	got := parseSessionIDFromCodexTurnMetadata(headers)
	if got != "" {
		t.Errorf("parseSessionIDFromCodexTurnMetadata with non-string values = %q, want empty", got)
	}
}

func TestParseSessionIDFromAnthropicHeaders_ClaudeCodeSessionID(t *testing.T) {
	// Claude Code CLI 原生头应优先于 anthropic-beta
	headers := http.Header{}
	headers.Set("X-Claude-Code-Session-Id", "claude-sess-001")
	headers.Set("anthropic-beta", "prompt-caching-2024-07-31; session-id=beta-sess-999")
	got := parseSessionIDFromAnthropicHeaders(headers)
	if got != "claude-sess-001" {
		t.Errorf("parseSessionIDFromAnthropicHeaders Claude Code priority = %q, want %q", got, "claude-sess-001")
	}
}

func TestRecognizeSessionID_HeaderFallback_OpenAI(t *testing.T) {
	// OpenAI 协议：Codex 头应优先于 body 中的 metadata.user_id
	body := []byte(`{"metadata":{"user_id":"{\"session_id\":\"body-sess\"}"}}`)
	headers := http.Header{}
	headers.Set("X-Codex-Turn-Metadata", `{"session_id":"header-sess"}`)

	got := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, headers)
	if got != "header-sess" {
		t.Errorf("RecognizeSessionID OpenAI header fallback = %q, want %q", got, "header-sess")
	}
}

func TestRecognizeSessionID_HeaderFallback_Anthropic(t *testing.T) {
	// Anthropic 协议：Claude Code 头应优先于 body 中的 metadata.user_id
	body := []byte(`{"metadata":{"user_id":"{\"session_id\":\"body-sess\"}"}}`)
	headers := http.Header{}
	headers.Set("X-Claude-Code-Session-Id", "claude-header-sess")

	got := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, headers)
	if got != "claude-header-sess" {
		t.Errorf("RecognizeSessionID Anthropic header fallback = %q, want %q", got, "claude-header-sess")
	}
}

// ---- 5. 已知前缀映射 ----

func TestLookupKnownAgentPrefix(t *testing.T) {
	tests := []struct {
		name      string
		wantCanon string
		wantFound bool
	}{
		{"aider", "aider", true},
		{"Aider", "aider", true},
		{"AIDER", "aider", true},
		{"cline", "cline", true},
		{"cursor", "cursor", true},
		{"copilot", "copilot", true},
		{"windsurf", "windsurf", true},
		{"hermes", "hermes", true},
		{"pi", "pi", true},
		{"codex-cli", "codex-cli", true},
		{"claude-code", "claude-code", true},
		{"anthropic-cli", "claude-code", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCanon, gotFound := lookupKnownAgentPrefix(tt.name)
			if gotFound != tt.wantFound {
				t.Errorf("lookupKnownAgentPrefix(%q) found = %v, want %v", tt.name, gotFound, tt.wantFound)
			}
			if gotCanon != tt.wantCanon {
				t.Errorf("lookupKnownAgentPrefix(%q) canon = %q, want %q", tt.name, gotCanon, tt.wantCanon)
			}
		})
	}
}

func TestLookupKnownAgentPrefix_Unknown(t *testing.T) {
	unknowns := []string{"unknown-bot", "random", "test", ""}
	for _, name := range unknowns {
		_, found := lookupKnownAgentPrefix(name)
		if found {
			t.Errorf("lookupKnownAgentPrefix(%q) found = true, want false", name)
		}
	}
}

// ---- 6. 现有 Agent 不受影响 ----

func TestRecognizeAgentTool_ExistingAgentsUnchanged(t *testing.T) {
	// 确保现有 Agent 的识别不受新映射影响
	tests := []struct {
		ua       string
		wantName string
		wantInfo string
	}{
		{"claude-cli/1.2.3", "claude-cli", "1.2.3"},
		{"openclaw/1.0.0", "openclaw", "1.0.0"},
		{"OpenAI/JS 4.0.0", "OpenAI/JS", "4.0.0"},
		{"OpenAI-Python/1.50.0", "OpenAI-Python", "1.50.0"},
		{"opencode/0.5.0", "opencode", "0.5.0"},
		{"Kilo-Code/1.0", "kilo-code", "1.0"},
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
