package models

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"hash/fnv"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

var (
	agentHttpSubTableMutex sync.Mutex
)

// ============================================================================
// 主进程 context 注入
// ============================================================================
//
// 由 main.go 在启动时调用 setAppContext 注入主进程 context；
// 后台 goroutine（如 task 特征回填）通过 getAppContext 拿到可随进程退出
// 自动取消的 ctx，避免 SIGINT/SIGTERM 后残留 goroutine。
//
// 兼容：若 main.go 注入路径不存在（老启动入口），getAppContext 返回
// context.Background()，goroutine 永远不会主动取消，仅由 panic recovery /
// 自然结束兜底。
//
// ============================================================================

var (
	appContextMu sync.RWMutex
	appContext   context.Context
)

// ============================================================================
// Task 特征回填共享常量与辅助函数
// ============================================================================
//
// 原在 temp_backfill_session_id.go 中定义，被 task 特征回填复用；
// session_id 一次性补全已下线（v2.0.16b），这些符号保留作为 task 特征
// 回填的运行配置常量。
//
// ============================================================================

const (
	taskFeatureBackfillModeOff      = "off"
	taskFeatureBackfillModeEstimate = "estimate"
	taskFeatureBackfillModeHeaders  = "headers"
	taskFeatureBackfillModeBody     = "body"

	defaultTaskFeatureBackfillBatchSize    = 20
	defaultTaskFeatureBackfillBatchSleepMs = 1000
	defaultTaskFeatureBackfillBodyMaxBytes = 256 * 1024
)

// envIntDefault 从环境变量读取 int 值，缺失或非法时返回 def。
func envIntDefault(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// envUint64Default 从环境变量读取 uint64 值，缺失或非法时返回 def。
func envUint64Default(key string, def uint64) uint64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// reachedMaxRows 判断累计已处理行数是否触及 maxRows 上限。
func reachedMaxRows(done, maxRows uint64) bool {
	return maxRows > 0 && done >= maxRows
}

// remainingLimit 返回当前批次允许的最大条数，受 maxRows 收敛。
func remainingLimit(batchSize int, done, maxRows uint64) int {
	if batchSize <= 0 {
		batchSize = 1
	}
	if maxRows == 0 || done >= maxRows {
		return batchSize
	}
	remaining := maxRows - done
	if remaining < uint64(batchSize) {
		return int(remaining)
	}
	return batchSize
}

// sleepWithContext 在指定时长内睡眠，期间若 ctx 被取消则立刻返回 false。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// formatAverageBytes 把「总字节 / 行数」格式化为人类可读字符串。
func formatAverageBytes(totalBytes, rows uint64) string {
	if rows == 0 {
		return "0B"
	}
	return formatBackfillBytes(totalBytes / rows)
}

// formatBackfillBytes 把字节数格式化为人类可读字符串（B/KiB/MiB/...）。
func formatBackfillBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// setAppContext 注入主进程 context（由 main.go 调用）。
func setAppContext(ctx context.Context) {
	appContextMu.Lock()
	defer appContextMu.Unlock()
	appContext = ctx
}

// getAppContext 获取主进程 context；未注入时返回 context.Background()。
// 后台 goroutine 应在每次循环迭代中检查 ctx.Err()。
func getAppContext() context.Context {
	appContextMu.RLock()
	defer appContextMu.RUnlock()
	if appContext == nil {
		return context.Background()
	}
	return appContext
}

// GetSubTableIndex 根据用户名和模型名称计算哈希分表索引
func GetSubTableIndex(userName, modelName string, subTableNum int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userName + "_" + modelName))
	return int(h.Sum32() % uint32(subTableNum))
}

// GetAgentHttpTableName 根据用户名和模型名称生成对应的哈希分表名
func GetAgentHttpTableName(userName, modelName string, subTableNum int) string {
	index := GetSubTableIndex(userName, modelName, subTableNum)
	return fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", index)
}

// InitAgentHttpSubTables 根据配置的分表数量，自动创建所有分表
func InitAgentHttpSubTables(subTableNum int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	logger.Printf("[database.DB] Initializing %d agent HTTP sub tables...", subTableNum)
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{})
		if err != nil {
			// v2.0.51: AutoMigrate 在索引已存在时可能报 "already exists" / "Duplicate key name"，
			// 这是测试重复初始化或手动迁移后的正常现象，忽略即可（索引结构由 EnsureStatsCompositeIndex 保证）。
			errStr := err.Error()
			if strings.Contains(errStr, "already exists") || strings.Contains(errStr, "Duplicate key name") {
				logger.Printf("[database.DB] Table %s migrate skipped (index exists): %v", tableName, err)
			} else {
				return fmt.Errorf("failed to migrate table %s: %w", tableName, err)
			}
		}
		logger.Printf("[database.DB] Table %s ready", tableName)
	}
	logger.Printf("[database.DB] All %d agent HTTP sub tables initialized", subTableNum)

	// v2.0.51: 确保复合索引 idx_user_model_created 存在（AutoMigrate 在部分场景下
	// 可能不创建命名复合索引，这里显式 CREATE INDEX IF NOT EXISTS 兜底）。
	// 该索引是 /ChatAnalysisTotal 统计页面按 (user_name, model_name, created_at)
	// 过滤聚合的关键，缺失时即使只选 1 天也会全表扫描导致白屏。
	if err := EnsureStatsCompositeIndex(subTableNum); err != nil {
		logger.Printf("[WARNING] Failed to ensure stats composite index: %v", err)
	}

	// Task 特征后台回填（异步执行，不阻塞主线程启动服务）。
	// 测试环境可显式跳过，避免后台 goroutine 与 SQLite 内存库清理竞争。
	if os.Getenv("LSM_SKIP_BACKGROUND_BACKFILL") != "1" {
		go func() {
			if os.Getenv("LSM_SKIP_TASK_FEATURE_BACKFILL") == "1" {
				return
			}
			if err := runTransactionTaskFeatureBackfill(getAppContext(), subTableNum); err != nil {
				logger.Printf("[WARNING] Failed to backfill task features: %v", err)
			}
		}()
	}

	return nil
}

// StatsCompositeIndexName 统计查询复合索引名（user_name, model_name, created_at）
const StatsCompositeIndexName = "idx_user_model_created"

// EnsureStatsCompositeIndex 确保所有分表上存在 (user_name, model_name, created_at) 复合索引
// v2.0.51: /ChatAnalysisTotal 统计页面慢查询修复的关键索引。
// 幂等创建：先查 information_schema.STATISTICS 判断是否存在；不存在则 CREATE INDEX。
// 对 MySQL "Duplicate key name" / SQLite "already exists" 错误做兜底忽略，保证重复调用安全。
func EnsureStatsCompositeIndex(subTableNum int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	ensured := 0
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}
		// 先检查索引是否已存在，避免重复 CREATE INDEX 的报错
		var indexCount int64
		row := database.DB.Raw(
			"SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?",
			tableName, StatsCompositeIndexName,
		).Row()
		_ = row.Scan(&indexCount)
		if indexCount > 0 {
			continue
		}
		// 显式创建复合索引；若已存在（并发/重复调用场景）则兜底忽略
		if err := database.DB.Exec(
			fmt.Sprintf("CREATE INDEX `%s` ON `%s` (`user_name`, `model_name`, `created_at`)",
				StatsCompositeIndexName, tableName),
		).Error; err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "already exists") || strings.Contains(errStr, "Duplicate key name") {
				// 幂等兜底：索引已存在则视为成功
				continue
			}
			logger.Printf("[WARNING] Failed to create index %s on %s: %v", StatsCompositeIndexName, tableName, err)
			continue
		}
		ensured++
	}
	if ensured > 0 {
		logger.Printf("[database.DB] Created %s index on %d sub-table(s)", StatsCompositeIndexName, ensured)
	}
	return nil
}

// taskFeatureBackfillConfig 是历史 Task 特征回填的运行配置。
type taskFeatureBackfillConfig struct {
	mode         string
	batchSize    int
	batchSleepMs int
	bodyMaxBytes uint64
	maxRows      uint64
}

type taskFeatureBackfillEstimate struct {
	Rows         uint64 `gorm:"column:rows"`
	MinID        uint64 `gorm:"column:min_id"`
	MaxID        uint64 `gorm:"column:max_id"`
	RequestBytes uint64 `gorm:"column:request_bytes"`
}

func envTaskFeatureBackfillConfig() taskFeatureBackfillConfig {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("LSM_TASK_FEATURE_BACKFILL_MODE")))
	switch mode {
	case taskFeatureBackfillModeOff, taskFeatureBackfillModeEstimate, taskFeatureBackfillModeBody:
	case "":
		mode = taskFeatureBackfillModeEstimate
	default:
		logger.Printf("[database.DB] invalid LSM_TASK_FEATURE_BACKFILL_MODE=%q, fallback to estimate", mode)
		mode = taskFeatureBackfillModeEstimate
	}
	return taskFeatureBackfillConfig{
		mode:         mode,
		batchSize:    envIntDefault("LSM_TASK_FEATURE_BACKFILL_BATCH_SIZE", defaultTaskFeatureBackfillBatchSize),
		batchSleepMs: envIntDefault("LSM_TASK_FEATURE_BACKFILL_BATCH_SLEEP_MS", defaultTaskFeatureBackfillBatchSleepMs),
		bodyMaxBytes: envUint64Default("LSM_TASK_FEATURE_BACKFILL_BODY_MAX_BYTES", defaultTaskFeatureBackfillBodyMaxBytes),
		maxRows:      envUint64Default("LSM_TASK_FEATURE_BACKFILL_MAX_ROWS", 0),
	}
}

func runTransactionTaskFeatureBackfill(ctx context.Context, subTableNum int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	conf := envTaskFeatureBackfillConfig()
	if conf.mode == taskFeatureBackfillModeOff {
		logger.Printf("[database.DB] task feature backfill disabled by LSM_TASK_FEATURE_BACKFILL_MODE=off")
		return nil
	}
	if conf.mode == taskFeatureBackfillModeEstimate {
		return estimateTransactionTaskFeatures(ctx, subTableNum)
	}
	return backfillTransactionTaskFeatures(ctx, subTableNum, conf)
}

func estimateTransactionTaskFeatures(ctx context.Context, subTableNum int) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	totalRows := uint64(0)
	totalBytes := uint64(0)
	for i := 0; i < subTableNum; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}
		var stat taskFeatureBackfillEstimate
		err := database.DB.WithContext(ctx).Table(tableName).
			Select("COALESCE(MIN(id),0) AS min_id, COALESCE(MAX(id),0) AS max_id, COUNT(*) AS rows, COALESCE(SUM(request_content_length),0) AS request_bytes").
			Where("is_parsed IS NULL OR is_parsed = ? OR user_message_count IS NULL", false).
			Scan(&stat).Error
		if err != nil {
			logger.Printf("[database.DB][TASK_BACKFILL][ESTIMATE] table=%s failed: %v", tableName, err)
			continue
		}
		if stat.Rows == 0 {
			continue
		}
		totalRows += stat.Rows
		totalBytes += stat.RequestBytes
		logger.Printf("[database.DB][TASK_BACKFILL][ESTIMATE] table=%s rows=%d id_range=%d..%d request_bytes=%s avg_request=%s",
			tableName, stat.Rows, stat.MinID, stat.MaxID, formatBackfillBytes(stat.RequestBytes), formatAverageBytes(stat.RequestBytes, stat.Rows))
	}
	logger.Printf("[database.DB][TASK_BACKFILL][ESTIMATE] total_rows=%d request_bytes=%s avg_request=%s; default mode does not read request_body; set LSM_TASK_FEATURE_BACKFILL_MODE=body explicitly to backfill",
		totalRows, formatBackfillBytes(totalBytes), formatAverageBytes(totalBytes, totalRows))
	return nil
}

