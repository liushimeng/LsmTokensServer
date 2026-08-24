package spider

// ==================== MCP 共享类型 / 会话管理 / 内容提取 ====================
//
// 该文件集中放置四个 MCP 接口（/SpiderWebData、/GetSpiderDataSource、
// /InputSpiderDailyInfo、/GetSpiderDailyInfo）共享的：
//   1. 通用响应包装（MCPAPIResponse）
//   2. 爬虫会话模型（SpiderSession / SessionAction / PageState / PageLink / PageForm / FormElement）
//   3. 交互动作模型（InteractiveAction + ActionType* 常量）
//   4. 会话管理（spiderSessions / sessionCleanupLoop / getOrCreateSession / generateSessionID / recordSessionAction）
//   5. 内容提取辅助（detectLanguage / extractTitleSimple / extractContentSimpleWithLimit / extractTextFromHTML /
//                    scoreContent / countChineseChars / cleanText / cleanWhitespace / removeHTMLTagsSimple /
//                    htmlUnescapeSimple / readAll / extractPageState）
//   6. HTTP 辅助（mcpSetNoCacheHeaders / mcpLogMCP）
//   7. 服务启动入口（StartMCPWebServer）
//
// 三个接口的请求/响应与 Handler 拆分到：
//   - mcp_interface_spiderwebdata.go          (/SpiderWebData)
//   - mcp_interface_getspiderdatasource.go    (/GetSpiderDataSource)
//   - mcp_interface_inputspiderdailyinfo.go   (/InputSpiderDailyInfo)
//   - mcp_interface_getspiderdailyinfo.go     (/GetSpiderDailyInfo)

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// ==================== MCP 通用响应 ====================

// MCPAPIResponse MCP 通用响应
type MCPAPIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ==================== 交互动作类型常量 ====================
// v2.0.7：在原 14 个 action 基础上扩展为 27 个，覆盖：
//   - 鼠标全功能：左键 click / 右键 right_click / 中键 middle_click / 双击 double_click / 坐标 click_at / 滚轮 wheel
//   - 键盘：press_key（含 modifier 组合键）/ type_text（连续键入文本）
//   - Tab 页面管理：new_tab / switch_tab / close_tab / list_tabs
//   - 调试观察：console_logs / network_log / elements（CSS/XPath 增强抽取）/ dom（节点详情）/ eval（JS 求值）
//   - 存储：localStorage / sessionStorage / cookies
//   - 其他：upload_file（input[type=file]）/ element_screenshot（元素级截图）

const (
	ActionTypeNavigate     = "navigate"      // 导航到新 URL
	ActionTypeClick        = "click"         // 点击元素
	ActionTypeScroll       = "scroll"        // 滚动页面
	ActionTypeScrollTo     = "scroll_to"     // 滚动到特定位置
	ActionTypeFillForm     = "fill_form"     // 填写表单
	ActionTypeExtract      = "extract"       // 提取内容
	ActionTypeScreenshot   = "screenshot"    // 截屏
	ActionTypeGetPageState = "get_state"     // 获取页面状态
	ActionTypeWait         = "wait"          // 等待元素出现或超时
	ActionTypeHover        = "hover"         // 鼠标悬停元素
	ActionTypeSelect       = "select"        // 选择下拉框选项
	ActionTypeKeypress     = "keypress"      // 键盘按键
	ActionTypeSwitchFrame  = "switch_frame"  // 切换 iframe
	ActionTypeDragAndDrop  = "drag_and_drop" // 拖拽元素

	// v2.0.7: 鼠标全功能
	ActionTypeRightClick  = "right_click"  // 右键点击元素
	ActionTypeDoubleClick = "double_click" // 双击元素
	ActionTypeMiddleClick = "middle_click" // 中键点击元素
	ActionTypeClickAt     = "click_at"     // 在 (x, y) 坐标上点击
	ActionTypeMouseMove   = "mouse_move"   // 移动鼠标到 (x, y) 或元素中心
	ActionTypeWheel       = "wheel"        // 鼠标滚轮滚动 (deltaX, deltaY)

	// v2.0.7: 键盘增强
	ActionTypePressKey = "press_key" // 组合键（key + modifiers 数组）
	ActionTypeTypeText = "type_text" // 连续键入文本（逐字符 insertText）

	// v2.0.7: Tab 页面管理（基于 Page domain 的 Target）
	ActionTypeNewTab    = "new_tab"    // 打开新 Tab 并切换过去
	ActionTypeSwitchTab = "switch_tab" // 切换到指定 tab（index 或 url 匹配）
	ActionTypeCloseTab  = "close_tab"  // 关闭指定 tab
	ActionTypeListTabs  = "list_tabs"  // 列出所有 tab

	// v2.0.7: 调试观察
	ActionTypeConsoleLogs = "console_logs" // 读取 Console 输出（log/warn/error/info/debug）
	ActionTypeNetworkLog  = "network_log"  // 读取 Network 请求/响应列表
	ActionTypeElements    = "elements"     // 增强元素抽取（XPath / 区域限定 / attribute 列表）
	ActionTypeDom         = "dom"          // 查询单个 DOM 节点详情（属性/box/可见性/outerHTML）
	ActionTypeEval        = "eval"         // 在页面上下文执行 JS 并返回结果

	// v2.0.7: 存储
	ActionTypeLocalStorage   = "local_storage"   // localStorage get/set/remove/clear/keys
	ActionTypeSessionStorage = "session_storage" // sessionStorage get/set/remove/clear/keys
	ActionTypeCookies        = "cookies"         // cookies get/set/delete/clear

	// v2.0.7: 其他
	ActionTypeUploadFile        = "upload_file"        // 设置 input[type=file] 文件
	ActionTypeElementScreenshot = "element_screenshot" // 单元素截图

	// v2.0.14: 自愈 action
	ActionTypeRestartBrowser = "restart_browser" // 重启 Chrome 进程（级联 context canceled 时手动恢复）
)

// InteractiveAction 交互动作
type InteractiveAction struct {
	Type       string                 `json:"type"`               // 动作类型
	URL        string                 `json:"url,omitempty"`      // 导航目标 URL
	Selector   string                 `json:"selector,omitempty"` // 元素选择器 (CSS/XPath)
	XPath      string                 `json:"xpath,omitempty"`    // XPath 选择器
	Parameters map[string]interface{} `json:"params,omitempty"`   // 其他参数
}

// PageState 页面状态
type PageState struct {
	URL         string            `json:"url"`
	Title       string            `json:"title"`
	Links       []PageLink        `json:"links,omitempty"`
	Forms       []PageForm        `json:"forms,omitempty"`
	ScrollY     int               `json:"scroll_y,omitempty"`
	ScrollX     int               `json:"scroll_x,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// PageLink 页面链接
type PageLink struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

// PageForm 页面表单
type PageForm struct {
	ID       string        `json:"id,omitempty"`
	Action   string        `json:"action,omitempty"`
	Method   string        `json:"method,omitempty"`
	Elements []FormElement `json:"elements,omitempty"`
}

// FormElement 表单元素
type FormElement struct {
	Type string `json:"type"`
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

// SpiderSession 爬虫会话
type SpiderSession struct {
	SessionID      string          `json:"session_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	ExpiresAt      time.Time       `json:"expires_at"` // TTL=10 分钟
	History        []SessionAction `json:"history,omitempty"`
	CurrentURL     string          `json:"current_url"`
	CurrentRawHTML string          `json:"current_raw_html,omitempty"` // 用于 click 动作
	PageState      *PageState      `json:"page_state,omitempty"`

	// CDP 资源（v2.0.0 新增，json 跳过）
	cdpCtx    context.Context    `json:"-"`
	cdpCancel context.CancelFunc `json:"-"`
	cdpTarget string             `json:""`
	cdpMu     sync.Mutex         `json:"-"`

	// v2.0.7: 多 tab 管理（key=alias 字符串，value=targetID）
	// 同一 session 内可开多个 chrome tab，alias 是用户自定义的标识
	SessionTabs map[string]string `json:"session_tabs,omitempty"`
	ActiveTab   string            `json:"active_tab,omitempty"`

	// v2.0.8: per-session 反反爬状态（首次 navigate 时构建，TTL 内冻结）
	fingerprint *Fingerprint
	fpApplied   bool
	fpMu        sync.Mutex

	// v2.0.19: per-session UA 覆盖（移动端降级时临时设置，优先级高于 uaForSession）
	OverrideUA string `json:"-"`

	// v2.0.9: per-session 代理绑定（首次 navigate 时绑定；anti-bot 重试时可重新分配）
	BoundProxy   string     `json:"bound_proxy,omitempty"`
	BoundProxyAt time.Time  `json:"bound_proxy_at,omitempty"`
	BoundProxyMu sync.Mutex `json:"-"`

	// v2.0.9: 资源屏蔽统计（仅在 SpiderBlockResourcesEnabled 时填充；json 跳过）
	BlockStats   *SessionBlockStats `json:"-"`
	BlockStatsMu sync.Mutex         `json:"-"`
}

