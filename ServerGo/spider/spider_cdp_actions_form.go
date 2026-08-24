package spider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// actionFillForm fill_form action
// params 格式：{"fields":[{"selector":"...","value":"..."}, ...], "submit":"form-selector?"}
func actionFillForm(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	fieldsRaw, ok := action.Parameters["fields"]
	if !ok {
		return nil, fmt.Errorf("fill_form: params.fields (array) required, e.g. {\"fields\":[{\"selector\":\"...\",\"value\":\"...\"}]}")
	}
	fieldsArr, ok := fieldsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("fill_form: params.fields must be array")
	}
	if len(fieldsArr) == 0 {
		return nil, fmt.Errorf("fill_form: params.fields is empty")
	}
	timeout := resolveRequestTimeout(req)

	// v2.0.10: 收集每个字段的实际效果，用于响应里给 Agent 反馈
	fieldReports := make([]FillFormFieldStatus, 0, len(fieldsArr))
	submitClicked := false
	hardErr := ""

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		for i, f := range fieldsArr {
			fm, ok := f.(map[string]interface{})
			if !ok {
				return fmt.Errorf("fill_form: fields[%d] must be object {selector,value}", i)
			}
			sel, _ := fm["selector"].(string)
			val, _ := fm["value"].(string)
			if sel == "" {
				return fmt.Errorf("fill_form: fields[%d].selector required", i)
			}
			parsed := parseSelector(sel)
			waitAction := buildWaitVisible(parsed, 5*time.Second)
			if err := chromedp.Run(ctx, waitAction); err != nil {
				// 即便可见性等待失败，仍尝试 JS 兜底（v2.0.10：受控 SPA 偶发不可见）
				fieldReports = append(fieldReports, FillFormFieldStatus{
					Selector: sel, Strategy: "wait_failed",
					Expected: val, VerifiedOK: false,
					Error: fmt.Sprintf("not visible: %v", err),
				})
				continue
			}
			// v2.0.9: 受控 SPA 输入框 chromedp.Clear/SendKeys 走原生路径常失败，
			// clear 失败时降级 JS 兜底清空，再尝试写入。
			strategy := "native_chromedp"
			clearAction := buildClear(parsed)
			if err := chromedp.Run(ctx, clearAction); err != nil {
				if jsErr := clearControlledInputJS(ctx, parsed); jsErr != nil {
					fieldReports = append(fieldReports, FillFormFieldStatus{
						Selector: sel, Strategy: "clear_failed",
						Expected: val, VerifiedOK: false,
						Error: fmt.Sprintf("clear failed: %v", err),
					})
					continue
				}
				strategy = "controlled_js"
			}
			sendAction := buildSendKeys(parsed, val)
			var diags *ControlledInputDiagnostics
			if err := chromedp.Run(ctx, sendAction); err != nil {
				// 原生 SendKeys 失败：降级为受控输入 JS 写入
				if d, jsErr := runControlledInputJS(ctx, parsed, val); jsErr != nil {
					fieldReports = append(fieldReports, FillFormFieldStatus{
						Selector: sel, Strategy: "sendkeys_failed",
						Expected: val, VerifiedOK: false,
						Error: fmt.Sprintf("sendKeys failed: %v", err),
					})
					continue
				} else {
					diags = d
					strategy = "controlled_js"
				}
			} else {
				// v2.0.11: 原生 SendKeys 成功后也尝试一次受控输入 JS 探测，
				// 仅用于拿到 ControlledInputDiagnostics（不影响 DOM value）。
				// 对非受控组件：DOM value 已是期望值，framework_consumed=true，无副作用。
				// 对受控组件：探测能识别"DOM 写入成功但 React state 未更新"，让 Agent 改用 eval。
				if d, jsErr := runControlledInputJS(ctx, parsed, val); jsErr == nil {
					diags = d
					if !d.FrameworkConsumed {
						// 探测到 framework 未消费：把 strategy 标记为 controlled_js，
						// verifiedOK 在下面降级为 false
						strategy = "controlled_js"
					}
				}
			}
			// v2.0.10: 写入后回读真实 DOM 值，校验效果。
			// 受控组件在每次 render 时可能把 value 复位，因此这里在写入后
			// 立即读一次。读到的实际值既用于 verified_ok，也填到响应里
			// 给 Agent 诊断。
			actual, actualLen, readErr := readInputValueJS(ctx, parsed)
			if readErr != nil {
				fieldReports = append(fieldReports, FillFormFieldStatus{
					Selector: sel, Strategy: strategy,
					Expected: val, VerifiedOK: false,
					Diagnostics: diags,
					Error:       fmt.Sprintf("readback failed: %v", readErr),
				})
				continue
			}
			verifiedOK := actual == val
			// v2.0.11: 若 runControlledInputJS 提供了 frameworkConsumed=false，
			// 即使 DOM value 一致，verifiedOK 也降级为 false，让 Agent 识别
			// "DOM 写入成功但框架未消费"的隐患（onClick 会提交旧值）。
			if verifiedOK && diags != nil && !diags.FrameworkConsumed && strategy == "controlled_js" {
				verifiedOK = false
			}
			fieldReports = append(fieldReports, FillFormFieldStatus{
				Selector: sel, Strategy: strategy,
				Expected: val, Actual: truncateForReport(actual, 200),
				ActualLen: actualLen, VerifiedOK: verifiedOK,
				Diagnostics: diags,
			})
		}
		if submitSel, ok := action.Parameters["submit"].(string); ok && submitSel != "" {
			p := parseSelector(submitSel)
			clickAct := buildClick(p)
			if err := chromedp.Run(ctx, clickAct); err != nil {
				return fmt.Errorf("fill_form: submit click failed: %s", submitSel)
			}
			submitClicked = true
			_ = chromedp.Run(ctx, chromedp.Sleep(1*time.Second))
		}
		return nil
	})
	if err != nil {
		hardErr = err.Error()
	}
	// 抓取结果页
	var postHTML, postTitle, currentURL string
	_ = runWithSession(session, engine, 10*time.Second, func(ctx context.Context) error {
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

	// v2.0.10: 汇总 fill_form 效果校验，附到响应里
	resp.FillFormResult = aggregateFillFormReport(fieldReports, submitClicked, hardErr)
	if hardErr != "" {
		return resp, fmt.Errorf("%s", hardErr)
	}
	return resp, nil
}

// aggregateFillFormReport 汇总各字段写入效果，生成 FillFormResult 响应。
// v2.0.10 提取为独立函数便于单测。
//   - hardErr 非空：AllVerifiedOK=false，并附加 hard error 警告。
//   - 任何字段 VerifiedOK=false：AllVerifiedOK=false；如果该字段没填 Error 而实际值和期望不一致，
//     额外追加「framework render 复位」类警告，便于 Agent 改用 eval 单 roundtrip 兜底。
//
// v2.0.11: 若 Diagnostics.FrameworkConsumed=false，追加"SPA framework 未消费 input 事件"
//
//	警告（即使 DOM value 与期望一致），明确告诉 Agent：React _valueTracker / Vue state
//	未被更新，submit.click 会提交旧值。
func aggregateFillFormReport(fieldReports []FillFormFieldStatus, submitClicked bool, hardErr string) *FillFormResult {
	allOK := hardErr == "" && len(fieldReports) > 0
	warnings := []string{}
	for _, fr := range fieldReports {
		if !fr.VerifiedOK {
			allOK = false
			// v2.0.11: 优先用 Diagnostics 区分"DOM 写入失败" vs "框架未消费"
			if fr.Diagnostics != nil && !fr.Diagnostics.FrameworkConsumed && fr.Error == "" {
				hint := "framework 未消费 input 事件"
				if fr.Diagnostics.HasValueTracker {
					hint += "（React _valueTracker 未更新）"
				} else if fr.Diagnostics.HasVue {
					hint += "（Vue __vue__ 状态未同步）"
				} else if fr.Diagnostics.HasSan {
					hint += "（San __data 未更新）"
				}
				warnings = append(warnings, fmt.Sprintf(
					"field %q: %s; 建议改用 eval 单 roundtrip 完成 set + 派发 InputEvent + 立即 click submit",
					fr.Selector, hint))
				continue
			}
			if fr.Error == "" && fr.Actual != fr.Expected {
				warnings = append(warnings, fmt.Sprintf(
					"field %q expected length=%d, actual length=%d; 页面的框架可能在每次 render 复位 value，建议改用 eval 单 roundtrip 完成 set + 派发事件 + 立即 click submit",
					fr.Selector, len(fr.Expected), fr.ActualLen))
			}
		}
	}
	if hardErr != "" {
		warnings = append(warnings, "fill_form encountered hard error: "+hardErr)
	}
	return &FillFormResult{
		AllVerifiedOK: allOK,
		Fields:        fieldReports,
		SubmitClicked: submitClicked,
		Warnings:      warnings,
	}
}

// readInputValueJS 用 JS 回读目标 input/textarea/contenteditable 的当前值，
// 用于 fill_form 的写入后校验（v2.0.10）。
// 返回 (actualValue, actualLen, error)。actualValue 可能为空字符串（合法场景）。
func readInputValueJS(ctx context.Context, sel parsedSelector) (string, int, error) {
	js := fmt.Sprintf(`(function(){
	  const el = %s;
	  if (!el) return null;
	  const tag = el.tagName;
	  if (tag === 'INPUT' || tag === 'TEXTAREA') return el.value;
	  if (el.isContentEditable) return el.textContent;
	  return el.textContent;
	})()`, elementLocatorJS(sel))
	var raw interface{}
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
		return "", 0, err
	}
	if raw == nil {
		return "", 0, fmt.Errorf("element not found: %s", sel.Raw)
	}
	s, ok := raw.(string)
	if !ok {
		// JS 理论上总返回 string，但兜底处理意外类型
		s = fmt.Sprintf("%v", raw)
	}
	return s, len(s), nil
}

