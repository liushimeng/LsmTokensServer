package spider

// ==================== v2.0.18 patch2：登录墙检测 / 移动端降级 / session_id 轮换 单测 ====================
//
// 基于 问题分析报告_20260629_152341.md（机器之心 www.jiqizhixin.com 文章库采集）
// 覆盖 3 个新能力：
//   1. detectLoginWallSignals：识别登录墙 / 付费墙 / 数据服务 Landing Page
//   2. LoginWallAlternativeHints：基于 host 给出替代路径建议
//   3. MobileFallbackURL：桌面 URL → m.xxx.com
//   4. enrichFailureResponseWithLoginWall：失败响应追加登录墙字段
//   5. rotateSessionID：captcha 后 session_id 自动加 _r<N> 后缀

import (
	"strings"
	"testing"
)

// TestDetectLoginWallSignals_JiqizhixinLanding 模拟机器之心数据服务 Landing Page
// 必须命中 data_service_landing + login_wall signals
func TestDetectLoginWallSignals_JiqizhixinLanding(t *testing.T) {
	r := &SpiderWebDataResponse{
		URL:   "https://www.jiqizhixin.com/articles",
		Title: "Landing Page | 数据服务",
		Content: "机器之心·数据服务 - 赋能大模型：RSS/MCP/AI Skills 驱动的数据引擎。" +
			"已订阅文章库？点此 登录订阅文章库，解锁 30161 篇文章阅读。立即申请内测。",
		RawHTML: `<html><body>
			<header>数据服务 Landing Page</header>
			<h1>机器之心·数据服务</h1>
			<p>RSS / MCP / AI Skills 驱动的数据引擎</p>
			<a href="/login">登录订阅文章库</a>
			<a href="/rss">前往了解 RSS</a>
			<a href="/api">前往了解 API</a>
			<a class="cta" href="/apply">立即申请内测</a>
			<p>商务合作: zhaoyunfeng@jiqizhixin.com</p>
		</body></html>`,
	}
	lws := detectLoginWallSignals(r, r.URL)
	if !lws.Detected {
		t.Fatalf("expected login_wall detected; got signals=%v", lws.MatchedRules)
	}
	if lws.WallType != "data_service_landing" && lws.WallType != "login_wall" {
		t.Errorf("expected data_service_landing or login_wall; got %s", lws.WallType)
	}
	// 至少命中 2 条规则
	if len(lws.MatchedRules) < 2 {
		t.Errorf("expected at least 2 matched rules; got %d: %v", len(lws.MatchedRules), lws.MatchedRules)
	}
}

// TestDetectLoginWallSignals_NormalSite 模拟正常站点 — 不应误判
func TestDetectLoginWallSignals_NormalSite(t *testing.T) {
	r := &SpiderWebDataResponse{
		URL:     "https://github.com/foo/bar",
		Title:   "GitHub - foo/bar",
		Content: "Repository for foo/bar project. Contributors and documentation.",
		RawHTML: `<html><body><h1>foo/bar</h1><p>Some content here.</p></body></html>`,
	}
	lws := detectLoginWallSignals(r, r.URL)
	if lws.Detected {
		t.Errorf("false positive on normal GitHub page; signals=%v", lws.MatchedRules)
	}
}

// TestDetectLoginWallSignals_NilResult 边界：nil 输入不 panic
func TestDetectLoginWallSignals_NilResult(t *testing.T) {
	lws := detectLoginWallSignals(nil, "")
	if lws.Detected {
		t.Errorf("expected no detection for nil result")
	}
}

// TestDetectLoginWallSignals_PaywallURL URL 路径含 /subscribe 直接命中付费墙
func TestDetectLoginWallSignals_PaywallURL(t *testing.T) {
	r := &SpiderWebDataResponse{
		URL:     "https://example.com/subscribe",
		Title:   "订阅",
		Content: "立即订阅",
	}
	lws := detectLoginWallSignals(r, r.URL)
	if !lws.Detected {
		t.Fatalf("expected detection via URL path")
	}
	if lws.WallType != "paywall" {
		t.Errorf("expected paywall; got %s", lws.WallType)
	}
}

