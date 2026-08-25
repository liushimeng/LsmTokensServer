package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"strings"
	"sync"

	"gorm.io/gorm"
)

const AgentDstEndPointTableName = "TAgentDstEndPoint"

var (
	agentDstEndPointMutex sync.RWMutex
)

// InitAgentDstEndPointTable 初始化源站接入点表
func InitAgentDstEndPointTable() error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", AgentDstEndPointTableName)
	err := database.DB.Table(AgentDstEndPointTableName).AutoMigrate(&TAgentDstEndPoint{})
	if err != nil {
		return fmt.Errorf("failed to migrate table %s: %w", AgentDstEndPointTableName, err)
	}
	logger.Printf("[database.DB] Table %s migrated successfully", AgentDstEndPointTableName)
	return nil
}

// ValidateDstEndPointInput 校验源站接入点输入
func ValidateDstEndPointInput(platformName, modelName, urlAddress, apiKey string) error {
	platformName = strings.TrimSpace(platformName)
	modelName = strings.TrimSpace(modelName)
	urlAddress = strings.TrimSpace(urlAddress)
	apiKey = strings.TrimSpace(apiKey)

	if len(platformName) < 1 || len(platformName) > 64 {
		return fmt.Errorf("平台名称长度必须在 1-64 位之间")
	}
	if len(modelName) < 1 || len(modelName) > 64 {
		return fmt.Errorf("模型名称长度必须在 1-64 位之间")
	}
	if len(urlAddress) < 8 || len(urlAddress) > 160 {
		return fmt.Errorf("URL 地址长度必须在 8-160 位之间")
	}
	if len(apiKey) < 8 || len(apiKey) > 180 {
		return fmt.Errorf("API Key 长度必须在 8-180 位之间")
	}
	return nil
}

// AddDstEndPoint 添加源站接入点
func AddDstEndPoint(item *TAgentDstEndPoint) error {
	agentDstEndPointMutex.Lock()
	defer agentDstEndPointMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	item.PlatformName = strings.TrimSpace(item.PlatformName)
	item.ModelName = strings.TrimSpace(item.ModelName)
	item.URLAddress = strings.TrimSpace(item.URLAddress)
	item.APIKey = normalizeAPIKey(item.APIKey)

	if err := ValidateDstEndPointInput(item.PlatformName, item.ModelName, item.URLAddress, item.APIKey); err != nil {
		return err
	}

	if item.Status == 0 {
		item.Status = 1 // 默认启用
	}

	// 创建记录
	err := database.DB.Table(AgentDstEndPointTableName).Create(item).Error
	if err != nil {
		return fmt.Errorf("failed to create dst endpoint: %w", err)
	}

	// 添加到内存缓存
	addDstEndPointToCache(item)

	// 同步模型信息
	go SyncModelInfoFromEndpoint(item.ModelName)

	logger.Printf("[database.DB] Added dst endpoint: %s/%s", item.PlatformName, item.ModelName)

	// 记录用户操作日志
	user, _ := GetUserByID(item.UserID)
	if user != nil {
		logger.LogUserAction("ADD_ENDPOINT", user.UserName, fmt.Sprintf("平台=%s 模型=%s", item.PlatformName, item.ModelName))
	}
	return nil
}

