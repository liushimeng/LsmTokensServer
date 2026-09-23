package models

// 20260923：Agent 信息统计「分表过滤缺失」回归测试。
//
// 背景：交易分表按 (user_name, model_name) 哈希共享，同一张分表内有大量不同
// (user, model) 组合的记录。GetAgentInfoUsageStatsByUserModel /
// GetHourlyTrendByUserModel / GetHourlyTrendByUser 旧实现缺少 model_name（甚至
// user_name）过滤，导致单用户单模型视角串入同表其它模型/用户的统计
// （页面表现为：切换模型名统计不变、出现未使用的 Agent 如 LsmAgentGame-City-Human）。
//
// 本文件通过「构造同分表碰撞数据」验证修复后的隔离性。

import (
	"fmt"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

// insertScopeFixture 向指定 (user, model) 哈希分表插入一条带 AgentToolName 的记录。
func insertScopeFixture(t *testing.T, userName, modelName, agentToolName string, input, output, total uint64) {
	t.Helper()
	tableName := GetAgentHttpTableName(userName, modelName, testCfg.DBMysqlSubTableNumber)
	item := &TAgentHttpTransactionDataItem{
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		UserName:         userName,
		ModelName:        modelName,
		AgentToolName:    agentToolName,
		TokensInputSize:  input,
		TokensOutputSize: output,
		TokensAllSize:    total,
	}
	if err := database.DB.Table(tableName).Create(item).Error; err != nil {
		t.Fatalf("insert scope fixture %s/%s failed: %v", userName, modelName, err)
	}
}

// findCollidingModel 枚举模型名，找到一个与 targetTable 同分表的 (userName, model) 组合。
// 用于构造「同一张分表内多个 (user, model) 组合」的碰撞测试数据。
func findCollidingModel(t *testing.T, targetTable, userName string) string {
	t.Helper()
	for i := 0; i < 512; i++ {
		m := fmt.Sprintf("collide-%d", i)
		if GetAgentHttpTableName(userName, m, testCfg.DBMysqlSubTableNumber) == targetTable {
			return m
		}
	}
	t.Fatalf("no colliding model found for %s (user=%s)", targetTable, userName)
	return ""
}

// sumTrendPoints 趋势响应全部桶的调用次数/Tokens 合计（含零值桶）。
func sumTrendPoints(res *HourlyTrendResult) (count int64, tokensAll uint64) {
	for _, p := range res.Points {
		count += p.Count
		tokensAll += p.TokensTotal
	}
	return count, tokensAll
}

// 单用户单模型统计：不得串入同分表内「同用户其它模型」与「其他用户」的数据。
func TestAgentInfoUsageStatsByUserModel_ShardIsolation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)

	// 构造同分表碰撞干扰数据（collide-%d 命名保证不等于 model-a）
	targetTable := GetAgentHttpTableName("alice", "model-a", testCfg.DBMysqlSubTableNumber)
	bobModel := findCollidingModel(t, targetTable, "bob")
	insertScopeFixture(t, "bob", bobModel, "LsmAgentGame-City-Human", 1, 1, 2)
	aliceOther := findCollidingModel(t, targetTable, "alice")
	insertScopeFixture(t, "alice", aliceOther, "other-agent", 5, 5, 10)

	summary, stats, err := GetAgentInfoUsageStatsByUserModel("alice", "model-a", testCfg.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetAgentInfoUsageStatsByUserModel failed: %v", err)
	}
	if summary.AgentCount != 1 || summary.TotalCallCount != 1 ||
		summary.TokensAllSize != 300 || summary.TokensInputSize != 100 || summary.TokensOutputSize != 200 {
		t.Fatalf("summary leaked cross-model/user data: %+v", summary)
	}
	if len(stats) != 1 || stats[0].AgentToolName != "claude-cli" {
		t.Fatalf("stats leaked cross-model/user data: %+v", stats)
	}
}

// 单用户单模型趋势：不得串入同分表其它 (user, model) 的数据。
func TestHourlyTrendByUserModel_ShardIsolation(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)

	targetTable := GetAgentHttpTableName("alice", "model-a", testCfg.DBMysqlSubTableNumber)
	bobModel := findCollidingModel(t, targetTable, "bob")
	insertScopeFixture(t, "bob", bobModel, "LsmAgentGame-City-Human", 1, 1, 2)
	aliceOther := findCollidingModel(t, targetTable, "alice")
	insertScopeFixture(t, "alice", aliceOther, "other-agent", 5, 5, 10)

	res, err := GetHourlyTrendByUserModel("alice", "model-a", testCfg.DBMysqlSubTableNumber, 24)
	if err != nil {
		t.Fatalf("GetHourlyTrendByUserModel failed: %v", err)
	}
	count, tokensAll := sumTrendPoints(res)
	if count != 1 || tokensAll != 300 {
		t.Fatalf("trend leaked cross-model/user data: count=%d tokensAll=%d", count, tokensAll)
	}
}

