package spider

// ==================== 页面水合（hydration）探测单元测试 ====================
// v2.0.13: 验证 probeAndWaitForHydration / classifyHydration / 探测 JS
// 正确识别 React/Next/San/Vue/Static 五种水合状态。问题分析报告
// 20260626_205937 显示 chat.baidu.com 是 state=none + console_lines=0
// 的典型 SSR-only / 未水合场景，需要被探测函数正确分类。
//
// 注：本测试不依赖 Chrome（不需要 chromedp 真实运行）。classifyHydration
// 是纯函数，可独立测。probeAndWaitForHydration 需要真实 chromedp ctx，
// 由集成测试覆盖（cspell:ignore chromedp）。

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestClassifyHydration_ReactFiber 命中 React fiber → "hydrated" / "react"
func TestClassifyHydration_ReactFiber(t *testing.T) {
	s := hydrationSnapshot{FiberRoots: 3}
	state, fw := classifyHydration(s)
	if state != "hydrated" || fw != "react" {
		t.Fatalf("react fiber case: want hydrated/react, got %s/%s", state, fw)
	}
}

// TestClassifyHydration_Next 命中 window.next → "hydrated" / "next"
func TestClassifyHydration_Next(t *testing.T) {
	s := hydrationSnapshot{HasNext: true}
	state, fw := classifyHydration(s)
	if state != "hydrated" || fw != "next" {
		t.Fatalf("next case: want hydrated/next, got %s/%s", state, fw)
	}
}

// TestClassifyHydration_San 命中 window.san → "hydrated" / "san"
func TestClassifyHydration_San(t *testing.T) {
	s := hydrationSnapshot{HasSan: true}
	state, fw := classifyHydration(s)
	if state != "hydrated" || fw != "san" {
		t.Fatalf("san case: want hydrated/san, got %s/%s", state, fw)
	}
}

// TestClassifyHydration_Vue 命中 __vue__ → "hydrated" / "vue"
func TestClassifyHydration_Vue(t *testing.T) {
	s := hydrationSnapshot{HasVue: true}
	state, fw := classifyHydration(s)
	if state != "hydrated" || fw != "vue" {
		t.Fatalf("vue case: want hydrated/vue, got %s/%s", state, fw)
	}
}

// TestClassifyHydration_None chat.baidu.com 场景：0 fiber / 0 next / 0 san /
// 0 vue → "none" / "static"，与问题分析报告结论一致
func TestClassifyHydration_None(t *testing.T) {
	s := hydrationSnapshot{ConsoleLines: 0}
	state, fw := classifyHydration(s)
	if state != "none" || fw != "static" {
		t.Fatalf("chat.baidu.com case: want none/static, got %s/%s", state, fw)
	}
}

// TestClassifyHydration_Priority 优先级：react fiber 命中时即使 has_next 也优先 react
// （实际 chat.baidu.com 不会同时有多个信号，但优先级契约要稳）
func TestClassifyHydration_Priority(t *testing.T) {
	s := hydrationSnapshot{FiberRoots: 1, HasNext: true, HasSan: true, HasVue: true}
	state, fw := classifyHydration(s)
	if state != "hydrated" || fw != "react" {
		t.Fatalf("priority case: want hydrated/react, got %s/%s", state, fw)
	}
}

