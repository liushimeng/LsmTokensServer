package spider

// ==================== v2.0.20：RSS / Atom Feed 自动 fallback 单元测试 ====================
//
// 覆盖：
//   - LookupRSSFallbackSources：已知站点命中、未知站点兜底、URL 解析容错
//   - parseRSSOrAtom：RSS 2.0 / Atom 1.0 / 容错回退
//   - parseFlexibleDate：RFC1123 / RFC3339 / RFC822 / unix 时间戳 / 异常输入
//   - rssFetchResultToElements：RSSItem → WebElements 转换
//   - tryRSSFallbackForURL：rss_first 快速路径
//
// 不依赖外部网络；Feed 内容用 stub 字符串构造。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ==================== LookupRSSFallbackSources ====================

func TestLookupRSSFallbackSources_Empty(t *testing.T) {
	_, err := LookupRSSFallbackSources("")
	if err == nil {
		t.Errorf("expected error for empty URL, got nil")
	}
}

func TestLookupRSSFallbackSources_Invalid(t *testing.T) {
	_, err := LookupRSSFallbackSources("https://")
	if err == nil {
		t.Errorf("expected error for invalid URL, got nil")
	}
}

func TestLookupRSSFallbackSources_KnownJiqizhixin(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://www.jiqizhixin.com/articles")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !src.Known {
		t.Errorf("expected Known=true for jiqizhixin.com")
	}
	if len(src.Candidates) == 0 {
		t.Errorf("expected non-empty candidates")
	}
	// 必须包含 RSS 路径
	hasRSS := false
	for _, c := range src.Candidates {
		if strings.Contains(c, "/rss") || strings.Contains(c, "/feed") {
			hasRSS = true
			break
		}
	}
	if !hasRSS {
		t.Errorf("expected candidates to include /rss or /feed path, got %v", src.Candidates)
	}
}

func TestLookupRSSFallbackSources_KnownHostAlias(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://jiqizhixin.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !src.Known {
		t.Errorf("expected Known=true (without www)")
	}
	if len(src.Candidates) == 0 {
		t.Errorf("expected non-empty candidates")
	}
}

func TestLookupRSSFallbackSources_UnknownHost(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://example.com/articles")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Known {
		t.Errorf("expected Known=false for unknown host")
	}
	if len(src.Candidates) < 3 {
		t.Errorf("expected at least 3 generic candidates for unknown host, got %d", len(src.Candidates))
	}
}

func TestLookupRSSFallbackSources_UnknownWithFeedInPath(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://example.com/api/rss/articles")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// path 中已经含 rss / feed → 应直接作为候选
	if len(src.Candidates) == 0 {
		t.Errorf("expected candidates")
	}
	if !strings.Contains(src.Candidates[0], "/rss") && !strings.Contains(src.Candidates[0], "/feed") {
		t.Errorf("expected candidate containing /rss or /feed, got %v", src.Candidates[0])
	}
}

// ==================== parseRSSOrAtom ====================

