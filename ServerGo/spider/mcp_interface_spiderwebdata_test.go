package spider

// ==================== /SpiderWebData 失败响应辅助函数单测 ====================
//
// 覆盖问题分析报告 2026-06-24 中的几个修复点：
//   1. partial_result 在反爬/captcha 失败时必须携带 session_id / elements / page_state
//      （v2.0.7 文档约定：data.session_id 用于多轮对话）
//   2. partial_result 内的 raw_html 必须被截断到合理大小，避免响应流被 SIGKILL
//   3. timeout 分支也应当尽量回填 session_id 等关键字段

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBuildPartialResultForFailure_AntiBotTruncatesRawHTML 验证：反爬失败时 raw_html 被截断
func TestBuildPartialResultForFailure_AntiBotTruncatesRawHTML(t *testing.T) {
	bigHTML := strings.Repeat("a", 1<<20) // 1 MB
	r := &SpiderWebDataResponse{
		URL:     "https://example.com/",
		Title:   "Example",
		Content: "hello",
		RawHTML: bigHTML,
		Elements: &WebElements{
			Links: []WebElementLink{{Text: "x", Href: "/x", URL: "https://example.com/x"}},
		},
		SessionID: "spider_test_1",
	}

	got := buildPartialResultForFailure(r, "anti_bot")
	if got == nil {
		t.Fatal("nil partial result")
	}
	if got.RawHTML == bigHTML {
		t.Errorf("raw_html not truncated for anti_bot (len=%d)", len(got.RawHTML))
	}
	if len(got.RawHTML) > failurePartialHTMLCharLimit+200 { // 200 = 注释前缀安全余量
		t.Errorf("raw_html still too large: %d bytes", len(got.RawHTML))
	}
	if !strings.Contains(got.RawHTML, "truncated by MCP") {
		t.Error("expected truncation comment in raw_html")
	}
	// 关键诊断字段不能丢
	if got.SessionID != "spider_test_1" {
		t.Errorf("session_id lost in partial_result: %q", got.SessionID)
	}
	if got.Elements == nil || len(got.Elements.Links) != 1 {
		t.Error("elements lost in partial_result")
	}
	if got.Title != "Example" || got.URL != "https://example.com/" {
		t.Error("title/url lost in partial_result")
	}
}

// TestBuildPartialResultForFailure_CaptchaAlsoTruncates 验证 captcha 失败同样截断 raw_html
func TestBuildPartialResultForFailure_CaptchaAlsoTruncates(t *testing.T) {
	r := &SpiderWebDataResponse{
		RawHTML: strings.Repeat("X", 1<<20),
	}
	got := buildPartialResultForFailure(r, "captcha")
	if got == nil || len(got.RawHTML) > failurePartialHTMLCharLimit+200 {
		t.Errorf("captcha: expected truncated raw_html, got %d bytes", len(got.RawHTML))
	}
}

// TestBuildPartialResultForFailure_RegionBlockTruncates 验证 region_block 失败同样截断 raw_html
func TestBuildPartialResultForFailure_RegionBlockTruncates(t *testing.T) {
	r := &SpiderWebDataResponse{
		RawHTML: strings.Repeat("Y", 1<<20),
	}
	got := buildPartialResultForFailure(r, "region_block")
	if got == nil || len(got.RawHTML) > failurePartialHTMLCharLimit+200 {
		t.Errorf("region_block: expected truncated raw_html, got %d bytes", len(got.RawHTML))
	}
}

// TestBuildPartialResultForFailure_TimeoutKeepsRawHTML 验证 timeout 失败不截断（HTML 通常较小）
func TestBuildPartialResultForFailure_TimeoutKeepsRawHTML(t *testing.T) {
	html := "<html><body>short timeout page</body></html>"
	r := &SpiderWebDataResponse{
		RawHTML: html,
	}
	got := buildPartialResultForFailure(r, "timeout")
	if got == nil || got.RawHTML != html {
		t.Error("timeout: should preserve small raw_html verbatim")
	}
}

// TestBuildPartialResultForFailure_NilInput 验证 nil 入参不 panic
func TestBuildPartialResultForFailure_NilInput(t *testing.T) {
	if got := buildPartialResultForFailure(nil, "anti_bot"); got != nil {
		t.Errorf("nil in should give nil out, got %+v", got)
	}
}

