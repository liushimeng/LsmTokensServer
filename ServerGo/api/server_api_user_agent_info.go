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
		Hours  int    `json:"hours"` // trend 用：1~720；<=0 视为 24
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}

	switch req.Action {
	case "trend":
		// 小时粒度 K 线图数据：仅扫本用户模型对应的分表，JWT claims 保证越权防护。
		userModels, err := modelsdb.GetUserModelsByUserID(claims.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{Success: false, Message: err.Error()})
			return
		}
		modelNames := make([]string, 0, len(userModels))
		for _, userModel := range userModels {
			modelNames = append(modelNames, userModel.ModelName)
		}
		res, err := modelsdb.GetHourlyTrendByUser(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Hours)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "查询成功",
			"data":    res,
		})
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
