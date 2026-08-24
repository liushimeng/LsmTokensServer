package spider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"math"
	"math/rand"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// ==================== 单个 action 实现 ====================

func actionNavigate(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	targetURL := resolveActionURL(action, req, session)
	if targetURL == "" {
		// v2.0.19 补丁（基于问题分析报告_20260630_095236 §3.2）：
		// 显式返回 error 而不是把空 URL 透传给 chromedp.Navigate，
		// 后者在某些版本上会触发 nil 指针解引用（chromedp 内部 panic）。
		// Agent 收到错误后可显式补充 url/action.url 重试。
		hasActionURL := action != nil && action.URL != ""
		hasReqURL := req != nil && req.URL != ""
		hasSessionURL := session != nil && session.CurrentURL != ""
		return nil, fmt.Errorf(
			"navigate: no target URL available (action.url=%v, req.url=%v, session.currentURL=%v); "+
				"pass action.url or req.url, or call navigate first to populate session.currentURL",
			hasActionURL, hasReqURL, hasSessionURL,
		)
	}
	maxLen := req.MaxContentLen
	if maxLen <= 0 {
		maxLen = respCrawlMaxLen
	}
	timeout := resolveRequestTimeout(req)
	// v2.0.0: navigate action 也走 session tab（attachCDPContext），
	// 确保 navigate 后的页面与后续 action 在同一 tab
	ctx, cancel, err := attachCDPContext(session, engine, timeout)
	if err != nil {
		return nil, err
	}
	// P0: navigate 后保存 cancel 到 session，供后续 action 复用同一 tab
	// 注意：cancel 会在 session 被清理或下次 attachCDPContext 时调用
	session.cdpCancel = cancel
	// v2.0.8: 传入 session 让 crawlWebDataCDPWithCtx 应用 per-session fingerprint
	resp, err := engine.crawlWebDataCDPWithCtx(ctx, session, req.DataSourceID, targetURL, timeout, maxLen)
	// v2.0.13: 可选等待客户端 SPA 水合（hydration）完成 —
	// 解决 chat.baidu.com 等"SSR HTML 已就绪但客户端 bundle 未水合"
	// 导致所有 fill_form / click / eval 均无业务侧回调的问题。
	if err == nil && resp != nil && req.WaitForHydration {
		hydrateCtx, hydrateCancel := context.WithTimeout(ctx, 6*time.Second)
		resp.HydrationState = probeAndWaitForHydration(hydrateCtx, req.WaitForHydrationMs)
		hydrateCancel()
	}
	return resp, err
}

