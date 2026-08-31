package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"
)

const AgentHttpAIRouteTableName = "TAgentHttpAIRoute"

var (
	agentAIRouteMutex sync.RWMutex
)

// InitAgentHttpAIRouteTable 初始化智能路由表
func InitAgentHttpAIRouteTable() error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", AgentHttpAIRouteTableName)
	err := database.DB.Table(AgentHttpAIRouteTableName).AutoMigrate(&TAgentHttpAIRoute{})
	if err != nil {
		return fmt.Errorf("failed to migrate table %s: %w", AgentHttpAIRouteTableName, err)
	}
	logger.Printf("[database.DB] Table %s migrated successfully", AgentHttpAIRouteTableName)
	return nil
}

// ValidateAIRouteInput 校验智能路由输入
func ValidateAIRouteInput(userID, userModelID uint64, dstEndPointIDList, dstEndPointAlgorithmTypeList string, protocolType int) error {
	if userID == 0 {
		return fmt.Errorf("用户 ID 不能为空")
	}
	if userModelID == 0 {
		return fmt.Errorf("用户模型 ID 不能为空")
	}
	if protocolType != protocol.AgentProtocolType_Anthropic && protocolType != protocol.AgentProtocolType_OpenAI {
		return fmt.Errorf("协议类型必须为 Anthropic(1) 或 OpenAI(2)")
	}
	if strings.TrimSpace(dstEndPointIDList) == "" {
		return fmt.Errorf("目标源站 ID 列表不能为空")
	}
	ids, err := ParseDstEndPointIDList(dstEndPointIDList)
	if err != nil {
		return fmt.Errorf("目标源站 ID 列表格式错误: %w", err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("目标源站 ID 列表不能为空")
	}
	if _, _, err := NormalizeDstEndPointAlgorithmTypeList(dstEndPointIDList, dstEndPointAlgorithmTypeList); err != nil {
		return err
	}
	return nil
}

// ParseDstEndPointIDList 将逗号分隔的 ID 字符串解析为 uint64 切片
func ParseDstEndPointIDList(listStr string) ([]uint64, error) {
	if listStr == "" {
		return nil, nil
	}
	parts := strings.Split(listStr, ",")
	ids := make([]uint64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint id %q: %w", p, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ParseDstEndPointAlgorithmTypeList 将逗号分隔的源站协议处理算法字符串解析为 int 切片
func ParseDstEndPointAlgorithmTypeList(listStr string) ([]int, error) {
	if strings.TrimSpace(listStr) == "" {
		return nil, nil
	}
	parts := strings.Split(listStr, ",")
	types := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		algorithmType, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint algorithm type %q: %w", p, err)
		}
		if algorithmType != DstEndPointAlgorithmType_Direct && algorithmType != DstEndPointAlgorithmType_ProtocolConverter {
			return nil, fmt.Errorf("目标源站协议转换算法类型只能为 1 或 2")
		}
		types = append(types, algorithmType)
	}
	return types, nil
}

// ParseDstEndPointIDStatusList 将逗号分隔的源站可用状态字符串解析为 int 切片
// 状态值: 1=启用, 0=禁用
func ParseDstEndPointIDStatusList(listStr string) ([]int, error) {
	if strings.TrimSpace(listStr) == "" {
		return nil, nil
	}
	parts := strings.Split(listStr, ",")
	statuses := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		status, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint status %q: %w", p, err)
		}
		if status != 0 && status != 1 {
			return nil, fmt.Errorf("目标源站可用状态只能为 0 或 1")
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// BuildDefaultDstEndPointIDStatusList 根据源站数量构建默认全启用的状态列表
func BuildDefaultDstEndPointIDStatusList(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "1"
	}
	return strings.Join(parts, ",")
}

// FormatDstEndPointIDStatusList 将状态切片格式化为逗号分隔字符串
func FormatDstEndPointIDStatusList(statuses []int) string {
	if len(statuses) == 0 {
		return ""
	}
	parts := make([]string, len(statuses))
	for i, s := range statuses {
		parts[i] = strconv.Itoa(s)
	}
	return strings.Join(parts, ",")
}

// NormalizeDstEndPointIDStatusList 校验并规范化源站状态列表，与源站 ID 列表严格一一对应
// 若状态列表为空，返回全1默认值；若长度不一致，自动补全（缺省1）或截断到与ID列表一致
func NormalizeDstEndPointIDStatusList(endpointList, statusList string) (string, []int, error) {
	ids, err := ParseDstEndPointIDList(endpointList)
	if err != nil {
		return "", nil, fmt.Errorf("目标源站 ID 列表格式错误: %w", err)
	}
	if len(ids) == 0 {
		return "", nil, fmt.Errorf("目标源站 ID 列表不能为空")
	}
	if strings.TrimSpace(statusList) == "" {
		statusList = BuildDefaultDstEndPointIDStatusList(len(ids))
	}
	statuses, err := ParseDstEndPointIDStatusList(statusList)
	if err != nil {
		return "", nil, err
	}
	// 长度不一致时自动补全或截断
	if len(statuses) < len(ids) {
		for i := len(statuses); i < len(ids); i++ {
			statuses = append(statuses, 1)
		}
	} else if len(statuses) > len(ids) {
		statuses = statuses[:len(ids)]
	}
	return FormatDstEndPointIDStatusList(statuses), statuses, nil
}

// BuildDefaultDstEndPointAlgorithmTypeList 根据源站数量构建默认协议直连算法列表
func BuildDefaultDstEndPointAlgorithmTypeList(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = strconv.Itoa(DstEndPointAlgorithmType_Direct)
	}
	return strings.Join(parts, ",")
}

// FormatDstEndPointAlgorithmTypeList 将算法类型切片格式化为逗号分隔字符串
func FormatDstEndPointAlgorithmTypeList(types []int) string {
	if len(types) == 0 {
		return ""
	}
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = strconv.Itoa(t)
	}
	return strings.Join(parts, ",")
}