// TestLoginWallAlternativeHints_Jiqizhixin 已知站点专项建议
func TestLoginWallAlternativeHints_Jiqizhixin(t *testing.T) {
	hints := LoginWallAlternativeHints("https://www.jiqizhixin.com/articles")
	if len(hints) == 0 {
		t.Fatal("expected hints for jiqizhixin.com")
	}
	// 必须包含 RSS 链接
	foundRSS := false
	foundMobileHint := false
	for _, h := range hints {
		if strings.Contains(h, "jiqizhixin.com/rss") {
			foundRSS = true
		}
		if strings.Contains(h, "m.jiqizhixin.com") {
			foundMobileHint = true
		}
	}
	if !foundRSS {
		t.Errorf("expected RSS hint; got %v", hints)
	}
	if !foundMobileHint {
		t.Errorf("expected mobile fallback hint; got %v", hints)
	}
}

// TestLoginWallAlternativeHints_UnknownHost 未知 host 返回通用建议
func TestLoginWallAlternativeHints_UnknownHost(t *testing.T) {
	hints := LoginWallAlternativeHints("https://example-unknown-12345.com/path")
	if len(hints) < 3 {
		t.Errorf("expected at least 3 generic hints; got %d", len(hints))
	}
}

// TestLoginWallAlternativeHints_EmptyURL 空 URL 返回通用建议
func TestLoginWallAlternativeHints_EmptyURL(t *testing.T) {
	hints := LoginWallAlternativeHints("")
	if len(hints) == 0 {
		t.Fatal("expected fallback hints for empty URL")
	}
}

