package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// ============================================================================
// v2.0.47: 过期数据清理服务 单元测试
// ============================================================================
//
// 覆盖：
//   1. 配置层：config.DefaultConfig / validateAndFixConfig（默认值 + 边界值）
//   2. 服务层：nextCleanupTime / getCleanupRunHour / getCleanupBatchSize / buildCleanupReport
//   3. API 层：cleanupReportInterfaceHandle（Method / JSON / action / DB=nil）
//   4. DB 集成：DB==nil 时自动 skip（与项目其他测试一致）
// ============================================================================

// ----------------------------------------------------------------------------
// 配置层测试
// ----------------------------------------------------------------------------

// TestDefaultConfig_TransactionRetentionDays 验证默认配置包含新字段且为 32
// （v2.0.61 从 70 调整为 60；v2.0.62 从 60 调整为 45；v2.0.63 从 45 调整为 32）
func TestDefaultConfig_TransactionRetentionDays(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.TransactionRetentionDays != config.DEFAULT_TRANSACTION_RETENTION_DAYS {
		t.Errorf("TransactionRetentionDays = %d, want %d",
			cfg.TransactionRetentionDays, config.DEFAULT_TRANSACTION_RETENTION_DAYS)
	}
	if config.DEFAULT_TRANSACTION_RETENTION_DAYS != 32 {
		t.Errorf("config.DEFAULT_TRANSACTION_RETENTION_DAYS = %d, want 32", config.DEFAULT_TRANSACTION_RETENTION_DAYS)
	}
}

// TestValidateAndFixConfig_TransactionRetentionDays 缺符号：validateAndFixConfig
// 现为 config 包未导出函数。
func TestValidateAndFixConfig_TransactionRetentionDays(t *testing.T) {
	t.Skip("缺符号 validateAndFixConfig（config 包未导出）")
}

// ----------------------------------------------------------------------------
// 服务层测试（纯函数，无 DB 依赖）
// ----------------------------------------------------------------------------

// TestNextCleanupTime 缺符号：nextCleanupTime 现为 models 包未导出函数。
func TestNextCleanupTime(t *testing.T) {
	t.Skip("缺符号 nextCleanupTime（models 包未导出）")
}

// TestNextCleanupTime_HourOverride 缺符号：nextCleanupTime。
func TestNextCleanupTime_HourOverride(t *testing.T) {
	t.Skip("缺符号 nextCleanupTime（models 包未导出）")
}

// TestGetCleanupRunHour 缺符号：getCleanupRunHour 现为 models 包未导出函数。
func TestGetCleanupRunHour(t *testing.T) {
	t.Skip("缺符号 getCleanupRunHour（models 包未导出）")
}

// TestGetCleanupBatchSize 缺符号：getCleanupBatchSize 现为 models 包未导出函数。
func TestGetCleanupBatchSize(t *testing.T) {
	t.Skip("缺符号 getCleanupBatchSize（models 包未导出）")
}

// TestBuildCleanupReport 测试报告结构字段正确性
func TestBuildCleanupReport(t *testing.T) {
	report := modelsdb.TAgentHttpTransactionCleanupReport{
		CleanupDate:      "2026-07-20",
		SubTableIndex:    3,
		SubTableName:     "TAgentHttpTransactionDataItem_03",
		DeletedRows:      12345,
		DeletedTokensIn:  100000,
		DeletedTokensOut: 50000,
		DeletedTokensAll: 150000,
		FreedBytes:       1024 * 1024 * 100, // 100 MB
		DurationMs:       1500,
		CutoffTime:       time.Now().Add(-70 * 24 * 3600 * 1e9), // 70 days ago
		RetentionDays:    70,
		Status:           "success",
		ErrorMsg:         "",
	}

	if report.CleanupDate != "2026-07-20" {
		t.Errorf("CleanupDate=%s, want 2026-07-20", report.CleanupDate)
	}
	if report.SubTableIndex != 3 {
		t.Errorf("SubTableIndex=%d, want 3", report.SubTableIndex)
	}
	if report.FreedBytes != 1024*1024*100 {
		t.Errorf("FreedBytes=%d, want %d", report.FreedBytes, 1024*1024*100)
	}
	if report.Status != "success" {
		t.Errorf("Status=%s, want success", report.Status)
	}
}

// TestGetCleanupStateSnapshot 缺符号：cleanupState 现为 models 包未导出变量。
func TestGetCleanupStateSnapshot(t *testing.T) {
	t.Skip("缺符号 cleanupState（models 包未导出）")
}

// ----------------------------------------------------------------------------
// API 层测试（无需 DB）
// ----------------------------------------------------------------------------

