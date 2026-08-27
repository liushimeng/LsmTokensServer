package api

import (
	"encoding/json"
	"net/http"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// managerInfoInterfaceHandle 管理端首页信息（无登录态，返回管理员标识）
func managerInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	json.NewEncoder(w).Encode(userLoginResp{
		Success: true,
		Data: map[string]interface{}{
			"user_id":    0,
			"user_name":  "管理员",
			"login_type": "manager",
			"model_name": "",
		},
	})
}

// managerModelListInterfaceHandle 管理端获取所有模型列表（不限用户）
func managerModelListInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userLoginResp{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	// 管理端返回所有用户的所有模型
	models, err := modelsdb.GetAllUserModels()
	if err != nil {
		json.NewEncoder(w).Encode(userLoginResp{
			Success: false,
			Message: "获取模型列表失败: " + err.Error(),
		})
		return
	}

	// 响应脱敏：API Key 仅返回前 8 位掩码（完整 Key 经 ChatDialogInterface reveal_key 按需获取）
	maskUserModelAPIKeys(models)

	json.NewEncoder(w).Encode(userLoginResp{
		Success: true,
		Data:    models,
	})
}
