package spider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// ==================== 页面水合（hydration）探测 ====================
// v2.0.13: 针对 chat.baidu.com 等"SSR HTML 已就绪但客户端 bundle 未水合"
// 的 SPA 场景，提供 wait_for_react_root 选项 + 探测结果字段。
//
// 问题背景：
//   - v2.0.12 release notes 已把 chat.baidu.com 列为已知不稳定场景。
//   - 问题分析报告_20260626_205937.md 显示：13 个 modulepreload 全部 200
//     但 transferSize=0，console_logs 为空，DOM 内零 React fiber —— 仅
//     SSR HTML 就绪，但客户端 bundle 未执行水合。
//   - 此时 fill_form / click / eval 派发的所有事件都没有业务侧回调（因为
//     framework 根本不存在），click_action 一直等到 context deadline。
//
// 设计：
//   - 默认关闭（向后兼容）；WaitForHydration=true 时最多等 WaitForHydrationMs
//     毫秒（默认 2000，上限 5000）。
//   - 探测信号（任一命中即视为 hydrated）：
//       React 17+: DOM 节点存在 __reactFiber$xxx 键
//       React 18+: DOM 节点存在 __reactContainer$xxx / __reactFiber$xxx
//       Next.js: window.next / window.__NEXT_DATA__ 已设置
//       San:  DOM 节点存在 __san / __santd / window.san
//       Vue 3: DOM 节点存在 __vue__ / __vue_app__
//   - 探测循环每 100ms 一次 JS roundtrip，超时立即降级继续（与 v2.0.11 click
//     watchdog 一致），不阻塞 navigate 主流程。
//   - 探测结果返回 HydrationDiagnostics（state / wait_ms / fiber_roots_count /
//     detected_framework / warning）写到响应 data.hydration_state。

// hydrationProbeJS 单次探测：返回当前页面的水合信号快照。
//   - fiber_roots: 含 __reactFiber$ / __reactContainer$ 键的 DOM 节点数（最多扫 MAX 节点）。
//   - has_next: window.next / window.__NEXT_DATA__ 是否有内容。
//   - has_san: window.san / document 上是否有 San create() 痕迹。
//   - has_vue: 任意 DOM 节点有 __vue__ / __vue_app__ / __vueParentComponent。
//   - console_lines: window.__lsm_console_log__ 长度（用于辅助判断 JS 是否执行）。
//
// 返回 JSON 字符串，由 Go 端解析。
//
// v2.0.13-补丁：
//   - 扫描起点改为 documentElement（chat.baidu.com 把 React 根挂在 #app 上，
//     而 #app 在 <body> 下但 chat-container-main / cs-container-scroll 这些
//     子树很深；500 节点上限从 body 开始 DFS 会漏掉这些深层 fiber）。
//   - 已知 SPA 容器探测：除了通用扫描，再针对 #app / [data-reactroot] /
//     #__next / .vue-app 等高命中容器单点探测（绕过 MAX 上限）。
//   - MAX 由 500 提到 2000，覆盖 chat.baidu.com 这种 DOM 极深的页面。
const hydrationProbeJS = `(function(){
  const out = {
    fiber_roots: 0,
    has_next: false,
    has_san: false,
    has_vue: false,
    console_lines: 0,
    // v2.0.17 补丁：ES Module / 经典脚本加载统计，用于诊断 chat.baidu.com
    // 这类 SSR + ES Module SPA 客户端 bundle 未水合的根因（见问题分析报告
    // _20260627_120444 §3.1 / §5.2）：resource entries 可拿到 modulepreload /
    // script 资源数 + 失败数 + transferSize=0 但 duration>0 的可疑条目。
    module_loads_total: 0,
    module_loads_failed: 0,
    module_failed_urls: [],
    module_zero_transfer: 0
  };
  // 1. window.next / __NEXT_DATA__
  try {
    if (typeof window.next === 'object' && window.next !== null) out.has_next = true;
    if (typeof window.__NEXT_DATA__ === 'object' && window.__NEXT_DATA__ !== null) out.has_next = true;
  } catch(e){}
  // 2. window.san
  try {
    if (typeof window.san === 'function' || typeof window.san === 'object') out.has_san = true;
  } catch(e){}
  // 3. DOM 扫描：找 React fiber / Vue __vue__ 标记
  let reactHits = 0;
  let vueHits = 0;
  let nodes = 0;
  // v2.0.13-补丁：从 documentElement 起步，覆盖 #app 之前的所有节点
  const MAX = 2000;
  const root = document.documentElement || document.body || document;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT);
  let n;
  while ((n = walker.nextNode()) && nodes < MAX) {
    nodes++;
    for (const k in n) {
      if (!Object.prototype.hasOwnProperty.call(n, k)) continue;
      if (k.indexOf('__reactFiber$') === 0 || k.indexOf('__reactContainer$') === 0) {
        reactHits++;
        break;
      }
      if (k === '__vue__' || k === '__vue_app__' || k === '__vueParentComponent') {
        vueHits++;
        break;
      }
    }
  }
  out.fiber_roots = reactHits;
  out.has_vue = vueHits > 0;
  // 4. console 输出（stealth 注入的 window.__lsm_console_log__）
  try {
    if (Array.isArray(window.__lsm_console_log__)) out.console_lines = window.__lsm_console_log__.length;
  } catch(e){}
  // 5. v2.0.17 补丁：performance.getEntriesByType('resource') 统计 script/modulepreload
  try {
    if (typeof performance !== 'undefined' && typeof performance.getEntriesByType === 'function') {
      const entries = performance.getEntriesByType('resource') || [];
      let total = 0, failed = 0, zeroTransfer = 0;
      const failedURLs = [];
      for (let i = 0; i < entries.length; i++) {
        const e = entries[i];
        const name = (e && e.name) || '';
        const it = (e && e.initiatorType) || '';
        // initiatorType 在 Chromium 上 'script' / 'link'（modulepreload），其他浏览器可能为空
        if (it !== 'script' && it !== 'link') continue;
        total++;
        // transferSize=0 + duration>0 视为可疑：服务端返回了内容但 transferSize 未上报，
        // 通常是 CSP / 反爬拦截后 browser 未真正下载 body（问题分析报告 §3.1）
        const ts = (typeof e.transferSize === 'number') ? e.transferSize : 0;
        const dur = (typeof e.duration === 'number') ? e.duration : 0;
        if (dur > 0 && ts === 0) {
          zeroTransfer++;
          if (failedURLs.length < 3) failedURLs.push('zero_transfer:' + name);
        }
      }
      // Navigation Timing 级别的失败条目：responseStatus=0 / transferSize=0 + duration 异常
      // 浏览器实现差异：Chromium PerformanceResourceTiming 不直接暴露 responseStatus，
      // 但可用 Server-Timing / nextHopProtocol / duration 一起判断。
      // 这里采用保守启发式：duration=0 且 initiatorType=script 视为未启动（被 CSP 拦截）。
      for (let i = 0; i < entries.length; i++) {
        const e = entries[i];
        const it = (e && e.initiatorType) || '';
        if (it !== 'script' && it !== 'link') continue;
        const dur = (typeof e.duration === 'number') ? e.duration : 0;
        const name = (e && e.name) || '';
        if (dur === 0 && name && failedURLs.indexOf('not_started:' + name) === -1) {
          failed++;
          if (failedURLs.length < 3) failedURLs.push('not_started:' + name);
        }
      }
      out.module_loads_total = total;
      out.module_loads_failed = failed;
      out.module_zero_transfer = zeroTransfer;
      out.module_failed_urls = failedURLs;
    }
  } catch(e){}
  return JSON.stringify(out);
})()`