func TestParseRSSOrAtom_RSSBasic(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>Test Feed</title>
    <link>https://example.com</link>
    <item>
      <title>Article One</title>
      <link>https://example.com/a/1</link>
      <description>First article body</description>
      <pubDate>Mon, 30 Jun 2026 14:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Article Two</title>
      <link>https://example.com/a/2</link>
      <description>Second article body</description>
      <pubDate>Sun, 29 Jun 2026 10:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`
	items, err := parseRSSOrAtom(body, "application/rss+xml")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "Article One" {
		t.Errorf("expected first item title 'Article One', got %q", items[0].Title)
	}
	if items[0].URL != "https://example.com/a/1" {
		t.Errorf("expected first item URL, got %q", items[0].URL)
	}
	if items[0].Summary != "First article body" {
		t.Errorf("expected summary, got %q", items[0].Summary)
	}
	if items[0].PublishedAt.IsZero() {
		t.Errorf("expected non-zero publishedAt")
	}
}

func TestParseRSSOrAtom_AtomBasic(t *testing.T) {
	body := `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Test Atom Feed</title>
  <entry>
    <title>Atom Article One</title>
    <id>tag:example.com,2026:1</id>
    <link href="https://example.com/atom/1"/>
    <summary>First atom summary</summary>
    <published>2026-06-30T14:00:00Z</published>
    <author><name>John Doe</name></author>
  </entry>
</feed>`
	items, err := parseRSSOrAtom(body, "application/atom+xml")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Atom Article One" {
		t.Errorf("expected 'Atom Article One', got %q", items[0].Title)
	}
	if items[0].Author != "John Doe" {
		t.Errorf("expected author 'John Doe', got %q", items[0].Author)
	}
	if items[0].URL != "https://example.com/atom/1" {
		t.Errorf("expected URL, got %q", items[0].URL)
	}
}

func TestParseRSSOrAtom_EmptyBody(t *testing.T) {
	_, err := parseRSSOrAtom("", "text/xml")
	if err == nil {
		t.Errorf("expected error for empty body")
	}
}

func TestParseRSSOrAtom_AutoDetectByContent(t *testing.T) {
	// content-type 未指定，但 body 是 RSS
	body := `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <item><title>Auto RSS</title><link>https://x.com/1</link></item>
  </channel>
</rss>`
	items, err := parseRSSOrAtom(body, "")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Auto RSS" {
		t.Errorf("expected auto-detected RSS item, got %v", items)
	}
}

func TestParseRSSOrAtom_UnknownFormat(t *testing.T) {
	body := `not xml at all`
	_, err := parseRSSOrAtom(body, "")
	if err == nil {
		t.Errorf("expected error for unknown format")
	}
}

// ==================== parseFlexibleDate ====================

func TestParseFlexibleDate_RFC1123(t *testing.T) {
	dt := parseFlexibleDate("Mon, 30 Jun 2026 14:00:00 +0000")
	if dt.IsZero() {
		t.Errorf("expected non-zero date")
	}
	if dt.Year() != 2026 || dt.Month() != time.June || dt.Day() != 30 {
		t.Errorf("unexpected parsed date: %v", dt)
	}
}

func TestParseFlexibleDate_RFC3339(t *testing.T) {
	dt := parseFlexibleDate("2026-06-30T14:00:00Z")
	if dt.IsZero() {
		t.Errorf("expected non-zero date")
	}
	if dt.Year() != 2026 {
		t.Errorf("unexpected year: %d", dt.Year())
	}
}

func TestParseFlexibleDate_RFC822(t *testing.T) {
	dt := parseFlexibleDate("30 Jun 26 14:00 +0000")
	if dt.IsZero() {
		t.Errorf("expected non-zero date for RFC822")
	}
}

func TestParseFlexibleDate_UnixSeconds(t *testing.T) {
	dt := parseFlexibleDate("1719848400") // 2026-07-01 ish
	if dt.IsZero() {
		t.Errorf("expected non-zero date for unix seconds")
	}
}

func TestParseFlexibleDate_Empty(t *testing.T) {
	dt := parseFlexibleDate("")
	if !dt.IsZero() {
		t.Errorf("expected zero time for empty input")
	}
}

func TestParseFlexibleDate_Garbage(t *testing.T) {
	dt := parseFlexibleDate("not a date")
	if !dt.IsZero() {
		t.Errorf("expected zero time for garbage input")
	}
}

// ==================== rssFetchResultToElements ====================

func TestRssFetchResultToElements_NilResult(t *testing.T) {
	links, headings, articles := rssFetchResultToElements(nil)
	if len(links) != 0 || len(headings) != 0 || len(articles) != 0 {
		t.Errorf("expected empty slices for nil input")
	}
}

func TestRssFetchResultToElements_EmptyItems(t *testing.T) {
	r := &RSSFetchResult{Items: []RSSItem{}}
	links, headings, articles := rssFetchResultToElements(r)
	if len(links) != 0 || len(headings) != 0 || len(articles) != 0 {
		t.Errorf("expected empty slices for empty items")
	}
}

func TestRssFetchResultToElements_WithItems(t *testing.T) {
	r := &RSSFetchResult{
		Items: []RSSItem{
			{
				Title:       "Article 1",
				URL:         "https://example.com/1",
				Summary:     "First summary",
				Author:      "Alice",
				PublishedAt: time.Date(2026, 6, 30, 14, 0, 0, 0, time.UTC),
				Tags:        []string{"AI", "Tech"},
			},
		},
	}
	links, headings, articles := rssFetchResultToElements(r)
	if len(links) != 1 || links[0].Text != "Article 1" || links[0].URL != "https://example.com/1" {
		t.Errorf("unexpected links: %v", links)
	}
	if len(headings) != 1 || headings[0].Level != 2 || headings[0].Text != "Article 1" {
		t.Errorf("unexpected headings: %v", headings)
	}
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].Title != "Article 1" {
		t.Errorf("unexpected article title: %q", articles[0].Title)
	}
	if articles[0].Position != 0 {
		t.Errorf("expected position 0, got %d", articles[0].Position)
	}
	// summary 应包含 author/date/tags 元数据（格式：@author (YYYY-MM-DD) [tag,tag]）
	if !strings.Contains(articles[0].Summary, "@Alice") {
		t.Errorf("expected summary to include @Alice, got %q", articles[0].Summary)
	}
	if !strings.Contains(articles[0].Summary, "2026-06-30") {
		t.Errorf("expected summary to include date, got %q", articles[0].Summary)
	}
	if !strings.Contains(articles[0].Summary, "[AI,Tech]") {
		t.Errorf("expected summary to include tags, got %q", articles[0].Summary)
	}
}

// ==================== FetchRSSTries（含 httptest） ====================

func TestFetchRSSTries_HTTPServer(t *testing.T) {
	// 起一个本地 httptest server，返回 RSS 内容
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Local Test Feed</title>
    <item><title>Local Article 1</title><link>https://localhost/1</link></item>
    <item><title>Local Article 2</title><link>https://localhost/2</link></item>
  </channel>
</rss>`))
	}))
	defer server.Close()

	sources := RSSFallbackSource{
		Candidates: []string{server.URL + "/feed"},
		Known:      true,
	}
	client := &http.Client{Timeout: 5 * time.Second}
	res := FetchRSSTries(context.Background(), sources, client, 10)

	if !res.Success {
		t.Fatalf("expected Success=true, got %v (errType=%s, err=%s)",
			res.TriedURLs, res.ErrorType, res.ErrorMsg)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.SourceURL != sources.Candidates[0] {
		t.Errorf("expected SourceURL %q, got %q", sources.Candidates[0], res.SourceURL)
	}
	if res.HTTPStatus != 200 {
		t.Errorf("expected HTTPStatus=200, got %d", res.HTTPStatus)
	}
}