// TestBuildPartialResultForFailure_SmallHTMLNotTruncated 验证小 HTML 不被改写
func TestBuildPartialResultForFailure_SmallHTMLNotTruncated(t *testing.T) {
	html := "<html><body>small</body></html>"
	r := &SpiderWebDataResponse{RawHTML: html, SessionID: "x"}
	got := buildPartialResultForFailure(r, "anti_bot")
	if got.RawHTML != html {
		t.Errorf("small HTML should not be truncated, got %q", got.RawHTML)
	}
}

// TestBuildFailureDataTopLevelFields_AntiBotIncludesAll 验证反爬时 session_id /
// elements / page_state / url / title / content / data_source_id / crawl_time 都平铺到顶层
func TestBuildFailureDataTopLevelFields_AntiBotIncludesAll(t *testing.T) {
	ts := time.Date(2026, 6, 24, 17, 8, 45, 0, time.UTC)
	r := &SpiderWebDataResponse{
		URL:          "https://community.openai.com/categories",
		Title:        "Categories - OpenAI Developer Community",
		Content:      "Skip to main content",
		SessionID:    "spider_1782291938404111609",
		DataSourceID: 42,
		CrawlTime:    ts,
		Elements: &WebElements{
			Links: []WebElementLink{{Text: "x", Href: "/x", URL: "https://community.openai.com/x"}},
		},
		PageState: &PageState{URL: "https://community.openai.com/categories", Title: "Categories"},
	}

	out := map[string]interface{}{}
	buildFailureDataTopLevelFields(out, r, []string{"captcha_script_detected"}, true, "captcha")

	// 关键 session_id 必须出现在顶层（v2.0.7 文档约定）
	if got, ok := out["session_id"].(string); !ok || got != "spider_1782291938404111609" {
		t.Errorf("session_id missing or wrong at top level: %+v", out["session_id"])
	}
	if out["elements"] == nil {
		t.Error("elements missing at top level")
	}
	if out["page_state"] == nil {
		t.Error("page_state missing at top level")
	}
	if out["url"] != "https://community.openai.com/categories" {
		t.Error("url missing at top level")
	}
	if out["title"] != "Categories - OpenAI Developer Community" {
		t.Error("title missing at top level")
	}
	if out["content"] != "Skip to main content" {
		t.Error("content missing at top level")
	}
	if out["data_source_id"] != uint64(42) {
		t.Error("data_source_id missing at top level")
	}
	if out["crawl_time"] != ts {
		t.Error("crawl_time missing at top level")
	}
	hint, ok := out["anti_bot_hint"].(string)
	if !ok || hint == "" {
		t.Error("anti_bot_hint missing for captcha signal")
	}
	if !strings.Contains(hint, "验证码") && !strings.Contains(hint, "captcha") {
		t.Errorf("anti_bot_hint should mention captcha, got %q", hint)
	}
}

// TestBuildFailureDataTopLevelFields_NoHintForTimeout 验证非反爬分支不附加 anti_bot_hint
func TestBuildFailureDataTopLevelFields_NoHintForTimeout(t *testing.T) {
	r := &SpiderWebDataResponse{SessionID: "s1", URL: "u", Title: "t"}
	out := map[string]interface{}{}
	buildFailureDataTopLevelFields(out, r, nil, false, "")
	if _, ok := out["anti_bot_hint"]; ok {
		t.Error("anti_bot_hint should be absent when includeAntiBotHints=false")
	}
	if out["session_id"] != "s1" {
		t.Error("session_id must still be hoisted to top level for timeout/region_block/etc")
	}
}

// TestBuildFailureDataTopLevelFields_NilInput 验证 nil 入参不 panic
func TestBuildFailureDataTopLevelFields_NilInput(t *testing.T) {
	out := map[string]interface{}{}
	buildFailureDataTopLevelFields(out, nil, nil, true, "anti_bot")
	if len(out) != 0 {
		t.Errorf("expected empty output for nil input, got %+v", out)
	}
}

