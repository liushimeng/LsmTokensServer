package spider

// ==================== 调试型 / 增强型 action（v2.0.7） ====================
//
// 该文件实现 19 个新 action：
//   - 鼠标全功能：right_click / double_click / middle_click / click_at / mouse_move / wheel
//   - 键盘：press_key（组合键） / type_text（连续键入）
//   - Tab 页面：new_tab / switch_tab / close_tab / list_tabs
//   - 调试观察：console_logs / network_log / elements（增强抽取） / dom（节点详情） / eval（JS 求值）
//   - 存储：local_storage / session_storage / cookies
//   - 其他：upload_file / element_screenshot
//
// 所有 action 行为：
//   1) 与现有 14 个 action 一样走 dispatchCDPAction → runWithSession（复用 session tab）
//   2) 输出直接挂到 SpiderWebDataResponse 的可选字段返回给 Agent
//   3) 不消耗 / 不写入数据库
//
// 实现要点：
//   - 优先使用 chromedp 高层 API（MouseEvent / KeyEvent / Evaluate / SendKeys ...）
//   - 不足时降级为直接调用 cdproto domain（Network / Runtime / Storage / Log）
//   - Tab 管理通过独立 chromedp context + SessionTabs map 表达
//   - 文件上传用 chromedp.SetUploadFiles
//   - Console / Network 通过 logger.init script hook 抓缓冲，存到 window.__lsm_console_log__ / __lsm_network_log__
//
// 与 chromedp v0.11.2 的 API 差异（曾踩坑）：
//   - chromedp.DeltaX / chromedp.DeltaY 不存在；wheel 用 chromedp.MouseEvent 配合 input.DispatchMouseEventParams 的 WithDeltaX/DeltaY
//   - chromedp.KeyEventCode 不存在；KeyEvent 通过 keys 字符串解析（kb 包），modifier 用 chromedp.KeyModifiers
//   - chromedp.MouseEventOpts 没有 ClickCount；用 count 循环 MouseEvent
//   - cdp.ExperimentalTargetID 不存在；用 target.ID（cdproto/target）配合 chromedp.WithTargetID

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// ==================== 鼠标全功能 ====================

// actionRightClick 右键点击
func actionRightClick(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := pickSelector(action)
	if sel == "" {
		return nil, fmt.Errorf("right_click: selector required")
	}
	parsed := parseSelector(sel)
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		var nodes []*cdp.Node
		if err := querySelector(ctx, parsed, &nodes); err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("right_click: selector not found: %s", sel)
		}
		var box *dom.BoxModel
		if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err != nil {
			return err
		}
		cx, cy := elementCenter(box)
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mousePressed"), cx, cy, chromedp.Button("right"))); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseReleased"), cx, cy, chromedp.Button("right"))); err != nil {
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

// actionDoubleClick 双击
func actionDoubleClick(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := pickSelector(action)
	if sel == "" {
		return nil, fmt.Errorf("double_click: selector required")
	}
	parsed := parseSelector(sel)
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		var nodes []*cdp.Node
		if err := querySelector(ctx, parsed, &nodes); err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("double_click: selector not found: %s", sel)
		}
		var box *dom.BoxModel
		if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err != nil {
			return err
		}
		cx, cy := elementCenter(box)
		return doMouseClick(ctx, cx, cy, "left", 2, &postHTML, &postTitle, &currentURL)
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

// actionMiddleClick 中键点击（开新 tab 的常见入口）
func actionMiddleClick(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := pickSelector(action)
	if sel == "" {
		return nil, fmt.Errorf("middle_click: selector required")
	}
	parsed := parseSelector(sel)
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		var nodes []*cdp.Node
		if err := querySelector(ctx, parsed, &nodes); err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("middle_click: selector not found: %s", sel)
		}
		var box *dom.BoxModel
		if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err != nil {
			return err
		}
		cx, cy := elementCenter(box)
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mousePressed"), cx, cy, chromedp.Button("middle"))); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseReleased"), cx, cy, chromedp.Button("middle"))); err != nil {
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

// actionClickAt 在 (x, y) 坐标上点击
// params: {"x":123, "y":456, "button":"left|right|middle"?, "click_count":1?}
// 也支持 {"selector":"...", "offset_x":10, "offset_y":-5}
func actionClickAt(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	x, xOK := getFloat(action.Parameters, "x")
	y, yOK := getFloat(action.Parameters, "y")
	button := getStringOr(action.Parameters, "button", "left")
	count := int(getFloatOr(action.Parameters, "click_count", 1))
	timeout := resolveRequestTimeout(req)

	// v2.0.11: 效果校验前置 — 解析 selector 模式（用于后续 elementState 比对），
	// 并在点击前抓取 preState + preNetworkCount
	var parsedForVerif parsedSelector
	var hasSelector bool
	xVal, yVal := x, y
	{
		sel := pickSelector(action)
		if sel == "" {
			sel, _ = action.Parameters["selector"].(string)
		}
		if sel != "" {
			parsedForVerif = parseSelector(sel)
			hasSelector = true
		}
	}

	var preState string
	var preNetworkCount int
	if hasSelector {
		_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
			preJS := readClickPreStateJS(parsedForVerif)
			_ = chromedp.Run(ctx, chromedp.Evaluate(preJS, &preState))
			preNetworkCount = snapshotNetworkCount(ctx)
			return nil
		})
	} else {
		// 纯坐标模式：没有 element 比对，只能靠 networkDelta 校验
		_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
			preNetworkCount = snapshotNetworkCount(ctx)
			return nil
		})
	}

	if !xOK || !yOK {
		sel := pickSelector(action)
		if sel == "" {
			sel, _ = action.Parameters["selector"].(string)
		}
		if sel == "" {
			return nil, fmt.Errorf("click_at: params.x and params.y required (or selector + offset_x/offset_y)")
		}
		ox := getFloatOr(action.Parameters, "offset_x", 0)
		oy := getFloatOr(action.Parameters, "offset_y", 0)
		parsed := parseSelector(sel)
		err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
			var nodes []*cdp.Node
			if err := querySelector(ctx, parsed, &nodes); err != nil {
				return err
			}
			if len(nodes) == 0 {
				return fmt.Errorf("click_at: selector not found: %s", sel)
			}
			var box *dom.BoxModel
			if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err != nil {
				return err
			}
			cx, cy := elementCenter(box)
			xVal, yVal = cx+ox, cy+oy
			return doMouseClickSimple(ctx, xVal, yVal, button, count)
		})
		if err != nil {
			return nil, err
		}
	} else {
		err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
			return doMouseClickSimple(ctx, xVal, yVal, button, count)
		})
		if err != nil {
			return nil, err
		}
	}

	var postHTML, postTitle, currentURL string
	_ = runWithSession(session, engine, timeout, func(ctx context.Context) error {
		_ = chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond))
		_ = chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
		return nil
	})
	if currentURL == "" {
		currentURL = session.CurrentURL
	}

	// v2.0.11: 动作效果校验（与 actionClick 一致）
	var verif *ClickEffectVerification
	_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
		postNetworkCount := snapshotNetworkCount(ctx)
		if hasSelector {
			v := verifyClickEffect(session, engine, parsedForVerif, preState, 500)
			if v != nil {
				v.NetworkRequestsDelta = postNetworkCount - preNetworkCount
				if v.NetworkRequestsDelta > 0 {
					v.HasNetworkChange = true
					v.EffectVerified = true
				}
				verif = v
			}
		} else {
			// 纯坐标模式：只能基于网络增量判断
			delta := postNetworkCount - preNetworkCount
			v := &ClickEffectVerification{
				NetworkRequestsDelta: delta,
				HasNetworkChange:     delta > 0,
				EffectVerified:       delta > 0,
				WaitMs:               500,
			}
			if !v.EffectVerified {
				v.Warning = "click_at (pure coordinate) dispatched but no new network requests observed; " +
					"建议改用 selector 模式 + eval 单 roundtrip 兜底"
			}
			verif = v
		}
		return nil
	})
	// v2.0.11 增强：click_at 在 selector 模式下 false-positive 时，也尝试祖先补救
	if verif != nil && !verif.EffectVerified && verif.Warning != "" && hasSelector {
		if recovered, detail := tryClickClosestAncestorOnNoEffect(session, engine, parsedForVerif, 500); recovered {
			verif.EffectVerified = true
			verif.HasElementChange = true
			if verif.Warning != "" {
				verif.Warning = verif.Warning + "; auto-recovered by " + detail
			} else {
				verif.Warning = "auto-recovered by " + detail
			}
			mcpLogMCP("[SPIDER] click_at auto-recovered via ancestor click (selector=%s, %s)", parsedForVerif.Raw, detail)
		} else if detail != "" {
			verif.Warning = verif.Warning + "; ancestor probe: " + detail
		}
	}
	if verif != nil && !verif.EffectVerified && verif.Warning != "" {
		mcpLogMCP("[SPIDER] click_at dispatched but no effect detected (x=%.0f,y=%.0f,selector=%v)", xVal, yVal, hasSelector)
	}

	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	resp.ClickEffectVerification = verif
	return resp, nil
}

