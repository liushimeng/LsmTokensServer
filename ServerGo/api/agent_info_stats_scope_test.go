package api

// 20260923：/AgentInfoInterface action=stats 管理端「单用户单模型」分支回归测试。
// 构造同分表碰撞数据，验证 scoped 查询不再串入同表其它 (user, model) 的 Agent 统计
// （此前表现为：选择任何模型都出现未使用的 LsmAgentGame-City-Human 等无关 Agent）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// findCollidingModelForTable 枚举模型名，找到与 targetTable 同分表的 (userName, model) 组合。
func findCollidingModelForTable(t *testing.T, targetTable, userName string) string {
	t.Helper()
	for i := 0; i < 512; i++ {
		m := fmt.Sprintf("collide-%d", i)
		if modelsdb.GetAgentHttpTableName(userName, m, config.G.DBMysqlSubTableNumber) == targetTable {
			return m
		}
	}
	t.Fatalf("no colliding model found for %s (user=%s)", targetTable, userName)
	return ""
}

func TestAgentInfoInterfaceStatsScopedByUserModel(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// alice/model-a 自身数据
	insertAgentInfoUsageFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)

	// 同分表碰撞干扰数据：其他用户 + 同用户其它模型
	targetTable := modelsdb.GetAgentHttpTableName("alice", "model-a", config.G.DBMysqlSubTableNumber)
	bobModel := findCollidingModelForTable(t, targetTable, "bob")
	insertAgentInfoUsageFixture(t, "bob", bobModel, "LsmAgentGame-City-Human", 1, 1, 2)
	aliceOther := findCollidingModelForTable(t, targetTable, "alice")
	insertAgentInfoUsageFixture(t, "alice", aliceOther, "other-agent", 5, 5, 10)

	req := httptest.NewRequest(http.MethodPost, "/AgentInfoInterface",
		bytes.NewBufferString(`{"action":"stats","days":0,"user_name":"alice","model_name":"model-a"}`))
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
		t.Fatalf("response not success: %s", rec.Body.String())
	}
	if resp.Data.Summary.TotalCallCount != 1 || resp.Data.Summary.TokensAllSize != 300 {
		t.Fatalf("scoped summary leaked cross-model/user data: %+v", resp.Data.Summary)
	}
	if len(resp.Data.Agents) != 1 || resp.Data.Agents[0].AgentToolName != "claude-cli" {
		t.Fatalf("scoped agents leaked cross-model/user data: %+v", resp.Data.Agents)
	}
}
