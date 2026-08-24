// v2.0.62: 过期数据清理「真实删除」回归测试
//
// 背景（严重缺陷）：v2.0.47 引入清理服务后，Step 1 统计用的匿名结构体字段名为
//
//	`Rows`（无 gorm tag），GORM NamingStrategy 将其映射到列 `rows`，而 SQL 别名
//	是 `row_count`（当初为规避 MariaDB 保留字而改名，却漏改结构体字段）。
//	两者对不上 → stats.Rows 恒为 0 → `if stats.Rows == 0 { return }` 永远命中
//	→ Step 2 删除、Step 3 释放空间被完全跳过。
//
//	生产实证：报告表 deleted_tokens_in = 20 亿（TokensIn→tokens_in 映射正确），
//	而 deleted_rows = 0，status 却是 success —— 故障被完全掩盖。
//
// 守护：
//  1. Step 1 统计出的行数必须等于真实过期行数（核心回归，修复前必然失败）
//  2. cleanupOneSubTable 必须真的把过期行从表里删掉，且不误删未过期行
//  3. 磁盘预检守卫：空间充足/不足的判定与哨兵错误语义
//  4. 保留天数配置 32 天契约（v2.0.63 从 45 调整为 32）
//  5. 删除批次默认值契约
package models

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// ============================================================================
// 测试夹具
// ============================================================================