// NormalizeDstEndPointAlgorithmTypeList 校验并规范化源站算法列表，与源站 ID 列表严格一一对应
func NormalizeDstEndPointAlgorithmTypeList(endpointList, algorithmList string) (string, []int, error) {
	ids, err := ParseDstEndPointIDList(endpointList)
	if err != nil {
		return "", nil, fmt.Errorf("目标源站 ID 列表格式错误: %w", err)
	}
	if len(ids) == 0 {
		return "", nil, fmt.Errorf("目标源站 ID 列表不能为空")
	}
	if strings.TrimSpace(algorithmList) == "" {
		algorithmList = BuildDefaultDstEndPointAlgorithmTypeList(len(ids))
	}
	types, err := ParseDstEndPointAlgorithmTypeList(algorithmList)
	if err != nil {
		return "", nil, err
	}
	if len(types) != len(ids) {
		return "", nil, fmt.Errorf("目标源站 ID 列表与协议转换算法类型列表长度必须一致")
	}
	return FormatDstEndPointAlgorithmTypeList(types), types, nil
}

func GetDstEndPointAlgorithmTypeName(algorithmType int) string {
	switch algorithmType {
	case DstEndPointAlgorithmType_ProtocolConverter:
		return "协议转换器"
	case DstEndPointAlgorithmType_Direct:
		return "协议直连"
	default:
		return "未知"
	}
}

func getProtocolTypeName(protocolType int) string {
	switch protocolType {
	case protocol.AgentProtocolType_Anthropic:
		return "Anthropic"
	case protocol.AgentProtocolType_OpenAI:
		return "OpenAI"
	default:
		return "-"
	}
}

// GetFirstDstEndPointIDFromRoute 根据路由获取实际使用的目标源站 ID
func GetFirstDstEndPointIDFromRoute(route *TAgentHttpAIRoute) (uint64, error) {
	if route == nil {
		return 0, fmt.Errorf("route is nil")
	}
	if route.DstEndPointIDList == "" {
		return 0, fmt.Errorf("no destination endpoint configured for route id=%d", route.ID)
	}
	ids, err := ParseDstEndPointIDList(route.DstEndPointIDList)
	if err != nil {
		return 0, fmt.Errorf("failed to parse dst_endpoint_id_list: %w", err)
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("no destination endpoint configured for route id=%d", route.ID)
	}
	switch route.AlgorithmStrategyType {
	case AlgorithmStrategyType_FirstID:
		return ids[0], nil
	default:
		return ids[0], nil
	}
}

// autoFillRouteNewFields 自动填充路由新字段
func autoFillRouteNewFields(item *TAgentHttpAIRoute) {
	if item.DstEndPointIDList != "" {
		ids, _ := ParseDstEndPointIDList(item.DstEndPointIDList)
		item.DstEndPointIDNumber = len(ids)
		if normalized, _, err := NormalizeDstEndPointAlgorithmTypeList(item.DstEndPointIDList, item.DstEndPointAlgorithmTypeList); err == nil {
			item.DstEndPointAlgorithmTypeList = normalized
		}
		// 自动填充状态列表：为空时生成全1默认值；长度不一致时补全/截断
		if normalizedStatus, _, err := NormalizeDstEndPointIDStatusList(item.DstEndPointIDList, item.DstEndPointIDStatusList); err == nil {
			item.DstEndPointIDStatusList = normalizedStatus
		}
	}
	if item.AlgorithmStrategyType == 0 {
		item.AlgorithmStrategyType = AlgorithmStrategyType_FirstID
	}
}

// validateAIRouteEndpointAlgorithms 校验源站列表与每项协议处理算法是否匹配
func validateAIRouteEndpointAlgorithms(route *TAgentHttpAIRoute) error {
	if route == nil {
		return fmt.Errorf("route is nil")
	}
	ids, err := ParseDstEndPointIDList(route.DstEndPointIDList)
	if err != nil {
		return fmt.Errorf("目标源站 ID 列表格式错误: %w", err)
	}
	_, algorithmTypes, err := NormalizeDstEndPointAlgorithmTypeList(route.DstEndPointIDList, route.DstEndPointAlgorithmTypeList)
	if err != nil {
		return err
	}
	seen := make(map[uint64]bool, len(ids))
	for i, id := range ids {
		if seen[id] {
			return fmt.Errorf("目标源站 ID 不能重复: %d", id)
		}
		seen[id] = true

		var dstEndPoint TAgentDstEndPoint
		err := database.DB.Table(AgentDstEndPointTableName).
			Where("id = ? AND deleted_at IS NULL", id).
			First(&dstEndPoint).Error
		if err != nil {
			return fmt.Errorf("dst endpoint not found (id=%d): %w", id, err)
		}
		if dstEndPoint.UserID != route.UserID {
			return fmt.Errorf("目标源站不属于当前路由用户: %d", id)
		}
		switch algorithmTypes[i] {
		case DstEndPointAlgorithmType_Direct:
			if dstEndPoint.ProtocolType != route.ProtocolType {
				return fmt.Errorf("协议直连要求源站协议与路由协议一致: endpoint_id=%d", id)
			}
		case DstEndPointAlgorithmType_ProtocolConverter:
			if dstEndPoint.ProtocolType == route.ProtocolType {
				return fmt.Errorf("协议转换器要求源站协议与路由协议相反: endpoint_id=%d", id)
			}
		default:
			return fmt.Errorf("目标源站协议转换算法类型只能为 1 或 2")
		}
	}
	return nil
}

