package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
)

// userAgentInfoInterfaceHandle 用户 Agent 信息统计 API（用户维度，只读）
func userAgentInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "未登录",
		})
		return
	}

	var req struct {
		Action string `json:"action"`
		Days   int    `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}

	switch req.Action {
	case "", "stats":
		userModels, err := modelsdb.GetUserModelsByUserID(claims.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		modelNames := make([]string, 0, len(userModels))
		for _, userModel := range userModels {
			modelNames = append(modelNames, userModel.ModelName)
		}
		summary, agents, err := modelsdb.GetAgentInfoUsageStatsByUser(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Days)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "查询成功",
			"data":    agentInfoStatsData{Summary: summary, Agents: agents},
		})
	default:
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "未知操作: " + req.Action,
		})
	}
}
