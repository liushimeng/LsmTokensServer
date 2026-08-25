package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	AgentHttpUserInfoTableName      = "TAgentHttpUserInfo"
	AgentHttpUserModelInfoTableName = "TAgentHttpUserModelInfo"
	AgentHttpAgentInfoTableName     = "TAgentHttpAgentInfo"
)

var (
	agentUserMutex sync.RWMutex
)

// InitAgentHttpUserInfoTable 初始化用户信息表
func InitAgentHttpUserInfoTable() error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", AgentHttpUserInfoTableName)
	err := database.DB.Table(AgentHttpUserInfoTableName).AutoMigrate(&TAgentHttpUserInfo{})
	if err != nil {
		return fmt.Errorf("failed to migrate table %s: %w", AgentHttpUserInfoTableName, err)
	}
	logger.Printf("[database.DB] Table %s migrated successfully", AgentHttpUserInfoTableName)
	return nil
}

// InitAgentHttpUserModelInfoTable 初始化用户模型信息表
func InitAgentHttpUserModelInfoTable() error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", AgentHttpUserModelInfoTableName)
	err := database.DB.Table(AgentHttpUserModelInfoTableName).AutoMigrate(&TAgentHttpUserModelInfo{})
	if err != nil {
		return fmt.Errorf("failed to migrate table %s: %w", AgentHttpUserModelInfoTableName, err)
	}
	logger.Printf("[database.DB] Table %s migrated successfully", AgentHttpUserModelInfoTableName)
	return nil
}

// ValidateUserInput 校验用户输入
func ValidateUserInput(userName, password, phone string, anthropicEnabled, openAIEnabled bool) error {
	userName = strings.TrimSpace(userName)
	phone = strings.TrimSpace(phone)

	if len(userName) < 3 || len(userName) > 50 {
		return fmt.Errorf("用户名长度必须在 3-50 位之间")
	}
	if len(password) > 0 && len(password) < 6 {
		return fmt.Errorf("密码长度至少 6 位")
	}
	if len(password) > 128 {
		return fmt.Errorf("密码长度不能超过 128 位")
	}
	if phone != "" {
		// 简单手机号校验：纯数字，长度 7-20 位
		if len(phone) < 7 || len(phone) > 20 {
			return fmt.Errorf("手机号长度必须在 7-20 位之间")
		}
		for _, c := range phone {
			if c < '0' || c > '9' {
				return fmt.Errorf("手机号只能包含数字")
			}
		}
	}
	if !anthropicEnabled && !openAIEnabled {
		return fmt.Errorf("Anthropic 和 OpenAI 协议至少需要启用一个")
	}
	return nil
}

// AddUser 添加用户
func AddUser(item *TAgentHttpUserInfo) error {
	agentUserMutex.Lock()
	defer agentUserMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	item.UserName = strings.TrimSpace(item.UserName)
	item.Phone = strings.TrimSpace(item.Phone)
	// 默认启用用户
	if item.Status == 0 {
		item.Status = UserStatus_Enabled
	}

	if err := ValidateUserInput(item.UserName, item.Password, item.Phone, item.AnthropicEnabled, item.OpenAIEnabled); err != nil {
		return err
	}

	// 检查用户名是否已存在
	var count int64
	err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("user_name = ? AND deleted_at IS NULL", item.UserName).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check user name: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("user name '%s' already exists", item.UserName)
	}

	// 检查手机号是否已存在（如果提供了手机号）
	if item.Phone != "" {
		err = database.DB.Table(AgentHttpUserInfoTableName).
			Where("phone = ? AND deleted_at IS NULL", item.Phone).
			Count(&count).Error
		if err != nil {
			return fmt.Errorf("failed to check phone: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("phone '%s' already exists", item.Phone)
		}
	}

	// 创建记录
	err = database.DB.Table(AgentHttpUserInfoTableName).Create(item).Error
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	AddUserToCache(item)
	logger.Printf("[database.DB] Added user: %s", item.UserName)

	// 记录用户操作日志（管理员操作）
	logger.LogUserAction("ADD_USER", "admin", fmt.Sprintf("用户名=%s", item.UserName))
	return nil
}

