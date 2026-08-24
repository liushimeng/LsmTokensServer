package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strconv"
	"time"
)

// UserAIRouteInterfaceRequest 用户智能路由查询请求
type UserAIRouteInterfaceRequest struct {
	Action                       string `json:"action"`
	ID                           uint64 `json:"id"`
	DstEndPointID                uint64 `json:"dst_endpoint_id"`
	DstEndPointIDList            string `json:"dst_endpoint_id_list"`
	DstEndPointIDStatusList      string `json:"dst_endpoint_id_status_list"`
	DstEndPointAlgorithmTypeList string `json:"dst_endpoint_algorithm_type_list"`
	AlgorithmStrategyType        int    `json:"algorithm_strategy_type"`
	ModelName                    string `json:"model_name"`
	ProtocolType                 int    `json:"protocol_type"`
	Days                         int    `json:"days"`
}

// UserAIRouteInterfaceResponse 用户智能路由查询响应
type UserAIRouteInterfaceResponse struct {
	Success bool                     `json:"success"`
	Message string                   `json:"message"`
	Data    []map[string]interface{} `json:"data,omitempty"`
}

// userAIRouteInterfaceHandle 用户智能路由 API（JWT 鉴权 + 模型归属校验）
// 支持查询(list)和更新(update)操作
func userAIRouteInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

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

	var req UserAIRouteInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}

	switch req.Action {
	case "", "list":
		handleUserAIRouteList(w, claims)
	case "list_endpoints":
		handleUserEndpointList(w, claims)
	case "count_record":
		handleUserAIRouteCountRecord(w, claims, req.ModelName, req.ProtocolType, req.Days)
	case "count_record_by_protocol":
		// v2.0.44：用户端按协议拆分记录数接口，返回 {anthropic, openai, total}；
		// 不接收 protocol_type 参数（用户端 JWT 已隐式校验，越权不可能）
		handleUserAIRouteCountRecordByProtocol(w, claims, req.ModelName, req.Days)
	case "update":
		handleUserAIRouteUpdate(w, claims, &req)
	default:
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "未知操作: " + req.Action,
		})
	}
}

func handleUserAIRouteList(w http.ResponseWriter, claims *UserTokenClaims) {
	models, err := modelsdb.GetUserModelsByUserID(claims.UserID)
	if err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "获取模型列表失败: " + err.Error(),
		})
		return
	}

	// v2.0.66：用户端此前是逐条 modelsdb.GetRouteLastUsedTime 的 N+1，且失败时
	// 走 modelsdb.GetRouteLastUsedTime 的零值缓存（5 分钟 TTL），一次超时会让
	// 「最后使用」列在整个 TTL 窗口内持续显示错误状态。改为与管理员端一致的
	// 批量快路径，并把「查询失败」与「未使用」区分开。
	type userRouteEntry struct {
		route     modelsdb.TAgentHttpAIRoute
		modelName string
	}
	var routeEntries []userRouteEntry
	var batchItems []modelsdb.RouteBatchStatItem
	for _, model := range models {
		routes, err := modelsdb.GetAIRoutesByUserModelID(model.ID)
		if err != nil {
			continue
		}
		for _, route := range routes {
			routeEntries = append(routeEntries, userRouteEntry{route: route, modelName: model.ModelName})
			batchItems = append(batchItems, modelsdb.RouteBatchStatItem{
				RouteID:  route.ID,
				Protocol: route.ProtocolType,
				Days:     0,
				Key: modelsdb.RouteBatchStatKey{
					UserName:     claims.UserName,
					ModelName:    model.ModelName,
					ProtocolType: route.ProtocolType,
				},
			})
		}
	}
	statsMap := modelsdb.BatchGetRouteLastUsedTimes(batchItems, config.G.DBMysqlSubTableNumber)

	var allRoutes []map[string]interface{}
	for _, e := range routeEntries {
		enriched := enrichRouteForUser(e.route, claims.UserName, e.modelName, statsMap)
		if enriched != nil {
			allRoutes = append(allRoutes, enriched)
		}
	}

	json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
		Success: true,
		Message: "查询成功",
		Data:    allRoutes,
	})
}

func handleUserEndpointList(w http.ResponseWriter, claims *UserTokenClaims) {
	endpoints, err := modelsdb.GetDstEndPointsByUserID(claims.UserID)
	if err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "获取源站列表失败: " + err.Error(),
		})
		return
	}

	var result []map[string]interface{}
	for _, ep := range endpoints {
		result = append(result, map[string]interface{}{
			"id":            ep.ID,
			"platform_name": ep.PlatformName,
			"model_name":    ep.ModelName,
			"protocol_type": ep.ProtocolType,
			"url_address":   ep.URLAddress,
			"status":        ep.Status,
		})
	}

	json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
		Success: true,
		Message: "查询成功",
		Data:    result,
	})
}

