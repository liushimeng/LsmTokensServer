package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"net/http"
	"strconv"
)

// ChatDialogInterfaceRequest 对话配置查询请求
type ChatDialogInterfaceRequest struct {
	Action    string `json:"action"`     // "models" | "config"
	UserName  string `json:"user_name"`  // 管理员必填
	ModelName string `json:"model_name"` // config action 必填
}

// ChatDialogInterfaceResponse 对话配置查询响应
type ChatDialogInterfaceResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// chatDialogInterfaceHandle 管理员对话配置 API
func chatDialogInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "仅支持 POST"})
		return
	}

	var req ChatDialogInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "models":
		handleChatDialogModels(w, req.UserName)
	case "config":
		handleChatDialogConfig(w, req.UserName, req.ModelName)
	default:
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "未知操作: " + req.Action})
	}
}

// userChatDialogInterfaceHandle 用户对话配置 API（JWT 鉴权）
func userChatDialogInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "仅支持 POST"})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "未登录"})
		return
	}

	var req ChatDialogInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "models":
		handleChatDialogModelsByUserID(w, claims.UserID)
	case "config":
		if req.ModelName == "" {
			json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "缺少 model_name 参数"})
			return
		}
		if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
			json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: err.Error()})
			return
		}
		handleChatDialogConfigByUserID(w, claims.UserID, req.ModelName)
	default:
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "未知操作: " + req.Action})
	}
}

// handleChatDialogModels 管理员获取用户模型列表
func handleChatDialogModels(w http.ResponseWriter, userName string) {
	if userName == "" {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "缺少 user_name 参数"})
		return
	}
	user, err := modelsdb.GetUserByName(userName)
	if err != nil {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "用户不存在"})
		return
	}
	handleChatDialogModelsByUserID(w, user.ID)
}

// handleChatDialogModelsByUserID 根据用户ID获取模型列表
func handleChatDialogModelsByUserID(w http.ResponseWriter, userID uint64) {
	models, err := modelsdb.GetUserModelsByUserID(userID)
	if err != nil {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "获取模型列表失败: " + err.Error()})
		return
	}

	var result []map[string]interface{}
	for _, m := range models {
		keyDisplay := ""
		if len(m.APIKey) > 8 {
			keyDisplay = m.APIKey[:8] + "****"
		} else if m.APIKey != "" {
			keyDisplay = "****"
		}
		result = append(result, map[string]interface{}{
			"id":             m.ID,
			"model_name":     m.ModelName,
			"api_key_masked": keyDisplay,
		})
	}

	json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: true, Data: result})
}

// handleChatDialogConfig 管理员获取模型对话配置
func handleChatDialogConfig(w http.ResponseWriter, userName, modelName string) {
	if userName == "" {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "缺少 user_name 参数"})
		return
	}
	user, err := modelsdb.GetUserByName(userName)
	if err != nil {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "用户不存在"})
		return
	}
	handleChatDialogConfigByUserID(w, user.ID, modelName)
}

// handleChatDialogConfigByUserID 根据用户ID获取模型对话配置
func handleChatDialogConfigByUserID(w http.ResponseWriter, userID uint64, modelName string) {
	if modelName == "" {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "缺少 model_name 参数"})
		return
	}

	// 获取用户模型信息（含 API Key）
	model, err := modelsdb.GetUserModelByUserIDAndModelName(userID, modelName)
	if err != nil {
		json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{Success: false, Message: "模型不存在: " + err.Error()})
		return
	}

	// 获取路由信息（确定协议类型）
	routes, _ := modelsdb.GetAIRoutesByUserModelID(model.ID)
	protocolType := 0
	var dstEndpointIDs []uint64
	if len(routes) > 0 {
		protocolType = routes[0].ProtocolType
		dstEndpointIDs, _ = modelsdb.ParseDstEndPointIDList(routes[0].DstEndPointIDList)
	}

	// 获取用户信息（用于返回 + protocolType 回退推断）
	user, _ := modelsdb.GetCachedUserByID(userID)
	userName := ""
	if user != nil {
		userName = user.UserName
	}

	// 无路由时从用户启用的协议回退推断
	if protocolType == 0 && user != nil {
		if user.AnthropicEnabled {
			protocolType = protocol.AgentProtocolType_Anthropic
		} else if user.OpenAIEnabled {
			protocolType = protocol.AgentProtocolType_OpenAI
		}
	}

	// 获取源站列表信息
	var endpoints []map[string]interface{}
	for _, epID := range dstEndpointIDs {
		ep, err := modelsdb.GetDstEndPointByID(epID)
		if err != nil || ep == nil {
			continue
		}
		endpoints = append(endpoints, map[string]interface{}{
			"id":            ep.ID,
			"platform_name": ep.PlatformName,
			"model_name":    ep.ModelName,
			"protocol_type": ep.ProtocolType,
			"status":        ep.Status,
		})
	}

	// 确定代理 URL 路径前缀
	proxyPath := config.G.AgentAnthropicListenURL
	if protocolType == protocol.AgentProtocolType_OpenAI {
		proxyPath = config.G.AgentOpenAIListenURL
	}

	// 构建代理服务基础 URL（供前端 JS 直接拼接完整 API 地址）
	agentBaseURL := "http://" + config.G.AgentProductListenAddr + ":" + strconv.Itoa(config.G.AgentListenPort)

	json.NewEncoder(w).Encode(ChatDialogInterfaceResponse{
		Success: true,
		Data: map[string]interface{}{
			"model_id":             model.ID,
			"model_name":           model.ModelName,
			"api_key":              model.APIKey,
			"protocol_type":        protocolType,
			"agent_addr":           config.G.AgentProductListenAddr,
			"agent_port":           config.G.AgentListenPort,
			"proxy_path":           proxyPath,
			"anthropic_proxy_path": config.G.AgentAnthropicListenURL,
			"openai_proxy_path":    config.G.AgentOpenAIListenURL,
			"agent_base_url":       agentBaseURL,
			"user_name":            userName,
			"endpoints":            endpoints,
		},
	})
}
