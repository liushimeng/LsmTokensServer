package spider

import (
	"context"
	"encoding/json"
	"fmt"
	models "github.com/lishimeng/LsmTokensServer/models"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// ==================== CDP 交互动作派发器 ====================
// v2.0.0 重构：12 个 action 全部用 chromedp 真正实现。
// 取代 v1.5.0 的 processInteractiveAction（4 个 stub + 4 个 fallback re-fetch）。

// extractStateJS 在 get_state 时注入：序列化链接/表单/滚动位置为 JSON
const extractStateJS = `(function() {
  const links = Array.from(document.querySelectorAll('a[href]')).slice(0, 100).map(a => ({
    text: (a.innerText || a.textContent || '').trim().slice(0, 200),
    href: a.href
  })).filter(l => l.href && !l.href.startsWith('javascript:') && l.href !== '#' && l.href !== location.href + '#');

  const forms = Array.from(document.forms).map(f => ({
    id: f.id || '',
    action: f.action || '',
    method: (f.method || 'get').toLowerCase(),
    elements: Array.from(f.elements).slice(0, 50).map(el => ({
      type: el.type || el.tagName.toLowerCase(),
      name: el.name || '',
      id: el.id || ''
    }))
  }));

  return JSON.stringify({
    url: location.href,
    title: document.title,
    links: links,
    forms: forms,
    scroll_y: window.scrollY || 0,
    scroll_x: window.scrollX || 0,
    content_type: document.contentType || ''
  });
})()`

// extractCDPPageState 通过 chromedp 抓当前页面状态（JS 评估）
func extractCDPPageState(ctx context.Context) (*PageState, error) {
	var jsonStr string
	if err := chromedp.Run(ctx, chromedp.Evaluate(extractStateJS, &jsonStr)); err != nil {
		return nil, err
	}
	var state PageState
	if err := json.Unmarshal([]byte(jsonStr), &state); err != nil {
		return nil, fmt.Errorf("parse page state: %w", err)
	}
	return &state, nil
}

// dispatchCDPAction 处理 12 个交互动作
func dispatchCDPAction(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, ds *models.TSpiderDataSource) (resp *SpiderWebDataResponse, err error) {
	if action == nil {
		return nil, fmt.Errorf("action is nil")
	}

	// P0: panic recovery — 防止单个 action panic 把整个 session 拉死
	//
	// v2.0.21 改进（基于问题分析报告_20260630_220512 §1.4 navigate panic 报告）：
	// 原实现仅把 panic 值包成 error，Agent 拿到「action panic: runtime
	// error: invalid memory address or nil pointer dereference」是黑盒。
	// 现在把 session_id + action_type + 上一次成功 URL 都打包进 error，
	// 客户端可立即定位是「session 已被 captcha 标记 / engine cascade
	// canceled / context 过期」中哪一种。
	defer func() {
		if r := recover(); r != nil {
			mcpLogMCP("[SPIDER] PANIC in action %s (session=%s, currentURL=%s): %v",
				action.Type,
				func() string {
					if session != nil {
						return session.SessionID
					}
					return "<nil>"
				}(),
				func() string {
					if session != nil {
						return session.CurrentURL
					}
					return "<nil>"
				}(),
				r)
			resp = nil
			var sessionID, currentURL string
			if session != nil {
				sessionID = session.SessionID
				currentURL = session.CurrentURL
			}
			err = fmt.Errorf("action %s panic (session=%s, currentURL=%q): %v; "+
				"if session was rejected by captcha/anti-bot, call restart_browser or use a fresh session_id (do NOT pass url= or session_id=); "+
				"if currentURL is empty, populate it with action.url first",
				action.Type, sessionID, currentURL, r)
		}
	}()

	// P0(v2.0.19): engine 必须在最早期判 nil + 检查 rootCtx 健康。
	// 问题分析报告_20260630_112635 §4.3 反馈：cascade context canceled 后
	// session 内部 cdpCtx / cdpTarget 已 nil，但 action handler 仍尝试访问
	// engine.rootCtx（已 cancel）触发 nil 指针解引用 panic（修复前占比 25%）。
	// 这里把 engine/rootCtx 校验提到 cdpCtx 检查之前，避免进入下一层
	// runWithSession / attachCDPContext 后才暴露问题。
	engine := GetSpiderEngine()
	if engine == nil {
		return nil, fmt.Errorf("spider engine not initialized")
	}
	if !engine.isRunning {
		return nil, fmt.Errorf("spider engine not running")
	}
	// v2.0.19: 引擎 rootCtx 已 cascade canceled（健康检查 loop / RestartChrome 中）
	// 时直接给出可读错误 + 建议 restart_browser，避免每个 action 重复踩同一颗雷。
	//
	// v2.0.19 补丁（基于问题分析报告_20260630_112635 §7 建议 4）：cascade
	// context canceled 后若不主动 restart_browser，Agent 必须手动调用
	// restart_browser action 才能恢复（报告 §4.4 实测 60s+ 才拿到响应）。
	// 这里在第一个非 restart_browser action 命中 cascade 时**自动**触发
	// 一次 restart_browser（15s 上限、独立 goroutine），让下一次 action
	// 立即能恢复工作。autoRestartOnce 防止 Agent 在 restart 期间连续发
	// 请求触发多个并行 restart 把 e.mu 锁死。
	//
	// v2.0.30 补丁（基于问题分析报告_20260707_062144 §2.2 + 建议 4）：
	// 上版实现把 ActionTypeRestartBrowser 也一并拒绝 → Agent 拿到错误后
	// 无路可走，整条 MCP HTTP 层被拖死（报告 §1.1：06:11+ GET /healthz
	// 连续 180s HTTP:000）。改为：restart_browser 自身**放行**到
	// actionRestartBrowser，让它走 `restartChromeForced()` 旁路强制重启
	// （绕过 e.isRunning + 5s 短门闩 forceRestartMu），cascade 自愈不再死锁。
	if engine.rootCtx == nil || engine.rootCtx.Err() != nil {
		mcpLogMCP("[SPIDER] action %s sees engine rootCtx is cancelled (cascade state)", action.Type)
		if action.Type == ActionTypeRestartBrowser {
			// v2.0.30: 放行 — 让 actionRestartBrowser 走 forced 重启路径
			// （在 dispatch 守卫外层继续执行 action 处理；session 字段
			// 检查时也会发现 cdpCtx 已 done，自动 detach 后 attach 重建）。
			mcpLogMCP("[SPIDER] restart_bypass cascade: ActionTypeRestartBrowser will use forced path")
			// 继续走后续 session 检查与 action 执行；不返回错误
		} else if tryAutoRestartOnce(engine) {
			mcpLogMCP("[SPIDER] cascade detected, auto-triggering restart_browser (issue §7 建议 4)")
			go func() {
				_ = engine.RestartChrome()
			}()
			return nil, fmt.Errorf("action %s rejected: spider engine root context is cancelled; "+
				"auto-restart triggered (30s idempotent gate), retry with action=restart_browser after 1s",
				action.Type)
		} else {
			return nil, fmt.Errorf("action %s rejected: spider engine root context is cancelled; "+
				"call restart_browser action or wait for auto-recovery (5s short gate after recent forced restart)",
				action.Type)
		}
	}

	// P0: session 有效性检查
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}

	// P0: 进入 action 前检查 session CDP 上下文是否有效
	if session.cdpCtx != nil {
		select {
		case <-session.cdpCtx.Done():
			// 旧的已死，清理后让 attachCDPContext 重建
			detachCDPContext(session)
		default:
		}
	}

	session.cdpMu.Lock()
	defer session.cdpMu.Unlock()

	// P0: 每个 action 执行后强制清理 session CDP 资源，防止 tab 泄漏导致 busy 锁死
	// 注意：actionNavigate 内部会调用 attachCDPContext，它自己管理 tab 生命周期
	// 其他 action 通过 runWithSession 复用 tab，这里统一兜底释放
	//
	// v2.0.18 补丁（基于问题分析报告_20260629_143200 §3.5）：restart_browser
	// 内部已经 detachAllSpiderSessions 把所有 session 的 cdpCtx / cdpCancel /
	// cdpTarget 都置 nil 了，dispatcher 再 detach 一次是 no-op + 噪音；跳过。
	defer func() {
		if err != nil && engine != nil {
			if strings.Contains(err.Error(), "context canceled") {
				engine.recordContextCanceledFailure()
			}
		}
		if action.Type != ActionTypeNavigate && action.Type != ActionTypeRestartBrowser {
			// 非 navigate action：复用 tab 后主动 detach，避免 session 堆积
			// navigate 需要保留 tab 供后续 action 复用，不在这里释放
			// restart_browser 已自行处理所有 session 释放
			detachCDPContext(session)
		}
	}()

	// v2.0.3: 根据数据源配置选择 UA（如果配置了 per-source UA）
	// 在 action 执行前注入 stealth 和 per-source 配置
	if ds != nil && ds.ID > 0 {
		// 这里可以扩展：根据 ds 的额外配置注入不同的 Chrome 参数
		// 当前版本：UA 策略在 startChromeProcess 中统一处理
	}

	switch action.Type {
	case ActionTypeNavigate:
		return actionNavigate(session, action, req, engine)
	case ActionTypeClick:
		return actionClick(session, action, req, engine)
	case ActionTypeScroll:
		return actionScroll(session, action, req, engine)
	case ActionTypeScrollTo:
		return actionScrollTo(session, action, req, engine)
	case ActionTypeFillForm:
		return actionFillForm(session, action, req, engine)
	case ActionTypeExtract:
		return actionExtract(session, action, req, engine)
	case ActionTypeScreenshot:
		return actionScreenshot(session, action, req, engine)
	case ActionTypeGetPageState:
		return actionGetState(session, action, req, engine)
	case ActionTypeWait:
		return actionWait(session, action, req, engine)
	case ActionTypeHover:
		return actionHover(session, action, req, engine)
	case ActionTypeSelect:
		return actionSelect(session, action, req, engine)
	case ActionTypeKeypress:
		return actionKeypress(session, action, req, engine)
	case ActionTypeSwitchFrame:
		return actionSwitchFrame(session, action, req, engine)
	case ActionTypeDragAndDrop:
		return actionDragAndDrop(session, action, req, engine)

	// v2.0.7: 鼠标全功能
	case ActionTypeRightClick:
		return actionRightClick(session, action, req, engine)
	case ActionTypeDoubleClick:
		return actionDoubleClick(session, action, req, engine)
	case ActionTypeMiddleClick:
		return actionMiddleClick(session, action, req, engine)
	case ActionTypeClickAt:
		return actionClickAt(session, action, req, engine)
	case ActionTypeMouseMove:
		return actionMouseMove(session, action, req, engine)
	case ActionTypeWheel:
		return actionWheel(session, action, req, engine)

	// v2.0.7: 键盘增强
	case ActionTypePressKey:
		return actionPressKey(session, action, req, engine)
	case ActionTypeTypeText:
		return actionTypeText(session, action, req, engine)

	// v2.0.7: Tab 页面管理
	case ActionTypeNewTab:
		return actionNewTab(session, action, req, engine)
	case ActionTypeSwitchTab:
		return actionSwitchTab(session, action, req, engine)
	case ActionTypeCloseTab:
		return actionCloseTab(session, action, req, engine)
	case ActionTypeListTabs:
		return actionListTabs(session, action, req, engine)

	// v2.0.14: 自愈 action
	case ActionTypeRestartBrowser:
		return actionRestartBrowser(session, action, req, engine)

	// v2.0.7: 调试观察
	case ActionTypeConsoleLogs:
		return actionConsoleLogs(session, action, req, engine)
	case ActionTypeNetworkLog:
		return actionNetworkLog(session, action, req, engine)
	case ActionTypeElements:
		return actionElements(session, action, req, engine)
	case ActionTypeDom:
		return actionDom(session, action, req, engine)
	case ActionTypeEval:
		return actionEval(session, action, req, engine)

	// v2.0.7: 存储
	case ActionTypeLocalStorage:
		return actionLocalStorage(session, action, req, engine)
	case ActionTypeSessionStorage:
		return actionSessionStorage(session, action, req, engine)
	case ActionTypeCookies:
		return actionCookies(session, action, req, engine)

	// v2.0.7: 其他
	case ActionTypeUploadFile:
		return actionUploadFile(session, action, req, engine)
	case ActionTypeElementScreenshot:
		return actionElementScreenshot(session, action, req, engine)

	default:
		return nil, fmt.Errorf("unknown action type: %s", action.Type)
	}
}

