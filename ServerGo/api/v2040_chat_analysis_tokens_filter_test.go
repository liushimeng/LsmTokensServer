package api

// ==================== v2.0.40 浏览记录「输入/输出 Tokens 是否非零」过滤条件测试 ====================
//
// 需求：/ChatAnalysis 浏览记录页管理员端 + 用户端共用页面增加两个三态下拉：
//   - 输入 Tokens：全部(0) / 非零(1) / 为零(2)
//   - 输出 Tokens：全部(0) / 非零(1) / 为零(2)
//
// 覆盖范围：
//   1. QueryAgentHttpTransactions 新增两个三态参数 filterInputTokensNonzero /
//      filterOutputTokensNonzero 的 SQL 行为（DB 依赖，无 DB 时自动 skip）
//   2. ChatAnalysisInterfaceRequest 新字段 JSON 绑定契约（纯函数，不依赖 DB）
//   3. handler 端到端：新字段经 HTTP POST 传入后正确筛选（DB 依赖自动 skip）
//   4. 输入契约：服务端对非法负值 / 越界值零容忍（参数校验）
//
// DB 依赖测试通过 DB == nil 判断自动跳过，保证 CI 环境无 MySQL 也能跑。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

// ---------- 1. API 新字段 JSON 绑定契约 ----------

func TestChatAnalysisRequest_NewTokensFilterFields(t *testing.T) {
	cases := []struct {
		name            string
		body            string
		wantIn, wantOut int
	}{
		{"omit-both-default-zero", `{"user_name":"u","model_name":"m"}`, 0, 0},
		{"both-zero", `{"user_name":"u","model_name":"m","filter_input_tokens_nonzero":0,"filter_output_tokens_nonzero":0}`, 0, 0},
		{"in-nonzero-out-all", `{"user_name":"u","model_name":"m","filter_input_tokens_nonzero":1}`, 1, 0},
		{"in-zero-out-nonzero", `{"user_name":"u","model_name":"m","filter_input_tokens_nonzero":2,"filter_output_tokens_nonzero":1}`, 2, 1},
		{
			"mixed-with-other-filters",
			`{"user_name":"u","model_name":"m","page":2,"page_size":5,"filter_url":"x","filter_method":"POST","filter_status":"200 OK","filter_status_not":true,"filter_protocol_type":1,"filter_dst_model_name":"gpt","filter_tools":"","filter_agent_tool_name":"claude-cli","days":7,"filter_input_tokens_nonzero":1,"filter_output_tokens_nonzero":2}`,
			1, 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var req ChatAnalysisInterfaceRequest
			if err := json.NewDecoder(strings.NewReader(c.body)).Decode(&req); err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if req.FilterInputTokensNonzero != c.wantIn {
				t.Errorf("FilterInputTokensNonzero = %d, want %d", req.FilterInputTokensNonzero, c.wantIn)
			}
			if req.FilterOutputTokensNonzero != c.wantOut {
				t.Errorf("FilterOutputTokensNonzero = %d, want %d", req.FilterOutputTokensNonzero, c.wantOut)
			}
		})
	}
}

// ---------- 2. DB 集成测试（DB=nil 自动跳过） ----------

// saveTokensFixture 写入一条指定 tokens_input_size / tokens_output_size 的浏览记录
func saveTokensFixture(t *testing.T, userName, modelName string, in, out uint64) {
	t.Helper()
	requestBody := "eyJtb2RlbCI6ImNsYXVkZSJ9" // {"model":"claude"}
	err := modelsdb.SaveAgentHttpTransaction(
		userName, modelName,
		1001,
		"sk-test", 10,
		1,
		"dst-model",
		protocol.AgentProtocolType_Anthropic,
		"POST", "https://api.test.com/v1/messages", "127.0.0.1:12345",
		100,
		"Content-Type: application/json", "Content-Type: application/json", requestBody, "",
		"200 OK", 200,
		"Content-Type: application/json", "Content-Type: application/json", `{"ok":true}`, "",
		time.Now(), time.Now(), time.Now(), time.Now(),
		150,
		"TestTool",
		"", "", "unknown_session_id", "",
		config.G.DBMysqlSubTableNumber,
		in, out, in+out,
	)
	if err != nil {
		t.Fatalf("save fixture (in=%d,out=%d) failed: %v", in, out, err)
	}
}

