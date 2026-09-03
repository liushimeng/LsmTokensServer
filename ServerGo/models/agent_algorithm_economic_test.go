package models

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
)

// ============================================================================
// EconomicAlgorithmSelector 测试 —— v2.0.7 启动时间戳哈希
// ============================================================================

func makeTestRoute(id uint64, endpointIDs []uint64) *CachedAIRoute {
	route := &CachedAIRoute{}
	route.ID = id
	route.UserModelID = id * 10
	route.DstEndPointIDs = endpointIDs
	route.DstEndPointAlgorithmTypes = make([]int, len(endpointIDs))
	for i := range route.DstEndPointAlgorithmTypes {
		route.DstEndPointAlgorithmTypes[i] = 1
	}
	// 默认全部启用
	route.DstEndPointIDStatuses = make([]int, len(endpointIDs))
	for i := range route.DstEndPointIDStatuses {
		route.DstEndPointIDStatuses[i] = 1
	}
	return route
}

// makeTestRouteWithStatuses 创建带自定义状态列表的测试路由
func makeTestRouteWithStatuses(id uint64, endpointIDs []uint64, statuses []int) *CachedAIRoute {
	route := makeTestRoute(id, endpointIDs)
	if len(statuses) == len(endpointIDs) {
		copy(route.DstEndPointIDStatuses, statuses)
	}
	return route
}