// truncateForReport 把写入报告里的实际值截到指定上限（v2.0.10）。
func truncateForReport(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// actionExtract extract action
// params: {"selector":"...", "attribute":"href"?, "limit":50?}
func actionExtract(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel, _ := action.Parameters["selector"].(string)
	// P0: extract 参数校验补全：selector 必须非空
	if sel == "" {
		return nil, fmt.Errorf("extract: params.selector required and must not be empty")
	}
	parsed := parseSelector(sel)
	attr, _ := action.Parameters["attribute"].(string)
	limit := 50
	if v, ok := action.Parameters["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	// P0: limit < 0 时报错
	if limit < 0 {
		return nil, fmt.Errorf("extract: params.limit must be >= 0")
	}
	timeout := resolveRequestTimeout(req)

	var resultJSON string
	// v2.0.18 补丁（基于问题分析报告_20260629_143200 §3.3）：extract 对
	// 不存在的选择器在反爬/未水合页面上容易等到 deadline 才 timeout，
	// 既不能给 Agent 任何线索又把连接耗光。改成 selector 命中 0 个节点
	// 直接返回 "[]" + 警告，避免挂到 timeout。timeout / context canceled
	// 也走快速失败 + 诊断路径，不再向上抛 raw timeout error。
	emptyHint := "selector matched 0 nodes; page may be unhydrated / anti-bot / live DOM diverged from SSR"

	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if parsed.Strategy == selCSS || parsed.Strategy == selXPath {
			var nodes []*cdp.Node
			if err := chromedp.Run(ctx, chromedp.Nodes(parsed.Query, &nodes, chromedp.ByQueryAll)); err != nil {
				return err
			}
			// v2.0.18 快速失败：0 个节点直接返回空数组，不进入 attribute / OuterHTML 循环。
			if len(nodes) == 0 {
				resultJSON = "[]"
				return nil
			}
			if len(nodes) > limit {
				nodes = nodes[:limit]
			}
			if attr != "" {
				vals := make([]string, 0, len(nodes))
				for _, node := range nodes {
					var v string
					_ = chromedp.Run(ctx, chromedp.AttributeValue(node, attr, &v, nil))
					vals = append(vals, v)
				}
				b, _ := json.Marshal(vals)
				resultJSON = string(b)
				return nil
			}
			htmls := make([]string, 0, len(nodes))
			for _, node := range nodes {
				var h string
				_ = chromedp.Run(ctx, chromedp.OuterHTML(node, &h))
				htmls = append(htmls, h)
			}
			b, _ := json.Marshal(htmls)
			resultJSON = string(b)
			return nil
		}
		// text 模式
		js := fmt.Sprintf(`(function(){
		  const kw = %q;
		  const out = [];
		  function walk(n) {
		    if (!n) return;
		    if (n.nodeType === 3 && n.textContent.includes(kw)) out.push({text: n.textContent.trim()});
		    for (const c of n.childNodes) walk(c);
		  }
		  walk(document.body);
		  return JSON.stringify(out.slice(0, %d));
		})()`, parsed.TextKeyword, limit)
		return chromedp.Run(ctx, chromedp.Evaluate(js, &resultJSON))
	})
	if err != nil {
		// v2.0.18 快速失败：timeout / context canceled / chromedp 错误时返回空结果 +
		// 警告，而不是向上抛 raw error 让 Agent 看不到任何有用信息。
		errStr := err.Error()
		if strings.Contains(errStr, "context canceled") ||
			strings.Contains(errStr, "context deadline exceeded") ||
			strings.Contains(errStr, "i/o timeout") {
			warning := "extract failed: " + errStr + " — " + emptyHint
			return &SpiderWebDataResponse{
				URL:       session.CurrentURL,
				Title:     "extracted (failed fast)",
				Content:   "[]",
				CrawlTime: time.Now().UTC(),
				HasMore:   false,
				Warnings:  []string{warning},
			}, nil
		}
		return nil, err
	}
	// v2.0.18：0 命中时附加警告，Agent 可据此改 eval / API 直连。
	var warnings []string
	if resultJSON == "[]" {
		warnings = append(warnings, "extract: "+emptyHint+"; try action.eval to inspect live DOM, or wait_for_hydration=true")
	}
	return &SpiderWebDataResponse{
		URL:       session.CurrentURL,
		Title:     "extracted",
		Content:   resultJSON,
		CrawlTime: time.Now().UTC(),
		HasMore:   false,
		Warnings:  warnings,
	}, nil
}

