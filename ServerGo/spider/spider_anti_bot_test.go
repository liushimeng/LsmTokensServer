package spider

// ==================== v2.0.8 反反爬单元测试 ====================
//
// 覆盖 UA 池 / 头随机化 / 代理池 / 指纹 / stealth 脚本构造 / 抖动 / 重试
// 不依赖 Chrome / 网络，可在 CI 中运行

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// ==================== UA 池 ====================

func TestUAPoolNext_RoundRobin(t *testing.T) {
	pool := LoadUAPool([]string{"ua-1", "ua-2", "ua-3"})
	expected := []string{"ua-1", "ua-2", "ua-3", "ua-1", "ua-2", "ua-3"}
	for i, want := range expected {
		got := pool.Next()
		if got != want {
			t.Errorf("call %d: got %q, want %q", i, got, want)
		}
	}
}

func TestUAPoolPeekAll(t *testing.T) {
	pool := LoadUAPool([]string{"ua-1", "ua-2"})
	all := pool.PeekAll()
	if len(all) != 2 {
		t.Errorf("PeekAll returned %d items, want 2", len(all))
	}
	// 外部修改不影响 pool
	all[0] = "MUTATED"
	all2 := pool.PeekAll()
	if all2[0] != "ua-1" {
		t.Errorf("PeekAll returned shared slice; mutation leaked")
	}
}

func TestUAPool_OverrideCustom(t *testing.T) {
	pool := LoadUAPool([]string{"x", "y"})
	all := pool.PeekAll()
	if len(all) != 2 {
		t.Errorf("custom pool size: got %d, want 2", len(all))
	}
}

func TestUAPool_BuiltinFallback(t *testing.T) {
	pool := LoadUAPool(nil)
	all := pool.PeekAll()
	if len(all) < 12 {
		t.Errorf("builtin pool size: got %d, want >= 12", len(all))
	}
}

// ==================== 头随机化 ====================

func TestHeaderBundle_AcceptLanguageMatchesUA(t *testing.T) {
	// macOS UA -> en-US 优先
	macUA := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	b := BuildHeaderBundle(macUA, "", "", nil, 1)
	if !strings.HasPrefix(b.AcceptLanguage, "en-US") {
		t.Errorf("macOS AcceptLanguage should start with en-US, got %q", b.AcceptLanguage)
	}
	// Linux UA -> zh-CN 优先（默认）
	linuxUA := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	b = BuildHeaderBundle(linuxUA, "", "", nil, 1)
	if !strings.HasPrefix(b.AcceptLanguage, "zh-CN") {
		t.Errorf("Linux AcceptLanguage should start with zh-CN, got %q", b.AcceptLanguage)
	}
	// 自定义 baseLang 应被尊重
	b = BuildHeaderBundle(linuxUA, "ja,en;q=0.5", "", nil, 1)
	if b.AcceptLanguage != "ja,en;q=0.5" {
		t.Errorf("custom baseLang: got %q, want %q", b.AcceptLanguage, "ja,en;q=0.5")
	}
}

func TestHeaderBundle_Deterministic(t *testing.T) {
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	b1 := BuildHeaderBundle(ua, "", "https://ref.example.com", map[string]string{"X-Custom": "v1"}, 42)
	b2 := BuildHeaderBundle(ua, "", "https://ref.example.com", map[string]string{"X-Custom": "v1"}, 42)
	// AcceptLanguage / SecCHUA / SecCHPlatform 应该一致
	if b1.AcceptLanguage != b2.AcceptLanguage {
		t.Errorf("AcceptLanguage not deterministic: %q vs %q", b1.AcceptLanguage, b2.AcceptLanguage)
	}
	if b1.SecCHUA != b2.SecCHUA {
		t.Errorf("SecCHUA not deterministic")
	}
}

func TestHeaderBundle_SecCHUAPresent(t *testing.T) {
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	b := BuildHeaderBundle(ua, "", "", nil, 1)
	if b.SecCHUA == "" {
		t.Error("SecCHUA should be non-empty for Chrome UA")
	}
	// Safari UA 不发 Sec-CH-UA
	safariUA := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
	bs := BuildHeaderBundle(safariUA, "", "", nil, 1)
	if bs.SecCHUA != "" {
		t.Error("SecCHUA should be empty for Safari UA")
	}
}

func TestHeaderBundle_IsEmpty(t *testing.T) {
	empty := HeaderBundle{}
	if !empty.IsEmpty() {
		t.Error("zero-value HeaderBundle should be empty")
	}
	nonEmpty := HeaderBundle{Accept: "*/*"}
	if nonEmpty.IsEmpty() {
		t.Error("non-empty HeaderBundle should not be empty")
	}
}

func TestHeaderBundle_ToNetworkHeaders(t *testing.T) {
	b := HeaderBundle{
		Accept:         "text/html",
		AcceptLanguage: "en",
		Custom:         map[string]string{"X-Custom": "v1"},
	}
	h := b.ToNetworkHeaders()
	if h["Accept"] != "text/html" {
		t.Errorf("Accept not set: %v", h)
	}
	if h["X-Custom"] != "v1" {
		t.Errorf("Custom not set: %v", h)
	}
}

