package api

// v2.0.74 回归测试（自动化测试报告 20260826_201128 BUG-4/SUG-1）：
// 模型 API Key 列表/对话配置响应默认脱敏，完整 Key 仅经 ChatDialogInterface
// action=reveal_key 显式获取（本人/管理员按需揭示，前端不持久化）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestMaskAPIKey 掩码格式：前 8 位 + ****（与前端 substring(0,8)+'****' 展示一致，恰好 8 位全掩码）
func TestMaskAPIKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-1234567890abcdef", "sk-12345****"},
		{"1234567890", "12345678****"},
		{"12345678", "****"},
		{"short", "****"},
		{"", ""},
	}
	for _, c := range cases {
		if got := MaskAPIKey(c.in); got != c.want {
			t.Errorf("MaskAPIKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// setupMaskUserModel 造一个带长 API Key 的用户模型，返回 (user, fullKey)
func setupMaskUserModel(t *testing.T, userName, modelName string) (*modelsdb.TAgentHttpUserInfo, string) {
	t.Helper()
	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         userName,
		AnthropicEnabled: true,
		OpenAIEnabled:    true,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}
	fullKey := "sk-masktest-" + userName + "-0123456789abcdef"
	if len(fullKey) < 32 {
		t.Fatalf("测试 Key 长度不足 32: %d", len(fullKey))
	}
	if err := modelsdb.AddUserModel(&modelsdb.TAgentHttpUserModelInfo{
		UserID:    user.ID,
		ModelName: modelName,
		APIKey:    fullKey,
	}); err != nil {
		t.Fatalf("add model failed: %v", err)
	}
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("reload cache failed: %v", err)
	}
	return user, fullKey
}

// TestUserModelListAPIKeyMasked 用户端 /UserModelListInterface 响应 API Key 默认脱敏，
// 完整 Key 不得出现在响应体中。
func TestUserModelListAPIKeyMasked(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	user, fullKey := setupMaskUserModel(t, "maskuser", "mask-model-01")

	token, err := generateUserToken(user, "user", "")
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}
	req := httptest.NewRequest("POST", "/UserModelListInterface", nil)
	req.AddCookie(&http.Cookie{Name: userLoginCookieName, Value: token})
	rec := httptest.NewRecorder()
	userModelListInterfaceHandle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), fullKey) {
		t.Fatalf("列表响应泄漏完整 API Key: %s", rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			ModelName string `json:"model_name"`
			APIKey    string `json:"api_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body: %s", err, rec.Body.String())
	}
	if !resp.Success || len(resp.Data) != 1 {
		t.Fatalf("success=%v data=%d, body: %s", resp.Success, len(resp.Data), rec.Body.String())
	}
	if want := fullKey[:8] + "****"; resp.Data[0].APIKey != want {
		t.Fatalf("api_key = %q, want %q", resp.Data[0].APIKey, want)
	}
}

// TestManagerModelListAPIKeyMasked 管理端 /UserModelListInterface（全量模型）同样脱敏。
func TestManagerModelListAPIKeyMasked(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	_, fullKey := setupMaskUserModel(t, "maskadmin", "mask-model-02")

	rec := httptest.NewRecorder()
	managerModelListInterfaceHandle(rec, httptest.NewRequest("POST", "/UserModelListInterface", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), fullKey) {
		t.Fatalf("管理端列表响应泄漏完整 API Key: %s", rec.Body.String())
	}
	if want := fullKey[:8] + "****"; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("响应应含掩码 Key %q: %s", want, rec.Body.String())
	}
}

// TestUserModelManageListAPIKeyMasked /UserModelManageInterface list（用户管理页展开模型）同样脱敏。
func TestUserModelManageListAPIKeyMasked(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	user, fullKey := setupMaskUserModel(t, "maskmanage", "mask-model-03")

	body, _ := json.Marshal(map[string]interface{}{"action": "list", "user_id": user.ID})
	req := httptest.NewRequest("POST", "/UserModelManageInterface", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userModelManageInterfaceHandle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), fullKey) {
		t.Fatalf("模型管理列表响应泄漏完整 API Key: %s", rec.Body.String())
	}
	if want := fullKey[:8] + "****"; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("响应应含掩码 Key %q: %s", want, rec.Body.String())
	}
}

// callUserChatDialog 用户端 ChatDialogInterface 便捷调用
func callUserChatDialog(t *testing.T, token string, payload map[string]string) (bool, string, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/ChatDialogInterface", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: userLoginCookieName, Value: token})
	rec := httptest.NewRecorder()
	userChatDialogInterfaceHandle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool                   `json:"success"`
		Message string                 `json:"message"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body: %s", err, rec.Body.String())
	}
	return resp.Success, resp.Message, resp.Data
}

