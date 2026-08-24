package spider

// ==================== MCP /SpiderWebData 接口 ====================
//
// 用途：执行网页爬取（支持 navigate / click / scroll / scroll_to /
//                       fill_form / extract / screenshot / get_state 共 8 个交互动作）
//
// 数据流：HTTP POST → MCPSpiderWebDataHandler → dispatchCDPAction (spider_cdp_actions.go)
//                                              → engine.crawlWebDataCDP (spider_cdp_engine.go)
//
// Agent 工作流：
//   1) POST /GetSpiderDataSource  拿到目标 data_source_id
//   2) POST /SpiderWebData        传 data_source_id 触发爬取（可选带 session_id / action）
//   3) POST /InputSpiderDailyInfo 显式保存数据

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	models "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ==================== v2.0.30：MCP handler 并发限流 ====================
//
// 基于问题分析报告_20260707_062144 §3.2 + 建议 2：同时段并发 3-5 个 Agent 触发
// /SpiderWebData 时，handler goroutine 堆积 + engine.sem 全占 + chromedp rootCtx
// cascade cancel，导致 HTTP 层整体饥饿，/healthz 也返回 HTTP:000。
//
// 修复：引入独立的 HTTP-level semaphore `mcpHandlerSem`，控制并发 handler
// goroutine 数（与 CDP tab 槽位 `engine.sem` 完全解耦）。超出 cap 时，
// handler 在 2s 内获取不到 slot，立即返回 503 + JSON envelope：
//
//	{success:false, error_type:"server_busy",
//	 hint:"too many concurrent crawler requests; retry after 2s"}
//
// 调用方行为：Agent 收到 503 server_busy 后等待 2-5s 重试，而非无限挂着
// 等 HTTP:000，避免整个 mux 调度饿死。
//
// `InitMCPHandlerSem(cap int)` 在 main.go 启动钩子调用；运行期不重建 channel
// （避免 send on closed channel 风险）。

var (
	mcpHandlerSem     chan struct{}
	mcpHandlerSemCap  int
	mcpHandlerSemOnce sync.Once
)

// initMCPHandlerSem 初始化 MCP handler 并发限流 channel（一次性）。
// cap=0 时使用默认 8；负数或 nil channel 视为未初始化。
func InitMCPHandlerSem(cap int) {
	mcpHandlerSemOnce.Do(func() {
		if cap <= 0 {
			cap = 8
		}
		mcpHandlerSem = make(chan struct{}, cap)
		mcpHandlerSemCap = cap
	})
}

// acquireMCPHandlerSem 尝试在 timeout 内获取 handler slot。
// 返回 true = 已获得（调用方负责 defer release）；false = 超时（应返回 503）。
func acquireMCPHandlerSem(timeout time.Duration) bool {
	if mcpHandlerSem == nil {
		// 未初始化时不阻塞（兼容早期 / 测试场景）
		return true
	}
	select {
	case mcpHandlerSem <- struct{}{}:
		return true
	case <-time.After(timeout):
		return false
	}
}

// releaseMCPHandlerSem 释放 handler slot（defer 调用）。
func releaseMCPHandlerSem() {
	if mcpHandlerSem == nil {
		return
	}
	select {
	case <-mcpHandlerSem:
	default:
	}
}

// getSpiderHandlerTimeout 获取 handler 硬超时（从配置读取，默认 180s，上限 300s）
// v2.0.3: 新增软上限概念 — 超过 info 级上限后仍可继续 60s，避免 Cloudflare 验证页被截断
func getSpiderHandlerTimeout() time.Duration {
	if config.G != nil && config.G.SpiderHandlerTimeoutSec > 0 {
		t := config.G.SpiderHandlerTimeoutSec
		if t > 300 {
			t = 300
		}
		return time.Duration(t) * time.Second
	}
	return 180 * time.Second
}

// writeSpiderResponse 把 MCPAPIResponse 写入 HTTP 响应并标记已写入。
// 调用此函数的所有路径相当于"已承诺响应客户端"，panic recovery 会跳过兜底写入。
//
// v2.0.20：基于问题分析报告_20260630_150225 §5.3 #1：客户端偶发 HTTP/0.9 when
// not allowed + curl exit 1（response body 为空）多源于 handler 中途 RST。
// 引入此 helper：
//   - 在写入前先通过 atomic.Store 标记"已写入响应"
//   - handler 顶层的 panic recovery 仅在 !responseWrittenFlag.Load() 时兜底
//   - 这样即便 panic 发生在某次 encode 之后，panic recovery 不会再写入二次 header
//     导致「http: superfluous response.WriteHeader call」警告
func writeSpiderResponse(w http.ResponseWriter, flag *atomic.Bool, resp MCPAPIResponse) {
	flag.Store(true)
	_ = json.NewEncoder(w).Encode(resp)
}

// trackingResponseWriter 包装 http.ResponseWriter 追踪"是否已 Write/WriteHeader"。
// 在 handler 内替换原始 w，这样即便 encode 之后 panic 触达 recover，只要
// 曾经至少 write 过一次 body（哪怕被客户端 RST 中途打断），recover 都
// 会跳过兜底写入，避免"superfluous response.WriteHeader call" 让 curl
// 客户端拿到畸形 HTTP 响应。
//
// v2.0.27（基于问题分析报告_20260703_062125 §4.3 "handler PANIC 后
// 客户端连 curl 52 也拿不到"）：不管 encode 路径是 helper 还是直接
// json.NewEncoder(w).Encode(...)，只要追踪器看到过一次 Write/WriteHeader，
// 就视为"已写响应"。
//
// panic recovery 改用 trackingResponseWriter.written 而非 atomic 布尔，
// 不再需要在每个 encode 之后手动 Store(true)，减少遗漏风险。
type trackingResponseWriter struct {
	http.ResponseWriter
	written atomic.Bool
}

func (t *trackingResponseWriter) Write(b []byte) (int, error) {
	t.written.Store(true)
	return t.ResponseWriter.Write(b)
}

func (t *trackingResponseWriter) WriteHeader(code int) {
	t.written.Store(true)
	t.ResponseWriter.WriteHeader(code)
}

// httputil.Flusher 接口：把底层可能的 Flush 暴露出去，json.Encoder.Encode
// 在某些响应体下会触发 Flush（比如 ResponseWriter 底用了 bufio.Writer）
func (t *trackingResponseWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// getSpiderHandlerSoftTimeout 获取 handler 软超时（默认 = 硬超时，但允许额外 60s 缓冲）
// 用于：Cloudflare 验证页等需要较长时间加载的场景
func getSpiderHandlerSoftTimeout() time.Duration {
	hard := getSpiderHandlerTimeout()
	// 软上限 = 硬上限 + 60s，但不超过 360s
	soft := hard + 60*time.Second
	if soft > 360*time.Second {
		soft = 360 * time.Second
	}
	return soft
}

// getSpiderRSSFetchTimeout 获取 RSS fallback 单候选 fetch 超时。
// v2.0.43：默认 15s（原 8s/6s 对国际慢站/高延迟连接不够），上限 60s。
func getSpiderRSSFetchTimeout() time.Duration {
	if config.G != nil && config.G.SpiderRSSFetchTimeoutSec > 0 {
		t := config.G.SpiderRSSFetchTimeoutSec
		if t > 60 {
			t = 60
		}
		if t < 5 {
			t = 5
		}
		return time.Duration(t) * time.Second
	}
	return 15 * time.Second
}

// classifySpiderError 对爬虫错误进行分类，用于 Agent 端快速决策
func classifySpiderError(err error) string {
	if err == nil {
		return ""
	}
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "451") || strings.Contains(errStr, "unavailable for legal reasons"):
		return "region_block"
	case strings.Contains(errStr, "403") || strings.Contains(errStr, "forbidden"):
		return "region_block"
	case strings.Contains(errStr, "429") || strings.Contains(errStr, "too many requests"):
		return "rate_limit"
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline"):
		return "timeout"
	case strings.Contains(errStr, "captcha") || strings.Contains(errStr, "challenge"):
		return "captcha"
	// v2.0.9: CDP 交互失败 — 受控/contenteditable 元素聚焦或上下文绑定问题，
	// 区别于 unknown，便于 Agent 端改用 JS 兜底或坐标点击等策略重试。
	case strings.Contains(errStr, "-32000"),
		strings.Contains(errStr, "invalid context"),
		strings.Contains(errStr, "not focusable"),
		strings.Contains(errStr, "clear failed"):
		return "interaction_failed"
	// v2.0.11: SPA 框架兼容性 — click 派发但业务侧未收到副作用（false positive）。
	// 常见场景：React 18+ 自定义 onClick 提交、Vue/Svelte 受控组件事件委托校验失败。
	// Agent 据此改用 eval 单 roundtrip 完成 set + InputEvent + submit.click 兜底。
	case strings.Contains(errStr, "spa_no_effect"),
		strings.Contains(errStr, "click effect not detected"):
		return "spa_no_effect"
	// v2.0.3: chromedp 折叠错误 — 原始 HTTP 码被丢弃，按常见反爬场景归类为 region_block
	case strings.Contains(errStr, "err_http_response_code_failure"),
		strings.Contains(errStr, "err_empty_response"),
		strings.Contains(errStr, "err_invalid_http_response"),
		strings.Contains(errStr, "err_aborted"):
		return "region_block"
	// v2.0.22: 浏览器沙箱 DNS 不可达（基于问题分析报告_20260701_060527 §3.1-§3.2
	// modelscope.cn 案例：宿主机 nslookup 正常，CDP Chromium 内报
	// ERR_NAME_NOT_RESOLVED）。这是浏览器进程 / seccomp 沙箱导致的标准
	// 反爬之外的环境层错误，与 anti_bot/captcha 不同。Agent 看到
	// errType=dns_unresolved 后应：
	//   1) 不要 retry 浏览器（继续 retry 几乎必然失败 + 触发 panic）
	//   2) 直接调用 fallback_strategy=auto 触发 RSS（用 Go 标准 net/http
	//      不依赖浏览器沙箱 DNS），或切换到 API 直连 / 镜像站
	// errType=dns_unresolved 也会被 shouldTryRSSFallback 命中，
	// 自动启用 RSS 兜底。
	case strings.Contains(errStr, "err_name_not_resolved"),
		strings.Contains(errStr, "name_not_resolved"),
		strings.Contains(errStr, "net::err_name"),
		strings.Contains(errStr, "cdp_dns_unresolved"),
		strings.Contains(errStr, "dns_unresolved"):
		return "dns_unresolved"
	// v2.0.21: session 失效 / 当前页缺失 — 通常因为 captcha / anti-bot
	// 把 session 标记为不可交互（参考问题分析报告_20260630_220512 §1.4
	// "click: no current page in session" 失败链）。
	//
	// v2.0.22 扩展（基于问题分析报告_20260701_060527 §1.2 第一次尝试
	// 拿到的 message = "Crawl failed: spider goroutine panic:
	// runtime error: index out of range [-1]"）：把 "spider goroutine
	// panic" / "index out of range" 都归到 session_invalid，让 Agent
	// 拿到 session_id 后可调 restart_browser 走 restart 路径自愈。
	// session_invalid 已在 shouldTryRSSFallback 白名单中，RSS 兜底
	// 会自动启用。
	case strings.Contains(errStr, "no current page in session"),
		strings.Contains(errStr, "action panic"),
		strings.Contains(errStr, "spider goroutine panic"),
		strings.Contains(errStr, "index out of range"):
		return "session_invalid"
	// v2.0.26（基于问题分析报告_20260703_061200 / _061632 / _062125）：
	// 06:18 起多个 Agent 报告连 https://example.com/ 都触发
	// "runtime error: invalid memory address or nil pointer dereference"。
	// 错误字符串含 "internal server error" + "nil pointer" / "nil map"
	// 时归到 internal_panic；Agent 据此判断 chromedp target / session
	// 池已被污染，**必须**走 restart_browser / 重启 LsmTokensServer 自愈，
	// 不能再 retry 同一 URL（会进入 panic 自循环）。
	case strings.Contains(errStr, "invalid memory address") || strings.Contains(errStr, "nil pointer"):
		return "internal_panic"
	default:
		return "unknown"
	}
}

// detectAntiBotSignals 对爬取结果进行反爬特征签名检测
// 返回检测到的错误类型（空字符串表示未命中）和信号列表
func detectAntiBotSignals(result *SpiderWebDataResponse) (string, []string) {
	if result == nil {
		return "", nil
	}

	var signals []string
	htmlLower := strings.ToLower(result.RawHTML)
	titleLower := strings.ToLower(result.Title)
	contentLower := strings.ToLower(result.Content)

	// 1. 验证码脚本检测
	if strings.Contains(htmlLower, "aliyuncaptcha") ||
		strings.Contains(htmlLower, "recaptcha") ||
		strings.Contains(htmlLower, "hcaptcha") ||
		strings.Contains(htmlLower, "geetest") ||
		strings.Contains(htmlLower, "turnstile") {
		signals = append(signals, "captcha_script_detected")
	}

	// 2. 反爬推广模板检测（常见中文反爬推广页）
	antiBotKeywords := []string{
		"机器之心·数据服务", "还在费劲爬数据", "数据服务已上线",
		"zhaoyunfeng@jiqizhixin.com",
		"访问被拒绝", "您的访问请求被拒绝",
		"ua black list", "user agent black list",
		"请使用浏览器访问", "请开启javascript",
		"安全验证", "人机验证", "智能验证",
	}
	for _, kw := range antiBotKeywords {
		if strings.Contains(titleLower, kw) || strings.Contains(contentLower, kw) || strings.Contains(htmlLower, kw) {
			signals = append(signals, "anti_bot_template:"+kw)
		}
	}

	// 3. 内容异常检测 — 内容极短 + 无有效段落（通过 Elements.Paragraphs 判断）
	paragraphCount := 0
	if result.Elements != nil {
		paragraphCount = len(result.Elements.Paragraphs)
	}
	if len(result.Content) < 100 && paragraphCount == 0 {
		// 进一步确认：标题含推广性质或 HTML 含 captcha
		if len(signals) > 0 || strings.Contains(titleLower, "服务") || strings.Contains(titleLower, "验证") {
			signals = append(signals, "suspicious_short_content")
		}
	}

	// 4. elements sanity 升级：headings 和 links 的 URL 全空（反爬空白页特征）
	if result.Elements != nil && len(result.Elements.Headings) > 0 && len(result.Elements.Links) > 0 {
		allHeadingsEmptyURL := true
		for _, h := range result.Elements.Headings {
			if h.URL != "" {
				allHeadingsEmptyURL = false
				break
			}
		}
		allLinksEmptyURL := true
		for _, l := range result.Elements.Links {
			if l.URL != "" {
				allLinksEmptyURL = false
				break
			}
		}
		if allHeadingsEmptyURL && allLinksEmptyURL {
			signals = append(signals, "all_element_urls_empty")
		}
	}

	// 5. 导航链接占比过高 + 无正文段落（入口页/反爬页特征）
	if result.Elements != nil && len(result.Elements.Links) > 0 {
		navCount := 0
		for _, l := range result.Elements.Links {
			if l.Scope == "nav" {
				navCount++
			}
		}
		navRatio := navCount * 100 / len(result.Elements.Links)
		if navRatio > 80 && len(result.Elements.Paragraphs) == 0 {
			signals = append(signals, "nav_dominant_no_paragraphs")
		}
	}

	if len(signals) == 0 {
		return "", nil
	}

	// 根据信号优先级确定 error_type
	// 验证码类优先
	for _, s := range signals {
		if strings.Contains(s, "captcha") {
			return "captcha", signals
		}
	}
	// 区域封锁/UA黑名单优先
	for _, s := range signals {
		if strings.Contains(s, "black list") || strings.Contains(s, "访问被拒绝") || strings.Contains(s, "ua black") {
			return "region_block", signals
		}
	}
	// 反爬模板优先
	for _, s := range signals {
		if strings.Contains(s, "anti_bot_template") || strings.Contains(s, "all_element_urls_empty") || strings.Contains(s, "nav_dominant_no_paragraphs") {
			return "anti_bot", signals
		}
	}
	// 内容异常兜底
	for _, s := range signals {
		if strings.Contains(s, "suspicious_short_content") {
			return "anti_bot", signals
		}
	}

	return "anti_bot", signals
}