// TestMobileFallbackURL 桌面 URL → m.xxx.com
func TestMobileFallbackURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://www.jiqizhixin.com/articles", "https://m.jiqizhixin.com/articles"},
		{"https://jiqizhixin.com/articles", "https://m.jiqizhixin.com/articles"},
		{"http://www.example.com/foo/bar", "http://m.example.com/foo/bar"},
		{"https://m.jiqizhixin.com/articles", ""}, // 已是 m. 开头，跳过
		{"", ""},          // 空输入
		{"not-a-url", ""}, // 无协议
	}
	for _, tt := range tests {
		got := MobileFallbackURL(tt.input)
		if got != tt.want {
			t.Errorf("MobileFallbackURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestExtractHost host 提取
func TestExtractHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://www.jiqizhixin.com/articles", "www.jiqizhixin.com"},
		{"http://example.com", "example.com"},
		{"https://example.com:8080/path", "example.com"},
		{"", ""},
		{"not-a-url", "not-a-url"},
	}
	for _, tt := range tests {
		got := extractHost(tt.input)
		if got != tt.want {
			t.Errorf("extractHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestEnrichFailureResponseWithLoginWall 失败响应注入登录墙字段
func TestEnrichFailureResponseWithLoginWall(t *testing.T) {
	r := &SpiderWebDataResponse{
		URL:     "https://www.jiqizhixin.com/articles",
		Title:   "Landing Page | 数据服务",
		Content: "登录订阅文章库，解锁 30161 篇文章。立即申请内测。",
		RawHTML: "<html><body>数据服务 Landing Page</body></html>",
	}
	respData := map[string]interface{}{}
	enrichFailureResponseWithLoginWall(respData, r, r.URL)

	if _, ok := respData["login_wall_signals"]; !ok {
		t.Fatalf("expected login_wall_signals in response; got keys=%v", respDataKeys(respData))
	}
	if _, ok := respData["login_wall_signals"]; !ok {
		t.Errorf("missing login_wall_signals field")
	}
	if _, ok := respData["login_wall_alternative_hints"]; !ok {
		t.Errorf("missing login_wall_alternative_hints field")
	}
	if _, ok := respData["login_wall_hint"]; !ok {
		t.Errorf("missing login_wall_hint field")
	}
	warnings, ok := respData["warnings"].([]string)
	if !ok || len(warnings) == 0 {
		t.Errorf("expected warnings to be appended; got %v", respData["warnings"])
	}
}

// TestEnrichFailureResponseWithLoginWall_NoDetection 正常页面不注入字段
func TestEnrichFailureResponseWithLoginWall_NoDetection(t *testing.T) {
	r := &SpiderWebDataResponse{
		URL:     "https://github.com/foo",
		Title:   "GitHub - foo",
		Content: "Repository",
	}
	respData := map[string]interface{}{}
	enrichFailureResponseWithLoginWall(respData, r, r.URL)
	if _, ok := respData["login_wall_signals"]; ok {
		t.Errorf("unexpected login_wall_signals on normal page")
	}
	if _, ok := respData["login_wall_hint"]; ok {
		t.Errorf("unexpected login_wall_hint on normal page")
	}
}

// TestEnrichFailureResponseWithLoginWall_NilSafety nil 输入不 panic
func TestEnrichFailureResponseWithLoginWall_NilSafety(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on nil inputs: %v", r)
		}
	}()
	enrichFailureResponseWithLoginWall(nil, nil, "")
	enrichFailureResponseWithLoginWall(map[string]interface{}{}, nil, "")
}

// TestRotateSessionID session_id 加 _r<N> 后缀
func TestRotateSessionID(t *testing.T) {
	sess := &SpiderSession{
		SessionID: "spider_test_abc",
	}
	old := sess.SessionID
	newID := rotateSessionID(sess, 1)
	if newID == "" {
		t.Fatal("rotateSessionID returned empty")
	}
	if sess.SessionID != newID {
		t.Errorf("session.SessionID not updated in place: got %q, want %q", sess.SessionID, newID)
	}
	if sess.SessionID == old {
		t.Errorf("session_id not changed: still %q", old)
	}
	if !strings.HasSuffix(sess.SessionID, "_r1") {
		t.Errorf("expected _r1 suffix; got %q", sess.SessionID)
	}

	// 第二次调用应叠加 _r2
	newID2 := rotateSessionID(sess, 2)
	if !strings.HasSuffix(newID2, "_r2") {
		t.Errorf("expected _r2 suffix on second call; got %q", newID2)
	}
}

// TestRotateSessionID_NilSession nil 输入 no-op 不 panic
func TestRotateSessionID_NilSession(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on nil session: %v", r)
		}
	}()
	got := rotateSessionID(nil, 1)
	if got != "" {
		t.Errorf("expected empty string for nil session; got %q", got)
	}
}

// TestPickMobileUA 移动端 UA 池轮询
func TestPickMobileUA(t *testing.T) {
	if len(MobileUAPool) == 0 {
		t.Fatal("MobileUAPool is empty")
	}
	ua0 := PickMobileUA(0)
	ua1 := PickMobileUA(1)
	if ua0 == "" || ua1 == "" {
		t.Errorf("PickMobileUA returned empty: ua0=%q ua1=%q", ua0, ua1)
	}
	if ua0 == ua1 {
		t.Errorf("expected different UAs at idx 0/1; got both %q", ua0)
	}
	// 负数 idx 应回退到 0
	if PickMobileUA(-1) != ua0 {
		t.Errorf("negative idx should map to idx 0")
	}
}

// TestPriorityWallType 优先级比较
func TestPriorityWallType(t *testing.T) {
	// login_wall > data_service_landing > unknown
	if priorityWallType("login_wall") <= priorityWallType("data_service_landing") {
		t.Errorf("login_wall should outrank data_service_landing")
	}
	if priorityWallType("data_service_landing") <= priorityWallType("unknown") {
		t.Errorf("data_service_landing should outrank unknown")
	}
	if priorityWallType("paywall") <= priorityWallType("unknown") {
		t.Errorf("paywall should outrank unknown")
	}
}

// ==================== v2.0.19 回归测试（基于问题分析报告_20260630_095236）====================
//
// 覆盖报告 §3.1 / §3.2 / §6 建议：
//   - MobileFallbackURL 边界（空 host / 空 path / "https://"）不 panic、不返回畸形 URL
//   - extractContentSimpleWithLimit 在 maxContentLen > len(bestContent) 时不 panic
//   - resolveActionURL 在 session == nil 时返回空字符串、不 panic
//   - detectLoginWallSignals 在 nil response / nil url 时不 panic

// TestMobileFallbackURL_EdgeCases 边界场景：避免负索引 / 畸形 URL
func TestMobileFallbackURL_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"https prefix only", "https://", ""},
		{"path only", "/articles", ""},
		{"slash only", "/", ""},
		{"scheme + host with trailing slash", "https://example.com/", "https://m.example.com/"},
		{"uppercase scheme https", "HTTPS://example.com/foo", "https://m.example.com/foo"},
		{"weird www-less with port", "https://example.com:8080/x", "https://m.example.com:8080/x"},
		{"double m. prefix", "https://m.example.com/foo", ""}, // 已 m. 跳过
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("MobileFallbackURL(%q) panicked: %v", tt.input, r)
				}
			}()
			got := MobileFallbackURL(tt.input)
			if got != tt.want {
				t.Errorf("MobileFallbackURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestExtractContentSimpleWithLimit_NoPanicOnEdgeCases 报告 §3.1 建议 1：
// extractContentSimpleWithLimit 在 maxContentLen > len(bestContent) 时不应
// 触发 s[negative:maxContentLen] panic。已知当前代码已被外层 if 守卫，
// 这里仍加测试防止未来重构去掉守卫。
func TestExtractContentSimpleWithLimit_NoPanicOnEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		html string
		max  int
	}{
		{"empty html", "", 100},
		{"html shorter than max", "<p>hello</p>", 10000},
		{"max=0 default", "<p>foo</p>", 0},
		{"max=1 single char", "<p>a</p>", 1},
		{"html with no Chinese content but max huge", "<title>Empty</title>", 100000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("extractContentSimpleWithLimit panicked: html=%q max=%d err=%v", c.html, c.max, r)
				}
			}()
			out := extractContentSimpleWithLimit(c.html, c.max)
			if len(out) > c.max+10 { // +10 给可能的 "..." 后缀留余量
				t.Errorf("output len %d exceeds max %d by too much", len(out), c.max)
			}
		})
	}
}