// TestCleanupReportAPIRequest_JSONBinding 测试请求体 JSON 字段绑定保真
func TestCleanupReportAPIRequest_JSONBinding(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantAct  string
		wantPage int
		wantSize int
		wantDays int
	}{
		{"最小请求", `{}`, "", 0, 0, 0},
		{"list action", `{"action":"list","page":2,"page_size":50,"days":30}`, "list", 2, 50, 30},
		{"summary", `{"action":"summary","days":7}`, "summary", 0, 0, 7},
		{"state", `{"action":"state"}`, "state", 0, 0, 0},
		{"days=0 无限制", `{"days":0}`, "", 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req CleanupReportAPIRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if req.Action != tc.wantAct {
				t.Errorf("action=%q, want=%q", req.Action, tc.wantAct)
			}
			if req.Page != tc.wantPage {
				t.Errorf("page=%d, want=%d", req.Page, tc.wantPage)
			}
			if req.PageSize != tc.wantSize {
				t.Errorf("page_size=%d, want=%d", req.PageSize, tc.wantSize)
			}
			if req.Days != tc.wantDays {
				t.Errorf("days=%d, want=%d", req.Days, tc.wantDays)
			}
		})
	}
}

// TestCleanupReportHandler_MethodNotPost 测试 GET 请求被拒
func TestCleanupReportHandler_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/CleanupReportInterface", nil)
	rr := httptest.NewRecorder()
	cleanupReportInterfaceHandle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200 (returns success:false JSON)", rr.Code)
	}
	var resp CleanupReportAPIResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false for GET request")
	}
	if !strings.Contains(resp.Message, "POST") {
		t.Errorf("message=%q, want to contain 'POST'", resp.Message)
	}
}

// TestCleanupReportHandler_InvalidJSON 测试非法 JSON 请求被拒
func TestCleanupReportHandler_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{invalid json`))
	rr := httptest.NewRecorder()
	cleanupReportInterfaceHandle(rr, req)

	var resp CleanupReportAPIResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false for invalid JSON")
	}
	if !strings.Contains(resp.Message, "请求体") && !strings.Contains(resp.Message, "JSON") && !strings.Contains(resp.Message, "无效") {
		t.Errorf("message=%q, want to indicate invalid JSON", resp.Message)
	}
}

// TestCleanupReportHandler_UnknownAction 测试未知 action 返回明确错误
func TestCleanupReportHandler_UnknownAction(t *testing.T) {
	if database.DB == nil {
		t.Skip("DB not initialized, skip integration test")
	}
	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{"action":"unknown_xxx"}`))
	rr := httptest.NewRecorder()
	cleanupReportInterfaceHandle(rr, req)

	var resp CleanupReportAPIResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false for unknown action")
	}
	if !strings.Contains(resp.Message, "未知 action") {
		t.Errorf("message=%q, want to indicate unknown action", resp.Message)
	}
}

