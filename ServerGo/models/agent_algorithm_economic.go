package models

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/logger"
	"github.com/lishimeng/LsmTokensServer/recognizer"
	"hash/fnv"
	mathrand "math/rand"
	"sync"
	"time"
)

// ============================================================================
// 经济型算法（AlgorithmStrategyType_Economic = 3）
// ============================================================================
//
// 设计目标：Session 级别负载均衡，将不同会话分配到各源站；服务重启后
// 重新随机分配，保证各源站均衡使用，避免频繁重启导致 session 总是
// 集中到同一端点。
//
// 核心机制 —— 「实时目标源站列表（livePool）」消费语义 + session 确定性哈希：
//  1. 通过 Session 识别层（agent_algorithm_session_recognition.go）从请求 body
//     中解析 session_id，支持 Anthropic 和 OpenAI 协议。
//  2. 每个路由在内存里维护一份 livePool（实时源站列表），由路由配置
//     DstEndPointIDs 初始化或重新填充。
//  3. 新 session：
//     a) 若 livePool 非空，使用 session_id + route_id + 启动时间戳
//        的确定性哈希计算端点索引，保证同一 session 在当前服务生命周期内
//        总是分配到同一端点（session 粘性）。
//     b) 服务重启后（启动时间戳变化），同一 session 会重新随机分配，
//        保证各源站均衡使用。
//     c) 分配后从 livePool swap-remove 弹出。
//     d) livePool 空时按 DstEndPointIDs 重新排序填充。
//  4. 已有映射的 session 命中 sessionIndex；命中时校验 EndPointID 是否
//     仍在 DstEndPointIDs（不在则视为失效并清掉，走新 session 路径）。
//  5. Session 识别由独立层实现，供所有算法策略复用。
//
// 确定性分配：
//   - 使用 FNV-1a 哈希 session_id + route_id + 启动时间戳，计算确定性索引
//   - 同一 session 在当前服务生命周期内总是分配到同一端点
//   - 服务重启后（时间戳变），同一 session 重新随机分配，保证均衡
//   - 不同 session 在端点间均匀分布
//
// 同步 API：
//   - SyncEconomicRouteEndpoints(routeID, newEndpointIDs)
//     把 livePool / session 队列 / 失败计数与新源站列表对齐（增/删都行）。
//     调用方需先写 database.DB + 内存缓存再调用本函数。
//
// v2.0.6 重构：
//   - 用「实时源站列表消费」替代 round-robin pointer
//   - 增/删双向同步（add 端点 push 到 livePool，remove 端点清理 session 映射）
//   - OnEndpointFailure 不再直接调 RemoveEndpointFromAIRoute（避免锁内回调），
//     改为返回 (shouldRemove, removedID)，由 forwardWithRetry 在循环外同步触发
//
// v2.0.7 新增：
//   - session 确定性哈希分配，引入启动时间戳作为哈希种子
//   - 服务重启后同一 session 重新随机分配，保证源站均衡使用
//   - 避免服务频繁重启导致 session 总是落到同一端点
//
// v2.0.8 重构：
//   - session_id 识别提取到独立层（agent_algorithm_session_recognition.go），
//     按协议分别实现（agent_algorithm_openai_session_recognition.go /
//     agent_algorithm_anthropic_session_recognition.go），供所有算法复用。
//
// 内存模型：
//   - 经济型的所有状态存储在内存中，重启后重新初始化
//   - 源站移除操作同步更新 database.DB 和内存缓存
//
// ============================================================================

// economicStartTime 服务启动时间戳，用于哈希种子。
// 服务重启后该值变化，导致同一 session_id 的哈希结果重新随机，
// 从而保证各源站均衡使用。
var economicStartTime = time.Now().UnixNano()

const (
	// EconomicSessionQueueMaxSize 每个路由最多保留的 session 映射数
	// 超出时从队首（最旧）驱逐，实现 LRU 语义
	EconomicSessionQueueMaxSize = 50

	// EconomicEndpointMaxConsecutiveFailures 单个源站最大连续失败次数
	// 达到后触发源站冷却摘除（v2.0.75 起为内存级冷却，不再持久化写库删除）
	EconomicEndpointMaxConsecutiveFailures = 3

	// EconomicEndpointCooldownDuration 源站连续失败达阈值后的内存冷却时长。
	// 冷却期间该源站被从 livePool 摘除（不参与新 session 分配），
	// 到期后自动回归 livePool 恢复参与负载均衡；
	// 路由配置（DstEndPointIDList）不受影响，源站的最终去留仍由管理员在 Web 端决定。
	EconomicEndpointCooldownDuration = 10 * time.Minute
)

// hashSessionToEndpoint 使用 FNV-1a 哈希将 session_id 确定性映射到 livePool 中的索引。
// 输入包含 session_id + route_id + 启动时间戳，保证：
//  1. 同一 session 在当前服务生命周期内总是分配到同一端点（session 粘性）
//  2. 服务重启后（时间戳变），同一 session 重新随机分配，保证均衡
//  3. 不同 session 在端点间均匀分布
func hashSessionToEndpoint(sessionID string, routeID uint64, livePool []uint64) int {
	h := fnv.New64a()
	// 写入 session_id
	h.Write([]byte(sessionID))
	// 写入 route_id（大端序，保证跨平台一致）
	binary.Write(h, binary.BigEndian, routeID)
	// 写入启动时间戳（服务重启后变化，保证重新随机）
	binary.Write(h, binary.BigEndian, economicStartTime)
	// 写入 livePool 内容（保证同一 livePool 状态下分配一致）
	for _, id := range livePool {
		binary.Write(h, binary.BigEndian, id)
	}
	hash := h.Sum64()
	return int(hash % uint64(len(livePool)))
}