// SpiderWebDataRequest 爬取请求
type SpiderWebDataRequest struct {
	URL             string             `json:"url"`
	Timeout         float64            `json:"timeout,omitempty"`
	DataSourceID    uint64             `json:"data_source_id,omitempty"`
	MaxContentLen   int                `json:"max_content_len,omitempty"`
	SessionID       string             `json:"session_id,omitempty"`   // 会话 ID（用于连续对话）
	Action          *InteractiveAction `json:"action,omitempty"`       // 交互动作（可选）
	ReturnPageState bool               `json:"return_state,omitempty"` // 是否返回页面状态
	// v2.0.13: navigate 后等待客户端 SPA 水合（hydration）完成，
	// 解决 chat.baidu.com 等"SSR HTML 已就绪但客户端 bundle 未水合"
	// 导致所有后续 fill_form / click / eval 均无业务侧回调的问题。
	// 默认 false（向后兼容）；为 true 时最多等待 WaitForHydrationMs 毫秒
	// 等待 React fiber / Next / San / Vue 任一框架接管信号出现，超时即
	// 降级继续；同时在响应 data.hydration_state 中返回探测结果。
	WaitForHydration   bool `json:"wait_for_hydration,omitempty"`
	WaitForHydrationMs int  `json:"wait_for_hydration_ms,omitempty"` // 0=默认 2000ms，上限 5000ms

	// v2.0.20: 反爬 fallback 策略（基于问题分析报告_20260630_150225 §5.3 建议 2 +
	// §5.1"切换数据源 / 用 RSS / 第三方索引"）。当主浏览器爬取反复触发 anti_bot /
	// captcha / login_wall 时，handler 会按策略自动降级到非浏览器兜底（RSS feed），
	// 让 Agent 不必手动切换数据源。
	//
	// 可选值：
	//   - "" 或 "auto"（默认）：先尝试浏览器爬取；所有浏览器 attempt + mobile fallback
	//     耗尽后，自动尝试 RSS feed（仅在检测到 login_wall / paywall / anti_bot 时）
	//   - "rss_first"：跳过浏览器爬取，直接尝试 RSS（适合"已知该站付费墙、Agent
	//     只想要 RSS 列表"场景）
	//   - "none"：完全禁用 fallback，与旧版本兼容
	//
	// 启用 fallback 时，handler 会在响应顶层附加 rss_fallback_used=true / rss_source_url=
	// rss_items_count=，Agent 据此知道这是 RSS 来源（不是浏览器渲染内容）。
	FallbackStrategy string `json:"fallback_strategy,omitempty"`
}

// SpiderWebDataResponse 爬取响应
type SpiderWebDataResponse struct {
	URL          string     `json:"url"`
	Title        string     `json:"title"`
	Content      string     `json:"content"`
	RawHTML      string     `json:"raw_html"`
	CrawlTime    time.Time  `json:"crawl_time"`
	Language     string     `json:"language,omitempty"`
	DataSourceID uint64     `json:"data_source_id,omitempty"`
	SessionID    string     `json:"session_id,omitempty"` // 会话 ID
	PageState    *PageState `json:"page_state,omitempty"` // 页面状态（可选）
	HasMore      bool       `json:"has_more,omitempty"`   // 是否有更多内容/可交互
	Screenshot   string     `json:"screenshot,omitempty"` // v2.0.0: screenshot action 的 base64 PNG
	// v2.0.1: 结构化元素清单（链接 / 标题 / 段落），Agent 据此自主判断：
	//   - 单篇文章（heading + 长段落）
	//   - 文章列表（多条 link + 多个 heading）
	//   - 导航入口页（多为 nav 链接，缺 heading / 段落），需要继续 click / navigate
	Elements *WebElements `json:"elements,omitempty"`

	// v2.0.7: 调试 / 观察型字段 — 各种 action 把产出挂到这几个字段返回给 Agent
	// 不入库：本次 /SpiderWebData 调用只返回，不写入数据库
	ConsoleLogs []ConsoleLogEntry `json:"console_logs,omitempty"` // console_logs action
	NetworkLog  []NetworkLogEntry `json:"network_log,omitempty"`  // network_log action
	Dom         *DomNodeDetail    `json:"dom,omitempty"`          // dom action（节点详情）
	Storage     *StorageSnapshot  `json:"storage,omitempty"`      // local_storage / session_storage action
	Cookies     []CookieEntry     `json:"cookies,omitempty"`      // cookies action
	EvalResult  *EvalResult       `json:"eval_result,omitempty"`  // eval action
	Tabs        []TabInfo         `json:"tabs,omitempty"`         // list_tabs / switch_tab action
	ExtractList []ExtractedItem   `json:"extract_list,omitempty"` // elements action（增强抽取）

	// v2.0.9: 资源屏蔽统计（仅在 SpiderBlockResourcesEnabled 时填充）
	BlockStats *SessionBlockStats `json:"block_stats,omitempty"`

	// v2.0.10: fill_form 动作的逐字段效果校验结果，让 Agent 区分「接口调用成功」
	// 与「目标元素真的被赋值」。受控 SPA（如 chat.baidu.com 文心一言）即使 JS 写入
	// 成功，也可能因为 framework 重渲染把 value 复位，Agent 据此判断是否需要
	// 改用 eval 单 roundtrip 完成 set + 派发事件 + 立即 click submit。
	FillFormResult *FillFormResult `json:"fill_form_result,omitempty"`

	// v2.0.11: click / click_at 动作效果校验 — 区分「CDP 坐标点击已派发」
	// 与「业务侧真的收到 onClick 并触发了副作用」。spa_no_effect 用于 false
	// positive 诊断（chat.baidu.com 文心一言 case：5 种 click 路径全部成功
	// 返回但 React onClick 提交回调未被触发）。
	ClickEffectVerification *ClickEffectVerification `json:"click_effect_verification,omitempty"`

	// v2.0.13: 页面水合（hydration）状态探测 — 区分「SSR HTML 已就绪」
	// 与「客户端 SPA 已接管」。chat.baidu.com 文心一言 case 显示：13 个
	// modulepreload 全部 200 但 transferSize=0，console_logs 为空，DOM 内
	// 零 React fiber —— 仅 SSR HTML 就绪，但客户端 bundle 未执行水合，
	// 后续所有 fill_form / click / eval 都不会触发业务侧回调。Agent
	// 据此判断是否需要等待更久、或换用 API 直连方案。
	HydrationState *HydrationDiagnostics `json:"hydration_state,omitempty"`

	// v2.0.13-补丁：顶层人类可读 warnings — Agent 不必深读 hydration_state 嵌套
	// 字段也能识别风险。如 hydration 探测命中「client bundle not hydrated」三连击
	// （state=timeout + detected_framework=static + console_lines=0），追加一条
	// warning 提示 Agent 不要再试 fill_form / click / eval，评估改走 API 直连。
	Warnings []string `json:"warnings,omitempty"`

	// v2.0.20: 反爬兜底（RSS / Atom feed）来源元数据。仅当 handler 启用
	// fallback_strategy=auto/rss_first 且成功从 RSS feed 拿到数据时填充。
	// Agent 据此判断返回内容的来源是 RSS（结构化字段，更可靠），而不是浏览器渲染
	// 出来的 HTML（反爬场景下可能为空 / captcha 推广页）。
	//   - RSSFallbackUsed: true 时主体内容来自 RSS feed 而非浏览器
	//   - RSSSourceURL: 实际抓取的 feed URL
	//   - RSSItemCount: 解析出的条目数
	//   - RSSTriedURLs: 顺序试过的 feed URL 列表（含失败原因），便于诊断
	RSSFallbackUsed bool     `json:"rss_fallback_used,omitempty"`
	RSSSourceURL    string   `json:"rss_source_url,omitempty"`
	RSSItemCount    int      `json:"rss_item_count,omitempty"`
	RSSTriedURLs    []string `json:"rss_tried_urls,omitempty"`

	// v2.0.30: forced_restart 标记 — restart_browser 在 cascade context canceled
	// 状态下走 `restartChromeForced()` 旁路路径时为 true。Agent 据此判断：
	//   - 旧 session 的 cdpCtx 已死，必须用响应里返回的 session_id 续接
	//   - 下一次 action 应优先 fresh navigate，不要依赖 session tab 复用
	// 仅 cascade 强制重启时填充，正常 restart 不出现该字段（omitempty）。
	ForcedRestart bool `json:"forced_restart,omitempty"`

	// v2.0.30: SPA 路由替代路径 hints（基于 Agent5_InfoQ_报告_20260705）：
	// Nuxt / Next / Vue Router / React Router 等 SPA 导航 tab 无 href，
	// a.click() 不会触发 Vue Router pushState；Agent 应改用 absolute URL
	// navigate 直接访问目标路径。infoq.cn 已验证可直链的 topic URL：
	// /topic/AI /topic/architecture /topic/BigData /topic/cloud-computing
	// /aibriefs 等。仅在 hint 非空时附加（未知 host 不写入）。
	SPAAlternativeHints []string `json:"spa_alternative_hints,omitempty"`
}

// HydrationDiagnostics 页面水合状态探测（v2.0.13）
//   - State: 探测结果分类 — hydrated（找到框架接管信号）/ none（DOM 静态，
//     无 SPA 框架，如 SSR + 不水合的页面）/ timeout（探测窗口耗尽仍未
//     出现接管信号，但 DOM 仍在变，框架可能在异步初始化）
//   - WaitMs: 实际探测耗时
//   - FiberRootsCount: 找到的 React fiber root 数量（0 表示无 React 接管）
//   - HasNext / HasSan / HasVue: Next.js / San / Vue 接管信号
//   - ConsoleLines: window.__lsm_console_log__ 长度（v2.0.13-补丁：透传给 Agent，
//     0 表示客户端 JS 完全没执行，是 "client bundle never executed" 的关键证据）
//   - DetectedFramework: 第一个被命中的框架名（react/next/san/vue/static）
//   - Warning: 失败/降级提示
type HydrationDiagnostics struct {
	State           string `json:"state"`                       // hydrated / none / timeout
	WaitMs          int    `json:"wait_ms"`                     // 实际探测耗时（毫秒）
	FiberRootsCount int    `json:"fiber_roots_count,omitempty"` // React fiber root 计数
	HasNext         bool   `json:"has_next,omitempty"`          // window.next / __NEXT_DATA__ 是否存在
	HasSan          bool   `json:"has_san,omitempty"`           // window.san / San create() 是否可检测
	HasVue          bool   `json:"has_vue,omitempty"`           // __vue__ 是否出现在任意 DOM 节点上
	ConsoleLines    int    `json:"console_lines"`               // window.__lsm_console_log__ 长度；0=JS 没执行（不带 omitempty，0 是关键证据）
	// v2.0.17 补丁：ES Module / script 资源加载统计，用于诊断 chat.baidu.com 等
	// SSR HTML 已就绪但客户端 bundle 未水合的根因（见问题分析报告
	// _20260627_120444 §3.1 / §5.2）。统计来源 performance.getEntriesByType('resource')
	// 中 initiatorType=script|link 的条目。
	ModuleLoadsTotal   int      `json:"module_loads_total,omitempty"`   // 探测到的 script+modulepreload 资源数
	ModuleLoadsFailed  int      `json:"module_loads_failed,omitempty"`  // duration=0 视为未启动（被 CSP/反爬拦截）
	ModuleZeroTransfer int      `json:"module_zero_transfer,omitempty"` // duration>0 + transferSize=0 可疑条目
	ModuleFailedURLs   []string `json:"module_failed_urls,omitempty"`   // 前 3 条失败 URL（带 zero_transfer:/not_started: 前缀）
	DetectedFramework  string   `json:"detected_framework,omitempty"`   // react/next/san/vue/static（按命中顺序）
	Warning            string   `json:"warning,omitempty"`              // 失败/降级提示（如 chat.baidu.com 客户端 bundle 未水合）
}

// FillFormFieldStatus 描述单字段填写效果（v2.0.10）
type FillFormFieldStatus struct {
	Selector   string `json:"selector"`             // 原始 selector
	Strategy   string `json:"strategy,omitempty"`   // "native_chromedp" / "controlled_js" / "fallback"
	Expected   string `json:"expected,omitempty"`   // 请求填写的值
	Actual     string `json:"actual,omitempty"`     // 实际读取到的值（截断 200 字符）
	ActualLen  int    `json:"actual_len,omitempty"` // 实际值长度（避免长文本撑爆响应）
	VerifiedOK bool   `json:"verified_ok"`          // 实际值是否完全等于期望值
	Error      string `json:"error,omitempty"`      // 该字段写入异常时的错误描述
	// v2.0.11: SPA framework 状态机一致性 — DOM value 与 React _valueTracker / Vue
	// state 对比结果。受控组件每次 render 复位 value 时，DOM value 看似 OK，但框架
	// 内部 state 仍为旧值，onClick 提交会触发旧值。该字段让 Agent 区分"DOM 写入成功"
	// 与"框架状态机已消费 input 事件"。
	Diagnostics *ControlledInputDiagnostics `json:"diagnostics,omitempty"`
}

// ControlledInputDiagnostics 受控输入 JS 写入的框架状态机一致性诊断（v2.0.11）
//   - DOMValue: 当前 DOM value（与 Actual 同源，但截断策略独立）
//   - ReactTrackerValue: React _valueTracker.getValue()（若存在）；与 DOMValue 不一致
//     表示 React state 未消费 input 事件。
//   - HasValueTracker / HasVue / HasSan: 框架探测信号，便于 Agent 改用对应框架特定
//     的兜底（如 San a:model 双向绑定事件）。
//   - FrameworkConsumed: 综合判断 — React tracker 一致 / Vue 已 emit / DOM 已更新
//     任一为真即视为消费成功。
type ControlledInputDiagnostics struct {
	DOMValue          string `json:"dom_value"`
	ReactTrackerValue string `json:"react_tracker_value,omitempty"`
	HasValueTracker   bool   `json:"has_value_tracker"`
	HasVue            bool   `json:"has_vue"`
	HasSan            bool   `json:"has_san"`
	FrameworkConsumed bool   `json:"framework_consumed"`
}

// FillFormResult fill_form 整体效果汇总（v2.0.10）
type FillFormResult struct {
	AllVerifiedOK bool                  `json:"all_verified_ok"`          // 所有字段 verified_ok=true
	Fields        []FillFormFieldStatus `json:"fields"`                   // 逐字段报告
	SubmitClicked bool                  `json:"submit_clicked,omitempty"` // 是否触发了 submit 点击
	Warnings      []string              `json:"warnings,omitempty"`       // 人类可读的告警（如页面可能覆写值）
}

// ClickEffectVerification click / click_at 动作效果校验（v2.0.11）
//   - HasElementChange: 目标元素 className / disabled / value 等属性在点击前后发生变化
//   - HasNetworkChange: 期间 __lsm_network_log__ 新增请求（≥1 即视为业务侧收到副作用）
//   - EffectVerified: HasElementChange || HasNetworkChange 任一为 true
//   - PreState / PostState: 校验前后元素 className 快照，便于 Agent 对比
//   - NetworkRequestsDelta: 新增请求数（含 GET，非仅 POST — GET 也可能是触发业务的副作用）
//   - WaitMs: 实际等待时长
//   - Warning: 失败原因（spa_no_effect / 无法定位元素 等），Agent 据此改用 eval 单 roundtrip
type ClickEffectVerification struct {
	HasElementChange     bool   `json:"has_element_change"`
	HasNetworkChange     bool   `json:"has_network_change"`
	EffectVerified       bool   `json:"effect_verified"`
	PreState             string `json:"pre_state,omitempty"`
	PostState            string `json:"post_state,omitempty"`
	NetworkRequestsDelta int    `json:"network_requests_delta"`
	WaitMs               int    `json:"wait_ms"`
	Warning              string `json:"warning,omitempty"`
}

// ConsoleLogEntry Console 日志条目（v2.0.7）
type ConsoleLogEntry struct {
	Level  string    `json:"level"`         // log / warn / error / info / debug
	Text   string    `json:"text"`          // 日志文本
	URL    string    `json:"url,omitempty"` // 来源 URL
	Line   int       `json:"line,omitempty"`
	Column int       `json:"column,omitempty"`
	Time   time.Time `json:"time,omitempty"`
	Source string    `json:"source,omitempty"` // javascript / network / storage / ...
}

// NetworkLogEntry Network 请求 / 响应摘要（v2.0.7）
type NetworkLogEntry struct {
	RequestID       string            `json:"request_id"`
	URL             string            `json:"url"`
	Method          string            `json:"method,omitempty"`
	Status          int               `json:"status,omitempty"`
	StatusText      string            `json:"status_text,omitempty"`
	Type            string            `json:"type,omitempty"` // Document / XHR / Fetch / Image / ...
	FromCache       bool              `json:"from_cache,omitempty"`
	Initiator       string            `json:"initiator,omitempty"` // script / parser / other
	StartTime       time.Time         `json:"start_time,omitempty"`
	EndTime         time.Time         `json:"end_time,omitempty"`
	Duration        int64             `json:"duration_ms,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	PostData        string            `json:"post_data,omitempty"`
	MimeType        string            `json:"mime_type,omitempty"`
	Failed          bool              `json:"failed,omitempty"`
	FailureText     string            `json:"failure_text,omitempty"`
}

// DomNodeDetail DOM 节点详情（v2.0.7）
type DomNodeDetail struct {
	Found         bool              `json:"found"`
	Tag           string            `json:"tag,omitempty"`
	ID            string            `json:"id,omitempty"`
	ClassName     string            `json:"class_name,omitempty"`
	OuterHTML     string            `json:"outer_html,omitempty"`
	InnerText     string            `json:"inner_text,omitempty"`
	InnerHTML     string            `json:"inner_html,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Box           *DomBox           `json:"box,omitempty"`
	Visible       bool              `json:"visible,omitempty"`
	Enabled       bool              `json:"enabled,omitempty"`
	Parent        string            `json:"parent,omitempty"`
	Children      []string          `json:"children,omitempty"`
	ComputedStyle map[string]string `json:"computed_style,omitempty"`
}

// DomBox 节点几何信息
type DomBox struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
}

// StorageSnapshot 存储快照（v2.0.7）
type StorageSnapshot struct {
	Kind   string            `json:"kind"` // "local" / "session"
	Keys   []string          `json:"keys,omitempty"`
	Values map[string]string `json:"values,omitempty"`
	Op     string            `json:"op,omitempty"`    // get / set / remove / clear / keys
	Key    string            `json:"key,omitempty"`   // 涉及单 key 时返回
	Value  string            `json:"value,omitempty"` // 涉及单 key 时返回
}

