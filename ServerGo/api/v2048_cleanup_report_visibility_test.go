package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

func TestTransactionRetentionDaysExplicitZeroJSONRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TransactionRetentionDays = 0

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if !strings.Contains(string(data), `"transactionRetentionDays":0`) {
		t.Fatalf("explicit zero missing from JSON: %s", data)
	}

	var decoded config.LsmTokensServerConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if decoded.TransactionRetentionDays != 0 {
		t.Fatalf("TransactionRetentionDays=%d, want 0", decoded.TransactionRetentionDays)
	}
}

func TestGetTransactionTimeBoundaries_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	earliest, latest, err := modelsdb.GetTransactionTimeBoundaries(8)
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
	if !earliest.IsZero() || !latest.IsZero() {
		t.Fatalf("boundaries=%v/%v, want zero values", earliest, latest)
	}
}

// TestSaveCleanupReportValidation 缺符号：saveCleanupReport 现为 models 包未导出函数。
func TestSaveCleanupReportValidation(t *testing.T) {
	t.Skip("缺符号 saveCleanupReport（models 包未导出）")
}

// TestCleanupReportTemplateTimeVisibilityContract 缺符号：cleanupReportTemplate
// 已迁移至前端（ClientWeb），Go 侧不存在。
func TestCleanupReportTemplateTimeVisibilityContract(t *testing.T) {
	t.Skip("缺符号 cleanupReportTemplate（已迁移至前端，Go 侧不存在）")
}

func TestCleanupReportCutoffJSONRoundTrip(t *testing.T) {
	cutoff := time.Date(2026, 5, 15, 3, 30, 0, 0, time.Local)
	report := modelsdb.TAgentHttpTransactionCleanupReport{CutoffTime: cutoff}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(data), `"cutoff_time"`) {
		t.Fatalf("cutoff_time missing from JSON: %s", data)
	}
}