func TestEconomicSelectorSessionAffinity(t *testing.T) {
	ResetEconomicRouteState(100)
	defer ResetEconomicRouteState(100)

	route := makeTestRoute(100, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	id1, ok := sel.SelectForSession(route, "session-A")
	if !ok || id1 == 0 {
		t.Fatalf("first select failed: id=%d, ok=%v", id1, ok)
	}

	// 同一 session 应返回相同源站（当前服务生命周期内粘性）
	id2, ok := sel.SelectForSession(route, "session-A")
	if !ok || id2 != id1 {
		t.Errorf("session affinity broken: first=%d, second=%d", id1, id2)
	}
}

func TestEconomicSelectorLivePoolConsume(t *testing.T) {
	ResetEconomicRouteState(150)
	defer ResetEconomicRouteState(150)

	route := makeTestRoute(150, []uint64{10, 20, 30})
	sel := &EconomicAlgorithmSelector{}

	// 消费 3 次后 livePool 应该清空
	got := make(map[uint64]bool)
	for i := 0; i < 3; i++ {
		id, ok := sel.SelectForSession(route, fmt.Sprintf("session-%d", i))
		if !ok {
			t.Fatalf("select failed for session-%d", i)
		}
		got[id] = true
	}
	if len(got) != 3 {
		t.Errorf("expected 3 unique endpoints, got %d: %v", len(got), got)
	}

	info, _, err := GetEconomicStateInfo(150)
	if err != nil {
		t.Fatal(err)
	}
	if info.LivePoolSize != 0 {
		t.Errorf("expected livePool empty after 3 consumes, got size %d", info.LivePoolSize)
	}
}

func TestEconomicSelectorLivePoolRefill(t *testing.T) {
	ResetEconomicRouteState(160)
	defer ResetEconomicRouteState(160)

	route := makeTestRoute(160, []uint64{100, 200, 300})
	sel := &EconomicAlgorithmSelector{}

	// 消费 3 次，耗尽 livePool
	for i := 0; i < 3; i++ {
		sel.SelectForSession(route, fmt.Sprintf("s-%d", i))
	}
	// 第四次：应触发 livePool 重新排序填充
	id, ok := sel.SelectForSession(route, "s-new")
	if !ok {
		t.Fatal("expected ok after refill")
	}
	if id != 100 && id != 200 && id != 300 {
		t.Errorf("refill returned unexpected endpoint %d", id)
	}
}

func TestEconomicSelectorQueueOverflow(t *testing.T) {
	ResetEconomicRouteState(300)
	defer ResetEconomicRouteState(300)

	route := makeTestRoute(300, []uint64{1, 2})
	sel := &EconomicAlgorithmSelector{}

	// 添加 EconomicSessionQueueMaxSize + 10 个 session
	total := EconomicSessionQueueMaxSize + 10
	for i := 0; i < total; i++ {
		sel.SelectForSession(route, fmt.Sprintf("session-%04d", i))
	}

	_, count, err := GetEconomicStateInfo(300)
	if err != nil {
		t.Fatal(err)
	}
	if count != EconomicSessionQueueMaxSize {
		t.Errorf("expected queue size %d, got %d", EconomicSessionQueueMaxSize, count)
	}

	// 最早的 session 应已被淘汰；查询它时按新 session 路径处理
	oldest := "session-0000"
	id, ok := sel.SelectForSession(route, oldest)
	if !ok {
		t.Fatal("select failed for evicted session")
	}
	if id != 1 && id != 2 {
		t.Errorf("expected endpoint 1 or 2, got %d", id)
	}
}

func TestEconomicSelectorFallbackNoSession(t *testing.T) {
	ResetEconomicRouteState(400)
	defer ResetEconomicRouteState(400)

	route := makeTestRoute(400, []uint64{100, 200, 300})
	sel := &EconomicAlgorithmSelector{}

	// 空 session_id 应退化为 Select()，返回 DstEndPointIDs[0]
	id, ok := sel.SelectForSession(route, "")
	if !ok || id != 100 {
		t.Errorf("expected 100 (first), got %d, ok=%v", id, ok)
	}

	// v2.0.75：兜底路径会初始化经济型状态（用于冷却源站检查），但不得消费 livePool
	if state, exists := economicStates[400]; exists {
		state.mu.Lock()
		poolLen := len(state.livePool)
		state.mu.Unlock()
		if poolLen != 0 {
			t.Errorf("fallback path should not consume livePool, got len=%d", poolLen)
		}
	}
}

func TestEconomicSelectorSelectFallback(t *testing.T) {
	ResetEconomicRouteState(500)
	defer ResetEconomicRouteState(500)

	route := makeTestRoute(500, []uint64{42, 84})
	sel := &EconomicAlgorithmSelector{}

	id, ok := sel.Select(route)
	if !ok || id != 42 {
		t.Errorf("expected 42, got %d, ok=%v", id, ok)
	}
}

func TestEconomicSelectorSingleEndpoint(t *testing.T) {
	ResetEconomicRouteState(700)
	defer ResetEconomicRouteState(700)

	route := makeTestRoute(700, []uint64{42})
	sel := &EconomicAlgorithmSelector{}

	// 单源站：livePool 只有一项，每次消费后 refill，永远是该 ID
	for i := 0; i < 5; i++ {
		id, ok := sel.SelectForSession(route, fmt.Sprintf("s-%d", i))
		if !ok || id != 42 {
			t.Errorf("single endpoint: expected 42, got %d, ok=%v", id, ok)
		}
	}
}

func TestEconomicSelectorNilRoute(t *testing.T) {
	sel := &EconomicAlgorithmSelector{}

	id, ok := sel.Select(nil)
	if ok || id != 0 {
		t.Errorf("nil route: expected 0, false, got %d, %v", id, ok)
	}

	id, ok = sel.SelectForSession(nil, "session-1")
	if ok || id != 0 {
		t.Errorf("nil route SelectForSession: expected 0, false, got %d, %v", id, ok)
	}
}

func TestEconomicOnEndpointFailureShouldRemove(t *testing.T) {
	ResetEconomicRouteState(750)
	defer ResetEconomicRouteState(750)

	sel := &EconomicAlgorithmSelector{}

	// 失败 2 次不应触发移除
	shouldRemove, _ := sel.OnEndpointFailure(750, 1)
	if shouldRemove {
		t.Error("expected shouldRemove=false after 1 failure")
	}
	shouldRemove, _ = sel.OnEndpointFailure(750, 1)
	if shouldRemove {
		t.Error("expected shouldRemove=false after 2 failures")
	}

	// 第 3 次应触发移除
	shouldRemove, removedID := sel.OnEndpointFailure(750, 1)
	if !shouldRemove {
		t.Error("expected shouldRemove=true after 3 failures")
	}
	if removedID != 1 {
		t.Errorf("expected removedID=1, got %d", removedID)
	}
}

func TestEconomicOnRequestSuccess(t *testing.T) {
	ResetEconomicRouteState(760)
	defer ResetEconomicRouteState(760)

	sel := &EconomicAlgorithmSelector{}

	// 累计失败计数
	sel.OnEndpointFailure(760, 1)
	sel.OnEndpointFailure(760, 1)
	sel.OnEndpointFailure(760, 2)

	state := getEconomicState(760)
	state.mu.Lock()
	c1 := state.endpointFailureCount[1]
	c2 := state.endpointFailureCount[2]
	state.mu.Unlock()
	if c1 != 2 || c2 != 1 {
		t.Errorf("expected failure counts 2/1, got %d/%d", c1, c2)
	}

	// 成功应清零所有计数
	sel.OnRequestSuccess(760)
	state.mu.Lock()
	c1 = state.endpointFailureCount[1]
	c2 = state.endpointFailureCount[2]
	state.mu.Unlock()
	if c1 != 0 || c2 != 0 {
		t.Errorf("expected failure counts 0/0 after success, got %d/%d", c1, c2)
	}
}

// ============================================================================
// SyncEconomicRouteEndpoints 测试 —— 增/删双向同步
// ============================================================================

func TestEconomicSyncEndpoints_Add(t *testing.T) {
	ResetEconomicRouteState(800)
	defer ResetEconomicRouteState(800)

	route := makeTestRoute(800, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 先消费 2 次（首次填充 livePool 3 个，剩 1 个）
	sel.SelectForSession(route, "s1")
	sel.SelectForSession(route, "s2")

	// 模拟 Web 添加端点 4
	SyncEconomicRouteEndpoints(800, []uint64{1, 2, 3, 4})

	info, _, _ := GetEconomicStateInfo(800)
	// livePool 当前 = [剩余 1 个] + 新增 3 个（1,2,4 不在 livePool 中）= 4 个
	if info.LivePoolSize != 4 {
		t.Errorf("expected livePool size 4, got %d (pool=%v)", info.LivePoolSize, info.LivePool)
	}
	// 验证 4 在 livePool 中
	found4 := false
	for _, id := range info.LivePool {
		if id == 4 {
			found4 = true
		}
	}
	if !found4 {
		t.Errorf("expected livePool to contain 4, got %v", info.LivePool)
	}
}

func TestEconomicSyncEndpoints_Remove(t *testing.T) {
	ResetEconomicRouteState(810)
	defer ResetEconomicRouteState(810)

	route := makeTestRoute(810, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 消费所有 3 个源站，耗尽 livePool
	sel.SelectForSession(route, "s1")
	sel.SelectForSession(route, "s2")
	sel.SelectForSession(route, "s3")

	// 移除端点 2
	SyncEconomicRouteEndpoints(810, []uint64{1, 3})

	info, _, _ := GetEconomicStateInfo(810)
	for _, id := range info.LivePool {
		if id == 2 {
			t.Errorf("livePool should not contain removed endpoint 2, got %v", info.LivePool)
		}
	}
}

func TestEconomicSyncEndpoints_Remove_ReassignSession(t *testing.T) {
	ResetEconomicRouteState(820)
	defer ResetEconomicRouteState(820)

	route := makeTestRoute(820, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 建 6 个 session 映射
	for i := 0; i < 6; i++ {
		sel.SelectForSession(route, fmt.Sprintf("s-%d", i))
	}

	// 移除端点 2（必定有部分 session 映射到 2）
	SyncEconomicRouteEndpoints(820, []uint64{1, 3})

	// 验证：所有 session 都不应映射到 2
	state := getEconomicState(820)
	state.mu.Lock()
	for _, entry := range state.sessionQueue {
		if entry.EndPointID == 2 {
			t.Errorf("session %s still mapped to removed endpoint 2", entry.SessionID)
		}
	}
	state.mu.Unlock()

	// 验证：移除的端点 2 的失败计数被清掉
	state.mu.Lock()
	if _, exists := state.endpointFailureCount[2]; exists {
		t.Error("expected endpoint 2 failure count to be cleared")
	}
	state.mu.Unlock()
}

func TestEconomicSyncEndpoints_RemoveAll(t *testing.T) {
	ResetEconomicRouteState(830)
	defer ResetEconomicRouteState(830)

	route := makeTestRoute(830, []uint64{1, 2})
	sel := &EconomicAlgorithmSelector{}

	// 建 3 个 session
	for i := 0; i < 3; i++ {
		sel.SelectForSession(route, fmt.Sprintf("s-%d", i))
	}

	// 模拟「所有源站被清空」（极端边界，应清掉 session 队列）
	SyncEconomicRouteEndpoints(830, []uint64{})

	info, _, _ := GetEconomicStateInfo(830)
	if info.SessionCount != 0 {
		t.Errorf("expected session queue cleared, got size %d", info.SessionCount)
	}
	if info.LivePoolSize != 0 {
		t.Errorf("expected livePool cleared, got size %d", info.LivePoolSize)
	}
}

func TestEconomicAlgorithmSwitchFromEconomic(t *testing.T) {
	ResetEconomicRouteState(840)
	defer ResetEconomicRouteState(840)

	route := makeTestRoute(840, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 先建一些 session
	sel.SelectForSession(route, "s1")
	sel.SelectForSession(route, "s2")

	// 验证状态存在
	info, _, _ := GetEconomicStateInfo(840)
	if info == nil {
		t.Fatal("expected economic state to exist")
	}

	// 切到非经济型
	ResetEconomicRouteState(840)

	// 状态应被清空
	info2, _, _ := GetEconomicStateInfo(840)
	if info2 != nil {
		t.Errorf("expected economic state to be cleared after Reset, got %+v", info2)
	}
}

func TestEconomicConsumeSkipsRemovedEndpoint(t *testing.T) {
	ResetEconomicRouteState(850)
	defer ResetEconomicRouteState(850)

	route := makeTestRoute(850, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 建一个 session，假设分到了 2
	id1, ok := sel.SelectForSession(route, "sess-A")
	if !ok {
		t.Fatal("first select failed")
	}

	// 模拟：路由配置中删除了该源站（id1 不再在 DstEndPointIDs 中）
	route2 := makeTestRoute(850, []uint64{99, 100}) // 不含 id1

	// 再次查询：应识别为失效并重新分配
	id2, ok := sel.SelectForSession(route2, "sess-A")
	if !ok {
		t.Fatal("expected ok after invalidation")
	}
	// 重新分配到的 ID 应是 route2 中的某个
	if id2 != 99 && id2 != 100 {
		t.Errorf("expected reassigned endpoint 99 or 100, got %d", id2)
	}
	// 不应等于原 id1
	if id2 == id1 {
		t.Errorf("expected different endpoint after invalidation, got same %d", id1)
	}
}

// ============================================================================
// 并发测试
// ============================================================================

func TestEconomicConcurrentSessionAssign(t *testing.T) {
	ResetEconomicRouteState(900)
	defer ResetEconomicRouteState(900)

	route := makeTestRoute(900, []uint64{10, 20, 30, 40, 50})
	sel := &EconomicAlgorithmSelector{}

	const N = 100
	var wg sync.WaitGroup
	results := make([]uint64, N)
	errs := make(chan string, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, ok := sel.SelectForSession(route, fmt.Sprintf("session-%d", idx))
			if !ok {
				errs <- fmt.Sprintf("goroutine %d: select failed", idx)
				return
			}
			results[idx] = id
		}(i)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}

	// 验证：所有 ID 都属于 route.DstEndPointIDs
	for i, id := range results {
		valid := false
		for _, allowed := range route.DstEndPointIDs {
			if id == allowed {
				valid = true
				break
			}
		}
		if !valid {
			t.Errorf("goroutine %d: invalid endpoint %d", i, id)
		}
	}
}

func TestEconomicConcurrentSessionAffinity(t *testing.T) {
	ResetEconomicRouteState(910)
	defer ResetEconomicRouteState(910)

	route := makeTestRoute(910, []uint64{10, 20, 30})
	sel := &EconomicAlgorithmSelector{}

	// 同一 sessionID 在并发下也应返回相同 endpoint
	const Goroutines = 50
	sessionID := "stable-session"
	var wg sync.WaitGroup
	results := make([]uint64, Goroutines)

	for i := 0; i < Goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, ok := sel.SelectForSession(route, sessionID)
			if !ok {
				t.Errorf("goroutine %d: select failed", idx)
				return
			}
			results[idx] = id
		}(i)
	}
	wg.Wait()

	// 所有结果应一致
	first := results[0]
	for i, id := range results {
		if id != first {
			t.Errorf("goroutine %d: got %d, expected %d (affinity broken)", i, id, first)
		}
	}
}

// ============================================================================
// 确定性哈希测试 —— v2.0.7 启动时间戳种子
// ============================================================================

func TestHashSessionToEndpoint_Deterministic(t *testing.T) {
	livePool := []uint64{10, 20, 30, 40, 50}

	// 同一 session + route + livePool 应总是返回相同索引
	idx1 := hashSessionToEndpoint("sess-abc", 100, livePool)
	idx2 := hashSessionToEndpoint("sess-abc", 100, livePool)
	if idx1 != idx2 {
		t.Errorf("expected same index, got %d vs %d", idx1, idx2)
	}

	// 索引应在合法范围内
	if idx1 < 0 || idx1 >= len(livePool) {
		t.Errorf("index %d out of range [0, %d)", idx1, len(livePool))
	}
}

func TestHashSessionToEndpoint_DifferentSession(t *testing.T) {
	livePool := []uint64{10, 20, 30}
	routeID := uint64(200)

	// 不同 session 应分配到不同端点（大概率）
	distribution := make(map[uint64]int)
	for i := 0; i < 100; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		idx := hashSessionToEndpoint(sessionID, routeID, livePool)
		endpointID := livePool[idx]
		distribution[endpointID]++
	}

	// 100 个 session 应分布到 3 个端点
	if len(distribution) != 3 {
		t.Errorf("expected 3 endpoints used, got %d: %v", len(distribution), distribution)
	}

	// 每个端点至少被分配到几次（均匀性检查）
	for _, id := range livePool {
		if distribution[id] < 10 {
			t.Errorf("endpoint %d only got %d sessions, expected >= 10", id, distribution[id])
		}
	}
}

func TestHashSessionToEndpoint_RouteIDMatters(t *testing.T) {
	livePool := []uint64{10, 20, 30}
	sessionID := "same-session"

	// 不同 routeID 应可能产生不同索引
	idx1 := hashSessionToEndpoint(sessionID, 100, livePool)
	idx2 := hashSessionToEndpoint(sessionID, 200, livePool)

	// 只是验证两者都在合法范围内，不强制要求不同（哈希碰撞可能相同）
	if idx1 < 0 || idx1 >= len(livePool) {
		t.Errorf("idx1 %d out of range", idx1)
	}
	if idx2 < 0 || idx2 >= len(livePool) {
		t.Errorf("idx2 %d out of range", idx2)
	}
}

func TestHashSessionToEndpoint_LivePoolMatters(t *testing.T) {
	livePool1 := []uint64{10, 20, 30}
	livePool2 := []uint64{10, 20, 30, 40} // 多了一个端点
	sessionID := "same-session"
	routeID := uint64(100)

	// livePool 内容不同，哈希输入不同，可能产生不同索引
	idx1 := hashSessionToEndpoint(sessionID, routeID, livePool1)
	idx2 := hashSessionToEndpoint(sessionID, routeID, livePool2)

	// 两者都应在合法范围内
	if idx1 < 0 || idx1 >= len(livePool1) {
		t.Errorf("idx1 %d out of range for livePool1", idx1)
	}
	if idx2 < 0 || idx2 >= len(livePool2) {
		t.Errorf("idx2 %d out of range for livePool2", idx2)
	}
}

// TestEconomicSelectorCrossRestartRandomization 验证服务重启后同一 session 重新随机分配
// 这是 v2.0.7 的核心目标：保证服务频繁重启后各源站均衡使用
func TestEconomicSelectorCrossRestartRandomization(t *testing.T) {
	// 保存当前启动时间戳
	originalStartTime := economicStartTime
	defer func() { economicStartTime = originalStartTime }()

	ResetEconomicRouteState(1000)

	route := makeTestRoute(1000, []uint64{10, 20, 30, 40, 50})
	sel := &EconomicAlgorithmSelector{}
	sessionID := "cross-restart-session"

	// 第一次分配（当前启动时间戳）
	id1, ok := sel.SelectForSession(route, sessionID)
	if !ok {
		t.Fatal("first select failed")
	}

	// 模拟重启：改变启动时间戳 + 重置状态
	economicStartTime = originalStartTime + 1
	ResetEconomicRouteState(1000)

	// 再次分配（新启动时间戳）
	id2, ok := sel.SelectForSession(route, sessionID)
	if !ok {
		t.Fatal("second select after reset failed")
	}

	// 服务重启后，同一 session 应重新随机分配（可能不同）
	// 注意：哈希碰撞可能导致相同，但概率很低；这里只验证都在合法范围内
	validEndpoints := map[uint64]bool{10: true, 20: true, 30: true, 40: true, 50: true}
	if !validEndpoints[id1] {
		t.Errorf("first allocation invalid: %d", id1)
	}
	if !validEndpoints[id2] {
		t.Errorf("second allocation invalid: %d", id2)
	}

	// 多次重启后统计分布，验证均衡性
	distribution := make(map[uint64]int)
	distribution[id1]++
	distribution[id2]++

	// 模拟更多次重启
	for i := 0; i < 48; i++ {
		economicStartTime = originalStartTime + int64(i+2)
		ResetEconomicRouteState(1000)
		id, ok := sel.SelectForSession(route, sessionID)
		if !ok {
			t.Fatalf("select failed at restart %d", i)
		}
		distribution[id]++
	}

	// 50 次重启后，5 个端点都应被使用（均衡性）
	if len(distribution) < 3 {
		t.Errorf("expected at least 3 endpoints used after 50 restarts, got %d: %v", len(distribution), distribution)
	}

	// 每个端点至少被使用几次
	for _, id := range route.DstEndPointIDs {
		if distribution[id] < 3 {
			t.Errorf("endpoint %d only got %d allocations after 50 restarts, expected >= 3", id, distribution[id])
		}
	}
}

func TestEconomicSelectorMultipleSessionsDistribution(t *testing.T) {
	ResetEconomicRouteState(1100)
	defer ResetEconomicRouteState(1100)

	route := makeTestRoute(1100, []uint64{10, 20, 30})
	sel := &EconomicAlgorithmSelector{}

	// 创建 30 个不同 session，检查分布
	distribution := make(map[uint64]int)
	for i := 0; i < 30; i++ {
		sessionID := fmt.Sprintf("distributed-session-%d", i)
		id, ok := sel.SelectForSession(route, sessionID)
		if !ok {
			t.Fatalf("select failed for %s", sessionID)
		}
		distribution[id]++
	}

	// 3 个端点都应被使用
	if len(distribution) != 3 {
		t.Errorf("expected 3 endpoints used, got %d: %v", len(distribution), distribution)
	}

	// 每个端点至少被分配到 5 次（30/3=10，允许一定偏差）
	for _, id := range route.DstEndPointIDs {
		if distribution[id] < 5 {
			t.Errorf("endpoint %d only got %d sessions, expected >= 5", id, distribution[id])
		}
	}
}

func TestEconomicSelectorLivePoolRefillDeterministic(t *testing.T) {
	ResetEconomicRouteState(1200)
	defer ResetEconomicRouteState(1200)

	route := makeTestRoute(1200, []uint64{10, 20})
	sel := &EconomicAlgorithmSelector{}

	// 消费 2 次，耗尽 livePool
	sel.SelectForSession(route, "s1")
	sel.SelectForSession(route, "s2")

	info, _, _ := GetEconomicStateInfo(1200)
	if info.LivePoolSize != 0 {
		t.Fatalf("expected livePool empty, got %d", info.LivePoolSize)
	}

	// 第三次：触发 refill，新 session 应被确定性分配
	id3, ok := sel.SelectForSession(route, "s3")
	if !ok {
		t.Fatal("select after refill failed")
	}
	if id3 != 10 && id3 != 20 {
		t.Errorf("unexpected endpoint %d", id3)
	}

	// 再次查询同一 session，应返回相同端点（session 粘性）
	id3Again, ok := sel.SelectForSession(route, "s3")
	if !ok {
		t.Fatal("select again failed")
	}
	if id3 != id3Again {
		t.Errorf("session affinity broken after refill: first=%d, second=%d", id3, id3Again)
	}
}

// ============================================================================
// v2.0.17：知识问答（KB）分支测试
// ============================================================================

// seedEndpointCacheForTest 注入 N 个启用 / 禁用源站到 agentCache.endpoints。
// 返回注入的 ID 列表（顺序与 enabledFlags 一致）。
func seedEndpointCacheForTest(ids []uint64, enabledFlags []bool) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.endpoints == nil {
		agentCache.endpoints = make(map[uint64]*TAgentDstEndPoint)
	}
	for i, id := range ids {
		status := 0
		if enabledFlags[i] {
			status = 1
		}
		agentCache.endpoints[id] = &TAgentDstEndPoint{
			ID:        id,
			Status:    status,
			ModelName: "test-model",
		}
	}
}

// clearEndpointCacheForTest 清理测试注入的源站（避免污染其它测试）
func clearEndpointCacheForTest(ids []uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	for _, id := range ids {
		delete(agentCache.endpoints, id)
	}
}

func TestIsAdvancedAgentToolName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"claude-cli exact", "claude-cli", true},
		{"claude-cli with version", "claude-cli/1.0.18", true},
		{"OpenAI/JS", "OpenAI/JS", true},
		{"OpenAI/JS with version", "OpenAI/JS 4.0.0", true},
		{"OpenAI/Python", "OpenAI/Python", true},
		{"opencode", "opencode", true},
		{"Kilo-Code", "Kilo-Code", true},
		{"kilo-code lowercase", "kilo-code", true},
		{"unknown UA", "SomeRandomAgent", false},
		{"empty", "", false},
		{"substring only", "myclaude-cli", false},
		{"RAG bot", "rag-bot/2.0", false},
		{"internal web UI", "Mozilla/5.0 (WebChat)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsAdvancedAgentToolName(tc.in)
			if got != tc.want {
				t.Errorf("IsAdvancedAgentToolName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsKnowledgeBaseRequest(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		tools     string
		agent     string
		want      bool
	}{
		{"empty session + empty tools + unknown UA", "", "", "SomeAgent/1.0", true},
		{"empty session + empty tools + RAG bot", "", "", "rag-bot", true},
		{"empty session + empty tools + web UI", "", "", "Mozilla/5.0", true},

		{"has session id", "sid-123", "", "SomeAgent/1.0", false},
		{"has anthropic tools", "", "Read,Write", "SomeAgent/1.0", false},
		{"has openai tools", "", "get_weather", "SomeAgent/1.0", false},
		{"claude-cli advanced", "", "", "claude-cli/1.0", false},
		{"OpenAI/JS advanced", "", "", "OpenAI/JS 4.0.0", false},
		{"Kilo-Code advanced", "", "", "Kilo-Code", false},
		{"opencode advanced", "", "", "opencode", false},
		{"session + advanced", "sid", "", "claude-cli", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsKnowledgeBaseRequest(tc.sessionID, tc.tools, tc.agent)
			if got != tc.want {
				t.Errorf("got=%v, want=%v", got, tc.want)
			}
		})
	}
}