func TestFetchRSSTries_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	sources := RSSFallbackSource{
		Candidates: []string{server.URL + "/feed"},
		Known:      true,
	}
	res := FetchRSSTries(context.Background(), sources, nil, 10)

	if res.Success {
		t.Errorf("expected Success=false for 404 response")
	}
	if res.HTTPStatus != 404 {
		t.Errorf("expected HTTPStatus=404, got %d", res.HTTPStatus)
	}
	if res.ErrorType != "not_found" {
		t.Errorf("expected error_type=not_found, got %s", res.ErrorType)
	}
	if len(res.TriedURLs) != 1 || res.TriedURLs[0] != sources.Candidates[0] {
		t.Errorf("expected TriedURLs contains the only candidate, got %v", res.TriedURLs)
	}
}

func TestFetchRSSTries_PickFirst(t *testing.T) {
	// 第一个 404，第二个 200 — 必须顺序尝试并返回第二个
	calls := []int{0, 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			calls[0]++
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/second" {
			calls[1]++
			w.WriteHeader(200)
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel><item><title>Second Feed</title><link>https://x.com/2</link></item></channel>
</rss>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	sources := RSSFallbackSource{
		Candidates: []string{server.URL + "/first", server.URL + "/second"},
		Known:      false,
	}
	res := FetchRSSTries(context.Background(), sources, nil, 10)

	if !res.Success {
		t.Fatalf("expected Success=true (second should succeed), got errorType=%s", res.ErrorType)
	}
	if calls[0] != 1 || calls[1] != 1 {
		t.Errorf("expected both endpoints called once, got %v", calls)
	}
	if len(res.Items) != 1 || res.Items[0].Title != "Second Feed" {
		t.Errorf("expected second feed item, got %v", res.Items)
	}
}

func TestFetchRSSTries_EmptyFeed(t *testing.T) {
	// RSS 解析成功但 0 items
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel><title>Empty Feed</title></channel>
</rss>`))
	}))
	defer server.Close()

	sources := RSSFallbackSource{Candidates: []string{server.URL + "/feed"}}
	res := FetchRSSTries(context.Background(), sources, nil, 10)

	if res.Success {
		t.Errorf("expected Success=false for empty feed")
	}
}

// ==================== rssToSpiderResponse ====================

func TestRssToSpiderResponse_Basic(t *testing.T) {
	rssRes := RSSFetchResult{
		Success:   true,
		SourceURL: "https://example.com/feed",
		Items: []RSSItem{
			{
				Title:       "First Article",
				URL:         "https://example.com/1",
				Summary:     "First summary",
				PublishedAt: time.Date(2026, 6, 30, 14, 0, 0, 0, time.UTC),
			},
			{
				Title:       "Second Article",
				URL:         "https://example.com/2",
				Summary:     "Second summary",
				PublishedAt: time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC),
			},
		},
		HTTPStatus: 200,
	}

	resp := rssToSpiderResponse(rssRes, "https://www.jiqizhixin.com/articles")
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if !resp.RSSFallbackUsed {
		t.Errorf("expected RSSFallbackUsed=true")
	}
	if resp.RSSSourceURL != "https://example.com/feed" {
		t.Errorf("expected RSSSourceURL, got %q", resp.RSSSourceURL)
	}
	if resp.RSSItemCount != 2 {
		t.Errorf("expected RSSItemCount=2, got %d", resp.RSSItemCount)
	}
	if !resp.HasMore {
		t.Errorf("expected HasMore=true")
	}
	if resp.Elements == nil {
		t.Fatal("expected Elements not nil")
	}
	if len(resp.Elements.Articles) != 2 {
		t.Errorf("expected 2 articles, got %d", len(resp.Elements.Articles))
	}
	if !strings.Contains(resp.Content, "First Article") {
		t.Errorf("expected content to include First Article: %s", resp.Content)
	}
	if resp.Title == "" {
		t.Errorf("expected non-empty title")
	}
}

// ==================== shouldTryRSSFallback ====================

func TestShouldTryRSSFallback_NoneStrategy(t *testing.T) {
	if shouldTryRSSFallback("anti_bot", nil, "none") {
		t.Errorf("expected shouldTryRSSFallback=false for strategy=none")
	}
}

func TestShouldTryRSSFallback_RSSFirst(t *testing.T) {
	if !shouldTryRSSFallback("invalid_type", nil, "rss_first") {
		t.Errorf("expected shouldTryRSSFallback=true for rss_first strategy (always)")
	}
}

func TestShouldTryRSSFallback_Auto_AntiBot(t *testing.T) {
	if !shouldTryRSSFallback("anti_bot", nil, "auto") {
		t.Errorf("expected shouldTryRSSFallback=true for anti_bot")
	}
	if !shouldTryRSSFallback("captcha", nil, "auto") {
		t.Errorf("expected shouldTryRSSFallback=true for captcha")
	}
	if !shouldTryRSSFallback("region_block", nil, "auto") {
		t.Errorf("expected shouldTryRSSFallback=true for region_block")
	}
	if !shouldTryRSSFallback("timeout", nil, "auto") {
		t.Errorf("expected shouldTryRSSFallback=true for timeout")
	}
}

func TestShouldTryRSSFallback_Auto_LoginWall(t *testing.T) {
	// 即便 errType 不在以上列表，但 crawlResult 命中 login_wall → 启用 fallback
	crawlResult := &SpiderWebDataResponse{
		URL:     "https://www.jiqizhixin.com/articles",
		Title:   "数据服务 Landing Page",
		Content: "RSS/MCP 数据引擎",
	}
	if !shouldTryRSSFallback("invalid_type", crawlResult, "auto") {
		t.Errorf("expected shouldTryRSSFallback=true when login_wall detected")
	}
}

func TestShouldTryRSSFallback_Auto_NoMatch(t *testing.T) {
	if shouldTryRSSFallback("some_other_error", nil, "auto") {
		t.Errorf("expected shouldTryRSSFallback=false for unrecognized errType")
	}
}

// ==================== tryRSSFallbackForURL ====================

func TestTryRSSFallbackForURL_Empty(t *testing.T) {
	_, used := tryRSSFallbackForURL("", "rss_first", 10)
	if used {
		t.Errorf("expected used=false for empty URL")
	}
}

func TestTryRSSFallbackForURL_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <item><title>RSS First Item</title><link>https://test.com/1</link></item>
  </channel>
</rss>`))
	}))
	defer server.Close()

	resp, used := tryRSSFallbackForURL(server.URL+"/articles", "rss_first", 10)
	if !used {
		t.Fatalf("expected used=true")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if !resp.RSSFallbackUsed {
		t.Errorf("expected RSSFallbackUsed=true")
	}
	if resp.RSSItemCount != 1 {
		t.Errorf("expected 1 item, got %d", resp.RSSItemCount)
	}
	if len(resp.Warnings) == 0 {
		t.Errorf("expected warnings to indicate rss_first strategy")
	} else if !strings.Contains(resp.Warnings[0], "rss_first") {
		t.Errorf("expected warning to contain 'rss_first', got %q", resp.Warnings[0])
	}
}

