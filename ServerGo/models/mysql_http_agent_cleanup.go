package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gorm.io/gorm/clause"
)

// ============================================================================
// v2.0.47: 过期数据清理服务（浏览记录定期回收）
// ============================================================================
//
// 背景：TAgentHttpTransactionDataItem 分表（00-07）单行最大约 4MB（4 个 longtext
//   字段），高频请求下表会持续膨胀。长期运行后 MySQL 数据目录越来越大、
//   查询越来越慢、备份时间越来越长。
//
// 设计：
//   1. 配置：server_conf.go TransactionRetentionDays（默认 60 天；0 = 禁用）
//   2. 调度：每天 LSM_CLEANUP_HOUR 触发（默认凌晨 3 点），服务启动 30s 后
//      先跑一次（让 MySQL/缓存就绪）
//   3. 清理策略：
//      a) Step 1: 统计待删记录的 COUNT + Tokens（删除前先 SELECT SUM）
//      b) Step 2: 分批硬删除（Unscoped + LIMIT 5000，参考 v2.0.29 约定）
//      c) Step 3: ALTER TABLE ... ENGINE=InnoDB 重建释放磁盘空间（MySQL 8.0
//         对 InnoDB 的 OPTIMIZE TABLE 等价于 ALTER 重建，普通用户权限即可）
//      d) Step 4: 写清理报告 TAgentHttpTransactionCleanupReport
//   4. 容错：单张分表清理失败 → 写 status=failed 报告 + 继续下一张表，不中断
//   5. 优雅退出：通过 getAppContext() 监听 SIGINT/SIGTERM
//
// 数据流：8 张分表 × 每天 1 行报告 → 8 行/天，1 年 ≈ 2920 行可忽略
// ============================================================================

// cleanupServiceState 清理服务运行状态（用于调试 + 防止并发启动）
type cleanupServiceState struct {
	mu                    sync.Mutex
	running               bool      // 当前是否正在执行清理
	lastRunAt             time.Time // 上次成功完成时间
	lastReportCount       int       // 上次清理写入的报告数
	lastError             string    // 上次清理的错误信息（成功时为空）
	lastDurationMs        int64     // 上次清理耗时
	isEnabled             bool      // 配置 TransactionRetentionDays>0
	retentionDays         int       // v2.0.62: 当前生效的保留天数（供 /CleanupReport 页面展示）
	earliestTransactionAt time.Time // 所有浏览记录分表中最早的保存时间
	latestTransactionAt   time.Time // 所有浏览记录分表中最新的保存时间
	lastCutoffTime        time.Time // 最近一次清理使用的过期判定时间
}

var cleanupState cleanupServiceState

// InitCleanupReportTable 初始化清理报告表（AutoMigrate + 复合唯一索引）
//
// 在 main.go 启动钩子调用一次。database.DB 为 nil 时跳过（保持向后兼容）。
// 表不存在时自动创建，存在时幂等（AutoMigrate 不会破坏现有数据）。
func InitCleanupReportTable() error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化，跳过清理报告表初始化")
	}
	if err := database.DB.Table(CleanupReportTableName).AutoMigrate(&TAgentHttpTransactionCleanupReport{}); err != nil {
		return fmt.Errorf("failed to migrate %s: %w", CleanupReportTableName, err)
	}
	logger.Printf("[database.DB] Table %s ready", CleanupReportTableName)
	return nil
}

// StartTransactionCleanupService 启动后台清理 goroutine
//
// 入参为 nil 或 TransactionRetentionDays<=0 时，仅记录日志不启动 goroutine。
// 启动失败由 InitCleanupReportTable 报告；这里只负责 goroutine 生命周期。
func StartTransactionCleanupService(cfg *config.LsmTokensServerConfig) {
	if cfg == nil {
		logger.Printf("[INIT] Transaction cleanup service disabled (cfg is nil)")
		return
	}

	cleanupState.mu.Lock()
	cleanupState.isEnabled = cfg.TransactionRetentionDays > 0
	cleanupState.retentionDays = cfg.TransactionRetentionDays
	cleanupState.mu.Unlock()

	if cfg.TransactionRetentionDays <= 0 {
		logger.Printf("[INIT] Transaction cleanup service disabled (TransactionRetentionDays=%d means disabled)",
			cfg.TransactionRetentionDays)
		return
	}

	logger.Printf("[INIT] Transaction cleanup service started (retention=%d days, run_hour=%d)",
		cfg.TransactionRetentionDays, getCleanupRunHour())

	// v2.0.67: 启动时确保清理扫描索引 (created_at, id)。
	// 缺该索引时 shard_00（133GB）扫首批 1000 行就超 30s readTimeout
	// → invalid connection → 整表清理失败（生产实证 2026-07-30）。
	// 索引缺失不阻断启动：退化为「慢但可能成功」，下次重启再补建。
	if err := EnsureCleanupCreatedAtIndex(cfg.DBMysqlSubTableNumber); err != nil {
		logger.Printf("[WARNING] Failed to ensure cleanup scan index: %v", err)
	}

	go transactionCleanupLoop(cfg)
}

// transactionCleanupLoop 后台清理主循环
//
// 启动 30s 后先跑一次（让 MySQL/缓存就绪），之后每天 LSM_CLEANUP_HOUR 触发。
// 监听 getAppContext() 实现优雅退出。
//
// v2.0.67 补偿调度：单次运行写出的报告里若有 failed 分表，60s 后自动重跑一次
// （最多连续重跑 cleanupAutoRerunMaxAttempts 次）。背景：清理失败九成是
// 连接级瞬时故障（invalid connection / MySQL 短暂抖动），等 24 小时到下一天
// 才自愈，期间报告页持续挂「❌ 失败」；而失败分表的已删行数仍在累积，
// 立即重跑通常能直接清完。重跑是幂等的（报告按 cleanup_date+sub_table_index
// upsert，同一分表当天只保留最后一次结果）。
//
// 模式参考：
//   - spider_cdp_browser.go healthCheckLoop（ctx 取消 + ticker）
//   - sessionCleanupLoop（简单 ticker）
func transactionCleanupLoop(cfg *config.LsmTokensServerConfig) {
	// 首次启动 30s 后跑一次
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	consecutiveFailedRuns := 0

	for {
		select {
		case <-getAppContext().Done():
			logger.Printf("[CLEANUP] service stopping due to app context cancellation")
			return
		case <-timer.C:
			failedCount := runDailyCleanup(cfg)

			if failedCount > 0 {
				consecutiveFailedRuns++
				if consecutiveFailedRuns <= cleanupAutoRerunMaxAttempts {
					// 有分表失败：60s 后补偿重跑（瞬时连接故障大概率已恢复）
					logger.Printf("[CLEANUP] %d sub-table(s) failed; auto rerun %d/%d in %v",
						failedCount, consecutiveFailedRuns, cleanupAutoRerunMaxAttempts, cleanupAutoRerunDelay)
					timer.Reset(cleanupAutoRerunDelay)
					continue
				}
				logger.Printf("[CLEANUP] %d sub-table(s) still failing after %d auto reruns; wait for next scheduled run",
					failedCount, cleanupAutoRerunMaxAttempts)
			}
			consecutiveFailedRuns = 0

			// 计算下次执行时间（默认凌晨 3 点）
			next := nextCleanupTime()
			timer.Reset(time.Until(next))
			logger.Printf("[CLEANUP] next run scheduled at %s", next.Format("2006-01-02 15:04:05"))
		}
	}
}

// cleanupAutoRerunDelay 有分表清理失败时的自动重跑间隔。
const cleanupAutoRerunDelay = 60 * time.Second

// cleanupAutoRerunMaxAttempts 单次日程内允许的连续自动重跑上限。
//
// 超过该次数说明故障不是瞬时的（MySQL 持续不可用 / 索引缺失导致的确定性
// 超时），继续重跑只是空转，交还给下一天的正常日程（或下次服务重启）。
const cleanupAutoRerunMaxAttempts = 5