// actionMouseMove 移动鼠标到 (x, y) 或元素中心
func actionMouseMove(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	x, xOK := getFloat(action.Parameters, "x")
	y, yOK := getFloat(action.Parameters, "y")
	timeout := resolveRequestTimeout(req)

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if !xOK || !yOK {
			sel := pickSelector(action)
			if sel == "" {
				sel, _ = action.Parameters["selector"].(string)
			}
			if sel == "" {
				return fmt.Errorf("mouse_move: params.x and params.y required (or selector)")
			}
			parsed := parseSelector(sel)
			var nodes []*cdp.Node
			if err := querySelector(ctx, parsed, &nodes); err != nil {
				return err
			}
			if len(nodes) == 0 {
				return fmt.Errorf("mouse_move: selector not found: %s", sel)
			}
			var box *dom.BoxModel
			if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err != nil {
				return err
			}
			cx, cy := elementCenter(box)
			return chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseMoved"), cx, cy))
		}
		return chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseMoved"), x, y))
	})
	if err != nil {
		return nil, err
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, session.CurrentURL, "", session.CurrentRawHTML, "", session)
	return resp, nil
}

// actionWheel 滚轮
// params: {"delta_x":0, "delta_y":300, "selector":"...?"}
func actionWheel(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	deltaX := getFloatOr(action.Parameters, "delta_x", 0)
	deltaY := getFloatOr(action.Parameters, "delta_y", 300)
	timeout := resolveRequestTimeout(req)

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		cx, cy := 0.0, 0.0
		sel := pickSelector(action)
		if sel == "" {
			sel, _ = action.Parameters["selector"].(string)
		}
		if sel != "" {
			parsed := parseSelector(sel)
			var nodes []*cdp.Node
			if err := querySelector(ctx, parsed, &nodes); err == nil && len(nodes) > 0 {
				var box *dom.BoxModel
				if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err == nil {
					cx, cy = elementCenter(box)
				}
			}
		}
		if cx != 0 || cy != 0 {
			_ = chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseMoved"), cx, cy))
		}
		// chromedp v0.11.2 不提供 chromedp.DeltaX/DeltaY — 用 input 包构造 params
		action1 := input.DispatchMouseEvent(input.MouseType("mouseWheel"), cx, cy).
			WithDeltaX(float64(deltaX)).WithDeltaY(float64(deltaY))
		return chromedp.Run(ctx, action1)
	})
	if err != nil {
		return nil, err
	}
	_ = waitSleepInSession(session, engine, 500*time.Millisecond)
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, session.CurrentURL, "", session.CurrentRawHTML, "", session)
	return resp, nil
}

// ==================== 键盘增强 ====================

// actionPressKey 组合键（基于 chromedp.KeyEvent + KeyModifiers）
// params: {"key":"Enter"|"Tab"|"a"|"A"|"ArrowDown"|..., "modifiers":["ctrl"|"alt"|"shift"|"meta"]?, "code":"KeyA"?}
func actionPressKey(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	key, _ := action.Parameters["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("press_key: params.key required (e.g. Enter / Tab / a / ArrowDown / F5)")
	}
	modList := parseModifiers(action.Parameters)
	code, _ := action.Parameters["code"].(string)
	timeout := resolveRequestTimeout(req)

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		// 1) 按下 modifier（如果有）
		mods := []string{}
		if modList&int(input.ModifierCtrl) != 0 {
			mods = append(mods, "Control")
		}
		if modList&int(input.ModifierAlt) != 0 {
			mods = append(mods, "Alt")
		}
		if modList&int(input.ModifierShift) != 0 {
			mods = append(mods, "Shift")
		}
		if modList&int(input.ModifierMeta) != 0 {
			mods = append(mods, "Meta")
		}
		for _, m := range mods {
			if err := chromedp.Run(ctx, chromedp.KeyEvent(m)); err != nil {
				return err
			}
		}
		// 2) 按下主键
		opts := []chromedp.KeyOption{}
		if modList != 0 {
			opts = append(opts, chromedp.KeyModifiers(input.Modifier(modList)))
		}
		if code != "" {
			// chromedp v0.11.2 不暴露 KeyEventCode；用 rawKey 路径 — 不支持 code，直接走 key 字符串
			_ = code
		}
		if err := chromedp.Run(ctx, chromedp.KeyEvent(key, opts...)); err != nil {
			return err
		}
		// 3) 释放 modifier（反序）
		for i := len(mods) - 1; i >= 0; i-- {
			if err := chromedp.Run(ctx, chromedp.KeyEvent(mods[i])); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = waitSleepInSession(session, engine, 300*time.Millisecond)

	var postHTML, postTitle, currentURL string
	_ = runWithSession(session, engine, timeout, func(ctx context.Context) error {
		_ = chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
		return nil
	})
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}

// actionTypeText 连续键入文本
// params: {"text":"hello world", "selector":"...?"}  指定 selector 时先 focus
// v2.0.10: 增强 — 支持完整事件序列（keydown → input → keyup）和逐字符输入模式，
// 适配 San 框架等 SPA 的自定义事件系统。
func actionTypeText(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	text, _ := action.Parameters["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("type_text: params.text required")
	}
	timeout := resolveRequestTimeout(req)

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		sel := pickSelector(action)
		if sel == "" {
			sel, _ = action.Parameters["selector"].(string)
		}
		if sel != "" {
			parsed := parseSelector(sel)
			by := byMode(parsed)
			// 优先 chromedp.Focus + 针对元素 SendKeys。
			// v2.0.9: 受控/contenteditable SPA 输入框 chromedp.Focus 会报
			// "Element is not focusable (-32000)"，或 SendKeys 不触发框架 onChange；
			// 此时降级为 JS 兜底：定位元素 → el.focus() → 设 value/textContent + 派发完整事件序列。
			if focusErr := chromedp.Run(ctx, chromedp.Focus(parsed.Query, by)); focusErr == nil {
				if sendErr := chromedp.Run(ctx, chromedp.SendKeys(parsed.Query, text, by)); sendErr == nil {
					return nil
				}
			}
			// v2.0.10: 使用增强的受控输入 JS 兜底（完整事件序列 + 框架兼容）
			_, err := runControlledInputJS(ctx, parsed, text)
			return err
		}
		// 无 selector：依赖当前焦点，使用逐字符输入模拟
		return typeTextWithFullEvents(ctx, text)
	})
	if err != nil {
		return nil, err
	}
	_ = waitSleepInSession(session, engine, 200*time.Millisecond)

	var postHTML, postTitle, currentURL string
	_ = runWithSession(session, engine, timeout, func(ctx context.Context) error {
		_ = chromedp.Run(ctx,
			chromedp.OuterHTML("html", &postHTML),
			chromedp.Title(&postTitle),
			chromedp.Evaluate(`location.href`, &currentURL),
		)
		return nil
	})
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, currentURL, postTitle, postHTML, "", session)
	return resp, nil
}