// ==================== v2.0.21：第三方 RSS 聚合 + HTML 兜底抽取 ====================

// TestMatchAggregatorFeeds_KnownHost 验证机器之心等已知 host 能命中第三方聚合
func TestMatchAggregatorFeeds_KnownHost(t *testing.T) {
	got := matchAggregatorFeeds("www.jiqizhixin.com jiqizhixin.com")
	if len(got) == 0 {
		t.Errorf("expected at least 1 aggregator feed for jiqizhixin.com, got 0")
	}
	hasRsshub := false
	for _, u := range got {
		if strings.Contains(u, "rsshub") || strings.Contains(u, "injahow") {
			hasRsshub = true
			break
		}
	}
	if !hasRsshub {
		t.Errorf("expected rsshub/injahow aggregator, got %v", got)
	}
}

// TestMatchAggregatorFeeds_UnknownHost 验证未知 host 不命中聚合
func TestMatchAggregatorFeeds_UnknownHost(t *testing.T) {
	got := matchAggregatorFeeds("unknown-site-12345.com")
	if len(got) != 0 {
		t.Errorf("expected no aggregator feeds for unknown host, got %v", got)
	}
}

// TestLookupRSSFallbackSources_AggregatorForJiqizhixin 验证已知站点 lookup 返回 aggregator feeds
func TestLookupRSSFallbackSources_AggregatorForJiqizhixin(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://www.jiqizhixin.com/articles")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(src.AggregatorFeeds) == 0 {
		t.Errorf("expected non-empty AggregatorFeeds for jiqizhixin.com, got 0")
	}
}