// AddAIRoute 添加智能路由
func AddAIRoute(item *TAgentHttpAIRoute) error {
	agentAIRouteMutex.Lock()
	defer agentAIRouteMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 自动填充新字段
	autoFillRouteNewFields(item)

	if err := ValidateAIRouteInput(item.UserID, item.UserModelID, item.DstEndPointIDList, item.DstEndPointAlgorithmTypeList, item.ProtocolType); err != nil {
		return err
	}

	// 验证用户模型是否存在
	var userModel TAgentHttpUserModelInfo
	err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", item.UserModelID).
		First(&userModel).Error
	if err != nil {
		return fmt.Errorf("user model not found: %w", err)
	}

	if err := validateAIRouteEndpointAlgorithms(item); err != nil {
		return err
	}

	// 检查该模型下是否已存在相同协议的路由
	var count int64
	err = database.DB.Table(AgentHttpAIRouteTableName).
		Where("user_model_id = ? AND protocol_type = ? AND deleted_at IS NULL", item.UserModelID, item.ProtocolType).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check ai route: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("ai route for this model and protocol already exists")
	}

	// 创建记录
	err = database.DB.Table(AgentHttpAIRouteTableName).Create(item).Error
	if err != nil {
		return fmt.Errorf("failed to create ai route: %w", err)
	}

	addRouteToCache(item)
	logger.Printf("[database.DB] Added AI route: user=%d, model=%d, protocol=%d, endpointList=%s", item.UserID, item.UserModelID, item.ProtocolType, item.DstEndPointIDList)

	// 记录用户操作日志
	user, _ := GetUserByID(item.UserID)
	userName := ""
	if user != nil {
		userName = user.UserName
	}
	LogAIRouteAction("ADD_ROUTE", userName, item.UserModelID, item.DstEndPointIDList)

	return nil
}

// UpdateAIRoute 更新智能路由
func UpdateAIRoute(item *TAgentHttpAIRoute) error {
	agentAIRouteMutex.Lock()
	defer agentAIRouteMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if item.ID == 0 {
		return fmt.Errorf("id is required for update")
	}

	// 先查询现有路由数据
	var existing TAgentHttpAIRoute
	err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		First(&existing).Error
	if err != nil {
		return fmt.Errorf("ai route not found (id=%d): %w", item.ID, err)
	}

	// 合并更新数据（只更新传入的字段，保持其他不变）
	updated := existing
	if item.UserID != 0 {
		updated.UserID = item.UserID
	}
	if item.UserModelID != 0 {
		updated.UserModelID = item.UserModelID
	}
	if item.ProtocolType != 0 {
		updated.ProtocolType = item.ProtocolType
	}
	if item.DstEndPointIDList != "" {
		updated.DstEndPointIDList = item.DstEndPointIDList
	}
	if item.DstEndPointIDStatusList != "" {
		updated.DstEndPointIDStatusList = item.DstEndPointIDStatusList
	} else if item.DstEndPointIDList != "" && updated.DstEndPointIDList != item.DstEndPointIDList {
		// v2.0.18 patch3 修复：源站顺序调整但前端未传 status list 时，
		//   按旧 ID → 旧 status 映射表，把旧状态按"原 ID 索引"映射到新顺序位置，
		//   避免禁用源站位置错位（不再退化为全 1 默认值）。
		updated.DstEndPointIDStatusList = remapStatusListByIDs(
			existing.DstEndPointIDList, existing.DstEndPointIDStatusList,
			item.DstEndPointIDList,
		)
	}
	if item.DstEndPointAlgorithmTypeList != "" {
		updated.DstEndPointAlgorithmTypeList = item.DstEndPointAlgorithmTypeList
	} else if item.DstEndPointIDList != "" && updated.DstEndPointIDList != item.DstEndPointIDList {
		// 同步：源站顺序调整但前端未传 algorithm list 时，
		//   按旧 ID → 旧 algorithm 映射，避免 algorithm 跟随错位
		updated.DstEndPointAlgorithmTypeList = remapAlgorithmTypeListByIDs(
			existing.DstEndPointIDList, existing.DstEndPointAlgorithmTypeList,
			item.DstEndPointIDList,
		)
	}
	if item.AlgorithmStrategyType != 0 {
		updated.AlgorithmStrategyType = item.AlgorithmStrategyType
	}

	// 自动填充新字段
	autoFillRouteNewFields(&updated)

	if err := ValidateAIRouteInput(updated.UserID, updated.UserModelID, updated.DstEndPointIDList, updated.DstEndPointAlgorithmTypeList, updated.ProtocolType); err != nil {
		return err
	}

	// 验证用户模型是否存在
	var userModel TAgentHttpUserModelInfo
	err = database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", updated.UserModelID).
		First(&userModel).Error
	if err != nil {
		return fmt.Errorf("user model not found: %w", err)
	}

	if err := validateAIRouteEndpointAlgorithms(&updated); err != nil {
		return err
	}

	// 记录更新前的策略类型（用于经济型算法同步）
	oldStrategy := existing.AlgorithmStrategyType

	// 更新记录
	result := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		Updates(map[string]interface{}{
			"user_id":                          updated.UserID,
			"user_model_id":                    updated.UserModelID,
			"protocol_type":                    updated.ProtocolType,
			"dst_endpoint_id_list":             updated.DstEndPointIDList,
			"dst_endpoint_id_status_list":      updated.DstEndPointIDStatusList,
			"dst_endpoint_algorithm_type_list": updated.DstEndPointAlgorithmTypeList,
			"dst_endpoint_id_number":           updated.DstEndPointIDNumber,
			"algorithm_strategy_type":          updated.AlgorithmStrategyType,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update ai route: %w", result.Error)
	}

	updateRouteInCache(&updated)

	// 经济型算法：在锁外同步状态（避免与 agentAIRouteMutex 交叉持锁）
	//   - 切到非经济型：清空经济型状态
	//   - 切到/保持经济型：把 livePool + session 队列 + 失败计数与新源站列表对齐
	if oldStrategy == AlgorithmStrategyType_Economic && updated.AlgorithmStrategyType != AlgorithmStrategyType_Economic {
		ResetEconomicRouteState(updated.ID)
	}
	if updated.AlgorithmStrategyType == AlgorithmStrategyType_Economic {
		newEndpointIDs, _ := ParseDstEndPointIDList(updated.DstEndPointIDList)
		SyncEconomicRouteEndpoints(updated.ID, newEndpointIDs)
	}

	logger.Printf("[database.DB] Updated AI route (id=%d)", item.ID)

	// 记录用户操作日志
	user, _ := GetUserByID(updated.UserID)
	userName := ""
	if user != nil {
		userName = user.UserName
	}
	LogAIRouteAction("UPDATE_ROUTE", userName, updated.UserModelID, updated.DstEndPointIDList)

	return nil
}

// RotateAIRouteEndpointList 把指定路由的源站列表向左滚动一格：
// DstEndPointIDList[0] 移到末尾，DstEndPointAlgorithmTypeList 同步滚动，
// 然后写回 database.DB 并同步内存缓存。供稳定型算法连续 3 次失败时调用。
//
// 设计要点：
//   - 与 UpdateAIRoute 共享 agentAIRouteMutex，避免并发写冲突。
//   - 单个源站或空列表时直接返回 nil（无可滚动对象）。
//   - 失败时返回 error，调用方决定如何记录日志（这里只打 [ROUTE] 调试日志）。
//   - 不调用 LogAIRouteAction：这是系统自动行为，不污染用户操作日志。
func RotateAIRouteEndpointList(routeID uint64) error {
	agentAIRouteMutex.Lock()
	defer agentAIRouteMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	var existing TAgentHttpAIRoute
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		First(&existing).Error; err != nil {
		return fmt.Errorf("ai route not found (id=%d): %w", routeID, err)
	}

	ids, err := ParseDstEndPointIDList(existing.DstEndPointIDList)
	if err != nil {
		return fmt.Errorf("parse dst endpoint id list failed: %w", err)
	}
	if len(ids) <= 1 {
		// 单源站或空列表无需滚动
		return nil
	}

	// 用 NormalizeDstEndPointAlgorithmTypeList 解析算法列表，保证与 ID 列表长度一致
	_, algoTypes, err := NormalizeDstEndPointAlgorithmTypeList(existing.DstEndPointIDList, existing.DstEndPointAlgorithmTypeList)
	if err != nil {
		return fmt.Errorf("normalize algorithm type list failed: %w", err)
	}

	oldIDList := existing.DstEndPointIDList
	oldAlgoList := FormatDstEndPointAlgorithmTypeList(algoTypes)

	// 滚动：第 0 个移到末尾，状态列表同步滚动
	rotatedIDs := append(append([]uint64{}, ids[1:]...), ids[0])
	rotatedAlgos := append(append([]int{}, algoTypes[1:]...), algoTypes[0])

	// 同步滚动状态列表
	_, statuses, err := NormalizeDstEndPointIDStatusList(existing.DstEndPointIDList, existing.DstEndPointIDStatusList)
	if err != nil {
		return fmt.Errorf("normalize status list failed: %w", err)
	}
	rotatedStatuses := append(append([]int{}, statuses[1:]...), statuses[0])

	newIDList := formatUint64List(rotatedIDs)
	newAlgoList := FormatDstEndPointAlgorithmTypeList(rotatedAlgos)
	newStatusList := FormatDstEndPointIDStatusList(rotatedStatuses)

	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		Updates(map[string]interface{}{
			"dst_endpoint_id_list":             newIDList,
			"dst_endpoint_id_status_list":      newStatusList,
			"dst_endpoint_algorithm_type_list": newAlgoList,
		}).Error; err != nil {
		return fmt.Errorf("failed to update rotated lists: %w", err)
	}

	updated := existing
	updated.DstEndPointIDList = newIDList
	updated.DstEndPointIDStatusList = newStatusList
	updated.DstEndPointAlgorithmTypeList = newAlgoList
	updateRouteInCache(&updated)

	logger.Printf("[ROUTE] Stable rotate: routeID=%d, ids:[%s]->[%s], algos:[%s]->[%s], statuses:[%s]->[%s]",
		routeID, oldIDList, newIDList, oldAlgoList, newAlgoList, FormatDstEndPointIDStatusList(statuses), newStatusList)
	return nil
}

// formatUint64List 将 uint64 切片格式化为逗号分隔字符串
func formatUint64List(ids []uint64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(id, 10)
	}
	return strings.Join(parts, ",")
}

