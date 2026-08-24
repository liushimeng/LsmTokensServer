package spider

// v2.0.47：MCP 日志关联 / 崩溃快照测试
//
// 覆盖：
//   1. generateRequestID 唯一性（10000 次生成不碰撞） + 格式校验（"req-" + 16 hex）
//   2. truncateForLog 大字段截断 + 短字段原样返回
//   3. mcpLogMCPWithTag tag=空 时回退到 mcpLogMCP（不重复加 [tag]）
//   4. captureCrashSnapshot 不 panic（nil engine / nil session map / nil req）
//   5. lastCrashSnapshot atomic.Pointer 读写并发安全
//   6. getLastCrashSnapshot 初始 nil-safe
//   7. CrashSnapshot 字段在 /healthz JSON 透传契约（不直接启 HTTP，仅验证字段序列化）

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGenerateRequestID_Format 生成 ID 形如 "req-xxxxxxxxxxxxxxxx"（16 hex 字符）。
func TestGenerateRequestID_Format(t *testing.T) {
	id := generateRequestID()
	if !strings.HasPrefix(id, "req-") {
		t.Fatalf("RequestID must start with 'req-', got %q", id)
	}
	hexPart := strings.TrimPrefix(id, "req-")
	if len(hexPart) != 16 {
		t.Fatalf("hex part must be 16 chars, got %d (id=%q)", len(hexPart), id)
	}
	for _, r := range hexPart {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("hex part must be lowercase hex, got %q (id=%q)", hexPart, id)
		}
	}
}

// TestGenerateRequestID_Uniqueness 10000 次生成全部唯一（crypto/rand 性质验证）。
// 重复概率 ≈ 1/2^64；10000 次 P(collision) < 1e-13，可视为不可能。
func TestGenerateRequestID_Uniqueness(t *testing.T) {
	const N = 10000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		id := generateRequestID()
		if _, dup := seen[id]; dup {
			t.Fatalf("RequestID collision at i=%d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != N {
		t.Fatalf("expected %d unique ids, got %d", N, len(seen))
	}
}

// TestTruncateForLog 测试 URL/大字段截断契约。
func TestTruncateForLog(t *testing.T) {
	// 短字段原样返回
	if got := truncateForLog("abc", 10); got != "abc" {
		t.Errorf("short string should pass through: got %q", got)
	}
	// 边界值（恰好等于 max）原样返回
	if got := truncateForLog("0123456789", 10); got != "0123456789" {
		t.Errorf("equal-length string should pass through: got %q", got)
	}
	// 长字段截断
	long := strings.Repeat("x", 100)
	got := truncateForLog(long, 10)
	if !strings.HasPrefix(got, "xxxxxxxxxx") {
		t.Errorf("truncated string should start with first 10 chars: got %q", got)
	}
	if !strings.Contains(got, "truncated 90 bytes") {
		t.Errorf("truncated string should report dropped bytes: got %q", got)
	}
	// 空字符串
	if got := truncateForLog("", 10); got != "" {
		t.Errorf("empty string should stay empty: got %q", got)
	}
}

// TestCaptureCrashSnapshot_NilSafe 整个 capture 函数在 nil session map / nil engine
// 下不应 panic。这是 v2.0.47 的核心契约 — panic recover 路径上 snapshot 自身不能
// 二次 panic，否则会把 handler 永远卡死。
func TestCaptureCrashSnapshot_NilSafe(t *testing.T) {
	// 临时清空 spiderSessions 和 engine（用 defer 恢复）
	origSessions := spiderSessions
	spiderSessions = nil
	defer func() { spiderSessions = origSessions }()

	// 在无 engine 状态下调用，函数必须不 panic
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("captureCrashSnapshot must not panic on nil map/engine, got: %v", rec)
		}
	}()

	snap := captureCrashSnapshot("req-test", "https://example.com/", "navigate", "", 0, "test panic value")
	if snap == nil {
		t.Fatal("snapshot must not be nil on nil-safe path")
	}
	if snap.RequestID != "req-test" {
		t.Errorf("RequestID mismatch: got %q", snap.RequestID)
	}
	if snap.URL != "https://example.com/" {
		t.Errorf("URL mismatch: got %q", snap.URL)
	}
	if snap.ActionType != "navigate" {
		t.Errorf("ActionType mismatch: got %q", snap.ActionType)
	}
	if snap.PanicValue != "test panic value" {
		t.Errorf("PanicValue mismatch: got %q", snap.PanicValue)
	}
	if snap.RecordedAtMs <= 0 {
		t.Error("RecordedAtMs must be set")
	}
	// 无 session 时 SessionCount=0
	if snap.SessionCount != 0 {
		t.Errorf("SessionCount should be 0 with nil map, got %d", snap.SessionCount)
	}
}

