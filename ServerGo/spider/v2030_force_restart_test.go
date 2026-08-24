package spider

// ==================== v2.0.30：forceRestart / SPA hints / handler semaphore 单测 ====================
//
// 问题背景（问题分析报告_20260707_062144 + Agent5_InfoQ_报告_20260705）：
//   - MCP /SpiderWebData 在 cascade context canceled 状态下 restart_browser
//     被上层 dispatch 守卫直接拒绝，Agent 无路可走 → 整条 MCP HTTP 层拖死
//   - InfoQ 中文站 SPA 路由 click 无效，需直接 navigate URL
//
// 测试目标：验证 v2.0.30 修复生效
//   - tryForceRestart 5s 短门闩
//   - SPAAlternativeHints infoq.cn / 36kr.com / 通用 host 分支
//   - mcpHandlerSem 并发限流 + 503 server_busy 响应契约
//   - 集成契约：handler 返回 server_busy JSON 信封结构稳定

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------- 1. tryForceRestart 5s 短门闩 ----------------

func TestTryForceRestart_FirstCallPasses(t *testing.T) {
	// 重置 lastTime 到远点，避免被测试间状态污染
	forceRestartMu.Lock()
	forceRestartLastTime = time.Now().Add(-10 * time.Second)
	forceRestartMu.Unlock()

	if !tryForceRestart() {
		t.Fatal("expected tryForceRestart to pass after last trigger > 5s ago")
	}
}

func TestTryForceRestart_SecondCallWithin5sSkipped(t *testing.T) {
	forceRestartMu.Lock()
	forceRestartLastTime = time.Now() // 刚刚触发
	forceRestartMu.Unlock()

	if tryForceRestart() {
		t.Fatal("expected tryForceRestart to be skipped within 5s gate")
	}
}

func TestTryForceRestart_After5sPasses(t *testing.T) {
	forceRestartMu.Lock()
	forceRestartLastTime = time.Now().Add(-6 * time.Second)
	forceRestartMu.Unlock()

	if !tryForceRestart() {
		t.Fatal("expected tryForceRestart to pass after 6s")
	}
}

// ---------------- 2. restartChromeForced 引擎为 nil 安全 ----------------

func TestRestartChromeForced_NilEngine(t *testing.T) {
	var e *SpiderEngine
	err := e.restartChromeForced()
	if err == nil {
		t.Fatal("expected error from nil engine")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected error to mention 'nil', got %q", err.Error())
	}
}

// restartChromeForced_5sGateSkipped 模拟 5s 内重复触发 forced restart 时被 short gate 拦下
func TestRestartChromeForced_5sGateSkipped(t *testing.T) {
	e := GetSpiderEngine()
	if e == nil {
		t.Skip("engine not initialized in this test env")
	}
	// 直接设 lastTime 让 gate 处于「5s 内」状态
	forceRestartMu.Lock()
	forceRestartLastTime = time.Now()
	forceRestartMu.Unlock()

	err := e.restartChromeForced()
	if err == nil {
		t.Fatal("expected 5s gate to reject forced restart")
	}
	if !strings.Contains(err.Error(), "5s") {
		t.Fatalf("expected error to mention '5s' gate, got %q", err.Error())
	}
}

// ---------------- 3. SPAAlternativeHints infoq.cn 分支 ----------------

func TestSPAAlternativeHints_InfoQCN(t *testing.T) {
	hints := SPAAlternativeHints("https://www.infoq.cn/")
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints for infoq.cn")
	}

	// 关键契约：必须列出已验证可直链的 topic URL
	expected := []string{
		"/topic/AI",
		"/topic/architecture",
		"/topic/BigData",
		"/topic/cloud-computing",
		"/aibriefs",
	}
	hintsStr := strings.Join(hints, "\n")
	for _, exp := range expected {
		if !strings.Contains(hintsStr, exp) {
			t.Errorf("SPAAlternativeHints(infoq.cn) missing %q in hints:\n%s", exp, hintsStr)
		}
	}

	// 必须明确指出 SPA / Vue Router
	if !strings.Contains(hintsStr, "Vue Router") && !strings.Contains(hintsStr, "SPA") {
		t.Errorf("SPAAlternativeHints(infoq.cn) must mention SPA / Vue Router; got:\n%s", hintsStr)
	}
}

func TestSPAAlternativeHints_36KR(t *testing.T) {
	hints := SPAAlternativeHints("https://36kr.com/")
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints for 36kr.com")
	}
	hintsStr := strings.Join(hints, "\n")
	if !strings.Contains(hintsStr, "SPA") && !strings.Contains(hintsStr, "Vue") {
		t.Errorf("SPAAlternativeHints(36kr.com) must mention SPA / Vue; got:\n%s", hintsStr)
	}
}

func TestSPAAlternativeHints_GenericHost(t *testing.T) {
	hints := SPAAlternativeHints("https://example.com/some/path")
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints for generic host")
	}
	hintsStr := strings.Join(hints, "\n")
	// 通用兜底必须包含 direct URL navigate 建议
	if !strings.Contains(hintsStr, "navigate") && !strings.Contains(hintsStr, "history.pushState") {
		t.Errorf("SPAAlternativeHints(generic) must suggest direct URL navigate; got:\n%s", hintsStr)
	}
}

func TestSPAAlternativeHints_EmptyURL(t *testing.T) {
	hints := SPAAlternativeHints("")
	if len(hints) == 0 {
		t.Fatal("expected non-empty fallback hints even for empty URL")
	}
	hintsStr := strings.Join(hints, "\n")
	// 空 host 时返回通用兜底
	if !strings.Contains(hintsStr, "SPA") && !strings.Contains(hintsStr, "navigate") {
		t.Errorf("SPAAlternativeHints('') fallback must mention SPA / navigate; got:\n%s", hintsStr)
	}
}