// SessionBlockStats 资源屏蔽统计（维度三；仅日志和调试字段，不入库）
type SessionBlockStats struct {
	BlockedTotal int            `json:"blocked_total"`
	ByPattern    map[string]int `json:"by_pattern,omitempty"`
	LastLogAt    time.Time      `json:"last_log_at,omitempty"`
}

// SessionAction 会话中的动作记录
type SessionAction struct {
	Timestamp  time.Time         `json:"timestamp"`
	Action     InteractiveAction `json:"action"`
	ResultURL  string            `json:"result_url,omitempty"`
	Screenshot string            `json:"screenshot,omitempty"` // Base64 截图（可选）
}

// ==================== 全局会话存储 ====================

var (
	spiderSessions   = make(map[string]*SpiderSession)
	spiderSessionsMu sync.RWMutex
	spiderSessionTTL = 10 * time.Minute
	// v2.0.33（基于问题分析报告_20260709_145100 §4.1-§4.3）：限制全局 session 数量
	// 上限，防止 5 个 SubAgent 并发 + 每个 agent 内部多次 action 调用时 session map
	// 无界增长，导致 Chrome tab/进程累积泄漏。上限取 SpiderMaxConcurrency×4 且
	// 不低于 16；超过时按 LRU（最早 UpdatedAt）淘汰未活跃 session。
	maxSpiderSessions = 64
)

// v2.0.34（基于问题分析报告_20260709_162130 §建议-中期 3）：全局 panic 计数与
// 最近一次 panic 时间（unix 毫秒，0 表示从未）。所有 spider goroutine / 元素抽取
// 的 recover 路径都调用 recordSpiderPanic 累加，/healthz 透传给外部看门狗，
// 用于识别"持续 panic -> 应停用 MCP Spider 改走 RSS/API 直连"。
var (
	spiderPanicCount    atomic.Int64
	spiderLastPanicAtMs atomic.Int64
)

// recordSpiderPanic 记录一次 spider 相关 panic（计数 + 时间戳 + 日志）。
// 必须在 recover 分支内调用，且自身不可 panic。
func recordSpiderPanic(rec interface{}) {
	spiderPanicCount.Add(1)
	spiderLastPanicAtMs.Store(time.Now().UTC().UnixMilli())
	mcpLogMCP("[SPIDER] panic recorded (total=%d): %v", spiderPanicCount.Load(), rec)
}

// ==================== v2.0.47 MCP 请求关联日志 / 崩溃快照 ====================
//
// 背景（基于 问题分析报告_20260717_094145.md §三 + §六）：
//   现有 MCP 日志只有 mcpLogMCP 一处，没有跨调用栈的关联 ID，
//   运维侧拿到 "PANIC in /SpiderWebData handler" 时无法把 "Received request"
//   和 panic 行串起来；崩溃后也只能看到 stack trace，看不到崩溃瞬间的
//   engine/session/URL/attempt 等上下文，下次类似崩溃定位成本高。
//
// 设计原则：
//   1. 不改 / 不在 HTTP 代理热路径加日志（CLAUDE.md §1 运行保护 + 用户硬约束）
//   2. 只在 MCP 入口（4 个 handler）注入 RequestID + 在 panic recover 路径
//      抓快照；其它内部调用栈通过 `mcpLogMCPWithTag(reqID, ...)` 透传
//   3. 快照通过 atomic.Pointer 暴露给 /healthz，外部看门狗一次 GET 就能拿到
//      崩溃瞬间的系统状态，无需 log grep

// generateRequestID 生成 8 字节十六进制 RequestID（"req-xxxxxxxxxxxxxxxx"）。
// 用 crypto/rand 而非 time.Now() — 防止毫秒级并发请求 ID 碰撞 + 抗时间回拨。
// 单调用约 200ns，4 个 MCP handler 入口每秒最多几百次调用，可忽略成本。
func generateRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败极其罕见（仅 fd 耗尽），降级为时间戳方案。
		return fmt.Sprintf("req-fallback-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(b[:])
}

// CrashSnapshot 描述一次 panic 瞬间的 MCP 服务状态快照（值类型，可直接 JSON 化）。
// v2.0.47：crash 现场上下文固定字段（URL / ActionType / Attempt / SessionID
// / SessionCount / SemUsed / BusyFails / Stack）+ 一个 Map 兜底额外上下文，
// 后续新增字段不破坏 /healthz 契约。
type CrashSnapshot struct {
	RequestID    string                 `json:"request_id"`
	RecordedAtMs int64                  `json:"recorded_at_ms"` // unix 毫秒（UTC）
	URL          string                 `json:"url,omitempty"`
	ActionType   string                 `json:"action_type,omitempty"`
	Attempt      int                    `json:"attempt,omitempty"`
	SessionID    string                 `json:"session_id,omitempty"`
	Method       string                 `json:"method,omitempty"`
	RemoteAddr   string                 `json:"remote_addr,omitempty"`
	PanicValue   string                 `json:"panic_value,omitempty"`
	SessionCount int                    `json:"session_count"`
	SemUsed      int                    `json:"sem_used"`
	SemCapacity  int                    `json:"sem_capacity"`
	BusyFails    int                    `json:"busy_fails"`
	ChromeActive int                    `json:"chrome_active_sessions"`
	ChromePID    int                    `json:"chrome_pid,omitempty"`
	ChromeWS     string                 `json:"chrome_ws,omitempty"`
	Extra        map[string]interface{} `json:"extra,omitempty"`
}

// v2.0.47：最近一次 panic 快照（atomic.Pointer 保证并发读无锁）。
// /healthz 透传给外部看门狗，运维一次 GET 就能看到崩溃现场。
var lastCrashSnapshot atomic.Pointer[CrashSnapshot]

// captureCrashSnapshot 抓取 MCP 服务当前状态快照（必须在 recover 分支内调用，
// 自身只做 atomic load + 字符串拼接，不主动调用任何可能 panic 的 helper）。
//
// 关键约束：
//  1. 不持 spiderSessionsMu 锁（避免与 cleanupLoop 死锁）
//  2. 不调 GetSpiderEngine().xxx() 等可能 nil 解引用的方法（用 len + ok 模式）
//  3. 用 tryAcquire 风格的 sem 探测而非阻塞读
func captureCrashSnapshot(reqID, url, actionType, sessionID string, attempt int, panicValue interface{}) *CrashSnapshot {
	snap := &CrashSnapshot{
		RequestID:    reqID,
		RecordedAtMs: time.Now().UTC().UnixMilli(),
		URL:          truncateForLog(url, 256),
		ActionType:   actionType,
		Attempt:      attempt,
		SessionID:    sessionID,
		PanicValue:   fmt.Sprintf("%v", panicValue),
		Extra:        map[string]interface{}{},
	}

	// Session 数（无锁读 map len，安全；map 本身在 panic 期间不会被并发写）
	spiderSessionsMu.RLock()
	snap.SessionCount = len(spiderSessions)
	if sessionID != "" {
		if s, ok := spiderSessions[sessionID]; ok && s != nil {
			snap.Extra["session_cdp_target_empty"] = s.cdpTarget == ""
			snap.Extra["session_action_count"] = len(s.History)
		}
	}
	spiderSessionsMu.RUnlock()

	// 引擎状态
	if eng := GetSpiderEngine(); eng != nil {
		snap.SemUsed = len(eng.sem)
		if config.G != nil && config.G.SpiderMaxConcurrency > 0 {
			snap.SemCapacity = config.G.SpiderMaxConcurrency
		} else {
			snap.SemCapacity = 4
		}
		eng.busyMu.Lock()
		snap.BusyFails = eng.busyFailCount
		eng.busyMu.Unlock()
		// chrome pid：从 chromeCmd 读进程号（无需持 mu 锁；os.Getpid 本身不 panic）
		if eng.chromeCmd != nil && eng.chromeCmd.Process != nil {
			snap.ChromePID = eng.chromeCmd.Process.Pid
		}
		snap.ChromeWS = eng.wsURL
		// chrome_active_sessions（真占着 tab 的）
		chromeActive := 0
		spiderSessionsMu.RLock()
		for _, s := range spiderSessions {
			if s != nil && s.cdpTarget != "" {
				chromeActive++
			}
		}
		spiderSessionsMu.RUnlock()
		snap.ChromeActive = chromeActive
		snap.Extra["engine_running"] = eng.isRunning
		if eng.rootCtx != nil {
			snap.Extra["root_ctx_err"] = fmt.Sprintf("%v", eng.rootCtx.Err())
		}
	}

	lastCrashSnapshot.Store(snap)
	return snap
}

