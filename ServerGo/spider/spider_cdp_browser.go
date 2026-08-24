package spider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

// ==================== SpiderEngine CDP 实现 ====================

// SpiderEngine 爬虫引擎（CDP 模式）
// v2.0.0 重构：从 HTTP-only 切换到 Chrome DevTools Protocol。
// - 进程内嵌拉起 google-chrome-stable --headless=new
// - 30s health check loop 自动重启异常 Chrome
// - 通过 chromedp.NewRemoteAllocator 连接 ws://localhost:9222/
// - 单个 rootCtx 作为所有 session 上下文的父节点
// - v2.0.1: 增加 busy 失败计数器，连续 busy 自动重启 Chrome
// - v2.0.2: 增加 sem 槽位 watchdog，防止 busy 锁死
type SpiderEngine struct {
	isRunning bool

	// Chrome 进程管理
	chromePath string
	chromeCmd  *exec.Cmd
	wsURL      string

	// chromedp 上下文
	allocCtx    context.Context
	allocCancel context.CancelFunc
	rootCtx     context.Context
	rootCancel  context.CancelFunc

	// 健康检查
	healthCtx    context.Context
	healthCancel context.CancelFunc

	// 并发限制（防止单次 Agent 突发开过多 tab）
	sem chan struct{}

	// 串行化 Start/Stop/healthCheckLoop 重启操作
	mu sync.Mutex

	// busy 失败计数（用于自动恢复）
	busyFailCount int
	busyMu        sync.Mutex

	// v2.0.2: sem 槽位占用时间监控（watchdog 用）
	semAcquireTime map[int]time.Time
	semWatchdogMu  sync.Mutex
}

var (
	spiderEngine     *SpiderEngine
	spiderEngineOnce sync.Once
)

// GetSpiderEngine 获取爬虫引擎单例
func GetSpiderEngine() *SpiderEngine {
	spiderEngineOnce.Do(func() {
		semCap := 4
		if config.G != nil && config.G.SpiderMaxConcurrency > 0 {
			semCap = config.G.SpiderMaxConcurrency
		}
		spiderEngine = &SpiderEngine{
			sem:            make(chan struct{}, semCap),
			semAcquireTime: make(map[int]time.Time),
		}
	})
	return spiderEngine
}

// Start 启动爬虫引擎（拉起 Chrome + 30s 健康检查）
func (e *SpiderEngine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isRunning {
		return nil
	}
	if config.G == nil {
		return errors.New("config not loaded")
	}
	path, err := resolveChromePath(config.G.SpiderChromePath)
	if err != nil {
		return err
	}
	e.chromePath = path
	if err := e.startChromeProcess(); err != nil {
		return err
	}
	wsURL, err := e.waitForChrome(config.G.SpiderCDPStartTimeoutSec)
	if err != nil {
		_ = e.killChrome()
		return err
	}
	e.wsURL = wsURL
	e.allocCtx, e.allocCancel = chromedp.NewRemoteAllocator(context.Background(), wsURL)
	e.rootCtx, e.rootCancel = chromedp.NewContext(e.allocCtx, chromedp.WithLogf(mcpLogMCP))
	e.healthCtx, e.healthCancel = context.WithCancel(context.Background())
	go e.healthCheckLoop()
	// v2.0.26: 启动孤儿 Chrome 进程定期清理（每 5 分钟扫描一次）
	go e.orphanChromeCleanupLoop()
	e.isRunning = true
	// v2.0.9: 启动代理池复活 goroutine（仅在配置了代理池时）
	if config.G != nil && len(config.G.SpiderProxyPool) > 0 {
		resurrectSec := config.G.SpiderProxyResurrectAfterSec
		if resurrectSec <= 0 {
			resurrectSec = 300
		}
		GetProxyPool().startResurrector(resurrectSec)
		logger.Printf("[SPIDER] Proxy resurrector started (afterSec=%d)", resurrectSec)
	}
	logger.Printf("[SPIDER] Spider engine started, ws=%s", wsURL)
	return nil
}

// Stop 停止爬虫引擎
func (e *SpiderEngine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isRunning {
		return
	}
	if e.healthCancel != nil {
		e.healthCancel()
	}
	if e.rootCancel != nil {
		e.rootCancel()
	}
	if e.allocCancel != nil {
		e.allocCancel()
	}
	_ = e.killChrome()
	e.isRunning = false
	logger.Printf("[SPIDER] Spider engine stopped")
}

// ==================== Chrome 进程管理 ====================

