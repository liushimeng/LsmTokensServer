package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
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
		summary, models, err := modelsdb.GetModelInfoUsageStatsByUser(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Days)
		if err != nil {
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		dstSummary, dstModels, err := modelsdb.GetModelInfoUsageStatsByUserDstModel(claims.UserName, modelNames, config.G.DBMysqlSubTableNumber, req.Days)
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