// ==================== 代理池 ====================

func TestProxyPool_Next(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080", "socks5://p2:1080"})
	if got := pool.Next(); got != "http://p1:8080" {
		t.Errorf("first: got %q", got)
	}
	if got := pool.Next(); got != "socks5://p2:1080" {
		t.Errorf("second: got %q", got)
	}
	if got := pool.Next(); got != "http://p1:8080" {
		t.Errorf("third (wrap): got %q", got)
	}
}

func TestProxyPool_Empty(t *testing.T) {
	pool := LoadProxyPool([]string{})
	if got := pool.Next(); got != "" {
		t.Errorf("empty pool should return empty string, got %q", got)
	}
}

func TestProxyPool_FilterInvalidScheme(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080", "ftp://bad:21", "socks5://p2:1080", ""})
	all := pool.PeekAll()
	// 实际上 LoadProxyPool 只在用户调用时过滤，PeekAll 不暴露此过滤
	// 我们通过 Next 测试有效数量
	count := 0
	expected := []string{"http://p1:8080", "socks5://p2:1080", "http://p1:8080", "socks5://p2:1080"}
	for _, want := range expected {
		got := pool.Next()
		if got == want {
			count++
		}
	}
	if count != 4 {
		t.Errorf("invalid scheme should be filtered: only %d/4 matched", count)
	}
	_ = all
}

func TestResolveProxyForDataSource_Override(t *testing.T) {
	pool := LoadProxyPool([]string{"http://default:8080"})
	perSource := map[int]string{42: "socks5://per-source:1080"}
	got := ResolveProxyForDataSource(pool, perSource, 42)
	if got != "socks5://per-source:1080" {
		t.Errorf("per-source override: got %q", got)
	}
}

func TestResolveProxyForDataSource_Fallback(t *testing.T) {
	pool := LoadProxyPool([]string{"http://default:8080"})
	perSource := map[int]string{42: "socks5://per-source:1080"}
	got := ResolveProxyForDataSource(pool, perSource, 999)
	// 未知 dsID 应走池（轮询）
	if got != "http://default:8080" {
		t.Errorf("fallback to pool: got %q", got)
	}
}

// ==================== 指纹 ====================

func TestFingerprint_Deterministic(t *testing.T) {
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	fp1 := BuildFingerprint(ua, 12345)
	fp2 := BuildFingerprint(ua, 12345)
	if fp1.ViewportW != fp2.ViewportW || fp1.ViewportH != fp2.ViewportH {
		t.Errorf("viewport not deterministic: %v vs %v", fp1.ViewportW, fp2.ViewportW)
	}
	if fp1.HardwareConcurrency != fp2.HardwareConcurrency {
		t.Errorf("HC not deterministic: %d vs %d", fp1.HardwareConcurrency, fp2.HardwareConcurrency)
	}
	if fp1.DeviceMemoryGB != fp2.DeviceMemoryGB {
		t.Errorf("Memory not deterministic")
	}
	if fp1.TimezoneOffsetMin != fp2.TimezoneOffsetMin {
		t.Errorf("TZ not deterministic")
	}
}

func TestFingerprint_DifferentSeeds(t *testing.T) {
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	fp1 := BuildFingerprint(ua, 1)
	fp2 := BuildFingerprint(ua, 2)
	// 至少有一个字段不同
	diff := fp1.ViewportW != fp2.ViewportW ||
		fp1.HardwareConcurrency != fp2.HardwareConcurrency ||
		fp1.DeviceMemoryGB != fp2.DeviceMemoryGB ||
		fp1.TimezoneOffsetMin != fp2.TimezoneOffsetMin
	if !diff {
		t.Error("different seeds should produce different fingerprints")
	}
}

func TestFingerprint_Ranges(t *testing.T) {
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		fp := BuildFingerprint(ua, r.Int63())
		if fp.ViewportW < 1366 || fp.ViewportW > 1920 {
			t.Errorf("viewport W out of range: %d", fp.ViewportW)
		}
		if fp.ViewportH < 768 || fp.ViewportH > 1080 {
			t.Errorf("viewport H out of range: %d", fp.ViewportH)
		}
		hcSet := map[int]bool{2: true, 4: true, 8: true, 16: true}
		if !hcSet[fp.HardwareConcurrency] {
			t.Errorf("HC not in {2,4,8,16}: %d", fp.HardwareConcurrency)
		}
		memSet := map[int]bool{2: true, 4: true, 8: true}
		if !memSet[fp.DeviceMemoryGB] {
			t.Errorf("Memory not in {2,4,8}: %d", fp.DeviceMemoryGB)
		}
		if fp.TimezoneOffsetMin < -720 || fp.TimezoneOffsetMin > 720 {
			t.Errorf("TZ out of range: %d", fp.TimezoneOffsetMin)
		}
	}
}

// ==================== Stealth 脚本 ====================

