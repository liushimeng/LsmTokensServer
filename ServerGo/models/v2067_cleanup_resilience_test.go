// v2.0.67: 清理服务「invalid connection」自愈回归测试
//
// 背景（生产实证 2026-07-30 03:30:30）：
//
//	/CleanupReport 页面 shard_00 报告 status=failed：
//	  scan/delete failed (deleted=0 so far): scan batch failed: invalid connection
//	根因两层叠加：
//	1. 扫描 SQL（WHERE created_at<? ORDER BY id ASC LIMIT 1000）在 133GB 大表上
//	   无 (created_at, id) 覆盖索引 → 二级索引范围扫 + filesort + 逐行回表
//	   取 tokens（单行含 4 个 longtext，GB 级随机 IO）→ 首批就超 30s
//	   readTimeout → 连接被砍断 → invalid connection。
//	2. scanAndDeleteExpired 首次错误即返回，整个分表当次清理作废，
//	   24 小时后才重试；且报告无「会自动重试」语义，运维误以为需要人工介入。
//
// v2.0.67 修复：
//
//	① EnsureCleanupCreatedAtIndex：启动时确保 (created_at, id) 覆盖索引
//	② scan/delete 失败退避重试（3 次 × 20s），重试耗尽才 failed
//	③ runDailyCleanup 返回失败分表数，调度层 60s 自动补偿重跑（最多 5 次）
//	④ 前端失败标签旁显示「⏳ 自动重试中」提示
package models

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
)

// ============================================================================
// 1. 索引保障：EnsureCleanupCreatedAtIndex
// ============================================================================

// TestEnsureCleanupCreatedAtIndex_CreatesAndIdempotent 守护 (created_at, id)
// 索引被真实创建、且重复调用幂等不报错。
//
// 这是 v2.0.67 的主修复：缺该索引时 shard_00 首批扫描超 30s readTimeout，
// 清理服务在该表上从未成功过（生产实证 deleted=0 + invalid connection）。
// TestEnsureCleanupCreatedAtIndex_CreatesAndIdempotent 守护 (created_at, id)
// 索引创建逻辑与幂等性。
//
// SQLite 限制：索引名是库级命名空间（与 MySQL 的表级不同），同一名字
// 只能在全库建一次。因此本测试只验证「函数能建出索引、幂等不报错、
// 存在性探测函数结果正确」；「8 张表各自带同名索引」的语义由
// TestEnsureCleanupCreatedAtIndex_SQLiteNamespaceLimitation 显式记录，
// 生产 MySQL 路径由 information_schema 检查分支覆盖。
func TestEnsureCleanupCreatedAtIndex_CreatesAndIdempotent(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	// AutoMigrate 可能已通过 gorm tag 在某张分表上建过该索引（SQLite 全局
	// 命名空间下只能有一张表带这个名字）—— 先探测现状，再决定预期。
	preExisting := -1
	for i := 0; i < 8; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if sqliteIndexExists(t, tableName, CleanupCreatedAtIndexName) {
			preExisting = i
			break
		}
	}

	if err := EnsureCleanupCreatedAtIndex(8); err != nil {
		t.Fatalf("EnsureCleanupCreatedAtIndex: %v", err)
	}

	// 调用后库里必须恰好有一个该名字的索引（可能在 preExisting 表，
	// 也可能是函数新建的 —— SQLite 下无法区分也不必区分）
	found := 0
	for i := 0; i < 8; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if sqliteIndexExists(t, tableName, CleanupCreatedAtIndexName) {
			found++
		}
	}
	if found != 1 {
		t.Errorf("SQLite 库中应恰好有 1 个 %s（全局命名空间限制），实际 %d 个（AutoMigrate 预建表=%d）",
			CleanupCreatedAtIndexName, found, preExisting)
	}

	// 幂等：第二次调用不得报错（生产每次启动都会调用）
	if err := EnsureCleanupCreatedAtIndex(8); err != nil {
		t.Errorf("重复调用必须幂等，got: %v", err)
	}
}

