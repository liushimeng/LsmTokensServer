package models

// v2.0.83: 服务质量监控聚合（mysql_http_agent_qos_stats.go）单元测试。
//
// 覆盖：基准推理速度 / 拥堵系数分档与卡顿率 / 错误分类率 / 小时-天桶与热力图
// 对齐 / 周 P50/P95 分位 / 用户隔离 / 空库零值 / 60s 缓存命中。
// 直接调用 buildQoSMonitorReport（绕过缓存），缓存行为单独验证。

import (
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

// insertQoSFixture 向指定 (user, model) 哈希分表插入一条 QoS 测试记录。
// ts 同时写 CreatedAt 与 RequestStartAt（后者不得晚于前者）。
func insertQoSFixture(t *testing.T, userName, modelName, dstModelName, status string, ts time.Time, elapsed int64, output uint64) {
	t.Helper()
	tableName := GetAgentHttpTableName(userName, modelName, testCfg.DBMysqlSubTableNumber)
	item := &TAgentHttpTransactionDataItem{
		CreatedAt:        ts,
		UpdatedAt:        ts,
		RequestStartAt:   ts,
		UserName:         userName,
		ModelName:        modelName,
		DstModelName:     dstModelName,
		ResponseStatus:   status,
		ElapsedMs:        elapsed,
		TokensOutputSize: output,
		TokensAllSize:    output,
	}
	if err := database.DB.Table(tableName).Create(item).Error; err != nil {
		t.Fatalf("insert qos fixture %s/%s failed: %v", userName, modelName, err)
	}
}

// findQoSReport 在报告中定位指定目标模型。
func findQoSReport(t *testing.T, report *QoSMonitorReport, dstModelName string) *QoSModelReport {
	t.Helper()
	for i := range report.Models {
		if report.Models[i].DstModelName == dstModelName {
			return &report.Models[i]
		}
	}
	t.Fatalf("dst model %q not found in report (%d models)", dstModelName, len(report.Models))
	return nil
}

// 基准推理速度 + 实时拥堵系数 + 分档状态 + 错误率 + 抖动率 + 热力图落位。
func TestQoSMonitorReport_BaselineAndLive(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-baseline-model"
	// 6 天前成功请求：1000ms / 100 tok
	insertQoSFixture(t, "qos-u1", "qos-m1", dst, "200 OK", now.Add(-6*24*time.Hour), 1000, 100)
	// 5 天前成功请求：2000ms / 200 tok
	insertQoSFixture(t, "qos-u1", "qos-m1", dst, "200 OK", now.Add(-5*24*time.Hour), 2000, 200)
	// 2 分钟前拥堵请求：4500ms / 100 tok
	insertQoSFixture(t, "qos-u1", "qos-m1", dst, "200 OK", now.Add(-2*time.Minute), 4500, 100)
	// 3 分钟前 429 限流：无输出 Token，不参与拥堵与基准
	insertQoSFixture(t, "qos-u1", "qos-m1", dst, "429 Too Many Requests", now.Add(-3*time.Minute), 100, 0)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	// 基准 = (1000+2000+4500) / (100+200+100) = 18.75 ms/Token（滚动 7 天含近期样本）
	if m.BaselineMsPerToken != 18.75 {
		t.Errorf("baseline = %v, want 18.75", m.BaselineMsPerToken)
	}
	if m.BaselineSamples != 3 {
		t.Errorf("baseline samples = %v, want 3", m.BaselineSamples)
	}
	// 实时窗口（近 5 分钟）只含 4500ms 与 429 两笔
	if m.Live.ReqCount != 2 {
		t.Errorf("live req count = %v, want 2", m.Live.ReqCount)
	}
	// 拥堵系数 = 4500 / (18.75 × 100) = 2.4 → 中度排队（2~5）
	if m.Live.Coefficient != 2.4 {
		t.Errorf("live coefficient = %v, want 2.4", m.Live.Coefficient)
	}
	if m.Status != "moderate" {
		t.Errorf("status = %q, want moderate", m.Status)
	}
	if !m.Alert {
		t.Errorf("alert should be true (coefficient 2.4 > 2.0)")
	}
	// 错误率 = 1/2 = 50%
	if m.Live.ErrorRate != 50 {
		t.Errorf("live error rate = %v, want 50", m.Live.ErrorRate)
	}
	// 平均时延 = (4500+100)/2 = 2300；抖动率 = 2200/2300×100 ≈ 95.65
	if m.Live.AvgElapsedMs != 2300 {
		t.Errorf("live avg elapsed = %v, want 2300", m.Live.AvgElapsedMs)
	}

	// 今天的小时桶（尾桶）：2 笔请求、卡顿率 100%（唯一拥堵样本 2.4 > 2.0）
	today := m.Days[len(m.Days)-1]
	if today.Count != 2 {
		t.Errorf("today bucket count = %v, want 2", today.Count)
	}
	if today.StallRate != 100 {
		t.Errorf("today stall rate = %v, want 100", today.StallRate)
	}
	if today.AvgCongestion != 2.4 {
		t.Errorf("today avg congestion = %v, want 2.4", today.AvgCongestion)
	}
	if today.Error429 != 1 || today.Error5xx != 0 || today.ErrorOther != 0 {
		t.Errorf("today errors 429/5xx/other = %v/%v/%v, want 1/0/0", today.Error429, today.Error5xx, today.ErrorOther)
	}
	// 热力图：今天行（下标 6）当前小时列
	if m.Heatmap[6][now.Hour()] != 2.4 {
		t.Errorf("heatmap today cell = %v, want 2.4", m.Heatmap[6][now.Hour()])
	}
	if m.HeatmapCounts[6][now.Hour()] != 2 {
		t.Errorf("heatmap today count = %v, want 2", m.HeatmapCounts[6][now.Hour()])
	}
	// 分钟序列长度固定 60
	if len(m.Minutes) != qosMinuteBuckets {
		t.Errorf("minutes length = %d, want %d", len(m.Minutes), qosMinuteBuckets)
	}
}

// 周级 P50/P95 与桶级 P95 分位正确性。
func TestQoSMonitorReport_Percentiles(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-pct-model"
	elapsed := []int64{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000}
	for i, e := range elapsed {
		insertQoSFixture(t, "qos-u2", "qos-m2", dst, "200 OK", now.Add(-time.Duration(len(elapsed)-i)*time.Minute), e, 10)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if m.WeekP50Ms != 500 {
		t.Errorf("week p50 = %v, want 500", m.WeekP50Ms)
	}
	if m.WeekP95Ms != 1000 {
		t.Errorf("week p95 = %v, want 1000", m.WeekP95Ms)
	}
	if m.Calls7d != 10 {
		t.Errorf("calls 7d = %v, want 10", m.Calls7d)
	}
	// 最近一小时桶聚合了全部 10 笔（都在 10 分钟内）
	lastHour := m.Hours[len(m.Hours)-1]
	if lastHour.Count != 10 {
		t.Errorf("last hour count = %v, want 10", lastHour.Count)
	}
	if lastHour.P95ElapsedMs != 1000 {
		t.Errorf("last hour p95 = %v, want 1000", lastHour.P95ElapsedMs)
	}
	if lastHour.AvgElapsedMs != 550 {
		t.Errorf("last hour avg = %v, want 550", lastHour.AvgElapsedMs)
	}
}

// 用户端隔离：user_name 过滤后不串入他人数据。
func TestQoSMonitorReport_UserIsolation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	insertQoSFixture(t, "qos-alice", "qos-am", "qos-alice-dst", "200 OK", now.Add(-time.Minute), 1000, 100)
	// 构造同分表碰撞的干扰数据（他人）
	targetTable := GetAgentHttpTableName("qos-alice", "qos-am", testCfg.DBMysqlSubTableNumber)
	bobModel := findCollidingModel(t, targetTable, "qos-bob")
	insertQoSFixture(t, "qos-bob", bobModel, "qos-bob-dst", "200 OK", now.Add(-time.Minute), 9999, 100)

	userReport, err := buildQoSMonitorReport("qos-alice", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport(alice) failed: %v", err)
	}
	if len(userReport.Models) != 1 || userReport.Models[0].DstModelName != "qos-alice-dst" {
		t.Fatalf("user report should only contain alice's dst model, got %+v", userReport.Models)
	}

	allReport, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport(all) failed: %v", err)
	}
	findQoSReport(t, allReport, "qos-alice-dst")
	findQoSReport(t, allReport, "qos-bob-dst")
}