func handleUserAIRouteUpdate(w http.ResponseWriter, claims *UserTokenClaims, req *UserAIRouteInterfaceRequest) {
	if req.ID == 0 {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "路由 ID 不能为空",
		})
		return
	}

	// 1. 查询现有路由
	route, err := modelsdb.GetAIRouteByID(req.ID)
	if err != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "路由不存在: " + err.Error(),
		})
		return
	}

	// 2. 验证路由所属的模型属于当前用户
	model, err := modelsdb.GetUserModelByID(route.UserModelID)
	if err != nil || model == nil || model.UserID != claims.UserID {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "无权修改该路由",
		})
		return
	}

	// 3. 更新路由（仅修改目标源站，其他字段保持不变）
	item := &modelsdb.TAgentHttpAIRoute{
		ID:                           route.ID,
		UserID:                       route.UserID,
		UserModelID:                  route.UserModelID,
		ProtocolType:                 route.ProtocolType,
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
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "更新失败: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
		Success: true,
		Message: "更新成功",
	})
}

// handleUserAIRouteCountRecord 单条查询当前用户某模型某协议类型的记录数
func handleUserAIRouteCountRecord(w http.ResponseWriter, claims *UserTokenClaims, modelName string, protocolType int, days int) {
	var count int64
	if modelName != "" {
		c, err := modelsdb.CountAgentHttpTransactionsByDays(claims.UserName, modelName, protocolType, config.G.DBMysqlSubTableNumber, days)
		if err != nil {
			logger.Printf("[UserAIRoute] count_record failed: user=%s model=%s pt=%d days=%d err=%v", claims.UserName, modelName, protocolType, days, err)
			json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
				Success: false,
				Message: "查询失败: " + err.Error(),
			})
			return
		}
		count = c
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "查询成功",
		"data":    count,
	})
}

// handleUserAIRouteCountRecordByProtocol v2.0.44：用户端按协议拆分返回 {anthropic, openai, total}。
// JWT claims 强制覆盖 username（不可被前端篡改）；不接收 protocol_type 参数（无意义 —
// 用户端只有当前用户自己的记录，且浏览记录表的 protocol_type 是按 HTTP 头衍生）。
//
// 参数:
//   - claims：JWT 强制 username
//   - modelName：当前用户某模型
//   - days：同 handleUserAIRouteCountRecord
//
// 错误路径：modelName 为空 → 全 0 兜底；database.DB 错误 → 返回 success=false。
func handleUserAIRouteCountRecordByProtocol(w http.ResponseWriter, claims *UserTokenClaims, modelName string, days int) {
	zeroResp := map[string]interface{}{
		"anthropic": int64(0),
		"openai":    int64(0),
		"total":     int64(0),
	}
	if claims == nil || claims.UserName == "" || modelName == "" {
		// 缺关键参数：返回全 0
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "查询成功",
			"data":    zeroResp,
		})
		return
	}
	anth, errA := modelsdb.CountAgentHttpTransactionsByDays(claims.UserName, modelName, 1, config.G.DBMysqlSubTableNumber, days)
	open, errO := modelsdb.CountAgentHttpTransactionsByDays(claims.UserName, modelName, 2, config.G.DBMysqlSubTableNumber, days)
	if errA != nil {
		logger.Printf("[UserAIRoute] count_record_by_protocol anthropic failed: user=%s model=%s days=%d err=%v", claims.UserName, modelName, days, errA)
	}
	if errO != nil {
		logger.Printf("[UserAIRoute] count_record_by_protocol openai failed: user=%s model=%s days=%d err=%v", claims.UserName, modelName, days, errO)
	}
	if errA != nil && errO != nil {
		json.NewEncoder(w).Encode(UserAIRouteInterfaceResponse{
			Success: false,
			Message: "查询失败: " + errA.Error(),
		})
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

// enrichRouteForUser 为用户端丰富路由信息，包含完整的源站列表和当前活动源站
// userName 用于查询该路由最近一次代理请求的时间（按 user_name+model_name 哈希定位分表）。
func enrichRouteForUser(route modelsdb.TAgentHttpAIRoute, userName, modelName string, statsMap map[uint64]modelsdb.RouteBatchStatResult) map[string]interface{} {
	result := map[string]interface{}{
		"id":                               route.ID,
		"user_model_id":                    route.UserModelID,
		"model_name":                       modelName,
		"protocol_type":                    route.ProtocolType,
		"dst_endpoint_id_list":             route.DstEndPointIDList,
		"dst_endpoint_algorithm_type_list": route.DstEndPointAlgorithmTypeList,
		"dst_endpoint_id_number":           route.DstEndPointIDNumber,
		"algorithm_strategy_type":          route.AlgorithmStrategyType,
		"algorithm_name":                   modelsdb.GetAlgorithmName(route.AlgorithmStrategyType),
	}

	// v2.0.71：注入「最后成功记录」「最后失败记录」两组字段（与管理端契约完全一致）。
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
	} else {
		// v2.0.71：单条兜底也走 modelsdb.GetRouteLastRecordByStatus，与批量路径同源同语义。
		if row, lookupErr := modelsdb.GetRouteLastRecordByStatus(userName, modelName, route.ProtocolType, config.G.DBMysqlSubTableNumber, true); lookupErr == nil {
			succAt = row.CreatedAt
			succStatus = row.ResponseStatus
			succCode = modelsdb.ParseResponseStatusCode(row.ResponseStatus)
			succDstModelName = row.DstModelName
			succHasRecord = !row.CreatedAt.IsZero()
		} else {
			succFailed = true
		}
		if row, lookupErr := modelsdb.GetRouteLastRecordByStatus(userName, modelName, route.ProtocolType, config.G.DBMysqlSubTableNumber, false); lookupErr == nil {
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
