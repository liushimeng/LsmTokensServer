package models

// v2.0.83: 服务质量监控聚合（mysql_http_agent_qos_stats.go）单元测试。
//
// 覆盖：基准推理速度 / 拥堵系数分档与卡顿率 / 错误分类率 / 小时-天桶与热力图
// 对齐 / 周 P50/P95 分位 / 用户隔离 / 空库零值 / 60s 缓存命中。
// 直接调用 buildQoSMonitorReport（绕过缓存），缓存行为单独验证。

import (
	"math"
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

// v2.0.84 时间维度数据质量优化测试
// ---------------------------------------------------------------

// Token 分档基准：小请求与大请求各自归一化后拥堵系数趋近 1，
// 不再被全局 Σ/Σ 基准把小请求放大数倍。
func TestQoSMonitorReport_TokenBandBaseline(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-band-model"
	// 30 笔小请求：3000ms / 100 tok（分档基准 30ms/token）
	for i := 0; i < 30; i++ {
		insertQoSFixture(t, "qos-b1", "qos-bm1", dst, "200 OK", now.Add(-time.Duration(i+5)*time.Minute), 3000, 100)
	}
	// 30 笔大请求：20000ms / 4000 tok（分档基准 5ms/token）
	for i := 0; i < 30; i++ {
		insertQoSFixture(t, "qos-b1", "qos-bm1", dst, "200 OK", now.Add(-time.Duration(i+5)*time.Minute), 20000, 4000)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	// 全局基准 = (30×3000 + 30×20000) / (30×100 + 30×4000) ≈ 5.61 ms/token
	if math.Abs(m.BaselineMsPerToken-5.61) > 0.01 {
		t.Errorf("global baseline = %v, want ≈5.61", m.BaselineMsPerToken)
	}
	if m.BaselineBands != 2 {
		t.Errorf("calibrated bands = %d, want 2", m.BaselineBands)
	}
	// 两类请求各自系数 1.0 → 桶均值 1.0（全局基准会让小请求达 5.35）
	lastHour := m.Hours[len(m.Hours)-1]
	if math.Abs(lastHour.AvgCongestion-1.0) > 0.01 {
		t.Errorf("band-normalized congestion = %v, want 1.0", lastHour.AvgCongestion)
	}
	if lastHour.CoefficientSamples != 60 {
		t.Errorf("coefficient samples = %v, want 60", lastHour.CoefficientSamples)
	}
	if lastHour.StallRate != 0 {
		t.Errorf("stall rate = %v, want 0", lastHour.StallRate)
	}
}

// 成功时延与全量时延分离：429 快速失败不再拉低成功时延。
func TestQoSMonitorReport_SuccessLatencySplit(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-split-model"
	insertQoSFixture(t, "qos-u3", "qos-m3", dst, "200 OK", now.Add(-time.Minute), 5000, 100)
	insertQoSFixture(t, "qos-u3", "qos-m3", dst, "429 Too Many Requests", now.Add(-2*time.Minute), 50, 0)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	lastHour := m.Hours[len(m.Hours)-1]

	if lastHour.Count != 2 || lastHour.SuccessCount != 1 {
		t.Errorf("count/success = %d/%d, want 2/1", lastHour.Count, lastHour.SuccessCount)
	}
	if lastHour.OkAvgElapsedMs != 5000 {
		t.Errorf("ok avg elapsed = %v, want 5000", lastHour.OkAvgElapsedMs)
	}
	if lastHour.AvgElapsedMs != 2525 {
		t.Errorf("all avg elapsed = %v, want 2525", lastHour.AvgElapsedMs)
	}
	if lastHour.ErrorRate != 50 {
		t.Errorf("error rate = %v, want 50", lastHour.ErrorRate)
	}
	if lastHour.Error429 != 1 {
		t.Errorf("error 429 = %v, want 1", lastHour.Error429)
	}
	// 成功率口径：成功 1 笔无 429 干扰
	if m.Live.SuccessCount != 1 || m.Live.OkAvgElapsedMs != 5000 {
		t.Errorf("live success/ok avg = %d/%d, want 1/5000", m.Live.SuccessCount, m.Live.OkAvgElapsedMs)
	}
}

// 抖动率 = 拥堵系数变异系数（无量纲 %）。
func TestQoSCV_Jitter(t *testing.T) {
	if v := qosCV([]float64{2, 2, 2}); v != 0 {
		t.Errorf("constant series CV = %v, want 0", v)
	}
	// [1,2]：均值 1.5、总体标准差 0.5 → CV = 1/3
	if v := qosCV([]float64{1, 2}); math.Abs(v-1.0/3) > 1e-9 {
		t.Errorf("CV([1,2]) = %v, want 1/3", v)
	}
	if v := qosCV([]float64{1}); v != 0 {
		t.Errorf("single sample CV = %v, want 0", v)
	}
}

// 进行中时间桶标记：当前分钟/小时/天桶 partial=true，历史桶 false。
func TestQoSMonitorReport_PartialBuckets(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-partial-model"
	insertQoSFixture(t, "qos-u4", "qos-m4", dst, "200 OK", now.Add(-2*time.Minute), 1000, 100)
	insertQoSFixture(t, "qos-u4", "qos-m4", dst, "200 OK", now.Add(-26*time.Hour), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if !m.Minutes[len(m.Minutes)-1].Partial {
		t.Errorf("current minute bucket should be partial")
	}
	if m.Minutes[0].Partial {
		t.Errorf("oldest minute bucket should not be partial")
	}
	if !m.Hours[len(m.Hours)-1].Partial {
		t.Errorf("current hour bucket should be partial")
	}
	if m.Hours[0].Partial {
		t.Errorf("oldest hour bucket should not be partial")
	}
	if !m.Days[len(m.Days)-1].Partial {
		t.Errorf("today bucket should be partial")
	}
	if m.Days[0].Partial {
		t.Errorf("oldest day bucket should not be partial")
	}
	// 7 天序列日期连续（末位 = 今天）
	wantToday := now.Format("2006-01-02")
	if m.Days[len(m.Days)-1].Bucket != wantToday {
		t.Errorf("last day bucket = %q, want %q", m.Days[len(m.Days)-1].Bucket, wantToday)
	}
	if m.Days[0].Bucket != now.AddDate(0, 0, -6).Format("2006-01-02") {
		t.Errorf("first day bucket = %q, want 6 days ago", m.Days[0].Bucket)
	}
}

// 实时窗口阶梯：5 分钟样本不足时回退 15/60 分钟，并返回实际窗口与最后请求时间。
func TestQoSMonitorReport_LiveWindowLadder(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-live-model"
	// 5 分钟窗口内只有 3 笔（< qosLiveMinSamples=5），15 分钟窗口补足到 6 笔
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-u5", "qos-m5", dst, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), 1000, 100)
	}
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-u5", "qos-m5", dst, "200 OK", now.Add(-time.Duration(i+8)*time.Minute), 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	if m.Live.WindowMinutes != 15 {
		t.Errorf("live window = %d, want 15", m.Live.WindowMinutes)
	}
	if m.Live.ReqCount < 6 {
		t.Errorf("live req count = %d, want >= 6", m.Live.ReqCount)
	}
	if m.Live.LastRequestAtMs == 0 {
		t.Errorf("live last request at should be set")
	}
}

// 只有 20 分钟前的数据：5/15 分钟窗口为空 → 回退 60 分钟。
func TestQoSMonitorReport_LiveWindowFallback60(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-live60-model"
	insertQoSFixture(t, "qos-u6", "qos-m6", dst, "200 OK", now.Add(-20*time.Minute), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	if m.Live.WindowMinutes != 60 {
		t.Errorf("live window = %d, want 60", m.Live.WindowMinutes)
	}
	if m.Live.ReqCount != 1 {
		t.Errorf("live req count = %d, want 1", m.Live.ReqCount)
	}
	if m.Status == "idle" {
		t.Errorf("model with 20min-old traffic should not be idle")
	}
}

// 周成功分位：错误请求不进入 week_ok_* 口径。
func TestQoSMonitorReport_WeekOkPercentiles(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-weekok-model"
	for i, e := range []int64{100, 200, 300, 400, 500} {
		insertQoSFixture(t, "qos-u7", "qos-m7", dst, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), e, 10)
	}
	// 错误请求（10ms 快速失败）只进全量口径
	insertQoSFixture(t, "qos-u7", "qos-m7", dst, "500 Internal Server Error", now.Add(-time.Minute), 10, 0)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	if m.WeekOkP50Ms != 300 {
		t.Errorf("week ok p50 = %v, want 300", m.WeekOkP50Ms)
	}
	if m.WeekOkP95Ms != 500 {
		t.Errorf("week ok p95 = %v, want 500", m.WeekOkP95Ms)
	}
	if m.WeekP50Ms == m.WeekOkP50Ms {
		t.Errorf("all-week p50 should include error sample and differ from ok-only p50")
	}
	if m.OkCalls7d != 5 || m.ErrCalls7d != 1 {
		t.Errorf("ok/err calls 7d = %d/%d, want 5/1", m.OkCalls7d, m.ErrCalls7d)
	}
}

// GetQoSMonitorReport 30s 缓存命中（同键返回同一实例指针）。
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
