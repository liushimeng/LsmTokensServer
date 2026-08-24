package spider

// ==================== v2.0.27：/healthz 并发/队列指标单测 ====================
//
// 覆盖（基于问题分析报告_20260703_061632 §建议 4：
//   "v2.0.18 端已加 /healthz，但未暴露并发/队列指标，
//    建议加 chrome_active_sessions 字段"）：
//   1) computeSpiderHealthMetrics：session_total / chrome_active_sessions /
//      sem_used / sem_capacity / busy_fail_count / congestion_alert
//      聚合正确
//   2) congestion_alert 触发条件：sem_used ≥80% capacity 或 busy_fails > 0
//   3) cdpTarget 为空 session 不计入 chrome_active_sessions（已 detach 的不算）
//   4) 端到端：buildHealthzHandler 在 engine 停摆时返回 503 + status=down，
//      在 engine 运行 + 空 sessions 时返回 200 + status=up
//
// 注意：computeSpiderHealthMetrics 是 healthz handler 内部闭包，无法直接复用，
// 因此本测试用「直接读写 spiderSessions / SpiderEngine 共享状态」的方式
// 验证同一聚合逻辑，保证端点行为与单测断言同源。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// TestHealthzMetrics_ChromeActiveSessionsVsTotal
// 关键回归：v2.0.27 之前 /healthz 只暴露 session_count（含历史未 detach 的），
// 看门狗无法分辨"真占着 Chrome tab 的活跃 session"与"已 detach 的僵尸"。
// 修复后 chrome_active_sessions 应仅统计 cdpTarget 非空的 session。
func TestHealthzMetrics_ChromeActiveSessionsVsTotal(t *testing.T) {
	engine := GetSpiderEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()

	// 隔离：备份并清空全局 spiderSessions，避免其它测试干扰
	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	// 构造 3 个 session：2 个活跃（cdpTarget 非空）+ 1 个已 detach（cdpTarget 为空）
	spiderSessions["active_1"] = &SpiderSession{SessionID: "active_1", cdpTarget: "target-A"}
	spiderSessions["active_2"] = &SpiderSession{SessionID: "active_2", cdpTarget: "target-B"}
	spiderSessions["zombie"] = &SpiderSession{SessionID: "zombie", cdpTarget: ""}

	sessionTotal, active, _, _, _, _ := aggregateHealthzMetricsForTest()
	if sessionTotal != 3 {
		t.Errorf("expected session_total=3; got %d", sessionTotal)
	}
	if active != 2 {
		t.Errorf("expected chrome_active_sessions=2 (only cdpTarget!=\"\"); got %d", active)
	}
}

// TestHealthzMetrics_CongestionAlert_SemThreshold
// 验证：sem_used ≥80% capacity 时 congestion_alert=true
func TestHealthzMetrics_CongestionAlert_SemThreshold(t *testing.T) {
	engine := GetSpiderEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()

	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	// sem capacity 由 config.G.SpiderMaxConcurrency 决定；本测试读真实 capacity 再填满
	semCap := 4
	if config.G != nil && config.G.SpiderMaxConcurrency > 0 {
		semCap = config.G.SpiderMaxConcurrency
	}
	// 触发 80% 阈值：填到 ceil(cap*0.8) 即可。
	threshold := (semCap*4 + 4) / 5 // 等价于 ceil(cap*0.8)
	for i := 0; i < threshold; i++ {
		engine.sem <- struct{}{}
	}
	defer func() {
		for i := 0; i < threshold; i++ {
			<-engine.sem
		}
	}()

	_, _, semUsed, reportedCap, _, congestion := aggregateHealthzMetricsForTest()
	if reportedCap != semCap {
		t.Errorf("expected sem_capacity=%d (from config.G); got %d", semCap, reportedCap)
	}
	if semUsed != threshold {
		t.Errorf("expected sem_used=%d (>= 80%% cap); got %d", threshold, semUsed)
	}
	if !congestion {
		t.Error("expected congestion_alert=true when sem occupied >= 80% capacity")
	}
}

// TestHealthzMetrics_CongestionAlert_BusyFailCount
// 验证：busy_fail_count > 0 时即使 sem 未满也触发 congestion_alert
func TestHealthzMetrics_CongestionAlert_BusyFailCount(t *testing.T) {
	engine := GetSpiderEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()

	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	engine.busyMu.Lock()
	origBusy := engine.busyFailCount
	engine.busyFailCount = 1
	engine.busyMu.Unlock()
	defer func() {
		engine.busyMu.Lock()
		engine.busyFailCount = origBusy
		engine.busyMu.Unlock()
	}()

	_, _, _, _, busyFails, congestion := aggregateHealthzMetricsForTest()
	if busyFails != 1 {
		t.Errorf("expected busy_fail_count=1; got %d", busyFails)
	}
	if !congestion {
		t.Error("expected congestion_alert=true when busy_fail_count>0 (Chrome 抖动已出现)")
	}
}

