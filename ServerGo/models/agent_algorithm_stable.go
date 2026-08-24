package models

import (
	"github.com/lishimeng/LsmTokensServer/logger"
	"sync"
)

const (
	// StableAlgorithm_MaxConsecutiveFailures 最大连续失败次数
	// 连续失败达到此次数后，把 DstEndPointIDList 第 0 个源站滚动到末尾，切换到下一个。
	StableAlgorithm_MaxConsecutiveFailures = 3
)

// ============================================================================
// 稳定型算法的状态模型（v2.x 重构）
// ============================================================================
// 不再维护内存里的 ActiveIndex / RouteEndpointState。
// "当前生效的源站" 永远等于 DstEndPointIDList 的第 0 个。
//
// 滚动切换由 RotateAIRouteEndpointList(routeID) 真正修改 database.DB + 内存缓存里的列表，
// 让重启之后顺序仍然正确，避免 "服务重启后重新从配置顺序开始" 与
// 用户手动调整顺序之间的语义冲突。
//
// 唯一的瞬时状态是 "当前队首源站连续失败次数"：仅按 routeID 维护一个计数器。
// 计数器不持久化（重启清零是合理的——重启后队首可能已是新的源站）。
// ============================================================================

// stableFailureCounters 按 routeID 统计当前队首源站的连续失败次数
var (
	stableFailureCounters   = make(map[uint64]int)
	stableFailureCountersMu sync.Mutex
)

// resetStableFailureCounter 清零某个路由的失败计数器
func resetStableFailureCounter(routeID uint64) {
	stableFailureCountersMu.Lock()
	delete(stableFailureCounters, routeID)
	stableFailureCountersMu.Unlock()
}

// incStableFailureCounter 递增并返回当前的失败计数
func incStableFailureCounter(routeID uint64) int {
	stableFailureCountersMu.Lock()
	stableFailureCounters[routeID]++
	v := stableFailureCounters[routeID]
	stableFailureCountersMu.Unlock()
	return v
}

// ============================================================================
// StableAlgorithmSelector 稳定型算法选择器
// ============================================================================
// Select: 永远返回 DstEndPointIDs[0]
// OnRequestSuccess: 清零失败计数
// OnRequestFailure: 失败计数 +1；达 3 触发 RotateAIRouteEndpointList，列表向左滚动一格，
//
//	原队首移到队尾；同时同步滚动 DstEndPointAlgorithmTypeList。
//
// ============================================================================
type StableAlgorithmSelector struct{}

// Select 选择目标源站：稳定型算法选择第一个状态为启用的源站。
// 若全部禁用，返回 (0, false)。
func (s *StableAlgorithmSelector) Select(route *CachedAIRoute) (uint64, bool) {
	if route == nil || len(route.DstEndPointIDs) == 0 {
		return 0, false
	}
	for i, id := range route.DstEndPointIDs {
		if i < len(route.DstEndPointIDStatuses) && route.DstEndPointIDStatuses[i] == 1 {
			return id, true
		}
		// 兼容：状态列表缺失或长度不足时，默认启用
		if i >= len(route.DstEndPointIDStatuses) {
			return id, true
		}
	}
	return 0, false
}

// OnRequestSuccess 请求成功：清零路由的连续失败计数器
func (s *StableAlgorithmSelector) OnRequestSuccess(routeID uint64) {
	resetStableFailureCounter(routeID)
}

// OnRequestFailure 请求失败：递增失败计数；达到阈值时滚动列表并清零计数。
// route 仅用于早期短路（单源站时不滚动）。
// 滚动时跳过状态为0的源站，只滚动可用源站。
func (s *StableAlgorithmSelector) OnRequestFailure(routeID uint64, route *CachedAIRoute) {
	if route == nil || len(route.DstEndPointIDs) <= 1 {
		// 没有可滚动的对象，直接清零，避免计数无限增长
		resetStableFailureCounter(routeID)
		return
	}

	count := incStableFailureCounter(routeID)
	if count < StableAlgorithm_MaxConsecutiveFailures {
		return
	}

	// 达到阈值：先清零，再触发滚动。
	// 即使滚动失败也要清零，避免下一次请求又走到这里立即重试导致雪崩。
	resetStableFailureCounter(routeID)
	if err := RotateAIRouteEndpointList(routeID); err != nil {
		logger.Printf("[ROUTE] Stable rotate failed for routeID=%d: %v", routeID, err)
	}
}
