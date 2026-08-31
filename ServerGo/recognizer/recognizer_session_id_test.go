package recognizer

import (
	"github.com/lishimeng/LsmTokensServer/protocol"
	"net/http"
	"testing"
)

// ============================================================================
// Session 识别层测试
// ============================================================================

func TestRecognizeSessionID_Anthropic_Normal(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages": [{"role": "user", "content": "hello"}],
		"metadata": {
			"user_id": "{\"device_id\":\"45d277355416ee1b\",\"account_uuid\":\"\",\"session_id\":\"2e8c1243-ed7c-477f-8fb7-d03225568634\"}"
		}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "2e8c1243-ed7c-477f-8fb7-d03225568634" {
		t.Errorf("expected session_id, got %q", sid)
	}
}

func TestRecognizeSessionID_Anthropic_NoMetadata(t *testing.T) {
	body := []byte(`{"model": "claude-sonnet-4-20250514", "max_tokens": 1024}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_Anthropic_EmptyUserID(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": ""}}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_Anthropic_InvalidJSON(t *testing.T) {
	body := []byte(`{not valid json}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_Anthropic_InvalidInnerJSON(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": "not a json string"}}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_Anthropic_NoSessionID(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": "{\"device_id\":\"abc\"}"}}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_Normal(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hello"}],
		"metadata": {
			"user_id": "{\"device_id\":\"abc123\",\"account_uuid\":\"\",\"session_id\":\"openai-session-456\"}"
		}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "openai-session-456" {
		t.Errorf("expected 'openai-session-456', got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_NoMetadata(t *testing.T) {
	body := []byte(`{"model": "gpt-4", "messages": [{"role": "user", "content": "hello"}]}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_EmptyUserID(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": ""}}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_InvalidInnerJSON(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": "not a json string"}}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_NoSessionID(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": "{\"device_id\":\"abc\"}"}}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_UnknownProtocol(t *testing.T) {
	body := []byte(`{"metadata": {"user_id": "{\"session_id\":\"test\"}"}}`)
	sid := RecognizeSessionID(body, 999, nil)
	if sid != "" {
		t.Errorf("expected empty for unknown protocol, got %q", sid)
	}
}

func TestRecognizeSessionID_CrossProtocolConsistency(t *testing.T) {
	// 验证同一 body 在两种协议下都能识别（当前实现一致）
	body := []byte(`{
		"metadata": {
			"user_id": "{\"session_id\":\"cross-protocol-session\"}"
		}
	}`)
	anthropicSID := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	openAISID := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if anthropicSID != openAISID {
		t.Errorf("cross-protocol mismatch: anthropic=%q, openai=%q", anthropicSID, openAISID)
	}
	if anthropicSID != "cross-protocol-session" {
		t.Errorf("expected 'cross-protocol-session', got %q", anthropicSID)
	}
}

// ============================================================================
// SessionRecognizer 接口测试
// ============================================================================

func TestAnthropicSessionRecognizer_ProtocolType(t *testing.T) {
	r := &anthropicSessionRecognizer{}
	if r.ProtocolType() != protocol.AgentProtocolType_Anthropic {
		t.Errorf("expected protocol type %d, got %d", protocol.AgentProtocolType_Anthropic, r.ProtocolType())
	}
}

func TestOpenAISessionRecognizer_ProtocolType(t *testing.T) {
	r := &openAISessionRecognizer{}
	if r.ProtocolType() != protocol.AgentProtocolType_OpenAI {
		t.Errorf("expected protocol type %d, got %d", protocol.AgentProtocolType_OpenAI, r.ProtocolType())
	}
}

func TestRegisterSessionRecognizer(t *testing.T) {
	// 测试自定义识别器注册
	customRecognizer := &mockSessionRecognizer{
		protocolType: 999,
		result:       "custom-session",
	}
	RegisterSessionRecognizer(999, customRecognizer)

	body := []byte(`{}`)
	sid := RecognizeSessionID(body, 999, nil)
	if sid != "custom-session" {
		t.Errorf("expected 'custom-session', got %q", sid)
	}
}

type mockSessionRecognizer struct {
	protocolType int
	result       string
}

func (m *mockSessionRecognizer) Recognize(body []byte, headers http.Header) string {
	return m.result
}

func (m *mockSessionRecognizer) ProtocolType() int {
	return m.protocolType
}

// ============================================================================
// v2.0.23 OpenAI 协议 session_id 兼容性识别 —— client_metadata / prompt_cache_key
// ============================================================================
//
// 真实场景：OpenAI Codex CLI 等新型 Agent 把对话稳定标识写到 body 顶层
// `client_metadata.session_id` / `client_metadata.thread_id` / `prompt_cache_key`
// 三个字段，且 metadata.user_id 通常缺失。新识别器必须按优先级链命中这些字段。

const codexRealSampleBody = `{
  "model": "gpt-5",
  "messages": [{"role": "user", "content": "hi"}],
  "stream": true,
  "prompt_cache_key": "019f205b-9c1f-7331-b4f2-a0715e480692",
  "client_metadata": {
    "session_id": "019f205b-9c1f-7331-b4f2-a0715e480692",
    "turn_id": "019f2066-c9c2-7992-8c45-d81bd455c221",
    "thread_id": "019f205b-9c1f-7331-b4f2-a0715e480692",
    "x-codex-installation-id": "8439ef58-95b2-4e26-ba5f-92a7425e66a4",
    "x-codex-window-id": "019f205b-9c1f-7331-b4f2-a0715e480692:0"
  }
}`

func TestRecognizeSessionID_OpenAI_CodexClientMetadata(t *testing.T) {
	body := []byte(codexRealSampleBody)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	want := "019f205b-9c1f-7331-b4f2-a0715e480692"
	if sid != want {
		t.Errorf("expected Codex session_id %q, got %q", want, sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_OnlySessionID(t *testing.T) {
	// 只有 client_metadata.session_id，无 prompt_cache_key / thread_id
	body := []byte(`{
		"model": "gpt-5",
		"messages": [{"role": "user", "content": "hi"}],
		"client_metadata": {
			"session_id": "codex-only-sess",
			"turn_id": "turn-abc"
		}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "codex-only-sess" {
		t.Errorf("expected session_id, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_OnlyThreadID(t *testing.T) {
	// 只有 client_metadata.thread_id（session_id 缺失时回退）
	body := []byte(`{
		"model": "gpt-5",
		"messages": [{"role": "user", "content": "hi"}],
		"client_metadata": {
			"thread_id": "codex-thread-only",
			"turn_id": "turn-xyz"
		}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "codex-thread-only" {
		t.Errorf("expected thread_id fallback, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_EmptyValues(t *testing.T) {
	// client_metadata 存在但两个字段都是空串 → 继续往下找
	body := []byte(`{
		"model": "gpt-5",
		"client_metadata": {"session_id": "", "thread_id": "   "},
		"prompt_cache_key": "fallback-key"
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "fallback-key" {
		t.Errorf("expected prompt_cache_key fallback, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_AllEmpty(t *testing.T) {
	// client_metadata 与 prompt_cache_key 都空 → 回到顶层 session_id 兜底
	body := []byte(`{
		"model": "gpt-5",
		"client_metadata": {"session_id": "", "thread_id": ""},
		"prompt_cache_key": "",
		"session_id": "top-level-fallback"
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "top-level-fallback" {
		t.Errorf("expected top-level session_id, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_PromptCacheKey_Alone(t *testing.T) {
	// 只有 prompt_cache_key，无 client_metadata
	body := []byte(`{
		"model": "gpt-5",
		"prompt_cache_key": "prompt-cache-only-123"
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "prompt-cache-only-123" {
		t.Errorf("expected prompt_cache_key, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_MetadataUserIDStillWins(t *testing.T) {
	// metadata.user_id 命中时优先级最高，不被 client_metadata 覆盖
	body := []byte(`{
		"model": "gpt-5",
		"metadata": {"user_id": "{\"session_id\":\"metadata-wins\"}"},
		"client_metadata": {"session_id": "codex-loses"},
		"prompt_cache_key": "prompt-loses"
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "metadata-wins" {
		t.Errorf("metadata.user_id should win, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadataPriority(t *testing.T) {
	// client_metadata.session_id 优先于 client_metadata.thread_id
	body := []byte(`{
		"client_metadata": {
			"session_id": "primary-session-id",
			"thread_id": "secondary-thread-id"
		}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "primary-session-id" {
		t.Errorf("session_id should win over thread_id, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_NoFields(t *testing.T) {
	// client_metadata 存在但既无 session_id 也无 thread_id → 跳过
	body := []byte(`{
		"client_metadata": {"turn_id": "t1", "x-other": "v"}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_NonStringValue(t *testing.T) {
	// client_metadata.session_id 是数字 → Unmarshal 失败 / 留空
	body := []byte(`{
		"client_metadata": {"session_id": 12345, "thread_id": "ok-thread-id"}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "ok-thread-id" {
		t.Errorf("expected thread_id fallback on non-string session_id, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_MalformedJSON(t *testing.T) {
	body := []byte(`{not json`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "" {
		t.Errorf("expected empty for malformed body, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_ClientMetadata_NestedDeep(t *testing.T) {
	// 真实场景里 client_metadata 字段会很多嵌套；要确保解析不漏字段
	body := []byte(`{
		"model": "gpt-5",
		"messages": [{"role": "user", "content": "hi"}],
		"client_metadata": {
			"session_id": "deep-sess-001",
			"turn_id": "019f2066-c9c2-7992-8c45-d81bd455c221",
			"thread_id": "019f205b-9c1f-7331-b4f2-a0715e480692",
			"x-codex-installation-id": "8439ef58-95b2-4e26-ba5f-92a7425e66a4",
			"x-codex-window-id": "019f205b-9c1f-7331-b4f2-a0715e480692:0",
			"x-codex-turn-metadata": "{\"installation_id\":\"8439ef58-95b2-4e26-ba5f-92a7425e66a4\",\"session_id\":\"019f205b-9c1f-7331-b4f2-a0715e480692\",\"thread_id\":\"019f205b-9c1f-7331-b4f2-a0715e480692\",\"turn_id\":\"019f2066-c9c2-7992-8c45-d81bd455c221\",\"request_kind\":\"turn\",\"thread_source\":\"user\",\"sandbox\":\"seatbelt\",\"turn_started_at_unix_ms\":1782955035078}"
		}
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	if sid != "deep-sess-001" {
		t.Errorf("expected deep-sess-001, got %q", sid)
	}
}

func TestRecognizeSessionID_Anthropic_ClientMetadataIgnored(t *testing.T) {
	// Anthropic 协议不启用 client_metadata / prompt_cache_key 路径
	// （即使 body 里有这些字段，Anthropic recognizer 应继续走 metadata.user_id）
	body := []byte(`{
		"model": "claude-sonnet-4-20250514",
		"client_metadata": {"session_id": "codex-only"},
		"prompt_cache_key": "prompt-only"
	}`)
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_Anthropic, nil)
	if sid != "" {
		t.Errorf("Anthropic should ignore client_metadata, got %q", sid)
	}
}

// ============================================================================
// 底层辅助函数单元测试
// ============================================================================

func TestParseSessionIDFromClientMetadata(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"session_id only", `{"client_metadata":{"session_id":"sess-abc1234567"}}`, "sess-abc1234567"},
		{"thread_id only", `{"client_metadata":{"thread_id":"thread-xyz98765"}}`, "thread-xyz98765"},
		{"both, session_id wins", `{"client_metadata":{"session_id":"sess-abc1234567","thread_id":"thread-xyz98765"}}`, "sess-abc1234567"},
		{"both, thread_id empty", `{"client_metadata":{"session_id":"sess-abc1234567","thread_id":""}}`, "sess-abc1234567"},
		{"both empty", `{"client_metadata":{"session_id":"","thread_id":""}}`, ""},
		{"short session_id filtered", `{"client_metadata":{"session_id":"s1"}}`, ""},
		{"whitespace trimmed", `{"client_metadata":{"session_id":"  sess-abc1234567  ","thread_id":"  thread-xyz98765  "}}`, "sess-abc1234567"},
		{"no client_metadata field", `{"model":"x"}`, ""},
		{"client_metadata empty object", `{"client_metadata":{}}`, ""},
		{"malformed json", `{not json`, ""},
		{"session_id is number", `{"client_metadata":{"session_id":123,"thread_id":"ok"}}`, ""},
		{"session_id is bool", `{"client_metadata":{"session_id":true}}`, ""},
		{"nested other fields", `{"client_metadata":{"turn_id":"t","session_id":"sess-abc1234567"}}`, "sess-abc1234567"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSessionIDFromClientMetadata([]byte(tc.body))
			if got != tc.want {
				t.Errorf("want=%q got=%q", tc.want, got)
			}
		})
	}
}

func TestParseSessionIDFromPromptCacheKey(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"standard uuid", `{"prompt_cache_key":"019f205b-9c1f-7331-b4f2-a0715e480692"}`, "019f205b-9c1f-7331-b4f2-a0715e480692"},
		{"whitespace trimmed", `{"prompt_cache_key":"  abc  "}`, "abc"},
		{"empty string", `{"prompt_cache_key":""}`, ""},
		{"whitespace only", `{"prompt_cache_key":"   "}`, ""},
		{"missing field", `{"model":"x"}`, ""},
		{"non-string value (number)", `{"prompt_cache_key":123}`, ""},
		{"non-string value (bool)", `{"prompt_cache_key":true}`, ""},
		{"malformed json", `{not json`, ""},
		{"null value", `{"prompt_cache_key":null}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSessionIDFromPromptCacheKey([]byte(tc.body))
			if got != tc.want {
				t.Errorf("want=%q got=%q", tc.want, got)
			}
		})
	}
}

// ============= v2.0.76 阶段BD：RecognizeSessionIDWithSource 与 Agent 专用头测试 =============

// TestRecognizeSessionIDWithSource_Matched 验证识别命中时两字段同值
func TestRecognizeSessionIDWithSource_Matched(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Claude-Code-Session-Id", "144ca9ed-c216-40f2-87a7-cd9df1dc7f3c")
	res := RecognizeSessionIDWithSource([]byte(`{"model":"claude-3"}`), protocol.AgentProtocolType_Anthropic, headers)
	if res.AgentToolSessionID != "144ca9ed-c216-40f2-87a7-cd9df1dc7f3c" {
		t.Errorf("AgentToolSessionID = %q, want claude session", res.AgentToolSessionID)
	}
	if res.EffectiveSessionID != res.AgentToolSessionID {
		t.Errorf("EffectiveSessionID = %q, want same as AgentToolSessionID", res.EffectiveSessionID)
	}
}

// TestRecognizeSessionIDWithSource_NotMatched 验证未识别时两字段均为空
func TestRecognizeSessionIDWithSource_NotMatched(t *testing.T) {
	res := RecognizeSessionIDWithSource([]byte(`{"model":"gpt-4","messages":[]}`), protocol.AgentProtocolType_OpenAI, http.Header{})
	if res.AgentToolSessionID != "" {
		t.Errorf("AgentToolSessionID = %q, want empty", res.AgentToolSessionID)
	}
	if res.EffectiveSessionID != "" {
		t.Errorf("EffectiveSessionID = %q, want empty", res.EffectiveSessionID)
	}
}

// TestRecognizeSessionIDWithSource_CompatWithLegacy 验证与原 RecognizeSessionID 结果一致
func TestRecognizeSessionIDWithSource_CompatWithLegacy(t *testing.T) {
	body := []byte(`{"model":"gpt-4","metadata":{"user_id":"{\"session_id\":\"legacy-sess-1234567890\"}"}}`)
	headers := http.Header{}
	legacy := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, headers)
	res := RecognizeSessionIDWithSource(body, protocol.AgentProtocolType_OpenAI, headers)
	if legacy == "" {
		t.Fatal("legacy path should recognize session")
	}
	if res.EffectiveSessionID != legacy {
		t.Errorf("WithSource=%q != legacy=%q", res.EffectiveSessionID, legacy)
	}
}

// agentToolSessionHeaderCases v2.0.76 阶段BD 新增 Agent 专用头用例
var agentToolSessionHeaderCases = []struct {
	name    string
	header  string
	value   string
}{
	{"aider", "X-Aider-Session-Id", "aider-sess-aaaaaaaa-bbbb-cccc"},
	{"continue", "X-Continue-Session-Id", "continue-session-1234567890"},
	{"cursor", "X-Cursor-Session-Id", "cursor-session-0987654321"},
	{"cline", "X-Cline-Session-Id", "cline-session-abcdefghij"},
	{"copilot", "X-Github-Copilot-Session-Id", "copilot-session-1234-5678"},
	{"kilo-code", "X-Kilo-Code-Session-Id", "kilo-session-aaaabbbbcccc"},
	{"windsurf", "X-Windsurf-Session-Id", "windsurf-session-ddddeeeeffff"},
}

// TestRecognizeSessionID_AgentToolHeaders 验证新增 Agent 专用头识别
func TestRecognizeSessionID_AgentToolHeaders(t *testing.T) {
	for _, tc := range agentToolSessionHeaderCases {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set(tc.header, tc.value)
			got := RecognizeSessionID([]byte(`{"model":"gpt-4","messages":[]}`), protocol.AgentProtocolType_OpenAI, headers)
			if got != tc.value {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.value)
			}
		})
	}
}

// TestRecognizeSessionID_AgentToolHeaders_Anthropic 验证 Anthropic 协议下
// Agent 专用头不生效（这些头只注册在 OpenAI 识别路径；Anthropic 走专用头集合）
func TestRecognizeSessionID_AgentToolHeaders_Anthropic(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Aider-Session-Id", "aider-sess-aaaaaaaa-bbbb-cccc")
	got := RecognizeSessionID([]byte(`{"model":"claude-3"}`), protocol.AgentProtocolType_Anthropic, headers)
	if got != "" {
		t.Errorf("Anthropic path got %q, want empty (aider header is OpenAI-path only)", got)
	}
}

// TestRecognizeSessionID_AgentToolHeaders_ShortValueRejected 验证短值被最小长度校验拒绝
func TestRecognizeSessionID_AgentToolHeaders_ShortValueRejected(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Cursor-Session-Id", "short") // 5 < sessionIDMinHeaderLen(10)
	got := RecognizeSessionID([]byte(`{"model":"gpt-4","messages":[]}`), protocol.AgentProtocolType_OpenAI, headers)
	if got != "" {
		t.Errorf("got %q, want empty for short value", got)
	}
}