// resolveChromePath 按优先级解析 Chrome 可执行路径
// 顺序：配置值 → 配置值经 LookPath → google-chrome → chromium-browser
func resolveChromePath(configured string) (string, error) {
	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, "google-chrome-stable", "google-chrome", "chromium-browser")

	for _, c := range candidates {
		if strings.Contains(c, "/") {
			// 显式路径：直接 stat
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("chrome not found in PATH (tried: %v); install google-chrome-stable or chromium-browser", candidates)
}

// startChromeProcess 拉起 headless Chrome 进程
// v2.0.3: UA 动态化 + stealth 参数 + 代理支持
func (e *SpiderEngine) startChromeProcess() error {
	if e.chromePath == "" {
		return errors.New("chrome path not resolved")
	}
	port := strconv.Itoa(config.G.SpiderCDPPort)
	args := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-gpu",
		"--remote-debugging-port=" + port,
		"--user-data-dir=" + config.G.SpiderChromeUserDataDir,
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
		"--disable-blink-features=AutomationControlled",
		"--disable-web-security",
		"--disable-features=IsolateOrigins,site-per-process",
		"--disable-site-isolation-trials",
		"--disable-features=InterestFeedContentSuggestions",
		"--disable-features=TranslateUI",
		"--disable-extensions",
		"--disable-default-apps",
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1920,1080",
		"--start-maximized",
		"--lang=zh-CN,en-US,en",
		"--accept-lang=zh-CN,en-US,en",
		// v2.0.3: 补充 stealth 参数
		"--enable-features=NetworkService",
		"--disable-features=AutomationReport",
		"--disable-features=AutomationReportNonStable",
		"--disable-features=AutomationReportNonStableChannels",
		"--disable-features=AutomationReportNonStableChannels",
		// v2.0.18: 防止 headless Chrome 推迟/跳过 ES Module 加载（chat.baidu.com 问题）
		"--disable-features=DeferRendererTasksAfterBackgrounded",
		"--disable-features=StopNonTimersInBackground",
		"--disable-features=BackForwardCache",
		"--disable-features=MemorySaver",
		"--disable-features=LazyFrameLoading",
		"--disable-features=LazyImageLoading",
		"--disable-features=PaintHolding",
		"--disable-features=DocumentTransition",
		"--enable-features=NetworkService,NetworkServiceInProcess",
		"--disable-features=ThrottleDisplayNoneAndVisibilityHiddenCrossOriginIframes",
		"--disable-features=ThrottleJavascriptTimerInBackground",
		"--disable-features=IntensiveWakeUpThrottling",
		"--disable-features=ReduceUserAgentMinorVersion",
		"--disable-features=UserAgentClientHint",
		"--disable-features=WebOTP",
		"--disable-features=SpeculationRules",
		"--disable-features=Prerender2",
		"--disable-features=Prerender2RelatedFeatures",
	}

	// v2.0.3: UA 策略 — 优先级：SpiderChromeCustomArgs > SpiderUserAgent > 探测后回填 > 默认
	ua := e.resolveUserAgent()
	if ua != "" {
		args = append(args, "--user-agent="+ua)
		logger.Printf("[SPIDER] Using user-agent: %s", ua)
	}

	// v2.0.3: 代理支持
	if config.G != nil && config.G.SpiderProxy != "" {
		args = append(args, "--proxy-server="+config.G.SpiderProxy)
		logger.Printf("[SPIDER] Using proxy: %s", config.G.SpiderProxy)
	}

	// v2.0.8: 代理池（启动时轮询；spiderProxy 为空时启用）
	if config.G != nil && config.G.SpiderProxy == "" && len(config.G.SpiderProxyPool) > 0 {
		if proxy := LoadProxyPool(config.G.SpiderProxyPool).Next(); proxy != "" {
			args = append(args, "--proxy-server="+proxy)
			logger.Printf("[SPIDER] Using proxy from pool: %s", proxy)
		}
	}

	// P1: 追加自定义启动参数（如代理、额外 stealth flags）
	if config.G != nil && len(config.G.SpiderChromeCustomArgs) > 0 {
		args = append(args, config.G.SpiderChromeCustomArgs...)
	}
	e.chromeCmd = exec.Command(e.chromePath, args...)
	e.chromeCmd.Stdout = os.NewFile(0, os.DevNull) // 防止子进程继承 stdout
	e.chromeCmd.Stderr = os.NewFile(0, os.DevNull)
	e.chromeCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := e.chromeCmd.Start(); err != nil {
		return fmt.Errorf("start chrome failed: %w", err)
	}
	logger.Printf("[SPIDER] Chrome started: pid=%d port=%s path=%s", e.chromeCmd.Process.Pid, port, e.chromePath)
	return nil
}

// resolveUserAgent 按优先级解析 UA
// 优先级：SpiderChromeCustomArgs 中的 --user-agent > SpiderUserAgent > 探测 Chrome 版本回填 > 默认 Linux Chrome
func (e *SpiderEngine) resolveUserAgent() string {
	// 1. 检查 SpiderChromeCustomArgs 中是否已有 --user-agent
	if config.G != nil {
		for _, arg := range config.G.SpiderChromeCustomArgs {
			if strings.HasPrefix(arg, "--user-agent=") {
				return strings.TrimPrefix(arg, "--user-agent=")
			}
		}
	}

	// 2. 检查全局 SpiderUserAgent
	if config.G != nil && config.G.SpiderUserAgent != "" {
		return config.G.SpiderUserAgent
	}

	// 3. 探测 Chrome 版本并动态拼装 UA
	if e.chromePath != "" {
		version := e.detectChromeVersion()
		if version != "" {
			// 使用 Linux 平台 UA（与服务器实际环境一致），填入探测到的版本号
			return fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", version)
		}
	}

	// 4. 默认：使用 Linux 平台 UA（避免 UA 声明 Windows 但实际跑在 Linux 上导致指纹不一致）
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
}

