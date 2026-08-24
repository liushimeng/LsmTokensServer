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
	"testing"
	"time"
)

// insertAgentInfoUsageFixtureAged 同 insertAgentInfoUsageFixture，但 created_at 可指定。
func insertAgentInfoUsageFixtureAged(t *testing.T, userName, modelName, agentToolName string, input, output, total uint64, age time.Duration) {
	t.Helper()
	tableName := modelsdb.GetAgentHttpTableName(userName, modelName, config.G.DBMysqlSubTableNumber)
	ts := time.Now().Add(-age)
	item := &modelsdb.TAgentHttpTransactionDataItem{
		CreatedAt:        ts,
		UpdatedAt:        ts,
		UserName:         userName,
		ModelName:        modelName,
		DstModelName:     "dst-" + modelName,
		AgentToolName:    agentToolName,
		ProtocolType:     protocol.AgentProtocolType_Anthropic,
		TokensInputSize:  input,
		TokensOutputSize: output,
		TokensAllSize:    total,
	}
	if err := database.DB.Table(tableName).Create(item).Error; err != nil {
		t.Fatalf("create aged agent fixture failed: %v", err)
	}
}

// TestAgentInfoUsageStatsAllDaysFilter 验证 days 参数对管理员 AgentInfo 聚合的影响。
func TestAgentInfoUsageStatsAllDaysFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertAgentInfoUsageFixtureAged(t, "alice", "source-a", "claude-cli", 100, 200, 300, 1*24*time.Hour)
	insertAgentInfoUsageFixtureAged(t, "bob", "source-b", "claude-cli", 50, 50, 100, 2*24*time.Hour)
	insertAgentInfoUsageFixtureAged(t, "alice", "source-c", "opencode", 30, 70, 100, 10*24*time.Hour)

	// days=0 包含全部 3 条（2 个 agent）
	_, statsAll, err := modelsdb.GetAgentInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("modelsdb.GetAgentInfoUsageStatsAll(days=0) failed: %v", err)
	}
	if len(statsAll) != 2 {
		t.Fatalf("days=0 stats len = %d, want 2", len(statsAll))
	}
	var totalAll int64
	for _, s := range statsAll {
		totalAll += s.CallCount
	}
	if totalAll != 3 {
		t.Fatalf("days=0 total call count = %d, want 3", totalAll)
	}

	// days=3 仅包含前 2 条（都是 claude-cli），opencode 10 天前被过滤
	summary3, stats3, err := modelsdb.GetAgentInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, 3)
	if err != nil {
		t.Fatalf("modelsdb.GetAgentInfoUsageStatsAll(days=3) failed: %v", err)
	}
	if len(stats3) != 1 || stats3[0].AgentToolName != "claude-cli" {
		t.Fatalf("days=3 stats: %+v", stats3)
	}
	if summary3.TotalCallCount != 2 || summary3.TokensAllSize != 400 {
		t.Fatalf("days=3 summary: %+v", summary3)
	}
}

// TestAgentInfoUsageStatsByUserDaysFilter 验证 days 参数对用户 AgentInfo 聚合的影响。
func TestAgentInfoUsageStatsByUserDaysFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertAgentInfoUsageFixtureAged(t, "alice", "source-a", "claude-cli", 100, 200, 300, 1*24*time.Hour)
	insertAgentInfoUsageFixtureAged(t, "alice", "source-b", "opencode", 50, 50, 100, 5*24*time.Hour)
	// bob 的记录不应影响 alice
	insertAgentInfoUsageFixtureAged(t, "bob", "source-a", "opencode", 1000, 1000, 2000, 1*24*time.Hour)

	// days=0 包含 alice 两条
	summary0, stats0, err := modelsdb.GetAgentInfoUsageStatsByUser("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("modelsdb.GetAgentInfoUsageStatsByUser(days=0) failed: %v", err)
	}
	if summary0.AgentCount != 2 || summary0.TotalCallCount != 2 || summary0.TokensAllSize != 400 {
		t.Fatalf("days=0 summary: %+v", summary0)
	}
	if len(stats0) != 2 {
		t.Fatalf("days=0 stats len = %d, want 2", len(stats0))
	}

	// days=3 只剩 claude-cli（1 天内）；opencode 5 天前被过滤
	summary3, stats3, err := modelsdb.GetAgentInfoUsageStatsByUser("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 3)
	if err != nil {
		t.Fatalf("modelsdb.GetAgentInfoUsageStatsByUser(days=3) failed: %v", err)
	}
	if summary3.AgentCount != 1 || summary3.TotalCallCount != 1 || summary3.TokensAllSize != 300 {
		t.Fatalf("days=3 summary: %+v", summary3)
	}
	if len(stats3) != 1 || stats3[0].AgentToolName != "claude-cli" {
		t.Fatalf("days=3 stats: %+v", stats3)
	}
}

// TestAgentInfoInterfaceAPIDaysParam 验证管理员 API handler 接受 days 参数。
func TestAgentInfoInterfaceAPIDaysParam(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertAgentInfoUsageFixtureAged(t, "alice", "source-a", "claude-cli", 100, 200, 300, 1*24*time.Hour)
	insertAgentInfoUsageFixtureAged(t, "alice", "source-b", "opencode", 50, 50, 100, 10*24*time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/AgentInfoInterface", bytes.NewBufferString(`{"action":"stats","days":3}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	agentInfoInterfaceHandle(rec, req)

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Summary modelsdb.AgentInfoUsageSummary `json:"summary"`
			Agents  []modelsdb.AgentInfoUsageStat  `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v body=%s", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("days=3 stats should succeed: %s", rec.Body.String())
	}
	if resp.Data.Summary.TotalCallCount != 1 || len(resp.Data.Agents) != 1 || resp.Data.Agents[0].AgentToolName != "claude-cli" {
		t.Fatalf("unexpected response: %+v", resp.Data)
	}
}
