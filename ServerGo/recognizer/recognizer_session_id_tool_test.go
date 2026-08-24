package recognizer

import (
	"github.com/lishimeng/LsmTokensServer/protocol"
	"net/http"
	"os"
	"testing"
)

// ============================================================================
// OpenClaw Agent 工具识别器测试
// ============================================================================

const openClawSampleBodyJSON = `{
  "model": "MiniMax-M3",
  "messages": [
    {
      "role": "system",
      "content": "You are a personal assistant running inside OpenClaw.\n## Tooling\n## Skills\n\n<available_skills>...</available_skills>\n\n## Runtime\nRuntime: agent=main | session=agent:main:lsminterserver | sessionId=144ca9ed-c216-40f2-87a7-cd9df1dc7f3c | host=iZ0jlgcs84efcq5lvzsk27Z | repo=/home/aicon/.openclaw/workspace | os=Linux 6.8.0-100-generic (x64) | node=v22.22.1 | model=liusm191-server/liusm191-server-model | default_model=liusm191-server/liusm191-server-model | shell=bash | channel=webchat | capabilities=none | thinking=off\nCurrent model identity: liusm191-server/liusm191-server-model.\nReasoning: off (hidden unless on/stream)."
    },
    {
      "role": "user",
      "content": "[Thu 2026-06-25 10:18 GMT+8] 给出最近 CPU 使用率最高的 5个 进程或是服务？"
    }
  ],
  "stream": true,
  "tools": []
}`

const openClawSampleBodyJSONDifferentSession = `{
  "model": "MiniMax-M3",
  "messages": [
    {
      "role": "system",
      "content": "You are a personal assistant running inside OpenClaw.\n## Runtime\nRuntime: agent=main | session=agent:main:lsminterserver | sessionId=19e08717-e98c-44f9-8885-1fbc78b90720 | host=iZ0jlgcs84efcq5lvzsk27Z"
    },
    {
      "role": "user",
      "content": "hi"
    }
  ]
}`

func loadOpenClawSample(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("OpenClaw sample not available at %s: %v", path, err)
	}
	return b
}

func newOpenClawHeader(ua string) http.Header {
	h := http.Header{}
	h.Set("User-Agent", ua)
	return h
}

func TestOpenClawSessionRecognizer_CanTrigger(t *testing.T) {
	r := &openClawSessionRecognizer{}
	if !r.CanTrigger("OpenAI/JS 4.0.0") {
		t.Errorf("expected trigger on 'OpenAI/JS 4.0.0'")
	}
	if !r.CanTrigger("something OpenAI/JS something") {
		t.Errorf("expected trigger when substring present")
	}
	if r.CanTrigger("Mozilla/5.0") {
		t.Errorf("did not expect trigger on non-OpenAI/JS UA")
	}
	if r.CanTrigger("") {
		t.Errorf("did not expect trigger on empty UA")
	}
}

func TestOpenClawSessionRecognizer_AgentName(t *testing.T) {
	if (&openClawSessionRecognizer{}).AgentName() != "OpenClaw" {
		t.Errorf("expected AgentName 'OpenClaw'")
	}
}

func TestOpenClawSessionRecognizer_Recognize_FromInlineSample(t *testing.T) {
	r := &openClawSessionRecognizer{}
	got := r.Recognize([]byte(openClawSampleBodyJSON))
	want := "144ca9ed-c216-40f2-87a7-cd9df1dc7f3c"
	if got != want {
		t.Errorf("expected session_id %q, got %q", want, got)
	}
}

func TestOpenClawSessionRecognizer_Recognize_DifferentSession(t *testing.T) {
	r := &openClawSessionRecognizer{}
	got := r.Recognize([]byte(openClawSampleBodyJSONDifferentSession))
	want := "19e08717-e98c-44f9-8885-1fbc78b90720"
	if got != want {
		t.Errorf("expected session_id %q, got %q", want, got)
	}
}