// sessionMapEntry 单条 session→endpoint 映射
type sessionMapEntry struct {
	SessionID  string
	EndPointID uint64
}

// economicRouteState 单个路由的经济型算法状态
type economicRouteState struct {
	mu sync.Mutex

	// livePool 实时目标源站列表（消费视图）。
	// - 新 session 分配时通过确定性哈希取索引并从切片中弹出（swap-remove）；
	// - 空时按 route.DstEndPointIDs 重新排序填充；
	// - Web 增/删源站时按 add/remove 同步；
	// - route.DstEndPointIDs 才是 source-of-truth，livePool 只是消费视图。
	livePool []uint64

	// LRU 队列（队头 = 最旧，队尾 = 最新）
	sessionQueue []*sessionMapEntry
	// sessionID → 队列条目的快速索引
	sessionIndex map[string]*sessionMapEntry

	// 按源站 ID 统计连续失败次数（成功时清零）
	endpointFailureCount map[uint64]int

	// knownEndpoints 记录「曾被分配或曾出现在路由配置中的端点 ID」，
	// 用于 SyncEconomicRouteEndpoints 在 livePool 为空时仍能正确识别被 Web 移除的源站。
	knownEndpoints map[uint64]bool

	// cooldownEndpoints 记录因连续失败被临时摘除的源站及其冷却截止时间。
	// 到期后由 recoverCooldownEndpointsLocked 自动回归 livePool。
	cooldownEndpoints map[uint64]time.Time
}

// economicStates 按 routeID 维护的经济型算法状态表
var (
	economicStates   = make(map[uint64]*economicRouteState)
	economicStatesMu sync.RWMutex
)

// getEconomicState 获取或初始化指定路由的经济型状态（double-check locking）
func getEconomicState(routeID uint64) *economicRouteState {
	// 快速读路径
	economicStatesMu.RLock()
	if s, ok := economicStates[routeID]; ok {
		economicStatesMu.RUnlock()
		return s
	}
	economicStatesMu.RUnlock()

	// 慢速写路径
	economicStatesMu.Lock()
	defer economicStatesMu.Unlock()
	// double-check
	if s, ok := economicStates[routeID]; ok {
		return s
	}
	s := &economicRouteState{
		sessionIndex:         make(map[string]*sessionMapEntry),
		endpointFailureCount: make(map[uint64]int),
		knownEndpoints:       make(map[uint64]bool),
		cooldownEndpoints:    make(map[uint64]time.Time),
	}
	economicStates[routeID] = s
	return s
}

// containsUint64 判断切片中是否包含指定 ID
func containsUint64(s []uint64, target uint64) bool {
	for _, v := range s {
		if v == target {
			return true
		}
	}
	return false
}

// removeFromLivePool 从 livePool 中删除指定 ID（O(n) 扫描，源站数 < 10）
func removeFromLivePool(pool []uint64, target uint64) []uint64 {
	for i, v := range pool {
		if v == target {
			// swap-remove 保持 O(1)
			pool[i] = pool[len(pool)-1]
			return pool[:len(pool)-1]
		}
	}
	return pool
}

// sortedCopy 返回 src 的拷贝并按端点 ID 升序排序
// 使用确定性排序，保证 livePool 的初始顺序一致，
// 使哈希计算在相同输入下产生相同结果。
func sortedCopy(src []uint64) []uint64 {
	out := make([]uint64, len(src))
	copy(out, src)
	// 按端点 ID 升序排序（简单冒泡，源站数 < 10）
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i] > out[j] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// EconomicAlgorithmSelector 经济型算法选择器
type EconomicAlgorithmSelector struct{}

// Select 经济型算法的默认 Select（不含 sessionID），退化为第一个可用源站（不消费 livePool）
// 设计要点：兜底路径不应污染实时列表状态，调用方应优先通过 SelectForSession 传 sessionID。
func (s *EconomicAlgorithmSelector) Select(route *CachedAIRoute) (uint64, bool) {
	if route == nil || len(route.DstEndPointIDs) == 0 {
		return 0, false
	}
	// v2.0.75：兜底路径跳过冷却中的源站（连续失败被摘除的源站不再承接无 session 请求）
	for i, id := range route.DstEndPointIDs {
		status := 1
		if i < len(route.DstEndPointIDStatuses) {
			status = route.DstEndPointIDStatuses[i]
		}
		if status == 1 && !s.IsEndpointCooling(route.ID, id) {
			return id, true
		}
	}
	return 0, false
}