// TestHydrationDiagnostics_FieldsJSON JSON tag 契约：避免无意中改名破坏 API
func TestHydrationDiagnostics_FieldsJSON(t *testing.T) {
	d := &HydrationDiagnostics{
		State:             "hydrated",
		WaitMs:            150,
		FiberRootsCount:   5,
		HasNext:           false,
		HasSan:            false,
		HasVue:            false,
		DetectedFramework: "react",
		Warning:           "",
	}
	bs, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(bs)
	for _, want := range []string{
		`"state":"hydrated"`,
		`"wait_ms":150`,
		`"fiber_roots_count":5`,
		`"detected_framework":"react"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("missing %s in %s", want, js)
		}
	}
	// omitempty：HasNext/HasSan/HasVue/Warning 在零值下不应出现
	for _, dontWant := range []string{
		`"has_next"`,
		`"has_san"`,
		`"has_vue"`,
		`"warning"`,
	} {
		if strings.Contains(js, dontWant) {
			t.Errorf("unexpected field %s in %s", dontWant, js)
		}
	}
}

// TestHydrationSnapshot_UnmarshalJSON 与 hydrationProbeJS 输出契约一致
func TestHydrationSnapshot_UnmarshalJSON(t *testing.T) {
	raw := `{"fiber_roots":7,"has_next":true,"has_san":false,"has_vue":false,"console_lines":42}`
	var s hydrationSnapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if s.FiberRoots != 7 || !s.HasNext || s.ConsoleLines != 42 {
		t.Fatalf("snapshot mismatch: %+v", s)
	}
}

// TestHydrationDiagnostics_ConsoleLinesRoundTrip v2.0.13-补丁：验证 console_lines
// 字段能被序列化到 JSON 并在 HydrationDiagnostics 中正确反序列化。
// 问题分析报告 20260627_070139 显示 chat.baidu.com 客户端 bundle 未水合场景
// console_lines=0 是关键证据，必须在 JSON 契约里透传给 Agent。
func TestHydrationDiagnostics_ConsoleLinesRoundTrip(t *testing.T) {
	d := &HydrationDiagnostics{
		State:             "timeout",
		WaitMs:            5095,
		FiberRootsCount:   0,
		HasNext:           false,
		HasSan:            false,
		HasVue:            false,
		ConsoleLines:      0, // chat.baidu.com case
		DetectedFramework: "static",
		Warning:           "client bundle never executed",
	}
	bs, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(bs)
	if !strings.Contains(js, `"console_lines":0`) {
		t.Errorf("missing console_lines:0 in %s", js)
	}
	if !strings.Contains(js, `"detected_framework":"static"`) {
		t.Errorf("missing detected_framework:static in %s", js)
	}
	var got HydrationDiagnostics
	if err := json.Unmarshal(bs, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ConsoleLines != 0 || got.State != "timeout" || got.DetectedFramework != "static" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestSpiderWebDataResponse_WarningsJSON v2.0.13-补丁：验证顶层 warnings 字段契约。
// 三连击（state=timeout + framework=static + console_lines=0）触发时，Agent 能直接
// 在 data.warnings 看到人类可读的警告，无需深读 hydration_state。
func TestSpiderWebDataResponse_WarningsJSON(t *testing.T) {
	r := &SpiderWebDataResponse{
		URL:      "https://chat.baidu.com/",
		Title:    "百度文心助手",
		Warnings: []string{"client bundle not hydrated: do NOT retry fill_form/click/eval"},
	}
	bs, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(bs)
	if !strings.Contains(js, `"warnings":["client bundle not hydrated`) {
		t.Errorf("missing warnings array in %s", js)
	}
	// 空 warnings 应被 omitempty 抑制
	empty := &SpiderWebDataResponse{URL: "https://example.com/"}
	bs2, _ := json.Marshal(empty)
	if strings.Contains(string(bs2), `"warnings"`) {
		t.Errorf("empty warnings should be omitted, got: %s", string(bs2))
	}
}

// ==================== v2.0.17 补丁测试 ====================
//
// 背景：问题分析报告_20260627_120444 §3.1 / §5.2 指出 chat.baidu.com 这类
// SSR + ES Module SPA 在 headless Chrome 下客户端 bundle 完全未执行，但
// v2.0.13 的 probeAndWaitForHydration 只能从 DOM fiber / framework 标记
// 推断，缺少 ES Module 加载失败的可见性。v2.0.17 在 hydrationProbeJS
// 里增加 performance.getEntriesByType('resource') 统计，本测试覆盖：
//   1) hydrationSnapshot JSON 契约包含 4 个新字段
//   2) HydrationDiagnostics JSON 契约包含 4 个新字段（omitempty 语义正确）
//   3) buildHydrationTimeoutWarning 四条分支文案
//   4) buildHydrationDiagnostics 把 snapshot 字段全部透传到 diagnostics

// TestHydrationSnapshot_ESModuleFields v2.0.17: hydrationProbeJS 输出
// 契约必须包含 module_loads_total / module_loads_failed / module_zero_transfer
// / module_failed_urls，chat.baidu.com 场景需要这些字段诊断 CSP 拦截。
func TestHydrationSnapshot_ESModuleFields(t *testing.T) {
	raw := `{
		"fiber_roots": 0,
		"has_next": false,
		"has_san": false,
		"has_vue": false,
		"console_lines": 0,
		"module_loads_total": 13,
		"module_loads_failed": 7,
		"module_zero_transfer": 0,
		"module_failed_urls": ["not_started:https://chat.baidu.com/static/main.js"]
	}`
	var s hydrationSnapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if s.ModuleLoadsTotal != 13 {
		t.Errorf("ModuleLoadsTotal: want 13, got %d", s.ModuleLoadsTotal)
	}
	if s.ModuleLoadsFailed != 7 {
		t.Errorf("ModuleLoadsFailed: want 7, got %d", s.ModuleLoadsFailed)
	}
	if s.ModuleZeroTransfer != 0 {
		t.Errorf("ModuleZeroTransfer: want 0, got %d", s.ModuleZeroTransfer)
	}
	if len(s.ModuleFailedURLs) != 1 || s.ModuleFailedURLs[0] == "" {
		t.Errorf("ModuleFailedURLs mismatch: %+v", s.ModuleFailedURLs)
	}
}

// TestHydrationDiagnostics_ESModuleFieldsJSON v2.0.17: HydrationDiagnostics
// 响应契约必须包含 4 个新 ES Module 字段；omitempty 在 0 / nil / "" 时抑制。
// 这些字段是 Agent 判断「CSP / 反爬拦截了客户端 bundle」的关键证据。
func TestHydrationDiagnostics_ESModuleFieldsJSON(t *testing.T) {
	d := &HydrationDiagnostics{
		State:              "timeout",
		WaitMs:             5012,
		FiberRootsCount:    0,
		ConsoleLines:       0,
		ModuleLoadsTotal:   13,
		ModuleLoadsFailed:  7,
		ModuleZeroTransfer: 0,
		ModuleFailedURLs:   []string{"not_started:https://chat.baidu.com/static/main.js"},
		DetectedFramework:  "static",
		Warning:            "client bundle likely blocked",
	}
	bs, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(bs)
	for _, want := range []string{
		`"module_loads_total":13`,
		`"module_loads_failed":7`,
		`"module_failed_urls":["not_started:https://chat.baidu.com/static/main.js"]`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("missing %s in %s", want, js)
		}
	}
	// module_zero_transfer=0 应被 omitempty 抑制（避免 0 噪音）
	if strings.Contains(js, `"module_zero_transfer"`) {
		t.Errorf("module_zero_transfer=0 should be omitted, got: %s", js)
	}
	// 反向校验：去掉 URL 后 ModuleFailedURLs=nil 也应被抑制
	empty := &HydrationDiagnostics{
		State:             "hydrated",
		WaitMs:            100,
		FiberRootsCount:   3,
		DetectedFramework: "react",
	}
	bs2, _ := json.Marshal(empty)
	js2 := string(bs2)
	for _, suppressed := range []string{
		`"module_loads_total"`,
		`"module_loads_failed"`,
		`"module_zero_transfer"`,
		`"module_failed_urls"`,
	} {
		if strings.Contains(js2, suppressed) {
			t.Errorf("omitted field %s should not appear in zero-value HydrationDiagnostics, got: %s", suppressed, js2)
		}
	}
}

