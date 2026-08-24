package spider

// ==================== v2.0.25：fallback_strategy_hint / cookies.import / looks_like_search 单测 ====================
//
// 覆盖 v2.0.25 三项新增可观测性能力：
//   - buildFallbackStrategyHint(8x errType + 已知/unknown host RSS 建议)
//   - enrichRSSFallbackResponseWithLoginWall(jiqizhixin/36kr/huxiu/普通站)
//   - parseImportCookiesList (nil/非数组/缺 name/空数组/正常多 cookie)
//   - looksLikeArticleURL：/search?q= 模式 + 三个反例

import (
	"strings"
	"testing"
)

// ==================== buildFallbackStrategyHint ====================

func TestBuildFallbackStrategyHint_EmptyOrTimeout(t *testing.T) {
	cases := []string{"", "timeout", "timeout_hard", "unknown"}
	for _, errType := range cases {
		if got := buildFallbackStrategyHint(errType, "https://www.jiqizhixin.com/articles"); got != "" {
			t.Errorf("errType=%q: expected empty hint; got %q", errType, got)
		}
	}
}

func TestBuildFallbackStrategyHint_AntiBot(t *testing.T) {
	hint := buildFallbackStrategyHint("anti_bot", "")
	if hint == "" {
		t.Fatalf("expected non-empty hint for anti_bot")
	}
	if !strings.Contains(hint, "fallback_strategy") {
		t.Errorf("expected fallback_strategy suggestion; got %q", hint)
	}
	if !strings.Contains(hint, "rss_first") {
		t.Errorf("expected rss_first suggestion; got %q", hint)
	}
}

func TestBuildFallbackStrategyHint_Captcha(t *testing.T) {
	hint := buildFallbackStrategyHint("captcha", "")
	if !strings.Contains(hint, "fallback_strategy") {
		t.Errorf("expected fallback_strategy suggestion; got %q", hint)
	}
}

func TestBuildFallbackStrategyHint_KnownHostJiqizhixin(t *testing.T) {
	url := "https://www.jiqizhixin.com/articles"
	hint := buildFallbackStrategyHint("data_service_landing", url)
	if hint == "" {
		t.Fatalf("expected non-empty hint for data_service_landing on jiqizhixin")
	}
	// 应携带已知 RSS feed 候选
	if !strings.Contains(hint, "jiqizhixin.com/rss") {
		t.Errorf("expected known RSS candidate jiqizhixin.com/rss; got %q", hint)
	}
	if !strings.Contains(hint, "rss_first") {
		t.Errorf("expected rss_first suggestion; got %q", hint)
	}
}

func TestBuildFallbackStrategyHint_UnknownHost(t *testing.T) {
	// LookupRSSFallbackSources 对未知 host 也会返回通用兜底（/rss, /feed, /atom.xml），
	// 所以 buildFallbackStrategyHint 仍会输出"首选候选: <scheme>://<host>/rss"。
	// 这里只需校验：(1) 整体 hint 非空，(2) 包含 fallback_strategy=rss_first 字样。
	hint := buildFallbackStrategyHint("anti_bot", "https://www.example.com/some/page")
	if hint == "" {
		t.Fatalf("expected non-empty hint for unknown host")
	}
	if !strings.Contains(hint, "fallback_strategy") {
		t.Errorf("expected fallback_strategy suggestion; got %q", hint)
	}
	if !strings.Contains(hint, "rss_first") {
		t.Errorf("expected rss_first on unknown host; got %q", hint)
	}
}

func TestBuildFallbackStrategyHint_AllErrTypes(t *testing.T) {
	validTypes := []string{"anti_bot", "captcha", "region_block", "login_wall",
		"paywall", "data_service_landing", "session_invalid", "dns_unresolved"}
	for _, errType := range validTypes {
		if got := buildFallbackStrategyHint(errType, "https://www.36kr.com/news"); got == "" {
			t.Errorf("errType=%s expected non-empty; got empty", errType)
		}
	}
}

// ==================== enrichRSSFallbackResponseWithLoginWall ====================

func TestEnrichRSSFallbackResponseWithLoginWall_Jiqizhixin(t *testing.T) {
	resp := &SpiderWebDataResponse{
		RSSSourceURL: "https://rsshub.app/jiqizhixin",
		Warnings:     []string{},
	}
	enrichRSSFallbackResponseWithLoginWall(resp, "https://www.jiqizhixin.com/articles")
	if len(resp.Warnings) != 1 {
		t.Fatalf("expected 1 warning; got %d (%v)", len(resp.Warnings), resp.Warnings)
	}
	if !strings.Contains(resp.Warnings[0], "jiqizhixin.com") {
		t.Errorf("expected host mention; got %q", resp.Warnings[0])
	}
	if !strings.Contains(resp.Warnings[0], "RSSHub") && !strings.Contains(resp.Warnings[0], "RSS") {
		t.Errorf("expected RSS source mention; got %q", resp.Warnings[0])
	}
	if !strings.Contains(resp.Warnings[0], "op:import") {
		t.Errorf("expected cookie import hint; got %q", resp.Warnings[0])
	}
}

func TestEnrichRSSFallbackResponseWithLoginWall_36kr(t *testing.T) {
	resp := &SpiderWebDataResponse{RSSSourceURL: "https://rsshub.app/36kr", Warnings: nil}
	enrichRSSFallbackResponseWithLoginWall(resp, "https://36kr.com/news")
	if len(resp.Warnings) != 1 {
		t.Fatalf("expected 1 warning; got %d (%v)", len(resp.Warnings), resp.Warnings)
	}
	if !strings.Contains(resp.Warnings[0], "36kr.com") {
		t.Errorf("expected host mention; got %q", resp.Warnings[0])
	}
}