func resolveRequestTimeout(req *SpiderWebDataRequest) time.Duration {
	if req.Timeout > 0 {
		t := time.Duration(req.Timeout) * time.Second
		if t < 5*time.Second {
			t = 5 * time.Second
		}
		if t > 180*time.Second {
			t = 180 * time.Second
		}
		return t
	}
	return 90 * time.Second
}

// resolveActionURL 解析 action 的目标 URL
// 优先级：action.URL > req.URL > session.CurrentURL
// v2.0.19 补丁（基于问题分析报告_20260630_095236 §3.2）：session 可能为 nil，
// 访问 session.CurrentURL 会触发 nil 指针解引用 panic。session == nil 时
// 返回空字符串，调用方负责用错误终止而非继续传给 chromedp。
func resolveActionURL(action *InteractiveAction, req *SpiderWebDataRequest, session *SpiderSession) string {
	if action != nil && action.URL != "" {
		return action.URL
	}
	if req != nil && req.URL != "" {
		return req.URL
	}
	if session == nil {
		return ""
	}
	return session.CurrentURL
}

const respCrawlMaxLen = 10000

func fillResultMeta(resp *SpiderWebDataResponse, url, title, html, content string, session *SpiderSession) {
	resp.URL = url
	if title != "" {
		resp.Title = title
	} else {
		resp.Title = extractTitle(html)
	}
	if content != "" {
		resp.Content = content
	} else {
		resp.Content = extractContentSimpleWithLimit(html, respCrawlMaxLen)
	}
	resp.RawHTML = html
	resp.Language = detectLanguage(resp.Content)
	resp.CrawlTime = time.Now().UTC()
	resp.HasMore = true
	if html != "" {
		resp.Elements = extractWebElements(html, url)
	}
	if session != nil {
		session.CurrentURL = url
		if html != "" {
			session.CurrentRawHTML = html
		}
	}
}