// getLastCrashSnapshot 读取最近一次快照（/healthz 用，nil-safe）。
func getLastCrashSnapshot() *CrashSnapshot {
	return lastCrashSnapshot.Load()
}

// truncateForLog 截断 URL 等大字段，避免日志爆行。
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated " + fmt.Sprintf("%d", len(s)-max) + " bytes)"
}

// mcpLogMCPWithTag 带 [tag] 前缀的 MCP 日志（如 [req-xxxxxxxx/attempt-2]）。
// 与 mcpLogMCP 同样的 logger.logger 兜底，仅在 print format 前追加 tag，不影响性能。
func mcpLogMCPWithTag(tag, format string, v ...interface{}) {
	if tag == "" {
		mcpLogMCP(format, v...)
		return
	}
	if logger.Ready() {
		logger.Printf("[MCP]["+tag+"] "+format, v...)
	} else {
		logger.Printf("[MCP]["+tag+"] "+format, v...)
	}
}

// mcpLogMCPRequestStart 在 4 个 MCP handler 入口统一打 request-start 日志。
// 返回生成的 RequestID，handler 内部用 mcpLogMCPWithTag(reqID, ...) 透传。
//
// 设计为 `defer mcpLogMCPRequestEnd(reqID, start)` 配对使用 — handler 返回前
// 自动打印 elapsed / status，避免每个 handler 散落"handler end"日志。
func mcpLogMCPRequestStart(endpoint, remoteAddr string) (reqID string, start time.Time) {
	reqID = generateRequestID()
	start = time.Now()
	mcpLogMCPWithTag(reqID, "request START endpoint=%s remote=%s", endpoint, remoteAddr)
	return reqID, start
}

// mcpLogMCPRequestEnd handler 返回前打印 elapsed + 状态码（如能拿到）。
// 在 defer 中调用，因此即使 handler panic 也会执行（与 recover 顺序：recover
// 在 defer 内先执行 → 设 responseWrittenFlag=true → 此 defer 后执行）。
// statusCode 用 *int 形式传入（handler 可能通过 tw.status 内部取），默认 200。
func mcpLogMCPRequestEnd(reqID string, start time.Time, statusCode int, success bool, errMsg string) {
	elapsed := time.Since(start)
	if errMsg == "" && success {
		mcpLogMCPWithTag(reqID, "request END elapsed=%v status=%d success=true", elapsed, statusCode)
	} else {
		trunc := truncateForLog(errMsg, 256)
		mcpLogMCPWithTag(reqID, "request END elapsed=%v status=%d success=false err=%q", elapsed, statusCode, trunc)
	}
}

// setMaxSpiderSessions 根据配置刷新 session 数量上限（启动时调用）。
func setMaxSpiderSessions() {
	cap := 64
	if config.G != nil && config.G.SpiderMaxConcurrency > 0 {
		cap = config.G.SpiderMaxConcurrency * 4
	}
	if cap < 16 {
		cap = 16
	}
	if cap > 256 {
		cap = 256
	}
	maxSpiderSessions = cap
}

// evictOldestSpiderSessionIfNeeded 若 session 数超过上限，按 UpdatedAt 最久淘汰
// 一个未持有的 session（cdpCtx==nil 表示当前没有 action 占用）。如果所有 session
// 都在使用中，跳过淘汰并打日志，避免杀掉正在服务的 tab。
func evictOldestSpiderSessionIfNeeded() {
	for len(spiderSessions) > maxSpiderSessions {
		var oldestID string
		var oldestTime time.Time
		for id, s := range spiderSessions {
			if s == nil {
				oldestID = id
				break
			}
			if s.cdpCtx != nil {
				continue // 正在使用中，不淘汰
			}
			if oldestID == "" || s.CreatedAt.Before(oldestTime) || (s.CreatedAt.Equal(oldestTime) && s.UpdatedAt.Before(oldestTime)) {
				oldestID = id
				oldestTime = s.CreatedAt
			}
		}
		if oldestID == "" {
			mcpLogMCP("[SPIDER] WARN: spider session count %d exceeds cap %d but all sessions are active; consider lowering concurrency", len(spiderSessions), maxSpiderSessions)
			return
		}
		s := spiderSessions[oldestID]
		detachCDPContext(s)
		delete(spiderSessions, oldestID)
		mcpLogMCP("[SPIDER] evicted oldest idle spider session %s (count=%d, cap=%d)", oldestID, len(spiderSessions), maxSpiderSessions)
	}
}

// sessionCleanupLoop 定期清理过期会话（每分钟跑一次）
// v2.0.26: 改用 detachCDPContext 幂等释放全部 CDP 资源（cdpCtx / cdpCancel /
// cdpTarget + sem 槽位），而非仅调 s.cdpCancel()。原实现在 cdpCancel 的
// sync.Once 已被前一次调用消费后，cdpCtx / cdpTarget 字段不会被清空，
// 导致残留 tab 引用和 sem 泄漏。
func sessionCleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().UTC()
		spiderSessionsMu.Lock()
		for id, s := range spiderSessions {
			if now.After(s.ExpiresAt) {
				// 幂等释放全部 CDP 资源（cdpCtx / cdpCancel / cdpTarget / sem）
				detachCDPContext(s)
				delete(spiderSessions, id)
				mcpLogMCP("Cleaned up expired spider session: %s", id)
			}
		}
		spiderSessionsMu.Unlock()
	}
}

// releaseSpiderSession 完整释放一个 session：在 spiderSessionsMu 锁下调用
// detachCDPContext（取消 CDP context / 释放 sem 槽位 / 清空 cdpTarget）并将其从
// 全局 map 中删除。
//
// v2.0.34（基于问题分析报告_20260709_162130 §4.2 / §建议-中期 2）：原 detachCDPContext
// 只清字段不删 map entry，导致 session_total 在 Chrome 重启 / panic / 超时后仍累加，
// 与 sem_used=0 矛盾（"信号量释放了，session 注册表没释放"）。本函数统一"释放即删除"
// 语义，供 panic recover / detachAllSpiderSessions 等不持 spiderSessionsMu 的路径使用。
//
// 注意：调用方不能已持有 spiderSessionsMu（会自死锁）；持 spiderSessionsMu 的路径
// （sessionCleanupLoop / getOrCreateSession / evictOldestSpiderSessionIfNeeded）
// 仍走内联 detach+delete。本函数对 nil / 已删除 session 幂等。
func releaseSpiderSession(s *SpiderSession) {
	if s == nil {
		return
	}
	spiderSessionsMu.Lock()
	// v2.0.35（基于问题分析报告_20260709_174800 §2.2 实测）：
	// rotateSessionID（anti-bot retry）会改 s.SessionID 字段为 "xxx_r1_r2"
	// 后缀，但 spiderSessions map 的 key 仍是原始 ID。若只按 s.SessionID 查
	// map 会找不到 entry -> delete 失败 -> session_total 永久泄漏（实测
	// DNS/captcha 失败一次后 session_total=1 不归零）。
	// 修复：先按当前 SessionID 查；命中即删；未命中再按指针遍历 map 反查
	// 删除指向同一 session 的 entry，保证"释放即删除"语义在 rotateSessionID
	// 之后依然成立。
	if _, ok := spiderSessions[s.SessionID]; ok {
		detachCDPContext(s)
		delete(spiderSessions, s.SessionID)
		mcpLogMCP("[SPIDER] released spider session %s from map (remaining=%d)", s.SessionID, len(spiderSessions))
	} else {
		// 按指针反查：rotateSessionID 改了 SessionID 字段但没改 map key
		foundKey := ""
		for id, ss := range spiderSessions {
			if ss == s {
				foundKey = id
				break
			}
		}
		if foundKey != "" {
			detachCDPContext(s)
			delete(spiderSessions, foundKey)
			mcpLogMCP("[SPIDER] released spider session by pointer (mapKey=%s, currentID=%s, remaining=%d)", foundKey, s.SessionID, len(spiderSessions))
		} else {
			// 已不在 map 中（可能被并发路径删除），仍兜底释放其 CDP 资源避免 sem 泄漏。
			detachCDPContext(s)
		}
	}
	spiderSessionsMu.Unlock()
}