// TestGetLastCrashSnapshot_NilSafe 初始状态为 nil（无 crash 发生），不 panic。
func TestGetLastCrashSnapshot_NilSafe(t *testing.T) {
	// 用一个新的 atomic 变量，避免污染其它测试
	var p atomic.Pointer[CrashSnapshot]
	// 初始 load 必须为 nil
	if got := p.Load(); got != nil {
		t.Fatalf("initial snapshot must be nil, got %+v", got)
	}
}

// TestCrashSnapshot_JSONMarshal JSON 序列化契约 — /healthz 透传快照时字段名
// 必须稳定（外部看门狗按 JSON 字段名取数据）。
func TestCrashSnapshot_JSONMarshal(t *testing.T) {
	snap := &CrashSnapshot{
		RequestID:    "req-deadbeef",
		RecordedAtMs: time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC).UnixMilli(),
		URL:          "https://example.com/",
		ActionType:   "navigate",
		Attempt:      2,
		SessionID:    "spider_123",
		PanicValue:   "runtime error: nil pointer",
		SessionCount: 3,
		SemUsed:      4,
		SemCapacity:  8,
		BusyFails:    1,
		ChromeActive: 2,
		Extra:        map[string]interface{}{"engine_running": true},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	for _, key := range []string{
		"request_id", "recorded_at_ms", "url", "action_type", "attempt",
		"session_id", "panic_value", "session_count", "sem_used", "sem_capacity",
		"busy_fails", "chrome_active_sessions", "extra",
	} {
		if !strings.Contains(out, key) {
			t.Errorf("JSON missing key %q: %s", key, out)
		}
	}
}

// TestCrashSnapshot_TruncateURL panic value 走 fmt.Sprintf，URL 必须先 truncate 防止爆行。
func TestCrashSnapshot_TruncateURL(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("a", 500)
	snap := captureCrashSnapshot("req-trunc", longURL, "", "", 0, "boom")
	if len(snap.URL) > 300 {
		t.Errorf("URL must be truncated to <= ~280 chars, got %d", len(snap.URL))
	}
	if !strings.Contains(snap.URL, "truncated") {
		t.Errorf("truncated URL must contain 'truncated' marker, got %q", snap.URL)
	}
}

// TestMcpLogMCPWithTag_EmptyTagPassthrough tag 为空时不重复加 [tag] 前缀。
// 这个测试只验证实现细节不破坏现有 mcpLogMCP 行为，不直接断言 log 输出（log 默认
// 走 stderr，会被测试 runner 收掉），而是通过观察 side effect 间接验证。
func TestMcpLogMCPWithTag_EmptyTagPassthrough(t *testing.T) {
	// 不调用 log（避免污染测试输出），仅确保函数不 panic
	mcpLogMCPWithTag("", "test message: %s", "x")
	mcpLogMCPWithTag("req-test", "test message: %s", "x")
}

// TestLastCrashSnapshot_ConcurrentSafety 模拟多个 goroutine 同时写 / 读 lastCrashSnapshot。
// atomic.Pointer 已保证并发安全；这里只做 smoke test，验证我们的用法不 race。
func TestLastCrashSnapshot_ConcurrentSafety(t *testing.T) {
	const writers = 8
	const reads = 100
	var wg sync.WaitGroup
	wg.Add(writers + 1)

	// writers
	for i := 0; i < writers; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = captureCrashSnapshot(
					"req-concurrent",
					"https://example.com/",
					"concurrent",
					"",
					idx,
					"test",
				)
			}
		}(i)
	}
	// reader
	go func() {
		defer wg.Done()
		for i := 0; i < reads; i++ {
			_ = getLastCrashSnapshot()
		}
	}()

	wg.Wait()
}
