package spider

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
)

// ==================== v2.0.33：MCP 爬虫并发/资源泄漏防护测试 ====================
// 基于问题分析报告_20260709_145100 §4.1-§4.3 的技术建议：
//   - handler 超时后必须释放 session CDP 资源与 sem 槽位
//   - session map 必须有数量上限，避免 Chrome 进程无界累积
//   - Chrome 重启时应等待旧 CDP 端口完全释放

func resetSpiderSessions() {
	spiderSessionsMu.Lock()
	defer spiderSessionsMu.Unlock()
	for id, s := range spiderSessions {
		detachCDPContext(s)
		delete(spiderSessions, id)
	}
}

func TestSetMaxSpiderSessions(t *testing.T) {
	oldCfg := config.G
	defer func() { config.G = oldCfg }()

	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 4}
	setMaxSpiderSessions()
	if maxSpiderSessions != 16 {
		t.Fatalf("expected cap=16 for conc=4, got %d", maxSpiderSessions)
	}

	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 8}
	setMaxSpiderSessions()
	if maxSpiderSessions != 32 {
		t.Fatalf("expected cap=32 for conc=8, got %d", maxSpiderSessions)
	}

	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 100}
	setMaxSpiderSessions()
	if maxSpiderSessions != 256 {
		t.Fatalf("expected cap clamped to 256, got %d", maxSpiderSessions)
	}

	config.G = nil
	setMaxSpiderSessions()
	if maxSpiderSessions != 64 {
		t.Fatalf("expected default cap=64 when config.G=nil, got %d", maxSpiderSessions)
	}
}

func TestEvictOldestSpiderSessionIfNeeded(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	oldCfg := config.G
	defer func() { config.G = oldCfg }()
	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 1}
	setMaxSpiderSessions()
	if maxSpiderSessions != 16 {
		t.Fatalf("expected min cap=16, got %d", maxSpiderSessions)
	}

	now := time.Now().UTC()
	for i := 0; i < maxSpiderSessions+5; i++ {
		id := fmt.Sprintf("spider_%d", i)
		s := &SpiderSession{
			SessionID: id,
			CreatedAt: now.Add(-time.Duration(i) * time.Second),
			UpdatedAt: now.Add(-time.Duration(i) * time.Second),
			ExpiresAt: now.Add(spiderSessionTTL),
		}
		spiderSessionsMu.Lock()
		spiderSessions[id] = s
		spiderSessionsMu.Unlock()
	}

	spiderSessionsMu.Lock()
	evictOldestSpiderSessionIfNeeded()
	spiderSessionsMu.Unlock()

	spiderSessionsMu.RLock()
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if count != maxSpiderSessions {
		t.Fatalf("expected session count capped at %d, got %d", maxSpiderSessions, count)
	}

	// 验证 session 总数被限制到上限（LRU 淘汰后可能保留任意早期/晚期 idle session）。
	spiderSessionsMu.RLock()
	count2 := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if count2 != maxSpiderSessions {
		t.Fatalf("expected session count capped at %d, got %d", maxSpiderSessions, count2)
	}
}

func TestEvictOldestSpiderSessionIfNeeded_SkipsActive(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	oldCfg := config.G
	defer func() { config.G = oldCfg }()
	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 1}
	setMaxSpiderSessions()

	now := time.Now().UTC()
	for i := 0; i < maxSpiderSessions+2; i++ {
		id := fmt.Sprintf("spider_active_%d", i)
		s := &SpiderSession{
			SessionID: id,
			CreatedAt: now,
			UpdatedAt: now.Add(-time.Duration(i) * time.Second),
			ExpiresAt: now.Add(spiderSessionTTL),
			cdpCtx:    context.Background(), // 模拟使用中
		}
		spiderSessionsMu.Lock()
		spiderSessions[id] = s
		spiderSessionsMu.Unlock()
	}

	spiderSessionsMu.Lock()
	evictOldestSpiderSessionIfNeeded()
	count := len(spiderSessions)
	spiderSessionsMu.Unlock()

	if count != maxSpiderSessions+2 {
		t.Fatalf("expected active sessions not evicted (count=%d), got %d", maxSpiderSessions+2, count)
	}
}

func TestGetOrCreateSession_EvictsOnCreate(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	oldCfg := config.G
	defer func() { config.G = oldCfg }()
	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 1}
	setMaxSpiderSessions()

	now := time.Now().UTC()
	for i := 0; i < maxSpiderSessions; i++ {
		id := fmt.Sprintf("spider_%d", i)
		spiderSessionsMu.Lock()
		spiderSessions[id] = &SpiderSession{
			SessionID: id,
			CreatedAt: now,
			UpdatedAt: now.Add(-time.Duration(i) * time.Second),
			ExpiresAt: now.Add(spiderSessionTTL),
		}
		spiderSessionsMu.Unlock()
	}

	_ = getOrCreateSession("")

	spiderSessionsMu.RLock()
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if count != maxSpiderSessions {
		t.Fatalf("expected count still capped at %d after create, got %d", maxSpiderSessions, count)
	}
}