// backfillTransactionTaskFeatures 显式 body 模式：低速回填历史记录 Task 特征字段。
func backfillTransactionTaskFeatures(ctx context.Context, subTableNum int, conf taskFeatureBackfillConfig) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	logger.Printf("[database.DB] Backfilling task features for %d sub tables (body mode, batch_size=%d, sleep=%dms, body_max=%s, max_rows=%d)...",
		subTableNum, conf.batchSize, conf.batchSleepMs, formatBackfillBytes(conf.bodyMaxBytes), conf.maxRows)
	totalUpdated := uint64(0)
	totalBodyBytes := uint64(0)
	sleepDur := time.Duration(conf.batchSleepMs) * time.Millisecond

	for i := 0; i < subTableNum; i++ {
		if ctx.Err() != nil || reachedMaxRows(totalUpdated, conf.maxRows) {
			break
		}
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}
		var lastID uint64

		for {
			if ctx.Err() != nil || reachedMaxRows(totalUpdated, conf.maxRows) {
				break
			}
			limit := remainingLimit(conf.batchSize, totalUpdated, conf.maxRows)
			var candidates []struct {
				ID                   uint64
				RequestContentLength uint64
			}
			err := database.DB.WithContext(ctx).Table(tableName).
				Select("id, request_content_length").
				Where("(is_parsed IS NULL OR is_parsed = ? OR user_message_count IS NULL) AND id > ? AND request_content_length > 0 AND request_content_length <= ?", false, lastID, conf.bodyMaxBytes).
				Order("id ASC").
				Limit(limit).
				Scan(&candidates).Error
			if err != nil {
				logger.Printf("[WARNING] Backfill query failed for %s: %v", tableName, err)
				break
			}
			if len(candidates) == 0 {
				break
			}
			lastID = candidates[len(candidates)-1].ID

			for _, c := range candidates {
				if ctx.Err() != nil || reachedMaxRows(totalUpdated, conf.maxRows) {
					break
				}
				var record struct {
					RequestMethod string
					RequestURL    string
					RequestBody   string
				}
				err := database.DB.WithContext(ctx).Table(tableName).
					Select("request_method, request_url, request_body").
					Where("id = ?", c.ID).
					First(&record).Error
				if err != nil {
					logger.Printf("[WARNING] Backfill get record failed for %s id=%d: %v", tableName, c.ID, err)
					continue
				}
				totalBodyBytes += uint64(len(record.RequestBody))

				isTask, model, isStream, hasSystem, hasTools, msgCount, userMsgCount :=
					parseRequestBodyFeatures(record.RequestBody, record.RequestMethod, record.RequestURL)
				err = database.DB.WithContext(ctx).Table(tableName).Where("id = ?", c.ID).Updates(map[string]interface{}{
					"is_parsed":          true,
					"is_task":            isTask,
					"task_model":         model,
					"is_stream":          isStream,
					"has_system_prompt":  hasSystem,
					"has_tool_call":      hasTools,
					"message_count":      msgCount,
					"user_message_count": userMsgCount,
				}).Error
				if err != nil {
					logger.Printf("[WARNING] Backfill update failed for %s id=%d: %v", tableName, c.ID, err)
				} else {
					totalUpdated++
				}
			}
			if !sleepWithContext(ctx, sleepDur) {
				break
			}
		}
	}

	logger.Printf("[database.DB] Task feature backfill complete: %d records updated, %s body read", totalUpdated, formatBackfillBytes(totalBodyBytes))
	return ctx.Err()
}

// SaveAgentHttpTransaction 保存一次 HTTP 代理请求响应记录到哈希分表
// body: 实际转发给目标源站的请求体（可能经过协议转换）
// srcBody: 客户端原始请求体（未经协议转换），与 body 相同时可传空字符串
// respBody: 实际从目标源站接收到的响应体（可能经过协议转换）
// srcRespBody: 目标源站原始响应体（未经协议转换），与 respBody 相同时可传空字符串
func SaveAgentHttpTransaction(
	userName, modelName string,
	userID int64,
	apiKey string,
	dstEndPointID uint64,
	dstEndPointAlgorithmType int,
	dstModelName string,
	protocolType int,
	method, requestURL, remoteAddr string,
	contentLength int64,
	headers, srcHeaders, body string,
	srcBody string,
	status string,
	respContentLength int64,
	respHeaders, srcRespHeaders, respBody string,
	srcRespBody string,
	requestStartAt, requestEndAt, responseStartAt, responseEndAt time.Time,
	elapsedMs int64,
	toolIdentifier string,
	agentToolName string,
	agentToolInfo string,
	sessionID string,
	subTableNum int,
	tokensInputSize, tokensOutputSize, tokensAllSize uint64,
) error {
	agentHttpSubTableMutex.Lock()
	defer agentHttpSubTableMutex.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	// 确保目标表存在（若分表数量变更或首次写入）
	if !IsTableExists(tableName) {
		err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{})
		if err != nil {
			return fmt.Errorf("failed to auto-migrate table %s: %w", tableName, err)
		}
	}

	// 容错处理：response_body 可能包含非 UTF-8 字节（MiniMax 等模型会输出包含
	// 不可打印字符的 thinking 块 / 二进制签名等）。MySQL utf8 列直接 INSERT 会
	// 报 Error 1366，导致整条记录丢弃，/ChatAnalysis 上看不到任何记录。
	//
	// 容错处理：response_body 可能包含非 UTF-8 字节（MiniMax 等模型会输出包含
	// 不可打印字符的 thinking 块 / 二进制签名等）。MySQL utf8 列直接 INSERT 会
	// 报 Error 1366，导致整条记录丢弃，/ChatAnalysis 上看不到任何记录。
	//
	// 处理策略：
	//   1) response_body: 与 request_body 一致做 base64 编码后再存。前端页面
	//      的 decodeBody() 已经在调用 atob + TextDecoder('utf-8')，天然兼容。
	//   2) headers / 其他文本字段: 用 UTF-8 替换（U+FFFD）兜底，绝不让 INSERT 失败。
	respBodyForLog := base64.StdEncoding.EncodeToString([]byte(respBody))
	if dstEndPointAlgorithmType == 0 {
		dstEndPointAlgorithmType = DstEndPointAlgorithmType_Direct
	}

	record := &TAgentHttpTransactionDataItem{
		UserID:                     uint64(userID),
		UserName:                   userName,
		ModelName:                  modelName,
		DstModelName:               dstModelName,
		APIKey:                     apiKey,
		DstEndPointID:              dstEndPointID,
		DstEndPointAlgorithmType:   dstEndPointAlgorithmType,
		ProtocolType:               protocolType,
		RequestMethod:              method,
		RequestURL:                 requestURL,
		RequestRemoteAddr:          sanitizeUTF8(remoteAddr),
		RequestContentLength:       uint64(contentLength),
		RequestHeaders:             sanitizeUTF8(headers),
		RequestSrcProtocolHeaders:  sanitizeUTF8(srcHeaders),
		RequestBody:                body,
		RequestSrcProtocolBody:     srcBody,
		ResponseStatus:             status,
		ResponseContentLength:      uint64(respContentLength),
		ResponseHeaders:            sanitizeUTF8(respHeaders),
		ResponseSrcProtocolHeaders: sanitizeUTF8(srcRespHeaders),
		ResponseBody:               respBodyForLog,
		ResponseSrcProtocolBody:    srcRespBody,
		TokensInputSize:            tokensInputSize,
		TokensOutputSize:           tokensOutputSize,
		TokensAllSize:              tokensAllSize,
		RequestStartAt:             requestStartAt,
		RequestEndAt:               requestEndAt,
		ResponseStartAt:            responseStartAt,
		ResponseEndAt:              responseEndAt,
		ElapsedMs:                  elapsedMs,
		ToolIdentifier:             sanitizeUTF8(toolIdentifier),
		AgentToolName:              agentToolName,
		AgentToolInfo:              agentToolInfo,
	}

	// 预解析 request_body 特征，避免后续查询时重复解析大字段
	record.IsParsed = true
	record.IsTask, record.TaskModel, record.IsStream, record.HasSystemPrompt, record.HasToolCall, record.MessageCount, record.UserMessageCount =
		parseRequestBodyFeatures(body, method, requestURL)

	// 解析请求体中的 tools 字段，提取工具名称列表
	record.RequestTools = truncateRequestTools(ParseRequestToolsFromBody(body))

	// 保存识别出的 Session ID
	record.SessionID = sessionID

	err := database.DB.Table(tableName).Create(record).Error
	if err != nil {
		return fmt.Errorf("failed to save record to %s: %w", tableName, err)
	}

	// 数据写入后，使该用户+模型的统计缓存失效
	invalidateStatsCacheByUserModel(userName, modelName)

	return nil
}

// sanitizeUTF8 把字符串中所有非 UTF-8 字节替换为 U+FFFD（replacement char）。
// MySQL 默认 utf8 / utf8mb3 列对非法字节会抛 Error 1366，导致整条 INSERT 失败。
// 调用方关心的字段是 headers / remote_addr / tool_identifier 这类可能含原始客户端
// 字节的字符串 —— 把"损坏字节"替换为"�"是 loss-y 但安全的兜底，保证记录不丢。
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(0xFFFD)
			i++
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func parseRequestToolsFilter(filterTools string) []string {
	parts := strings.Split(filterTools, ",")
	tools := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		tool := strings.TrimSpace(part)
		tool = strings.Trim(tool, "'\"[] ")
		if tool == "" || seen[tool] {
			continue
		}
		tools = append(tools, tool)
		seen[tool] = true
	}
	return tools
}

