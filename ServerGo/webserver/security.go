package webserver

// Web 端口安全中间件链（迁移自旧工程 server_web_security.go）
// SanitizeInput / ValidateField 等输入过滤已随 api/security_util.go 迁移，此处不再重复。

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lishimeng/LsmTokensServer/api"
)

// ========== 安全响应头中间件 ==========

// securityHeadersMiddleware 添加基础安全响应头
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;")
		next.ServeHTTP(w, r)
	})
}

// publicSecurityHeadersMiddleware 公网服务使用的严格安全头
func publicSecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self';")
		next.ServeHTTP(w, r)
	})
}

// ========== 请求大小限制中间件 ==========

// requestSizeLimitMiddleware 限制请求体大小，防止 DoS
func requestSizeLimitMiddleware(maxSize int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxSize {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				fmt.Fprint(w, `{"success":false,"message":"请求体超过大小限制"}`)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			next.ServeHTTP(w, r)
		})
	}
}

// ========== 速率限制中间件 ==========

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

var (
	rateLimitMu    sync.RWMutex
	rateLimitStore = make(map[string]*rateLimitEntry)
)

// rateLimitMiddleware 基于 IP 的滑动窗口速率限制
func rateLimitMiddleware(maxRequests int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := api.GetClientIP(r)
			now := time.Now()

			rateLimitMu.Lock()
			entry, exists := rateLimitStore[clientIP]
			if !exists || now.Sub(entry.windowStart) > window {
				rateLimitStore[clientIP] = &rateLimitEntry{count: 1, windowStart: now}
				rateLimitMu.Unlock()
				next.ServeHTTP(w, r)
				return
			}

			entry.count++
			currentCount := entry.count
			rateLimitMu.Unlock()

			if currentCount > maxRequests {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, `{"success":false,"message":"请求过于频繁，请 %d 秒后再试"}`, int(window.Seconds()))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// cleanRateLimitStore 定期清理过期的速率限制记录
func cleanRateLimitStore() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rateLimitMu.Lock()
		now := time.Now()
		for ip, entry := range rateLimitStore {
			if now.Sub(entry.windowStart) > 10*time.Minute {
				delete(rateLimitStore, ip)
			}
		}
		rateLimitMu.Unlock()
	}
}

// ========== 防重放攻击中间件 ==========

const (
	replayTimeWindow   = 5 * time.Minute
	replayNonceCleanup = 10 * time.Minute
)

type nonceEntry struct {
	timestamp time.Time
}

var (
	nonceMu    sync.RWMutex
	nonceStore = make(map[string]*nonceEntry)
)

// replayProtectionMiddleware 防重放攻击（校验 X-Timestamp 和 X-Nonce）
// 对 API 请求生效，不强制要求前端发送（向后兼容），但如果发送了就严格校验
func replayProtectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只对 POST/PUT/PATCH/DELETE 请求做防重放校验
		if r.Method != http.MethodPost && r.Method != http.MethodPut &&
			r.Method != http.MethodPatch && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}

		timestampStr := r.Header.Get("X-Timestamp")
		nonceStr := r.Header.Get("X-Nonce")

		// 如果前端没有发送时间戳和 nonce，跳过校验（向后兼容）
		if timestampStr == "" || nonceStr == "" {
			next.ServeHTTP(w, r)
			return
		}

		timestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"success":false,"message":"无效的时间戳格式"}`)
			return
		}

		now := time.Now()
		if timestamp.Before(now.Add(-replayTimeWindow)) || timestamp.After(now.Add(replayTimeWindow)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"success":false,"message":"时间戳偏差过大（超过 %d 分钟）"}`, int(replayTimeWindow.Minutes()))
			return
		}

		nonceKey := timestampStr + ":" + nonceStr
		nonceMu.Lock()
		if _, exists := nonceStore[nonceKey]; exists {
			nonceMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"success":false,"message":"重复的请求（nonce 已被使用）"}`)
			return
		}
		nonceStore[nonceKey] = &nonceEntry{timestamp: now}
		nonceMu.Unlock()

		next.ServeHTTP(w, r)
	})
}

// cleanNonceStore 定期清理过期的 nonce 记录
func cleanNonceStore() {
	ticker := time.NewTicker(replayNonceCleanup)
	defer ticker.Stop()
	for range ticker.C {
		nonceMu.Lock()
		now := time.Now()
		for key, entry := range nonceStore {
			if now.Sub(entry.timestamp) > replayTimeWindow*2 {
				delete(nonceStore, key)
			}
		}
		nonceMu.Unlock()
	}
}

func init() {
	go cleanRateLimitStore()
	go cleanNonceStore()
}

// ========== 综合安全中间件组合 ==========

// ManagerSecurityChain 管理员 Web 服务安全中间件链（内网，基本防护）
func ManagerSecurityChain(handler http.Handler) http.Handler {
	chain := securityHeadersMiddleware(handler)
	chain = requestSizeLimitMiddleware(10 * 1024 * 1024)(chain) // 10MB
	chain = rateLimitMiddleware(300, time.Minute)(chain)        // 内网宽松：300 req/min
	return chain
}

// UserSecurityChain 用户 Web 服务安全中间件链（公网，严格防护）
func UserSecurityChain(handler http.Handler) http.Handler {
	chain := publicSecurityHeadersMiddleware(handler)
	chain = requestSizeLimitMiddleware(5 * 1024 * 1024)(chain) // 5MB
	chain = rateLimitMiddleware(100, time.Minute)(chain)       // 公网严格
	chain = replayProtectionMiddleware(chain)
	return chain
}