// CookieEntry Cookie 条目（v2.0.7）
type CookieEntry struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	Size     int     `json:"size,omitempty"`
	HTTPOnly bool    `json:"http_only,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"same_site,omitempty"`
}

// EvalResult JS 求值结果（v2.0.7）
type EvalResult struct {
	Expression string      `json:"expression"`
	Result     interface{} `json:"result"`
	Type       string      `json:"type,omitempty"`
	HasError   bool        `json:"has_error,omitempty"`
	ErrorMsg   string      `json:"error,omitempty"`
}

// TabInfo Tab 信息（v2.0.7）
type TabInfo struct {
	Index    int    `json:"index"`
	TargetID string `json:"target_id"`
	Type     string `json:"type"` // "page" / "iframe" / "service_worker" / ...
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	Active   bool   `json:"active,omitempty"`
}

// ExtractedItem 增强元素抽取条目（v2.0.7 elements action）
type ExtractedItem struct {
	Selector   string            `json:"selector"`
	OuterHTML  string            `json:"outer_html,omitempty"`
	InnerText  string            `json:"inner_text,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Box        *DomBox           `json:"box,omitempty"`
}

// MCPSpiderWebDataHandler /SpiderWebData 接口处理
func MCPSpiderWebDataHandler(w http.ResponseWriter, r *http.Request) {
	// v2.0.47：handler 入口生成 RequestID（贯穿整个请求生命周期的关联 tag）。
	// 与 recordSpiderPanic 配合，让"request START"和"PANIC"两条日志能 grep 到一起。
	reqID, startTime := mcpLogMCPRequestStart("/SpiderWebData", r.RemoteAddr)
	defer func() {
		mcpLogMCPRequestEnd(reqID, startTime, 200, true, "")
	}()
	// v2.0.47：handler 内 current* closure 变量必须在 defer 之前声明，
	// panic recover 路径（在 defer 闭包内）才能读到。初始值用 r 兜底：
	// 即使 body 还没解析，也能拿到最大上下文（参考值，未必真实）。
	currentActionType := ""
	currentAttempt := 0
	currentURLHint := r.URL.String()
	currentSessionIDHint := r.Header.Get("X-Session-ID")

	// v2.0.30：handler 入口并发限流（基于问题分析报告_20260707_062144 §3.2 + 建议 2）。
	// 同时段并发 3-5 个 Agent 触发 /SpiderWebData 时，handler goroutine 堆积
	// + engine.sem 全占 + chromedp rootCtx cascade cancel，导致 HTTP 层整体饥饿、
	// /healthz 也 HTTP:000。在 handler 入口加独立 HTTP-level semaphore，
	// 2s 内获取不到 slot 立即返回 503 server_busy，避免无限挂着。
	if !acquireMCPHandlerSem(2 * time.Second) {
		mcpLogMCP("[SPIDER] handler concurrency cap (%d) exceeded, returning 503 server_busy", mcpHandlerSemCap)
		w.Header().Set("Content-Type", "application/json")
		mcpSetNoCacheHeaders(w)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: fmt.Sprintf("MCP handler concurrency cap (%d) exceeded; retry after 2s", mcpHandlerSemCap),
			Data: map[string]interface{}{
				"error_type": "server_busy",
				"hint":       "too many concurrent crawler requests; retry after 2s (issue 20260707_062144 §3.2)",
			},
		})
		return
	}
	defer releaseMCPHandlerSem()

	// P0: panic recovery — 防止 chromedp 内部 panic 导致 TCP 空响应
	// v2.0.20: handler 计数 — 用 sync/atomic 标记是否已写入响应；
	// 防止 handler 中途任何早退路径（如 restart_browser 偶发 RST / 服务重启
	// 期间 RST 响应）造成客户端「HTTP/0.9 when not allowed + curl exit 1」
	// 空响应场景（基于问题分析报告_20260630_150225 §5.3 #1）。
	// 同时在 goroutine watchdog 跑满 + 主路径因超时尚未写响应时也能兜底。
	var responseWrittenFlag atomic.Bool
	// v2.0.27: 用 trackingResponseWriter 包装 w，自动追踪 Write / WriteHeader。
	// 后续 handler 内 json.NewEncoder(w).Encode(...) 一旦写了响应体，tracker
	// 就标记 written=true；顶层的 panic recovery 看 tracker 就不再重复写，
	// 避免「http: superfluous response.WriteHeader call」让客户端拿到畸形响应。
	tw := &trackingResponseWriter{ResponseWriter: w}
	w = tw
	defer func() {
		if rec := recover(); rec != nil {
			// v2.0.27（基于问题分析报告_20260703_062125 §1.5 / §2.2 / §4.1）：
			// "/SpiderWebData" 接口陷入 PANIC 自循环 —— WATCHDOG 强制清理 → RSS
			// fallback 失败 → failure path nil pointer → handler panic → 残留状态污染
			// → 下一轮新请求复用已坏 session → 再 watchdog → 再 RSS fallback → 再 PANIC，
			// 即便 https://example.com/ 也复现，证实是 handler 内部状态问题。
			//
			// 根因：
			//   1) 首次 navigate wait_for_hydration=true 触发 cascade context
			//      canceled，goroutine 在 attachCDPContext 重连半途被 watchdog
			//      detachCDPContext 拆掉 session.cdpCtx，两个路径对 session 的
			//      cdpCtx / cdpTarget 字段存在 data race + 幂等锁部分失效
			//   2) handler recover 仅告知 Agent 主动调 restart_browser，没有
			//      服务端自愈兜底；Agent 不一定照做，服务永远停在 PANIC 态
			//   3) recover 返回前没把 responseWrittenFlag 标记为 true，其它
			//      recover 块（比如嵌套 recover）一旦再试图写响应头会触发
			//      "superfluous response.WriteHeader call" 让客户端收到畸形响应
			//
			// 修复（v2.0.27 四层兜底）：
			//   ① 打印 stack trace（debug.Stack），让运维侧定位 nil 源
			//   ② 服务端主动自愈：detachAllSpiderSessions + 异步 RestartChrome
			//      让下一次请求进入 clean state，不再依赖 Agent 操作
			//   ③ 标记 responseWrittenFlag=true，避免重复写响应头
			//   ④ error_type=internal_panic + engine_recovery_hint，Agent
			//      可据此判断是否需要人工介入（kill + restart 进程）
			stack := debug.Stack()
			mcpLogMCP("PANIC in /SpiderWebData handler: %v\n%s", rec, stack)
			// v2.0.34：累计 panic 计数 + 时间戳，/healthz 透传给外部看门狗
			recordSpiderPanic(rec)

			// v2.0.47（基于问题分析报告_20260717_094145.md §三）：
			// 抓取崩溃瞬间的 MCP 服务状态快照（URL / ActionType / Attempt /
			// SessionID / Chrome 状态 / Engine 状态等），存入 atomic.Pointer。
			// /healthz 透传给外部看门狗，运维一次 GET 即可拿到崩溃现场上下文，
			// 不用 log grep。captureCrashSnapshot 内仅做 len/load + nil check，
			// 不调任何可能 panic 的 helper，自身不 panic。
			snap := captureCrashSnapshot(reqID, currentURLHint, currentActionType, currentSessionIDHint, currentAttempt, rec)
			mcpLogMCPWithTag(reqID, "crash snapshot captured: url=%s action=%s attempt=%d session_id=%s session_count=%d sem_used=%d/%d busy_fails=%d",
				snap.URL, snap.ActionType, snap.Attempt, snap.SessionID,
				snap.SessionCount, snap.SemUsed, snap.SemCapacity, snap.BusyFails)

			// v2.0.27 ②：服务端自愈 —— 主动清理所有 session 的 cdpCtx/cdpCancel /
			// cdpTarget，避免下一个请求复用已坏 session 进入 PANIC 自循环。
			// 异步 RestartChrome 让 chromatic engine rootCtx 被替换成新一层，
			// 下一次 runAttempt 走 attachCDPContext 重建 tab 时不会碰到 cascade
			// canceled 状态。自愈失败也不要把 handler 再次拉死（独立 goroutine）。
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						mcpLogMCP("[SPIDER] PANIC during self-heal after handler panic: %v", rec)
					}
				}()
				mcpLogMCP("[SPIDER] self-healing after handler panic: detachAllSpiderSessions + RestartChrome")
				detachAllSpiderSessions()
				if eng := GetSpiderEngine(); eng != nil {
					if err := eng.RestartChrome(); err != nil {
						mcpLogMCP("[SPIDER] self-heal RestartChrome failed: %v (will retry on next restart_browser or request)", err)
					}
				}
			}()

			// v2.0.27 ③：优先用 trackingResponseWriter.written 判断（如果 encode
			// 已至少了一次 Write 就会是 true），其次再回退 atomic 显式标记
			if !tw.written.Load() && !responseWrittenFlag.Load() {
				tw.Header().Set("Content-Type", "application/json")
				mcpSetNoCacheHeaders(tw)
				tw.WriteHeader(http.StatusInternalServerError)
				// v2.0.27 ③：标记已写入，避免嵌套 recover / 二次 path 再次写响应头
				responseWrittenFlag.Store(true)
				tw.written.Store(true)
				_ = json.NewEncoder(tw).Encode(MCPAPIResponse{
					Success: false,
					Message: fmt.Sprintf("Internal server error: %v", rec),
					Data: map[string]interface{}{
						"error_type": "internal_panic",
						"engine_recovery_hint": "MCP /SpiderWebData handler 触发了未捕获 panic；" +
							"chromedp target / session 池可能已污染。\n" +
							"服务端已自动执行 detachAllSpiderSessions + RestartChrome 自愈，" +
							"多数场景下下一次请求应能正常返回（即使不带 action=restart_browser）。\n" +
							"如仍 panic，需 kill LsmTokensServer + Chrome 整个进程后重启。\n" +
							"立即可试：在下一次请求中带 {\"action\":{\"type\":\"restart_browser\"}} 强制重置 Chrome。\n" +
							"请勿连续重试同一 URL（会持续进入「panic → 残留 → 再 panic」循环）。",
					},
				})
			}
		}
	}()

	// P0: 请求级硬超时 — 防止 chromedp 阻塞导致客户端 Empty Reply (curl 52)
	// v2.0.3: 使用软超时，给 Cloudflare 验证页等场景额外 60s 缓冲
	spiderHandlerTimeout := getSpiderHandlerTimeout()
	spiderSoftTimeout := getSpiderHandlerSoftTimeout()
	ctx, cancel := context.WithTimeout(r.Context(), spiderSoftTimeout)
	defer cancel()
	// 替换 r.Context 为带超时的 ctx，后续 chromedp 操作可以感知
	// 注意：当前 handler 未直接传 ctx 到 chromedp，但超时控制通过 goroutine 兜底

	w.Header().Set("Content-Type", "application/json")
	mcpSetNoCacheHeaders(w)

	mcpLogMCP("Received request for /SpiderWebData from %s", r.RemoteAddr)
	// v2.0.47：把"Received request"日志统一为带 reqID 的 tag 版本，便于 grep 关联。
	mcpLogMCPWithTag(reqID, "request start method=%s content_length=%d content_type=%q",
		r.Method, r.ContentLength, r.Header.Get("Content-Type"))

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "Method not allowed, use POST",
		})
		return
	}

	var req SpiderWebDataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mcpLogMCP("Failed to decode request: %v", err)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}
	// v2.0.47：req 解析成功后立即把 URL / SessionID 同步到 closure 变量，
	// panic recover 抓 snapshot 时能看到 body 里的真实 URL/SessionID（兜底是 r 头）。
	if req.URL != "" {
		currentURLHint = req.URL
	}
	if req.SessionID != "" {
		currentSessionIDHint = req.SessionID
	}

	// 使用带超时的 channel 做整体请求超时兜底
	type result struct {
		crawlResult *SpiderWebDataResponse
		err         error
		signals     []string
		timedOut    bool
	}

	// v2.0.8: 自适应重试循环
	// 每次 attempt 启动新 goroutine；hit anti_bot/captcha 时 reroll session 并 retry
	plan := BuildRetryPlan(config.G.SpiderAntiBotAutoRetry)
	var lastResult result
	var attemptSession *SpiderSession
	engine := GetSpiderEngine()

	// v2.0.20: rss_first 快速路径 — Agent 显式声明"已知该站付费墙、跳过浏览器、
	// 直接走 RSS"。节省 30s+ chromedp retry 时间。
	//
	// 注意：fallback_strategy 的语义在 response 字段声明里已说明：
	//   - "" 或 "auto" → 先浏览器 retry + mobile fallback 失败后再走 RSS
	//   - "rss_first" → 跳过浏览器，直接走 RSS
	//   - "none" → 完全禁用 fallback
	rssFallbackStrategy := strings.ToLower(strings.TrimSpace(req.FallbackStrategy))
	if rssFallbackStrategy == "" {
		rssFallbackStrategy = "auto"
	}
	if rssFallbackStrategy == "rss_first" && req.URL != "" && req.Action == nil {
		// 仅简单 navigate 走快速路径；带 action 的请求仍走浏览器（Agent 需要 DOM 交互）
		var quickResp *SpiderWebDataResponse
		var quickUsed bool
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					mcpLogMCP("[SPIDER] PANIC in rss_first quick path: %v", rec)
				}
			}()
			quickResp, quickUsed = tryRSSFallbackForURL(req.URL, "rss_first", 50)
		}()
		if quickUsed && quickResp != nil {
			mcpLogMCP("[SPIDER] rss_first returned %d items (url=%s)", quickResp.RSSItemCount, req.URL)
			json.NewEncoder(w).Encode(MCPAPIResponse{
				Success: true,
				Message: "Crawl completed (via RSS quick path)",
				Data:    quickResp,
			})
			return
		}
		mcpLogMCP("[SPIDER] rss_first yielded no items for %s; will fall through to browser path", req.URL)
	}

	// runAttempt 跑一次爬取，返回 (res, session) 让外层决定是否 reroll + retry
	runAttempt := func() (result, *SpiderSession) {
		resultCh := make(chan result, 1)
		var sessionRef *SpiderSession

		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					// v2.0.34（基于问题分析报告_20260709_162130 §4.1 / §建议-中期 4）：
					//   ① 打印完整 stack，便于定位 index out of range 的确切代码行
					//      （原实现只 log %v，无法区分 extractArticleCards / extractWebElements）
					//   ② 累计 panic 指标，/healthz 透传 panic_count / last_panic_at
					//   ③ 立即 releaseSpiderSession 释放当前 attempt 的 Chrome target +
					//      从 spiderSessions map 删除，避免 panic 后 session 残留导致
					//      session_total 累加且 sem_used 不归零（报告 §4.2 矛盾现象）
					stack := debug.Stack()
					mcpLogMCP("PANIC in spider goroutine: %v\n%s", rec, stack)
					recordSpiderPanic(rec)
					if sessionRef != nil {
						// 持 cdpMu 与 watchdog 路径互斥后再释放；releaseSpiderSession
						// 内部取 spiderSessionsMu，锁序 cdpMu -> spiderSessionsMu，
						// 全仓无反向取锁路径，不会死锁。
						sessionRef.cdpMu.Lock()
						releaseSpiderSession(sessionRef)
						sessionRef.cdpMu.Unlock()
					}
					resultCh <- result{err: fmt.Errorf("spider goroutine panic: %v", rec)}
				}
			}()

			// v2.0.18: handler 级看门狗 — 每个 goroutine 内加独立超时检测，
			// 超过 60s 主动 detachCDPContext + 释放 sem，防止 chromedp 死锁导致
			// 服务卡死（见问题分析报告_20260629_093329 §5.2 建议 5）。
			// v2.0.26 修复：watchdog 改用 GetSpiderEngine() 获取最新实例
			//（restart 后局部变量 engine 可能指向旧实例，其 sem 已被重置）；
			// session 查找加 RLock 保护（原来直接读 map 存在数据竞态）。
			// v2.0.27 修复（基于问题分析报告_20260703_062125 §1.5 / §4.1）：
			// watchdog / ctx.Done() / dispatchCDPAction 三条路径对 session 的
			// cdpCtx / cdpCancel / cdpTarget 写操作存在数据竞态；如果 watchdog
			// 在 runWithSession / attachCDPContext 中途中断了 session 状态切换，
			// 下一轮 getOrCreateSession 复用 struct 碰到半初始化字段会触发
			// nil pointer panic。修复：
			//   (a) watchdog 用 session.cdpMu 拿锁后再 detach，与 action 路径
			//       的 cdpMu.Lock() 互斥
			//   (b) 用 watchdogDidDetach atomic 标记 + sync.Once 保证 detach
			//       只执行一次，避免与 ctx.Done() 路径（也调 detachCDPContext
			//       ）并发触发 data race
			//   (c) detach 后额外置 session.CurrentRawHTML="" /
			//       session.CurrentURL 保留但标记 session.SessionID 加
			//       "_dead" 后缀提示调用方"此 session 已被清理，下次应新建"
			watchdogSessionID := req.SessionID
			watchdogDone := make(chan struct{})
			defer close(watchdogDone)
			var watchdogDetachOnce sync.Once
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						mcpLogMCP("[SPIDER] PANIC in watchdog goroutine: %v", rec)
						recordSpiderPanic(rec)
					}
				}()
				select {
				case <-watchdogDone:
					return
				case <-time.After(60 * time.Second):
					watchdogDetachOnce.Do(func() {
						mcpLogMCP("[SPIDER] WATCHDOG: action goroutine exceeded 60s, forcing cleanup")
						spiderSessionsMu.RLock()
						s, ok := spiderSessions[watchdogSessionID]
						spiderSessionsMu.RUnlock()
						if ok && s != nil {
							// v2.0.27: 持 cdpMu 后再 detach，避免与 action 路径
							// 同时写 s.cdpCtx / s.cdpTarget 字段
							s.cdpMu.Lock()
							detachCDPContext(s)
							s.cdpMu.Unlock()
						}
						// v2.0.26: 用最新 engine 实例释放 sem，避免 restart 后
						// 旧 engine.sem 引用失效导致 sem 槽位泄漏。
						if eng := GetSpiderEngine(); eng != nil {
							select {
							case <-eng.sem:
								eng.releaseSem()
							default:
							}
						}
					})
				}
			}()

			session := getOrCreateSession(req.SessionID)
			if req.SessionID == "" {
				req.SessionID = session.SessionID
				mcpLogMCP("Created new session: %s", session.SessionID)
			} else {
				mcpLogMCP("Using existing session: %s", req.SessionID)
			}
			sessionRef = session

			var crawlResult *SpiderWebDataResponse
			var err error

			var dsRecord *models.TSpiderDataSource
			if req.DataSourceID > 0 && database.DB != nil {
				ds, dsErr := models.GetSpiderDataSourceByID(req.DataSourceID)
				if dsErr == nil && ds != nil {
					// v2.0.19: 拒绝已禁用的数据源（与 server_web_spider_crawl.go 一致）
					if ds.Status != 1 {
						resultCh <- result{err: fmt.Errorf("data source ID=%d (%s) is disabled (status=%d), re-enable it before crawling", ds.ID, ds.PlatformName, ds.Status)}
						return
					}
					dsRecord = ds
					mcpLogMCP("Using data source: %s (ID=%d)", ds.PlatformName, ds.ID)
				}
			}

			if req.Action != nil {
				mcpLogMCP("Processing interactive action: %s", req.Action.Type)
				// v2.0.47：把关键决策点的 mcpLogMCP 升级为带 reqID + action 的 tag 版本。
				currentActionType = req.Action.Type
				mcpLogMCPWithTag(reqID, "dispatch action=%s session=%s", req.Action.Type, session.SessionID)
				crawlResult, err = dispatchCDPAction(session, req.Action, &req, dsRecord)
				if crawlResult != nil {
					recordSessionAction(session, *req.Action, crawlResult.URL)
				}
			} else {
				if req.URL == "" && dsRecord != nil {
					req.URL = dsRecord.URLAddress
					mcpLogMCP("Using data source URL: %s", req.URL)
				}
				if req.URL == "" {
					resultCh <- result{err: fmt.Errorf("URL is required (pass url or data_source_id)")}
					return
				}

				if dsRecord != nil {
					mcpLogMCP("Using data source config for: %s (ID: %d)", dsRecord.PlatformName, dsRecord.ID)
				}

				mcpLogMCP("Crawling URL: %s", req.URL)
				// v2.0.47：把"Crawling URL"决策点升级为带 reqID 的 tag 版本，
				// 排查 hang/navigate 超时时直接 grep reqID 即可看到调用上下文。
				mcpLogMCPWithTag(reqID, "crawl start url=%s session=%s timeout=%v max_content_len=%d",
					req.URL, session.SessionID, req.Timeout, req.MaxContentLen)
				// v2.0.0: 简单 navigate 走 rootCtx 的 default tab（不占 session tab 槽位）
				crawlResult, err = engine.crawlWebDataCDP(session, req.DataSourceID, req.URL, req.Timeout, req.MaxContentLen)
				// v2.0.13: 可选等待客户端 SPA 水合（hydration）完成 — 解决
				// chat.baidu.com 等"SSR 已就绪但客户端 bundle 未水合"导致
				// 所有 fill_form / click / eval 均无业务侧回调的问题。
				if err == nil && crawlResult != nil && req.WaitForHydration {
					// v2.0.18 补丁（基于问题分析报告_20260629_143200 §3.4）：
					// 级联 context canceled 状态下 engine.rootCtx 可能已经被
					// 重启流程 cancel 掉；此时 WithTimeout 会立即 done，
					// probeHydrationOnce 第一次就拿到 context canceled 错误，
					// 整个 navigate 后续也会失败。改成：如果 rootCtx 已经
					// canceled，直接返回 timeout 诊断、不要继续 probe，让
					// 调用方看到 hydration_state=timeout 后立即评估改走
					// restart_browser 或 API 直连。
					rootCtx := engine.rootCtx
					rootCtxCanceled := rootCtx != nil && rootCtx.Err() != nil
					if rootCtx == nil || rootCtxCanceled {
						crawlResult.HydrationState = &HydrationDiagnostics{
							State:             "timeout",
							WaitMs:            0,
							DetectedFramework: "static",
							Warning:           "hydration skipped: spider engine root context is cancelled (cascade context canceled) — call restart_browser or switch to API-direct",
						}
						crawlResult.Warnings = append(crawlResult.Warnings,
							"hydration skipped due to engine root context cancellation; do NOT retry fill_form/click/eval; switch to API-direct or restart_browser")
						mcpLogMCP("[SPIDER] hydration skipped: rootCtx canceled (cascade state), returning timeout diagnostic")
					} else {
						// rootCtx 健康：用其派生 WithTimeout 跑探测。
						hydrateCtx, hydrateCancel := context.WithTimeout(rootCtx, 6*time.Second)
						crawlResult.HydrationState = probeAndWaitForHydration(hydrateCtx, req.WaitForHydrationMs)
						hydrateCancel()
					}
					// v2.0.13-补丁：把"客户端 bundle 未水合"三连击信号透传为顶层
					// warning + error_type，避免 Agent 误以为 success=true 就是有效内容。
					// 三连击 = state=timeout + detected_framework=static + console_lines=0，
					// 即 chat.baidu.com 问题分析报告里的典型 case。
					if h := crawlResult.HydrationState; h != nil &&
						h.State == "timeout" && h.DetectedFramework == "static" && h.ConsoleLines == 0 {
						crawlResult.Warnings = append(crawlResult.Warnings,
							"client bundle not hydrated: state=timeout + framework=static + console_lines=0 — "+
								"SSR HTML ready but client JS never executed. "+
								"Do NOT retry fill_form/click/eval; switch to API-direct approach.")
					}
					// v2.0.17 补丁：客户端 bundle 被 CSP / 反爬拦截（hydration 探测
					// 看到 module_loads_failed > 0 或 module_zero_transfer > 0）
					// 同样意味着 CDP 事件不会有业务侧回调，提示 Agent 评估改走
					// API 直连方案（见问题分析报告_20260627_120444 §3.1）。
					if h := crawlResult.HydrationState; h != nil &&
						h.State == "timeout" && h.DetectedFramework == "static" &&
						(h.ModuleLoadsFailed > 0 || h.ModuleZeroTransfer > 0) {
						crawlResult.Warnings = append(crawlResult.Warnings,
							fmt.Sprintf(
								"client bundle likely blocked: module_loads_failed=%d, module_zero_transfer=%d — "+
									"script resources were intercepted (CSP / anti-bot). "+
									"Do NOT retry fill_form/click/eval; switch to API-direct approach.",
								h.ModuleLoadsFailed, h.ModuleZeroTransfer))
					}
				}

				recordSessionAction(session, InteractiveAction{
					Type: ActionTypeNavigate,
					URL:  req.URL,
				}, req.URL)
			}

			if err != nil {
				resultCh <- result{crawlResult: crawlResult, err: err}
				return
			}

			crawlResult.SessionID = session.SessionID
			session.CurrentURL = crawlResult.URL
			if crawlResult.RawHTML != "" {
				session.CurrentRawHTML = crawlResult.RawHTML
			}

			if req.ReturnPageState {
				crawlResult.PageState = extractPageState(crawlResult.RawHTML, crawlResult.URL)
			}

			if crawlResult.RawHTML != "" && crawlResult.Elements == nil {
				crawlResult.Elements = extractWebElements(crawlResult.RawHTML, crawlResult.URL)
			}

			// v2.0.3: elements sanity check
			if crawlResult.Elements != nil {
				allHeadingsEmptyURL := true
				for _, h := range crawlResult.Elements.Headings {
					if h.URL != "" {
						allHeadingsEmptyURL = false
						break
					}
				}
				allLinksEmptyURL := true
				for _, l := range crawlResult.Elements.Links {
					if l.URL != "" {
						allLinksEmptyURL = false
						break
					}
				}
				if len(crawlResult.Elements.Headings) > 0 && len(crawlResult.Elements.Links) > 0 &&
					allHeadingsEmptyURL && allLinksEmptyURL {
					mcpLogMCP("[SPIDER] WARN: elements sanity check failed — all heading/link URLs are empty (possible anti-bot page)")
				}
			}

			// v2.0.3: 反爬特征签名检测
			// v2.0.8: 命中时把 signals 写入 result，让外层 retry loop 决定是否重roll
			antiBotType, antiBotSignals := detectAntiBotSignals(crawlResult)
			if antiBotType != "" {
				mcpLogMCP("[SPIDER] Anti-bot signals detected: type=%s, signals=%v", antiBotType, antiBotSignals)
				resultCh <- result{
					crawlResult: crawlResult,
					err:         fmt.Errorf("anti-bot detected: %s", antiBotType),
					signals:     antiBotSignals,
				}
				return
			}

			if req.DataSourceID > 0 {
				crawlResult.DataSourceID = req.DataSourceID
			}

			// v2.0.30：成功路径中加入 SPA 路由 hints（基于 Agent5_InfoQ_报告_20260705）。
			// 即使 crawlWebDataCDP 成功返回，Agent 仍然可能 next 调 click 导航
			// 触发 SPA 路由失败；提前透传已知 topic URL 让 Agent 直接用 absolute
			// URL navigate 替代 click。
			if hints := SPAAlternativeHints(crawlResult.URL); len(hints) > 0 {
				crawlResult.SPAAlternativeHints = hints
			}

			crawlResult.HasMore = true

			mcpLogMCP("Crawl completed: %s, title: %s, content: %d, lang: %s, session: %s",
				crawlResult.URL, crawlResult.Title, len(crawlResult.Content), crawlResult.Language, session.SessionID)

			resultCh <- result{crawlResult: crawlResult, err: nil}
		}()

		select {
		case res := <-resultCh:
			return res, sessionRef
		case <-ctx.Done():
			mcpLogMCP("/SpiderWebData handler timeout after %v (soft limit)", spiderSoftTimeout)
			errType := "timeout"
			if time.Since(startTime) > spiderHandlerTimeout {
				mcpLogMCP("/SpiderWebData exceeded hard timeout %v, returning with warning", spiderHandlerTimeout)
				errType = "timeout_hard"
			}
			// v2.0.26 修复：session 查找加 RLock 保护（原来直接读 map
			// 存在数据竞态）；sem 释放用最新 engine 实例（restart 后
			// 局部变量 engine.sem 可能已重置）。
			// v2.0.33（基于问题分析报告_20260709_145100 §4.1-§4.3）：
			// handler 超时时必须同步等待 cleanup 完成再返回，否则 goroutine 异步
			// detach 与后续 retry / 新请求的 attach 竞态，会导致 sem 槽位重复释放
			// 或 session.cdpCtx 半初始化被复用，进而触发 index out of range panic 与
			// Chrome 进程泄漏。这里改用同步 cleanup + 5s 超时兜底。
			cleanupDone := make(chan struct{})
			go func() {
				defer close(cleanupDone)
				spiderSessionsMu.RLock()
				s, ok := spiderSessions[req.SessionID]
				spiderSessionsMu.RUnlock()
				if ok && s != nil {
					s.cdpMu.Lock()
					detachCDPContext(s)
					s.cdpMu.Unlock()
				}
				if eng := GetSpiderEngine(); eng != nil {
					select {
					case <-eng.sem:
						eng.releaseSem()
					default:
					}
				}
			}()
			select {
			case <-cleanupDone:
			case <-time.After(5 * time.Second):
				mcpLogMCP("[SPIDER] WARN: handler timeout cleanup did not finish within 5s")
			}
			select {
			case res := <-resultCh:
				res.timedOut = true
				res.err = fmt.Errorf("%s: %v", errType, res.err)
				return res, sessionRef
			case <-time.After(500 * time.Millisecond):
			}
			return result{
				err:      fmt.Errorf("%s after %v", errType, spiderSoftTimeout),
				timedOut: true,
			}, sessionRef
		}
	}

	// v2.0.8: 重试循环
	// v2.0.9: 扩展 — 代理重绑 / tab-kill / chrome-kill-on-exhausted
	// v2.0.43: 新增一次 timeout 退避重试（3s），应对国际站偶发网络拥塞/TLS
	// 握手慢导致 context deadline exceeded 的场景。timeout retry 不消耗
	// anti-bot 重试预算，也不旋转 session/UA/代理（纯网络层退避）。
	killChromeTried := false
	timeoutRetryUsed := false
	timeoutRetrying := false
	for attempt := 0; attempt <= plan.MaxAttempts || timeoutRetrying; attempt++ {
		// v2.0.47：每次 attempt 入口把 currentAttempt 同步到 closure 变量，
		// panic recover 抓 snapshot 时无需重扫调用栈。
		currentAttempt = attempt
		if attempt > 0 {
			if timeoutRetrying {
				mcpLogMCP("[SPIDER] Timeout retry attempt %d/%d after 3s backoff (no session rotation)", attempt, plan.MaxAttempts)
				// v2.0.47：带 reqID + attempt 号的 retry 决策日志
				mcpLogMCPWithTag(reqID, "retry timeout_backoff attempt=%d/%d", attempt, plan.MaxAttempts)
				// 轻量清理：kill tab 以清除可能卡死的页面，然后固定退避 3s
				if config.G != nil && config.G.SpiderAntiBotKillTabOnRetry && engine != nil {
					if err := engine.KillTabOnly(); err != nil {
						logger.Printf("[SPIDER] KillTabOnly failed: %v", err)
					}
				}
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
				}
				timeoutRetrying = false
			} else {
				mcpLogMCP("[SPIDER] Retry attempt %d/%d (after anti-bot/captcha)", attempt, plan.MaxAttempts)
				// v2.0.47：带 reqID + attempt 号的 retry 决策日志
				mcpLogMCPWithTag(reqID, "retry anti_bot attempt=%d/%d", attempt, plan.MaxAttempts)
				rerollSessionAntiBot(attemptSession, req.DataSourceID, engine)
				// v2.0.18 patch2：captcha 命中后主动轮换 session_id
				// 报告 §2.2：同一 session_id 复用易触发 CDP context canceled
				rotateSessionID(attemptSession, attempt)
				// v2.0.9: anti-bot 重试时尝试重新绑定代理（仅当 config.G.SpiderProxyBindPerSession=true）
				if config.G != nil && config.G.SpiderProxyBindPerSession {
					if newProxy := BindProxyForSession(attemptSession, req.DataSourceID); newProxy != "" {
						mcpLogMCP("[SPIDER] session %s re-bound to proxy: %s", attemptSession.SessionID, newProxy)
					}
				}
				// v2.0.9: 轻量恢复 — 每次重试前 kill tab
				if config.G != nil && config.G.SpiderAntiBotKillTabOnRetry && engine != nil {
					if err := engine.KillTabOnly(); err != nil {
						logger.Printf("[SPIDER] KillTabOnly failed: %v", err)
					}
				}
				backoff := plan.BackoffMs[attempt]
				if backoff > 0 {
					select {
					case <-time.After(time.Duration(backoff) * time.Millisecond):
					case <-ctx.Done():
					}
				}
			}
		}
		res, sess := runAttempt()
		attemptSession = sess
		errType := ""
		if res.err != nil {
			errType = classifySpiderError(res.err)
		}
		// v2.0.9: 记录代理失败（用于健康跟踪）
		if attemptSession != nil && (errType == "anti_bot" || errType == "captcha" || errType == "region_block") {
			GetProxyPool().RecordFailure(attemptSession.BoundProxy, errType)
		}
		if res.err == nil {
			// 成功：记录代理成功
			if attemptSession != nil {
				GetProxyPool().RecordSuccess(attemptSession.BoundProxy)
			}
			json.NewEncoder(w).Encode(MCPAPIResponse{
				Success: true,
				Message: "Crawl completed",
				Data:    res.crawlResult,
			})
			return
		}
		// v2.0.22（基于问题分析报告_20260701_060527 §3.1-§3.2 modelscope.cn
		// 案例）：浏览器沙箱 DNS 不可达是环境层错误，retry 同一个浏览器进程
		// 几乎必然再次失败，且每次 retry 都会消耗 60s 软超时 + 触发
		// "index out of range [-1]" goroutine panic（详见报告 §1.2 第一次
		// 尝试 message）。在第一次命中 errType=dns_unresolved 时主动触发
		// 一次 RestartChrome（独立 goroutine，不阻塞当前 attempt），
		// 让重试有可能换一个 chromium 子进程继续（如果父进程能 inherit
		// 正确的 /etc/resolv.conf）。注意：这不是银弹，浏览器启动参数
		// 没有 --dns-server 时重启通常还是失败，但至少给了运维一次
		// 自愈机会，且不会让 Agent 拿着同一个死局一直 retry。
		if errType == "dns_unresolved" && attempt == 0 {
			mcpLogMCP("[SPIDER] errType=dns_unresolved on first attempt; triggering RestartChrome once in background to flush stale browser DNS state (url=%s)", req.URL)
			go func() {
				if err := engine.RestartChrome(); err != nil {
					mcpLogMCP("[SPIDER] background RestartChrome (dns_unresolved recovery) failed: %v", err)
				}
			}()
		}
		// v2.0.43: timeout 退避重试 — 国际站高延迟/拥塞时，3s 后退避一次可能成功
		if errType == "timeout" && !timeoutRetryUsed {
			remaining := spiderSoftTimeout - time.Since(startTime)
			if remaining > 8*time.Second {
				mcpLogMCP("[SPIDER] errType=timeout on attempt %d; will do one 3s backoff retry (remaining=%v)", attempt, remaining)
				timeoutRetryUsed = true
				timeoutRetrying = true
				lastResult = res
				continue
			}
		}
		if ShouldAutoRetry(errType, plan) && attempt < plan.MaxAttempts {
			mcpLogMCP("[SPIDER] Anti-bot/captcha hit (errType=%s, attempt %d/%d), will retry",
				errType, attempt+1, plan.MaxAttempts)
			lastResult = res
			continue
		}
		lastResult = res
		break
	}

	// v2.0.35（基于问题分析报告_20260709_174800 §2.2 / §四.Bug B / §推测的修复优先级 P0 #2）：
	// retry loop 退出时（成功 / 失败 / 全部 retry 耗尽），attemptSession 已经被
	// getOrCreateSession 写入 spiderSessions map。原实现从不在 retry 退出时
	// 释放，导致 DNS 失败 / anti-bot / captcha / timeout 等任何错误路径下
	// session_total 都会累加（实测 DNS 一次失败：session_total=1），与 sem_used=0
	// 形成"信号量释放但 session 注册表未释放"的矛盾。这里统一在退出前释放：
	//   - 成功路径：返回 res.crawlResult 给 Agent，但 session map 也清理（Agent
	//     拿到 session_id 后想复用需要带 session_id 进来；getOrCreateSession
	//     找不到会自然新建）
	//   - 失败路径：所有 attempt 失败时清掉这次尝试的 session，避免累积泄漏
	//
	// 注意：仅在最后一次 attempt 的 session 上释放；mid-retry 路径
	// （attempt > 0 触发的 rerollSessionAntiBot / rotateSessionID 会改
	// attemptSession.SessionID），所以按"当前 res.err != nil 才释放"判断更稳妥。
	if lastResult.err != nil && attemptSession != nil {
		// 失败路径：释放该 session 的 CDP 资源 + 从 map 删除，避免
		// "DNS 一次失败 → session_total=1 永远泄漏"
		releaseSpiderSession(attemptSession)
		attemptSession = nil
		mcpLogMCP("[SPIDER] released attempt session after retry loop exit (error path)")
	} else if lastResult.err == nil && attemptSession != nil && req.SessionID == "" {
		// 成功路径：仅当 Agent 没指定 session_id 时清理（Agent 主动指定
		// session_id 时可能后续还要复用，不能清）；req.SessionID == "" 表示
		// 服务端自动创建的临时 session
		releaseSpiderSession(attemptSession)
		attemptSession = nil
		mcpLogMCP("[SPIDER] released auto-created attempt session after successful crawl")
	}

	// v2.0.18 patch2：登录墙降级 — 简化策略：仅把 mobile_fallback URL 作为 hint
	// 透传给 Agent（写到 login_wall_alternative_hints），由 Agent 决定是否发起新请求。
	// 不在 retry loop 内自动执行移动端 fallback 的原因：dispatchCDPAction 是 action-driven
	// 而非 URL-driven；新增独立 mobile 路径会与现有 session/CDP 生命周期耦合。
	// 实际 Agent 拿到 hint 后调用 /SpiderWebData { url: "m.jiqizhixin.com/..." } 即可。
	//
	// v2.0.22（基于问题分析报告_20260701_060527 §3.1-§3.2 modelscope.cn 案例）：
	// DNS 不可达时让 Agent 看到一条人类可读 warning，明确提示：
	//   1) 这是浏览器沙箱 DNS 解析问题，不是反爬
	//   2) 不要继续 retry 浏览器（同一进程必然同样失败）
	//   3) RSS fallback 已在更下方自动尝试，如 RSS 也无该站 feed 则需
	//      切换到 API 直连 / 镜像站 / 人工校对
	//   4) 运维侧：给 Chromium 启动加 --dns-server=<host-dns> 或
	//      --host-resolver-rules='MAP * ~NOTFOUND, EXCLUDE localhost'
	if lastResult.err != nil {
		dnErrType := classifySpiderError(lastResult.err)
		if dnErrType == "dns_unresolved" {
			if lastResult.crawlResult == nil {
				lastResult.crawlResult = &SpiderWebDataResponse{URL: req.URL}
			}
			if lastResult.crawlResult != nil {
				lastResult.crawlResult.Warnings = append(lastResult.crawlResult.Warnings,
					"browser sandbox DNS unresolved: "+
						"Chromium subprocess could not resolve the target host while host-level nslookup works. "+
						"Causes: (a) browser running in a network namespace with a broken /etc/resolv.conf, "+
						"(b) Async DNS Resolver (DoH) is blocked in the sandbox, "+
						"(c) seccomp blocks UDP/53 + TCP/853 egress. "+
						"Do NOT retry the browser path — same process will fail identically. "+
						"This response also auto-tried RSS fallback; if RSS returned 0 items, switch to API-direct or a mirror site.",
				)
			}
		}
	}
	if lastResult.err != nil && lastResult.crawlResult != nil {
		lws := detectLoginWallSignals(lastResult.crawlResult, lastResult.crawlResult.URL)
		if lws.Detected {
			if mobileURL := MobileFallbackURL(lastResult.crawlResult.URL); mobileURL != "" {
				// hint 已通过 enrichFailureResponseWithLoginWall 添加；
				// 这里仅打日志告知 Agent 移动端候选 URL
				mcpLogMCP("[SPIDER] login_wall detected on %s; mobile fallback candidate: %s",
					lastResult.crawlResult.URL, mobileURL)
			}
		}
	}

	// v2.0.19: 移动端 UA 自动降级 — 当所有桌面 UA 重试均因 anti_bot/captcha 失败时，
	// 自动用移动端 UA + 移动端 URL 发起一次额外尝试。大量国内站点（机器之心 / 36kr / 虎嗅）
	// 桌面端部署登录墙但 m.xxx.com 移动版仍可访问。
	//
	// v2.0.19 补丁（基于问题分析报告_20260630_095236 §6 建议 4 + 报告 §3.3）：
	// 当 detectLoginWallSignals 已经把目标站识别为明确的 login_wall /
	// paywall / data_service_landing 时，移动端 fallback 几乎必然同样撞墙
	// （m.jiqizhixin.com 返回空 HTML 是触发「index out of range [-1]」
	// panic 的真实源头）。这种情况直接跳过 mobile fallback，把 login_wall
	// 错误类型透传给 Agent，让 Agent 决策切到 RSS / 替代数据源，避免
	// 触发 panic + 浪费 30s 兜底超时。
	if lastResult.err != nil {
		lastErrType := classifySpiderError(lastResult.err)
		if (lastErrType == "anti_bot" || lastErrType == "captcha") && attemptSession != nil {
			origURL := req.URL
			crawlURL := origURL
			if lastResult.crawlResult != nil && lastResult.crawlResult.URL != "" {
				crawlURL = lastResult.crawlResult.URL
			}
			mobileURL := MobileFallbackURL(crawlURL)
			mobileUA := PickMobileUA(0)

			// 登录墙已识别为明确付费 / 数据服务 Landing Page 时，跳过 mobile fallback
			skipMobileFallback := false
			if lastResult.crawlResult != nil {
				lws := detectLoginWallSignals(lastResult.crawlResult, crawlURL)
				if lws.Detected && (lws.WallType == "paywall" || lws.WallType == "data_service_landing") {
					mcpLogMCP("[SPIDER] login_wall type=%s detected on %s; skip mobile fallback (already covered by login_wall hints)", lws.WallType, crawlURL)
					skipMobileFallback = true
				}
			}

			if !skipMobileFallback && mobileURL != "" && mobileUA != "" {
				mcpLogMCP("[SPIDER] All desktop retries exhausted (errType=%s), attempting mobile fallback: %s (ua=%s...)",
					lastErrType, mobileURL, mobileUA[:50])
				// v2.0.19: 通过 session.OverrideUA 临时切换为移动端 UA（线程安全，不影响全局 config.G）
				attemptSession.OverrideUA = mobileUA
				// 强制 session 重roll fingerprint（下次 prepareSessionNavigation 会用新 UA）
				rerollSessionAntiBot(attemptSession, req.DataSourceID, engine)
				// 切换到移动端 URL
				req.URL = mobileURL

				// v2.0.19 补丁（基于问题分析报告_20260630_095236 §3.1 + 建议 5）：
				// runAttempt 内部 goroutine 已经有 defer recover，但 mobile fallback 路径
				// 上 runAttempt 之外的代码（result 处理 / 状态恢复）一旦 panic 仍会把
				// 整个 handler 拉死（curl 客户端拿到空响应 + exit 1）。这里加同步层
				// recover，把任何 panic 降级成普通 error，让 handler 继续走完整失败响应
				// 路径，避免「index out of range [-1]/[-2]」 goroutine panic 升级为
				// 服务级中断。
				var res result
				var sess *SpiderSession
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							mcpLogMCP("PANIC in mobile fallback attempt: %v", rec)
							res = result{err: fmt.Errorf("mobile fallback panic: %v", rec)}
						}
					}()
					res, sess = runAttempt()
				}()
				attemptSession = sess
				// 恢复原始 URL + 清理 OverrideUA（即便上面 panic 也要恢复，否则后续请求会一直走移动端 UA）
				req.URL = origURL
				if attemptSession != nil {
					attemptSession.OverrideUA = ""
				}
				if res.err == nil {
					mcpLogMCP("[SPIDER] Mobile fallback succeeded for %s", mobileURL)
					if attemptSession != nil {
						GetProxyPool().RecordSuccess(attemptSession.BoundProxy)
					}
					json.NewEncoder(w).Encode(MCPAPIResponse{
						Success: true,
						Message: "Crawl completed (mobile fallback)",
						Data:    res.crawlResult,
					})
					return
				}
				mcpLogMCP("[SPIDER] Mobile fallback also failed: %v", res.err)
				lastResult = res
			}
		}
	}

	// v2.0.9: 重量恢复 — 重试耗尽后（且命中 anti_bot/captcha）尝试 kill Chrome 再 retry 一次
	// v2.0.20: 改造 — 抽出 tryFallbackToRSSFeeds 步骤，让 RSS 兜底能在 kill Chrome 前后都生效
	if config.G != nil && config.G.SpiderAntiBotKillOnExhausted && !killChromeTried && engine != nil {
		lastErrType := ""
		if lastResult.err != nil {
			lastErrType = classifySpiderError(lastResult.err)
		}
		if ShouldAutoRetry(lastErrType, plan) {
			killChromeTried = true
			mcpLogMCP("[SPIDER] retry plan exhausted; killing Chrome (killOnExhausted=true) and retrying once more")
			if err := engine.RestartChrome(); err != nil {
				logger.Printf("[SPIDER] RestartChrome failed: %v", err)
			} else {
				// 一次额外 attempt
				res, sess := runAttempt()
				attemptSession = sess
				if res.err == nil {
					if attemptSession != nil {
						GetProxyPool().RecordSuccess(attemptSession.BoundProxy)
					}
					json.NewEncoder(w).Encode(MCPAPIResponse{
						Success: true,
						Message: "Crawl completed (after Chrome restart)",
						Data:    res.crawlResult,
					})
					return
				}
				lastResult = res
			}
		}
	}

	// 所有 attempt 失败：按 lastResult 输出
	res := lastResult
	errType := ""
	if res.err != nil {
		errType = classifySpiderError(res.err)
	}

	// v2.0.24：lifted to outer scope so timeout_hint（三个 failure 路径都用）能复用
	fallbackReqURL := req.URL
	if res.crawlResult != nil && res.crawlResult.URL != "" {
		fallbackReqURL = res.crawlResult.URL
	}

	// v2.0.20: 反爬兜底 — 当浏览器兜底全部失败且启用 fallback_strategy=auto 时，
	// 尝试标准 HTTP 客户端 fetch RSS / Atom feed；命中即把 RSS items 转成
	// 等价的 SpiderWebDataResponse 返回，让 Agent 拿到结构化文章列表。
	//
	// 触发条件（任一）：
	//   - req.FallbackStrategy == "rss_first"（Agent 显式声明跳过浏览器）
	//   - req.FallbackStrategy != "none" 且 errType ∈ {anti_bot, captcha, region_block, login_wall, timeout}
	//     且 lastResult.crawlResult 为 nil 或命中 login_wall signals
	//
	// 注意：调用前必须先做安全检查（lastResult.crawlResult 必须存在或 URL 已配置），
	// 避免对无 URL 的请求无限重试 RSS。
	if rssFallbackStrategy != "none" && shouldTryRSSFallback(errType, res.crawlResult, rssFallbackStrategy) {
		// 同步捕获 panic：rss fallback 不能因为一处解析问题把整个 handler 拉死
		var fallbackResp *SpiderWebDataResponse
		var fallbackUsed bool
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					mcpLogMCP("[SPIDER] PANIC in RSS fallback: %v", rec)
				}
			}()
			fallbackResp, fallbackUsed = tryRSSFallbackForFailure(fallbackReqURL, res.crawlResult, req, errType)
		}()
		if fallbackUsed && fallbackResp != nil {
			mcpLogMCP("[SPIDER] RSS fallback returned %d items (url=%s, strategy=%s)",
				fallbackResp.RSSItemCount, fallbackResp.RSSSourceURL, rssFallbackStrategy)
			json.NewEncoder(w).Encode(MCPAPIResponse{
				Success: true,
				Message: "Crawl completed (via RSS fallback)",
				Data:    fallbackResp,
			})
			return
		}
		mcpLogMCP("[SPIDER] RSS fallback yielded no items (strategy=%s, errType=%s); falling through to failure path",
			rssFallbackStrategy, errType)
	}

	// v2.0.21: 终极兜底 — 从 lastResult.RawHTML 中提取文章级 URL
	// 适用场景：RSS / 第三方聚合全部失败，但浏览器抓到的 HTML 仍含 SSR
	// 渲染的推荐链接 / 导航 / 底部"相关阅读"。这些链接能给 Agent 至少
	// 知道"该站还有这些文章存在"（参考问题分析报告_20260630_220512 §3.2-§3.3
	// partial_result 缺 URL 的关键缺口）。
	//
	// 触发条件：rssFallbackStrategy != "none" 且 lastResult 有 RawHTML。
	// 返回成功时早返回；返回失败时继续走 failure path 透传给 Agent。
	if rssFallbackStrategy != "none" && res.crawlResult != nil {
		var htmlResp *SpiderWebDataResponse
		var htmlUsed bool
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					mcpLogMCP("[SPIDER] PANIC in HTML article URL fallback: %v", rec)
				}
			}()
			htmlResp, htmlUsed = tryHTMLArticleURLFallback(res.crawlResult, errType)
		}()
		if htmlUsed && htmlResp != nil {
			mcpLogMCP("[SPIDER] HTML article URL fallback returned %d items (url=%s)",
				htmlResp.RSSItemCount, res.crawlResult.URL)
			json.NewEncoder(w).Encode(MCPAPIResponse{
				Success: true,
				Message: "Crawl completed (via HTML article URL fallback)",
				Data:    htmlResp,
			})
			return
		}
	}
	if res.timedOut {
		timeoutMsg := fmt.Sprintf("Crawl timeout after %v", spiderSoftTimeout)
		if errType == "timeout_hard" {
			timeoutMsg = fmt.Sprintf("Crawl timeout after %v (exceeded hard limit %v)", spiderSoftTimeout, spiderHandlerTimeout)
		}
		respData := map[string]interface{}{"error_type": errType}
		// 超时分支：session 已经创建出来（如果 res.crawlResult 有 session_id），尽量回填
		buildFailureDataTopLevelFields(respData, res.crawlResult, nil, false, errType)
		// v2.0.18 patch2：登录墙检测（即使 timeout 也能识别商业化改版）
		// v2.0.25（基于 LsmTokensServer.log 2026/07/03 06:16:12 / 06:17:32 / 06:18:50 /
		// 06:19:47 / 06:20:21 PANIC 堆栈）：handler 4 分钟软超时分支（res.timedOut=true）
		// 且 goroutine 没产出 crawlResult（res.crawlResult==nil）时，
		// 原代码 `res.crawlResult.URL` 触发 nil pointer dereference panic，
		// 把整个 MCP /SpiderWebData handler 拉死并造成客户端 curl exit 1。
		// 改用已经在 1189-1192 行做了 nil-safe 处理的 fallbackReqURL，
		// 并保证 enrichFailureResponseWithLoginWall 自身对 nil 也安全
		//（见函数首行 if r == nil return）。这样 4 分钟超时 + RSS fallback 全失败
		// 时，handler 仍然返回标准化失败响应（error_type / timeout_hint），
		// 不会让 MCP 客户端拿到空响应。
		enrichFailureResponseWithLoginWall(respData, res.crawlResult, fallbackReqURL)
		// v2.0.24：timeout 诊断增强 — 把"下一步怎么走"写进响应顶层 hint，
		// 避免 Agent 拿到 120 字节 timeout 错误后盲目重试。
		// 参考 spider_report_data_source_6_2026-07-02 §4-§5（TechCrunch case）。
		if hint := buildTimeoutHint(res.err, fallbackReqURL); hint != "" {
			respData["timeout_hint"] = hint
		}
		// v2.0.27（基于问题分析报告_20260703_062125 §1.5 timeout+RSS fallback
		// failure path）：当 res.timedOut=true 且 res.crawlResult==nil 时，说明
		// goroutine 在 60s watchdog 强制清理前没产出任何结果，handler 顶层 recover
		// 已主动触发服务端自愈（detachAllSpiderSessions + RestartChrome）。
		// 多数场景下下一次请求可正常返回；如仍超时，需人工介入。
		if res.crawlResult == nil {
			respData["engine_recovery_hint"] = "MCP /SpiderWebData 在 60s watchdog 强制清理前未产出结果；" +
				"chromedp target / session 池可能已污染。\n" +
				"服务端已自动执行 detachAllSpiderSessions + RestartChrome 自愈，" +
				"多数场景下下一次请求可正常返回（即使不带 action=restart_browser）。\n" +
				"如仍超时：在下一次请求中带 {\"action\":{\"type\":\"restart_browser\"}} 强制重置 Chrome。\n" +
				"长期方案：如 restart_browser 后仍连续超时，需 kill LsmTokensServer + Chrome 整个进程后重启。\n" +
				"请勿连续重试同一 URL（会进入「timeout → 残留损伤 → 连续 timeout」循环）。"
		}
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: timeoutMsg,
			Data:    respData,
		})
		return
	}
	if res.crawlResult != nil {
		mcpLogMCP("Crawl failed: %v (errType=%s, signals=%v)", res.err, errType, res.signals)
		respData := map[string]interface{}{
			"partial_result": buildPartialResultForFailure(res.crawlResult, errType),
		}
		if errType != "" {
			respData["error_type"] = errType
		}
		if len(res.signals) > 0 {
			respData["signals"] = res.signals
		}
		// v2.0.8: 反爬/captcha/region_block 失败时把 session_id / elements / page_state 平铺到顶层
		// 满足 v2.0.7 文档"data.session_id 用于多轮对话"约定；Agent 拿到 session_id 可继续 diagnose
		// (例如：session_id + action.get_state / eval console_logs 看反爬脚本加载情况)
		buildFailureDataTopLevelFields(respData, res.crawlResult, res.signals, errType == "anti_bot" || errType == "captcha", errType)
		// v2.0.24：timeout 诊断增强 — partial_result 路径同样注入 hint
		if errType == "timeout" || errType == "timeout_hard" {
			if hint := buildTimeoutHint(res.err, res.crawlResult.URL); hint != "" {
				respData["timeout_hint"] = hint
			}
		}
		message := fmt.Sprintf("Crawl failed: %v", res.err)
		if errType == "anti_bot" || errType == "captcha" {
			message = fmt.Sprintf("Crawl blocked: anti-bot detected (%s) after %d retries", errType, plan.MaxAttempts)
		}
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: message,
			Data:    respData,
		})
		return
	}
	mcpLogMCP("Crawl failed: %v (errType=%s)", res.err, errType)
	respData := map[string]interface{}{}
	if errType != "" {
		respData["error_type"] = errType
	}
	// v2.0.24：timeout 诊断增强 — 这是 TechCrunch 案例走的分支
	// （crawlResult == nil，err = "CDP fetch failed: context deadline exceeded"）。
	// 原本响应只有 {"error_type":"timeout"} + 120 字节，Agent 无法决策下一步。
	if errType == "timeout" || errType == "timeout_hard" {
		if hint := buildTimeoutHint(res.err, fallbackReqURL); hint != "" {
			respData["timeout_hint"] = hint
		}
	}
	// v2.0.26（基于问题分析报告_20260703_061200 / _061632 / _062125）：
	// 当 res.crawlResult==nil 且 res.timedOut==false 时，说明 goroutine 提前
	// 失败且没产出任何结果（典型表现是 errType=unknown 或 internal_panic）。
	// 这种情况意味着 chromedp target / session 池已污染，Agent 不应 retry
	// 同一 URL，应先发 restart_browser action。
	if res.crawlResult == nil && (errType == "unknown" || errType == "internal_panic") {
		respData["engine_recovery_hint"] = "MCP /SpiderWebData goroutine 在失败时未产出结果；" +
			"chromedp target / session 池可能已污染。\n" +
			"立即可试：下一次请求带 {\"action\":{\"type\":\"restart_browser\"}} 让 handler 自动重启 Chrome。\n" +
			"长期方案：如 restart_browser 后仍失败，需 kill LsmTokensServer + Chrome 整个进程后重启。\n" +
			"请勿在未做 restart_browser 的情况下连续重试同一 URL（会进入「panic → 污染 → 连续 panic」循环）。"
	}
	json.NewEncoder(w).Encode(MCPAPIResponse{
		Success: false,
		Message: fmt.Sprintf("Crawl failed: %v", res.err),
		Data:    respData,
	})
}

// failurePartialHTMLCharLimit 反爬/captcha 失败时 partial_result 内 raw_html 截断长度
// 设 8KB 平衡诊断价值（能看见反爬脚本片段 / 标题/描述）vs 响应体过大致截断（参见
// 问题分析报告 2026-06-24：partial_result 内嵌数 MB raw_html 时 curl 端 SIGKILL）
const failurePartialHTMLCharLimit = 8 * 1024

// buildPartialResultForFailure 在爬取失败时构造一个"瘦身版"partial_result：
//   - raw_html 截断到 8KB（避免响应流被 SIGKILL 截断）
//   - 其余字段保留（title / content / elements / page_state 供 Agent 复用）
//
// 输入 r 不会被修改；返回 *SpiderWebDataResponse 是浅拷贝 + 截断的 raw_html。
// r 为 nil 时返回 nil（调用方应自行处理）。
func buildPartialResultForFailure(r *SpiderWebDataResponse, errType string) *SpiderWebDataResponse {
	if r == nil {
		return nil
	}
	clone := *r
	// 反爬/captcha/region_block：raw_html 数 MB 级是常见情形（页面虽然被拦但 HTML 已渲染完），
	// 截断到 8KB 既能保留诊断线索（captcha_script 节点/标题）又不会撑爆响应流。
	// 其他失败类型（timeout / rate_limit）raw_html 通常较小，不动它。
	if errType == "anti_bot" || errType == "captcha" || errType == "region_block" {
		if len(clone.RawHTML) > failurePartialHTMLCharLimit {
			clone.RawHTML = truncateRunes(clone.RawHTML, failurePartialHTMLCharLimit)
			clone.RawHTML = clone.RawHTML + "\n<!-- [truncated by MCP: full HTML suppressed on anti-bot/captcha to keep response small; use action.eval/console_logs to inspect live DOM] -->\n"
		}
	}
	// v2.0.21: captcha/anti_bot 失败响应里把 RawHTML 中的文章 URL 抽出来
	// 挂到 elements.articles / elements.headings / elements.links，Agent
	// 拿到 partial_result 就能直接看到「该站还有这些文章」（参考问题分析
	// 报告_20260630_220512 §3.2-§3.3 partial_result 缺 URL 的关键缺口）。
	//
	// 设计原则：与主流程 extractWebElements 互补；只在主流程零 articles 命中
	// 时启用，避免重复输出。
	if errType == "anti_bot" || errType == "captcha" || errType == "region_block" {
		if clone.Elements == nil || len(clone.Elements.Articles) == 0 {
			items := ExtractArticleURLsFromHTML(clone.RawHTML, clone.URL)
			if len(items) > 0 {
				links, headings, articles := rssFetchResultToElements(&RSSFetchResult{
					Items:     items,
					SourceURL: clone.URL + " (partial-html-extract)",
				})
				if clone.Elements == nil {
					clone.Elements = &WebElements{
						Links:      []WebElementLink{},
						Headings:   []WebElementHeading{},
						Paragraphs: []WebElementParagraph{},
					}
				}
				// 合并（不覆盖已有）
				if len(articles) > 0 {
					clone.Elements.Articles = append(clone.Elements.Articles, articles...)
				}
				if len(headings) > 0 && len(clone.Elements.Headings) == 0 {
					clone.Elements.Headings = headings
				}
				if len(links) > 0 && len(clone.Elements.Links) == 0 {
					clone.Elements.Links = links
				}
				mcpLogMCP("[SPIDER] buildPartialResultForFailure: extracted %d article URLs from %s (errType=%s)",
					len(items), clone.URL, errType)
			}
		}
	}
	return &clone
}

// buildFailureDataTopLevelFields 在失败响应 data.* 顶层补齐 session_id / elements / page_state /
// url / title / content / data_source_id / crawl_time，让 Agent 在 success=false 时也能
// 复用 v2.0.7 文档约定的"data.session_id 用于多轮对话"能力（用于 session 诊断：eval
// 反爬脚本 / console_logs / get_state 等）。
// includeAntiBotHints=true 时附加 anti_bot_hint 文本提示，让 Agent 立即识别反爬原因。
// errType 是 classifySpiderError 出来的字符串，v2.0.25：用于注入 buildFallbackStrategyHint。
func buildFailureDataTopLevelFields(respData map[string]interface{}, r *SpiderWebDataResponse, signals []string, includeAntiBotHints bool, errType string) {
	if r == nil {
		return
	}
	if r.SessionID != "" {
		respData["session_id"] = r.SessionID
	}
	if r.URL != "" {
		respData["url"] = r.URL
	}
	if r.Title != "" {
		respData["title"] = r.Title
	}
	if r.Content != "" {
		respData["content"] = r.Content
	}
	if r.DataSourceID > 0 {
		respData["data_source_id"] = r.DataSourceID
	}
	if !r.CrawlTime.IsZero() {
		respData["crawl_time"] = r.CrawlTime
	}
	if r.Elements != nil {
		respData["elements"] = r.Elements
	}
	if r.PageState != nil {
		respData["page_state"] = r.PageState
	}
	if includeAntiBotHints {
		hint := buildAntiBotHint(signals)
		if hint != "" {
			respData["anti_bot_hint"] = hint
		}
	}
	// v2.0.18 patch2：登录墙信号在 anti-bot/captcha 命中时一并检测
	// 报告会同时把 login_wall_signals / login_wall_alternative_hints / warnings
	// 注入到 respData（命中时）。
	enrichFailureResponseWithLoginWall(respData, r, r.URL)
	// v2.0.25：无论 includeAntiBotHints 是否为 true，只要 errType 是反爬 / login_wall /
	// region_block / dns_unresolved 八类之一，就注入 fallback_strategy_hint。让 Agent
	// 不需要读懂 login_wall_alternative_hints 六行 + 自己拼 fallback_strategy 字段。
	if hint := buildFallbackStrategyHint(errType, r.URL); hint != "" {
		respData["fallback_strategy_hint"] = hint
	}
	// v2.0.30：SPA 路由 hints —— 永远注入（不依赖 login_wall / anti_bot 信号），
	// 让 Agent 一开始就知道当前 URL 是否 SPA、需要 direct URL navigate 替代 click。
	// 基于 Agent5_InfoQ_报告_20260705：infoq.cn 导航 tab 是 Nuxt + Vue Router，
	// a.click() 不会触发 pushState，必须 navigate URL 直链 /topic/AI 等路径。
	enrichFailureResponseWithSPA(respData, r.URL)
}

// buildTimeoutHint v2.0.24 新增：CDP 浏览器抓取超时（context deadline exceeded）
// 时的可读提示。背景：spider_report_data_source_6_2026-07-02（TechCrunch case）连续 6 次
// "Crawl failed: CDP fetch failed: context deadline exceeded" + 仅 120 字节响应
// （{"error_type":"timeout"}），Agent 无法判断是浏览器渲染慢 / 反爬 / 网络问题，
// 只能盲目重试或放弃。
//
// 设计目标：在 timeout 失败响应里同时给出：
//   - 根因分类（CDP domcontentloaded/load 不触发 / 反爬 / 网络问题 / DNS）
//   - 可立即执行的下一步（fallback_strategy=rss_first / fallback_strategy=auto）
//   - Agent 友好的人类语言，避免深读嵌套 JSON
//
// 输入：
//   - err: 原始错误（用于从 message 中识别 "context deadline exceeded" /
//     "ERR_NAME_NOT_RESOLVED" / "ERR_EMPTY_RESPONSE" 等典型 Chrome 网络错误）
//   - url: 目标 URL（用于判断是否命中已知 RSS feed 候选）
//
// 返回：人类可读提示字符串（空字符串表示 errType 非 timeout / 无可建议）
func buildTimeoutHint(err error, url string) string {
	if err == nil {
		return ""
	}
	errStr := strings.ToLower(err.Error())
	// 只对真正 CDP 渲染超时做提示；不要把 anti-bot 触发的超时误归到这里
	if !strings.Contains(errStr, "deadline") && !strings.Contains(errStr, "timeout") {
		return ""
	}

	// DNS 不可达已经在 classify 里归为 dns_unresolved，不会走到这里；
	// 但保留兜底文案供后续扩展。
	var hints []string

	// 1) 总是告诉 Agent 这是浏览器渲染层超时，不是网络/服务不可达
	hints = append(hints,
		"目标站点在 headless Chrome 内 domcontentloaded/load 长时间未触发；"+
			"这是浏览器渲染层问题（重 JS / 广告 / 反爬脚本探测 headless），不是 HTTP 不可达。")

	// 2) 检查目标 URL 是否有 RSS 候选
	if url != "" {
		sources, lookupErr := LookupRSSFallbackSources(url)
		if lookupErr == nil && len(sources.Candidates) > 0 {
			hints = append(hints,
				fmt.Sprintf("立即可试：用 fallback_strategy=rss_first 重试同一 URL，"+
					"handler 会跳过浏览器直接 fetch RSS feed（首选候选: %s）。",
					sources.Candidates[0]))
		} else {
			hints = append(hints,
				"立即可试：用 fallback_strategy=rss_first 重试；handler 会探测 /feed /rss /atom.xml 标准路径。")
		}
	} else {
		hints = append(hints,
			"立即可试：用 fallback_strategy=rss_first 重试；handler 会探测 /feed /rss /atom.xml 标准路径。")
	}

	// 3) 切数据源 / 走移动端 / 走第三方索引
	hints = append(hints,
		"长期方案：调 /GetSpiderDataSource 切换数据源（RSS 镜像 / 第三方索引 / 移动端 m.站），"+
			"或在 description 中标注 'prefer_rss=1' 让后续调度默认走 RSS。")

	return strings.Join(hints, " ")
}

// buildFallbackStrategyHint v2.0.25：对非 timeout 的反爬 / 登录墙 / 付费墙失败
// 注入「下一步动作」可操作提示。Agent 不必读懂 login_wall_alternative_hints 六行文字，
// 直接看到"下次请求 fallback_strategy=rss_first 走 RSS"就能拼下一个请求。
//
// v2.0.26（基于问题分析报告_20260703_061200 / _061632 / _062125）：新增
// internal_panic 分支（参考下方 case），与 anti_bot 八类并列。Agent 拿到
// errType=internal_panic 后会被明确告知：不要 retry 浏览器、必须先发
// restart_browser action 让 chromedp target / session 池自愈。
//
// 与 buildTimeoutHint 的区别：buildTimeoutHint 只对 errType=timeout 生效；
// 本函数对 anti_bot / captcha / region_block / login_wall / paywall /
// data_service_landing / session_invalid / dns_unresolved / internal_panic
// 九类 errType 生效。
//
// 返回空字符串表示 errType 无需额外提示（如 timeout / unknown / 未知），调用方用空判断。
func buildFallbackStrategyHint(errType, rawURL string) string {
	// v2.0.26 特殊处理：internal_panic 走完整流程后会拼出 RSS fallback 建议，
	// 但 chromedp 池已污染时 rss_first 也会失败（共用同一进程）。这种场景 Agent
	// 应只走 restart_browser 路径，不应被引导到 RSS；提前在 case 内已注入
	// actionable 文本，函数末尾不再追加 RSS / 切数据源 / cookie 兜底段。
	if errType == "internal_panic" {
		return "errType=internal_panic: handler 顶层捕获了未处理的 panic（典型为 nil pointer / nil map）；" +
			"chromedp target / session 池已污染。**必须**在下一次请求带 " +
			"{\"action\":{\"type\":\"restart_browser\"}} 让 handler 自愈；如 restart_browser 后仍 panic，" +
			"需 kill LsmTokensServer + Chrome 整个进程后重启。" +
			"RSS fallback 不可用（共用同一进程），切数据源 / 注入 cookie 也不能绕过污染状态。"
	}
	if errType == "" || errType == "timeout" || errType == "timeout_hard" || errType == "unknown" {
		return ""
	}
	validTypes := map[string]bool{
		"anti_bot": true, "captcha": true, "region_block": true,
		"login_wall": true, "paywall": true, "data_service_landing": true,
		"session_invalid": true, "dns_unresolved": true,
		"internal_panic": true,
	}
	if !validTypes[errType] {
		return ""
	}
	var parts []string
	switch errType {
	case "anti_bot", "captcha":
		parts = append(parts, fmt.Sprintf("errType=%s: 浏览器被反爬/验证码拦截；同会话重试大概率重发同一反爬 fingerprint，基本不会自愈。", errType))
	case "region_block":
		parts = append(parts, "errType=region_block: 目标站点拒绝来自当前 IP/地区；建议换数据源或配置高匿代理。")
	case "login_wall", "paywall":
		parts = append(parts, fmt.Sprintf("errType=%s: 目标站点命中登录/付费墙；浏览器爬取无效，用户需注入订阅 cookie 才能解锁原文。", errType))
	case "data_service_landing":
		parts = append(parts, "errType=data_service_landing: 目标站点已商业化改版（如机器之心 2026），原文章内容已被付费数据服务 Landing Page 替换。")
	case "session_invalid":
		parts = append(parts, "errType=session_invalid: CDP session 被后端反爬标记为不可交互；需 restart_browser 重置会话。")
	case "dns_unresolved":
		parts = append(parts, "errType=dns_unresolved: 浏览器沙箱 DNS 不可达（与 Go 标准 net/http 是不同解析栈），不要用 retry 浏览器的方式恢复。")
	}

	if rawURL != "" {
		sources, lookupErr := LookupRSSFallbackSources(rawURL)
		if lookupErr == nil && len(sources.Candidates) > 0 {
			parts = append(parts, fmt.Sprintf(
				"立即可试：下次请求带 fallback_strategy=%q 重试同一 URL（RSS 首选候选: %s）；"+
					"RSS 走 Go 标准 net/http，不依赖浏览器进程，能绕开 captcha / login_wall / anti_bot。",
				"rss_first", sources.Candidates[0]))
		} else {
			parts = append(parts, fmt.Sprintf(
				"立即可试：下次请求带 fallback_strategy=%q 重试；handler 会探测 /feed /rss /atom.xml 标准路径。",
				"rss_first"))
		}
	} else {
		parts = append(parts, fmt.Sprintf(
			"立即可试：下次请求带 fallback_strategy=%q 重试；handler 会探测 /feed /rss /atom.xml 标准路径。",
			"rss_first"))
	}
	parts = append(parts,
		"长期方案：调 /GetSpiderDataSource 切换数据源；或在服务器配置 spiderUserAgentPerSource + 高匿代理；"+
			"用户提供登录 cookie 后通过 action={type:cookies, op:import} 一次性导入多条 cookie 注入。")
	return strings.Join(parts, " ")
}

// enrichRSSFallbackResponseWithLoginWall v2.0.25：RSS fallback 成功时，把 login_wall
// / 付费墙提示追加到 resp.Warnings。背景：即使 rsshub.app 兜底拿到 RSS 列表，Agent
// 下次访问相同 host 还会傻乎乎走浏览器路径。这条 warning 告诉 Agent ——
// "这个 host 历史上是付费墙站，拿原文需要注入 cookie / 切换数据源"。
func enrichRSSFallbackResponseWithLoginWall(resp *SpiderWebDataResponse, rawURL string) {
	if resp == nil {
		return
	}
	host := extractHost(rawURL)
	if host == "" {
		return
	}
	hostLower := strings.ToLower(host)
	switch {
	case strings.Contains(hostLower, "jiqizhixin.com"):
		resp.appendWarning(fmt.Sprintf(
			"host=%s 为已知付费墙站（2026 商业化改版）。本次结果来自第三方 RSS 聚合（%s）；"+
				"如需原文请调 /GetSpiderDataSource 切换数据源，或用 action={type:cookies, op:import} 注入订阅 cookie。",
			hostLower, resp.RSSSourceURL))
	case strings.Contains(hostLower, "36kr.com"), strings.Contains(hostLower, "huxiu.com"):
		resp.appendWarning(fmt.Sprintf(
			"host=%s 为已知付费墙/登录墙站。本次结果来自第三方 RSS 聚合（%s）；"+
				"如需原文拿正文请调 /GetSpiderDataSource 切换数据源，或通过 action={type:cookies, op:import} 注入 cookie。",
			hostLower, resp.RSSSourceURL))
	}
}

// appendWarning 内部 helper：懒初始化 Warnings 切片，避免 nil panic
func (r *SpiderWebDataResponse) appendWarning(msg string) {
	if r == nil {
		return
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	r.Warnings = append(r.Warnings, msg)
}

// buildAntiBotHint 根据反爬信号生成可读的提示文本
func buildAntiBotHint(signals []string) string {
	if len(signals) == 0 {
		return ""
	}
	// 优先用 captcha 关键字
	for _, s := range signals {
		if strings.Contains(s, "captcha") {
			return "目标站点检测到验证码/反爬挑战脚本（captcha/turnstile/hcaptcha/recaptcha）。当前会话不可继续数据提取；可尝试：用 /GetSpiderDataSource 切换数据源；为目标域配置 spiderUserAgentPerSource + 高匿代理；或换官方 RSS / 第三方索引。"
		}
	}
	for _, s := range signals {
		if strings.Contains(s, "black list") || strings.Contains(s, "ua black") {
			return "目标站点 UA 黑名单 / 区域封锁（403/451）。建议：为目标域配置 spiderUserAgentPerSource + 代理；或换数据源。"
		}
	}
	for _, s := range signals {
		if strings.Contains(s, "anti_bot_template") {
			return "目标站点命中已知反爬推广模板（如机器之心数据服务等）。建议：换数据源或人工授权后通过 /GetSpiderDataSource 注入 cookie。"
		}
	}
	if len(signals) > 0 {
		return fmt.Sprintf("命中反爬信号 %v。建议：参考 MCP 文档切换数据源 / 调整 UA 与代理。", signals)
	}
	return ""
}

// ==================== v2.0.18 patch2：登录墙 / 付费 Landing Page 检测 ====================
//
// 基于 问题分析报告_20260629_152341.md（机器之心 www.jiqizhixin.com 文章库采集）：
//  - 公开网站已转为「数据服务 Landing Page」+ 强制登录墙
//  - 原 /articles 路径未登录用户被强制重定向到商业化页面
//  - 当前 MCP 接到 captcha 后仅说"反爬检测到"，无法告诉 Agent 这是登录墙，
//    应该走 RSS / 移动端 / 官方 API 等替代路径
//
// 新增：
//   - LoginWallSignals / detectLoginWallSignals：从 HTML/标题/content 中识别
//     登录墙 / 付费墙 / 数据服务 Landing Page，给出 error_type=login_wall
//   - LoginWallAlternativeHints：基于页面指纹给出替代路径建议
//     （官方 RSS / m.xxx.com 移动版 / 付费 API 申请）
//   - enrichFailureResponseWithLoginWall：失败响应中追加 login_wall_signals /
//     login_wall_alternative_hints / login_wall_warning 三个字段，
//     让 Agent 立即识别登录墙并切换数据源

// LoginWallSignals 登录墙 / 付费 Landing Page 检测信号集合
type LoginWallSignals struct {
	Detected     bool     // 是否检出登录墙
	MatchedRules []string // 命中的规则列表（人类可读）
	WallType     string   // 登录墙类型：login_wall / paywall / data_service_landing / unknown
}

// loginWallKeywordRules 登录墙 / 付费墙关键字识别规则
// 每条规则：关键字列表（全小写）+ 规则名 + 推断 wallType
// 设计原则：
//   - 至少 2 个不同位置的命中（title/html/content）才算命中，避免误判
//   - 优先识别「已登录用户专属内容」类关键词
//   - 商业化转型站（如机器之心 2026 改版）需明确归类为 data_service_landing，
//     这样 Agent 能直接知道"该站改付费了，爬虫无效"
type loginWallKeywordRule struct {
	Keywords []string // 至少 1 个命中
	RuleName string
	WallType string
}

var loginWallKeywordRules = []loginWallKeywordRule{
	{
		// 登录墙：要求用户登录才能查看完整内容
		Keywords: []string{"请先登录", "登录后查看", "登录后阅读", "请登录", "登录解锁", "登录订阅", "解锁全部", "查看全部内容"},
		RuleName: "login_required_text",
		WallType: "login_wall",
	},
	{
		// 付费墙 / 订阅墙
		Keywords: []string{"订阅会员", "付费会员", "立即订阅", "pro会员", "pro 会员", "成为会员", "升级会员", "付费解锁", "成为 pro", "解锁全文"},
		RuleName: "paywall_prompt_text",
		WallType: "paywall",
	},
	{
		// 数据服务 Landing Page（机器之心 2026 改版特征）
		Keywords: []string{"数据服务", "rss/mcp", "mcp 数据", "ai skills", "数据引擎", "申请内测", "解锁 30", "解锁20", "申请数据", "商务合作"},
		RuleName: "data_service_landing_text",
		WallType: "data_service_landing",
	},
	{
		// 移动端 / 旧版兜底（出现"前往了解 m.xxx.com"或"前往了解 xxx.com/rss"时）
		Keywords: []string{"m.jiqizhixin.com", "前往了解", "前往申请", "前往 rss", "前往 m.", "前往api", "前往 api"},
		RuleName: "mobile_alternative_hint",
		WallType: "data_service_landing",
	},
}

// detectLoginWallSignals 检测页面是否为登录墙 / 付费墙 / 数据服务 Landing Page
// 返回结构化信号集合；Detected=false 表示未检出
func detectLoginWallSignals(result *SpiderWebDataResponse, url string) LoginWallSignals {
	if result == nil {
		return LoginWallSignals{}
	}
	htmlLower := strings.ToLower(result.RawHTML)
	titleLower := strings.ToLower(result.Title)
	contentLower := strings.ToLower(result.Content)

	// 至少 2 个不同位置的命中才算确认；标题单独命中通常已足够（"数据服务 Landing Page"
	// 这类标题本身就有强语义），故标题命中按 2 个位置权重计算。
	score := 0
	matched := make([]string, 0, 4)
	var bestType = "unknown"

	for _, rule := range loginWallKeywordRules {
		hits := 0
		hitLocations := []string{}
		for _, kw := range rule.Keywords {
			kwLower := strings.ToLower(kw)
			if titleLower != "" && strings.Contains(titleLower, kwLower) {
				hits += 2 // 标题权重翻倍
				hitLocations = append(hitLocations, "title")
			}
			if htmlLower != "" && strings.Contains(htmlLower, kwLower) {
				hits++
				hitLocations = append(hitLocations, "html")
			}
			if contentLower != "" && strings.Contains(contentLower, kwLower) {
				hits++
				hitLocations = append(hitLocations, "content")
			}
		}
		// 至少 2 个命中位置（标题权重翻倍后计入）或至少 3 个原始命中
		if hits >= 2 {
			matched = append(matched, fmt.Sprintf("%s (hits=%d, locs=%v)", rule.RuleName, hits, dedupeStrings(hitLocations)))
			if bestType == "unknown" || priorityWallType(rule.WallType) > priorityWallType(bestType) {
				bestType = rule.WallType
			}
		}
	}

	// 强信号：URL 路径含 /login /signin /subscribe /paywall /pro 时直接判定
	if url != "" {
		uLower := strings.ToLower(url)
		if strings.Contains(uLower, "/login") || strings.Contains(uLower, "/signin") || strings.Contains(uLower, "/sign-in") {
			matched = append(matched, "url_path_login")
			score += 2
			if bestType == "unknown" {
				bestType = "login_wall"
			}
		}
		if strings.Contains(uLower, "/subscribe") || strings.Contains(uLower, "/paywall") || strings.Contains(uLower, "/pro") || strings.Contains(uLower, "/membership") {
			matched = append(matched, "url_path_paywall")
			score += 2
			if bestType == "unknown" {
				bestType = "paywall"
			}
		}
	}

	if len(matched) == 0 {
		return LoginWallSignals{}
	}
	return LoginWallSignals{
		Detected:     true,
		MatchedRules: matched,
		WallType:     bestType,
	}
}

// priorityWallType 给 wallType 一个优先级（用于冲突解决）
func priorityWallType(wt string) int {
	switch wt {
	case "login_wall":
		return 3
	case "paywall":
		return 2
	case "data_service_landing":
		return 1
	default:
		return 0
	}
}

// dedupeStrings 简单去重 helper
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// LoginWallAlternativeHints 给出登录墙场景下的替代路径建议
// 基于 URL host 派发（机器之心 / 36kr / 虎嗅 / 量子位 等已知站点）
// 未命中已知站点的返回通用 RSS / API / 移动端建议
func LoginWallAlternativeHints(url string) []string {
	host := extractHost(url)
	if host == "" {
		return []string{
			"尝试切换到目标站点的官方 RSS feed",
			"尝试移动端 UA + m.xxx.com 子域（部分站点移动端无登录墙）",
			"通过 /GetSpiderDataSource 切换到其他数据源",
			"用户提供登录 cookie 后通过 cookies action 注入",
		}
	}
	hostLower := strings.ToLower(host)

	// 已知站点专项建议（机器之心 2026 改版报告驱动）
	switch {
	case strings.Contains(hostLower, "jiqizhixin.com"):
		return []string{
			"机器之心 2026 年完成商业化改版，原 /articles 已转为付费数据服务 Landing Page",
			"官方 RSS: https://www.jiqizhixin.com/rss（页面上有「前往了解」入口）",
			// v2.0.25: 显式指出下次请求可以直接走 RSS 快速路径，不用浏览器重试
			"如要跳过浏览器直接尝试 RSS，下次请求带 fallback_strategy=\"rss_first\"（Go 标准 net/http 不依赖浏览器进程，绕开 captcha / login_wall）",
			"付费数据服务内测申请（页面提到「RSS/MCP/AI Skills 驱动的数据引擎」）",
			"迁移到其他开放 AI 媒体：量子位 (qbitai.com) / 新智元 (ai-topics.com) / PaperWeekly",
			"用户提供订阅 cookie 后通过 action={type:cookies, op:import} 一次性导入多条 cookie 解锁全文",
			"移动端备选 URL: https://m.jiqizhixin.com/articles（部分商业化改版未覆盖移动端）",
		}
	case strings.Contains(hostLower, "36kr.com"):
		return []string{
			"36 氪部分内容需登录，尝试 m.36kr.com 移动端",
			"官方 RSS: https://36kr.com/feed",
			"通过 /GetSpiderDataSource 切换数据源",
		}
	case strings.Contains(hostLower, "huxiu.com"):
		return []string{
			"虎嗅部分内容需登录，尝试 m.huxiu.com 移动端",
			"官方 RSS: https://www.huxiu.com/rss/",
			"通过 /GetSpiderDataSource 切换数据源",
		}
	case strings.Contains(hostLower, "qbitai.com"):
		return []string{
			"量子位 RSS: https://www.qbitai.com/feed",
			"如登录墙命中，尝试移动端 UA",
		}
	}

	// 通用兜底建议
	return []string{
		fmt.Sprintf("尝试切换到 %s 的官方 RSS feed（路径多为 /rss 或 /feed）", host),
		fmt.Sprintf("尝试移动端 UA + m.%s 子域（部分站点移动端无登录墙）", host),
		"通过 /GetSpiderDataSource 切换到其他数据源",
		"用户提供登录 cookie 后通过 cookies action 注入",
	}
}

// ==================== v2.0.30：SPA 路由替代路径 hints ====================
//
// 基于 Agent5_InfoQ_报告_20260705：InfoQ 中文站 (data_source_id=5) 是
// Nuxt + Vue Router SPA，导航 tab 无 href、`a.click()` 不会触发 Vue Router
// pushState。Agent 自行用 direct URL 访问 `/topic/AI`、`/topic/architecture`
// 等 topic URL 绕过。本函数为类似 SPA 站点（Nuxt / Next / Vue Router /
// React Router）提供已验证可直链的 topic URL 表 + 通用 fallback 建议。
//
// 与 `LoginWallAlternativeHints` 不同：
//   - `LoginWallAlternativeHints` 仅在登录墙命中时调用（`enrichFailureResponseWithLoginWall`）
//   - `SPAAlternativeHints` 永远调用（`enrichFailureResponseWithSPA`），让 Agent
//     一开始就知道当前 URL 是否 SPA、需要 direct URL navigate
//
// 返回值始终为切片（非 nil），避免 Agent 端 nil 判断；空 host 时返回通用兜底。

// SPAAlternativeHints 给出 SPA 路由 click 无效场景下的替代路径建议
// 基于 URL host 派发（已知站点专项：infoq.cn / 36kr.com / 虎嗅 / 量子位）
// 未命中已知站点返回通用 SPA 路由建议（direct URL navigate 替代 click）
func SPAAlternativeHints(url string) []string {
	host := extractHost(url)
	if host == "" {
		return []string{
			"目标站点可能是 SPA（Nuxt / Next / Vue Router / React Router），导航 tab 无 href 时 click 无效",
			"改用 absolute URL 直接 navigate 替代 click（参 action type=navigate 文档）",
			"若需逐项展开导航项，先 navigate 到 /robots.txt 或 sitemap 找到目标 topic URL",
			"如 SPA 路由 click 失败后页面无变化，尝试通过 eval 调用 window.history.pushState 或 router.push() 强制跳转",
		}
	}
	hostLower := strings.ToLower(host)

	// 已知站点专项建议（InfoQ 中文站 2026-07 报告驱动）
	switch {
	case strings.Contains(hostLower, "infoq.cn"):
		return []string{
			"InfoQ 中文站是 Nuxt + Vue Router SPA，导航 tab 无 href、a.click() 不会触发 Vue Router pushState",
			"已验证可直链的 topic URL（直接 navigate 替代 click 导航）：",
			"  - https://www.infoq.cn/topic/AI            （AI & 大模型）",
			"  - https://www.infoq.cn/topic/architecture （架构）",
			"  - https://www.infoq.cn/topic/BigData      （大数据）",
			"  - https://www.infoq.cn/topic/cloud-computing （云计算）",
			"  - https://www.infoq.cn/aibriefs           （AI 快讯，36h 内最新文章集中）",
			"  - https://www.infoq.cn/                   （首页，今日文章列表）",
			"备选：通过 eval 调用 window.history.pushState 或 Vue Router 的 router.push('/topic/AI')",
		}
	case strings.Contains(hostLower, "36kr.com"):
		return []string{
			"36 氪是 Vue SPA，导航 tab 点击可能不触发路由跳转",
			"已验证可直链的频道 URL：https://36kr.com/information/AI、https://36kr.com/information/technology",
			"备选：通过 eval 调用 router.push('/information/AI')",
		}
	case strings.Contains(hostLower, "huxiu.com"):
		return []string{
			"虎嗅部分页面是 SPA，导航 tab 点击可能不触发路由跳转",
			"改用 absolute URL 直接 navigate 替代 click",
			"备选：通过 eval 调用 history.pushState 强制跳转",
		}
	}

	// 通用兜底建议（针对未知 host 的 SPA）
	return []string{
		fmt.Sprintf("目标站点 %s 可能是 SPA（Nuxt / Next / Vue Router / React Router），导航 tab 无 href 时 click 无效", host),
		"改用 absolute URL 直接 navigate 替代 click（参 action type=navigate 文档）",
		"通过 eval 调用 window.history.pushState 或框架 router.push() 强制跳转目标路径",
		"若需展开导航项，先 navigate 到 /robots.txt 或 sitemap.xml 找到目标 topic URL",
	}
}

// enrichFailureResponseWithSPA 在响应中追加 SPA 路由相关 hints 字段
// 与 enrichFailureResponseWithLoginWall 不同：本函数**永远调用**，不依赖
// login_wall / paywall / anti_bot 任何信号判断 —— SPA 路由问题是结构性
// 问题，Agent 一开始就需要知道，避免首次 click 后才发现。
//
// 输出字段（始终填充，空 host 也返回通用兜底）：
//   - spa_alternative_hints: 已验证直链 URL / direct URL navigate 建议
//   - 仅当 hints 非空时附加（已知空 host 也会给通用兜底，但不为 nil）
func enrichFailureResponseWithSPA(respData map[string]interface{}, url string) {
	if respData == nil {
		return
	}
	hints := SPAAlternativeHints(url)
	if len(hints) == 0 {
		return
	}
	respData["spa_alternative_hints"] = hints
}
func extractHost(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// 去掉协议
	lower := strings.ToLower(rawURL)
	if idx := strings.Index(lower, "://"); idx >= 0 {
		lower = lower[idx+3:]
	}
	// 取 host 部分（首个 / 之前）
	if idx := strings.Index(lower, "/"); idx >= 0 {
		lower = lower[:idx]
	}
	// 去掉端口
	if idx := strings.Index(lower, ":"); idx >= 0 {
		lower = lower[:idx]
	}
	return lower
}

// buildLoginWallHint 根据登录墙类型生成人类可读 hint
func buildLoginWallHint(wallType string, signals []string) string {
	switch wallType {
	case "login_wall":
		return fmt.Sprintf("目标站点命中登录墙（%v）。建议：用户提供登录 cookie 后通过 cookies action 注入；或切换到 RSS feed / 移动端版本 / 替代数据源。", signals)
	case "paywall":
		return fmt.Sprintf("目标站点命中付费墙（%v）。建议：用户提供订阅 cookie 后注入；或尝试官方 RSS feed；或迁移到免费替代媒体。", signals)
	case "data_service_landing":
		return fmt.Sprintf("目标站点已转为付费数据服务 Landing Page（%v）。爬虫对商业化站点无效，建议：申请官方 API 内测 / 切换到官方 RSS / 迁移到其他开放 AI 媒体。", signals)
	default:
		return fmt.Sprintf("目标站点可能存在登录墙/付费墙（%v）。建议：尝试 RSS / 移动端 / 替代数据源。", signals)
	}
}

// enrichFailureResponseWithLoginWall 在失败响应中追加登录墙相关字段
// 调用方：buildFailureDataTopLevelFields 末尾（仅 anti_bot/captcha/login_wall 失败时调用）
// 输出字段（全部 omitempty，命中才填充）：
//   - login_wall_signals: 命中的规则列表
//   - login_wall_alternative_hints: 替代路径建议
//   - login_wall_warning: 人类可读警告（warnings 数组追加一项）
//   - login_wall_hint: 一行可读提示（顶层 key，类似 anti_bot_hint）
func enrichFailureResponseWithLoginWall(respData map[string]interface{}, r *SpiderWebDataResponse, url string) {
	if respData == nil || r == nil {
		return
	}
	lws := detectLoginWallSignals(r, url)
	if !lws.Detected {
		return
	}
	respData["login_wall_signals"] = lws.MatchedRules
	respData["login_wall_alternative_hints"] = LoginWallAlternativeHints(url)
	respData["login_wall_hint"] = buildLoginWallHint(lws.WallType, lws.MatchedRules)

	// warnings 数组追加（Agent 自检 checklist 友好）
	warning := buildLoginWallHint(lws.WallType, lws.MatchedRules)
	if existing, ok := respData["warnings"].([]string); ok {
		respData["warnings"] = append(existing, warning)
	} else if existing2, ok := respData["warnings"].([]interface{}); ok {
		respData["warnings"] = append(existing2, warning)
	} else {
		respData["warnings"] = []string{warning}
	}
}

// ==================== v2.0.20：RSS / Atom Feed 自动 fallback 辅助函数 ====================
//
// 这些函数由 MCPSpiderWebDataHandler 在浏览器 retry 循环 + mobile fallback
// 全部耗尽后调用；如果 RSS 抓取成功，handler 把 RSS items 转成等价的
// SpiderWebDataResponse 直接返回 success=true，让 Agent 拿到文章列表。
//
// 设计原则：
//   - 把 RSS 抓取做成"独立模块 + 边界函数"组合：spider_rss_fallback.go 提供
//     LookupRSSFallbackSources / FetchRSSTries / rssFetchResultToElements；
//     本文件只编排"何时调用 + 如何转 response"。
//   - 调用前后 try/catch panic recovery，避免一处 feed 解析问题把 handler 拉死。
//   - 行为约定：
//       * shouldTryRSSFallback：判断 errType + strategy 是否启用 fallback
//       * tryRSSFallbackForFailure：从 lastResult 构建 fallback response（带原 crawlResult）
//       * tryRSSFallbackForURL：rss_first 模式快速路径

// shouldTryRSSFallback 判断是否启用 RSS fallback
// 触发条件（v2.0.21 加强）：
//   - strategy == "rss_first" → 总是返回 true（rss_first 路径里已经走通用 fallback 之前的判断）
//   - strategy == "auto" → errType ∈ {anti_bot, captcha, region_block, timeout, timeout_hard, login_wall, paywall}
//     或 resCrawlResult 命中 login_wall / paywall / data_service_landing
//
// 注意：rss_first 模式下的快速路径在 handler 入口处已处理，本函数主要服务于
// auto 模式（浏览器 retry 失败后兜底）。
//
// v2.0.21 改动（基于问题分析报告_20260630_220512 §3.3）：把 errType=captcha
// 明确提升为"必须尝试 RSS 兜底"——之前 captcha 已在内但本函数返回 false
// 时整个分支会被外层 if 跳过；现在即使 lastResult 为空（err 早返回时），
// 只要 errType 命中也会触发 fallback（让 RSS 还能在浏览器完全失败时接管）。
//
// v2.0.22 改动（基于问题分析报告_20260701_060527 §3.1-§3.2 modelscope.cn
// 案例）：errType=dns_unresolved 也加入 fallback 触发列表。原因：CDP
// 浏览器沙箱 DNS 不可达时，继续 retry 浏览器毫无意义（go 标准 net/http
// 与浏览器沙箱用的是不同的 DNS 解析栈），但 RSS fallback 用的是 Go 标准
// net/http.Client，不依赖浏览器进程，所以能把内容带回。Agent 看到
// errType=dns_unresolved + warning 含 "primary browser crawl failed
// (errType=dns_unresolved); RSS fallback returned N items" 即可确认
// 浏览器环境有问题、不用继续 retry。
func shouldTryRSSFallback(errType string, resCrawlResult *SpiderWebDataResponse, strategy string) bool {
	if strategy == "none" {
		return false
	}
	if strategy == "rss_first" {
		return true
	}
	// auto 模式：errType 维度
	switch errType {
	case "anti_bot", "captcha", "region_block", "timeout", "timeout_hard", "login_wall", "paywall", "session_invalid", "dns_unresolved":
		return true
	}
	// 即便 errType 不在以上列表，但如果检测到 login_wall signals，仍启用 fallback
	if resCrawlResult != nil {
		lws := detectLoginWallSignals(resCrawlResult, resCrawlResult.URL)
		if lws.Detected {
			return true
		}
	}
	return false
}

// tryRSSFallbackForFailure 失败结果基础上构造 RSS fallback response
// 返回 (response, used)：
//   - response: 转换后的 SpiderWebDataResponse（含 RSS items 当作 elements）；
//     当 RSS fetch 失败或 fetch 返回 0 item 时返回 nil
//   - used: true 时表示成功构造；调用方据此判断是否 early-return
func tryRSSFallbackForFailure(rawURL string, lastCrawlResult *SpiderWebDataResponse, req SpiderWebDataRequest, errType string) (*SpiderWebDataResponse, bool) {
	if rawURL == "" {
		// 兜底用 req.URL
		rawURL = req.URL
	}
	if rawURL == "" {
		return nil, false
	}
	sources, err := LookupRSSFallbackSources(rawURL)
	if err != nil {
		mcpLogMCP("[SPIDER] RSS fallback: failed to lookup sources for %s: %v", rawURL, err)
		return nil, false
	}
	rssCtx, cancel := context.WithTimeout(context.Background(), getSpiderRSSFetchTimeout())
	defer cancel()
	rssRes := FetchRSSTries(rssCtx, sources, nil, 50)
	if !rssRes.Success || len(rssRes.Items) == 0 {
		mcpLogMCP("[SPIDER] RSS fallback: no items for %s (tried=%v, errorType=%s, errorMsg=%s)",
			rawURL, rssRes.TriedURLs, rssRes.ErrorType, rssRes.ErrorMsg)
		return nil, false
	}
	// 转换到 SpiderWebDataResponse
	resp := rssToSpiderResponse(rssRes, rawURL)
	// 继承 lastCrawlResult 的 session_id（如有），让 Agent 能继续 session 复用
	if lastCrawlResult != nil && lastCrawlResult.SessionID != "" {
		resp.SessionID = lastCrawlResult.SessionID
	}
	if req.SessionID != "" {
		resp.SessionID = req.SessionID
	}
	if req.DataSourceID > 0 {
		resp.DataSourceID = req.DataSourceID
	}
	// 透传 anti-bot hint / login_wall hints，便于 Agent 知道 fallback 来源
	if lastCrawlResult != nil {
		resp.Warnings = append(resp.Warnings,
			fmt.Sprintf("primary browser crawl failed (errType=%s); RSS fallback returned %d items from %s",
				errType, resp.RSSItemCount, resp.RSSSourceURL),
		)
	}
	// v2.0.25：即使 RSS 成功也追加 login_wall 提示，让 Agent 知道该站历史付费墙特征
	enrichRSSFallbackResponseWithLoginWall(resp, rawURL)
	return resp, true
}

// tryRSSFallbackForURL rss_first 快速路径：从 URL 直接 fetch RSS
// 与 tryRSSFallbackForFailure 区别：不需要 lastResult 直接从 URL 解析
//   - maxItems=0 时使用 50 上限
func tryRSSFallbackForURL(rawURL string, strategy string, maxItems int) (*SpiderWebDataResponse, bool) {
	if rawURL == "" {
		return nil, false
	}
	if maxItems <= 0 {
		maxItems = 50
	}
	sources, err := LookupRSSFallbackSources(rawURL)
	if err != nil {
		mcpLogMCP("[SPIDER] rss_first lookup failed for %s: %v", rawURL, err)
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	rssRes := FetchRSSTries(ctx, sources, nil, maxItems)
	if !rssRes.Success || len(rssRes.Items) == 0 {
		mcpLogMCP("[SPIDER] rss_first yielded no items for %s (tried=%v, errType=%s)",
			rawURL, rssRes.TriedURLs, rssRes.ErrorType)
		return nil, false
	}
	resp := rssToSpiderResponse(rssRes, rawURL)
	resp.Warnings = append(resp.Warnings,
		fmt.Sprintf("strategy=%s: RSS fast path returned %d items from %s (browser path skipped)",
			strategy, resp.RSSItemCount, resp.RSSSourceURL),
	)
	// v2.0.25：rss_first 快速路径同样追加 login_wall 付费墙提示
	enrichRSSFallbackResponseWithLoginWall(resp, rawURL)
	return resp, true
}

// tryHTMLArticleURLFallback 终极兜底：当 RSS / 第三方聚合 / 反爬识别全部失败时，
// 从 lastResult.RawHTML（captcha 替换 body 后仍可能含 SSR 链接）中提取
// "形似文章 URL"的链接，转成等价 RSSItem 响应。
//
// 触发条件：
//   - RSS / 第三方聚合全部失败
//   - lastResult 不为 nil 且 RawHTML 非空
//   - 至少能提取到 1 个 article 候选 URL
//
// 返回 (response, used)：
//   - response: 含 elements.articles + elements.headings + elements.links 的最小响应
//   - used: true 时表示成功构造；调用方据此判断是否替换主失败响应
//
// v2.0.21 设计动机：报告 §3.3 指出 partial_result 缺 URL 是关键缺口，
// 即便站点完全登录墙，原始 HTML 中 SSR 渲染的导航 / 推荐 / 底部链接
// 仍能给 Agent 指出"该站还有这些文章存在"——比零数据强。
func tryHTMLArticleURLFallback(lastCrawlResult *SpiderWebDataResponse, errType string) (*SpiderWebDataResponse, bool) {
	if lastCrawlResult == nil || strings.TrimSpace(lastCrawlResult.RawHTML) == "" {
		return nil, false
	}
	baseURL := lastCrawlResult.URL
	if baseURL == "" {
		return nil, false
	}
	items := ExtractArticleURLsFromHTML(lastCrawlResult.RawHTML, baseURL)
	if len(items) == 0 {
		mcpLogMCP("[SPIDER] HTML article URL fallback yielded 0 items for %s", baseURL)
		return nil, false
	}
	// 复用 RSSFetchResult 数据结构以走 rssToSpiderResponse 统一逻辑
	rssRes := RSSFetchResult{
		Success:   true,
		Items:     items,
		SourceURL: baseURL + " (html-fallback)",
		FetchedAt: time.Now().UTC(),
	}
	resp := rssToSpiderResponse(rssRes, baseURL)
	resp.Warnings = append(resp.Warnings,
		fmt.Sprintf("primary browser crawl failed (errType=%s); HTML article URL fallback returned %d items (RSS/aggregator all failed)",
			errType, resp.RSSItemCount),
	)
	// 标记来源是 HTML fallback 而非 RSS
	resp.RSSSourceURL = "(html-extract) " + baseURL
	mcpLogMCP("[SPIDER] HTML article URL fallback returned %d items (errType=%s, url=%s)",
		len(items), errType, baseURL)
	// v2.0.25：HTML 兜底同样追加 login_wall 付费墙提示
	enrichRSSFallbackResponseWithLoginWall(resp, baseURL)
	return resp, true
}

// 字段映射：
//   - URL / Title / Content: 用 RSS feed 的 host / first item title / 汇总
//   - Elements.Links / Headings / Articles: 每条 RSS item 一条
//   - RSSFallbackUsed / RSSSourceURL / RSSItemCount / RSSTriedURLs: 元数据
//   - HasMore = true（Agent 可继续调 /SpiderWebData 抓具体文章）
//   - CrawlTime: 当前 UTC
//   - Warnings: 一条提示（由调用方追加）
func rssToSpiderResponse(rssRes RSSFetchResult, rawURL string) *SpiderWebDataResponse {
	links, headings, articles := rssFetchResultToElements(&rssRes)
	host := extractHost(rawURL)

	// title 取第一条 article 标题
	title := ""
	if len(articles) > 0 {
		title = articles[0].Title
	}
	if title == "" && host != "" {
		title = host + " RSS feed"
	}
	// content 用 RSS items 拼成有序列表（最多 50 条），便于 Agent 不走 elements 也能拿
	var contentParts []string
	for i, it := range rssRes.Items {
		if i >= 50 {
			break
		}
		if it.Title == "" {
			continue
		}
		line := fmt.Sprintf("- %s", strings.TrimSpace(it.Title))
		if it.URL != "" {
			line += " (" + it.URL + ")"
		}
		if it.Summary != "" {
			line += "\n  " + truncateRunes(it.Summary, 200)
		}
		contentParts = append(contentParts, line)
	}
	content := strings.Join(contentParts, "\n")
	if content == "" {
		content = fmt.Sprintf("RSS feed (host=%s) returned %d items", host, len(rssRes.Items))
	}

	elements := &WebElements{
		Links:      links,
		Headings:   headings,
		Paragraphs: []WebElementParagraph{},
		Articles:   articles,
	}

	resp := &SpiderWebDataResponse{
		URL:             rawURL,
		Title:           title,
		Content:         content,
		RawHTML:         "",
		CrawlTime:       time.Now().UTC(),
		DataSourceID:    0,
		HasMore:         true,
		Elements:        elements,
		RSSFallbackUsed: true,
		RSSSourceURL:    rssRes.SourceURL,
		RSSItemCount:    len(rssRes.Items),
		RSSTriedURLs:    rssRes.TriedURLs,
		Warnings:        []string{},
	}
	return resp
}