// UpdateDstEndPoint 更新源站接入点
func UpdateDstEndPoint(item *TAgentDstEndPoint) error {
	agentDstEndPointMutex.Lock()
	defer agentDstEndPointMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if item.ID == 0 {
		return fmt.Errorf("id is required for update")
	}

	item.PlatformName = strings.TrimSpace(item.PlatformName)
	item.ModelName = strings.TrimSpace(item.ModelName)
	item.URLAddress = strings.TrimSpace(item.URLAddress)
	item.APIKey = normalizeAPIKey(item.APIKey)

	if err := ValidateDstEndPointInput(item.PlatformName, item.ModelName, item.URLAddress, item.APIKey); err != nil {
		return err
	}

	// 更新记录
	result := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		Updates(map[string]interface{}{
			"platform_name": item.PlatformName,
			"model_name":    item.ModelName,
			"protocol_type": item.ProtocolType,
			"url_address":   item.URLAddress,
			"api_key":       item.APIKey,
			"status":        item.Status,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update dst endpoint: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("dst endpoint not found (id=%d)", item.ID)
	}

	// 更新内存缓存
	invalidateDstEndPointCache(item.ID)
	addDstEndPointToCache(item)

	// 同步模型信息
	go SyncModelInfoFromEndpoint(item.ModelName)

	logger.Printf("[database.DB] Updated dst endpoint (id=%d): %s/%s", item.ID, item.PlatformName, item.ModelName)

	// 记录用户操作日志
	user, _ := GetUserByID(item.UserID)
	if user != nil {
		logger.LogUserAction("UPDATE_ENDPOINT", user.UserName, fmt.Sprintf("平台=%s 模型=%s", item.PlatformName, item.ModelName))
	}
	return nil
}

// UpdateDstEndPointStatus 更新源站接入点状态（启用/禁用）
func UpdateDstEndPointStatus(id uint64, status int) error {
	agentDstEndPointMutex.Lock()
	defer agentDstEndPointMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if id == 0 {
		return fmt.Errorf("id is required")
	}
	if status != 0 && status != 1 {
		return fmt.Errorf("status must be 0 or 1")
	}

	// 查询现有记录
	var existing TAgentDstEndPoint
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&existing).Error
	if err != nil {
		return fmt.Errorf("dst endpoint not found (id=%d): %w", id, err)
	}

	// 如果状态没有变化，直接返回
	if existing.Status == status {
		return nil
	}

	// 更新状态
	result := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]interface{}{
			"status": status,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update dst endpoint status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("dst endpoint not found (id=%d)", id)
	}

	// 更新内存缓存
	existing.Status = status
	invalidateDstEndPointCache(id)
	addDstEndPointToCache(&existing)

	// 同步更新所有关联路由中的状态列表（不再物理删除ID）
	if err := updateEndpointStatusInRoutes(id, status); err != nil {
		logger.Printf("[WARNING] Failed to update endpoint %d status in routes: %v", id, err)
	}

	logger.Printf("[database.DB] Updated dst endpoint status (id=%d): status=%d", id, status)

	// 记录用户操作日志
	user, _ := GetUserByID(existing.UserID)
	if user != nil {
		statusText := "禁用"
		if status == 1 {
			statusText = "启用"
		}
		logger.LogUserAction("TOGGLE_ENDPOINT", user.UserName, fmt.Sprintf("平台=%s 模型=%s 操作=%s", existing.PlatformName, existing.ModelName, statusText))
	}
	return nil
}