// runWithSession 在 session 的 CDP context 上跑一段 chromedp action
func runWithSession(session *SpiderSession, engine *SpiderEngine, timeout time.Duration, fn func(ctx context.Context) error) error {
	// P0: session nil 检查
	if session == nil {
		return fmt.Errorf("session is nil")
	}
	// v2.0.19: 在 attachCDPContext 之前再确认 engine.rootCtx 健康（问题分析
	// 报告_20260630_112635 §4.3：cascade state 下 attachCDPContext 内部
	// context.WithTimeout(engine.rootCtx, ...) 会立即 done + 触发 nil 解引用）。
	if engine == nil {
		return fmt.Errorf("spider engine is nil")
	}
	if engine.rootCtx == nil || engine.rootCtx.Err() != nil {
		return fmt.Errorf("spider engine root context cancelled; call restart_browser to recover")
	}
	ctx, cancel, err := attachCDPContext(session, engine, timeout)
	if err != nil {
		return err
	}
	// P0: 每个 action 完成后强制释放 tab，避免 session 堆积导致 busy 锁死
	defer cancel()
	// P0(v2.0.9): attachCDPContext 复用 tab 时会直接返回原 cdpCtx 而忽略本次 timeout，
	// 导致复用路径的 action 没有 per-action deadline，click/double_click 在永不稳定的
	// SPA 节点上会一路挂起到 handler 软超时（4m0s）。这里对执行层再包一层 timeout，
	// 确保单个 action 在 ~timeout（默认 90s）内以 context deadline exceeded 返回。
	runCtx, runCancel := context.WithTimeout(ctx, timeout)
	defer runCancel()
	return fn(runCtx)
}

