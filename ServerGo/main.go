// LsmTokensServer - AI Tokens 代理与管理服务（前后端分离架构，后端入口）
//
// 前端见 ../ClientWeb（React + Vite）。
// 启动顺序（与旧版一致，AI 代理为核心链路）：
//  1. 配置/日志初始化
//  2. MySQL（可选，失败不影响 AI 代理）
//  3. AI 代理服务（29000 HTTP / 29003 HTTPS）← 核心优先
//  4. 爬虫引擎（CDP）+ MCP 服务（29002）
//  5. 管理员 Web（9101）/ 用户 Web（29001）——静态托管 ClientWeb
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/proxy"
	"github.com/lishimeng/LsmTokensServer/spider"
	"github.com/lishimeng/LsmTokensServer/webserver"
)

const (
	APP_NAME       = "LsmTokensServer"
	PRODUCT_NAME   = "Lsm AI Tokens 代理服务"
	PID_FILE       = "lsmtokensserver.pid"
	DEFAULT_CONFIG = "LsmTokensServer.conf"
)

// buildTime 由编译脚本通过 -ldflags 注入
var buildTime string

var (
	daemonMode      bool
	stopMode        bool
	sortUserLogMode bool
	configFile      string
)

func init() {
	flag.BoolVar(&daemonMode, "d", false, "Start daemon mode")
	flag.BoolVar(&stopMode, "u", false, "Stop daemon")
	flag.BoolVar(&sortUserLogMode, "s", false, "Sort user action log file by timestamp")
	flag.StringVar(&configFile, "c", DEFAULT_CONFIG, "Configuration file path")
}

// checkProcess 检查进程是否运行
func checkProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// stopDaemon 停止守护进程
func stopDaemon() bool {
	data, err := os.ReadFile(PID_FILE)
	if err != nil {
		fmt.Println("PID file not found, daemon not running")
		return true
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		fmt.Println("Invalid PID file")
		os.Remove(PID_FILE)
		return true
	}
	if !checkProcess(pid) {
		fmt.Printf("Process %d not running, cleaning up PID file\n", pid)
		os.Remove(PID_FILE)
		return true
	}
	fmt.Printf("Stopping %s (PID: %d)...\n", APP_NAME, pid)
	process, _ := os.FindProcess(pid)
	if err := process.Kill(); err != nil {
		fmt.Printf("Failed to kill process %d: %v\n", pid, err)
		return false
	}
	for range 30 {
		if !checkProcess(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.Remove(PID_FILE)
	fmt.Printf("%s stopped successfully\n", APP_NAME)
	return true
}

// startDaemon 双 fork 后台守护进程
func startDaemon() {
	if _, err := os.Stat(PID_FILE); err == nil {
		if data, err := os.ReadFile(PID_FILE); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil && checkProcess(pid) {
				fmt.Printf("%s is already running (PID: %d), stopping existing process...\n", APP_NAME, pid)
				stopDaemon()
			}
		}
	}
	if !daemonMode {
		return
	}
	var args []string
	for _, arg := range os.Args[1:] {
		if arg != "-d" {
			args = append(args, arg)
		}
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}
	pid := cmd.Process.Pid
	os.WriteFile(PID_FILE, []byte(strconv.Itoa(pid)), 0644)
	fmt.Printf("%s started successfully (PID: %d)\n", APP_NAME, pid)
	os.Exit(0)
}

// sortUserLogFile 对用户操作日志文件按时间倒序排序（-s 模式）
func sortUserLogFile(logPath string) error {
	fmt.Printf("Sorting user log file: %s\n", logPath)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("failed to read log file: %v", err)
	}
	type logEntry struct {
		line      string
		timestamp time.Time
		valid     bool
	}
	var entries []logEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 行格式: [2006-01-02 15:04:05] ...
		parts := strings.SplitN(line, "] ", 2)
		ts, perr := time.Parse("2006-01-02 15:04:05", strings.TrimPrefix(parts[0], "["))
		valid := perr == nil
		_ = ts
		entries = append(entries, logEntry{line: line, timestamp: ts, valid: valid})
	}
	// 时间倒序（最新在前）
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			a, b := entries[i], entries[j]
			if (b.valid && !a.valid) || (a.valid && b.valid && b.timestamp.After(a.timestamp)) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	backupPath := logPath + ".bak." + time.Now().Format("20060102150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("failed to backup log file: %v", err)
	}
	var result strings.Builder
	for _, e := range entries {
		result.WriteString(e.line)
		result.WriteString("\n")
	}
	return os.WriteFile(logPath, []byte(result.String()), 0644)
}

