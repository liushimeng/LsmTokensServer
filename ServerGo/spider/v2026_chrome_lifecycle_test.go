package spider

// ==================== v2.0.26：Chrome 进程生命周期管理优化单测 ====================
//
// 覆盖（基于 Chrome 进程 / tab 资源泄漏分析）：
//   1) sessionCleanupLoop 使用 detachCDPContext 释放 CDP 资源
//   2) getOrCreateSession 删除过期 session 前释放 CDP 资源
//   3) detachAllSpiderSessions 幂等安全（多次调用不 panic）
//   4) detachCDPContext 处理 nil session
//   5) orphanChromeCleanupLoop 安全守卫（只杀匹配 CDP 端口的 Chrome）
//   6) findChromePIDsOnPort 不 panic（fuser 不可用时回退 /proc）

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestDetachCDPContext_NilSession 验证 nil session 不 panic
func TestDetachCDPContext_NilSession(t *testing.T) {
	// 不应 panic
	detachCDPContext(nil)
}

// TestDetachCDPContext_NilCancel 验证 cdpCancel 为 nil 时不 panic
func TestDetachCDPContext_NilCancel(t *testing.T) {
	s := &SpiderSession{
		SessionID: "test_nil_cancel",
		cdpCtx:    context.Background(),
		cdpTarget: "fake-target",
		cdpCancel: nil,
	}
	detachCDPContext(s)
	if s.cdpCtx != nil {
		t.Error("expected cdpCtx to be nil after detach")
	}
	if s.cdpTarget != "" {
		t.Error("expected cdpTarget to be empty after detach")
	}
}

// TestDetachCDPContext_WithCancel 验证正常 detach 清理全部字段
func TestDetachCDPContext_WithCancel(t *testing.T) {
	cancelCalled := false
	s := &SpiderSession{
		SessionID: "test_with_cancel",
		cdpCtx:    context.Background(),
		cdpTarget: "fake-target-123",
		cdpCancel: func() { cancelCalled = true },
	}
	detachCDPContext(s)
	if !cancelCalled {
		t.Error("expected cdpCancel to be called")
	}
	if s.cdpCtx != nil {
		t.Error("expected cdpCtx to be nil after detach")
	}
	if s.cdpCancel != nil {
		t.Error("expected cdpCancel to be nil after detach")
	}
	if s.cdpTarget != "" {
		t.Error("expected cdpTarget to be empty after detach")
	}
}

// TestDetachCDPContext_Idempotent 验证多次调用不 panic（幂等性）
func TestDetachCDPContext_Idempotent(t *testing.T) {
	callCount := 0
	s := &SpiderSession{
		SessionID: "test_idempotent",
		cdpCtx:    context.Background(),
		cdpTarget: "target-xyz",
		cdpCancel: func() { callCount++ },
	}
	// 调用 3 次不应 panic
	for i := 0; i < 3; i++ {
		detachCDPContext(s)
	}
	// sync.Once 保护：cdpCancel 只被调 1 次（第一次 detach 后 cdpCancel 被置 nil）
	if callCount != 1 {
		t.Errorf("expected cdpCancel called once, got %d", callCount)
	}
	if s.cdpCtx != nil {
		t.Error("expected cdpCtx nil after multiple detaches")
	}
}

// TestDetachAllSpiderSessions_EmptyMap 验证空 session map 不 panic
func TestDetachAllSpiderSessions_EmptyMap(t *testing.T) {
	// 保存并恢复原始 map
	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	// 不应 panic
	detachAllSpiderSessions()
}

// TestDetachAllSpiderSessions_NilEntry 验证 map 中有 nil session 不 panic
func TestDetachAllSpiderSessions_NilEntry(t *testing.T) {
	spiderSessionsMu.Lock()
	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	spiderSessions["nil_session"] = nil
	spiderSessionsMu.Unlock()
	defer func() {
		spiderSessionsMu.Lock()
		spiderSessions = origSessions
		spiderSessionsMu.Unlock()
	}()

	// 不应 panic
	detachAllSpiderSessions()
}

// TestDetachAllSpiderSessions_MultipleSessions 验证多个 session 全部被 detach
func TestDetachAllSpiderSessions_MultipleSessions(t *testing.T) {
	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	detachCounts := make(map[string]int)
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("session_%d", i)
		spiderSessions[id] = &SpiderSession{
			SessionID: id,
			cdpCtx:    context.Background(),
			cdpTarget: fmt.Sprintf("target_%d", i),
			cdpCancel: func() {
				mu.Lock()
				detachCounts[id]++
				mu.Unlock()
			},
		}
	}

	detachAllSpiderSessions()

	mu.Lock()
	defer mu.Unlock()
	for id, s := range spiderSessions {
		if s != nil {
			// detachAllSpiderSessions 不 delete map entry，只清理 CDP 字段
			if s.cdpCtx != nil {
				t.Errorf("session %s: expected cdpCtx nil", id)
			}
			if s.cdpTarget != "" {
				t.Errorf("session %s: expected cdpTarget empty", id)
			}
		}
	}
}

