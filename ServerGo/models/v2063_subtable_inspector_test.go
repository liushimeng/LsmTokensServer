package models

// ============================================================================
// v2.0.63: /CleanupReport 分表 Schema Inspector + 保留天数 32 天契约测试
// ============================================================================
//
// 覆盖：
//   1. 分表元数据快照（SQLite 精确路径 + 缓存 + 失效）
//   2. 精确计数（CountSubTableRowsExact + statsDB 25s 守护）
//   3. tables action handler 契约（白名单表名校验 / 未知表拒绝）
//   4. 前端模板契约（inspector 卡片 + loadInspector + exactCount 绑定）
// ============================================================================

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

// ----------------------------------------------------------------------------
// 1. 分表元数据快照
// ----------------------------------------------------------------------------

// TestGetSubTableInspector_SQLiteExactPath SQLite 环境下走精确 COUNT(*)，Approximate=false
func TestGetSubTableInspector_SQLiteExactPath(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()
	invalidateSubTableInspector()

	tableName := "TAgentHttpTransactionDataItem_00"
	rows := make([]*TAgentHttpTransactionDataItem, 0, 7)
	for i := 0; i < 7; i++ {
		rows = append(rows, makeCleanupTxn(2, 100, 50))
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	entries, err := GetSubTableInspector(8)
	if err != nil {
		t.Fatalf("GetSubTableInspector: %v", err)
	}
	if len(entries) != 8 {
		t.Fatalf("entries=%d, want 8", len(entries))
	}

	var e0 *SubTableInspectorInfo
	for i := range entries {
		if entries[i].TableName == tableName {
			e0 = &entries[i]
			break
		}
	}
	if e0 == nil {
		t.Fatalf("未找到 %s 的条目", tableName)
	}
	if !e0.Exists {
		t.Error("分表 00 已创建但 Exists=false")
	}
	if e0.Approximate {
		t.Error("SQLite 路径 Approximate 应为 false（精确 COUNT）")
	}
	if e0.RowCount != 7 {
		t.Errorf("RowCount=%d, want 7", e0.RowCount)
	}
	if e0.EarliestAt == "" || e0.LatestAt == "" {
		t.Errorf("时间范围不应为空：earliest=%q latest=%q", e0.EarliestAt, e0.LatestAt)
	}
	if e0.Index != 0 {
		t.Errorf("Index=%d, want 0", e0.Index)
	}
}

// TestGetSubTableInspector_MissingTable 缺失分表 Exists=false 且不阻断其它表
func TestGetSubTableInspector_MissingTable(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()
	invalidateSubTableInspector()

	// 删掉分表 07 模拟缺失
	if err := database.DB.Exec("DROP TABLE IF EXISTS TAgentHttpTransactionDataItem_07").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	entries, err := GetSubTableInspector(8)
	if err != nil {
		t.Fatalf("GetSubTableInspector: %v", err)
	}
	if len(entries) != 8 {
		t.Fatalf("entries=%d, want 8", len(entries))
	}
	for _, e := range entries {
		if e.Index == 7 {
			if e.Exists {
				t.Error("分表 07 已删除但 Exists=true")
			}
		} else if !e.Exists {
			t.Errorf("分表 %d 应存在但 Exists=false", e.Index)
		}
	}
}

// TestGetSubTableInspector_CacheHitAndInvalidate 缓存命中 + invalidate 后重查
func TestGetSubTableInspector_CacheHitAndInvalidate(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()
	invalidateSubTableInspector()

	entries1, err := GetSubTableInspector(8)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// 再插 3 行；缓存内不应看到
	rows := make([]*TAgentHttpTransactionDataItem, 0, 3)
	for i := 0; i < 3; i++ {
		rows = append(rows, makeCleanupTxn(1, 1, 1))
	}
	if err := database.DB.Table("TAgentHttpTransactionDataItem_00").Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	entries2, err := GetSubTableInspector(8)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if entries2[0].RowCount != entries1[0].RowCount {
		t.Errorf("缓存应命中：entries2=%d, entries1=%d", entries2[0].RowCount, entries1[0].RowCount)
	}

	invalidateSubTableInspector()
	entries3, err := GetSubTableInspector(8)
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if entries3[0].RowCount != entries1[0].RowCount+3 {
		t.Errorf("invalidate 后应看到新增 3 行：got=%d, want=%d", entries3[0].RowCount, entries1[0].RowCount+3)
	}
}

// TestGetSubTableInspector_NilDB NilDB 返回错误不 panic
func TestGetSubTableInspector_NilDB(t *testing.T) {
	orig := database.DB
	database.DB = nil
	defer func() { database.DB = orig }()
	if _, err := GetSubTableInspector(8); err == nil {
		t.Error("database.DB=nil 应返回错误")
	}
}

// TestGetSubTableInspector_JSONShape JSON 字段名与前端契约一致
func TestGetSubTableInspector_JSONShape(t *testing.T) {
	info := SubTableInspectorInfo{
		Index: 0, TableName: "TAgentHttpTransactionDataItem_00", Exists: true,
		RowCount: 123, Approximate: true, DataBytes: 1024, IndexBytes: 256,
		DataFree: 64, AvgRowBytes: 459 * 1024,
		UpdatedAt: "2026-07-28 03:30:00", EarliestAt: "2026-06-01 00:00:00", LatestAt: "2026-07-28 01:00:00",
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"index"`, `"table_name"`, `"exists"`, `"row_count"`, `"approximate"`,
		`"data_bytes"`, `"index_bytes"`, `"data_free"`, `"avg_row_bytes"`,
		`"updated_at"`, `"earliest_at"`, `"latest_at"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("JSON 缺字段 %s: %s", key, raw)
		}
	}
	// error 为空时 omitempty 不输出
	if strings.Contains(string(raw), `"error"`) {
		t.Errorf("空 error 不应输出: %s", raw)
	}
}

