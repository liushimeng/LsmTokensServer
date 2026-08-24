package api

import (
	"bytes"
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// insertModelInfoUsageFixtureAged 同 insertModelInfoUsageFixture，但 created_at 可指定，
// 用于 days 过滤测试。
func insertModelInfoUsageFixtureAged(t *testing.T, userName, modelName, dstModelName string, input, output, total uint64, age time.Duration) {
	t.Helper()
	tableName := modelsdb.GetAgentHttpTableName(userName, modelName, config.G.DBMysqlSubTableNumber)
	// v2.0.59: 用「目标本地日期的正午」构造 created_at，避免日界测试在
	// 非 UTC 时区（如 Asia/Shanghai 凌晨 1 点）因跨天边界而整批落空。
	// 语义保持原「age 前」不变：0≤age<24h → 今天本地日期；24h≤age<48h → 昨天本地日期 …
	now := time.Now()
	daysBack := int(age / (24 * time.Hour))
	ts := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local).AddDate(0, 0, -daysBack)
	item := &modelsdb.TAgentHttpTransactionDataItem{
		CreatedAt:        ts,
		UpdatedAt:        ts,
		UserName:         userName,
		ModelName:        modelName,
		DstModelName:     dstModelName,
		ProtocolType:     protocol.AgentProtocolType_Anthropic,
		TokensInputSize:  input,
		TokensOutputSize: output,
		TokensAllSize:    total,
	}
	if err := database.DB.Table(tableName).Create(item).Error; err != nil {
		t.Fatalf("create aged fixture failed: %v", err)
	}
}

// TestModelInfoUsageStatsAllDaysFilter 验证 days 参数对管理员 ModelInfo 聚合的影响。
// - days=0：包含所有历史记录
// - days=3：仅包含 3 天内的记录（10 天前的应被过滤掉）
func TestModelInfoUsageStatsAllDaysFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 1 天内
	insertModelInfoUsageFixtureAged(t, "alice", "source-a", "dst-a", 100, 200, 300, 1*24*time.Hour)
	// 2 天前（应包含在 days=3）
	insertModelInfoUsageFixtureAged(t, "alice", "source-b", "dst-a", 50, 50, 100, 2*24*time.Hour)
	// 10 天前（应被 days=3 过滤）
	insertModelInfoUsageFixtureAged(t, "alice", "source-c", "dst-b", 1000, 1000, 2000, 10*24*time.Hour)

	// days=0 包含全部 3 条
	_, statsAll, err := modelsdb.GetModelInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("modelsdb.GetModelInfoUsageStatsAll(days=0) failed: %v", err)
	}
	var totalAll int64
	for _, s := range statsAll {
		totalAll += s.CallCount
	}
	if totalAll != 3 {
		t.Fatalf("days=0 total call count = %d, want 3", totalAll)
	}

	// days=3 仅包含前两条
	_, stats3, err := modelsdb.GetModelInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, 3)
	if err != nil {
		t.Fatalf("modelsdb.GetModelInfoUsageStatsAll(days=3) failed: %v", err)
	}
	var total3 int64
	for _, s := range stats3 {
		total3 += s.CallCount
	}
	if total3 != 2 {
		t.Fatalf("days=3 total call count = %d, want 2", total3)
	}

	// dst-b 不应出现（其唯一记录 10 天前）
	for _, s := range stats3 {
		if s.ModelName == "dst-b" {
			t.Fatalf("days=3 should not include dst-b (10 days old)")
		}
	}
}

// TestModelInfoUsageStatsByUserDaysFilter 验证 days 参数对用户 ModelInfo 平台模型聚合的影响。
func TestModelInfoUsageStatsByUserDaysFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixtureAged(t, "alice", "source-a", "dst-a", 100, 200, 300, 1*24*time.Hour)
	insertModelInfoUsageFixtureAged(t, "alice", "source-b", "dst-b", 50, 50, 100, 5*24*time.Hour)

	// days=0 包含 2 个模型
	summary0, _, err := modelsdb.GetModelInfoUsageStatsByUser("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("modelsdb.GetModelInfoUsageStatsByUser(days=0) failed: %v", err)
	}
	if summary0.ModelCount != 2 || summary0.TotalCallCount != 2 {
		t.Fatalf("days=0 summary: %+v", summary0)
	}

	// days=3 只剩 source-a（1 天内）；source-b 5 天前被过滤
	summary3, stats3, err := modelsdb.GetModelInfoUsageStatsByUser("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 3)
	if err != nil {
		t.Fatalf("modelsdb.GetModelInfoUsageStatsByUser(days=3) failed: %v", err)
	}
	if summary3.ModelCount != 1 || summary3.TotalCallCount != 1 {
		t.Fatalf("days=3 summary: %+v", summary3)
	}
	if len(stats3) != 1 || stats3[0].ModelName != "source-a" {
		t.Fatalf("days=3 stats: %+v", stats3)
	}
}