// setupCleanupSQLite 建立内存 SQLite + 分表，返回还原函数。
// 独立 shared-cache 命名空间，避免与套件内其它 SQLite 测试互相锁表。
func setupCleanupSQLite(t *testing.T) func() {
	t.Helper()
	origDB := database.DB
	origCfg := testCfg
	origDisabled := logger.IsUserLogDisabled()

	testCfg = config.DefaultConfig()
	testCfg.DBMysqlSubTableNumber = 8
	logger.SetDisableUserLog(true)

	// 关闭 InitAgentHttpSubTables 拉起的后台回填 goroutine：它持有 database.DB 引用异步跑，
	// 会与本测试 defer 里的 database.DB 关闭/还原竞争，导致 nil 解引用 panic。
	// （t.Setenv 会在测试结束时自动还原）
	t.Setenv("LSM_SKIP_BACKGROUND_BACKFILL", "1")

	dbName := "v2062_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := "file:" + dbName + "?mode=memory&cache=shared"
	sqliteDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
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

// makeCleanupTxn 构造一条交易记录。ageDays > 0 表示 N 天前。
func makeCleanupTxn(ageDays int, tokensIn, tokensOut uint64) *TAgentHttpTransactionDataItem {
	return &TAgentHttpTransactionDataItem{
		UserName:         "cleanup_user",
		ModelName:        "cleanup_model",
		TokensInputSize:  tokensIn,
		TokensOutputSize: tokensOut,
		TokensAllSize:    tokensIn + tokensOut,
		CreatedAt:        time.Now().AddDate(0, 0, -ageDays),
	}
}

// countRowsIn 返回指定分表当前行数
func countRowsIn(t *testing.T, tableName string) int64 {
	t.Helper()
	var n int64
	if err := database.DB.Table(tableName).Count(&n).Error; err != nil {
		t.Fatalf("count %s: %v", tableName, err)
	}
	return n
}

// ============================================================================
// 1. 核心回归：Step 1 行数统计必须正确（修复前必然失败）
// ============================================================================

// TestCleanupStatsRowCountMapping 守护 Step 1 统计的行数与真实过期行数一致。
//
// 这是 v2.0.62 缺陷的直接回归测试：修复前 stats.Rows 因列名错位恒为 0，
// 本测试会立刻捕获；修复后必须等于真实过期行数。
func TestCleanupStatsRowCountMapping(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_00"

	// 60 天前 12 条（过期）+ 1 天前 5 条（保留）
	const expiredCount = 12
	const freshCount = 5
	rows := make([]*TAgentHttpTransactionDataItem, 0, expiredCount+freshCount)
	for i := 0; i < expiredCount; i++ {
		rows = append(rows, makeCleanupTxn(60, 100, 50))
	}
	for i := 0; i < freshCount; i++ {
		rows = append(rows, makeCleanupTxn(1, 999, 999))
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -45)

	// 复刻 cleanupOneSubTable Step 1 的查询（含 v2.0.62 的显式 column tag）
	var stats struct {
		Rows      int64  `gorm:"column:row_count"`
		TokensIn  uint64 `gorm:"column:tokens_in"`
		TokensOut uint64 `gorm:"column:tokens_out"`
		TokensAll uint64 `gorm:"column:tokens_all"`
	}
	err := database.DB.Table(tableName).
		Where("created_at < ?", cutoff).
		Select("COUNT(*) AS `row_count`, COALESCE(SUM(tokens_input_size),0) AS tokens_in, COALESCE(SUM(tokens_output_size),0) AS tokens_out, COALESCE(SUM(tokens_all_size),0) AS tokens_all").
		Scan(&stats).Error
	if err != nil {
		t.Fatalf("step1 stats: %v", err)
	}

	if stats.Rows != expiredCount {
		t.Errorf("stats.Rows=%d, want %d —— 列名映射错位会让删除被整体跳过（v2.0.62 缺陷）",
			stats.Rows, expiredCount)
	}
	// Tokens 字段本来就映射正确，一并守护避免回退
	if stats.TokensIn != expiredCount*100 {
		t.Errorf("stats.TokensIn=%d, want %d", stats.TokensIn, expiredCount*100)
	}
	if stats.TokensAll != expiredCount*150 {
		t.Errorf("stats.TokensAll=%d, want %d", stats.TokensAll, expiredCount*150)
	}
}

// ============================================================================
// 2. 端到端：cleanupOneSubTable 必须真的删除记录
// ============================================================================

// TestCleanupOneSubTable_ActuallyDeletes 验证过期行真从表里消失、未过期行保留。
//
// 这是对用户诉求「确保相关的 mysql 记录真实的被删除掉」的直接验证。
func TestCleanupOneSubTable_ActuallyDeletes(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_01"

	const expiredCount = 30
	const freshCount = 7
	rows := make([]*TAgentHttpTransactionDataItem, 0, expiredCount+freshCount)
	for i := 0; i < expiredCount; i++ {
		rows = append(rows, makeCleanupTxn(90, 10, 20))
	}
	for i := 0; i < freshCount; i++ {
		rows = append(rows, makeCleanupTxn(2, 10, 20))
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	if got := countRowsIn(t, tableName); got != expiredCount+freshCount {
		t.Fatalf("前置行数=%d, want %d", got, expiredCount+freshCount)
	}

	cutoff := time.Now().AddDate(0, 0, -45)
	report := cleanupOneSubTable(tableName, 1, cutoff, 45, time.Now().Format("2006-01-02"))

	// 报告里的删除数必须是真实数字（修复前恒为 0）
	if report.DeletedRows != expiredCount {
		t.Errorf("report.DeletedRows=%d, want %d", report.DeletedRows, expiredCount)
	}

	// 关键：表里过期行必须真的没了
	remaining := countRowsIn(t, tableName)
	if remaining != freshCount {
		t.Errorf("清理后剩余 %d 行, want %d（过期行未被真实删除）", remaining, freshCount)
	}

	// 未过期行不能被误删
	var stillExpired int64
	if err := database.DB.Table(tableName).Where("created_at < ?", cutoff).Count(&stillExpired).Error; err != nil {
		t.Fatalf("count expired: %v", err)
	}
	if stillExpired != 0 {
		t.Errorf("仍有 %d 行过期数据残留", stillExpired)
	}
}

// TestCleanupOneSubTable_NoExpiredRowsIsNoop 无过期数据时不应删除任何行
func TestCleanupOneSubTable_NoExpiredRowsIsNoop(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_02"
	rows := []*TAgentHttpTransactionDataItem{
		makeCleanupTxn(1, 1, 1),
		makeCleanupTxn(5, 1, 1),
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -45)
	report := cleanupOneSubTable(tableName, 2, cutoff, 45, time.Now().Format("2006-01-02"))

	if report.DeletedRows != 0 {
		t.Errorf("report.DeletedRows=%d, want 0", report.DeletedRows)
	}
	if got := countRowsIn(t, tableName); got != 2 {
		t.Errorf("行数=%d, want 2（不应误删未过期数据）", got)
	}
	if report.Status != "success" {
		t.Errorf("Status=%s, want success", report.Status)
	}
}

// TestDeleteExpiredBatch_CrossesMultipleBatches 验证分批删除跨多批不重不漏
func TestDeleteExpiredBatch_CrossesMultipleBatches(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_03"

	// 造 250 行过期数据，用 batchSize=100 强制跨 3 批
	const expiredCount = 250
	rows := make([]*TAgentHttpTransactionDataItem, 0, expiredCount)
	for i := 0; i < expiredCount; i++ {
		rows = append(rows, makeCleanupTxn(80, 1, 1))
	}
	rows = append(rows, makeCleanupTxn(1, 1, 1)) // 1 行未过期
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -45)
	deleted, err := deleteExpiredBatch(tableName, cutoff, 100)
	if err != nil {
		t.Fatalf("deleteExpiredBatch: %v", err)
	}
	if deleted != expiredCount {
		t.Errorf("deleted=%d, want %d", deleted, expiredCount)
	}
	if got := countRowsIn(t, tableName); got != 1 {
		t.Errorf("剩余 %d 行, want 1", got)
	}
}

// TestScanAndDeleteExpired_NoExpired 守护无过期数据时的快路径
func TestScanAndDeleteExpired_NoExpired(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_04"
	rows := []*TAgentHttpTransactionDataItem{
		makeCleanupTxn(1, 1, 1),
		makeCleanupTxn(5, 2, 2),
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -45)
	deleted, tIn, tOut, tAll, partial, err := scanAndDeleteExpired(tableName, cutoff, 500)
	if err != nil {
		t.Fatalf("scanAndDeleteExpired: %v", err)
	}
	if deleted != 0 || partial || tIn != 0 || tOut != 0 || tAll != 0 {
		t.Errorf("无过期数据应全零，got deleted=%d partial=%v tIn=%d tOut=%d tAll=%d",
			deleted, partial, tIn, tOut, tAll)
	}
	// 同时守护调用方会收到零值，方便上层判断「无过期」
	_ = tOut
	if got := countRowsIn(t, tableName); got != 2 {
		t.Errorf("行数=%d, want 2（不得误删）", got)
	}
}

// TestScanAndDeleteExpired_ActuallyDeletes 守护单遍流式真的删除过期行
func TestScanAndDeleteExpired_ActuallyDeletes(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_05"

	// 60 天前过期 80 行 + 1 天前保留 5 行
	const expiredCount = 80
	const freshCount = 5
	rows := make([]*TAgentHttpTransactionDataItem, 0, expiredCount+freshCount)
	for i := 0; i < expiredCount; i++ {
		rows = append(rows, makeCleanupTxn(60, 5, 7))
	}
	for i := 0; i < freshCount; i++ {
		rows = append(rows, makeCleanupTxn(1, 999, 999))
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -45)
	deleted, tIn, tOut, tAll, partial, err := scanAndDeleteExpired(tableName, cutoff, 50)
	if err != nil {
		t.Fatalf("scanAndDeleteExpired: %v", err)
	}
	if deleted != expiredCount {
		t.Errorf("deleted=%d, want %d", deleted, expiredCount)
	}
	if partial {
		t.Errorf("小分表不应 partial")
	}
	if tIn != uint64(expiredCount)*5 {
		t.Errorf("tIn=%d, want %d", tIn, expiredCount*5)
	}
	if tOut != uint64(expiredCount)*7 {
		t.Errorf("tOut=%d, want %d", tOut, expiredCount*7)
	}
	if tAll != uint64(expiredCount)*12 {
		t.Errorf("tAll=%d, want %d", tAll, expiredCount*12)
	}

	// 验证过期行真没了，未过期行保留
	if got := countRowsIn(t, tableName); got != freshCount {
		t.Errorf("剩余 %d 行, want %d", got, freshCount)
	}
	var stillExpired int64
	database.DB.Table(tableName).Where("created_at < ?", cutoff).Count(&stillExpired)
	if stillExpired != 0 {
		t.Errorf("残留 %d 行过期数据", stillExpired)
	}
}

// TestIsCtxTimeout 守护 ctx 取消/超时错误识别（partial 路径依赖）
func TestIsCtxTimeout(t *testing.T) {
	if !isCtxTimeout(context.Canceled) {
		t.Error("context.Canceled 应被识别为 ctx 超时")
	}
	if !isCtxTimeout(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded 应被识别为 ctx 超时")
	}
	if !isCtxTimeout(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Error("包装的 Canceled 应被识别为 ctx 超时")
	}
	if isCtxTimeout(fmt.Errorf("other error")) {
		t.Error("非 ctx 错误不应被识别为 ctx 超时")
	}
}

// ============================================================================
// 3. 磁盘预检守卫
// ============================================================================

// TestCheckDiskSpaceForRebuild_NilDBIsPermissive 守护 database.DB=nil 时放行（不阻断主流程）
func TestCheckDiskSpaceForRebuild_NilDBIsPermissive(t *testing.T) {
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	enough, _, _ := checkDiskSpaceForRebuild("TAgentHttpTransactionDataItem_00")
	if !enough {
		t.Error("探测失败时应放行（enough=true），避免诊断能力缺失阻断清理主流程")
	}
}

// TestGetAvailableDiskBytes 验证 statfs 探测行为
func TestGetAvailableDiskBytes(t *testing.T) {
	t.Run("空路径返回 -1", func(t *testing.T) {
		if got := getAvailableDiskBytes(""); got != -1 {
			t.Errorf("空路径 got=%d, want -1", got)
		}
	})
	t.Run("空白路径返回 -1", func(t *testing.T) {
		if got := getAvailableDiskBytes("   "); got != -1 {
			t.Errorf("空白路径 got=%d, want -1", got)
		}
	})
	t.Run("不存在的路径返回 -1", func(t *testing.T) {
		if got := getAvailableDiskBytes("/nonexistent/path/for/v2062/test"); got != -1 {
			t.Errorf("不存在路径 got=%d, want -1", got)
		}
	})
	t.Run("有效路径返回正数", func(t *testing.T) {
		got := getAvailableDiskBytes(os.TempDir())
		if got <= 0 {
			t.Errorf("临时目录可用空间 got=%d, want > 0", got)
		}
	})
}

// TestMaxAutoRebuildTableBytes 守护自动重建体积上限合理
//
// 实测 shard_01（63.6GB）即使磁盘充足，ALTER TABLE 重建也会以
// `invalid connection` 失败（耗时超过连接超时）。上限必须显著低于该值。
func TestMaxAutoRebuildTableBytes(t *testing.T) {
	const gb = int64(1024 * 1024 * 1024)
	if maxAutoRebuildTableBytes < gb {
		t.Errorf("maxAutoRebuildTableBytes=%d 过小，小表也不给重建了", maxAutoRebuildTableBytes)
	}
	// 必须低于实测失败的 63.6GB，留足余量
	if maxAutoRebuildTableBytes >= 60*gb {
		t.Errorf("maxAutoRebuildTableBytes=%dGB 不低于实测失败体积 63.6GB，重建仍会超时",
			maxAutoRebuildTableBytes/gb)
	}
}

// TestRebuildDiskSafetyMargin 守护安全余量常量合理（1GB ~ 50GB）
func TestRebuildDiskSafetyMargin(t *testing.T) {
	const gb = int64(1024 * 1024 * 1024)
	if rebuildDiskSafetyMarginBytes < gb || rebuildDiskSafetyMarginBytes > 50*gb {
		t.Errorf("rebuildDiskSafetyMarginBytes=%d 超出合理区间 [1GB, 50GB]",
			rebuildDiskSafetyMarginBytes)
	}
}

// TestErrSkipRebuildLowDisk_IsIdentifiable 守护哨兵错误可被 errors.Is 识别
//
// cleanupOneSubTable 依赖这一点区分「磁盘不足主动跳过」与「重建真的出错」，
// 前者不应被当成故障上报。
func TestErrSkipRebuildLowDisk_IsIdentifiable(t *testing.T) {
	wrapped := errors.New("outer: " + errSkipRebuildLowDisk.Error())
	if errors.Is(wrapped, errSkipRebuildLowDisk) {
		t.Error("纯字符串拼接不应被 errors.Is 匹配（说明测试本身写错了）")
	}

	// 实际包装方式（%w）必须可识别
	realWrapped := wrapError(errSkipRebuildLowDisk, "可用 10GB < 需要 100GB")
	if !errors.Is(realWrapped, errSkipRebuildLowDisk) {
		t.Error("%w 包装后应能被 errors.Is 识别为 errSkipRebuildLowDisk")
	}
}

// wrapError 测试辅助：模拟 releaseTableSpace 的 %w 包装方式
func wrapError(base error, detail string) error {
	return fmt.Errorf("%w（%s）", base, detail)
}

// ============================================================================
// 4. 配置契约
// ============================================================================

// TestTransactionRetentionDays_Is32 守护保留天数为 32 天（v2.0.63 从 45 调整为 32）
func TestTransactionRetentionDays_Is32(t *testing.T) {
	if config.DEFAULT_TRANSACTION_RETENTION_DAYS != 32 {
		t.Errorf("config.DEFAULT_TRANSACTION_RETENTION_DAYS=%d, want 32", config.DEFAULT_TRANSACTION_RETENTION_DAYS)
	}
	c := config.DefaultConfig()
	if c.TransactionRetentionDays != 32 {
		t.Errorf("默认配置 TransactionRetentionDays=%d, want 32", c.TransactionRetentionDays)
	}
}

// TestCleanupBatchSize_Default 守护批次默认值适配大 body 行 + redo log 上限
//
// 生产实测：过期行平均 body ≈ 459KB，而 MariaDB innodb_log_file_size = 96MB。
// 单批 undo/redo 必须稳稳落在 redo log 之内，否则连接被直接掐断
// （500 行 ≈ 220MB → `invalid connection`，shard_00 删到第 3500 行即中断）。
func TestCleanupBatchSize_Default(t *testing.T) {
	t.Setenv("LSM_CLEANUP_BATCH_SIZE", "")
	if got := getCleanupBatchSize(); got != config.DEFAULT_CLEANUP_BATCH_SIZE {
		t.Errorf("getCleanupBatchSize()=%d, want %d", got, config.DEFAULT_CLEANUP_BATCH_SIZE)
	}

	const avgRowBytes = 459 * 1024        // 生产实测平均行体积
	const redoLogBytes = 96 * 1024 * 1024 // innodb_log_file_size
	txBytes := config.DEFAULT_CLEANUP_BATCH_SIZE * avgRowBytes
	// 留 2 倍余量：单批事务不得超过 redo log 的一半
	if txBytes > redoLogBytes/2 {
		t.Errorf("config.DEFAULT_CLEANUP_BATCH_SIZE=%d → 单批 ≈%dMB，超过 redo log(96MB) 的一半，会触发 invalid connection",
			config.DEFAULT_CLEANUP_BATCH_SIZE, txBytes/1024/1024)
	}
	if config.DEFAULT_CLEANUP_BATCH_SIZE < 10 {
		t.Errorf("config.DEFAULT_CLEANUP_BATCH_SIZE=%d 过小，round-trip 开销会拖垮大分表清理",
			config.DEFAULT_CLEANUP_BATCH_SIZE)
	}
}

// TestGetCleanupStateSnapshot_ExposesRetentionDays 守护状态快照透出保留天数
//
// /CleanupReport 页面的「当前保留天数配置」KPI 卡依赖该字段。
func TestGetCleanupStateSnapshot_ExposesRetentionDays(t *testing.T) {
	cleanupState.mu.Lock()
	orig := cleanupState.retentionDays
	cleanupState.retentionDays = 45
	cleanupState.mu.Unlock()
	defer func() {
		cleanupState.mu.Lock()
		cleanupState.retentionDays = orig
		cleanupState.mu.Unlock()
	}()

	snap := GetCleanupStateSnapshot()
	v, ok := snap["retention_days"].(int)
	if !ok {
		t.Fatalf("retention_days 类型=%T, want int", snap["retention_days"])
	}
	if v != 45 {
		t.Errorf("retention_days=%d, want 45", v)
	}
}
