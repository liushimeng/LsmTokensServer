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
	// 两类请求各自系数 1.0 → 桶均值 1.0（全局基准会让小请求达 5.35）。
	// 注意：60 笔样本横跨「当前小时」与「上一小时」（取决于运行时刻的分钟数），
	// 因此用「今天」天桶断言，避免测试随执行时刻漂移（历史用例曾按 lastHour 断言而偶发失败）。
	today := m.Days[len(m.Days)-1]
	if math.Abs(today.AvgCongestion-1.0) > 0.01 {
		t.Errorf("band-normalized congestion = %v, want 1.0", today.AvgCongestion)
	}
	if today.CoefficientSamples != 60 {
		t.Errorf("coefficient samples = %v, want 60", today.CoefficientSamples)
	}
	if today.StallRate != 0 {
		t.Errorf("stall rate = %v, want 0", today.StallRate)
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

// v2.0.85 固定窗口统计：stats_24h / stats_7d 与小时桶同一套成功口径。
func TestQoSMonitorReport_WindowStats(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-winstats-model"
	// 近 24h：1 笔 200（5000ms/100tok）+ 1 笔 429（50ms 快速失败）
	insertQoSFixture(t, "qos-u8", "qos-m8", dst, "200 OK", now.Add(-1*time.Minute), 5000, 100)
	insertQoSFixture(t, "qos-u8", "qos-m8", dst, "429 Too Many Requests", now.Add(-2*time.Minute), 50, 0)
	// 3 天前的成功请求只进 7d 口径
	insertQoSFixture(t, "qos-u8", "qos-m8", dst, "200 OK", now.Add(-3*24*time.Hour), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if m.Stats24h.Window != "24h" || m.Stats7d.Window != "7d" {
		t.Errorf("window tags = %q/%q, want 24h/7d", m.Stats24h.Window, m.Stats7d.Window)
	}
	if m.Stats24h.Calls != 2 {
		t.Errorf("stats24h calls = %v, want 2", m.Stats24h.Calls)
	}
	if m.Stats24h.SuccessRate != 50 {
		t.Errorf("stats24h success rate = %v, want 50", m.Stats24h.SuccessRate)
	}
	if m.Stats24h.ErrorRate != 50 {
		t.Errorf("stats24h error rate = %v, want 50", m.Stats24h.ErrorRate)
	}
	if m.Stats24h.OkAvgElapsedMs != 5000 {
		t.Errorf("stats24h ok avg = %v, want 5000（429 快速失败不得拉低成功时延）", m.Stats24h.OkAvgElapsedMs)
	}
	if m.Stats7d.Calls != 3 {
		t.Errorf("stats7d calls = %v, want 3", m.Stats7d.Calls)
	}
	if m.Stats7d.SuccessRate != qosRound2(2.0/3.0*100) {
		t.Errorf("stats7d success rate = %v, want %.2f", m.Stats7d.SuccessRate, qosRound2(2.0/3.0*100))
	}
}

// v2.0.85 下一时段预测：昨天同小时 12 笔同规格请求（系数 1.0，样本 12 → 可信度中）。
func TestQoSMonitorReport_PredictionNextHour(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-predict-model"
	nextHour := (now.Hour() + 1) % 24
	// now-23h = 昨天「当前小时+1」的同一分钟 → 落入目标小时桶
	yesterdaySameHour := now.Add(-23 * time.Hour)
	if yesterdaySameHour.Hour() != nextHour {
		t.Fatalf("fixture hour = %d, want next hour %d", yesterdaySameHour.Hour(), nextHour)
	}
	for i := 0; i < 12; i++ {
		insertQoSFixture(t, "qos-u9", "qos-m9", dst, "200 OK", yesterdaySameHour, 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if m.Prediction.NextHour.Hour != nextHour {
		t.Errorf("next hour = %d, want %d", m.Prediction.NextHour.Hour, nextHour)
	}
	// 全部样本同规格：band(100tok) 基准 = 全局基准 = 10ms/tok → 系数 = 1.0
	if m.Prediction.NextHour.AvgCongestion != 1.0 {
		t.Errorf("next hour congestion = %v, want 1.0", m.Prediction.NextHour.AvgCongestion)
	}
	if m.Prediction.NextHour.Samples != 12 {
		t.Errorf("next hour samples = %v, want 12", m.Prediction.NextHour.Samples)
	}
	if m.Prediction.NextHour.Confidence != "medium" {
		t.Errorf("next hour confidence = %q, want medium（12 ≥ 10）", m.Prediction.NextHour.Confidence)
	}

	// 今日剩余：长度为 23 - 当前小时，hour 连续递增
	wantLen := 23 - now.Hour()
	if len(m.Prediction.TodayRemaining) != wantLen {
		t.Fatalf("today remaining len = %d, want %d", len(m.Prediction.TodayRemaining), wantLen)
	}
	for i, f := range m.Prediction.TodayRemaining {
		if f.Hour != now.Hour()+1+i {
			t.Errorf("today remaining[%d].hour = %d, want %d", i, f.Hour, now.Hour()+1+i)
		}
	}
	if len(m.Prediction.TodayRemaining) > 0 && m.Prediction.TodayRemaining[0].AvgCongestion != 1.0 {
		t.Errorf("today remaining[0] congestion = %v, want 1.0", m.Prediction.TodayRemaining[0].AvgCongestion)
	}

	// 明日预测：昨天是唯一完整天 → 系数 1.0、样本 12、可信度中、预计调用 12 次
	tomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	if m.Prediction.Tomorrow.Date != tomorrow.Format("2006-01-02") {
		t.Errorf("tomorrow date = %q, want %q", m.Prediction.Tomorrow.Date, tomorrow.Format("2006-01-02"))
	}
	if m.Prediction.Tomorrow.Weekday != int(tomorrow.Weekday()) {
		t.Errorf("tomorrow weekday = %d, want %d", m.Prediction.Tomorrow.Weekday, int(tomorrow.Weekday()))
	}
	if m.Prediction.Tomorrow.AvgCongestion != 1.0 {
		t.Errorf("tomorrow congestion = %v, want 1.0", m.Prediction.Tomorrow.AvgCongestion)
	}
	if m.Prediction.Tomorrow.Confidence != "medium" {
		t.Errorf("tomorrow confidence = %q, want medium", m.Prediction.Tomorrow.Confidence)
	}
	if m.Prediction.Tomorrow.Calls != 12 {
		t.Errorf("tomorrow calls = %v, want 12", m.Prediction.Tomorrow.Calls)
	}
}

// v2.0.85 趋势研判：仅近 24h 有数据 → unknown；两窗口等量同规格 → flat。
func TestQoSMonitorReport_PredictionTrend(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dstA = "qos-trend-a"
	// 只有近 24h 数据：前 24h 无样本 → unknown
	for i := 0; i < 5; i++ {
		insertQoSFixture(t, "qos-ua", "qos-ma", dstA, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), 1000, 100)
	}

	const dstB = "qos-trend-b"
	// 近 24h 与前 24h 各 5 笔同规格（系数同为 1.0）→ flat
	// （前窗样本取 now-24h-5min，避开整点分钟恰好落入后窗的边界）
	for i := 0; i < 5; i++ {
		insertQoSFixture(t, "qos-ub", "qos-mb", dstB, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), 1000, 100)
		insertQoSFixture(t, "qos-ub", "qos-mb", dstB, "200 OK", now.Add(-24*time.Hour-time.Duration(i+1)*5*time.Minute), 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	if got := findQoSReport(t, report, dstA).Prediction.Trend24h; got != "unknown" {
		t.Errorf("trend A = %q, want unknown（前 24h 无样本）", got)
	}
	if got := findQoSReport(t, report, dstB).Prediction.Trend24h; got != "flat" {
		t.Errorf("trend B = %q, want flat（两窗口同规格等量）", got)
	}
}

// v2.0.85 今日 vs 周均值：今日拥堵显著高于完整天 → 正偏差；
// 只有今天数据的模型 → 明日预测可信度 low 且无系数。
func TestQoSMonitorReport_PredictionTodayVsWeek(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-today-week-model"
	// 昨天：1000ms/100tok ×5 + 10000ms/1000tok ×5 → 全局基准 10ms/tok，系数 1.0
	yesterday := now.Add(-24 * time.Hour)
	for i := 0; i < 5; i++ {
		insertQoSFixture(t, "qos-uc", "qos-mc", dst, "200 OK", yesterday.Add(-time.Duration(i)*time.Minute), 1000, 100)
		insertQoSFixture(t, "qos-uc", "qos-mc", dst, "200 OK", yesterday.Add(-time.Duration(i)*time.Minute), 10000, 1000)
	}
	// 今天：2000ms/100tok ×5 → 系数 ≈ 2000/(12.35×100) ≈ 1.62，显著高于昨天 0.81
	for i := 0; i < 5; i++ {
		insertQoSFixture(t, "qos-uc", "qos-mc", dst, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), 2000, 100)
	}

	const dst2 = "qos-only-today"
	insertQoSFixture(t, "qos-ud", "qos-md", dst2, "200 OK", now.Add(-time.Minute), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	if m.Prediction.TodayVsWeek <= 0 {
		t.Errorf("today vs week = %v, want > 0（今日 1.62 vs 昨天 0.81）", m.Prediction.TodayVsWeek)
	}

	m2 := findQoSReport(t, report, dst2)
	if m2.Prediction.Tomorrow.Confidence != "low" || m2.Prediction.Tomorrow.AvgCongestion != 0 {
		t.Errorf("no-full-day tomorrow = conf %q cong %v, want low/0",
			m2.Prediction.Tomorrow.Confidence, m2.Prediction.Tomorrow.AvgCongestion)
	}
	if m2.Prediction.Trend24h != "unknown" {
		t.Errorf("no-full-day trend = %q, want unknown", m2.Prediction.Trend24h)
	}
}

// ===================== v2.0.86 阶段DA：时段画像 / 时段带 / 峰谷 / 全站汇总 =====================

// 构造某一天某小时的固定时间点（本地时区）。
func qosDayHour(now time.Time, daysAgo, hour int) time.Time {
	d := now.AddDate(0, 0, -daysAgo)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 30, 0, 0, now.Location())
}

// 小时画像：近 7 天同一小时各 1 笔 → calls=7、日均=1、系数=1.0。
func TestQoSMonitorReport_HourProfile(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-profile-model"
	const hour = 14
	for d := 0; d < 7; d++ {
		// 1000ms / 100 token → 系数 1.0（全局基准 = 1000/100 = 10ms/Token）
		insertQoSFixture(t, "qos-u9", "qos-m9", dst, "200 OK", qosDayHour(now, d, hour), 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if len(m.HourProfile) != 24 {
		t.Fatalf("hour profile len = %d, want 24", len(m.HourProfile))
	}
	p := m.HourProfile[hour]
	if p.Calls != 7 {
		t.Errorf("profile[%d].calls = %d, want 7", hour, p.Calls)
	}
	if p.Days != 7 {
		t.Errorf("profile[%d].days = %d, want 7", hour, p.Days)
	}
	if p.AvgCallsPerDay != 1 {
		t.Errorf("profile[%d].avg_calls_per_day = %v, want 1", hour, p.AvgCallsPerDay)
	}
	if p.AvgCongestion != 1 {
		t.Errorf("profile[%d].avg_congestion = %v, want 1", hour, p.AvgCongestion)
	}
	// 无流量的小时必须是 0 调用、0 系数
	if m.HourProfile[3].Calls != 0 || m.HourProfile[3].AvgCongestion != 0 {
		t.Errorf("profile[3] = calls %d / cong %v, want 0/0", m.HourProfile[3].Calls, m.HourProfile[3].AvgCongestion)
	}
}

// 时段带归并：上午带（9-11）含 9/10/11 三点；午间带（12-13）两点；Share 之和 ≈ 100%。
func TestQoSMonitorReport_TimeBands(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-band-model"
	// 上午带：9/10/11 各 1 笔；午间带：12 时 2 笔、13 时 1 笔
	for _, h := range []int{9, 10, 11} {
		insertQoSFixture(t, "qos-u10", "qos-m10", dst, "200 OK", qosDayHour(now, 0, h), 1000, 100)
	}
	insertQoSFixture(t, "qos-u10", "qos-m10", dst, "200 OK", qosDayHour(now, 0, 12), 1000, 100)
	insertQoSFixture(t, "qos-u10", "qos-m10", dst, "200 OK", qosDayHour(now, 0, 12), 1000, 100)
	insertQoSFixture(t, "qos-u10", "qos-m10", dst, "200 OK", qosDayHour(now, 0, 13), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if len(m.Bands7d) != len(qosTimeBands) {
		t.Fatalf("bands7d len = %d, want %d", len(m.Bands7d), len(qosTimeBands))
	}
	byKey := map[string]QoSTimeBand{}
	for _, b := range m.Bands7d {
		byKey[b.Key] = b
	}
	if got := byKey["forenoon"].Calls; got != 3 {
		t.Errorf("forenoon calls = %d, want 3（9/10/11 三点）", got)
	}
	if got := byKey["noon"].Calls; got != 3 {
		t.Errorf("noon calls = %d, want 3（12 时 2 笔 + 13 时 1 笔）", got)
	}
	if got := byKey["forenoon"].StartHour; got != 9 {
		t.Errorf("forenoon start hour = %d, want 9", got)
	}
	if got := byKey["forenoon"].EndHour; got != 12 {
		t.Errorf("forenoon end hour = %d, want 12", got)
	}
	// Share 之和 ≈ 100%
	var shareSum float64
	for _, b := range m.Bands7d {
		shareSum += b.Share
	}
	if shareSum < 99 || shareSum > 101 {
		t.Errorf("band share sum = %v, want ~100", shareSum)
	}
}

// 时段带时长差异：凌晨带（6h）总调用多于午间带（2h）时，
// 「每小时强度」口径必须把两者区分开（否则"带越长数越大"会误导比较）。
func TestQoSMonitorReport_TimeBandLengthSemantics(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-bandlen-model"
	// 凌晨带 6 小时各 1 笔 = 6 笔（1 笔/小时）；午间带 12 时 3 笔 = 3 笔（1.5 笔/小时）
	for _, h := range []int{0, 1, 2, 3, 4, 5} {
		insertQoSFixture(t, "qos-u11", "qos-m11", dst, "200 OK", qosDayHour(now, 0, h), 1000, 100)
	}
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-u11", "qos-m11", dst, "200 OK", qosDayHour(now, 0, 12), 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	byKey := map[string]QoSTimeBand{}
	for _, b := range m.Bands7d {
		byKey[b.Key] = b
	}
	dawn, noon := byKey["dawn"], byKey["noon"]
	if dawn.Calls != 6 || noon.Calls != 3 {
		t.Fatalf("dawn/noon calls = %d/%d, want 6/3", dawn.Calls, noon.Calls)
	}
	// 总调用 凌晨(6) > 午间(3)，但每小时强度 凌晨(1.0) < 午间(1.5)
	if !(dawn.AvgCallsPerHour < noon.AvgCallsPerHour) {
		t.Errorf("avg_calls_per_hour dawn=%v noon=%v, want dawn < noon（按时长归一）",
			dawn.AvgCallsPerHour, noon.AvgCallsPerHour)
	}
	// 日均口径 = 该带每天的调用总量（贡献度），凌晨 6 > 午间 3
	if !(dawn.AvgCallsPerDay > noon.AvgCallsPerDay) {
		t.Errorf("avg_calls_per_day dawn=%v noon=%v, want dawn > noon", dawn.AvgCallsPerDay, noon.AvgCallsPerDay)
	}
}

// 峰谷识别：14 时为峰、3 时为谷；零流量的小时不得被选为谷。
func TestQoSMonitorReport_PeakValley(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-peak-model"
	for i := 0; i < 10; i++ {
		insertQoSFixture(t, "qos-u12", "qos-m12", dst, "200 OK", qosDayHour(now, 0, 14), 1000, 100)
	}
	insertQoSFixture(t, "qos-u12", "qos-m12", dst, "200 OK", qosDayHour(now, 0, 3), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if m.PeakValley.PeakHour != 14 {
		t.Errorf("peak hour = %d, want 14", m.PeakValley.PeakHour)
	}
	if m.PeakValley.ValleyHour != 3 {
		t.Errorf("valley hour = %d, want 3（零流量小时不得被选为谷）", m.PeakValley.ValleyHour)
	}
	if m.PeakValley.PeakBandKey != "afternoon" {
		t.Errorf("peak band = %q, want afternoon", m.PeakValley.PeakBandKey)
	}
	if m.PeakValley.ValleyBandKey != "dawn" {
		t.Errorf("valley band = %q, want dawn", m.PeakValley.ValleyBandKey)
	}
	if len(m.PeakValley.TopBands) != len(qosTimeBands) {
		t.Errorf("top bands len = %d, want %d", len(m.PeakValley.TopBands), len(qosTimeBands))
	}
	// TopBands 按调用占比降序
	for i := 1; i < len(m.PeakValley.TopBands); i++ {
		if m.PeakValley.TopBands[i-1].Share < m.PeakValley.TopBands[i].Share {
			t.Errorf("top bands not sorted desc at %d: %v < %v", i,
				m.PeakValley.TopBands[i-1].Share, m.PeakValley.TopBands[i].Share)
		}
	}
}

// 全站汇总的样本加权口径：直接构造模型报告，验证跨模型聚合是**样本加权**而非算术平均。
//
// 模型 A：系数 2.0、样本 30；模型 B：系数 4.0、样本 10
// → 加权 = (2×30 + 4×10) / 40 = 2.5（算术平均会是 3.0）。
func TestQoSMonitorReport_ReportAggregationWeighted(t *testing.T) {
	mkProfile := func(cong float64, samples, calls int64) []QoSHourProfile {
		ps := make([]QoSHourProfile, 24)
		for h := range ps {
			ps[h] = QoSHourProfile{Hour: h}
		}
		ps[10] = QoSHourProfile{
			Hour: 10, Calls: calls, SuccessCount: calls, Days: 1,
			AvgCongestion: cong, CoefficientSamples: samples, Confidence: "high",
		}
		return ps
	}
	models := []QoSModelReport{
		{DstModelName: "agg-a", HourProfile: mkProfile(2, 30, 30)},
		{DstModelName: "agg-b", HourProfile: mkProfile(4, 10, 10)},
	}
	profiles, bands, bandsToday, pv := aggregateQoSReport(models, time.Now())
	if len(profiles) != 24 {
		t.Fatalf("profile len = %d, want 24", len(profiles))
	}
	p := profiles[10]
	if p.Calls != 40 {
		t.Errorf("calls = %d, want 40", p.Calls)
	}
	if p.AvgCongestion != 2.5 {
		t.Errorf("congestion = %v, want 2.5（样本加权，非算术平均 3.0）", p.AvgCongestion)
	}
	if p.CoefficientSamples != 40 {
		t.Errorf("samples = %d, want 40", p.CoefficientSamples)
	}
	if len(bands) != len(qosTimeBands) || len(bandsToday) != len(qosTimeBands) {
		t.Errorf("bands len = %d/%d, want %d", len(bands), len(bandsToday), len(qosTimeBands))
	}
	if pv.PeakHour != 10 {
		t.Errorf("peak hour = %d, want 10", pv.PeakHour)
	}
}

// 全站汇总（DB 链路）：两模型同一小时的调用量求和、峰小时取全站口径。
func TestQoSMonitorReport_ReportAggregation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dstA = "qos-agg-a"
	const dstB = "qos-agg-b"
	// 模型 A：3 时 3 笔（基准样本）+ 10 时 10 笔
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-u13", "qos-m13", dstA, "200 OK", qosDayHour(now, 6, 3), 1000, 100)
	}
	for i := 0; i < 10; i++ {
		insertQoSFixture(t, "qos-u13", "qos-m13", dstA, "200 OK", qosDayHour(now, 0, 10), 2000, 100)
	}
	// 模型 B：3 时 3 笔（基准样本）+ 10 时 10 笔
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-u14", "qos-m14", dstB, "200 OK", qosDayHour(now, 6, 3), 1000, 100)
	}
	for i := 0; i < 10; i++ {
		insertQoSFixture(t, "qos-u14", "qos-m14", dstB, "200 OK", qosDayHour(now, 0, 10), 4000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	if len(report.HourProfile) != 24 {
		t.Fatalf("report hour profile len = %d, want 24", len(report.HourProfile))
	}
	p := report.HourProfile[10]
	if p.Calls != 20 {
		t.Errorf("report profile calls = %d, want 20（两模型各 10）", p.Calls)
	}
	if len(report.Bands) != len(qosTimeBands) || len(report.BandsToday) != len(qosTimeBands) {
		t.Errorf("report bands len = %d/%d, want %d", len(report.Bands), len(report.BandsToday), len(qosTimeBands))
	}
	if report.PeakValley.PeakHour != 10 {
		t.Errorf("report peak hour = %d, want 10", report.PeakValley.PeakHour)
	}
}

// 窗口状态分档：stats_24h.status 按窗口口径给出四档 + 告警。
// 告警阈值与实时窗口一致：系数 > 2.0 或错误率 > 5%（轻度排队 1.2~2.0 不告警）。
func TestQoSMonitorReport_WindowStatus(t *testing.T) {
	cases := []struct {
		cong    float64
		errRate float64
		want    string
		alert   bool
	}{
		{0.9, 0, "ok", false},
		{1.5, 0, "light", false},
		{3.0, 0, "moderate", true},
		{6.0, 0, "severe", true},
		{0.5, 8, "severe", true}, // 系数正常但错误率高 → 故障主导
	}
	for _, c := range cases {
		status, alert := qosStatusOfWindow(c.cong, c.errRate, 10)
		if status != c.want || alert != c.alert {
			t.Errorf("qosStatusOfWindow(cong=%v, err=%v) = %q/%v, want %q/%v",
				c.cong, c.errRate, status, alert, c.want, c.alert)
		}
	}
	if status, alert := qosStatusOfWindow(1.0, 0, 0); status != "idle" || alert {
		t.Errorf("qosStatusOfWindow(no call) = %q/%v, want idle/false", status, alert)
	}
}

// ===================== v2.0.87 阶段DB：多窗口（24h/7d/30d）与 7 天口径隔离 =====================

// 30 天扫描 + 7 天口径隔离：
//   - 20 天前的请求只进 30d 统计，不得进入 calls_7d / stats_7d / 7 天基准；
//   - windows 三窗口齐全，画像/时段带/趋势长度正确。
func TestQoSMonitorReport_MonthWindowBoundary(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-month-model"
	// 20 天前：3:30，1000ms/100tok ×3
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-um", "qos-mm", dst, "200 OK", qosDayHour(now, 20, 3).Add(time.Duration(i)*time.Minute), 1000, 100)
	}
	// 近 24h：2000ms/100tok ×3（基准应只由这 3 笔决定 = 20ms/tok）
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-um", "qos-mm", dst, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), 2000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if m.Calls7d != 3 || m.Calls30d != 6 {
		t.Errorf("calls 7d/30d = %d/%d, want 3/6", m.Calls7d, m.Calls30d)
	}
	if m.Stats24h.Calls != 3 || m.Stats7d.Calls != 3 || m.Stats30d.Calls != 6 {
		t.Errorf("window calls 24h/7d/30d = %d/%d/%d, want 3/3/6",
			m.Stats24h.Calls, m.Stats7d.Calls, m.Stats30d.Calls)
	}
	if m.BaselineSamples != 3 || m.BaselineMsPerToken != 20 {
		t.Errorf("baseline samples/value = %d/%v, want 3/20（30 天前的样本不得进入 7 天基准）",
			m.BaselineSamples, m.BaselineMsPerToken)
	}
	if len(m.Days) != 7 || len(m.Days30d) != 30 {
		t.Errorf("days len = %d / days30d len = %d, want 7 / 30", len(m.Days), len(m.Days30d))
	}
	if m.Days30d[0].Bucket != now.AddDate(0, 0, -29).Format("2006-01-02") {
		t.Errorf("days30d[0] = %q, want 29 days ago", m.Days30d[0].Bucket)
	}

	for _, win := range []string{"24h", "7d", "30d"} {
		v, ok := m.Windows[win]
		if !ok {
			t.Fatalf("windows[%s] missing", win)
		}
		if len(v.HourProfile) != 24 || len(v.Bands) != len(qosTimeBands) {
			t.Errorf("windows[%s] profile/bands len = %d/%d, want 24/%d",
				win, len(v.HourProfile), len(v.Bands), len(qosTimeBands))
		}
		if v.Stats.Window != win {
			t.Errorf("windows[%s].stats.window = %q", win, v.Stats.Window)
		}
	}
	if m.Windows["30d"].HourProfile[3].Calls != 3 {
		t.Errorf("30d profile[3].calls = %d, want 3", m.Windows["30d"].HourProfile[3].Calls)
	}
	if m.Windows["7d"].HourProfile[3].Calls != 0 {
		t.Errorf("7d profile[3].calls = %d, want 0（20 天前样本不得进入 7 天画像）",
			m.Windows["7d"].HourProfile[3].Calls)
	}
	if len(m.Windows["24h"].Trend) != 24 || len(m.Windows["7d"].Trend) != 7 || len(m.Windows["30d"].Trend) != 30 {
		t.Errorf("trend len 24h/7d/30d = %d/%d/%d, want 24/7/30",
			len(m.Windows["24h"].Trend), len(m.Windows["7d"].Trend), len(m.Windows["30d"].Trend))
	}
}

// 峰谷必须随窗口变化：20 天前的 3 点高峰不能冒充近 7 天高峰。
func TestQoSMonitorReport_MonthPeakValleyIsolation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-month-pv"
	// 20 天前 3:30 ×5 → 30 天口径的峰
	for i := 0; i < 5; i++ {
		insertQoSFixture(t, "qos-up", "qos-mp", dst, "200 OK", qosDayHour(now, 20, 3).Add(time.Duration(i)*time.Minute), 1000, 100)
	}
	// 近 7 天 14:30 ×1 → 7 天口径的峰
	insertQoSFixture(t, "qos-up", "qos-mp", dst, "200 OK", qosDayHour(now, 1, 14), 1000, 100)

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)

	if got := m.Windows["30d"].PeakValley.PeakHour; got != 3 {
		t.Errorf("30d peak hour = %d, want 3", got)
	}
	if got := m.Windows["7d"].PeakValley.PeakHour; got != 14 {
		t.Errorf("7d peak hour = %d, want 14", got)
	}
	if got := m.PeakValley.PeakHour; got != 14 {
		t.Errorf("compat peak_valley.peak_hour = %d, want 14", got)
	}
	if m.Windows["30d"].HourProfile[3].Calls != 5 || m.Windows["7d"].HourProfile[3].Calls != 0 {
		t.Errorf("profile[3] calls 30d/7d = %d/%d, want 5/0",
			m.Windows["30d"].HourProfile[3].Calls, m.Windows["7d"].HourProfile[3].Calls)
	}
}

// 运行经验预测仍只取近 7 天：20 天前同时段样本不得进入 next_hour。
func TestQoSMonitorReport_PredictionSevenDayIsolation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-month-predict"
	nextHour := (now.Hour() + 1) % 24
	// 20 天前的「下一小时」样本 ×30：若误入预测，样本数会变成 32
	for i := 0; i < 30; i++ {
		ts := qosDayHour(now, 20, nextHour).Add(time.Duration(i) * time.Second)
		insertQoSFixture(t, "qos-uf", "qos-mf", dst, "200 OK", ts, 1000, 100)
	}
	// 昨天同时段（近 7 天）×2
	yesterday := now.Add(-23 * time.Hour)
	for i := 0; i < 2; i++ {
		insertQoSFixture(t, "qos-uf", "qos-mf", dst, "200 OK", yesterday.Add(time.Duration(i)*time.Second), 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	if m.Prediction.NextHour.Samples != 2 {
		t.Errorf("next hour samples = %d, want 2（20 天前样本不得进入预测）", m.Prediction.NextHour.Samples)
	}
	if m.Prediction.NextHour.Confidence != "low" {
		t.Errorf("next hour confidence = %q, want low", m.Prediction.NextHour.Confidence)
	}
}

// 周分位只取近 7 天：20 天前的超慢请求不得改变 week_ok_p95。
func TestQoSMonitorReport_WeekPercentileSevenDayIsolation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	now := time.Now()
	const dst = "qos-month-pct"
	insertQoSFixture(t, "qos-uw", "qos-mw", dst, "200 OK", qosDayHour(now, 20, 10), 100000, 100)
	for i := 0; i < 3; i++ {
		insertQoSFixture(t, "qos-uw", "qos-mw", dst, "200 OK", now.Add(-time.Duration(i+1)*time.Minute), 1000, 100)
	}

	report, err := buildQoSMonitorReport("", testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("buildQoSMonitorReport failed: %v", err)
	}
	m := findQoSReport(t, report, dst)
	if m.WeekOkP95Ms != 1000 {
		t.Errorf("week ok p95 = %d, want 1000（20 天前的超慢样本不得进入周分位）", m.WeekOkP95Ms)
	}
}

// 全站三窗口加权：两模型 30d 系数 2/4、样本各 10 → 全站 3（样本加权，非算术平均）。
func TestQoSAggregateWindows_Weighted(t *testing.T) {
	mk := func(name string, cong float64, samples, calls int64) QoSModelReport {
		profiles := make([]QoSHourProfile, 24)
		for h := 0; h < 24; h++ {
			profiles[h] = QoSHourProfile{Hour: h}
		}
		profiles[10] = QoSHourProfile{
			Hour: 10, Calls: calls, SuccessCount: calls, Days: 30,
			AvgCongestion: cong, CoefficientSamples: samples, OkAvgElapsedMs: 1000, OkP95Ms: 2000,
			AvgCallsPerDay: float64(calls) / 30,
		}
		stats := QoSWindowStats{
			Window: "30d", Calls: calls, SuccessRate: 100, AvgCongestion: cong,
			CoefficientSamples: samples, OkAvgElapsedMs: 1000, OkP95Ms: 2000,
		}
		return QoSModelReport{
			DstModelName: name,
			Windows: map[string]QoSWindowView{
				"30d": {Window: "30d", Stats: stats, HourProfile: profiles,
					Bands: buildQoSTimeBands(qosHourAggsFromProfile(profiles))},
			},
		}
	}
	models := []QoSModelReport{mk("a", 2, 10, 10), mk("b", 4, 10, 20)}
	out := aggregateQoSWindows(models)
	v, ok := out["30d"]
	if !ok {
		t.Fatal("windows[30d] missing")
	}
	if v.Stats.AvgCongestion != 3 {
		t.Errorf("30d site congestion = %v, want 3（样本加权）", v.Stats.AvgCongestion)
	}
	if v.Stats.Calls != 30 {
		t.Errorf("30d site calls = %v, want 30", v.Stats.Calls)
	}
	if v.HourProfile[10].Calls != 30 {
		t.Errorf("30d site profile[10].calls = %v, want 30", v.HourProfile[10].Calls)
	}
	if v.PeakValley.PeakHour != 10 {
		t.Errorf("30d site peak hour = %d, want 10", v.PeakValley.PeakHour)
	}
}