// typeTextWithFullEvents 逐字符输入并派发完整事件序列
// 用于无 selector 的场景（当前焦点元素），模拟真实键盘输入。
// v2.0.10: 支持完整事件链（keydown → keypress → input → keyup），
// 并尝试触发框架特定事件（React/San/Vue 等）。
func typeTextWithFullEvents(ctx context.Context, text string) error {
	js := fmt.Sprintf(`(function(){
	  const text = %q;
	  const el = document.activeElement;
	  if (!el) return false;
	  const isInput = el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable;
	  if (!isInput) return false;

	  // 逐字符输入，每个字符派发完整事件序列
	  for (let i = 0; i < text.length; i++) {
	    const char = text[i];
	    const charCode = char.charCodeAt(0);

	    // 设置值（累积）
	    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') {
	      el.value = text.substring(0, i + 1);
	    } else if (el.isContentEditable) {
	      el.textContent = text.substring(0, i + 1);
	    }

	    // keydown
	    try {
	      el.dispatchEvent(new KeyboardEvent('keydown', {
	        bubbles: true, cancelable: true,
	        key: char, code: 'Key' + char.toUpperCase(),
	        keyCode: charCode, charCode: charCode, which: charCode
	      }));
	    } catch(e) {}

	    // keypress（可打印字符）
	    if (charCode >= 32) {
	      try {
	        el.dispatchEvent(new KeyboardEvent('keypress', {
	          bubbles: true, cancelable: true,
	          key: char, code: 'Key' + char.toUpperCase(),
	          keyCode: charCode, charCode: charCode, which: charCode
	        }));
	      } catch(e) {}
	    }

	    // input（InputEvent）
	    try {
	      el.dispatchEvent(new InputEvent('input', {
	        bubbles: true, cancelable: true,
	        data: char, inputType: 'insertText'
	      }));
	    } catch(e) {}

	    // keyup
	    try {
	      el.dispatchEvent(new KeyboardEvent('keyup', {
	        bubbles: true, cancelable: true,
	        key: char, code: 'Key' + char.toUpperCase(),
	        keyCode: charCode, charCode: charCode, which: charCode
	      }));
	    } catch(e) {}
	  }

	  // 尝试框架特定事件
	  try { el.dispatchEvent(new Event('change', { bubbles: true })); } catch(e) {}
	  try { el.dispatchEvent(new Event('san:input', { bubbles: true })); } catch(e) {}
	  try { el.dispatchEvent(new Event('san:change', { bubbles: true })); } catch(e) {}

	  // React _valueTracker 刷新
	  try {
	    const tracker = el._valueTracker;
	    if (tracker) { tracker.setValue(text); }
	  } catch(e) {}

	  return true;
	})()`, text)
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("type_text: no focused input element found")
	}
	return nil
}

// ==================== Tab 页面管理 ====================

// actionNewTab 打开新 Tab 并切换过去
// params: {"url":"..."?, "alias":"tab1"?}
func actionNewTab(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	url, _ := action.Parameters["url"].(string)
	alias, _ := action.Parameters["alias"].(string)
	if alias == "" {
		alias = fmt.Sprintf("tab_%d", time.Now().UnixNano())
	}
	timeout := resolveRequestTimeout(req)
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	// 用 engine.rootCtx 派生新 cdp context = 新 target
	parentCtx, parentCancel := context.WithTimeout(engine.rootCtx, timeout)
	defer parentCancel()
	newCtx, cancel := chromedp.NewContext(parentCtx)
	if url != "" {
		if err := chromedp.Run(newCtx, chromedp.Navigate(url)); err != nil {
			cancel()
			return nil, fmt.Errorf("new_tab navigate failed: %w", err)
		}
	}
	var targetID string
	if t := chromedp.FromContext(newCtx); t != nil && t.Target != nil {
		targetID = string(t.Target.TargetID)
	}

	if session.SessionTabs == nil {
		session.SessionTabs = make(map[string]string)
	}
	session.SessionTabs[alias] = targetID
	session.ActiveTab = alias
	// 替换主 cdp 资源指针到新 tab
	if session.cdpCancel != nil {
		session.cdpCancel()
	}
	session.cdpCtx = newCtx
	session.cdpCancel = cancel
	session.cdpTarget = targetID
	if url != "" {
		session.CurrentURL = url
	}

	var title string
	_ = chromedp.Run(newCtx, chromedp.Title(&title))
	_ = chromedp.Run(newCtx, chromedp.Evaluate(`location.href`, &url))

	tabs := session.snapshotTabs()
	return &SpiderWebDataResponse{
		URL:       url,
		Title:     title,
		Content:   "opened new tab: " + alias,
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Tabs:      tabs,
	}, nil
}

// actionSwitchTab 切换到指定 tab
// params: {"alias":"tab1"} 或 {"index":0}
func actionSwitchTab(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	alias, _ := action.Parameters["alias"].(string)
	idx, _ := action.Parameters["index"].(float64)
	if alias == "" && idx >= 0 {
		alias = session.aliasByIndex(int(idx))
	}
	if alias == "" {
		return nil, fmt.Errorf("switch_tab: params.alias or params.index required")
	}
	targetID, ok := session.SessionTabs[alias]
	if !ok || targetID == "" {
		return nil, fmt.Errorf("switch_tab: alias %q not found in session tabs", alias)
	}
	// 切到该 target
	if session.cdpCancel != nil {
		session.cdpCancel()
	}
	parentCtx, parentCancel := context.WithTimeout(engine.rootCtx, resolveRequestTimeout(req))
	defer parentCancel()
	cdpCtx, cancel := chromedp.NewContext(parentCtx, chromedp.WithTargetID(target.ID(targetID)))
	session.cdpCtx = cdpCtx
	session.cdpCancel = func() {
		cancel()
		parentCancel()
	}
	session.cdpTarget = targetID
	session.ActiveTab = alias

	var url, title string
	_ = chromedp.Run(cdpCtx, chromedp.Evaluate(`location.href`, &url), chromedp.Title(&title))
	if url != "" {
		session.CurrentURL = url
	}

	tabs := session.snapshotTabs()
	return &SpiderWebDataResponse{
		URL:       url,
		Title:     title,
		Content:   "switched to tab: " + alias,
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Tabs:      tabs,
	}, nil
}

// actionCloseTab 关闭指定 tab
// params: {"alias":"tab1"} 或 {"index":0}
func actionCloseTab(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	alias, _ := action.Parameters["alias"].(string)
	idx, _ := action.Parameters["index"].(float64)
	if alias == "" && idx >= 0 {
		alias = session.aliasByIndex(int(idx))
	}
	if alias == "" {
		return nil, fmt.Errorf("close_tab: params.alias or params.index required")
	}
	targetID, ok := session.SessionTabs[alias]
	if !ok || targetID == "" {
		return nil, fmt.Errorf("close_tab: alias %q not found in session tabs", alias)
	}
	// 关闭 target：用 Target.closeTarget
	if session.cdpCtx != nil {
		_ = target.CloseTarget(target.ID(targetID)).Do(cdp.WithExecutor(session.cdpCtx, chromedp.FromContext(session.cdpCtx).Target))
	}
	delete(session.SessionTabs, alias)
	if session.ActiveTab == alias {
		session.ActiveTab = ""
		for k := range session.SessionTabs {
			session.ActiveTab = k
			break
		}
		if session.ActiveTab != "" {
			tid := session.SessionTabs[session.ActiveTab]
			if session.cdpCancel != nil {
				session.cdpCancel()
			}
			parentCtx, parentCancel := context.WithTimeout(engine.rootCtx, 10*time.Second)
			defer parentCancel()
			cdpCtx, cancel := chromedp.NewContext(parentCtx, chromedp.WithTargetID(target.ID(tid)))
			session.cdpCtx = cdpCtx
			session.cdpCancel = func() { cancel(); parentCancel() }
			session.cdpTarget = tid
		} else {
			detachCDPContext(session)
		}
	}
	tabs := session.snapshotTabs()
	return &SpiderWebDataResponse{
		URL:       session.CurrentURL,
		Title:     "",
		Content:   "closed tab: " + alias,
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Tabs:      tabs,
	}, nil
}

