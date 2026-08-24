package system

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
)

// readReqBody 读取 httptest 收到的请求体
func readReqBody(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// TestDstEndPointConnectivityWithResult_AnthropicFailure 验证 Anthropic 协议失败时
// TestResult 携带完整的请求/响应信息（URL / 请求头 / 请求体 / 响应头 / 响应体 / 状态码），
// 这是"添加源站失败时展示完整排查信息"的核心诉求。
func TestDstEndPointConnectivityWithResult_AnthropicFailure(t *testing.T) {
	var gotAPIKey, gotBody, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotBody = readReqBody(r)
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-abc-123")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		UserID:       1,
		PlatformName: "Anthropic",
		ModelName:    "claude-3-5-sonnet",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "sk-secret-anthropic-key",
	}

	result := TestDstEndPointConnectivityWithResult(ep, "", 0)

	if result.Success {
		t.Fatalf("expected failure, got success; message=%s", result.Message)
	}
	if result.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode=%d, want 401", result.StatusCode)
	}
	if result.Message == "" {
		t.Error("Message should not be empty on failure")
	}
	// 请求信息完整
	if !strings.HasSuffix(result.RequestURL, "/v1/messages") {
		t.Errorf("RequestURL=%q, want suffix /v1/messages", result.RequestURL)
	}
	if result.RequestBody == "" || !strings.Contains(result.RequestBody, "claude-3-5-sonnet") {
		t.Errorf("RequestBody missing model: %q", result.RequestBody)
	}
	if result.RequestHeaders == "" {
		t.Error("RequestHeaders should not be empty")
	}
	if !strings.Contains(result.RequestHeaders, "Anthropic-Version") && !strings.Contains(result.RequestHeaders, "anthropic-version") {
		t.Errorf("RequestHeaders should carry anthropic-version: %q", result.RequestHeaders)
	}
	// 响应信息完整
	if result.ResponseBody == "" || !strings.Contains(result.ResponseBody, "invalid x-api-key") {
		t.Errorf("ResponseBody missing error detail: %q", result.ResponseBody)
	}
	if result.ResponseHeaders == "" || !strings.Contains(result.ResponseHeaders, "X-Request-Id") {
		t.Errorf("ResponseHeaders missing server headers: %q", result.ResponseHeaders)
	}
	// 安全回归：请求头中 API Key 必须被掩码，禁止明文回传前端
	if strings.Contains(result.RequestHeaders, "sk-secret-anthropic-key") {
		t.Errorf("RequestHeaders leaked plaintext API key: %q", result.RequestHeaders)
	}
	// 假源站确实收到了真实 key + 正确 path（掩码只发生在展示层）
	if gotAPIKey != "sk-secret-anthropic-key" {
		t.Errorf("server got x-api-key=%q, want real key", gotAPIKey)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("server path=%q, want /v1/messages", gotPath)
	}
	if !strings.Contains(gotBody, "claude-3-5-sonnet") {
		t.Errorf("server body missing model: %q", gotBody)
	}
}

// TestDstEndPointConnectivityWithResult_OpenAIFailure 验证 OpenAI 协议失败时的完整信息 + 掩码。
func TestDstEndPointConnectivityWithResult_OpenAIFailure(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"model not found","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		UserID:       2,
		PlatformName: "OpenAI",
		ModelName:    "gpt-4o",
		ProtocolType: protocol.AgentProtocolType_OpenAI,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "sk-secret-openai-key",
	}

	result := TestDstEndPointConnectivityWithResult(ep, "", 0)

	if result.Success {
		t.Fatalf("expected failure, got success; message=%s", result.Message)
	}
	if result.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode=%d, want 400", result.StatusCode)
	}
	if !strings.HasSuffix(result.RequestURL, "/chat/completions") {
		t.Errorf("RequestURL=%q, want suffix /chat/completions", result.RequestURL)
	}
	if !strings.Contains(result.ResponseBody, "model not found") {
		t.Errorf("ResponseBody missing error: %q", result.ResponseBody)
	}
	if result.RequestHeaders == "" {
		t.Error("RequestHeaders should not be empty")
	}
	// 掩码：Authorization 保留 "Bearer " 前缀但掩码 token
	if strings.Contains(result.RequestHeaders, "sk-secret-openai-key") {
		t.Errorf("RequestHeaders leaked plaintext API key: %q", result.RequestHeaders)
	}
	if !strings.Contains(strings.ToLower(result.RequestHeaders), "authorization: bearer") {
		t.Errorf("RequestHeaders should keep 'Authorization: Bearer' prefix: %q", result.RequestHeaders)
	}
	// 假源站收到真实 Bearer token
	if gotAuth != "Bearer sk-secret-openai-key" {
		t.Errorf("server got Authorization=%q, want real bearer", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("server path=%q, want /v1/chat/completions", gotPath)
	}
}