// runWithWatchdog 在 session 上跑一段 chromedp action，并对"等待稳定"这一步加硬超时。
// v2.0.11: 解决 React/Vue 高频重渲染导致 chromedp.WaitReady("body") 永远不满足，
// 进而 click action 挂死 10s+ 的问题。与 click_at 已有的"绕过 WaitReady"路径对齐，
// 但保留一个轻量稳定的等待（最多 waitMs，超时即视为已稳定继续）。
//   - waitMs: 等待 DOM 稳定的硬上限，默认 800ms；超时则降级为 sleep(100ms) 后立即返回。
//   - 该 helper 仅做"等待稳定"层加固，runCtx 仍是 ~90s 上限（来自 runWithSession）。
func runWithWatchdog(session *SpiderSession, engine *SpiderEngine, timeout time.Duration, waitMs int, fn func(ctx context.Context) error) error {
	if waitMs <= 0 {
		waitMs = 800
	}
	return runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		// 等待稳定：硬上限 waitMs，期间 WaitReady 超时即降级
		watchdogCtx, watchdogCancel := context.WithTimeout(ctx, time.Duration(waitMs)*time.Millisecond)
		defer watchdogCancel()
		// 用 chromedp.ActionFunc 套 watchdogCtx，让 WaitReady 在超时时立即返回
		waitAction := chromedp.ActionFunc(func(c context.Context) error {
			if err := chromedp.Run(watchdogCtx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
				// watchdog 命中：忽略 err，降级 sleep
				_ = chromedp.Sleep(100 * time.Millisecond).Do(c)
				return nil
			}
			return nil
		})
		_ = waitAction.Do(ctx)
		return nil
	})
}

