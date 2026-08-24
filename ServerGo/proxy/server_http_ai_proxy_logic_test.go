package proxy

// 代理核心逻辑测试（迁移自旧工程 test_proxy_logic_test.go）
// 覆盖 extractAPIKey / parseModelFromBody / replaceModelInBody / getRelativePath /
// buildProtocolAwareTargetURL / isValidProtocol / validateRequestModelName。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
)

// TestExtractAPIKey 测试从 Authorization Header 提取 API Key
func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name      string
		auth      string
		wantKey   string
		wantError bool
	}{
		{"valid bearer", "Bearer sk-test123", "sk-test123", false},
		{"valid bearer lowercase", "bearer sk-test456", "sk-test456", false},
		{"missing header", "", "", true},
		{"invalid format no space", "Bearer-sk-test", "", true},
		{"invalid format no bearer", "Basic sk-test", "", true},
		{"empty key", "Bearer ", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/Anthropic/v1/messages", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			key, err := extractAPIKey(req)
			if tt.wantError {
				if err == nil {
					t.Errorf("extractAPIKey() expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("extractAPIKey() unexpected error: %v", err)
				return
			}
			if key != tt.wantKey {
				t.Errorf("extractAPIKey() = %q, want %q", key, tt.wantKey)
			}
		})
	}
}

// TestParseModelFromBody 测试从请求体解析模型名称
func TestParseModelFromBody(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantModel string
		wantError bool
	}{
		{"valid model", `{"model":"claude-3-5-sonnet","messages":[]}`, "claude-3-5-sonnet", false},
		{"empty model", `{"model":"","messages":[]}`, "", true},
		{"missing model", `{"messages":[]}`, "", true},
		{"invalid json", `not json`, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, err := parseModelFromBody([]byte(tt.body))
			if tt.wantError {
				if err == nil {
					t.Errorf("parseModelFromBody() expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("parseModelFromBody() unexpected error: %v", err)
				return
			}
			if model != tt.wantModel {
				t.Errorf("parseModelFromBody() = %q, want %q", model, tt.wantModel)
			}
		})
	}
}

// jsonEqual 比较两个 JSON 字符串是否等价（忽略 key 顺序）
func jsonEqual(a, b string) bool {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return a == b
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	aj, _ := json.Marshal(av)
	bj, _ := json.Marshal(bv)
	return bytes.Equal(aj, bj)
}