// TestDstEndPointConnectivityWithResult_Success 验证成功场景字段齐全。
func TestDstEndPointConnectivityWithResult_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"你好"}]}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		UserID:       1,
		ModelName:    "claude-3-5-sonnet",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "sk-ok",
	}

	result := TestDstEndPointConnectivityWithResult(ep, "", 0)
	if !result.Success {
		t.Fatalf("expected success, got failure: %s", result.Message)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode=%d, want 200", result.StatusCode)
	}
	if result.RequestHeaders == "" || result.RequestBody == "" || result.ResponseHeaders == "" || result.ResponseBody == "" {
		t.Errorf("all fields should be populated on success: %+v", result)
	}
}

// TestDstEndPointConnectivityWithResult_ParamValidation 验证参数缺失时不发请求、返回明确 message，
// 且不带请求/响应详情（前端此时回退到小红条展示）。
func TestDstEndPointConnectivityWithResult_ParamValidation(t *testing.T) {
	cases := []struct {
		name string
		ep   *modelsdb.TAgentDstEndPoint
		want string
	}{
		{"empty-url", &modelsdb.TAgentDstEndPoint{ProtocolType: protocol.AgentProtocolType_OpenAI, APIKey: "k", ModelName: "m"}, "URL 地址为空"},
		{"empty-key", &modelsdb.TAgentDstEndPoint{ProtocolType: protocol.AgentProtocolType_OpenAI, URLAddress: "http://x", ModelName: "m"}, "API Key 为空"},
		{"empty-model", &modelsdb.TAgentDstEndPoint{ProtocolType: protocol.AgentProtocolType_OpenAI, URLAddress: "http://x", APIKey: "k"}, "模型名称为空"},
		{"nil", nil, "endpoint is nil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := TestDstEndPointConnectivityWithResult(c.ep, "", 0)
			if result.Success {
				t.Fatalf("expected failure for %s", c.name)
			}
			if !strings.Contains(result.Message, c.want) {
				t.Errorf("Message=%q, want contains %q", result.Message, c.want)
			}
			if result.RequestURL != "" || result.ResponseBody != "" {
				t.Errorf("validation failure should not carry request/response detail: %+v", result)
			}
		})
	}
}

// TestFormatHeadersForDisplay 单测掩码 helper。
func TestFormatHeadersForDisplay(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer sk-abc-123")
	h.Set("X-Api-Key", "sk-xyz-789")
	h.Set("Anthropic-Version", "2023-06-01")

	out := formatHeadersForDisplay(h)

	if strings.Contains(out, "sk-abc-123") || strings.Contains(out, "sk-xyz-789") {
		t.Errorf("output leaked secrets: %q", out)
	}
	if !strings.Contains(out, "Authorization: Bearer ") {
		t.Errorf("should keep Bearer prefix: %q", out)
	}
	if !strings.Contains(out, "Content-Type: application/json") {
		t.Errorf("non-sensitive header should be intact: %q", out)
	}
	if !strings.Contains(out, "Anthropic-Version: 2023-06-01") {
		t.Errorf("anthropic-version should be intact: %q", out)
	}
	if formatHeadersForDisplay(http.Header{}) != "" {
		t.Error("empty header should produce empty string")
	}
}