// runDailyCleanup 执行一次完整清理：遍历所有分表 + 写报告
//
// 串行处理 8 张分表（避免并发 OPTIMIZE TABLE 互相阻塞）。
// 单表失败不影响其他表继续执行（v2.0.24 启动钩子 CleanupEmptySpiderDailyInfos 同款容错）。
//
// 返回：本次运行中 status=failed 的分表数量（v2.0.67 起驱动自动补偿重跑）。
func runDailyCleanup(cfg *config.LsmTokensServerConfig) int {
	if cfg == nil || database.DB == nil {
		return 0
	}
	if cfg.TransactionRetentionDays <= 0 {
		return 0
	}

	start := time.Now()
	cleanupState.mu.Lock()
	cleanupState.running = true
	cleanupState.mu.Unlock()
	failedTables := 0
	defer func() {
		cleanupState.mu.Lock()
		cleanupState.running = false
		cleanupState.lastRunAt = time.Now()
		cleanupState.lastDurationMs = time.Since(start).Milliseconds()
		cleanupState.mu.Unlock()
		logger.Printf("[CLEANUP] daily cleanup completed in %d ms (failed_tables=%d)",
			time.Since(start).Milliseconds(), failedTables)
	}()

	cutoff := time.Now().Add(-time.Duration(cfg.TransactionRetentionDays) * 24 * time.Hour)
	today := time.Now().Format("2006-01-02")
	reportCount := 0
	runErrors := make([]string, 0)

	earliestAt, latestAt, boundaryErr := GetTransactionTimeBoundaries(cfg.DBMysqlSubTableNumber)
	cleanupState.mu.Lock()
	cleanupState.lastCutoffTime = cutoff
	cleanupState.earliestTransactionAt = earliestAt
	cleanupState.latestTransactionAt = latestAt
	cleanupState.lastError = ""
	cleanupState.mu.Unlock()
	if boundaryErr != nil {
		runErrors = append(runErrors, "query transaction time boundaries: "+boundaryErr.Error())
		logger.Printf("[WARNING] failed to query transaction time boundaries: %v", boundaryErr)
	}

	for i := 0; i < cfg.DBMysqlSubTableNumber; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			logger.Printf("[WARNING] cleanup skipped missing table %s", tableName)
			continue
		}

		reportCount++
		report := cleanupOneSubTable(tableName, i, cutoff, cfg.TransactionRetentionDays, today)
		if report.Status != "success" {
			runErrors = append(runErrors, fmt.Sprintf("%s: %s", tableName, report.ErrorMsg))
			if report.Status == "failed" {
				failedTables++
			}
		}
		if err := saveCleanupReport(&report); err != nil {
			runErrors = append(runErrors, fmt.Sprintf("save report %s: %v", tableName, err))
			logger.Printf("[WARNING] failed to write cleanup report for %s: %v", tableName, err)
			// 报告写入失败不影响清理结果（删除已生效）
		}

		logger.Printf("[CLEANUP] table=%s status=%s deleted=%d freed=%.2fMB duration=%dms%s",
			tableName, report.Status, report.DeletedRows,
			float64(report.FreedBytes)/1024/1024, report.DurationMs,
			func() string {
				if report.ErrorMsg != "" {
					return " err=" + report.ErrorMsg
				}
				return ""
			}())
	}

	cleanupState.mu.Lock()
	cleanupState.lastReportCount = reportCount
	cleanupState.lastError = strings.Join(runErrors, "; ")
	cleanupState.mu.Unlock()

	// v2.0.63: 清理完成后让分表元数据缓存过期，/CleanupReport 页面下一轮
	// 访问能立刻看到删除后的最新行数（而不是等 10 分钟 TTL 自然过期）。
	InvalidateSubTableInspector()
	return failedTables
}

func saveCleanupReport(report *TAgentHttpTransactionCleanupReport) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if report == nil {
		return fmt.Errorf("清理报告不能为空")
	}

	return database.DB.Table(CleanupReportTableName).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cleanup_date"}, {Name: "sub_table_index"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"sub_table_name",
			"deleted_rows",
			"deleted_tokens_in",
			"deleted_tokens_out",
			"deleted_tokens_all",
			"freed_bytes",
			"duration_ms",
			"cutoff_time",
			"retention_days",
			"status",
			"error_msg",
			"created_at",
		}),
	}).Create(report).Error
}

// GetTransactionTimeBoundaries 返回所有浏览记录分表的最早和最新保存时间。
// 查询只聚合 created_at，不读取浏览记录的大字段。
func GetTransactionTimeBoundaries(subTableNum int) (time.Time, time.Time, error) {
	if database.DB == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("数据库未初始化")
	}
	if subTableNum <= 0 {
		return time.Time{}, time.Time{}, nil
	}

	var earliest time.Time
	var latest time.Time
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var boundary struct {
			Earliest *time.Time `gorm:"column:earliest_at"`
			Latest   *time.Time `gorm:"column:latest_at"`
		}
		if err := database.DB.Table(tableName).
			Select("MIN(created_at) AS earliest_at, MAX(created_at) AS latest_at").
			Scan(&boundary).Error; err != nil {
			return earliest, latest, fmt.Errorf("查询 %s 时间边界失败: %w", tableName, err)
		}
		if boundary.Earliest != nil && (earliest.IsZero() || boundary.Earliest.Before(earliest)) {
			earliest = *boundary.Earliest
		}
		if boundary.Latest != nil && (latest.IsZero() || boundary.Latest.After(latest)) {
			latest = *boundary.Latest
		}
	}
	return earliest, latest, nil
}

// cleanupOneSubTable 清理单张分表：统计 → 删 → 释放空间 → 构建报告
//
// 顺序不可换：必须先 SELECT SUM 再 DELETE（删了就拿不到 Tokens）。
// 失败处理：单步失败 → 累积错误信息到 ErrorMsg，status=failed，其他表继续。
func cleanupOneSubTable(tableName string, index int, cutoff time.Time, retentionDays int, today string) TAgentHttpTransactionCleanupReport {
	start := time.Now()
	report := TAgentHttpTransactionCleanupReport{
		CleanupDate:   today,
		SubTableIndex: index,
		SubTableName:  tableName,
		CutoffTime:    cutoff,
		RetentionDays: retentionDays,
		Status:        "success",
		CreatedAt:     time.Now(),
	}

	// Step 1: 统计待删记录的 COUNT + Tokens
	//
	// 重要：`rows` 是 MariaDB 保留字，因此 SQL 别名用 `row_count`。
	//
	// v2.0.62 严重缺陷修复：这里必须写显式 `gorm:"column:..."` tag。
	//   原实现字段名为 `Rows`（无 tag），GORM NamingStrategy 会把它映射到列 `rows`，
	//   而 SQL 别名是 `row_count` —— 两者对不上，Scan 后 stats.Rows 恒为 0。
	//   结果：下方 `if stats.Rows == 0 { return }` 永远命中，Step 2 删除与
	//   Step 3 释放空间被完全跳过，清理服务上线以来从未真正删除过任何一行。
	//   （生产实证：报告表里 deleted_tokens_in 有 20 亿，deleted_rows 却是 0 ——
	//     因为 TokensIn→tokens_in 恰好映射正确，唯独行数字段错位。）
	//   四个字段一律加显式 tag，杜绝同类隐式映射再次埋雷。
	// Step 1+2 合流：v2.0.62 之前的实现先单 SQL COUNT 整张分表，对 172GB 的
	// shard_00 / 64GB 的 shard_01 会被 database.StatsDB() 25s ctx 杀掉，导致 deleted=0。
	// 现在改成 keyset 分页边扫边删（与 v2.0.58 scanShardPaged 同款模式）：
	//   - 每批 SELECT 5000 行（仅小字段，禁 longtext），ctx 友好
	//   - 累积 tokens 统计 + 立即删除该批 id
	//   - ctx 取消时返回「partial」状态与已删条数，不是失败
	deleted, tIn, tOut, tAll, partial, err := scanAndDeleteExpired(tableName, cutoff, getCleanupBatchSize())
	report.DeletedRows = deleted
	report.DeletedTokensIn = tIn
	report.DeletedTokensOut = tOut
	report.DeletedTokensAll = tAll

	if err != nil {
		report.Status = "failed"
		report.ErrorMsg = fmt.Sprintf("scan/delete failed (deleted=%d so far): %v", deleted, err)
		report.DurationMs = time.Since(start).Milliseconds()
		return report
	}

	if deleted == 0 && !partial {
		// 无过期数据：跳过重建（避免不必要的锁表）
		report.DurationMs = time.Since(start).Milliseconds()
		return report
	}

	if partial {
		report.Status = "partial"
		report.ErrorMsg = fmt.Sprintf("已删除 %d 行；本次 ctx 超时，分表下次继续清理", deleted)
	}

	// Step 3: 释放磁盘空间（ALTER TABLE ... ENGINE=InnoDB 重建）
	freedBytes, err := releaseTableSpace(tableName)
	if err != nil {
		// 删除成功但 OPTIMIZE 未完成：标记 partial，不影响其他表
		report.Status = "partial"
		report.FreedBytes = freedBytes // 即便失败也记录返回的估算值
		report.DurationMs = time.Since(start).Milliseconds()

		// v2.0.62: 区分「磁盘不足主动跳过」与「重建真的出错」。
		// 前者是预期内的安全降级（删除已生效），文案要让运维一眼看懂不是故障。
		if errors.Is(err, errSkipRebuildLowDisk) {
			report.ErrorMsg = fmt.Sprintf("已删除 %d 行并生效；%v", deleted, err)
		} else {
			report.ErrorMsg = fmt.Sprintf("step3 optimize failed (deleted=%d still applied): %v", deleted, err)
		}
		return report
	}
	report.FreedBytes = freedBytes

	report.DurationMs = time.Since(start).Milliseconds()
	return report
}

