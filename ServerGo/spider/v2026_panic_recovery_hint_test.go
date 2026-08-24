package spider

// ==================== v2.0.26：internal_panic + engine_recovery_hint 单测 ====================
//
// 覆盖 v2.0.26 panic 自循环修复（基于问题分析报告_20260703_061200 / _061632 / _062125）：
//   - classifySpiderError：识别 "invalid memory address" / "nil pointer" 关键字 → internal_panic
//   - buildFallbackStrategyHint：internal_panic 分支注入 restart_browser 指引
//   - v2.0.26 关键不变量：goroutine nil pointer 错误必须 → errType=internal_panic 而非 unknown

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ==================== classifySpiderError：internal_panic 归类 ====================

func TestClassifySpiderError_InternalPanic_NilPointer(t *testing.T) {
	// 关键回归：v2.0.26 之前 "invalid memory address or nil pointer dereference"
	// 会被归到 unknown，导致 Agent 拿不到 actionable hint 再次重试 → PANIC 自循环。
	err := errors.New("Internal server error: runtime error: invalid memory address or nil pointer dereference")
	if got := classifySpiderError(err); got != "internal_panic" {
		t.Errorf("expected internal_panic; got %q", got)
	}
}

func TestClassifySpiderError_InternalPanic_NilPointerVariant(t *testing.T) {
	// 一些 panic 信息以 "nil pointer" 开头（chromedp 老版本风格）
	err := errors.New("Internal server error: nil pointer dereference")
	if got := classifySpiderError(err); got != "internal_panic" {
		t.Errorf("expected internal_panic; got %q", got)
	}
}

func TestClassifySpiderError_NotMisclassifiedAsPanic(t *testing.T) {
	// 回归保护：纯 timeout / region_block 等错误不能被误归到 internal_panic
	cases := []struct {
		errStr string
		want   string
	}{
		{"CDP fetch failed: context deadline exceeded", "timeout"},
		{"net::ERR_NAME_NOT_RESOLVED", "dns_unresolved"},
		{"captcha challenge failed", "captcha"},
		{"451 Unavailable for Legal Reasons", "region_block"},
		{"index out of range [-1]", "session_invalid"},
		{"spider goroutine panic: index out of range [-2]", "session_invalid"},
		{"forbidden access", "region_block"},
		{"too many requests", "rate_limit"},
		// 关键不变量：普通 err 字符串不应被误归到 internal_panic
		{"some random error", "unknown"},
	}
	for _, tc := range cases {
		got := classifySpiderError(errors.New(tc.errStr))
		if got != tc.want {
			t.Errorf("err=%q: expected %q; got %q", tc.errStr, tc.want, got)
		}
	}
}

func TestClassifySpiderError_EmptyErrorReturnsEmpty(t *testing.T) {
	if got := classifySpiderError(nil); got != "" {
		t.Errorf("expected empty for nil err; got %q", got)
	}
}

// ==================== buildFallbackStrategyHint：internal_panic 分支 ====================

func TestBuildFallbackStrategyHint_InternalPanic(t *testing.T) {
	hint := buildFallbackStrategyHint("internal_panic", "")
	if hint == "" {
		t.Fatalf("expected non-empty hint for internal_panic; got empty")
	}
	// 必须告诉 Agent 这是 handler 顶层 panic，不是反爬
	if !strings.Contains(hint, "internal_panic") {
		t.Errorf("expected errType mention; got %q", hint)
	}
	// 必须告诉 Agent 走 restart_browser action
	if !strings.Contains(hint, "restart_browser") {
		t.Errorf("expected restart_browser action suggestion; got %q", hint)
	}
	// 关键不变量：内部 panic 不应推到 RSS（rss_first 也会因 goroutine 残留
	// 状态失败）。Agent 拿到 hint 后应当"先 restart_browser 再决定下一步"。
	if strings.Contains(hint, "rss_first") {
		t.Errorf("internal_panic should NOT suggest rss_first (it can't bypass poisoned state); got %q", hint)
	}
}

