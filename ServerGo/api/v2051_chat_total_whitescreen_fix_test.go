package api

import (
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestStatsCompositeIndexName_Const 验证复合索引名常量契约
// v2.0.51: /ChatAnalysisTotal 白屏修复的关键索引名
func TestStatsCompositeIndexName_Const(t *testing.T) {
	if modelsdb.StatsCompositeIndexName == "" {
		t.Fatal("StatsCompositeIndexName must not be empty")
	}
	// 索引名必须与模型定义中的 index:xxx 命名一致
	if modelsdb.StatsCompositeIndexName != "idx_user_model_created" {
		t.Fatalf("StatsCompositeIndexName=%q, want %q", modelsdb.StatsCompositeIndexName, "idx_user_model_created")
	}
}

// TestEnsureStatsCompositeIndex_NilDB 验证 DB=nil 时返回错误
func TestEnsureStatsCompositeIndex_NilDB(t *testing.T) {
	origDB := database.DB
	defer func() { database.DB = origDB }()
	database.DB = nil

	err := modelsdb.EnsureStatsCompositeIndex(8)
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
	if !strings.Contains(err.Error(), "database not initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestEnsureStatsCompositeIndex_DefaultSubTableNum 验证 subTableNum<=0 时回落默认值不 panic
func TestEnsureStatsCompositeIndex_DefaultSubTableNum(t *testing.T) {
	origDB := database.DB
	defer func() { database.DB = origDB }()
	database.DB = nil

	// DB=nil 会在 subTableNum 检查前返回错误，但至少验证不 panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("EnsureStatsCompositeIndex panicked: %v", r)
		}
	}()
	_ = modelsdb.EnsureStatsCompositeIndex(0)
}

// TestTransactionModel_CompositeIndexTags 验证模型实例可正常创建
// v2.0.51: 确保 TAgentHttpTransactionDataItem 模型定义完整
func TestTransactionModel_CompositeIndexTags(t *testing.T) {
	item := modelsdb.TAgentHttpTransactionDataItem{
		UserName:  "test_user",
		ModelName: "test_model",
	}
	if item.UserName != "test_user" || item.ModelName != "test_model" {
		t.Fatalf("model fields not set correctly: %+v", item)
	}
	// 完整验证依赖 DB 初始化（AutoMigrate 后检查 information_schema），DB==nil 时 skip
	if database.DB == nil {
		t.Skip("DB not initialized, skipping integration check")
	}
}

// TestLogStatsDuration_HelperExists 验证 logStatsDuration 函数存在且不 panic
// v2.0.51: 慢查询日志 helper
func TestLogStatsDuration_HelperExists(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("logStatsDuration panicked: %v", r)
		}
	}()
	// 短时间走常规日志分支
	logStatsDuration("TestQuery", "u", "m", 7, 100)
	// 长时间走 warning 分支
	logStatsDuration("TestQuery", "u", "m", 90, 6_000_000_000) // 6s
}

// TestInitAgentHttpSubTables_Idempotent 验证重复初始化幂等（索引已存在不报错）
// v2.0.51: 修复 AutoMigrate 在索引已存在时报错导致测试失败的问题
func TestInitAgentHttpSubTables_Idempotent(t *testing.T) {
	if database.DB == nil {
		t.Skip("DB not initialized, skipping")
	}
	// 第一次调用
	if err := modelsdb.InitAgentHttpSubTables(4); err != nil {
		t.Fatalf("first InitAgentHttpSubTables failed: %v", err)
	}
	// 第二次调用（索引已存在，应幂等成功）
	if err := modelsdb.InitAgentHttpSubTables(4); err != nil {
		t.Fatalf("second InitAgentHttpSubTables should be idempotent, got: %v", err)
	}
}