// readClickPreStateJS 返回点击前元素状态快照 JS 表达式片段（v2.0.11）。
// 用于 actionClick / actionClickAt 的效果校验对比：
//   - className 变化 → disabled 状态变化 → value 变化 → textContent 变化 任一为 true 即视为副作用已发生
//
// 返回的 JS 是单值字符串（className + '|' + disabled + '|' + value + '|' + textContent.slice(0,80)）。
func readClickPreStateJS(sel parsedSelector) string {
	return fmt.Sprintf(`(()=>{const el=%s;if(!el)return '';const c=el.className||'';const d=el.disabled?'1':'0';const v=(el.value!==undefined?el.value:(el.textContent||'')).toString().slice(0,80);return c+'|'+(d==='1'?'disabled':'')+'|'+v;})()`, elementLocatorJS(sel))
}

// readClickPostStateJS 点击后读取元素状态 + 网络日志增量。
//   - elementState: 同 readClickPreStateJS 格式
//   - networkDelta: __lsm_network_log__ 长度增量（点击前快照长度 - 当前长度）
//
// 由调用方在调用前后分别跑一次，对比结果。
const readClickPostStateJS = `(function(){
  const el = arguments[0];
  if (!el) return JSON.stringify({elementState: '', networkDelta: 0});
  const c = el.className || '';
  const d = el.disabled ? '1' : '0';
  const v = (el.value !== undefined ? el.value : (el.textContent || '')).toString().slice(0, 80);
  const network = (window.__lsm_network_log__ || []).length;
  return JSON.stringify({
    elementState: c + '|' + (d === '1' ? 'disabled' : '') + '|' + v,
    networkDelta: network
  });
})()`