// actionScreenshot screenshot action
// params: {"format":"png", "quality":80, "full_page":true}
// v2.0.0-fix: 改为在 session tab 上执行，保留登录态/表单状态。
// 通过 chromedp.WithTargetID 复用 session 的 target，避免独立 context 的状态丢失。
func actionScreenshot(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	quality := 80
	if v, ok := action.Parameters["quality"].(float64); ok && v > 0 {
		quality = int(v)
	}
	fullPage := true
	if v, ok := action.Parameters["full_page"].(bool); ok {
		fullPage = v
	}
	timeout := resolveRequestTimeout(req)
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	var buf []byte
	var postTitle, currentURL string

	// 使用 session 的 CDP context（复用 tab，保留状态）
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		var err error
		if fullPage {
			err = chromedp.Run(ctx, chromedp.FullScreenshot(&buf, quality))
		} else {
			err = chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf))
		}
		if err != nil {
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
		Content:    fmt.Sprintf("screenshot captured (%d bytes)", len(buf)),
		CrawlTime:  time.Now().UTC(),
		HasMore:    true,
		Screenshot: b64,
	}, nil
}

// actionWait wait action
// params: {"selector":"...", "timeout":10?}
// 等待指定元素出现在页面上，或等待固定超时。
func actionWait(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel, _ := action.Parameters["selector"].(string)
	waitTimeout := 10 * time.Second
	if t, ok := action.Parameters["timeout"].(float64); ok && t > 0 {
		waitTimeout = time.Duration(t) * time.Second
	}
	if waitTimeout > 60*time.Second {
		// v2.0.3: 允许通过配置放宽 wait timeout 上限（默认 60s，最大 300s）
		maxWait := 60 * time.Second
		if config.G != nil && config.G.SpiderActionWaitSec > 60 {
			maxWait = time.Duration(config.G.SpiderActionWaitSec) * time.Second
			if maxWait > 300*time.Second {
				maxWait = 300 * time.Second
			}
		}
		if waitTimeout > maxWait {
			waitTimeout = maxWait
		}
	}

	timeout := resolveRequestTimeout(req)
	if timeout < waitTimeout+5*time.Second {
		timeout = waitTimeout + 5*time.Second
	}

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if sel != "" {
			parsed := parseSelector(sel)
			waitAction := buildWaitVisible(parsed, waitTimeout)
			if err := chromedp.Run(ctx, waitAction); err != nil {
				return fmt.Errorf("wait: element not found: %s", sel)
			}
		} else {
			// 无 selector：纯 sleep 等待
			_ = chromedp.Run(ctx, chromedp.Sleep(waitTimeout))
		}
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

// actionHover hover action
// params: {"selector":"..."}
// 鼠标悬停到指定元素上，触发 hover 效果（下拉菜单、tooltip 等）。
func actionHover(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := action.Selector
	if sel == "" {
		sel, _ = action.Parameters["selector"].(string)
	}
	if sel == "" {
		return nil, fmt.Errorf("hover: selector required")
	}
	parsed := parseSelector(sel)
	if session.CurrentURL == "" {
		// v2.0.21 改进
		return nil, fmt.Errorf("hover: no current page in session (session=%s); "+
			"call action.navigate first, or drop session_id to start a new session",
			session.SessionID)
	}
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if parsed.Strategy == selText {
			// text 模式：通过 JS 找到元素并 dispatch mouseover
			js := fmt.Sprintf(`(function(){
			  const kw = %q;
			  function walk(n) {
			    if (!n) return null;
			    if (n.nodeType === 3 && n.textContent.includes(kw)) return n.parentElement;
			    for (const c of n.childNodes) { const r = walk(c); if (r) return r; }
			    return null;
			  }
			  const el = walk(document.body);
			  if (el) {
			    const ev = new MouseEvent('mouseover', { bubbles: true, cancelable: true, view: window });
			    el.dispatchEvent(ev);
			    const ev2 = new MouseEvent('mouseenter', { bubbles: true, cancelable: true, view: window });
			    el.dispatchEvent(ev2);
			    return true;
			  }
			  return false;
			})()`, parsed.TextKeyword)
			var ok bool
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("text selector not found: %s", sel)
			}
		} else {
			var nodes []*cdp.Node
			by := chromedp.ByQuery
			if parsed.Strategy == selXPath {
				by = chromedp.BySearch
			}
			if err := chromedp.Run(ctx, chromedp.Nodes(parsed.Query, &nodes, by)); err != nil {
				return err
			}
			if len(nodes) == 0 {
				return fmt.Errorf("selector not found: %s", sel)
			}
			// 先获取元素位置，再 dispatch mousemove 到元素中心模拟 hover
			var box *dom.BoxModel
			if err := chromedp.Run(ctx, chromedp.Dimensions(nodes[0], &box)); err != nil {
				return err
			}
			var cx, cy float64
			if box != nil && len(box.Border) >= 8 {
				// Border quad: [x1,y1, x2,y2, x3,y3, x4,y4] clockwise
				// 中心点 = (x1+x3)/2, (y1+y3)/2
				cx = (box.Border[0] + box.Border[4]) / 2
				cy = (box.Border[1] + box.Border[5]) / 2
			} else {
				return fmt.Errorf("hover: unable to get element dimensions")
			}
			if err := chromedp.Run(ctx,
				chromedp.MouseEvent(input.MouseType("mouseMoved"), cx, cy),
			); err != nil {
				return err
			}
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(800*time.Millisecond))
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

// actionSelect select action
// params: {"selector":"...", "value":"..."} or {"text":"..."}
// 选择下拉框 <select> 的选项，支持按 value 或按 text 匹配。
func actionSelect(session *SpiderSession, action *InteractiveAction, req *SpiderWebDataRequest, engine *SpiderEngine) (*SpiderWebDataResponse, error) {
	sel := action.Selector
	if sel == "" {
		sel, _ = action.Parameters["selector"].(string)
	}
	if sel == "" {
		return nil, fmt.Errorf("select: selector required")
	}
	parsed := parseSelector(sel)
	val, _ := action.Parameters["value"].(string)
	text, _ := action.Parameters["text"].(string)
	if val == "" && text == "" {
		return nil, fmt.Errorf("select: params.value or params.text required")
	}
	if session.CurrentURL == "" {
		// v2.0.21 改进
		return nil, fmt.Errorf("select: no current page in session (session=%s); "+
			"call action.navigate first, or drop session_id to start a new session",
			session.SessionID)
	}
	timeout := resolveRequestTimeout(req)

	var postHTML, postTitle, currentURL string
	err := runWithSession(session, engine, timeout, func(ctx context.Context) error {
		if parsed.Strategy == selText {
			return fmt.Errorf("select: text selector not supported, use CSS/XPath")
		}
		var nodes []*cdp.Node
		by := chromedp.ByQuery
		if parsed.Strategy == selXPath {
			by = chromedp.BySearch
		}
		if err := chromedp.Run(ctx, chromedp.Nodes(parsed.Query, &nodes, by)); err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("select: selector not found: %s", sel)
		}
		if val != "" {
			if err := chromedp.Run(ctx, chromedp.SetValue(parsed.Query, val, by)); err != nil {
				return err
			}
		} else {
			js := fmt.Sprintf(`(function(){
			  const sel = document.querySelector(%q);
			  if (!sel) return false;
			  for (const opt of sel.options) {
			    if (opt.text === %q) { sel.value = opt.value; sel.dispatchEvent(new Event('change', {bubbles: true})); return true; }
			  }
			  return false;
			})()`, parsed.Query, text)
			if parsed.Strategy == selXPath {
				js = fmt.Sprintf(`(function(){
				  const result = document.evaluate(%q, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
				  const sel = result.singleNodeValue;
				  if (!sel) return false;
				  for (const opt of sel.options) {
				    if (opt.text === %q) { sel.value = opt.value; sel.dispatchEvent(new Event('change', {bubbles: true})); return true; }
				  }
				  return false;
				})()`, parsed.Query, text)
			}
			var ok bool
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("select: option text not found: %s", text)
			}
		}
		_ = chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond))
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

