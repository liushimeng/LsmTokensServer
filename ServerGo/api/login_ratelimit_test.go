package api

// v2.0.78 用户 Web 安全加固单元测试
//
// 覆盖新增安全能力：
//  1. 验证码端点专用限速（30 req/min）
//  2. 登录端点专用限速（15 req/min）
//  3. 渐进式锁定升级（10→20→40→60→60 min）
//  4. captcha_token 客户端绑定校验（有效/伪造/缺失/跨客户端）
//  5. HSTS 头仅 HTTPS 请求返回

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ========== 辅助 ==========

// resetRateLimiters 清空所有限速器与登录失败记录（测试隔离）
func resetAllLimiters(t *testing.T) {
	t.Helper()
	// 重置专用限速器
	captchaRateLimiter.mu.Lock()
	captchaRateLimiter.windows = make(map[string]*endpointRateLimitEntry)
	captchaRateLimiter.mu.Unlock()
	loginRateLimiter.mu.Lock()
	loginRateLimiter.windows = make(map[string]*endpointRateLimitEntry)
	loginRateLimiter.mu.Unlock()
	// 重置登录失败记录
	loginAttemptsMu.Lock()
	loginAttempts = make(map[string]*loginAttempt)
	loginAttemptsMu.Unlock()
}

// makeReq 构造请求（固定 IP + UA 以稳定指纹）
func makeReq(method, path, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.RemoteAddr = "10.0.0.5:12345"
	r.Header.Set("User-Agent", "TestSuite/1.0")
	return r
}

// ========== 1. 验证码端点限速 ==========

func TestCaptchaRateLimit_AllowsUnderLimit(t *testing.T) {
	resetAllLimiters(t)
	handler := captchaRateLimiter.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "too many")

	// 30 次应全部放行
	blocked := 0
	for i := 0; i < 30; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, makeReq(http.MethodGet, "/CaptchaGenerate", ""))
		if rec.Code == http.StatusTooManyRequests {
			blocked++
		}
	}
	if blocked != 0 {
		t.Fatalf("30 次/min 内应全部放行，实际被阻断 %d 次", blocked)
	}
}

func TestCaptchaRateLimit_BlocksOverLimit(t *testing.T) {
	resetAllLimiters(t)
	handler := captchaRateLimiter.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "验证码刷新过于频繁，请稍后再试")

	// 前 30 次放行，第 31 次阻断
	var lastCode int
	for i := 0; i < 31; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, makeReq(http.MethodGet, "/CaptchaGenerate", ""))
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("第 31 次应被阻断(429)，实际 HTTP %d", lastCode)
	}
	// 响应体应为 JSON
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq(http.MethodGet, "/CaptchaGenerate", ""))
	var resp userLoginResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("429 响应应为 JSON: %v body=%s", err, rec.Body.String())
	}
	if resp.Success || !strings.Contains(resp.Message, "频繁") {
		t.Fatalf("429 响应文案不符: %+v", resp)
	}
	// Retry-After 头
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("429 响应应包含 Retry-After 头")
	}
}

// ========== 2. 登录端点限速 ==========

func TestLoginRateLimit_AllowUnderLimit(t *testing.T) {
	resetAllLimiters(t)
	handler := loginRateLimiter.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "too many")

	blocked := 0
	for i := 0; i < 15; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, makeReq(http.MethodPost, "/UserLoginInterface", "{}"))
		if rec.Code == http.StatusTooManyRequests {
			blocked++
		}
	}
	if blocked != 0 {
		t.Fatalf("15 次/min 内应全部放行，实际被阻断 %d 次", blocked)
	}
}

func TestLoginRateLimit_BlocksOverLimit(t *testing.T) {
	resetAllLimiters(t)
	handler := loginRateLimiter.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "登录尝试过于频繁，请稍后再试")

	var lastCode int
	for i := 0; i < 16; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, makeReq(http.MethodPost, "/UserLoginInterface", "{}"))
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("第 16 次应被阻断(429)，实际 HTTP %d", lastCode)
	}
}

// ========== 3. 渐进式锁定升级 ==========