// deleteExpiredBatch 分批硬删除 created_at < cutoff 的记录
//
// 模式参考：v2.0.29 DeleteAgentHttpTransactions（Unscoped 硬删除约定）。
// 与单 SQL 批量删除的区别：
//   - 单 SQL DELETE WHERE created_at<? 可能产生大事务（百万行 → binlog 暴涨）
//   - 分批 LIMIT 5000 + 500ms sleep 让 binlog/主从跟上
//   - 5000 平衡：太大会撑大事务日志；太小会频繁 round-trip
//
// 返回：累计删除条数；中途出错返回 (n, err)。
func deleteExpiredBatch(tableName string, cutoff time.Time, batchSize int) (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if batchSize <= 0 {
		batchSize = config.DEFAULT_CLEANUP_BATCH_SIZE
	}

	var totalDeleted int64
	for {
		// 每批：先 SELECT id（仅主键，反射开销最小），再 DELETE WHERE id IN ?
		var ids []uint64
		err := database.DB.Table(tableName).
			Select("id").
			Where("created_at < ?", cutoff).
			Order("id ASC").
			Limit(batchSize).
			Scan(&ids).Error
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to fetch expired ids: %w", err)
		}
		if len(ids) == 0 {
			break
		}

		// Unscoped() 硬删除（与 CLAUDE.md / v2.0.29 一致）
		result := database.DB.Table(tableName).
			Unscoped().
			Where("id IN ?", ids).
			Delete(&TAgentHttpTransactionDataItem{})
		if result.Error != nil {
			return totalDeleted, fmt.Errorf("failed to delete batch (size=%d, deleted so far=%d): %w",
				len(ids), totalDeleted, result.Error)
		}

		totalDeleted += result.RowsAffected

		// 提前退出：本批删除数 < 期望批次大小 → 剩余可忽略
		if int64(len(ids)) < int64(batchSize) {
			break
		}

		// 限速：500ms 让 binlog/从库跟上
		time.Sleep(500 * time.Millisecond)

		// 监听 ctx 取消（优雅退出）
		ctx := getAppContext()
		if ctx != nil {
			select {
			case <-ctx.Done():
				return totalDeleted, ctx.Err()
			default:
			}
		}
	}

	return totalDeleted, nil
}

// ============================================================================
// v2.0.62: 分页删除 —— 解决大表上单 SQL COUNT 触发的连接超时
// ============================================================================
//
// 背景：Step 1 / Step 2 原实现都用单 SQL（COUNT(*) / DELETE WHERE created_at < ?）
// 对整张分表操作。生产 shard_00 = 172GB / shard_01 = 64GB，单 SQL 需要扫描
// 数千万行，被 database.StatsDB() 25s ctx（v2.0.54 防止连接泄漏）杀掉。
// v2.0.62 修复「行数为 0」缺陷后，大分表的 COUNT 又卡在 30s —— 修复
// 缺陷 ① 暴露了「不分页」的次生问题。
//
// 策略：复用 v2.0.58 `scanShardPaged` 同样的 keyset 分页模式：
//   - 每批 SELECT id + created_at + 3 个 tokens 列（O(N) 流式，不读 longtext）
//   - 增量累加行数与 tokens，扫到的 id 立即进入 DELETE 批次
//   - 主键 seek 跳过比 OFFSET 快得多，且不与并发插入错行
//
// 与原实现的差异：
//   - 不再分 Step 1（统计）和 Step 2（删除），合并为「边扫边删」单遍流式
//   - 无大表 COUNT 开销；分表大小只决定总耗时，不决定单 SQL 是否超时
//   - 30s 内能安全扫/删完一个小分表；大表则会被 ctx 取消，部分结果依然有效
// ============================================================================