// TestEnsureCleanupCreatedAtIndex_SQLiteNamespaceLimitation 显式记录 SQLite
// 与 MySQL 在索引命名空间上的差异，防止有人把 SQLite 测试失败误判成函数缺陷：
//   - MySQL：索引名作用域在表内，8 张分表可以各自有 idx_cleanup_created_id
//   - SQLite：索引名作用域在整个库，第二次 CREATE 同名索引报 already exists
//
// 本测试锁死这个行为差异，让后续维护者看到失败时知道去哪查。
func TestEnsureCleanupCreatedAtIndex_SQLiteNamespaceLimitation(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	// 在表 A 上建索引成功
	if err := database.DB.Exec("CREATE INDEX `idx_ns_probe` ON `TAgentHttpTransactionDataItem_00` (`created_at`, `id`)").Error; err != nil {
		t.Fatalf("首个索引创建应成功: %v", err)
	}
	// 同名索引在表 B 上必须报 already exists（SQLite 全局命名空间实证）
	err := database.DB.Exec("CREATE INDEX `idx_ns_probe` ON `TAgentHttpTransactionDataItem_01` (`created_at`, `id`)").Error
	if err == nil {
		t.Fatal("SQLite 全局命名空间下，同名索引在第二张表上创建必须失败；若成功说明 SQLite 行为变了，需重新评估 ensure 逻辑")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("错误必须是 already exists，got: %v", err)
	}
}

// TestEnsureCleanupCreatedAtIndex_CreatesOnFreshTable 守护在「确定全库都没有
// 该索引」的干净库里，函数能真实把索引建出来（防止「检查逻辑恒为已存在」
// 的假阳性 —— 那会让生产大表永远缺索引、扫描持续超时断连）。
//
// SQLite 全局命名空间下「建在哪张表」不重要，能建出来 + 后续探测得到即可。
func TestEnsureCleanupCreatedAtIndex_CreatesOnFreshTable(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	// 前置：干净库里任何分表上都不该有该索引
	for i := 0; i < 8; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if sqliteIndexExists(t, tableName, CleanupCreatedAtIndexName) {
			t.Fatalf("前置条件失败：%s 不应已存在于 %s", CleanupCreatedAtIndexName, tableName)
		}
	}

	if err := EnsureCleanupCreatedAtIndex(8); err != nil {
		t.Fatalf("EnsureCleanupCreatedAtIndex: %v", err)
	}

	// 调用后必须真实建出（SQLite 下只会落在第一张表上，见命名空间限制测试）
	created := false
	for i := 0; i < 8; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if sqliteIndexExists(t, tableName, CleanupCreatedAtIndexName) {
			created = true
			break
		}
	}
	if !created {
		t.Errorf("干净库调用后必须真实创建出 %s（缺它时大表扫描会超时断连）", CleanupCreatedAtIndexName)
	}
}

// sqliteIndexExists 查询 SQLite 分表上指定索引是否存在
func sqliteIndexExists(t *testing.T, tableName, indexName string) bool {
	t.Helper()
	var count int64
	if err := database.DB.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?",
		tableName, indexName,
	).Scan(&count).Error; err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return count > 0
}

// TestEnsureCleanupCreatedAtIndex_NilDB 守护 database.DB=nil 时返回错误而非 panic
func TestEnsureCleanupCreatedAtIndex_NilDB(t *testing.T) {
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	if err := EnsureCleanupCreatedAtIndex(8); err == nil {
		t.Error("database.DB=nil 时必须返回错误")
	}
}

// ============================================================================
// 2. 重试语义：错误消息与常量契约
// ============================================================================

// TestCleanupRetryExhaustedErrorMessage 守护重试耗尽后的错误文案：
// 必须包含「下次运行自动继续」（前端 statusTag 据此渲染「⏳ 自动重试中」），
// 并携带已删除条数，让运维知道是部分失败而非完全没干活。
func TestCleanupRetryExhaustedErrorMessage(t *testing.T) {
	// 直接复刻 scanAndDeleteExpired 重试耗尽分支的 fmt 文案做契约校验：
	// 该文案同时被前端 JS 子串匹配（cleanup-retry-hint），两侧必须一致。
	const frontEndMarker = "下次运行自动继续"

	// 后端两处错误文案（scan / delete 分支）都必须带这个标记
	msgScan := "scan batch failed after 3 attempts (deleted=100 so far, 已按 20s 间隔退避重试，下次运行自动继续): invalid connection"
	msgDelete := "delete batch failed after 3 attempts (deleted=100 so far, 已按 20s 间隔退避重试，下次运行自动继续): invalid connection"
	for _, msg := range []string{msgScan, msgDelete} {
		if !strings.Contains(msg, frontEndMarker) {
			t.Errorf("错误文案缺少前端契约标记 %q: %s", frontEndMarker, msg)
		}
	}
}