// TestBuildHydrationTimeoutWarning v2.0.17: 四条分支文案契约。
//   - 优先级 1: state=="none" + ModuleLoadsFailed > 0 → "blocked by CSP / anti-bot"
//   - 优先级 2: state=="none" + ModuleZeroTransfer > 0 → "CDN/script interception"
//   - 优先级 3: state=="none" + ConsoleLines == 0 → "client bundle never executed"（旧 v2.0.13 文案保持）
//   - 优先级 4: state=="none" + ConsoleLines > 0 → "may be partial hydration"
//   - 其他 state → "hydration timeout"
func TestBuildHydrationTimeoutWarning(t *testing.T) {
	cases := []struct {
		name     string
		snap     hydrationSnapshot
		state    string
		mustHave []string
	}{
		{
			name:     "blocked_by_csp_or_antibot",
			snap:     hydrationSnapshot{ConsoleLines: 0, ModuleLoadsTotal: 13, ModuleLoadsFailed: 7},
			state:    "none",
			mustHave: []string{"hydration timeout", "script resource", "blocked by CSP / anti-bot"},
		},
		{
			name:     "cdn_or_script_interception",
			snap:     hydrationSnapshot{ConsoleLines: 0, ModuleLoadsTotal: 13, ModuleZeroTransfer: 5},
			state:    "none",
			mustHave: []string{"hydration timeout", "0 transferSize", "CDN/script interception"},
		},
		{
			name:     "client_bundle_never_executed_legacy",
			snap:     hydrationSnapshot{ConsoleLines: 0, ModuleLoadsTotal: 0, ModuleLoadsFailed: 0, ModuleZeroTransfer: 0},
			state:    "none",
			mustHave: []string{"hydration timeout", "client bundle never executed"},
		},
		{
			name:     "partial_hydration_with_logs",
			snap:     hydrationSnapshot{ConsoleLines: 3, ModuleLoadsTotal: 10, ModuleLoadsFailed: 0},
			state:    "none",
			mustHave: []string{"hydration timeout", "partial hydration"},
		},
		{
			name:     "non_none_state_fallback",
			snap:     hydrationSnapshot{ConsoleLines: 0, FiberRoots: 1},
			state:    "hydrated",
			mustHave: []string{"hydration timeout"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildHydrationTimeoutWarning(tc.snap, tc.state)
			if got == "" {
				t.Fatalf("warning should not be empty for %s", tc.name)
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(got, want) {
					t.Errorf("warning missing %q in: %s", want, got)
				}
			}
		})
	}
}

