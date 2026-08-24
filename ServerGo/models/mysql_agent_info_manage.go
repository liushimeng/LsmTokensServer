package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"sync"
	"time"
)

var (
	agentInfoMutex sync.RWMutex
)

// InitAgentHttpAgentInfoTable 初始化 AI Agent 工具信息表
func InitAgentHttpAgentInfoTable() error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", AgentHttpAgentInfoTableName)
	err := database.DB.Table(AgentHttpAgentInfoTableName).AutoMigrate(&TAgentHttpAgentInfo{})
	if err != nil {
		return fmt.Errorf("failed to migrate table %s: %w", AgentHttpAgentInfoTableName, err)
	}
	logger.Printf("[database.DB] Table %s migrated successfully", AgentHttpAgentInfoTableName)
	return nil
}

// GetDistinctAgentToolNames 从 TAgentHttpAgentInfo 获取所有 Agent 工具名称列表
func GetDistinctAgentToolNames() ([]string, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	// 先尝试从内存缓存获取
	if cachedList := GetCachedAgentToolNameList(); cachedList != nil {
		return cachedList, nil
	}

	// 从数据库查询
	var agentInfos []TAgentHttpAgentInfo
	err := database.DB.Table(AgentHttpAgentInfoTableName).
		Where("deleted_at IS NULL").
		Order("usage_count DESC").
		Find(&agentInfos).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query agent tool names: %w", err)
	}

	result := make([]string, 0, len(agentInfos))
	for _, info := range agentInfos {
		result = append(result, info.AgentToolName)
	}

	return result, nil
}

// GetCachedAgentToolNameList 从内存缓存获取 Agent 工具名称列表
func GetCachedAgentToolNameList() []string {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()

	if agentCache.agentToolNames != nil {
		return agentCache.agentToolNames
	}
	return nil
}

// UpdateAgentInfoUsageInCache 更新内存中的 Agent 使用统计
func UpdateAgentInfoUsageInCache(agentToolName string, seenAt time.Time) {
	if agentToolName == "" || agentToolName == "unknown" {
		return
	}

	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	// 确保 map 初始化
	if agentCache.agentInfos == nil {
		agentCache.agentInfos = make(map[string]*TAgentHttpAgentInfo)
	}

	if info, exists := agentCache.agentInfos[agentToolName]; exists {
		// 更新现有记录
		info.LastSeenAt = seenAt
		info.UsageCount++
	} else {
		// 创建新记录
		agentCache.agentInfos[agentToolName] = &TAgentHttpAgentInfo{
			AgentToolName: agentToolName,
			FirstSeenAt:   seenAt,
			LastSeenAt:    seenAt,
			UsageCount:    1,
		}
		// 更新名称列表缓存
		agentCache.agentToolNames = append(agentCache.agentToolNames, agentToolName)
	}
}

// UpdateAgentInfoUsage 更新 Agent 工具使用统计（原子操作）
func UpdateAgentInfoUsage(agentToolName string, seenAt time.Time) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	if agentToolName == "" || agentToolName == "unknown" {
		return nil
	}

	// 先更新内存缓存
	UpdateAgentInfoUsageInCache(agentToolName, seenAt)

	// 异步更新数据库（避免阻塞）
	go func() {
		agentInfoMutex.Lock()
		defer agentInfoMutex.Unlock()

		now := time.Now()

		// 使用 INSERT ... ON DUPLICATE KEY UPDATE 原子操作
		result := database.DB.Exec(`
			INSERT INTO `+AgentHttpAgentInfoTableName+`
				(agent_tool_name, first_seen_at, last_seen_at, usage_count, created_at, updated_at)
			VALUES (?, ?, ?, 1, ?, ?)
			ON DUPLICATE KEY UPDATE
				last_seen_at = VALUES(last_seen_at),
				usage_count = usage_count + 1,
				updated_at = VALUES(updated_at)
		`, agentToolName, seenAt, seenAt, now, now)

		if result.Error != nil {
			logger.Printf("[database.DB] Failed to update agent info usage for %s: %v", agentToolName, result.Error)
		}
	}()

	return nil
}

// GetAgentToolStats 从 TAgentHttpAgentInfo 获取 Agent 工具统计数据
func GetAgentToolStats() ([]TAgentHttpAgentInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var agentInfos []TAgentHttpAgentInfo
	err := database.DB.Table(AgentHttpAgentInfoTableName).
		Where("deleted_at IS NULL").
		Order("usage_count DESC").
		Limit(100).
		Find(&agentInfos).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query agent tool stats: %w", err)
	}

	return agentInfos, nil
}

// MigrateAgentToolColumns 为所有分表添加 agent_tool_name 和 agent_tool_info 字段
func MigrateAgentToolColumns(subTableNum int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	for i := 0; i < subTableNum; i++ {
		tableName := GetAgentHttpTableName("", "", i+1)

		// 检查并添加 agent_tool_name 字段
		var columnExist bool
		database.DB.Raw("SHOW COLUMNS FROM " + tableName + " LIKE 'agent_tool_name'").Scan(&columnExist)
		if !columnExist {
			logger.Printf("[database.DB] Adding column agent_tool_name to %s", tableName)
			err := database.DB.Exec("ALTER TABLE " + tableName + " ADD COLUMN agent_tool_name VARCHAR(64) DEFAULT ''").Error
			if err != nil {
				logger.Printf("[database.DB] Warning: Failed to add agent_tool_name to %s: %v", tableName, err)
			} else {
				// 添加索引
				database.DB.Exec("CREATE INDEX idx_" + tableName + "_agent_tool_name ON " + tableName + "(agent_tool_name)")
			}
		}

		// 检查并添加 agent_tool_info 字段
		database.DB.Raw("SHOW COLUMNS FROM " + tableName + " LIKE 'agent_tool_info'").Scan(&columnExist)
		if !columnExist {
			logger.Printf("[database.DB] Adding column agent_tool_info to %s", tableName)
			err := database.DB.Exec("ALTER TABLE " + tableName + " ADD COLUMN agent_tool_info VARCHAR(512) DEFAULT ''").Error
			if err != nil {
				logger.Printf("[database.DB] Warning: Failed to add agent_tool_info to %s: %v", tableName, err)
			}
		}
	}

	logger.Printf("[database.DB] Agent tool columns migration completed")
	return nil
}