// updateEndpointStatusInRoutes 更新所有包含该源站的路由中的状态列表
// 禁用(status=0)时把对应位置状态置为0；启用(status=1)时置为1
// 不再物理删除 DstEndPointIDList 中的源站ID
func updateEndpointStatusInRoutes(endpointID uint64, status int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 查询所有包含该源站的路由
	var routes []TAgentHttpAIRoute
	err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("deleted_at IS NULL").
		Find(&routes).Error
	if err != nil {
		return fmt.Errorf("failed to query routes: %w", err)
	}

	var updatedCount int

	for _, route := range routes {
		if route.DstEndPointIDList == "" {
			continue
		}

		ids, err := ParseDstEndPointIDList(route.DstEndPointIDList)
		if err != nil {
			continue
		}

		// 找到源站索引
		idx := -1
		for i, id := range ids {
			if id == endpointID {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue
		}

		// 解析状态列表，空时生成全1默认值
		_, statuses, err := NormalizeDstEndPointIDStatusList(route.DstEndPointIDList, route.DstEndPointIDStatusList)
		if err != nil {
			continue
		}

		// 更新对应位置状态
		if idx < len(statuses) {
			if statuses[idx] == status {
				continue // 状态已一致，无需更新
			}
			statuses[idx] = status
		}

		newStatusList := FormatDstEndPointIDStatusList(statuses)

		result := database.DB.Table(AgentHttpAIRouteTableName).
			Where("id = ? AND deleted_at IS NULL", route.ID).
			Updates(map[string]interface{}{
				"dst_endpoint_id_status_list": newStatusList,
			})
		if result.Error != nil {
			logger.Printf("[WARNING] Failed to update route %d status list for endpoint %d: %v", route.ID, endpointID, result.Error)
			continue
		}

		// 更新内存缓存
		updated := route
		updated.DstEndPointIDStatusList = newStatusList
		updateRouteInCache(&updated)

		// v2.x: 如果路由使用经济型算法，同步更新 livePool（移除被禁用的源站）
		// 避免禁用源站残留在 livePool 中仍被 SelectForSession 选中
		if route.AlgorithmStrategyType == AlgorithmStrategyType_Economic {
			if syncIDs, err := ParseDstEndPointIDList(route.DstEndPointIDList); err == nil {
				SyncEconomicRouteEndpoints(route.ID, syncIDs)
				logger.Printf("[database.DB] Synced economic livePool for route %d after endpoint %d status change to %d", route.ID, endpointID, status)
			}
		}

		updatedCount++
		logger.Printf("[database.DB] Updated endpoint %d status to %d in route %d, new status list: %s", endpointID, status, route.ID, newStatusList)
	}

	if updatedCount > 0 {
		logger.Printf("[database.DB] Updated endpoint %d status in %d route(s)", endpointID, updatedCount)
	}
	return nil
}

// maxBatchDstEndPointIDs 批量操作单次上限（防 IN 子句撞 max_allowed_packet）
const maxBatchDstEndPointIDs = 500

// BatchUpdateDstEndPointStatus 批量更新源站状态（启用/禁用）
//
//	ids: 待更新的源站 ID 列表（已去重 0 项）
//	status: 目标状态，0=禁用 1=启用；非法值自动回退为 1
//
// 行为约定：
//   - 逐条遍历、逐条处理，**追加已存在的跳过但不中断**，**删除/禁用不存在的不算错**；
//     反馈累计命中数（updatedCount）+ 错误列表
//   - 复用 UpdateDstEndPointStatus：自动同步内存缓存 + 路由状态列表 + 用户操作日志
//   - 上限保护：超过 maxBatchDstEndPointIDs 直接返回错误（避免 IN 子句过大）
//   - database.DB==nil 时静默回退为「全部入参都返回失败」，便于无 database.DB 环境单测
func BatchUpdateDstEndPointStatus(ids []uint64, status int) (int64, []error) {
	if len(ids) == 0 {
		return 0, []error{fmt.Errorf("ids 不能为空")}
	}
	if len(ids) > maxBatchDstEndPointIDs {
		return 0, []error{fmt.Errorf("批量更新单次上限 %d 条，当前 %d 条", maxBatchDstEndPointIDs, len(ids))}
	}
	// 状态值兜底：非法（非 0/1）→ 1
	if status != 0 && status != 1 {
		status = 1
	}
	// database.DB 未初始化：模拟整批失败，便于单测快速验证
	if database.DB == nil {
		var errs []error
		for _, id := range ids {
			errs = append(errs, fmt.Errorf("更新源站 %d 状态失败: database not initialized", id))
		}
		return 0, errs
	}

	var updatedCount int64
	var errs []error
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if err := UpdateDstEndPointStatus(id, status); err != nil {
			// 复用现有函数：源站不存在（gorm.ErrRecordNotFound）已被 UpdateDstEndPointStatus
			// 包装为可读错误；这里只追加错误，**不中断整批**
			errs = append(errs, fmt.Errorf("更新源站 %d 状态失败: %w", id, err))
			continue
		}
		updatedCount++
	}
	logger.Printf("[database.DB] BatchUpdateDstEndPointStatus: status=%d updated=%d failed=%d", status, updatedCount, len(errs))
	return updatedCount, errs
}

// BatchDeleteDstEndPoints 批量硬删除源站接入点
//
//	ids: 待删除的源站 ID 列表
//
// 行为约定：
//   - 逐条遍历、逐条处理；**不存在的跳过不算错**
//   - 复用 DeleteDstEndPoint：自动清理内存缓存 + 用户操作日志
//   - 上限保护：超过 maxBatchDstEndPointIDs 直接返回错误
//   - database.DB==nil 时静默回退为「全部入参都返回失败」
func BatchDeleteDstEndPoints(ids []uint64) (int64, []error) {
	if len(ids) == 0 {
		return 0, []error{fmt.Errorf("ids 不能为空")}
	}
	if len(ids) > maxBatchDstEndPointIDs {
		return 0, []error{fmt.Errorf("批量删除单次上限 %d 条，当前 %d 条", maxBatchDstEndPointIDs, len(ids))}
	}
	if database.DB == nil {
		var errs []error
		for _, id := range ids {
			errs = append(errs, fmt.Errorf("删除源站 %d 失败: database not initialized", id))
		}
		return 0, errs
	}

	var deletedCount int64
	var errs []error
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if err := DeleteDstEndPoint(id); err != nil {
			errs = append(errs, fmt.Errorf("删除源站 %d 失败: %w", id, err))
			continue
		}
		deletedCount++
	}
	logger.Printf("[database.DB] BatchDeleteDstEndPoints: deleted=%d failed=%d", deletedCount, len(errs))
	return deletedCount, errs
}

