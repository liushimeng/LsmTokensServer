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

// insertAgentInfoUsageFixture 向交易分表插入一条带 AgentToolName 的记录。
func insertAgentInfoUsageFixture(t *testing.T, userName, modelName, agentToolName string, input, output, total uint64) {
	t.Helper()
	tableName := modelsdb.GetAgentHttpTableName(userName, modelName, config.G.DBMysqlSubTableNumber)
	item := &modelsdb.TAgentHttpTransactionDataItem{
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
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
		t.Fatalf("create agent usage fixture failed: %v", err)
	}
}

func TestAgentInfoUsageStatsAll(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertAgentInfoUsageFixture(t, "alice", "source-a", "claude-cli", 100, 200, 300)
	insertAgentInfoUsageFixture(t, "bob", "source-b", "claude-cli", 50, 50, 100)
	insertAgentInfoUsageFixture(t, "alice", "source-c", "opencode", 30, 70, 100)
	// 空 AgentToolName 应归一化为 unknown，不能丢失。
	insertAgentInfoUsageFixture(t, "carol", "source-d", "", 10, 10, 20)

	summary, stats, err := modelsdb.GetAgentInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetAgentInfoUsageStatsAll failed: %v", err)
	}
	if summary.AgentCount != 3 {
		t.Fatalf("agent_count = %d, want 3", summary.AgentCount)
	}
	if summary.TotalCallCount != 4 || summary.TokensAllSize != 520 || summary.TokensInputSize != 190 || summary.TokensOutputSize != 330 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	// claude-cli 调用次数最多排第一
	if stats[0].AgentToolName != "claude-cli" {
		t.Fatalf("first agent = %s, want claude-cli", stats[0].AgentToolName)
	}
	if stats[0].CallCount != 2 || stats[0].TokensAllSize != 400 || stats[0].UserCount != 2 {
		t.Fatalf("unexpected claude-cli stat: %+v", stats[0])
	}
	// 确认 unknown 被纳入统计
	var foundUnknown bool
	for _, s := range stats {
		if s.AgentToolName == "unknown" {
			foundUnknown = true
			if s.CallCount != 1 || s.TokensAllSize != 20 {
				t.Fatalf("unexpected unknown stat: %+v", s)
			}
		}
	}
	if !foundUnknown {
		t.Fatalf("unknown agent stat missing; data lost")
	}
}

func TestAgentInfoUsageStatsByUser(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertAgentInfoUsageFixture(t, "alice", "source-a", "claude-cli", 100, 200, 300)
	insertAgentInfoUsageFixture(t, "alice", "source-b", "opencode", 50, 50, 100)
	insertAgentInfoUsageFixture(t, "bob", "source-a", "claude-cli", 1000, 1000, 2000)

	summary, stats, err := modelsdb.GetAgentInfoUsageStatsByUser("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetAgentInfoUsageStatsByUser failed: %v", err)
	}
	if summary.AgentCount != 2 || summary.TotalCallCount != 2 || summary.TokensAllSize != 400 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if stats[0].AgentToolName != "claude-cli" || stats[0].TokensAllSize != 300 {
		t.Fatalf("unexpected stats[0]: %+v", stats[0])
	}
	// bob 的数据不能进入 alice 的统计
	if summary.TokensAllSize == 2300 {
		t.Fatalf("user stats leaked other user's data")
	}
}

func TestAgentInfoInterfaceStatsAPI(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertAgentInfoUsageFixture(t, "alice", "source-a", "claude-cli", 100, 200, 300)

	// days=3 触发日期槽位补全，返回 3 天趋势（含空槽位）
	req := httptest.NewRequest(http.MethodPost, "/AgentInfoInterface", bytes.NewBufferString(`{"action":"stats","days":3}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	agentInfoInterfaceHandle(rec, req)

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Summary modelsdb.AgentInfoUsageSummary `json:"summary"`
			Agents  []modelsdb.AgentInfoUsageStat  `json:"agents"`
			Trend   []modelsdb.DailyStat           `json:"trend"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v body=%s", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("response not success: %s", rec.Body.String())
	}
	if resp.Data.Summary.TotalCallCount != 1 || len(resp.Data.Agents) != 1 || resp.Data.Agents[0].AgentToolName != "claude-cli" {
		t.Fatalf("unexpected response: %+v", resp.Data)
	}
	// trend 应包含 3 个槽位（days 默认 3）
	if len(resp.Data.Trend) != 3 {
		t.Fatalf("trend len = %d, want 3", len(resp.Data.Trend))
	}
}

func TestUserAgentInfoStatsRequiresLogin(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/AgentInfoInterface", bytes.NewBufferString(`{"action":"stats"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userAgentInfoInterfaceHandle(rec, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp["success"] != false || resp["message"] != "未登录" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}
