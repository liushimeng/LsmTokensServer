package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// logFile / logger / cfg 原为旧工程 package main 的全局变量，迁入本包统一管理
var (
	logFile *os.File
	logger  *log.Logger
)

// logConfig 保存日志相关的配置项（由 SetConfig 注入，替代旧全局 cfg）
type logConfig struct {
	LogFileURL     string
	LogMaxSizeMB   int
	UserInfoLogURL string
}

var cfg logConfig

// SetConfig 注入日志配置（在 config.LoadConfig 之后、InitLogger 之前调用）
func SetConfig(fileURL string, maxSizeMB int, userInfoLogURL string) {
	cfg = logConfig{LogFileURL: fileURL, LogMaxSizeMB: maxSizeMB, UserInfoLogURL: userInfoLogURL}
}

// logRotateMutex 保护日志轮转期间对 logFile 和 logger 的并发访问
var logRotateMutex sync.Mutex

// userLogFile 用户信息日志文件句柄
var userLogFile *os.File

// userLogMutex 用户信息日志锁
var userLogMutex sync.Mutex

// disableUserLog 全局开关：true 时 LogUserAction 直接返回，不写文件
// 启用方式：设置环境变量 LSM_DISABLE_USER_LOG=1（测试 initTestEnv 默认设置），
// 或在运行时通过 SetDisableUserLog(true) 显式开关。
// 目的：让测试代码产生的 fixture 操作不再污染 LsmTokensServerUsersInfo.log。
var disableUserLog atomic.Bool

// SetDisableUserLog 运行时切换是否禁用用户信息日志写入
// 主要供测试代码使用（避免每次新增测试 fixture 都要登记到 isTestLogEntry）
func SetDisableUserLog(disable bool) {
	disableUserLog.Store(disable)
}

// IsUserLogDisabled 返回当前是否禁用用户信息日志
func IsUserLogDisabled() bool {
	return disableUserLog.Load()
}

func init() {
	// 环境变量 LSM_DISABLE_USER_LOG=1 时默认禁用；
	// 测试代码（go test）也可通过 SetDisableUserLog(true) 开启。
	if os.Getenv("LSM_DISABLE_USER_LOG") == "1" {
		disableUserLog.Store(true)
	}
}

// logSeparator 输出分隔线
func logSeparator(title string) {
	logger.Println("==================================================")
	logger.Printf("=== %s", title)
	logger.Println("==================================================")
}

// rotateLogIfNeeded 检查日志文件大小，如果超过限制则进行轮转
func rotateLogIfNeeded(logPath string, maxSizeMB int) error {
	info, err := os.Stat(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
	if info.Size() < maxSizeBytes {
		return nil
	}

	dir := filepath.Dir(logPath)
	base := filepath.Base(logPath)
	nextIndex := 1
	for {
		backupName := fmt.Sprintf("%s.%d.log", strings.TrimSuffix(base, filepath.Ext(base)), nextIndex)
		backupPath := filepath.Join(dir, backupName)
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			break
		}
		nextIndex++
		if nextIndex > 99 {
			break
		}
	}

	backupName := fmt.Sprintf("%s.%d.log", strings.TrimSuffix(base, filepath.Ext(base)), nextIndex)
	backupPath := filepath.Join(dir, backupName)
	err = os.Rename(logPath, backupPath)
	if err != nil {
		return err
	}

	log.Printf("Log rotated: current size %.2f MB > max %d MB, backup to %s",
		float64(info.Size())/(1024*1024), maxSizeMB, backupPath)

	return nil
}

