package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestV2081UserManageUpdateThenUserLoginDoUserLogin
// 阶段AR（20260831）：验证主链路（update→DB→login）无 Bug。
func TestV2081UserManageUpdateThenUserLoginDoUserLogin(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 1) 添加一个用户（与管理 Web 流程一致）
	addPayload, _ := json.Marshal(map[string]interface{}{
		"action":            "add",
		"user_name":         "oldname",
		"password":          "oldpass",
		"phone":             "13800000001",
		"anthropic_enabled": true,
		"openai_enabled":    false,
	})
	req := httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(addPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var addResp struct {
		Success bool                         `json:"success"`
		Data    modelsdb.TAgentHttpUserInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("unmarshal add failed: %v", err)
	}
	if !addResp.Success {
		t.Fatalf("add failed: body=%s", rec.Body.String())
	}
	userID := addResp.Data.ID

	// 2) 管理员修改用户名 + 密码 + 手机号
	updatePayload, _ := json.Marshal(map[string]interface{}{
		"action":            "update",
		"id":                userID,
		"user_name":         "newname",
		"password":          "newpass",
		"phone":             "13900000002",
		"anthropic_enabled": true,
		"openai_enabled":    false,
	})
	req = httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var updateResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("unmarshal update failed: %v", err)
	}
	if !updateResp.Success {
		t.Fatalf("update failed: %s", updateResp.Message)
	}

	// 3) 直接调 doUserLogin 验证
	_, err := doUserLogin(userLoginReq{
		LoginType: "user",
		UserName:  "newname",
		Password:  "newpass",
		Phone:     "13900000002",
	})
	if err != nil {
		t.Fatalf("doUserLogin failed after update: %v", err)
	}
}