// SelectForSession 核心选择逻辑：按 session_id 路由到固定源站（livePool 消费语义）
// 流程：
//  1. 命中 sessionIndex 且 EndPointID 仍属于路由配置且状态为启用 → 直接返回（保持 session 粘性）
//  2. 命中但 EndPointID 已被 Web 删或状态为禁用 → 清理旧映射 + 从 livePool 防御性删除
//  3. 新 session：livePool 空时按可用源站（状态为1）排序填充；通过确定性哈希取索引并弹出
//  4. 写 sessionIndex/sessionQueue，超出 LRU 上限驱逐队首
func (s *EconomicAlgorithmSelector) SelectForSession(route *CachedAIRoute, sessionID string) (uint64, bool) {
	if route == nil || len(route.DstEndPointIDs) == 0 {
		return 0, false
	}
	if sessionID == "" {
		return s.Select(route)
	}

	state := getEconomicState(route.ID)
	state.mu.Lock()
	defer state.mu.Unlock()

	// 先回收冷却期已结束的源站（v2.0.75：冷却自动恢复，保持池满载参与负载均衡）
	recoverCooldownEndpointsLocked(state, route)

	// 命中已有映射
	if entry, ok := state.sessionIndex[sessionID]; ok {
		// 验证映射的源站仍在路由配置中且状态为启用
		idx := -1
		for i, id := range route.DstEndPointIDs {
			if id == entry.EndPointID {
				idx = i
				break
			}
		}
		if idx != -1 {
			status := 1
			if idx < len(route.DstEndPointIDStatuses) {
				status = route.DstEndPointIDStatuses[idx]
			}
			if status == 1 {
				// v2.x: 同时检查源站本体状态（TAgentDstEndPoint.Status）
				if ep, epOK := GetCachedDstEndPointByID(entry.EndPointID); epOK && ep.Status == 0 {
					// 源站本体被禁用：清理映射走重新分配
					logger.Printf("[ECONOMIC] Route %d: session %s mapped to disabled endpoint %d (endpoint status=0), clearing mapping",
						route.ID, sessionID, entry.EndPointID)
				} else if deadline, cooling := state.cooldownEndpoints[entry.EndPointID]; !cooling || time.Now().After(deadline) {
					// 粘性源站正处于冷却期（连续失败被摘除）：清理映射走重新分配
					RecordSelection(AlgorithmStrategyType_Economic)
					return entry.EndPointID, true
				}
			}
		}
		// 源站已被 Web 移除或状态为禁用：清理旧 session 映射 + 从 livePool 防御性删除
		// 然后强制清空 livePool，让下面的 fill 路径按新路由配置重新填充
		delete(state.sessionIndex, sessionID)
		for i, e := range state.sessionQueue {
			if e.SessionID == sessionID {
				state.sessionQueue = append(state.sessionQueue[:i], state.sessionQueue[i+1:]...)
				break
			}
		}
		state.livePool = nil
	}

	// 新 session 分配
	// livePool 空：按可用源站（状态为1）确定性排序填充（保证初始顺序一致）
	if len(state.livePool) == 0 {
		availableIDs := make([]uint64, 0, len(route.DstEndPointIDs))
		for i, id := range route.DstEndPointIDs {
			status := 1
			if i < len(route.DstEndPointIDStatuses) {
				status = route.DstEndPointIDStatuses[i]
			}
			if status == 1 && !isEndpointCoolingLocked(state, id) {
				availableIDs = append(availableIDs, id)
			}
		}
		state.livePool = sortedCopy(availableIDs)
		// 把当前路由配置加入 knownEndpoints
		for _, id := range route.DstEndPointIDs {
			state.knownEndpoints[id] = true
		}
	}

	if len(state.livePool) == 0 {
		return 0, false
	}

	// 使用确定性哈希计算索引，保证同一 session 在当前服务生命周期内总是分配到同一端点
	idx := hashSessionToEndpoint(sessionID, route.ID, state.livePool)
	endpointID := state.livePool[idx]

	// v2.x: 如果选中的源站被禁用（路由内状态或源站本体状态），从 livePool 移除并重新选择
	if !isEndpointEnabled(route, endpointID) {
		logger.Printf("[ECONOMIC] Route %d: hash-selected endpoint %d is disabled, removing from livePool and reselecting",
			route.ID, endpointID)
		state.livePool = removeFromLivePool(state.livePool, endpointID)
		if len(state.livePool) == 0 {
			return 0, false
		}
		idx = hashSessionToEndpoint(sessionID, route.ID, state.livePool)
		endpointID = state.livePool[idx]
		// 再次检查（防御性）：如果仍然禁用，直接返回失败
		if !isEndpointEnabled(route, endpointID) {
			return 0, false
		}
	}

	// swap-remove O(1) 弹出：用最后一个元素覆盖被取走的元素
	state.livePool[idx] = state.livePool[len(state.livePool)-1]
	state.livePool = state.livePool[:len(state.livePool)-1]

	// 写入 session 队列
	entry := &sessionMapEntry{SessionID: sessionID, EndPointID: endpointID}
	state.sessionQueue = append(state.sessionQueue, entry)
	state.sessionIndex[sessionID] = entry

	// LRU 驱逐
	for len(state.sessionQueue) > EconomicSessionQueueMaxSize {
		evicted := state.sessionQueue[0]
		state.sessionQueue = state.sessionQueue[1:]
		delete(state.sessionIndex, evicted.SessionID)
	}

	RecordSelection(AlgorithmStrategyType_Economic)
	return endpointID, true
}

// InvalidateSessionMapping 清除指定 session 的粘性映射（v2.0.74）。
// 用于 forwardWithRetry 在同一请求内重试换源：某源站触发故障转移后，
// 清掉 session→endpoint 映射，下一轮 SelectForSession 从 livePool 重新分配，
// 避免重试循环反复打在同一源站。
// 不动 livePool / 失败计数，不影响其它请求与其它 session 的粘性。
func (s *EconomicAlgorithmSelector) InvalidateSessionMapping(routeID uint64, sessionID string) {
	if sessionID == "" {
		return
	}
	state := getEconomicState(routeID)
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.sessionIndex[sessionID]; !ok {
		return
	}
	delete(state.sessionIndex, sessionID)
	for i, e := range state.sessionQueue {
		if e.SessionID == sessionID {
			state.sessionQueue = append(state.sessionQueue[:i], state.sessionQueue[i+1:]...)
			break
		}
	}
}