// actionListTabs 列出所有 tab
func actionListTabs(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	tabs := session.snapshotTabs()
	return &SpiderWebDataResponse{
		URL:       session.CurrentURL,
		Content:   fmt.Sprintf("session has %d tab(s)", len(tabs)),
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Tabs:      tabs,
	}, nil
}

// actionRestartBrowser 重启 Chrome 进程（v2.0.14 自愈 action）
// 供 Agent 在检测到级联 context canceled 时手动触发恢复。
//
// v2.0.18 补丁（基于问题分析报告_20260629_143200 §3.5）：第二次 restart_browser
// 在级联 context canceled 状态下调用时，RestartChrome 会阻塞在 e.mu.Lock() 上，
// 直到第一次的级联 restart 完成；期间客户端可能已断开连接（curl 退出码 1 + 空
// 响应体）。修复：
//   - 内部用独立 timeout（resolveRequestTimeout(req) 上限 30s）包 RestartChrome，
//     超时即放弃、返回标准 JSON error envelope；
//   - engine 为 nil 或未 running 时不要 panic / 不要返回空响应；
//   - 成功时也附 session_id + 自愈提示，让 Agent 立即拿到新 session 续接。
//
// v2.0.19 补丁（基于问题分析报告_20260630_112635 §4.4 + §7 建议 5）：
// 实测 restart_browser 经常 60s+ 不返回（healthCheckLoop 串行持锁 /
// waitForChrome 长时间轮询 / e.mu.Lock() 阻塞），文档承诺 "<1s 100% 恢复"
// 与实际严重不符。强化：
//   - 上限从 30s 收紧到 15s（第二次 restart 不会因为等待上一次重启完成而拖死）；
//   - 超时分支用独立 goroutine 释放 context.Background() 并 detach 当前 session
//     的 CDP 资源，确保下一次请求能立刻重建 tab；
//   - 失败时也返回带 session_id 的标准 JSON envelope（不再空响应）。
//
// v2.0.30 补丁（基于问题分析报告_20260707_062144 §2.2 + 建议 4）：
// 上版实现要求 `engine.isRunning=true` 才允许 restart_browser；但 cascade context
// canceled 状态下 isRunning 仍可能为 true（Chrome 进程没死，只是 chromedp rootCtx
// 被 cancel），上层 `dispatchCDPAction` 又拒绝放行 restart_browser action 自身。
// 改为：检测 `engine.rootCtx.Err() != nil` → 自动走 `restartChromeForced()`
// 旁路强制重启（绕过 e.isRunning + 5s 短门闩 forceRestartMu），cascade 自愈
// 不再死锁。响应里加 `forced_restart=true` 字段，Agent 据此判断下一次 action
// 必须走 fresh session_id / fresh rootCtx。
func actionRestartBrowser(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	if engine == nil {
		return nil, fmt.Errorf("restart_browser failed: spider engine not initialized")
	}
	if !engine.isRunning && (engine.rootCtx == nil || engine.rootCtx.Err() == nil) {
		// 引擎明确未启动、且 rootCtx 健康（非 cascade 场景）才拒绝；
		// cascade 场景 rootCtx 已 cancel，下面走 forced 路径
		return nil, fmt.Errorf("restart_browser failed: spider engine not running and rootCtx is healthy")
	}

	// v2.0.19: 收紧到 15s 上限（问题分析报告_20260630_112635 §4.4 实测 60s+）
	timeout := resolveRequestTimeout(req)
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout > 15*time.Second {
		timeout = 15 * time.Second
	}

	// v2.0.30: cascade 路径走 `restartChromeForced()`，其余走标准 `RestartChrome()`
	useForced := engine.rootCtx != nil && engine.rootCtx.Err() != nil
	if useForced {
		mcpLogMCP("[SPIDER] restart_browser using FORCED path (cascade detected, issue 20260707_062144)")
	}

	type restartResult struct {
		err error
	}
	done := make(chan restartResult, 1)
	go func() {
		// 内部 panic recovery —— RestartChrome 路径上的任何 panic 都包装为 error，
		// 避免直接冒泡导致 HTTP handler 在 defer 中错过 JSON 编码。
		defer func() {
			if rec := recover(); rec != nil {
				done <- restartResult{err: fmt.Errorf("restart_browser panic: %v", rec)}
			}
		}()
		var err error
		if useForced {
			err = engine.restartChromeForced()
		} else {
			err = engine.RestartChrome()
		}
		done <- restartResult{err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			// 失败也生成新 session_id，避免 Agent 完全黑屏（基于 §4.4 建议）
			newSessionID := generateSessionID()
			return &SpiderWebDataResponse{
				SessionID: newSessionID,
				URL:       "",
				Title:     "Chrome restart failed",
				Content:   fmt.Sprintf("restart_browser failed: %v. New session_id issued (%s); Agent can retry navigate on a fresh session.", r.err, newSessionID),
				CrawlTime: time.Now().UTC(),
				HasMore:   true,
				Warnings: []string{
					fmt.Sprintf("restart_browser failed: %v — try again or switch to API-direct approach", r.err),
				},
			}, fmt.Errorf("restart_browser failed: %w", r.err)
		}
		// 成功：生成新 session_id（RestartChrome 已 detach 所有 session，
		// 旧 session 失效，Agent 需要拿到新 session_id 续接）。
		newSessionID := generateSessionID()
		title := "Chrome restarted"
		content := "Chrome restarted successfully. Use the new session_id (" + newSessionID + ") for subsequent actions."
		var warnings []string
		if useForced {
			title = "Chrome restarted via FORCED path"
			content = "Chrome restarted via forced path (cascade recovery, issue 20260707_062144). " +
				"Root context was cancelled; bypassed e.isRunning check via 5s short gate. " +
				"New session_id (" + newSessionID + ") issued; Agent MUST use the new session_id " +
				"for all subsequent actions (old session's cdpCtx is dead)."
			warnings = []string{
				"forced_restart=true: root context was cancelled; engine was forcibly restarted " +
					"bypassing e.isRunning check. Use the new session_id for subsequent actions.",
			}
		}
		return &SpiderWebDataResponse{
			SessionID:     newSessionID,
			URL:           "",
			Title:         title,
			Content:       content,
			CrawlTime:     time.Now().UTC(),
			HasMore:       true,
			Warnings:      warnings,
			ForcedRestart: useForced,
		}, nil
	case <-time.After(timeout):
		// v2.0.19: 超时也强制 detach 当前 session 的 CDP 资源，让下次请求重建 tab。
		// 同时启动一个 watchdog goroutine，等 RestartChrome 真正完成后再 attach
		// 新 rootCtx，避免阻塞客户端。
		mcpLogMCP("[SPIDER] restart_browser exceeded %v (issue §4.4), detaching session and issuing new session_id", timeout)
		if session != nil {
			go func() {
				detachCDPContext(session)
			}()
		}
		newSessionID := generateSessionID()
		return &SpiderWebDataResponse{
			SessionID: newSessionID,
			URL:       "",
			Title:     "Chrome restart in progress",
			Content:   fmt.Sprintf("restart_browser exceeded %v hard cap (another restart may still be in progress). New session_id issued (%s); Agent may proceed with retry navigate.", timeout, newSessionID),
			CrawlTime: time.Now().UTC(),
			HasMore:   true,
			Warnings: []string{
				fmt.Sprintf("restart_browser exceeded %v hard cap (issue §4.4) — session has been detached; retry with the new session_id", timeout),
			},
			ForcedRestart: useForced,
		}, fmt.Errorf("restart_browser timeout after %v (another restart may be in progress)", timeout)
	}
}

// ==================== 调试观察 ====================