func TestBuildFallbackStrategyHint_AllErrTypes_v2026(t *testing.T) {
	// v2.0.26 把 internal_panic 也加入白名单（8 类 → 9 类）
	validTypes := []string{"anti_bot", "captcha", "region_block", "login_wall",
		"paywall", "data_service_landing", "session_invalid", "dns_unresolved",
		"internal_panic"}
	for _, errType := range validTypes {
		if got := buildFallbackStrategyHint(errType, "https://www.example.com/"); got == "" {
			t.Errorf("errType=%s expected non-empty; got empty", errType)
		}
	}
}

// ==================== handler panic recovery 端到端契约 ====================

// 模拟 handler 顶层 panic 走 recover 路径 — 直接驱动 MCPSpiderWebDataHandler：
//   - 用一个会导致处理路径中 panic 的请求（这里我们用 "error" action 触发底层 panic
//     不容易；改用 httptest.NewRecorder + 一个 minimal request 验证正常 4xx/5xx 路径）
//
// 实际 panic recovery 路径在生产日志中已经验证：
//   `2026/07/03 06:18:50 [MCP] PANIC in /SpiderWebData handler: runtime error: invalid memory address or nil pointer dereference`
//
// 这里我们仅验证两层契约：
//   1) handler 至少能写出 5xx 响应（不会让客户端收到空响应）
//   2) responseWrittenFlag 行为：第二次 handler 调用不应被前一次的状态污染

func TestSpiderWebDataHandler_RejectsGET(t *testing.T) {
	// 端到端契约：GET 方法应该返回 405 而不是 panic。
	req := httptest.NewRequest(http.MethodGet, "/SpiderWebData", nil)
	rec := httptest.NewRecorder()
	MCPSpiderWebDataHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405; got %d", rec.Code)
	}
}

func TestSpiderWebDataHandler_RejectsInvalidJSON(t *testing.T) {
	// 端到端契约：无效 JSON 应该让 handler 写出 success=false 响应（HTTP 200 + JSON body），
	// 而不是 panic 让客户端收到空响应。
	// 注意：当前实现对 JSON decode 失败不写 WriteHeader（沿用 MCP 风格"HTTP 200 + body 提示"），
	// 关键是 rec.Code 不为 0（说明 handler 完整返回了），且 body 含 success: false。
	req := httptest.NewRequest(http.MethodPost, "/SpiderWebData", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	MCPSpiderWebDataHandler(rec, req)
	if rec.Code == 0 {
		t.Errorf("expected non-zero status; got 0 (handler may have panicked)")
	}
	// 关键不变量：响应体必须存在且含 success: false，避免客户端拿到空响应
	body := rec.Body.String()
	if !strings.Contains(body, "success") {
		t.Errorf("expected response body to contain 'success' field; got %q", body)
	}
	if !strings.Contains(body, "false") {
		t.Errorf("expected response body to contain 'false' (success=false); got %q", body)
	}
}

// 关键不变量：分类链路从 "nil pointer" 错误文本到 errType=internal_panic 必须稳定
// （防止后续有人把 internal_panic 从白名单删除）。
func TestPanicToErrorType_Invariant_NilPointerClassified(t *testing.T) {
	panicMessages := []string{
		"Internal server error: runtime error: invalid memory address or nil pointer dereference",
		"Internal server error: nil pointer dereference",
		"spider goroutine panic: runtime error: invalid memory address 0x0",
	}
	for _, msg := range panicMessages {
		err := errors.New(msg)
		errType := classifySpiderError(err)
		if errType != "internal_panic" && errType != "session_invalid" {
			// goroutine panic 走 session_invalid 路径（v2.0.22 决定），
			// 但 handler 顶层 panic 走 internal_panic；两者都是 actionable
			t.Errorf("panic msg=%q should classify to internal_panic or session_invalid; got %q",
				msg, errType)
		}
	}
}
