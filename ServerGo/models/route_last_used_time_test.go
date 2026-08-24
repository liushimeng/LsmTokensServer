package models

import (
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/protocol"
)

// insertRouteLastUsedFixture 向交易分表插入一条时间戳为指定偏移量的记录。
// 用于 GetRouteLastUsedTime 的 created_at 时间过滤 / 排序测试。
func insertRouteLastUsedFixture(t *testing.T, userName, modelName string, protocolType int, age time.Duration) {
	t.Helper()
	tableName := GetAgentHttpTableName(userName, modelName, testCfg.DBMysqlSubTableNumber)
	ts := time.Now().Add(-age)
	item := &TAgentHttpTransactionDataItem{
		CreatedAt:        ts,
		UpdatedAt:        ts,
		UserName:         userName,
		ModelName:        modelName,
		DstModelName:     "dst-" + modelName,
		ProtocolType:     protocolType,
		TokensInputSize:  10,
		TokensOutputSize: 10,
		TokensAllSize:    20,
	}
	if err := database.DB.Table(tableName).Create(item).Error; err != nil {
		t.Fatalf("create route last-used fixture failed: %v", err)
	}
}

// TestGetRouteLastUsedTime_NoRecord 验证当 user+model 在分表内没有任何记录时，
// 返回零值 time.Time（不报错），前端据此展示「未使用」。
func TestGetRouteLastUsedTime_NoRecord(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	got, err := GetRouteLastUsedTime("nobody", "nope-model", 0, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("GetRouteLastUsedTime on empty should not error: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero time for empty records, got %v", got)
	}
}

// TestGetRouteLastUsedTime_PicksLatest 验证取最新一条记录。
// 三条记录按"老→新"顺序插入，最后插入的是 30 分钟前那条，应被 ORDER BY id DESC LIMIT 1 选中。
// 实际生产场景 id 与 created_at 单调同序（请求发生顺序即写入顺序），
// id DESC LIMIT 1 等价于 created_at DESC LIMIT 1，命中主键索引 O(1)。
func TestGetRouteLastUsedTime_PicksLatest(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	userName := "alice"
	modelName := "latest-pick"

	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_Anthropic, 5*24*time.Hour) // 5 天前（最早插入）
	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_Anthropic, 2*24*time.Hour) // 2 天前
	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_Anthropic, 30*time.Minute) // 30 分钟前（最后插入，应胜出）

	got, err := GetRouteLastUsedTime(userName, modelName, 0, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("GetRouteLastUsedTime failed: %v", err)
	}
	if got.IsZero() {
		t.Fatalf("expected non-zero time, got zero")
	}
	// 30 分钟前那条记录的时间戳应该在「查询时刻前 ~40 分钟 ~ 20 分钟」范围内
	if got.After(time.Now()) {
		t.Fatalf("last used time %v is in the future", got)
	}
	if time.Since(got) > 40*time.Minute || time.Since(got) < 20*time.Minute {
		t.Fatalf("last used time %v should be ~30 min ago (got since=%v)", got, time.Since(got))
	}
}

// TestGetRouteLastUsedTime_ProtocolFilter 验证 protocolType>0 时只统计对应协议的记录。
// 插入 2 条 Anthropic + 1 条 OpenAI，protocolType=1 应返回 2 条 Anthropic 中最新的；
// protocolType=2 应返回 OpenAI 那条。
func TestGetRouteLastUsedTime_ProtocolFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	userName := "bob"
	modelName := "proto-filter"

	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_Anthropic, 3*time.Hour)    // Anthropic 老
	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_Anthropic, 30*time.Minute) // Anthropic 新
	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_OpenAI, 1*time.Hour)

	// 仅 Anthropic
	anthro, err := GetRouteLastUsedTime(userName, modelName, protocol.AgentProtocolType_Anthropic, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("Anthropic lookup failed: %v", err)
	}
	if anthro.IsZero() {
		t.Fatalf("Anthropic last used time should not be zero")
	}
	// 应该是 30 分钟前那条（最新 Anthropic）
	if time.Since(anthro) > 2*time.Hour {
		t.Fatalf("Anthropic last used time %v should be ~30 min ago", anthro)
	}

	// 仅 OpenAI
	openai, err := GetRouteLastUsedTime(userName, modelName, protocol.AgentProtocolType_OpenAI, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("OpenAI lookup failed: %v", err)
	}
	if openai.IsZero() {
		t.Fatalf("OpenAI last used time should not be zero")
	}
	// OpenAI 那条是 1 小时前
	if time.Since(openai) > 2*time.Hour || time.Since(openai) < 30*time.Minute {
		t.Fatalf("OpenAI last used time %v should be ~1 hour ago", openai)
	}
}

// TestGetRouteLastUsedTime_ProtocolFilterNoMatch 验证 protocolType=1 但只有 OpenAI 记录时返回零值。
func TestGetRouteLastUsedTime_ProtocolFilterNoMatch(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	userName := "carol"
	modelName := "proto-nomatch"

	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_OpenAI, 10*time.Minute)

	got, err := GetRouteLastUsedTime(userName, modelName, protocol.AgentProtocolType_Anthropic, testCfg.DBMysqlSubTableNumber)
	if err != nil {
		t.Fatalf("filter no match should not error: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero time when protocol filter has no match, got %v", got)
	}
}

// TestGetRouteLastUsedTime_DefaultSubTableNum 验证 subTableNum<=0 时使用默认值。
func TestGetRouteLastUsedTime_DefaultSubTableNum(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	userName := "dave"
	modelName := "default-table-num"

	insertRouteLastUsedFixture(t, userName, modelName, protocol.AgentProtocolType_Anthropic, 1*time.Hour)

	// subTableNum=0 应被归一化为 DEFAULT_SUB_TABLE_NUM
	got, err := GetRouteLastUsedTime(userName, modelName, 0, 0)
	if err != nil {
		t.Fatalf("subTableNum=0 should not error: %v", err)
	}
	if got.IsZero() {
		t.Fatalf("expected non-zero time with default sub-table num")
	}
}