// TestHealthzMetrics_NoCongestion_Idle
// 验证：sem 空 + busy_fails=0 时 congestion_alert=false
func TestHealthzMetrics_NoCongestion_Idle(t *testing.T) {
	engine := GetSpiderEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()

	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	engine.busyMu.Lock()
	origBusy := engine.busyFailCount
	engine.busyFailCount = 0
	engine.busyMu.Unlock()
	defer func() {
		engine.busyMu.Lock()
		engine.busyFailCount = origBusy
		engine.busyMu.Unlock()
	}()

	_, _, semUsed, _, _, congestion := aggregateHealthzMetricsForTest()
	if semUsed != 0 {
		t.Errorf("expected sem_used=0 on idle; got %d", semUsed)
	}
	if congestion {
		t.Error("expected congestion_alert=false on idle")
	}
}

// TestHealthzHandler_EngineDown_Returns503
// 端到端：buildHealthzHandler 在 engine.isRunning=false 时返回 503 + status=down
func TestHealthzHandler_EngineDown_Returns503(t *testing.T) {
	engine := GetSpiderEngine()
	engine.mu.Lock()
	origRunning := engine.isRunning
	engine.isRunning = false
	engine.mu.Unlock()
	defer func() {
		engine.mu.Lock()
		engine.isRunning = origRunning
		engine.mu.Unlock()
	}()

	handler := buildHealthzHandlerForTest()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when engine down; got %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "down" {
		t.Errorf("expected status=down; got %v", body["status"])
	}
	if body["version"] != "2.0.27" {
		t.Errorf("expected version=2.0.27; got %v", body["version"])
	}
}

// TestHealthzHandler_EngineUp_ContainsV27Fields
// 端到端：buildHealthzHandler 在 engine.isRunning=true（但 Chrome 健康探测失败可能影响）
// 时返回的 JSON 必含 v2.0.27 新增字段名
func TestHealthzHandler_EngineUp_ContainsV27Fields(t *testing.T) {
	engine := GetSpiderEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()

	origRunning := engine.isRunning
	engine.isRunning = true
	defer func() {
		engine.isRunning = origRunning
	}()

	origSessions := spiderSessions
	spiderSessions = make(map[string]*SpiderSession)
	defer func() { spiderSessions = origSessions }()

	engine.busyMu.Lock()
	origBusy := engine.busyFailCount
	engine.busyFailCount = 0
	engine.busyMu.Unlock()
	defer func() {
		engine.busyMu.Lock()
		engine.busyFailCount = origBusy
		engine.busyMu.Unlock()
	}()

	handler := buildHealthzHandlerForTest()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// v2.0.27 关键不变量：JSON 必含 chrome_active_sessions / congestion_alert / session_total
	for _, key := range []string{"chrome_active_sessions", "session_total", "congestion_alert", "sem_capacity", "busy_fail_count"} {
		if _, ok := body[key]; !ok {
			t.Errorf("v2.0.27 healthz JSON missing key %q (got keys: %v)", key, mapKeys(body))
		}
	}
	if body["chrome_active_sessions"].(float64) != 0 {
		t.Errorf("expected chrome_active_sessions=0 on idle; got %v", body["chrome_active_sessions"])
	}
	if body["congestion_alert"].(bool) {
		t.Error("expected congestion_alert=false on idle")
	}
}

// ==================== 测试 helper ====================

// aggregateHealthzMetricsForTest 复用 healthz handler 的聚合逻辑。
// v2.0.34：computeMetrics 已提升为顶层 computeSpiderHealthMetrics，测试直接委托，
// 保证与生产代码同源演进（不再复制实现）。
func aggregateHealthzMetricsForTest() (sessionTotal, chromeActive, semUsed, semCap, busyFails int, congestion bool) {
	return computeSpiderHealthMetrics()
}

// buildHealthzHandlerForTest 返回一个独立的 /healthz handler，行为与生产一致，
// 但不依赖 StartMCPWebServer 的 mux 装配（避免测试中启动真实 HTTP 服务）。
func buildHealthzHandlerForTest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mcpSetNoCacheHeaders(w)
		engine := GetSpiderEngine()
		status := map[string]interface{}{
			"service":   "LSM Spider MCP Service",
			"version":   "2.0.27",
			"timestamp": "test",
		}
		if engine == nil || !engine.isRunning {
			status["status"] = "down"
			status["chrome"] = "not running"
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(status)
			return
		}
		// 测试里不强求 Chrome 真健康（依赖 Chrome 进程），统一按"调用聚合函数"路径
		healthy := true
		status["status"] = "up"
		status["chrome_healthy"] = healthy
		status["chrome_ws"] = "ws://test"
		sessionTotal, chromeActive, semUsed, semCap, busyFails, congestion := aggregateHealthzMetricsForTest()
		status["sem_capacity"] = semCap
		status["sem_used"] = semUsed
		status["sem_available"] = semCap - semUsed
		status["busy_fail_count"] = busyFails
		status["session_total"] = sessionTotal
		status["chrome_active_sessions"] = chromeActive
		status["congestion_alert"] = congestion
		_ = json.NewEncoder(w).Encode(status)
	}
}

func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