// TestModelInfoUsageStatsByUserDstModelDaysFilter 验证 days 参数对用户目标模型聚合的影响。
func TestModelInfoUsageStatsByUserDstModelDaysFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixtureAged(t, "alice", "source-a", "dst-a", 100, 200, 300, 1*24*time.Hour)
	insertModelInfoUsageFixtureAged(t, "alice", "source-a", "dst-b", 50, 50, 100, 7*24*time.Hour)

	// days=5：dst-a（1 天内）+ dst-b 不在内（7 天前）
	_, stats5, err := modelsdb.GetModelInfoUsageStatsByUserDstModel("alice", []string{"source-a"}, config.G.DBMysqlSubTableNumber, 5)
	if err != nil {
		t.Fatalf("modelsdb.GetModelInfoUsageStatsByUserDstModel(days=5) failed: %v", err)
	}
	if len(stats5) != 1 || stats5[0].ModelName != "dst-a" {
		t.Fatalf("days=5 dst stats: %+v", stats5)
	}

	// days=14：两条都包含
	_, stats14, err := modelsdb.GetModelInfoUsageStatsByUserDstModel("alice", []string{"source-a"}, config.G.DBMysqlSubTableNumber, 14)
	if err != nil {
		t.Fatalf("modelsdb.GetModelInfoUsageStatsByUserDstModel(days=14) failed: %v", err)
	}
	if len(stats14) != 2 {
		t.Fatalf("days=14 dst stats len = %d, want 2", len(stats14))
	}
}

// TestClampStatsDays 验证 days 边界处理：负数视为 0；超过 365 截断到 365。
func TestClampStatsDays(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-1, 0},
		{0, 0},
		{1, 1},
		{365, 365},
		{366, 365},
		{9999, 365},
	}
	for _, c := range cases {
		if got := modelsdb.ClampStatsDays(c.in); got != c.want {
			t.Errorf("modelsdb.ClampStatsDays(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestGetDailyStatsAll 验证全站按天聚合的 modelsdb.DailyStat 统计（调用次数 + Tokens）。
func TestGetDailyStatsAll(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 今天 2 条
	insertModelInfoUsageFixtureAged(t, "alice", "source-a", "dst-a", 100, 200, 300, 1*time.Hour)
	insertModelInfoUsageFixtureAged(t, "bob", "source-b", "dst-a", 50, 50, 100, 2*time.Hour)
	// 2 天前 1 条
	yesterday2 := 2 * 24 * time.Hour
	insertModelInfoUsageFixtureAged(t, "alice", "source-c", "dst-b", 30, 70, 100, yesterday2)
	// 10 天前（应被 days=5 过滤）
	insertModelInfoUsageFixtureAged(t, "alice", "source-d", "dst-c", 1000, 1000, 2000, 10*24*time.Hour)

	// days=5 应包含 3 条（今天 2 + 2 天前 1），跨 3 天有数据
	stats, err := modelsdb.GetDailyStatsAll(config.G.DBMysqlSubTableNumber, 5)
	if err != nil {
		t.Fatalf("GetDailyStatsAll failed: %v", err)
	}
	if len(stats) != 5 {
		t.Fatalf("modelsdb.GetDailyStatsAll(days=5) len = %d, want 5", len(stats))
	}

	// 最后一天（今天）应有 2 调用 400 total tokens
	last := stats[len(stats)-1]
	if last.Count != 2 {
		t.Fatalf("today count = %d, want 2", last.Count)
	}
	if last.TokensTotal != 400 || last.TokensInput != 150 || last.TokensOutput != 250 {
		t.Fatalf("today tokens = %+v", last)
	}

	// 验证非空天至少包含 3 天有数据
	dataDays := 0
	for _, s := range stats {
		if s.Count > 0 {
			dataDays++
		}
	}
	if dataDays < 2 {
		t.Fatalf("data days = %d, want >= 2", dataDays)
	}

	// days=0 返回所有历史记录
	statsAll, err := modelsdb.GetDailyStatsAll(config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("modelsdb.GetDailyStatsAll(days=0) failed: %v", err)
	}
	totalAll := int64(0)
	for _, s := range statsAll {
		totalAll += s.Count
	}
	if totalAll != 4 {
		t.Fatalf("days=0 total count = %d, want 4", totalAll)
	}
}

// TestGetDailyStatsAllNilDB 验证 database.DB==nil 时返回错误。
func TestGetDailyStatsAllNilDB(t *testing.T) {
	origDB := database.DB
	defer func() { database.DB = origDB }()
	database.DB = nil

	_, err := modelsdb.GetDailyStatsAll(16, 7)
	if err == nil || !strings.Contains(err.Error(), "database not initialized") {
		t.Fatalf("modelsdb.GetDailyStatsAll(nil database.DB) err = %v, want database not initialized", err)
	}
}

// TestModelInfoInterfaceAPIDaysParam 验证 API handler 接受 days 参数并透传到 SQL 层。
func TestModelInfoInterfaceAPIDaysParam(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixtureAged(t, "alice", "source-a", "dst-a", 100, 200, 300, 1*24*time.Hour)
	insertModelInfoUsageFixtureAged(t, "alice", "source-b", "dst-b", 50, 50, 100, 10*24*time.Hour)

	// 传 days=3：10 天前那条被过滤
	req := httptest.NewRequest(http.MethodPost, "/ModelInfoInterface", bytes.NewBufferString(`{"action":"stats","days":3}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	modelInfoInterfaceHandle(rec, req)

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Summary modelsdb.ModelInfoUsageSummary `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v body=%s", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("days=3 stats should succeed: %s", rec.Body.String())
	}
	if resp.Data.Summary.TotalCallCount != 1 {
		t.Fatalf("days=3 total_call_count = %d, want 1", resp.Data.Summary.TotalCallCount)
	}

	// 验证请求 body 里包含 days 字段名（防止后续误改）
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("response not success: %s", rec.Body.String())
	}
}
