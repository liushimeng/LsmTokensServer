package api

import (
	"encoding/json"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strings"
)

// ===== API Request/Response Types =====

type userManageReq struct {
	Action string `json:"action"`
	ID     uint64 `json:"id"`
	// User fields
	UserName         string `json:"user_name"`
	Password         string `json:"password"`
	Phone            string `json:"phone"`
	AnthropicEnabled bool   `json:"anthropic_enabled"`
	OpenAIEnabled    bool   `json:"openai_enabled"`
	// Model fields
	UserID    uint64 `json:"user_id"`
	ModelName string `json:"model_name"`
	Status    int    `json:"status"`
}

type userManageResp struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ===== API Handlers =====

func userManageInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}
	var req userManageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求体超过大小限制"})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "list":
		users, err := modelsdb.GetAllUsers(0, 0)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: users})
	case "add":
		userName, err := ValidateField(strings.TrimSpace(req.UserName), 50, "用户名")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		password, err := ValidateField(strings.TrimSpace(req.Password), 128, "密码")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		phone, err := ValidateField(strings.TrimSpace(req.Phone), 20, "手机号")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		item := &modelsdb.TAgentHttpUserInfo{
			UserName:         userName,
			Password:         password,
			Phone:            phone,
			AnthropicEnabled: req.AnthropicEnabled,
			OpenAIEnabled:    req.OpenAIEnabled,
		}
		if err := modelsdb.AddUser(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "添加成功", Data: item})
	case "update":
		userName, err := ValidateField(strings.TrimSpace(req.UserName), 50, "用户名")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		password, err := ValidateField(strings.TrimSpace(req.Password), 128, "密码")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		phone, err := ValidateField(strings.TrimSpace(req.Phone), 20, "手机号")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		item := &modelsdb.TAgentHttpUserInfo{
			ID:               req.ID,
			UserName:         userName,
			Password:         password,
			Phone:            phone,
			AnthropicEnabled: req.AnthropicEnabled,
			OpenAIEnabled:    req.OpenAIEnabled,
		}
		if err := modelsdb.UpdateUser(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "更新成功"})
	case "delete":
		if err := modelsdb.DeleteUser(req.ID); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "删除成功"})
	case "update_status":
		if err := modelsdb.UpdateUserStatus(req.ID, req.Status); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "状态更新成功"})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}

func userModelManageInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}
	var req userManageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求体超过大小限制"})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "list":
		models, err := modelsdb.GetUserModelsByUserID(req.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: models})
	case "add":
		modelName, err := ValidateField(strings.TrimSpace(req.ModelName), 64, "模型名称")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		item := &modelsdb.TAgentHttpUserModelInfo{
			UserID:    req.UserID,
			ModelName: modelName,
		}
		if err := modelsdb.AddUserModel(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "添加成功", Data: item})
	case "update":
		modelName, err := ValidateField(strings.TrimSpace(req.ModelName), 64, "模型名称")
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		item := &modelsdb.TAgentHttpUserModelInfo{
			ID:        req.ID,
			UserID:    req.UserID,
			ModelName: modelName,
		}
		if err := modelsdb.UpdateUserModel(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "更新成功"})
	case "delete":
		if err := modelsdb.DeleteUserModel(req.ID); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "删除成功"})
	case "update_status":
		if err := modelsdb.UpdateUserModelStatus(req.ID, req.Status); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "状态更新成功"})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}
