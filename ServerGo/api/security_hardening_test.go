package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// v2.0.56 安全加固单元测试

func TestHashAndVerifyPassword(t *testing.T) {
	hashed, err := HashPassword("s3cret-pa55")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !IsPasswordHashed(hashed) {
		t.Fatalf("expected bcrypt hash, got %q", hashed)
	}
	if ok, legacy := VerifyPassword(hashed, "s3cret-pa55"); !ok || legacy {
		t.Fatalf("bcrypt verify failed: ok=%v legacy=%v", ok, legacy)
	}
	if ok, _ := VerifyPassword(hashed, "wrong"); ok {
		t.Fatal("wrong password should not verify")
	}
	// 旧明文兼容
	if ok, legacy := VerifyPassword("plainOld", "plainOld"); !ok || !legacy {
		t.Fatalf("legacy plaintext verify failed: ok=%v legacy=%v", ok, legacy)
	}
}

func TestSubtleConstantTimeEq(t *testing.T) {
	if !subtleConstantTimeEq("abc", "abc") {
		t.Fatal("equal strings should match")
	}
	if subtleConstantTimeEq("abc", "abd") || subtleConstantTimeEq("abc", "abcd") || subtleConstantTimeEq("", "a") {
		t.Fatal("different strings should not match")
	}
}

func TestMaskPhone(t *testing.T) {
	got := MaskPhone("13812345678")
	if got != "138****5678" {
		t.Fatalf("MaskPhone 11 位失败: %s", got)
	}
	if MaskPhone("short") != "****" || MaskPhone("") != "" {
		t.Fatalf("MaskPhone 边界失败: %s / %s", MaskPhone("short"), MaskPhone(""))
	}
}

func TestValidateFieldNoLongerRewritesPasswords(t *testing.T) {
	// 旧 SanitizeInput 会把含 "end"/"select" 的密码静默改写；新实现必须原样返回
	out, err := ValidateField("my-select-end@pwd", 128, "密码")
	if err != nil || out != "my-select-end@pwd" {
		t.Fatalf("密码被改写: out=%q err=%v", out, err)
	}
	if _, err := ValidateField("toooooooooooooooo-long", 5, "用户名"); err == nil {
		t.Fatal("超长输入应报错")
	}
}

func TestGetClientIPIgnoresSpoofedHeadersByDefault(t *testing.T) {
	// 默认不信任 X-Forwarded-For（防伪造绕过防爆破）
	r := httptest.NewRequest("POST", "/UserLoginInterface", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.RemoteAddr = "9.9.9.9:12345"
	if ip := getClientIP(r); ip != "9.9.9.9" {
		t.Fatalf("应忽略伪造 XFF，got %s", ip)
	}
}

func TestManagerAuthMiddlewareRejectsUnauthenticated(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := ManagerAuthMiddleware(next)

	// 未登录访问业务 API → 401 JSON，不触达 next
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/UserManageInterface", nil)
	handler.ServeHTTP(rec, req)
	if called {
		t.Fatal("未登录不应触达业务 handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	var resp userLoginResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Success {
		t.Fatalf("401 响应格式错误: %v %s", err, rec.Body.String())
	}

	// 公开路由放行
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest("POST", "/ManagerLoginInterface", nil))
	if !called && rec2.Code == http.StatusOK {
		// ManagerLoginInterface handler 本身会被调用（进入 next）
		t.Log("公开路由已放行")
	}
}

// 网关代理支持：SPA 页面导航（非数据接口）未登录放行，数据接口仍拦截
func TestManagerAuthMiddlewareSPANavigationPassThrough(t *testing.T) {
	for _, p := range []string{"/ChatAnalysis", "/ChatAnalysisTotal", "/UserManage", "/ManagerHome"} {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
		handler := ManagerAuthMiddleware(next)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		handler.ServeHTTP(rec, req)
		if !called || rec.Code != http.StatusOK {
			t.Fatalf("SPA 页面路由 %s 应放行由前端路由接管: called=%v code=%d", p, called, rec.Code)
		}
	}

	// 页面型伪装请求访问数据接口：仍必须拦截（302 登录页），不得泄露数据
	for _, p := range []string{"/ChatAnalysisInterface", "/UserManageInterface", "/ChatAnalysisTotalWS", "/ProtocolConvertAnalyzerStatus"} {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
		handler := ManagerAuthMiddleware(next)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Header.Set("Accept", "text/html")
		handler.ServeHTTP(rec, req)
		if called || rec.Code != http.StatusFound {
			t.Fatalf("数据接口 %s 伪装 text/html 仍应拦截: called=%v code=%d", p, called, rec.Code)
		}
	}
}

func TestJWTSecretDeterministicInProcess(t *testing.T) {
	a := string(getJWTSecret())
	b := string(getJWTSecret())
	if a == "" || a != b {
		t.Fatalf("getJWTSecret 应进程内稳定且非空")
	}
}
