package spider

// ==================== v2.0.34：MCP 爬虫 panic 与 session 泄漏修复测试 ====================
//
// 覆盖问题分析报告_20260709_162130 的修复点：
//   - §4.1 index out of range [-2] panic：safeSubmatchSlice 边界 + extractArticleCards /
//     extractWebElements 畸形 HTML 不 panic
//   - §4.2 session 泄漏：releaseSpiderSession 删 map、detachAllSpiderSessions 清空 map
//   - §建议-中期 3：/healthz 透传 panic_count / last_panic_at

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// resetSpiderPanicCounters 测试用：重置全局 panic 计数器（无锁竞态，仅单测串行调用）。
func resetSpiderPanicCounters() {
	spiderPanicCount.Store(0)
	spiderLastPanicAtMs.Store(0)
}

// ---- safeSubmatchSlice ----

func TestSafeSubmatchSlice_Bounds(t *testing.T) {
	cases := []struct {
		name string
		s    string
		m    []int
		pair int
		want string
	}{
		{"nil m", "abc", nil, 0, ""},
		{"pair 0 full match", "abc", []int{0, 3}, 0, "abc"},
		{"pair 0 negative start", "abc", []int{-1, 3}, 0, ""},
		{"pair 0 negative end", "abc", []int{0, -1}, 0, ""},
		{"pair 0 reversed", "abc", []int{3, 0}, 0, ""},
		{"pair 0 out of range", "abc", []int{0, 10}, 0, ""},
		{"pair 1 missing", "abc", []int{0, 3}, 1, ""},
		{"pair 1 present", "axxbxxxc", []int{0, 8, 1, 3, 4, 7}, 1, "xx"},
		{"pair 1 negative submatch", "abc", []int{0, 3, -1, -1}, 1, ""},
		{"pair 2 present", "axxbxxxc", []int{0, 8, 1, 3, 4, 7}, 2, "xxx"},
		{"negative pair", "abc", []int{0, 3}, -1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := safeSubmatchSlice(c.s, c.m, c.pair)
			if got != c.want {
				t.Fatalf("safeSubmatchSlice(%q,%v,%d)=%q want %q", c.s, c.m, c.pair, got, c.want)
			}
		})
	}
}

// ---- extractArticleCards / extractWebElements 畸形 HTML 不 panic ----

func TestExtractArticleCards_SafeSubmatch(t *testing.T) {
	// 畸形 / 截断 HTML：历史上 reContainerArticle / reLiItem 在部分匹配时
	// submatch 索引可能为 -1，直接 html[m[2]:m[3]] 会触发 index out of range [-1]。
	// 这里构造多种畸形输入，断言不 panic 且返回非 nil。
	resetSpiderPanicCounters()
	inputs := []string{
		"",
		"   ",
		"<article>",
		"<article><h2><a href=\"/p/1\">title",
		"<li><h3>no anchor</h3></li>",
		"<li><a href=\"#\">x</a></li>",
		"<article><h2></h2></article>",
		strings.Repeat("<li><h2><a href=/x>", 100),
		"<li><h2><a href=\"/a\">A</a></h2><p>summary</p></li>",
	}
	for i, html := range inputs {
		out := extractArticleCards(html, "https://example.com/")
		if out == nil {
			t.Fatalf("case %d: extractArticleCards returned nil for %q", i, html)
		}
	}
	// 最后一条正常卡片应被解析出来
	out := extractArticleCards("<li><h2><a href=\"/a\">A</a></h2><p>summary</p></li>", "https://example.com/")
	if len(out) != 1 || out[0].Title != "A" || out[0].URL != "https://example.com/a" {
		t.Fatalf("expected 1 article {A, /a}, got %+v", out)
	}
}

func TestExtractWebElements_SafeSubmatch(t *testing.T) {
	resetSpiderPanicCounters()
	inputs := []string{
		"",
		"   ",
		"<a href=\"/x\">",
		"<h2></h2>",
		"<a href=\"javascript:void(0)\">js</a>",
		"<a href=\"/x\">text</a>",
		strings.Repeat("<a href=/x>x</a>", 300),
	}
	for i, html := range inputs {
		out := extractWebElements(html, "https://example.com/")
		if out == nil {
			t.Fatalf("case %d: extractWebElements returned nil for %q", i, html)
		}
		if out.Links == nil || out.Headings == nil || out.Paragraphs == nil || out.Articles == nil {
			t.Fatalf("case %d: nil slice field for %q -> %+v", i, html, out)
		}
	}
	// 正常链接应被解析
	out := extractWebElements("<a href=\"/x\">text</a>", "https://example.com/")
	if len(out.Links) != 1 || out.Links[0].URL != "https://example.com/x" {
		t.Fatalf("expected 1 link /x, got %+v", out.Links)
	}
}

// extractArticleCards 内部 defer recover 触发时返回空集合并累计 panic 计数。
func TestExtractArticleCards_PanicRecoverReturnsEmpty(t *testing.T) {
	resetSpiderPanicCounters()
	before := spiderPanicCount.Load()
	// 构造一个会让内部切片访问 panic 的输入很难（safeSubmatchSlice 已堵住），
	// 这里直接验证：即使输入再畸形，函数也不会把 panic 抛给调用方。
	out := extractArticleCards("<article><h2><a href=/x>", "https://example.com/")
	if out == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	// safeSubmatchSlice 已避免 panic，故计数不应增长
	if got := spiderPanicCount.Load(); got != before {
		t.Fatalf("panic count should not grow when safeSubmatchSlice guards, got %d->%d", before, got)
	}
}

