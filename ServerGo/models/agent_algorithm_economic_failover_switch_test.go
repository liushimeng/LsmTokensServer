package models

import (
	"testing"
	"time"
)

// ============================================================================
// v2.0.78 经济型「连续 3 次失败自动切换」对齐稳定型语义测试
// 方案：tmpPlan/智能路由经济型算法连续3次失败自动切换优化方案_20260914.md
//
// 覆盖回归点：
//   1. 失败计数按源站维度独立累计：其它源站的成功不得清零故障源站计数
//      （旧实现 OnRequestSuccess 清零全部计数 → 单一故障源站永远达不到阈值）
//   2. OnEndpointFailure 记录 RecordFailure 指标（与稳定型对齐）
//   3. 全冷却探测重置：全部启用源站冷却时 Select / SelectForSession /
//      SelectForKBRequest 立即恢复遍历，不再 fail-fast 一个冷却周期
//   4. 部分冷却不触发探测重置（冷却语义保持）
//   5. 模拟 forwardWithRetry 语义：达阈值后继续切换下一个源站，同一请求内
//      多个达阈值源站都被冷却（旧实现单值覆盖 + break 中断重试）
// ============================================================================

// TestEconomicFailureCountSurvivesOtherEndpointSuccess 核心回归（Bug 1）：
// 模拟线上场景——源站 1 持续故障、源站 2 正常。每个请求先在源站 1 失败、
// 请求内重试在源站 2 成功。源站 1 必须在第 3 个请求达到冷却阈值。
func TestEconomicFailureCountSurvivesOtherEndpointSuccess(t *testing.T) {
	ResetEconomicRouteState(10100)
	defer ResetEconomicRouteState(10100)

	sel := &EconomicAlgorithmSelector{}

	// 前两个请求：源站 1 失败 + 源站 2 成功（成功不得清零源站 1 的计数）
	for i := 0; i < 2; i++ {
		if should, _ := sel.OnEndpointFailure(10100, 1); should {
			t.Fatalf("request %d: should not cooldown before threshold", i+1)
		}
		sel.OnEndpointSuccess(10100, 2)
	}

	// 第 3 个请求：源站 1 失败应达到阈值
	should, cooledID := sel.OnEndpointFailure(10100, 1)
	if !should {
		t.Fatal("endpoint 1 should reach cooldown threshold after 3 consecutive failures, even with endpoint 2 succeeding in between")
	}
	if cooledID != 1 {
		t.Fatalf("expected cooledID=1, got %d", cooledID)
	}
}

// TestEconomicOnEndpointFailureRecordsMetric 指标对齐（缺失 5）：
// OnEndpointFailure 每次递增全局 RoutingMetrics.Failures。
func TestEconomicOnEndpointFailureRecordsMetric(t *testing.T) {
	ResetEconomicRouteState(10200)
	defer ResetEconomicRouteState(10200)

	sel := &EconomicAlgorithmSelector{}
	before := GetRoutingMetrics().Failures
	sel.OnEndpointFailure(10200, 1)
	sel.OnEndpointFailure(10200, 1)
	after := GetRoutingMetrics().Failures
	if after-before != 2 {
		t.Errorf("expected failures metric +2, got +%d", after-before)
	}
}