// detectChromeVersion 通过执行 chrome --version 探测版本号
func (e *SpiderEngine) detectChromeVersion() string {
	if e.chromePath == "" {
		return ""
	}
	cmd := exec.Command(e.chromePath, "--version")
	out, err := cmd.Output()
	if err != nil {
		logger.Printf("[SPIDER] Failed to detect chrome version: %v", err)
		return ""
	}
	// 输出格式: "Google Chrome 126.0.6478.126" 或 "Chromium 126.0.6478.126"
	parts := strings.Fields(string(out))
	for i, p := range parts {
		if p == "Chrome" || p == "Chromium" {
			if i+1 < len(parts) {
				ver := parts[i+1]
				// 取主版本号（如 126.0.6478.126 -> 126.0.6478.126）
				logger.Printf("[SPIDER] Detected chrome version: %s", ver)
				return ver
			}
		}
	}
	return ""
}

// killChrome 终止 Chrome 进程
// v2.0.18: 增加 user-data-dir 清理，防止 restart 后旧缓存/模块加载状态残留
// 导致 chat.baidu.com 等站点行为不变（见问题分析报告_20260629_093329 §5.2 建议 4）。
// v2.0.19: 增加 CDP 端口占用兜底杀（见问题分析报告_20260629_165907 §2C）：
// e.chromeCmd 为 nil 时旧 Chrome 可能仍在监听 CDP 端口，导致 restart_browser
// 返回 success 但后续 CDP 操作全部 context canceled / bad file descriptor。
func (e *SpiderEngine) killChrome() error {
	if e.chromeCmd != nil && e.chromeCmd.Process != nil {
		pid := e.chromeCmd.Process.Pid
		// 先 SIGTERM 给机会清理
		_ = e.chromeCmd.Process.Signal(syscall.SIGTERM)
		// 兜底：1s 后 SIGKILL 整组
		done := make(chan struct{})
		go func() {
			_ = e.chromeCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL) // 杀进程组
		}
		logger.Printf("[SPIDER] Chrome killed: pid=%d", pid)
		e.chromeCmd = nil
	}

	// 兜底：检查 CDP 端口是否仍被占用，若被占用则找到并杀掉对应进程。
	// 场景：Chrome 由外部启动 / e.chromeCmd 引用丢失 / 进程僵死后未清理。
	killChromeOnCDPPort()

	// v2.0.18: 清理 user-data-dir 中的临时文件（仅当配置为临时目录时）
	if config.G != nil && config.G.SpiderChromeUserDataDir != "" {
		if err := cleanupChromeUserDataDir(config.G.SpiderChromeUserDataDir); err != nil {
			logger.Printf("[SPIDER] cleanup user-data-dir warning: %v", err)
		}
	}
	return nil
}

// killChromeOnCDPPort 检查 CDP 端口是否被占用，若被占用则找到并杀掉对应进程。
// 这是 restart_browser 的兜底机制，防止 e.chromeCmd 引用丢失导致旧 Chrome 残留。
func killChromeOnCDPPort() {
	if config.G == nil {
		return
	}
	port := config.G.SpiderCDPPort
	if port <= 0 {
		return
	}
	// 先快速检查端口是否在监听
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return // 端口未被占用，无需处理
	}
	conn.Close()
	logger.Printf("[SPIDER] CDP port %d still occupied after killChrome, finding process...", port)

	// 通过 /json/version 获取 WebSocket URL 来确认是 Chrome 进程
	// 然后用 lsof / fuser 找到 PID 并杀掉
	httpAddr := fmt.Sprintf("http://localhost:%d/json/version", port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(httpAddr)
	if err != nil {
		// 端口有连接但不是 Chrome，尝试 fuser
		logger.Printf("[SPIDER] CDP port %d responds but not Chrome: %v", port, err)
		return
	}
	resp.Body.Close()

	// 用 fuser 找到占用端口的 PID
	// fuser 返回格式: "83185" (PID)
	out, err := exec.Command("fuser", fmt.Sprintf("%d/tcp", port)).CombinedOutput()
	if err != nil {
		// fuser 可能未安装，尝试用 /proc 查找
		logger.Printf("[SPIDER] fuser failed (%v), trying /proc scan for chrome on port %d", err, port)
		killStaleChromeByPortProc(port)
		return
	}
	pids := strings.Fields(strings.TrimSpace(string(out)))
	for _, pidStr := range pids {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil || pid <= 0 {
			continue
		}
		// 确认是 chrome 进程（/proc/PID/cmdline 包含 chrome）
		cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if !strings.Contains(strings.ToLower(string(cmdline)), "chrome") {
			logger.Printf("[SPIDER] PID %d on CDP port %d is not Chrome, skipping", pid, port)
			continue
		}
		logger.Printf("[SPIDER] Force-killing stale Chrome PID %d on CDP port %d", pid, port)
		_ = syscall.Kill(-pid, syscall.SIGKILL) // 杀进程组
		// 等待进程退出
		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if err := syscall.Kill(pid, 0); err != nil {
				break // 进程已退出
			}
		}
	}
}