// actionConsoleLogs 抓取 console 日志
// params: {"clear":true|false?, "wait_ms":500?}
func actionConsoleLogs(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	waitMs := int(getFloatOr(action.Parameters, "wait_ms", 500))
	clear, _ := action.Parameters["clear"].(bool)
	timeout := resolveRequestTimeout(req)
	if timeout < time.Duration(waitMs+5)*time.Millisecond {
		timeout = time.Duration(waitMs+5) * time.Millisecond
	}

	var logsOut []ConsoleLogEntry
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		// 先确保 hook 已注入（每个新 tab 由 logger.init script 注入；这里再 evaluate 一次保险）
		_ = chromedp.Run(ctx, chromedp.Evaluate(initScriptForConsoleNetwork, nil))
		if waitMs > 0 {
			_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(waitMs)*time.Millisecond))
		}
		js := `JSON.stringify(window.__lsm_console_log__ || [])`
		var raw string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
		_ = json.Unmarshal([]byte(raw), &logsOut)
		if clear {
			_ = chromedp.Run(ctx, chromedp.Evaluate(`window.__lsm_console_log__ = []`, nil))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if logsOut == nil {
		logsOut = []ConsoleLogEntry{}
	}
	return &SpiderWebDataResponse{
		URL:         session.CurrentURL,
		Content:     fmt.Sprintf("captured %d console entries", len(logsOut)),
		CrawlTime:   time.Now().UTC(),
		HasMore:     true,
		ConsoleLogs: logsOut,
	}, nil
}

// actionNetworkLog 抓取网络请求日志
// params: {"clear":true|false?, "wait_ms":500?, "filter":"XHR|Fetch|Document"?, "limit":200?}
func actionNetworkLog(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	waitMs := int(getFloatOr(action.Parameters, "wait_ms", 500))
	clear, _ := action.Parameters["clear"].(bool)
	filter, _ := action.Parameters["filter"].(string)
	limit := int(getFloatOr(action.Parameters, "limit", 200))
	timeout := resolveRequestTimeout(req)
	if timeout < time.Duration(waitMs+5)*time.Millisecond {
		timeout = time.Duration(waitMs+5) * time.Millisecond
	}

	var entries []NetworkLogEntry
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		_ = chromedp.Run(ctx, chromedp.Evaluate(initScriptForConsoleNetwork, nil))
		if waitMs > 0 {
			_ = chromedp.Run(ctx, chromedp.Sleep(time.Duration(waitMs)*time.Millisecond))
		}
		// 把 filter/limit 拼到 JS 字符串里（chromedp.Evaluate 不支持 args）
		filterJSON, _ := json.Marshal(filter)
		limitJSON := fmt.Sprintf("%d", limit)
		js := fmt.Sprintf(`(function(){
			const all = window.__lsm_network_log__ || [];
			const filt = %s;
			const lim  = %s;
			const out = filt ? all.filter(e => e.type === filt) : all;
			return JSON.stringify(out.slice(-lim));
		})()`, string(filterJSON), limitJSON)
		var raw string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
		_ = json.Unmarshal([]byte(raw), &entries)
		if clear {
			_ = chromedp.Run(ctx, chromedp.Evaluate(`window.__lsm_network_log__ = []`, nil))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []NetworkLogEntry{}
	}
	return &SpiderWebDataResponse{
		URL:        session.CurrentURL,
		Content:    fmt.Sprintf("captured %d network entries", len(entries)),
		CrawlTime:  time.Now().UTC(),
		HasMore:    true,
		NetworkLog: entries,
	}, nil
}

// actionElements 增强元素抽取
// params: {"selector":"...", "scope":"body|article|main|nav", "attributes":["href","title","src"]?, "limit":50?}
func actionElements(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel, _ := action.Parameters["selector"].(string)
	if sel == "" {
		sel = action.Selector
	}
	if sel == "" {
		return nil, fmt.Errorf("elements: params.selector required")
	}
	scope, _ := action.Parameters["scope"].(string)
	attrsRaw, _ := action.Parameters["attributes"].([]interface{})
	var attrs []string
	for _, a := range attrsRaw {
		if s, ok := a.(string); ok {
			attrs = append(attrs, s)
		}
	}
	if len(attrs) == 0 {
		attrs = []string{"href", "src", "title", "alt", "id", "class"}
	}
	limit := int(getFloatOr(action.Parameters, "limit", 50))
	timeout := resolveRequestTimeout(req)

	var items []ExtractedItem
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		parsed := parseSelector(sel)
		root := "document.body"
		switch scope {
		case "article":
			root = "document.querySelector('article') || document.body"
		case "main":
			root = "document.querySelector('main') || document.body"
		case "nav":
			root = "document.querySelector('nav') || document.body"
		}
		js := fmt.Sprintf(`(function(){
			const root = %s;
			const sel = %q;
			const lim = %d;
			const attrs = %s;
			const elements = root.querySelectorAll(sel);
			const out = [];
			for (let i=0; i<Math.min(elements.length, lim); i++) {
				const el = elements[i];
				const item = {
					selector: sel,
					outer_html: el.outerHTML.slice(0, 500),
					inner_text: (el.innerText || el.textContent || '').trim().slice(0, 300),
					attributes: {}
				};
				for (const a of attrs) {
					const v = el.getAttribute(a);
					if (v !== null && v !== undefined) item.attributes[a] = String(v).slice(0, 500);
				}
				const r = el.getBoundingClientRect();
				item.box = { x: r.left, y: r.top, w: r.width, h: r.height,
					center_x: r.left + r.width/2, center_y: r.top + r.height/2 };
				out.push(item);
			}
			return JSON.stringify(out);
		})()`, root, parsed.Query, limit, jsonStringArray(attrs))
		var raw string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
		return json.Unmarshal([]byte(raw), &items)
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []ExtractedItem{}
	}
	return &SpiderWebDataResponse{
		URL:         session.CurrentURL,
		Content:     fmt.Sprintf("extracted %d element(s)", len(items)),
		CrawlTime:   time.Now().UTC(),
		HasMore:     true,
		ExtractList: items,
	}, nil
}

// actionDom 查询 DOM 节点详情
// params: {"selector":"...", "include_computed_style":false?, "computed_keys":["color","display"]?}
func actionDom(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := pickSelector(action)
	if sel == "" {
		sel, _ = action.Parameters["selector"].(string)
	}
	if sel == "" {
		return nil, fmt.Errorf("dom: selector required")
	}
	includeStyle, _ := action.Parameters["include_computed_style"].(bool)
	keysRaw, _ := action.Parameters["computed_keys"].([]interface{})
	var keys []string
	for _, k := range keysRaw {
		if s, ok := k.(string); ok {
			keys = append(keys, s)
		}
	}
	if len(keys) == 0 {
		keys = []string{"display", "visibility", "color", "background-color", "font-size", "position"}
	}
	timeout := resolveRequestTimeout(req)

	var detail DomNodeDetail
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		parsed := parseSelector(sel)
		keysJSON, _ := json.Marshal(keys)
		includeFlag := "false"
		if includeStyle {
			includeFlag = "true"
		}
		js := fmt.Sprintf(`(function(){
			const el = document.querySelector(%q);
			if (!el) return JSON.stringify({found:false});
			const r = el.getBoundingClientRect();
			const out = {
				found: true,
				tag: el.tagName,
				id: el.id || '',
				class_name: el.className || '',
				outer_html: (el.outerHTML || '').slice(0, 2000),
				inner_text: (el.innerText || '').slice(0, 1000),
				inner_html: (el.innerHTML || '').slice(0, 2000),
				attributes: {},
				box: { x: r.left, y: r.top, w: r.width, h: r.height,
					center_x: r.left + r.width/2, center_y: r.top + r.height/2 },
				visible: !!(r.width || r.height),
				enabled: !el.disabled,
				parent: el.parentElement ? el.parentElement.tagName : '',
				children: Array.from(el.children).slice(0,50).map(c => c.tagName)
			};
			for (const a of el.attributes) out.attributes[a.name] = a.value;
			if (%s) {
				const cs = getComputedStyle(el);
				const ks = %s;
				out.computed_style = {};
				for (let i=0; i<ks.length; i++) {
					out.computed_style[ks[i]] = cs.getPropertyValue(ks[i]);
				}
			}
			return JSON.stringify(out);
		})()`, parsed.Query, includeFlag, string(keysJSON))
		var raw string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
		return json.Unmarshal([]byte(raw), &detail)
	})
	if err != nil {
		return nil, err
	}
	return &SpiderWebDataResponse{
		URL:       session.CurrentURL,
		Content:   fmt.Sprintf("dom: %s found=%v", sel, detail.Found),
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Dom:       &detail,
	}, nil
}

