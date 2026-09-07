package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
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
		Action    string `json:"action"`
		Days      int    `json:"days"`
		Hours     int    `json:"hours"` // trend 用：1~720；<=0 视为 24
		ModelName string `json:"model_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}
	req.ModelName = strings.TrimSpace(req.ModelName)
	// 阶段BV：user_name 强制以 JWT claims 为准（前端传了也无效），防止越权
	effectiveUserName := claims.UserName
	effectiveModelName := req.ModelName

	switch req.Action {
	case "trend":
		// 小时粒度 K 线图数据：JWT claims 保证越权防护。
		// 阶段BV：若指定 model_name，则走单模型分表；否则按本人全模型聚合
		var res *modelsdb.HourlyTrendResult
		var err error
		if effectiveModelName != "" {
			res, err = modelsdb.GetHourlyTrendByUserModel(
				effectiveUserName, effectiveModelName, config.G.DBMysqlSubTableNumber, req.Hours)
		} else {
			userModels, muErr := modelsdb.GetUserModelsByUserID(claims.UserID)
			if muErr != nil {
				json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{Success: false, Message: muErr.Error()})
				return
			}
			modelNames := make([]string, 0, len(userModels))
			for _, userModel := range userModels {
				modelNames = append(modelNames, userModel.ModelName)
			}
			res, err = modelsdb.GetHourlyTrendByUser(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Hours)
		}
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
		var summary *modelsdb.AgentInfoUsageSummary
		var agents []modelsdb.AgentInfoUsageStat
		var statsErr error
		if effectiveModelName != "" {
			// 阶段BV：用户端单模型视角
			summary, agents, statsErr = modelsdb.GetAgentInfoUsageStatsByUserModel(
				effectiveUserName, effectiveModelName, config.G.DBMysqlSubTableNumber, req.Days)
		} else {
			// 原行为：本人全模型聚合
			userModels, muErr := modelsdb.GetUserModelsByUserID(claims.UserID)
			if muErr != nil {
				json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
					Success: false,
					Message: muErr.Error(),
				})
				return
			}
			modelNames := make([]string, 0, len(userModels))
			for _, userModel := range userModels {
				modelNames = append(modelNames, userModel.ModelName)
			}
			summary, agents, statsErr = modelsdb.GetAgentInfoUsageStatsByUser(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Days)
		}
		if statsErr != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: statsErr.Error(),
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