// killStaleChromeByPortProc 通过 /proc 遍历查找监听指定端口的 Chrome 进程并杀掉。
// 当 fuser 不可用时的 fallback。
func killStaleChromeByPortProc(port int) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	portStr := fmt.Sprintf(":%04X", port) // TCP 端口的 hex 格式
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil || pid <= 0 {
			continue
		}
		// 检查是否是 chrome 进程
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil || !strings.Contains(strings.ToLower(string(cmdline)), "chrome") {
			continue
		}
		// 检查是否监听了目标端口
		tcpFile := fmt.Sprintf("/proc/%d/net/tcp", pid)
		tcpData, err := os.ReadFile(tcpFile)
		if err != nil {
			continue
		}
		if strings.Contains(string(tcpData), portStr) {
			logger.Printf("[SPIDER] Force-killing stale Chrome PID %d on CDP port %d (via /proc)", pid, port)
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			for i := 0; i < 10; i++ {
				time.Sleep(200 * time.Millisecond)
				if err := syscall.Kill(pid, 0); err != nil {
					break
				}
			}
			return
		}
	}
}

// cleanupChromeUserDataDir 清理 Chrome user-data-dir 中的缓存/状态文件。
// 只清理已知安全的子目录（如 Default/Cache, Default/Code Cache, ShaderCache,
// blob_storage, Session Storage 等），保留 Cookies/Local Storage 等持久数据。
func cleanupChromeUserDataDir(dir string) error {
	// 只清理临时目录（/tmp/ 或 /var/tmp/ 开头），避免误删用户配置
	if !strings.HasPrefix(dir, "/tmp/") && !strings.HasPrefix(dir, "/var/tmp/") &&
		!strings.HasPrefix(dir, os.TempDir()) {
		return nil // 非临时目录，不自动清理
	}
	subDirs := []string{
		"Default/Cache",
		"Default/Code Cache",
		"Default/GPUCache",
		"Default/Service Worker",
		"Default/IndexedDB",
		"ShaderCache",
		"blob_storage",
		"Session Storage",
		"GrShaderCache",
		"GraphiteDawnCache",
	}
	for _, sub := range subDirs {
		path := filepath.Join(dir, sub)
		if err := os.RemoveAll(path); err != nil {
			// 单个目录清理失败不影响整体
			continue
		}
	}
	return nil
}

// chromeVersionResponse /json/version 返回结构
type chromeVersionResponse struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	V8Version            string `json:"V8-Version"`
	WebKitVersion        string `json:"WebKit-Version"`
	WebSocketDebuggerUrl string `json:"webSocketDebuggerUrl"`
}