// detachAllSpiderSessions 释放所有 session 的 CDP 资源（cdpCtx / cdpCancel / cdpTarget）。
// 用于 Chrome 进程重启后让每个 session 必须在下次 attachCDPContext 中重建 tab，避免
// 旧 ctx 残留导致后续 action（eval / click / network_log 等）触发空响应或 nil pointer。
// v2.0.17 补丁：见问题分析报告_20260627_120444 restart_browser 级联失败 case。
// v2.0.34：释放后同步清空 spiderSessions map，避免重启后 session_total 仍累加
// （问题分析报告_20260709_162130 §4.2：sem_used=0 但 session_total=13 的矛盾）。
func detachAllSpiderSessions() {
	spiderSessionsMu.Lock()
	defer spiderSessionsMu.Unlock()
	for id, s := range spiderSessions {
		if s == nil {
			delete(spiderSessions, id)
			continue
		}
		// detachCDPContext 内部会处理 cdpCancel 的 sync.Once 幂等；
		// 这里直接调用，让 releaseSemOnce 把 sem 槽也释放出来。
		detachCDPContext(s)
		delete(spiderSessions, id)
	}
}
func generateSessionID() string {
	return fmt.Sprintf("spider_%d", time.Now().UnixNano())
}

// getOrCreateSession 获取或创建会话
func getOrCreateSession(sessionID string) *SpiderSession {
	now := time.Now().UTC()
	if sessionID != "" {
		spiderSessionsMu.RLock()
		session, exists := spiderSessions[sessionID]
		spiderSessionsMu.RUnlock()
		if exists {
			if now.After(session.ExpiresAt) {
				spiderSessionsMu.Lock()
				// v2.0.26: 释放过期 session 的 CDP 资源后再删除，
				// 避免 tab 和 sem 槽位泄漏（原实现仅 delete map entry）。
				detachCDPContext(session)
				delete(spiderSessions, sessionID)
				spiderSessionsMu.Unlock()
				mcpLogMCP("Session %s expired, detached CDP context and creating new one", sessionID)
			} else {
				session.UpdatedAt = now
				session.ExpiresAt = now.Add(spiderSessionTTL)
				return session
			}
		}
	}

	newID := generateSessionID()
	session := &SpiderSession{
		SessionID: newID,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(spiderSessionTTL),
	}
	spiderSessionsMu.Lock()
	// v2.0.33（基于问题分析报告_20260709_145100 §4.1-§4.3）：创建新 session 前
	// 先做 LRU 淘汰，避免并发高峰时 session map 无界增长导致 Chrome 进程累积。
	// 注意：必须在写入新 session 之前淘汰到上限以下，否则总量仍会超限。
	for len(spiderSessions) >= maxSpiderSessions {
		var oldestID string
		var oldestTime time.Time
		for id, s := range spiderSessions {
			if s == nil {
				oldestID = id
				break
			}
			if s.cdpCtx != nil {
				continue
			}
			if oldestID == "" || s.CreatedAt.Before(oldestTime) || (s.CreatedAt.Equal(oldestTime) && s.UpdatedAt.Before(oldestTime)) {
				oldestID = id
				oldestTime = s.CreatedAt
			}
		}
		if oldestID == "" {
			mcpLogMCP("[SPIDER] WARN: cannot create new session: count=%d at cap=%d and all sessions active", len(spiderSessions), maxSpiderSessions)
			break
		}
		detachCDPContext(spiderSessions[oldestID])
		delete(spiderSessions, oldestID)
		mcpLogMCP("[SPIDER] evicted oldest idle spider session %s to make room (count=%d, cap=%d)", oldestID, len(spiderSessions), maxSpiderSessions)
	}
	spiderSessions[newID] = session
	spiderSessionsMu.Unlock()
	return session
}

const maxSessionHistory = 50

// recordSessionAction 记录会话动作
func recordSessionAction(session *SpiderSession, action InteractiveAction, resultURL string) {
	if session == nil {
		return
	}
	session.History = append(session.History, SessionAction{
		Timestamp: time.Now().UTC(),
		Action:    action,
		ResultURL: resultURL,
	})
	if len(session.History) > maxSessionHistory {
		session.History = session.History[len(session.History)-maxSessionHistory:]
	}
	session.UpdatedAt = time.Now().UTC()
}

// ==================== 语言检测 ====================

// detectLanguage 简单的语言检测
func detectLanguage(text string) string {
	if text == "" {
		return "unknown"
	}

	chineseChars := countChineseChars(text)
	totalChars := len([]rune(text))

	if totalChars == 0 {
		return "unknown"
	}

	chineseRatio := float64(chineseChars) / float64(totalChars)

	if chineseRatio > 0.3 {
		return "zh"
	}

	englishPatterns := []string{"the", "a", "is", "are", "was", "were", "in", "on", "at"}
	lowerText := strings.ToLower(text)
	englishCount := 0
	for _, p := range englishPatterns {
		if strings.Contains(lowerText, " "+p+" ") {
			englishCount++
		}
	}

	if englishCount >= 2 {
		return "en"
	}

	return "unknown"
}

// ==================== 内容提取器 ====================

// 常见内容容器选择器（用于正则匹配）
var (
	// 标题提取正则
	reTitleMeta  = regexp.MustCompile(`(?is)<meta[^>]*property="og:title"[^>]*content="([^"]*)"`)
	reTitleMeta2 = regexp.MustCompile(`(?is)<meta[^>]*name="twitter:title"[^>]*content="([^"]*)"`)

	// 描述提取正则
	reDescMeta  = regexp.MustCompile(`(?is)<meta[^>]*property="og:description"[^>]*content="([^"]*)"`)
	reDescMeta2 = regexp.MustCompile(`(?is)<meta[^>]*name="description"[^>]*content="([^"]*)"`)

	// 文章内容区域正则
	reArticleTags = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>`),
		regexp.MustCompile(`(?is)<div[^>]*class="[^"]*article[^"]*"[^>]*>(.*?)</div>`),
		regexp.MustCompile(`(?is)<div[^>]*class="[^"]*content[^"]*"[^>]*>(.*?)</div>`),
		regexp.MustCompile(`(?is)<div[^>]*class="[^"]*main[^"]*"[^>]*>(.*?)</div>`),
		regexp.MustCompile(`(?is)<div[^>]*id="[^"]*article[^"]*"[^>]*>(.*?)</div>`),
		regexp.MustCompile(`(?is)<div[^>]*id="[^"]*content[^"]*"[^>]*>(.*?)</div>`),
		regexp.MustCompile(`(?is)<main[^>]*>(.*?)</main>`),
	}

	// 段落提取
	reParagraph = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)

	// 清理用正则
	reScript   = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle    = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reComment  = regexp.MustCompile(`(?is)<!--.*?-->`)
	reNoscript = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	reIframe   = regexp.MustCompile(`(?is)<iframe[^>]*>.*?</iframe>`)
	reNav      = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	reFooter   = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`)
	reHeader   = regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`)
	reAside    = regexp.MustCompile(`(?is)<aside[^>]*>.*?</aside>`)
	reAds      = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*ad[^"]*"[^>]*>.*?</div>`)
)

// extractTitleSimple 增强的标题提取
func extractTitleSimple(html string) string {
	if matches := reTitleMeta.FindStringSubmatch(html); len(matches) >= 2 {
		if title := cleanText(matches[1]); title != "" {
			return title
		}
	}
	if matches := reTitleMeta2.FindStringSubmatch(html); len(matches) >= 2 {
		if title := cleanText(matches[1]); title != "" {
			return title
		}
	}
	reTitle := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	if matches := reTitle.FindStringSubmatch(html); len(matches) >= 2 {
		if title := cleanText(matches[1]); title != "" {
			return title
		}
	}
	reH1 := regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	if matches := reH1.FindStringSubmatch(html); len(matches) >= 2 {
		if title := cleanText(removeHTMLTagsSimple(matches[1])); title != "" {
			return title
		}
	}
	return "Untitled"
}

