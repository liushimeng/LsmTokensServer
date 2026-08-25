package api

import (
	"encoding/json"
	"net/http"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// userModelOptionsInterfaceHandle 用户名+模型名下拉选项接口（仅管理端）
// 供管理端各查询页面的级联下拉使用：一次返回全部用户及其名下模型名列表。
// 页面生命周期内前端缓存只调用一次；响应不包含 APIKey 等敏感字段。
func userModelOptionsInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	users, err := modelsdb.GetAllUsers(0, 0)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	allModels, err := modelsdb.GetAllUserModels()
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	// 内存按 UserID 组装 user_name → model_names（两次查询，避免逐用户 N+1）
	modelNamesByUserID := make(map[uint64][]string, len(users))
	for _, m := range allModels {
		modelNamesByUserID[m.UserID] = append(modelNamesByUserID[m.UserID], m.ModelName)
	}

	type userOption struct {
		UserName   string   `json:"user_name"`
		ModelNames []string `json:"model_names"`
	}
	userList := make([]userOption, 0, len(users))
	for _, u := range users {
		names := modelNamesByUserID[u.ID]
		if names == nil {
			names = []string{}
		}
		userList = append(userList, userOption{UserName: u.UserName, ModelNames: names})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"users":   userList,
	})
}