// TestBuildAntiBotHint 验证不同信号 → 不同提示文本
func TestBuildAntiBotHint(t *testing.T) {
	cases := []struct {
		name     string
		signals  []string
		mustHave string
	}{
		{"captcha", []string{"captcha_script_detected"}, "captcha"},
		{"blacklist", []string{"ua black list detected"}, "UA"},
		{"template", []string{"anti_bot_template:机器之心·数据服务"}, "推广模板"},
		{"generic", []string{"suspicious_short_content"}, "反爬"},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hint := buildAntiBotHint(c.signals)
			if c.mustHave == "" {
				if hint != "" {
					t.Errorf("expected empty hint for %v, got %q", c.signals, hint)
				}
				return
			}
			if !strings.Contains(hint, c.mustHave) {
				t.Errorf("hint %q must contain %q", hint, c.mustHave)
			}
		})
	}
}

// ==================== v2.0.24：buildTimeoutHint 测试 ====================
//
// 背景：spider_report_data_source_6_2026-07-02（TechCrunch case）连续 6 次
// "Crawl failed: CDP fetch failed: context deadline exceeded" 响应只有
// {"error_type":"timeout"}，Agent 无法决策下一步。v2.0.24 在 timeout 失败响应里
// 注入 timeout_hint，提示 Agent 改用 fallback_strategy=rss_first / 切数据源。
func TestBuildTimeoutHint_ContextDeadline(t *testing.T) {
	err := fmt.Errorf("CDP fetch failed: context deadline exceeded")
	hint := buildTimeoutHint(err, "https://techcrunch.com/")
	if hint == "" {
		t.Fatalf("expected non-empty hint for context deadline exceeded")
	}
	// 必须提到浏览器渲染层（而不是泛泛说"网络问题"）
	if !strings.Contains(hint, "渲染") && !strings.Contains(hint, "domcontentloaded") {
		t.Errorf("hint should explain it's a browser rendering issue, got %q", hint)
	}
	// 必须给出可执行的下一步：rss_first
	if !strings.Contains(hint, "fallback_strategy=rss_first") {
		t.Errorf("hint should suggest fallback_strategy=rss_first, got %q", hint)
	}
	// 必须包含目标站的首选 RSS 候选（v2.0.24 把 techcrunch.com 注册了）
	if !strings.Contains(hint, "techcrunch.com/feed") {
		t.Errorf("hint should include techcrunch.com/feed candidate, got %q", hint)
	}
}

func TestBuildTimeoutHint_NonTimeout(t *testing.T) {
	// 非 timeout 错误不应该返回 hint（避免给 Agent 错误信号）
	err := fmt.Errorf("connection refused: dial tcp 1.2.3.4:443")
	hint := buildTimeoutHint(err, "https://example.com/")
	if hint != "" {
		t.Errorf("expected empty hint for non-timeout error, got %q", hint)
	}
}

func TestBuildTimeoutHint_NilErr(t *testing.T) {
	hint := buildTimeoutHint(nil, "https://example.com/")
	if hint != "" {
		t.Errorf("expected empty hint for nil err, got %q", hint)
	}
}

func TestBuildTimeoutHint_UnknownHostStillSuggestsRSS(t *testing.T) {
	// 未知 host 也能建议通用 /feed /rss /atom.xml 探测
	err := fmt.Errorf("CDP fetch failed: context deadline exceeded")
	hint := buildTimeoutHint(err, "https://random-unknown-site-xyz.com/")
	if hint == "" {
		t.Fatalf("expected non-empty hint for timeout")
	}
	if !strings.Contains(hint, "/feed") && !strings.Contains(hint, "/rss") {
		t.Errorf("hint should suggest generic RSS paths, got %q", hint)
	}
}