func escapeSQLLikePattern(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// v2.0.42: 交易表「列表安全可 SELECT」列名常量 + 白名单契约。
//
// 为什么必须显式白名单：TAgentHttpTransactionDataItem 中包含 6 个浏览记录列表禁查字段：
//   - request_headers
//   - request_body
//   - request_src_protocol_body
//   - response_headers
//   - response_body
//   - response_src_protocol_body
//
// body 单字段约 1MB，headers 也可能很大。任何列表查询访问这些字段都会占满 IO。
//
// 约束：
//   - 列表类查询（QueryAgentHttpTransactions / GetProtocolAnalysisStats 等）：
//     必须使用下面 selectTransactionColumns() 的返回值做 Select(...)；
//   - 详情类查询：只能按 ID、按字段单列取；完整独立详情页可按 ID 取一次完整记录；
//   - 禁止新增 "全表 SELECT *" 或 "列表查询不带 Select 限定列" 的查询路径。
//
// 新增列只允许 append 到 selectTransactionColumns() 中，禁止向白名单中加入上述 6 个字段。
func selectTransactionColumns() string {
	return "id, created_at, updated_at, deleted_at, user_id, user_name, model_name, " +
		"dst_model_name, dst_endpoint_id, dst_endpoint_algorithm_type, protocol_type, " +
		"request_method, request_url, request_remote_addr, request_content_length, " +
		"response_status, response_content_length, " +
		"tokens_input_size, tokens_output_size, tokens_all_size, " +
		"request_start_at, request_end_at, response_start_at, response_end_at, " +
		"elapsed_ms, tool_identifier, request_tools, session_id, agent_tool_name, agent_tool_info"
}

// QueryAgentHttpTransactions 根据用户名和模型索引名称查询该哈希分表中的记录（支持分页和过滤）
//
// filterInputTokensNonzero / filterOutputTokensNonzero 为三态整数：
//
//	0 = 全部（不过滤）；1 = 仅保留非零（>0）；2 = 仅保留为零（=0）
func QueryAgentHttpTransactions(
	userName, modelName string,
	subTableNum, page, pageSize int,
	filterURL, filterMethod, filterStatus string,
	filterStatusNot bool,
	filterProtocolType int,
	filterDstModelName string,
	filterTools string,
	filterAgentToolName string,
	days int,
	filterInputTokensNonzero int,
	filterOutputTokensNonzero int,
) ([]TAgentHttpTransactionDataItem, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 5
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	startTime := time.Now()

	query := database.DB.Table(tableName).Where("user_name = ? AND model_name = ?", userName, modelName)
	// v2.0.41：span 编码同时支持「天」(days>0) 与「小时」(days<0)，统一走 resolveStatsSpanCutoff
	if cutoff, ok := resolveStatsSpanCutoff(days); ok {
		query = query.Where("created_at >= ?", cutoff)
	}
	if filterURL != "" {
		query = query.Where("request_url LIKE ?", "%"+filterURL+"%")
	}
	if filterMethod != "" {
		query = query.Where("request_method = ?", filterMethod)
	}
	if filterStatus != "" {
		if filterStatusNot {
			query = query.Where("response_status != ?", filterStatus)
		} else {
			query = query.Where("response_status = ?", filterStatus)
		}
	}
	if filterProtocolType > 0 {
		query = query.Where("protocol_type = ?", filterProtocolType)
	}
	if filterDstModelName != "" {
		query = query.Where("dst_model_name = ?", filterDstModelName)
	}
	if tools := parseRequestToolsFilter(filterTools); len(tools) > 0 {
		for _, tool := range tools {
			likeTool := escapeSQLLikePattern(tool)
			query = query.Where(
				"(request_tools = ? OR request_tools LIKE ? ESCAPE '\\\\' OR request_tools LIKE ? ESCAPE '\\\\' OR request_tools LIKE ? ESCAPE '\\\\')",
				tool,
				likeTool+",%",
				"%,"+likeTool+",%",
				"%,"+likeTool,
			)
		}
	}
	if filterAgentToolName != "" {
		query = query.Where("agent_tool_name = ?", filterAgentToolName)
	}
	if filterInputTokensNonzero == 1 {
		query = query.Where("tokens_input_size > 0")
	} else if filterInputTokensNonzero == 2 {
		query = query.Where("tokens_input_size = 0")
	}
	if filterOutputTokensNonzero == 1 {
		query = query.Where("tokens_output_size > 0")
	} else if filterOutputTokensNonzero == 2 {
		query = query.Where("tokens_output_size = 0")
	}

	// 查询总数
	countStart := time.Now()
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count records: %w", err)
	}
	countElapsed := time.Since(countStart)

	if total == 0 {
		logger.Printf("[database.DB] QueryAgentHttpTransactions table=%s days=%d total=0 records=0 count=%v query=0 total=%v",
			tableName, days, countElapsed, time.Since(startTime))
		return []TAgentHttpTransactionDataItem{}, total, nil
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if page > totalPages {
		page = totalPages
	}

	var records []TAgentHttpTransactionDataItem
	offset := (page - 1) * pageSize
	// 列表查询排除大字段（request_body/response_body/request_headers/response_headers），减少网络传输和内存占用
	queryStart := time.Now()
	listQuery := database.DB.Table(tableName).Where("user_name = ? AND model_name = ?", userName, modelName)
	// v2.0.41：span 编码同时支持「天」(days>0) 与「小时」(days<0)，统一走 resolveStatsSpanCutoff
	if cutoff, ok := resolveStatsSpanCutoff(days); ok {
		listQuery = listQuery.Where("created_at >= ?", cutoff)
	}
	if filterURL != "" {
		listQuery = listQuery.Where("request_url LIKE ?", "%"+filterURL+"%")
	}
	if filterMethod != "" {
		listQuery = listQuery.Where("request_method = ?", filterMethod)
	}
	if filterStatus != "" {
		if filterStatusNot {
			listQuery = listQuery.Where("response_status != ?", filterStatus)
		} else {
			listQuery = listQuery.Where("response_status = ?", filterStatus)
		}
	}
	if filterProtocolType > 0 {
		listQuery = listQuery.Where("protocol_type = ?", filterProtocolType)
	}
	if filterDstModelName != "" {
		listQuery = listQuery.Where("dst_model_name = ?", filterDstModelName)
	}
	if tools := parseRequestToolsFilter(filterTools); len(tools) > 0 {
		for _, tool := range tools {
			likeTool := escapeSQLLikePattern(tool)
			listQuery = listQuery.Where(
				"(request_tools = ? OR request_tools LIKE ? ESCAPE '\\\\' OR request_tools LIKE ? ESCAPE '\\\\' OR request_tools LIKE ? ESCAPE '\\\\')",
				tool,
				likeTool+",%",
				"%,"+likeTool+",%",
				"%,"+likeTool,
			)
		}
	}
	if filterAgentToolName != "" {
		listQuery = listQuery.Where("agent_tool_name = ?", filterAgentToolName)
	}
	if filterInputTokensNonzero == 1 {
		listQuery = listQuery.Where("tokens_input_size > 0")
	} else if filterInputTokensNonzero == 2 {
		listQuery = listQuery.Where("tokens_input_size = 0")
	}
	if filterOutputTokensNonzero == 1 {
		listQuery = listQuery.Where("tokens_output_size > 0")
	} else if filterOutputTokensNonzero == 2 {
		listQuery = listQuery.Where("tokens_output_size = 0")
	}
	// v2.0.39：使用 selectTransactionColumns() 统一白名单，禁止任何遗漏 4 个 longtext。
	result := listQuery.Select(selectTransactionColumns()).
		Order("id DESC").Limit(pageSize).Offset(offset).Find(&records)
	if result.Error != nil {
		return nil, 0, fmt.Errorf("failed to query records: %w", result.Error)
	}
	queryElapsed := time.Since(queryStart)
	totalElapsed := time.Since(startTime)

	logger.Printf("[database.DB] QueryAgentHttpTransactions table=%s days=%d total=%d records=%d count=%v query=%v total=%v",
		tableName, days, total, len(records), countElapsed, queryElapsed, totalElapsed)

	return records, total, nil
}

// GetDistinctDstModelNames 从指定用户-模型的分表中查询去重的目标模型名称列表
func GetDistinctDstModelNames(userName, modelName string, subTableNum int) ([]string, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	var names []string
	err := database.DB.Table(tableName).
		Where("user_name = ? AND model_name = ? AND dst_model_name != '' AND dst_model_name IS NOT NULL", userName, modelName).
		Distinct("dst_model_name").
		Pluck("dst_model_name", &names).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct dst model names: %w", err)
	}
	return names, nil
}

// chatAnalysisDetailFieldColumns 是 /ChatAnalysis 展开区块允许按需读取的字段白名单。
// map value 仅由服务端常量提供，禁止把客户端 field 直接传给 Select。
var chatAnalysisDetailFieldColumns = map[string]string{
	"request_headers":            "request_headers",
	"request_body":               "request_body",
	"request_src_protocol_body":  "request_src_protocol_body",
	"response_headers":           "response_headers",
	"response_body":              "response_body",
	"response_src_protocol_body": "response_src_protocol_body",
}

func ResolveChatAnalysisDetailColumn(field string) (string, bool) {
	column, ok := chatAnalysisDetailFieldColumns[field]
	return column, ok
}

// GetAgentHttpTransactionFieldByID 根据 ID 只查询一个详情字段（用于列表页点击展开后按需加载）。
func GetAgentHttpTransactionFieldByID(userName, modelName string, subTableNum int, id uint64, field string) (string, error) {
	column, ok := ResolveChatAnalysisDetailColumn(field)
	if !ok {
		return "", fmt.Errorf("unsupported detail field: %s", field)
	}
	if userName == "" || modelName == "" || id == 0 {
		return "", fmt.Errorf("user name, model name and id are required")
	}
	if database.DB == nil {
		return "", fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	var value string
	result := database.DB.Table(tableName).Select(column).
		Where("id = ? AND user_name = ? AND model_name = ?", id, userName, modelName).
		Limit(1).Scan(&value)
	if result.Error != nil {
		return "", fmt.Errorf("failed to query record field: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return "", fmt.Errorf("record not found (id=%d)", id)
	}
	if field == "request_headers" || field == "response_headers" {
		value = RedactAuthorizationBearerHeaderText(value)
	}
	return value, nil
}

// GetAgentHttpTransactionByID 根据 ID 查询单条记录
func GetAgentHttpTransactionByID(userName, modelName string, subTableNum int, id uint64) (*TAgentHttpTransactionDataItem, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	var record TAgentHttpTransactionDataItem
	// v2.0.39: 详情页按 ID 单条取（白名单契约允许）；
	// 禁止新增"不带 ID、不带 Select 的全表 First/Find"路径。
	result := database.DB.Table(tableName).Where("id = ? AND user_name = ? AND model_name = ?", id, userName, modelName).First(&record)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("record not found (id=%d)", id)
		}
		return nil, fmt.Errorf("failed to query record: %w", result.Error)
	}
	record.RequestHeaders = RedactAuthorizationBearerHeaderText(record.RequestHeaders)
	record.RequestSrcProtocolHeaders = RedactAuthorizationBearerHeaderText(record.RequestSrcProtocolHeaders)
	return &record, nil
}

// IDRangeStat ID范围段统计
type IDRangeStat struct {
	RangeStart uint64 `json:"range_start"`
	RangeEnd   uint64 `json:"range_end"`
	Count      int64  `json:"count"`
}

// TimeRangeStat 时间范围统计
type TimeRangeStat struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// GetIDRangeStats 查询指定用户在ID范围段内的统计
func GetIDRangeStats(userName, modelName string, subTableNum int, rangeSize int) ([]IDRangeStat, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if rangeSize <= 0 {
		rangeSize = 1000
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	var maxID uint64
	err := database.DB.Table(tableName).Select("COALESCE(MAX(id), 0)").Where("user_name = ? AND model_name = ?", userName, modelName).Scan(&maxID).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get max id: %w", err)
	}
	if maxID == 0 {
		return nil, nil
	}

	var stats []IDRangeStat
	for start := uint64(1); start <= maxID; start += uint64(rangeSize) {
		end := start + uint64(rangeSize) - 1
		if end > maxID {
			end = maxID
		}
		var count int64
		err := database.DB.Table(tableName).Where("user_name = ? AND model_name = ? AND id >= ? AND id <= ?", userName, modelName, start, end).Count(&count).Error
		if err != nil {
			return nil, fmt.Errorf("failed to count id range %d-%d: %w", start, end, err)
		}
		stats = append(stats, IDRangeStat{
			RangeStart: start,
			RangeEnd:   end,
			Count:      count,
		})
	}
	return stats, nil
}

// TimeStatsMaxDays 时间调用次数统计的"按小时聚合"上限天数；
// 超过此阈值时自动降级为"按天聚合"，避免返回过多桶（days=90 时 2160 桶）导致网络传输和前端渲染卡死。
// 前端 brush 选区生成报告仍可按 hour/day 颗粒度拉（走 GetTokensRangeReport），仅影响首屏概览柱图。
const TimeStatsMaxDays = 7

// GetTimeRangeStats 查询指定用户按小时/天的调用统计
// v2.0.48: days > TimeStatsMaxDays(7) 时自动降级为"按天聚合"，减少返回桶数。
// v2.0.52: 改用 Go 端聚合 — 旧实现 SELECT DATE_FORMAT(created_at)+COUNT GROUP BY
//
//	会让 MySQL 走 "Using temporary; Using filesort"，对 7 天 20K 行场景有 GROUP BY 开销。
//	新实现只 SELECT 单列 created_at，复合索引快速定位 20K 行，
//	Go 端按小时/天桶聚合。性能更好且不再依赖 DATE_FORMAT。
func GetTimeRangeStats(userName, modelName string, subTableNum int, days int) ([]TimeRangeStat, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days > 365 {
		days = 365
	}

	// 按实际颗粒度拼缓存 key，避免小时/天不同颗粒度碰撞
	granularity := "hour"
	if days > TimeStatsMaxDays {
		granularity = "day"
	}
	cacheKey := makeStatsCacheKey("GetTimeRangeStats", userName, modelName, subTableNum, days, granularity)
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if stats, valid := cached.([]TimeRangeStat); valid {
			return stats, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	// 校验表名格式，防止 SQL 注入
	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}

	goFmt := "2006-01-02 15:04"
	if granularity == "day" {
		goFmt = "2006-01-02"
	}

	// v2.0.52: 只 SELECT 单列 created_at，复合索引 (user_name, model_name, created_at)
	// 让 WHERE 走索引范围扫描，rows 大幅减少。Go 端桶聚合。
	// v2.0.54: 绑定超时 context，超时真正取消查询并释放连接。
	var createdAts []time.Time
	sdb, cancel := database.StatsDB()
	defer cancel()
	query := sdb.Table(tableName).
		Select("created_at").
		Where("user_name = ? AND model_name = ?", userName, modelName)
	if days > 0 {
		query = query.Where("created_at >= DATE_SUB(NOW(), INTERVAL ? DAY)", days)
	}

	if err := query.Find(&createdAts).Error; err != nil {
		return nil, fmt.Errorf("failed to get time range stats rows: %w", err)
	}

	// Go 端桶聚合
	bucketCounts := make(map[string]int64)
	for _, ts := range createdAts {
		key := ts.Format(goFmt)
		bucketCounts[key]++
	}

	// 补齐空槽位
	var stats []TimeRangeStat
	now := time.Now()
	if granularity == "hour" {
		startTime := now.Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Hour)
		for t := startTime; !t.After(now); t = t.Add(time.Hour) {
			key := t.Format(goFmt)
			stats = append(stats, TimeRangeStat{Date: key, Count: bucketCounts[key]})
		}
	} else {
		for i := days - 1; i >= 0; i-- {
			key := now.AddDate(0, 0, -i).Format(goFmt)
			stats = append(stats, TimeRangeStat{Date: key, Count: bucketCounts[key]})
		}
	}

	// 写入缓存
	setStatsToCache(cacheKey, stats)

	return stats, nil
}

// CountAgentHttpTransactions 根据用户名和模型索引名称查询对应哈希分表的记录条数
func CountAgentHttpTransactions(userName, modelName string, protocolType int, subTableNum int) (int64, error) {
	return CountAgentHttpTransactionsByDays(userName, modelName, protocolType, subTableNum, 0)
}