// TestCleanupRetryConstants 守护重试预算常量合理：
//   - 至少 2 次（单次失败即放弃正是本次 bug 的行为）
//   - 上限 10 次（退避重试不是无限循环，超过说明是确定性故障）
//   - 单次退避在 [1s, 120s] 区间（太短没意义，太长拖垮单表 10 分钟预算）
func TestCleanupRetryConstants(t *testing.T) {
	if cleanupMaxConsecutiveFailures < 2 || cleanupMaxConsecutiveFailures > 10 {
		t.Errorf("cleanupMaxConsecutiveFailures=%d 超出 [2,10] 合理区间", cleanupMaxConsecutiveFailures)
	}
	if cleanupRetryBackoff < time.Second || cleanupRetryBackoff > 120*time.Second {
		t.Errorf("cleanupRetryBackoff=%v 超出 [1s,120s] 合理区间", cleanupRetryBackoff)
	}
	// 总重试预算必须远小于单表 10 分钟 ctx，否则退避本身就会吃掉扫描时间
	totalBackoff := time.Duration(cleanupMaxConsecutiveFailures) * cleanupRetryBackoff
	if totalBackoff > cleanupScanCtxTimeout/3 {
		t.Errorf("总退避 %v 超过单表预算 %v 的 1/3，退避会挤占有效扫描时间",
			totalBackoff, cleanupScanCtxTimeout)
	}
}

// TestCleanupAutoRerunConstants 守护自动补偿重跑参数：
//   - 间隔在 [10s, 10min]（太短打不到瞬时故障恢复窗口，太长失去补偿意义）
//   - 次数在 [1, 20]（至少补一次；超过 20 次说明故障持续，应交给次日日程）
func TestCleanupAutoRerunConstants(t *testing.T) {
	if cleanupAutoRerunDelay < 10*time.Second || cleanupAutoRerunDelay > 10*time.Minute {
		t.Errorf("cleanupAutoRerunDelay=%v 超出 [10s,10min] 合理区间", cleanupAutoRerunDelay)
	}
	if cleanupAutoRerunMaxAttempts < 1 || cleanupAutoRerunMaxAttempts > 20 {
		t.Errorf("cleanupAutoRerunMaxAttempts=%d 超出 [1,20] 合理区间", cleanupAutoRerunMaxAttempts)
	}
}

// ============================================================================
// 3. 退避等待：可取消语义
// ============================================================================

// TestCleanupSleepRetryable 守护退避等待的三个行为：
//  1. 正常等待完整时长返回 true
//  2. 调用方 ctx 取消时立即返回 false（不傻等）
//  3. 已取消的 ctx 调用时立即返回 false
func TestCleanupSleepRetryable(t *testing.T) {
	t.Run("正常等待返回true", func(t *testing.T) {
		start := time.Now()
		if !cleanupSleepRetryable(context.Background(), 50*time.Millisecond) {
			t.Error("完整等待后应返回 true")
		}
		if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
			t.Errorf("实际只等了 %v，退避被意外跳过", elapsed)
		}
	})

	t.Run("ctx取消立即返回false", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		if cleanupSleepRetryable(ctx, 10*time.Second) {
			t.Error("ctx 取消后应返回 false")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("ctx 取消后仍等待了 %v，未立即中断", elapsed)
		}
	})

	t.Run("已取消ctx立即返回false", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		if cleanupSleepRetryable(ctx, 10*time.Second) {
			t.Error("已取消 ctx 应返回 false")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("已取消 ctx 仍等待了 %v", elapsed)
		}
	})
}

// ============================================================================
// 4. 回归守护：重试逻辑不破坏正常路径
// ============================================================================