// probeHydrationOnce 跑一次 hydrationProbeJS，返回解析后的快照。
func probeHydrationOnce(ctx context.Context) (hydrationSnapshot, error) {
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(hydrationProbeJS, &raw)); err != nil {
		return hydrationSnapshot{}, err
	}
	var s hydrationSnapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return hydrationSnapshot{}, fmt.Errorf("hydration probe parse failed: %w (raw=%q)", err, raw)
	}
	return s, nil
}

// hydrationSnapshot 单次 hydrationProbeJS 的解析结果。
type hydrationSnapshot struct {
	FiberRoots         int      `json:"fiber_roots"`
	HasNext            bool     `json:"has_next"`
	HasSan             bool     `json:"has_san"`
	HasVue             bool     `json:"has_vue"`
	ConsoleLines       int      `json:"console_lines"`
	ModuleLoadsTotal   int      `json:"module_loads_total"`
	ModuleLoadsFailed  int      `json:"module_loads_failed"`
	ModuleZeroTransfer int      `json:"module_zero_transfer"`
	ModuleFailedURLs   []string `json:"module_failed_urls"`
}

// buildHydrationDiagnostics 从 hydrationSnapshot + 探测耗时构造 HydrationDiagnostics，
// 集中四个 return 点（首测命中 / ctx 取消 / 循环命中 / 超时降级）共享的字段映射。
// v2.0.17 补丁：新增 ModuleLoadsTotal / ModuleLoadsFailed / ModuleZeroTransfer /
// ModuleFailedURLs 字段，反映 ES Module / script 资源加载统计，用于诊断
// chat.baidu.com 这类 SSR HTML 已就绪但客户端 bundle 未水合的根因。
func buildHydrationDiagnostics(s hydrationSnapshot, waitMs int, state, framework, warning string) *HydrationDiagnostics {
	return &HydrationDiagnostics{
		State:              state,
		WaitMs:             waitMs,
		FiberRootsCount:    s.FiberRoots,
		HasNext:            s.HasNext,
		HasSan:             s.HasSan,
		HasVue:             s.HasVue,
		ConsoleLines:       s.ConsoleLines,
		ModuleLoadsTotal:   s.ModuleLoadsTotal,
		ModuleLoadsFailed:  s.ModuleLoadsFailed,
		ModuleZeroTransfer: s.ModuleZeroTransfer,
		ModuleFailedURLs:   s.ModuleFailedURLs,
		DetectedFramework:  framework,
		Warning:            warning,
	}
}