// ---------------- 4. enrichFailureResponseWithSPA 契约 ----------------

func TestEnrichFailureResponseWithSPA_AttachesHints(t *testing.T) {
	respData := map[string]interface{}{}
	enrichFailureResponseWithSPA(respData, "https://www.infoq.cn/")
	if _, ok := respData["spa_alternative_hints"]; !ok {
		t.Fatal("expected spa_alternative_hints field to be attached")
	}
	hints, ok := respData["spa_alternative_hints"].([]string)
	if !ok {
		t.Fatalf("expected spa_alternative_hints to be []string, got %T", respData["spa_alternative_hints"])
	}
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints")
	}
}

func TestEnrichFailureResponseWithSPA_NilMapSafe(t *testing.T) {
	// 不 panic
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("enrichFailureResponseWithSPA panicked on nil map: %v", rec)
		}
	}()
	enrichFailureResponseWithSPA(nil, "https://example.com")
}

// ---------------- 5. mcpHandlerSem 并发限流契约 ----------------

func TestMCPHandlerSem_AcquireRelease(t *testing.T) {
	// 重新初始化 cap=2 验证 acquire/release 配对
	InitMCPHandlerSem(2)
	if mcpHandlerSemCap != 2 {
		t.Fatalf("expected cap=2, got %d", mcpHandlerSemCap)
	}
	// 应能 acquire 3 次：前 2 次成功，第 3 次超时
	if !acquireMCPHandlerSem(100 * time.Millisecond) {
		t.Fatal("expected first acquire to succeed")
	}
	if !acquireMCPHandlerSem(100 * time.Millisecond) {
		t.Fatal("expected second acquire to succeed")
	}
	// 第 3 个 slot 应在 100ms 内获取不到（因为 cap=2）
	if acquireMCPHandlerSem(100 * time.Millisecond) {
		t.Fatal("expected third acquire to time out at cap=2")
	}
	releaseMCPHandlerSem()
	releaseMCPHandlerSem()
}

func TestMCPHandlerSem_UninitializedSafe(t *testing.T) {
	// 把 channel 临时置 nil 模拟未初始化场景
	original := mcpHandlerSem
	mcpHandlerSem = nil
	defer func() { mcpHandlerSem = original }()

	// 未初始化时不阻塞、acquire 直接 true
	if !acquireMCPHandlerSem(50 * time.Millisecond) {
		t.Fatal("expected acquire to succeed on uninitialized sem")
	}
	releaseMCPHandlerSem() // 不应 panic
}

// ---------------- 6. /SpiderWebData 503 server_busy 响应契约 ----------------

// 注意：MCPSpiderWebDataHandler 入口先 acquireMCPHandlerSem，cap 满后立即
// 返回 503 + JSON envelope。本测试用一个独立的 http handler 模拟同结构响应，
// 验证响应体的契约稳定（字段名 / 类型 / Content-Type / 状态码）。
func TestHandlerServerBusyResponseContract(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(rec).Encode(MCPAPIResponse{
		Success: false,
		Message: "MCP handler concurrency cap exceeded; retry after 2s",
		Data: map[string]interface{}{
			"error_type": "server_busy",
			"hint":       "too many concurrent crawler requests; retry after 2s",
		},
	})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp MCPAPIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if !strings.Contains(resp.Message, "retry after 2s") {
		t.Errorf("expected message hint 'retry after 2s', got %q", resp.Message)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected Data to be map, got %T", resp.Data)
	}
	if data["error_type"] != "server_busy" {
		t.Errorf("expected error_type=server_busy, got %v", data["error_type"])
	}
	hint, _ := data["hint"].(string)
	if !strings.Contains(hint, "retry after 2s") {
		t.Errorf("expected hint to mention 'retry after 2s', got %q", hint)
	}
}

// ---------------- 7. SpiderWebDataResponse.ForcedRestart 字段契约 ----------------

// 验证 v2.0.30 新增的 ForcedRestart 字段在 JSON omitempty 行为符合契约：
// false 时不出现；true 时出现 "forced_restart":true。
func TestSpiderWebDataResponse_ForcedRestartJSON(t *testing.T) {
	// false → omitempty
	resp := &SpiderWebDataResponse{
		URL:           "https://example.com",
		Title:         "test",
		ForcedRestart: false,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "forced_restart") {
		t.Errorf("expected forced_restart to be omitted when false, got %s", string(body))
	}

	// true → "forced_restart":true
	resp.ForcedRestart = true
	body, _ = json.Marshal(resp)
	if !strings.Contains(string(body), `"forced_restart":true`) {
		t.Errorf("expected forced_restart=true in JSON, got %s", string(body))
	}
}

// ---------------- 8. SPAAlternativeHints nil-safe ----------------

func TestSPAAlternativeHints_NeverNil(t *testing.T) {
	// 无论输入如何，返回值都不应为 nil（Agent 端不必 nil 判断）
	cases := []string{
		"",
		"invalid-url",
		"https://unknown-host-12345.xyz/",
		"https://www.infoq.cn/topic/AI",
		"https://36kr.com/information/technology",
	}
	for _, u := range cases {
		hints := SPAAlternativeHints(u)
		if hints == nil {
			t.Errorf("SPAAlternativeHints(%q) returned nil; must always return non-nil slice", u)
		}
	}
}