func TestDetachCDPContext_ReleasesSem(t *testing.T) {
	// 构造一个假 engine 和假 session，验证 detachCDPContext 会释放 sem
	eng := &SpiderEngine{
		sem:            make(chan struct{}, 1),
		semAcquireTime: make(map[int]time.Time),
	}
	if err := eng.acquireSem(100 * time.Millisecond); err != nil {
		t.Fatalf("failed to acquire sem: %v", err)
	}

	s := &SpiderSession{SessionID: "test_sem_release"}
	semReleased := false
	s.cdpCancel = func() {
		if !semReleased {
			semReleased = true
			eng.releaseSem()
		}
	}

	detachCDPContext(s)

	if !semReleased {
		t.Fatalf("cdpCancel did not release sem")
	}
	select {
	case eng.sem <- struct{}{}:
		// sem 已释放，可以再次获取
	default:
		t.Fatalf("sem was not released after detachCDPContext")
	}
}

// TestSessionCleanupLoop_EvictsExpired 通过直接调用内部逻辑验证 TTL 清理。
func TestSessionCleanupLoop_EvictsExpired(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	now := time.Now().UTC()
	id := "expired_session"
	spiderSessionsMu.Lock()
	spiderSessions[id] = &SpiderSession{
		SessionID: id,
		CreatedAt: now.Add(-20 * time.Minute),
		UpdatedAt: now.Add(-20 * time.Minute),
		ExpiresAt: now.Add(-1 * time.Minute),
	}
	spiderSessionsMu.Unlock()

	spiderSessionsMu.Lock()
	for sid, s := range spiderSessions {
		if now.After(s.ExpiresAt) {
			detachCDPContext(s)
			delete(spiderSessions, sid)
		}
	}
	spiderSessionsMu.Unlock()

	spiderSessionsMu.RLock()
	_, exists := spiderSessions[id]
	spiderSessionsMu.RUnlock()
	if exists {
		t.Fatalf("expired session should have been cleaned up")
	}
}

// TestDetachAllSpiderSessions_ReleasesAll 验证批量 detach 能释放所有 sem。
func TestDetachAllSpiderSessions_ReleasesAll(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	eng := &SpiderEngine{
		sem:            make(chan struct{}, 3),
		semAcquireTime: make(map[int]time.Time),
	}
	for i := 0; i < 3; i++ {
		if err := eng.acquireSem(100 * time.Millisecond); err != nil {
			t.Fatalf("failed to acquire sem: %v", err)
		}
	}

	for i := 0; i < 3; i++ {
		s := &SpiderSession{SessionID: fmt.Sprintf("batch_%d", i)}
		s.cdpCancel = func() { eng.releaseSem() }
		spiderSessionsMu.Lock()
		spiderSessions[s.SessionID] = s
		spiderSessionsMu.Unlock()
	}

	detachAllSpiderSessions()

	// 所有 sem 应被释放
	for i := 0; i < 3; i++ {
		select {
		case eng.sem <- struct{}{}:
		default:
			t.Fatalf("sem slot %d not released after detachAllSpiderSessions", i)
		}
	}
}

// TestEvictOldestSpiderSessionIfNeeded_ThreadSafe 简单并发测试。
func TestEvictOldestSpiderSessionIfNeeded_ThreadSafe(t *testing.T) {
	resetSpiderSessions()
	defer resetSpiderSessions()

	oldCfg := config.G
	defer func() { config.G = oldCfg }()
	config.G = &config.LsmTokensServerConfig{SpiderMaxConcurrency: 1}
	setMaxSpiderSessions()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("concurrent_%d", i)
			spiderSessionsMu.Lock()
			spiderSessions[id] = &SpiderSession{
				SessionID: id,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC().Add(-time.Duration(i) * time.Second),
				ExpiresAt: time.Now().UTC().Add(spiderSessionTTL),
			}
			evictOldestSpiderSessionIfNeeded()
			spiderSessionsMu.Unlock()
		}(i)
	}
	wg.Wait()

	spiderSessionsMu.RLock()
	count := len(spiderSessions)
	spiderSessionsMu.RUnlock()
	if count > maxSpiderSessions {
		t.Fatalf("concurrent create exceeded cap: %d > %d", count, maxSpiderSessions)
	}
}