// 从真实样本文件验证：Session-01 / Session-02 内容一致、sessionId 不同
func TestOpenClawSessionRecognizer_RealSamples(t *testing.T) {
	b1 := loadOpenClawSample(t, "SessionAnalysis/OpenClaw-Session-01.json")
	b2 := loadOpenClawSample(t, "SessionAnalysis/OpenClaw-Session-02.json")

	r := &openClawSessionRecognizer{}
	sid1 := r.Recognize(b1)
	sid2 := r.Recognize(b2)
	if sid1 != "144ca9ed-c216-40f2-87a7-cd9df1dc7f3c" {
		t.Errorf("Session-01 expected 144ca9ed..., got %q", sid1)
	}
	if sid2 != "19e08717-e98c-44f9-8885-1fbc78b90720" {
		t.Errorf("Session-02 expected 19e08717..., got %q", sid2)
	}
	if sid1 == sid2 {
		t.Errorf("Session-01 and Session-02 must have different session_ids")
	}
}

func TestOpenClawSessionRecognizer_Recognize_NoSystemMessage(t *testing.T) {
	body := []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`)
	if got := (&openClawSessionRecognizer{}).Recognize(body); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestOpenClawSessionRecognizer_Recognize_NoMessages(t *testing.T) {
	body := []byte(`{"model":"x"}`)
	if got := (&openClawSessionRecognizer{}).Recognize(body); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestOpenClawSessionRecognizer_Recognize_NoSessionID(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"hello world"}]}`)
	if got := (&openClawSessionRecognizer{}).Recognize(body); got != "" {
		t.Errorf("expected empty when no sessionId=, got %q", got)
	}
}

func TestOpenClawSessionRecognizer_Recognize_MalformedBody(t *testing.T) {
	if got := (&openClawSessionRecognizer{}).Recognize([]byte(`{not json`)); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := (&openClawSessionRecognizer{}).Recognize([]byte{}); got != "" {
		t.Errorf("expected empty on nil/empty body, got %q", got)
	}
}

func TestOpenClawSessionRecognizer_Recognize_NoTerminator(t *testing.T) {
	// 仅有 sessionId= 但没有终止符：应把整段剩余视为 session_id
	body := []byte(`{"messages":[{"role":"system","content":"sessionId=abc123"}]}`)
	if got := (&openClawSessionRecognizer{}).Recognize(body); got != "abc123" {
		t.Errorf("expected abc123 fallback, got %q", got)
	}
}

// ============================================================================
// OpenAI Recognizer 调度器：UA 触发 + 通用 fallback
// ============================================================================