// extractContentSimpleWithLimit 增强的内容提取（支持长度限制）
func extractContentSimpleWithLimit(html string, maxContentLen int) string {
	if maxContentLen <= 0 {
		maxContentLen = 10000
	}

	var desc string
	if matches := reDescMeta.FindStringSubmatch(html); len(matches) >= 2 {
		desc = cleanText(matches[1])
	} else if matches := reDescMeta2.FindStringSubmatch(html); len(matches) >= 2 {
		desc = cleanText(matches[1])
	}

	cleaned := html
	cleaned = reScript.ReplaceAllString(cleaned, "")
	cleaned = reStyle.ReplaceAllString(cleaned, "")
	cleaned = reComment.ReplaceAllString(cleaned, "")
	cleaned = reNoscript.ReplaceAllString(cleaned, "")
	cleaned = reIframe.ReplaceAllString(cleaned, "")
	cleaned = reNav.ReplaceAllString(cleaned, "")
	cleaned = reFooter.ReplaceAllString(cleaned, "")
	cleaned = reHeader.ReplaceAllString(cleaned, "")
	cleaned = reAside.ReplaceAllString(cleaned, "")
	cleaned = reAds.ReplaceAllString(cleaned, "")

	var bestContent string
	var bestScore int

	for _, re := range reArticleTags {
		matches := re.FindAllString(cleaned, -1)
		for _, match := range matches {
			content := extractTextFromHTML(match)
			score := scoreContent(content)
			if score > bestScore && score > 50 {
				bestScore = score
				bestContent = content
			}
		}
	}

	if bestScore < 100 {
		var paragraphs []string
		matches := reParagraph.FindAllStringSubmatch(cleaned, -1)
		for _, m := range matches {
			if len(m) >= 2 {
				p := cleanText(removeHTMLTagsSimple(m[1]))
				if len(p) > 20 {
					paragraphs = append(paragraphs, p)
				}
			}
		}
		if len(paragraphs) > 0 {
			bestContent = strings.Join(paragraphs, "\n\n")
		}
	}

	if bestContent == "" {
		bestContent = removeHTMLTagsSimple(cleaned)
		bestContent = cleanText(bestContent)
	}

	if desc != "" && len(bestContent) < 200 {
		if !strings.Contains(bestContent, desc) {
			bestContent = desc + "\n\n" + bestContent
		}
	}

	bestContent = cleanWhitespace(bestContent)
	if len(bestContent) > maxContentLen {
		// v2.0.19 补丁（基于问题分析报告_20260630_095236 §3.1）：
		// 显式 clamp maxContentLen 到 bestContent 长度，避免未来重构去掉外层
		// if 条件时触发 s[negative:maxContentLen] panic。三个分支都共用一个
		// safeMax，所有 LastIndex + slice 都基于 safeMax。
		safeMax := maxContentLen
		if safeMax > len(bestContent) {
			safeMax = len(bestContent)
		}
		if idx := strings.LastIndex(bestContent[:safeMax], "。"); idx > safeMax*3/4 {
			bestContent = bestContent[:idx+1] + "..."
		} else if idx := strings.LastIndex(bestContent[:safeMax], "."); idx > safeMax*3/4 {
			bestContent = bestContent[:idx+1] + "..."
		} else {
			bestContent = bestContent[:safeMax] + "..."
		}
	}

	return bestContent
}

// extractTextFromHTML 从 HTML 片段提取文本
func extractTextFromHTML(html string) string {
	var paragraphs []string
	matches := reParagraph.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			p := cleanText(removeHTMLTagsSimple(m[1]))
			if len(p) > 15 {
				paragraphs = append(paragraphs, p)
			}
		}
	}

	if len(paragraphs) > 0 {
		return strings.Join(paragraphs, "\n\n")
	}

	return cleanText(removeHTMLTagsSimple(html))
}

// scoreContent 给提取的内容评分
func scoreContent(content string) int {
	if content == "" {
		return 0
	}

	score := 0
	contentLen := len(content)
	if contentLen > 500 {
		score += 50
	} else if contentLen > 200 {
		score += 30
	} else if contentLen > 100 {
		score += 10
	}

	paragraphs := strings.Split(content, "\n\n")
	if len(paragraphs) > 3 {
		score += 30
	} else if len(paragraphs) > 1 {
		score += 15
	}

	chineseChars := countChineseChars(content)
	if chineseChars > 50 {
		score += 40
	} else if chineseChars > 20 {
		score += 20
	}

	return score
}

// countChineseChars 统计中文字符数
func countChineseChars(s string) int {
	count := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			count++
		}
	}
	return count
}

// cleanText 清理文本
func cleanText(s string) string {
	s = htmlUnescapeSimple(s)
	s = strings.TrimSpace(s)
	return s
}

// cleanWhitespace 清理空白
func cleanWhitespace(s string) string {
	reMultiNewline := regexp.MustCompile(`\n\s*\n\s*\n`)
	s = reMultiNewline.ReplaceAllString(s, "\n\n")
	reMultiSpace := regexp.MustCompile(`[ \t]+`)
	s = reMultiSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// removeHTMLTagsSimple 移除 HTML 标签
func removeHTMLTagsSimple(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return re.ReplaceAllString(s, "")
}

// htmlUnescapeSimple 简单的 HTML 实体解码
func htmlUnescapeSimple(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&#8211;", "-")
	s = strings.ReplaceAll(s, "&#8212;", "-")
	s = strings.ReplaceAll(s, "&#8217;", "'")
	s = strings.ReplaceAll(s, "&#8220;", "\"")
	s = strings.ReplaceAll(s, "&#8221;", "\"")
	s = strings.ReplaceAll(s, "&#x201c;", "\"")
	s = strings.ReplaceAll(s, "&#x201d;", "\"")
	s = strings.ReplaceAll(s, "&#x2018;", "'")
	s = strings.ReplaceAll(s, "&#x2019;", "'")
	s = strings.ReplaceAll(s, "&#8230;", "...")
	s = strings.ReplaceAll(s, "&hellip;", "...")
	return s
}

// readAll 读取响应体（v2.0.0: 保留兼容旧代码，新代码优先用 io.ReadAll）
func readAll(body io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(body, limit))
}

// extractPageState 从 HTML 提取页面状态
func extractPageState(html, url string) *PageState {
	state := &PageState{
		URL:   url,
		Title: extractTitleSimple(html),
	}

	linkRe := regexp.MustCompile(`(?is)<a[^>]*href="([^"]*)"[^>]*>([^<]*)</a>`)
	linkMatches := linkRe.FindAllStringSubmatch(html, -1)
	for _, m := range linkMatches {
		if len(m) >= 3 {
			href := cleanText(m[1])
			text := cleanText(m[2])
			if href != "" && href != "#" {
				state.Links = append(state.Links, PageLink{
					Text: text,
					Href: href,
				})
			}
		}
	}

	if len(state.Links) > 100 {
		state.Links = state.Links[:100]
	}

	return state
}

// ==================== 网页元素抽取（v2.0.1） ====================
// 设计目标：把网页拆成"链接 / 标题 / 段落"三类元素，配合完整 URL 与原文片段，
// 由 Agent 自己判断：单篇文章、文章列表、还是需要继续导航的入口页。
// 本程序不做"是否文章"的语义判定——这是 Agent 的职责。

// WebElementLink 元素：链接（导航 / 文章列表 / 推荐等）
type WebElementLink struct {
	Text  string `json:"text"`            // 链接显示文本（已清理）
	Href  string `json:"href"`            // 原始 href（相对路径可能存在）
	URL   string `json:"url"`             // 解析后的完整 URL（resolveURL(base, href)）
	Title string `json:"title,omitempty"` // title 属性
	Rel   string `json:"rel,omitempty"`   // rel 属性（nofollow / next ...）
	Scope string `json:"scope,omitempty"` // 所在容器："nav" / "article" / "list" / "body"
}

// WebElementHeading 元素：标题（h1/h2/h3）
type WebElementHeading struct {
	Level int    `json:"level"`         // 1/2/3
	Text  string `json:"text"`          // 标题文本
	URL   string `json:"url,omitempty"` // 标题内若有 <a>，其解析后的完整 URL
}

// WebElementParagraph 元素：段落（长文本候选）
type WebElementParagraph struct {
	Text      string `json:"text"`              // 段落原文
	Snippet   string `json:"snippet,omitempty"` // 长度 <= 240 的预览
	URL       string `json:"url,omitempty"`     // 段落所在 article/a 的解析后 URL（如有）
	WordCount int    `json:"word_count"`        // 中文字符数（用于 Agent 过滤）
}

// WebElements 网页元素集合（统一返回结构）
type WebElements struct {
	Links      []WebElementLink      `json:"links,omitempty"`      // 所有 <a href="...">（去重 + 绝对化）
	Headings   []WebElementHeading   `json:"headings,omitempty"`   // h1/h2/h3
	Paragraphs []WebElementParagraph `json:"paragraphs,omitempty"` // 长段落正文候选
	// Articles v2.0.18 补丁：列表型页面（<li> 卡片 / <article> 卡片）抽取
	// 的「标题 + URL + 摘要」三元组。当 SSR HTML 中 list 项内嵌 <h2><a>
	// + <p> 摘要但 live DOM 因 React/未水合拿不到时，Agent 可直接复用
	// Articles 字段得到"标题-简报-完整 URL"对齐结果。
	Articles []WebElementArticle `json:"articles,omitempty"`
}