// TestV2081UserManageEditFormNoChange：编辑表单不动 phone/password，保留原值。
func TestV2081UserManageEditFormNoChange(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	addPayload, _ := json.Marshal(map[string]interface{}{
		"action":            "add",
		"user_name":         "foo",
		"password":          "secret123",
		"phone":             "13812345678",
		"anthropic_enabled": true,
		"openai_enabled":    true,
	})
	req := httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(addPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	var addResp struct {
		Success bool                         `json:"success"`
		Data    modelsdb.TAgentHttpUserInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("unmarshal add failed: %v", err)
	}
	if !addResp.Success {
		t.Fatalf("add failed: body=%s", rec.Body.String())
	}
	userID := addResp.Data.ID

	// 列表接口拿到的 phone 已被 MaskPhone
	listReq := httptest.NewRequest("POST", "/UserManageInterface", strings.NewReader(`{"action":"list"}`))
	listReq.Header.Set("Content-Type", "application/json")
	listRec := httptest.NewRecorder()
	userManageInterfaceHandle(listRec, listReq)
	var listResp struct {
		Success bool                         `json:"success"`
		Data    []modelsdb.TAgentHttpUserInfo `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list failed: %v", err)
	}
	if !listResp.Success || len(listResp.Data) != 1 {
		t.Fatalf("list failed")
	}
	maskedPhone := listResp.Data[0].Phone
	if !strings.Contains(maskedPhone, "****") {
		t.Fatalf("expected masked phone, got %q", maskedPhone)
	}

	// 修复后的前端：清空 password 和 phone 提交
	updatePayload, _ := json.Marshal(map[string]interface{}{
		"action":            "update",
		"id":                userID,
		"user_name":         listResp.Data[0].UserName,
		"password":          "",
		"phone":             "",
		"anthropic_enabled": true,
		"openai_enabled":    true,
	})
	req = httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	var updateResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("unmarshal update failed: %v", err)
	}
	if !updateResp.Success {
		t.Fatalf("update failed: %s", updateResp.Message)
	}

	// 验证 DB 中 phone 与 password 未被空字符串误清
	persisted, err := modelsdb.GetUserByID(userID)
	if err != nil {
		t.Fatalf("get user after update failed: %v", err)
	}
	if persisted.Phone != "13812345678" {
		t.Fatalf("phone should be preserved when empty submitted, got %q", persisted.Phone)
	}
	if persisted.Password == "" {
		t.Fatalf("password should be preserved (hashed) when empty submitted, got empty")
	}

	// 应当可以用原始密码 + 原始手机号登录
	_, err = doUserLogin(userLoginReq{
		LoginType: "user",
		UserName:  "foo",
		Password:  "secret123",
		Phone:     "13812345678",
	})
	if err != nil {
		t.Fatalf("doUserLogin failed after no-op update: %v", err)
	}
}

// TestV2081DoUserLoginEmptyPhone：phone 改选填，验证空 phone 的登录路径与统一错误提示。
func TestV2081DoUserLoginEmptyPhone(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	addPayload, _ := json.Marshal(map[string]interface{}{
		"action":            "add",
		"user_name":         "nophone",
		"password":          "pwd123",
		"anthropic_enabled": true,
		"openai_enabled":    true,
	})
	req := httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(addPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	var addResp struct {
		Success bool                         `json:"success"`
		Data    modelsdb.TAgentHttpUserInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("unmarshal add failed: %v", err)
	}
	if !addResp.Success {
		t.Fatalf("add failed: body=%s", rec.Body.String())
	}

	// 用空手机号登录应成功
	_, err := doUserLogin(userLoginReq{
		LoginType: "user",
		UserName:  "nophone",
		Password:  "pwd123",
		Phone:     "",
	})
	if err != nil {
		t.Fatalf("doUserLogin with empty phone failed: %v", err)
	}

	// 用非空手机号登录应失败（DB 中 phone 为空，统一提示）
	_, err = doUserLogin(userLoginReq{
		LoginType: "user",
		UserName:  "nophone",
		Password:  "pwd123",
		Phone:     "13900000000",
	})
	if err == nil {
		t.Fatalf("expected login to fail when DB phone empty but request phone non-empty")
	}
	if !strings.Contains(err.Error(), "用户名、密码或手机号错误") {
		t.Fatalf("expected unified error, got %v", err)
	}
}

// TestV2081UserManageCacheMutation 阶段AR 关键回归测试：
// 验证"添加用户 → 修改响应脱敏 → 缓存中 user.Password 不被清空"。
// 这一修复是本次根因的核心：原版 AddUserToCache 缓存的是原结构体指针，
// 调用方在响应前对 item.Password = ""（脱敏）会反向修改缓存，
// 导致同会话后续所有用户端登录都拿不到 bcrypt 哈希，验证 password 全部失败。
func TestV2081UserManageCacheMutation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	addPayload, _ := json.Marshal(map[string]interface{}{
		"action":            "add",
		"user_name":         "cached_user",
		"password":          "supersecret",
		"phone":             "13911112222",
		"anthropic_enabled": true,
		"openai_enabled":    true,
	})
	req := httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(addPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var addResp struct {
		Success bool                         `json:"success"`
		Data    modelsdb.TAgentHttpUserInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("unmarshal add failed: %v", err)
	}
	if !addResp.Success {
		t.Fatalf("add failed: %s", rec.Body.String())
	}

	// 响应数据已脱敏（Password 字段为空字符串）
	if addResp.Data.Password != "" {
		t.Fatalf("expected response to mask password, got %q", addResp.Data.Password)
	}

	// 但缓存中的用户必须仍然保留 bcrypt 哈希，登录才能通过
	cached, ok := modelsdb.GetCachedUserByName("cached_user")
	if !ok {
		t.Fatalf("user should be in cache after add")
	}
	prefixLen := 3
	if len(cached.Password) < prefixLen {
		prefixLen = len(cached.Password)
	}
	if !strings.HasPrefix(cached.Password, "$2") {
		t.Fatalf("cache must retain bcrypt hash, got prefix=%q", cached.Password[:prefixLen])
	}

	// 直接走登录主流程，确认密码校验通过
	_, err := doUserLogin(userLoginReq{
		LoginType: "user",
		UserName:  "cached_user",
		Password:  "supersecret",
		Phone:     "13911112222",
	})
	if err != nil {
		t.Fatalf("login failed because cache mutated by response desensitize: %v", err)
	}
}