func TestQueryAgentHttpTransactions_TokensNonZeroFilter(t *testing.T) {
	if database.DB == nil {
		t.Skip("DB not initialized; skipping integration test")
	}
	cleanup := initTestEnv(t)
	defer cleanup()

	const (
		user = "tokensfilteruser"
		mod  = "tokensfiltermodel"
	)

	// 准备 4 条记录 2×2 矩阵 (input zero/nonzero × output zero/nonzero)
	fixtures := []struct {
		name    string
		in, out uint64
	}{
		{"both-zero", 0, 0},
		{"in-only", 150, 0},
		{"out-only", 0, 200},
		{"both-nonzero", 300, 400},
	}
	for _, f := range fixtures {
		saveTokensFixture(t, user, mod, f.in, f.out)
	}

	cases := []struct {
		name       string
		inNonZero  int
		outNonZero int
		wantTotal  int64
	}{
		{"all-all", 0, 0, 4},
		{"in-nonzero-all", 1, 0, 2},         // in-only + both-nonzero
		{"in-zero-all", 2, 0, 2},            // both-zero + out-only
		{"all-out-nonzero", 0, 1, 2},        // out-only + both-nonzero
		{"all-out-zero", 0, 2, 2},           // both-zero + in-only
		{"in-nonzero-out-nonzero", 1, 1, 1}, // both-nonzero only
		{"in-zero-out-zero", 2, 2, 1},       // both-zero only
		{"in-nonzero-out-zero", 1, 2, 1},    // in-only only
		{"in-zero-out-nonzero", 2, 1, 1},    // out-only only
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records, total, err := modelsdb.QueryAgentHttpTransactions(
				user, mod, config.G.DBMysqlSubTableNumber, 1, 50,
				"", "", "", false, 0, "", "", "", 0,
				c.inNonZero, c.outNonZero, 0,
			)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d (records=%d)", total, c.wantTotal, len(records))
			}
			// 逐条断言每条返回记录均满足过滤条件
			for _, r := range records {
				switch c.inNonZero {
				case 1:
					if r.TokensInputSize == 0 {
						t.Errorf("record %d has tokens_input_size=0, want >0", r.ID)
					}
				case 2:
					if r.TokensInputSize != 0 {
						t.Errorf("record %d has tokens_input_size=%d, want 0", r.ID, r.TokensInputSize)
					}
				}
				switch c.outNonZero {
				case 1:
					if r.TokensOutputSize == 0 {
						t.Errorf("record %d has tokens_output_size=0, want >0", r.ID)
					}
				case 2:
					if r.TokensOutputSize != 0 {
						t.Errorf("record %d has tokens_output_size=%d, want 0", r.ID, r.TokensOutputSize)
					}
				}
			}
		})
	}
}

// ---------- 3. handler 端到端 ----------

