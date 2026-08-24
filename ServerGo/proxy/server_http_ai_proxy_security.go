package proxy

import (
	"encoding/json"
	"fmt"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	proxyAuthRateBurst       = 5
	proxyAuthRatePerSecond   = 1
	proxyAuthBucketTTL       = 10 * time.Minute
	proxyAuthCleanupInterval = time.Minute
)

var (
	proxyAPIKeyPattern = regexp.MustCompile(`^(sk-[A-Za-z0-9_-]{16,}|[A-Fa-f0-9]{16}-[A-Fa-f0-9]{16}-[0-9]{8,}-[0-9]{6,})$`)
	proxyAuthLimiter   = newProxyAuthFailureLimiter()
)

const authorizationBearerAPIKeyMask = modelsdb.AuthorizationBearerAPIKeyMask

type proxyAuthBucket struct {
	mu      sync.Mutex
	tokens  float64
	last    time.Time
	updated time.Time
}

type proxyAuthFailureLimiter struct {
	buckets sync.Map // ip(string) -> *proxyAuthBucket，正常授权请求仅执行一次 Load，无全局锁竞争
	gcMu    sync.Mutex
	lastGC  time.Time
}

func newProxyAuthFailureLimiter() *proxyAuthFailureLimiter {
	return &proxyAuthFailureLimiter{lastGC: time.Now()}
}

// allowFailure 记录一次认证失败并判断是否仍允许返回普通 401。
// 超过阈值后返回 false，调用方应直接返回 429，避免暴力猜测 API Key 继续消耗资源。
func (l *proxyAuthFailureLimiter) allowFailure(ip string, now time.Time) bool {
	ip = normalizeProxyClientIP(ip)
	l.cleanup(now)

	actual, _ := l.buckets.LoadOrStore(ip, &proxyAuthBucket{tokens: proxyAuthRateBurst, last: now, updated: now})
	bucket := actual.(*proxyAuthBucket)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	bucket.refill(now)
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

func (l *proxyAuthFailureLimiter) isLimited(ip string, now time.Time) bool {
	actual, ok := l.buckets.Load(normalizeProxyClientIP(ip))
	if !ok {
		return false
	}

	bucket := actual.(*proxyAuthBucket)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	bucket.refill(now)
	return bucket.tokens < 1
}

func (l *proxyAuthFailureLimiter) reset(ip string) {
	l.buckets.Delete(normalizeProxyClientIP(ip))
}

func (l *proxyAuthFailureLimiter) cleanup(now time.Time) {
	if now.Sub(l.lastGC) < proxyAuthCleanupInterval {
		return
	}
	l.gcMu.Lock()
	defer l.gcMu.Unlock()
	if now.Sub(l.lastGC) < proxyAuthCleanupInterval {
		return
	}
	l.buckets.Range(func(key, value any) bool {
		bucket := value.(*proxyAuthBucket)
		bucket.mu.Lock()
		stale := now.Sub(bucket.updated) > proxyAuthBucketTTL
		bucket.mu.Unlock()
		if stale {
			l.buckets.Delete(key)
		}
		return true
	})
	l.lastGC = now
}

func (b *proxyAuthBucket) refill(now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * proxyAuthRatePerSecond
		if b.tokens > proxyAuthRateBurst {
			b.tokens = proxyAuthRateBurst
		}
		b.last = now
	}
	b.updated = now
}

func normalizeProxyClientIP(ip string) string {
	if ip == "" {
		return "unknown"
	}
	return ip
}

func getProxyClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func isValidProxyAPIKeyFormat(apiKey string) bool {
	return proxyAPIKeyPattern.MatchString(apiKey)
}

func writeProxyAuthFailure(w http.ResponseWriter, r *http.Request, message string) {
	clientIP := getProxyClientIP(r)
	if !proxyAuthLimiter.allowFailure(clientIP, time.Now()) {
		http.Error(w, `{"error":"Too Many Requests","message":"Too many failed authentication attempts"}`, http.StatusTooManyRequests)
		return
	}
	http.Error(w, `{"error":"Unauthorized","message":"`+message+`"}`, http.StatusUnauthorized)
}

