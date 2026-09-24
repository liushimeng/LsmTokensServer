package api

// ============================================================================
// v2.0.79 阶段CR: CleanupReportInterface list action 附带清理报告项统计契约测试
//
// 覆盖：
//   1. list 响应含 stats（状态分布/耗时/清理周期/Tokens）
//   2. list 响应含 sub_table_stats（按分表聚合）
//   3. 空库时 stats 存在且零值（不阻断主列表）
// ============================================================================

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

func TestCleanupReportHandler_ListIncludesStats(t *testing.T) {
	restore := setupCleanupSQLite_local(t)
	defer restore()
	if err := modelsdb.InitCleanupReportTable(); err != nil {
		t.Fatalf("init cleanup report table: %v", err)
	}

	cutoff := time.Date(2026, 8, 25, 3, 30, 0, 0, time.Local)
	rows := []*modelsdb.TAgentHttpTransactionCleanupReport{
		{CleanupDate: "2026-09-23", SubTableIndex: 0, SubTableName: "TAgentHttpTransactionDataItem_00", DeletedRows: 100, DeletedTokensAll: 900, DurationMs: 1200, CutoffTime: cutoff, Status: "success"},
		{CleanupDate: "2026-09-23", SubTableIndex: 1, SubTableName: "TAgentHttpTransactionDataItem_01", DeletedRows: 30, DeletedTokensAll: 200, DurationMs: 800, CutoffTime: cutoff, Status: "failed"},
	}
	for i, r := range rows {
		if err := database.DB.Table(modelsdb.CleanupReportTableName).Create(r).Error; err != nil {
			t.Fatalf("insert report %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{"action":"list","page":1,"page_size":20,"days":30}`))
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
	if resp.Stats == nil {
		t.Fatal("list 响应应包含 stats")
	}
	if resp.Stats.TotalTasks != 2 || resp.Stats.SuccessCount != 1 || resp.Stats.FailedCount != 1 {
		t.Errorf("stats 计数错误: %+v", resp.Stats)
	}
	if resp.Stats.TotalDeletedRows != 130 || resp.Stats.TotalTokensAll != 1100 {
		t.Errorf("stats 汇总错误: %+v", resp.Stats)
	}
	if resp.Stats.FirstCleanupDate != "2026-09-23" || resp.Stats.LastCleanupDate != "2026-09-23" {
		t.Errorf("stats 清理周期错误: %+v", resp.Stats)
	}
	if resp.Stats.MinCutoffTime == "" || resp.Stats.MaxCutoffTime == "" {
		t.Errorf("stats cutoff 应非空（SQLite string 规整路径）: %+v", resp.Stats)
	}
	if len(resp.SubTableStats) != 2 {
		t.Fatalf("sub_table_stats=%d, want 2", len(resp.SubTableStats))
	}
	if resp.SubTableStats[0].SubTableIndex != 0 || resp.SubTableStats[0].TaskCount != 1 ||
		resp.SubTableStats[0].DeletedRows != 100 || resp.SubTableStats[0].AvgDurationMs != 1200 {
		t.Errorf("sub_table_stats[0] 错误: %+v", resp.SubTableStats[0])
	}
	if resp.SubTableStats[1].FailedCount != 1 || resp.SubTableStats[1].DeletedTokensAll != 200 {
		t.Errorf("sub_table_stats[1] 错误: %+v", resp.SubTableStats[1])
	}

	// 主列表字段不受影响（回归守护）
	if resp.Total != 2 || len(resp.Reports) != 2 {
		t.Errorf("reports 列表应不受影响: total=%d len=%d", resp.Total, len(resp.Reports))
	}
}

func TestCleanupReportHandler_ListStatsEmptyDB(t *testing.T) {
	restore := setupCleanupSQLite_local(t)
	defer restore()
	if err := modelsdb.InitCleanupReportTable(); err != nil {
		t.Fatalf("init cleanup report table: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/CleanupReportInterface",
		strings.NewReader(`{"action":"list"}`))
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
	if resp.Stats == nil {
		t.Fatal("空库时 stats 仍应返回（零值）")
	}
	if resp.Stats.TotalTasks != 0 || len(resp.SubTableStats) != 0 {
		t.Errorf("空库 stats 应为零值: %+v", resp.Stats)
	}
}