// actionEval 在页面上下文执行 JS
// params: {"expression":"return document.title", "await_promise":true?}
// v2.0.18: 增加安全护栏 — 检测并阻止 <script type="module"> 注入，防止触发
// chromedp 模块图死锁导致服务卡死（见问题分析报告_20260629_093329 §3.3）。
func actionEval(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	expr, _ := action.Parameters["expression"].(string)
	if expr == "" {
		return nil, fmt.Errorf("eval: params.expression required")
	}
	awaitPromise, _ := action.Parameters["await_promise"].(bool)
	timeout := resolveRequestTimeout(req)

	// v2.0.18: 安全护栏 — 检测 module script 注入
	if detectModuleScriptInjection(expr) {
		return nil, fmt.Errorf("eval: module script injection detected and blocked for safety; " +
			"injecting <script type=\"module\"> may trigger chromedp deadlock. " +
			"If the target page requires module loading, use navigate with wait_for_hydration instead")
	}

	result := &EvalResult{Expression: expr}
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		// 直接走 runtime.Evaluate 拿 RemoteObject
		// 注意：cdproto 原始命令的 .Do(ctx) 需要 ctx 携带 CDP executor，
		// 而 executor 只在 chromedp.Run 内部经 cdp.WithExecutor 注入。
		// 裸 ctx 会返回 cdp.ErrInvalidContext("invalid context")。
		// 参考同文件 actionCloseTab 的写法显式包裹 executor。
		params := runtime.Evaluate(expr).WithReturnByValue(true).WithAwaitPromise(awaitPromise)
		exCtx := cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Target)
		rp, expErr, doErr := params.Do(exCtx)
		if doErr != nil {
			result.HasError = true
			result.ErrorMsg = doErr.Error()
			return nil
		}
		if expErr != nil {
			result.HasError = true
			result.ErrorMsg = expErr.Error()
			return nil
		}
		result.Type = string(rp.Type)
		if rp.Value != nil {
			var v interface{}
			if err := json.Unmarshal(rp.Value, &v); err == nil {
				result.Result = v
			} else {
				result.Result = string(rp.Value)
			}
		} else if rp.Description != "" {
			result.Result = rp.Description
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &SpiderWebDataResponse{
		URL:        session.CurrentURL,
		Content:    fmt.Sprintf("eval: %s", expr),
		CrawlTime:  time.Now().UTC(),
		HasMore:    true,
		EvalResult: result,
	}, nil
}

// detectModuleScriptInjection 检测 JS 表达式是否试图注入 module script。
// v2.0.18: 防止 eval 注入 <script type="module"> 触发 chromedp 死锁。
// 检测模式：
//   - createElement('script') + type = 'module' / "module"
//   - appendChild + .type + module
//   - document.write / innerHTML / insertAdjacentHTML 含 <script type=module
//   - 任何包含 <script 和 type=module 的 HTML 字符串注入（含 Go JSON 转义 <）
func detectModuleScriptInjection(expr string) bool {
	lower := strings.ToLower(expr)
	// 快速排除：不包含 script 或 module 的不可能是注入
	if !strings.Contains(lower, "script") || !strings.Contains(lower, "module") {
		return false
	}
	// 模式 1: createElement('script') + type = module
	if strings.Contains(lower, "createelement") {
		if strings.Contains(lower, "'module'") || strings.Contains(lower, "\"module\"") || strings.Contains(lower, "=module") {
			return true
		}
	}
	// 模式 2: .type = 'module' / .type="module" 且伴随 append/insert
	if strings.Contains(lower, "type") && strings.Contains(lower, "module") {
		if strings.Contains(lower, "appendchild") || strings.Contains(lower, "insertbefore") || strings.Contains(lower, "append") {
			return true
		}
	}
	// 模式 3: document.write / innerHTML / insertAdjacentHTML 含 script type=module
	if (strings.Contains(lower, "document.write") || strings.Contains(lower, "innerhtml") || strings.Contains(lower, "insertadjacenthtml")) &&
		strings.Contains(lower, "type=module") {
		return true
	}
	// 模式 4: 任何包含 <script 或 <script 和 type= 和 module 的 HTML 字符串（覆盖 innerHTML 等变体 + Go JSON 转义）
	// 注意：type=module 可能是 type="module" 或 type='module'，所以检查 type= + module 同时出现
	if (strings.Contains(lower, "<script") || strings.Contains(lower, "\\u003cscript")) && strings.Contains(lower, "type=") && strings.Contains(lower, "module") {
		return true
	}
	return false
}

// ==================== 存储 ====================

func actionLocalStorage(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	return actionStorageImpl(session, action, req, engine, "local")
}

func actionSessionStorage(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	return actionStorageImpl(session, action, req, engine, "session")
}

func actionStorageImpl(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine, kind string) (*SpiderWebDataResponse, error) {
	op, _ := action.Parameters["op"].(string)
	if op == "" {
		op = "get"
	}
	key, _ := action.Parameters["key"].(string)
	val, _ := action.Parameters["value"].(string)
	timeout := resolveRequestTimeout(req)

	snap := StorageSnapshot{Kind: kind, Op: op, Key: key, Value: val}
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		store := fmt.Sprintf("%sStorage", kind)
		switch op {
		case "keys":
			js := fmt.Sprintf(`JSON.stringify((function(){
				const out = [];
				for (let i=0; i<%s.length; i++) out.push(%s.key(i));
				return out;
			})())`, store, store)
			var raw string
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
				return err
			}
			_ = json.Unmarshal([]byte(raw), &snap.Keys)
		case "get":
			if key == "" {
				js := fmt.Sprintf(`JSON.stringify((function(){
					const out = {};
					for (let i=0; i<%s.length; i++) {
						const k = %s.key(i);
						out[k] = %s.getItem(k);
					}
					return {keys: Object.keys(out), values: out};
				})())`, store, store, store)
				var raw string
				if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
					return err
				}
				var r struct {
					Keys   []string          `json:"keys"`
					Values map[string]string `json:"values"`
				}
				_ = json.Unmarshal([]byte(raw), &r)
				snap.Keys = r.Keys
				snap.Values = r.Values
			} else {
				js := fmt.Sprintf(`%s.getItem(%q)`, store, key)
				if err := chromedp.Run(ctx, chromedp.Evaluate(js, &snap.Value)); err != nil {
					return err
				}
			}
		case "set":
			js := fmt.Sprintf(`%s.setItem(%q, %q); %s.getItem(%q)`, store, key, val, store, key)
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &snap.Value)); err != nil {
				return err
			}
		case "remove":
			js := fmt.Sprintf(`%s.removeItem(%q); null`, store, key)
			return chromedp.Run(ctx, chromedp.Evaluate(js, nil))
		case "clear":
			js := fmt.Sprintf(`%s.clear(); null`, store)
			return chromedp.Run(ctx, chromedp.Evaluate(js, nil))
		default:
			return fmt.Errorf("unknown op: %s", op)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &SpiderWebDataResponse{
		URL:       session.CurrentURL,
		Content:   fmt.Sprintf("%s %s: %s", kind, op, key),
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Storage:   &snap,
	}, nil
}