// WebElementArticle v2.0.18：列表卡片抽取结果（机器之心文章库类站点）。
type WebElementArticle struct {
	Title    string `json:"title"`              // 标题文本（来自 <h2><a> 或 <h3><a>）
	URL      string `json:"url,omitempty"`      // 标题链接的绝对 URL
	Summary  string `json:"summary,omitempty"`  // 摘要（同一卡片内的短 <p>，<=200 字）
	Position int    `json:"position,omitempty"` // 在 list 中的位置（0-indexed）
}

// 容器识别选择器（用于给 WebElementLink 标 scope）
var (
	reScopeNav     = regexp.MustCompile(`(?is)<(nav|header)\b[^>]*>`)
	reScopeArticle = regexp.MustCompile(`(?is)<article\b[^>]*>`)
	reScopeList    = regexp.MustCompile(`(?is)<(ul|ol)\b[^>]*>`)

	reScopeNavEnd     = regexp.MustCompile(`(?is)</(nav|header)>`)
	reScopeArticleEnd = regexp.MustCompile(`(?is)</article>`)
	reScopeListEnd    = regexp.MustCompile(`(?is)</(ul|ol)>`)

	// 元素抽取正则
	reAllAnchor = regexp.MustCompile(`(?is)<a\b([^>]*)>([\s\S]*?)</a>`)
	reAttr      = regexp.MustCompile(`(?is)\b(href|title|rel)\s*=\s*"([^"]*)"`)

	reHeading1 = regexp.MustCompile(`(?is)<h1\b([^>]*)>([\s\S]*?)</h1>`)
	reHeading2 = regexp.MustCompile(`(?is)<h2\b([^>]*)>([\s\S]*?)</h2>`)
	reHeading3 = regexp.MustCompile(`(?is)<h3\b([^>]*)>([\s\S]*?)</h3>`)

	// 用于段落抽取（长段落候选）
	reContainerArticle = regexp.MustCompile(`(?is)<article\b[^>]*>([\s\S]*?)</article>`)
	reContainerMain    = regexp.MustCompile(`(?is)<main\b[^>]*>([\s\S]*?)</main>`)

	// v2.0.18 列表卡片抽取：<li>...</li> / <article>...</article>。
	// 每个卡片内要求有 <h2><a> / <h3><a> 形式的标题链接；
	// 摘要取卡片内第一个非空 <p>，<=200 字。
	reLiItem  = regexp.MustCompile(`(?is)<li\b[^>]*>([\s\S]*?)</li>`)
	rePSimple = regexp.MustCompile(`(?is)<p\b[^>]*>([\s\S]*?)</p>`)
)

// isInsideScope 判断 match 索引是否落在 scopeMarkers 内（用于 nav/list/article 标注）
func isInsideScope(idx int, markers [][3]interface{}) string {
	for _, m := range markers {
		start, ok1 := m[0].(int)
		end, ok2 := m[1].(int)
		if !ok1 || !ok2 {
			continue
		}
		if idx >= start && idx <= end {
			if s, ok := m[2].(string); ok {
				return s
			}
		}
	}
	return "body"
}

// collectScopeMarkers 扫描 html，定位 nav/article/ul 容器从开标签到闭标签的字节区间。
// 用区间匹配（而不是仅看开标签），因为链接/段落通常在容器内部，开标签之后。
func collectScopeMarkers(html string) [][3]interface{} {
	type spec struct {
		open  *regexp.Regexp
		close *regexp.Regexp
		scope string
	}
	specs := []spec{
		{reScopeNav, reScopeNavEnd, "nav"},
		{reScopeArticle, reScopeArticleEnd, "article"},
		{reScopeList, reScopeListEnd, "list"},
	}
	out := make([][3]interface{}, 0, 16)
	for _, sp := range specs {
		opens := sp.open.FindAllStringIndex(html, -1)
		for _, om := range opens {
			// 找闭标签（忽略嵌套同标签，按第一个闭标签算）
			cl := sp.close.FindStringIndex(html[om[1]:])
			var end int
			if cl != nil {
				end = om[1] + cl[1]
			} else {
				// 没找到闭标签就一路到文末
				end = len(html)
			}
			out = append(out, [3]interface{}{om[0], end, sp.scope})
		}
	}
	return out
}

// resolveLinkScope 根据元素索引估算 scope
func resolveLinkScope(idx int, markers [][3]interface{}) string {
	return isInsideScope(idx, markers)
}

// parseAttrs 从 <a ...> 属性串中提取 href/title/rel
func parseAttrs(attrStr string) (href, title, rel string) {
	for _, m := range reAttr.FindAllStringSubmatch(attrStr, -1) {
		if len(m) < 3 {
			continue
		}
		switch strings.ToLower(m[1]) {
		case "href":
			href = m[2]
		case "title":
			title = m[2]
		case "rel":
			rel = m[2]
		}
	}
	return
}

// safeSubmatchSlice 返回 html 中第 pair 个捕获组的切片 html[m[2*pair]:m[2*pair+1]]，
// 仅当该 submatch 索引对存在、且起止均非负、且在字符串范围内时才切片。
//
// v2.0.34（基于问题分析报告_20260709_162130 §4.1）：正则 FindAllStringSubmatchIndex
// 返回的索引对在畸形 HTML / 部分匹配下可能为负数（-1 表示未参与匹配），直接用于切片会
// 触发 "index out of range [-1]/[-2]" panic，进而把整个 spider goroutine 拉死。
// 所有对 submatch 切片的访问统一走本函数，杜绝负数索引 panic。
func safeSubmatchSlice(s string, m []int, pair int) string {
	idx := pair * 2
	if m == nil || idx < 0 || idx+1 >= len(m) {
		return ""
	}
	start, end := m[idx], m[idx+1]
	if start < 0 || end < 0 || start > end || start > len(s) || end > len(s) {
		return ""
	}
	return s[start:end]
}