// TestResolveActionURL_NilSafety 报告 §3.2 建议 3：resolveActionURL 在
// action / req / session 任一为 nil 时不应 panic。
func TestResolveActionURL_NilSafety(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("resolveActionURL panicked on nil inputs: %v", r)
		}
	}()
	sess := &SpiderSession{SessionID: "s1", CurrentURL: "https://cached.example/"}
	if got := resolveActionURL(nil, nil, nil); got != "" {
		t.Errorf("all nil should return empty string; got %q", got)
	}
	if got := resolveActionURL(&InteractiveAction{}, nil, nil); got != "" {
		t.Errorf("empty action+nil req+nil session should return empty; got %q", got)
	}
	if got := resolveActionURL(nil, &SpiderWebDataRequest{URL: "https://req.example/"}, nil); got != "https://req.example/" {
		t.Errorf("req.URL should be used; got %q", got)
	}
	if got := resolveActionURL(nil, nil, sess); got != "https://cached.example/" {
		t.Errorf("session.CurrentURL should be used; got %q", got)
	}
}

// TestDetectLoginWallSignals_NilSafetyAgain 报告 §6 建议 4：登录墙检测本身
// 在 crawlResult 为 nil（移动端 fallback 后没有抓取到任何内容）时不应 panic，
// 让外层能基于 wallType==false 走正常的 mobile fallback 路径。
func TestDetectLoginWallSignals_NilSafetyAgain(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("detectLoginWallSignals panicked on nil result: %v", r)
		}
	}()
	lws := detectLoginWallSignals(nil, "https://m.jiqizhixin.com/")
	if lws.Detected {
		t.Errorf("nil result should not detect login_wall; got %+v", lws)
	}
}

// ==================== 测试辅助函数 ====================

// respDataKeys 返回 map 所有 key（仅用于错误信息）
func respDataKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// r.URLDetectMatch 占位以避免 linter 报错（保留 helper 接口）
func (r *SpiderWebDataResponse) URLDetectMatch(m map[string]interface{}) bool {
	_, ok := m["login_wall_signals"]
	return ok
}