// actionCookies 操作 cookies
// params:
//   - op=get|set|delete|clear|import
//   - set: {name, value, domain?, path?, http_only?, secure?}
//   - import (v2.0.25): {cookies: [{name, value, domain?, path?, http_only?, secure?}, ...]}
func actionCookies(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	op, _ := action.Parameters["op"].(string)
	if op == "" {
		op = "get"
	}
	timeout := resolveRequestTimeout(req)

	var out []CookieEntry
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if err := chromedp.Run(ctx, network.Enable()); err != nil {
			return err
		}
		switch op {
		case "get":
			cookies, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			for _, c := range cookies {
				out = append(out, cookieToEntry(c))
			}
			return nil
		case "set":
			name, _ := action.Parameters["name"].(string)
			value, _ := action.Parameters["value"].(string)
			if name == "" {
				return fmt.Errorf("cookies.set: params.name required")
			}
			cp := &network.CookieParam{Name: name, Value: value}
			if d, ok := action.Parameters["domain"].(string); ok && d != "" {
				cp.Domain = d
			}
			if p, ok := action.Parameters["path"].(string); ok && p != "" {
				cp.Path = p
			}
			if h, ok := action.Parameters["http_only"].(bool); ok {
				cp.HTTPOnly = h
			}
			if s, ok := action.Parameters["secure"].(bool); ok {
				cp.Secure = s
			}
			return network.SetCookies([]*network.CookieParam{cp}).Do(ctx)
		case "import":
			// v2.0.25：批量导入 cookie。用户提供的订阅 cookie 经常是一整段
			// "Cookie: SESSION=a; PAID=b; TRACK=c"，逐条 set 低效。
			// params.cookies: [{name, value, domain?, path?, http_only?, secure?}, ...]
			params, err := parseImportCookiesList(action.Parameters["cookies"])
			if err != nil {
				return fmt.Errorf("cookies.import: %w", err)
			}
			if len(params) == 0 {
				return fmt.Errorf("cookies.import: params.cookies is empty")
			}
			if err := network.SetCookies(params).Do(ctx); err != nil {
				return fmt.Errorf("cookies.import: SetCookies failed: %w", err)
			}
			// 导入后拉取一次当前 cookies，让 Agent 确认注入成功
			cookies, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			for _, c := range cookies {
				out = append(out, cookieToEntry(c))
			}
			return nil
		case "delete":
			name, _ := action.Parameters["name"].(string)
			if name == "" {
				return fmt.Errorf("cookies.delete: params.name required")
			}
			return network.DeleteCookies(name).Do(ctx)
		case "clear":
			return network.ClearBrowserCookies().Do(ctx)
		default:
			return fmt.Errorf("cookies: unknown op: %s", op)
		}
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []CookieEntry{}
	}
	return &SpiderWebDataResponse{
		URL:       session.CurrentURL,
		Content:   fmt.Sprintf("cookies %s: %d entries", op, len(out)),
		CrawlTime: time.Now().UTC(),
		HasMore:   true,
		Cookies:   out,
	}, nil
}

func cookieToEntry(c *network.Cookie) CookieEntry {
	ce := CookieEntry{
		Name:     c.Name,
		Value:    c.Value,
		Domain:   c.Domain,
		Path:     c.Path,
		HTTPOnly: c.HTTPOnly,
		Secure:   c.Secure,
		Size:     int(c.Size),
	}
	if c.Expires > 0 {
		ce.Expires = float64(c.Expires)
	}
	if string(c.SameSite) != "" {
		ce.SameSite = c.SameSite.String()
	}
	return ce
}

// parseImportCookiesList 解析 cookies.import 请求参数（v2.0.25）。
// 供 actionCookies case "import" 调用，独立出来便于单元测试（无需 Chrome session）。
// 输入：action.Parameters["cookies"] 的未解析 interface{}（应 []interface{}）。
// 输出：[]*network.CookieParam 用于 network.SetCookies 批量写入。
//
// 容错：
//   - cookies 非数组/空数组 → error（逐条 set 空 payload 无意义）
//   - 单条缺 name → error（不能写没有名字的 cookie）
//   - 单条 value 缺失 → 视为空字符串（允许写 placeholder cookie）
//   - domain / path 必须显式传；不从 request URL 推断（跨域注入场景常见）
func parseImportCookiesList(raw interface{}) ([]*network.CookieParam, error) {
	if raw == nil {
		return nil, fmt.Errorf("params.cookies is nil")
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("params.cookies must be array, got %T", raw)
	}
	params := make([]*network.CookieParam, 0, len(items))
	for i, rawItem := range items {
		m, ok := rawItem.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("item %d is not an object (got %T)", i, rawItem)
		}
		name, _ := m["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("item %d missing name", i)
		}
		cp := &network.CookieParam{Name: name}
		if v, ok2 := m["value"].(string); ok2 {
			cp.Value = v
		}
		if v, ok2 := m["domain"].(string); ok2 {
			cp.Domain = v
		}
		if v, ok2 := m["path"].(string); ok2 {
			cp.Path = v
		}
		if v, ok2 := m["http_only"].(bool); ok2 {
			cp.HTTPOnly = v
		}
		if v, ok2 := m["secure"].(bool); ok2 {
			cp.Secure = v
		}
		params = append(params, cp)
	}
	return params, nil
}

// ==================== 其他 ====================

// actionUploadFile 设置 input[type=file] 文件
// params: {"selector":"...", "files":["/path/a.png"]}
func actionUploadFile(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := pickSelector(action)
	if sel == "" {
		sel, _ = action.Parameters["selector"].(string)
	}
	if sel == "" {
		return nil, fmt.Errorf("upload_file: selector required")
	}
	filesRaw, ok := action.Parameters["files"].([]interface{})
	if !ok || len(filesRaw) == 0 {
		return nil, fmt.Errorf("upload_file: params.files (array of paths) required")
	}
	files := make([]string, 0, len(filesRaw))
	for _, f := range filesRaw {
		if s, ok := f.(string); ok {
			files = append(files, s)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("upload_file: no valid string paths in params.files")
	}
	timeout := resolveRequestTimeout(req)

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		parsed := parseSelector(sel)
		by := byMode(parsed)
		var nodes []*cdp.Node
		if err := chromedp.Run(ctx, chromedp.Nodes(parsed.Query, &nodes, by)); err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("upload_file: selector not found: %s", sel)
		}
		return chromedp.Run(ctx, chromedp.SetUploadFiles(parsed.Query, files, by))
	})
	if err != nil {
		return nil, err
	}
	resp := &SpiderWebDataResponse{}
	fillResultMeta(resp, session.CurrentURL, "", session.CurrentRawHTML, "", session)
	return resp, nil
}

// actionElementScreenshot 元素级截图（暂用 page screenshot 全图 + 返回 element selector 信息）
// params: {"selector":"...", "quality":80?}
func actionElementScreenshot(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := pickSelector(action)
	if sel == "" {
		sel, _ = action.Parameters["selector"].(string)
	}
	if sel == "" {
		return nil, fmt.Errorf("element_screenshot: selector required")
	}
	quality := int(getFloatOr(action.Parameters, "quality", 80))
	timeout := resolveRequestTimeout(req)
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	var buf []byte
	var currentURL, postTitle string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		parsed := parseSelector(sel)
		by := byMode(parsed)
		var nodes []*cdp.Node
		if err := chromedp.Run(ctx, chromedp.Nodes(parsed.Query, &nodes, by)); err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("element_screenshot: selector not found: %s", sel)
		}
		// 用 FullScreenshot（不需 selector）；quality 0-100，0 表示 png
		qual := 0
		if quality > 0 && quality <= 100 {
			qual = quality
		}
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, qual)); err != nil {
			return err
		}
		_ = chromedp.Run(ctx, chromedp.Title(&postTitle))
		_ = chromedp.Run(ctx, chromedp.Evaluate(`location.href`, &currentURL))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if currentURL == "" {
		currentURL = session.CurrentURL
	}
	b64 := base64.StdEncoding.EncodeToString(buf)
	return &SpiderWebDataResponse{
		URL:        currentURL,
		Title:      postTitle,
		Content:    fmt.Sprintf("element screenshot (%d bytes) of %s", len(buf), sel),
		CrawlTime:  time.Now().UTC(),
		HasMore:    true,
		Screenshot: b64,
	}, nil
}

// ==================== 辅助函数 ====================

func pickSelector(a *InteractiveAction) string {
	if a == nil {
		return ""
	}
	if a.Selector != "" {
		return a.Selector
	}
	return a.XPath
}

func byMode(p parsedSelector) chromedp.QueryOption {
	if p.Strategy == selXPath {
		return chromedp.BySearch
	}
	return chromedp.ByQuery
}

func elementCenter(box *dom.BoxModel) (float64, float64) {
	if box == nil || len(box.Border) < 8 {
		return 0, 0
	}
	cx := (box.Border[0] + box.Border[4]) / 2
	cy := (box.Border[1] + box.Border[5]) / 2
	return cx, cy
}

