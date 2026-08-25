package models

import (
	"net/http"
	"testing"
)

// v2.0.74：402（源站账号余额不足）必须触发故障转移，
// 否则代理会把 402 直接透传给客户端且不累计失败计数，永远不切换源站。
func TestIsFailoverError402(t *testing.T) {
	if !IsFailoverError(http.StatusPaymentRequired, nil) {
		t.Fatalf("402 should trigger failover")
	}
	if !IsFailoverError(http.StatusTooManyRequests, nil) {
		t.Fatalf("429 should trigger failover")
	}
	if IsFailoverError(http.StatusBadRequest, nil) {
		t.Fatalf("400 should NOT trigger failover")
	}
	if !IsFailoverError(0, &netTimeoutError{}) {
		t.Fatalf("network error should trigger failover")
	}
}

type netTimeoutError struct{}

func (netTimeoutError) Error() string   { return "i/o timeout" }
func (netTimeoutError) Timeout() bool   { return true }
func (netTimeoutError) Temporary() bool { return true }

// v2.0.74：经济型 session 粘性在请求内重试换源 ——
// 某源站失败后 InvalidateSessionMapping 应让下一次 SelectForSession 分配到其它源站。
func TestEconomicInvalidateSessionMappingRetriesDifferentEndpoint(t *testing.T) {
	ResetEconomicRouteState(1)
	defer ResetEconomicRouteState(1)

	route := &CachedAIRoute{DstEndPointIDs: []uint64{101, 102}}
	route.ID = 1
	sel := &EconomicAlgorithmSelector{}

	first, ok := sel.SelectForSession(route, "sess-402")
	if !ok {
		t.Fatalf("first select failed")
	}

	// 模拟该源站返回 402 触发请求内重试：清映射后必须分配到另一个源站
	sel.InvalidateSessionMapping(route.ID, "sess-402")
	second, ok := sel.SelectForSession(route, "sess-402")
	if !ok {
		t.Fatalf("second select failed")
	}
	if second == first {
		t.Fatalf("after invalidation, session should be re-assigned to a different endpoint: first=%d second=%d", first, second)
	}

	// 无关 session 不受影响；空 sessionID 是 no-op
	sel.InvalidateSessionMapping(route.ID, "")
	sel.InvalidateSessionMapping(route.ID, "not-exist")
}