// ==================== 低层 helper：按 selector 策略构建 chromedp.Action ====================

func buildWaitVisible(sel parsedSelector, t time.Duration) chromedp.Action {
	switch sel.Strategy {
	case selXPath:
		return chromedp.WaitVisible(sel.Query, chromedp.BySearch)
	case selText:
		// text 模式无法直接 wait visible，返回空 action（由调用方通过 JS 检查）
		return chromedp.ActionFunc(func(ctx context.Context) error { return nil })
	default:
		return chromedp.WaitVisible(sel.Query, chromedp.ByQuery)
	}
}

func buildClear(sel parsedSelector) chromedp.Action {
	switch sel.Strategy {
	case selXPath:
		return chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.Run(ctx, chromedp.Clear(sel.Query, chromedp.BySearch)); err == nil {
				return nil
			}
			return clearControlledInputJS(ctx, sel)
		})
	case selText:
		return chromedp.ActionFunc(func(ctx context.Context) error {
			return clearControlledInputJS(ctx, sel)
		})
	default:
		return chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.Run(ctx, chromedp.Clear(sel.Query, chromedp.ByQuery)); err == nil {
				return nil
			}
			return clearControlledInputJS(ctx, sel)
		})
	}
}

func buildSendKeys(sel parsedSelector, val string) chromedp.Action {
	switch sel.Strategy {
	case selXPath:
		return chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.Run(ctx, chromedp.SendKeys(sel.Query, val, chromedp.BySearch)); err == nil {
				return nil
			}
			_, err := runControlledInputJS(ctx, sel, val)
			return err
		})
	case selText:
		return chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := runControlledInputJS(ctx, sel, val)
			return err
		})
	default:
		return chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.Run(ctx, chromedp.SendKeys(sel.Query, val, chromedp.ByQuery)); err == nil {
				return nil
			}
			_, err := runControlledInputJS(ctx, sel, val)
			return err
		})
	}
}