// TestLooksLikeArticleURL 验证文章 URL 启发式识别
func TestLooksLikeArticleURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.jiqizhixin.com/articles/12345", true},
		{"https://example.com/news/foo-bar", true},
		{"https://blog.example.com/post/2026/06/30/title", true},
		{"https://www.jiqizhixin.com/data-service", false}, // 没有 /article /news 关键字
		{"https://example.com/about", false},
		{"", false},
		{"javascript:void(0)", false},
		{"#anchor", false},
		{"https://www.jiqizhixin.com/index.html", false}, // 排除首页
		{"https://www.jiqizhixin.com/article/2026/06/foo.html", true},
	}
	for _, c := range cases {
		got := looksLikeArticleURL(c.url)
		if got != c.want {
			t.Errorf("looksLikeArticleURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// TestExtractArticleURLsFromHTML_Basic 验证从 HTML 中提取文章 URL
func TestExtractArticleURLsFromHTML_Basic(t *testing.T) {
	html := `<html><body>
		<nav><a href="/">Home</a><a href="/about">About</a></nav>
		<main>
			<a href="/articles/12345">First Article Title Long Enough</a>
			<a href="/articles/67890">Second Article Title Long Enough</a>
			<a href="/data-service">数据服务</a>
			<a href="/login">登录</a>
			<a href="javascript:void(0)">Click</a>
		</main>
	</body></html>`
	items := ExtractArticleURLsFromHTML(html, "https://www.jiqizhixin.com/")
	if len(items) != 2 {
		t.Errorf("expected 2 article URLs, got %d: %+v", len(items), items)
	}
	for _, it := range items {
		if !strings.HasPrefix(it.URL, "https://www.jiqizhixin.com/articles/") {
			t.Errorf("expected absolute article URL, got %q", it.URL)
		}
	}
}

// TestExtractArticleURLsFromHTML_Empty 验证空 HTML 返回 nil
func TestExtractArticleURLsFromHTML_Empty(t *testing.T) {
	items := ExtractArticleURLsFromHTML("", "https://example.com/")
	if items != nil {
		t.Errorf("expected nil for empty HTML, got %+v", items)
	}
}

// TestExtractArticleURLsFromHTML_NoArticles 验证无文章 URL 时返回空
func TestExtractArticleURLsFromHTML_NoArticles(t *testing.T) {
	html := `<html><body>
		<nav><a href="/">Home</a><a href="/about">About</a><a href="/data-service">服务</a></nav>
	</body></html>`
	items := ExtractArticleURLsFromHTML(html, "https://www.jiqizhixin.com/")
	if len(items) != 0 {
		t.Errorf("expected 0 article URLs, got %d: %+v", len(items), items)
	}
}

// TestExtractArticleURLsFromHTML_Dedup 验证 URL 去重
func TestExtractArticleURLsFromHTML_Dedup(t *testing.T) {
	html := `<html><body>
		<a href="/articles/12345">First Long Article Title</a>
		<a href="/articles/12345">Same URL Different Text Long Enough</a>
		<a href="/articles/12345">Third Occurrence Long Enough Text</a>
	</body></html>`
	items := ExtractArticleURLsFromHTML(html, "https://example.com/")
	if len(items) != 1 {
		t.Errorf("expected 1 deduped item, got %d: %+v", len(items), items)
	}
}

// TestTryRSSFallbackForFailure_AggregatorFallthrough 验证主候选失败后
// 第三方聚合候选会被尝试（v2.0.21 行为）
func TestTryRSSFallbackForFailure_AggregatorFallthrough(t *testing.T) {
	// httptest 第一个 endpoint 返回 404，模拟官方 /rss 跳走
	// 第二个 endpoint 返回有效 RSS，模拟第三方 RSSHub
	calls := []int{0, 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rss" {
			calls[0]++
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/jiqizhixin" {
			calls[1]++
			w.Header().Set("Content-Type", "application/rss+xml")
			w.WriteHeader(200)
			w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <item><title>Aggregator Item 1</title><link>https://x.com/1</link></item>
  </channel>
</rss>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	// 构造 sources：主候选 + 第三方聚合候选
	sources := RSSFallbackSource{
		Candidates:      []string{server.URL + "/rss"},
		AggregatorFeeds: []string{server.URL + "/jiqizhixin"},
		Known:           true,
	}
	res := FetchRSSTries(context.Background(), sources, nil, 10)
	if !res.Success {
		t.Fatalf("expected success via aggregator, got errorType=%s, errorMsg=%s", res.ErrorType, res.ErrorMsg)
	}
	if calls[0] != 1 || calls[1] != 1 {
		t.Errorf("expected both endpoints called once, got main=%d, aggregator=%d", calls[0], calls[1])
	}
	if res.SourceURL != server.URL+"/jiqizhixin" {
		t.Errorf("expected SourceURL to be aggregator, got %q", res.SourceURL)
	}
	if len(res.Items) != 1 || res.Items[0].Title != "Aggregator Item 1" {
		t.Errorf("expected aggregator item, got %+v", res.Items)
	}
}

// ==================== v2.0.24：TechCrunch RSS fallback 测试 ====================
//
// 背景：spider_report_data_source_6_2026-07-02（TechCrunch case）显示
// 该站在 headless Chrome 下反复 CDP timeout，但官方 RSS feed 稳定可读。
// v2.0.24 把 techcrunch.com / www.techcrunch.com 加入 knownRSSFeedCandidates
// 让 RSS fallback 一次命中。
func TestLookupRSSFallbackSources_TechCrunch(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://techcrunch.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !src.Known {
		t.Errorf("expected Known=true (registered in knownRSSFeedCandidates)")
	}
	if len(src.Candidates) == 0 {
		t.Fatalf("expected non-empty candidates for techcrunch.com")
	}
	// 第一个候选必须是 /feed/（TechCrunch 官方主 feed）
	if !strings.Contains(src.Candidates[0], "techcrunch.com/feed") {
		t.Errorf("expected first candidate to contain 'techcrunch.com/feed', got %q", src.Candidates[0])
	}
	// Aggregator 应该也命中（rsshub.app/techcrunch）
	hasAgg := false
	for _, a := range src.AggregatorFeeds {
		if strings.Contains(a, "rsshub.app/techcrunch") {
			hasAgg = true
			break
		}
	}
	if !hasAgg {
		t.Errorf("expected rsshub.app/techcrunch in AggregatorFeeds, got %v", src.AggregatorFeeds)
	}
}

func TestLookupRSSFallbackSources_TechCrunchWWW(t *testing.T) {
	src, err := LookupRSSFallbackSources("https://www.techcrunch.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !src.Known {
		t.Errorf("expected Known=true for www.techcrunch.com (alias registered)")
	}
	if len(src.Candidates) == 0 {
		t.Fatalf("expected non-empty candidates for www.techcrunch.com")
	}
	if !strings.Contains(src.Candidates[0], "techcrunch.com/feed") {
		t.Errorf("expected first candidate to be techcrunch.com/feed, got %q", src.Candidates[0])
	}
}