// loadConfigSafe 加载配置并解析各日志路径（相对路径基于可执行文件目录）
func loadConfigSafe() *config.LsmTokensServerConfig {
	resolved, err := config.ResolvePath(configFile)
	if err != nil {
		log.Printf("Warning: failed to resolve config path, using original: %v", err)
		resolved = configFile
	}
	cfg, err := config.LoadConfig(resolved)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	for ptr := range map[*string]string{
		&cfg.LogFileURL:          cfg.LogFileURL,
		&cfg.BuildDateTimeLogURL: cfg.BuildDateTimeLogURL,
		&cfg.UserInfoLogURL:      cfg.UserInfoLogURL,
	} {
		if p, err := config.ResolvePath(*ptr); err == nil {
			*ptr = p
		}
	}
	return cfg
}

func main() {
	flag.Parse()

	// 主进程 context：后台 goroutine 随进程退出
	appCtx, appCancel := context.WithCancel(context.Background())
	modelsdb.SetAppContext(appCtx)
	defer appCancel()

	if sortUserLogMode {
		cfg := loadConfigSafe()
		if err := sortUserLogFile(cfg.UserInfoLogURL); err != nil {
			log.Fatalf("Failed to sort user log: %v", err)
		}
		os.Exit(0)
	}
	if stopMode {
		if stopDaemon() {
			os.Exit(0)
		}
		os.Exit(1)
	}

	// ===== 1. 配置 + 日志 =====
	cfg := loadConfigSafe()
	config.G = cfg
	if err := logger.InitLogger(cfg.LogFileURL, cfg.LogMaxSizeMB); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	logger.SetConfig(cfg.LogFileURL, cfg.LogMaxSizeMB, cfg.UserInfoLogURL)
	if err := logger.InitUserInfoLogger(); err != nil {
		log.Printf("[WARNING] Failed to initialize user info logger: %v", err)
	}

	logger.Printf("[INIT] App Name: %s %s", APP_NAME, config.APP_VERSION)
	logger.Printf("[INIT] Config File: %s", configFile)
	logger.Printf("[INIT] Log File: %s", cfg.LogFileURL)
	if buildTime != "" {
		logger.Printf("[INIT] Build Time: %s", buildTime)
	}
	logger.Printf("[INIT] MySQL: %s@%s/%s", cfg.DBMysql.User, cfg.DBMysql.Url, cfg.DBMysql.DataBase)
	logger.Printf("[INIT] Manager Web: %d, User Web: %d, MCP: %d", cfg.ManagerWebListenPort, cfg.UserWebListenPort, cfg.McpWebListenPort)

	// ===== 2. MySQL（可选；失败不影响 AI 代理）=====
	if cfg.DBMysql.Url == "" || cfg.DBMysql.User == "" || cfg.DBMysql.Pwd == "" {
		logger.Printf("[INIT] MySQL not fully configured (url/user/pwd required), database logging disabled")
	} else if err := database.InitMySQL(&cfg.DBMysql); err != nil {
		logger.Printf("[WARNING] Failed to connect to MySQL: %v (database logging disabled)", err)
	} else {
		for _, init := range []func() error{
			modelsdb.InitAgentHttpUserInfoTable,
			modelsdb.InitAgentHttpUserModelInfoTable,
			modelsdb.InitAgentDstEndPointTable,
			modelsdb.InitAgentHttpAIRouteTable,
			modelsdb.InitAgentModelInfoTable,
			modelsdb.InitAgentHttpAgentInfoTable,
		} {
			if err := init(); err != nil {
				logger.Printf("[WARNING] Failed to init table: %v", err)
			}
		}
		if err := modelsdb.MigrateAgentToolColumns(cfg.DBMysqlSubTableNumber); err != nil {
			logger.Printf("[WARNING] Failed to migrate agent tool columns: %v", err)
		}
		if err := modelsdb.InitAgentHttpSubTables(cfg.DBMysqlSubTableNumber); err != nil {
			logger.Printf("[WARNING] Failed to init sub tables: %v", err)
		} else if err := modelsdb.InitCleanupReportTable(); err != nil {
			logger.Printf("[WARNING] Failed to init cleanup report table: %v", err)
		} else {
			modelsdb.StartTransactionCleanupService(cfg)
		}
		if err := modelsdb.InitUserOperationLogTable(); err != nil {
			logger.Printf("[WARNING] Failed to init user operation log table: %v", err)
		} else {
			logger.DBLogWriter = modelsdb.AddUserOperationLog
			logger.Printf("[INIT] User operation log table initialized, DB log writer injected")
		}
		if err := modelsdb.InitSpiderTables(); err != nil {
			logger.Printf("[WARNING] Failed to init spider tables: %v", err)
		} else {
			logger.Printf("[INIT] Spider tables initialized successfully")
			if os.Getenv("LSM_CLEANUP_EMPTY_DAILY_INFO") == "1" {
				if removed, err := modelsdb.CleanupEmptySpiderDailyInfos(); err != nil {
					logger.Printf("[WARNING] Cleanup empty daily info failed: %v", err)
				} else if removed > 0 {
					logger.Printf("[INIT] Cleanup empty daily info: removed %d record(s)", removed)
				}
			}
		}
		if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
			logger.Printf("[WARNING] Failed to load agent cache: %v", err)
		} else {
			logger.Printf("[INIT] Agent cache loaded successfully")
		}
		modelsdb.InitStatsCache()
		logger.Printf("[INIT] Stats cache initialized")
	}

	// ===== 3. AI 代理服务（核心链路：29000 HTTP / 29003 HTTPS）=====
	go proxy.StartAIProxyService(cfg)
	logger.Printf("[INIT] AI proxy service started on port %d (HTTP)", cfg.AgentListenPort)
	if cfg.AgentHttpsListenPort > 0 && cfg.AgentHttpsListenPort != cfg.AgentListenPort {
		logger.Printf("[INIT] AI proxy HTTPS service started on port %d", cfg.AgentHttpsListenPort)
	}

	if daemonMode {
		startDaemon()
	}
	if !daemonMode {
		os.WriteFile(PID_FILE, []byte(strconv.Itoa(os.Getpid())), 0644)
	}

	// 信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Printf("[SIGNAL] %v received, shutting down", sig)
		appCancel()
		os.Remove(PID_FILE)
		proxy.StopAIProxyService()
		spider.GetSpiderEngine().Stop()
		if database.DB != nil {
			database.CloseMySQL()
		}
		os.Exit(0)
	}()

	// ===== 4. 爬虫引擎（CDP）+ MCP handler 限流 =====
	if err := spider.GetSpiderEngine().Start(); err != nil {
		logger.Printf("[ERROR] Failed to start spider engine (Chrome): %v", err)
		logger.Printf("[ERROR] MCP /SpiderWebData will be unavailable")
	} else {
		logger.Printf("[INIT] Spider engine (CDP) started")
	}
	maxHandlerConc := 8
	if cfg.SpiderMaxConcurrency > 0 {
		maxHandlerConc = cfg.SpiderMaxConcurrency * 2
		if maxHandlerConc > 16 {
			maxHandlerConc = 16
		}
		if maxHandlerConc < 4 {
			maxHandlerConc = 4
		}
	}
	spider.InitMCPHandlerSem(maxHandlerConc)
	logger.Printf("[INIT] MCP handler concurrency limit set to %d", maxHandlerConc)

	if cfg.EnableSpiderScheduler {
		spider.GetSpiderScheduler().Start()
		logger.Printf("[INIT] Spider scheduler started")
	} else {
		logger.Printf("[INIT] Spider scheduler disabled (enableSpiderScheduler=false)")
	}

	// ===== 5. Web 服务（静态托管 ClientWeb，API 阶段5 挂载）=====
	go webserver.StartManagerWebServer(cfg)
	go webserver.StartUserWebServer(cfg)

	// ===== 6. MCP Web 服务（爬虫 MCP，阻塞）=====
	go spider.StartMCPWebServer(cfg.McpWebListenPort)
	logger.Printf("[INIT] MCP Web server started on port %d", cfg.McpWebListenPort)

	log.Printf("%s %s (%s) running\n", APP_NAME, PRODUCT_NAME, config.APP_VERSION)
	log.Printf("Manager Web Admin: http://localhost:%d/", cfg.ManagerWebListenPort)
	log.Printf("AI Proxy (HTTP):   http://localhost:%d/", cfg.AgentListenPort)
	if cfg.AgentHttpsListenPort > 0 && cfg.AgentHttpsListenPort != cfg.AgentListenPort {
		log.Printf("AI Proxy (HTTPS):  https://localhost:%d/", cfg.AgentHttpsListenPort)
	}
	log.Printf("Log File: %s", cfg.LogFileURL)

	select {}
}
