package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"strings"
	"sync"

	"gorm.io/gorm"
)

var (
	agentModelMutex sync.RWMutex
)

// ValidateUserModelInput 校验用户模型输入
func ValidateUserModelInput(modelName string) error {
	modelName = strings.TrimSpace(modelName)
	if len(modelName) < 8 || len(modelName) > 64 {
		return fmt.Errorf("平台模型名称长度必须在 8-64 位之间")
	}
	return nil
}

// ValidateUserModelAPIKey 校验模型 API Key
func ValidateUserModelAPIKey(apiKey string) error {
	if len(apiKey) < 32 || len(apiKey) > 128 {
		return fmt.Errorf("API Key 长度必须在 32-128 位之间")
	}
	return nil
}

// AddUserModel 添加用户模型
func AddUserModel(item *TAgentHttpUserModelInfo) error {
	agentModelMutex.Lock()
	defer agentModelMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 默认启用状态
	if item.Status == 0 {
		item.Status = UserModelStatus_Enabled
	}

	item.ModelName = strings.TrimSpace(item.ModelName)
	if err := ValidateUserModelInput(item.ModelName); err != nil {
		return err
	}

	// 获取用户名用于生成 API Key
	var user TAgentHttpUserInfo
	err := database.DB.Table(AgentHttpUserInfoTableName).Where("id = ? AND deleted_at IS NULL", item.UserID).First(&user).Error
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// 自动生成 API Key（如果未提供）
	if item.APIKey == "" {
		generated, gerr := generateAPIKey(user.UserName, item.ModelName)
		if gerr != nil {
			return gerr
		}
		item.APIKey = generated
	} else {
		item.APIKey = strings.TrimSpace(item.APIKey)
		if err := ValidateUserModelAPIKey(item.APIKey); err != nil {
			return err
		}
	}

	// 检查 API Key 是否已存在
	var count int64
	err = database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("api_key = ? AND deleted_at IS NULL", item.APIKey).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check api key: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("api key already exists")
	}

	// 检查同一用户下模型名称是否重复
	err = database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("user_id = ? AND model_name = ? AND deleted_at IS NULL", item.UserID, item.ModelName).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check model name: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("model name '%s' already exists for this user", item.ModelName)
	}

	// 创建记录
	err = database.DB.Table(AgentHttpUserModelInfoTableName).Create(item).Error
	if err != nil {
		return fmt.Errorf("failed to create user model: %w", err)
	}

	AddModelToCache(item)
	logger.Printf("[database.DB] Added user model: %s (api_key prefix: %s...)", item.ModelName, item.APIKey[:8])

	// 记录用户操作日志（复用前面已查询的 user 变量）
	logger.LogUserAction("ADD_MODEL", user.UserName, fmt.Sprintf("模型名称=%s", item.ModelName))
	return nil
}

// UpdateUserModel 更新用户模型
func UpdateUserModel(item *TAgentHttpUserModelInfo) error {
	agentModelMutex.Lock()
	defer agentModelMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if item.ID == 0 {
		return fmt.Errorf("id is required for update")
	}

	item.ModelName = strings.TrimSpace(item.ModelName)
	if err := ValidateUserModelInput(item.ModelName); err != nil {
		return err
	}

	// 检查同一用户下模型名称是否与其他记录冲突
	var count int64
	err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("user_id = ? AND model_name = ? AND id != ? AND deleted_at IS NULL", item.UserID, item.ModelName, item.ID).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check model name: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("model name '%s' already exists for this user", item.ModelName)
	}

	// 更新记录
	result := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		Updates(map[string]any{
			"model_name": item.ModelName,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update user model: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user model not found (id=%d)", item.ID)
	}

	invalidateModelCache(item.ID)

	// 重新加载模型到缓存，确保代理热路径立即生效
	updatedModel, err := GetUserModelByID(item.ID)
	if err == nil {
		AddModelToCache(updatedModel)
	}

	logger.Printf("[database.DB] Updated user model (id=%d): %s", item.ID, item.ModelName)

	// 记录用户操作日志
	user, _ := GetUserByID(item.UserID)
	if user != nil {
		logger.LogUserAction("UPDATE_MODEL", user.UserName, fmt.Sprintf("模型名称=%s", item.ModelName))
	}
	return nil
}