func writeProxyRateLimited(w http.ResponseWriter) {
	http.Error(w, `{"error":"Too Many Requests","message":"Too many failed authentication attempts"}`, http.StatusTooManyRequests)
}

func copySafeProxyRequestHeaders(dst http.Header, src http.Header, clientIP string, protocolType int) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if !protocol.ShouldForwardProxyHeader(canonical, protocolType) {
			continue
		}
		for _, value := range values {
			dst.Add(canonical, value)
		}
	}
	if clientIP != "" {
		dst.Set("X-Forwarded-For", clientIP)
	}
}

func ShouldForwardProxyHeader(key string, protocolType int) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "host", "cookie",
		"authorization", "x-api-key", "x-forwarded-for", "x-forwarded-host",
		"x-forwarded-proto", "x-real-ip", "content-length":
		return false
	case "content-type", "accept", "user-agent", "x-request-id",
		"anthropic-version", "anthropic-beta", "openai-beta":
		return true
	}
	return strings.HasPrefix(strings.ToLower(key), "x-stainless-") || protocolType == protocol.AgentProtocolType_OpenAI && strings.HasPrefix(strings.ToLower(key), "openai-")
}

func redactSensitiveHeaderValue(key, value string) string {
	switch strings.ToLower(key) {
	case "authorization":
		return "Bearer ***"
	case "x-api-key", "api-key", "proxy-authorization", "cookie", "set-cookie":
		return "***"
	}
	return value
}

func redactAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	if len(apiKey) <= 8 {
		return "***"
	}
	return apiKey[:4] + "***" + apiKey[len(apiKey)-4:]
}

func formatRedactedHeaders(headers http.Header) string {
	var b strings.Builder
	for key, values := range headers {
		fmt.Fprintf(&b, "%s: %s\n", key, redactSensitiveHeaderValue(key, strings.Join(values, ", ")))
	}
	return b.String()
}

func formatRawHeaders(headers http.Header) string {
	var b strings.Builder
	for key, values := range headers {
		fmt.Fprintf(&b, "%s: %s\n", key, strings.Join(values, ", "))
	}
	return b.String()
}

// redactAuthorizationBearerHeaderText 已迁至 models/security_redact.go（modelsdb.RedactAuthorizationBearerHeaderText）

type cappedLogWriter struct {
	buf   []byte
	limit int
	total int64
}

func newCappedLogWriter(limit int) *cappedLogWriter {
	return &cappedLogWriter{limit: limit, buf: make([]byte, 0, limit)}
}

func (w *cappedLogWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if len(w.buf) < w.limit {
		remain := min(w.limit-len(w.buf), len(p))
		w.buf = append(w.buf, p[:remain]...)
	}
	return len(p), nil
}

func (w *cappedLogWriter) String() string {
	if w.total > int64(w.limit) {
		return string(w.buf) + "\n...[truncated]"
	}
	return string(w.buf)
}

func (w *cappedLogWriter) Len() int64 {
	return w.total
}

func redactSensitiveJSONBody(body string) string {
	if body == "" || strings.Contains(body, "[truncated]") {
		return body
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body
	}
	if !redactSensitiveJSONValue(v) {
		return body
	}
	redacted, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return string(redacted)
}

func redactSensitiveJSONValue(v any) bool {
	switch data := v.(type) {
	case map[string]any:
		changed := false
		for key, value := range data {
			if isSensitiveJSONKey(key) {
				data[key] = "***"
				changed = true
				continue
			}
			if redactSensitiveJSONValue(value) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, item := range data {
			if redactSensitiveJSONValue(item) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func isSensitiveJSONKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	switch normalized {
	case "api_key", "apikey", "authorization", "access_token", "refresh_token", "token", "secret", "password":
		return true
	}
	return strings.Contains(normalized, "api_key") || strings.Contains(normalized, "access_token")
}
