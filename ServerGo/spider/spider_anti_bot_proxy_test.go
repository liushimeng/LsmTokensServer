package spider

// ==================== v2.0.9 Proxy Pool 健康跟踪单元测试 ====================
//
// 覆盖 ProxyPool.RecordFailure / RecordSuccess / ResurrectDeadProxies / HealthSnapshot / Next skip-dead

import (
	"github.com/lishimeng/LsmTokensServer/config"
	"testing"
	"time"
)

// TestProxyPool_Next_SkipsDead 验证 Next() 跳过 Dead 项
func TestProxyPool_Next_SkipsDead(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080", "http://p2:8080", "http://p3:8080"})
	// 手动标记 p2 为死亡
	pool.items[1].Health.Dead = true
	pool.items[1].Health.DeadSince = time.Now()
	// 连续取 6 次（应有 2 个 live 项轮转，永远不会返回 p2）
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		u := pool.Next()
		seen[u]++
		if u == "http://p2:8080" {
			t.Errorf("Next() returned dead proxy: %s", u)
		}
	}
	// 应该看到 p1 和 p3 各 3 次
	if seen["http://p1:8080"] == 0 || seen["http://p3:8080"] == 0 {
		t.Errorf("expected both live proxies to be returned; seen=%v", seen)
	}
}

// TestProxyPool_RecordFailure_DeadThreshold 验证连续失败达阈值后标记 Dead
func TestProxyPool_RecordFailure_DeadThreshold(t *testing.T) {
	// 临时调整 config.G.SpiderProxyDeadThreshold（直接修改全局 config.G）
	if config.G == nil {
		t.Skip("config.G is nil; skipping proxy threshold test")
	}
	originalThreshold := config.G.SpiderProxyDeadThreshold
	config.G.SpiderProxyDeadThreshold = 3
	defer func() { config.G.SpiderProxyDeadThreshold = originalThreshold }()

	pool := LoadProxyPool([]string{"http://p1:8080", "http://p2:8080"})
	// 触发 3 次失败
	for i := 0; i < 3; i++ {
		pool.RecordFailure("http://p1:8080", "anti_bot")
	}
	if !pool.items[0].Health.Dead {
		t.Error("p1 should be Dead after 3 consecutive failures")
	}
	if pool.items[0].Health.ConsecutiveFails != 3 {
		t.Errorf("p1 ConsecutiveFails: got %d, want 3", pool.items[0].Health.ConsecutiveFails)
	}
	// p2 不受影响
	if pool.items[1].Health.Dead {
		t.Error("p2 should not be Dead (no failures recorded)")
	}
}

// TestProxyPool_RecordSuccess_ResetsCounter 验证成功重置连续失败计数
func TestProxyPool_RecordSuccess_ResetsCounter(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080"})
	pool.RecordFailure("http://p1:8080", "anti_bot")
	pool.RecordFailure("http://p1:8080", "anti_bot")
	if pool.items[0].Health.ConsecutiveFails != 2 {
		t.Errorf("after 2 failures: got %d, want 2", pool.items[0].Health.ConsecutiveFails)
	}
	pool.RecordSuccess("http://p1:8080")
	if pool.items[0].Health.ConsecutiveFails != 0 {
		t.Errorf("after success: got %d, want 0", pool.items[0].Health.ConsecutiveFails)
	}
}

// TestProxyPool_RecordFailure_UnknownProxy 验证未知 URL no-op
func TestProxyPool_RecordFailure_UnknownProxy(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080"})
	pool.RecordFailure("http://unknown:9999", "anti_bot")
	// p1 应不受影响
	if pool.items[0].Health.ConsecutiveFails != 0 {
		t.Errorf("unknown proxy failure should not affect p1; got %d", pool.items[0].Health.ConsecutiveFails)
	}
}

// TestProxyPool_ResurrectDeadProxies 验证复活冷却逻辑
func TestProxyPool_ResurrectDeadProxies(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080", "http://p2:8080"})
	// 标记 p1 死亡（DeadSince 在冷却窗口外）
	pool.items[0].Health.Dead = true
	pool.items[0].Health.DeadSince = time.Now().Add(-10 * time.Minute)
	pool.items[0].Health.ConsecutiveFails = 5
	// 标记 p2 死亡（DeadSince 在冷却窗口内）
	pool.items[1].Health.Dead = true
	pool.items[1].Health.DeadSince = time.Now().Add(-10 * time.Second)
	// 复活冷却 = 5 分钟
	resurrected := pool.ResurrectDeadProxies(300)
	if resurrected != 1 {
		t.Errorf("expected 1 resurrection, got %d", resurrected)
	}
	if pool.items[0].Health.Dead {
		t.Error("p1 should be resurrected (DeadSince > 5min ago)")
	}
	if !pool.items[1].Health.Dead {
		t.Error("p2 should NOT be resurrected (DeadSince within cooldown)")
	}
}

// TestProxyPool_HealthSnapshot 验证快照包含所有代理
func TestProxyPool_HealthSnapshot(t *testing.T) {
	pool := LoadProxyPool([]string{"http://p1:8080", "http://p2:8080"})
	snap := pool.HealthSnapshot()
	if len(snap) != 2 {
		t.Errorf("snapshot size: got %d, want 2", len(snap))
	}
	if _, ok := snap["http://p1:8080"]; !ok {
		t.Error("snapshot missing p1")
	}
	if _, ok := snap["http://p2:8080"]; !ok {
		t.Error("snapshot missing p2")
	}
}