// CountAgentHttpTransactionsByDays 根据用户名、模型、协议和时间跨度查询对应哈希分表的记录条数。
// days > 0 时只统计最近 days 天；days <= 0 时不加时间条件，表示无限制。
func CountAgentHttpTransactionsByDays(userName, modelName string, protocolType int, subTableNum int, days int) (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	// 尝试从缓存获取（仅当 protocolType == 0 时缓存，因为协议类型过滤是常见场景）
	var cacheKey string
	if protocolType == 0 {
		cacheKey = makeStatsCacheKey("CountAgentHttpTransactionsByDays", userName, modelName, subTableNum, days, "")
		if cached, ok := getStatsFromCache(cacheKey); ok {
			if count, valid := cached.(int64); valid {
				return count, nil
			}
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	sdb, cancel := database.StatsDB()
	defer cancel()
	query := sdb.Table(tableName).Where("user_name = ? AND model_name = ?", userName, modelName)
	if protocolType > 0 {
		query = query.Where("protocol_type = ?", protocolType)
	}
	if cutoff, ok := resolveStatsSpanCutoff(days); ok {
		query = query.Where("created_at >= ?", cutoff)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count records in %s: %w", tableName, err)
	}

	// 写入缓存
	if protocolType == 0 && cacheKey != "" {
		setStatsToCache(cacheKey, count)
	}

	return count, nil
}

// ProtocolAnalysisStats 协议分析统计
type ProtocolAnalysisStats struct {
	// 通用统计
	MethodStats     map[string]int64 `json:"method_stats"`
	URLPatternStats map[string]int64 `json:"url_pattern_stats"`
	StatusStats     map[string]int64 `json:"status_stats"`
	AvgElapsedMs    int64            `json:"avg_elapsed_ms"`
	MinElapsedMs    int64            `json:"min_elapsed_ms"`
	MaxElapsedMs    int64            `json:"max_elapsed_ms"`
	AvgReqSize      int64            `json:"avg_req_size"`
	AvgRespSize     int64            `json:"avg_resp_size"`

	// 协议特有统计
	ModelStats      map[string]int64 `json:"model_stats"`
	StreamCount     int64            `json:"stream_count"`
	NonStreamCount  int64            `json:"non_stream_count"`
	HasSystemPrompt int64            `json:"has_system_prompt"`
	HasToolCall     int64            `json:"has_tool_call"`
	MultiTurnCount  int64            `json:"multi_turn_count"`
	SingleTurnCount int64            `json:"single_turn_count"`

	// 样本说明
	SampleCount int `json:"sample_count"`
	SampleLimit int `json:"sample_limit"`
}

// GetProtocolAnalysisStats 获取协议分析统计（仅分析最近 limit 条记录）
// 使用预解析字段，完全避免加载 request_body/response_body 等大字段
func GetProtocolAnalysisStats(userName, modelName string, subTableNum int, limit int) (*ProtocolAnalysisStats, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	// 尝试从缓存获取（固定 limit=500 的缓存，因为统计页面固定使用 500）
	var cacheKey string
	if limit == 500 {
		cacheKey = makeStatsCacheKey("GetProtocolAnalysisStats", userName, modelName, subTableNum, 0, "500")
		if cached, ok := getStatsFromCache(cacheKey); ok {
			if stats, valid := cached.(*ProtocolAnalysisStats); valid {
				return stats, nil
			}
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	type statsRecord struct {
		RequestMethod         string
		RequestURL            string
		ResponseStatus        string
		ElapsedMs             int64
		RequestContentLength  uint64
		ResponseContentLength uint64
		IsTask                bool
		TaskModel             string
		IsStream              bool
		HasSystemPrompt       bool
		HasToolCall           bool
		MessageCount          int
		UserMessageCount      int
	}
	var records []statsRecord

	sdb, cancel := database.StatsDB()
	defer cancel()
	err := sdb.Table(tableName).
		Select("request_method", "request_url", "response_status", "elapsed_ms",
			"request_content_length", "response_content_length",
			"is_task", "task_model", "is_stream", "has_system_prompt", "has_tool_call", "message_count", "user_message_count").
		Where("user_name = ? AND model_name = ?", userName, modelName).
		Order("id DESC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query protocol analysis stats: %w", err)
	}

	stats := &ProtocolAnalysisStats{
		MethodStats:     make(map[string]int64),
		URLPatternStats: make(map[string]int64),
		StatusStats:     make(map[string]int64),
		ModelStats:      make(map[string]int64),
		SampleCount:     len(records),
		SampleLimit:     limit,
		MinElapsedMs:    -1,
		MaxElapsedMs:    -1,
	}

	var totalReqSize, totalRespSize, totalElapsed int64

	for _, rec := range records {
		// HTTP 方法
		stats.MethodStats[rec.RequestMethod]++

		// URL 模式（只保留路径部分）
		urlPath := rec.RequestURL
		if idx := strings.LastIndex(urlPath, "?"); idx > 0 {
			urlPath = urlPath[:idx]
		}
		stats.URLPatternStats[urlPath]++

		// 响应状态
		stats.StatusStats[rec.ResponseStatus]++

		// 响应时间
		totalElapsed += rec.ElapsedMs
		if stats.MinElapsedMs < 0 || rec.ElapsedMs < stats.MinElapsedMs {
			stats.MinElapsedMs = rec.ElapsedMs
		}
		if stats.MaxElapsedMs < 0 || rec.ElapsedMs > stats.MaxElapsedMs {
			stats.MaxElapsedMs = rec.ElapsedMs
		}

		// 请求/响应大小
		totalReqSize += int64(rec.RequestContentLength)
		totalRespSize += int64(rec.ResponseContentLength)

		// 协议特征统计（使用预解析字段）
		if rec.TaskModel != "" {
			stats.ModelStats[rec.TaskModel]++
		}
		if rec.IsStream {
			stats.StreamCount++
		} else {
			stats.NonStreamCount++
		}
		if rec.HasSystemPrompt {
			stats.HasSystemPrompt++
		}
		if rec.HasToolCall {
			stats.HasToolCall++
		}
		if rec.UserMessageCount > 1 {
			stats.MultiTurnCount++
		} else {
			stats.SingleTurnCount++
		}
	}

	count := int64(len(records))
	if count > 0 {
		stats.AvgElapsedMs = totalElapsed / count
		stats.AvgReqSize = totalReqSize / count
		stats.AvgRespSize = totalRespSize / count
	} else {
		stats.MinElapsedMs = 0
		stats.MaxElapsedMs = 0
	}

	// 写入缓存
	if cacheKey != "" {
		setStatsToCache(cacheKey, stats)
	}

	return stats, nil
}

// isValidTableName 校验表名是否只包含合法字符（字母、数字、下划线）
func isValidTableName(tableName string) bool {
	if tableName == "" {
		return false
	}
	for i := 0; i < len(tableName); i++ {
		c := tableName[i]
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_' {
			return false
		}
	}
	return true
}

// IsTableExists 检查表是否存在（使用 GORM Migrator，兼容多种数据库）
func IsTableExists(tableName string) bool {
	if !isValidTableName(tableName) {
		log.Printf("[WARNING] Invalid table name: %s", tableName)
		return false
	}
	if database.DB == nil {
		return false
	}
	exists := database.DB.Migrator().HasTable(tableName)
	return exists
}

// ModelUsageStats 模型使用统计
type ModelUsageStats struct {
	CallCount        int64  `json:"call_count"`
	TokensAllSize    uint64 `json:"tokens_all_size"`
	TokensInputSize  uint64 `json:"tokens_input_size"`
	TokensOutputSize uint64 `json:"tokens_output_size"`
}

// ModelInfoUsageStat 模型信息页使用统计明细
type ModelInfoUsageStat struct {
	ModelName        string  `json:"model_name"`
	CallCount        int64   `json:"call_count"`
	TokensAllSize    uint64  `json:"tokens_all_size"`
	TokensInputSize  uint64  `json:"tokens_input_size"`
	TokensOutputSize uint64  `json:"tokens_output_size"`
	CallShare        float64 `json:"call_share"`
	TokenShare       float64 `json:"token_share"`
	UserCount        int64   `json:"user_count,omitempty"`
}

// ModelInfoUsageSummary 模型信息页使用统计汇总
type ModelInfoUsageSummary struct {
	ModelCount       int    `json:"model_count"`
	TotalCallCount   int64  `json:"total_call_count"`
	TokensAllSize    uint64 `json:"tokens_all_size"`
	TokensInputSize  uint64 `json:"tokens_input_size"`
	TokensOutputSize uint64 `json:"tokens_output_size"`
}

type modelInfoUsageAccumulator struct {
	ModelInfoUsageStat
	users map[string]struct{}
}

func normalizeSubTableNum(subTableNum int) int {
	if subTableNum <= 0 {
		return config.DEFAULT_SUB_TABLE_NUM
	}
	return subTableNum
}

func finalizeModelInfoUsageStats(acc map[string]*modelInfoUsageAccumulator) (*ModelInfoUsageSummary, []ModelInfoUsageStat) {
	summary := &ModelInfoUsageSummary{}
	stats := make([]ModelInfoUsageStat, 0, len(acc))
	for _, item := range acc {
		if item.ModelName == "" {
			continue
		}
		item.UserCount = int64(len(item.users))
		summary.TotalCallCount += item.CallCount
		summary.TokensAllSize += item.TokensAllSize
		summary.TokensInputSize += item.TokensInputSize
		summary.TokensOutputSize += item.TokensOutputSize
	}

	for _, item := range acc {
		if item.ModelName == "" {
			continue
		}
		stat := item.ModelInfoUsageStat
		if summary.TotalCallCount > 0 {
			stat.CallShare = float64(stat.CallCount) * 100 / float64(summary.TotalCallCount)
		}
		if summary.TokensAllSize > 0 {
			stat.TokenShare = float64(stat.TokensAllSize) * 100 / float64(summary.TokensAllSize)
		}
		stats = append(stats, stat)
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].TokensAllSize != stats[j].TokensAllSize {
			return stats[i].TokensAllSize > stats[j].TokensAllSize
		}
		if stats[i].CallCount != stats[j].CallCount {
			return stats[i].CallCount > stats[j].CallCount
		}
		return stats[i].ModelName < stats[j].ModelName
	})
	summary.ModelCount = len(stats)
	return summary, stats
}

// clampStatsDays 把 days 限制到合法范围（0=无限制；>0 限制到 365）
func ClampStatsDays(days int) int {
	if days < 0 {
		return 0
	}
	if days > 365 {
		return 365
	}
	return days
}

// v2.0.40：时间跨度统一编码为单个 int（span）。
//   - span == 0：无限制（不加时间过滤）
//   - span  > 0：最近 span 天（上限 365）
//   - span  < 0：最近 (-span) 小时（上限 -720，即 30 天等价小时数）
//
// 之所以用「负值编码小时」而不是新增字段，是为了让 /AIRouteManage 列表列既有的
// 单一 days 参数在两个 API（manager/user）与两个 database.DB 查询函数间零签名改动地贯通，
// 同时保证 makeStatsCacheKey 对小时/天产生互不冲突的缓存键。
const maxStatsSpanHours = 720 // 30 天等价小时数，防止小时值撑爆 created_at 过滤

// resolveStatsSpanCutoff 解释 span，返回 (cutoff, 是否需要过滤)。
// 需要过滤时表示只统计 created_at >= cutoff 的记录。
func resolveStatsSpanCutoff(span int) (time.Time, bool) {
	if span == 0 {
		return time.Time{}, false
	}
	if span > 0 {
		days := ClampStatsDays(span)
		if days <= 0 {
			return time.Time{}, false
		}
		return time.Now().AddDate(0, 0, -days), true
	}
	hours := -span
	if hours > maxStatsSpanHours {
		hours = maxStatsSpanHours
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour), true
}

// widerStatsSpan 在两个 span 中返回覆盖时间窗口更宽的那个（供批量聚合取最宽窗口）。
// 0（无限制）覆盖一切，其次是天（正值），最后是小时（负值）。天/小时通过换算成小时数比较。
func widerStatsSpan(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	spanHours := func(s int) int {
		if s > 0 {
			return ClampStatsDays(s) * 24
		}
		h := -s
		if h > maxStatsSpanHours {
			h = maxStatsSpanHours
		}
		return h
	}
	if spanHours(a) >= spanHours(b) {
		return a
	}
	return b
}

// applyStatsDaysWhere 给 GORM chain 加 created_at 时间过滤；days<=0 不加。
// 用 Go 端预先计算的 cutoff timestamp，避免 SQL 方言差异（MySQL DATE_SUB / SQLite julianday）。
func applyStatsDaysWhere(tx *gorm.DB, days int) *gorm.DB {
	if days > 0 {
		return tx.Where("created_at >= ?", time.Now().AddDate(0, 0, -days))
	}
	return tx
}

// DailyStat 全站单日汇总（调用次数 + Tokens），用于 ModelInfo / AgentInfo 时序折线图。
// 聚合范围为全站所有分表，不按模型/用户/Agent 维度拆分。
type DailyStat struct {
	Date         string `json:"date"`
	Count        int64  `json:"count"`
	TokensInput  uint64 `json:"tokens_input"`
	TokensOutput uint64 `json:"tokens_output"`
	TokensTotal  uint64 `json:"tokens_total"`
}

