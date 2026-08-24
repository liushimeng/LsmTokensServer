package spider

import "testing"

// TestExtractWebElements_Article 验证单篇文章页：1 个 H1 + 若干长段落 + 少链接
func TestExtractWebElements_Article(t *testing.T) {
	html := `<!doctype html><html><head><title>Test Article</title></head><body>
<nav><a href="/">Home</a><a href="/about">About</a></nav>
<article>
<h1>深入理解 Go 语言调度器</h1>
<a href="/articles/related-1">相关文章一</a>
<p>这是一段很长的中文段落，用于模拟正文内容。本段超过 60 个中文字符以确保被识别为正文候选段落。在 Go 语言中，调度器负责协调 goroutine 与系统线程的关系。</p>
<p>这是第二段正文，继续深入讨论 GMP 模型。G 代表 goroutine，M 代表 machine，P 代表 processor。Go 调度器通过 P 来管理本地 goroutine 队列，从而减少对全局锁的竞争提升并发性能与扩展能力。</p>
<p>第三段：抢占式调度在 Go 1.14 之前是基于协作式的，1.14 之后通过信号机制实现真正的抢占，从而避免某个 goroutine 长时间占用 CPU 导致整个程序无法响应外部事件或者调度器无法及时切换其他可运行 goroutine。</p>
</article>
</body></html>`

	els := extractWebElements(html, "https://example.com/articles/123")
	if els == nil {
		t.Fatal("nil elements")
	}

	if got := len(els.Headings); got != 1 {
		t.Errorf("headings: got %d, want 1", got)
	}
	if len(els.Headings) > 0 && els.Headings[0].Level != 1 {
		t.Errorf("heading level: got %d, want 1", els.Headings[0].Level)
	}

	if got := len(els.Paragraphs); got < 3 {
		t.Errorf("paragraphs: got %d, want >=3", got)
	}

	// 链接：nav 2 个 + article 内 1 个
	if got := len(els.Links); got != 3 {
		t.Errorf("links: got %d, want 3", got)
	}
	// 验证 scope 分布
	navCount, articleCount := 0, 0
	for _, l := range els.Links {
		switch l.Scope {
		case "nav":
			navCount++
		case "article":
			articleCount++
		}
		// 所有 URL 都必须已解析成绝对 URL
		if l.URL == "" || l.URL[:4] != "http" {
			t.Errorf("link URL not absolute: %q", l.URL)
		}
	}
	if navCount != 2 || articleCount != 1 {
		t.Errorf("scope: nav=%d article=%d, want 2/1", navCount, articleCount)
	}
}

// TestExtractWebElements_List 验证文章列表页：多个 H2 + 短段落摘要
func TestExtractWebElements_List(t *testing.T) {
	html := `<html><body>
<ul class="news-list">
<li><h2><a href="/n/1">新闻一：苹果发布新款手机</a></h2>
<p>短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要。</p></li>
<li><h2><a href="/n/2">新闻二：英伟达股价创新高</a></h2>
<p>短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要。</p></li>
<li><h2><a href="/n/3">新闻三：特斯拉自动驾驶更新</a></h2>
<p>短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要。</p></li>
<li><h2><a href="/n/4">新闻四：OpenAI 发布新模型</a></h2>
<p>短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要。</p></li>
<li><h2><a href="/n/5">新闻五：SpaceX 火箭发射成功</a></h2>
<p>短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要短摘要。</p></li>
</ul>
</body></html>`

	els := extractWebElements(html, "https://example.com/news")
	if got := len(els.Headings); got != 5 {
		t.Errorf("headings: got %d, want 5", got)
	}
	for i, h := range els.Headings {
		if h.URL == "" {
			t.Errorf("heading[%d] URL empty", i)
		}
	}
	// 列表页：链接应 >= 5，scope 主要是 list
	if got := len(els.Links); got < 5 {
		t.Errorf("links: got %d, want >=5", got)
	}
}