func actionClick(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := action.Selector
	if sel == "" {
		sel = action.XPath
	}
	if sel == "" {
		return nil, fmt.Errorf("click: selector or xpath required")
	}
	parsed := parseSelector(sel)
	if session.CurrentURL == "" {
		// v2.0.21 改进：把可恢复路径放进 error 文本，避免 Agent 反复撞同一
		// 堵墙（参考问题分析报告_20260630_220512 §1.4 描述的 "click: no
		// current page in session" 失败链）。
		return nil, fmt.Errorf(
			"click: no current page in session (session=%s); "+
				"this usually means the previous navigate was rejected by captcha/anti-bot, "+
				"or session expired (TTL=10min). "+
				"Recovery: (1) call action.navigate first to populate currentURL, or "+
				"(2) drop session_id and start a new session by omitting it, or "+
				"(3) call action.restart_browser to reset engine state",
			session.SessionID,
		)
	}
	timeout := resolveRequestTimeout(req)

	// v2.0.11: 读取点击前状态快照 + 网络计数，用于后续效果校验
	var preState string
	var preNetworkCount int
	_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
		preJS := readClickPreStateJS(parsed)
		_ = chromedp.Run(ctx, chromedp.Evaluate(preJS, &preState))
		preNetworkCount = snapshotNetworkCount(ctx)
		return nil
	})

	var postHTML, postTitle, currentURL string
	err := runWithWatchdog(session, engine, timeout, 1500, func(ctx context.Context) error {
		// v2.0.3: 真人化交互 — 随机延迟 + 鼠标移动模拟
		// v2.0.9: Human-like 增强（可选；默认关闭以保持 v2.0.8 行为）
		humanLike := config.G != nil && config.G.SpiderHumanLikeEnabled
		if humanLike && config.G.SpiderThinkingTimeMeanMs > 0 {
			_ = ThinkingSleep(ctx, config.G.SpiderThinkingTimeMeanMs, config.G.SpiderThinkingTimeSigmaMs)
		}
		if parsed.Strategy != selText {
			var nodes []*cdp.Node
			if err := querySelector(ctx, parsed, &nodes); err != nil {
				return err
			}
			if len(nodes) > 0 {
				// 模拟鼠标移动到元素中心
				var box *dom.BoxModel
				if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err == nil && box != nil && len(box.Border) >= 8 {
					cx := (box.Border[0] + box.Border[4]) / 2
					cy := (box.Border[1] + box.Border[5]) / 2
					// v2.0.9: 微动 + 贝塞尔鼠标
					if humanLike && config.G.SpiderMicroMouseMovements {
						_ = MicroMouseMovements(ctx, int(box.Border[4]-box.Border[0]+50), int(box.Border[5]-box.Border[1]+50), int(cx), int(cy))
					}
					if humanLike && config.G.SpiderBezierMouseMove {
						// v2.0.19: 起点改为视口内随机方向（150-350px），替代固定正上方 200px
						angle := rand.Float64() * 2 * math.Pi
						dist := 150.0 + rand.Float64()*200.0
						sx := float64(cx) + dist*math.Cos(angle)
						sy := float64(cy) + dist*math.Sin(angle)
						if sx < 0 {
							sx = 10
						}
						if sx > 1920 {
							sx = 1910
						}
						if sy < 0 {
							sy = 10
						}
						if sy > 1080 {
							sy = 1070
						}
						_ = SmoothMouseMove(ctx, sx, sy, float64(cx), float64(cy), 8)
					} else {
						_ = chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseMoved"), cx, cy))
					}
					// 随机延迟 200-600ms
					_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(200+rand.Intn(400))*time.Millisecond))
				}
			}
		}
		// text 模式：跳过 querySelector，直接 JS click
		if parsed.Strategy == selText {
			kw := parsed.TextKeyword
			js := fmt.Sprintf(`(function(){
			  const kw = %q;
			  function walk(n) {
			    if (!n) return null;
			    if (n.nodeType === 3 && n.textContent.includes(kw)) return n.parentElement;
			    for (const c of n.childNodes) { const r = walk(c); if (r) return r; }
			    return null;
			  }
			  const el = walk(document.body);
			  if (el) { el.click(); return true; }
			  return false;
			})()`, kw)
			var ok bool
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("text selector not found: %s", sel)
			}
		} else {
			var nodes []*cdp.Node
			if err := querySelector(ctx, parsed, &nodes); err != nil {
				return err
			}
			if len(nodes) == 0 {
				return fmt.Errorf("selector not found: %s", sel)
			}
			if err := chromedp.Run(ctx, chromedp.MouseClickNode(nodes[0])); err != nil {
				return err
			}
		}
		// v2.0.11: 等待页面稳定由 runWithWatchdog 接管（1500ms 硬上限，
		// 超时即降级 sleep 100ms 后继续），不再依赖 WaitReady("body") 无限等待。
		_ = chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond))
		return chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}

	// v2.0.11: 动作效果校验 — 区分 CDP 点击已派发 与 业务侧副作用已发生
	var verif *ClickEffectVerification
	_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
		postNetworkCount := snapshotNetworkCount(ctx)
		// 调用统一校验 helper，并补全 NetworkRequestsDelta
		v := verifyClickEffect(session, engine, parsed, preState, 500)
		if v != nil {
			v.NetworkRequestsDelta = postNetworkCount - preNetworkCount
			if v.NetworkRequestsDelta > 0 {
				v.HasNetworkChange = true
				v.EffectVerified = true
			}
			verif = v
		}
		return nil
	})

	// v2.0.11 增强：false-positive（CDP click 已派发但业务侧无反应）时，自动尝试
	// 点击最近的可点击祖先，常见于 React 18+ 把 onClick 绑在父级 button 而选择器
	// 命中 IMG/SVG 子节点。recovered=true 时把 EffectVerified 翻为 true 并把祖先
	// 信息附加到 Warning，便于 Agent 复用同样的选择器模式。
	if verif != nil && !verif.EffectVerified && verif.Warning != "" {
		if recovered, detail := tryClickClosestAncestorOnNoEffect(session, engine, parsed, 500); recovered {
			verif.EffectVerified = true
			verif.HasElementChange = true
			if verif.Warning != "" {
				verif.Warning = verif.Warning + "; auto-recovered by " + detail
			} else {
				verif.Warning = "auto-recovered by " + detail
			}
			mcpLogMCP("[SPIDER] click auto-recovered via ancestor click (selector=%s, %s)", sel, detail)
		} else if detail != "" {
			verif.Warning = verif.Warning + "; ancestor probe: " + detail
		}
	}

	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	resp.ClickEffectVerification = verif
	if verif != nil && !verif.EffectVerified && verif.Warning != "" {
		// false positive：click 已派发但业务侧无反应，把 spa_no_effect 透传给 Agent
		// 注：这里只挂载到响应字段，不直接 fail — 与 click_at 一致（动作执行成功 + 业务效果缺失是常见情况）
		mcpLogMCP("[SPIDER] click dispatched but no effect detected (selector=%s, pre=%q post=%q)", sel, verif.PreState, verif.PostState)
	}
	return resp, nil
}