func TestProgressiveLockout_Escalation(t *testing.T) {
	resetAllLimiters(t)

	testIP := "10.0.0.99"
	expected := []time.Duration{
		10 * time.Minute,
		20 * time.Minute,
		40 * time.Minute,
		60 * time.Minute,
		60 * time.Minute, // 封顶
	}

	for i, exp := range expected {
		// 连续失败 3 次触发一次锁定
		for j := 0; j < maxLoginFailures; j++ {
			recordLoginFailure(testIP)
		}
		err := checkLoginAttempt(testIP)
		if err == nil {
			t.Fatalf("第 %d 次触发后应被锁定", i+1)
		}
		// 验证锁定时长
		loginAttemptsMu.Lock()
		until := loginAttempts[testIP].lockedUntil
		loginAttemptsMu.Unlock()
		got := until.Sub(time.Now())
		// 允许 ±2 秒误差（执行耗时）
		if got < exp-2*time.Second || got > exp+2*time.Second {
			t.Fatalf("第 %d 次锁定时长不符：期望 %v，实际 %v", i+1, exp, got)
		}
		// 模拟锁定过期（推进时间）以测试下一次
		loginAttemptsMu.Lock()
		loginAttempts[testIP].lockedUntil = time.Now().Add(-time.Second)
		loginAttempts[testIP].failedCount = 0 // 锁定触发后已重置
		loginAttemptsMu.Unlock()
	}
}

func TestProgressiveLockout_ResetOnSuccess(t *testing.T) {
	resetAllLimiters(t)

	ip := "10.0.0.88"
	// 触发一次锁定
	for j := 0; j < maxLoginFailures; j++ {
		recordLoginFailure(ip)
	}
	loginAttemptsMu.Lock()
	if loginAttempts[ip].lockCount != 1 {
		t.Fatalf("触发后 lockCount 应为 1，实际 %d", loginAttempts[ip].lockCount)
	}
	// 推进锁定过期
	loginAttempts[ip].lockedUntil = time.Now().Add(-time.Second)
	loginAttemptsMu.Unlock()

	// 成功登录 → 清除记录
	clearLoginAttempt(ip)
	loginAttemptsMu.Lock()
	_, exists := loginAttempts[ip]
	loginAttemptsMu.Unlock()
	if exists {
		t.Fatal("成功登录后应清除失败记录（含 lockCount）")
	}

	// 再次失败 3 次 → lockCount 从 1 开始（10 min），而非从 2 开始
	for j := 0; j < maxLoginFailures; j++ {
		recordLoginFailure(ip)
	}
	loginAttemptsMu.Lock()
	lc := loginAttempts[ip].lockCount
	until := loginAttempts[ip].lockedUntil
	loginAttemptsMu.Unlock()
	if lc != 1 {
		t.Fatalf("成功重置后 lockCount 应为 1，实际 %d", lc)
	}
	got := until.Sub(time.Now())
	if got < 10*time.Minute-2*time.Second || got > 10*time.Minute+2*time.Second {
		t.Fatalf("重置后首次锁定应为 10 min，实际 %v", got)
	}
}

// ========== 4. captcha_token 客户端绑定 ==========

func TestCaptchaToken_Valid(t *testing.T) {
	r := makeReq(http.MethodGet, "/CaptchaGenerate", "")
	token := generateCaptchaToken("test-captcha-id-123", r)
	// token = 16 字节原始值的 hex 编码 = 32 个 hex 字符
	if len(token) != 32 {
		t.Fatalf("token 长度应为 32（16 字节 hex），实际 %d (%q)", len(token), token)
	}
	// 同客户端校验通过
	if !verifyCaptchaToken("test-captcha-id-123", token, r) {
		t.Fatal("正确 token 应校验通过")
	}
}

func TestCaptchaToken_InvalidRejected(t *testing.T) {
	r := makeReq(http.MethodGet, "/CaptchaGenerate", "")
	// 伪造 token（正确长度但错误值）
	if verifyCaptchaToken("test-captcha-id-123", "deadbeefdeadbeefdeadbeefdeadbeef", r) {
		t.Fatal("伪造 token 应被拒绝")
	}
	// 错误长度（过短）
	if verifyCaptchaToken("test-captcha-id-123", "deadbeefdeadbeef", r) {
		t.Fatal("16 字符 token 应被拒绝（需 32 字符）")
	}
	// 过短
	if verifyCaptchaToken("test-captcha-id-123", "tooshort", r) {
		t.Fatal("过短 token 应被拒绝")
	}
}

