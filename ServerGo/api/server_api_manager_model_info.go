package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"sync"
)

// ModelInfoManageInterfaceRequest 模型信息管理请求
type ModelInfoManageInterfaceRequest struct {
	Action            string  `json:"action"`
	ID                uint64  `json:"id"`
	ModelName         string  `json:"model_name"`
	Description       string  `json:"description"`
	CostPer100wInput  float64 `json:"cost_per_100w_input"`
	CostPer100wOutput float64 `json:"cost_per_100w_output"`
	MaxContextLength  int     `json:"max_context_length"`
}

// modelInfoStatsData 模型信息统计响应数据
type modelInfoStatsData struct {
	Summary    *modelsdb.ModelInfoUsageSummary `json:"summary"`
	Models     []modelsdb.ModelInfoUsageStat   `json:"models"`
	DstSummary *modelsdb.ModelInfoUsageSummary `json:"dst_summary,omitempty"`
	DstModels  []modelsdb.ModelInfoUsageStat   `json:"dst_models,omitempty"`
	Trend      []modelsdb.DailyStat            `json:"trend,omitempty"`
}

// modelInfoInterfaceHandle 管理员模型信息统计 API
func modelInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}

	var req struct {
		Action string `json:"action"`
		Days   int    `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "", "stats":
		var summary *modelsdb.ModelInfoUsageSummary
		var modelList []modelsdb.ModelInfoUsageStat
		var trend []modelsdb.DailyStat
		var statsErr, trendErr error

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			summary, modelList, statsErr = modelsdb.GetModelInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, req.Days)
		}()
		go func() {
			defer wg.Done()
			trend, trendErr = modelsdb.GetDailyStatsAll(config.G.DBMysqlSubTableNumber, req.Days)
		}()
		wg.Wait()

		if statsErr != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: statsErr.Error()})
			return
		}
		if trendErr != nil {
			logger.Printf("[WARNING] ModelInfoInterface trend stats failed: %v", trendErr)
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: "查询成功",
			Data:    modelInfoStatsData{Summary: summary, Models: modelList, Trend: trend},
		})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}

// modelInfoManageInterfaceHandle 模型信息管理 API
func modelInfoManageInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}

	var req ModelInfoManageInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "list":
		items, err := modelsdb.GetAllModelInfos(0, 0)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		//  enrich with endpoint count and usage stats
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
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: result})
	case "add":
		item := &modelsdb.TAgentModelInfo{
			ModelName:         req.ModelName,
			Description:       req.Description,
			CostPer100wInput:  req.CostPer100wInput,
			CostPer100wOutput: req.CostPer100wOutput,
			MaxContextLength:  req.MaxContextLength,
		}
		if err := modelsdb.AddModelInfo(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "添加成功", Data: item})
	case "update":
		item := &modelsdb.TAgentModelInfo{
			ID:                req.ID,
			ModelName:         req.ModelName,
			Description:       req.Description,
			CostPer100wInput:  req.CostPer100wInput,
			CostPer100wOutput: req.CostPer100wOutput,
			MaxContextLength:  req.MaxContextLength,
		}
		if err := modelsdb.UpdateModelInfo(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "更新成功"})
	case "delete":
		if err := modelsdb.DeleteModelInfo(req.ID); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "删除成功"})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}