// verifyClickEffect 校验 click / click_at 是否触发了业务侧副作用（v2.0.11）。
//   - sel: 选择器（用于在 runCtx 内 JS 定位元素）
//   - preState: 点击前 readClickPreStateJS 返回的字符串
//   - waitMs: 等待副作用发生的窗口，默认 500ms
//
// 返回 *ClickEffectVerification。EffectVerified=false 时填充 Warning 提示 Agent。
// 实现要点：
//   - 用 runWithSession 的 runCtx 作为执行环境（per-action deadline 仍由上层负责）
//   - 校验逻辑全部在 JS 中完成，避免额外 RPC；PostState + NetworkDelta 一次返回
//   - 校验窗口 500ms 内若无 elementState / networkDelta 变化，标记 spa_no_effect
func verifyClickEffect(session *SpiderSession, engine *SpiderEngine, sel parsedSelector, preState string, waitMs int) *ClickEffectVerification {
	if waitMs <= 0 {
		waitMs = 500
	}
	v := &ClickEffectVerification{
		PreState: preState,
		WaitMs:   waitMs,
	}
	err := runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
		// 等副作用窗口
		_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(waitMs)*time.Millisecond))
		locator := elementLocatorJS(sel)
		js := fmt.Sprintf(`(function(){
		  const el = %s;
		  if (!el) return JSON.stringify({found:false});
		  const c = el.className || '';
		  const d = el.disabled ? '1' : '0';
		  const v = (el.value !== undefined ? el.value : (el.textContent || '')).toString().slice(0, 80);
		  const network = (window.__lsm_network_log__ || []).length;
		  return JSON.stringify({
		    found: true,
		    elementState: c + '|' + (d === '1' ? 'disabled' : '') + '|' + v,
		    networkCount: network
		  });
		})()`, locator)
		var raw string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
			return err
		}
		var result struct {
			Found        bool   `json:"found"`
			ElementState string `json:"elementState"`
			NetworkCount int    `json:"networkCount"`
		}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return err
		}
		if !result.Found {
			v.Warning = "element not found after click (likely detached)"
			return nil
		}
		v.PostState = result.ElementState
		// 比较 pre / post
		v.HasElementChange = preState != "" && preState != result.ElementState
		// networkDelta: 这里 NetworkCount 是当前总数，调用方在调用前后各自缓存一次比较
		// 简化：上层在调用前后各自缓存 preNetworkCount / postNetworkCount 直接传入
		v.HasNetworkChange = result.NetworkCount > 0 && v.NetworkRequestsDelta > 0
		v.EffectVerified = v.HasElementChange || v.HasNetworkChange
		if !v.EffectVerified {
			v.Warning = "click dispatched but no effect detected (spa_no_effect); " +
				"建议改用 eval 单 roundtrip 完成 set + InputEvent + submit.click"
		}
		return nil
	})
	if err != nil {
		v.Warning = fmt.Sprintf("verify effect failed: %v", err)
	}
	return v
}

// snapshotNetworkCount 取一次当前 network_log 长度快照（v2.0.11）。
// 用于 verifyClickEffect 调用方在 click 前后做 delta 比较。
func snapshotNetworkCount(ctx context.Context) int {
	var n int
	_ = chromedp.Run(ctx, chromedp.Evaluate(`(window.__lsm_network_log__||[]).length`, &n))
	return n
}

