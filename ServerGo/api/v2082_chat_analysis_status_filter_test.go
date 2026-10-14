package api

// 阶段CV（20261014）：/ChatAnalysis「状态」筛选查询优化回归测试。
//
// 背景：response_status 列存的是 http.Response.Status 完整文本（"200 OK"、"529 "），
// 旧实现 `response_status = '200'` 全串精确匹配永远命中 0 行，状态筛选实际失效。
// 阶段CV 改为状态码前缀匹配（LIKE 'code%' / NOT LIKE 'code%'）+ NormalizeStatusCodeFilter 净化。
//
// 覆盖：
//  1. NormalizeStatusCodeFilter 单元测试（纯数字 / 带短语脏值 / 空白 / 非法 / 超 3 位）
//  2. 集成测试：真实分表写入 4 条不同状态文本记录 →
//     正查（200/529 尾随空格/前缀 5）· 取反 · 非法输入忽略 · 空值全量

import (
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
)

// TestNormalizeStatusCodeFilter 单元测试：状态筛选输入净化
func TestNormalizeStatusCodeFilter(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"200", "200"},               // 标准纯数字
		{"429", "429"},               // 限流码
		{"529", "529"},               // Anthropic 限流码
		{"200 OK", "200"},            // 带短语脏值（旧客户端可能直传完整状态文本）
		{" 404 ", "404"},             // 首尾空白
		{"529 ", "529"},              // 尾随空格（模拟库内 "529 " 值形态）
		{"2", "2"},                   // 1 位前缀（手输过程态，命中全部 2xx）
		{"20", "20"},                 // 2 位前缀
		{"1234", "123"},              // 超 3 位截断
		{"12x3", "12"},               // 数字后跟非数字，截断
		{"", ""},                     // 空值 → 不生效
		{"   ", ""},                  // 纯空白 → 不生效
		{"abc", ""},                  // 非数字 → 不生效
		{"-1", ""},                   // 负号非数字 → 不生效
		{"Too Many Requests", ""},    // 纯短语 → 不生效
	}
	for _, c := range cases {
		if got := modelsdb.NormalizeStatusCodeFilter(c.in); got != c.want {
			t.Errorf("NormalizeStatusCodeFilter(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// saveStatusFilterRecord 向测试分表写入一条指定状态文本的交易记录
func saveStatusFilterRecord(t *testing.T, userName, modelName, status string) {
	t.Helper()
	err := modelsdb.SaveAgentHttpTransaction(
		userName, modelName,
		1,
		"sk-status-filter-test", 42,
		0,
		"dst-model-status",
		protocol.AgentProtocolType_Anthropic,
		"POST", "https://api.test.com/v1/messages", "127.0.0.1:12345",
		100,
		"Content-Type: application/json", "Content-Type: application/json", `e30=`, "",
		status, int64(len(status)),
		"Content-Type: application/json", "Content-Type: application/json", `{"ok":true}`, "",
		time.Now(), time.Now(), time.Now(), time.Now(),
		150,
		"TestTool/1.0",
		"",                   // agentToolName
		"",                   // agentToolInfo
		"unknown_session_id", // 测试占位 session_id
		"",                   // agentToolSessionID
		config.G.DBMysqlSubTableNumber,
		0, 0, 0,
	)
	if err != nil {
		t.Fatalf("save transaction with status %q failed: %v", status, err)
	}
}

// queryByStatusFilter 按状态筛选查询（其余筛选条件全空）
func queryByStatusFilter(t *testing.T, userName, modelName, status string, not bool) ([]modelsdb.TAgentHttpTransactionDataItem, int64) {
	t.Helper()
	records, total, err := modelsdb.QueryAgentHttpTransactions(
		userName, modelName, config.G.DBMysqlSubTableNumber, 1, 10,
		"", "", status, not, 0, "", "", "", 3, 0, 0, 0,
	)
	if err != nil {
		t.Fatalf("query with status=%q not=%v failed: %v", status, not, err)
	}
	return records, total
}

// TestChatAnalysisStatusCodePrefixFilter 集成测试：状态码前缀匹配全路径
func TestChatAnalysisStatusCodePrefixFilter(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	const (
		user  = "statusfilteruser"
		model = "model-status"
	)
	// 写入 4 条记录，覆盖库内真实状态文本形态
	saveStatusFilterRecord(t, user, model, "200 OK")
	saveStatusFilterRecord(t, user, model, "404 Not Found")
	saveStatusFilterRecord(t, user, model, "529 ") // Anthropic 限流：码 + 尾随空格，无短语
	saveStatusFilterRecord(t, user, model, "500 Internal Server Error")

	// 1. 旧精确匹配失效问题的回归：'200' 应命中 "200 OK"（旧实现 = '200' 返回 0）
	records, total := queryByStatusFilter(t, user, model, "200", false)
	if total != 1 || len(records) != 1 {
		t.Fatalf("filter 200: total=%d len=%d, want 1/1（状态码前缀匹配应命中 '200 OK'）", total, len(records))
	}
	if records[0].ResponseStatus != "200 OK" {
		t.Errorf("filter 200 returned response_status=%q, want %q", records[0].ResponseStatus, "200 OK")
	}

	// 2. 529（尾随空格形态）应命中
	_, total = queryByStatusFilter(t, user, model, "529", false)
	if total != 1 {
		t.Fatalf("filter 529: total=%d, want 1（'529 ' 尾随空格值应命中）", total)
	}

	// 3. 404 应命中
	_, total = queryByStatusFilter(t, user, model, "404", false)
	if total != 1 {
		t.Fatalf("filter 404: total=%d, want 1", total)
	}

	// 4. 1 位前缀 '5' → 命中全部 5xx（500 + 529）
	_, total = queryByStatusFilter(t, user, model, "5", false)
	if total != 2 {
		t.Fatalf("filter 5 (prefix): total=%d, want 2（500 + 529）", total)
	}

	// 5. 取反：'2' NOT → 排除 200，剩 3 条
	_, total = queryByStatusFilter(t, user, model, "2", true)
	if total != 3 {
		t.Fatalf("filter 2 NOT: total=%d, want 3（404/529/500）", total)
	}

	// 6. 取反：'529' NOT → 排除 529，剩 3 条
	_, total = queryByStatusFilter(t, user, model, "529", true)
	if total != 3 {
		t.Fatalf("filter 529 NOT: total=%d, want 3", total)
	}

	// 7. 脏值 "200 OK"（旧客户端完整状态文本）经净化后等同 "200"
	_, total = queryByStatusFilter(t, user, model, "200 OK", false)
	if total != 1 {
		t.Fatalf("filter '200 OK' (dirty): total=%d, want 1（净化后按 200 前缀匹配）", total)
	}

	// 8. 非法输入 "abc" → 筛选不生效，全量 4 条
	_, total = queryByStatusFilter(t, user, model, "abc", false)
	if total != 4 {
		t.Fatalf("filter 'abc' (invalid): total=%d, want 4（非法值应忽略筛选）", total)
	}

	// 9. 空值 → 全量 4 条
	_, total = queryByStatusFilter(t, user, model, "", false)
	if total != 4 {
		t.Fatalf("filter '': total=%d, want 4", total)
	}
}