// 空数据模型返回零值报告不 panic。
func TestQoSMonitorReport_Empty(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	report, err := buildQoSMonitorReport("qos-nobody", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	if report.ModelCount != 0 || len(report.Models) != 0 {
		t.Errorf("empty report should have no models, got %d", report.ModelCount)
	}
}

// 状态分档与告警阈值边界（纯函数）。
func TestQoSStatusOf_Tiers(t *testing.T) {
	cases := []struct {
		live    QoSLiveStats
		status  string
		alerted bool
	}{
		{QoSLiveStats{}, "idle", false},
		{QoSLiveStats{ReqCount: 5, Coefficient: 1.0}, "ok", false},
		{QoSLiveStats{ReqCount: 5, Coefficient: 1.2}, "light", false}, // 1.2 落在「1.2~2.0 轻度」
		{QoSLiveStats{ReqCount: 5, Coefficient: 2.0}, "moderate", false},
		{QoSLiveStats{ReqCount: 5, Coefficient: 5.0}, "moderate", true}, // 5.0 落在「2.0~5.0 中度」（严重为 >5.0）
		{QoSLiveStats{ReqCount: 5, Coefficient: 5.01}, "severe", true},
		{QoSLiveStats{ReqCount: 5, Coefficient: 1.0, ErrorRate: 6}, "severe", true}, // 错误率主导
		{QoSLiveStats{ReqCount: 5, Coefficient: 1.5}, "light", false},
	}
	for i, c := range cases {
		status, alert := qosStatusOf(c.live)
		if status != c.status || alert != c.alerted {
			t.Errorf("case %d: got (%q,%v), want (%q,%v)", i, status, alert, c.status, c.alerted)
		}
	}
}

// GetQoSMonitorReport 60s 缓存命中（同键返回同一实例指针）。
func TestGetQoSMonitorReport_Cache(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	const user = "qos-cache-user"
	r1, err := GetQoSMonitorReport(user, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("first GetQoSMonitorReport failed: %v", err)
	}
	r2, err := GetQoSMonitorReport(user, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("second GetQoSMonitorReport failed: %v", err)
	}
	if r1 != r2 {
		t.Errorf("second call should hit 60s cache and return the same report instance")
	}
}
