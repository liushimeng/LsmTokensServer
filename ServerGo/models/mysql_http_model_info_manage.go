package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"strings"
	"sync"
)

var (
	agentModelInfoMutex sync.RWMutex
)

// ValidateModelInfoInput 校验模型信息输入
func ValidateModelInfoInput(modelName string) error {
	if strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("模型名称不能为空")
	}
	return nil
}

// AddModelInfo 添加模型信息
func AddModelInfo(item *TAgentModelInfo) error {
	agentModelInfoMutex.Lock()
	defer agentModelInfoMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	item.ModelName = strings.TrimSpace(item.ModelName)
	if err := ValidateModelInfoInput(item.ModelName); err != nil {
		return err
	}

	// 检查是否已存在
	var count int64
	if err := database.DB.Table(AgentModelInfoTableName).
		Where("model_name = ? AND deleted_at IS NULL", item.ModelName).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check model info: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("模型 '%s' 已存在", item.ModelName)
	}

	err := database.DB.Table(AgentModelInfoTableName).Create(item).Error
	if err != nil {
		return fmt.Errorf("failed to create model info: %w", err)
	}

	addModelInfoToCache(item)
	logger.Printf("[database.DB] Added model info: %s (id=%d)", item.ModelName, item.ID)
	return nil
}