// extractWebElements 抽取页面元素集合
//   - 链接：去重 + 绝对化 + scope 标注，最多 200 条
//   - 标题：h1/h2/h3，最多 50 条
//   - 段落：从 article/main/正文 p 中挑选 >60 中文字 或 >120 英文词的段落，最多 20 条
func extractWebElements(html, baseURL string) (out *WebElements) {
	// v2.0.34：兜底 recover，保证抽取过程任何残留 panic 都返回非 nil 空集合，
	// 而不是把 spider goroutine 整个拉死（见问题分析报告_20260709_162130 §4.1）。
	defer func() {
		if rec := recover(); rec != nil {
			recordSpiderPanic(rec)
			mcpLogMCP("[SPIDER] PANIC in extractWebElements: %v", rec)
			out = &WebElements{
				Links:      []WebElementLink{},
				Headings:   []WebElementHeading{},
				Paragraphs: []WebElementParagraph{},
				Articles:   []WebElementArticle{},
			}
		}
	}()

	if strings.TrimSpace(html) == "" {
		// v2.0.18 补丁：返回空 HTML 时也保留非 nil 空 slice 字段，
		// 确保 JSON 契约稳定（Agent 端可写 els.articles || [] 而无需 nil 检查）。
		return &WebElements{
			Links:      []WebElementLink{},
			Headings:   []WebElementHeading{},
			Paragraphs: []WebElementParagraph{},
			Articles:   []WebElementArticle{},
		}
	}

	out = &WebElements{
		Links:      []WebElementLink{},
		Headings:   []WebElementHeading{},
		Paragraphs: []WebElementParagraph{},
	}

	scopeMarkers := collectScopeMarkers(html)

	// ---- 链接 ----
	seen := make(map[string]struct{}, 64)
	for _, m := range reAllAnchor.FindAllStringSubmatchIndex(html, -1) {
		if len(m) < 6 {
			continue
		}
		// v2.0.34：submatch 索引可能为负（部分匹配），统一走 safeSubmatchSlice
		attrStr := safeSubmatchSlice(html, m, 1)
		innerStr := safeSubmatchSlice(html, m, 2)
		href, title, rel := parseAttrs(attrStr)
		href = strings.TrimSpace(htmlUnescapeSimple(href))
		if href == "" || href == "#" || strings.HasPrefix(strings.ToLower(href), "javascript:") || strings.HasPrefix(strings.ToLower(href), "mailto:") {
			continue
		}
		text := cleanText(removeHTMLTagsSimple(innerStr))
		if text == "" {
			// 文本全空的链接跳过
			continue
		}
		abs := resolveURL(baseURL, href)
		key := abs
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		scope := resolveLinkScope(m[0], scopeMarkers)
		out.Links = append(out.Links, WebElementLink{
			Text:  truncateRunes(text, 200),
			Href:  href,
			URL:   abs,
			Title: cleanText(htmlUnescapeSimple(title)),
			Rel:   strings.TrimSpace(rel),
			Scope: scope,
		})
		if len(out.Links) >= 200 {
			break
		}
	}

	// ---- 标题 ----
	headingRegexes := []*regexp.Regexp{reHeading1, reHeading2, reHeading3}
	for levelIdx, re := range headingRegexes {
		level := levelIdx + 1
		for _, m := range re.FindAllStringSubmatchIndex(html, -1) {
			if len(m) < 6 {
				continue
			}
			// v2.0.34：submatch 索引统一走 safeSubmatchSlice
			attrStr := safeSubmatchSlice(html, m, 1)
			innerStr := safeSubmatchSlice(html, m, 2)
			text := cleanText(removeHTMLTagsSimple(innerStr))
			if text == "" {
				continue
			}
			// 标题内若嵌 <a>，解析该 href 作为 URL
			absURL := ""
			if href, _, _ := parseAttrs(attrStr); href != "" {
				absURL = resolveURL(baseURL, href)
			} else {
				if am := reAllAnchor.FindStringSubmatch(innerStr); len(am) >= 3 {
					h, _, _ := parseAttrs(am[1])
					absURL = resolveURL(baseURL, h)
				}
			}
			out.Headings = append(out.Headings, WebElementHeading{
				Level: level,
				Text:  truncateRunes(text, 300),
				URL:   absURL,
			})
			if len(out.Headings) >= 50 {
				break
			}
		}
		if len(out.Headings) >= 50 {
			break
		}
	}

	// ---- 段落 ----
	// 优先在 article / main 中挑长段落，否则从整页挑
	bodyScope := html
	if m := reContainerArticle.FindStringSubmatch(html); len(m) >= 2 {
		bodyScope = m[1]
	} else if m := reContainerMain.FindStringSubmatch(html); len(m) >= 2 {
		bodyScope = m[1]
	}
	// 先去掉 script/style/nav/aside 块
	bodyScope = reScript.ReplaceAllString(bodyScope, "")
	bodyScope = reStyle.ReplaceAllString(bodyScope, "")
	bodyScope = reNav.ReplaceAllString(bodyScope, "")
	bodyScope = reAside.ReplaceAllString(bodyScope, "")
	bodyScope = reHeader.ReplaceAllString(bodyScope, "")
	bodyScope = reFooter.ReplaceAllString(bodyScope, "")

	seenParagraph := make(map[string]struct{}, 16)
	for _, m := range reParagraph.FindAllStringSubmatch(bodyScope, -1) {
		if len(m) < 2 {
			continue
		}
		raw := cleanText(removeHTMLTagsSimple(m[1]))
		if raw == "" {
			continue
		}
		wc := countChineseChars(raw)
		enWords := len(strings.Fields(raw))
		// 候选段落阈值：
		//   - 中文：>= 50 字
		//   - 纯英文：>= 25 词
		//   - 中英混合：中文 + 英文词/2 >= 50
		if wc < 50 && enWords < 25 && wc+enWords/2 < 50 {
			continue
		}
		// 同段落去重（多篇文章可能共享同一段开头）
		key := truncateRunes(raw, 80)
		if _, ok := seenParagraph[key]; ok {
			continue
		}
		seenParagraph[key] = struct{}{}
		out.Paragraphs = append(out.Paragraphs, WebElementParagraph{
			Text:      raw,
			Snippet:   truncateRunes(raw, 240),
			WordCount: wc,
		})
		if len(out.Paragraphs) >= 20 {
			break
		}
	}
	if out.Links == nil {
		out.Links = []WebElementLink{}
	}
	if out.Headings == nil {
		out.Headings = []WebElementHeading{}
	}
	if out.Paragraphs == nil {
		out.Paragraphs = []WebElementParagraph{}
	}

	// v2.0.18 补丁（基于问题分析报告_20260629_143200 §3.2）：列表型页面
	// （机器之心文章库 / 36kr / 虎嗅 类）SSR HTML 通常是 <li>...</li> 卡片，
	// 内嵌 <h2><a> 标题链接 + <p> 摘要。live DOM 因 React 未水合 / 反爬
	// 干扰可能拿不到这些链接，导致 elements.links 只有导航、elements.
	// paragraphs 把多条文章拼成一个长段落。这里新增 Articles 字段：从
	// <li> / <article> 卡片里同时抓 title + URL + summary 三元组，保证
	// Agent 即使在反爬态下也能从 SSR HTML 中恢复"标题-简报-完整 URL"
	// 三件套对齐结果。
	out.Articles = extractArticleCards(html, baseURL)
	if out.Articles == nil {
		out.Articles = []WebElementArticle{}
	}

	// v2.0.3: 空 URL 检测日志 — 超过 30% 链接 URL 为空时打 warn
	emptyLinkURLCount := 0
	for _, link := range out.Links {
		if link.URL == "" {
			emptyLinkURLCount++
		}
	}
	if len(out.Links) > 0 && emptyLinkURLCount*100/len(out.Links) > 30 {
		mcpLogMCP("[SPIDER] WARN: %d/%d links have empty URL (baseURL=%s)", emptyLinkURLCount, len(out.Links), baseURL)
	}

	return out
}

// extractArticleCards v2.0.18：从 HTML 中抽「<li>/<article> 卡片内嵌 h2/h3
// 标题链接 + p 摘要」三元组。最多 50 张卡。标题链接 href 优先用 <h2>
// /<h3> 内的 <a href>，未命中则回退到卡片内第一个 <a href>。摘要取卡片
// 内第一个非空 <p>，清洗后 <=200 字。位置从 0 开始按文档顺序递增。
func extractArticleCards(html, baseURL string) (out []WebElementArticle) {
	// v2.0.34：兜底 recover，避免抽取残留 panic 把 spider goroutine 拉死
	// （见问题分析报告_20260709_162130 §4.1）。
	defer func() {
		if rec := recover(); rec != nil {
			recordSpiderPanic(rec)
			mcpLogMCP("[SPIDER] PANIC in extractArticleCards: %v", rec)
			out = []WebElementArticle{}
		}
	}()
	out = make([]WebElementArticle, 0, 16)
	seen := make(map[string]struct{}, 16)

	// 先按 <article> 抽（语义最强），再按 <li> 抽（兜底列表）。
	containers := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"article", reContainerArticle},
		{"li", reLiItem},
	}

	for _, c := range containers {
		for _, m := range c.re.FindAllStringSubmatchIndex(html, -1) {
			// v2.0.34：原 guard `len(m) < 2` 只保证 full match 索引对存在，
			// 但随后访问 m[2]/m[3]（第一个捕获组）在未匹配捕获组时会拿到 -1/-1
			// 触发 "index out of range [-1]/[-2]" panic。改为要求捕获组索引对存在，
			// 并统一走 safeSubmatchSlice 处理负数 / 越界。
			if len(m) < 4 {
				continue
			}
			body := safeSubmatchSlice(html, m, 1)
			if body == "" {
				continue
			}

			title := ""
			href := ""
			// 优先 h2 > h3
			for _, hRe := range []*regexp.Regexp{reHeading2, reHeading3} {
				hm := hRe.FindStringSubmatchIndex(body)
				if len(hm) < 6 {
					continue
				}
				hAttr := safeSubmatchSlice(body, hm, 1)
				hInner := safeSubmatchSlice(body, hm, 2)
				if hInner == "" {
					continue
				}
				am := reAllAnchor.FindStringSubmatchIndex(hInner)
				if len(am) >= 3 {
					href, _, _ = parseAttrs(safeSubmatchSlice(hInner, am, 1))
				} else {
					// 标题元素自身可能有 href（少见）
					href, _, _ = parseAttrs(hAttr)
				}
				title = cleanText(removeHTMLTagsSimple(hInner))
				break
			}
			if title == "" || href == "" {
				continue
			}
			absURL := resolveURL(baseURL, href)
			if absURL == "" {
				continue
			}
			key := absURL + "|" + truncateRunes(title, 40)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			// 摘要：第一个非空 <p>，长度截断到 200 字
			summary := ""
			if pm := rePSimple.FindStringSubmatch(body); len(pm) >= 2 {
				s := cleanText(removeHTMLTagsSimple(pm[1]))
				if s != "" {
					summary = truncateRunes(s, 200)
				}
			}

			out = append(out, WebElementArticle{
				Title:    truncateRunes(title, 200),
				URL:      absURL,
				Summary:  summary,
				Position: len(out),
			})
			if len(out) >= 50 {
				return out
			}
		}
	}
	return out
}