// GetDailyStatsAll 全站维度按天聚合调用次数与 Tokens，用于时序折线图。
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（最大 365）。
// 使用 Go 端按 created_at 日期分组，避免 SQL 方言差异（MySQL DATE_FORMAT / SQLite strftime），
// 与 GetModelInfoUsageStatsAll 一致的全表扫描 + Go 聚合模式。
func GetDailyStatsAll(subTableNum int, days int) ([]DailyStat, error) {
	// v2.0.57: 改走 database.StatsDB() 25s context（修复 v2.0.55 trend_chart 卡死根因 —— 8 张分表全表扫描无超时保护）
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return []DailyStat{}, nil
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsDays(days)

	acc := make(map[string]*DailyStat)
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		// v2.0.58: keyset 分页扫描，避免一次性拉全表（created_at + 3 列）。
		// acc 指针 map 在批间保持同一实例（勿每批重建）。
		err := scanShardPaged(sdb, tableName,
			"id, created_at, tokens_input_size, tokens_output_size, tokens_all_size",
			days, func(rows []shardScanRow) {
				for _, row := range rows {
					dateKey := row.CreatedAt.Format("2006-01-02")
					if existing, ok := acc[dateKey]; ok {
						existing.Count++
						existing.TokensInput += row.TokensInputSize
						existing.TokensOutput += row.TokensOutputSize
						existing.TokensTotal += row.TokensAllSize
					} else {
						acc[dateKey] = &DailyStat{
							Date:         dateKey,
							Count:        1,
							TokensInput:  row.TokensInputSize,
							TokensOutput: row.TokensOutputSize,
							TokensTotal:  row.TokensAllSize,
						}
					}
				}
			})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, fmt.Errorf("failed to get daily stats from %s: %w", tableName, err)
		}
	}

	if len(acc) == 0 {
		return []DailyStat{}, nil
	}

	results := make([]DailyStat, 0, len(acc))
	if days > 0 {
		now := time.Now()
		for i := days - 1; i >= 0; i-- {
			date := now.AddDate(0, 0, -i).Format("2006-01-02")
			if stat, ok := acc[date]; ok {
				results = append(results, *stat)
			} else {
				results = append(results, DailyStat{Date: date})
			}
		}
	} else {
		for _, stat := range acc {
			results = append(results, *stat)
		}
		sort.Slice(results, func(i, j int) bool { return results[i].Date < results[j].Date })
	}

	return results, nil
}

// GetModelInfoUsageStatsAll 管理员模型信息页统计：按全站目标模型(dst_model_name)聚合调用次数和 Tokens。
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（最大 365）。
func GetModelInfoUsageStatsAll(subTableNum int, days int) (*ModelInfoUsageSummary, []ModelInfoUsageStat, error) {
	if database.DB == nil {
		return nil, nil, fmt.Errorf("database not initialized")
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsDays(days)

	acc := make(map[string]*modelInfoUsageAccumulator)
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var rows []struct {
			ModelName        string `gorm:"column:model_name"`
			UserName         string `gorm:"column:user_name"`
			CallCount        int64  `gorm:"column:call_count"`
			TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
			TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
			TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		}

		err := applyStatsDaysWhere(database.DB.Table(tableName), days).
			Select("dst_model_name as model_name, user_name, COUNT(*) as call_count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
			Where("dst_model_name <> ''").
			Group("dst_model_name, user_name").
			Scan(&rows).Error
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get model info usage stats from %s: %w", tableName, err)
		}

		for _, row := range rows {
			modelName := strings.TrimSpace(row.ModelName)
			if modelName == "" {
				continue
			}
			item := acc[modelName]
			if item == nil {
				item = &modelInfoUsageAccumulator{
					ModelInfoUsageStat: ModelInfoUsageStat{ModelName: modelName},
					users:              make(map[string]struct{}),
				}
				acc[modelName] = item
			}
			item.CallCount += row.CallCount
			item.TokensAllSize += row.TokensAllSize
			item.TokensInputSize += row.TokensInputSize
			item.TokensOutputSize += row.TokensOutputSize
			if row.UserName != "" {
				item.users[row.UserName] = struct{}{}
			}
		}
	}

	summary, stats := finalizeModelInfoUsageStats(acc)
	return summary, stats, nil
}

// GetModelInfoUsageStatsByUser 用户模型信息页统计：按当前用户的平台模型(model_name)聚合调用次数和 Tokens。
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（最大 365）。
func GetModelInfoUsageStatsByUser(userName string, modelNames []string, subTableNum int, days int) (*ModelInfoUsageSummary, []ModelInfoUsageStat, error) {
	if database.DB == nil {
		return nil, nil, fmt.Errorf("database not initialized")
	}
	userName = strings.TrimSpace(userName)
	if userName == "" {
		return nil, nil, fmt.Errorf("user_name is required")
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsDays(days)

	seen := make(map[string]struct{})
	acc := make(map[string]*modelInfoUsageAccumulator)
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}

		tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
		if !IsTableExists(tableName) {
			continue
		}

		var row struct {
			CallCount        int64  `gorm:"column:call_count"`
			TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
			TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
			TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		}
		err := applyStatsDaysWhere(database.DB.Table(tableName), days).
			Select("COUNT(*) as call_count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
			Where("user_name = ? AND model_name = ?", userName, modelName).
			Scan(&row).Error
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get model info usage stats for %s/%s: %w", userName, modelName, err)
		}
		if row.CallCount == 0 && row.TokensAllSize == 0 && row.TokensInputSize == 0 && row.TokensOutputSize == 0 {
			continue
		}

		acc[modelName] = &modelInfoUsageAccumulator{
			ModelInfoUsageStat: ModelInfoUsageStat{
				ModelName:        modelName,
				CallCount:        row.CallCount,
				TokensAllSize:    row.TokensAllSize,
				TokensInputSize:  row.TokensInputSize,
				TokensOutputSize: row.TokensOutputSize,
				UserCount:        1,
			},
			users: map[string]struct{}{userName: {}},
		}
	}

	summary, stats := finalizeModelInfoUsageStats(acc)
	return summary, stats, nil
}

// GetModelInfoUsageStatsByUserDstModel 用户模型信息页目标模型统计：按当前用户的目标源站模型(dst_model_name)聚合调用次数和 Tokens。
// days<=0 表示无限制；days>0 仅统计 created_at 在最近 N 天内的记录（最大 365）。
func GetModelInfoUsageStatsByUserDstModel(userName string, modelNames []string, subTableNum int, days int) (*ModelInfoUsageSummary, []ModelInfoUsageStat, error) {
	if database.DB == nil {
		return nil, nil, fmt.Errorf("database not initialized")
	}
	userName = strings.TrimSpace(userName)
	if userName == "" {
		return nil, nil, fmt.Errorf("user_name is required")
	}
	subTableNum = normalizeSubTableNum(subTableNum)
	days = ClampStatsDays(days)

	seen := make(map[string]struct{})
	acc := make(map[string]*modelInfoUsageAccumulator)
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}

		tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
		if !IsTableExists(tableName) {
			continue
		}

		var rows []struct {
			ModelName        string `gorm:"column:model_name"`
			CallCount        int64  `gorm:"column:call_count"`
			TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
			TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
			TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		}
		err := applyStatsDaysWhere(database.DB.Table(tableName), days).
			Select("dst_model_name as model_name, COUNT(*) as call_count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
			Where("user_name = ? AND model_name = ? AND dst_model_name <> ''", userName, modelName).
			Group("dst_model_name").
			Scan(&rows).Error
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get dst model info usage stats for %s/%s: %w", userName, modelName, err)
		}

		for _, row := range rows {
			dstModelName := strings.TrimSpace(row.ModelName)
			if dstModelName == "" {
				continue
			}
			item := acc[dstModelName]
			if item == nil {
				item = &modelInfoUsageAccumulator{
					ModelInfoUsageStat: ModelInfoUsageStat{ModelName: dstModelName},
					users:              make(map[string]struct{}),
				}
				acc[dstModelName] = item
			}
			item.CallCount += row.CallCount
			item.TokensAllSize += row.TokensAllSize
			item.TokensInputSize += row.TokensInputSize
			item.TokensOutputSize += row.TokensOutputSize
			item.users[userName] = struct{}{}
		}
	}

	summary, stats := finalizeModelInfoUsageStats(acc)
	return summary, stats, nil
}

// GetModelUsageStatsAll 全平台维度：统计所有分表中指定模型的调用次数和Tokens（按 model_name 匹配 dst_model_name）
func GetModelUsageStatsAll(modelName string, subTableNum int) (*ModelUsageStats, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	stats := &ModelUsageStats{}
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var result struct {
			Count            int64  `gorm:"column:count"`
			TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
			TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
			TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
		}

		err := database.DB.Table(tableName).
			Select("COUNT(*) as count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
			Where("dst_model_name = ?", modelName).
			Scan(&result).Error
		if err != nil {
			continue // 忽略单表错误，继续统计其他表
		}

		stats.CallCount += result.Count
		stats.TokensAllSize += result.TokensAllSize
		stats.TokensInputSize += result.TokensInputSize
		stats.TokensOutputSize += result.TokensOutputSize
	}

	return stats, nil
}

// GetModelUsageStatsByUser 用户维度：统计指定用户的所有模型调用次数和Tokens（按 user_name + model_name 匹配）
func GetModelUsageStatsByUser(userName string, modelName string, subTableNum int) (*ModelUsageStats, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	// 用户维度的数据在特定分表中
	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !IsTableExists(tableName) {
		return &ModelUsageStats{}, nil
	}

	var result struct {
		Count            int64  `gorm:"column:count"`
		TokensAllSize    uint64 `gorm:"column:tokens_all_size"`
		TokensInputSize  uint64 `gorm:"column:tokens_input_size"`
		TokensOutputSize uint64 `gorm:"column:tokens_output_size"`
	}

	err := database.DB.Table(tableName).
		Select("COUNT(*) as count, COALESCE(SUM(tokens_all_size), 0) as tokens_all_size, COALESCE(SUM(tokens_input_size), 0) as tokens_input_size, COALESCE(SUM(tokens_output_size), 0) as tokens_output_size").
		Where("user_name = ? AND model_name = ?", userName, modelName).
		Scan(&result).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get model usage stats: %w", err)
	}

	return &ModelUsageStats{
		CallCount:        result.Count,
		TokensAllSize:    result.TokensAllSize,
		TokensInputSize:  result.TokensInputSize,
		TokensOutputSize: result.TokensOutputSize,
	}, nil
}

// ============================================================================
// v2.0.29: 浏览记录批量删除（仅管理员端）
// ============================================================================

// maxBatchDeleteIDs 单次批量删除 ID 上限
//
// 防止 WHERE id IN (...) 子句超长撞 max_allowed_packet（MySQL 默认 64MB / 单 SQL
// 限制）。本项目浏览记录 ID 为 uint64，按 20 字符 + 1 分隔符估，500 条 ~10KB，
// 远低于 64MB，但留足冗余避免极端情况。
const maxBatchDeleteIDs = 500