// RemoveEndpointFromAIRoute 从路由中永久移除指定源站（经济型算法专用）
// 移除后更新 database.DB + 内存缓存，同步 DstEndPointIDList 和 DstEndPointAlgorithmTypeList。
// 如果移除后源站列表为空，返回错误（不允许清空所有源站）。
// 由 EconomicAlgorithmSelector.OnEndpointFailure 在连续失败达到阈值时调用。
func RemoveEndpointFromAIRoute(routeID uint64, endpointID uint64) error {
	agentAIRouteMutex.Lock()
	defer agentAIRouteMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	var existing TAgentHttpAIRoute
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		First(&existing).Error; err != nil {
		return fmt.Errorf("ai route not found (id=%d): %w", routeID, err)
	}

	ids, err := ParseDstEndPointIDList(existing.DstEndPointIDList)
	if err != nil {
		return fmt.Errorf("parse dst endpoint id list failed: %w", err)
	}

	_, algoTypes, err := NormalizeDstEndPointAlgorithmTypeList(existing.DstEndPointIDList, existing.DstEndPointAlgorithmTypeList)
	if err != nil {
		return fmt.Errorf("normalize algorithm type list failed: %w", err)
	}

	_, statuses, err := NormalizeDstEndPointIDStatusList(existing.DstEndPointIDList, existing.DstEndPointIDStatusList)
	if err != nil {
		return fmt.Errorf("normalize status list failed: %w", err)
	}

	// 找到并移除指定源站
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
		return fmt.Errorf("endpoint %d not found in route %d", endpointID, routeID)
	}

	// 不允许清空所有源站
	if len(newIDs) == 0 {
		return fmt.Errorf("cannot remove last endpoint %d from route %d", endpointID, routeID)
	}

	newIDList := formatUint64List(newIDs)
	newAlgoList := FormatDstEndPointAlgorithmTypeList(newAlgos)
	newStatusList := FormatDstEndPointIDStatusList(newStatuses)

	oldIDList := existing.DstEndPointIDList

	// 更新 database.DB
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		Updates(map[string]interface{}{
			"dst_endpoint_id_list":             newIDList,
			"dst_endpoint_id_status_list":      newStatusList,
			"dst_endpoint_algorithm_type_list": newAlgoList,
			"dst_endpoint_id_number":           len(newIDs),
		}).Error; err != nil {
		return fmt.Errorf("failed to update route after removing endpoint: %w", err)
	}

	// 同步内存缓存
	updated := existing
	updated.DstEndPointIDList = newIDList
	updated.DstEndPointIDStatusList = newStatusList
	updated.DstEndPointAlgorithmTypeList = newAlgoList
	updated.DstEndPointIDNumber = len(newIDs)
	updateRouteInCache(&updated)

	logger.Printf("[ROUTE] Economic remove endpoint: routeID=%d, removed=%d, ids:[%s]->[%s]",
		routeID, endpointID, oldIDList, newIDList)
	return nil
}