// querySelector 跨 selector strategy 查找节点
func querySelector(ctx context.Context, sel parsedSelector, out *[]*cdp.Node) error {
	switch sel.Strategy {
	case selXPath:
		return chromedp.Run(ctx, chromedp.Nodes(sel.Query, out, chromedp.BySearch))
	case selText:
		// text 模式：通过 JS 找第一个匹配节点，返回占位 sentinel 让 clickNode 知道走 JS 路径
		js := fmt.Sprintf(`(function(){
		  const kw = %q;
		  function walk(n) {
		    if (!n) return null;
		    if (n.nodeType === 3 && n.textContent.includes(kw)) return n.parentElement;
		    for (const c of n.childNodes) { const r = walk(c); if (r) return r; }
		    return null;
		  }
		  const el = walk(document.body);
		  return el ? (el.tagName + ':' + (el.id || '') + '.' + (el.className || '')) : '';
		})()`, sel.TextKeyword)
		var marker string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &marker)); err != nil {
			return err
		}
		if marker == "" {
			return nil // 没找到；out 留空
		}
		// 放一个 sentinel node 触发 clickNode 走 JS 路径
		*out = []*cdp.Node{{NodeID: 0}} // 用零值表示"用 JS click"
		return nil
	default: // selCSS
		return chromedp.Run(ctx, chromedp.Nodes(sel.Query, out, chromedp.ByQuery))
	}
}

// clickNode 点击已找到节点
func clickNode(ctx context.Context, sel parsedSelector, node *cdp.Node) error {
	if sel.Strategy == selText || (node != nil && node.NodeID == 0) {
		// text 模式或 sentinel：直接用 JS 派发 click 事件
		kw := sel.TextKeyword
		js := fmt.Sprintf(`(function(){
		  const kw = %q;
		  function walk(n) {
		    if (!n) return null;
		    if (n.nodeType === 3 && n.textContent.includes(kw)) return n.parentElement;
		    for (const c of n.childNodes) { const r = walk(c); if (r) return r; }
		    return null;
		  }
		  const el = walk(document.body);
		  if (el) { el.click(); return true; }
		  return false;
		})()`, kw)
		var ok bool
		return chromedp.Run(ctx, chromedp.Evaluate(js, &ok))
	}
	return chromedp.Run(ctx, chromedp.MouseClickNode(node))
}