// tryClickClosestAncestorOnNoEffect v2.0.11 增强：click 派发后业务侧无副作用时
// （verifyClickEffect 返回 EffectVerified=false），自动尝试点击「最近的可点击祖先」。
//   - 场景：SPA 框架（如 chat.baidu.com 文心一言 React 18+）把 onClick 绑在父级 button 上，
//     而选择器命中的是 IMG/SVG 子节点。React 合成事件冒泡时会绕过 IMG，导致 onClick
//     永远收不到。解决方案：从命中节点向上找最近一个原生 clickable / role=button /
//     data-* 提交信号的祖先，先做 disabled 探测，再 click()。
//   - waitMs: 祖先点击后等待副作用的窗口（默认 500ms）。
//   - 返回 (success bool, recoveryDetails string)：success=true 表示祖先点击后
//     已观察到副作用（state change 或 network request）；recoveryDetails 是给 Agent
//     看的诊断信息，会附到 ClickEffectVerification.Warning 末尾。
//
// 设计取舍：
//   - 仅在 EffectVerified=false 时触发，避免对正常点击产生额外副作用。
//   - 找到祖先但 click() 失败不会让原 click 报错；只是返回 false 告知调用方未救活。
//   - 不修改 click 本身的返回值，Agent 仍按 click 的 success 判断动作执行结果；
//     救活信息只附加到 ClickEffectVerification 让 Agent 看到后续补救路径。
//
// buildAncestorProbeJS 返回「点击最近的可点击祖先」探测 JS（v2.0.11 增强）。
// 输出 JSON 字段契约：
//   - found: bool，原始目标是否被定位到
//   - ancestor: bool，是否找到了 clickable 祖先
//   - clicked: bool，是否成功调用了 ancestor.click()
//   - disabled: bool，ancestor 当前是否 disabled
//   - tag / id / cls: ancestor 的 tagName / id / className 摘要，便于诊断
//   - error: string，click() 抛错时的错误信息（成功时为空）
//
// 抽出来作为独立函数便于单元测试断言字段名稳定。
//
// v2.0.19 补丁（基于问题分析报告_20260630_112635 §4.3 / §7 建议 6）：
// captcha / 空白页（无 data-reactroot / __vueParentComponent）上 parentElement
// 链遍历 6 层可能全部是 null + document.body；之前版本若命中元素被 detach
// 后 cursor.parentElement 立即返回 null，循环正常退出。但当 DOM 在 evaluate
// 期间被反爬脚本替换 / 大量 frame detache 时，cursor 可能短暂变成已 detach
// 节点（children.length === 0 + parentElement === null），已加
// `cursor && cursor !== document.body` 守卫；再附加 children.length 守卫，
// 防止 JS 引擎在 chrome 上对已 detach Node 访问 children 时抛
// InvalidStateError / DOMException（部分版本上 stack trace 被报为
// "index out of range [-1]"，见报告 §4.3 第 2 项猜测）。
func buildAncestorProbeJS(sel parsedSelector) string {
	return fmt.Sprintf(`(function(){
		  const el = %s;
		  if (!el) return JSON.stringify({found:false});
		  function isClickable(node){
		    if (!node || node.nodeType !== 1) return false;
		    if (node.disabled) return false;
		    // v2.0.19: 已 detach 节点 isConnected=false + children.length=0，
		    // 跳过避免在反爬脚本快速替换 DOM 时访问已 detach 子树。
		    if (node.isConnected === false) return false;
		    const tag = node.tagName;
		    if (tag === 'A' || tag === 'BUTTON' || tag === 'INPUT') return true;
		    const role = node.getAttribute && node.getAttribute('role');
		    if (role === 'button' || role === 'link') return true;
		    if (node.getAttribute && node.getAttribute('data-submit')) return true;
		    try {
		      const cur = window.getComputedStyle(node).cursor;
		      if (cur === 'pointer') return true;
		    } catch(e) {}
		    return false;
		  }
		  let cursor = el;
		  let ancestor = null;
		  for (let i = 0; i < 6 && cursor && cursor !== document.body; i++) {
		    try {
		      if (cursor.children && cursor.children.length === 0 && !cursor.isConnected) {
		        // 已 detach 且无子节点：跳出循环，避免再次解引用
		        break;
		      }
		    } catch(e) { break; }
		    cursor = cursor.parentElement;
		    if (cursor && isClickable(cursor)) { ancestor = cursor; break; }
		  }
		  if (!ancestor) return JSON.stringify({found:true, ancestor:false});
		  const ancDisabled = !!ancestor.disabled;
		  try { ancestor.click(); } catch(e) {
		    return JSON.stringify({found:true, ancestor:true, clicked:false, error:String(e), disabled:ancDisabled});
		  }
		  return JSON.stringify({
		    found: true,
		    ancestor: true,
		    clicked: true,
		    disabled: ancDisabled,
		    tag: ancestor.tagName,
		    id: ancestor.id || '',
		    cls: (ancestor.className || '').toString().slice(0, 60)
		  });
		})()`, elementLocatorJS(sel))
}

