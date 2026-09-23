package api

// 登录/验证码端点专用限速器（v2.0.78 用户 Web 安全加固）
//
// 独立于 webserver 全局限速器（webserver/security.go 的 rateLimitMiddleware），
// 针对高成本端点（验证码图片生成、登录凭据校验）收紧限制，形成双层防护：
//   - 全局 100 req/min（webserver 层）：防止总体溢出
//   - 端点 30/15 req/min（本层）：防止针对性 CPU/IO 攻击
//
// 超限返回 HTTP 429 + Retry-After + JSON，调用方（前端/api 客户端）据此提示用户。

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// endpointRateLimitEntry 端点限速单 IP 滑动窗口记录
type endpointRateLimitEntry struct {
	count       int
	windowStart time.Time
}

// endpointLimiter 基于 IP 的滑动窗口端点级限速器
type endpointLimiter struct {
	mu           sync.Mutex
	windows      map[string]*endpointRateLimitEntry
	maxPerWindow int
	window       time.Duration
}

// newEndpointLimiter 创建端点限速器并启动后台清理
func newEndpointLimiter(maxPerWindow int, window time.Duration) *endpointLimiter {
	l := &endpointLimiter{
		windows:      make(map[string]*endpointRateLimitEntry),
		maxPerWindow: maxPerWindow,
		window:       window,
	}
	go l.cleanup()
	return l
}

// allow 判断当前 IP 是否允许通过（滑动窗口）
func (l *endpointLimiter) allow(clientIP string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	entry, exists := l.windows[clientIP]
	if !exists || now.Sub(entry.windowStart) > l.window {
		l.windows[clientIP] = &endpointRateLimitEntry{count: 1, windowStart: now}
		return true
	}

	entry.count++
	return entry.count <= l.maxPerWindow
}

// cleanup 定期清理过期记录（防止 map 长期膨胀）
func (l *endpointLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for ip, e := range l.windows {
			if now.Sub(e.windowStart) > l.window*2 {
				delete(l.windows, ip)
			}
		}
		l.mu.Unlock()
	}
}

// Wrap 包装 handler：超限返回 429 JSON，否则放行
func (l *endpointLimiter) Wrap(handle http.HandlerFunc, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := getClientIP(r)
		if !l.allow(clientIP) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(userLoginResp{
				Success: false,
				Message: message,
			})
			return
		}
		handle(w, r)
	}
}

// 包级共享限速器实例（单例，与 webserver 全局限速器独立）
var (
	// captchaRateLimiter 验证码生成端点限速：30 req/min/IP（图片生成 CPU 密集）
	captchaRateLimiter = newEndpointLimiter(30, time.Minute)
	// loginRateLimiter 登录端点限速：15 req/min/IP（凭据校验成本最高）
	loginRateLimiter = newEndpointLimiter(15, time.Minute)
)
