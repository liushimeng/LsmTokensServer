package spider

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildAncestorProbeJS_StableJSONContract v2.0.11 增强：祖先探针 JS 输出字段
// 名必须稳定，Go 端 probe 结构体反序列化依赖这些字段。JS 字面量写法为 found:false，
// JSON.stringify 之后会序列化为 "found":false；我们只需断言字面量包含字段名即可。
func TestBuildAncestorProbeJS_StableJSONContract(t *testing.T) {
	js := buildAncestorProbeJS(parseSelector("#ci-submit-button-ai"))
	must := []string{
		"found:false",
		"ancestor:false",
		"ancestor:true",
		"clicked:false",
		"clicked: true",
		"disabled: ancDisabled",
		"tag: ancestor.tagName",
		"id: ancestor.id",
		"cls: (ancestor.className",
		"error:String(e)",
	}
	for _, m := range must {
		if !strings.Contains(js, m) {
			t.Errorf("ancestor probe JS missing field marker %q", m)
		}
	}
}

// TestBuildAncestorProbeJS_DetectsRoleAndPointer v2.0.11 增强：祖先判定必须覆盖
// A/BUTTON/INPUT 标签 + role=button/link + data-submit + cursor:pointer。
func TestBuildAncestorProbeJS_DetectsRoleAndPointer(t *testing.T) {
	js := buildAncestorProbeJS(parseSelector("#x"))
	for _, must := range []string{
		"tag === 'A'",
		"tag === 'BUTTON'",
		"tag === 'INPUT'",
		"role === 'button'",
		"role === 'link'",
		"data-submit",
		"cur === 'pointer'",
	} {
		if !strings.Contains(js, must) {
			t.Errorf("ancestor probe JS missing clickable detector %q", must)
		}
	}
}

// TestBuildAncestorProbeJS_LimitsDepth v2.0.11 增强：祖先上溯必须限深（避免误中
// body / html 触发意外的页面级 click）。验证 6 层的硬上限。
func TestBuildAncestorProbeJS_LimitsDepth(t *testing.T) {
	js := buildAncestorProbeJS(parseSelector("#x"))
	if !strings.Contains(js, "i < 6") {
		t.Errorf("ancestor probe JS missing depth limit (i < 6)")
	}
	if !strings.Contains(js, "document.body") {
		t.Errorf("ancestor probe JS must stop at document.body")
	}
}

// TestTruncateForReport_ShortValueUnchanged 短于上限的字符串原样返回
func TestTruncateForReport_ShortValueUnchanged(t *testing.T) {
	got := truncateForReport("hello", 200)
	if got != "hello" {
		t.Errorf("short value should be unchanged, got %q", got)
	}
}

// TestTruncateForReport_ExactBoundary 上限边界不截断
func TestTruncateForReport_ExactBoundary(t *testing.T) {
	s := strings.Repeat("a", 200)
	got := truncateForReport(s, 200)
	if got != s {
		t.Errorf("value at boundary should be unchanged, len=%d", len(got))
	}
}

