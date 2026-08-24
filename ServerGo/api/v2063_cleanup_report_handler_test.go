package api

// v2.0.63: cleanupReportInterfaceHandle tables action 测试。
// 原 v2063_subtable_inspector_test.go 中测试清理报告 handler「tables」白名单校验的部分。
// setupCleanupSQLite/makeCleanupTxn（原 models 包测试 helper）在本文件内独立实现最小版本，
// inspector 缓存失效走导出的 models.InvalidateSubTableInspector。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// setupCleanupSQLite_local 建立隔离的内存 SQLite + 分表（local 版本，原 models 测试 helper 不导出）。
func setupCleanupSQLite_local(t *testing.T) func() {
	t.Helper()
	origDB := database.DB
	origCfg := config.G

	t.Setenv("LSM_SKIP_BACKGROUND_BACKFILL", "1")

	config.G = config.DefaultConfig()
	config.G.DBMysqlSubTableNumber = 8

	dbName := "v2063_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sqliteDB, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, e := sqliteDB.DB(); e == nil && sqlDB != nil {
		sqlDB.SetMaxOpenConns(1)
	}
	database.DB = sqliteDB
	if err := modelsdb.InitAgentHttpSubTables(config.G.DBMysqlSubTableNumber); err != nil {
		t.Fatalf("init sub tables: %v", err)
	}
	modelsdb.InvalidateSubTableInspector()
	return func() {
		if sqlDB, _ := database.DB.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = origDB
		config.G = origCfg
	}
}

// makeCleanupTxn_local 构造一条交易记录（local 版本）。
func makeCleanupTxn_local(ageDays int, tokensIn, tokensOut uint64) *modelsdb.TAgentHttpTransactionDataItem {
	return &modelsdb.TAgentHttpTransactionDataItem{
		UserName:         "cleanup_user",
		ModelName:        "cleanup_model",
		TokensInputSize:  tokensIn,
		TokensOutputSize: tokensOut,
		TokensAllSize:    tokensIn + tokensOut,
		CreatedAt:        time.Now().AddDate(0, 0, -ageDays),
	}
}

func TestCleanupReportHandler_TablesAction(t *testing.T) {
	restore := setupCleanupSQLite_local(t)
	defer restore()
	modelsdb.InvalidateSubTableInspector()

	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{"action":"tables"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	cleanupReportInterfaceHandle(w, req)

	var resp CleanupReportAPIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if !resp.Success {
		t.Fatalf("success=false: %s", resp.Message)
	}
	if len(resp.Tables) != 8 {
		t.Errorf("tables=%d, want 8", len(resp.Tables))
	}
}

// TestCleanupReportHandler_TablesExactCount tables+table 精确计数回显
func TestCleanupReportHandler_TablesExactCount(t *testing.T) {
	restore := setupCleanupSQLite_local(t)
	defer restore()
	modelsdb.InvalidateSubTableInspector()

	rows := make([]*modelsdb.TAgentHttpTransactionDataItem, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, makeCleanupTxn_local(1, 1, 1))
	}
	if err := database.DB.Table("TAgentHttpTransactionDataItem_02").Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{"action":"tables","table":"TAgentHttpTransactionDataItem_02"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	cleanupReportInterfaceHandle(w, req)

	var resp CleanupReportAPIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success=false: %s", resp.Message)
	}
	if resp.ExactTable != "TAgentHttpTransactionDataItem_02" {
		t.Errorf("exact_table=%q", resp.ExactTable)
	}
	if resp.ExactRowCount != 5 {
		t.Errorf("exact_row_count=%d, want 5", resp.ExactRowCount)
	}
}

// TestCleanupReportHandler_TablesRejectsUnknownTable 白名单表名校验（防任意表名探测）
func TestCleanupReportHandler_TablesRejectsUnknownTable(t *testing.T) {
	restore := setupCleanupSQLite_local(t)
	defer restore()
	modelsdb.InvalidateSubTableInspector()

	for _, bad := range []string{
		"users", "TAgentHttpAIRoute", "TAgentHttpTransactionDataItem",
		"TAgentHttpTransactionDataItem_0", "TAgentHttpTransactionDataItem_99",
		"TAgentHttpTransactionDataItem_00; DROP TABLE users",
	} {
		req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
			strings.NewReader(`{"action":"tables","table":`+jsonString(bad)+`}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		cleanupReportInterfaceHandle(w, req)

		var resp CleanupReportAPIResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Success {
			t.Errorf("table=%q 应被拒绝但 success=true", bad)
		}
		if resp.ExactRowCount != 0 {
			t.Errorf("table=%q 被拒时不应返回精确行数", bad)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}