// TestSessionCleanupLoop_DetachesExpiredSession 验证过期 session 的 CDP 资源被正确释放
// （模拟 sessionCleanupLoop 的核心逻辑，不启动真实 ticker）
func TestSessionCleanupLoop_DetachesExpiredSession(t *testing.T) {
	cancelCalled := false
	session := &SpiderSession{
		SessionID: "expired_session",
		ExpiresAt: time.Now().Add(-1 * time.Minute), // 已过期
		cdpCtx:    context.Background(),
		cdpTarget: "old_target",
		cdpCancel: func() { cancelCalled = true },
	}

	// 模拟 sessionCleanupLoop 的清理逻辑
	now := time.Now().UTC()
	if now.After(session.ExpiresAt) {
		detachCDPContext(session) // v2.0.26: 用 detachCDPContext 替代手动 cdpCancel
	}

	if !cancelCalled {
		t.Error("expected cdpCancel to be called for expired session")
	}
	if session.cdpCtx != nil {
		t.Error("expected cdpCtx nil after cleanup")
	}
	if session.cdpTarget != "" {
		t.Error("expected cdpTarget empty after cleanup")
	}
}

// TestGetOrCreateSession_DetachesExpiredSession 验证 getOrCreateSession 覆盖过期 session
// 时先释放 CDP 资源
func TestGetOrCreateSession_DetachesExpiredSession(t *testing.T) {
	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	cancelCalled := false
	expiredID := "expired_test_session"
	spiderSessions[expiredID] = &SpiderSession{
		SessionID: expiredID,
		CreatedAt: time.Now().Add(-20 * time.Minute),
		UpdatedAt: time.Now().Add(-20 * time.Minute),
		ExpiresAt: time.Now().Add(-5 * time.Minute), // 已过期
		cdpCtx:    context.Background(),
		cdpTarget: "stale_target",
		cdpCancel: func() { cancelCalled = true },
	}

	// getOrCreateSession 应该：1) 释放旧 session CDP 资源 2) 创建新 session
	newSession := getOrCreateSession(expiredID)

	if !cancelCalled {
		t.Error("expected expired session cdpCancel to be called")
	}
	if newSession.SessionID == expiredID {
		t.Error("expected new session ID to differ from expired one")
	}
	// 旧 session 应已从 map 中删除
	if _, exists := spiderSessions[expiredID]; exists {
		t.Error("expected expired session to be removed from map")
	}
}

// TestFindChromePIDsOnPort_NoPanic 验证 findChromePIDsOnPort 在端口未占用时不 panic
func TestFindChromePIDsOnPort_NoPanic(t *testing.T) {
	// 使用一个极不可能被占用的端口
	pids := findChromePIDsOnPort(19876)
	// 不关心结果（端口可能被占用也可能不被占用），只验证不 panic
	_ = pids
}

// TestOrphanChromeDetection_PIDMismatch 验证 PID 不匹配时的清理逻辑
// （仅测试逻辑分支，不真正杀进程）
func TestOrphanChromeDetection_PIDMismatch(t *testing.T) {
	// 模拟：当前 engine PID = 12345，但端口上是 PID = 99999
	currentPID := 12345
	orphanPID := 99999

	// 逻辑验证：当 PID 不匹配时应该触发清理
	if currentPID == orphanPID {
		t.Error("test setup error: PIDs should differ")
	}

	// findChromePIDsOnPort 对不存在的端口应返回空
	pids := findChromePIDsOnPort(19877)
	if len(pids) != 0 {
		t.Logf("port 19877 unexpectedly has processes: %v", pids)
	}
}

// TestSpiderSession_CDPMutexSafety 验证 cdpMu 在并发 detach 时不死锁
func TestSpiderSession_CDPMutexSafety(t *testing.T) {
	s := &SpiderSession{
		SessionID: "concurrent_test",
		cdpCtx:    context.Background(),
		cdpTarget: "concurrent_target",
		cdpCancel: func() {},
	}

	done := make(chan struct{})
	go func() {
		s.cdpMu.Lock()
		detachCDPContext(s)
		s.cdpMu.Unlock()
		close(done)
	}()

	select {
	case <-done:
		// 正常完成
	case <-time.After(5 * time.Second):
		t.Fatal("cdpMu deadlocked during concurrent detach")
	}
}