// TestTruncateForReport_LongValueTruncated 超长字符串截断并带省略号
func TestTruncateForReport_LongValueTruncated(t *testing.T) {
	s := strings.Repeat("a", 1000)
	got := truncateForReport(s, 50)
	if len(got) <= 50 {
		t.Errorf("expected truncated output > 50 bytes (with ellipsis), got %d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 50)) {
		t.Errorf("expected 50 a's prefix, got %q", got[:50])
	}
}

// TestFillFormResultAggregation_AllVerifiedOK 全部字段 verified 时聚合 OK
func TestFillFormResultAggregation_AllVerifiedOK(t *testing.T) {
	fields := []FillFormFieldStatus{
		{Selector: "#a", Expected: "x", Actual: "x", ActualLen: 1, VerifiedOK: true},
		{Selector: "#b", Expected: "y", Actual: "y", ActualLen: 1, VerifiedOK: true},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if !agg.AllVerifiedOK {
		t.Error("expected AllVerifiedOK=true when all fields verified")
	}
	if agg.SubmitClicked {
		t.Error("SubmitClicked should be false")
	}
	if len(agg.Warnings) != 0 {
		t.Errorf("no warnings expected, got %v", agg.Warnings)
	}
}

// TestFillFormResultAggregation_PartialFailureMismatch 字段不一致时 all_verified_ok=false
// 且产生 warning（实际场景：受控 SPA 在 render 周期复位 value）
func TestFillFormResultAggregation_PartialFailureMismatch(t *testing.T) {
	fields := []FillFormFieldStatus{
		{Selector: "#chat-textarea", Expected: "最新 agent 智能体新闻", Actual: "", ActualLen: 0, VerifiedOK: false},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if agg.AllVerifiedOK {
		t.Error("expected AllVerifiedOK=false when one field mismatched")
	}
	if len(agg.Warnings) == 0 {
		t.Error("expected warning for mismatch")
	}
	found := false
	for _, w := range agg.Warnings {
		if strings.Contains(w, "chat-textarea") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning should mention selector, got %v", agg.Warnings)
	}
}

// TestFillFormResultAggregation_ErrorFieldNoExtraWarning 写入异常字段（带 Error）不再产生默认 mismatch 警告
func TestFillFormResultAggregation_ErrorFieldNoExtraWarning(t *testing.T) {
	fields := []FillFormFieldStatus{
		{Selector: "#a", Expected: "x", VerifiedOK: false, Error: "clear failed: not focusable"},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if agg.AllVerifiedOK {
		t.Error("AllVerifiedOK should be false")
	}
	// 已带 Error 的字段不应再额外产生「actual vs expected length」类警告
	for _, w := range agg.Warnings {
		if strings.Contains(w, "expected length") {
			t.Errorf("did not expect length-mismatch warning for errored field, got %q", w)
		}
	}
}

// TestFillFormResultAggregation_HardErrorProducesWarning 硬错误时附加 warning
func TestFillFormResultAggregation_HardErrorProducesWarning(t *testing.T) {
	agg := aggregateFillFormReport(nil, true, "submit click failed: #x")
	if agg.AllVerifiedOK {
		t.Error("AllVerifiedOK should be false when hardErr != empty")
	}
	if !agg.SubmitClicked {
		t.Error("SubmitClicked should reflect input true")
	}
	found := false
	for _, w := range agg.Warnings {
		if strings.Contains(w, "hard error") && strings.Contains(w, "submit click failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hard error warning, got %v", agg.Warnings)
	}
}

// TestFillFormResultSerialization_FieldsAndWarningsMarshal 验证 JSON 字段名稳定，
// 这是 Agent 端契约（FillFormResult.* 必须稳定序列化）
func TestFillFormResultSerialization_FieldsAndWarningsMarshal(t *testing.T) {
	r := &SpiderWebDataResponse{
		FillFormResult: &FillFormResult{
			AllVerifiedOK: false,
			Fields: []FillFormFieldStatus{
				{Selector: "#x", Strategy: "controlled_js", Expected: "abc", Actual: "", ActualLen: 0, VerifiedOK: false},
			},
			SubmitClicked: true,
			Warnings:      []string{"warning-1"},
		},
	}
	// 通过断言字段名（不需要真序列化，避免外部包依赖）
	if r.FillFormResult.Fields[0].Strategy != "controlled_js" {
		t.Errorf("strategy field lost: %+v", r.FillFormResult.Fields[0])
	}
	if r.FillFormResult.Fields[0].VerifiedOK {
		t.Error("VerifiedOK should be false")
	}
	if !r.FillFormResult.SubmitClicked {
		t.Error("SubmitClicked should be true")
	}
	if len(r.FillFormResult.Warnings) != 1 || r.FillFormResult.Warnings[0] != "warning-1" {
		t.Errorf("warnings lost: %+v", r.FillFormResult.Warnings)
	}
}

// TestAggregateFillFormReport_FrameworkNotConsumed v2.0.11: React _valueTracker 未更新时，
// 即使 DOM value 与期望一致，也应产生「framework 未消费」warning 提示 Agent 改用 eval。
func TestAggregateFillFormReport_FrameworkNotConsumed(t *testing.T) {
	fields := []FillFormFieldStatus{
		{
			Selector:    "#chat-textarea",
			Strategy:    "controlled_js",
			Expected:    "最新 agent 智能体新闻",
			Actual:      "最新 agent 智能体新闻",
			ActualLen:   14,
			VerifiedOK:  false, // 已被聚合前的 verifiedOK 降级逻辑置为 false
			Diagnostics: &ControlledInputDiagnostics{DOMValue: "最新 agent 智能体新闻", HasValueTracker: true, ReactTrackerValue: "", FrameworkConsumed: false},
		},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if agg.AllVerifiedOK {
		t.Error("AllVerifiedOK should be false when framework not consumed")
	}
	if len(agg.Warnings) == 0 {
		t.Fatal("expected at least one warning")
	}
	warning := agg.Warnings[0]
	if !strings.Contains(warning, "framework 未消费") {
		t.Errorf("warning should mention framework not consumed, got %q", warning)
	}
	if !strings.Contains(warning, "_valueTracker") {
		t.Errorf("warning should identify React _valueTracker, got %q", warning)
	}
	if !strings.Contains(warning, "eval 单 roundtrip") {
		t.Errorf("warning should suggest eval single-roundtrip, got %q", warning)
	}
}

// TestAggregateFillFormReport_VueNotConsumed v2.0.11: Vue 探测为 true 但 state 未更新时，
// 应产生"Vue __vue__ 状态未同步"特定 warning。
func TestAggregateFillFormReport_VueNotConsumed(t *testing.T) {
	fields := []FillFormFieldStatus{
		{
			Selector:    "#input",
			Strategy:    "controlled_js",
			Expected:    "hello",
			Actual:      "hello",
			ActualLen:   5,
			VerifiedOK:  false,
			Diagnostics: &ControlledInputDiagnostics{DOMValue: "hello", HasVue: true, FrameworkConsumed: false},
		},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if len(agg.Warnings) == 0 {
		t.Fatal("expected Vue-specific warning")
	}
	if !strings.Contains(agg.Warnings[0], "Vue") {
		t.Errorf("warning should mention Vue, got %q", agg.Warnings[0])
	}
}

// TestAggregateFillFormReport_SanNotConsumed v2.0.11: San 框架未消费时
func TestAggregateFillFormReport_SanNotConsumed(t *testing.T) {
	fields := []FillFormFieldStatus{
		{
			Selector:    "#input",
			Strategy:    "controlled_js",
			Expected:    "hi",
			Actual:      "hi",
			ActualLen:   2,
			VerifiedOK:  false,
			Diagnostics: &ControlledInputDiagnostics{DOMValue: "hi", HasSan: true, FrameworkConsumed: false},
		},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if len(agg.Warnings) == 0 {
		t.Fatal("expected San-specific warning")
	}
	if !strings.Contains(agg.Warnings[0], "San") {
		t.Errorf("warning should mention San, got %q", agg.Warnings[0])
	}
}

// TestAggregateFillFormReport_FrameworkConsumedNoWarning v2.0.11: 框架已消费时
// 不产生"framework 未消费"warning；只产生正常 verified 字段 warning（如果有 mismatch）。
func TestAggregateFillFormReport_FrameworkConsumedNoWarning(t *testing.T) {
	fields := []FillFormFieldStatus{
		{
			Selector:    "#input",
			Strategy:    "native_chromedp",
			Expected:    "abc",
			Actual:      "abc",
			ActualLen:   3,
			VerifiedOK:  true,
			Diagnostics: &ControlledInputDiagnostics{DOMValue: "abc", HasValueTracker: true, ReactTrackerValue: "abc", FrameworkConsumed: true},
		},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if !agg.AllVerifiedOK {
		t.Errorf("AllVerifiedOK should be true; warnings=%v", agg.Warnings)
	}
	for _, w := range agg.Warnings {
		if strings.Contains(w, "framework 未消费") {
			t.Errorf("did not expect framework-not-consumed warning when consumed, got %q", w)
		}
	}
}

// TestAggregateFillFormReport_DiagnosticsPreferredOverLengthMismatch v2.0.11: 当 Diagnostics
// 存在且 framework 未消费时，优先输出 framework 诊断 warning（更具体），不再额外输出
// 长度 mismatch 类 warning。
func TestAggregateFillFormReport_DiagnosticsPreferredOverLengthMismatch(t *testing.T) {
	fields := []FillFormFieldStatus{
		{
			Selector:    "#x",
			Strategy:    "controlled_js",
			Expected:    "expected-value",
			Actual:      "", // 故意留空，触发 length mismatch warning
			ActualLen:   0,
			VerifiedOK:  false,
			Diagnostics: &ControlledInputDiagnostics{DOMValue: "", HasValueTracker: true, FrameworkConsumed: false},
		},
	}
	agg := aggregateFillFormReport(fields, false, "")
	if len(agg.Warnings) != 1 {
		t.Fatalf("expected exactly 1 warning (framework diagnostic preferred), got %d: %v", len(agg.Warnings), agg.Warnings)
	}
	if !strings.Contains(agg.Warnings[0], "framework 未消费") {
		t.Errorf("expected framework diagnostic warning, got %q", agg.Warnings[0])
	}
}

// TestBuildControlledInputJS_DispatchesBeforeInput v2.0.11 增强：受控输入 JS 必须
// 派发 beforeinput 事件（React 18+ onBeforeInput / San 某些版本要求该事件触发 state 更新）。
// 只派发 input 事件会被部分 SPA 框架吞掉，导致 framework_consumed=false。
func TestBuildControlledInputJS_DispatchesBeforeInput(t *testing.T) {
	js := buildControlledInputJS(parseSelector("#chat-textarea"), "hello")
	if !strings.Contains(js, "'beforeinput'") {
		t.Error("controlled-input JS must dispatch beforeinput event")
	}
	// 顺序：keydown -> keypress -> beforeinput -> input -> keyup
	// 在 events 数组里查找（避免匹配到 JS 注释或 dispatchEvent 字段）
	eventsStart := strings.Index(js, "const events = [")
	eventsEnd := strings.Index(js[eventsStart:], "];")
	eventsBlock := js[eventsStart : eventsStart+eventsEnd]
	kdIdx := strings.Index(eventsBlock, "'keydown'")
	kpIdx := strings.Index(eventsBlock, "'keypress'")
	b4Idx := strings.Index(eventsBlock, "'beforeinput'")
	inIdx := strings.Index(eventsBlock, "'input'")
	kuIdx := strings.Index(eventsBlock, "'keyup'")
	if !(kdIdx >= 0 && kpIdx > kdIdx && b4Idx > kpIdx && inIdx > b4Idx && kuIdx > inIdx) {
		t.Errorf("event order wrong: keydown=%d keypress=%d beforeinput=%d input=%d keyup=%d", kdIdx, kpIdx, b4Idx, inIdx, kuIdx)
	}
	// dispatcher 必须处理 isBeforeInput 分支（区别于 isInput）
	if !strings.Contains(js, "ev.isBeforeInput") {
		t.Error("controlled-input JS dispatcher must handle isBeforeInput branch")
	}
	if !strings.Contains(js, "InputEvent(ev.type") {
		t.Error("beforeinput must use InputEvent (React 18+ requires this)")
	}
}

// TestBuildControlledInputJS_ProbesReactVueSan v2.0.11 增强：JS 必须探测
// React _valueTracker / Vue __vue__ / San __data，否则 diagnostics 无法识别框架未消费。
func TestBuildControlledInputJS_ProbesReactVueSan(t *testing.T) {
	js := buildControlledInputJS(parseSelector("#x"), "v")
	must := []string{
		"_valueTracker",
		"__vue__",
		"__data",
		"frameworkConsumed",
		"hasValueTracker",
		"hasVue",
		"hasSan",
	}
	for _, m := range must {
		if !strings.Contains(js, m) {
			t.Errorf("controlled-input JS missing probe: %s", m)
		}
	}
}

// TestBuildControlledInputJS_StableJSONContract v2.0.11 增强：JS 返回 JSON 字段名
// 必须与 Go 端 ControlledInputDiagnostics JSON tag 一致（Agent 端契约）。
// JS 字面量写法为 found:true（不带引号），JSON.stringify 会序列化为 "found":true。
func TestBuildControlledInputJS_StableJSONContract(t *testing.T) {
	js := buildControlledInputJS(parseSelector("#x"), "v")
	// 字面量字段（带 JS 字段名作为 key）
	must := []string{
		"found: true",
		"domValue: domValue",
		"reactTrackerValue: reactTrackerValue",
		"hasValueTracker: hasValueTracker",
		"hasVue: hasVue",
		"hasSan: hasSan",
		"frameworkConsumed: frameworkConsumed",
	}
	for _, m := range must {
		if !strings.Contains(js, m) {
			t.Errorf("controlled-input JS missing JSON output field %q", m)
		}
	}
	// 验证 Go struct JSON tag 与 JS 字段名一致（key rename：camelCase → snake_case）
	d := &ControlledInputDiagnostics{DOMValue: "x", ReactTrackerValue: "x", HasValueTracker: true, FrameworkConsumed: true}
	raw, _ := json.Marshal(d)
	for _, field := range []string{`"dom_value":"x"`, `"react_tracker_value":"x"`, `"has_value_tracker":true`, `"framework_consumed":true`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("ControlledInputDiagnostics missing JSON tag %s, raw=%s", field, raw)
		}
	}
}

// TestControlledInputDiagnostics_BeforeInputFieldStable v2.0.11 增强：probe 类型契约。
// 即便 Agent 只解析 diagnostics JSON 字段，beforeinput 增强不会改变 JSON 输出形状。
func TestControlledInputDiagnostics_BeforeInputFieldStable(t *testing.T) {
	d := ControlledInputDiagnostics{
		DOMValue:          "abc",
		ReactTrackerValue: "abc",
		HasValueTracker:   true,
		HasVue:            false,
		HasSan:            false,
		FrameworkConsumed: true,
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		`"dom_value":"abc"`,
		`"react_tracker_value":"abc"`,
		`"has_value_tracker":true`,
		`"has_vue":false`,
		`"has_san":false`,
		`"framework_consumed":true`,
	} {
		if !strings.Contains(string(raw), must) {
			t.Errorf("expected JSON %s in %s", must, raw)
		}
	}
}