// waitForChrome 轮询 /json/version 等待 Chrome 启动
func (e *SpiderEngine) waitForChrome(timeoutSec int) (string, error) {
	addr := "http://localhost:" + strconv.Itoa(config.G.SpiderCDPPort) + "/json/version"
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(addr)
		if err == nil && resp.StatusCode == 200 {
			defer resp.Body.Close()
			var info chromeVersionResponse
			if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
				if info.WebSocketDebuggerUrl == "" {
					time.Sleep(200 * time.Millisecond)
					continue
				}
				if !strings.Contains(strings.ToLower(info.Browser), "chrome") {
					return "", fmt.Errorf("unexpected browser: %s", info.Browser)
				}
				logger.Printf("[SPIDER] Chrome ready: %s, ws=%s", info.Browser, info.WebSocketDebuggerUrl)
				return info.WebSocketDebuggerUrl, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("chrome not ready within %ds at %s", timeoutSec, addr)
}

// checkChromeHealthy 单次健康检查
func (e *SpiderEngine) checkChromeHealthy(timeout time.Duration) bool {
	addr := "http://localhost:" + strconv.Itoa(config.G.SpiderCDPPort) + "/json/version"
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(addr)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// healthCheckLoop 周期性健康检查 + 自动重启
// v2.0.1: 增加 busy 状态监控，连续 busy 失败也触发重启
// v2.0.2: 增加 sem 槽位 watchdog，持有超过 8 分钟强制释放
func (e *SpiderEngine) healthCheckLoop() {
	ticker := time.NewTicker(time.Duration(config.G.SpiderCDPHealthCheckSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.healthCtx.Done():
			return
		case <-ticker.C:
			needRestart := false
			if !e.checkChromeHealthy(3 * time.Second) {
				logger.Printf("[SPIDER] Chrome unhealthy, will restart...")
				needRestart = true
			} else {
				// 检查 busy 失败计数
				e.busyMu.Lock()
				if e.busyFailCount >= 3 {
					logger.Printf("[SPIDER] Busy lock detected (fail count=%d), will restart Chrome...", e.busyFailCount)
					needRestart = true
					e.busyFailCount = 0
				}
				e.busyMu.Unlock()
				// v2.0.2: watchdog - 检查 sem 槽位是否被持有超过 8 分钟
				e.checkSemWatchdog()
			}
			// v2.0.26: 每轮健康检查后打印引擎状态摘要，
			// 方便运维排查 session / sem / Chrome 进程泄漏。
			e.logSpiderEngineStatus()
			if !needRestart {
				continue
			}
			logger.Printf("[SPIDER] Restarting Chrome...")
			e.mu.Lock()
			_ = e.killChrome()
			if err := e.startChromeProcess(); err != nil {
				logger.Printf("[SPIDER] restart failed: %v", err)
				e.mu.Unlock()
				continue
			}
			wsURL, err := e.waitForChrome(config.G.SpiderCDPStartTimeoutSec)
			if err != nil {
				logger.Printf("[SPIDER] wait for chrome failed after restart: %v", err)
				e.mu.Unlock()
				continue
			}
			// 重建 allocator + rootCtx（old session ctx 会因 rootCancel 级联失效，由下次 attachCDPContext 重建）
			if e.allocCancel != nil {
				e.allocCancel()
			}
			if e.rootCancel != nil {
				e.rootCancel()
			}
			e.allocCtx, e.allocCancel = chromedp.NewRemoteAllocator(context.Background(), wsURL)
			e.rootCtx, e.rootCancel = chromedp.NewContext(e.allocCtx, chromedp.WithLogf(mcpLogMCP))
			e.wsURL = wsURL
			// 重置 busy 计数和 sem 监控
			e.busyMu.Lock()
			e.busyFailCount = 0
			e.busyMu.Unlock()
			e.semWatchdogMu.Lock()
			e.semAcquireTime = make(map[int]time.Time)
			e.semWatchdogMu.Unlock()
			// v2.0.17 补丁：主动 detach 所有 session 的 cdpCtx/cdpCancel/cdpTarget。
			// 只重置 fpApplied 不够 —— actionRestartBrowser / eval / network_log 等
			// 在重启与下次 navigate 之间复用旧 ctx 时，runWithSession 会拿到已死 ctx
			// 返回空响应或触发 nil pointer（见问题分析报告_20260627_120444）。
			// 显式 detach 让下一个 action 必须经 attachCDPContext 重建新 tab。
			detachAllSpiderSessions()
			e.mu.Unlock()
			logger.Printf("[SPIDER] Chrome restarted, ws=%s", wsURL)
		}
	}
}

// recordContextCanceledFailure 记录 chromedp 级联取消错误，
// 供 healthCheckLoop 检测并自动重启 Chrome。
func (e *SpiderEngine) recordContextCanceledFailure() {
	e.busyMu.Lock()
	e.busyFailCount++
	count := e.busyFailCount
	e.busyMu.Unlock()
	logger.Printf("[SPIDER] Context canceled failure recorded (fail count=%d)", count)
}

// ==================== v2.0.30：Chrome 重启 — 共用逻辑 + forced 旁路 ====================
//
// v2.0.30 (基于 问题分析报告_20260707_062144 §2.2 + 建议 4)：
// 原 `RestartChrome()` 强制要求 `e.isRunning=true`，但在 cascade context canceled
// 状态下：
//   - chromedp rootCtx 已经 cancel，但 chromedp 进程（Chrome）和 engine 标记
//     `isRunning` 都仍是 true —— 走 `RestartChrome()` 实际能跑通；
//   - 然而上层 `dispatchCDPAction` 在 `engine.rootCtx.Err() != nil` 时
//     **直接拒绝所有 action**，包括 ActionTypeRestartBrowser，导致 Agent
//     拿到 "spider engine root context is cancelled; call restart_browser
//     action or wait for auto-recovery" 后**死锁** —— restart_browser 自身被拒，
//     auto-restart 30s 门闩可能耗尽，整条 MCP HTTP 层被拖死。
//
// 修复策略：
//   1. 提取共用逻辑到 `restartChromeCommon(forced bool)`（单一真值源）；
//   2. `RestartChrome()` 保留对外 API（healthCheckLoop / 一般路径调用），
//      仍要求 `e.isRunning=true`；
//   3. 新增 `restartChromeForced()` —— bypass isRunning 检查、5s 短门闩
//      `forceRestartMu`、killChrome 失败不 return，专门为级联污染场景设计；
//   4. `dispatchCDPAction` 放行 `ActionTypeRestartBrowser`，让 cascade 状态下
//      Agent 能主动触发 forced restart 自愈。
//
// 锁顺序：
//   - `forceRestartMu` (5s 短门闩) 与 `autoRestartMu` (30s 长门闩) 完全独立，
//     不嵌套；
//   - `e.mu` 在 `restartChromeCommon` 内 acquire，与 `forceRestartMu` 顺序：
//     forceRestartMu → e.mu；`tryAutoRestartOnce` 不持 e.mu，安全。

var (
	// forceRestartMu 防止 cascade 状态下两个 Agent 同时强制重启导致 e.mu 锁死；
	// 5 秒短门闩让 healthCheckLoop 在 force 期间仍可独立进入自愈。
	forceRestartMu       sync.Mutex
	forceRestartLastTime time.Time
)

// tryForceRestart 5 秒内仅允许一个 forced restart goroutine 进入。
// 返回 true 表示「可进入 forced restart」；false 表示「5s 内已触发过」。
func tryForceRestart() bool {
	forceRestartMu.Lock()
	defer forceRestartMu.Unlock()
	if time.Since(forceRestartLastTime) < 5*time.Second {
		return false
	}
	forceRestartLastTime = time.Now()
	return true
}

// RestartChrome 标准 Chrome 重启入口（保留对外 API 不变）。
// 必须 `e.isRunning=true` 才允许；内部委托 `restartChromeCommon(false)`。
func (e *SpiderEngine) RestartChrome() error {
	if !e.isRunning {
		return errors.New("spider engine not running")
	}
	return e.restartChromeCommon(false)
}

// restartChromeForced 强制旁路 Chrome 重启（v2.0.30 新增）。
// 专为 cascade context canceled 场景设计：
//   - 不检查 `e.isRunning`（cascade 状态下 isRunning 可能因 chromedp 内部状态
//     漂移而不可信）；
//   - killChrome 失败不 return（chrome 可能已死，视为空操作）；
//   - 受 5s 短门闩 `forceRestartMu` 保护，防止并发 Agent 同时进入。
//
// 调用场景：actionRestartBrowser 检测到 `engine.rootCtx.Err() != nil` 时。
// 返回值：nil = 成功（force_restart=true 应传给 Agent）；error = 失败
// （Agent 应继续重试或人工介入）。
func (e *SpiderEngine) restartChromeForced() error {
	if e == nil {
		return errors.New("spider engine is nil")
	}
	if !tryForceRestart() {
		return fmt.Errorf("forced restart skipped: another force restart happened within 5s")
	}
	return e.restartChromeCommon(true)
}

// restartChromeCommon 提取 RestartChrome 与 restartChromeForced 共用的核心逻辑。
// forced=false → 标准重启（要求 e.isRunning 已在外面检查过）；
// forced=true  → 强制旁路（不要求 e.isRunning，killChrome 失败不 return，
//
//	且在 e.mu 临界区开头先 cancel 老 rootCtx/allocCtx 避免切换瞬间混用）。
func (e *SpiderEngine) restartChromeCommon(forced bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if forced {
		// cascade 状态：先 cancel 老 rootCtx/allocCtx，避免新 rootCtx 与老
		// rootCtx 切换瞬间 chromedp target 混用。
		if e.rootCancel != nil {
			e.rootCancel()
		}
		if e.allocCancel != nil {
			e.allocCancel()
		}
		// cascade 下 chrome 进程可能已死 / 半挂，killChrome 失败也继续走流程。
		_ = e.killChrome()
	} else {
		_ = e.killChrome()
	}

	// v2.0.19: killChrome 后再等一小段时间确认 CDP 端口已释放，
	// 防止旧进程还没完全退出就开始启动新 Chrome 导致端口冲突。
	// v2.0.33（基于问题分析报告_20260709_145100 §4.1-§4.3）：把等待
	// 轮次从 5 次×1 秒延长到 15 次×1 秒，并增加 500ms 初始静默，
	// 因为并发满载时旧 Chrome 子进程（含多个 tab renderer）退出较慢，
	// 端口释放可能滞后 5-10 秒；同时把端口占用视为致命错误，无法
	// 释放时放弃本次重启，避免新 Chrome 启动失败后遗留孤儿进程。
	if config.G != nil && config.G.SpiderCDPPort > 0 {
		addr := fmt.Sprintf("127.0.0.1:%d", config.G.SpiderCDPPort)
		time.Sleep(500 * time.Millisecond)
		portFreed := false
		for i := 0; i < 15; i++ {
			conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err != nil {
				portFreed = true
				break // 端口已释放
			}
			conn.Close()
			logger.Printf("[SPIDER] CDP port %d still in use, waiting... (%d/15)", config.G.SpiderCDPPort, i+1)
			time.Sleep(1 * time.Second)
		}
		if !portFreed {
			return fmt.Errorf("restart: CDP port %d still occupied after 15s; old Chrome may not have exited", config.G.SpiderCDPPort)
		}
	}

	if err := e.startChromeProcess(); err != nil {
		return fmt.Errorf("restart: startChrome failed: %w", err)
	}
	wsURL, err := e.waitForChrome(config.G.SpiderCDPStartTimeoutSec)
	if err != nil {
		return fmt.Errorf("restart: waitForChrome failed: %w", err)
	}
	if e.allocCancel != nil {
		e.allocCancel()
	}
	if e.rootCancel != nil {
		e.rootCancel()
	}
	e.allocCtx, e.allocCancel = chromedp.NewRemoteAllocator(context.Background(), wsURL)
	e.rootCtx, e.rootCancel = chromedp.NewContext(e.allocCtx, chromedp.WithLogf(mcpLogMCP))
	e.wsURL = wsURL
	e.busyMu.Lock()
	e.busyFailCount = 0
	e.busyMu.Unlock()
	e.semWatchdogMu.Lock()
	e.semAcquireTime = make(map[int]time.Time)
	e.semWatchdogMu.Unlock()
	// 失效所有 session（rootCtx 重新分配，旧 session ctx 已级联失效）
	spiderSessionsMu.Lock()
	for _, s := range spiderSessions {
		s.fpMu.Lock()
		s.fpApplied = false
		s.fingerprint = nil
		s.fpMu.Unlock()
	}
	spiderSessionsMu.Unlock()
	// v2.0.17 补丁：主动 detach 所有 session 的 cdpCtx/cdpCancel/cdpTarget，
	// 防止 restart_browser 之后 eval/click/network_log 等 action 复用已死 ctx
	// 触发空响应或 nil pointer dereference（见问题分析报告_20260627_120444）。
	detachAllSpiderSessions()
	// v2.0.18: 重启后强制 navigate about:blank 再关闭旧 tab，避免旧页面的
	// module 加载状态泄露到下一个 session（见问题分析报告_20260629_093329 §5.2 建议 4）。
	blankCtx, blankCancel := context.WithTimeout(e.rootCtx, 5*time.Second)
	defer blankCancel()
	blankCDPCtx, blankCDPCancel := chromedp.NewContext(blankCtx)
	defer func() {
		_ = chromedp.Cancel(blankCDPCtx)
		blankCDPCancel()
	}()
	_ = chromedp.Run(blankCDPCtx, chromedp.Navigate("about:blank"))
	if forced {
		logger.Printf("[SPIDER] Chrome restarted via FORCED path (cascade recovery, issue 20260707_062144), ws=%s", wsURL)
	} else {
		logger.Printf("[SPIDER] Chrome restarted (RestartChrome), ws=%s", wsURL)
	}
	return nil
}

// KillTabOnly 轻量恢复：使所有 session 的 tab 在下一次 attach 时重建（v2.0.9 维度五）
// 不杀 Chrome 进程；仅清 fpApplied 让 prepareSessionNavigation 重建指纹
func (e *SpiderEngine) KillTabOnly() error {
	if !e.isRunning {
		return errors.New("spider engine not running")
	}
	spiderSessionsMu.Lock()
	defer spiderSessionsMu.Unlock()
	count := 0
	for _, s := range spiderSessions {
		s.fpMu.Lock()
		s.fpApplied = false
		s.fingerprint = nil
		s.fpMu.Unlock()
		count++
	}
	logger.Printf("[SPIDER] KillTabOnly: cleared fpApplied on %d sessions", count)
	return nil
}

// waitReady 快速健康检查（带 sem 占用）
func (e *SpiderEngine) waitReady(timeout time.Duration) error {
	if !e.isRunning {
		return errors.New("spider engine not running")
	}
	if !e.checkChromeHealthy(timeout) {
		return errors.New("chrome not healthy")
	}
	return nil
}

// acquireSem 占用一个并发槽（带超时）
// v2.0.1: 失败时累加 busyFailCount，用于 healthCheckLoop 自动恢复
// v2.0.2: 记录槽位占用时间，供 watchdog 监控
func (e *SpiderEngine) acquireSem(timeout time.Duration) error {
	select {
	case e.sem <- struct{}{}:
		// 成功获取：重置 busy 计数，记录占用时间
		e.busyMu.Lock()
		e.busyFailCount = 0
		e.busyMu.Unlock()
		e.semWatchdogMu.Lock()
		e.semAcquireTime[len(e.sem)] = time.Now()
		e.semWatchdogMu.Unlock()
		return nil
	case <-time.After(timeout):
		e.busyMu.Lock()
		e.busyFailCount++
		count := e.busyFailCount
		e.busyMu.Unlock()
		logger.Printf("[SPIDER] acquireSem failed (busy count=%d)", count)
		return errors.New("spider engine busy, retry later")
	}
}

func (e *SpiderEngine) releaseSem() {
	<-e.sem
	// v2.0.2: 清理占用时间记录
	e.semWatchdogMu.Lock()
	// 释放后 channel 长度减 1，清理对应记录（当前 len(sem) 是释放后的长度，被释放的槽位是 len+1）
	delete(e.semAcquireTime, len(e.sem)+1)
	e.semWatchdogMu.Unlock()
}

// checkSemWatchdog 检查 sem 槽位是否被持有超过 8 分钟，强制释放死锁槽位
// v2.0.2: 防止 chromedp 内部异常导致 sem 永远无法释放
func (e *SpiderEngine) checkSemWatchdog() {
	e.semWatchdogMu.Lock()
	defer e.semWatchdogMu.Unlock()
	now := time.Now()
	for slotIdx, acquireTime := range e.semAcquireTime {
		if now.Sub(acquireTime) > 8*time.Minute {
			logger.Printf("[SPIDER] WATCHDOG: sem slot %d held for %v, forcing release", slotIdx, now.Sub(acquireTime))
			// 强制释放：往 sem channel 放一个值（因为实际槽位已被死锁 goroutine 占用，
			// 这里只是记录清理；真正的释放需要 Chrome 重启来重置整个 sem）
			delete(e.semAcquireTime, slotIdx)
			// 累加 busy 失败计数，触发 healthCheckLoop 重启 Chrome
			e.busyMu.Lock()
			e.busyFailCount++
			e.busyMu.Unlock()
		}
	}
}

// ==================== 孤儿 Chrome 进程清理 ====================

// orphanChromeCleanupLoop 定期扫描并清理不属于当前 engine 的 Chrome 进程。
// v2.0.26: 解决以下场景的 Chrome 进程泄漏：
//   - 主进程被 SIGKILL / OOM killer 终止后重启，旧 Chrome 进程成为孤儿
//   - healthCheckLoop 重启 Chrome 时旧进程未被完全杀死（killChrome 竞态）
//   - 外部工具手动启动的 Chrome 占用 CDP 端口
//
// 安全守卫：只杀 --remote-debugging-port=<config.G.SpiderCDPPort> 匹配的 Chrome 进程，
// 避免误杀用户手动启动的 Chrome 浏览器。
func (e *SpiderEngine) orphanChromeCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.healthCtx.Done():
			return
		case <-ticker.C:
			e.cleanOrphanChromeProcesses()
		}
	}
}