// TestMCPSpiderWebDataFailureResponse_DataStructure 端到端：模拟反爬命中，
// 确认 (a) data.session_id 在顶层、(b) data.elements 在顶层、(c) data.partial_result.raw_html 被截断
func TestMCPSpiderWebDataFailureResponse_DataStructure(t *testing.T) {
	bigHTML := strings.Repeat("captcha ", 1<<18) // 256KB
	r := &SpiderWebDataResponse{
		URL:          "https://community.openai.com/categories",
		Title:        "Categories - OpenAI Developer Community",
		Content:      "Skip to main content",
		RawHTML:      bigHTML,
		SessionID:    "spider_1782291938404111609",
		DataSourceID: 1,
		CrawlTime:    time.Now().UTC(),
		Elements: &WebElements{
			Links: []WebElementLink{{Text: "x", Href: "/x", URL: "https://community.openai.com/x"}},
		},
		PageState: &PageState{URL: "https://community.openai.com/categories"},
	}
	signals := []string{"captcha_script_detected"}

	// 模拟 handler 失败分支构造的 data map
	respData := map[string]interface{}{
		"partial_result": buildPartialResultForFailure(r, "captcha"),
		"error_type":     "captcha",
		"signals":        signals,
	}
	buildFailureDataTopLevelFields(respData, r, signals, true, "captcha")

	// (a) 顶层 session_id
	if got, _ := respData["session_id"].(string); got != "spider_1782291938404111609" {
		t.Errorf("top-level session_id missing: %+v", respData["session_id"])
	}
	// (b) 顶层 elements
	if respData["elements"] == nil {
		t.Error("top-level elements missing")
	}
	// (c) partial_result 内 raw_html 已被截断
	pr, ok := respData["partial_result"].(*SpiderWebDataResponse)
	if !ok || pr == nil {
		t.Fatal("partial_result missing or wrong type")
	}
	if len(pr.RawHTML) > failurePartialHTMLCharLimit+200 {
		t.Errorf("partial_result.raw_html too large: %d", len(pr.RawHTML))
	}
	if !strings.Contains(pr.RawHTML, "truncated by MCP") {
		t.Error("expected truncation marker in partial_result.raw_html")
	}
	// (d) anti_bot_hint 应当存在
	if hint, _ := respData["anti_bot_hint"].(string); hint == "" {
		t.Error("anti_bot_hint missing")
	}
	// (e) JSON 序列化必须能完成（确保字段类型/JSON tag 正确）
	b, err := json.Marshal(respData)
	if err != nil {
		t.Fatalf("marshal respData failed: %v", err)
	}
	js := string(b)
	for _, must := range []string{
		`"session_id":"spider_1782291938404111609"`,
		`"error_type":"captcha"`,
		`"captcha_script_detected"`,
		`"anti_bot_hint"`,
		`"elements"`,
		`"partial_result"`,
	} {
		if !strings.Contains(js, must) {
			t.Errorf("JSON output missing %q\nfull: %s", must, js)
		}
	}
	// (f) raw_html 已被截断到 8KB 量级（远小于 256KB 原始大小）
	if len(pr.RawHTML) >= 200000 {
		t.Errorf("raw_html not aggressively truncated: %d bytes", len(pr.RawHTML))
	}
}

// TestClassifySpiderError_SpaNoEffect v2.0.11: spa_no_effect 错误分类
func TestClassifySpiderError_SpaNoEffect(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"spa_no_effect suffix", errString("click effect not detected (spa_no_effect)"), "spa_no_effect"},
		{"bare spa_no_effect", errString("spa_no_effect"), "spa_no_effect"},
		{"click effect not detected", errString("click effect not detected"), "spa_no_effect"},
		{"unrelated", errString("context deadline exceeded"), "timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifySpiderError(c.err); got != c.want {
				t.Errorf("classifySpiderError(%q) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// errString 让 error 接口满足一个常量字符串
type errString string

func (e errString) Error() string { return string(e) }

// TestClickEffectVerification_DefaultFields v2.0.11: 字段默认值契约（避免无意中改变 JSON tag）
func TestClickEffectVerification_DefaultFields(t *testing.T) {
	v := &ClickEffectVerification{}
	if v.EffectVerified {
		t.Error("EffectVerified default should be false")
	}
	if v.NetworkRequestsDelta != 0 {
		t.Error("NetworkRequestsDelta default should be 0")
	}
	if v.WaitMs != 0 {
		t.Error("WaitMs default should be 0")
	}
	if v.Warning != "" {
		t.Error("Warning default should be empty")
	}
}

// TestControlledInputDiagnostics_DefaultFields v2.0.11: 字段默认值契约
func TestControlledInputDiagnostics_DefaultFields(t *testing.T) {
	d := &ControlledInputDiagnostics{}
	if d.FrameworkConsumed {
		t.Error("FrameworkConsumed default should be false")
	}
	if d.HasValueTracker || d.HasVue || d.HasSan {
		t.Error("has* flags default should be false")
	}
	if d.DOMValue != "" || d.ReactTrackerValue != "" {
		t.Error("string fields default should be empty")
	}
}