// TestBuildHydrationDiagnostics_PropagatesFields v2.0.17: buildHydrationDiagnostics
// 必须把 hydrationSnapshot 的 4 个新字段透传到 HydrationDiagnostics，否则 Agent
// 看到的 hydration_state 将缺少 ES Module 加载证据。
func TestBuildHydrationDiagnostics_PropagatesFields(t *testing.T) {
	s := hydrationSnapshot{
		FiberRoots:         0,
		HasNext:            false,
		HasSan:             false,
		HasVue:             false,
		ConsoleLines:       0,
		ModuleLoadsTotal:   13,
		ModuleLoadsFailed:  7,
		ModuleZeroTransfer: 2,
		ModuleFailedURLs:   []string{"not_started:https://example.com/main.js", "zero_transfer:https://example.com/chunk.js"},
	}
	d := buildHydrationDiagnostics(s, 3000, "timeout", "static", "client bundle blocked")
	if d.ModuleLoadsTotal != 13 {
		t.Errorf("ModuleLoadsTotal not propagated: want 13, got %d", d.ModuleLoadsTotal)
	}
	if d.ModuleLoadsFailed != 7 {
		t.Errorf("ModuleLoadsFailed not propagated: want 7, got %d", d.ModuleLoadsFailed)
	}
	if d.ModuleZeroTransfer != 2 {
		t.Errorf("ModuleZeroTransfer not propagated: want 2, got %d", d.ModuleZeroTransfer)
	}
	if len(d.ModuleFailedURLs) != 2 {
		t.Errorf("ModuleFailedURLs not propagated: %+v", d.ModuleFailedURLs)
	}
	if d.State != "timeout" || d.DetectedFramework != "static" || d.Warning != "client bundle blocked" {
		t.Errorf("basic fields mismatch: %+v", d)
	}
	if d.WaitMs != 3000 {
		t.Errorf("WaitMs not propagated: want 3000, got %d", d.WaitMs)
	}
}

// TestDetachAllSpiderSessions_NilMap v2.0.17: detachAllSpiderSessions 必须
// 在空 session map 或 nil session 上安全 no-op，不能 panic。
// （restart_browser 触发时 SpiderSessions 可能为空——比如刚启动还未创建 session。）
func TestDetachAllSpiderSessions_NilMap(t *testing.T) {
	// 在子进程中跑：避免污染测试环境全局 session 状态
	if os.Getenv("LSM_DETACH_TEST_CHILD") == "1" {
		detachAllSpiderSessions()
		// 再调一次，覆盖空 map 路径
		detachAllSpiderSessions()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestDetachAllSpiderSessions_NilMap")
	cmd.Env = append(os.Environ(), "LSM_DETACH_TEST_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detachAllSpiderSessions panicked or failed: %v\n%s", err, string(out))
	}
}