func TestExtractRequestToolNamesForAlgorithm(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty body", "", ""},
		{"not JSON", "not a json", ""},
		{"anthropic tools", `{"tools":[{"name":"Read"},{"name":"Write"}]}`, "Read,Write"},
		{"openai function tools", `{"tools":[{"type":"function","function":{"name":"get_weather"}}]}`, "get_weather"},
		{"openai tool_calls fallback", `{"messages":[{"role":"assistant","tool_calls":[{"function":{"name":"lookup"}}]}]}`, "lookup"},
		{"plain chat (no tools)", `{"messages":[{"role":"user","content":"hi"}]}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractRequestToolNamesForAlgorithm([]byte(tc.body))
			if got != tc.want {
				t.Errorf("got=%q, want=%q", got, tc.want)
			}
		})
	}
}

func TestEconomicSelectForKBRequest_PicksAvailable(t *testing.T) {
	ResetEconomicRouteState(900)
	defer ResetEconomicRouteState(900)

	ids := []uint64{901, 902, 903, 904}
	seedEndpointCacheForTest(ids, []bool{true, true, true, true})
	defer clearEndpointCacheForTest(ids)

	route := makeTestRoute(900, ids)
	sel := &EconomicAlgorithmSelector{}

	// 多次调用：应总是返回 DstEndPointIDs 中的某个，且不消耗 livePool
	seen := make(map[uint64]bool)
	for i := 0; i < 20; i++ {
		id, ok := sel.SelectForKBRequest(route)
		if !ok {
			t.Fatalf("iteration %d: SelectForKBRequest returned false", i)
		}
		if !containsUint64(ids, id) {
			t.Errorf("iteration %d: returned id %d not in route config", i, id)
		}
		seen[id] = true
	}
	// 20 次随机应至少覆盖 2 个源站（极小概率只命中一个）
	if len(seen) < 2 {
		t.Errorf("expected at least 2 different endpoints over 20 random picks, got %d (seen=%v)", len(seen), seen)
	}

	// v2.0.75：KB 分支会初始化经济型状态（用于冷却源站检查），但不得消费 livePool
	if state, exists := economicStates[900]; exists {
		state.mu.Lock()
		poolLen := len(state.livePool)
		state.mu.Unlock()
		if poolLen != 0 {
			t.Errorf("KB branch should not consume livePool, got len=%d", poolLen)
		}
	}
}

func TestEconomicSelectForKBRequest_SkipsDisabled(t *testing.T) {
	ResetEconomicRouteState(910)
	defer ResetEconomicRouteState(910)

	// 912 禁用，911/913/914 启用
	ids := []uint64{911, 912, 913, 914}
	statuses := []int{1, 0, 1, 1}
	route := makeTestRouteWithStatuses(910, ids, statuses)
	sel := &EconomicAlgorithmSelector{}

	for i := 0; i < 30; i++ {
		id, ok := sel.SelectForKBRequest(route)
		if !ok {
			t.Fatalf("iteration %d: SelectForKBRequest returned false", i)
		}
		if id == 912 {
			t.Errorf("iteration %d: returned disabled endpoint 912", i)
		}
	}
}

func TestEconomicSelectForKBRequest_AllDisabled(t *testing.T) {
	ResetEconomicRouteState(920)
	defer ResetEconomicRouteState(920)

	ids := []uint64{921, 922}
	statuses := []int{0, 0}
	route := makeTestRouteWithStatuses(920, ids, statuses)
	sel := &EconomicAlgorithmSelector{}

	if id, ok := sel.SelectForKBRequest(route); ok {
		t.Errorf("expected (0, false) when all disabled, got (%d, %v)", id, ok)
	}
}

func TestEconomicSelectForKBRequest_MissingInCache(t *testing.T) {
	ResetEconomicRouteState(930)
	defer ResetEconomicRouteState(930)

	// 仅 931 启用，932/933 禁用（不再依赖缓存，只依赖 DstEndPointIDStatuses）
	ids := []uint64{931, 932, 933}
	statuses := []int{1, 0, 0}
	route := makeTestRouteWithStatuses(930, ids, statuses)
	sel := &EconomicAlgorithmSelector{}

	// 多次调用只能返回 931（其它两个状态为禁用）
	for i := 0; i < 10; i++ {
		id, ok := sel.SelectForKBRequest(route)
		if !ok || id != 931 {
			t.Errorf("iteration %d: expected 931 (only one enabled), got (%d, %v)", i, id, ok)
		}
	}
}

func TestEconomicSelectForKBRequest_NilRoute(t *testing.T) {
	sel := &EconomicAlgorithmSelector{}
	if id, ok := sel.SelectForKBRequest(nil); ok || id != 0 {
		t.Errorf("nil route: expected (0, false), got (%d, %v)", id, ok)
	}
}

// ============================================================================
// v2.0.20：合成 Session ID 缓存测试
// ============================================================================

func TestIsSyntheticSessionEligibleAgent(t *testing.T) {
	ResetSyntheticSessionCache()
	defer ResetSyntheticSessionCache()

	tests := []struct {
		name     string
		agent    string
		expected bool
	}{
		{"opencode exact", "opencode", true},
		{"opencode uppercase", "OpenCode", true},
		{"openai/python exact", "openai/python", true},
		{"openai/python uppercase", "OpenAI/Python", true},
		{"openai/python with version", "OpenAI/Python 1.0.0", true},
		// v2.0.75：这些 Agent 识别不到 session_id 时也会落入固定首源站，
		// 故纳入合成 session 名单（获得粘性 + TTL 轮换均衡）
		{"claude-cli eligible (v2.0.75)", "claude-cli", true},
		{"openai/js eligible (v2.0.75)", "openai/js", true},
		{"kilo-code eligible (v2.0.75)", "kilo-code", true},
		{"empty string", "", false},
		{"unknown agent", "some-other-agent", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsSyntheticSessionEligibleAgent(tc.agent)
			if got != tc.expected {
				t.Errorf("IsSyntheticSessionEligibleAgent(%q) = %v, want %v", tc.agent, got, tc.expected)
			}
		})
	}
}

func TestGetOrSynthesizeSessionID(t *testing.T) {
	ResetSyntheticSessionCache()
	defer ResetSyntheticSessionCache()

	// 首次调用：生成新 session_id
	id1, ok1 := GetOrSynthesizeSessionID("alice", "gpt-4")
	if !ok1 {
		t.Fatal("first call should succeed")
	}
	// v2.0.76 阶段BD：合成 session 带 self_generate_ 前缀 + 24 位 hex = 38 字符
	if !IsSyntheticSessionID(id1) {
		t.Errorf("session_id %q should have %q prefix", id1, SyntheticSessionIDPrefix)
	}
	if len(id1) != len(SyntheticSessionIDPrefix)+24 {
		t.Errorf("session_id length = %d, want %d", len(id1), len(SyntheticSessionIDPrefix)+24)
	}
	// 验证前缀之后是 hex 字符串
	for _, c := range id1[len(SyntheticSessionIDPrefix):] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("session_id contains non-hex char: %c", c)
			break
		}
	}

	// 第二次调用：同一 key 返回相同 session_id（缓存复用）
	id2, ok2 := GetOrSynthesizeSessionID("alice", "gpt-4")
	if !ok2 {
		t.Fatal("second call should succeed")
	}
	if id1 != id2 {
		t.Errorf("cache miss: id1=%s, id2=%s", id1, id2)
	}

	// 不同 key 返回不同 session_id
	id3, ok3 := GetOrSynthesizeSessionID("bob", "gpt-4")
	if !ok3 {
		t.Fatal("different key call should succeed")
	}
	if id1 == id3 {
		t.Errorf("different users should get different ids, both got %s", id1)
	}

	id4, ok4 := GetOrSynthesizeSessionID("alice", "claude-3")
	if !ok4 {
		t.Fatal("different model call should succeed")
	}
	if id1 == id4 {
		t.Errorf("different models should get different ids, both got %s", id1)
	}
}

func TestGetOrSynthesizeSessionID_EmptyParams(t *testing.T) {
	ResetSyntheticSessionCache()
	defer ResetSyntheticSessionCache()

	if _, ok := GetOrSynthesizeSessionID("", "gpt-4"); ok {
		t.Error("empty userName should return false")
	}
	if _, ok := GetOrSynthesizeSessionID("alice", ""); ok {
		t.Error("empty modelName should return false")
	}
	if _, ok := GetOrSynthesizeSessionID("", ""); ok {
		t.Error("both empty should return false")
	}
}

func TestGetOrSynthesizeSessionID_TTLExpiry(t *testing.T) {
	ResetSyntheticSessionCache()
	defer ResetSyntheticSessionCache()

	// 手动注入一个已过期的缓存条目
	syntheticSessionCacheMu.Lock()
	syntheticSessionCache["expired_user|model"] = &syntheticSessionEntry{
		SessionID: "old_session_id_0000000000",
		LastUsed:  time.Now().Add(-EconomicSyntheticSessionTTL - time.Minute),
	}
	syntheticSessionCacheMu.Unlock()

	// 过期后应生成新的 session_id
	id, ok := GetOrSynthesizeSessionID("expired_user", "model")
	if !ok {
		t.Fatal("should succeed after expiry")
	}
	if id == "old_session_id_0000000000" {
		t.Error("should not return expired session_id")
	}
	if !IsSyntheticSessionID(id) {
		t.Errorf("new session_id %q should have %q prefix", id, SyntheticSessionIDPrefix)
	}
	if len(id) != len(SyntheticSessionIDPrefix)+24 {
		t.Errorf("new session_id length = %d, want %d", len(id), len(SyntheticSessionIDPrefix)+24)
	}
}

// ============= v2.0.76 阶段BD：合成 session 前缀与配置化 TTL 测试 =============

// TestSyntheticSessionIDPrefixFormat 验证合成 session id 的前缀格式与总长度
func TestSyntheticSessionIDPrefixFormat(t *testing.T) {
	ResetSyntheticSessionCache()
	defer ResetSyntheticSessionCache()

	id, ok := GetOrSynthesizeSessionID("prefix_user", "prefix_model")
	if !ok {
		t.Fatal("synthesize should succeed")
	}
	if !strings.HasPrefix(id, SyntheticSessionIDPrefix) {
		t.Errorf("id = %q, want prefix %q", id, SyntheticSessionIDPrefix)
	}
	// self_generate_ (14) + 24 hex = 38
	if len(id) != 38 {
		t.Errorf("len(id) = %d, want 38", len(id))
	}
}

// TestIsSyntheticSessionID 验证合成 session 判断函数
func TestIsSyntheticSessionID(t *testing.T) {
	if !IsSyntheticSessionID("self_generate_abc123") {
		t.Error("self_generate_ prefixed id should be synthetic")
	}
	if !IsSyntheticSessionID(SyntheticSessionIDPrefix) {
		t.Error("bare prefix should be synthetic")
	}
	if IsSyntheticSessionID("") {
		t.Error("empty string should not be synthetic")
	}
	if IsSyntheticSessionID("144ca9ed-c216-40f2-87a7-cd9df1dc7f3c") {
		t.Error("real UUID session id should not be synthetic")
	}
	if IsSyntheticSessionID("unknown_session_id") {
		t.Error("unknown placeholder should not be synthetic")
	}
	if IsSyntheticSessionID("selfgenerate_abc") {
		t.Error("missing underscore variant should not be synthetic")
	}
}

// TestGetSyntheticSessionTTL_Config 验证 TTL 配置化：配置生效、未配置回退默认
func TestGetSyntheticSessionTTL_Config(t *testing.T) {
	// 备份全局配置，测试后恢复
	saved := config.G
	defer func() { config.G = saved }()

	// 场景1：配置 60 分钟 → TTL = 60min
	config.G = &config.LsmTokensServerConfig{AgentSessionIdleTimeoutMinutes: 60}
	if got := GetSyntheticSessionTTL(); got != 60*time.Minute {
		t.Errorf("TTL = %v, want 60m", got)
	}

	// 场景2：配置 1 分钟（下限）→ TTL = 1min
	config.G = &config.LsmTokensServerConfig{AgentSessionIdleTimeoutMinutes: 1}
	if got := GetSyntheticSessionTTL(); got != time.Minute {
		t.Errorf("TTL = %v, want 1m", got)
	}

	// 场景3：配置非法（0/负值）→ 回退默认 15min
	config.G = &config.LsmTokensServerConfig{AgentSessionIdleTimeoutMinutes: 0}
	if got := GetSyntheticSessionTTL(); got != EconomicSyntheticSessionTTL {
		t.Errorf("TTL = %v, want default %v", got, EconomicSyntheticSessionTTL)
	}
	config.G = &config.LsmTokensServerConfig{AgentSessionIdleTimeoutMinutes: -5}
	if got := GetSyntheticSessionTTL(); got != EconomicSyntheticSessionTTL {
		t.Errorf("TTL = %v, want default %v", got, EconomicSyntheticSessionTTL)
	}

	// 场景4：config.G 为 nil（单测/早期启动）→ 回退默认
	config.G = nil
	if got := GetSyntheticSessionTTL(); got != EconomicSyntheticSessionTTL {
		t.Errorf("TTL = %v, want default %v", got, EconomicSyntheticSessionTTL)
	}
}

func TestGetOrSynthesizeSessionID_Concurrent(t *testing.T) {
	ResetSyntheticSessionCache()
	defer ResetSyntheticSessionCache()

	// 并发调用同一 key，结果应一致
	const goroutines = 20
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			id, ok := GetOrSynthesizeSessionID("concurrent_user", "model-x")
			if !ok {
				t.Errorf("goroutine %d: call failed", idx)
				return
			}
			results[idx] = id
		}(i)
	}
	wg.Wait()

	// 所有结果应相同（缓存复用）
	first := results[0]
	if first == "" {
		t.Fatal("first result is empty")
	}
	for i := 1; i < goroutines; i++ {
		if results[i] != first {
			t.Errorf("goroutine %d got %s, want %s", i, results[i], first)
		}
	}
}

// ============================================================================
// v2.x: 禁用源站过滤测试 —— 验证禁用源站不会被路由到
// ============================================================================

// TestSyncEconomicRouteEndpoints_FiltersDisabled 验证 SyncEconomicRouteEndpoints
// 添加源站到 livePool 时过滤掉禁用状态（DstEndPointIDStatuses = 0）的源站
// 注意：此测试需要数据库和缓存初始化，使用 initTestEnv
func TestSyncEconomicRouteEndpoints_FiltersDisabled(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	ResetEconomicRouteState(2000)
	defer ResetEconomicRouteState(2000)

	// 创建路由 TAgentHttpAIRoute 并写入缓存
	route := &TAgentHttpAIRoute{
		ID:                      2000,
		UserID:                  1,
		UserModelID:             20000,
		ProtocolType:            1,
		DstEndPointIDList:       "2001,2002,2003",
		DstEndPointIDStatusList: "1,0,1",
		DstEndPointAlgorithmTypeList: "1,1,1",
		DstEndPointIDNumber:     3,
		AlgorithmStrategyType:   AlgorithmStrategyType_Economic,
	}
	addRouteToCache(route)

	// 调用 SyncEconomicRouteEndpoints
	SyncEconomicRouteEndpoints(2000, []uint64{2001, 2002, 2003})

	// 验证 livePool 不包含禁用的 2002
	info, _, _ := GetEconomicStateInfo(2000)
	for _, id := range info.LivePool {
		if id == 2002 {
			t.Errorf("livePool should not contain disabled endpoint 2002, got %v", info.LivePool)
		}
	}
	// 验证 livePool 只包含启用的 2001 和 2003
	if info.LivePoolSize != 2 {
		t.Errorf("expected livePool size 2 (only enabled), got %d (pool=%v)", info.LivePoolSize, info.LivePool)
	}
}

// TestSelectForSession_SkipsDisabledInLivePool 验证 SelectForSession
// 从 livePool 选择时跳过禁用源站
func TestSelectForSession_SkipsDisabledInLivePool(t *testing.T) {
	ResetEconomicRouteState(2100)
	defer ResetEconomicRouteState(2100)

	// 创建路由：3 个源站全部启用
	route := makeTestRoute(2100, []uint64{2101, 2102, 2103})
	sel := &EconomicAlgorithmSelector{}

	// 先填充 livePool
	id1, ok := sel.SelectForSession(route, "session-1")
	if !ok {
		t.Fatal("first SelectForSession failed")
	}

	// 现在修改路由状态，禁用刚才选中的源站
	for i, id := range route.DstEndPointIDs {
		if id == id1 {
			route.DstEndPointIDStatuses[i] = 0
		}
	}

	// 继续选择，不应再选到被禁用的源站
	for i := 0; i < 10; i++ {
		id, ok := sel.SelectForSession(route, fmt.Sprintf("session-%d", i+10))
		if !ok {
			// livePool 可能耗尽，重新填充
			routeCopy := *route
			routeCopy.DstEndPointIDStatuses = []int{1, 1, 1}
			route = &routeCopy
			continue
		}
		if id == id1 {
			t.Errorf("iteration %d: selected disabled endpoint %d", i, id1)
		}
	}
}

// TestSelectForSession_MappingToDisabledEndpoint 验证 session 映射到禁用源站时
// 清理映射并重新分配
func TestSelectForSession_MappingToDisabledEndpoint(t *testing.T) {
	ResetEconomicRouteState(2200)
	defer ResetEconomicRouteState(2200)

	// 创建路由：3 个源站全部启用
	route := makeTestRoute(2200, []uint64{2201, 2202, 2203})
	sel := &EconomicAlgorithmSelector{}

	// 建立 session 映射
	sessionID := "test-session"
	id1, ok := sel.SelectForSession(route, sessionID)
	if !ok {
		t.Fatal("first SelectForSession failed")
	}

	// 禁用该源站（路由内状态）
	for i, id := range route.DstEndPointIDs {
		if id == id1 {
			route.DstEndPointIDStatuses[i] = 0
		}
	}

	// 将路由加入缓存（供 GetCachedRouteByID 查找）
	addRouteToCache(&TAgentHttpAIRoute{
		ID:                    2200,
		UserModelID:           22000,
		DstEndPointIDList:     "2201,2202,2203",
		DstEndPointIDStatusList: formatStatusList(route.DstEndPointIDStatuses),
	})
	defer removeRouteFromCache(22000, 2200)

	// 再次选择，应该清理映射并分配到其他源站
	id2, ok := sel.SelectForSession(route, sessionID)
	if !ok {
		t.Fatal("second SelectForSession failed")
	}
	if id2 == id1 {
		t.Errorf("expected different endpoint after disabling, got same %d", id1)
	}
}

// formatStatusList 辅助函数：格式化状态切片为字符串（复用现有函数）
func formatStatusList(statuses []int) string {
	return FormatDstEndPointIDStatusList(statuses)
}

// ============================================================================
// v2.0.77 阶段BS：智能路由算法切换 Session 粘性兼容 —— 通配映射测试
//
// 场景：指定型 / 稳定型 → 经济型 切换时，SelectForSession 命中通配映射，
// 保持切换前正在使用的目标源站不被静默切换。
// ============================================================================

// TestSeedEconomicWildcardSession_BasicFlow
// 验证 SeedEconomicWildcardSession 把 currentEffective 写入通配映射
// 并从 livePool 中移除（源站可用 + 未冷却场景）。
func TestSeedEconomicWildcardSession_BasicFlow(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3000)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	// 注册到缓存（isEndpointEnabled 需要查缓存，缓存中没有该路由视为全部启用）
	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	route := makeTestRoute(routeID, []uint64{11, 22, 33})
	sel := &EconomicAlgorithmSelector{}

	// 预热：建一个 session 让 livePool 被消费
	if _, ok := sel.SelectForSession(route, "warmup-session"); !ok {
		t.Fatal("warmup SelectForSession failed")
	}

	// 模拟「稳定型 → 经济型」切换：通配预填到 DstEndPointIDs[0] = 11
	SeedEconomicWildcardSession(routeID, 11)

	// 1) sessionIndex 应包含通配键
	info, _, _ := GetEconomicStateInfo(routeID)
	if info == nil {
		t.Fatal("expected economic state to exist after seed")
	}
	state := getEconomicState(routeID)
	state.mu.Lock()
	wEntry, wOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if !wOK {
		t.Fatal("expected wildcard session entry to exist")
	}
	if wEntry.EndPointID != 11 {
		t.Errorf("expected wildcard endpoint=11, got %d", wEntry.EndPointID)
	}

	// 2) 通配源站应从 livePool 中被移除
	state.mu.Lock()
	for _, id := range state.livePool {
		if id == 11 {
			t.Errorf("expected endpoint 11 to be removed from livePool, but found in %v", state.livePool)
		}
	}
	state.mu.Unlock()
}

// TestSeedEconomicWildcardSession_DisabledFallback
// 验证：通配源站被禁用时，SeedEconomicWildcardSession 仍写入通配映射
// 但保留在 livePool（SelectForSession 会校验可用性自动 fallthrough）。
func TestSeedEconomicWildcardSession_DisabledFallback(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3100)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	// 源站本体禁用需注册到 endpoint 缓存
	addDstEndPointToCache(&TAgentDstEndPoint{
		ID:     11,
		Status: 0, // 禁用
	})
	defer func() {
		invalidateDstEndPointCache(11)
	}()

	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	route := makeTestRoute(routeID, []uint64{11, 22, 33})
	sel := &EconomicAlgorithmSelector{}

	// 模拟 UpdateAIRoute 调用顺序：先 SyncEconomicRouteEndpoints 填充 livePool，
	// 再 SeedEconomicWildcardSession 预填通配映射
	SyncEconomicRouteEndpoints(routeID, []uint64{11, 22, 33})
	SeedEconomicWildcardSession(routeID, 11)

	// 通配映射应存在
	state := getEconomicState(routeID)
	state.mu.Lock()
	_, wOK := state.sessionIndex[EconomicWildcardSessionID]
	contains11 := containsUint64(state.livePool, 11)
	state.mu.Unlock()
	if !wOK {
		t.Fatal("expected wildcard entry to exist even when endpoint disabled")
	}
	// 源站被禁用，应保留在 livePool（不消费）
	if !contains11 {
		t.Errorf("expected disabled endpoint 11 to remain in livePool, got %v", state.livePool)
	}

	// SelectForSession 命中通配分支时应校验失败，fallthrough 到哈希重选
	id, ok := sel.SelectForSession(route, "real-session-1")
	if !ok {
		t.Fatal("SelectForSession should succeed via fallback")
	}
	if id == 11 {
		t.Errorf("expected fallback to NOT pick disabled endpoint 11, got %d", id)
	}
}

// TestSelectForSession_WildcardSingleUse
// 验证：通配条目被命中一次后立即清掉，第二个不同 session 的请求不再命中通配，
// 走「新 session 哈希分配」。
func TestSelectForSession_WildcardSingleUse(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3200)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	route := makeTestRoute(routeID, []uint64{11, 22, 33})
	sel := &EconomicAlgorithmSelector{}

	// 预热
	_, _ = sel.SelectForSession(route, "warmup")

	// 切换：通配预填到 11
	SeedEconomicWildcardSession(routeID, 11)

	// 第一个 session：应命中通配 → 端到 11
	id1, ok := sel.SelectForSession(route, "session-1")
	if !ok {
		t.Fatal("first SelectForSession failed")
	}
	if id1 != 11 {
		t.Errorf("expected wildcard hit to endpoint 11, got %d", id1)
	}

	// 通配条目应已被清掉
	state := getEconomicState(routeID)
	state.mu.Lock()
	_, wStillOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if wStillOK {
		t.Errorf("expected wildcard entry to be cleared after single use, but still exists")
	}

	// 第二个 session：通配已清掉，走「新 session 哈希分配」
	id2, ok := sel.SelectForSession(route, "session-2")
	if !ok {
		t.Fatal("second SelectForSession failed")
	}
	// session-2 不应再命中 11（11 已从 livePool 移除，且 session-2 是新 session 走哈希）
	if id2 == 11 {
		t.Errorf("expected second session to NOT hit endpoint 11 (should pick from remaining livePool), got %d", id2)
	}

	// 第三个 session：与 session-1 相同的 session_id，应走真实映射粘性
	id3, ok := sel.SelectForSession(route, "session-1")
	if !ok {
		t.Fatal("third SelectForSession (same as first) failed")
	}
	if id3 != 11 {
		t.Errorf("expected session-1 to remain sticky to endpoint 11, got %d", id3)
	}
}

// TestSeedEconomicWildcardSession_LRUEviction
// 验证：通配条目受 LRU 上限约束（EconomicSessionQueueMaxSize = 50），
// 大量新 session 写入后通配条目可能被驱逐（设计预期：通配条目通常在切换后
// 第一次请求就被消费掉，不会长期驻留）。
func TestSeedEconomicWildcardSession_LRUEviction(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3300)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33,44,55",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	route := makeTestRoute(routeID, []uint64{11, 22, 33, 44, 55})
	sel := &EconomicAlgorithmSelector{}

	SeedEconomicWildcardSession(routeID, 11)

	// 连续写 EconomicSessionQueueMaxSize + 5 个新 session，触发 LRU 驱逐通配条目
	for i := 0; i < EconomicSessionQueueMaxSize+5; i++ {
		_, _ = sel.SelectForSession(route, fmt.Sprintf("session-%d", i))
	}

	// 通配条目应被 LRU 驱逐（被挤到队首）
	state := getEconomicState(routeID)
	state.mu.Lock()
	_, wStillOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if wStillOK {
		t.Errorf("expected wildcard entry to be evicted by LRU after %d sessions, but still exists",
			EconomicSessionQueueMaxSize+5)
	}
}

// TestSyncEconomicRouteEndpoints_RemovesWildcardEntry
// 验证：SyncEconomicRouteEndpoints 在 DstEndPointIDList 变化时
// 正确处理通配条目（与普通 session 条目同等对待）：
//   - 通配源站被 Web 删除：通配条目重新分配到剩余源站
//   - 整个 DstEndPointIDList 被清空：通配条目直接清理
func TestSyncEconomicRouteEndpoints_RemovesWildcardEntry(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3400)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	SeedEconomicWildcardSession(routeID, 11)

	// 场景 1: Web 删除通配源站 11，剩余 22,33 → 通配条目应被重分配到 22 或 33
	SyncEconomicRouteEndpoints(routeID, []uint64{22, 33})

	state := getEconomicState(routeID)
	state.mu.Lock()
	wEntry, wOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if !wOK {
		t.Fatal("expected wildcard entry to remain and be reassigned")
	}
	if wEntry.EndPointID == 11 {
		t.Errorf("expected wildcard endpoint to be reassigned away from 11, got %d", wEntry.EndPointID)
	}
	if wEntry.EndPointID != 22 && wEntry.EndPointID != 33 {
		t.Errorf("expected wildcard endpoint=22 or 33, got %d", wEntry.EndPointID)
	}

	// 场景 2: 清空整个 DstEndPointIDList → 通配条目应被清理
	SyncEconomicRouteEndpoints(routeID, []uint64{})
	state.mu.Lock()
	_, wStillOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if wStillOK {
		t.Errorf("expected wildcard entry to be cleared when DstEndPointIDList becomes empty")
	}
}

// TestUpdateAIRoute_StableToEconomic_SeedWildcard
// 端到端模拟：模拟 UpdateAIRoute 中「稳定型 → 经济型」的兼容分支调用 SeedEconomicWildcardSession
// 验证切换后第一次有 session 的请求命中原 DstEndPointIDs[0]。
//
// 注：本测试不调用 UpdateAIRoute（涉及数据库），仅复用其算法切换逻辑：
// 1) 调用 SeedEconomicWildcardSession(routeID, newEndpointIDs[0]) 模拟兼容分支
// 2) 调用 SelectForSession 验证通配命中
func TestUpdateAIRoute_StableToEconomic_SeedWildcard(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3500)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	route := makeTestRoute(routeID, []uint64{11, 22, 33})
	sel := &EconomicAlgorithmSelector{}

	// 1) 模拟「稳定型」正在服务：DstEndPointIDs[0] = 11 是当前生效源站
	currentEffective := route.DstEndPointIDs[0]
	if currentEffective != 11 {
		t.Fatalf("expected current effective=11, got %d", currentEffective)
	}

	// 2) 模拟 UpdateAIRoute 的兼容分支：调用 SeedEconomicWildcardSession
	SeedEconomicWildcardSession(routeID, currentEffective)

	// 3) 切换后第一次带 session 的请求：应命中 11（与切换前一致）
	id, ok := sel.SelectForSession(route, "ongoing-session")
	if !ok {
		t.Fatal("post-switch SelectForSession failed")
	}
	if id != 11 {
		t.Errorf("expected ongoing-session to keep using endpoint 11 after switch, got %d", id)
	}

	// 4) 同一个 session 的后续请求：仍粘性命中 11
	id2, ok := sel.SelectForSession(route, "ongoing-session")
	if !ok {
		t.Fatal("sticky follow-up SelectForSession failed")
	}
	if id2 != 11 {
		t.Errorf("expected ongoing-session to remain sticky to 11, got %d", id2)
	}
}

// TestSeedEconomicWildcardSession_ZeroEndpoint
// 边界场景：传入 currentEffective=0 时直接返回，不写入任何状态。
func TestSeedEconomicWildcardSession_ZeroEndpoint(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3600)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	SeedEconomicWildcardSession(routeID, 0)

	state := getEconomicState(routeID)
	state.mu.Lock()
	_, wOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if wOK {
		t.Errorf("expected no wildcard entry when currentEffective=0")
	}
}

// TestSelectForSession_WildcardNoRealHit
// 验证：通配命中不会破坏「新 session 路径」的语义。
// 当通配源站不可用时，SelectForSession 应跳过通配分支走哈希重选。
func TestSelectForSession_WildcardNoRealHit(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	routeID := uint64(3700)
	ResetEconomicRouteState(routeID)
	defer ResetEconomicRouteState(routeID)

	// 注册源站缓存：把 11 禁用
	addDstEndPointToCache(&TAgentDstEndPoint{
		ID:     11,
		Status: 0,
	})
	defer func() {
		invalidateDstEndPointCache(11)
	}()

	addRouteToCache(&TAgentHttpAIRoute{
		ID:                routeID,
		UserModelID:       routeID * 10,
		DstEndPointIDList: "11,22,33",
	})
	defer removeRouteFromCache(routeID*10, routeID)

	route := makeTestRoute(routeID, []uint64{11, 22, 33})
	sel := &EconomicAlgorithmSelector{}

	// 模拟 UpdateAIRoute 调用顺序：先 SyncEconomicRouteEndpoints 填充 livePool，再 Seed
	SyncEconomicRouteEndpoints(routeID, []uint64{11, 22, 33})
	SeedEconomicWildcardSession(routeID, 11)

	// 通配源站 11 不可用 → 应跳过通配分支 → 走「新 session 哈希分配」
	id, ok := sel.SelectForSession(route, "real-session")
	if !ok {
		t.Fatal("SelectForSession should succeed via fallback")
	}
	if id == 11 {
		t.Errorf("expected fallback to NOT pick disabled endpoint 11, got %d", id)
	}

	// 通配条目应保留（未被消费）
	state := getEconomicState(routeID)
	state.mu.Lock()
	_, wStillOK := state.sessionIndex[EconomicWildcardSessionID]
	state.mu.Unlock()
	if !wStillOK {
		t.Errorf("expected wildcard entry to remain when disabled (not consumed)")
	}
}