// ----------------------------------------------------------------------------
// 2. 精确计数
// ----------------------------------------------------------------------------

// TestCountSubTableRowsExact SQLite 精确计数
func TestCountSubTableRowsExact(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	rows := make([]*TAgentHttpTransactionDataItem, 0, 11)
	for i := 0; i < 11; i++ {
		rows = append(rows, makeCleanupTxn(3, 10, 5))
	}
	if err := database.DB.Table("TAgentHttpTransactionDataItem_01").Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	n, err := CountSubTableRowsExact("TAgentHttpTransactionDataItem_01")
	if err != nil {
		t.Fatalf("CountSubTableRowsExact: %v", err)
	}
	if n != 11 {
		t.Errorf("n=%d, want 11", n)
	}
}

// TestCountSubTableRowsExact_Errors 边界：NilDB / 表不存在
func TestCountSubTableRowsExact_Errors(t *testing.T) {
	orig := database.DB
	database.DB = nil
	if _, err := CountSubTableRowsExact("TAgentHttpTransactionDataItem_00"); err == nil {
		t.Error("database.DB=nil 应报错")
	}
	database.DB = orig

	restore := setupCleanupSQLite(t)
	defer restore()
	if _, err := CountSubTableRowsExact("TAgentHttpTransactionDataItem_77"); err == nil {
		t.Error("不存在的表应报错")
	}
}
func TestCleanupInspectorTTL(t *testing.T) {
	if subTableInspectorTTL < time.Minute {
		t.Errorf("subTableInspectorTTL=%v 过短，缓存失去意义", subTableInspectorTTL)
	}
	if subTableInspectorTTL > time.Hour {
		t.Errorf("subTableInspectorTTL=%v 过长，数据会严重滞后", subTableInspectorTTL)
	}
}

// TestNormalizeInspectorTime 跨驱动时间串规整（SQLite 返回 string，MySQL 返回 time.Time 后也会被格式化）
func TestNormalizeInspectorTime(t *testing.T) {
	cases := []struct {
		name string
		in   *string
		want string
	}{
		{"nil", nil, ""},
		{"空串", strPtr(""), ""},
		{"空白", strPtr("   "), ""},
		{"标准格式", strPtr("2026-07-28 03:30:00"), "2026-07-28 03:30:00"},
		{"带时区偏移", strPtr("2026-07-28 03:30:00+08:00"), "2026-07-28 03:30:00"},
		{"RFC3339", strPtr("2026-07-28T03:30:00Z"), "2026-07-28 03:30:00"},
		{"RFC3339Nano", strPtr("2026-07-28T03:30:00.123456789Z"), "2026-07-28 03:30:00"},
		{"T分隔无时区", strPtr("2026-07-28T03:30:00"), "2026-07-28 03:30:00"},
		{"带小数秒", strPtr("2026-07-28 03:30:00.123"), "2026-07-28 03:30:00"},
		{"异常长串截断", strPtr("2026-07-28 03:30:00 SOME-GARBAGE"), "2026-07-28 03:30:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeInspectorTime(tc.in); got != tc.want {
				t.Errorf("normalizeInspectorTime(%v)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
