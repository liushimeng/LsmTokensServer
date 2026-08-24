package api

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
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// initTestEnv 初始化 API 层测试环境（与 models/testutil_test.go 同构）
func initTestEnv(t *testing.T) func() {
	oldSkipBackfill, hadSkipBackfill := os.LookupEnv("LSM_SKIP_TASK_FEATURE_BACKFILL")
	os.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", "1")

	oldDisabled := logger.IsUserLogDisabled()
	logger.SetDisableUserLog(true)

	config.G = config.DefaultConfig()
	config.G.DBMysqlSubTableNumber = 4

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("[TEST] ")

	sqliteDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	database.DB = sqliteDB

	for _, init := range []func() error{
		modelsdb.InitAgentHttpUserInfoTable,
		modelsdb.InitAgentHttpUserModelInfoTable,
		modelsdb.InitAgentDstEndPointTable,
		modelsdb.InitAgentHttpAIRouteTable,
	} {
		if err := init(); err != nil {
			t.Fatalf("failed to init table: %v", err)
		}
	}
	if err := modelsdb.InitAgentHttpSubTables(config.G.DBMysqlSubTableNumber); err != nil {
		t.Fatalf("failed to init sub tables: %v", err)
	}
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("failed to load cache: %v", err)
	}

	return func() {
		sqlDB, _ := database.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = nil
		config.G = nil
		logger.SetDisableUserLog(oldDisabled)
		if hadSkipBackfill {
			os.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", oldSkipBackfill)
		} else {
			os.Unsetenv("LSM_SKIP_TASK_FEATURE_BACKFILL")
		}
	}
}