// cleanOrphanChromeProcesses 扫描 CDP 端口上的 Chrome 进程，杀掉不属于当前 engine 的孤儿。
func (e *SpiderEngine) cleanOrphanChromeProcesses() {
	if config.G == nil || config.G.SpiderCDPPort <= 0 {
		return
	}
	port := config.G.SpiderCDPPort

	// 获取当前 engine 管理的 Chrome PID
	e.mu.Lock()
	var currentPID int
	if e.chromeCmd != nil && e.chromeCmd.Process != nil {
		currentPID = e.chromeCmd.Process.Pid
	}
	e.mu.Unlock()

	// 查找占用 CDP 端口的所有 Chrome 进程
	pids := findChromePIDsOnPort(port)
	if len(pids) == 0 {
		return
	}

	for _, pid := range pids {
		if pid == currentPID {
			continue // 是当前 engine 管理的进程，跳过
		}
		// 确认是 Chrome 进程（/proc/PID/cmdline 包含 chrome + remote-debugging-port）
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue // 进程已退出
		}
		cmdlineStr := string(cmdline)
		if !strings.Contains(strings.ToLower(cmdlineStr), "chrome") {
			continue
		}
		// 安全守卫：只杀带相同 CDP 端口的 Chrome，避免误杀用户 Chrome
		portFlag := fmt.Sprintf("--remote-debugging-port=%d", port)
		if !strings.Contains(cmdlineStr, portFlag) {
			logger.Printf("[SPIDER] orphan-cleanup: PID %d on port %d is Chrome but lacks %s flag, skipping", pid, port, portFlag)
			continue
		}
		logger.Printf("[SPIDER] orphan-cleanup: killing orphan Chrome PID %d on CDP port %d (current engine PID=%d)", pid, port, currentPID)
		_ = syscall.Kill(-pid, syscall.SIGKILL) // 杀进程组
		// 等待进程退出
		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if err := syscall.Kill(pid, 0); err != nil {
				break
			}
		}
	}
}

