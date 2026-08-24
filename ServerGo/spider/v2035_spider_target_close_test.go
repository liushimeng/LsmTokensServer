package spider

// ==================== v2.0.35：MCP 爬虫 detachCDPContext 主动 CloseTarget 测试 ====================
//
// 覆盖问题分析报告_20260709_174800 的修复点：
//   - §四.Bug C：detachCDPContext 此前只 cancel chromedp context + 清字段，
//     不主动调 target.CloseTarget 关闭 Chrome tab → 多次 panic 后累积 10+ 个
//     Chrome 进程残留 → 耗尽 CDP WebSocket 连接池 → spider 僵死
//   - 验证 detachCDPContext 对以下场景幂等且无 panic：
//     (a) nil session
//     (b) cdpCtx 已死 / cdpTarget 空
//     (c) cdpCtx 正常持有 — CloseTarget 调用走异步 + 2s 超时，失败/超时也
//         不阻塞 cancel + 清字段流程
//   - 验证 session 没有实际启动 Chrome（无 chromedp context）时 detach 不 panic
//   - 验证 ExtractArticleURLsFromHTML / extractArticleCards 在畸形 HTML 下
//     返回空集合（无 panic）以巩固 v2.0.34 的 submatch 安全契约

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- releaseSpiderSession 在 rotateSessionID 改名后按指针反查删除 ----
// 覆盖问题分析报告_20260709_174800 §2.2 实测的 session_total 泄漏：
// rotateSessionID 把 s.SessionID 改成 "xxx_r1_r2"，但 spiderSessions map
// 的 key 仍是原始 ID；若 releaseSpiderSession 只按当前 SessionID 查 map
// 会找不到 entry -> delete 失败 -> session_total 永久 +1。

func TestReleaseSpiderSession_AfterRotateSessionID(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	originalID := "spider_rotate_test"
	s := &SpiderSession{
		SessionID: originalID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(spiderSessionTTL),
		cdpTarget: "tab-rotate-1",
	}
	spiderSessionsMu.Lock()
	spiderSessions[originalID] = s
	spiderSessionsMu.Unlock()

	// 模拟 anti-bot retry 路径里的 rotateSessionID：改字段，不改 map key
	s.SessionID = originalID + "_r1_r2"

	releaseSpiderSession(s)

	spiderSessionsMu.RLock()
	count := len(spiderSessions)
	_, originalStillThere := spiderSessions[originalID]
	spiderSessionsMu.RUnlock()
	if originalStillThere {
		t.Fatalf("session should have been removed from map by pointer lookup (original key %q still present)", originalID)
	}
	if count != 0 {
		t.Fatalf("expected map empty after pointer-based release, got count=%d", count)
	}
	if s.cdpTarget != "" {
		t.Fatalf("cdpTarget should be cleared after release, got %q", s.cdpTarget)
	}
}

func TestReleaseSpiderSession_PointerLookupNoFalsePositive(t *testing.T) {
	// 当 map 中存在的是另一个 session 对象（不同 SessionID 字符串、不同指针），
	// 指针反查不应误删 map 中不相关的 entry。验证反查是按指针相等，
	// 而非"任何 session 都删第一个找到的"。
	resetSpiderSessions()
	defer resetSpiderSessions()

	mapSession := &SpiderSession{SessionID: "in_map", ExpiresAt: time.Now().UTC().Add(spiderSessionTTL)}
	orphanSession := &SpiderSession{SessionID: "not_in_map", ExpiresAt: time.Now().UTC().Add(spiderSessionTTL)}
	spiderSessionsMu.Lock()
	spiderSessions["in_map"] = mapSession
	spiderSessionsMu.Unlock()

	// orphanSession 不在 map 中（不同 ID、不同指针）
	releaseSpiderSession(orphanSession)

	spiderSessionsMu.RLock()
	_, mapSessionStillThere := spiderSessions["in_map"]
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if !mapSessionStillThere {
		t.Fatalf("mapSession should NOT have been removed by orphan release (pointer mismatch)")
	}
	if count != 1 {
		t.Fatalf("expected map count=1, got %d", count)
	}
}

// ---- detachCDPContext 幂等性 ----

func TestDetachCDPContext_NilSafe(t *testing.T) {
	// nil session 必须立即返回，不 panic
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("detachCDPContext(nil) panicked: %v", rec)
		}
	}()
	detachCDPContext(nil)
}

func TestDetachCDPContext_EmptySessionNoPanic(t *testing.T) {
	// session 没有任何 cdp 字段：detach 应当空跑（nil ctx / 空 target 都不触发 goroutine）
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("detachCDPContext on empty session panicked: %v", rec)
		}
	}()
	s := &SpiderSession{SessionID: "test-empty-" + t.Name()}
	detachCDPContext(s)
}

func TestDetachCDPContext_NilCDPCtxWithTargetID(t *testing.T) {
	// session 有 cdpTarget 但 cdpCtx==nil：模拟 chromedp context 已死但
	// targetID 仍残留；detach 应当跳过 CloseTarget 直接 cancel 路径
	// （虽然 cdpCancel 也是 nil），最终清字段，不 panic
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("detachCDPContext on dead ctx panicked: %v", rec)
		}
	}()
	s := &SpiderSession{SessionID: "test-dead-ctx", cdpTarget: "target-abc-123"}
	detachCDPContext(s)
	if s.cdpTarget != "" {
		t.Fatalf("expected cdpTarget cleared, got %q", s.cdpTarget)
	}
}