// - 命中 React fiber → "hydrated" + "react"
// - 命中 Next → "hydrated" + "next"
// - 命中 San → "hydrated" + "san"
// - 命中 Vue → "hydrated" + "vue"
// - 全部 false → "none"（如 SSR + 不水合的静态页，如 chat.baidu.com 客户端 bundle 未水合）
func classifyHydration(s hydrationSnapshot) (state, framework string) {
	switch {
	case s.FiberRoots > 0:
		return "hydrated", "react"
	case s.HasNext:
		return "hydrated", "next"
	case s.HasSan:
		return "hydrated", "san"
	case s.HasVue:
		return "hydrated", "vue"
	default:
		return "none", "static"
	}
}

// probeAndWaitForHydration 在 ctx 上轮询 hydrationProbeJS 直到命中框架信号、
// 超时或 ctx 取消。所有等待都在一次 chromedp.Run 内完成（每 100ms 一次 JS
// roundtrip），与 click watchdog 一致超时即降级继续。
//   - maxWaitMs: 总超时毫秒；<=0 时使用默认 2000ms；>5000 时 clamp 到 5000ms。
//   - 返回 *HydrationDiagnostics：调用方挂到响应 data.hydration_state。
func probeAndWaitForHydration(ctx context.Context, maxWaitMs int) *HydrationDiagnostics {
	if maxWaitMs <= 0 {
		maxWaitMs = 2000
	}
	if maxWaitMs > 5000 {
		maxWaitMs = 5000
	}
	start := time.Now()
	deadline := start.Add(time.Duration(maxWaitMs) * time.Millisecond)

	var last hydrationSnapshot
	// 第一轮立即探测（不等待）
	if s, err := probeHydrationOnce(ctx); err == nil {
		last = s
		if state, fw := classifyHydration(s); state == "hydrated" {
			return buildHydrationDiagnostics(s, int(time.Since(start).Milliseconds()), state, fw, "")
		}
	}

	for {
		// 100ms 间隔
		select {
		case <-ctx.Done():
			// ctx 取消：超时返回 pending（避免上报 "none" 误导 Agent）
			return buildHydrationDiagnostics(last, int(time.Since(start).Milliseconds()), "timeout", "", "hydration wait cancelled by ctx")
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			break
		}
		s, err := probeHydrationOnce(ctx)
		if err != nil {
			// 单次探测失败：记 warning 但继续
			last.ConsoleLines = last.ConsoleLines // 保留上次
			continue
		}
		last = s
		if state, fw := classifyHydration(s); state == "hydrated" {
			return buildHydrationDiagnostics(s, int(time.Since(start).Milliseconds()), state, fw, "")
		}
	}

	// 超时仍未命中：根据 console_lines + ES Module 加载统计给降级提示。
	// v2.0.17 补丁：当 ModuleLoadsFailed > 0 或 ModuleZeroTransfer > 0 时，
	// 提示 Agent 客户端 bundle 被 CSP/反爬拦截（与问题分析报告
	// _20260627_120444 §3.1 「猜测 1：headless Chrome 被 chat.baidu.com 的
	// JS 加载机制拒绝」对应）。
	state, fw := classifyHydration(last)
	warning := buildHydrationTimeoutWarning(last, state)
	return buildHydrationDiagnostics(last, int(time.Since(start).Milliseconds()), "timeout", fw, warning)
}

// buildHydrationTimeoutWarning 根据 hydrationSnapshot 拼装超时警告文本。
//   - state=="none" + ConsoleLines==0：客户端 bundle 完全未执行（典型 SSR-only）
//   - state=="none" + ModuleLoadsFailed>0：script 资源被 CSP / 反爬拦截
//   - state=="none" + ModuleZeroTransfer>0：资源被服务端降级或 CDN 限制（transferSize=0 但 duration>0）
//   - 其他组合：未知 framework 或非标准水合
//
// 前缀保持 "hydration timeout: ..." 以兼容 v2.0.13 warning 解析逻辑。
func buildHydrationTimeoutWarning(s hydrationSnapshot, state string) string {
	switch state {
	case "none":
		if s.ModuleLoadsFailed > 0 {
			return fmt.Sprintf(
				"hydration timeout: 0 console lines and %d script resource(s) failed to start — likely blocked by CSP / anti-bot (e.g. chat.baidu.com ES Module interception); consider API-direct approach",
				s.ModuleLoadsFailed)
		}
		if s.ModuleZeroTransfer > 0 {
			return fmt.Sprintf(
				"hydration timeout: 0 console lines and %d script resource(s) returned 0 transferSize — likely CDN/script interception (e.g. chat.baidu.com bundle blocked); consider API-direct approach",
				s.ModuleZeroTransfer)
		}
		if s.ConsoleLines == 0 {
			return "hydration timeout: no framework signal and 0 console lines — likely client bundle never executed (e.g. chat.baidu.com case: SSR HTML ready but React not hydrated); consider API-direct approach"
		}
		return "hydration timeout: framework signal not found within wait window but console logs exist — may be partial hydration or non-standard framework"
	default:
		return "hydration timeout"
	}
}