// TestEconomicAllCoolingProbeReset_Select 全冷却探测重置（Bug 4）：
// 全部源站冷却时无 session 兜底 Select 应立即恢复选择（冷却表被清空），
// 而不是 fail-fast 返回 (0, false)。
func TestEconomicAllCoolingProbeReset_Select(t *testing.T) {
	ResetEconomicRouteState(10300)
	defer ResetEconomicRouteState(10300)

	route := makeTestRoute(10300, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 全部源站进入冷却
	for _, id := range []uint64{1, 2, 3} {
		sel.cooldownEndpointForDuration(10300, id, time.Hour)
	}

	got, ok := sel.Select(route)
	if !ok {
		t.Fatal("Select should recover via probe reset when all endpoints cooling, got (0, false)")
	}
	if got == 0 {
		t.Fatalf("expected a valid endpoint, got 0")
	}
	for _, id := range []uint64{1, 2, 3} {
		if sel.IsEndpointCooling(10300, id) {
			t.Errorf("cooldown for endpoint %d should be reset by probe", id)
		}
	}
}

// TestEconomicAllCoolingProbeReset_SelectForSession 全冷却探测重置：
// session 粘性路径在全冷却时应立即恢复分配（粘性源站直接复用，最小扰动亲和）。
func TestEconomicAllCoolingProbeReset_SelectForSession(t *testing.T) {
	ResetEconomicRouteState(10400)
	defer ResetEconomicRouteState(10400)

	route := makeTestRoute(10400, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 先建立 session 粘性映射
	sticky, ok := sel.SelectForSession(route, "session-probe")
	if !ok {
		t.Fatal("initial select failed")
	}

	// 全部源站进入冷却
	for _, id := range []uint64{1, 2, 3} {
		sel.cooldownEndpointForDuration(10400, id, time.Hour)
	}

	got, ok := sel.SelectForSession(route, "session-probe")
	if !ok {
		t.Fatal("SelectForSession should recover via probe reset when all endpoints cooling")
	}
	if got != sticky {
		t.Logf("note: sticky=%d, reassigned=%d (probe reset re-assign allowed)", sticky, got)
	}
}

// TestEconomicAllCoolingProbeReset_KBRequest 全冷却探测重置：
// 知识问答随机分支在全冷却时应立即恢复选择。
func TestEconomicAllCoolingProbeReset_KBRequest(t *testing.T) {
	ResetEconomicRouteState(10500)
	defer ResetEconomicRouteState(10500)

	route := makeTestRoute(10500, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	for _, id := range []uint64{1, 2, 3} {
		sel.cooldownEndpointForDuration(10500, id, time.Hour)
	}

	got, ok := sel.SelectForKBRequest(route)
	if !ok || got == 0 {
		t.Fatalf("KB select should recover via probe reset when all endpoints cooling, got (%d, %v)", got, ok)
	}
}

// TestEconomicPartialCoolingNoProbeReset 防误伤：仅部分源站冷却时不得触发探测重置，
// 冷却中的源站继续被三条选择路径跳过（冷却语义保持不变）。
func TestEconomicPartialCoolingNoProbeReset(t *testing.T) {
	ResetEconomicRouteState(10600)
	defer ResetEconomicRouteState(10600)

	route := makeTestRoute(10600, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 冷却 1/3 两个源站，源站 3 仍可用 → 不应重置
	sel.cooldownEndpointForDuration(10600, 1, time.Hour)
	sel.cooldownEndpointForDuration(10600, 2, time.Hour)

	if id, ok := sel.Select(route); !ok || id != 3 {
		t.Fatalf("Select should return endpoint 3 (partial cooling), got (%d, %v)", id, ok)
	}
	if !sel.IsEndpointCooling(10600, 1) || !sel.IsEndpointCooling(10600, 2) {
		t.Error("partial cooldown must NOT be reset by probe")
	}
	if id, ok := sel.SelectForKBRequest(route); !ok || id != 3 {
		t.Fatalf("KB select should return endpoint 3 (partial cooling), got (%d, %v)", id, ok)
	}
}

// TestEconomicCooldownThresholdRetriesNextEndpoint 模拟 forwardWithRetry 语义
// （Bug 2/3 回归）：源站 1 达到阈值后不中断重试、继续切换源站 2；
// 同一请求内源站 2 也达阈值时两者都被冷却（收集集合而非单值覆盖）。
func TestEconomicCooldownThresholdRetriesNextEndpoint(t *testing.T) {
	ResetEconomicRouteState(10700)
	defer ResetEconomicRouteState(10700)

	route := makeTestRoute(10700, []uint64{1, 2, 3})
	sel := &EconomicAlgorithmSelector{}

	// 预热：源站 1、2 各已有 2 次失败计数（真实场景由前序请求累计）
	sel.OnEndpointFailure(10700, 1)
	sel.OnEndpointFailure(10700, 1)
	sel.OnEndpointFailure(10700, 2)
	sel.OnEndpointFailure(10700, 2)

	// 模拟 forwardWithRetry 的新语义（noteFailure + 循环结束统一冷却）：
	// 本请求先在源站 1 失败（第 3 次 → 达阈值），继续切换源站 2 也失败（第 3 次 → 达阈值）
	var cooldownIDs []uint64
	note := func(endpointID uint64) {
		if should, cooledID := sel.OnEndpointFailure(10700, endpointID); should {
			cooldownIDs = append(cooldownIDs, cooledID)
		}
	}
	note(1) // 源站 1 第 3 次失败：达阈值，但请求继续
	note(2) // 源站 2 第 3 次失败：同样达阈值

	if len(cooldownIDs) != 2 || cooldownIDs[0] != 1 || cooldownIDs[1] != 2 {
		t.Fatalf("both endpoints reaching threshold should be collected, got %v", cooldownIDs)
	}

	// 循环结束统一冷却（defer 语义）
	for _, id := range cooldownIDs {
		sel.CooldownEndpoint(10700, id)
	}
	if !sel.IsEndpointCooling(10700, 1) || !sel.IsEndpointCooling(10700, 2) {
		t.Fatal("both endpoints should be cooling after request finish")
	}
	if sel.IsEndpointCooling(10700, 3) {
		t.Fatal("endpoint 3 should not be cooling")
	}

	// 后续请求：三条路径都只能选到源站 3（经济型遍历自动切换到下一个源站）
	if id, ok := sel.Select(route); !ok || id != 3 {
		t.Fatalf("Select should switch to endpoint 3, got (%d, %v)", id, ok)
	}
	if id, ok := sel.SelectForSession(route, "session-after-cooldown"); !ok || id != 3 {
		t.Fatalf("SelectForSession should switch to endpoint 3, got (%d, %v)", id, ok)
	}
	if id, ok := sel.SelectForKBRequest(route); !ok || id != 3 {
		t.Fatalf("SelectForKBRequest should switch to endpoint 3, got (%d, %v)", id, ok)
	}
}