// OnRequestSuccess 请求成功：清零该路由的连续失败计数
func (s *EconomicAlgorithmSelector) OnRequestSuccess(routeID uint64) {
	state := getEconomicState(routeID)
	state.mu.Lock()
	for k := range state.endpointFailureCount {
		state.endpointFailureCount[k] = 0
	}
	state.mu.Unlock()
}

// OnRequestFailure 请求失败：记录日志（不触发源站移除，源站移除由 OnEndpointFailure 处理）
func (s *EconomicAlgorithmSelector) OnRequestFailure(routeID uint64, route *CachedAIRoute) {
	// 经济型算法的失败处理通过 OnEndpointFailure 按源站粒度跟踪
	// 此方法保留用于兼容 forwardWithRetry 中的通用调用
}

// OnEndpointFailure 指定源站请求失败：递增该源站的连续失败计数
// 达到阈值时返回 (shouldCooldown=true, removedID)，由调用方在循环外调
// CooldownEndpoint 完成「内存级冷却摘除」（v2.0.75 起不再写库删除源站，
// 冷却到期后源站自动回归 livePool 恢复负载均衡，路由配置保持完整）。
// 不在算法层直接调 database.DB 函数，避免算法层与 database.DB 层互斥锁交叉持有。
func (s *EconomicAlgorithmSelector) OnEndpointFailure(routeID uint64, endpointID uint64) (shouldCooldown bool, removedID uint64) {
	state := getEconomicState(routeID)
	state.mu.Lock()
	state.endpointFailureCount[endpointID]++
	count := state.endpointFailureCount[endpointID]
	state.mu.Unlock()

	logger.Printf("[ECONOMIC] Route %d: endpoint %d failure count=%d", routeID, endpointID, count)

	if count < EconomicEndpointMaxConsecutiveFailures {
		return false, 0
	}

	logger.Printf("[ECONOMIC] Route %d: endpoint %d reached %d consecutive failures, will be cooled down for %s", routeID, endpointID, count, EconomicEndpointCooldownDuration)
	return true, endpointID
}

// EndpointCooldownHook 经济型源站冷却事件回调钩子类型。
// 参数：routeID 路由ID，endpointID 被冷却的源站ID，duration 冷却时长。
// 钩子函数应快速返回，避免阻塞代理转发热路径。
type EndpointCooldownHook func(routeID uint64, endpointID uint64, duration time.Duration)

// onEndpointCooldownHook 全局冷却事件回调钩子（可选，nil 表示不触发）
var onEndpointCooldownHook struct {
	mu   sync.RWMutex
	hook EndpointCooldownHook
}

// SetEndpointCooldownHook 设置经济型源站冷却事件回调钩子（阶段 7.6 新增）。
// 用于外部订阅冷却事件（如日志记录、指标上报、通知推送）。
// 传入 nil 表示清除钩子。
func SetEndpointCooldownHook(hook EndpointCooldownHook) {
	onEndpointCooldownHook.mu.Lock()
	defer onEndpointCooldownHook.mu.Unlock()
	onEndpointCooldownHook.hook = hook
}

// triggerEndpointCooldownHook 触发冷却事件回调（内部使用，非阻塞）
func triggerEndpointCooldownHook(routeID uint64, endpointID uint64, duration time.Duration) {
	onEndpointCooldownHook.mu.RLock()
	hook := onEndpointCooldownHook.hook
	onEndpointCooldownHook.mu.RUnlock()
	if hook != nil {
		// 异步触发，避免阻塞代理转发热路径
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Printf("[ECONOMIC] cooldown hook panic: %v", r)
				}
			}()
			hook(routeID, endpointID, duration)
		}()
	}
}

// CooldownEndpoint 把指定源站从 livePool 摘除并进入冷却期（默认 EconomicEndpointCooldownDuration）。
// 冷却为纯内存状态：不写 database.DB、不改路由配置，到期后自动回归 livePool。
// 同时清零该源站的失败计数，避免恢复后因历史计数被立即再次摘除。
func (s *EconomicAlgorithmSelector) CooldownEndpoint(routeID uint64, endpointID uint64) {
	s.cooldownEndpointForDuration(routeID, endpointID, EconomicEndpointCooldownDuration)
	RecordCooldown()
	triggerEndpointCooldownHook(routeID, endpointID, EconomicEndpointCooldownDuration)
}

// cooldownEndpointForDuration CooldownEndpoint 的可指定时长版本（供测试使用）。
func (s *EconomicAlgorithmSelector) cooldownEndpointForDuration(routeID uint64, endpointID uint64, duration time.Duration) {
	state := getEconomicState(routeID)
	state.mu.Lock()
	defer state.mu.Unlock()

	state.livePool = removeFromLivePool(state.livePool, endpointID)
	state.cooldownEndpoints[endpointID] = time.Now().Add(duration)
	delete(state.endpointFailureCount, endpointID)

	logger.Printf("[ECONOMIC] Route %d: endpoint %d cooled down for %s (in-memory only, route config unchanged)", routeID, endpointID, duration)
}

// recoverCooldownEndpointsLocked 把冷却期已结束的源站回归 livePool（调用方需持有 state.mu）。
// 回归时去重，并校验该源站仍在路由配置的 DstEndPointIDs 中（被 Web 移除的不回归）。
// isEndpointCoolingLocked 判断源站是否处于冷却期（调用方需持有 state.mu）
func isEndpointCoolingLocked(state *economicRouteState, endpointID uint64) bool {
	deadline, ok := state.cooldownEndpoints[endpointID]
	return ok && time.Now().Before(deadline)
}