// DeleteAIRoute 删除智能路由（硬删除）
func DeleteAIRoute(id uint64) error {
	agentAIRouteMutex.Lock()
	defer agentAIRouteMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 先查询 modelID 和 UserModelID 用于缓存失效
	var route TAgentHttpAIRoute
	err := database.DB.Table(AgentHttpAIRouteTableName).Where("id = ? AND deleted_at IS NULL", id).First(&route).Error
	if err != nil {
		return fmt.Errorf("ai route not found (id=%d): %w", id, err)
	}

	result := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ?", id).
		Unscoped().
		Delete(&TAgentHttpAIRoute{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete ai route: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("ai route not found (id=%d)", id)
	}

	removeRouteFromCache(route.UserModelID, id)
	logger.Printf("[database.DB] Deleted AI route (id=%d)", id)

	// 记录用户操作日志
	user, _ := GetUserByID(route.UserID)
	userName := ""
	if user != nil {
		userName = user.UserName
	}
	LogAIRouteAction("DELETE_ROUTE", userName, route.UserModelID, route.DstEndPointIDList)

	return nil
}

// BatchUpdateAIRoute 批量更新智能路由
// updates 支持字段：algorithm_strategy_type(int), dst_endpoint_id_list(string),
//
//	dst_endpoint_id_status_list(string), dst_endpoint_algorithm_type_list(string)
//
// 协议一致性策略：
//   - 当传入 dst_endpoint_id_list 时，按每条路由的 protocol_type 自动校正 algorithm_type：
//     endpoint.protocol_type == route.protocol_type -> 协议直连(1)
//     endpoint.protocol_type != route.protocol_type -> 协议转换器(2)
//   - 即使前端传错的 algorithm_type 也被后端覆盖，保证不会触发"协议直连要求源站协议一致"错误
//   - 仅更新 algorithm_strategy_type 时不动 endpoint 列表，单协议场景天然安全
func BatchUpdateAIRoute(ids []uint64, updates map[string]interface{}) (int64, []error) {
	if len(ids) == 0 {
		return 0, []error{fmt.Errorf("ids 不能为空")}
	}
	algorithmStrategyType, _ := updates["algorithm_strategy_type"].(int)
	var dstEndPointIDList, dstEndPointIDStatusList, dstEndPointAlgorithmTypeList string
	if v, ok := updates["dst_endpoint_id_list"].(string); ok {
		dstEndPointIDList = v
	}
	if v, ok := updates["dst_endpoint_id_status_list"].(string); ok {
		dstEndPointIDStatusList = v
	}
	if v, ok := updates["dst_endpoint_algorithm_type_list"].(string); ok {
		dstEndPointAlgorithmTypeList = v
	}
	// 解析提前给定的 endpoint 算法类型（可能因前端的"混合协议"被禁用，此处通常为 1）
	presetAlgorithmTypes, _ := ParseDstEndPointAlgorithmTypeList(dstEndPointAlgorithmTypeList)

	var updatedCount int64
	var errs []error
	for _, id := range ids {
		// 在 database.DB 锁内逐条更新，使用现有 UpdateAIRoute（已实现合并语义）
		item := &TAgentHttpAIRoute{ID: id}
		if algorithmStrategyType != 0 {
			item.AlgorithmStrategyType = algorithmStrategyType
		}
		if dstEndPointIDList != "" {
			item.DstEndPointIDList = dstEndPointIDList
			item.DstEndPointIDStatusList = dstEndPointIDStatusList
			// 按本条路由的 protocol_type 校正 algorithm_type 列表，确保协议一致性
			item.DstEndPointAlgorithmTypeList = AlgorithmTypeListByRouteProtocol(id, dstEndPointIDList, presetAlgorithmTypes)
		}
		if err := UpdateAIRoute(item); err != nil {
			errs = append(errs, fmt.Errorf("更新路由 %d 失败: %w", id, err))
			continue
		}
		updatedCount++
	}
	return updatedCount, errs
}

// AlgorithmTypeListByRouteProtocol 根据路由当前 protocol_type 与 endpoint.protocol_type
// 自动生成 source endpoint 的算法类型列表。
// 若 routeID 不存在或 endpoint 不存在，回退为全 1（协议直连）。
func AlgorithmTypeListByRouteProtocol(routeID uint64, endpointList string, presetTypes []int) string {
	route, err := GetAIRouteByID(routeID)
	if err != nil || route == nil {
		return BuildDefaultDstEndPointAlgorithmTypeList(countCsv(endpointList))
	}
	ids, err := ParseDstEndPointIDList(endpointList)
	if err != nil || len(ids) == 0 {
		return ""
	}
	types := make([]int, 0, len(ids))
	for i, id := range ids {
		// 先看 preset（前端传过的）
		var preset int
		if i < len(presetTypes) {
			preset = presetTypes[i]
		}
		ep, err := GetDstEndPointByID(id)
		if err != nil || ep == nil {
			// endpoint 不存在，保留 preset（后端会再次校验失败）
			if preset == DstEndPointAlgorithmType_ProtocolConverter {
				types = append(types, DstEndPointAlgorithmType_ProtocolConverter)
			} else {
				types = append(types, DstEndPointAlgorithmType_Direct)
			}
			continue
		}
		if parseProtocolType(ep.ProtocolType) == parseProtocolType(route.ProtocolType) {
			types = append(types, DstEndPointAlgorithmType_Direct)
		} else {
			types = append(types, DstEndPointAlgorithmType_ProtocolConverter)
		}
	}
	return FormatDstEndPointAlgorithmTypeList(types)
}

// parseProtocolType 将 0/1/2 标准化为 1/2（兼容空字段）
func parseProtocolType(t int) int {
	if t == protocol.AgentProtocolType_OpenAI {
		return protocol.AgentProtocolType_OpenAI
	}
	return protocol.AgentProtocolType_Anthropic
}

// countCsv 计算逗号分隔字符串的元素数量
func countCsv(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return len(strings.Split(s, ","))
}

// BatchDeleteAIRoute 批量硬删除智能路由
func BatchDeleteAIRoute(ids []uint64) (int64, []error) {
	if len(ids) == 0 {
		return 0, []error{fmt.Errorf("ids 不能为空")}
	}
	var deletedCount int64
	var errs []error
	for _, id := range ids {
		if err := DeleteAIRoute(id); err != nil {
			errs = append(errs, fmt.Errorf("删除路由 %d 失败: %w", id, err))
			continue
		}
		deletedCount++
	}
	return deletedCount, errs
}

// BatchEndpointDetail 批量源站操作的单条路由结果明细
type BatchEndpointDetail struct {
	RouteID uint64 `json:"route_id"`
	Status  string `json:"status"` // "success" / "skip" / "fail"
	Reason  string `json:"reason"` // 跳过 / 失败原因（中文前端展示）
}

// BatchEndpointResult 批量源站操作的汇总结果
type BatchEndpointResult struct {
	SuccessCount int                    `json:"success_count"`
	SkipCount    int                    `json:"skip_count"`
	FailCount    int                    `json:"fail_count"`
	Details      []BatchEndpointDetail  `json:"details"`
}

// BatchAddEndpointsToRoutes 批量向多条路由追加源站
// 逐条处理：
//   - 路由已包含该源站 → 跳过该路由（继续处理其他路由）
//   - 路由未包含该源站 → 追加到列表末尾
//   - algorithmStrategyType != 0 时同时更新算法策略
//
// 协议一致性：追加时按路由 protocol_type 与源站 protocol_type 自动判定算法类型。
// 幂等性：重复执行相同追加操作，结果一致。
func BatchAddEndpointsToRoutes(routeIDs, endpointIDs []uint64, algorithmStrategyType int) BatchEndpointResult {
	result := BatchEndpointResult{Details: make([]BatchEndpointDetail, 0, len(routeIDs))}
	if len(routeIDs) == 0 || len(endpointIDs) == 0 {
		return result
	}
	// 预查源站信息，避免循环内重复查询
	endpointCache := make(map[uint64]*TAgentDstEndPoint, len(endpointIDs))
	for _, epID := range endpointIDs {
		if ep, err := GetDstEndPointByID(epID); err == nil && ep != nil {
			endpointCache[epID] = ep
		}
	}

	for _, routeID := range routeIDs {
		detail := BatchEndpointDetail{RouteID: routeID}
		// 单条路由加锁更新，避免并发冲突
		agentAIRouteMutex.Lock()
		err := batchAddEndpointsToSingleRoute(routeID, endpointIDs, algorithmStrategyType, endpointCache)
		agentAIRouteMutex.Unlock()

		if err != nil {
			// 区分"已存在可跳过"与真正的错误
			errStr := err.Error()
			if strings.Contains(errStr, "already exists") {
				detail.Status = "skip"
				detail.Reason = errStr
				result.SkipCount++
			} else {
				detail.Status = "fail"
				detail.Reason = errStr
				result.FailCount++
			}
		} else {
			detail.Status = "success"
			result.SuccessCount++
		}
		result.Details = append(result.Details, detail)
	}
	return result
}

// batchAddEndpointsToSingleRoute 向单条路由追加源站（调用方必须持有 agentAIRouteMutex）
func batchAddEndpointsToSingleRoute(routeID uint64, endpointIDs []uint64, algorithmStrategyType int, endpointCache map[uint64]*TAgentDstEndPoint) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	var existing TAgentHttpAIRoute
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		First(&existing).Error; err != nil {
		return fmt.Errorf("ai route not found (id=%d): %w", routeID, err)
	}

	// 解析现有源站列表
	currentIDs, err := ParseDstEndPointIDList(existing.DstEndPointIDList)
	if err != nil {
		return fmt.Errorf("parse dst endpoint id list failed: %w", err)
	}
	_, currentAlgos, err := NormalizeDstEndPointAlgorithmTypeList(existing.DstEndPointIDList, existing.DstEndPointAlgorithmTypeList)
	if err != nil {
		return fmt.Errorf("normalize algorithm type list failed: %w", err)
	}
	_, currentStatuses, err := NormalizeDstEndPointIDStatusList(existing.DstEndPointIDList, existing.DstEndPointIDStatusList)
	if err != nil {
		return fmt.Errorf("normalize status list failed: %w", err)
	}

	// 判重：全部已存在则跳过
	existingSet := make(map[uint64]bool, len(currentIDs))
	for _, id := range currentIDs {
		existingSet[id] = true
	}
	newEndpointIDs := make([]uint64, 0, len(endpointIDs))
	for _, epID := range endpointIDs {
		if existingSet[epID] {
			continue
		}
		// 源站不存在于缓存（查不到）也拒绝
		if _, ok := endpointCache[epID]; !ok {
			return fmt.Errorf("endpoint %d not found", epID)
		}
		newEndpointIDs = append(newEndpointIDs, epID)
	}
	if len(newEndpointIDs) == 0 {
		return fmt.Errorf("all endpoints already exists")
	}

	// 新列表 = 旧列表 + 新追加（去重后）
	mergedIDs := append(currentIDs, newEndpointIDs...)
	mergedAlgos := currentAlgos
	mergedStatuses := currentStatuses
	for _, epID := range newEndpointIDs {
		ep := endpointCache[epID]
		// 协议一致性判定：源站协议 == 路由协议 → 直连；否则 → 转换器
		if ep != nil && parseProtocolType(ep.ProtocolType) == parseProtocolType(existing.ProtocolType) {
			mergedAlgos = append(mergedAlgos, DstEndPointAlgorithmType_Direct)
		} else {
			mergedAlgos = append(mergedAlgos, DstEndPointAlgorithmType_ProtocolConverter)
		}
		mergedStatuses = append(mergedStatuses, 1) // 新源站默认启用
	}

	// 构造更新项（复用 UpdateAIRoute 语义，但批量场景直接走 DB 更新避免重复加锁）
	updated := existing
	updated.DstEndPointIDList = formatUint64List(mergedIDs)
	updated.DstEndPointAlgorithmTypeList = FormatDstEndPointAlgorithmTypeList(mergedAlgos)
	updated.DstEndPointIDStatusList = FormatDstEndPointIDStatusList(mergedStatuses)
	updated.DstEndPointIDNumber = len(mergedIDs)
	if algorithmStrategyType != 0 {
		updated.AlgorithmStrategyType = algorithmStrategyType
	}
	autoFillRouteNewFields(&updated)

	if err := ValidateAIRouteInput(updated.UserID, updated.UserModelID, updated.DstEndPointIDList, updated.DstEndPointAlgorithmTypeList, updated.ProtocolType); err != nil {
		return err
	}
	if err := validateAIRouteEndpointAlgorithms(&updated); err != nil {
		return err
	}

	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		Updates(map[string]interface{}{
			"dst_endpoint_id_list":             updated.DstEndPointIDList,
			"dst_endpoint_id_status_list":      updated.DstEndPointIDStatusList,
			"dst_endpoint_algorithm_type_list": updated.DstEndPointAlgorithmTypeList,
			"dst_endpoint_id_number":           updated.DstEndPointIDNumber,
			"algorithm_strategy_type":          updated.AlgorithmStrategyType,
		}).Error; err != nil {
		return fmt.Errorf("failed to update ai route: %w", err)
	}

	updateRouteInCache(&updated)
	logger.Printf("[ROUTE] Batch append endpoints: routeID=%d, added=%v, ids:[%s]->[%s]", routeID, newEndpointIDs, existing.DstEndPointIDList, updated.DstEndPointIDList)
	return nil
}

