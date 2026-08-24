package models

import (
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"time"

	"gorm.io/gorm"
)

// TAgentModelInfo 模型信息表
// 以 ModelName 为唯一关键字，存储模型的标签信息（成本、能力、动态统计）
// 为经济型和智能型算法提供决策数据
type TAgentModelInfo struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// 基础信息
	ModelName   string `json:"model_name" gorm:"size:64;uniqueIndex;comment:模型名称（唯一）"`
	Description string `json:"description" gorm:"size:256;comment:模型描述"`

	// 成本标签（手动配置）
	CostPer100wInput  float64 `json:"cost_per_100w_input" gorm:"comment:每100万输入token价格（元）"`
	CostPer100wOutput float64 `json:"cost_per_100w_output" gorm:"comment:每100万输出token价格（元）"`

	// 能力标签（手动配置）
	MaxContextLength int `json:"max_context_length" gorm:"comment:最大上下文长度"`

	// 动态标签（由定时任务从日志聚合计算）
	AvgTTFBms       int     `json:"avg_ttfb_ms" gorm:"comment:近7天平均首字时延(ms)"`
	AvgElapsedMs    int     `json:"avg_elapsed_ms" gorm:"comment:近7天平均总耗时(ms)"`
	TokensPerSecond float64 `json:"tokens_per_second" gorm:"comment:近7天平均生成速度(token/s)"`
	SuccessRate     float64 `json:"success_rate" gorm:"comment:近7天成功率"`
	Error429Rate    float64 `json:"error_429_rate" gorm:"comment:近7天限流错误率"`
	Error5xxRate    float64 `json:"error_5xx_rate" gorm:"comment:近7天服务端错误率"`
}

const AgentModelInfoTableName = "TAgentModelInfo"

// InitAgentModelInfoTable 初始化模型信息表
func InitAgentModelInfoTable() error {
	if database.DB == nil {
		return nil
	}
	logger.Printf("[database.DB] AutoMigrating table: %s", AgentModelInfoTableName)
	err := database.DB.Table(AgentModelInfoTableName).AutoMigrate(&TAgentModelInfo{})
	if err != nil {
		return err
	}
	logger.Printf("[database.DB] Table %s migrated successfully", AgentModelInfoTableName)
	return nil
}
