package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestUserModelOptionsInterface 用户名+模型名下拉选项接口单测
func TestUserModelOptionsInterface(t *testing.T) {
	clean := initTestEnv(t)
	defer clean()

	// 准备数据：2 个用户，各带 1-2 个模型
	u1 := &modelsdb.TAgentHttpUserInfo{UserName: "alice_opt", Password: "hashed-pwd-1", Status: 1, AnthropicEnabled: true}
	if err := modelsdb.AddUser(u1); err != nil {
		t.Fatalf("AddUser u1: %v", err)
	}
	u2 := &modelsdb.TAgentHttpUserInfo{UserName: "bob_opt", Password: "hashed-pwd-2", Status: 1, AnthropicEnabled: true}
	if err := modelsdb.AddUser(u2); err != nil {
		t.Fatalf("AddUser u2: %v", err)
	}
	if err := modelsdb.AddUserModel(&modelsdb.TAgentHttpUserModelInfo{UserID: u1.ID, ModelName: "model-aaaaaa", APIKey: "key-a-00000000000000000000000000001", Status: 1}); err != nil {
		t.Fatalf("AddUserModel a: %v", err)
	}
	if err := modelsdb.AddUserModel(&modelsdb.TAgentHttpUserModelInfo{UserID: u1.ID, ModelName: "model-bbbbbb", APIKey: "key-b-00000000000000000000000000002", Status: 1}); err != nil {
		t.Fatalf("AddUserModel b: %v", err)
	}
	if err := modelsdb.AddUserModel(&modelsdb.TAgentHttpUserModelInfo{UserID: u2.ID, ModelName: "model-cccccc", APIKey: "key-c-00000000000000000000000000003", Status: 1}); err != nil {
		t.Fatalf("AddUserModel c: %v", err)
	}

	handler := http.HandlerFunc(userModelOptionsInterfaceHandle)

	t.Run("GET 正常返回用户+模型级联", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/UserModelOptionsInterface", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Success bool `json:"success"`
			Users   []struct {
				UserName   string   `json:"user_name"`
				ModelNames []string `json:"model_names"`
			} `json:"users"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json decode: %v", err)
		}
		if !resp.Success || len(resp.Users) != 2 {
			t.Fatalf("success=%v users=%+v", resp.Success, resp.Users)
		}
		got := map[string][]string{}
		for _, u := range resp.Users {
			got[u.UserName] = u.ModelNames
		}
		if len(got["alice_opt"]) != 2 || len(got["bob_opt"]) != 1 {
			t.Fatalf("model cascade wrong: %+v", got)
		}
		// 响应不得泄露 API Key 字段
		if body := rec.Body.String(); strings.Contains(body, "api_key") || strings.Contains(body, "APIKey") {
			t.Fatalf("response leaks api key field: %s", body)
		}
	})

	t.Run("非 GET 方法拦截", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/UserModelOptionsInterface", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})
}