// BatchRemoveEndpointsFromRoutes 批量删除多条路由中的指定源站
// 逐条处理：
//   - 路由不包含该源站 → 跳过该路由
//   - 路由包含该源站 → 从列表中删除
//   - 删除后列表为空 → 该条拒绝（至少保留 1 个源站）
//   - algorithmStrategyType != 0 时同时更新算法策略
func BatchRemoveEndpointsFromRoutes(routeIDs, endpointIDs []uint64, algorithmStrategyType int) BatchEndpointResult {
	result := BatchEndpointResult{Details: make([]BatchEndpointDetail, 0, len(routeIDs))}
	if len(routeIDs) == 0 || len(endpointIDs) == 0 {
		return result
	}

	// 待删除集合，O(1) 查找
	removeSet := make(map[uint64]bool, len(endpointIDs))
	for _, epID := range endpointIDs {
		removeSet[epID] = true
	}

	for _, routeID := range routeIDs {
		detail := BatchEndpointDetail{RouteID: routeID}
		agentAIRouteMutex.Lock()
		err := batchRemoveEndpointsFromSingleRoute(routeID, removeSet, algorithmStrategyType)
		agentAIRouteMutex.Unlock()

		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "not found in route") || strings.Contains(errStr, "not found") {
				detail.Status = "skip"
				detail.Reason = errStr
				result.SkipCount++
			} else {
				detail.Status = "fail"
				detail.Reason = errStr
				result.FailCount++
			}
		} else {
			detail.Status = "success"
			result.SuccessCount++
		}
		result.Details = append(result.Details, detail)
	}
	return result
}