// cleanupScanRow 分页扫描轻量行结构（v2.0.42 longtext 白名单契约：仅 5 个小字段）。
type cleanupScanRow struct {
	ID               uint64    `gorm:"column:id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	TokensInputSize  uint64    `gorm:"column:tokens_input_size"`
	TokensOutputSize uint64    `gorm:"column:tokens_output_size"`
	TokensAllSize    uint64    `gorm:"column:tokens_all_size"`
}

// statsCleanupScanBatch 每批扫描行数（仅 5 个小字段，不读 longtext）。
//
// 取 1000 而非 v2.0.58 的 5000：扫描本身很轻，但每扫一批就要紧接着删除这批
// （每 50 行一个子事务 + 50ms 节流），批越大单轮耗时越长、ctx 超时时丢弃的
// 已扫描工作越多。1000 行 ≈ 20 个删除子事务 ≈ 1s，粒度更细、断点更平滑。
const statsCleanupScanBatch = 1000

// cleanupScanCtxTimeout scanAndDeleteExpired 单表的 ctx 超时。
//
// 不复用 database.StatsDB() 的 25s 上限：清理是分批后台任务，25s 只够处理 ~1000 行
// 不足以完成一张大表。10 分钟能稳定覆盖单张 60-80GB 分表，且仍能通过
// context.WithTimeout 触发 MySQL KILL 与连接归还。
const cleanupScanCtxTimeout = 10 * time.Minute

// cleanupMaxConsecutiveFailures 连续扫/删失败达到该次数后放弃当前分表。
//
// 配套 cleanupRetryBackoff：「invalid connection」这类连接级故障不值得
// 在 3 秒内退避重试（池里其他连接很可能同样被污染），但瞬时的网络抖动 /
// MySQL 重启又确实可以通过稍长等待后换一条新连接恢复。3 次 × 20s ≈ 1 分钟
// 的总预算，既能扛过 ~30s 级别的抖动，又不会让后台任务无限挂起。
const cleanupMaxConsecutiveFailures = 3

// cleanupRetryBackoff 每次重试前的退避等待（同时监听 appCtx 与表级 ctx 取消）。
const cleanupRetryBackoff = 20 * time.Second

// CleanupCreatedAtIndexName 清理扫描专用的 (created_at, id) 覆盖索引名。
//
// 背景（v2.0.67 根因）：scanAndDeleteExpired 的每批 SQL 是
//
//	SELECT id, created_at, tokens_*(3列) FROM t WHERE created_at < ? ORDER BY id ASC LIMIT 1000
//
// 生产分表上没有 (created_at, id) 索引时，MySQL 只能走 created_at 的
// 二级索引做范围扫描，再对全部过期行 filesort 按 id 排序；且 tokens_* 三列
// 不在索引里，每一行都要回主键聚簇索引取数 —— 而单行含 4 个 longtext、
// 平均体积数百 KB，千行批次就是 GB 级随机 IO。实测 133GB 的 shard_00
// 扫首批 1000 行就超过 30s，被驱动 readTimeout 砍断连接（invalid connection），
// 清理服务在该表上从未成功扫过一批。
//
// (created_at, id) 覆盖索引让执行计划变为：纯索引范围扫描 + 索引内排序
// + 仅按批内 1000 个 id 回表取 tokens，首批耗时从 >30s 降到毫秒级。
// 注意：必须显式写出两列 —— GORM `index:...,priority:n` 在分表 AutoMigrate
// 场景下不稳定（v2.0.51 已有同款教训），存在性检查 + 显式 CREATE INDEX 才可靠。
const CleanupCreatedAtIndexName = "idx_cleanup_created_id"

// EnsureCleanupCreatedAtIndex 确保所有分表上存在 (created_at, id) 复合索引。
//
// 幂等：先查 information_schema.STATISTICS（SQLite 无该表时直接尝试创建并
// 吞掉 "already exists"），已存在则跳过。与 EnsureStatsCompositeIndex 同款模式。
//
// 调用时机：StartTransactionCleanupService 启动时（清理服务未启用则不创建，
// 避免给不需要清理的部署强加索引构建开销）。8 张分表串行构建，失败仅告警
// 不阻断启动 —— 缺索引时清理退化为「慢但可能成功」，不是正确性问题。
func EnsureCleanupCreatedAtIndex(subTableNum int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	useInformationSchema := !isSQLiteDialect()
	ensured := 0
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}
		// 存在性检查：
		//   - MySQL/MariaDB：information_schema.STATISTICS 精确判断，避免多余的 CREATE
		//   - SQLite：无 information_schema，直接尝试创建并吞掉 "already exists"
		//     （SQLite 索引名是库级命名空间，跨表查 STATISTICS 语义本身就不成立）
		if useInformationSchema {
			var indexCount int64
			row := database.DB.Raw(
				"SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?",
				tableName, CleanupCreatedAtIndexName,
			).Row()
			_ = row.Scan(&indexCount)
			if indexCount > 0 {
				continue
			}
		} else if sqliteHasIndex(tableName, CleanupCreatedAtIndexName) {
			continue
		}
		if err := database.DB.Exec(
			fmt.Sprintf("CREATE INDEX `%s` ON `%s` (`created_at`, `id`)",
				CleanupCreatedAtIndexName, tableName),
		).Error; err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "already exists") || strings.Contains(errStr, "Duplicate key name") {
				continue
			}
			logger.Printf("[WARNING] Failed to create index %s on %s: %v", CleanupCreatedAtIndexName, tableName, err)
			continue
		}
		ensured++
	}
	if ensured > 0 {
		logger.Printf("[database.DB] Created %s index on %d sub-table(s)", CleanupCreatedAtIndexName, ensured)
	}
	return nil
}

// sqliteHasIndex 查询 SQLite 指定表上的索引是否存在（sqlite_master 精确到 tbl_name）。
//
// 仅供 EnsureCleanupCreatedAtIndex 的 SQLite 路径使用 —— 生产是 MySQL，该函数
// 只在测试环境（内存 SQLite）被走到。查询失败按「不存在」处理，让上层走
// CREATE + 吞重名的兜底路径。
func sqliteHasIndex(tableName, indexName string) bool {
	var count int64
	if err := database.DB.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?",
		tableName, indexName,
	).Scan(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// deleteExpiredIDsPaged 按 id 列表分批硬删除（与 deleteExpiredBatch 同款，但接受预扫描得到的 ID 集合）。
//
// 复用现有 batchSize 限制 + 500ms 节流。
func deleteExpiredIDsPaged(tableName string, ids []uint64, batchSize int) (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = config.DEFAULT_CLEANUP_BATCH_SIZE
	}

	var totalDeleted int64
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		result := database.DB.Table(tableName).
			Unscoped().
			Where("id IN ?", ids[start:end]).
			Delete(&TAgentHttpTransactionDataItem{})
		if result.Error != nil {
			return totalDeleted, fmt.Errorf("delete batch [%d:%d] failed: %w", start, end, result.Error)
		}
		totalDeleted += result.RowsAffected

		// 监听 ctx 取消（优雅退出）
		ctx := getAppContext()
		if ctx != nil {
			select {
			case <-ctx.Done():
				return totalDeleted, ctx.Err()
			default:
			}
		}
		if end < len(ids) {
			// v2.0.62: 批次已降到 50 行（受 innodb_log_file_size=96MB 约束），
			// 若沿用 500ms/批，删 37K 行要 6 分钟以上纯 sleep —— 远超 ctx 预算。
			// 50 行 ≈ 22MB 的小事务用 50ms 让 binlog/从库喘口气已足够。
			time.Sleep(cleanupBatchThrottle)
		}
	}
	return totalDeleted, nil
}

// cleanupBatchThrottle 分批删除之间的节流间隔。
//
// 与 batchSize 配套：批次越小、单批压力越低，需要的间隔也越短。
// 50 行/批（≈22MB 事务）配 50ms，既让 binlog/从库有喘息，又不至于让
// 大分表的清理被 sleep 本身拖垮。
const cleanupBatchThrottle = 50 * time.Millisecond

// scanAndDeleteExpired 单遍流式：边扫边删（替代 v2.0.62 之前的「先 COUNT 再 DELETE」两段式）。
//
// 返回 (deleted_rows, tokens_in, tokens_out, tokens_all, partial, err)：
//   - deleted_rows: 实际删除的条数
//   - partial: true 表示 ctx 超时，部分行可能未处理
//   - err: 致命错误（database.DB 为空等）
//
// 上下文：自建 10 分钟 ctx，不复用 database.StatsDB() 的 25s 上限。
// 后台清理是大表上的批量工作，25s 只够处理 ~1000 行；10 分钟能稳定覆盖
// 单张 60-80GB 分表。带 ctx 取消语义（监听 getAppContext + SELECT 取消传播）
// 避免 SIGTERM 后还占用连接。
func scanAndDeleteExpired(tableName string, cutoff time.Time, batchSize int) (int64, uint64, uint64, uint64, bool, error) {
	if database.DB == nil {
		return 0, 0, 0, 0, false, fmt.Errorf("数据库未初始化")
	}

	parent := getAppContext()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, cleanupScanCtxTimeout)
	defer cancel()
	sdb := database.DB.WithContext(ctx)

	var deletedRows int64
	var tIn, tOut, tAll uint64
	var partial bool
	var lastErr error
	consecutiveFailures := 0

	for {
		// 每轮都从头查「最早的一批过期行」。
		// 不用 keyset 游标：被删的行不会再出现，ORDER BY id ASC + LIMIT
		// 自然向前推进，且不会因删除数与扫描数不一致而漏行。
		var rows []cleanupScanRow
		err := sdb.Table(tableName).
			Select("id, created_at, tokens_input_size, tokens_output_size, tokens_all_size").
			Where("created_at < ?", cutoff).
			Order("id ASC").
			Limit(statsCleanupScanBatch).
			Find(&rows).Error
		if err != nil {
			if isCtxTimeout(err) {
				partial = true
				return deletedRows, tIn, tOut, tAll, partial, nil
			}
			lastErr = err
			consecutiveFailures++
			if consecutiveFailures >= cleanupMaxConsecutiveFailures {
				return deletedRows, tIn, tOut, tAll, false, fmt.Errorf(
					"scan batch failed after %d attempts (deleted=%d so far, 已按 %v 间隔退避重试，下次运行自动继续): %w",
					consecutiveFailures, deletedRows, cleanupRetryBackoff, lastErr)
			}
			logger.Printf("[CLEANUP] %s scan attempt %d/%d failed: %v；%v 后换新连接重试",
				tableName, consecutiveFailures, cleanupMaxConsecutiveFailures, err, cleanupRetryBackoff)
			if !cleanupSleepRetryable(ctx, cleanupRetryBackoff) {
				partial = true
				return deletedRows, tIn, tOut, tAll, partial, nil
			}
			continue
		}
		if len(rows) == 0 {
			break
		}

		// 累积 tokens（即使后续删除失败，统计也是真实值）
		ids := make([]uint64, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
			tIn += rows[i].TokensInputSize
			tOut += rows[i].TokensOutputSize
			tAll += rows[i].TokensAllSize
		}

		// 立即删除这一批 id（deleteExpiredIDsPaged 内部已带 50ms 节流，
		// 本批失败重试不会比正常节奏更猛；未删掉的 id 会在下轮扫描中重新出现，
		// tokens 重复累计的上限是一个批次，对「释放空间」决策无实质影响）
		n, err := deleteExpiredIDsPaged(tableName, ids, batchSize)
		if err != nil {
			deletedRows += n
			if isCtxTimeout(err) {
				partial = true
				return deletedRows, tIn, tOut, tAll, partial, nil
			}
			lastErr = err
			consecutiveFailures++
			if consecutiveFailures >= cleanupMaxConsecutiveFailures {
				return deletedRows, tIn, tOut, tAll, false, fmt.Errorf(
					"delete batch failed after %d attempts (deleted=%d so far, 已按 %v 间隔退避重试，下次运行自动继续): %w",
					consecutiveFailures, deletedRows, cleanupRetryBackoff, lastErr)
			}
			logger.Printf("[CLEANUP] %s delete attempt %d/%d failed: %v；%v 后换新连接重试",
				tableName, consecutiveFailures, cleanupMaxConsecutiveFailures, err, cleanupRetryBackoff)
			if !cleanupSleepRetryable(ctx, cleanupRetryBackoff) {
				partial = true
				return deletedRows, tIn, tOut, tAll, partial, nil
			}
			continue
		}
		deletedRows += n
		consecutiveFailures = 0 // 一次成功的扫+删即清零失败计数

		// 注意：这里**不能**推进 lastID 游标。
		// 本批的行刚刚被物理删除，下一轮 `id > lastID AND created_at < cutoff`
		// 从 0 重新查，命中的必然是「尚未删除的下一批」——天然向前推进。
		// 若按扫描结果推进 lastID，一旦某批删除数 < 扫描数（并发写入 / 部分失败），
		// 游标就会跳过残留行，导致过期数据永远删不干净。
		//
		// 同理，也不能用 `len(rows) < batch` 提前 break：那是「本批扫到的行数」，
		// 不代表表里没有更多过期行（v2.0.62 首版实现犯过这个错，导致每次运行
		// 只删 1000 行就退出，37K 行要跑 37 次才清完）。
		// 唯一的正确终止条件是「扫不到任何过期行」，即上面的 len(rows) == 0。
		if n == 0 {
			// 防御：扫到了行却一行也没删掉（理论上不该发生），
			// 继续循环会变成死循环，这里直接判为部分完成退出。
			partial = true
			return deletedRows, tIn, tOut, tAll, partial, nil
		}
	}
	return deletedRows, tIn, tOut, tAll, false, nil
}

// isCtxTimeout 判断是否为 ctx 取消/超时错误（用于把「部分结果」与「致命错误」区分）。
func isCtxTimeout(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// cleanupSleepRetryable 可取消的重试退避等待。
//
// 返回 true = 等待完整结束（可以重试）；false = 表级 ctx 或 appCtx 已取消
// （调用方应按 partial 语义退出，不做无谓重试）。
// 不用裸 time.Sleep：10 分钟表级 ctx 或 SIGTERM 到来时必须能立刻中断等待。
func cleanupSleepRetryable(ctx context.Context, d time.Duration) bool {
	appCtx := getAppContext()
	if appCtx == nil {
		appCtx = context.Background()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	case <-appCtx.Done():
		return false
	}
}

// ============================================================================
// v2.0.62: 表重建磁盘预检守卫
// ============================================================================
//
// 背景：Step 3 的 `ALTER TABLE ... ENGINE=InnoDB` 是「全表复制重建」——
//   InnoDB 会先把整张表写成一份新的 .ibd，成功后才替换旧文件。因此重建期间
//   磁盘上同时存在新旧两份数据，需要约等于表大小的额外可用空间。
//
// 风险实证（v2.0.62 调查）：shard_00 单表 171.9GB，而磁盘仅剩 84.6GB。
//   一旦 v2.0.62 修好「删除被跳过」缺陷、删除真正生效，该表就会进入 Step 3
//   触发 172GB 重建 —— 必然写满磁盘后失败回滚，有拖垮 MySQL 与整机的风险。
//
// 策略：重建前预检可用磁盘。空间不足则**跳过重建**并返回哨兵错误，
//   但**删除结果完全不受影响**（行已归还表内空闲链表供新数据复用，
//   表不会继续膨胀，只是空间暂不归还操作系统）。
// ============================================================================

// errSkipRebuildLowDisk 磁盘空间不足以安全重建表时返回的哨兵错误。
//
// 调用方（cleanupOneSubTable）据此把报告标记为 partial 而非 failed ——
// 删除是成功的，只有可选的空间回收步骤被安全跳过。
var errSkipRebuildLowDisk = fmt.Errorf("磁盘空间不足，已跳过表重建")

// rebuildDiskSafetyMarginBytes 重建所需空间之外的额外安全余量（5GB）。
//
// 除了表副本本身，重建过程还需要空间给 online DDL 日志、临时排序文件等；
// 且必须给 MySQL 其他表的正常写入留出余地，不能把磁盘用到 0。
const rebuildDiskSafetyMarginBytes int64 = 5 * 1024 * 1024 * 1024

// getMySQLDataDir 查询 MySQL 数据目录路径（用于 statfs 探测所在文件系统）
//
// 返回空字符串表示查询失败（调用方按「无法预检」处理）。
func getMySQLDataDir() string {
	if database.DB == nil {
		return ""
	}
	var dataDir string
	if err := database.DB.Raw("SELECT @@datadir").Scan(&dataDir).Error; err != nil {
		logger.Printf("[WARNING] query @@datadir failed: %v", err)
		return ""
	}
	return strings.TrimSpace(dataDir)
}

// getTableSizeBytes 查询表在磁盘上的总大小（数据 + 索引，字节）
//
// 返回 -1 表示查询失败。
func getTableSizeBytes(tableName string) int64 {
	if database.DB == nil {
		return -1
	}
	var size int64
	err := database.DB.Raw(`
		SELECT COALESCE(DATA_LENGTH, 0) + COALESCE(INDEX_LENGTH, 0)
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = ?
		LIMIT 1
	`, tableName).Scan(&size).Error
	if err != nil {
		logger.Printf("[WARNING] query table size for %s failed: %v", tableName, err)
		return -1
	}
	return size
}

// getAvailableDiskBytes 返回指定路径所在文件系统的可用字节数
//
// 返回 -1 表示探测失败（路径为空 / statfs 报错）。
// 使用 Bavail（非特权用户可用块）而非 Bfree，与 `df` 的 Avail 列语义一致。
func getAvailableDiskBytes(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return -1
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		logger.Printf("[WARNING] statfs %s failed: %v", path, err)
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// checkDiskSpaceForRebuild 判断磁盘空间是否足够安全重建指定表
//
// 返回：
//   - enough:  true = 可以安全重建；false = 空间不足应跳过
//   - avail:   可用字节数（-1 = 探测失败）
//   - needed:  预计所需字节数（-1 = 无法估算）
//
// 判据：avail >= tableSize + 5GB 安全余量。
//
// 容错语义：任一探测环节失败（拿不到 datadir / 表大小 / statfs 报错）时返回
//
//	enough=true —— 不因「诊断能力缺失」阻断主流程，保持与 v2.0.62 之前一致的行为，
//	但会记 warning 供运维排查。
func checkDiskSpaceForRebuild(tableName string) (bool, int64, int64) {
	tableSize := getTableSizeBytes(tableName)
	if tableSize < 0 {
		return true, -1, -1 // 无法估算：放行（保持旧行为）
	}

	dataDir := getMySQLDataDir()
	avail := getAvailableDiskBytes(dataDir)
	if avail < 0 {
		return true, -1, tableSize // 无法探测磁盘：放行（保持旧行为）
	}

	needed := tableSize + rebuildDiskSafetyMarginBytes
	if avail < needed {
		return false, avail, needed
	}

	// v2.0.62 补充：磁盘够也不代表能重建成功。
	// 实测 shard_01（63.6GB）在磁盘充足时 ALTER TABLE 仍以 `invalid connection`
	// 失败 —— 重建耗时远超连接/语句超时。超大表的在线重建应交给运维在维护窗口
	// 手工执行（可配合 pt-online-schema-change），后台清理不该在这里硬扛。
	if tableSize > maxAutoRebuildTableBytes {
		return false, avail, needed
	}
	return true, avail, needed
}

// maxAutoRebuildTableBytes 后台清理允许自动重建的单表大小上限（20GB）。
//
// 超过该阈值的表，ALTER TABLE 重建耗时会超过连接超时（实测 63.6GB 的分表必失败），
// 且长时间锁表影响线上写入。这类表交由运维在维护窗口手工重建。
const maxAutoRebuildTableBytes int64 = 20 * 1024 * 1024 * 1024

// releaseTableSpace 释放表磁盘空间（通过 information_schema.TABLES DATA_FREE 估算）
//
// 实现策略：
//   - MySQL 8.0 上 InnoDB 的 OPTIMIZE TABLE 等价于 ALTER TABLE ... ENGINE=InnoDB
//   - 普通用户权限即可（无需 root / SUPER）
//   - 我们用 ALTER 重建方式（兼容性更好）
//   - 重建前后通过 information_schema.TABLES 查询 DATA_FREE 估算释放字节数
//
// 返回：释放的字节数估算（>= 0）；失败时返回 (0, error)。
//
// 注意：DATA_FREE 在 InnoDB 上是近似值（InnoDB 不严格维护空闲空间），但能反映
//
//	删除大字段后的释放效果。对于含 4 个 longtext 字段的 TAgentHttpTransactionDataItem，
//	实际释放可能更大（InnoDB 会释放未使用的 extent）。
func releaseTableSpace(tableName string) (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	// v2.0.62: 重建前安全预检 —— 磁盘不足 or 表过大都跳过，避免写满磁盘 / 超时锁表。
	// 注意：此时删除已经完成并生效，跳过的只是「把空间归还操作系统」这一可选步骤。
	if enough, avail, needed := checkDiskSpaceForRebuild(tableName); !enough {
		const gb = 1024 * 1024 * 1024
		tableSize := getTableSizeBytes(tableName)
		// 区分两种跳过原因，让运维知道该扩容磁盘还是该安排维护窗口手工重建
		reason := fmt.Sprintf("可用 %.1fGB < 需要 %.1fGB", float64(avail)/gb, float64(needed)/gb)
		if tableSize > maxAutoRebuildTableBytes {
			reason = fmt.Sprintf("表体积 %.1fGB 超过自动重建上限 %.0fGB，需运维在维护窗口手工重建",
				float64(tableSize)/gb, float64(maxAutoRebuildTableBytes)/gb)
		}
		logger.Printf("[CLEANUP] skip rebuild of %s: %s (deletion already applied)", tableName, reason)
		return 0, fmt.Errorf("%w（%s）；删除已生效，空间由 InnoDB 内部复用", errSkipRebuildLowDisk, reason)
	}

	// 1. 查询重建前的 DATA_FREE（基准）
	beforeFree := queryTableDataFree(tableName)

	// 2. ALTER TABLE 重建（释放空间）
	//    注意：直接拼接 tableName 需保证安全；项目里表名都是 fmt.Sprintf("%02d") 拼接，
	//    加上 IsTableExists 预检，不存在 SQL 注入风险。
	alterSQL := fmt.Sprintf("ALTER TABLE `%s` ENGINE=InnoDB", tableName)
	if err := database.DB.Exec(alterSQL).Error; err != nil {
		return 0, fmt.Errorf("ALTER TABLE failed for %s: %w", tableName, err)
	}

	// 3. 查询重建后的 DATA_FREE
	afterFree := queryTableDataFree(tableName)

	// 4. 计算释放字节数（beforeFree - afterFree）
	//    若 afterFree < beforeFree 表示释放了空间；若 afterFree > beforeFree 表示
	//    InnoDB 内部扩展（罕见，常见于 ALTER 重组后空闲链表重排），返回 0 不报错。
	freed := beforeFree - afterFree
	if freed < 0 {
		freed = 0
	}
	return freed, nil
}

// queryTableDataFree 查询 information_schema.TABLES 的 DATA_FREE（字节）
//
// 返回 -1 表示查询失败。DATA_FREE 是字节数。
func queryTableDataFree(tableName string) int64 {
	if database.DB == nil {
		return -1
	}
	var dataFree int64
	err := database.DB.Raw(`
		SELECT DATA_FREE
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = ?
		LIMIT 1
	`, tableName).Scan(&dataFree).Error
	if err != nil {
		logger.Printf("[WARNING] query DATA_FREE for %s failed: %v", tableName, err)
		return -1
	}
	return dataFree
}

// nextCleanupTime 计算下次清理时间
//
// 默认凌晨 LSM_CLEANUP_HOUR（默认 3 点）。若当前时间已过今日的 hour:minute，
// 则返回明天同时刻。
func nextCleanupTime() time.Time {
	hour := getCleanupRunHour()
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 30, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// getCleanupRunHour 从环境变量 LSM_CLEANUP_HOUR 读取清理触发小时（0-23）
//
// 默认 config.DEFAULT_CLEANUP_RUN_HOUR（3 点）。环境变量非法时回落默认。
func getCleanupRunHour() int {
	v := strings.TrimSpace(os.Getenv("LSM_CLEANUP_HOUR"))
	if v == "" {
		return config.DEFAULT_CLEANUP_RUN_HOUR
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 23 {
		logger.Printf("[CLEANUP] invalid LSM_CLEANUP_HOUR=%q, fallback to %d", v, config.DEFAULT_CLEANUP_RUN_HOUR)
		return config.DEFAULT_CLEANUP_RUN_HOUR
	}
	return n
}

// getCleanupBatchSize 从环境变量 LSM_CLEANUP_BATCH_SIZE 读取每批删除条数
//
// 默认 config.DEFAULT_CLEANUP_BATCH_SIZE（5000）。环境变量 < 100 时回落默认（太小无意义）。
func getCleanupBatchSize() int {
	v := strings.TrimSpace(os.Getenv("LSM_CLEANUP_BATCH_SIZE"))
	if v == "" {
		return config.DEFAULT_CLEANUP_BATCH_SIZE
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 100 {
		logger.Printf("[CLEANUP] invalid LSM_CLEANUP_BATCH_SIZE=%q, fallback to %d", v, config.DEFAULT_CLEANUP_BATCH_SIZE)
		return config.DEFAULT_CLEANUP_BATCH_SIZE
	}
	return n
}

// GetCleanupStateSnapshot 返回清理服务当前状态（用于 /CleanupReport 页面展示）
//
// 返回 snapshot 包含：enabled / retention_days / running / last_run_at / last_duration_ms /
// last_error / last_report_count / earliest_transaction_at / latest_transaction_at / last_cutoff_time
func GetCleanupStateSnapshot() map[string]interface{} {
	cleanupState.mu.Lock()
	defer cleanupState.mu.Unlock()
	return map[string]interface{}{
		"enabled":                 cleanupState.isEnabled,
		"retention_days":          cleanupState.retentionDays,
		"running":                 cleanupState.running,
		"last_run_at":             cleanupState.lastRunAt,
		"last_duration_ms":        cleanupState.lastDurationMs,
		"last_error":              cleanupState.lastError,
		"last_report_count":       cleanupState.lastReportCount,
		"earliest_transaction_at": cleanupState.earliestTransactionAt,
		"latest_transaction_at":   cleanupState.latestTransactionAt,
		"last_cutoff_time":        cleanupState.lastCutoffTime,
		"next_run_at":             nextCleanupTime(),
	}
}

// QueryCleanupReports 分页查询清理报告（管理员端 + 用户端共用）
//
// 入参：
//   - page: 页码（从 1 开始）
//   - pageSize: 每页条数（默认 20，上限 100）
//   - days: 时间筛选天数（0 = 无限制，>0 = 最近 N 天）
//
// 返回：reports / total / err
func QueryCleanupReports(page, pageSize, days int) ([]TAgentHttpTransactionCleanupReport, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("数据库未初始化")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	query := database.DB.Table(CleanupReportTableName)
	if days > 0 {
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		query = query.Where("created_at >= ?", cutoff)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计清理报告总数失败: %w", err)
	}

	if total == 0 {
		return []TAgentHttpTransactionCleanupReport{}, 0, nil
	}

	var reports []TAgentHttpTransactionCleanupReport
	err := query.
		Order("cleanup_date DESC, sub_table_index ASC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&reports).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询清理报告失败: %w", err)
	}
	return reports, total, nil
}

// GetCleanupReportsDailySummary 按天聚合清理报告（用于 ECharts 时序图）
//
// 返回：日期 → {deleted_rows_sum, freed_bytes_sum, tokens_in_sum, tokens_out_sum, tokens_all_sum}
// 按日期升序排列。
type CleanupReportsDailySummary struct {
	Date             string `json:"date"`
	DeletedRows      int64  `json:"deleted_rows"`
	FreedBytes       int64  `json:"freed_bytes"`
	DeletedTokensIn  uint64 `json:"deleted_tokens_in"`
	DeletedTokensOut uint64 `json:"deleted_tokens_out"`
	DeletedTokensAll uint64 `json:"deleted_tokens_all"`
	ReportCount      int    `json:"report_count"`
}

func GetCleanupReportsDailySummary(days int) ([]CleanupReportsDailySummary, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	query := database.DB.Table(CleanupReportTableName).
		Select(`
			cleanup_date AS date,
			COALESCE(SUM(deleted_rows), 0) AS deleted_rows,
			COALESCE(SUM(freed_bytes), 0) AS freed_bytes,
			COALESCE(SUM(deleted_tokens_in), 0) AS deleted_tokens_in,
			COALESCE(SUM(deleted_tokens_out), 0) AS deleted_tokens_out,
			COALESCE(SUM(deleted_tokens_all), 0) AS deleted_tokens_all,
			COUNT(*) AS report_count
		`)

	if days > 0 {
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		query = query.Where("created_at >= ?", cutoff)
	}

	var summaries []CleanupReportsDailySummary
	err := query.
		Group("cleanup_date").
		Order("cleanup_date ASC").
		Scan(&summaries).Error
	if err != nil {
		return nil, fmt.Errorf("聚合清理报告失败: %w", err)
	}
	return summaries, nil
}

// GetCleanupReportsTotalSummary 汇总所有清理报告的累计指标（用于 KPI 卡）
//
// 返回：total_deleted_rows / total_freed_bytes / total_tokens / total_reports / last_run_at
type CleanupReportsTotalSummary struct {
	TotalDeletedRows int64     `json:"total_deleted_rows"`
	TotalFreedBytes  int64     `json:"total_freed_bytes"`
	TotalTokensIn    uint64    `json:"total_tokens_in"`
	TotalTokensOut   uint64    `json:"total_tokens_out"`
	TotalTokensAll   uint64    `json:"total_tokens_all"`
	TotalReports     int64     `json:"total_reports"`
	LastRunAt        time.Time `json:"last_run_at"`
	LastCleanupDate  string    `json:"last_cleanup_date"`
}

func GetCleanupReportsTotalSummary() (CleanupReportsTotalSummary, error) {
	var s CleanupReportsTotalSummary
	if database.DB == nil {
		return s, fmt.Errorf("数据库未初始化")
	}

	err := database.DB.Table(CleanupReportTableName).
		Select(`
			COALESCE(SUM(deleted_rows), 0) AS total_deleted_rows,
			COALESCE(SUM(freed_bytes), 0) AS total_freed_bytes,
			COALESCE(SUM(deleted_tokens_in), 0) AS total_tokens_in,
			COALESCE(SUM(deleted_tokens_out), 0) AS total_tokens_out,
			COALESCE(SUM(deleted_tokens_all), 0) AS total_tokens_all,
			COUNT(*) AS total_reports,
			MAX(created_at) AS last_run_at,
			MAX(cleanup_date) AS last_cleanup_date
		`).
		Scan(&s).Error
	if err != nil {
		return s, fmt.Errorf("汇总清理报告失败: %w", err)
	}
	return s, nil
}

// ensure context import used (referenced via getAppContext in cleanup loop)
var _ = context.Background

// ============================================================================
// v2.0.63: 分表 Schema Inspector（/CleanupReport 页面分表统计卡片）
// ============================================================================
//
// 背景：管理员要求「像 MySQL Workbench 的 Schema Inspector 一样查看每张分表
//   的记录总数 / 数据大小 / 索引大小 / 最早与最新记录时间」，但 8 张分表合计
//   280GB+，直接 COUNT(*) 会触发慢查询把数据库拖垮（v2.0.54 已确立的教训）。
//
// 策略（与 MySQL Workbench Schema Inspector 同源）：
//   1. 默认走 information_schema.TABLES 元数据 —— TABLE_ROWS 是 InnoDB 统计
//      估算值（单查询 O(1)，毫秒级），页面显示「≈近似值」徽章明示语义。
//   2. 单表精确计数作为可选增强：前端点击「精确」按钮 → COUNT(*) 走 database.StatsDB()
//      25s ctx，超时返回 approximate + 错误文案，不卡死数据库。
//   3. 结果带 10 分钟进程内缓存：每天首次访问后台预填，前端轮询直接命中缓存。
//   4. SQLite（测试环境）没有 information_schema → 缺失表回退 COUNT(*)。
// ============================================================================

// subTableInspectorTTL 分表元数据进程内缓存有效期。
//
// information_schema.TABLES 本身是统计快照，缓存 10 分钟足以平衡「页面刷新
// 响应快」与「数字不太旧」。清理服务每次跑完后主动调 InvalidateSubTableInspector
// 让下一轮页面访问拿到最新值。
const subTableInspectorTTL = 10 * time.Minute

// SubTableInspectorInfo 单张分表的元数据快照（JSON 形状与前端契约一致）
type SubTableInspectorInfo struct {
	Index       int    `json:"index"`           // 分表序号 0-7
	TableName   string `json:"table_name"`      // TAgentHttpTransactionDataItem_%02d
	Exists      bool   `json:"exists"`          // 表是否存在
	RowCount    int64  `json:"row_count"`       // 行数（approximate=true 时为 InnoDB 估算值）
	Approximate bool   `json:"approximate"`     // true = 估算值；false = 精确 COUNT(*)
	DataBytes   int64  `json:"data_bytes"`      // DATA_LENGTH（字节）
	IndexBytes  int64  `json:"index_bytes"`     // INDEX_LENGTH（字节）
	DataFree    int64  `json:"data_free"`       // DATA_FREE（字节；InnoDB 内部空闲空间）
	AvgRowBytes int64  `json:"avg_row_bytes"`   // AVG_ROW_LENGTH（字节）
	UpdatedAt   string `json:"updated_at"`      // information_schema.UPDATE_TIME（可空）
	EarliestAt  string `json:"earliest_at"`     // MIN(created_at)（可空）
	LatestAt    string `json:"latest_at"`       // MAX(created_at)（可空）
	Error       string `json:"error,omitempty"` // 查询该表时的错误（不阻断其它表）
}

// subTableInspectorCache 进程内缓存
var subTableInspectorCache struct {
	mu      sync.Mutex
	entries []SubTableInspectorInfo
	expires time.Time
}

// InvalidateSubTableInspector 清理完成后让缓存立即过期（测试 + runDailyCleanup 调用）
func InvalidateSubTableInspector() {
	subTableInspectorCache.mu.Lock()
	subTableInspectorCache.entries = nil
	subTableInspectorCache.expires = time.Time{}
	subTableInspectorCache.mu.Unlock()
}

// GetSubTableInspector 返回所有分表的元数据快照（带 10 分钟缓存）
//
// 单条 SQL 从 information_schema.TABLES 一次拉 8 行 + 每表一条 MIN/MAX 聚合
// （走主键/created_at 索引，毫秒级）。整体开销 ≈ 10ms，不会卡死数据库。
//
// SQLite（测试环境）没有 information_schema：检测到方言为 sqlite 时直接走
// per-table COUNT(*) + MIN/MAX（测试库只有几百行，开销可忽略）。
func GetSubTableInspector(subTableNum int) ([]SubTableInspectorInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	subTableInspectorCache.mu.Lock()
	if len(subTableInspectorCache.entries) > 0 && time.Now().Before(subTableInspectorCache.expires) {
		out := make([]SubTableInspectorInfo, len(subTableInspectorCache.entries))
		copy(out, subTableInspectorCache.entries)
		subTableInspectorCache.mu.Unlock()
		return out, nil
	}
	subTableInspectorCache.mu.Unlock()

	var entries []SubTableInspectorInfo
	if isSQLiteDialect() {
		entries = buildSubTableInspectorSQLite(subTableNum)
	} else {
		entries = buildSubTableInspectorMySQL(subTableNum)
	}

	subTableInspectorCache.mu.Lock()
	subTableInspectorCache.entries = entries
	subTableInspectorCache.expires = time.Now().Add(subTableInspectorTTL)
	subTableInspectorCache.mu.Unlock()

	out := make([]SubTableInspectorInfo, len(entries))
	copy(out, entries)
	return out, nil
}

// isSQLiteDialect 判断当前 GORM 是否跑在 SQLite 驱动上（测试环境）
func isSQLiteDialect() bool {
	if database.DB == nil {
		return false
	}
	name := strings.ToLower(database.DB.Dialector.Name())
	return strings.Contains(name, "sqlite")
}

// buildSubTableInspectorMySQL 单条 information_schema 查询 + 每表 MIN/MAX（MySQL/MariaDB 路径）
func buildSubTableInspectorMySQL(subTableNum int) []SubTableInspectorInfo {
	entries := make([]SubTableInspectorInfo, 0, subTableNum)

	// 1. 单条 SQL 拉 8 张表的元数据（不存在于 information_schema 的表查不到 → 后续补 exists=false）
	type metaRow struct {
		TableName   string `gorm:"column:TABLE_NAME"`
		TableRows   int64  `gorm:"column:TABLE_ROWS"`
		DataLength  int64  `gorm:"column:DATA_LENGTH"`
		IndexLength int64  `gorm:"column:INDEX_LENGTH"`
		DataFree    int64  `gorm:"column:DATA_FREE"`
		AvgRowLen   int64  `gorm:"column:AVG_ROW_LENGTH"`
		UpdateTime  string `gorm:"column:UPDATE_TIME"`
	}
	var metas []metaRow
	if err := database.DB.Raw(`
		SELECT TABLE_NAME,
		       COALESCE(TABLE_ROWS, 0)    AS TABLE_ROWS,
		       COALESCE(DATA_LENGTH, 0)   AS DATA_LENGTH,
		       COALESCE(INDEX_LENGTH, 0)  AS INDEX_LENGTH,
		       COALESCE(DATA_FREE, 0)     AS DATA_FREE,
		       COALESCE(AVG_ROW_LENGTH,0) AS AVG_ROW_LENGTH,
		       COALESCE(CAST(UPDATE_TIME AS CHAR), '') AS UPDATE_TIME
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME LIKE 'TAgentHttpTransactionDataItem\\_%' ESCAPE '\\'
	`).Scan(&metas).Error; err != nil {
		logger.Printf("[WARNING] sub-table inspector meta query failed: %v", err)
	}

	metaByName := make(map[string]metaRow, len(metas))
	for _, m := range metas {
		metaByName[m.TableName] = m
	}

	// 2. 逐表组装 + MIN/MAX created_at（走索引，毫秒级；单表失败不阻断其它表）
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		info := SubTableInspectorInfo{
			Index:       i,
			TableName:   tableName,
			Approximate: true, // information_schema.TABLE_ROWS 是 InnoDB 估算值
		}
		m, ok := metaByName[tableName]
		if !ok || !IsTableExists(tableName) {
			info.Exists = false
			entries = append(entries, info)
			continue
		}
		info.Exists = true
		info.RowCount = m.TableRows
		info.DataBytes = m.DataLength
		info.IndexBytes = m.IndexLength
		info.DataFree = m.DataFree
		info.AvgRowBytes = m.AvgRowLen
		info.UpdatedAt = strings.TrimSpace(m.UpdateTime)
		fillSubTableTimeRange(&info)
		entries = append(entries, info)
	}
	return entries
}

// fillSubTableTimeRange 查询单表 MIN/MAX(created_at) 并写入 info（失败仅记录 error 字段）
//
// 用 string 接收聚合结果再解析：MySQL 驱动原生返回 time.Time，SQLite 驱动返回
// string —— 统一走 string 兼容两种驱动（v2.0.63 实测 SQLite 下 *time.Time 扫描
// 会报 "unsupported Scan, storing driver.Value type string"）。
func fillSubTableTimeRange(info *SubTableInspectorInfo) {
	var boundary struct {
		Earliest *string `gorm:"column:earliest_at"`
		Latest   *string `gorm:"column:latest_at"`
	}
	if err := database.DB.Table(info.TableName).
		Select("MIN(created_at) AS earliest_at, MAX(created_at) AS latest_at").
		Scan(&boundary).Error; err != nil {
		info.Error = fmt.Sprintf("时间范围查询失败: %v", err)
		logger.Printf("[WARNING] sub-table inspector time range for %s failed: %v", info.TableName, err)
		return
	}
	info.EarliestAt = normalizeInspectorTime(boundary.Earliest)
	info.LatestAt = normalizeInspectorTime(boundary.Latest)
}

// normalizeInspectorTime 把驱动返回的时间串规整为 "2006-01-02 15:04:05"；空/解析失败返回 ""。
//
// SQLite 存储 created_at 的常见形态："2026-07-28 03:30:00+08:00"、"2026-07-28T03:30:00Z"、
// 以及带小数的 RFC3339Nano；MySQL 驱动偶尔也返回原始字节串。逐格式尝试，全部失败时
// 截断到 19 个字符保底（YYYY-MM-DD HH:MM:SS 前缀），保证页面有可读值。
func normalizeInspectorTime(raw *string) string {
	if raw == nil {
		return ""
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return ""
	}
	const layout = "2006-01-02 15:04:05"
	for _, f := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		layout,
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return t.Format(layout)
		}
	}
	if len(s) >= 19 {
		return strings.Replace(s[:19], "T", " ", 1)
	}
	return s
}

// buildSubTableInspectorSQLite SQLite 路径（测试环境）：per-table COUNT(*) + MIN/MAX
//
// 测试库只有几百行，COUNT(*) 开销可忽略；Approximate=false 表示精确值。
func buildSubTableInspectorSQLite(subTableNum int) []SubTableInspectorInfo {
	entries := make([]SubTableInspectorInfo, 0, subTableNum)
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		info := SubTableInspectorInfo{
			Index:       i,
			TableName:   tableName,
			Approximate: false,
		}
		if !IsTableExists(tableName) {
			info.Exists = false
			entries = append(entries, info)
			continue
		}
		info.Exists = true
		var count int64
		if err := database.DB.Table(tableName).Count(&count).Error; err != nil {
			info.Error = fmt.Sprintf("计数失败: %v", err)
			entries = append(entries, info)
			continue
		}
		info.RowCount = count
		fillSubTableTimeRange(&info)
		entries = append(entries, info)
	}
	return entries
}

// CountSubTableRowsExact 精确计数单张分表行数（走 database.StatsDB() 25s ctx）
//
// 前端「精确」按钮调用。大表 COUNT(*) 可能跑满 25s 被 ctx 取消 —— 返回错误，
// 调用方降级为 approximate 显示并提示「表过大，精确计数超时」。
func CountSubTableRowsExact(tableName string) (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if !IsTableExists(tableName) {
		return 0, fmt.Errorf("表 %s 不存在", tableName)
	}
	sdb, cancel := database.StatsDB()
	defer cancel()
	var count int64
	if err := sdb.Table(tableName).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("精确计数失败: %w", err)
	}
	return count, nil
}