// DeleteDstEndPoint 删除源站接入点（硬删除）
//
// 关联数据处理（v2.0.50）：先处理 TAgentHttpAIRoute 中所有引用该源站的路由 —
//   - 路由引用多个源站：把该源站从 DstEndPointIDList / DstEndPointIDStatusList /
//     DstEndPointAlgorithmTypeList 三个逗号分隔列表的同一位置一并剔除，并同步
//     DstEndPointIDNumber 与内存缓存，路由保留继续可用；
//   - 路由仅引用该源站（剔除后为空）：整套路由配置已失去意义，级联硬删除该路由。
//
// 处理完关联数据后再真实删除 TAgentDstEndPoint 记录，保证不留悬空引用。
func DeleteDstEndPoint(id uint64) error {
	agentDstEndPointMutex.Lock()
	defer agentDstEndPointMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 先查询信息用于日志
	var existing TAgentDstEndPoint
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ?", id).
		First(&existing).Error
	if err != nil {
		// 查询失败也尝试继续删除
		logger.Printf("[WARNING] Failed to query endpoint for deletion log: %v", err)
	}

	// 优先删除/清理 TAgentHttpAIRoute 中的关联数据，再删源站本体
	removedRefs, deletedRoutes := cleanupRoutesForEndpointDeletion(id)

	result := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ?", id).
		Unscoped().
		Delete(&TAgentDstEndPoint{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete dst endpoint: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("dst endpoint not found (id=%d)", id)
	}

	// 从内存缓存移除
	invalidateDstEndPointCache(id)

	logger.Printf("[database.DB] Deleted dst endpoint (id=%d), removed from %d route(s), cascade-deleted %d route(s)", id, removedRefs, deletedRoutes)

	// 记录用户操作日志
	if err == nil && existing.ID != 0 {
		user, _ := GetUserByID(existing.UserID)
		if user != nil {
			logger.LogUserAction("DELETE_ENDPOINT", user.UserName, fmt.Sprintf("平台=%s 模型=%s 清理路由引用=%d 级联删除路由=%d", existing.PlatformName, existing.ModelName, removedRefs, deletedRoutes))
		}
	}
	return nil
}

// cleanupRoutesForEndpointDeletion 在删除源站前清理 TAgentHttpAIRoute 中的关联数据。
// 返回 (从路由中剔除该源站引用的路由数, 级联删除的路由数)。
// 单条路由失败仅记录 warning 并继续处理下一条，不中断整体删除流程。
func cleanupRoutesForEndpointDeletion(endpointID uint64) (int, int) {
	if database.DB == nil || endpointID == 0 {
		return 0, 0
	}

	var routes []TAgentHttpAIRoute
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("deleted_at IS NULL").
		Find(&routes).Error; err != nil {
		logger.Printf("[WARNING] Failed to query routes for endpoint %d deletion: %v", endpointID, err)
		return 0, 0
	}

	var removedRefs, deletedRoutes int

	for _, route := range routes {
		if route.DstEndPointIDList == "" {
			continue
		}

		ids, err := ParseDstEndPointIDList(route.DstEndPointIDList)
		if err != nil || len(ids) == 0 {
			continue
		}

		// 检查该路由是否引用待删除源站
		referenced := false
		for _, id := range ids {
			if id == endpointID {
				referenced = true
				break
			}
		}
		if !referenced {
			continue
		}

		// 路由仅剩这一个源站：整套路由配置已无意义，级联硬删除
		if len(ids) == 1 {
			if err := deleteRouteForEndpointDeletion(&route); err != nil {
				logger.Printf("[WARNING] Failed to cascade-delete route %d for endpoint %d: %v", route.ID, endpointID, err)
				continue
			}
			deletedRoutes++
			logger.Printf("[database.DB] Cascade-deleted route %d (only referenced endpoint %d)", route.ID, endpointID)
			continue
		}

		// 多源站路由：从三个列表同一位置剔除该源站
		if err := removeEndpointRefFromRoute(&route, endpointID); err != nil {
			logger.Printf("[WARNING] Failed to remove endpoint %d from route %d: %v", endpointID, route.ID, err)
			continue
		}
		removedRefs++
	}

	if removedRefs > 0 || deletedRoutes > 0 {
		logger.Printf("[database.DB] Endpoint %d cleanup: removed from %d route(s), cascade-deleted %d route(s)", endpointID, removedRefs, deletedRoutes)
	}
	return removedRefs, deletedRoutes
}

// deleteRouteForEndpointDeletion 级联硬删除仅剩待删源站的路由（database.DB + 内存缓存 + 操作日志）
func deleteRouteForEndpointDeletion(route *TAgentHttpAIRoute) error {
	result := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ?", route.ID).
		Unscoped().
		Delete(&TAgentHttpAIRoute{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete ai route: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ai route not found (id=%d)", route.ID)
	}

	removeRouteFromCache(route.UserModelID, route.ID)

	user, _ := GetUserByID(route.UserID)
	userName := ""
	if user != nil {
		userName = user.UserName
	}
	LogAIRouteAction("DELETE_ROUTE", userName, route.UserModelID, route.DstEndPointIDList)
	return nil
}

// removeEndpointRefFromRoute 把指定源站从路由的三个逗号分隔列表（ID / 状态 / 算法）
// 同一位置一并剔除，同步 DstEndPointIDNumber、database.DB 与内存缓存。
// 调用方保证 ids 中确实包含 endpointID 且剔除后非空。
func removeEndpointRefFromRoute(route *TAgentHttpAIRoute, endpointID uint64) error {
	ids, err := ParseDstEndPointIDList(route.DstEndPointIDList)
	if err != nil {
		return fmt.Errorf("parse dst endpoint id list failed: %w", err)
	}

	_, algoTypes, err := NormalizeDstEndPointAlgorithmTypeList(route.DstEndPointIDList, route.DstEndPointAlgorithmTypeList)
	if err != nil {
		return fmt.Errorf("normalize algorithm type list failed: %w", err)
	}

	_, statuses, err := NormalizeDstEndPointIDStatusList(route.DstEndPointIDList, route.DstEndPointIDStatusList)
	if err != nil {
		return fmt.Errorf("normalize status list failed: %w", err)
	}

	newIDs := make([]uint64, 0, len(ids)-1)
	newAlgos := make([]int, 0, len(algoTypes)-1)
	newStatuses := make([]int, 0, len(statuses)-1)
	found := false
	for i, id := range ids {
		if id == endpointID {
			found = true
			continue
		}
		newIDs = append(newIDs, id)
		if i < len(algoTypes) {
			newAlgos = append(newAlgos, algoTypes[i])
		}
		if i < len(statuses) {
			newStatuses = append(newStatuses, statuses[i])
		}
	}
	if !found {
		return fmt.Errorf("endpoint %d not found in route %d", endpointID, route.ID)
	}
	if len(newIDs) == 0 {
		return fmt.Errorf("cannot remove last endpoint %d from route %d", endpointID, route.ID)
	}

	newIDList := formatUint64List(newIDs)
	newAlgoList := FormatDstEndPointAlgorithmTypeList(newAlgos)
	newStatusList := FormatDstEndPointIDStatusList(newStatuses)

	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", route.ID).
		Updates(map[string]interface{}{
			"dst_endpoint_id_list":             newIDList,
			"dst_endpoint_id_status_list":      newStatusList,
			"dst_endpoint_algorithm_type_list": newAlgoList,
			"dst_endpoint_id_number":           len(newIDs),
		}).Error; err != nil {
		return fmt.Errorf("failed to update route after removing endpoint: %w", err)
	}

	// 同步内存缓存
	updated := *route
	updated.DstEndPointIDList = newIDList
	updated.DstEndPointIDStatusList = newStatusList
	updated.DstEndPointAlgorithmTypeList = newAlgoList
	updated.DstEndPointIDNumber = len(newIDs)
	updateRouteInCache(&updated)

	logger.Printf("[database.DB] Removed endpoint %d from route %d, ids:[%s]->[%s]", endpointID, route.ID, route.DstEndPointIDList, newIDList)
	return nil
}

// GetDstEndPointsByUserID 根据用户 ID 获取所有源站接入点
func GetDstEndPointsByUserID(userID uint64) ([]TAgentDstEndPoint, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var items []TAgentDstEndPoint
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query dst endpoints: %w", err)
	}
	return items, nil
}

// GetDstEndPointByID 根据 ID 查询源站接入点（优先内存缓存）
func GetDstEndPointByID(id uint64) (*TAgentDstEndPoint, error) {
	// 优先从内存缓存查询（代理热路径）
	if ep, ok := GetCachedDstEndPointByID(id); ok {
		return ep, nil
	}

	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentDstEndPoint
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("dst endpoint (id=%d) not found", id)
		}
		return nil, fmt.Errorf("failed to query dst endpoint: %w", err)
	}
	return &item, nil
}

// GetDistinctPlatformNamesByUserID 查询某个用户已添加的所有去重平台名称
func GetDistinctPlatformNamesByUserID(userID uint64) ([]string, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var names []string
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Distinct("platform_name").
		Pluck("platform_name", &names).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct platform names: %w", err)
	}
	return names, nil
}

// GetDistinctModelNamesByUserID 查询某个用户已添加的所有去重模型名称
func GetDistinctModelNamesByUserID(userID uint64) ([]string, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var names []string
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Distinct("model_name").
		Pluck("model_name", &names).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct model names: %w", err)
	}
	return names, nil
}
