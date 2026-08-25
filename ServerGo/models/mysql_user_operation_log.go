package models

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
)

const UserOperationLogTableName = "TAgentUserOperationLog"

// 容量控制常量
const (
	userOpLogMaxRecords      = 100000 // 记录上限
	userOpLogCleanupBatch    = 10000  // 每次清理删除的记录数
	userOpLogCheckInterval   = 100    // 每写入 N 条检查一次容量
)

// TAgentUserOperationLog 用户操作日志表
type TAgentUserOperationLog struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt  time.Time `json:"created_at" gorm:"not null;index;comment:操作时间"`
	ActionType string    `json:"action_type" gorm:"size:32;index;idx_action_user_created,priority:1;comment:操作类型"`
	UserName   string    `json:"user_name" gorm:"size:50;index;idx_action_user_created,priority:2;comment:操作用户"`
	Details    string    `json:"details" gorm:"size:2048;comment:操作详情"`
}

var (
	userOpLogWriteCount atomic.Int64
	userOpLogCleanupMu sync.Mutex
)

// InitUserOperationLogTable 初始化用户操作日志表
func InitUserOperationLogTable() error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", UserOperationLogTableName)
	err := database.DB.Table(UserOperationLogTableName).AutoMigrate(&TAgentUserOperationLog{})
	if err != nil {
		return fmt.Errorf("failed to migrate table %s: %w", UserOperationLogTableName, err)
	}
	logger.Printf("[database.DB] Table %s migrated successfully", UserOperationLogTableName)
	return nil
}

// AddUserOperationLog 写入一条用户操作日志
func AddUserOperationLog(actionType, userName, details string) {
	if database.DB == nil {
		return
	}

	record := TAgentUserOperationLog{
		CreatedAt:  time.Now(),
		ActionType: actionType,
		UserName:   userName,
		Details:    details,
	}

	if err := database.DB.Table(UserOperationLogTableName).Create(&record).Error; err != nil {
		logger.Printf("[WARNING] Failed to insert user operation log: %v", err)
		return
	}

	// 容量检查：每写入 userOpLogCheckInterval 条检查一次
	count := userOpLogWriteCount.Add(1)
	if count%userOpLogCheckInterval == 0 {
		go CleanupOldOperationLogs()
	}
}

// QueryUserOperationLogs 分页查询用户操作日志
// keyword 对 action_type + user_name + details 三字段做模糊匹配
// actionType 和 userName 为精确筛选（可选，空字符串表示不筛选）
func QueryUserOperationLogs(page, pageSize int, keyword, actionType, userName string) ([]TAgentUserOperationLog, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("database not initialized")
	}

	table := database.DB.Table(UserOperationLogTableName)

	// 构建查询条件
	if keyword != "" {
		like := "%" + keyword + "%"
		table = table.Where("(action_type LIKE ? OR user_name LIKE ? OR details LIKE ?)", like, like, like)
	}
	if actionType != "" {
		table = table.Where("action_type = ?", actionType)
	}
	if userName != "" {
		table = table.Where("user_name = ?", userName)
	}

	// 统计总数
	var totalCount int64
	if err := table.Count(&totalCount).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count user operation logs: %w", err)
	}

	// 分页查询
	var records []TAgentUserOperationLog
	offset := (page - 1) * pageSize
	if err := table.Order("id DESC").Offset(offset).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to query user operation logs: %w", err)
	}

	return records, totalCount, nil
}

// CleanupOldOperationLogs 清理超限的最老记录
// 当总记录数超过 userOpLogMaxRecords 时，删除最老的 userOpLogCleanupBatch 条
func CleanupOldOperationLogs() {
	if database.DB == nil {
		return
	}

	// 加锁避免并发清理
	userOpLogCleanupMu.Lock()
	defer userOpLogCleanupMu.Unlock()

	var totalCount int64
	if err := database.DB.Table(UserOperationLogTableName).Count(&totalCount).Error; err != nil {
		logger.Printf("[WARNING] Failed to count user operation logs for cleanup: %v", err)
		return
	}

	if totalCount <= userOpLogMaxRecords {
		return
	}

	// 删除最老的 userOpLogCleanupBatch 条记录（子查询先查出要删除的 ID 集合）
	deleteCount := int64(userOpLogCleanupBatch)
	result := database.DB.Table(UserOperationLogTableName).
		Where("id IN (SELECT id FROM (SELECT id FROM TAgentUserOperationLog ORDER BY created_at ASC LIMIT ?) AS tmp)", deleteCount).
		Delete(&TAgentUserOperationLog{})
	if result.Error != nil {
		logger.Printf("[WARNING] Failed to cleanup user operation logs: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Printf("[CLEANUP] User operation logs: deleted %d old records (total was %d)", result.RowsAffected, totalCount)
	}
}
