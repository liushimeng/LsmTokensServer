package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// userModelInfoInterfaceHandle 用户模型信息 API（只读）
func userModelInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
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
	case "", "list":
		// 获取用户的所有模型（从用户模型映射表）
		userModels, err := modelsdb.GetUserModelsByUserID(claims.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}

		var result []map[string]interface{}
		for _, userModel := range userModels {
			// 按用户模型名称统计调用次数和Tokens
			usageStats, _ := modelsdb.GetModelUsageStatsByUser(claims.UserName, userModel.ModelName, config.G.DBMysqlSubTableNumber)
			if usageStats == nil {
				usageStats = &modelsdb.ModelUsageStats{}
			}

			// 查询对应的模型信息（如果有）
			modelInfo, _ := modelsdb.GetModelInfoByName(userModel.ModelName)
			if modelInfo == nil {
				modelInfo = &modelsdb.TAgentModelInfo{ModelName: userModel.ModelName}
			}

			m := map[string]interface{}{
				"id":                   modelInfo.ID,
				"model_name":           userModel.ModelName,
				"description":          modelInfo.Description,
				"cost_per_100w_input":  modelInfo.CostPer100wInput,
				"cost_per_100w_output": modelInfo.CostPer100wOutput,
				"max_context_length":   modelInfo.MaxContextLength,
				"avg_ttfb_ms":          modelInfo.AvgTTFBms,
				"avg_elapsed_ms":       modelInfo.AvgElapsedMs,
				"tokens_per_second":    modelInfo.TokensPerSecond,
				"success_rate":         modelInfo.SuccessRate,
				"error_429_rate":       modelInfo.Error429Rate,
				"error_5xx_rate":       modelInfo.Error5xxRate,
				"endpoint_count":       modelsdb.GetEndpointCountByModelName(userModel.ModelName),
				"call_count":           usageStats.CallCount,
				"tokens_all_size":      usageStats.TokensAllSize,
				"tokens_input_size":    usageStats.TokensInputSize,
				"tokens_output_size":   usageStats.TokensOutputSize,
			}
			result = append(result, m)
		}
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: true,
			Message: "查询成功",
			Data:    result,
		})
	case "stats":
		var summary *modelsdb.ModelInfoUsageSummary
		var models []modelsdb.ModelInfoUsageStat
		var statsErr error
		if effectiveModelName != "" {
			// 阶段BV：用户端单模型视角
			summary, models, statsErr = modelsdb.GetModelInfoUsageStatsByUserModel(
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
			summary, models, statsErr = modelsdb.GetModelInfoUsageStatsByUser(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Days)
		}
		if statsErr != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: statsErr.Error(),
			})
			return
		}
		// dst_summary / dst_models 仅在「本人全模型」视角下展示；
		// 单模型视角下 dst 列表即 models 本身，dstSummary/Models 与 summary/Models 重复。
		var dstSummary *modelsdb.ModelInfoUsageSummary
		var dstModels []modelsdb.ModelInfoUsageStat
		if effectiveModelName == "" {
			userModels, _ := modelsdb.GetUserModelsByUserID(claims.UserID)
			modelNames := make([]string, 0, len(userModels))
			for _, userModel := range userModels {
				modelNames = append(modelNames, userModel.ModelName)
			}
			dstSummary, dstModels, _ = modelsdb.GetModelInfoUsageStatsByUserDstModel(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Days)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "查询成功",
			"data":    modelInfoStatsData{Summary: summary, Models: models, DstSummary: dstSummary, DstModels: dstModels},
		})
	case "list_all":
		// 获取全平台所有模型信息（只读）
		items, err := modelsdb.GetAllModelInfos(0, 0)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		var result []map[string]interface{}
		for _, item := range items {
			usageStats, _ := modelsdb.GetModelUsageStatsAll(item.ModelName, config.G.DBMysqlSubTableNumber)
			if usageStats == nil {
				usageStats = &modelsdb.ModelUsageStats{}
			}
			m := map[string]interface{}{
				"id":                   item.ID,
				"model_name":           item.ModelName,
				"description":          item.Description,
				"cost_per_100w_input":  item.CostPer100wInput,
				"cost_per_100w_output": item.CostPer100wOutput,
				"max_context_length":   item.MaxContextLength,
				"avg_ttfb_ms":          item.AvgTTFBms,
				"avg_elapsed_ms":       item.AvgElapsedMs,
				"tokens_per_second":    item.TokensPerSecond,
				"success_rate":         item.SuccessRate,
				"error_429_rate":       item.Error429Rate,
				"error_5xx_rate":       item.Error5xxRate,
				"endpoint_count":       modelsdb.GetEndpointCountByModelName(item.ModelName),
				"call_count":           usageStats.CallCount,
				"tokens_all_size":      usageStats.TokensAllSize,
				"tokens_input_size":    usageStats.TokensInputSize,
				"tokens_output_size":   usageStats.TokensOutputSize,
			}
			result = append(result, m)
		}
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: true,
			Message: "查询成功",
			Data:    result,
		})
	default:
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "未知操作: " + req.Action,
		})
	}
}