func recoverCooldownEndpointsLocked(state *economicRouteState, route *CachedAIRoute) {
	if len(state.cooldownEndpoints) == 0 {
		return
	}
	now := time.Now()
	for endpointID, deadline := range state.cooldownEndpoints {
		if now.Before(deadline) {
			continue
		}
		delete(state.cooldownEndpoints, endpointID)
		if route == nil || !containsUint64(route.DstEndPointIDs, endpointID) {
			continue
		}
		if containsUint64(state.livePool, endpointID) {
			continue
		}
		state.livePool = append(state.livePool, endpointID)
		logger.Printf("[ECONOMIC] Route %d: endpoint %d cooldown expired, back to live pool", route.ID, endpointID)
	}
}

// IsEndpointCooling 判断指定源站当前是否处于冷却期（供测试与观测使用）。
func (s *EconomicAlgorithmSelector) IsEndpointCooling(routeID uint64, endpointID uint64) bool {
	state := getEconomicState(routeID)
	state.mu.Lock()
	defer state.mu.Unlock()
	return isEndpointCoolingLocked(state, endpointID)
}

// SyncEconomicRouteEndpoints 把路由的 livePool + session 队列 + 失败计数与新源站列表对齐
// 用于 Web 编辑路由（增/删源站）以及 OnEndpointFailure 触发的源站移除。
// 调用方需保证：
//  1. route.DstEndPointIDs 与 newEndpointIDs 一致
//  2. database.DB 与 agentCache.routes 已先更新
//  3. 不要在 agentAIRouteMutex 内调用（避免锁内回调）
//
// 行为：
//   - 新增 ID：append 到 livePool（去重），加入 knownEndpoints
//   - 移除 ID：从 livePool + knownEndpoints 中删除；session 队列中映射到这些 ID 的条目
//   - 若 newEndpointIDs 非空：随机重分配到 newEndpointIDs
//   - 若为空：直接清掉
//   - 清理被移除 ID 的 endpointFailureCount
//   - v2.x: 添加源站到 livePool 时过滤禁用状态（DstEndPointIDStatuses = 0 或 TAgentDstEndPoint.Status = 0）
func SyncEconomicRouteEndpoints(routeID uint64, newEndpointIDs []uint64) {
	state := getEconomicState(routeID)
	state.mu.Lock()
	defer state.mu.Unlock()

	// 获取路由配置，用于检查源站禁用状态
	cachedRoute, _ := GetCachedRouteByID(routeID)

	newSet := make(map[uint64]bool, len(newEndpointIDs))
	for _, id := range newEndpointIDs {
		newSet[id] = true
		state.knownEndpoints[id] = true
	}

	// 1. 计算 removed 集合：基于 knownEndpoints 而非 livePool。
	//    livePool 在消费后可能为空（被 swap-remove 清空或 refill 跳过），
	//    但 knownEndpoints 记录了「曾被分配或曾在配置中的端点」，是判定
	//    「Web 实际移除了哪些端点」的正确 source-of-truth。
	var removed []uint64
	for id := range state.knownEndpoints {
		if !newSet[id] {
			removed = append(removed, id)
		}
	}
	removedSet := make(map[uint64]bool, len(removed))
	for _, id := range removed {
		removedSet[id] = true
		delete(state.knownEndpoints, id)
	}
	// 冷却表也按新列表清理：不在新列表中的源站（即便从未进过 knownEndpoints）一并清掉冷却
	for id := range state.cooldownEndpoints {
		if !newSet[id] {
			removedSet[id] = true
			delete(state.cooldownEndpoints, id)
		}
	}

	// 2. 从 livePool 与冷却表移除被删 ID
	for _, id := range removed {
		state.livePool = removeFromLivePool(state.livePool, id)
		delete(state.cooldownEndpoints, id)
	}

	// 3. 统计 added：在新源站列表中但不在 livePool 的 ID
	liveSet := make(map[uint64]bool, len(state.livePool))
	for _, id := range state.livePool {
		liveSet[id] = true
	}
	var added []uint64
	for _, id := range newEndpointIDs {
		// 冷却中的源站不回归 livePool（到期后由 recoverCooldownEndpointsLocked 恢复）
		// v2.x: 禁用的源站不加入 livePool（路由内状态 DstEndPointIDStatuses = 0 或源站本体 Status = 0）
		if !liveSet[id] && !isEndpointCoolingLocked(state, id) && isEndpointEnabled(cachedRoute, id) {
			added = append(added, id)
		}
	}
	// 追加新增到 livePool
	state.livePool = append(state.livePool, added...)

	// 4. 处理 session 队列：映射到被删 ID 的条目重新分配
	//    newEndpointIDs 为空（路由被清空）时直接清掉这些条目。
	var reassigned int
	if len(newEndpointIDs) == 0 {
		// 路由被清空：移除所有映射到被删 ID 的 session 条目
		for _, entry := range state.sessionQueue {
			if !removedSet[entry.EndPointID] {
				continue
			}
			delete(state.sessionIndex, entry.SessionID)
		}
		var newQueue []*sessionMapEntry
		for _, entry := range state.sessionQueue {
			if _, ok := state.sessionIndex[entry.SessionID]; !ok {
				continue
			}
			newQueue = append(newQueue, entry)
		}
		state.sessionQueue = newQueue
	} else {
		for _, entry := range state.sessionQueue {
			if !removedSet[entry.EndPointID] {
				continue
			}
			entry.EndPointID = newEndpointIDs[mathrand.Intn(len(newEndpointIDs))]
			reassigned++
		}
	}

	// 5. 清理被移除端点的失败计数
	for _, id := range removed {
		delete(state.endpointFailureCount, id)
	}

	logger.Printf("[ECONOMIC] Route %d sync: added=%d removed=%d reassignedSessions=%d livePoolSize=%d",
		routeID, len(added), len(removed), reassigned, len(state.livePool))
}