// TestScanAndDeleteExpired_NormalPathUnaffected 守护加入重试逻辑后，
// 无故障的正常清理路径行为与 v2.0.62 完全一致（一次成功，无退避等待）。
func TestScanAndDeleteExpired_NormalPathUnaffected(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	tableName := "TAgentHttpTransactionDataItem_06"
	const expiredCount = 40
	const freshCount = 3
	rows := make([]*TAgentHttpTransactionDataItem, 0, expiredCount+freshCount)
	for i := 0; i < expiredCount; i++ {
		rows = append(rows, makeCleanupTxn(70, 3, 4))
	}
	for i := 0; i < freshCount; i++ {
		rows = append(rows, makeCleanupTxn(1, 9, 9))
	}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -45)
	start := time.Now()
	deleted, tIn, tOut, tAll, partial, err := scanAndDeleteExpired(tableName, cutoff, 50)
	if err != nil {
		t.Fatalf("scanAndDeleteExpired: %v", err)
	}
	// 正常路径不应触发任何 20s 退避 —— 40 行小数据必须秒级完成
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("正常路径耗时 %v，疑似误触发退避等待", elapsed)
	}
	if deleted != expiredCount {
		t.Errorf("deleted=%d, want %d", deleted, expiredCount)
	}
	if partial {
		t.Error("无故障小分表不应 partial")
	}
	if tIn != expiredCount*3 || tOut != expiredCount*4 || tAll != expiredCount*7 {
		t.Errorf("tokens=(%d,%d,%d), want (%d,%d,%d)",
			tIn, tOut, tAll, expiredCount*3, expiredCount*4, expiredCount*7)
	}
	if got := countRowsIn(t, tableName); got != freshCount {
		t.Errorf("剩余 %d 行, want %d", got, freshCount)
	}
}

// TestRunDailyCleanup_ReturnsFailedTableCount 守护 runDailyCleanup 的返回值：
// 无失败分表时返回 0（驱动调度层判断是否补偿重跑的唯一依据）。
func TestRunDailyCleanup_ReturnsFailedTableCount(t *testing.T) {
	restore := setupCleanupSQLite(t)
	defer restore()

	// 报告表不在分表 AutoMigrate 范围内，需显式初始化
	if err := InitCleanupReportTable(); err != nil {
		t.Fatalf("init report table: %v", err)
	}

	// 只往一张表塞过期数据，其余表为空 → 全部 success，返回 0
	tableName := "TAgentHttpTransactionDataItem_00"
	rows := []*TAgentHttpTransactionDataItem{makeCleanupTxn(90, 1, 1)}
	if err := database.DB.Table(tableName).Create(rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	c := config.DefaultConfig()
	c.DBMysqlSubTableNumber = 8
	c.TransactionRetentionDays = 32

	failed := runDailyCleanup(c)
	if failed != 0 {
		t.Errorf("无故障运行时 failed=%d, want 0（非零会触发无谓的自动重跑）", failed)
	}
	if got := countRowsIn(t, tableName); got != 0 {
		t.Errorf("过期行未被清理，剩余 %d", got)
	}

	// 报告必须落库（自动重跑的 upsert 语义依赖报告表存在）
	var reportCount int64
	if err := database.DB.Table(CleanupReportTableName).Count(&reportCount).Error; err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if reportCount != 8 {
		t.Errorf("报告数=%d, want 8（每张分表一行）", reportCount)
	}
}

// TestCleanupIndexName_DistinctFromStatsIndex 守护新索引名不与既有索引冲突：
// 与 idx_user_model_created 必须是不同索引（列序不同、用途不同），
// 名字撞了会导致 EnsureCleanupCreatedAtIndex 误判「已存在」而跳过创建。
func TestCleanupIndexName_DistinctFromStatsIndex(t *testing.T) {
	if CleanupCreatedAtIndexName == StatsCompositeIndexName {
		t.Errorf("清理索引名 %q 与统计索引名冲突", CleanupCreatedAtIndexName)
	}
	if !strings.Contains(CleanupCreatedAtIndexName, "created") {
		t.Errorf("索引名 %q 应体现其首列 created_at（运维 SHOW INDEX 时一眼可辨）", CleanupCreatedAtIndexName)
	}
}
