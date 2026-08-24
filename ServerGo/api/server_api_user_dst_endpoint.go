package api

import (
	"encoding/json"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/system"
	"net/http"
)

// UserDstEndPointInterfaceRequest 用户源站管理查询请求
type UserDstEndPointInterfaceRequest struct {
	Action string `json:"action"`
	ID     uint64 `json:"id"`
}

// UserDstEndPointInterfaceResponse 用户源站管理查询响应
type UserDstEndPointInterfaceResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// userDstEndPointInterfaceHandle 用户源站管理 API（JWT 鉴权 + 只读 + 归属校验）
// 仅支持 list 和 test 操作，禁止 add/update/delete
func userDstEndPointInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "未登录",
		})
		return
	}

	var req UserDstEndPointInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}

	switch req.Action {
	case "", "list":
		handleUserDstEndPointList(w, claims)
	case "test":
		handleUserDstEndPointTest(w, claims, req.ID)
	case "chat_info":
		handleUserDstEndPointChatInfo(w, claims, req.ID)
	case "chat_sync":
		handleUserDstEndPointChatSync(w, claims, r)
	case "chat_clear":
		handleUserDstEndPointChatClear(w, claims, req.ID)
	default:
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "无权执行该操作: " + req.Action,
		})
	}
}

// handleUserDstEndPointList 返回当前用户的源站列表（不含 API Key）
func handleUserDstEndPointList(w http.ResponseWriter, claims *UserTokenClaims) {
	endpoints, err := modelsdb.GetDstEndPointsByUserID(claims.UserID)
	if err != nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "获取源站列表失败: " + err.Error(),
		})
		return
	}

	// 过滤掉 API Key，不在响应中暴露
	var result []map[string]interface{}
	for _, ep := range endpoints {
		result = append(result, map[string]interface{}{
			"id":            ep.ID,
			"user_id":       ep.UserID,
			"platform_name": ep.PlatformName,
			"model_name":    ep.ModelName,
			"protocol_type": ep.ProtocolType,
			"url_address":   ep.URLAddress,
			"status":        ep.Status,
		})
	}

	json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
		Success: true,
		Message: "查询成功",
		Data:    result,
	})
}

// handleUserDstEndPointChatInfo 返回当前用户拥有的源站完整信息（含 API Key），用于前端对话功能
func handleUserDstEndPointChatInfo(w http.ResponseWriter, claims *UserTokenClaims, id uint64) {
	if id == 0 {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站 ID 不能为空",
		})
		return
	}

	item, err := modelsdb.GetDstEndPointByID(id)
	if err != nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "获取源站信息失败: " + err.Error(),
		})
		return
	}
	if item == nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站不存在",
		})
		return
	}

	// 归属校验
	if item.UserID != claims.UserID {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "无权访问该源站",
		})
		return
	}

	// 同时返回服务器内存中的对话历史
	session := modelsdb.GetOrCreateChatSession(id)
	json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
		Success: true,
		Data: map[string]interface{}{
			"id":                   item.ID,
			"platform_name":        item.PlatformName,
			"model_name":           item.ModelName,
			"protocol_type":        item.ProtocolType,
			"url_address":          item.URLAddress,
			"api_key":              item.APIKey,
			"status":               item.Status,
			"server_history":       session.Messages,
			"server_system_prompt": session.SystemPrompt,
		},
	})
}

// handleUserDstEndPointChatSync 用户端同步对话历史到服务器内存
func handleUserDstEndPointChatSync(w http.ResponseWriter, claims *UserTokenClaims, r *http.Request) {
	var syncReq struct {
		ID           uint64                     `json:"id"`
		SystemPrompt string                     `json:"system_prompt"`
		Messages     []modelsdb.ChatHistoryItem `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&syncReq); err != nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}

	// 归属校验
	item, err := modelsdb.GetDstEndPointByID(syncReq.ID)
	if err != nil || item == nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站不存在",
		})
		return
	}
	if item.UserID != claims.UserID {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "无权访问该源站",
		})
		return
	}

	modelsdb.UpdateChatSession(syncReq.ID, syncReq.SystemPrompt, syncReq.Messages)
	json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
		Success: true,
		Message: "同步成功",
	})
}

// handleUserDstEndPointChatClear 用户端清空服务器内存中的对话历史
func handleUserDstEndPointChatClear(w http.ResponseWriter, claims *UserTokenClaims, id uint64) {
	if id == 0 {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站 ID 不能为空",
		})
		return
	}

	item, err := modelsdb.GetDstEndPointByID(id)
	if err != nil || item == nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站不存在",
		})
		return
	}
	if item.UserID != claims.UserID {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "无权访问该源站",
		})
		return
	}

	modelsdb.ClearChatSession(id)
	json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
		Success: true,
		Message: "已清空服务器对话历史",
	})
}

// handleUserDstEndPointTest 测试当前用户拥有的源站连通性
func handleUserDstEndPointTest(w http.ResponseWriter, claims *UserTokenClaims, id uint64) {
	if id == 0 {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站 ID 不能为空",
		})
		return
	}

	// 1. 获取源站信息
	item, err := modelsdb.GetDstEndPointByID(id)
	if err != nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "获取源站信息失败: " + err.Error(),
		})
		return
	}
	if item == nil {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "源站不存在",
		})
		return
	}

	// 2. 归属校验：确保源站属于当前用户
	if item.UserID != claims.UserID {
		json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
			Success: false,
			Message: "无权测试该源站",
		})
		return
	}

	// 3. 执行连通性测试（传入当前用户信息用于记录保存）
	result := system.TestDstEndPointConnectivityWithResult(item, claims.UserName, claims.UserID)
	json.NewEncoder(w).Encode(UserDstEndPointInterfaceResponse{
		Success: result.Success,
		Message: result.Message,
		Data:    result,
	})
}