func TestRecognizeSessionID_OpenAI_OpenClawUATrigger(t *testing.T) {
	body := []byte(openClawSampleBodyJSON)
	h := newOpenClawHeader("OpenAI/JS 4.0.0")
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, h)
	if sid != "144ca9ed-c216-40f2-87a7-cd9df1dc7f3c" {
		t.Errorf("expected OpenClaw session_id, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_NoUA_FallsBackToMetadata(t *testing.T) {
	body := []byte(openClawSampleBodyJSON) // OpenClaw 内容，但不带 UA
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, nil)
	// 应走通用 metadata.user_id 路径；该 sample 不含 metadata.user_id，因此返回 ""
	if sid != "" {
		t.Errorf("expected empty when no metadata.user_id, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_UAButNoSystemMessage(t *testing.T) {
	// UA 命中但 body 不含 system message → OpenClaw 识别失败；无 metadata 也 fallback 失败
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	h := newOpenClawHeader("OpenAI/JS 4.0.0")
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, h)
	if sid != "" {
		t.Errorf("expected empty, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_OpenClawWinsOverMetadata(t *testing.T) {
	// 同时包含 OpenClaw system content 和 metadata.user_id；UA 触发时 OpenClaw 胜
	body := []byte(`{
		"messages": [
			{"role":"system","content":"Runtime: agent=main | sessionId=openclaw-sess-1 | rest"}
		],
		"metadata": {"user_id": "{\"session_id\":\"metadata-sess-2\"}"}
	}`)
	h := newOpenClawHeader("OpenAI/JS")
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, h)
	if sid != "openclaw-sess-1" {
		t.Errorf("expected OpenClaw session_id to win, got %q", sid)
	}
}

func TestRecognizeSessionID_OpenAI_NonOpenClawUA_StillUsesMetadata(t *testing.T) {
	// 非 OpenAI/JS UA → 跳过 OpenClaw recognizer；走 metadata.user_id
	body := []byte(`{
		"messages": [
			{"role":"system","content":"Runtime: agent=main | sessionId=should-not-be-used | rest"}
		],
		"metadata": {"user_id": "{\"session_id\":\"metadata-sess\"}"}
	}`)
	h := newOpenClawHeader("curl/7.81.0")
	sid := RecognizeSessionID(body, protocol.AgentProtocolType_OpenAI, h)
	if sid != "metadata-sess" {
		t.Errorf("expected metadata-sess fallback, got %q", sid)
	}
}

// ============================================================================
// 通用辅助：headers==nil/UA=="" 时快速短路
// ============================================================================

func TestTryAgentToolRecognizers_NilHeaders(t *testing.T) {
	if got := tryAgentToolRecognizers([]byte("{}"), nil); got != "" {
		t.Errorf("expected empty for nil headers, got %q", got)
	}
}

func TestTryAgentToolRecognizers_EmptyUA(t *testing.T) {
	h := http.Header{}
	if got := tryAgentToolRecognizers([]byte("{}"), h); got != "" {
		t.Errorf("expected empty for empty UA, got %q", got)
	}
}

func TestTryAgentToolRecognizers_NonMatchingUA(t *testing.T) {
	h := newOpenClawHeader("Mozilla/5.0")
	if got := tryAgentToolRecognizers([]byte(`{}`), h); got != "" {
		t.Errorf("expected empty for non-matching UA, got %q", got)
	}
}

// ============================================================================
// RegisterAgentToolSessionRecognizer 扩展性测试
// ============================================================================

type mockAgentToolRecognizer struct {
	name      string
	canTrig   bool
	recognize string
}

func (m *mockAgentToolRecognizer) AgentName() string            { return m.name }
func (m *mockAgentToolRecognizer) CanTrigger(ua string) bool    { return m.canTrig }
func (m *mockAgentToolRecognizer) Recognize(body []byte) string { return m.recognize }

func TestRegisterAgentToolSessionRecognizer(t *testing.T) {
	mock := &mockAgentToolRecognizer{
		name:      "MockTool",
		canTrig:   true,
		recognize: "mock-session-xyz",
	}
	RegisterAgentToolSessionRecognizer(mock)

	h := newOpenClawHeader("AnyUA/1.0")
	sid := RecognizeSessionID([]byte(`{}`), protocol.AgentProtocolType_OpenAI, h)
	if sid != "mock-session-xyz" {
		t.Errorf("expected 'mock-session-xyz', got %q", sid)
	}
}

// ============================================================================
// 底层辅助函数单元测试
// ============================================================================

func TestExtractFirstSystemMessageContent(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantOk  bool
		wantVal string
	}{
		{"single system message", `{"messages":[{"role":"system","content":"hello"}]}`, true, "hello"},
		{"system then user", `{"messages":[{"role":"system","content":"X"},{"role":"user","content":"Y"}]}`, true, "X"},
		{"user then system", `{"messages":[{"role":"user","content":"Y"},{"role":"system","content":"X"}]}`, true, "X"},
		{"no system role", `{"messages":[{"role":"user","content":"hi"}]}`, false, ""},
		{"empty messages", `{"messages":[]}`, false, ""},
		{"no messages field", `{"model":"x"}`, false, ""},
		{"malformed json", `{not json`, false, ""},
		{"content not string", `{"messages":[{"role":"system","content":[{"type":"text","text":"x"}]}]}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractFirstSystemMessageContent([]byte(tc.body))
			if ok != tc.wantOk {
				t.Errorf("ok mismatch: want=%v got=%v", tc.wantOk, ok)
			}
			if got != tc.wantVal {
				t.Errorf("content mismatch: want=%q got=%q", tc.wantVal, got)
			}
		})
	}
}

func TestExtractSessionIDFromOpenClawSystemContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"standard space-pipe-space", "Runtime: agent=main | sessionId=abc-123 | rest", "abc-123"},
		{"without trailing", "sessionId=xyz", "xyz"},
		{"with padding", "   sessionId=padded-id   ", "padded-id"},
		{"no sessionId at all", "no key here", ""},
		{"empty", "", ""},
		{"first occurrence only", "sessionId=first | x | sessionId=second |", "first"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSessionIDFromOpenClawSystemContent(tc.in)
			if got != tc.want {
				t.Errorf("want=%q got=%q", tc.want, got)
			}
		})
	}
}
