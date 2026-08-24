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

func insertModelInfoUsageFixture(t *testing.T, userName, modelName, dstModelName string, input, output, total uint64) {
	t.Helper()
	tableName := modelsdb.GetAgentHttpTableName(userName, modelName, config.G.DBMysqlSubTableNumber)
	item := &modelsdb.TAgentHttpTransactionDataItem{
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		UserName:         userName,
		ModelName:        modelName,
		DstModelName:     dstModelName,
		ProtocolType:     protocol.AgentProtocolType_Anthropic,
		TokensInputSize:  input,
		TokensOutputSize: output,
		TokensAllSize:    total,
	}
	if err := database.DB.Table(tableName).Create(item).Error; err != nil {
		t.Fatalf("create usage fixture failed: %v", err)
	}
}

func TestModelInfoUsageStatsAll(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixture(t, "alice", "source-a", "dst-a", 100, 200, 300)
	insertModelInfoUsageFixture(t, "bob", "source-b", "dst-a", 50, 50, 100)
	insertModelInfoUsageFixture(t, "alice", "source-c", "dst-b", 30, 70, 100)

	summary, stats, err := modelsdb.GetModelInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetModelInfoUsageStatsAll failed: %v", err)
	}
	if summary.ModelCount != 2 {
		t.Fatalf("model_count = %d, want 2", summary.ModelCount)
	}
	if summary.TotalCallCount != 3 || summary.TokensAllSize != 500 || summary.TokensInputSize != 180 || summary.TokensOutputSize != 320 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(stats) != 2 {
		t.Fatalf("stats len = %d, want 2", len(stats))
	}
	if stats[0].ModelName != "dst-a" {
		t.Fatalf("first model = %s, want dst-a", stats[0].ModelName)
	}
	if stats[0].CallCount != 2 || stats[0].TokensAllSize != 400 || stats[0].UserCount != 2 {
		t.Fatalf("unexpected dst-a stat: %+v", stats[0])
	}
	if stats[0].TokenShare < 79.99 || stats[0].TokenShare > 80.01 {
		t.Fatalf("dst-a token_share = %.4f, want 80", stats[0].TokenShare)
	}
	if stats[0].CallShare < 66.66 || stats[0].CallShare > 66.67 {
		t.Fatalf("dst-a call_share = %.4f, want 66.67", stats[0].CallShare)
	}
}

func TestModelInfoUsageStatsByUser(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixture(t, "alice", "source-a", "dst-a", 100, 200, 300)
	insertModelInfoUsageFixture(t, "alice", "source-b", "dst-b", 50, 50, 100)
	insertModelInfoUsageFixture(t, "bob", "source-a", "dst-a", 1000, 1000, 2000)

	summary, stats, err := modelsdb.GetModelInfoUsageStatsByUser("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetModelInfoUsageStatsByUser failed: %v", err)
	}
	if summary.ModelCount != 2 || summary.TotalCallCount != 2 || summary.TokensAllSize != 400 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(stats) != 2 || stats[0].ModelName != "source-a" || stats[0].TokensAllSize != 300 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats[0].TokenShare < 74.99 || stats[0].TokenShare > 75.01 {
		t.Fatalf("source-a token_share = %.4f, want 75", stats[0].TokenShare)
	}
}

func TestModelInfoUsageStatsByUserDstModel(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixture(t, "alice", "source-a", "dst-a", 100, 200, 300)
	insertModelInfoUsageFixture(t, "alice", "source-b", "dst-a", 50, 50, 100)
	insertModelInfoUsageFixture(t, "alice", "source-b", "dst-b", 20, 80, 100)
	insertModelInfoUsageFixture(t, "bob", "source-a", "dst-a", 1000, 1000, 2000)

	summary, stats, err := modelsdb.GetModelInfoUsageStatsByUserDstModel("alice", []string{"source-a", "source-b"}, config.G.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetModelInfoUsageStatsByUserDstModel failed: %v", err)
	}
	if summary.ModelCount != 2 || summary.TotalCallCount != 3 || summary.TokensAllSize != 500 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(stats) != 2 || stats[0].ModelName != "dst-a" || stats[0].TokensAllSize != 400 || stats[0].UserCount != 1 {
		t.Fatalf("unexpected dst stats: %+v", stats)
	}
	if stats[0].TokenShare < 79.99 || stats[0].TokenShare > 80.01 {
		t.Fatalf("dst-a token_share = %.4f, want 80", stats[0].TokenShare)
	}
	if stats[0].CallShare < 66.66 || stats[0].CallShare > 66.67 {
		t.Fatalf("dst-a call_share = %.4f, want 66.67", stats[0].CallShare)
	}
}
func TestModelInfoInterfaceStatsAPI(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertModelInfoUsageFixture(t, "alice", "source-a", "dst-a", 100, 200, 300)

	// days=3 触发日期槽位补全，返回 3 天趋势（含空槽位）
	req := httptest.NewRequest(http.MethodPost, "/ModelInfoInterface", bytes.NewBufferString(`{"action":"stats","days":3}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	modelInfoInterfaceHandle(rec, req)

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Summary modelsdb.ModelInfoUsageSummary `json:"summary"`
			Models  []modelsdb.ModelInfoUsageStat  `json:"models"`
			Trend   []modelsdb.DailyStat           `json:"trend"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v body=%s", err, rec.Body.String())
	}
	if !resp.Success {
		t.Fatalf("response not success: %s", rec.Body.String())
	}
	if resp.Data.Summary.TotalCallCount != 1 || len(resp.Data.Models) != 1 || resp.Data.Models[0].ModelName != "dst-a" {
		t.Fatalf("unexpected response: %+v", resp.Data)
	}
	// trend 应包含 3 个槽位（days 默认 3）
	if len(resp.Data.Trend) != 3 {
		t.Fatalf("trend len = %d, want 3", len(resp.Data.Trend))
	}
}

func TestUserModelInfoStatsRequiresLogin(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/ModelInfoInterface", bytes.NewBufferString(`{"action":"stats"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userModelInfoInterfaceHandle(rec, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp["success"] != false || resp["message"] != "未登录" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}