// 用户端「本人全模型」趋势：只统计模型列表内的数据，列表外模型与其他用户不计入。
func TestHourlyTrendByUser_ModelListScope(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)
	// 列表外：alice 的另一个模型（即使可能落在同一张分表）
	insertScopeFixture(t, "alice", "model-b", "other-agent", 5, 5, 10)
	// 其他用户
	insertScopeFixture(t, "bob", "model-c", "LsmAgentGame-City-Human", 1, 1, 2)

	res, err := GetHourlyTrendByUser("alice", []string{"model-a"}, testCfg.DBMysqlSubTableNumber, 24)
	if err != nil {
		t.Fatalf("GetHourlyTrendByUser failed: %v", err)
	}
	count, tokensAll := sumTrendPoints(res)
	if count != 1 || tokensAll != 300 {
		t.Fatalf("user trend leaked out-of-list/other-user data: count=%d tokensAll=%d", count, tokensAll)
	}
}

// 用户端「本人全模型」统计：列表外模型不计入（旧实现按表名去重会泄漏）。
func TestAgentInfoUsageStatsByUser_ListScope(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)
	// 列表外模型（验证不泄漏）
	insertScopeFixture(t, "alice", "model-b", "other-agent", 5, 5, 10)

	summary, stats, err := GetAgentInfoUsageStatsByUser("alice", []string{"model-a"}, testCfg.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetAgentInfoUsageStatsByUser failed: %v", err)
	}
	if summary.AgentCount != 1 || summary.TotalCallCount != 1 || summary.TokensAllSize != 300 {
		t.Fatalf("user stats leaked out-of-list model data: %+v", summary)
	}
	if len(stats) != 1 || stats[0].AgentToolName != "claude-cli" {
		t.Fatalf("user stats leaked out-of-list model data: %+v", stats)
	}
}

// 全站趋势小时桶：created_at 非整点（如 17:49）的数据必须落入当前整点槽位。
// 20260923 修复：旧实现桶键用原始分钟格式化（"17:49"），与整点槽位（"17:00"）
// 永不匹配，小时粒度趋势全部掉零。
func TestHourlyTrendAll_HourBucketAlignment(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)
	insertScopeFixture(t, "bob", "model-b", "opencode", 10, 20, 30)

	res, err := GetHourlyTrendAll(testCfg.DBMysqlSubTableNumber, 24)
	if err != nil {
		t.Fatalf("GetHourlyTrendAll failed: %v", err)
	}
	var count int64
	var tokensAll uint64
	for _, p := range res.Points {
		count += p.Count
		tokensAll += p.TokensTotal
	}
	if count != 2 || tokensAll != 330 {
		t.Fatalf("hourly trend dropped rows (bucket key misaligned): count=%d tokensAll=%d want 2/330", count, tokensAll)
	}
	// 当前小时桶应承载全部数据
	currentKey := truncateToHour(time.Now()).Format("2006-01-02 15:04")
	for _, p := range res.Points {
		if p.Date == currentKey && p.Count != 2 {
			t.Fatalf("current hour bucket count=%d, want 2", p.Count)
		}
	}
}

// 用户端趋势 hour 粒度同样要求桶键与整点槽位对齐（含 scoped 过滤）。
func TestHourlyTrendByUserModel_HourBucketAlignment(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)

	res, err := GetHourlyTrendByUserModel("alice", "model-a", testCfg.DBMysqlSubTableNumber, 24)
	if err != nil {
		t.Fatalf("GetHourlyTrendByUserModel failed: %v", err)
	}
	var count int64
	for _, p := range res.Points {
		count += p.Count
	}
	if count != 1 {
		t.Fatalf("scoped hourly trend dropped rows: count=%d want 1", count)
	}
}

// 同分表多模型去重：模型列表内多个模型哈希到同一张分表时，各自只统计自己的行，不重复累加。
func TestAgentInfoUsageStatsByUser_SameTableModelsNoDoubleCount(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	insertScopeFixture(t, "alice", "model-a", "claude-cli", 100, 200, 300)

	// 找一个与 model-a 同表但不同名的 alice 模型，加入同一份模型列表
	targetTable := GetAgentHttpTableName("alice", "model-a", testCfg.DBMysqlSubTableNumber)
	aliceOther := findCollidingModel(t, targetTable, "alice")
	insertScopeFixture(t, "alice", aliceOther, "other-agent", 5, 5, 10)

	modelList := []string{"model-a", aliceOther}
	summary, _, err := GetAgentInfoUsageStatsByUser("alice", modelList, testCfg.DBMysqlSubTableNumber, 0)
	if err != nil {
		t.Fatalf("GetAgentInfoUsageStatsByUser failed: %v", err)
	}
	// 两个模型同表：各算各的（300 + 10），绝不能把整表算两遍
	if summary.TotalCallCount != 2 || summary.TokensAllSize != 310 {
		t.Fatalf("same-table multi-model stats wrong: calls=%d tokens=%d want 2/310",
			summary.TotalCallCount, summary.TokensAllSize)
	}
}