// truncateRunes 按 rune 截断，避免中文乱码
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// strconvAtoiSafe 简易 Atoi 包装（避免 import strconv 带来的额外依赖冲突）
func strconvAtoiSafe(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > 999 {
			return n, true
		}
	}
	return n, true
}

// ==================== MCP HTTP 辅助 ====================

// mcpSetNoCacheHeaders 设置无缓存头
func mcpSetNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

// mcpLogMCP 简单的日志记录
func mcpLogMCP(format string, v ...interface{}) {
	if logger.Ready() {
		logger.Printf("[MCP] "+format, v...)
	} else {
		logger.Printf("[MCP] "+format, v...)
	}
}

// ==================== MCP 服务启动 ====================

// StartMCPWebServer 启动 MCP Web 服务
// computeSpiderHealthMetrics 在 spiderSessions / engine.sem / engine.busyFailCount 上
// 聚合并发指标，供 /healthz handler 与单测共用，避免在测试里启动真实 Chrome。
//
// v2.0.27：抽离为纯函数（基于问题分析报告_20260703_061632 §建议 4）。
// v2.0.34：从 StartMCPWebServer 闭包提升为顶层函数，统一聚合真值源。
func computeSpiderHealthMetrics() (sessionTotal, chromeActive, semUsed, semCap, busyFails int, congestion bool) {
	spiderSessionsMu.RLock()
	sessionTotal = len(spiderSessions)
	for _, s := range spiderSessions {
		if s != nil && s.cdpTarget != "" {
			chromeActive++
		}
	}
	spiderSessionsMu.RUnlock()

	semCap = 4
	if config.G != nil && config.G.SpiderMaxConcurrency > 0 {
		semCap = config.G.SpiderMaxConcurrency
	}
	if engine := GetSpiderEngine(); engine != nil {
		semUsed = len(engine.sem)
		engine.busyMu.Lock()
		busyFails = engine.busyFailCount
		engine.busyMu.Unlock()
	}
	// 拥塞判定：sem 占用 ≥80% capacity 或 busy 失败计数 > 0
	if semCap > 0 && semUsed*5 >= semCap*4 {
		congestion = true
	}
	if busyFails > 0 {
		congestion = true
	}
	return
}

func StartMCPWebServer(listenPort int) {
	if listenPort <= 0 {
		listenPort = 29002
	}

	// v2.0.33（基于问题分析报告_20260709_145100 §4.1-§4.3）：根据配置刷新
	// session 数量上限，避免无界增长。
	setMaxSpiderSessions()
	go sessionCleanupLoop()

	mux := http.NewServeMux()

	mux.HandleFunc("/SpiderWebData", MCPSpiderWebDataHandler)
	mux.HandleFunc("/GetSpiderDataSource", MCPGetSpiderDataSourceHandler)
	mux.HandleFunc("/InputSpiderDailyInfo", MCPInputSpiderDailyInfoHandler)
	mux.HandleFunc("/GetSpiderDailyInfo", MCPGetSpiderDailyInfoHandler)

	// v2.0.34：healthz 指标计算提升为顶层 computeSpiderHealthMetrics（原为闭包），
	// 供 /healthz handler 与单测共用同一份聚合逻辑，避免测试里复制实现导致漂移。

	// v2.0.18: healthz 端点 — 返回 chromedp 内部状态，用于外部看门狗检测服务卡死
	// v2.0.27: 补充并发/队列指标（chrome_active_sessions / congestion_alert），
	//   便于外部看门狗识别 Chrome 共享冲突（基于问题分析报告_20260703_061632 §建议 4：
	//   "v2.0.18 端已加 /healthz，但未暴露并发/队列指标，建议加 chrome_active_sessions 字段"）。
	//   关键指标：
	//     - chrome_active_sessions : cdpTarget 非空的 session 数（真占着 Chrome tab）
	//     - session_total          : spiderSessions 总数（含历史未 detach 的）
	//     - sem_used / sem_capacity / sem_available : v2.0.18 原有
	//     - busy_fail_count        : busy 失败计数（v2.0.18 原有）
	//     - congestion_alert       : sem_used ≥ 80% capacity 或 busy_fail_count > 0 时为 true
	//                              Agent/调度器据此错峰或触发 restart_browser
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mcpSetNoCacheHeaders(w)
		engine := GetSpiderEngine()
		status := map[string]interface{}{
			"service": "LSM Spider MCP Service",
			// v2.0.54: 与 config.APP_VERSION 同步（去掉 v 前缀），避免手改漏同步
			"version":   strings.TrimPrefix(config.APP_VERSION, "v"),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		// v2.0.34（基于问题分析报告_20260709_162130 §建议-中期 3）：panic 指标对
		// up/down 两种状态都透传，便于看门狗在 Chrome 未启动时也能看到历史 panic 累计，
		// 据此识别"持续 panic -> 应停用 MCP Spider 改走 RSS/API 直连"。
		status["panic_count"] = spiderPanicCount.Load()
		if ms := spiderLastPanicAtMs.Load(); ms > 0 {
			status["last_panic_at"] = time.UnixMilli(ms).UTC().Format(time.RFC3339)
		}
		// v2.0.47：透传最近一次 panic 现场快照（URL / Attempt / SessionID /
		// chrome 状态等），外部看门狗一次 GET 即可拿到崩溃上下文，无需 log grep。
		// 仅在有快照时输出字段（nil → 不写 key，避免 /healthz JSON 污染）。
		if snap := getLastCrashSnapshot(); snap != nil {
			status["last_crash_snapshot"] = map[string]interface{}{
				"request_id":             snap.RequestID,
				"recorded_at":            time.UnixMilli(snap.RecordedAtMs).UTC().Format(time.RFC3339),
				"url":                    snap.URL,
				"action_type":            snap.ActionType,
				"attempt":                snap.Attempt,
				"session_id":             snap.SessionID,
				"panic_value":            snap.PanicValue,
				"session_count":          snap.SessionCount,
				"sem_used":               snap.SemUsed,
				"sem_capacity":           snap.SemCapacity,
				"busy_fails":             snap.BusyFails,
				"chrome_active_sessions": snap.ChromeActive,
				"chrome_pid":             snap.ChromePID,
				"chrome_ws":              snap.ChromeWS,
				"extra":                  snap.Extra,
			}
		}
		if engine == nil || !engine.isRunning {
			status["status"] = "down"
			status["chrome"] = "not running"
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			status["status"] = "up"
			healthy := engine.checkChromeHealthy(2 * time.Second)
			status["chrome_healthy"] = healthy
			status["chrome_ws"] = engine.wsURL
			// v2.0.34: 并发指标统一通过顶层 computeSpiderHealthMetrics 聚合（含 chrome_active_sessions / congestion_alert）
			sessionTotal, chromeActive, semUsed, semCap, busyFails, congestion := computeSpiderHealthMetrics()
			status["sem_capacity"] = semCap
			status["sem_used"] = semUsed
			status["sem_available"] = semCap - semUsed
			status["busy_fail_count"] = busyFails
			status["session_total"] = sessionTotal
			status["chrome_active_sessions"] = chromeActive
			status["congestion_alert"] = congestion
			if !healthy {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}
		_ = json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"service": "LSM Spider MCP Service",
			// v2.0.54: 与 config.APP_VERSION 同步（去掉 v 前缀），避免手改漏同步
			"version": strings.TrimPrefix(config.APP_VERSION, "v"),
			"endpoints": []string{
				"/SpiderWebData - Crawl webpage (multi-turn interaction supported)",
				"/GetSpiderDataSource - Get spider data sources",
				"/InputSpiderDailyInfo - Save crawled data",
				"/GetSpiderDailyInfo - Query crawled data (single/batch/paged)",
				"/healthz - Health check (v2.0.35)",
			},
		})
	})

	addr := fmt.Sprintf(":%d", listenPort)
	mcpLogMCP("Starting MCP Web server on %s", addr)

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			mcpLogMCP("MCP Web server error: %v", err)
		}
	}()

	mcpLogMCP("MCP Web server started on port %d", listenPort)
}