func buildClick(sel parsedSelector) chromedp.Action {
	switch sel.Strategy {
	case selXPath:
		return chromedp.Click(sel.Query, chromedp.BySearch)
	case selText:
		// text 模式：通过 JS 找到元素并点击
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
		})()`, sel.TextKeyword)
		return chromedp.ActionFunc(func(ctx context.Context) error {
			var ok bool
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("text selector not found: %s", sel.Raw)
			}
			return nil
		})
	default:
		return chromedp.Click(sel.Query, chromedp.ByQuery)
	}
}

// elementLocatorJS 生成「在页面内定位目标元素」的 JS 表达式片段（返回 Element 或 null）。
// 供受控输入 JS 兜底复用，支持 CSS / XPath / text 三种策略。
func elementLocatorJS(sel parsedSelector) string {
	switch sel.Strategy {
	case selXPath:
		return fmt.Sprintf(`document.evaluate(%q, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue`, sel.Query)
	case selText:
		return fmt.Sprintf(`(function(){
		  const kw = %q;
		  function walk(n){ if(!n) return null; if(n.nodeType===3 && n.textContent.includes(kw)) return n.parentElement; for(const c of n.childNodes){ const r=walk(c); if(r) return r;} return null; }
		  return walk(document.body);
		})()`, sel.TextKeyword)
	default:
		return fmt.Sprintf(`document.querySelector(%q)`, sel.Query)
	}
}

// runControlledInputJS 受控/contenteditable 输入框写入兜底。
// v2.0.9: 重度 SPA（如文心一言）的 textarea 由框架受控，chromedp.Focus/Clear/SendKeys
// 走原生 CDP 路径会失败（not focusable / clear failed）或不触发框架 onChange。
// 这里通过 JS 直接 focus + 设置 value/textContent 并派发完整事件序列，
// 让受控组件感知到输入。支持 React/San/Vue/Angular 等框架。
// v2.0.10: 增强事件序列 — 模拟真实用户输入的完整键盘事件链，
// 并尝试框架特定的状态更新机制（React _valueTracker、San 自定义事件、Vue setter 等）。
// v2.0.11: 派发后读取 React _valueTracker.getValue() / Vue __vue__ / San __data
// 等框架内部状态，与 DOM value 对比；不一致时通过返回值告诉上层"框架未消费"。
// 返回 *ControlledInputDiagnostics，nil 表示元素未找到（与旧 error 路径语义一致）。
//
// buildControlledInputJS 把上述流程的 JS 模板抽出来作为独立函数，便于单元测试断言
// JS 内容（如：必须派发 beforeinput、必须探测 React _valueTracker、JSON 输出字段稳定）。
func buildControlledInputJS(sel parsedSelector, val string) string {
	return fmt.Sprintf(`(function(){
	  const el = %s;
	  if (!el) return JSON.stringify({found: false});
	  try { el.focus(); } catch(e) {}
	  const v = %q;
	  const tag = el.tagName;
	  const isInput = tag === 'INPUT' || tag === 'TEXTAREA';

	  // 1. 设置值（多种方式尝试）
	  if (isInput) {
	    // 方式1: 直接设置 value
	    el.value = v;
	    // 方式2: 尝试 React _valueTracker（React 15+）
	    try {
	      const tracker = el._valueTracker;
	      if (tracker) { tracker.setValue(''); }
	    } catch(e) {}
	    // 方式3: 尝试 San 框架的 __data 层
	    try {
	      if (el.__data) { el.__data.raw = v; }
	    } catch(e) {}
	    // 方式4: 尝试 Vue 的 __vue__ / __VUE__
	    try {
	      if (el.__vue__) { el.__vue__.$emit('input', v); }
	    } catch(e) {}
	  } else if (el.isContentEditable) {
	    el.textContent = v;
	  } else {
	    el.textContent = v;
	  }

	  // 2. 派发完整事件序列（模拟真实用户输入）
	  // v2.0.11: 增补 beforeinput — React/San/Vue 部分版本要求 beforeinput 后才会触发 state 更新，
	  // 否则只有 input 事件会被框架吞掉（state 不变）。同时 keydown/keypress 在 input 之前派发，
	  // 顺序与 Chromium 输入栈一致。
	  const events = [
	    { type: 'keydown',    bubbles: true, cancelable: true },
	    { type: 'keypress',   bubbles: true, cancelable: true },
	    { type: 'beforeinput',bubbles: true, cancelable: true, isBeforeInput: true },
	    { type: 'input',      bubbles: true, cancelable: true, isInput: true },
	    { type: 'keyup',      bubbles: true, cancelable: true }
	  ];

	  // 中文输入场景：添加 composition 事件
	  if (v.length > 0 && /[一-鿿]/.test(v)) {
	    events.splice(1, 0,
	      { type: 'compositionstart', bubbles: true, cancelable: true },
	      { type: 'compositionupdate', bubbles: true, cancelable: true, data: v },
	      { type: 'compositionend', bubbles: true, cancelable: true, data: v }
	    );
	  }

	  events.forEach(function(ev) {
	    try {
	      let event;
	      if (ev.isInput) {
	        event = new InputEvent(ev.type, {
	          bubbles: ev.bubbles,
	          cancelable: ev.cancelable,
	          data: v,
	          inputType: 'insertText'
	        });
	      } else if (ev.isBeforeInput) {
	        // beforeinput 与 input 同源 InputEvent，但 inputType 字段必须存在
	        // 否则部分浏览器（React 18+ onBeforeInput）会忽略
	        event = new InputEvent(ev.type, {
	          bubbles: ev.bubbles,
	          cancelable: ev.cancelable,
	          data: v,
	          inputType: 'insertText'
	        });
	      } else if (ev.data !== undefined) {
	        event = new CompositionEvent(ev.type, {
	          bubbles: ev.bubbles,
	          cancelable: ev.cancelable,
	          data: ev.data
	        });
	      } else {
	        event = new KeyboardEvent(ev.type, {
	          bubbles: ev.bubbles,
	          cancelable: ev.cancelable,
	          key: v.length > 0 ? v[v.length-1] : 'Unidentified',
	          code: 'Unidentified'
	        });
	      }
	      el.dispatchEvent(event);
	    } catch(e) {}
	  });

	  // 3. 派发 change 事件（blur 时触发）
	  try {
	    el.dispatchEvent(new Event('change', { bubbles: true }));
	  } catch(e) {}

	  // 4. 尝试框架特定事件（San 框架等）
	  try {
	    el.dispatchEvent(new Event('san:input', { bubbles: true }));
	    el.dispatchEvent(new Event('san:change', { bubbles: true }));
	  } catch(e) {}

	  // 5. 触发 blur + focus 刷新状态
	  try {
	    el.blur();
	    el.focus();
	  } catch(e) {}

	  // 6. v2.0.11: 读取框架内部状态，与 DOM value 对比判断是否真正被消费。
	  //   - DOMValue: 当前 DOM value（input/textarea）或 textContent（contenteditable）
	  //   - ReactTrackerValue: React 15+ 的 _valueTracker.getValue()（如果存在）
	  //     该值是 React 上一次 setState 的"上次值"，input 事件被消费后会更新到 v；
	  //     若仍为旧值说明 onChange 未被触发，state 未更新 → onClick 会提交旧值。
	  //   - FrameworkConsumed: 综合判断 — tracker 一致 / Vue 已 emit / DOM 已更新 任一为真
	  let domValue = '';
	  if (isInput) {
	    domValue = el.value || '';
	  } else if (el.isContentEditable) {
	    domValue = el.textContent || '';
	  }
	  let reactTrackerValue = null;
	  let hasValueTracker = false;
	  try {
	    if (el._valueTracker && typeof el._valueTracker.getValue === 'function') {
	      hasValueTracker = true;
	      reactTrackerValue = el._valueTracker.getValue();
	    }
	  } catch(e) {}
	  let hasVue = false;
	  try { hasVue = !!(el.__vue__ || el.__VUE__); } catch(e) {}
	  let hasSan = false;
	  try { hasSan = !!el.__data; } catch(e) {}

	  // framework_consumed 判定：
	  //   - tracker 存在且 getValue() === v → 已被 React 消费
	  //   - tracker 不存在但 domValue === v → 非 React 受控组件，DOM 已生效视为消费
	  //   - Vue / San 探测为 true 且 domValue === v → 框架状态已同步（保守判为消费）
	  let frameworkConsumed = false;
	  if (hasValueTracker) {
	    frameworkConsumed = reactTrackerValue === v;
	  } else {
	    frameworkConsumed = domValue === v;
	  }
	  if (!frameworkConsumed && (hasVue || hasSan) && domValue === v) {
	    frameworkConsumed = true;
	  }

	  return JSON.stringify({
	    found: true,
	    domValue: domValue,
	    reactTrackerValue: reactTrackerValue,
	    hasValueTracker: hasValueTracker,
	    hasVue: hasVue,
	    hasSan: hasSan,
	    frameworkConsumed: frameworkConsumed
	  });
	})()`, elementLocatorJS(sel), val)
}

func runControlledInputJS(ctx context.Context, sel parsedSelector, val string) (*ControlledInputDiagnostics, error) {
	js := buildControlledInputJS(sel, val)
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
		return nil, err
	}
	var probe struct {
		Found             bool   `json:"found"`
		DOMValue          string `json:"domValue"`
		ReactTrackerValue string `json:"reactTrackerValue"`
		HasValueTracker   bool   `json:"hasValueTracker"`
		HasVue            bool   `json:"hasVue"`
		HasSan            bool   `json:"hasSan"`
		FrameworkConsumed bool   `json:"frameworkConsumed"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("controlled input: parse diagnostics: %w", err)
	}
	if !probe.Found {
		return nil, fmt.Errorf("controlled input: element not found: %s", sel.Raw)
	}
	return &ControlledInputDiagnostics{
		DOMValue:          probe.DOMValue,
		ReactTrackerValue: probe.ReactTrackerValue,
		HasValueTracker:   probe.HasValueTracker,
		HasVue:            probe.HasVue,
		HasSan:            probe.HasSan,
		FrameworkConsumed: probe.FrameworkConsumed,
	}, nil
}