// UpdateModelInfo 更新模型信息
func UpdateModelInfo(item *TAgentModelInfo) error {
	agentModelInfoMutex.Lock()
	defer agentModelInfoMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if item.ID == 0 {
		return fmt.Errorf("id is required for update")
	}

	item.ModelName = strings.TrimSpace(item.ModelName)
	if err := ValidateModelInfoInput(item.ModelName); err != nil {
		return err
	}

	// 检查新名称是否与其他记录冲突
	var count int64
	if err := database.DB.Table(AgentModelInfoTableName).
		Where("model_name = ? AND id != ? AND deleted_at IS NULL", item.ModelName, item.ID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check model info: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("模型 '%s' 已存在", item.ModelName)
	}

	result := database.DB.Table(AgentModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		Updates(map[string]interface{}{
			"model_name":          item.ModelName,
			"description":         item.Description,
			"cost_per100w_input":  item.CostPer100wInput,
			"cost_per100w_output": item.CostPer100wOutput,
			"max_context_length":  item.MaxContextLength,
			"avg_ttf_bms":         item.AvgTTFBms,
			"avg_elapsed_ms":      item.AvgElapsedMs,
			"tokens_per_second":   item.TokensPerSecond,
			"success_rate":        item.SuccessRate,
			"error429_rate":       item.Error429Rate,
			"error5xx_rate":       item.Error5xxRate,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update model info: %w", result.Error)
	}

	// 重新加载到缓存
	var updated TAgentModelInfo
	if err := database.DB.Table(AgentModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		First(&updated).Error; err == nil {
		updateModelInfoInCache(&updated)
	}

	logger.Printf("[database.DB] Updated model info (id=%d)", item.ID)
	return nil
}

// DeleteModelInfo 删除模型信息（硬删除）
func DeleteModelInfo(id uint64) error {
	agentModelInfoMutex.Lock()
	defer agentModelInfoMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 先查询 modelName 用于缓存清理
	var item TAgentModelInfo
	database.DB.Table(AgentModelInfoTableName).Where("id = ? AND deleted_at IS NULL", id).First(&item)

	result := database.DB.Table(AgentModelInfoTableName).
		Where("id = ?", id).
		Unscoped().
		Delete(&TAgentModelInfo{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete model info: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("model info not found (id=%d)", id)
	}

	removeModelInfoFromCache(item.ModelName, id)
	logger.Printf("[database.DB] Deleted model info (id=%d)", id)
	return nil
}

// GetAllModelInfos 获取所有模型信息（带分页，page=0 或 pageSize=0 时返回全部，用于兼容旧调用）
func GetAllModelInfos(page, pageSize int) ([]TAgentModelInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var items []TAgentModelInfo
	query := database.DB.Table(AgentModelInfoTableName).
		Where("deleted_at IS NULL").
		Order("id ASC")

	// 分页参数有效时才启用分页
	if page > 0 && pageSize > 0 {
		if pageSize > 1000 {
			pageSize = 1000 // 限制最大页面大小
		}
		offset := (page - 1) * pageSize
		query = query.Limit(pageSize).Offset(offset)
	}

	err := query.Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query model infos: %w", err)
	}
	return items, nil
}

// GetModelInfoByID 根据 ID 获取模型信息
func GetModelInfoByID(id uint64) (*TAgentModelInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var item TAgentModelInfo
	err := database.DB.Table(AgentModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// GetModelInfoByName 根据名称获取模型信息（先查缓存）
func GetModelInfoByName(modelName string) (*TAgentModelInfo, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	if mi, ok := agentCache.modelInfos[modelName]; ok {
		return mi, true
	}
	return nil, false
}

// SyncModelInfoFromEndpoint 从源站同步模型信息（如果不存在则创建）
func SyncModelInfoFromEndpoint(modelName string) error {
	if database.DB == nil || modelName == "" {
		return nil
	}

	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil
	}

	// 检查是否已存在
	var count int64
	if err := database.DB.Table(AgentModelInfoTableName).
		Where("model_name = ? AND deleted_at IS NULL", modelName).
		Count(&count).Error; err != nil {
		return nil // 忽略错误（测试环境表可能不存在）
	}
	if count > 0 {
		return nil // 已存在，无需创建
	}

	// 自动创建
	item := &TAgentModelInfo{
		ModelName: modelName,
	}
	if err := database.DB.Table(AgentModelInfoTableName).Create(item).Error; err != nil {
		return fmt.Errorf("failed to sync model info: %w", err)
	}

	addModelInfoToCache(item)
	logger.Printf("[database.DB] Auto-synced model info from endpoint: %s (id=%d)", modelName, item.ID)
	return nil
}

// GetEndpointCountByModelName 获取某个模型关联的源站数量
func GetEndpointCountByModelName(modelName string) int {
	if database.DB == nil {
		return 0
	}
	var count int64
	database.DB.Table(AgentDstEndPointTableName).
		Where("model_name = ? AND deleted_at IS NULL", modelName).
		Count(&count)
	return int(count)
}

// GetModelInfosForUser 获取用户关联的所有模型信息
func GetModelInfosForUser(userID uint64) ([]TAgentModelInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	// 先获取用户的所有源站的 ModelName
	var modelNames []string
	err := database.DB.Table(AgentDstEndPointTableName).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Distinct("model_name").
		Pluck("model_name", &modelNames).Error
	if err != nil {
		return nil, err
	}
	if len(modelNames) == 0 {
		return []TAgentModelInfo{}, nil
	}

	// 再查询对应的模型信息
	var items []TAgentModelInfo
	err = database.DB.Table(AgentModelInfoTableName).
		Where("model_name IN ? AND deleted_at IS NULL", modelNames).
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ============================================================================
// 缓存操作
// ============================================================================

func addModelInfoToCache(item *TAgentModelInfo) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.modelInfos == nil {
		agentCache.modelInfos = make(map[string]*TAgentModelInfo)
	}
	if agentCache.modelInfosByID == nil {
		agentCache.modelInfosByID = make(map[uint64]*TAgentModelInfo)
	}
	agentCache.modelInfos[item.ModelName] = item
	agentCache.modelInfosByID[item.ID] = item
}

func updateModelInfoInCache(item *TAgentModelInfo) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.modelInfos == nil {
		agentCache.modelInfos = make(map[string]*TAgentModelInfo)
	}
	if agentCache.modelInfosByID == nil {
		agentCache.modelInfosByID = make(map[uint64]*TAgentModelInfo)
	}
	// 如果 modelName 变了，删除旧的 key
	for name, mi := range agentCache.modelInfos {
		if mi.ID == item.ID && name != item.ModelName {
			delete(agentCache.modelInfos, name)
			break
		}
	}
	agentCache.modelInfos[item.ModelName] = item
	agentCache.modelInfosByID[item.ID] = item
}

func removeModelInfoFromCache(modelName string, id uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.modelInfos != nil {
		delete(agentCache.modelInfos, modelName)
	}
	if agentCache.modelInfosByID != nil {
		delete(agentCache.modelInfosByID, id)
	}
}