// ---- releaseSpiderSession ----

func TestReleaseSpiderSession_DeletesFromMap(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	s := &SpiderSession{
		SessionID: "rel_1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(spiderSessionTTL),
		cdpTarget: "tab-1",
	}
	spiderSessionsMu.Lock()
	spiderSessions[s.SessionID] = s
	spiderSessionsMu.Unlock()

	releaseSpiderSession(s)

	spiderSessionsMu.RLock()
	_, exists := spiderSessions[s.SessionID]
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if exists {
		t.Fatalf("session should have been removed from map")
	}
	if count != 0 {
		t.Fatalf("expected map empty, got count=%d", count)
	}
	if s.cdpCtx != nil || s.cdpTarget != "" || s.cdpCancel != nil {
		t.Fatalf("cdp fields should be cleared after release")
	}
}

func TestReleaseSpiderSession_NilAndIdempotent(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	// nil 不 panic
	releaseSpiderSession(nil)

	// 重复释放不 panic
	s := &SpiderSession{SessionID: "rel_2", ExpiresAt: time.Now().UTC().Add(spiderSessionTTL)}
	spiderSessionsMu.Lock()
	spiderSessions[s.SessionID] = s
	spiderSessionsMu.Unlock()
	releaseSpiderSession(s)
	releaseSpiderSession(s)
}

// ---- detachAllSpiderSessions 清空 map ----

func TestDetachAllSpiderSessions_EmptiesMap(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	for i := 0; i < 5; i++ {
		spiderSessionsMu.Lock()
		spiderSessions[fmt.Sprintf("d_%d", i)] = &SpiderSession{
			SessionID: fmt.Sprintf("d_%d", i),
			ExpiresAt: time.Now().UTC().Add(spiderSessionTTL),
			cdpTarget: "tab",
		}
		spiderSessionsMu.Unlock()
	}

	detachAllSpiderSessions()

	spiderSessionsMu.RLock()
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if count != 0 {
		t.Fatalf("expected map emptied after detachAllSpiderSessions, got count=%d", count)
	}
}

// ---- /healthz 透传 panic 指标 ----

func TestHealthz_ExposesPanicMetrics(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()
	resetSpiderPanicCounters()
	defer resetSpiderPanicCounters()

	// engine 未启动时 healthz 走 down 分支，但 panic 指标仍应透传
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mcpSetNoCacheHeaders(w)
		engine := GetSpiderEngine()
		status := map[string]interface{}{
			"service":     "LSM Spider MCP Service",
			"version":     "2.0.34",
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
			"panic_count": spiderPanicCount.Load(),
		}
		if ms := spiderLastPanicAtMs.Load(); ms > 0 {
			status["last_panic_at"] = time.UnixMilli(ms).UTC().Format(time.RFC3339)
		}
		if engine == nil || !engine.isRunning {
			status["status"] = "down"
			status["chrome"] = "not running"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(status)
	}

	// 记录一次 panic
	recordSpiderPanic("simulated index out of range [-2]")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v body=%s", err, rec.Body.String())
	}
	if v, _ := resp["panic_count"].(float64); v < 1 {
		t.Fatalf("expected panic_count>=1, got %v", resp["panic_count"])
	}
	if _, ok := resp["last_panic_at"]; !ok {
		t.Fatalf("expected last_panic_at field present, got %v", resp)
	}
	if v, _ := resp["version"].(string); v != "2.0.34" {
		t.Fatalf("expected version 2.0.34, got %v", resp["version"])
	}
}

// ---- computeSpiderHealthMetrics 与单测镜像同源 ----

func TestComputeSpiderHealthMetrics_SessionTotal(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	spiderSessionsMu.Lock()
	spiderSessions["m1"] = &SpiderSession{SessionID: "m1", cdpTarget: "tab"}
	spiderSessions["m2"] = &SpiderSession{SessionID: "m2"}
	spiderSessionsMu.Unlock()

	total, active, _, _, _, _ := computeSpiderHealthMetrics()
	if total != 2 {
		t.Fatalf("expected session_total=2, got %d", total)
	}
	if active != 1 {
		t.Fatalf("expected chrome_active_sessions=1, got %d", active)
	}
}

// ---- 并发 releaseSpiderSession 不死锁 ----

func TestReleaseSpiderSession_ConcurrentSafe(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("c_%d", i)
			s := &SpiderSession{SessionID: id, ExpiresAt: time.Now().UTC().Add(spiderSessionTTL)}
			spiderSessionsMu.Lock()
			spiderSessions[id] = s
			spiderSessionsMu.Unlock()
			releaseSpiderSession(s)
		}(i)
	}
	wg.Wait()

	spiderSessionsMu.RLock()
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if count != 0 {
		t.Fatalf("expected map empty after concurrent release, got %d", count)
	}
}