// isEndpointEnabled 检查源站是否启用（路由内状态 + 源站本体状态）
// 用于 SyncEconomicRouteEndpoints 和 SelectForSession 过滤禁用源站
// 返回 true 当且仅当：
//   - 路由内状态 DstEndPointIDStatuses 为 1（启用）或状态列表缺失（默认启用）
//   - 源站本体 TAgentDstEndPoint.Status 为 1（启用）
func isEndpointEnabled(cachedRoute *CachedAIRoute, endpointID uint64) bool {
	// 检查路由内状态（DstEndPointIDStatuses）
	if cachedRoute != nil {
		for i, id := range cachedRoute.DstEndPointIDs {
			if id == endpointID {
				if i < len(cachedRoute.DstEndPointIDStatuses) {
					if cachedRoute.DstEndPointIDStatuses[i] == 0 {
						return false // 路由内状态为禁用
					}
				}
				break
			}
		}
	}
	// 检查源站本体状态（TAgentDstEndPoint.Status）
	if ep, ok := GetCachedDstEndPointByID(endpointID); ok {
		if ep.Status == 0 {
			return false // 源站本体状态为禁用
		}
	}
	return true
}

// ResetEconomicRouteState 重置指定路由的经济型算法状态
// 路由被删除 / 算法类型切到非经济型时调用
func ResetEconomicRouteState(routeID uint64) {
	economicStatesMu.Lock()
	delete(economicStates, routeID)
	economicStatesMu.Unlock()
}

// EconomicStateInfo 经济型算法状态信息（供测试和监控使用）
type EconomicStateInfo struct {
	RouteID      uint64
	LivePool     []uint64
	LivePoolSize int
	SessionCount int
}

// GetEconomicStateInfo 获取指定路由的经济型算法状态信息
// 返回 livePool 的快照（仅用于测试和监控）
func GetEconomicStateInfo(routeID uint64) (*EconomicStateInfo, int, error) {
	economicStatesMu.RLock()
	state, exists := economicStates[routeID]
	economicStatesMu.RUnlock()
	if !exists {
		return nil, 0, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	poolSnap := make([]uint64, len(state.livePool))
	copy(poolSnap, state.livePool)
	info := &EconomicStateInfo{
		RouteID:      routeID,
		LivePool:     poolSnap,
		LivePoolSize: len(state.livePool),
		SessionCount: len(state.sessionQueue),
	}
	return info, len(state.sessionQueue), nil
}

// ============================================================================
// v2.0.17：经济型算法 —— 知识问答分支（KB 路径）
// ============================================================================
//
// 触发条件（全部满足）：
//   1. session_id 未识别（RecognizeSessionID 返回空）
//   2. 请求 body 中未识别到 Anthropic Tool Call / OpenAI Function Call
//      （即 extractAnthropicToolNames + extractOpenAIToolNames 均为空）
//   3. User-Agent 识别出的 Agent Name 不在「高阶 Agent」白名单内
//      （白名单：claude-cli / OpenAI/JS / OpenAI/Python / opencode / Kilo-Code）
//
// 设计目标：很多「知识问答系统」（RAG、内部 AI 助手页面等）发送纯对话请求，
// 既不带 session_id 也不带 tools。如果这些请求仍走 livePool 消费语义，会污染
// 经济型的 session 粘性分布。改为从 route.DstEndPointIDs 中随机挑选一个
// 可用源站，实现更好的负载均衡。
//
// 注意：
//   - 本函数不消费 livePool，也不写 sessionIndex/sessionQueue
//   - 仅做一次随机挑选；连续失败回退仍由 forwardWithRetry 现有循环处理
//   - 源站被禁用（Status=0）或在缓存中查不到时跳过
//
// ============================================================================

// EconomicAdvancedAgentWhiteList v2.0.17：经济型算法识别到的 Agent Name 属于该集合时，
// 请求视作「高阶 Agent 工具请求」，不走知识问答分支。
//
// 大小写不敏感；命中规则：完全相等或前缀匹配 + 路径分隔符（用于识别 "claude-cli/1.x" 这类）。
//
// v2.0.73 扩展：新增 codex-cli / pi / hermes / aider / continue / cline /
// windsurf / cursor / copilot / claude-code 等常见 AI Agent 工具。
var EconomicAdvancedAgentWhiteList = map[string]bool{
	"claude-cli":    true,
	"claude-code":   true, // v2.0.73：Claude Code 新版 UA
	"openai/js":     true,
	"openai/python": true,
	"opencode":      true,
	"kilo-code":     true,
	"codex-cli":     true, // v2.0.73：OpenAI Codex CLI
	"pi":            true, // v2.0.73：Pi AI
	"hermes":        true, // v2.0.73：Hermes CLI
	"aider":         true, // v2.0.73：Aider AI pair programming
	"continue":      true, // v2.0.73：Continue IDE extension
	"cline":         true, // v2.0.73：Cline VS Code extension
	"windsurf":      true, // v2.0.73：Windsurf IDE (Codeium)
	"cursor":        true, // v2.0.73：Cursor IDE
	"copilot":       true, // v2.0.73：GitHub Copilot
	// v2.0.75 扩展：补充实际线上观测到的高阶 Agent UA，避免落入知识问答误判
	"atomcode":    true, // AtomCode
	"asyncopenai": true, // AsyncOpenAI SDK（openai-python 并发变体）
	"amp":         true, // Amp
	"rovo":        true, // Rovo
	"longcat":     true, // LongCat
	"grok-build":  true, // Grok
	"openclaw":    true, // OpenClaw
}

// IsAdvancedAgentToolName 判断 Agent 名称是否属于「高阶 Agent 工具白名单」。
// 比较时把 name 与白名单 key 都转为小写后做严格相等；为了兼容 "OpenAI/JS 4.0.0" 这类
// 后续带版本号的 UA，先按空白/换行截取再比较。
//
// 注意：斜杠 '/' 不作为截断符 —— "claude-cli/1.0.18" 应被识别为 "claude-cli"。
func IsAdvancedAgentToolName(name string) bool {
	if name == "" {
		return false
	}
	// 仅按空白/换行截断，斜杠保留以便识别 "claude-cli/1.0.18" 形式
	trimmed := name
	for i, c := range trimmed {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			trimmed = trimmed[:i]
			break
		}
	}
	lower := toLowerASCII(trimmed)
	if EconomicAdvancedAgentWhiteList[lower] {
		return true
	}
	// 兼容 "/version" 后缀：如 "claude-cli/1.0.18" 截到第一个 '/' 再比较
	if idx := indexRune(lower, '/'); idx > 0 {
		prefix := lower[:idx]
		if EconomicAdvancedAgentWhiteList[prefix] {
			return true
		}
	}
	return false
}