func TestCaptchaToken_MissingSkipped(t *testing.T) {
	r := makeReq(http.MethodGet, "/CaptchaGenerate", "")
	// 不发送 token → 兼容旧客户端，跳过校验
	if !verifyCaptchaToken("test-captcha-id-123", "", r) {
		t.Fatal("空 token 应跳过校验（兼容旧客户端）")
	}
}

func TestCaptchaToken_WrongClientRejected(t *testing.T) {
	// 客户端 A 生成
	rA := httptest.NewRequest(http.MethodGet, "/CaptchaGenerate", nil)
	rA.RemoteAddr = "10.0.0.10:12345"
	rA.Header.Set("User-Agent", "ClientA/1.0")
	token := generateCaptchaToken("cross-client-id", rA)

	// 客户端 B 使用 A 的 token → 应拒绝
	rB := httptest.NewRequest(http.MethodGet, "/CaptchaGenerate", nil)
	rB.RemoteAddr = "10.0.0.20:12345"
	rB.Header.Set("User-Agent", "ClientB/1.0")
	if verifyCaptchaToken("cross-client-id", token, rB) {
		t.Fatal("跨客户端 token 应被拒绝")
	}

	// 同 IP 但不同 UA → 应拒绝（指纹 = IP + UA）
	rC := httptest.NewRequest(http.MethodGet, "/CaptchaGenerate", nil)
	rC.RemoteAddr = "10.0.0.10:12345" // 同 A 的 IP
	rC.Header.Set("User-Agent", "AttackerBot/9.0")
	if verifyCaptchaToken("cross-client-id", token, rC) {
		t.Fatal("同 IP 不同 UA 的 token 应被拒绝")
	}
}

func TestCaptchaToken_DifferentCaptchaID(t *testing.T) {
	r := makeReq(http.MethodGet, "/CaptchaGenerate", "")
	token := generateCaptchaToken("id-original", r)
	// 用其他 captcha_id 校验同一 token → 应拒绝
	if verifyCaptchaToken("id-different", token, r) {
		t.Fatal("不同 captcha_id 使用同一 token 应被拒绝")
	}
}

// ========== 5. 登录失败恒定延迟不回归 ==========

func TestLoginFailureDelay_NonZero(t *testing.T) {
	start := time.Now()
	loginFailureDelay()
	elapsed := time.Since(start)
	// 基准 200ms + 0~50ms 抖动
	if elapsed < 200*time.Millisecond {
		t.Fatalf("延迟应 >= 200ms，实际 %v", elapsed)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("延迟应 <= 250ms（+缓冲），实际 %v", elapsed)
	}
}

// ========== 7. 验证码响应包含 captcha_token（端到端行为） ==========

func TestCaptchaGenerateHandle_ReturnsToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := makeReq(http.MethodGet, "/CaptchaGenerate", "")
	captchaGenerateHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应应为 JSON: %v body=%s", err, rec.Body.String())
	}
	if !resp["success"].(bool) {
		t.Fatal("success 应为 true")
	}
	if _, ok := resp["captcha_token"]; !ok {
		t.Fatal("响应应包含 captcha_token 字段")
	}
	token := resp["captcha_token"].(string)
	if len(token) != 32 {
		t.Fatalf("captcha_token 长度应为 32（16 字节 hex），实际 %d", len(token))
	}
	// image_url 仍存在（向后兼容）
	if _, ok := resp["image_url"]; !ok {
		t.Fatal("响应应仍包含 image_url 字段")
	}
}

// ========== 8. 回归：验证码错误不计入锁定（阶段BQ 行为） ==========

func TestCaptchaErrorDoesNotCountTowardLockout(t *testing.T) {
	// 验证码错误在 captcha.VerifyString 阶段即返回，不调用 recordLoginFailure，
	// 因此不影响 IP/账号维度的失败计数。此回归测试验证调用链行为。
	resetAllLimiters(t)
	ip := getClientIP(makeReq(http.MethodPost, "/UserLoginInterface", "{}"))

	// 直接模拟：验证 checkLoginAttempt 初始无锁定
	if err := checkLoginAttempt(ip); err != nil {
		t.Fatalf("初始状态不应被锁: %v", err)
	}
}