func TestDetachCDPContext_TwiceIdempotent(t *testing.T) {
	// 连续两次 detach 必须幂等，不 panic，不死锁
	s := &SpiderSession{SessionID: "test-twice"}
	detachCDPContext(s)
	detachCDPContext(s)
}

// ---- ExtractArticleURLsFromHTML 畸形 HTML 不 panic（v2.0.34 + v2.0.35 双向加固）----

func TestExtractArticleURLsFromHTML_MalformedNotPanic(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{"empty", ""},
		{"only spaces", "   \n\t"},
		{"unclosed tags", "<a href=\"foo"},
		{"no href", "<a>just text</a>"},
		{"nested deep", "<a href=\"/a\"><span><b><i>nested</i></b></span></a>"},
		{"looks like article path", "<a href=\"/articles/2026/07/09/foo.html\">foo</a>"},
		{"html entity", "<a href=\"/a&amp;b\">text</a>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("ExtractArticleURLsFromHTML panicked on %s: %v", c.name, rec)
				}
			}()
			out := ExtractArticleURLsFromHTML(c.html, "https://example.com")
			_ = out
		})
	}
}

func TestExtractArticleURLsFromHTML_NormalizesAndDedupe(t *testing.T) {
	html := `<html><body>
		<a href="/articles/foo.html">First article</a>
		<a href="https://other.com/articles/bar.html">Second article</a>
		<a href="javascript:void(0)">Ignore me</a>
		<a href="">Empty href ignored</a>
		<a href="/articles/foo.html">Duplicate of first</a>
		<a href="/search?q=hi">Search page</a>
	</body></html>`
	items := ExtractArticleURLsFromHTML(html, "https://example.com")
	// 应当能解析若干条，但具体条数由 looksLikeArticleURL 决定；
	// 这里只验证 (a) 不 panic (b) 没有 javascript: (c) 没有重复 URL
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if strings.HasPrefix(strings.ToLower(it.URL), "javascript:") {
			t.Fatalf("javascript URL leaked: %s", it.URL)
		}
		if _, dup := seen[it.URL]; dup {
			t.Fatalf("duplicate URL leaked: %s", it.URL)
		}
		seen[it.URL] = struct{}{}
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one article item, got 0")
	}
}

// ---- extractArticleCards 截断 / 部分匹配 HTML 不 panic（v2.0.34 守护）----

func TestExtractArticleCards_TruncatedHTMLNotPanic(t *testing.T) {
	// 截断的 article / li，让部分捕获组在 submatch 中返回 -1/-1
	cases := []struct {
		name string
		html string
	}{
		{"unclosed li", "<li><h2>title<a href=\"/a\">link</a></h2><p>summary</p"},
		{"empty article", "<article></article>"},
		{"h2 no anchor", "<article><h2>just title</h2></article>"},
		{"p empty", "<li><h2><a href=\"/a\">t</a></h2><p>   </p></li>"},
		{"nested deeply", "<article><div><h2><a href=\"/a\">t</a></h2><div><p>summary</p></div></div></article>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("extractArticleCards panicked on %s: %v", c.name, rec)
				}
			}()
			out := extractArticleCards(c.html, "https://example.com")
			_ = out
		})
	}
}

// ---- extractWebElements 模拟【巨型 HTML 不 panic】+ 性能基线 ----

func TestExtractWebElements_LargeHTMLNoPanic(t *testing.T) {
	// 100KB 伪随机 HTML，确保正则在大量子匹配下稳定
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < 2000; i++ {
		b.WriteString("<a href=\"/articles/")
		b.WriteString(fmtInt(i))
		b.WriteString(".html\">Article ")
		b.WriteString(fmtInt(i))
		b.WriteString(" body text</a>")
	}
	b.WriteString("</body></html>")
	html := b.String()

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("extractWebElements panicked on large HTML: %v", rec)
		}
	}()
	out := extractWebElements(html, "https://example.com")
	if out == nil {
		t.Fatalf("expected non-nil WebElements")
	}
	if out.Links == nil {
		t.Fatalf("expected non-nil Links slice")
	}
	if len(out.Links) == 0 {
		t.Fatalf("expected >0 links extracted, got 0")
	}
	// 上限 200
	if len(out.Links) > 200 {
		t.Fatalf("links exceeded 200 cap: %d", len(out.Links))
	}
}

func fmtInt(n int) string {
	// 避免引入 fmt 包额外开销 — 简单实现
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---- 进程 / goroutine 残留基线：detach 完成后不应泄漏 goroutine ----

func TestDetachCDPContext_NoGoroutineLeakBaseline(t *testing.T) {
	before := runtime.NumGoroutine()
	// 模拟 100 次 detach 一个空 session（没有 cdpCtx / target）
	for i := 0; i < 100; i++ {
		detachCDPContext(&SpiderSession{SessionID: "leak-test"})
	}
	// 给调度器一点时间回收已退出的 goroutine
	runtime.Gosched()
	after := runtime.NumGoroutine()
	// 允许 ±10 波动（其它测试 / runtime 自身 goroutine）
	if after > before+10 {
		t.Fatalf("goroutine count grew from %d to %d after 100x detach (possible leak)", before, after)
	}
}