// doMouseClickSimple 派发完整的 click 序列：moved → pressed → released
func doMouseClickSimple(ctx context.Context, x, y float64, button string, count int) error {
	if count < 1 {
		count = 1
	}
	btn := chromedp.Button("left")
	switch strings.ToLower(button) {
	case "right":
		btn = chromedp.Button("right")
	case "middle":
		btn = chromedp.Button("middle")
	}
	for i := 0; i < count; i++ {
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseMoved"), x, y)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mousePressed"), x, y, btn)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseType("mouseReleased"), x, y, btn)); err != nil {
			return err
		}
		if i < count-1 {
			_ = chromedp.Run(ctx, chromedp.Sleep(50*time.Millisecond))
		}
	}
	return nil
}

// doMouseClick 同 doMouseClickSimple，但额外抓取 postHTML/postTitle/currentURL
func doMouseClick(ctx context.Context, x, y float64, button string, count int, postHTML, postTitle, currentURL *string) error {
	if err := doMouseClickSimple(ctx, x, y, button, count); err != nil {
		return err
	}
	_ = chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond))
	return chromedp.Run(ctx,
		chromedp.OuterHTML("html", postHTML),
		chromedp.Title(postTitle),
		chromedp.Evaluate(`location.href`, currentURL),
	)
}

func getFloat(m map[string]interface{}, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func getFloatOr(m map[string]interface{}, key string, def float64) float64 {
	if v, ok := getFloat(m, key); ok {
		return v
	}
	return def
}

func getStringOr(m map[string]interface{}, key, def string) string {
	if m == nil {
		return def
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return def
}

// parseModifiers 从 params.modifiers 解析为 CDP Modifier 位标志位（int）
func parseModifiers(m map[string]interface{}) int {
	raw, _ := m["modifiers"].([]interface{})
	if len(raw) == 0 {
		if s, ok := m["modifiers"].(string); ok {
			for _, p := range strings.Split(s, "+") {
				raw = append(raw, strings.TrimSpace(p))
			}
		}
	}
	v := 0
	for _, x := range raw {
		s, ok := x.(string)
		if !ok {
			continue
		}
		switch strings.ToLower(s) {
		case "ctrl", "control":
			v |= int(input.ModifierCtrl)
		case "alt":
			v |= int(input.ModifierAlt)
		case "shift":
			v |= int(input.ModifierShift)
		case "meta", "cmd":
			v |= int(input.ModifierMeta)
		}
	}
	return v
}

func waitSleepInSession(session *SpiderSession, engine *SpiderEngine, d time.Duration) error {
	if session == nil {
		return nil
	}
	timeout := d + 2*time.Second
	return runWithSession(session, engine, timeout, func(ctx context.Context) error {
		_ = chromedp.Run(ctx, chromedp.Sleep(d))
		return nil
	})
}

func jsonStringArray(ss []string) string {
	b, _ := json.Marshal(ss)
	return string(b)
}

// session.snapshotTabs 列出所有 tab（ActiveTab 在前）
func (s *SpiderSession) snapshotTabs() []TabInfo {
	if s == nil || len(s.SessionTabs) == 0 {
		return []TabInfo{}
	}
	out := make([]TabInfo, 0, len(s.SessionTabs))
	idx := 0
	ordered := []string{}
	if s.ActiveTab != "" {
		ordered = append(ordered, s.ActiveTab)
	}
	// 其余按字典序
	rest := []string{}
	for k := range s.SessionTabs {
		if k == s.ActiveTab {
			continue
		}
		rest = append(rest, k)
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	for _, alias := range ordered {
		tid := s.SessionTabs[alias]
		out = append(out, TabInfo{
			Index:    idx,
			TargetID: tid,
			Type:     "page",
			URL:      s.CurrentURL,
			Title:    alias,
			Active:   alias == s.ActiveTab,
		})
		idx++
	}
	return out
}

// aliasByIndex 根据 index 找 alias（与 snapshotTabs 顺序一致）
func (s *SpiderSession) aliasByIndex(idx int) string {
	if s == nil {
		return ""
	}
	ordered := []string{}
	if s.ActiveTab != "" {
		ordered = append(ordered, s.ActiveTab)
	}
	rest := []string{}
	for k := range s.SessionTabs {
		if k == s.ActiveTab {
			continue
		}
		rest = append(rest, k)
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	if idx >= 0 && idx < len(ordered) {
		return ordered[idx]
	}
	return ""
}

// ==================== 初始化脚本：Console + Network hook ====================
//
// 在每个新 tab 加载时通过 AddScriptToEvaluateOnNewDocument 注入（由 SpiderEngine 启动后注册）。
// 暂存本文件供 SpiderEngine.Start 引用；下个 commit 接入 Page.AddScriptToEvaluateOnNewDocument。

const initScriptForConsoleNetwork = `(function(){
	if (window.__lsm_console_hooked__) return;
	window.__lsm_console_hooked__ = true;
	window.__lsm_console_log__ = [];
	const _orig = {};
	['log','info','warn','error','debug'].forEach(function(level){
		_orig[level] = console[level];
		console[level] = function(){
			try {
				const args = Array.from(arguments).map(function(a){
					if (a === null) return 'null';
					if (a === undefined) return 'undefined';
					if (typeof a === 'object') { try { return JSON.stringify(a).slice(0,1000); } catch(e){ return String(a); } }
					return String(a);
				});
				window.__lsm_console_log__.push({
					level: level,
					text: args.join(' '),
					time: new Date().toISOString(),
					location: location.href
				});
				if (window.__lsm_console_log__.length > 500) window.__lsm_console_log__ = window.__lsm_console_log__.slice(-300);
			} catch(e){}
			return _orig[level].apply(console, arguments);
		};
	});

	window.__lsm_network_log__ = [];
	const _fetch = window.fetch;
	if (_fetch) {
		window.fetch = function(input, logger.init){
			const start = Date.now();
			const reqUrl = typeof input === 'string' ? input : (input && input.url) || '';
			const method = (logger.init && logger.init.method) || (input && input.method) || 'GET';
			window.__lsm_network_log__.push({
				request_id: 'fetch_' + Math.random().toString(36).slice(2),
				url: reqUrl,
				method: method,
				start_time: new Date().toISOString()
			});
			return _fetch.apply(this, arguments).then(function(resp){
				const idx = window.__lsm_network_log__.findIndex(function(e){ return e.url === reqUrl && !e.end_time; });
				if (idx >= 0) {
					window.__lsm_network_log__[idx].status = resp.status;
					window.__lsm_network_log__[idx].end_time = new Date().toISOString();
					window.__lsm_network_log__[idx].duration_ms = Date.now() - start;
				}
				return resp;
			}, function(err){
				const idx = window.__lsm_network_log__.findIndex(function(e){ return e.url === reqUrl && !e.end_time; });
				if (idx >= 0) {
					window.__lsm_network_log__[idx].failed = true;
					window.__lsm_network_log__[idx].failure_text = String(err);
					window.__lsm_network_log__[idx].end_time = new Date().toISOString();
				}
				throw err;
			});
		};
	}
	const _open = XMLHttpRequest.prototype.open;
	const _send = XMLHttpRequest.prototype.send;
	XMLHttpRequest.prototype.open = function(method, url){
		this.__lsm_method = method;
		this.__lsm_url = url;
		return _open.apply(this, arguments);
	};
	XMLHttpRequest.prototype.send = function(){
		const start = Date.now();
		const entry = {
			request_id: 'xhr_' + Math.random().toString(36).slice(2),
			url: this.__lsm_url,
			method: this.__lsm_method,
			start_time: new Date().toISOString()
		};
		window.__lsm_network_log__.push(entry);
		this.addEventListener('loadend', function(){
			entry.status = this.status;
			entry.end_time = new Date().toISOString();
			entry.duration_ms = Date.now() - start;
		});
		if (window.__lsm_network_log__.length > 500) window.__lsm_network_log__ = window.__lsm_network_log__.slice(-300);
		return _send.apply(this, arguments);
	};
})()`