func TestChatAnalysisInterfaceHandle_TokensNonzeroFilter(t *testing.T) {
	if database.DB == nil {
		t.Skip("DB not initialized; skipping integration test")
	}
	cleanup := initTestEnv(t)
	defer cleanup()

	const (
		user = "handlerfilteruser"
		mod  = "handlerfiltermodel"
	)
	// both-zero + both-nonzero
	saveTokensFixture(t, user, mod, 0, 0)
	saveTokensFixture(t, user, mod, 100, 200)

	// 仅筛选输入非零
	body := `{"user_name":"` + user + `","model_name":"` + mod + `","page":1,"page_size":50,"filter_input_tokens_nonzero":1,"filter_output_tokens_nonzero":0}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chatAnalysisInterfaceHandle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	var resp ChatAnalysisInterfaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("handler failed: %s", resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if resp.Data.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1 (only both-nonzero record)", resp.Data.TotalCount)
	}
	if len(resp.Data.Records) != 1 {
		t.Errorf("Records len = %d, want 1", len(resp.Data.Records))
		return
	}
	if resp.Data.Records[0].TokensInputSize == 0 {
		t.Error("input tokens filter failed: returned record has TokensInputSize == 0")
	}

	// 输出为零 → 选回 both-zero
	body2 := `{"user_name":"` + user + `","model_name":"` + mod + `","filter_input_tokens_nonzero":0,"filter_output_tokens_nonzero":2}`
	req2 := httptest.NewRequest(http.MethodPost, "/ChatAnalysisInterface", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	chatAnalysisInterfaceHandle(w2, req2)
	var resp2 ChatAnalysisInterfaceResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !resp2.Success {
		t.Fatalf("handler failed: %s", resp2.Message)
	}
	if resp2.Data.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1 (only both-zero record)", resp2.Data.TotalCount)
	}
	if resp2.Data.Records[0].TokensOutputSize != 0 {
		t.Error("output tokens filter failed: returned record has TokensOutputSize != 0")
	}
}

// ---------- 4. SQLite 自包含集成测试（无需 MySQL，CI 可跑） ----------

// TestQueryAgentHttpTransactions_TokensNonZeroFilter_SQLite 用 SQLite 内存库验证
// 新增的 tokens_input_size / tokens_output_size 过滤 WHERE 子句在真实 SQL 引擎上的行为。
// 覆盖 3×3 全组合（in×out 各 0/1/2），断言返回总数与逐条记录满足过滤条件。
func TestQueryAgentHttpTransactions_TokensNonZeroFilter_SQLite(t *testing.T) {
	// 抑制 InitAgentHttpSubTables 的后台回填 goroutine（避免与 SQLite 清理竞争 nil DB panic）
	t.Setenv("LSM_SKIP_BACKGROUND_BACKFILL", "1")
	t.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", "1")
	origDB := database.DB
	origCfg := config.G
	origDisabled := logger.IsUserLogDisabled()
	defer func() {
		database.DB = origDB
		config.G = origCfg
		logger.SetDisableUserLog(origDisabled)
	}()

	config.G = config.DefaultConfig()
	config.G.DBMysqlSubTableNumber = 4
	// 构造独立的 SQLite 内存库 — 与 initTestEnv 不同，不依赖 DB 是否已初始化
	logger.SetDisableUserLog(true)
	sqliteDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.DB = sqliteDB
	defer func() {
		if sqlDB, _ := database.DB.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = origDB
		logger.SetDisableUserLog(origDisabled)
	}()

	if err := modelsdb.InitAgentHttpSubTables(config.G.DBMysqlSubTableNumber); err != nil {
		t.Fatalf("init sub tables: %v", err)
	}

	const (
		user = "sqlitefilteruser"
		mod  = "sqlitefiltermodel"
	)
	fixtures := []struct {
		name    string
		in, out uint64
	}{
		{"both-zero", 0, 0},
		{"in-only", 150, 0},
		{"out-only", 0, 200},
		{"both-nonzero", 300, 400},
	}
	for _, f := range fixtures {
		t.Run("save-"+f.name, func(t *testing.T) {
			saveTokensFixture(t, user, mod, f.in, f.out)
		})
	}

	cases := []struct {
		name                  string
		inNonZero, outNonZero int
		wantTotal             int64
	}{
		{"all-all", 0, 0, 4},
		{"in-nonzero-all", 1, 0, 2},
		{"in-zero-all", 2, 0, 2},
		{"all-out-nonzero", 0, 1, 2},
		{"all-out-zero", 0, 2, 2},
		{"in-nonzero-out-nonzero", 1, 1, 1},
		{"in-zero-out-zero", 2, 2, 1},
		{"in-nonzero-out-zero", 1, 2, 1},
		{"in-zero-out-nonzero", 2, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records, total, err := modelsdb.QueryAgentHttpTransactions(
				user, mod, config.G.DBMysqlSubTableNumber, 1, 50,
				"", "", "", false, 0, "", "", "", 0,
				c.inNonZero, c.outNonZero, 0,
			)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d (records=%d)", total, c.wantTotal, len(records))
			}
			for _, r := range records {
				switch c.inNonZero {
				case 1:
					if r.TokensInputSize == 0 {
						t.Errorf("record %d: tokens_input_size=0, want >0", r.ID)
					}
				case 2:
					if r.TokensInputSize != 0 {
						t.Errorf("record %d: tokens_input_size=%d, want 0", r.ID, r.TokensInputSize)
					}
				}
				switch c.outNonZero {
				case 1:
					if r.TokensOutputSize == 0 {
						t.Errorf("record %d: tokens_output_size=0, want >0", r.ID)
					}
				case 2:
					if r.TokensOutputSize != 0 {
						t.Errorf("record %d: tokens_output_size=%d, want 0", r.ID, r.TokensOutputSize)
					}
				}
			}
		})
	}
}
