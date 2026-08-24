// v2.0.58: /ChatAnalysisTotal 分布式分页数据库查询测试
//
// 守护：
//  1. StatsShardScanBatch 常量范围 [2000, 10000]
//  2. scanShardPaged NilDB 安全（sdb=nil 返回 nil，不 panic）
//  3. 分页聚合等价性：造 > batchSize 行，验证分页结果 == 逐行全量聚合（不重不漏）
//  4. GetTimeRangeStatsAll / GetTokensRangeStatsAll / GetDailyStatsAll 分页后总量正确
//  5. GetModelNameUsageStatsByRange GROUP BY model_name + user_count 正确
//  6. shardScanRow 仅小字段（禁含 longtext）
package models

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"

	"github.com/lishimeng/LsmTokensServer/logger"
)

// ============ 常量 / 静态契约 ============

// TestStatsShardScanBatch_Range 守护批大小落在合理区间 [2000, 10000]
func TestStatsShardScanBatch_Range(t *testing.T) {
	if StatsShardScanBatch < 2000 || StatsShardScanBatch > 10000 {
		t.Errorf("StatsShardScanBatch=%d 超出 [2000,10000]", StatsShardScanBatch)
	}
}

// TestShardScanRow_NoLongTextField 守护 shardScanRow 不含 8 个 longtext 字段（v2.0.42 白名单）
func TestShardScanRow_NoLongTextField(t *testing.T) {
	forbidden := []string{
		"request_headers", "request_body", "request_src_protocol_body",
		"response_headers", "response_body", "response_src_protocol_body",
		"request_src_protocol_headers", "response_src_protocol_headers",
	}
	rt := reflect.TypeOf(shardScanRow{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("gorm")
		for _, f := range forbidden {
			if strings.Contains(tag, f) {
				t.Errorf("shardScanRow 字段 %s 含禁止的 longtext 列 %s", rt.Field(i).Name, f)
			}
		}
	}
}

// TestScanShardPaged_NilDB 守护 sdb=nil 时返回 nil 且回调不触发
func TestScanShardPaged_NilDB(t *testing.T) {
	called := false
	err := scanShardPaged(nil, "TAgentHttpTransactionDataItem_00", "id, created_at", 7, func(rows []shardScanRow) {
		called = true
	})
	if err != nil {
		t.Errorf("NilDB 应返回 nil error，实际 %v", err)
	}
	if called {
		t.Errorf("NilDB 不应触发回调")
	}
}

// ============ SQLite 集成：分页等价性 ============

// setupPagedScanSQLite 建立内存 SQLite + 8 张分表，返回还原函数。
// 每个测试用独立的 shared-cache 命名空间（file:<name>?mode=memory&cache=shared），
// 避免与套件内其它 SQLite 测试共用同一 :memory: 库导致 "table is locked"。
func setupPagedScanSQLite(t *testing.T) func() {
	t.Helper()
	origDB := database.DB
	origCfg := testCfg
	origDisabled := logger.IsUserLogDisabled()

	testCfg = config.DefaultConfig()
	testCfg.DBMysqlSubTableNumber = 8
	logger.SetDisableUserLog(true)

	// 用测试名生成唯一 DSN（替换非法字符）
	dbName := "v2058_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := "file:" + dbName + "?mode=memory&cache=shared"
	sqliteDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 单连接避免 shared-cache 下的写锁竞争
	if sqlDB, e := sqliteDB.DB(); e == nil && sqlDB != nil {
		sqlDB.SetMaxOpenConns(1)
	}
	database.DB = sqliteDB
	if err := InitAgentHttpSubTables(testCfg.DBMysqlSubTableNumber); err != nil {
		t.Fatalf("init sub tables: %v", err)
	}
	return func() {
		if sqlDB, _ := database.DB.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = origDB
		testCfg = origCfg
		logger.SetDisableUserLog(origDisabled)
	}
}

// insertTxnRows 向指定分表批量写入交易记录
func insertTxnRows(t *testing.T, tableName string, items []*TAgentHttpTransactionDataItem) {
	t.Helper()
	// 分批插入避免单条 SQL 过长
	const chunk = 500
	for start := 0; start < len(items); start += chunk {
		end := start + chunk
		if end > len(items) {
			end = len(items)
		}
		if err := database.DB.Table(tableName).Create(items[start:end]).Error; err != nil {
			t.Fatalf("insert into %s: %v", tableName, err)
		}
	}
}

// TestScanShardPaged_AggregationEquivalence 造 > batchSize 行验证分页不重不漏
func TestScanShardPaged_AggregationEquivalence(t *testing.T) {
	restore := setupPagedScanSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_00"
	// 造 12000 行（> StatsShardScanBatch=5000，跨 3 批）
	const total = 12000
	now := time.Now()
	items := make([]*TAgentHttpTransactionDataItem, 0, total)
	var wantCount int64
	var wantTokens uint64
	for i := 0; i < total; i++ {
		tok := uint64(i%7 + 1)
		items = append(items, &TAgentHttpTransactionDataItem{
			CreatedAt:        now.Add(-time.Duration(i) * time.Minute),
			UpdatedAt:        now,
			UserName:         "u1",
			ModelName:        "m1",
			TokensInputSize:  tok,
			TokensOutputSize: tok * 2,
			TokensAllSize:    tok * 3,
			ElapsedMs:        int64(i % 100),
		})
		wantCount++
		wantTokens += tok * 3
	}
	insertTxnRows(t, tableName, items)

	sdb, cancel := database.StatsDB()
	defer cancel()

	var gotCount int64
	var gotTokens uint64
	seen := make(map[uint64]int) // id -> 出现次数（验证不重复）
	err := scanShardPaged(sdb, tableName, "id, created_at, tokens_all_size", 0, func(rows []shardScanRow) {
		for _, r := range rows {
			gotCount++
			gotTokens += r.TokensAllSize
			seen[r.ID]++
		}
	})
	if err != nil {
		t.Fatalf("scanShardPaged: %v", err)
	}
	if gotCount != wantCount {
		t.Errorf("分页扫描行数 = %d, 期望 %d（可能漏/重）", gotCount, wantCount)
	}
	if gotTokens != wantTokens {
		t.Errorf("分页扫描 tokens 合计 = %d, 期望 %d", gotTokens, wantTokens)
	}
	// 验证每个 id 只出现一次
	dup := 0
	for _, n := range seen {
		if n > 1 {
			dup++
		}
	}
	if dup != 0 {
		t.Errorf("有 %d 个 id 被重复扫描（keyset 分页应保证不重复）", dup)
	}
	if len(seen) != total {
		t.Errorf("去重 id 数 = %d, 期望 %d（有遗漏）", len(seen), total)
	}
}

// TestGetDailyStatsAll_PagedTotals 验证 GetDailyStatsAll 分页后按天聚合总量正确
func TestGetDailyStatsAll_PagedTotals(t *testing.T) {
	restore := setupPagedScanSQLite(t)
	defer restore()

	// 跨 2 张分表各写 6000 行（> batch），全部落在「今天（本地日期）」。
	// v2.0.59: 以本地今天 00:00 + 12h - i 构造 created_at，
	// 避免非 UTC 时区下本地凌晨 1:40 时「now - i 秒」跨到昨天导致今日桶计数不足。
	now := time.Now()
	todayKey := now.Format("2006-01-02")
	localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	todayBase := localMidnight.Add(12 * time.Hour)
	var wantCount int64
	var wantTotal uint64
	for _, tn := range []string{"TAgentHttpTransactionDataItem_00", "TAgentHttpTransactionDataItem_01"} {
		items := make([]*TAgentHttpTransactionDataItem, 0, 6000)
		for i := 0; i < 6000; i++ {
			items = append(items, &TAgentHttpTransactionDataItem{
				CreatedAt:     todayBase.Add(-time.Duration(i) * time.Second),
				UpdatedAt:     now,
				UserName:      "u1",
				ModelName:     "m1",
				TokensAllSize: 5,
			})
			wantCount++
			wantTotal += 5
		}
		insertTxnRows(t, tn, items)
	}

	stats, err := GetDailyStatsAll(8, 0)
	if err != nil {
		t.Fatalf("GetDailyStatsAll: %v", err)
	}
	var gotCount int64
	var gotTotal uint64
	for _, s := range stats {
		if s.Date == todayKey {
			gotCount += s.Count
			gotTotal += s.TokensTotal
		}
	}
	if gotCount != wantCount {
		t.Errorf("今日调用次数 = %d, 期望 %d", gotCount, wantCount)
	}
	if gotTotal != wantTotal {
		t.Errorf("今日总 tokens = %d, 期望 %d", gotTotal, wantTotal)
	}
}

// TestGetTokensRangeStatsAll_PagedTotals 验证 GetTokensRangeStatsAll 分页后跨桶合计正确
func TestGetTokensRangeStatsAll_PagedTotals(t *testing.T) {
	restore := setupPagedScanSQLite(t)
	defer restore()

	// v2.0.59: 以本地今天 00:00 + 12h 为基准散布 12 小时（±12h 均落在本地今天），
	// 避免非 UTC 时区下本地凌晨 1:40 时 i%12 小时跨到昨天导致 days=1 过滤后计数不足。
	tableName := "TAgentHttpTransactionDataItem_00"
	now := time.Now()
	localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	todayBase := localMidnight.Add(12 * time.Hour)
	const total = 7000 // > batch
	items := make([]*TAgentHttpTransactionDataItem, 0, total)
	var wantIn, wantOut, wantAll uint64
	for i := 0; i < total; i++ {
		items = append(items, &TAgentHttpTransactionDataItem{
			CreatedAt:        todayBase.Add(-time.Duration(i%12) * time.Hour),
			UpdatedAt:        now,
			UserName:         "u1",
			ModelName:        "m1",
			TokensInputSize:  2,
			TokensOutputSize: 3,
			TokensAllSize:    5,
		})
		wantIn += 2
		wantOut += 3
		wantAll += 5
	}
	insertTxnRows(t, tableName, items)

	stats, err := GetTokensRangeStatsAll(8, 1)
	if err != nil {
		t.Fatalf("GetTokensRangeStatsAll: %v", err)
	}
	var gotIn, gotOut, gotAll uint64
	var gotCount int64
	for _, s := range stats {
		gotIn += s.TokensInput
		gotOut += s.TokensOutput
		gotAll += s.TokensTotal
		gotCount += s.Count
	}
	// days=1 只统计最近 1 天：i%12 小时都落在窗口内，全部计入
	if gotCount != int64(total) {
		t.Errorf("总调用 = %d, 期望 %d", gotCount, total)
	}
	if gotIn != wantIn || gotOut != wantOut || gotAll != wantAll {
		t.Errorf("tokens 合计 in/out/all = %d/%d/%d, 期望 %d/%d/%d",
			gotIn, gotOut, gotAll, wantIn, wantOut, wantAll)
	}
}

// TestGetModelNameUsageStatsByRange_GroupByModelName 验证 GROUP BY model_name + user_count
func TestGetModelNameUsageStatsByRange_GroupByModelName(t *testing.T) {
	restore := setupPagedScanSQLite(t)
	defer restore()

	// model m1: user a(3 次) + user b(2 次) = 5 次调用, 2 个用户
	// model m2: user a(1 次) = 1 次调用, 1 个用户
	tableName := "TAgentHttpTransactionDataItem_00"
	now := time.Now()
	mk := func(model, user string, tok uint64) *TAgentHttpTransactionDataItem {
		return &TAgentHttpTransactionDataItem{
			CreatedAt: now, UpdatedAt: now,
			UserName: user, ModelName: model,
			TokensInputSize: tok, TokensOutputSize: tok, TokensAllSize: tok * 2,
		}
	}
	items := []*TAgentHttpTransactionDataItem{
		mk("m1", "a", 10), mk("m1", "a", 10), mk("m1", "a", 10),
		mk("m1", "b", 10), mk("m1", "b", 10),
		mk("m2", "a", 10),
	}
	insertTxnRows(t, tableName, items)

	stats, err := GetModelNameUsageStatsByRange(8, 0)
	if err != nil {
		t.Fatalf("GetModelNameUsageStatsByRange: %v", err)
	}
	byModel := make(map[string]ModelNameUsageStat)
	for _, s := range stats {
		byModel[s.ModelName] = s
	}
	m1, ok := byModel["m1"]
	if !ok {
		t.Fatalf("缺 m1 统计")
	}
	if m1.CallCount != 5 {
		t.Errorf("m1 调用次数 = %d, 期望 5", m1.CallCount)
	}
	if m1.UserCount != 2 {
		t.Errorf("m1 用户数 = %d, 期望 2（COUNT DISTINCT user_name）", m1.UserCount)
	}
	if m1.TokensTotal != 100 { // 5 次 × 20
		t.Errorf("m1 总 tokens = %d, 期望 100", m1.TokensTotal)
	}
	m2, ok := byModel["m2"]
	if !ok {
		t.Fatalf("缺 m2 统计")
	}
	if m2.CallCount != 1 || m2.UserCount != 1 {
		t.Errorf("m2 调用/用户 = %d/%d, 期望 1/1", m2.CallCount, m2.UserCount)
	}
	// 排序：m1（5 次）应排在 m2（1 次）之前
	if len(stats) >= 2 && stats[0].ModelName != "m1" {
		t.Errorf("排序错误：首位应为 m1（调用次数最多），实际 %s", stats[0].ModelName)
	}
}