// indexRune 内部 helper，避免引入 strings 包造成 import 循环噪音
func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// toLowerASCII 把 ASCII 字母转小写（中文/数字保持不变）。白名单 key 全部是 ASCII。
func toLowerASCII(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c + ('a' - 'A')
		}
		out[i] = c
	}
	return string(out)
}

// ============= v2.0.20：合成 Session ID 缓存 =============
// 适用于 opencode / OpenAI/Python 等高阶 Agent，当 RecognizeSessionID
// 无法从请求中识别 session_id 时，按 userName+modelName 维度缓存一个
// 合成 session_id，让连续请求走 SelectForSession 的 session 粘性路径，
// 同时不同对话（超时后）能轮换到不同源站，兼顾连贯性与负载均衡。

// EconomicSyntheticSessionTTL 合成 session 的有效期。同一 userName+modelName
// 在 TTL 内的连续请求复用同一 session_id；超过后重新生成，实现对话级轮换。
const EconomicSyntheticSessionTTL = 15 * time.Minute

// EconomicSyntheticSessionEligibleAgents 需要合成 session 的 Agent 名称集合。
// 大小写不敏感，匹配逻辑复用 toLowerASCII。
//
// v2.0.73 扩展：新增 codex-cli / hermes / aider / continue / cline 等
// 未主动发送 session_id 但需要 session 粘性路由的 Agent。
var EconomicSyntheticSessionEligibleAgents = map[string]bool{
	"opencode":      true,
	"openai/python": true,
	"codex-cli":     true, // v2.0.73：OpenAI Codex CLI
	"hermes":        true, // v2.0.73：Hermes CLI
	"aider":         true, // v2.0.73：Aider AI pair programming
	"continue":      true, // v2.0.73：Continue IDE extension
	"cline":         true, // v2.0.73：Cline VS Code extension
	// v2.0.75 扩展：线上观测到这些 Agent 请求未携带可识别 session_id，
	// 此前落入 Select() 兜底固定打第一个源站，无法参与 Session 级负载均衡。
	// 加入合成 session（userName+modelName，15 分钟 TTL）后获得粘性 + 轮换均衡。
	"atomcode":    true,
	"asyncopenai": true,
	"claude-cli":  true,
	"claude-code": true,
	"kilo-code":   true,
	"windsurf":    true,
	"cursor":      true,
	"copilot":     true,
	"pi":          true,
	"amp":         true,
	"rovo":        true,
	"longcat":     true,
	"grok-build":  true,
	"openclaw":    true,
	"openai/js":   true,
}

// syntheticSessionEntry 缓存条目：session_id + 最后使用时间。
type syntheticSessionEntry struct {
	SessionID string
	LastUsed  time.Time
}

// syntheticSessionCache 全局缓存，key = "userName|modelName"。
// 内存级，服务重启后重新生成（与 economicRouteState 行为一致）。
var (
	syntheticSessionCache   = make(map[string]*syntheticSessionEntry)
	syntheticSessionCacheMu sync.RWMutex
)

// IsSyntheticSessionEligibleAgent 判断 agent 是否需要合成 session。
// 匹配规则：完全相等或前缀匹配（与 IsAdvancedAgentToolName 一致）。
func IsSyntheticSessionEligibleAgent(agentName string) bool {
	if agentName == "" {
		return false
	}
	trimmed := agentName
	for i, c := range trimmed {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			trimmed = trimmed[:i]
			break
		}
	}
	lower := toLowerASCII(trimmed)
	if EconomicSyntheticSessionEligibleAgents[lower] {
		return true
	}
	if idx := indexRune(lower, '/'); idx > 0 {
		prefix := lower[:idx]
		if EconomicSyntheticSessionEligibleAgents[prefix] {
			return true
		}
	}
	return false
}