// clearControlledInputJS 受控输入框清空兜底（JS 设空值 + 派发完整事件序列）。
// v2.0.10: 同 runControlledInputJS，支持完整事件序列和框架特定状态更新。
func clearControlledInputJS(ctx context.Context, sel parsedSelector) error {
	js := fmt.Sprintf(`(function(){
	  const el = %s;
	  if (!el) return false;
	  try { el.focus(); } catch(e) {}
	  const tag = el.tagName;
	  const isInput = tag === 'INPUT' || tag === 'TEXTAREA';

	  // 1. 清空值（多种方式尝试）
	  if (isInput) {
	    // 方式1: 原生 setter（通过原型链，绕过 React 拦截）
	    const proto = tag === 'INPUT'
	      ? window.HTMLInputElement.prototype
	      : window.HTMLTextAreaElement.prototype;
	    const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
	    setter.call(el, '');
	    // 方式2: 直接设置
	    el.value = '';
	    // 方式3: React _valueTracker
	    try {
	      const tracker = el._valueTracker;
	      if (tracker) { tracker.setValue(''); }
	    } catch(e) {}
	    // 方式4: San 框架
	    try {
	      if (el.__data) { el.__data.raw = ''; }
	    } catch(e) {}
	  } else {
	    el.textContent = '';
	  }

	  // 2. 派发完整清空事件序列
	  const events = [
	    { type: 'keydown',  bubbles: true, cancelable: true, key: 'Backspace' },
	    { type: 'input',    bubbles: true, cancelable: true, isInput: true, data: '' },
	    { type: 'keyup',    bubbles: true, cancelable: true, key: 'Backspace' }
	  ];

	  events.forEach(function(ev) {
	    try {
	      let event;
	      if (ev.isInput) {
	        event = new InputEvent(ev.type, {
	          bubbles: ev.bubbles,
	          cancelable: ev.cancelable,
	          data: '',
	          inputType: 'deleteContentBackward'
	        });
	      } else {
	        event = new KeyboardEvent(ev.type, {
	          bubbles: ev.bubbles,
	          cancelable: ev.cancelable,
	          key: ev.key,
	          code: ev.key === 'Backspace' ? 'Backspace' : 'Unidentified'
	        });
	      }
	      el.dispatchEvent(event);
	    } catch(e) {}
	  });

	  // 3. change 事件
	  try {
	    el.dispatchEvent(new Event('change', { bubbles: true }));
	  } catch(e) {}

	  // 4. 框架特定事件
	  try {
	    el.dispatchEvent(new Event('san:input', { bubbles: true }));
	    el.dispatchEvent(new Event('san:change', { bubbles: true }));
	  } catch(e) {}

	  return true;
	})()`, elementLocatorJS(sel))
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("controlled input clear: element not found: %s", sel.Raw)
	}
	return nil
}