// checkAndRotateLog 每次写入前检查是否需要轮转
func checkAndRotateLog() error {
	logRotateMutex.Lock()
	defer logRotateMutex.Unlock()

	if logFile == nil {
		return nil
	}

	info, err := logFile.Stat()
	if err != nil {
		return err
	}

	maxSizeBytes := int64(cfg.LogMaxSizeMB) * 1024 * 1024
	if info.Size() < maxSizeBytes {
		return nil
	}

	logFile.Close()

	if err := rotateLogIfNeeded(cfg.LogFileURL, cfg.LogMaxSizeMB); err != nil {
		log.Printf("Rotation failed: %v", err)
		file, err := os.OpenFile(cfg.LogFileURL, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		logFile = file
		logger = log.New(file, "", log.LstdFlags)
		return err
	}

	file, err := os.OpenFile(cfg.LogFileURL, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	logFile = file
	logger = log.New(file, "", log.LstdFlags)

	logger.Println("[INFO] Log rotation completed, starting new log file")
	return nil
}

// InitLogger 初始化日志
func InitLogger(logPath string, maxSizeMB int) error {
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := rotateLogIfNeeded(logPath, maxSizeMB); err != nil {
		log.Printf("Warning: log rotation check failed: %v", err)
	}

	logRotateMutex.Lock()
	defer logRotateMutex.Unlock()

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	logFile = file
	logger = log.New(file, "", log.LstdFlags)
	return nil
}

// InitUserInfoLogger 初始化用户信息日志
func InitUserInfoLogger() error {
	userLogMutex.Lock()
	defer userLogMutex.Unlock()

	if userLogFile != nil {
		return nil
	}

	dir := filepath.Dir(cfg.UserInfoLogURL)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(cfg.UserInfoLogURL, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	userLogFile = file
	return nil
}

// isTestLogEntry 判断操作是否由测试代码产生（按操作者或被操作对象过滤）
// 集成测试 / 单元测试中会通过 API 创建/修改大量 fixture：
// 用户 testuser/routeuser/disableduser/...、模型 claude-3-opus/claude-3-5-sonnet-test/...、
// 平台 TestPlatform/MockPlatform 等。这些操作不应污染 LsmTokensServerUsersInfo.log。
func isTestLogEntry(username, details string) bool {
	testTokens := []string{
		// 测试用户
		"testuser", "routeuser", "disableduser", "converteruser",
		"streamconverter", "cruduser", "modeluser", "endpointuser",
		"protouser", "apiuser", "newapiuser", "days-user",
		// 测试模型
		"claude-3-opus", "claude-3-5-sonnet-test", "gpt-4-turbo-test", "gpt-4o-test",
		"claude-3-5-sonnet-disabled", "test-model", "stream-openai-model",
		"days-model",
		// 测试平台
		"TestPlatform", "MockPlatform",
	}
	combined := username + " " + details
	for _, token := range testTokens {
		if strings.Contains(combined, token) {
			return true
		}
	}
	// 自动识别：details 中的 key=value 形式（如 "用户名=alice"/"模型名称=foo-test"/"平台=MockXxx"），
	// value 命中测试命名模式（test/mock/fixture 后缀，"-test"/"_test"/"Mock"/"Test" 前缀等）
	// 即视为测试数据。配合 SetDisableUserLog(true) 形成双重保险。
	if hasTestFixtureValue(details) {
		return true
	}
	return false
}

// hasTestFixtureValue 检查 details 字符串中 key=value 的 value 是否命中常见测试命名模式
// 支持的 key：用户名/用户名称/模型名称/平台/平台名称/模型
func hasTestFixtureValue(details string) bool {
	keys := []string{"用户名=", "用户名称=", "模型名称=", "平台=", "平台名称=", "模型="}
	for _, key := range keys {
		idx := strings.Index(details, key)
		if idx < 0 {
			continue
		}
		// 截取 value 段（到下一个空格 / '[' / ',' / 末尾）
		rest := details[idx+len(key):]
		end := len(rest)
		for i, r := range rest {
			if r == ' ' || r == '[' || r == ',' {
				end = i
				break
			}
		}
		value := rest[:end]
		if isLikelyTestFixtureName(value) {
			return true
		}
	}
	return false
}

// isLikelyTestFixtureName 根据命名习惯判断 value 是不是测试 fixture
// 命中规则：包含 "-test" / "_test" / "-disabled" 后缀；以 "test"/"mock"/"fixture" 开头；含 ".test" / "TestModel" 等。
func isLikelyTestFixtureName(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	testSuffixes := []string{"-test", "_test", "-disabled", "-mock", "_mock"}
	for _, s := range testSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	testPrefixes := []string{"test", "mock", "fixture"}
	for _, p := range testPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	// 形如 "model-a"/"model-b"/"model-x"...（明显的占位 fixture）
	if strings.HasPrefix(lower, "model-") && len(value) <= 16 {
		return true
	}
	return false
}

// LogUserAction 记录用户操作日志
func LogUserAction(actionType string, username string, details string) {
	// 全局开关：测试环境可一次性关闭所有用户日志写入
	if disableUserLog.Load() {
		return
	}
	// 跳过测试代码产生的日志：操作者或被操作对象命中测试 fixture 关键词即视为测试数据
	if isTestLogEntry(username, details) {
		return
	}

	if userLogFile == nil {
		if err := InitUserInfoLogger(); err != nil {
			log.Printf("[WARNING] Failed to init user log: %v", err)
			return
		}
	}

	userLogMutex.Lock()
	defer userLogMutex.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("[%s] %s %s %s\n", timestamp, actionType, username, details)

	if _, err := userLogFile.WriteString(logLine); err != nil {
		log.Printf("[WARNING] Failed to write user log: %v", err)
	}
}

// Printf 输出常规日志（替代旧 package main 的全局 logger 变量）
// logger 未初始化时降级到标准 log，保证测试与早期调用安全
func Printf(format string, v ...interface{}) {
	if logger == nil {
		log.Printf(format, v...)
		return
	}
	logger.Printf(format, v...)
}

// Ready 返回底层 logger 是否已初始化（供调用方做兜底判断）
func Ready() bool {
	return logger != nil
}