// TestReplaceModelInBody 测试替换请求体中的模型名称
func TestReplaceModelInBody(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		oldModel string
		newModel string
		want     string
	}{
		{"replace model", `{"model":"user-model","messages":[]}`, "user-model", "dst-model", `{"model":"dst-model","messages":[]}`},
		{"same model no change", `{"model":"same","messages":[]}`, "same", "same", `{"model":"same","messages":[]}`},
		{"invalid json passthrough", `not json`, "old", "new", `not json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceModelInBody([]byte(tt.body), tt.oldModel, tt.newModel)
			if !jsonEqual(string(got), tt.want) {
				t.Errorf("replaceModelInBody() = %q, want %q", string(got), tt.want)
			}
		})
	}
}

// TestGetRelativePath 测试提取相对路径
func TestGetRelativePath(t *testing.T) {
	// 初始化全局配置，避免 nil 指针（getRelativePath 读取监听 URL 前缀）
	config.G = config.GetDefaultConfig()
	tests := []struct {
		name         string
		path         string
		protocolType int
		want         string
	}{
		{"anthropic with path", "/Anthropic/v1/messages", protocol.AgentProtocolType_Anthropic, "/v1/messages"},
		{"anthropic bare", "/Anthropic", protocol.AgentProtocolType_Anthropic, "/"},
		{"openai with path", "/OpenAI/v1/chat/completions", protocol.AgentProtocolType_OpenAI, "/v1/chat/completions"},
		{"openai bare", "/OpenAI", protocol.AgentProtocolType_OpenAI, "/"},
		{"other path", "/other/path", protocol.AgentProtocolType_Anthropic, "/other/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getRelativePath(tt.path, tt.protocolType)
			if got != tt.want {
				t.Errorf("getRelativePath(%q, %d) = %q, want %q", tt.path, tt.protocolType, got, tt.want)
			}
		})
	}
}

func TestBuildProtocolAwareTargetURL(t *testing.T) {
	tests := []struct {
		name                  string
		dstURL                string
		relativePath          string
		rawQuery              string
		srcProtocolType       int
		dstProtocolType       int
		endpointAlgorithmType int
		wantPath              string
		wantQuery             map[string]string
	}{
		{
			name:                  "openai chat completions to anthropic messages",
			dstURL:                "https://api.kimi.com/coding",
			relativePath:          "/chat/completions",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_ProtocolConverter,
			wantPath:              "/coding/v1/messages",
		},
		{
			name:                  "openai v1 chat completions to anthropic messages",
			dstURL:                "https://api.kimi.com/coding",
			relativePath:          "/v1/chat/completions",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_ProtocolConverter,
			wantPath:              "/coding/v1/messages",
		},
		{
			name:                  "anthropic messages to openai chat completions",
			dstURL:                "https://api.example.com",
			relativePath:          "/v1/messages",
			srcProtocolType:       protocol.AgentProtocolType_Anthropic,
			dstProtocolType:       protocol.AgentProtocolType_OpenAI,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_ProtocolConverter,
			wantPath:              "/v1/chat/completions",
		},
		{
			name:                  "endpoint and request query are merged",
			dstURL:                "https://api.kimi.com/coding?beta=true",
			relativePath:          "/chat/completions",
			rawQuery:              "timeout=60",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_ProtocolConverter,
			wantPath:              "/coding/v1/messages",
			wantQuery:             map[string]string{"beta": "true", "timeout": "60"},
		},
		{
			name:                  "unknown converter path unchanged",
			dstURL:                "https://api.example.com/base",
			relativePath:          "/models",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_ProtocolConverter,
			wantPath:              "/base/models",
		},
		{
			name:                  "direct endpoint keeps original path",
			dstURL:                "https://api.example.com/base",
			relativePath:          "/chat/completions",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_Direct,
			wantPath:              "/base/chat/completions",
		},
		{
			name:                  "canonical endpoint path is not duplicated",
			dstURL:                "https://api.kimi.com/coding/v1/messages",
			relativePath:          "/chat/completions",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_ProtocolConverter,
			wantPath:              "/coding/v1/messages",
		},
		// v2.0.20 路径去重：basePath 末段 /v1 + relativePath /v1/* 不应重复 /v1
		{
			name:                  "openai responses with v1 endpoint path not duplicated",
			dstURL:                "https://api.longcat.chat/openai/v1",
			relativePath:          "/v1/responses",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_OpenAI,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_Direct,
			wantPath:              "/openai/v1/responses",
		},
		{
			name:                  "openai chat completions with v1 endpoint path not duplicated",
			dstURL:                "https://api.example.com/openai/v1",
			relativePath:          "/v1/chat/completions",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_OpenAI,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_Direct,
			wantPath:              "/openai/v1/chat/completions",
		},
		{
			name:                  "anthropic v1 basePath with v1 messages not duplicated",
			dstURL:                "https://api.anthropic.com/v1",
			relativePath:          "/v1/messages",
			srcProtocolType:       protocol.AgentProtocolType_Anthropic,
			dstProtocolType:       protocol.AgentProtocolType_Anthropic,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_Direct,
			wantPath:              "/v1/messages",
		},
		{
			name:                  "v2 basePath keeps v1 prefix in relativePath",
			dstURL:                "https://api.example.com/openai/v2",
			relativePath:          "/v1/responses",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_OpenAI,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_Direct,
			wantPath:              "/openai/v2/v1/responses",
		},
		{
			name:                  "no v1 in basePath keeps relative v1 prefix",
			dstURL:                "https://api.example.com/openai",
			relativePath:          "/v1/responses",
			srcProtocolType:       protocol.AgentProtocolType_OpenAI,
			dstProtocolType:       protocol.AgentProtocolType_OpenAI,
			endpointAlgorithmType: modelsdb.DstEndPointAlgorithmType_Direct,
			wantPath:              "/openai/v1/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildProtocolAwareTargetURL(tt.dstURL, tt.relativePath, tt.rawQuery, tt.srcProtocolType, tt.dstProtocolType, tt.endpointAlgorithmType)
			if err != nil {
				t.Fatalf("buildProtocolAwareTargetURL() error: %v", err)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse target url %q: %v", got, err)
			}
			if parsed.Path != tt.wantPath {
				t.Fatalf("target path = %q, want %q (url=%s)", parsed.Path, tt.wantPath, got)
			}
			for key, wantValue := range tt.wantQuery {
				if gotValue := parsed.Query().Get(key); gotValue != wantValue {
					t.Fatalf("query %s = %q, want %q (url=%s)", key, gotValue, wantValue, got)
				}
			}
		})
	}
}

// TestIsValidProtocol 测试协议验证
func TestIsValidProtocol(t *testing.T) {
	anthropicUser := &modelsdb.TAgentHttpUserInfo{AnthropicEnabled: true, OpenAIEnabled: false}
	openAIUser := &modelsdb.TAgentHttpUserInfo{AnthropicEnabled: false, OpenAIEnabled: true}
	bothUser := &modelsdb.TAgentHttpUserInfo{AnthropicEnabled: true, OpenAIEnabled: true}
	noneUser := &modelsdb.TAgentHttpUserInfo{AnthropicEnabled: false, OpenAIEnabled: false}

	tests := []struct {
		name         string
		user         *modelsdb.TAgentHttpUserInfo
		protocolType int
		want         bool
	}{
		{"anthropic enabled", anthropicUser, protocol.AgentProtocolType_Anthropic, true},
		{"anthropic disabled", openAIUser, protocol.AgentProtocolType_Anthropic, false},
		{"openai enabled", openAIUser, protocol.AgentProtocolType_OpenAI, true},
		{"openai disabled", anthropicUser, protocol.AgentProtocolType_OpenAI, false},
		{"both enabled anthropic", bothUser, protocol.AgentProtocolType_Anthropic, true},
		{"both enabled openai", bothUser, protocol.AgentProtocolType_OpenAI, true},
		{"none anthropic", noneUser, protocol.AgentProtocolType_Anthropic, false},
		{"none openai", noneUser, protocol.AgentProtocolType_OpenAI, false},
		{"invalid type", bothUser, 99, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidProtocol(tt.user, tt.protocolType)
			if got != tt.want {
				t.Errorf("isValidProtocol() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateRequestModelName 测试请求 model 名称与 API Key 对应模型的匹配校验
func TestValidateRequestModelName(t *testing.T) {
	tests := []struct {
		name              string
		reqModelName      string
		expectedModelName string
		wantError         bool
	}{
		{"matching model", "liusm191-ai-model", "liusm191-ai-model", false},
		{"mismatched model", "wrong-model", "liusm191-ai-model", true},
		{"empty request model", "", "liusm191-ai-model", true},
		{"empty expected model", "liusm191-ai-model", "", true},
		{"case sensitive mismatch", "Liusm191-ai-model", "liusm191-ai-model", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequestModelName(tt.reqModelName, tt.expectedModelName)
			if tt.wantError {
				if err == nil {
					t.Errorf("validateRequestModelName() expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("validateRequestModelName() unexpected error: %v", err)
			}
		})
	}
}