// GetOrSynthesizeSessionID 从缓存获取或生成合成 session_id。
//
// 参数：
//   - userName：用户名称（来自 GetCachedUserByID）
//   - modelName：请求模型名称（来自 parseModelFromBody）
//
// 返回：
//   - (sessionID, true)：成功获取或生成
//   - ("", false)：userName 或 modelName 为空
//
// 行为：
//  1. cacheKey = "userName|modelName"
//  2. RLock 查缓存 → 命中且未过期 → 更新 LastUsed，返回缓存的 session_id
//  3. Lock 写缓存 → 二次检查（防并发生成）
//  4. 生成 24 位 hex 随机字符串（crypto/rand → 12 字节 → hex.EncodeToString）
//  5. 存入缓存，返回
func GetOrSynthesizeSessionID(userName, modelName string) (string, bool) {
	if userName == "" || modelName == "" {
		return "", false
	}

	cacheKey := userName + "|" + modelName
	now := time.Now()

	// 快速路径：读锁查缓存
	syntheticSessionCacheMu.RLock()
	entry, exists := syntheticSessionCache[cacheKey]
	if exists && now.Sub(entry.LastUsed) < EconomicSyntheticSessionTTL {
		sessionID := entry.SessionID
		entry.LastUsed = now
		syntheticSessionCacheMu.RUnlock()
		return sessionID, true
	}
	syntheticSessionCacheMu.RUnlock()

	// 慢路径：写锁生成
	syntheticSessionCacheMu.Lock()
	defer syntheticSessionCacheMu.Unlock()

	// 二次检查：并发场景下可能已被其他 goroutine 生成
	entry, exists = syntheticSessionCache[cacheKey]
	if exists && now.Sub(entry.LastUsed) < EconomicSyntheticSessionTTL {
		sessionID := entry.SessionID
		entry.LastUsed = now
		return sessionID, true
	}

	// 生成 24 位 hex 随机字符串（12 字节 → 24 hex 字符）
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败是系统级异常，降级为不合成
		return "", false
	}
	sessionID := hex.EncodeToString(b) // 24 字符

	syntheticSessionCache[cacheKey] = &syntheticSessionEntry{
		SessionID: sessionID,
		LastUsed:  now,
	}
	return sessionID, true
}

// ResetSyntheticSessionCache 清空合成 session 缓存（测试用）。
func ResetSyntheticSessionCache() {
	syntheticSessionCacheMu.Lock()
	defer syntheticSessionCacheMu.Unlock()
	syntheticSessionCache = make(map[string]*syntheticSessionEntry)
}

// ExtractRequestToolNamesForAlgorithm 从请求 body 字节中按 OpenAI/Anthropic 协议
// 提取 tools 名称（逗号分隔）。仅用于经济型算法的「知识问答」分支判断：
// 返回空表示 body 中不含任何 tool/function_call 声明。
//
// 实现复用 recognizer_anthropic_tool_call.go / recognizer_openai_function_call.go
// 的提取函数 + protocol analyzer 的 fallback（metadata.tools / parameters.tools）。
// 失败时返回 ""，调用方应据此判定为「无 tool call」。
func ExtractRequestToolNamesForAlgorithm(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		return ""
	}
	if obj == nil {
		return ""
	}
	// Anthropic 标准 tools[].name 优先
	if names := recognizer.ExtractAnthropicToolNames(obj); names != "" {
		return names
	}
	// OpenAI 标准 tools[].function.name
	if names := recognizer.ExtractOpenAIToolNames(obj); names != "" {
		return names
	}
	// OpenAI 兜底：messages[].tool_calls[].function.name
	if names := recognizer.ExtractOpenAIToolCallsFromMessages(obj); names != "" {
		return names
	}
	return ""
}

// SelectForKBRequest 知识问答分支：从 route.DstEndPointIDs 中随机挑选一个可用源站。
//
// 可用源站定义：
//   - DstEndPointIDStatuses 对应位置为 1（启用）
//   - 状态列表缺失时默认启用（兼容旧数据）
//
// 返回 (endpointID, true) 或 (0, false)。
// 不消费 livePool，不写 sessionIndex；连续失败回退由 forwardWithRetry 现有循环处理。
func (s *EconomicAlgorithmSelector) SelectForKBRequest(route *CachedAIRoute) (uint64, bool) {
	if route == nil || len(route.DstEndPointIDs) == 0 {
		return 0, false
	}
	// 收集可用源站（状态为1或状态列表缺失；v2.0.75 起跳过冷却中的源站）
	state := getEconomicState(route.ID)
	state.mu.Lock()
	recoverCooldownEndpointsLocked(state, route)
	state.mu.Unlock()
	available := make([]uint64, 0, len(route.DstEndPointIDs))
	for i, id := range route.DstEndPointIDs {
		status := 1
		if i < len(route.DstEndPointIDStatuses) {
			status = route.DstEndPointIDStatuses[i]
		}
		if status == 1 && !s.IsEndpointCooling(route.ID, id) {
			available = append(available, id)
		}
	}
	if len(available) == 0 {
		return 0, false
	}
	// 用 Fisher-Yates 洗牌后返回第一个，避免「总是选第一个」带来的偏差
	mathrand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})
	return available[0], true
}

// IsKnowledgeBaseRequest 判断当前请求是否属于「知识问答」分支。
// 返回 true 当且仅当 sessionID 为空、toolNames 为空、agentName 不在高阶白名单。
//
// 入参：
//   - sessionID：RecognizeSessionID 的结果
//   - toolNames：ExtractRequestToolNamesForAlgorithm 的结果
//   - agentName：recognizer.RecognizeAgentTool(...).AgentToolName
func IsKnowledgeBaseRequest(sessionID, toolNames, agentName string) bool {
	if sessionID != "" {
		return false
	}
	if toolNames != "" {
		return false
	}
	if IsAdvancedAgentToolName(agentName) {
		return false
	}
	return true
}