func TestBuildStealthScript_ContainsNoise(t *testing.T) {
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	fp := BuildFingerprint(ua, 12345)
	js := BuildStealthScript(fp, "")
	if !strings.Contains(js, "hardwareConcurrency") {
		t.Error("fingerprint JS should override hardwareConcurrency")
	}
	if !strings.Contains(js, "deviceMemory") {
		t.Error("fingerprint JS should override deviceMemory")
	}
	// canvas noise 仅在 CanvasNoiseSeed != 0 时注入
	if fp.CanvasNoiseSeed != 0 && !strings.Contains(js, "getImageData") {
		t.Error("CanvasNoiseSeed != 0 should inject canvas noise")
	}
}

func TestBuildStealthScript_NoFingerprint(t *testing.T) {
	js := BuildStealthScript(nil, "")
	// fp=nil 时应返回 baseJS（v2.0.7 字节级）
	if !strings.Contains(js, "navigator.webdriver") {
		t.Error("base stealth should cover navigator.webdriver")
	}
}

func TestBuildStealthScript_UserPrefix(t *testing.T) {
	prefix := `console.log("custom-stealth")`
	js := BuildStealthScript(nil, prefix)
	if !strings.HasPrefix(js, prefix) {
		t.Error("user prefix should be at the start of the script")
	}
	// baseJS 应该在 prefix 之后
	if !strings.Contains(js, "navigator.webdriver") {
		t.Error("baseJS should still be present after user prefix")
	}
}

// ==================== Jitter ====================

func TestJitterSleepMs_NoopWhenZero(t *testing.T) {
	// min=max=0 时 no-op（不阻塞）
	// 用 100ms 超时作为兜底：如果函数没立即返回则视为失败
	done := make(chan struct{}, 1)
	go func() {
		_ = JitterSleepMs(testCtx(), 0, 0)
		done <- struct{}{}
	}()
	select {
	case <-done:
		// 立即返回，符合预期
	case <-time.After(100 * time.Millisecond):
		t.Error("JitterSleepMs(0, 0) should be no-op and return immediately")
	}
}

func TestTryProxyScheme(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"http://1.2.3.4:8080", true},
		{"https://proxy.example.com:443", true},
		{"socks5://127.0.0.1:1080", true},
		{"ftp://bad:21", false},
		{"invalid", false},
		{"", false},
	}
	for _, c := range cases {
		got := tryProxyScheme(c.input)
		if got != c.want {
			t.Errorf("tryProxyScheme(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ==================== 重试 ====================

func TestShouldAutoRetry(t *testing.T) {
	plan := BuildRetryPlan(2)
	cases := []struct {
		errType string
		want    bool
	}{
		{"anti_bot", true},
		{"captcha", true},
		{"region_block", false},
		{"timeout", false},
		{"rate_limit", false},
		{"unknown", false},
		{"", false},
	}
	for _, c := range cases {
		got := ShouldAutoRetry(c.errType, plan)
		if got != c.want {
			t.Errorf("ShouldAutoRetry(%q) = %v, want %v", c.errType, got, c.want)
		}
	}
}

func TestBuildRetryPlan_DefaultTwo(t *testing.T) {
	plan := BuildRetryPlan(2)
	if plan.MaxAttempts != 2 {
		t.Errorf("MaxAttempts: got %d, want 2", plan.MaxAttempts)
	}
}

func TestBuildRetryPlan_ZeroDisables(t *testing.T) {
	plan := BuildRetryPlan(0)
	if plan.MaxAttempts != 0 {
		t.Errorf("MaxAttempts: got %d, want 0", plan.MaxAttempts)
	}
	// plan.MaxAttempts=0 时 ShouldAutoRetry 应始终返回 false
	if ShouldAutoRetry("anti_bot", plan) {
		t.Error("ShouldAutoRetry should return false when plan disabled")
	}
}

func TestBuildRetryPlan_ClampTo5(t *testing.T) {
	plan := BuildRetryPlan(99)
	if plan.MaxAttempts != 5 {
		t.Errorf("MaxAttempts: got %d, want 5 (clamped)", plan.MaxAttempts)
	}
}

func TestAntiBotState_RecordAndNext(t *testing.T) {
	s := NewAntiBotState()
	if s.Attempts != 0 {
		t.Error("initial Attempts should be 0")
	}
	s.Record(1, "anti_bot", []string{"captcha_script_detected"})
	if s.Attempts != 1 || s.LastErrType != "anti_bot" {
		t.Errorf("Record failed: %+v", s)
	}
}

// ==================== sessionSeedFromID ====================

func TestSessionSeedFromID_Deterministic(t *testing.T) {
	s1 := sessionSeedFromID("spider_12345")
	s2 := sessionSeedFromID("spider_12345")
	if s1 != s2 {
		t.Errorf("session seed not deterministic: %d vs %d", s1, s2)
	}
	s3 := sessionSeedFromID("spider_99999")
	if s1 == s3 {
		t.Error("different session IDs should produce different seeds")
	}
}

func TestSessionSeedFromID_Empty(t *testing.T) {
	s := sessionSeedFromID("")
	if s == 0 {
		t.Error("empty session ID should fall back to time-based seed (non-zero)")
	}
}

// ==================== helpers ====================

// testCtx 返回一个不会被 cancel 的 context（用于 JitterSleepMs 测试）
func testCtx() context.Context {
	return context.Background()
}