// TestExtractWebElements_NavOnly 验证导航/入口页：少 heading、零 paragraph、多数 link scope=nav
func TestExtractWebElements_NavOnly(t *testing.T) {
	html := `<html><body>
<nav>
<a href="/ai">人工智能</a>
<a href="/auto">智能驾驶</a>
<a href="/cloud">云计算</a>
<a href="/5g">5G 通信</a>
<a href="/iot">物联网</a>
</nav>
<div class="ad">广告内容</div>
</body></html>`

	els := extractWebElements(html, "https://example.com/")
	if got := len(els.Headings); got != 0 {
		t.Errorf("headings: got %d, want 0", got)
	}
	if got := len(els.Paragraphs); got != 0 {
		t.Errorf("paragraphs: got %d, want 0", got)
	}
	if got := len(els.Links); got < 5 {
		t.Errorf("links: got %d, want >=5", got)
	}
	for _, l := range els.Links {
		if l.Scope != "nav" {
			t.Errorf("link %q scope=%q, want nav", l.Text, l.Scope)
		}
	}
}

// TestExtractWebElements_RelativeURL 验证相对路径被解析成绝对 URL
func TestExtractWebElements_RelativeURL(t *testing.T) {
	html := `<html><body>
<a href="/abs">absolute path</a>
<a href="rel.html">relative file</a>
<a href="//cdn.example.com/x">protocol-relative</a>
<a href="https://other.example.com/y">fully qualified</a>
</body></html>`

	els := extractWebElements(html, "https://base.example.com/dir/page.html")
	want := map[string]string{
		"/abs":                        "https://base.example.com/abs",
		"rel.html":                    "https://base.example.com/dir/rel.html",
		"//cdn.example.com/x":         "https://cdn.example.com/x",
		"https://other.example.com/y": "https://other.example.com/y",
	}
	if len(els.Links) != len(want) {
		t.Fatalf("links: got %d, want %d", len(els.Links), len(want))
	}
	gotByText := map[string]string{}
	for _, l := range els.Links {
		gotByText[l.Href] = l.URL
	}
	for in, exp := range want {
		if gotByText[in] != exp {
			t.Errorf("href %q: got %q, want %q", in, gotByText[in], exp)
		}
	}
}

// TestExtractWebElements_Dedup 验证重复链接被去重
func TestExtractWebElements_Dedup(t *testing.T) {
	html := `<html><body>
<a href="/x">first</a>
<a href="/x">second</a>
<a href="/x">third</a>
</body></html>`

	els := extractWebElements(html, "https://example.com/")
	if got := len(els.Links); got != 1 {
		t.Errorf("links: got %d, want 1 (dedup)", got)
	}
}

// TestExtractWebElements_Empty 验证空 HTML 不 panic
func TestExtractWebElements_Empty(t *testing.T) {
	els := extractWebElements("", "https://example.com/")
	if els == nil {
		t.Fatal("nil elements")
	}
	if len(els.Links) != 0 || len(els.Headings) != 0 || len(els.Paragraphs) != 0 {
		t.Errorf("expected all empty, got %+v", els)
	}
	if els.Articles == nil {
		t.Errorf("Articles must be non-nil empty slice, not nil (JSON contract)")
	}
	if len(els.Articles) != 0 {
		t.Errorf("expected 0 articles, got %d", len(els.Articles))
	}
}

// TestExtractWebElements_ArticleCards 验证 v2.0.18 新增的列表型页面
// 「<li>/<article> 卡片内嵌 h2/h3 标题链接 + p 摘要」三元组抽取。
// 对应问题分析报告_20260629_143200 §3.2 反馈：elements.links 只有导航、
// elements.paragraphs 把多条文章拼成一个长段落。
func TestExtractWebElements_ArticleCards(t *testing.T) {
	html := `<html><body>
<nav><a href="/">Home</a></nav>
<ul class="article-list">
<li>
<h2><a href="/articles/1">大湾区有了第一家估值破200亿的「具身大脑」</a></h2>
<p>自变量达成融资奇迹。本文介绍大湾区具身智能赛道的产业格局与代表性企业，包括估值突破、技术路线与商业化进度。</p>
</li>
<li>
<h2><a href="/articles/2">多模态人形机器人运动生成框架</a></h2>
<p>一句话、一段音乐即可操纵机器人完成全身动作。本文介绍该框架的模型设计、训练数据与实际演示。</p>
</li>
<li>
<h2><a href="/articles/3">ICML 2026｜FLAG 扩散框架</a></h2>
<p>上智院、上交大、复旦联合提出 FLAG 扩散框架，还原空间转录组的基因-空间双重结构。</p>
</li>
</ul>
</body></html>`

	els := extractWebElements(html, "https://www.jiqizhixin.com/articles")
	if els == nil {
		t.Fatal("nil elements")
	}
	if got := len(els.Articles); got != 3 {
		t.Fatalf("articles: got %d, want 3", got)
	}

	// 标题 + URL + 摘要 对齐
	expected := []struct {
		title string
		url   string
	}{
		{"大湾区有了第一家估值破200亿的「具身大脑」", "https://www.jiqizhixin.com/articles/1"},
		{"多模态人形机器人运动生成框架", "https://www.jiqizhixin.com/articles/2"},
		{"ICML 2026｜FLAG 扩散框架", "https://www.jiqizhixin.com/articles/3"},
	}
	for i, want := range expected {
		got := els.Articles[i]
		if got.Title != want.title {
			t.Errorf("articles[%d].title: got %q, want %q", i, got.Title, want.title)
		}
		if got.URL != want.url {
			t.Errorf("articles[%d].url: got %q, want %q", i, got.URL, want.url)
		}
		if got.Summary == "" {
			t.Errorf("articles[%d].summary empty", i)
		}
		if got.Position != i {
			t.Errorf("articles[%d].position: got %d, want %d", i, got.Position, i)
		}
	}

	// 同时验证 nav 不被误识别为 article
	for _, a := range els.Articles {
		if a.URL == "https://www.jiqizhixin.com/" {
			t.Errorf("nav link should not appear as article: %+v", a)
		}
	}
}

