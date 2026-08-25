package models

// v2.0.74 首次运行配置自动回写与超级管理员禁用机制（阶段AL）
//
// 用于 main.go 启动后探测 "业务侧是否已接管系统"：
//   - 若返回 0 → 数据库无业务用户，超级管理员随机生成并保持可用；
//   - 若返回 ≥1 → 数据库已有用户，调用 config.DisableSuperAdmin 禁用管理端超级管理员。
//
// 实现走 GORM 软删除过滤 + 限 1 行 Count，避免大表全表扫描。

import (
	"fmt"

	"github.com/lishimeng/LsmTokensServer/database"
)

// CountActiveUsers 统计 TAgentHttpUserInfo 表中未软删除的用户数量（limit=1，避免大表扫描）。
// 返回值：
//   - 0 表示尚未被业务接管；
//   - ≥1 表示已有业务用户，主进程应当禁用超级管理员。
func CountActiveUsers() (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	var count int64
	row := database.DB.Table(AgentHttpUserInfoTableName).
		Where("deleted_at IS NULL").
		Limit(1).
		Count(&count)
	if row.Error != nil {
		return 0, fmt.Errorf("count users failed: %w", row.Error)
	}
	return count, nil
}