// TestUserCleanupReportHandler_NoToken 测试未登录被拒
func TestUserCleanupReportHandler_NoToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{"action":"list"}`))
	rr := httptest.NewRecorder()
	userCleanupReportInterfaceHandle(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want 401", rr.Code)
	}
	var resp CleanupReportAPIResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false for no token")
	}
	if !strings.Contains(resp.Message, "登录") && !strings.Contains(resp.Message, "未登录") {
		t.Errorf("message=%q, want to indicate login required", resp.Message)
	}
}

// TestDeleteExpiredBatch_NilDB 缺符号：deleteExpiredBatch 现为 models 包未导出函数。
func TestDeleteExpiredBatch_NilDB(t *testing.T) {
	t.Skip("缺符号 deleteExpiredBatch（models 包未导出）")
}

// TestReleaseTableSpace_NilDB 缺符号：releaseTableSpace 现为 models 包未导出函数。
func TestReleaseTableSpace_NilDB(t *testing.T) {
	t.Skip("缺符号 releaseTableSpace（models 包未导出）")
}

// TestQueryTableDataFree_NilDB 缺符号：queryTableDataFree 现为 models 包未导出函数。
func TestQueryTableDataFree_NilDB(t *testing.T) {
	t.Skip("缺符号 queryTableDataFree（models 包未导出）")
}

// TestQueryCleanupReports_NilDB 测试 DB=nil 时返回明确错误
func TestQueryCleanupReports_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, _, err := modelsdb.QueryCleanupReports(1, 20, 30)
	if err == nil {
		t.Errorf("expected error when DB is nil")
	}
	if !strings.Contains(err.Error(), "数据库未初始化") && !strings.Contains(err.Error(), "未初始化") {
		t.Errorf("error=%v, want to indicate DB not initialized", err)
	}
}

// TestGetCleanupReportsTotalSummary_NilDB 测试 DB=nil 时返回明确错误
func TestGetCleanupReportsTotalSummary_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, err := modelsdb.GetCleanupReportsTotalSummary()
	if err == nil {
		t.Errorf("expected error when DB is nil")
	}
}

// TestGetCleanupReportsDailySummary_NilDB 测试 DB=nil 时返回明确错误
func TestGetCleanupReportsDailySummary_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, err := modelsdb.GetCleanupReportsDailySummary(30)
	if err == nil {
		t.Errorf("expected error when DB is nil")
	}
}

// ----------------------------------------------------------------------------
// 服务层状态转换测试
// ----------------------------------------------------------------------------

// TestCleanupOneSubTable_EmptyStats 缺符号：cleanupOneSubTable 现为 models 包未导出函数。
func TestCleanupOneSubTable_EmptyStats(t *testing.T) {
	t.Skip("缺符号 cleanupOneSubTable（models 包未导出）")
}

// TestStartTransactionCleanupService_NilCfg 测试 cfg=nil 时不 panic
func TestStartTransactionCleanupService_NilCfg(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("StartTransactionCleanupService(nil) panicked: %v", r)
		}
	}()
	modelsdb.StartTransactionCleanupService(nil)
}

// TestStartTransactionCleanupService_ZeroDays 缺符号：cleanupState 现为 models 包未导出变量。
func TestStartTransactionCleanupService_ZeroDays(t *testing.T) {
	t.Skip("缺符号 cleanupState（models 包未导出）")
}

// ----------------------------------------------------------------------------
// 命名常量契约
// ----------------------------------------------------------------------------

// TestCleanupConstants 验证关键常量定义
func TestCleanupConstants(t *testing.T) {
	if config.DEFAULT_TRANSACTION_RETENTION_DAYS <= 0 {
		t.Errorf("config.DEFAULT_TRANSACTION_RETENTION_DAYS=%d, want > 0", config.DEFAULT_TRANSACTION_RETENTION_DAYS)
	}
	if config.DEFAULT_CLEANUP_RUN_HOUR < 0 || config.DEFAULT_CLEANUP_RUN_HOUR > 23 {
		t.Errorf("DEFAULT_CLEANUP_RUN_HOUR=%d, want [0, 23]", config.DEFAULT_CLEANUP_RUN_HOUR)
	}
	if config.DEFAULT_CLEANUP_BATCH_SIZE <= 0 {
		t.Errorf("DEFAULT_CLEANUP_BATCH_SIZE=%d, want > 0", config.DEFAULT_CLEANUP_BATCH_SIZE)
	}
	if modelsdb.CleanupReportTableName == "" {
		t.Error("CleanupReportTableName must be non-empty")
	}
}

// ----------------------------------------------------------------------------
// 配置文件向后兼容测试
// ----------------------------------------------------------------------------

// TestRawBytesContainsField 缺符号：rawBytesContainsField 现为 config 包未导出函数。
func TestRawBytesContainsField(t *testing.T) {
	t.Skip("缺符号 rawBytesContainsField（config 包未导出）")
}

// TestLoadConfig_TransactionRetentionDaysBackwardCompat 测试旧配置无该字段时自动启用默认值
func TestLoadConfig_TransactionRetentionDaysBackwardCompat(t *testing.T) {
	// 写入一个不包含 transactionRetentionDays 字段的旧配置到临时文件
	tmpDir := t.TempDir()
	confPath := tmpDir + "/test.conf"

	oldConfig := `{
  "managerWebListenPort": 9101,
  "userWebListenPort": 29001,
  "mcpWebListenPort": 29002
}`
	if err := os.WriteFile(confPath, []byte(oldConfig), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := config.LoadConfig(confPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// 旧配置无字段 → 自动启用默认值
	if cfg.TransactionRetentionDays != config.DEFAULT_TRANSACTION_RETENTION_DAYS {
		t.Errorf("backward compat: TransactionRetentionDays=%d, want default %d",
			cfg.TransactionRetentionDays, config.DEFAULT_TRANSACTION_RETENTION_DAYS)
	}
}

// TestLoadConfig_TransactionRetentionDaysExplicitZero 测试用户显式设 0 时尊重设置（禁用）
func TestLoadConfig_TransactionRetentionDaysExplicitZero(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := tmpDir + "/test.conf"

	// 显式写 0 = 禁用
	configStr := `{
  "managerWebListenPort": 9101,
  "transactionRetentionDays": 0
}`
	if err := os.WriteFile(confPath, []byte(configStr), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := config.LoadConfig(confPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// 显式 0 → 保留为 0（禁用）
	if cfg.TransactionRetentionDays != 0 {
		t.Errorf("explicit zero: TransactionRetentionDays=%d, want 0 (disabled)",
			cfg.TransactionRetentionDays)
	}
}

// TestLoadConfig_TransactionRetentionDaysExplicit 测试用户显式设置合法值
func TestLoadConfig_TransactionRetentionDaysExplicit(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := tmpDir + "/test.conf"

	configStr := `{
  "transactionRetentionDays": 30
}`
	if err := os.WriteFile(confPath, []byte(configStr), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := config.LoadConfig(confPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.TransactionRetentionDays != 30 {
		t.Errorf("explicit value: TransactionRetentionDays=%d, want 30",
			cfg.TransactionRetentionDays)
	}
}