func tryClickClosestAncestorOnNoEffect(session *SpiderSession, engine *SpiderEngine, sel parsedSelector, waitMs int) (bool, string) {
	if waitMs <= 0 {
		waitMs = 500
	}
	var recovered bool
	var detail string
	_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
		// 1) 取 preNetworkCount 用于后续 delta
		preNet := snapshotNetworkCount(ctx)
		// 2) 在 JS 中：找到原目标最近的 clickable 祖先并探测 disabled；click()
		//    若祖先与原目标不同则视为"补救"，再读 postNetworkCount 比较
		js := buildAncestorProbeJS(sel)
		var raw string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
			detail = fmt.Sprintf("ancestor probe failed: %v", err)
			return nil
		}
		var probe struct {
			Found    bool   `json:"found"`
			Ancestor bool   `json:"ancestor"`
			Clicked  bool   `json:"clicked"`
			Disabled bool   `json:"disabled"`
			Tag      string `json:"tag"`
			ID       string `json:"id"`
			Cls      string `json:"cls"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			detail = fmt.Sprintf("ancestor probe parse: %v", err)
			return nil
		}
		if !probe.Found {
			detail = "ancestor probe: original element not found"
			return nil
		}
		if !probe.Ancestor {
			detail = "no clickable ancestor found (target is leaf / disabled / non-button)"
			return nil
		}
		if !probe.Clicked {
			detail = fmt.Sprintf("ancestor click threw: %s (disabled=%v)", probe.Error, probe.Disabled)
			return nil
		}
		// 3) 等副作用窗口，再读 network count
		_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(waitMs)*time.Millisecond))
		postNet := snapshotNetworkCount(ctx)
		delta := postNet - preNet
		if delta > 0 {
			recovered = true
			detail = fmt.Sprintf("ancestor %s#%s.%s click -> %d new network requests (spa_no_effect recovered)", probe.Tag, probe.ID, probe.Cls, delta)
		} else {
			detail = fmt.Sprintf("ancestor %s#%s.%s clicked but still no network change (deeper handler missing)", probe.Tag, probe.ID, probe.Cls)
		}
		return nil
	})
	return recovered, detail
}

// autoRestartOnce 用于 cascade context canceled 时自动触发 restart_browser 的幂等门闩。
// v2.0.19（基于问题分析报告_20260630_112635 §7 建议 4）：Agent 在 cascade 期间
// 可能短时间并发触发多次 action，每个 action 都看到 rootCtx 已 cancel；若没有门闩，
// 每个 goroutine 都会调用 RestartChrome，把 e.mu 锁死（问题分析报告 §4.4 实测 60s+
// 卡死）。这里用 sync.Once + 标记截止时间：一次 restart 启动后 30 秒内不再触发
// 第二次自动 restart，让 healthCheckLoop 有时间完成自愈周期。
var (
	autoRestartMu       sync.Mutex
	autoRestartLastTime time.Time
)

// tryAutoRestartOnce 在 cascade context canceled 时返回 true（表示「应触发自动 restart」）。
// 30 秒内仅触发一次；超出 30 秒后若 rootCtx 仍未恢复，可再次触发。
func tryAutoRestartOnce(engine *SpiderEngine) bool {
	autoRestartMu.Lock()
	defer autoRestartMu.Unlock()
	if time.Since(autoRestartLastTime) < 30*time.Second {
		return false
	}
	autoRestartLastTime = time.Now()
	return true
}