// UpdateUser 更新用户
func UpdateUser(item *TAgentHttpUserInfo) error {
	agentUserMutex.Lock()
	defer agentUserMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if item.ID == 0 {
		return fmt.Errorf("id is required for update")
	}

	item.UserName = strings.TrimSpace(item.UserName)
	item.Phone = strings.TrimSpace(item.Phone)

	// 读取现有用户状态并保留
	existingItem, _ := GetUserByID(item.ID)
	if existingItem != nil {
		item.Status = existingItem.Status
	}

	if err := ValidateUserInput(item.UserName, item.Password, item.Phone, item.AnthropicEnabled, item.OpenAIEnabled); err != nil {
		return err
	}

	// 检查用户名是否与其他记录冲突
	var count int64
	err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("user_name = ? AND id != ? AND deleted_at IS NULL", item.UserName, item.ID).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check user name: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("user name '%s' already exists", item.UserName)
	}

	// 检查手机号是否与其他记录冲突（如果提供了手机号）
	if item.Phone != "" {
		err = database.DB.Table(AgentHttpUserInfoTableName).
			Where("phone = ? AND id != ? AND deleted_at IS NULL", item.Phone, item.ID).
			Count(&count).Error
		if err != nil {
			return fmt.Errorf("failed to check phone: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("phone '%s' already exists", item.Phone)
		}
	}

	// 构建更新字段
	updateFields := map[string]any{
		"user_name":         item.UserName,
		"phone":             item.Phone,
		"anthropic_enabled": item.AnthropicEnabled,
		"openai_enabled":    item.OpenAIEnabled,
	}
	// 密码不为空时才更新
	if item.Password != "" {
		updateFields["password"] = item.Password
	}

	// 更新记录
	result := database.DB.Table(AgentHttpUserInfoTableName).
		Where("id = ? AND deleted_at IS NULL", item.ID).
		Updates(updateFields)
	if result.Error != nil {
		return fmt.Errorf("failed to update user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found (id=%d)", item.ID)
	}

	invalidateUserCache(item.ID)

	// 重新加载用户到缓存，确保代理热路径立即生效
	updatedUser, err := GetUserByID(item.ID)
	if err == nil {
		AddUserToCache(updatedUser)
		// 重新加载该用户的所有模型到缓存（更新 modelsByUserModel 索引）
		models, err := GetUserModelsByUserID(item.ID)
		if err == nil {
			for i := range models {
				AddModelToCache(&models[i])
			}
		}
	}

	logger.Printf("[database.DB] Updated user (id=%d): %s", item.ID, item.UserName)

	// 记录用户操作日志（管理员操作）
	logger.LogUserAction("UPDATE_USER", "admin", fmt.Sprintf("用户名=%s", item.UserName))
	return nil
}

// DeleteUser 软删除用户
func DeleteUser(id uint64) error {
	agentUserMutex.Lock()
	defer agentUserMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 先查询用户名用于日志
	var userName string
	var existing TAgentHttpUserInfo
	err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("id = ?", id).
		First(&existing).Error
	if err == nil {
		userName = existing.UserName
	}

	result := database.DB.Table(AgentHttpUserInfoTableName).
		Where("id = ?", id).
		Delete(&TAgentHttpUserInfo{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found (id=%d)", id)
	}

	invalidateUserCache(id)
	logger.Printf("[database.DB] Deleted user (id=%d)", id)

	// 记录用户操作日志（管理员操作）
	if userName != "" {
		logger.LogUserAction("DELETE_USER", "admin", fmt.Sprintf("用户名=%s", userName))
	}
	return nil
}

// GetAllUsers 获取所有未删除的用户（带分页，page=0 或 pageSize=0 时返回全部，用于兼容旧调用）
func GetAllUsers(page, pageSize int) ([]TAgentHttpUserInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var items []TAgentHttpUserInfo
	query := database.DB.Table(AgentHttpUserInfoTableName).
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
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	return items, nil
}

// GetUserByID 根据 ID 查询用户
func GetUserByID(id uint64) (*TAgentHttpUserInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentHttpUserInfo
	err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user (id=%d) not found", id)
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}
	return &item, nil
}

// GetUserByName 根据用户名查询用户
func GetUserByName(name string) (*TAgentHttpUserInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var item TAgentHttpUserInfo
	err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("user_name = ? AND deleted_at IS NULL", name).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}
	return &item, nil
}

// UpdateUserPasswordHashed 更新用户密码为哈希值（v2.0.56 安全加固：登录时旧明文自动升级用）
func UpdateUserPasswordHashed(id uint64, hashedPassword string) error {
	agentUserMutex.Lock()
	defer agentUserMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	result := database.DB.Table(AgentHttpUserInfoTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{
			"password":   hashedPassword,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update user password: %w", result.Error)
	}
	invalidateUserCache(id)
	return nil
}

// UpdateUserStatus 更新用户状态
func UpdateUserStatus(id uint64, status int) error {
	agentUserMutex.Lock()
	defer agentUserMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 验证状态值
	if status != UserStatus_Enabled && status != UserStatus_Disabled {
		return fmt.Errorf("invalid status value")
	}

	// 更新记录
	result := database.DB.Table(AgentHttpUserInfoTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{
			"status": status,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update user status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found (id=%d)", id)
	}

	// 清除用户缓存（强制中间件重新从数据库加载）
	invalidateUserCache(id)

	logger.Printf("[database.DB] Updated user status (id=%d): status=%d", id, status)

	// 记录用户操作日志（管理员操作）
	user, _ := GetUserByID(id)
	if user != nil {
		statusStr := "启用"
		if status == UserStatus_Disabled {
			statusStr = "禁用"
		}
		logger.LogUserAction("UPDATE_USER_STATUS", "admin", fmt.Sprintf("用户名=%s 状态=%s", user.UserName, statusStr))
	}

	return nil
}
