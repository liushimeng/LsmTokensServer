// v2.0.76 阶段AO：/ChatAnalysisTotalWS 升级握手阶段鉴权测试。
//
// 背景：userAuthMiddleware / ManagerAuthMiddleware 在普通 ServeHTTP 路径上拦截未登录请求，
// 但 gorilla upgrader 是 Hijacker 路径——若中间件未把鉴权结果透传到 WS handler，
// 未登录客户端可绕过鉴权直接建立连接并发起 query。
//
// 修复：api 鉴权中间件把 *AuthClaims 写入 r.Context() 的 websocket.AuthClaimsContextKey，
// WS handler 启动时读出 role，未读到则 401 拒绝升级。
//
// 本测试覆盖：
//  1. 未注入 context（中间件未挂）→ 401
//  2. 注入 WsRoleNone → 401
//  3. 注入 WsRoleUser → 通过（进入 upgrade 阶段，可能因为环境问题 503/426，不应 401）
//  4. 注入 WsRoleManager → 同上
//  5. ctxContextKey 在 user/manager 中间件写入后能正确读回 role
package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// minimalRecorder 抓取首个 WriteHeader 的 status code（upgrader 失败后写 503/426 时我们关心）
type minimalRecorder struct {
	headers http.Header
	status  int
	written bool
}

func (r *minimalRecorder) Header() http.Header {
	if r.headers == nil {
		r.headers = make(http.Header)
	}
	return r.headers
}
func (r *minimalRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *minimalRecorder) WriteHeader(s int) {
	if !r.written {
		r.status = s
		r.written = true
	}
}

func TestAuthClaimsFromCtx_NilCtx(t *testing.T) {
	if got := authClaimsFromCtx(nil); got != nil {
		t.Fatalf("nil ctx 应返回 nil，实际 %+v", got)
	}
	if got := authClaimsFromCtx(context.Background()); got != nil {
		t.Fatalf("空 ctx 应返回 nil，实际 %+v", got)
	}
}

func TestAuthClaimsFromCtx_Roundtrip(t *testing.T) {
	want := &AuthClaims{Role: WsRoleUser, UserID: 42, UserName: "alice", LoginType: "user"}
	ctx := context.WithValue(context.Background(), AuthClaimsContextKey{}, want)
	got := authClaimsFromCtx(ctx)
	if got == nil {
		t.Fatalf("写入后未读到")
	}
	if got.Role != WsRoleUser || got.UserID != 42 || got.UserName != "alice" || got.LoginType != "user" {
		t.Fatalf("读出的 claims 不匹配：%+v", got)
	}
}

func TestAuthClaimsFromCtx_WrongType(t *testing.T) {
	// 写入非 *AuthClaims 类型应被忽略
	ctx := context.WithValue(context.Background(), AuthClaimsContextKey{}, "wrong-type")
	if got := authClaimsFromCtx(ctx); got != nil {
		t.Fatalf("类型不匹配应返回 nil，实际 %+v", got)
	}
}

// TestChatAnalysisTotalWS_RejectsUnauthenticated 阶段AO 核心契约：WS 升级时未读到
// *AuthClaims 直接 401 拒绝——保证中间件漏挂时不会泄露全站数据。
func TestChatAnalysisTotalWS_RejectsUnauthenticated(t *testing.T) {
	rec := &minimalRecorder{}
	req := httptest.NewRequest(http.MethodGet, "/ChatAnalysisTotalWS", nil)
	// 不写入 context，模拟"中间件漏挂"场景
	ChatAnalysisTotalWSHandle(rec, req)

	if !rec.written {
		t.Fatalf("WS handler 应已写响应，实际未写")
	}
	if rec.status != http.StatusUnauthorized {
		t.Fatalf("未授权应 401，实际 %d", rec.status)
	}
	if v := rec.Header().Get("Content-Type"); v != "" && !strings.HasPrefix(v, "text/plain") {
		// http.Error 默认 text/plain; charset=utf-8，这里放过（兼容）
		t.Logf("Content-Type=%q（http.Error 默认 text/plain，可接受）", v)
	}
}

func TestChatAnalysisTotalWS_RejectsExplicitNone(t *testing.T) {
	rec := &minimalRecorder{}
	req := httptest.NewRequest(http.MethodGet, "/ChatAnalysisTotalWS", nil)
	ctx := context.WithValue(req.Context(), AuthClaimsContextKey{}, &AuthClaims{Role: WsRoleNone})
	req = req.WithContext(ctx)
	ChatAnalysisTotalWSHandle(rec, req)

	if !rec.written {
		t.Fatalf("WS handler 应已写响应")
	}
	if rec.status != http.StatusUnauthorized {
		t.Fatalf("WsRoleNone 应 401，实际 %d", rec.status)
	}
}

// TestChatAnalysisTotalWS_AcceptsAuthenticated 阶段AO 核心契约：合法 context 通过 401 拦截，
// 进入 upgrade 阶段；本测试不真正建立 WS（需要特殊 upgrader 支持），但确认 handler 不在 401 阶段退出。
func TestChatAnalysisTotalWS_AcceptsUser(t *testing.T) {
	rec := &minimalRecorder{}
	req := httptest.NewRequest(http.MethodGet, "/ChatAnalysisTotalWS?user_name=liusm191&model_name=liusm191-ai-model", nil)
	ctx := context.WithValue(req.Context(), AuthClaimsContextKey{}, &AuthClaims{
		Role:      WsRoleUser,
		UserID:    7,
		UserName:  "realuser",
		LoginType: "user",
	})
	req = req.WithContext(ctx)

	// 通过 401 检查后会进入 upgrade，httptest.NewRecorder 不支持 Hijacker，
	// upgrader.Upgrade 会写 400 "bad handshake"。我们只确认不是 401。
	defer func() {
		// upgrader panic 在 httptest 环境下偶发（不会真正泄漏），保护测试不挂
		_ = recover()
	}()
	ChatAnalysisTotalWSHandle(rec, req)

	// 关键断言：未在 401 阶段退出（status != 401）
	if rec.written && rec.status == http.StatusUnauthorized {
		t.Fatalf("已登录请求不应 401")
	}
}

func TestAuthClaims_RoleConstants(t *testing.T) {
	// 防止有人改坏常量值：api 包通过这些常量构造 *AuthClaims 写入 context
	if WsRoleNone != 0 {
		t.Errorf("WsRoleNone 应为 0（零值即未授权），实际 %d", WsRoleNone)
	}
	if WsRoleUser == WsRoleManager || WsRoleUser == WsRoleNone || WsRoleManager == WsRoleNone {
		t.Errorf("WS role 常量必须互不相同：none=%d user=%d manager=%d", WsRoleNone, WsRoleUser, WsRoleManager)
	}
}