// TestTestResultJSONFields 验证新增字段的 JSON tag 正确（前端依赖 request_headers / response_headers）。
func TestTestResultJSONFields(t *testing.T) {
	tr := &TestResult{
		RequestHeaders:  "Authorization: Bearer ****",
		ResponseHeaders: "Content-Type: application/json",
	}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"request_headers"`) {
		t.Errorf("missing request_headers json tag: %s", s)
	}
	if !strings.Contains(s, `"response_headers"`) {
		t.Errorf("missing response_headers json tag: %s", s)
	}
}

// TestNormalizeAPIKey 验证 API Key 归一化：兼容用户从请求头整行粘贴的多种前缀形态。
func TestNormalizeAPIKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "sk-xxx", "sk-xxx"},
		{"bearer", "Bearer sk-xxx", "sk-xxx"},
		{"bearer-lower", "bearer sk-xxx", "sk-xxx"},
		{"authorization-bearer", "Authorization: Bearer sk-xxx", "sk-xxx"},
		{"authorization-bearer-nospace", "authorization:Bearer sk-xxx", "sk-xxx"},
		{"x-api-key", "x-api-key: sk-xxx", "sk-xxx"},
		{"x-api-key-bearer", "x-api-key: Bearer sk-xxx", "sk-xxx"},
		{"api-key", "api-key: sk-xxx", "sk-xxx"},
		{"surrounding-space", "  sk-xxx  ", "sk-xxx"},
		{"empty", "", ""},
		// v2.0.24：用户把前端脱敏展示值（纯星号）复制回 API Key 框的场景，归一化后应为空
		{"mask-bare", "************************", ""},
		{"mask-authorization-bearer", "Authorization: Bearer ************************", ""},
		{"mask-x-api-key", "x-api-key: ************************", ""},
		{"mask-short", "****", ""},
		{"real-with-star-inside", "sk-a*b", "sk-a*b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeAPIKey(c.in); got != c.want {
				t.Errorf("normalizeAPIKey(%q)=%q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestDstEndPointConnectivity_AnthropicStripsBearerPrefix 验证 Anthropic 协议在用户把
// "Authorization: Bearer sk-real" 整行粘进 API Key 框时，实际发出的 x-api-key 是纯 token。
func TestDstEndPointConnectivity_AnthropicStripsBearerPrefix(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		ModelName:    "claude-3-5-sonnet",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "Authorization: Bearer sk-real",
	}

	result := TestDstEndPointConnectivityWithResult(ep, "", 0)
	if !result.Success {
		t.Fatalf("expected success, got failure: %s", result.Message)
	}
	if gotAPIKey != "sk-real" {
		t.Errorf("server got x-api-key=%q, want %q (prefix must be stripped)", gotAPIKey, "sk-real")
	}
}

// TestDstEndPointConnectivity_OpenAIStripsBearerPrefix 验证 OpenAI 协议在同样的整行粘贴场景下，
// 实际发出的 Authorization 是 "Bearer sk-real"，不会叠加成 "Bearer Authorization: Bearer sk-real"。
func TestDstEndPointConnectivity_OpenAIStripsBearerPrefix(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"cmpl_1","choices":[]}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		ModelName:    "gpt-4o",
		ProtocolType: protocol.AgentProtocolType_OpenAI,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "Authorization: Bearer sk-real",
	}

	result := TestDstEndPointConnectivityWithResult(ep, "", 0)
	if !result.Success {
		t.Fatalf("expected success, got failure: %s", result.Message)
	}
	if gotAuth != "Bearer sk-real" {
		t.Errorf("server got Authorization=%q, want %q (no prefix stacking)", gotAuth, "Bearer sk-real")
	}
}

// TestDstEndPointConnectivity_RejectsMaskedAPIKey 验证用户把前端脱敏展示的星号值
// （如 "Authorization: Bearer ************************"）复制回 API Key 框再保存时，
// 归一化后为空，连通性测试直接以「API Key 为空」失败，而不是拿星号去打源站导致 401。
func TestDstEndPointConnectivity_RejectsMaskedAPIKey(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cases := []struct {
		name     string
		protocol int
		apiKey   string
	}{
		{"anthropic-authorization-bearer-mask", protocol.AgentProtocolType_Anthropic, "Authorization: Bearer ************************"},
		{"anthropic-bare-mask", protocol.AgentProtocolType_Anthropic, "************************"},
		{"openai-authorization-bearer-mask", protocol.AgentProtocolType_OpenAI, "Authorization: Bearer ************************"},
		{"openai-x-api-key-mask", protocol.AgentProtocolType_OpenAI, "x-api-key: ************************"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached = false
			ep := &modelsdb.TAgentDstEndPoint{
				ModelName:    "m",
				ProtocolType: c.protocol,
				URLAddress:   srv.URL + "/v1",
				APIKey:       c.apiKey,
			}
			result := TestDstEndPointConnectivityWithResult(ep, "", 0)
			if result.Success {
				t.Fatalf("expected failure for masked API Key, got success")
			}
			if result.Message != "API Key 为空" {
				t.Errorf("Message=%q, want %q", result.Message, "API Key 为空")
			}
			if reached {
				t.Errorf("masked API Key must not reach upstream, but request was sent")
			}
		})
	}
}

// TestResolveAuthHeaders 单元覆盖 resolveAuthHeaders：协议默认 / 强制 x-api-key / 强制 Bearer。
func TestResolveAuthHeaders(t *testing.T) {
	cases := []struct {
		name      string
		proto     int
		auth      int
		key       string
		wantName  string
		wantValue string
	}{
		{"anthropic-default", protocol.AgentProtocolType_Anthropic, 0, "sk-1", "x-api-key", "sk-1"},
		{"openai-default", protocol.AgentProtocolType_OpenAI, 0, "sk-1", "Authorization", "Bearer sk-1"},
		{"anthropic-force-x-api-key", protocol.AgentProtocolType_Anthropic, 1, "sk-1", "x-api-key", "sk-1"},
		{"openai-force-x-api-key", protocol.AgentProtocolType_OpenAI, 1, "sk-1", "x-api-key", "sk-1"},
		{"anthropic-force-bearer", protocol.AgentProtocolType_Anthropic, 2, "sk-1", "Authorization", "Bearer sk-1"},
		{"openai-force-bearer", protocol.AgentProtocolType_OpenAI, 2, "sk-1", "Authorization", "Bearer sk-1"},
		{"unknown-auth-falls-back-default", protocol.AgentProtocolType_Anthropic, 99, "sk-1", "x-api-key", "sk-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotValue := resolveAuthHeaders(c.proto, c.auth, c.key)
			if gotName != c.wantName || gotValue != c.wantValue {
				t.Errorf("resolveAuthHeaders(%d,%d,%q)=(%q,%q), want (%q,%q)",
					c.proto, c.auth, c.key, gotName, gotValue, c.wantName, c.wantValue)
			}
		})
	}
}

// TestDstEndPointConnectivity_AuthTypeForcesBearer 验证 LongCat 场景：Anthropic 协议路径
// + AuthType=2（强制 Authorization Bearer）能命中 200 成功路径。这是 v2.0.27 的根因修复。
func TestDstEndPointConnectivity_AuthTypeForcesBearer(t *testing.T) {
	var gotAuth, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		ModelName:    "m",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "ak-test",
		AuthType:     2, // 强制 Authorization Bearer
	}
	result := TestDstEndPointConnectivityWithResult(ep, "", 0)
	if !result.Success {
		t.Fatalf("expected success, got failure: %s", result.Message)
	}
	if gotAuth != "Bearer ak-test" {
		t.Errorf("expected Authorization=Bearer ak-test, got %q (x-api-key was %q)", gotAuth, gotAPIKey)
	}
	if gotAPIKey != "" {
		t.Errorf("x-api-key must NOT be sent when AuthType=2, got %q", gotAPIKey)
	}
}

// TestDstEndPointConnectivity_AuthTypeDefaultsStillXAPIKey 回归保护：AuthType=0（默认）
// 仍走协议默认（Anthropic→x-api-key）。
func TestDstEndPointConnectivity_AuthTypeDefaultsStillXAPIKey(t *testing.T) {
	var gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_1","content":[]}`))
	}))
	defer srv.Close()

	ep := &modelsdb.TAgentDstEndPoint{
		ModelName:    "m",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   srv.URL + "/v1",
		APIKey:       "ak-default",
		AuthType:     0,
	}
	result := TestDstEndPointConnectivityWithResult(ep, "", 0)
	if !result.Success {
		t.Fatalf("expected success, got failure: %s", result.Message)
	}
	if gotAPIKey != "ak-default" {
		t.Errorf("expected x-api-key=ak-default, got %q", gotAPIKey)
	}
	if gotAuth != "" {
		t.Errorf("Authorization must NOT be set when AuthType=0 + Anthropic, got %q", gotAuth)
	}
}