// DeleteUserModel 软删除用户模型
func DeleteUserModel(id uint64) error {
	agentModelMutex.Lock()
	defer agentModelMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	result := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("id = ?", id).
		Delete(&TAgentHttpUserModelInfo{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete user model: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user model not found (id=%d)", id)
	}

	// 先获取模型信息用于日志
	model, _ := GetUserModelByID(id)
	var userName string
	var modelName string
	if model != nil {
		user, _ := GetUserByID(model.UserID)
		if user != nil {
			userName = user.UserName
		}
		modelName = model.ModelName
	}

	invalidateModelCache(id)
	logger.Printf("[database.DB] Deleted user model (id=%d)", id)

	// 记录用户操作日志
	if userName != "" && modelName != "" {
		logger.LogUserAction("DELETE_MODEL", userName, fmt.Sprintf("模型名称=%s", modelName))
	}
	return nil
}

// GetUserModelsByUserID 根据用户 ID 获取所有模型
func GetUserModelsByUserID(userID uint64) ([]TAgentHttpUserModelInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var items []TAgentHttpUserModelInfo
	err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query user models: %w", err)
	}
	return items, nil
}

// GetAllUserModels 获取所有用户的所有模型（管理端用）
func GetAllUserModels() ([]TAgentHttpUserModelInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var items []TAgentHttpUserModelInfo
	err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("deleted_at IS NULL").
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query all user models: %w", err)
	}
	return items, nil
}

// GetUserModelByUserIDAndModelName 根据用户ID和模型名称查询用户模型
func GetUserModelByUserIDAndModelName(userID uint64, modelName string) (*TAgentHttpUserModelInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentHttpUserModelInfo
	err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("user_id = ? AND model_name = ? AND deleted_at IS NULL", userID, modelName).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user model '%s' not found for user", modelName)
		}
		return nil, fmt.Errorf("failed to query user model: %w", err)
	}
	return &item, nil
}

// GetUserModelByAPIKey 根据 API Key 查询模型
func GetUserModelByAPIKey(apiKey string) (*TAgentHttpUserModelInfo, error) {
	// 优先从内存缓存查询
	if m, ok := GetCachedModelByAPIKey(apiKey); ok {
		return m, nil
	}

	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentHttpUserModelInfo
	err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("api_key = ? AND deleted_at IS NULL", apiKey).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("invalid api key")
		}
		return nil, fmt.Errorf("failed to query user model: %w", err)
	}
	return &item, nil
}

// GetUserModelByID 根据 ID 查询用户模型
func GetUserModelByID(id uint64) (*TAgentHttpUserModelInfo, error) {
	// 优先从内存缓存查询
	if m, ok := GetCachedModelByID(id); ok {
		return m, nil
	}

	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentHttpUserModelInfo
	err := database.DB.Table(AgentHttpUserModelInfoTableName).Where("id = ? AND deleted_at IS NULL", id).First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user model (id=%d) not found", id)
		}
		return nil, fmt.Errorf("failed to query user model: %w", err)
	}
	return &item, nil
}

// UpdateUserModelStatus 更新用户模型状态
func UpdateUserModelStatus(id uint64, status int) error {
	agentModelMutex.Lock()
	defer agentModelMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 验证状态值
	if status != UserModelStatus_Enabled && status != UserModelStatus_Disabled {
		return fmt.Errorf("invalid status value")
	}

	// 更新记录
	result := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{
			"status": status,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update user model status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user model not found (id=%d)", id)
	}

	// 只更新缓存中的状态字段
	UpdateCachedUserModelStatus(id, status)

	logger.Printf("[database.DB] Updated user model status (id=%d): status=%d", id, status)

	// 记录用户操作日志
	model, _ := GetUserModelByID(id)
	if model != nil {
		user, _ := GetUserByID(model.UserID)
		if user != nil {
			statusStr := "启用"
			if status == UserModelStatus_Disabled {
				statusStr = "禁用"
			}
			logger.LogUserAction("UPDATE_MODEL_STATUS", user.UserName, fmt.Sprintf("模型名称=%s 状态=%s", model.ModelName, statusStr))
		}
	}
	return nil
}