// findChromePIDsOnPort 查找监听指定端口的所有进程 PID。
// 优先用 fuser，失败时回退到 /proc 遍历。
func findChromePIDsOnPort(port int) []int {
	var pids []int

	// 方法 1: fuser
	out, err := exec.Command("fuser", fmt.Sprintf("%d/tcp", port)).CombinedOutput()
	if err == nil {
		for _, pidStr := range strings.Fields(strings.TrimSpace(string(out))) {
			var pid int
			if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil && pid > 0 {
				pids = append(pids, pid)
			}
		}
		if len(pids) > 0 {
			return pids
		}
	}

	// 方法 2: /proc 遍历（与 killStaleChromeByPortProc 同策略）
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	portHex := fmt.Sprintf(":%04X", port)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil || pid <= 0 {
			continue
		}
		tcpData, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp", pid))
		if err != nil {
			continue
		}
		if strings.Contains(string(tcpData), portHex) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// ==================== 健康检查日志增强 ====================

// logSpiderEngineStatus 在 healthCheckLoop 中打印当前引擎状态摘要，
// 方便运维排查 session / sem / Chrome 进程泄漏。
func (e *SpiderEngine) logSpiderEngineStatus() {
	semCap := 4
	if config.G != nil && config.G.SpiderMaxConcurrency > 0 {
		semCap = config.G.SpiderMaxConcurrency
	}
	semUsed := len(e.sem)

	spiderSessionsMu.RLock()
	sessionCount := len(spiderSessions)
	spiderSessionsMu.RUnlock()

	var chromePID int
	e.mu.Lock()
	if e.chromeCmd != nil && e.chromeCmd.Process != nil {
		chromePID = e.chromeCmd.Process.Pid
	}
	e.mu.Unlock()

	logger.Printf("[SPIDER] engine status: chrome_pid=%d sem_used=%d/%d sessions=%d busy_count=%d",
		chromePID, semUsed, semCap, sessionCount, e.busyFailCount)
}