// DeleteAgentHttpTransaction 单条硬删除（管理员端 v2.0.29）
//
// 使用 Unscoped() 硬删除：TAgentHttpTransactionDataItem 模型虽然声明了
// gorm.DeletedAt 字段，但整个项目从未写过它（架构上把浏览记录视为流水），
// 软删反而会留下大量 dead rows 拖累后续查询。管理员清理历史异常记录时硬删更干净。
//
// 删除后立即调用 invalidateStatsCacheByUserModel 失效该 user+model 的统计缓存，
// 避免 /ChatAnalysisTotal 等统计页继续显示陈旧 count。
func DeleteAgentHttpTransaction(userName, modelName string, subTableNum int, id uint64) (int64, error) {
	if id == 0 {
		return 0, fmt.Errorf("invalid id: 0")
	}
	if userName == "" || modelName == "" {
		return 0, fmt.Errorf("invalid params: user_name and model_name are required")
	}
	if database.DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	result := database.DB.Table(tableName).Unscoped().
		Where("id = ? AND user_name = ? AND model_name = ?", id, userName, modelName).
		Delete(&TAgentHttpTransactionDataItem{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete record (id=%d): %w", id, result.Error)
	}

	// 删除后立即失效该 user+model 的统计缓存，避免 /ChatAnalysisTotal 等页面
	// 继续显示陈旧 count。
	invalidateStatsCacheByUserModel(userName, modelName)

	return result.RowsAffected, nil
}

// DeleteAgentHttpTransactions 批量硬删除（管理员端 v2.0.29）
//
// 单条 SQL `WHERE id IN ? AND user_name = ? AND model_name = ?` + Unscoped()，
// 不循环调用单条删除（v2.0.24 SpiderDailyInfo 的循环删除是 N+1 必要妥协，
// 本场景管理员跳过权限预检、无 N+1 风险）。
//
// 返回 RowsAffected：被实际删除的行数。管理员端无权限概念，仅区分"已删除"与"不存在"
// 两桶：skippedNotFound = len(req.IDs) - deleted。
//
// 检查顺序：参数校验（含 len(ids) 与 maxBatchDeleteIDs 上限）先于 database.DB 访问，
// 这样在无 database.DB 环境下也能精准测试参数校验路径。
func DeleteAgentHttpTransactions(userName, modelName string, subTableNum int, ids []uint64) (int64, error) {
	if userName == "" || modelName == "" {
		return 0, fmt.Errorf("invalid params: user_name and model_name are required")
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) > maxBatchDeleteIDs {
		return 0, fmt.Errorf("too many ids: %d (max %d)", len(ids), maxBatchDeleteIDs)
	}
	if database.DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	result := database.DB.Table(tableName).Unscoped().
		Where("id IN ? AND user_name = ? AND model_name = ?", ids, userName, modelName).
		Delete(&TAgentHttpTransactionDataItem{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to batch delete (count=%d): %w", len(ids), result.Error)
	}

	invalidateStatsCacheByUserModel(userName, modelName)

	return result.RowsAffected, nil
}

// ============================================================================
// v2.0.69: /AIRouteManage「最后响应状态」列
// ============================================================================
//
// 根因:
//   - 用户反馈「最后使用」列右侧需要新增一列「最后响应状态」，展示最后一条浏览记录的
//     HTTP 响应是否正常（基于 TAgentHttpTransactionDataItem.ResponseStatus 字段）。
//   - 该字段是 string（size:50;index），存的是 http.Response.Status 完整文本
//     （如 "200 OK"、"500 Internal Server Error"），不是裸 int 也不是 bool。
//
// 设计决策（合并查询，非独立批量函数）:
//   - 不另起一轮 BatchGetRouteLastResponseStatuses —— 那样会引入两轮 round-trip
//     浪费 database.DB 连接，且两轮之间若插入新记录，会产生「last_used 来自记录 A、
//     last_response_status 来自记录 B」的不一致。
//   - 改为扩展 GetRouteLastUsedTime 的等价单条函数 getRouteLastRecord：单 SQL
//     SELECT created_at, response_status FROM ... ORDER BY id DESC LIMIT 1，
//     保证 last_used 与 last_response_status 严格来自同一行。
//   - response_status 是 VARCHAR(50)，多 50B IO 远小于另起一轮的 16ms 网络 RTT。
//
// 强制规则:
//   - getRouteLastRecord 必须走 database.StatsDB() 25s context。
//   - cacheKey 前缀必须独立 "GetRouteLastRecord"，禁止污染
//     GetRouteLastUsedTime 的 time.Time 单字段缓存。
//   - 「查询失败」与「从未使用」必须用 LastResponseStatusFailed 区分，禁止把
//     数据库故障静默降级成「异常响应」或「未使用」。

// parseResponseStatusCode 解析 http.Response.Status 文本首段三位数字状态码。
//
// 兼容三种典型格式：
//   - "200"                 → 200
//   - "200 OK"              → 200
//   - "500 Internal Server Error" → 500
//
// 解析规则：从左到右扫描首个连续三位数字 token；找不到或数字不合法返回 0。
// 调用方约定 0 = 无记录/未使用/不可解析（与 LastResponseStatusCode 的零值语义一致）。
func ParseResponseStatusCode(s string) int {
	if s == "" {
		return 0
	}
	// 跳过前导空白（罕见，但 http.Status 文本可能含）
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	end := i
	for end < len(s) && end-i < 3 && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end-i != 3 {
		return 0
	}
	// 必须正好 3 位数字，避免 "2000" 误识别为 200
	if end < len(s) && s[end] >= '0' && s[end] <= '9' {
		// 超过 3 位数字 → 非合法状态码（HTTP 状态码标准范围 100-599）
		return 0
	}
	code := int(s[i]-'0')*100 + int(s[i+1]-'0')*10 + int(s[i+2]-'0')
	if code < 100 || code > 599 {
		return 0
	}
	return code
}

// isResponseSuccess 判断整数 HTTP 状态码是否在 2xx 成功区间。
// 仅 200<=code<300 视为成功；其它（4xx/5xx）视为异常；0/100/199 等均视为失败。
func isResponseSuccess(code int) bool {
	return code >= 200 && code < 300
}

// lastRecordRow getRouteLastRecordByStatus 单条查询的内部结果类型。
// 仅在批量链路内部使用，不导出 —— 调用方应通过 RouteBatchStatResult 的
// LastSuccess*/LastFailure* 公开字段访问结果。
type lastRecordRow struct {
	CreatedAt      time.Time
	ResponseStatus string
	DstModelName   string
}

// getRouteLastRecordByStatus 查询指定 user+model+protocol 在浏览记录分表中
// 「最后一条成功 / 失败响应」的 (created_at, response_status, dst_model_name)。
//
// v2.0.71：/AIRouteManage「最后成功记录」「最后失败记录」两列的数据源。
// 成功/失败判定在 SQL 层完成（LIKE 模式走参数化占位符）：
//   - success=true:  AND response_status LIKE '2%'      （2xx 成功区间）
//   - success=false: AND response_status NOT LIKE '2%'  （含空串 —— 传输层错误
//     写库时 response_status 为空，与 isResponseSuccess(0)=false 语义一致，归入失败）
//
// 性能要点全盘复用 v2.0.69 getRouteLastRecord：
//   - isValidTableName 校验 + fmt.Sprintf 拼接表名；
//   - database.StatsDB() 25s context，禁止裸 database.DB.Raw；
//   - statsCache 5 分钟 TTL（独立前缀 "GetRouteLastRecordByStatus" + success 标志
//     作为 extra 段，不污染任何旧缓存 key）；
//   - panic 兜底由 BatchGetRouteLastUsedTimes 的 recover() 负责。
//
// 执行计划：WHERE 命中 (user_name, model_name[, protocol_type]) 索引前缀，
// ORDER BY id DESC LIMIT 1 沿主键倒序扫描到首个满足 LIKE 条件的行即停。
// 路由有正常流量时命中极快；结果进 5 分钟缓存，与旧列同级开销。
//
// 返回:
//   - row: CreatedAt 为零值表示该 (user, model, protocol) 在分表中无满足条件的
//     记录（"暂无成功/失败记录"语义，非故障）。
//   - err: 仅在 SQL 执行失败时返回；err != nil 时 row 应整体丢弃（Batch 上层
//     标记 LastSuccessFailed/LastFailureFailed=true）。
func GetRouteLastRecordByStatus(userName, modelName string, protocolType int, subTableNum int, success bool) (lastRecordRow, error) {
	if database.DB == nil {
		return lastRecordRow{}, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	statusExtra := "failure"
	statusCond := "AND response_status NOT LIKE ?"
	if success {
		statusExtra = "success"
		statusCond = "AND response_status LIKE ?"
	}

	cacheKey := makeStatsCacheKey("GetRouteLastRecordByStatus", userName, modelName, subTableNum, protocolType, statusExtra)
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if r, valid := cached.(lastRecordRow); valid {
			return r, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !isValidTableName(tableName) {
		return lastRecordRow{}, fmt.Errorf("invalid table name: %s", tableName)
	}

	// LIKE 模式走参数化占位符：既避免 fmt.Sprintf 把 '2%' 的 % 当动词，
	// 也让 SQL 形态与参数彻底分离（与其它统计查询的防护约定一致）。
	var query string
	var args []interface{}
	if protocolType > 0 {
		query = fmt.Sprintf(
			"SELECT created_at, response_status, dst_model_name FROM %s WHERE user_name = ? AND model_name = ? AND protocol_type = ? "+statusCond+" ORDER BY id DESC LIMIT 1",
			tableName,
		)
		args = []interface{}{userName, modelName, protocolType, "2%"}
	} else {
		query = fmt.Sprintf(
			"SELECT created_at, response_status, dst_model_name FROM %s WHERE user_name = ? AND model_name = ? "+statusCond+" ORDER BY id DESC LIMIT 1",
			tableName,
		)
		args = []interface{}{userName, modelName, "2%"}
	}

	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return lastRecordRow{}, fmt.Errorf("database not initialized")
	}

	row := sdb.Raw(query, args...).Row()
	var r lastRecordRow
	if err := row.Scan(&r.CreatedAt, &r.ResponseStatus, &r.DstModelName); err != nil {
		if err == gorm.ErrRecordNotFound || err.Error() == "sql: no rows in result set" {
			setStatsToCache(cacheKey, r) // 缓存零值（与 GetRouteLastUsedTime 一致）
			return r, nil
		}
		return lastRecordRow{}, fmt.Errorf("failed to query last %s record from %s: %w", statusExtra, tableName, err)
	}

	setStatsToCache(cacheKey, r)
	return r, nil
}

// GetRouteLastUsedTime 查询指定 user+model+protocol 在浏览记录分表中最后一次
// 代理请求发生的时间（created_at 最大值）。
//
// 用于 /AIRouteManage 页面「路由配对列表」展示每条路由最后被实际调用的时间。
//
// 性能优化要点：
//  1. 用 user_name+model_name 哈希定位唯一一张分表（GetAgentHttpTableName），
//     单表内执行 SELECT created_at FROM ... WHERE user_name=? AND model_name=?
//     [AND protocol_type=?] ORDER BY id DESC LIMIT 1；
//  2. ORDER BY id DESC 而非 created_at DESC：id 是主键且单调递增，主键倒序
//     LIMIT 1 在 InnoDB 上是直接定位聚簇索引最右叶子节点（无需 filesort，
//     也无需扫 created_at 上的二级索引），单行 O(1) 返回；
//  3. 仅 SELECT created_at 一列，跳过整行反射 / 模型实例化；
//  4. 用原生 database.DB.Raw + Row().Scan(time.Time) 替代 GORM Find，避免 100+ 字段
//     反射 + 类型绑定 + soft-delete 自动追加等开销；
//  5. 复用 statsCache（5 分钟 TTL）按 (userName, modelName, subTableNum, protocolType)
//     维度做内存缓存，避免每次列表刷新都对同一对 (user, model) 重复扫描。
//     写入新事务时已有 invalidateStatsCacheByUserModel 兜底失效。
//
// protocolType<=0 表示不限协议（与路由列表语义一致：同 (user, model) 可能存在
// Anthropic + OpenAI 两条路由，protocolType=1/2 才会区分）。
func GetRouteLastUsedTime(userName, modelName string, protocolType int, subTableNum int) (time.Time, error) {
	if database.DB == nil {
		return time.Time{}, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	cacheKey := makeStatsCacheKey("GetRouteLastUsedTime", userName, modelName, subTableNum, protocolType, "")
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if ts, valid := cached.(time.Time); valid {
			return ts, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !isValidTableName(tableName) {
		return time.Time{}, fmt.Errorf("invalid table name: %s", tableName)
	}

	// 原生 SQL：单列 + 主键倒序 LIMIT 1，最快路径。表名已经 isValidTableName 校验过，
	// 直接 Sprintf 拼接到 SQL；user_name / model_name / protocol_type 走参数化占位符。
	var ts time.Time
	var query string
	var args []interface{}
	if protocolType > 0 {
		query = fmt.Sprintf(
			"SELECT created_at FROM %s WHERE user_name = ? AND model_name = ? AND protocol_type = ? ORDER BY id DESC LIMIT 1",
			tableName,
		)
		args = []interface{}{userName, modelName, protocolType}
	} else {
		query = fmt.Sprintf(
			"SELECT created_at FROM %s WHERE user_name = ? AND model_name = ? ORDER BY id DESC LIMIT 1",
			tableName,
		)
		args = []interface{}{userName, modelName}
	}

	// v2.0.66：走 database.StatsDB() 绑定 25s context。旧实现用裸 database.DB.Raw，超时只能等
	// 驱动 readTimeout(30s) 砍断 socket，连接被标记 invalid 后污染连接池。
	sdb, cancel := database.StatsDB()
	defer cancel()
	if sdb == nil {
		return time.Time{}, fmt.Errorf("database not initialized")
	}

	row := sdb.Raw(query, args...).Row()
	if err := row.Scan(&ts); err != nil {
		if err == gorm.ErrRecordNotFound || err.Error() == "sql: no rows in result set" {
			setStatsToCache(cacheKey, ts) // 缓存零值
			return ts, nil
		}
		return time.Time{}, fmt.Errorf("failed to query last used time from %s: %w", tableName, err)
	}

	setStatsToCache(cacheKey, ts)
	return ts, nil
}

// ============================================================================
// v2.0.66: /AIRouteManage「最后使用」列恒显示「未使用」修复
// ============================================================================
//
// 根因（三层叠加，缺一不成灾）:
//  1. list action 传 Days=0（无限制），BatchGetRouteStatsByRouteIDs 拼出的
//     `MAX(created_at) ... GROUP BY` 没有 created_at 下界，无法利用复合索引
//     idx_user_model_created，EXPLAIN 实测 73560 行 + Using temporary;
//     Using filesort；而「时间跨度统计」列走独立 batch_stats XHR、days=3，
//     能吃到索引范围扫描 —— 这就是「统计有数、最后使用没数」的直接原因。
//  2. 该查询用裸 database.DB.Raw 且无 context，超时只能等驱动 readTimeout(30s) 砍断
//     socket，日志实证 [30022ms] + `invalid connection`（19 次）。
//  3. 失败后 `continue` 丢弃整个分表 → 收尾循环给缺失路由补零值 →
//     enrichRoute 只看 IsZero() → 渲染「未使用」，把数据库故障伪装成正常状态。
//
// 修复策略：把 last_used 从 COUNT(*) 聚合里拆出来单独查。
// `ORDER BY id DESC LIMIT 1` 走 idx_user_model_id_desc，EXPLAIN rows=1，
// 实测 16ms vs 聚合版 179ms；配合有界并发，20 条路由整体 < 50ms。
//
// 关键取舍：这部分回退了 v2.0.39「合并查询消灭 N+1」的思路 —— 但当初的 N+1
// 是 20 条快查询，合并后变成 8 条会超时的慢查询，得不偿失。此处保留批量入口
// （调用方仍是一次调用），内部用有界并发跑快查询，两者的好处都拿到。

// batchLastUsedConcurrency 批量查 last_used 时的最大并发。
// 控制在个位数：单条查询 ~16ms，8 并发足以让 20 条路由在 50ms 内返回，
// 同时不与代理热路径抢 MySQL 连接（连接池上限 100）。
const batchLastUsedConcurrency = 8

// BatchGetRouteLastUsedTimes 批量查询每条路由「最后成功记录」与「最后失败记录」。
//
// 与 BatchGetRouteStatsByRouteIDs 的分工：
//   - 本函数只管最后成功/失败两条记录，走 ORDER BY id DESC LIMIT 1 快路径；
//   - BatchGetRouteStatsByRouteIDs 只管记录数，走 COUNT(*) GROUP BY 聚合。
//
// 按 (user_name, model_name, protocol_type) 去重后并发查询，同一 key 的多条
// 路由共享一次查询结果。查询失败的路由标记 LastSuccessFailed/LastFailureFailed=true，
// 供上层区分「暂无记录」与「查询失败」——禁止再把失败静默降级成零值。
//
// v2.0.71：由「整体最后一行」重构为「成功/失败各一行」（每 key 两次
// getRouteLastRecordByStatus），函数名保留以兼容调用方。
func BatchGetRouteLastUsedTimes(items []RouteBatchStatItem, subTableNum int) map[uint64]RouteBatchStatResult {
	result := make(map[uint64]RouteBatchStatResult, len(items))
	if len(items) == 0 {
		return result
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if len(items) > BatchRouteStatsKeyPairMax {
		items = items[:BatchRouteStatsKeyPairMax]
	}

	// 去重：同一 (user, model, protocol) 只查一次，结果回填给所有关联路由。
	// 同模型的 Anthropic / OpenAI 两条路由 protocol 不同，天然各查各的，
	// 不会像 v2.0.44 聚合路径那样共享同一个时间戳（协议扇出 bug）。
	type luKey struct {
		user     string
		model    string
		protocol int
	}
	keyRoutes := map[luKey][]uint64{}
	for _, it := range items {
		if it.RouteID == 0 {
			continue
		}
		if it.Key.UserName == "" || it.Key.ModelName == "" {
			// user/model 缺失无法定位分表，直接标记为查询失败：
			// 这通常意味着 lookupRouteModelName 没解析出模型名，属于真实故障，
			// 不能伪装成「暂无记录」。
			result[it.RouteID] = RouteBatchStatResult{RouteID: it.RouteID, LastSuccessFailed: true, LastFailureFailed: true}
			continue
		}
		k := luKey{user: it.Key.UserName, model: it.Key.ModelName, protocol: it.Key.ProtocolType}
		keyRoutes[k] = append(keyRoutes[k], it.RouteID)
	}
	if len(keyRoutes) == 0 {
		return result
	}

	keys := make([]luKey, 0, len(keyRoutes))
	for k := range keyRoutes {
		keys = append(keys, k)
	}

	type luOut struct {
		key          luKey
		successRow   lastRecordRow
		failureRow   lastRecordRow
		failed       bool // 两轮查询全部失败（连接级故障）
		successError bool // 仅 success 轮失败
		failureError bool // 仅 failure 轮失败
	}
	outCh := make(chan luOut, len(keys))
	sem := make(chan struct{}, batchLastUsedConcurrency)
	var wg sync.WaitGroup

	for _, k := range keys {
		wg.Add(1)
		go func(k luKey) {
			defer wg.Done()
			// panic 兜底：单个 key 查询异常不得拖垮整个页面加载。
			defer func() {
				if rec := recover(); rec != nil {
					logger.Printf("[AIRouteManage] BatchGetRouteLastUsedTimes panic for %s/%s pt=%d: %v",
						k.user, k.model, k.protocol, rec)
					outCh <- luOut{key: k, failed: true}
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			// v2.0.71：同一 key 依次查询「最后成功」「最后失败」两条记录，
			// 均走 ORDER BY id DESC LIMIT 1 快路径。两轮共享同一连接并发槽，
			// 总开销约 2×16ms；结果进 5 分钟缓存，重复刷新零 database.DB 压力。
			out := luOut{key: k}
			sRow, sErr := GetRouteLastRecordByStatus(k.user, k.model, k.protocol, subTableNum, true)
			if sErr != nil {
				logger.Printf("[AIRouteManage] last success record query failed for %s/%s pt=%d: %v",
					k.user, k.model, k.protocol, sErr)
				out.successError = true
			} else {
				out.successRow = sRow
			}
			fRow, fErr := GetRouteLastRecordByStatus(k.user, k.model, k.protocol, subTableNum, false)
			if fErr != nil {
				logger.Printf("[AIRouteManage] last failure record query failed for %s/%s pt=%d: %v",
					k.user, k.model, k.protocol, fErr)
				out.failureError = true
			} else {
				out.failureRow = fRow
			}
			// 两轮全部失败视为连接级故障（与旧 failed 语义一致）。
			out.failed = out.successError && out.failureError
			outCh <- out
		}(k)
	}

	wg.Wait()
	close(outCh)

	for out := range outCh {
		for _, rid := range keyRoutes[out.key] {
			r := RouteBatchStatResult{RouteID: rid}
			// v2.0.71：最后成功/失败记录分别回填。查询失败置对应 *Failed=true，
			// 禁止把数据库故障静默降级成「暂无记录」—— 与 v2.0.66/69/70 同源陷阱。
			if out.failed || out.successError {
				r.LastSuccessFailed = true
			} else if !out.successRow.CreatedAt.IsZero() {
				r.LastSuccessAt = out.successRow.CreatedAt
				r.LastSuccessAtUnix = out.successRow.CreatedAt.Unix()
				r.LastSuccessStatus = out.successRow.ResponseStatus
				r.LastSuccessStatusCode = ParseResponseStatusCode(out.successRow.ResponseStatus)
				r.LastSuccessDstModelName = out.successRow.DstModelName
				r.LastSuccessHasRecord = true
			}
			if out.failed || out.failureError {
				r.LastFailureFailed = true
			} else if !out.failureRow.CreatedAt.IsZero() {
				r.LastFailureAt = out.failureRow.CreatedAt
				r.LastFailureAtUnix = out.failureRow.CreatedAt.Unix()
				r.LastFailureStatus = out.failureRow.ResponseStatus
				r.LastFailureStatusCode = ParseResponseStatusCode(out.failureRow.ResponseStatus)
				r.LastFailureDstModelName = out.failureRow.DstModelName
				r.LastFailureHasRecord = true
			}
			result[rid] = r
		}
	}
	return result
}

// ============================================================================
// v2.0.37: 路由管理页面「加载中」卡顿 —— 批量聚合替代 N+1
// ============================================================================
//
// 根因:
//   - /AIRouteManage 页面前端 loadData() 先请求 list 拿到全部路由后，
//     对每条路由逐个发起 count_record XHR（并发 5），200 条路由 → N 次 SQL；
//   - 同时 enrichRoute 内对每条路由逐个调用 GetRouteLastUsedTime，又 N 次 SQL；
//   - 这两个 N+1 叠加就是「一直加载中」的表象（受浏览器连接池 + 后端 goroutine 调度
//     双重限制，实际完成速度远慢于预期，即便单次 SQL 只 SELECT 单列 LIMIT 1）。
//   - 注意：本项目查询浏览记录时从来都不加载 RequestBody / ResponseBody /
//     RequestSrcProtocolBody / ResponseSrcProtocolBody 这些 longtext 海量字段
//     （QueryAgentHttpTransactions 有显式白名单 Select；GetRouteLastUsedTime 只
//     SELECT created_at），所以 IO 压力单位是在「SQL 次数」而不是「单 SQL 数据量」。
//
// 修复:
//   - 后端新增一条"批量聚合"入口：前端一次性 POST 多个 (user_name, model_name,
//     protocol_type, days) 复合 key，后端按子表哈希聚合，把原来的「N 个分表」×
//     「每个分表 1 条聚合 SQL」发送给 MySQL，database.DB 端只返回 len(分表数) 行结果。
//   - 原来的 count_record / list 单条路径保留，向后兼容。
//
// SQL 注入防护:
//   - 表名走 isValidTableName 校验（正则 ^[A-Za-z0-9_]+$）；
//   - user_name / model_name / protocol_type / days 全部走参数化占位符 (?)，
//     不直接拼到 SQL 字符串。

const BatchRouteStatsKeyPairMax = 2000

// RouteBatchStatKey 批量统计时客户端传入的一次查询需求标识
type RouteBatchStatKey struct {
	UserName     string `json:"user_name"`
	ModelName    string `json:"model_name"`
	ProtocolType int    `json:"protocol_type"`
}

// RouteBatchStatItem 批量统计时客户端请求的单条 key
type RouteBatchStatItem struct {
	RouteID  uint64            `json:"route_id"`
	Protocol int               `json:"protocol_type"`
	Days     int               `json:"days"`
	Key      RouteBatchStatKey `json:"key"`
}

// RouteBatchStatResult 批量统计结果（每个 route_id 对应一行）。
//
// v2.0.44：新增协议区分字段 AnthropicCount / OpenAICount / CountByProtocol
// (key=protocol_type 值=count)。原 Count 字段 = 两协议总和（保持向后兼容，
// 调用方仍可拿 Count 当「总记录数」）。协议区分通过 GROUP BY 协议在 SQL 层完成，
// 一次聚合 SQL 拿到全部数据，避免二次查询。
// - protocol_type=1 → Anthropic 协议
// - protocol_type=2 → OpenAI 协议
// - protocol_type=0/其他 → OtherCount（兜底）
type RouteBatchStatResult struct {
	RouteID uint64 `json:"route_id"`
	Count   int64  `json:"count"` // 总记录数（v2.0.44：等于 AnthropicCount+OpenAICount+OtherCount）

	// v2.0.44：按协议拆分的记录数
	AnthropicCount  int64            `json:"anthropic_count"`             // protocol_type=1 的记录数
	OpenAICount     int64            `json:"openai_count"`                // protocol_type=2 的记录数
	OtherCount      int64            `json:"other_count"`                 // 其它/未知协议的记录数
	CountByProtocol map[string]int64 `json:"count_by_protocol,omitempty"` // 协议字符串→count，便于前端动态读取新协议

	// v2.0.71：最后成功记录（response_status 2xx）相关字段。
	//
	// 由 BatchGetRouteLastUsedTimes 内层 GetRouteLastRecordByStatus(success=true)
	// 单 SQL 同时 SELECT created_at, response_status, dst_model_name —— 三者严格
	// 来自同一条记录，禁止另起一轮独立查询导致串行不一致。
	//
	// 三态语义必须视觉可分（前端 renderLastRecordCell）：
	//   - 有记录：LastSuccessHasRecord=true，显示状态徽标 + 时间 + 目标模型
	//   - 暂无成功记录：LastSuccessHasRecord=false 且 LastSuccessFailed=false（灰斜体）
	//   - 查询失败：LastSuccessFailed=true（红色加粗，禁止把 database.DB 故障静默降级成
	//     「暂无成功记录」）
	LastSuccessAt           time.Time `json:"last_success_at,omitempty"`             // 最后一条 2xx 响应记录的时间
	LastSuccessAtUnix       int64     `json:"last_success_at_unix,omitempty"`        // 同上，unix 秒
	LastSuccessStatus       string    `json:"last_success_status,omitempty"`         // 原始 ResponseStatus 文本，如 "200 OK"
	LastSuccessStatusCode   int       `json:"last_success_status_code,omitempty"`    // 解析出的整数状态码，0 表示无记录/不可解析
	LastSuccessDstModelName string    `json:"last_success_dst_model_name,omitempty"` // 该记录实际目标模型名称
	LastSuccessHasRecord    bool      `json:"last_success_has_record,omitempty"`     // 是否存在 2xx 记录
	LastSuccessFailed       bool      `json:"last_success_failed,omitempty"`         // 查询失败（与「暂无成功记录」区分）

	// v2.0.71：最后失败记录（response_status 非 2xx，含空串=传输层错误）相关字段。
	// 字段语义与 LastSuccess* 完全同构，数据源为
	// GetRouteLastRecordByStatus(success=false)（response_status NOT LIKE '2%'）。
	LastFailureAt           time.Time `json:"last_failure_at,omitempty"`             // 最后一条非 2xx 响应记录的时间
	LastFailureAtUnix       int64     `json:"last_failure_at_unix,omitempty"`        // 同上，unix 秒
	LastFailureStatus       string    `json:"last_failure_status,omitempty"`         // 原始 ResponseStatus 文本，如 "500 Internal Server Error"；空串表示传输层错误
	LastFailureStatusCode   int       `json:"last_failure_status_code,omitempty"`    // 解析出的整数状态码，0 表示空串/不可解析
	LastFailureDstModelName string    `json:"last_failure_dst_model_name,omitempty"` // 该记录实际目标模型名称
	LastFailureHasRecord    bool      `json:"last_failure_has_record,omitempty"`     // 是否存在非 2xx 记录
	LastFailureFailed       bool      `json:"last_failure_failed,omitempty"`         // 查询失败（与「暂无失败记录」区分）
}

// BatchGetRouteStatsByRouteIDs 按 route_id 批量聚合查询每条路由在过去 days 天内
// 的记录总数（days<=0 表全量），按协议拆分成 Anthropic / OpenAI / Other 三个桶。
//
// v2.0.66：本函数不再产出「最后使用时间」—— 该职责已拆给
// BatchGetRouteLastUsedTimes。原因见该函数上方的根因分析：聚合路径在 Days=0 时
// 无法命中复合索引会超时，且结果按 (user, model) 扇出到所有 routeID，
// 同模型的双协议路由拿不到各自的时间戳。
//
// 聚合粒度按 user_name + model_name 哈希到的子表划分，每个子表仅执行 1 条聚合
// SQL（GROUP BY user_name, model_name, protocol_type），由 database.DB 端完成聚合；
// 禁止逐条 SELECT。
//
// 入参校验先于 database.DB 访问（无 database.DB 环境也能精准测试参数校验路径）。
func BatchGetRouteStatsByRouteIDs(items []RouteBatchStatItem, subTableNum int) (map[uint64]RouteBatchStatResult, error) {
	result := make(map[uint64]RouteBatchStatResult, len(items))
	if database.DB == nil || len(items) == 0 {
		return result, nil
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if len(items) > BatchRouteStatsKeyPairMax {
		items = items[:BatchRouteStatsKeyPairMax]
	}

	// pairKey: 子表名 + (user_name, model_name) 笛卡尔配对 —— 作为 GROUP BY 聚合的输入条件
	type pairKey struct {
		table string
		user  string
		model string
	}
	// 收集需要查询的 (表, user, model) 互不相同的配对（去重后生成 WHERE IN）
	pairSeen := map[pairKey]struct{}{}
	// 记录每个 pairKey 对应的 route_id 列表 + 最宽时间窗口的 span（v2.0.40：
	// span 负值编码小时，widerStatsSpan 负责在天/小时混合时取更宽窗口）。
	// spanSet 区分「尚未收到任何 span」与「span 恰为 0（无限制）」——
	// 否则 maxSpan 初值 0 会被 widerStatsSpan(0,x) 恒当成无限制，导致过滤永不生效。
	type pairAgg struct {
		routeIDs []uint64
		maxSpan  int
		spanSet  bool
	}
	pairAggMap := map[pairKey]*pairAgg{}

	for _, it := range items {
		if it.RouteID == 0 || it.Key.UserName == "" || it.Key.ModelName == "" {
			continue
		}
		tableName := GetAgentHttpTableName(it.Key.UserName, it.Key.ModelName, subTableNum)
		if !isValidTableName(tableName) {
			continue
		}
		pk := pairKey{table: tableName, user: it.Key.UserName, model: it.Key.ModelName}
		pairSeen[pk] = struct{}{}
		pa := pairAggMap[pk]
		if pa == nil {
			pa = &pairAgg{}
			pairAggMap[pk] = pa
		}
		pa.routeIDs = append(pa.routeIDs, it.RouteID)
		if !pa.spanSet {
			pa.maxSpan = it.Days
			pa.spanSet = true
		} else {
			pa.maxSpan = widerStatsSpan(pa.maxSpan, it.Days)
		}
	}

	// 按子表名聚合：每个子表下发 1 条 GROUP BY 聚合
	type shardAgg struct {
		table string
		pairs []pairKey
	}
	shardMap := map[string]*shardAgg{}
	for pk := range pairSeen {
		sa := shardMap[pk.table]
		if sa == nil {
			sa = &shardAgg{table: pk.table}
			shardMap[pk.table] = sa
		}
		sa.pairs = append(sa.pairs, pk)
	}

	// v2.0.66：收集查询失败的分表名，用于在函数末尾上抛错误 —— 旧实现只 continue
	// 并让缺失的路由补零值，调用方无法区分「真的 0 条」与「查询挂了」。
	var failedShards []string

	for _, sa := range shardMap {
		// 组 WHERE IN：一张子表内多对 (user_name, model_name)
		conds := make([]string, 0, len(sa.pairs))
		args := make([]interface{}, 0, 2*len(sa.pairs))
		for _, p := range sa.pairs {
			conds = append(conds, "(user_name = ? AND model_name = ?)")
			args = append(args, p.user, p.model)
		}
		where := strings.Join(conds, " OR ")

		// 全局最宽时间窗口（v2.0.40：span 负值编码小时，0=无限制）。
		// 同样用 maxSpanSet 区分「未设置」与「无限制(0)」。
		maxSpan := 0
		maxSpanSet := false
		for _, p := range sa.pairs {
			pa := pairAggMap[p]
			if pa == nil || !pa.spanSet {
				continue
			}
			if !maxSpanSet {
				maxSpan = pa.maxSpan
				maxSpanSet = true
			} else {
				maxSpan = widerStatsSpan(maxSpan, pa.maxSpan)
			}
		}

		// v2.0.44：SQL 改为 GROUP BY user_name, model_name, protocol_type
		// —— 一次聚合同时返回 (user, model, protocol_type) → (cnt, last_used)。
		// 这是协议区分记录数的核心：database.DB 端按协议 split 计数。
		// COALESCE(protocol_type, 0) 兼容 v2.0.40 之前的旧浏览记录表（没有该列，
		// 或 collate 阶段存在 NULL 旧记录）不会触发 converting NULL to int 错误——
		// 前者归类到 OtherCount bucket。
		// v2.0.66：移除 MAX(created_at) —— last_used 已由
		// BatchGetRouteLastUsedTimes 按 (user, model, protocol) 精确查询。
		// 本函数只做记录数聚合，SELECT 列表相应收窄。
		sqlText := fmt.Sprintf(
			"SELECT user_name, model_name, COALESCE(protocol_type, 0) AS protocol_type, COUNT(*) AS cnt FROM %s WHERE %s",
			sa.table, where,
		)
		if cutoff, ok := resolveStatsSpanCutoff(maxSpan); ok {
			// 所有子句共享 created_at 下界（>= cutoff）
			// 为确保 AND 绑定整批 OR 子句，把 OR 子句用括号包一层
			sqlText = fmt.Sprintf(
				"SELECT user_name, model_name, COALESCE(protocol_type, 0) AS protocol_type, COUNT(*) AS cnt FROM %s WHERE (%s) AND created_at >= ?",
				sa.table, where,
			)
			args = append(args, cutoff.Format("2006-01-02 15:04:05"))
		}
		sqlText += " GROUP BY user_name, model_name, COALESCE(protocol_type, 0)"

		// v2.0.66：走 database.StatsDB() 绑定 25s context，超时时驱动向 MySQL 发 KILL 并
		// 归还连接；旧实现用裸 database.DB.Raw，只能等 readTimeout(30s) 砍断 socket，
		// 连接被标记 invalid 后污染连接池（日志实证 19 次）。
		sdb, cancel := database.StatsDB()
		if sdb == nil {
			cancel()
			continue
		}
		rows, err := sdb.Raw(sqlText, args...).Rows()
		if err != nil {
			cancel()
			logger.Printf("[AIRouteManage] BatchGetRouteStats shard %s failed: %v", sa.table, err)
			// 记录失败的分表，让调用方知道这批统计不完整（而非「真的是 0」）。
			failedShards = append(failedShards, sa.table)
			continue
		}
		// 结果 -> pairAggMap[pk]
		// v2.0.44：每条 (user, model, protocol_type) 累加到同一 pair 的协议 bucket 里。
		// 同一个 pair 可能出现 1~2 行（Anthropic + OpenAI），所以需要按 protocol_type
		// 聚合到同一个 RouteBatchStatResult 上。
		for rows.Next() {
			var (
				user         string
				model        string
				protocolType int
				cnt          int64
			)
			if err := rows.Scan(&user, &model, &protocolType, &cnt); err != nil {
				rows.Close()
				cancel()
				return result, err
			}
			pk := pairKey{table: sa.table, user: user, model: model}
			pa := pairAggMap[pk]
			if pa == nil {
				continue
			}
			// v2.0.44：同一 pair 可能出现多行（每行对应一个 protocol_type），
			// 所以要先看 result[rid] 是否已初始化（首行 Anthropic 时）；
			// 后续 OpenAI 行需要累加到已有的 result 上而不是覆盖。
			//
			// v2.0.66：last_used 不再由本函数产出 —— 它扇出到 pair 下所有
			// routeID，同模型的 Anthropic / OpenAI 两条路由会拿到同一个时间戳
			// （SQL 里 GROUP BY 了 protocol_type，但映射回 routeID 时没按协议
			// 区分）。改由 BatchGetRouteLastUsedTimes 按 (user, model, protocol)
			// 精确查询。本函数只保留记录数聚合职责。
			for _, rid := range pa.routeIDs {
				r, exists := result[rid]
				if !exists {
					r = RouteBatchStatResult{RouteID: rid}
				}
				// protocol_type 分桶累计（1=Anthropic, 2=OpenAI）
				switch protocolType {
				case 1:
					r.AnthropicCount += cnt
				case 2:
					r.OpenAICount += cnt
				default:
					r.OtherCount += cnt
				}
				// 同时记录到 map，便于前端按字符串 key 查询
				if r.CountByProtocol == nil {
					r.CountByProtocol = make(map[string]int64, 4)
				}
				r.CountByProtocol[protocolTypeKey(protocolType)] += cnt
				r.Count += cnt
				result[rid] = r
			}
		}
		rows.Close()
		cancel()
	}

	// 没出现在聚合结果中的 (user, model) 返回零值 —— 从未使用过
	for _, it := range items {
		if _, ok := result[it.RouteID]; !ok {
			result[it.RouteID] = RouteBatchStatResult{RouteID: it.RouteID}
		}
	}
	// v2.0.66：分表查询失败时把错误上抛，不再静默把「查询失败」伪装成「0 条记录」。
	// 调用方（batch_stats）据此决定是提示错误还是渲染数字。
	if len(failedShards) > 0 {
		return result, fmt.Errorf("统计查询失败的分表: %s", strings.Join(failedShards, ", "))
	}
	return result, nil
}

// protocolTypeKey 把 protocol_type int 转成 map key 字符串，避免 Go map[int]int64
// 在 JSON 反序列化为 map[string]interface{} 时被前端强转成 map[interface{}]interface{}。
// 前端用 'anthropic' / 'openai' / 'unknown_<n>' 等稳定字符串即可。
func protocolTypeKey(protocolType int) string {
	switch protocolType {
	case 1:
		return "anthropic"
	case 2:
		return "openai"
	default:
		return "unknown"
	}
}
