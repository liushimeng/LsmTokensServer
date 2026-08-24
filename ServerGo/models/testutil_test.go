package models

import (
	"log"
	"os"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
)

// testCfg 测试用配置（替代旧工程 package main 的全局 cfg）
var testCfg *config.LsmTokensServerConfig

// initTestEnv 初始化测试环境（SQLite 内存数据库 + 日志 + 配置）
// （自旧工程 test_api_proxy_test.go 迁移的公共测试基建）
func initTestEnv(t *testing.T) func() {
	oldSkipBackfill, hadSkipBackfill := os.LookupEnv("LSM_SKIP_TASK_FEATURE_BACKFILL")
	os.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", "1")

	// 关闭用户信息日志写入：测试期间所有 fixture 操作不再污染 LsmTokensServerUsersInfo.log
	oldDisabled := logger.IsUserLogDisabled()
	logger.SetDisableUserLog(true)

	// 初始化配置
	testCfg = config.DefaultConfig()
	testCfg.DBMysqlSubTableNumber = 4 // 测试用较小分表数

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("[TEST] ")

	// 创建内存 SQLite 数据库
	sqliteDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	database.DB = sqliteDB

	// 初始化所有表
	if err := InitAgentHttpUserInfoTable(); err != nil {
		t.Fatalf("failed to init user info table: %v", err)
	}
	if err := InitAgentHttpUserModelInfoTable(); err != nil {
		t.Fatalf("failed to init user model table: %v", err)
	}
	if err := InitAgentDstEndPointTable(); err != nil {
		t.Fatalf("failed to init dst endpoint table: %v", err)
	}
	if err := InitAgentHttpAIRouteTable(); err != nil {
		t.Fatalf("failed to init ai route table: %v", err)
	}
	if err := InitAgentHttpSubTables(testCfg.DBMysqlSubTableNumber); err != nil {
		t.Fatalf("failed to init sub tables: %v", err)
	}
	if err := LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("failed to load cache: %v", err)
	}

	// 返回清理函数
	return func() {
		sqlDB, _ := database.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = nil
		testCfg = nil
		logger.SetDisableUserLog(oldDisabled)
		if hadSkipBackfill {
			os.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", oldSkipBackfill)
		} else {
			os.Unsetenv("LSM_SKIP_TASK_FEATURE_BACKFILL")
		}
	}
}
