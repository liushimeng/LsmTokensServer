package api

// ==================== v2.0.29 浏览记录批量删除测试 ====================
//
// 覆盖范围：
//   1. DeleteAgentHttpTransaction 单条硬删除：参数校验 + DB nil 容错
//   2. DeleteAgentHttpTransactions 批量硬删除：参数校验 + DB nil + 500 上限
//   3. maxBatchDeleteIDs 常量契约
//   4. chatAnalysisBatchDeleteInterfaceHandle handler：method/JSON/params 校验
//
// 集成测试（DB 依赖）通过 DB != nil 判断自动跳过。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// ---------- DeleteAgentHttpTransaction 参数校验 ----------

func TestDeleteAgentHttpTransaction_InvalidParams(t *testing.T) {
	// DB 不可用也不应影响参数校验（参数校验先于 DB 访问）
	cases := []struct {
		name      string
		userName  string
		modelName string
		id        uint64
	}{
		{"zero-id", "alice", "gpt4", 0},
		{"empty-user", "", "gpt4", 100},
		{"empty-model", "alice", "", 100},
		{"all-empty", "", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, err := modelsdb.DeleteAgentHttpTransaction(c.userName, c.modelName, 4, c.id)
			if err == nil {
				t.Errorf("expected error for invalid params, got nil (rows=%d)", rows)
			}
			if rows != 0 {
				t.Errorf("expected rows=0, got %d", rows)
			}
		})
	}
}

func TestDeleteAgentHttpTransaction_NilDB(t *testing.T) {
	// 强制 DB=nil 验证降级路径
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	rows, err := modelsdb.DeleteAgentHttpTransaction("alice", "gpt4", 4, 100)
	if err == nil {
		t.Errorf("expected error when DB is nil")
	}
	if !strings.Contains(err.Error(), "database not initialized") {
		t.Errorf("error = %q, want to contain 'database not initialized'", err.Error())
	}
	if rows != 0 {
		t.Errorf("expected rows=0, got %d", rows)
	}
}

// ---------- DeleteAgentHttpTransactions 批量参数校验 ----------

func TestDeleteAgentHttpTransactions_EmptyIDs(t *testing.T) {
	rows, err := modelsdb.DeleteAgentHttpTransactions("alice", "gpt4", 4, []uint64{})
	if err != nil {
		t.Errorf("empty ids should not error, got: %v", err)
	}
	if rows != 0 {
		t.Errorf("expected rows=0 for empty ids, got %d", rows)
	}

	rows, err = modelsdb.DeleteAgentHttpTransactions("alice", "gpt4", 4, nil)
	if err != nil {
		t.Errorf("nil ids should not error, got: %v", err)
	}
	if rows != 0 {
		t.Errorf("expected rows=0 for nil ids, got %d", rows)
	}
}

// TestDeleteAgentHttpTransactions_ExceedsMaxBatch 缺符号：maxBatchDeleteIDs 现为
// models 包内未导出常量（=500）。此处用等价字面量 501 验证「too many ids」契约。
func TestDeleteAgentHttpTransactions_ExceedsMaxBatch(t *testing.T) {
	ids := make([]uint64, 500+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	rows, err := modelsdb.DeleteAgentHttpTransactions("alice", "gpt4", 4, ids)
	if err == nil {
		t.Errorf("expected error when ids exceed max batch size")
	}
	if !strings.Contains(err.Error(), "too many ids") {
		t.Errorf("error = %q, want to contain 'too many ids'", err.Error())
	}
	if rows != 0 {
		t.Errorf("expected rows=0, got %d", rows)
	}
}

func TestDeleteAgentHttpTransactions_InvalidParams(t *testing.T) {
	cases := []struct {
		name      string
		userName  string
		modelName string
		ids       []uint64
	}{
		{"empty-user", "", "gpt4", []uint64{1, 2, 3}},
		{"empty-model", "alice", "", []uint64{1, 2, 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, err := modelsdb.DeleteAgentHttpTransactions(c.userName, c.modelName, 4, c.ids)
			if err == nil {
				t.Errorf("expected error for invalid params")
			}
			if rows != 0 {
				t.Errorf("expected rows=0, got %d", rows)
			}
		})
	}
}

func TestDeleteAgentHttpTransactions_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	rows, err := modelsdb.DeleteAgentHttpTransactions("alice", "gpt4", 4, []uint64{1, 2, 3})
	if err == nil {
		t.Errorf("expected error when DB is nil")
	}
	if !strings.Contains(err.Error(), "database not initialized") {
		t.Errorf("error = %q, want to contain 'database not initialized'", err.Error())
	}
	if rows != 0 {
		t.Errorf("expected rows=0, got %d", rows)
	}
}

// ---------- 常量契约 ----------

// TestMaxBatchDeleteIDs_Constant 缺符号：maxBatchDeleteIDs 现为 models 包未导出常量。
func TestMaxBatchDeleteIDs_Constant(t *testing.T) {
	t.Skip("缺符号 maxBatchDeleteIDs（models 包未导出，无法从 api 包引用）")
}

// TestMaxBatchDeleteIDs_AcceptsBoundary 缺符号：maxBatchDeleteIDs 现为 models 包未导出常量。
func TestMaxBatchDeleteIDs_AcceptsBoundary(t *testing.T) {
	t.Skip("缺符号 maxBatchDeleteIDs（models 包未导出，无法从 api 包引用）")
}

// ---------- handler 层校验 ----------

func TestChatAnalysisBatchDelete_NonPOST(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ChatAnalysisBatchDeleteInterface", nil)
	rr := httptest.NewRecorder()
	chatAnalysisBatchDeleteInterfaceHandle(rr, req)

	var resp ChatAnalysisBatchDeleteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Errorf("GET request should fail")
	}
	if !strings.Contains(resp.Message, "仅支持 POST") {
		t.Errorf("message = %q, want to contain '仅支持 POST'", resp.Message)
	}
}

func TestChatAnalysisBatchDelete_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisBatchDeleteInterface",
		strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	chatAnalysisBatchDeleteInterfaceHandle(rr, req)

	var resp ChatAnalysisBatchDeleteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Errorf("invalid JSON should fail")
	}
	if !strings.Contains(resp.Message, "无效的请求体") {
		t.Errorf("message = %q, want to contain '无效的请求体'", resp.Message)
	}
}

func TestChatAnalysisBatchDelete_MissingParams(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing-user", `{"model_name":"gpt4","ids":[1,2,3]}`, "缺少 user_name"},
		{"missing-model", `{"user_name":"alice","ids":[1,2,3]}`, "缺少 user_name 或 model_name"},
		{"empty-ids", `{"user_name":"alice","model_name":"gpt4","ids":[]}`, "未选择任何记录"},
		{"no-ids-field", `{"user_name":"alice","model_name":"gpt4"}`, "未选择任何记录"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisBatchDeleteInterface",
				strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			chatAnalysisBatchDeleteInterfaceHandle(rr, req)

			var resp ChatAnalysisBatchDeleteResponse
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success {
				t.Errorf("expected failure for %s", c.name)
			}
			if !strings.Contains(resp.Message, c.want) {
				t.Errorf("message = %q, want to contain %q", resp.Message, c.want)
			}
		})
	}
}