// batchRemoveEndpointsFromSingleRoute 从单条路由删除指定源站（调用方必须持有 agentAIRouteMutex）
func batchRemoveEndpointsFromSingleRoute(routeID uint64, removeSet map[uint64]bool, algorithmStrategyType int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	var existing TAgentHttpAIRoute
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		First(&existing).Error; err != nil {
		return fmt.Errorf("ai route not found (id=%d): %w", routeID, err)
	}

	ids, err := ParseDstEndPointIDList(existing.DstEndPointIDList)
	if err != nil {
		return fmt.Errorf("parse dst endpoint id list failed: %w", err)
	}
	_, algoTypes, err := NormalizeDstEndPointAlgorithmTypeList(existing.DstEndPointIDList, existing.DstEndPointAlgorithmTypeList)
	if err != nil {
		return fmt.Errorf("normalize algorithm type list failed: %w", err)
	}
	_, statuses, err := NormalizeDstEndPointIDStatusList(existing.DstEndPointIDList, existing.DstEndPointIDStatusList)
	if err != nil {
		return fmt.Errorf("normalize status list failed: %w", err)
	}

	// 判空：没有任何一个待删除源站存在于当前路由
	foundAny := false
	for _, id := range ids {
		if removeSet[id] {
			foundAny = true
			break
		}
	}
	if !foundAny {
		return fmt.Errorf("endpoint not found in route")
	}

	// 过滤掉要删除的源站
	newIDs := make([]uint64, 0, len(ids))
	newAlgos := make([]int, 0, len(algoTypes))
	newStatuses := make([]int, 0, len(statuses))
	for i, id := range ids {
		if removeSet[id] {
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

	// 不允许清空所有源站
	if len(newIDs) == 0 {
		return fmt.Errorf("cannot remove last endpoint: route must have at least 1 endpoint")
	}

	updated := existing
	updated.DstEndPointIDList = formatUint64List(newIDs)
	updated.DstEndPointAlgorithmTypeList = FormatDstEndPointAlgorithmTypeList(newAlgos)
	updated.DstEndPointIDStatusList = FormatDstEndPointIDStatusList(newStatuses)
	updated.DstEndPointIDNumber = len(newIDs)
	if algorithmStrategyType != 0 {
		updated.AlgorithmStrategyType = algorithmStrategyType
	}
	autoFillRouteNewFields(&updated)

	if err := ValidateAIRouteInput(updated.UserID, updated.UserModelID, updated.DstEndPointIDList, updated.DstEndPointAlgorithmTypeList, updated.ProtocolType); err != nil {
		return err
	}

	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", routeID).
		Updates(map[string]interface{}{
			"dst_endpoint_id_list":             updated.DstEndPointIDList,
			"dst_endpoint_id_status_list":      updated.DstEndPointIDStatusList,
			"dst_endpoint_algorithm_type_list": updated.DstEndPointAlgorithmTypeList,
			"dst_endpoint_id_number":           updated.DstEndPointIDNumber,
			"algorithm_strategy_type":          updated.AlgorithmStrategyType,
		}).Error; err != nil {
		return fmt.Errorf("failed to update ai route: %w", err)
	}

	updateRouteInCache(&updated)
	logger.Printf("[ROUTE] Batch remove endpoints: routeID=%d, ids:[%s]->[%s]", routeID, existing.DstEndPointIDList, updated.DstEndPointIDList)
	return nil
}

// GetAIRouteByID 根据路由 ID 获取智能路由
func GetAIRouteByID(id uint64) (*TAgentHttpAIRoute, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var item TAgentHttpAIRoute
	err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("ai route (id=%d) not found", id)
		}
		return nil, fmt.Errorf("failed to query ai route: %w", err)
	}
	return &item, nil
}

