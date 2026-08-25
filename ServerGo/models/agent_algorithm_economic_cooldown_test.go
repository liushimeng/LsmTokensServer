package models

import (
	"testing"
	"time"
)

// ============================================================================
// v2.0.75 经济型「内存冷却 + 自动恢复」测试
// 行为约定：连续失败达阈值的源站只从内存 livePool 摘除（不写库、不改路由配置），
// 冷却期结束后自动回归 livePool 参与下一轮分配（即下一轮遍历到时可重试）。
// ============================================================================

// TestEconomicCooldownRemovesFromLivePool 验证冷却摘除只影响内存状态：
// 冷却中的源站不再被新 session 分配，但路由配置保持完整。
func TestEconomicCooldownRemovesFromLivePool(t *testing.T) {
	ResetEconomicRouteState(9100)
	defer ResetEconomicRouteState(9100)

	route := makeTestRoute(9100, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 先消费掉部分 livePool，让池内保留确定内容
	if _, ok := sel.SelectForSession(route, "session-cool-1"); !ok {
		t.Fatal("first select failed")
	}

	// 冷却源站 2
	sel.cooldownEndpointForDuration(9100, 2, time.Hour)

	if !sel.IsEndpointCooling(9100, 2) {
		t.Fatal("endpoint 2 should be cooling")
	}
	if sel.IsEndpointCooling(9100, 1) {
		t.Fatal("endpoint 1 should not be cooling")
	}

	// 后续若干新 session 都不应分到源站 2
	for i := 0; i < 20; i++ {
		id, ok := sel.SelectForSession(route, "session-cool-next")
		if !ok {
			t.Fatalf("select %d failed", i)
		}
		if id == 2 {
			t.Fatalf("cooling endpoint 2 should not be assigned to new session, got %d at iter %d", id, i)
		}
	}
}

// TestEconomicCooldownAutoRecover 验证冷却到期后源站自动回归 livePool：
// 恢复后可重新承接 session 分配（下一轮遍历到时重试）。
func TestEconomicCooldownAutoRecover(t *testing.T) {
	ResetEconomicRouteState(9200)
	defer ResetEconomicRouteState(9200)

	route := makeTestRoute(9200, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 冷却源站 1，时长立即到期（负时长）
	sel.cooldownEndpointForDuration(9200, 1, -time.Second)
	if sel.IsEndpointCooling(9200, 1) {
		t.Fatal("endpoint 1 should not be cooling after expired duration")
	}

	// 多个新 session 分配，源站 1 应能重新出现（恢复参与负载均衡）
	seen := map[uint64]bool{}
	for i := 0; i < 30; i++ {
		id, ok := sel.SelectForSession(route, "session-recover")
		if !ok {
			t.Fatalf("select %d failed", i)
		}
		seen[id] = true
	}
	if !seen[1] {
		t.Errorf("endpoint 1 should recover and be assigned again, assignments=%v", seen)
	}
}

// TestEconomicCooldownSelectFallbackSkipsCooling 无 session 兜底 Select 跳过冷却源站。
func TestEconomicCooldownSelectFallbackSkipsCooling(t *testing.T) {
	ResetEconomicRouteState(9300)
	defer ResetEconomicRouteState(9300)

	route := makeTestRoute(9300, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	sel.cooldownEndpointForDuration(9300, 1, time.Hour)

	id, ok := sel.Select(route)
	if !ok {
		t.Fatal("select failed")
	}
	if id == 1 {
		t.Errorf("fallback Select should skip cooling endpoint 1, got %d", id)
	}
}

// TestEconomicCooldownKBRequestSkipsCooling 知识问答随机分支跳过冷却源站。
func TestEconomicCooldownKBRequestSkipsCooling(t *testing.T) {
	ResetEconomicRouteState(9400)
	defer ResetEconomicRouteState(9400)

	route := makeTestRoute(9400, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	sel.cooldownEndpointForDuration(9400, 1, time.Hour)
	sel.cooldownEndpointForDuration(9400, 2, time.Hour)

	for i := 0; i < 10; i++ {
		id, ok := sel.SelectForKBRequest(route)
		if !ok {
			t.Fatalf("KB select %d failed", i)
		}
		if id != 3 {
			t.Fatalf("KB select should only return endpoint 3 while 1/2 cooling, got %d", id)
		}
	}
}

// TestEconomicCooldownStickyMappingBypass 粘性映射指向冷却源站时不再直接返回。
func TestEconomicCooldownStickyMappingBypass(t *testing.T) {
	ResetEconomicRouteState(9500)
	defer ResetEconomicRouteState(9500)

	route := makeTestRoute(9500, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	id1, ok := sel.SelectForSession(route, "session-sticky")
	if !ok {
		t.Fatal("first select failed")
	}

	// 冷却粘性源站，再选同一 session 应换源
	sel.cooldownEndpointForDuration(9500, id1, time.Hour)
	id2, ok := sel.SelectForSession(route, "session-sticky")
	if !ok {
		t.Fatal("re-select failed")
	}
	if id2 == id1 {
		t.Errorf("sticky endpoint %d is cooling, session should be reassigned, got same", id1)
	}
}

// TestEconomicCooldownResetsFailureCount 冷却摘除时清零失败计数，
// 避免源站恢复后因历史计数被立即再次摘除。
func TestEconomicCooldownResetsFailureCount(t *testing.T) {
	ResetEconomicRouteState(9600)
	defer ResetEconomicRouteState(9600)

	sel := &EconomicAlgorithmSelector{}

	// 两次失败（未达阈值 3）
	for i := 0; i < 2; i++ {
		if should, _ := sel.OnEndpointFailure(9600, 7); should {
			t.Fatal("should not cooldown before threshold")
		}
	}
	// 冷却摘除后计数应清零：再失败一次不应达到阈值
	sel.cooldownEndpointForDuration(9600, 7, time.Hour)
	if should, _ := sel.OnEndpointFailure(9600, 7); should {
		t.Fatal("failure count should be reset after cooldown")
	}
}

// TestEconomicCooldownSyncRemovesDeleted Web 删除源站时同步清理冷却表。
func TestEconomicCooldownSyncRemovesDeleted(t *testing.T) {
	ResetEconomicRouteState(9700)
	defer ResetEconomicRouteState(9700)

	route := makeTestRoute(9700, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	sel.cooldownEndpointForDuration(9700, 2, time.Hour)

	// Web 删除源站 2
	SyncEconomicRouteEndpoints(9700, []uint64{1, 3})

	if sel.IsEndpointCooling(9700, 2) {
		t.Error("cooldown entry for removed endpoint 2 should be cleaned")
	}
	_ = route
}

// TestEconomicCooldownSyncDoesNotReaddCooling Sync 增量对齐时不得把冷却中的源站加回 livePool。
func TestEconomicCooldownSyncDoesNotReaddCooling(t *testing.T) {
	ResetEconomicRouteState(9800)
	defer ResetEconomicRouteState(9800)

	sel := &EconomicAlgorithmSelector{}

	sel.cooldownEndpointForDuration(9800, 5, time.Hour)

	// 以同一列表（含冷却中的 5）做同步：5 不应回到 livePool
	SyncEconomicRouteEndpoints(9800, []uint64{5, 6})

	state := getEconomicState(9800)
	state.mu.Lock()
	defer state.mu.Unlock()
	if containsUint64(state.livePool, 5) {
		t.Error("cooling endpoint 5 should not be re-added to livePool by Sync")
	}
	if !containsUint64(state.livePool, 6) {
		t.Error("endpoint 6 should be in livePool after Sync")
	}
}

// TestEconomicSyntheticSessionEligibleNewAgents v2.0.75 扩展的合成 session 名单。
func TestEconomicSyntheticSessionEligibleNewAgents(t *testing.T) {
	cases := []struct {
		agent string
		want  bool
	}{
		{"atomcode", true},
		{"atomcode/1.2.3", true},
		{"AsyncOpenAI", true},
		{"AsyncOpenAI/1.0", true},
		{"claude-cli", true},
		{"claude-code/2.0", true},
		{"opencode", true},
		{"unknown-agent", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsSyntheticSessionEligibleAgent(c.agent); got != c.want {
			t.Errorf("IsSyntheticSessionEligibleAgent(%q) = %v, want %v", c.agent, got, c.want)
		}
	}
}

// TestEconomicAdvancedAgentWhiteListNewMembers v2.0.75 扩展的高阶 Agent 白名单。
func TestEconomicAdvancedAgentWhiteListNewMembers(t *testing.T) {
	cases := []string{"atomcode", "asyncopenai", "amp", "rovo", "longcat", "grok-build", "openclaw"}
	for _, name := range cases {
		if !IsAdvancedAgentToolName(name) {
			t.Errorf("IsAdvancedAgentToolName(%q) should be true", name)
		}
	}
	if IsAdvancedAgentToolName("some-random-lib") {
		t.Error("unknown agent should not be advanced")
	}
}