func TestEnrichRSSFallbackResponseWithLoginWall_NormalHost(t *testing.T) {
	// 未知 / 非付费墙站不追加任何 warning
	resp := &SpiderWebDataResponse{RSSSourceURL: "https://example.com/feed", Warnings: []string{"existing"}}
	enrichRSSFallbackResponseWithLoginWall(resp, "https://example.com/posts")
	if len(resp.Warnings) != 1 {
		t.Fatalf("expected 1 warning (unchanged); got %d (%v)", len(resp.Warnings), resp.Warnings)
	}
	if resp.Warnings[0] != "existing" {
		t.Errorf("expected unchanged warning; got %q", resp.Warnings[0])
	}
}

func TestEnrichRSSFallbackResponseWithLoginWall_NilResp(t *testing.T) {
	// nil resp 不应 panic
	enrichRSSFallbackResponseWithLoginWall(nil, "https://www.jiqizhixin.com/")
}

// ==================== parseImportCookiesList ====================

func TestParseImportCookiesList_Nil(t *testing.T) {
	if _, err := parseImportCookiesList(nil); err == nil {
		t.Fatalf("expected error for nil")
	}
}

func TestParseImportCookiesList_NotArray(t *testing.T) {
	if _, err := parseImportCookiesList("not-array"); err == nil {
		t.Fatalf("expected error for non-array")
	}
}

func TestParseImportCookiesList_Empty(t *testing.T) {
	// 空数组应返回空 slice，让上层报"empty"而不是 error here
	got, err := parseImportCookiesList([]interface{}{})
	if err != nil {
		t.Fatalf("expected no error for empty array; got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0; got %d", len(got))
	}
}

func TestParseImportCookiesList_NotObject(t *testing.T) {
	_, err := parseImportCookiesList([]interface{}{"bad"})
	if err == nil {
		t.Fatalf("expected error for object type")
	}
}

func TestParseImportCookiesList_MissingName(t *testing.T) {
	_, err := parseImportCookiesList([]interface{}{
		map[string]interface{}{"value": "abc"},
	})
	if err == nil {
		t.Fatalf("expected error for missing name")
	}
}

func TestParseImportCookiesList_OK(t *testing.T) {
	params, err := parseImportCookiesList([]interface{}{
		map[string]interface{}{"name": "SESSION", "value": "abc", "domain": ".jiqizhixin.com"},
		map[string]interface{}{"name": "PAID", "value": "1", "path": "/", "http_only": true, "secure": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 2 {
		t.Fatalf("expected 2; got %d", len(params))
	}
	if params[0].Name != "SESSION" || params[0].Value != "abc" || params[0].Domain != ".jiqizhixin.com" {
		t.Errorf("first cookie mismatch: %+v", params[0])
	}
	if params[1].Name != "PAID" || !params[1].HTTPOnly || !params[1].Secure || params[1].Path != "/" {
		t.Errorf("second cookie mismatch: %+v", params[1])
	}
}

func TestParseImportCookiesList_MissingValueOK(t *testing.T) {
	// 缺 value 不应报错，允许写 placeholder
	params, err := parseImportCookiesList([]interface{}{
		map[string]interface{}{"name": "EMPTY"},
	})
	if err != nil {
		t.Fatalf("expected no error for missing value; got %v", err)
	}
	if len(params) != 1 || params[0].Name != "EMPTY" {
		t.Errorf("unexpected params: %+v", params)
	}
}

// ==================== looksLikeArticleURL 搜索模式 ====================

func TestLooksLikeArticleURL_SearchQuery(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://www.jiqizhixin.com/search?q=AgentAReL 2.0Agentic", true},
		{"https://www.jiqizhixin.com/search?q=AI", true},
		{"https://www.jiqizhixin.com/search?keyword=AI", true},
		// 反例
		{"https://www.jiqizhixin.com/search", false},  // 裸 /search 不算（搜索入口页）
		{"https://www.jiqizhixin.com/search/", false}, // /search/ 也不带 query
		{"https://www.jiqizhixin.com/searchpage", false},
	}
	for _, c := range cases {
		if got := looksLikeArticleURL(c.raw); got != c.want {
			t.Errorf("looksLikeArticleURL(%q)=%v; want %v", c.raw, got, c.want)
		}
	}
}

// ==================== LoginWallAlternativeHints jiqizhixin 拓展 ====================

func TestLoginWallAlternativeHints_JiqizhixinRssFirstVariant(t *testing.T) {
	hints := LoginWallAlternativeHints("https://www.jiqizhixin.com/articles")
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints for jiqizhixin")
	}
	hasRssFirst := false
	hasImport := false
	for _, h := range hints {
		if strings.Contains(h, "fallback_strategy") && strings.Contains(h, "rss_first") {
			hasRssFirst = true
		}
		if strings.Contains(h, "op:import") {
			hasImport = true
		}
	}
	if !hasRssFirst {
		t.Errorf("expected fallback_strategy=rss_first recommendation; got hints=%v", hints)
	}
	if !hasImport {
		t.Errorf("expected op:import cookie hint; got hints=%v", hints)
	}
}

// ==================== appendWarning 行为 ====================

func TestAppendWarning_NilResp(t *testing.T) {
	var r *SpiderWebDataResponse
	r.appendWarning("ignored") // 不应 panic
}

func TestAppendWarning_FirstCall(t *testing.T) {
	r := &SpiderWebDataResponse{}
	r.appendWarning("first")
	if len(r.Warnings) != 1 || r.Warnings[0] != "first" {
		t.Errorf("unexpected warnings: %v", r.Warnings)
	}
	r.appendWarning("second")
	if len(r.Warnings) != 2 {
		t.Errorf("expected 2 warnings; got %v", r.Warnings)
	}
}