// TestExtractWebElements_ArticleCards_ArticleTag 验证 <article> 标签也能
// 被识别（不限于 <li>），提升机器之心 / 36kr / 虎嗅 类站点的卡片抽取覆盖率。
func TestExtractWebElements_ArticleCards_ArticleTag(t *testing.T) {
	html := `<html><body>
<article>
<h2><a href="/post/a">深入理解 Transformer 注意力机制</a></h2>
<p>本文从第一性原理出发，深入剖析 Transformer 注意力机制的数学细节与工程实现。</p>
</article>
<article>
<h3><a href="/post/b">分布式训练中的 ZeRO 优化</a></h3>
<p>本文介绍 ZeRO 三阶段切分与显存优化策略，并给出 Megatron + DeepSpeed 集成示例。</p>
</article>
</body></html>`

	els := extractWebElements(html, "https://blog.example.com/")
	if got := len(els.Articles); got != 2 {
		t.Fatalf("articles: got %d, want 2", got)
	}
	if els.Articles[0].URL != "https://blog.example.com/post/a" {
		t.Errorf("articles[0].url: got %q", els.Articles[0].URL)
	}
	if els.Articles[1].URL != "https://blog.example.com/post/b" {
		t.Errorf("articles[1].url: got %q", els.Articles[1].URL)
	}
}

// TestExtractWebElements_ArticleCards_NoTitleLink 卡片内只有标题文本没有
// <a> 包裹时，应该跳过而不是产生空 URL 条目。
func TestExtractWebElements_ArticleCards_NoTitleLink(t *testing.T) {
	html := `<html><body>
<ul>
<li><h2>无链接标题</h2><p>摘要摘要摘要摘要摘要摘要摘要摘要摘要摘要。</p></li>
<li><h2><a href="/good">有链接标题</a></h2><p>摘要摘要摘要摘要摘要摘要摘要摘要摘要摘要。</p></li>
</ul>
</body></html>`

	els := extractWebElements(html, "https://example.com/list")
	if got := len(els.Articles); got != 1 {
		t.Fatalf("articles: got %d, want 1 (only titled-with-link)", got)
	}
	if els.Articles[0].URL != "https://example.com/good" {
		t.Errorf("articles[0].url: got %q", els.Articles[0].URL)
	}
}

// TestTruncateRunes 验证中文按 rune 截断
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello world", 5, "hello..."},
		{"你好世界", 2, "你好..."},
		{"abc", 10, "abc"},
		{"", 5, ""},
	}
	for _, c := range cases {
		got := truncateRunes(c.in, c.max)
		if got != c.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// TestStrconvAtoiSafe 验证简易 atoi
func TestStrconvAtoiSafe(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOk bool
	}{
		{"1", 1, true},
		{"3", 3, true},
		{"", 0, false},
		{"abc", 0, false},
		{"12x", 0, false},
	}
	for _, c := range cases {
		got, ok := strconvAtoiSafe(c.in)
		if ok != c.wantOk || (ok && got != c.want) {
			t.Errorf("strconvAtoiSafe(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.wantOk)
		}
	}
}