// GetAIRoutesByUserID 根据用户 ID 获取所有智能路由
// 优先从内存缓存查询（如果缓存中有该用户的所有路由），否则查询数据库
func GetAIRoutesByUserID(userID uint64) ([]TAgentHttpAIRoute, error) {
	// 先获取用户的模型列表
	models, err := GetUserModelsByUserID(userID)
	if err != nil {
		return nil, err
	}

	var allRoutes []TAgentHttpAIRoute
	cacheMiss := false

	// 尝试从缓存获取每个模型的路由
	for _, model := range models {
		if routes, ok := GetCachedRoutesByModelID(model.ID); ok && len(routes) > 0 {
			for _, r := range routes {
				allRoutes = append(allRoutes, r.TAgentHttpAIRoute)
			}
		} else {
			cacheMiss = true
			break
		}
	}

	// 如果所有模型的路由都在缓存中，直接返回
	if !cacheMiss && len(allRoutes) > 0 {
		return allRoutes, nil
	}

	// 缓存未命中，查询数据库
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var items []TAgentHttpAIRoute
	err = database.DB.Table(AgentHttpAIRouteTableName).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query ai routes: %w", err)
	}
	return items, nil
}

// GetAIRouteByUserModelIDAndProtocol 根据用户模型 ID 和协议类型查询智能路由
// 优先从内存缓存查询（返回 *CachedAIRoute），缓存未命中则查 database.DB（返回 *TAgentHttpAIRoute）
func GetAIRouteByUserModelIDAndProtocol(userModelID uint64, protocolType int) (*TAgentHttpAIRoute, error) {
	// 优先从内存缓存查询
	if cachedRoute, ok := GetCachedRouteByModelIDAndProtocol(userModelID, protocolType); ok {
		return &cachedRoute.TAgentHttpAIRoute, nil
	}

	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentHttpAIRoute
	err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("user_model_id = ? AND protocol_type = ? AND deleted_at IS NULL", userModelID, protocolType).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("ai route for user model (id=%d) protocol(%d) not found", userModelID, protocolType)
		}
		return nil, fmt.Errorf("failed to query ai route: %w", err)
	}
	return &item, nil
}

// GetCachedAIRouteByUserModelIDAndProtocol 根据用户模型 ID 和协议类型从缓存查询智能路由
// 代理热路径专用，返回 *CachedAIRoute（含预解析的 DstEndPointIDs）
func GetCachedAIRouteByUserModelIDAndProtocol(userModelID uint64, protocolType int) (*CachedAIRoute, error) {
	if cachedRoute, ok := GetCachedRouteByModelIDAndProtocol(userModelID, protocolType); ok {
		return cachedRoute, nil
	}
	return nil, fmt.Errorf("ai route for user model (id=%d) protocol(%d) not found in cache", userModelID, protocolType)
}

// GetAIRoutesByUserModelID 根据用户模型 ID 获取所有智能路由
func GetAIRoutesByUserModelID(userModelID uint64) ([]TAgentHttpAIRoute, error) {
	// 优先从内存缓存查询
	if routes, ok := GetCachedRoutesByModelID(userModelID); ok {
		result := make([]TAgentHttpAIRoute, len(routes))
		for i, r := range routes {
			result[i] = r.TAgentHttpAIRoute
		}
		return result, nil
	}

	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var items []TAgentHttpAIRoute
	err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("user_model_id = ? AND deleted_at IS NULL", userModelID).
		Order("protocol_type ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query ai routes: %w", err)
	}
	return items, nil
}

// GetAIRouteWithDetails 根据用户模型 ID 和协议类型查询智能路由及关联的源站详情
func GetAIRouteWithDetails(userModelID uint64, protocolType int) (*TAgentHttpAIRoute, *TAgentDstEndPoint, error) {
	route, err := GetAIRouteByUserModelIDAndProtocol(userModelID, protocolType)
	if err != nil {
		return nil, nil, err
	}

	selectedID, err := GetFirstDstEndPointIDFromRoute(route)
	if err != nil {
		return nil, nil, err
	}

	endpoint, err := GetDstEndPointByID(selectedID)
	if err != nil {
		return nil, nil, err
	}

	return route, endpoint, nil
}
