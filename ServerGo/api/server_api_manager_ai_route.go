package api

import (
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strconv"
	"time"
)

// aiRouteManageInterfaceHandle 智能路由管理 API
func aiRouteManageInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}
	var req struct {
		Action                       string                        `json:"action"`
		ID                           uint64                        `json:"id"`
		UserID                       uint64                        `json:"user_id"`
		UserName                     string                        `json:"user_name"`
		UserModelID                  uint64                        `json:"user_model_id"`
		ModelName                    string                        `json:"model_name"`
		ProtocolType                 int                           `json:"protocol_type"`
		DstEndPointID                uint64                        `json:"dst_endpoint_id"`
		DstEndPointIDList            string                        `json:"dst_endpoint_id_list"`
		DstEndPointIDStatusList      string                        `json:"dst_endpoint_id_status_list"`
		DstEndPointAlgorithmTypeList string                        `json:"dst_endpoint_algorithm_type_list"`
		AlgorithmStrategyType        int                           `json:"algorithm_strategy_type"`
		Days                         int                           `json:"days"`
		BatchIDs                     []uint64                      `json:"ids"`
		EndpointIDs                  []uint64                      `json:"endpoint_ids"`
		BatchItems                   []modelsdb.RouteBatchStatItem `json:"batch_items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "list":
		if req.UserID == 0 {
			// 返回所有用户的路由
			users, err := modelsdb.GetAllUsers(0, 0)
			if err != nil {
				json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
				return
			}
			// v2.0.37+：先逐 user 取路由 → 一次性查所有路由的 (last_used_at) 批量聚合
			var allRoutes []modelsdb.TAgentHttpAIRoute
			userNameByRouteID := make(map[uint64]string)
			for _, u := range users {
				routes, err := modelsdb.GetAIRoutesByUserID(u.ID)
				if err != nil {
					continue
				}
				for _, route := range routes {
					userNameByRouteID[route.ID] = u.UserName
					allRoutes = append(allRoutes, route)
				}
			}
			// 批量聚合 last_used：替代对每条路由调 modelsdb.GetRouteLastUsedTime 的 N+1
			batchItems := make([]modelsdb.RouteBatchStatItem, 0, len(allRoutes))
			for _, route := range allRoutes {
				batchItems = append(batchItems, modelsdb.RouteBatchStatItem{
					RouteID:  route.ID,
					Protocol: route.ProtocolType,
					Days:     0,
					Key: modelsdb.RouteBatchStatKey{
						UserName:     userNameByRouteID[route.ID],
						ModelName:    lookupRouteModelName(route.UserModelID),
						ProtocolType: route.ProtocolType,
					},
				})
			}
			// v2.0.66：last_used 改走专用快路径（ORDER BY id DESC LIMIT 1），
			// 不再复用 COUNT(*)+MAX(created_at) 聚合 —— 后者在 Days=0 时无法命中
			// 复合索引，实测 73560 行扫描并触发 30s 驱动超时，失败后静默补零值，
			// 导致本列恒显示「未使用」。
			statsMap := modelsdb.BatchGetRouteLastUsedTimes(batchItems, config.G.DBMysqlSubTableNumber)
			var enriched []map[string]interface{}
			for _, route := range allRoutes {
				enriched = append(enriched, enrichRoute(route, userNameByRouteID[route.ID], statsMap))
			}
			json.NewEncoder(w).Encode(userManageResp{Success: true, Data: enriched})
			return
		}
		routes, err := modelsdb.GetAIRoutesByUserID(req.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		user, _ := modelsdb.GetUserByID(req.UserID)
		userName := ""
		if user != nil {
			userName = user.UserName
		}
		// v2.0.37+：批量聚合 last_used
		batchItems := make([]modelsdb.RouteBatchStatItem, 0, len(routes))
		for _, route := range routes {
			batchItems = append(batchItems, modelsdb.RouteBatchStatItem{
				RouteID:  route.ID,
				Protocol: route.ProtocolType,
				Days:     0,
				Key: modelsdb.RouteBatchStatKey{
					UserName:     userName,
					ModelName:    lookupRouteModelName(route.UserModelID),
					ProtocolType: route.ProtocolType,
				},
			})
		}
		// v2.0.66：同上，last_used 走专用快路径
		statsMap := modelsdb.BatchGetRouteLastUsedTimes(batchItems, config.G.DBMysqlSubTableNumber)
		enriched := make([]map[string]interface{}, 0, len(routes))
		for _, route := range routes {
			enriched = append(enriched, enrichRoute(route, userName, statsMap))
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: enriched})
	case "list_models":
		models, err := modelsdb.GetUserModelsByUserID(req.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: models})
	case "list_endpoints":
		endpoints, err := modelsdb.GetDstEndPointsByUserID(req.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: endpoints})
	case "add":
		item := &modelsdb.TAgentHttpAIRoute{
			UserID:                       req.UserID,
			UserModelID:                  req.UserModelID,
			ProtocolType:                 req.ProtocolType,
			DstEndPointIDList:            req.DstEndPointIDList,
			DstEndPointAlgorithmTypeList: req.DstEndPointAlgorithmTypeList,
			AlgorithmStrategyType:        req.AlgorithmStrategyType,
		}
		// 前端兼容：如果前端只传了旧的 dst_endpoint_id，自动转换为列表
		if item.DstEndPointIDList == "" && req.DstEndPointID > 0 {
			item.DstEndPointIDList = strconv.FormatUint(req.DstEndPointID, 10)
		}
		if err := modelsdb.AddAIRoute(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "添加成功", Data: item})
	case "update":
		item := &modelsdb.TAgentHttpAIRoute{
			ID:                           req.ID,
			UserID:                       req.UserID,
			UserModelID:                  req.UserModelID,
			ProtocolType:                 req.ProtocolType,
			DstEndPointIDList:            req.DstEndPointIDList,
			DstEndPointIDStatusList:      req.DstEndPointIDStatusList,
			DstEndPointAlgorithmTypeList: req.DstEndPointAlgorithmTypeList,
			AlgorithmStrategyType:        req.AlgorithmStrategyType,
		}
		// 前端兼容：如果前端只传了旧的 dst_endpoint_id，自动转换为列表
		if item.DstEndPointIDList == "" && req.DstEndPointID > 0 {
			item.DstEndPointIDList = strconv.FormatUint(req.DstEndPointID, 10)
		}
		if err := modelsdb.UpdateAIRoute(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "更新成功"})
	case "count_record":
		handleManagerAIRouteCountRecord(w, req.UserName, req.ModelName, req.ProtocolType, req.Days)
	case "count_record_by_protocol":
		// v2.0.44：按协议拆分返回 {anthropic, openai, total}，前端用于「时间跨度统计」列
		// 协议区分展示。该接口不校验 protocolType 字段（用户端不传，避免历史页面残留 protocol_type 影响）。
		handleManagerAIRouteCountRecordByProtocol(w, req.UserName, req.ModelName, req.Days)
	case "batch_stats":
		// v2.0.37: 批量聚合多条路由的最后使用时间 + 记录数，解决 N+1 导致「加载中」
		if len(req.BatchItems) == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量统计需提供至少 1 条 (user_name, model_name, protocol_type)"})
			return
		}
		if len(req.BatchItems) > modelsdb.BatchRouteStatsKeyPairMax {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: fmt.Sprintf("批量统计单次上限 %d 条", modelsdb.BatchRouteStatsKeyPairMax)})
			return
		}
		batchResult, err := modelsdb.BatchGetRouteStatsByRouteIDs(req.BatchItems, config.G.DBMysqlSubTableNumber)
		if err != nil {
			logger.Printf("[AIRouteManage] batch_stats failed: %v", err)
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量查询失败: " + err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: batchResult})
	case "delete":
		if err := modelsdb.DeleteAIRoute(req.ID); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "删除成功"})
	case "batch_update":
		updates := map[string]interface{}{}
		if req.AlgorithmStrategyType != 0 {
			updates["algorithm_strategy_type"] = req.AlgorithmStrategyType
		}
		if req.DstEndPointIDList != "" {
			updates["dst_endpoint_id_list"] = req.DstEndPointIDList
			updates["dst_endpoint_id_status_list"] = req.DstEndPointIDStatusList
			updates["dst_endpoint_algorithm_type_list"] = req.DstEndPointAlgorithmTypeList
		}
		if len(updates) == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量更新需提供至少一个可更新字段（如 algorithm_strategy_type 或 dst_endpoint_id_list）"})
			return
		}
		updatedCount, errs := modelsdb.BatchUpdateAIRoute(req.BatchIDs, updates)
		if len(errs) > 0 {
			errMsgs := make([]string, 0, len(errs))
			for _, e := range errs {
				errMsgs = append(errMsgs, e.Error())
			}
			json.NewEncoder(w).Encode(userManageResp{
				Success: updatedCount > 0,
				Message: fmt.Sprintf("部分更新成功（%d/%d），%d 条失败", updatedCount, len(req.BatchIDs), len(errs)),
				Data:    map[string]interface{}{"updated_count": updatedCount, "error_count": len(errs), "errors": errMsgs},
			})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: fmt.Sprintf("批量更新成功，共更新 %d 条", updatedCount),
			Data:    map[string]interface{}{"updated_count": updatedCount},
		})
	case "batch_delete":
		deletedCount, errs := modelsdb.BatchDeleteAIRoute(req.BatchIDs)
		if len(errs) > 0 {
			errMsgs := make([]string, 0, len(errs))
			for _, e := range errs {
				errMsgs = append(errMsgs, e.Error())
			}
			json.NewEncoder(w).Encode(userManageResp{
				Success: deletedCount > 0,
				Message: fmt.Sprintf("部分删除成功（%d/%d），%d 条失败", deletedCount, len(req.BatchIDs), len(errs)),
				Data:    map[string]interface{}{"deleted_count": deletedCount, "error_count": len(errs), "errors": errMsgs},
			})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: fmt.Sprintf("批量删除成功，共删除 %d 条", deletedCount),
			Data:    map[string]interface{}{"deleted_count": deletedCount},
		})
	case "batch_append_endpoints":
		// v2.0.x: 批量向多条路由追加源站（逐条处理：已存在则跳过，不存在则追加）
		if len(req.BatchIDs) == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量追加需提供至少一条路由 ID"})
			return
		}
		if len(req.EndpointIDs) == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量追加需提供至少一个源站 ID"})
			return
		}
		result := modelsdb.BatchAddEndpointsToRoutes(req.BatchIDs, req.EndpointIDs, req.AlgorithmStrategyType)
		if result.FailCount > 0 && result.SuccessCount == 0 {
			json.NewEncoder(w).Encode(userManageResp{
				Success: false,
				Message: fmt.Sprintf("批量追加全部失败（%d 条）", result.FailCount),
				Data:    result,
			})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: fmt.Sprintf("批量追加完成：成功 %d 条，跳过 %d 条，失败 %d 条", result.SuccessCount, result.SkipCount, result.FailCount),
			Data:    result,
		})
	case "batch_remove_endpoints":
		// v2.0.x: 批量删除多条路由中的指定源站（逐条处理：不存在则跳过，列表为空则拒绝）
		if len(req.BatchIDs) == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量删除需提供至少一条路由 ID"})
			return
		}
		if len(req.EndpointIDs) == 0 {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "批量删除需提供至少一个源站 ID"})
			return
		}
		result := modelsdb.BatchRemoveEndpointsFromRoutes(req.BatchIDs, req.EndpointIDs, req.AlgorithmStrategyType)
		if result.FailCount > 0 && result.SuccessCount == 0 {
			json.NewEncoder(w).Encode(userManageResp{
				Success: false,
				Message: fmt.Sprintf("批量删除全部失败（%d 条）", result.FailCount),
				Data:    result,
			})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: fmt.Sprintf("批量删除完成：成功 %d 条，跳过 %d 条，失败 %d 条", result.SuccessCount, result.SkipCount, result.FailCount),
			Data:    result,
		})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}

// handleManagerAIRouteCountRecord 单条查询某用户某模型某协议类型的记录数
func handleManagerAIRouteCountRecord(w http.ResponseWriter, userName, modelName string, protocolType int, days int) {
	var count int64
	if userName != "" && modelName != "" {
		c, err := modelsdb.CountAgentHttpTransactionsByDays(userName, modelName, protocolType, config.G.DBMysqlSubTableNumber, days)
		if err != nil {
			logger.Printf("[AIRouteManage] count_record failed: user=%s model=%s pt=%d days=%d err=%v", userName, modelName, protocolType, days, err)
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "查询失败: " + err.Error()})
			return
		}
		count = c
	}
	json.NewEncoder(w).Encode(userManageResp{Success: true, Data: count})
}

// handleManagerAIRouteCountRecordByProtocol v2.0.44：单条查询某用户某模型下
// Anthropic / OpenAI 两协议的记录数 + 总数。前端用其协议区分显示「时间跨度统计」列。
//
// 参数:
//   - userName / modelName：必传，空时返回全 0（前端避免 undefined 类型错误）
//   - days：可负值（小时）或正值（天）或 0（无限制）；复用 v2.0.41 modelsdb.resolveStatsSpanCutoff helper
//
// 响应 data 字段：{anthropic, openai, total} —— 全部为 int64，未命中协议 = 0。
//
// 性能说明：仍然走 2 次 CountByDays（modelsdb.CountAgentHttpTransactionsByDays）而不是一条
// GROUP BY 跨协议 SQL。原因：(1) 同 (user, model) 所有记录哈希到同一个子表，2 次
// 跨协议 Count 是 2 次同子表 SQL（time span cache-friendly 命中 v2.0.41 复合索引）；
// (2) 2 协议 fallback 总成本 < 5ms；hot path 仍是 batch_stats GROUP BY 单 SQL。
func handleManagerAIRouteCountRecordByProtocol(w http.ResponseWriter, userName, modelName string, days int) {
	zeroResp := map[string]interface{}{
		"anthropic": int64(0),
		"openai":    int64(0),
		"total":     int64(0),
	}
	if userName == "" || modelName == "" {
		// 空入参兜底：返回全 0 避免前端 undefined
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "查询成功",
			"data":    zeroResp,
		})
		return
	}
	anth, errA := modelsdb.CountAgentHttpTransactionsByDays(userName, modelName, 1, config.G.DBMysqlSubTableNumber, days)
	open, errO := modelsdb.CountAgentHttpTransactionsByDays(userName, modelName, 2, config.G.DBMysqlSubTableNumber, days)
	if errA != nil {
		logger.Printf("[AIRouteManage] count_record_by_protocol anthropic failed: user=%s model=%s days=%d err=%v", userName, modelName, days, errA)
	}
	if errO != nil {
		logger.Printf("[AIRouteManage] count_record_by_protocol openai failed: user=%s model=%s days=%d err=%v", userName, modelName, days, errO)
	}
	if errA != nil && errO != nil {
		// 两个都失败：返回错误（前端降级为 - 显示）
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "查询失败: " + errA.Error()})
		return
	}
	if anth < 0 {
		anth = 0
	}
	if open < 0 {
		open = 0
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "查询成功",
		"data": map[string]interface{}{
			"anthropic": anth,
			"openai":    open,
			"total":     anth + open,
		},
	})
}

// enrichRoute 丰富路由信息，包含完整的源站列表和当前活动源站。
//
// v2.0.37+：最后使用时间 (last_used_at) 直接从传入的 statsMap（由调用方一次性
// modelsdb.BatchGetRouteStatsByRouteIDs 批量聚合得到）取，避免对每条路由都单独
// modelsdb.GetRouteLastUsedTime 触发 N+1 SQL。statsMap=nil 时退回旧路径（向后兼容）。
func enrichRoute(route modelsdb.TAgentHttpAIRoute, userName string, statsMap map[uint64]modelsdb.RouteBatchStatResult) map[string]interface{} {
	result := map[string]interface{}{
		"id":                               route.ID,
		"user_id":                          route.UserID,
		"user_name":                        userName,
		"user_model_id":                    route.UserModelID,
		"protocol_type":                    route.ProtocolType,
		"route_protocol_type":              route.ProtocolType,
		"dst_endpoint_id_list":             route.DstEndPointIDList,
		"dst_endpoint_algorithm_type_list": route.DstEndPointAlgorithmTypeList,
		"dst_endpoint_id_number":           route.DstEndPointIDNumber,
		"algorithm_strategy_type":          route.AlgorithmStrategyType,
		"algorithm_name":                   modelsdb.GetAlgorithmName(route.AlgorithmStrategyType),
	}
	model, err := modelsdb.GetUserModelByID(route.UserModelID)
	if err == nil && model != nil {
		result["model_name"] = model.ModelName
		result["api_key"] = model.APIKey

		// v2.0.71：注入「最后成功记录」「最后失败记录」两组字段。
		// 数据由 modelsdb.BatchGetRouteLastUsedTimes（内层 modelsdb.GetRouteLastRecordByStatus）批量产出，
		// 每组字段（时间/响应状态/目标模型）严格来自同一条记录。
		// *Failed 区分「暂无记录」与「查询失败」——禁止把数据库故障静默降级成
		// 「暂无成功/失败记录」（v2.0.66 同源陷阱）。
		var (
			succAt           time.Time
			succStatus       string
			succCode         int
			succDstModelName string
			succHasRecord    bool
			succFailed       bool
			failAt           time.Time
			failStatus       string
			failCode         int
			failDstModelName string
			failHasRecord    bool
			failFailed       bool
		)
		if statsMap != nil {
			if entry, ok := statsMap[route.ID]; ok {
				succAt = entry.LastSuccessAt
				succStatus = entry.LastSuccessStatus
				succCode = entry.LastSuccessStatusCode
				succDstModelName = entry.LastSuccessDstModelName
				succHasRecord = entry.LastSuccessHasRecord
				succFailed = entry.LastSuccessFailed
				failAt = entry.LastFailureAt
				failStatus = entry.LastFailureStatus
				failCode = entry.LastFailureStatusCode
				failDstModelName = entry.LastFailureDstModelName
				failHasRecord = entry.LastFailureHasRecord
				failFailed = entry.LastFailureFailed
			}
		}
		// 兜底：仅当 batch 路径没拿到（兼容旧调用方/单条场景）时回退到单条 SQL
		if statsMap == nil {
			// v2.0.71：单条兜底也走 modelsdb.GetRouteLastRecordByStatus，与批量路径同源同语义。
			if row, lookupErr := modelsdb.GetRouteLastRecordByStatus(userName, model.ModelName, route.ProtocolType, config.G.DBMysqlSubTableNumber, true); lookupErr == nil {
				succAt = row.CreatedAt
				succStatus = row.ResponseStatus
				succCode = modelsdb.ParseResponseStatusCode(row.ResponseStatus)
				succDstModelName = row.DstModelName
				succHasRecord = !row.CreatedAt.IsZero()
			} else {
				succFailed = true
			}
			if row, lookupErr := modelsdb.GetRouteLastRecordByStatus(userName, model.ModelName, route.ProtocolType, config.G.DBMysqlSubTableNumber, false); lookupErr == nil {
				failAt = row.CreatedAt
				failStatus = row.ResponseStatus
				failCode = modelsdb.ParseResponseStatusCode(row.ResponseStatus)
				failDstModelName = row.DstModelName
				failHasRecord = !row.CreatedAt.IsZero()
			} else {
				failFailed = true
			}
		}
		result["last_success_at"] = succAt
		result["last_success_at_unix"] = succAt.Unix()
		result["last_success_status"] = succStatus
		result["last_success_status_code"] = succCode
		result["last_success_dst_model_name"] = succDstModelName
		result["last_success_has_record"] = succHasRecord
		result["last_success_failed"] = succFailed
		switch {
		case succFailed:
			result["last_success_at_text"] = "查询失败"
		case !succHasRecord:
			result["last_success_at_text"] = "暂无成功记录"
		default:
			result["last_success_at_text"] = succAt.Format("2006-01-02 15:04:05")
		}
		result["last_failure_at"] = failAt
		result["last_failure_at_unix"] = failAt.Unix()
		result["last_failure_status"] = failStatus
		result["last_failure_status_code"] = failCode
		result["last_failure_dst_model_name"] = failDstModelName
		result["last_failure_has_record"] = failHasRecord
		result["last_failure_failed"] = failFailed
		switch {
		case failFailed:
			result["last_failure_at_text"] = "查询失败"
		case !failHasRecord:
			result["last_failure_at_text"] = "暂无失败记录"
		default:
			result["last_failure_at_text"] = failAt.Format("2006-01-02 15:04:05")
		}
	} else {
		// v2.0.66：模型查不到时同样属于故障，必须显式标记为「查询失败」。
		// 旧实现这里什么都不写，前端读到 undefined 后 fallback 成灰色「-」，
		// 与「未使用」视觉等同 —— 又是一处把故障伪装成正常状态的路径。
		// v2.0.71：两组字段全部显式置失败态（拿不到 model_name 无法定位分表，
		// 查询必然失败），禁止把故障渲染成「暂无记录」。
		result["last_success_at"] = time.Time{}
		result["last_success_at_unix"] = int64(0)
		result["last_success_status"] = ""
		result["last_success_status_code"] = 0
		result["last_success_dst_model_name"] = ""
		result["last_success_has_record"] = false
		result["last_success_failed"] = true
		result["last_success_at_text"] = "查询失败"
		result["last_failure_at"] = time.Time{}
		result["last_failure_at_unix"] = int64(0)
		result["last_failure_status"] = ""
		result["last_failure_status_code"] = 0
		result["last_failure_dst_model_name"] = ""
		result["last_failure_has_record"] = false
		result["last_failure_failed"] = true
		result["last_failure_at_text"] = "查询失败"
	}

	// 构建完整的源站列表信息
	ids, _ := modelsdb.ParseDstEndPointIDList(route.DstEndPointIDList)
	_, algorithmTypes, _ := modelsdb.NormalizeDstEndPointAlgorithmTypeList(route.DstEndPointIDList, route.DstEndPointAlgorithmTypeList)
	_, statuses, _ := modelsdb.NormalizeDstEndPointIDStatusList(route.DstEndPointIDList, route.DstEndPointIDStatusList)
	var endpointList []map[string]interface{}
	for i, id := range ids {
		ep, err := modelsdb.GetDstEndPointByID(id)
		if err == nil && ep != nil {
			algorithmType := modelsdb.DstEndPointAlgorithmType_Direct
			if i < len(algorithmTypes) {
				algorithmType = algorithmTypes[i]
			}
			inRouteStatus := 1
			if i < len(statuses) {
				inRouteStatus = statuses[i]
			}
			endpointList = append(endpointList, map[string]interface{}{
				"id":              ep.ID,
				"platform_name":   ep.PlatformName,
				"model_name":      ep.ModelName,
				"url_address":     ep.URLAddress,
				"protocol_type":   ep.ProtocolType,
				"status":          ep.Status,
				"in_route_status": inRouteStatus,
				"algorithm_type":  algorithmType,
				"algorithm_name":  modelsdb.GetDstEndPointAlgorithmTypeName(algorithmType),
			})
		}
	}
	result["endpoint_list"] = endpointList

	// 稳定型与指定型的 "当前生效源站" 都等于列表第 0 个；
	// 滚动切换由 modelsdb.RotateAIRouteEndpointList 直接修改列表本身，不再维护内存 ActiveIndex。
	// active_endpoint_index 固定 0，前端 renderEndpointList 仍按 (route.active_endpoint_index || 0) 取值。
	result["active_endpoint_index"] = 0

	// 当前实际使用的源站（用于前端默认显示）
	var currentEndpointID uint64 = 0
	if len(ids) > 0 {
		currentEndpointID = ids[0]
	}
	result["current_endpoint_id"] = currentEndpointID

	// 兼容旧字段：第一个源站 / 当前使用的源站
	selectedID := currentEndpointID
	if selectedID == 0 && len(ids) > 0 {
		selectedID = ids[0]
	}
	endpoint, err := modelsdb.GetDstEndPointByID(selectedID)
	if err == nil && endpoint != nil {
		result["platform_name"] = endpoint.PlatformName
		result["endpoint_model_name"] = endpoint.ModelName
		result["url_address"] = endpoint.URLAddress
		result["endpoint_protocol_type"] = endpoint.ProtocolType
		result["dst_endpoint_id"] = endpoint.ID
	}
	return result
}

// lookupRouteModelName 解析路由关联的 user_model_id 对应的 model_name，
// 用于 list 接口批量聚合 last_used 时构造 modelsdb.RouteBatchStatKey.Key.ModelName。
// 失败/不存在时返回空串（modelsdb.BatchGetRouteStatsByRouteIDs 内部已对空 user/model 跳过）。
func lookupRouteModelName(userModelID uint64) string {
	if userModelID == 0 {
		return ""
	}
	m, err := modelsdb.GetUserModelByID(userModelID)
	if err != nil || m == nil {
		return ""
	}
	return m.ModelName
}
