package api

// v2.0.74 阶段AL：超级管理员禁用态的登录 + 中间件测试。
// 覆盖：
//   - 登录接口在 disable 状态下返回禁用提示；
//   - 中间件在 disable 状态下对数据 API 返回 503。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

func TestManagerLogin_DisabledState(t *testing.T) {
	// 备份并恢复全局 config
	original := config.G
	defer func() { config.G = original }()
	config.G = config.GetDefaultConfig()
	config.G.Security.ManagerUserName = "disable"
	config.G.Security.ManagerPassword = "disable"
	config.G.Security.ManagerWebAuthDisabled = true

	// 即使用户名/密码都不正确，禁用态必须先于凭证校验返回
	body := `{"user_name":"admin","password":"any","captcha_id":"","captcha_code":""}`
	req := httptest.NewRequest(http.MethodPost, "/ManagerLoginInterface", strings.NewReader(body))
	rec := httptest.NewRecorder()

	// 验证码校验在禁用态之前？查看 handler：顺序是「凭证未配置 → 禁用态 → 验证码 → 凭证」。
	// 当前 ManagerUserName/Password 都非空且等于 disable，会命中 IsManagerDisabled 分支。
	managerLoginInterfaceHandle(rec, req)

	var resp userLoginResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode err=%v body=%s", err, rec.Body.String())
	}
	if resp.Success {
		t.Fatal("禁用态下登录应该失败")
	}
	if !strings.Contains(resp.Message, "已被禁用") {
		t.Errorf("提示文案应包含'已被禁用', got: %q", resp.Message)
	}
}

func TestManagerAuthMiddleware_DisabledStateRejectsDataAPIs(t *testing.T) {
	original := config.G
	defer func() { config.G = original }()
	config.G = config.GetDefaultConfig()
	config.G.Security.ManagerUserName = "disable"
	config.G.Security.ManagerPassword = "disable"
	config.G.Security.ManagerWebAuthDisabled = false // 关键：与 disable 区分 → 中间件走禁用分支

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := ManagerAuthMiddleware(next)

	// 数据 API → 应返回 503
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/UserManageInterface", nil))
	if called {
		t.Fatal("禁用态下数据 API 不应触达 next")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	var resp userLoginResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode err=%v", err)
	}
	if resp.Success || !strings.Contains(resp.Message, "已被禁用") {
		t.Errorf("响应文案错误: %+v", resp)
	}
}

func TestManagerAuthMiddleware_DisabledStateAllowsLoginPage(t *testing.T) {
	original := config.G
	defer func() { config.G = original }()
	config.G = config.GetDefaultConfig()
	config.G.Security.ManagerUserName = "disable"
	config.G.Security.ManagerPassword = "disable"
	config.G.Security.ManagerWebAuthDisabled = false

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := ManagerAuthMiddleware(next)

	// 验证码接口（公开）→ 应放行
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/CaptchaGenerate", nil))
	if !called {
		t.Fatal("禁用态下验证码接口应放行（前端可渲染提示页）")
	}
}