// TestChatDialogConfigMaskedAndRevealKey 用户端 config 默认脱敏 + reveal_key 显式取完整 Key；
// 越权（他人模型）reveal 被拒绝。
func TestChatDialogConfigMaskedAndRevealKey(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	user, fullKey := setupMaskUserModel(t, "maskchat", "mask-model-04")
	other, otherKey := setupMaskUserModel(t, "maskchat2", "mask-model-05")

	token, err := generateUserToken(user, "user", "")
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}

	// config：api_key 与 api_key_masked 均为掩码，响应不含完整 Key
	ok, msg, data := callUserChatDialog(t, token, map[string]string{"action": "config", "model_name": "mask-model-04"})
	if !ok {
		t.Fatalf("config failed: %s", msg)
	}
	if got := data["api_key"]; got != fullKey[:8]+"****" {
		t.Fatalf("config api_key = %v, want 掩码", got)
	}
	if got := data["api_key_masked"]; got != fullKey[:8]+"****" {
		t.Fatalf("config api_key_masked = %v, want 掩码", got)
	}

	// reveal_key：返回完整 Key（仅本人模型）
	ok, msg, data = callUserChatDialog(t, token, map[string]string{"action": "reveal_key", "model_name": "mask-model-04"})
	if !ok {
		t.Fatalf("reveal_key failed: %s", msg)
	}
	if got, _ := data["api_key"].(string); got != fullKey {
		t.Fatalf("reveal_key api_key = %q, want 完整 Key", got)
	}

	// 越权：他人模型 reveal 被拒绝
	ok, msg, _ = callUserChatDialog(t, token, map[string]string{"action": "reveal_key", "model_name": "mask-model-05"})
	if ok || !strings.Contains(msg, "无权") {
		t.Fatalf("越权 reveal 应被拒绝, ok=%v msg=%q", ok, msg)
	}
	_ = other
	_ = otherKey
}

// TestChatDialogManagerRevealKey 管理端 config 脱敏 + reveal_key 返回完整 Key。
func TestChatDialogManagerRevealKey(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	_, fullKey := setupMaskUserModel(t, "maskchatadm", "mask-model-06")

	call := func(action string) (bool, string, map[string]interface{}) {
		body, _ := json.Marshal(map[string]string{"action": action, "user_name": "maskchatadm", "model_name": "mask-model-06"})
		req := httptest.NewRequest("POST", "/ChatDialogInterface", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		chatDialogInterfaceHandle(rec, req)
		var resp struct {
			Success bool                   `json:"success"`
			Message string                 `json:"message"`
			Data    map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v, body: %s", err, rec.Body.String())
		}
		return resp.Success, resp.Message, resp.Data
	}

	ok, msg, data := call("config")
	if !ok {
		t.Fatalf("config failed: %s", msg)
	}
	if got := data["api_key"]; got != fullKey[:8]+"****" {
		t.Fatalf("config api_key = %v, want 掩码", got)
	}

	ok, msg, data = call("reveal_key")
	if !ok {
		t.Fatalf("reveal_key failed: %s", msg)
	}
	if got, _ := data["api_key"].(string); got != fullKey {
		t.Fatalf("reveal_key api_key = %q, want 完整 Key", got)
	}
}
