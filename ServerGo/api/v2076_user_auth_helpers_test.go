// v2.0.76 阶段AO：用户端纵深防御 helper 测试。
//
// 背景：userAuthMiddleware 是路由级第一道防线，业务 handler 内部仍做 getUserToken 二次校验。
// 阶段AO 抽取 requireUserClaimsOr401 helper，把"claims.UserID == 0"分支统一改为 401 JSON，
// 避免未来新增 mux 漏挂中间件时，业务 handler 返回 HTTP 200 success:false 误导前端。
//
// 本测试覆盖：
//  1. 未带 Cookie → 401 JSON（不带 Cookie → claims 零值 → 拒绝）
//  2. 带过期/伪造 Cookie → 401 JSON（JWT 解析失败 → 拒绝）
//  3. 正确登录态 → 返回 claims，调用方继续处理
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// helperReq 不带 Cookie 的请求
func helperReq(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, "/UserInfoInterface", nil)
}

func TestRequireUserClaimsOr401_NoCookie(t *testing.T) {
	w := httptest.NewRecorder()
	r := helperReq(t)
	claims, ok := requireUserClaimsOr401(w, r)
	if ok {
		t.Fatalf("无 Cookie 应拒绝，实际 ok=true, claims=%+v", claims)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无 Cookie 应 401，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type 应为 application/json，实际 %q", ct)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON：%v body=%s", err, w.Body.String())
	}
	if body["success"] != false {
		t.Errorf("success 应为 false，实际 %v", body["success"])
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Errorf("message 应非空，实际 %v", body["message"])
	}
}

func TestRequireUserClaimsOr401_BadCookie(t *testing.T) {
	w := httptest.NewRecorder()
	r := helperReq(t)
	// 设置一个伪造的 JWT（解析失败 → claims 零值）
	r.AddCookie(&http.Cookie{Name: userLoginCookieName, Value: "garbage.token.here"})
	claims, ok := requireUserClaimsOr401(w, r)
	if ok {
		t.Fatalf("伪造 Cookie 应拒绝，实际 ok=true, claims=%+v", claims)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("伪造 Cookie 应 401，实际 %d", w.Code)
	}
}

// 真实登录态测试需要 DB + JWT secret file，由其他集成测试覆盖；本单测只验证拒绝路径。

// 阶段AO：验证 isUserAPIPath / isManagerAPIPath 已移除 /CaptchaAudio 死白名单。
func TestIsUserAPIPath_RemovedCaptchaAudio(t *testing.T) {
	if isUserAPIPath("/CaptchaAudio") {
		t.Errorf("isUserAPIPath 不应再将 /CaptchaAudio 判定为 API 路径（端点未挂载，避免误导）")
	}
	// 其他典型 API 路径应仍命中
	if !isUserAPIPath("/UserInfoInterface") {
		t.Errorf("/UserInfoInterface 应判定为 API 路径")
	}
	if !isUserAPIPath("/ChatAnalysisTotalWS") {
		t.Errorf("/ChatAnalysisTotalWS 应判定为 API 路径")
	}
	if isUserAPIPath("/Home") {
		t.Errorf("SPA 路径 /Home 不应判定为 API 路径")
	}
}

func TestIsManagerAPIPath_RemovedCaptchaAudio(t *testing.T) {
	if isManagerAPIPath("/CaptchaAudio") {
		t.Errorf("isManagerAPIPath 不应再将 /CaptchaAudio 判定为 API 路径")
	}
	if !isManagerAPIPath("/UserManageInterface") {
		t.Errorf("/UserManageInterface 应判定为 API 路径")
	}
	if isManagerAPIPath("/UserManage") {
		t.Errorf("SPA 路径 /UserManage 不应判定为 API 路径")
	}
}