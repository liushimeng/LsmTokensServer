package models

// ============================================================================
// v2.0.79 阶段CR: GetCleanupReportsStats 清理报告项统计测试（SQLite 内存库）
//
// 覆盖：
//   1. DB=nil 显式报错
//   2. 空表零值安全（COALESCE 无 NULL 泄漏）
//   3. 多分表 × 多状态聚合：状态分布 / 耗时统计 / 清理周期 / Tokens / 分表聚合
//   4. cutoff_time 字符串规整（SQLite 驱动返回 string 的路径）
// ============================================================================

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

func makeCleanupReport(subTableIndex int, status string, deletedRows int64, tokensAll uint64, durationMs int64, cleanupDate string, cutoff time.Time) *TAgentHttpTransactionCleanupReport {
	return &TAgentHttpTransactionCleanupReport{
		CleanupDate:      cleanupDate,
		SubTableIndex:    subTableIndex,
		SubTableName:     fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", subTableIndex),
		DeletedRows:      deletedRows,
		DeletedTokensIn:  tokensAll / 3,
		DeletedTokensOut: tokensAll / 3,
		DeletedTokensAll: tokensAll,
		DurationMs:       durationMs,
		CutoffTime:       cutoff,
		RetentionDays:    30,
		Status:           status,
	}
}

func TestGetCleanupReportsStats_NilDB(t *testing.T) {
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	_, _, err := GetCleanupReportsStats()
	if err == nil {
		t.Fatal("DB=nil 应返回错误")
	}
	if !strings.Contains(err.Error(), "数据库未初始化") {
		t.Errorf("错误信息应说明数据库未初始化: %v", err)
	}
}

func TestGetCleanupReportsStats_EmptyTable(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()
	if err := InitCleanupReportTable(); err != nil {
		t.Fatalf("init cleanup report table: %v", err)
	}

	stats, subStats, err := GetCleanupReportsStats()
	if err != nil {
		t.Fatalf("GetCleanupReportsStats: %v", err)
	}
	if stats.TotalTasks != 0 || stats.SuccessCount != 0 || stats.FailedCount != 0 || stats.PartialCount != 0 {
		t.Errorf("空表计数应为 0: %+v", stats)
	}
	if stats.TotalDeletedRows != 0 || stats.TotalTokensAll != 0 || stats.TotalDurationMs != 0 {
		t.Errorf("空表汇总应为 0: %+v", stats)
	}
	if stats.FirstCleanupDate != "" || stats.LastCleanupDate != "" || stats.MinCutoffTime != "" || stats.MaxCutoffTime != "" {
		t.Errorf("空表时间字段应为空串: %+v", stats)
	}
	if len(subStats) != 0 {
		t.Errorf("空表分表聚合应为空: %+v", subStats)
	}
}

func TestGetCleanupReportsStats_Aggregates(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()
	if err := InitCleanupReportTable(); err != nil {
		t.Fatalf("init cleanup report table: %v", err)
	}

	cutoff1 := time.Date(2026, 8, 25, 3, 30, 0, 0, time.Local)
	cutoff2 := time.Date(2026, 9, 20, 3, 30, 0, 0, time.Local)
	// 报告表按 (cleanup_date, sub_table_index) 唯一 —— 同一天每分表最多一行
	rows := []*TAgentHttpTransactionCleanupReport{
		makeCleanupReport(0, "success", 100, 900, 1200, "2026-09-23", cutoff1),
		makeCleanupReport(1, "failed", 50, 300, 800, "2026-09-23", cutoff1),
		makeCleanupReport(0, "success", 200, 1800, 2400, "2026-09-24", cutoff2),
		makeCleanupReport(1, "partial", 20, 150, 600, "2026-09-24", cutoff2),
	}
	for i, r := range rows {
		if err := database.DB.Table(CleanupReportTableName).Create(r).Error; err != nil {
			t.Fatalf("insert report %d: %v", i, err)
		}
	}

	stats, subStats, err := GetCleanupReportsStats()
	if err != nil {
		t.Fatalf("GetCleanupReportsStats: %v", err)
	}

	// 状态分布
	if stats.TotalTasks != 4 {
		t.Errorf("TotalTasks=%d, want 4", stats.TotalTasks)
	}
	if stats.SuccessCount != 2 || stats.PartialCount != 1 || stats.FailedCount != 1 {
		t.Errorf("状态分布=%d/%d/%d, want 2/1/1", stats.SuccessCount, stats.PartialCount, stats.FailedCount)
	}

	// 删除量与 Tokens
	if stats.TotalDeletedRows != 370 {
		t.Errorf("TotalDeletedRows=%d, want 370", stats.TotalDeletedRows)
	}
	if stats.TotalTokensAll != 3150 {
		t.Errorf("TotalTokensAll=%d, want 3150", stats.TotalTokensAll)
	}

	// 耗时统计：total=5000 avg=1250 max=2400
	if stats.TotalDurationMs != 5000 || stats.MaxDurationMs != 2400 {
		t.Errorf("耗时=%d/%d, want 5000/2400", stats.TotalDurationMs, stats.MaxDurationMs)
	}
	if stats.AvgDurationMs < 1249 || stats.AvgDurationMs > 1251 {
		t.Errorf("AvgDurationMs=%d, want ≈1250", stats.AvgDurationMs)
	}

	// 清理周期
	if stats.FirstCleanupDate != "2026-09-23" || stats.LastCleanupDate != "2026-09-24" {
		t.Errorf("清理周期=%q~%q, want 2026-09-23~2026-09-24", stats.FirstCleanupDate, stats.LastCleanupDate)
	}

	// cutoff 规整为 "YYYY-MM-DD HH:MM:SS"（SQLite string 路径）
	if !strings.HasPrefix(stats.MinCutoffTime, "2026-08-25 03:30:00") {
		t.Errorf("MinCutoffTime=%q, want 前缀 2026-08-25 03:30:00", stats.MinCutoffTime)
	}
	if !strings.HasPrefix(stats.MaxCutoffTime, "2026-09-20 03:30:00") {
		t.Errorf("MaxCutoffTime=%q, want 前缀 2026-09-20 03:30:00", stats.MaxCutoffTime)
	}

	// 分表聚合
	if len(subStats) != 2 {
		t.Fatalf("分表聚合数=%d, want 2", len(subStats))
	}
	s0, s1 := subStats[0], subStats[1]
	if s0.SubTableIndex != 0 || s1.SubTableIndex != 1 {
		t.Errorf("分表索引=%d/%d, want 0/1", s0.SubTableIndex, s1.SubTableIndex)
	}
	if s0.TaskCount != 2 || s0.DeletedRows != 300 || s0.DeletedTokensAll != 2700 || s0.SuccessCount != 2 {
		t.Errorf("分表0 聚合错误: %+v", s0)
	}
	if s0.AvgDurationMs != 1800 || s0.LastCleanupDate != "2026-09-24" {
		t.Errorf("分表0 耗时/最近清理错误: %+v", s0)
	}
	if s1.TaskCount != 2 || s1.DeletedRows != 70 || s1.DeletedTokensAll != 450 || s1.FailedCount != 1 || s1.PartialCount != 1 {
		t.Errorf("分表1 聚合错误: %+v", s1)
	}
	if s1.AvgDurationMs != 700 || s1.LastCleanupDate != "2026-09-24" {
		t.Errorf("分表1 耗时/最近清理错误: %+v", s1)
	}
}
