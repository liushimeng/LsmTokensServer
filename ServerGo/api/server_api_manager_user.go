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
	UserName        string `json:"user_name"`
	Password        string `json:"password"`
	Phone           string `json:"phone"`
	AnthropicEnabled bool  `json:"anthropic_enabled"`
	OpenAIEnabled    bool  `json:"openai_enabled"`
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
		// v2.0.56 安全加固：响应脱敏（不回传密码哈希，手机号掩码）
		for i := range users {
			users[i].Password = ""
			users[i].Phone = MaskPhone(users[i].Phone)
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
		// v2.0.56：密码 bcrypt 哈希后入库
		hashed, err := HashPassword(password)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "密码加密失败"})
			return
		}
		password = hashed
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
		item.Password = "" // 响应脱敏
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
		// v2.0.56：编辑表单"未修改"判断：
		//   - password 留空或已是 bcrypt 哈希前缀 → 保留 existing.Password
		//     （防御：列表接口已将 Password 置空，编辑时不带值相当于"不修改"。）
		//   - phone 留空 → 保留 existing.Phone（兼容旧前端 + 用户 UX：清空等价"不修改"）。
		//   - phone 非空 → 直接写入 DB。
		// 阶段AR 关键修复：移除旧版 `strings.Contains(phone, "****")` 启发式判断。
		// 旧逻辑问题：admin 看到 MaskPhone 输出的 "138****1234" 直接保存，
		// 后端若不识别"掩码值 vs 真实值"就会误以为未修改——但实际上若 admin
		// 在该字段内输入新的完整手机号，phone 不含 **** 时能正常写入；
		// 真正风险在于 admin 重输的字符串碰巧包含 ****（被误判）。
		// 解决：前端把 phone 输入框留空并把 placeholder 标清"不修改请留空"，
		// admin 真正改 phone 时一定会清空原值再输入完整新号码，因此 phone 留空 = 未修改
		// 是稳定语义。
		existing, exErr := modelsdb.GetUserByID(req.ID)
		if exErr != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "用户不存在"})
			return
		}
		if password == "" || IsPasswordHashed(password) {
			password = existing.Password
		} else {
			hashed, herr := HashPassword(password)
			if herr != nil {
				json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "密码加密失败"})
				return
			}
			password = hashed
		}
		if phone == "" {
			phone = existing.Phone
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
		// 响应脱敏：API Key 仅返回前 8 位掩码（前端仅展示前缀，编辑回传空值表示未修改）
		maskUserModelAPIKeys(models)
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: models})
	case "reveal_key":
		if req.ID == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "缺少模型 ID"})
			return
		}
		model, err := modelsdb.GetUserModelByID(req.ID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "模型不存在: " + err.Error()})
			return
		}
		if model.UserID != req.UserID {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "无权访问该模型"})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: map[string]interface{}{"api_key": model.APIKey}})
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
