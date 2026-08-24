package spider

import (
	"context"
	"errors"
	"fmt"
	config "github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// waitForScriptResources 在 navigate 后等待页面至少加载若干 script 资源。
// v2.0.18: 针对 chat.baidu.com 等 Vite ESM 入口的 SPA，headless Chrome 在
// document.readyState=complete 时可能尚未开始 fetch 外部模块，导致 hydration
// 永远探测不到框架信号。本函数在 waitForNetworkIdle 之后额外等待：
//   - 若页面包含 <script type="module"> 或 <script src="...">，则等待至少一个
//     script 资源被 performance.getEntriesByType('resource') 记录到
//   - 最多等 maxWait（默认 5s），超时降级继续（不阻塞）
//   - 纯静态页面（无外部 script）立即返回
//
// 返回 (scriptLoaded bool, err error)：scriptLoaded=true 表示探测到至少一个 script
// 资源已加载；false 表示超时或页面无 script。
func waitForScriptResources(ctx context.Context, maxWait time.Duration) (bool, error) {
	if maxWait <= 0 {
		maxWait = 5 * time.Second
	}
	deadline := time.Now().Add(maxWait)

	// 先检查页面是否有外部 script（避免纯静态页无限等待）
	var hasScripts bool
	jsCheck := `(function(){
		const scripts = document.querySelectorAll('script[src], script[type="module"]');
		return scripts.length > 0;
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(jsCheck, &hasScripts)); err != nil {
		// 检查失败不阻塞，降级继续
		return false, nil
	}
	if !hasScripts {
		return true, nil // 无外部 script，无需等待
	}

	// 轮询 performance entries，等待至少一个 initiatorType=script 的资源
	jsCount := `(function(){
		if (typeof performance === 'undefined' || !performance.getEntriesByType) return 0;
		const entries = performance.getEntriesByType('resource') || [];
		let count = 0;
		for (let i = 0; i < entries.length; i++) {
			const it = entries[i].initiatorType;
			if (it === 'script' || it === 'link') count++;
		}
		return count;
	})()`

	for time.Now().Before(deadline) {
		var count int
		if err := chromedp.Run(ctx, chromedp.Evaluate(jsCount, &count)); err != nil {
			return false, nil // 降级继续
		}
		if count > 0 {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	// 超时：页面有 script 但没有任何 resource entry 被记录到
	return false, nil
}

// waitForNetworkIdle 等待网络空闲（无新请求持续 idleDuration 或总超时 maxWait）
// 替代固定 sleep，确保 SPA / lazy load / 异步数据加载完成后再抓取 HTML
func waitForNetworkIdle(ctx context.Context, idleDuration time.Duration, maxWait time.Duration) error {
	// 启用 network 事件监听
	if err := chromedp.Run(ctx, network.Enable()); err != nil {
		return err
	}

	reqCount := 0
	var lastReqTime time.Time
	var mu sync.Mutex

	// 监听请求开始和结束
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev.(type) {
		case *network.EventRequestWillBeSent:
			mu.Lock()
			reqCount++
			lastReqTime = time.Now()
			mu.Unlock()
		case *network.EventLoadingFinished, *network.EventLoadingFailed:
			mu.Lock()
			reqCount--
			if reqCount < 0 {
				reqCount = 0
			}
			mu.Unlock()
		}
	})

	start := time.Now()
	for {
		mu.Lock()
		idle := reqCount == 0 && !lastReqTime.IsZero() && time.Since(lastReqTime) >= idleDuration
		mu.Unlock()

		if idle {
			return nil
		}
		if time.Since(start) >= maxWait {
			// 超时但继续执行（可能页面仍有后台请求，但已等足够久）
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// buildStealthScriptBase 构造基础 stealth JS（v2.0.7 同字节级）
// v2.0.8: 从 const 改为函数，与 spider_anti_bot.go 的 BuildStealthScript 协作
func buildStealthScriptBase() string {
	return `(function() {
	// 覆盖 navigator.webdriver
	Object.defineProperty(navigator, 'webdriver', {
		get: () => undefined,
	});
	// 覆盖 plugins
	Object.defineProperty(navigator, 'plugins', {
		get: () => [1, 2, 3, 4, 5],
	});
	// 覆盖 languages
	Object.defineProperty(navigator, 'languages', {
		get: () => ['zh-CN', 'en-US', 'en'],
	});
	// 覆盖 chrome 对象
	window.chrome = {
		runtime: {},
		app: {},
		csi: function() {},
		loadTimes: function() {},
	};
	// 移除 automation 相关属性
	delete navigator.__proto__.webdriver;
	// 覆盖 notification permissions
	const originalQuery = window.Notification.requestPermission;
	window.Notification.requestPermission = function(cb) {
		if (cb) cb('default');
		return Promise.resolve('default');
	};
	// 覆盖 permission query
	const originalQuery2 = navigator.permissions.query;
	navigator.permissions.query = function(parameters) {
		if (parameters.name === 'notifications') {
			return Promise.resolve({ state: 'default', onchange: null });
		}
		return originalQuery2.apply(this, arguments);
	};
	// 覆盖 iframe contentWindow
	const originalCreateElement = document.createElement;
	document.createElement = function(tagName) {
		const element = originalCreateElement.apply(this, arguments);
		if (tagName === 'iframe') {
			try {
				Object.defineProperty(element, 'contentWindow', {
					get: function() {
						return window;
					}
				});
			} catch(e) {}
		}
		return element;
	};
	// 覆盖 canvas fingerprint (简单随机噪声)
	const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
	HTMLCanvasElement.prototype.toDataURL = function(type) {
		return originalToDataURL.apply(this, arguments);
	};
})`
}

// buildSessionStealthJS 构造当前 session 应注入的 stealth JS（v2.0.9）
//   - config.G.SpiderStealthProMode=false（默认）：返回 buildStealthScriptBase()（字节级与 v2.0.8 一致）
//   - config.G.SpiderStealthProMode=true：base + Stealth Pro（MediaDevices/字体/堆栈/chrome.runtime）
//
// session 可为 nil；fp 可为 nil（Stealth Pro 不依赖具体 fingerprint）
// v2.0.19: 当 config.G.SpiderStealthScript 非空时前缀拼接，与 ApplyFingerprint 注入保持一致。
func buildSessionStealthJS(session *SpiderSession, cfg *config.LsmTokensServerConfig) string {
	base := buildStealthScriptBase()
	var result string
	if config.G != nil && config.G.SpiderStealthProMode {
		var fp *Fingerprint
		if session != nil {
			fp = session.fingerprint
		}
		pro := buildStealthProJS(fp, config.G.SpiderStealthProFonts)
		result = base + ";\n" + pro
	} else {
		result = base
	}
	// v2.0.19: 用户自定义 stealth 脚本前缀（与 BuildStealthScript 行为一致）
	if config.G != nil && config.G.SpiderStealthScript != "" {
		prefix := config.G.SpiderStealthScript
		if len(prefix) > 16*1024 {
			prefix = prefix[:16*1024]
		}
		result = prefix + ";\n" + result
	}
	return result
}

// injectBlockURLPatterns 注入 Network.SetBlockedURLS（v2.0.9 维度三）
// config.G 未启用时 no-op
func injectBlockURLPatterns(ctx context.Context, cfg *config.LsmTokensServerConfig) {
	if config.G == nil || !config.G.SpiderBlockResourcesEnabled {
		return
	}
	patterns := ResolveBlockURLPatterns(config.G)
	if len(patterns) == 0 {
		return
	}
	if err := chromedp.Run(ctx, network.Enable()); err != nil {
		logger.Printf("[SPIDER] network.Enable failed: %v", err)
		return
	}
	if err := chromedp.Run(ctx, network.SetBlockedURLS(patterns)); err != nil {
		logger.Printf("[SPIDER] network.SetBlockedURLs failed: %v", err)
	}
}

// ==================== CDP 抓取引擎 ====================
// v2.0.0 重构：所有 fetch 走 chromedp，不再使用 net/http。
// HTTP 模式已被彻底移除。

// crawlWebDataCDP 通过 Chrome DevTools Protocol 抓取单个 URL
// 等价于 v1.5.0 的 crawlWebDataSimpleWithOptions，但走真实浏览器渲染。
//
// 参数：
//   - url: 目标 URL（必填）
//   - timeoutSeconds: 单次请求超时（0=默认 30s，会被 clamp 到 5-120s）
//   - maxContentLen: 抓取内容最大长度（0=使用 spider 默认）
//
// 返回 SpiderWebDataResponse，字段含义与 v1.5.0 完全一致。
//
// 注意：本函数不消耗 engine.sem —— 复用 rootCtx 的 default target，不开新 tab。
// 只有 attachCDPContext（per-session 独立 tab）才需要 sem 控制 tab 数量。
//
// v2.0.8: session 可为 nil；非 nil 时构建 per-session fingerprint + jitter
func (e *SpiderEngine) crawlWebDataCDP(session *SpiderSession, dsID uint64, url string, timeoutSeconds float64, maxContentLen int) (*SpiderWebDataResponse, error) {
	if err := e.waitReady(3 * time.Second); err != nil {
		return nil, fmt.Errorf("spider engine unavailable: %w", err)
	}

	// v2.0.19: 导航前主动检查 CDP socket 健康状态。
	// 场景：旧 Chrome 进程僵死 / socket bad file descriptor / rootCtx 已被 cancel
	// 但 healthCheckLoop 尚未触发重启。此时 chromedp.Run 会返回 context canceled，
	// Agent 拿不到有用的错误信息。提前检测 + 自动重启，避免白白消耗一次重试。
	if !e.checkChromeHealthy(2 * time.Second) {
		logger.Printf("[SPIDER] CDP socket unhealthy before navigate (%s), attempting auto-restart...", url)
		if err := e.RestartChrome(); err != nil {
			return nil, fmt.Errorf("CDP socket unhealthy and auto-restart failed: %w", err)
		}
		logger.Printf("[SPIDER] Auto-restart succeeded, proceeding with navigate to %s", url)
	}

	// 计算超时：默认 90s（海外站点需要更长时间），范围 5-180s
	timeout := 90 * time.Second
	if timeoutSeconds > 0 {
		t := time.Duration(timeoutSeconds) * time.Second
		if t < 5*time.Second {
			t = 5 * time.Second
		}
		if t > 180*time.Second {
			t = 180 * time.Second
		}
		timeout = t
	}

	// 创建子 context（每次请求独立，但 tab 复用 rootCtx 同 target）
	ctx, ctxCancel := context.WithTimeout(e.rootCtx, timeout)
	defer ctxCancel()
	cdpCtx, cancel := chromedp.NewContext(ctx)
	defer func() {
		// v2.0.2: 显式调用 chromedp.Cancel 彻底清理 WebSocket 资源
		_ = chromedp.Cancel(cdpCtx)
		cancel()
	}()

	var html, title string
	err := chromedp.Run(cdpCtx,
		// v2.0.8: pre-nav 准备（per-session fingerprint / jitter；无 session 时 no-op）
		chromedp.ActionFunc(func(ctx context.Context) error {
			return prepareSessionNavigation(ctx, session, dsID, e)
		}),
		chromedp.Navigate(url),
		// v2.0.9: 注入 stealth 脚本（base + 可选 Stealth Pro）
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Run(ctx, chromedp.Evaluate(buildSessionStealthJS(session, config.G), nil))
		}),
		// v2.0.9: 注入 Network.SetBlockedURLS 资源屏蔽（条件门控）
		chromedp.ActionFunc(func(ctx context.Context) error {
			injectBlockURLPatterns(ctx, config.G)
			return nil
		}),
		// 等待网络空闲，替代固定 sleep，确保 SPA / lazy load 完成
		chromedp.ActionFunc(func(ctx context.Context) error {
			return waitForNetworkIdle(ctx, 2*time.Second, 15*time.Second)
		}),
		// v2.0.18: 额外等待 script 资源加载，解决 chat.baidu.com 等 Vite ESM
		// 入口在 headless Chrome 中模块 fetch 被推迟导致 hydration 永远失败的
		// 问题（见问题分析报告_20260629_093329 §3.1）。
		chromedp.ActionFunc(func(ctx context.Context) error {
			loaded, _ := waitForScriptResources(ctx, 5*time.Second)
			if !loaded {
				logger.Printf("[SPIDER] waitForScriptResources: no script resources loaded within 5s (possible ESM module fetch blocked)")
			}
			return nil
		}),
		chromedp.OuterHTML("html", &html),
		chromedp.Title(&title),
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			e.recordContextCanceledFailure()
		}
		return nil, fmt.Errorf("CDP fetch failed: %w", err)
	}

	if title == "" {
		title = extractTitle(html)
	}
	if maxContentLen <= 0 {
		maxContentLen = 10000
	}
	content := extractContentSimpleWithLimit(html, maxContentLen)
	resp := &SpiderWebDataResponse{
		URL:       url,
		Title:     title,
		Content:   content,
		RawHTML:   html,
		CrawlTime: time.Now().UTC(),
		Language:  detectLanguage(content),
		HasMore:   true,
		Elements:  extractWebElements(html, url),
	}
	// v2.0.13: 可选等待客户端 SPA 水合完成（解决 chat.baidu.com SSR 已
	// 就绪但客户端 bundle 未水合导致 fill_form/click 全部失效的场景）。
	// 默认关闭（向后兼容）；调用方在外部有 SpiderWebDataRequest 时置 WaitForHydration。
	return resp, nil
}

// crawlWebDataCDPWithCtx 带外部 ctx 的版本（被 dispatchCDPAction 复用）
// 外部 ctx 用于 session 级别的取消（TTL 到期）。同样不消耗 sem。
//
// v2.0.8: session 可为 nil；非 nil 时构建 per-session fingerprint + jitter
func (e *SpiderEngine) crawlWebDataCDPWithCtx(parentCtx context.Context, session *SpiderSession, dsID uint64, url string, timeout time.Duration, maxContentLen int) (*SpiderWebDataResponse, error) {
	if err := e.waitReady(3 * time.Second); err != nil {
		return nil, fmt.Errorf("spider engine unavailable: %w", err)
	}

	// v2.0.19: 导航前主动检查 CDP socket 健康状态（同 crawlWebDataCDP）。
	// 仅在 parentCtx 未取消时尝试自动恢复。
	if parentCtx.Err() == nil && !e.checkChromeHealthy(2*time.Second) {
		logger.Printf("[SPIDER] CDP socket unhealthy before navigate (%s), attempting auto-restart...", url)
		if err := e.RestartChrome(); err != nil {
			return nil, fmt.Errorf("CDP socket unhealthy and auto-restart failed: %w", err)
		}
		logger.Printf("[SPIDER] Auto-restart succeeded, proceeding with navigate to %s", url)
	}

	ctx, ctxCancel := context.WithTimeout(parentCtx, timeout)
	defer ctxCancel()
	cdpCtx, cancel := chromedp.NewContext(ctx)
	defer func() {
		// v2.0.2: 显式调用 chromedp.Cancel 彻底清理 WebSocket 资源
		_ = chromedp.Cancel(cdpCtx)
		cancel()
	}()

	var html, title string
	err := chromedp.Run(cdpCtx,
		// v2.0.8: pre-nav 准备（per-session fingerprint / jitter；无 session 时 no-op）
		chromedp.ActionFunc(func(ctx context.Context) error {
			return prepareSessionNavigation(ctx, session, dsID, e)
		}),
		chromedp.Navigate(url),
		// v2.0.9: 注入 stealth 脚本（base + 可选 Stealth Pro）
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Run(ctx, chromedp.Evaluate(buildSessionStealthJS(session, config.G), nil))
		}),
		// v2.0.9: 注入 Network.SetBlockedURLS 资源屏蔽（条件门控）
		chromedp.ActionFunc(func(ctx context.Context) error {
			injectBlockURLPatterns(ctx, config.G)
			return nil
		}),
		// 等待网络空闲，替代固定 sleep，确保 SPA / lazy load 完成
		chromedp.ActionFunc(func(ctx context.Context) error {
			return waitForNetworkIdle(ctx, 2*time.Second, 15*time.Second)
		}),
		// v2.0.18: 额外等待 script 资源加载，解决 chat.baidu.com 等 Vite ESM
		// 入口在 headless Chrome 中模块 fetch 被推迟导致 hydration 永远失败的
		// 问题（见问题分析报告_20260629_093329 §3.1）。
		chromedp.ActionFunc(func(ctx context.Context) error {
			loaded, _ := waitForScriptResources(ctx, 5*time.Second)
			if !loaded {
				logger.Printf("[SPIDER] waitForScriptResources: no script resources loaded within 5s (possible ESM module fetch blocked)")
			}
			return nil
		}),
		chromedp.OuterHTML("html", &html),
		chromedp.Title(&title),
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			e.recordContextCanceledFailure()
		}
		return nil, fmt.Errorf("CDP fetch failed: %w", err)
	}
	if title == "" {
		title = extractTitle(html)
	}
	if maxContentLen <= 0 {
		maxContentLen = 10000
	}
	content := extractContentSimpleWithLimit(html, maxContentLen)
	return &SpiderWebDataResponse{
		URL:       url,
		Title:     title,
		Content:   content,
		RawHTML:   html,
		CrawlTime: time.Now().UTC(),
		Language:  detectLanguage(content),
		HasMore:   true,
		Elements:  extractWebElements(html, url),
	}, nil
}
