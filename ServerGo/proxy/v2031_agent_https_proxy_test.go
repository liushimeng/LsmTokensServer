package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// v2.0.31: AI 代理 HTTPS 监听端口（proxy 侧）。
// 覆盖 buildAIProxyMux 路由表（Anthropic/OpenAI 命中、未知路径 404、CORS 预检）与 Stop 幂等。

func TestBuildAIProxyMux_Routes(t *testing.T) {
	cfg := config.GetDefaultConfig()
	mux := buildAIProxyMux(cfg)
	if mux == nil {
		t.Fatal("buildAIProxyMux returned nil mux")
	}

	// Anthropic / OpenAI 路径应命中非空 handler（非 catch-all）
	anthropicReq := httptest.NewRequest(http.MethodPost, "/"+cfg.AgentAnthropicListenURL+"/v1/messages", nil)
	openaiReq := httptest.NewRequest(http.MethodPost, "/"+cfg.AgentOpenAIListenURL+"/v1/chat/completions", nil)
	if h, _ := mux.Handler(anthropicReq); h == nil {
		t.Fatal("no handler registered for Anthropic path")
	}
	if h, _ := mux.Handler(openaiReq); h == nil {
		t.Fatal("no handler registered for OpenAI path")
	}

	// 兜底路径返回 404 + 提示文案，且处理 CORS 预检
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown-path", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Not Found") {
		t.Fatalf("unknown path body = %q, want contains 'Not Found'", body)
	}

	// CORS 预检 → 204
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodOptions, "/whatever", nil))
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS preflight status = %d, want 204", rec2.Code)
	}
}

func TestStopAIProxyService_NilSafe(t *testing.T) {
	// 未启动任何 server 时调用 Stop 不应 panic（HTTP + HTTPS 均为 nil）
	aiProxyMutex.Lock()
	aiProxyServer = nil
	aiProxyTLSServer = nil
	aiProxyMutex.Unlock()
	StopAIProxyService()
}