func actionScroll(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	targetURL := session.CurrentURL
	if req.URL != "" {
		targetURL = req.URL
	}
	if targetURL == "" {
		return nil, fmt.Errorf("scroll: no URL in session")
	}
	timeout := resolveRequestTimeout(req)
	maxLen := req.MaxContentLen
	if maxLen <= 0 {
		maxLen = respCrawlMaxLen
	}

	// Phase 1: URL 模式翻页
	next := buildNextPageURL(targetURL, action.Parameters)
	if next != "" && next != targetURL {
		mcpLogMCP("scroll: URL pattern hit %s -> %s", targetURL, next)
		return engine.crawlWebDataCDPWithCtx(context.Background(), session, req.DataSourceID, next, timeout, maxLen)
	}

	// Phase 2: JS 滚动
	times := 1.0
	if t, ok := action.Parameters["times"].(float64); ok && t > 0 {
		times = t
	}
	delayMs := 600
	if d, ok := action.Parameters["delay_ms"].(float64); ok && d > 0 {
		delayMs = int(d)
	}

	var postHTML, postTitle, currentURL string
	var hasMore bool
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		// v2.0.9: 阅读式滚动（仅当 SpiderHumanLikeEnabled + SpiderReadingStyleScroll）
		if config.G != nil && config.G.SpiderHumanLikeEnabled && config.G.SpiderReadingStyleScroll {
			// 多段小步滚动，每段 200-500ms 抖动；总距离使用阅读式变量
			var vhInfo string
			if err := chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify({vh: window.innerHeight, max: document.body.scrollHeight, y: window.scrollY})`, &vhInfo)); err == nil {
				var st struct {
					VH int `json:"vh"`
				}
				_ = json.Unmarshal([]byte(vhInfo), &st)
				chunks := 3 + rand.Intn(4) // 3-6
				for i := 0; i < chunks; i++ {
					stepDelta := ReadingStyleScrollVar(st.VH, rand.New(rand.NewSource(time.Now().UnixNano())))
					if stepDelta <= 0 {
						stepDelta = st.VH / chunks
					}
					stepJS := fmt.Sprintf(`window.scrollBy(0, %d)`, stepDelta/chunks)
					_ = chromedp.Run(ctx, chromedp.Evaluate(stepJS, nil))
					_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(200+rand.Intn(300))*time.Millisecond))
				}
			}
		} else {
			js := fmt.Sprintf(`(function(t){
			  window.scrollBy(0, window.innerHeight * t);
			  return JSON.stringify({y: window.scrollY, max: document.body.scrollHeight, vh: window.innerHeight});
			})(%g)`, times)
			var info string
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &info)); err != nil {
				return err
			}
			var st struct {
				Y, Max, VH int `json:"y,max,vh"`
			}
			_ = json.Unmarshal([]byte(info), &st)
			hasMore = st.Y+st.VH < st.Max
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(delayMs)*time.Millisecond))
		return chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{HasMore: hasMore}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}

func actionScrollTo(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	x := 0.0
	y := 0.0
	if v, ok := action.Parameters["x"].(float64); ok {
		x = v
	}
	if v, ok := action.Parameters["y"].(float64); ok {
		y = v
	}
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	var hasMore bool
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		js := fmt.Sprintf(`window.scrollTo(%g, %g)`, x, y)
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, nil)); err != nil {
			return err
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(500*time.Millisecond))
		var info string
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			`JSON.stringify({y: window.scrollY, max: document.body.scrollHeight, vh: window.innerHeight})`,
			&info,
		)); err != nil {
			return err
		}
		var st struct {
			Y, Max, VH int `json:"y,max,vh"`
		}
		_ = json.Unmarshal([]byte(info), &st)
		hasMore = st.Y+st.VH < st.Max
		return chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{HasMore: hasMore}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}

func actionGetState(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	timeout := resolveRequestTimeout(req)
	var state *PageState
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		var e error
		state, e = extractCDPPageState(ctx)
		return e
	})
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &PageState{URL: session.CurrentURL}
	}
	return &SpiderWebDataResponse{
		URL:       state.URL,
		Title:     state.Title,
		CrawlTime: time.Now().UTC(),
		PageState: state,
		HasMore:   true,
	}, nil
}

func actionKeypress(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	key, _ := action.Parameters["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("keypress: params.key required")
	}
	if session.CurrentURL == "" {
		// v2.0.21 改进：可恢复路径（参见问题分析报告_20260630_220512 §1.4）
		return nil, fmt.Errorf("keypress: no current page in session (session=%s); "+
			"call action.navigate first to populate currentURL, or drop session_id to start a new session",
			session.SessionID)
	}
	timeout := resolveRequestTimeout(req)

	modifiersRaw, _ := action.Parameters["modifiers"].([]interface{})
	var modifiers []string
	for _, m := range modifiersRaw {
		if s, ok := m.(string); ok {
			modifiers = append(modifiers, s)
		}
	}

	var postHTML, postTitle, currentURL string
	// v2.0.11: 改用 runWithWatchdog，避免 React 高频重渲染导致后续 OuterHTML 等待挂死
	err := runWithWatchdog(session, engine, timeout, 1500, func(ctx context.Context) error {
		keyAction := chromedp.KeyEvent(key)
		if len(modifiers) > 0 {
			modMap := map[string]int{"ctrl": 1 << 0, "alt": 1 << 1, "shift": 1 << 2, "meta": 1 << 3}
			modVal := 0
			for _, m := range modifiers {
				if v, ok := modMap[m]; ok {
					modVal |= v
				}
			}
			keyAction = chromedp.KeyEvent(key, chromedp.KeyModifiers(input.Modifier(modVal)))
		}
		if err := chromedp.Run(ctx, keyAction); err != nil {
			return err
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(500*time.Millisecond))
		return chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}

func actionSwitchFrame(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	if session.CurrentURL == "" {
		// v2.0.21 改进
		return nil, fmt.Errorf("switch_frame: no current page in session (session=%s); "+
			"call action.navigate first, or drop session_id to start a new session",
			session.SessionID)
	}
	selector, _ := action.Parameters["selector"].(string)
	idxRaw, _ := action.Parameters["index"].(float64)
	idx := int(idxRaw)
	reset, _ := action.Parameters["reset"].(bool)

	if !reset && selector == "" && idx < 0 {
		return nil, fmt.Errorf("switch_frame: params.selector, params.index, or params.reset required")
	}

	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if reset {
			return chromedp.Run(ctx,
				chromedp.Reload(),
				chromedp.Sleep(500*time.Millisecond),
				chromedp.OuterHTML("html", &postHTML),
				chromedp.Title(&postTitle),
				chromedp.Evaluate(`location.href`, &currentURL),
			)
		}
		var jsGetHTML string
		if selector != "" {
			jsGetHTML = fmt.Sprintf(`(function(){
			  const el = document.querySelector(%q);
			  if (!el || !el.contentDocument) return '';
			  return el.contentDocument.documentElement.outerHTML;
			})()`, selector)
		} else {
			jsGetHTML = fmt.Sprintf(`(function(){
			  const frames = window.frames;
			  if (%d >= frames.length) return '';
			  try { return frames[%d].document.documentElement.outerHTML; } catch(e) { return ''; }
			})()`, idx, idx)
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(jsGetHTML, &postHTML)); err != nil {
			return err
		}
		if postHTML == "" {
			return fmt.Errorf("switch_frame: frame not found")
		}
		return chromedp.Run(ctx,
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}

func actionDragAndDrop(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sourceSel, _ := action.Parameters["source"].(string)
	targetSel, _ := action.Parameters["target"].(string)
	if sourceSel == "" || targetSel == "" {
		return nil, fmt.Errorf("drag_and_drop: params.source and params.target required")
	}
	if session.CurrentURL == "" {
		// v2.0.21 改进
		return nil, fmt.Errorf("drag_and_drop: no current page in session (session=%s); "+
			"call action.navigate first, or drop session_id to start a new session",
			session.SessionID)
	}
	sourceParsed := parseSelector(sourceSel)
	targetParsed := parseSelector(targetSel)
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if sourceParsed.Strategy == selText || targetParsed.Strategy == selText {
			return fmt.Errorf("drag_and_drop: text selector not supported")
		}
		sourceBy := chromedp.ByQuery
		if sourceParsed.Strategy == selXPath {
			sourceBy = chromedp.BySearch
		}
		targetBy := chromedp.ByQuery
		if targetParsed.Strategy == selXPath {
			targetBy = chromedp.BySearch
		}

		var sourceNodes, targetNodes []*cdp.Node
		if err := chromedp.Run(ctx, chromedp.Nodes(sourceParsed.Query, &sourceNodes, sourceBy)); err != nil {
			return err
		}
		if len(sourceNodes) == 0 {
			return fmt.Errorf("drag_and_drop: source not found: %s", sourceSel)
		}
		if err := chromedp.Run(ctx, chromedp.Nodes(targetParsed.Query, &targetNodes, targetBy)); err != nil {
			return err
		}
		if len(targetNodes) == 0 {
			return fmt.Errorf("drag_and_drop: target not found: %s", targetSel)
		}
		var sourceBox, targetBox *dom.BoxModel
		if err := chromedp.Run(ctx, chromedp.Dimensions(sourceNodes[0], &sourceBox)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.Dimensions(targetNodes[0], &targetBox)); err != nil {
			return err
		}

		var sx, sy, tx, ty float64
		if sourceBox != nil && len(sourceBox.Border) >= 8 {
			sx = (sourceBox.Border[0] + sourceBox.Border[4]) / 2
			sy = (sourceBox.Border[1] + sourceBox.Border[5]) / 2
		} else {
			return fmt.Errorf("drag_and_drop: unable to get source element dimensions")
		}
		if targetBox != nil && len(targetBox.Border) >= 8 {
			tx = (targetBox.Border[0] + targetBox.Border[4]) / 2
			ty = (targetBox.Border[1] + targetBox.Border[5]) / 2
		} else {
			return fmt.Errorf("drag_and_drop: unable to get target element dimensions")
		}

		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mousePressed"), sx, sy)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseMoved"), tx, ty)); err != nil {
			return err
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(200*time.Millisecond))
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseReleased"), tx, ty)); err != nil {
			return err
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(500*time.Millisecond))
		return chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}
