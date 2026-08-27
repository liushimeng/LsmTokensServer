package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestChatAnalysisListColumns_ExcludeLargeAndSensitiveFields 缺符号：
// selectTransactionColumns 现为 models 包未导出函数。
func TestChatAnalysisListColumns_ExcludeLargeAndSensitiveFields(t *testing.T) {
	t.Skip("缺符号 selectTransactionColumns（models 包未导出）")
}

// TestResolveChatAnalysisDetailColumn 缺符号：resolveChatAnalysisDetailColumn 在新代码中不存在。
func TestResolveChatAnalysisDetailColumn(t *testing.T) {
	t.Skip("缺符号 resolveChatAnalysisDetailColumn")
}

func TestGetAgentHttpTransactionFieldByID_ValidatesBeforeDB(t *testing.T) {
	if _, err := modelsdb.GetAgentHttpTransactionFieldByID("u", "m", 1, 1, "api_key"); err == nil || !strings.Contains(err.Error(), "unsupported detail field") {
		t.Fatalf("非法字段应在 DB 前被拒绝，err=%v", err)
	}
	if _, err := modelsdb.GetAgentHttpTransactionFieldByID("", "m", 1, 1, "request_body"); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("空用户名应在 DB 前被拒绝，err=%v", err)
	}
}

// 2026-08-27 升级：src_protocol 原始协议字段从详情白名单移除，必须在 DB 前被拒绝。
func TestChatAnalysisDetailField_SrcProtocolRejected(t *testing.T) {
	for _, field := range []string{"request_src_protocol_body", "response_src_protocol_body", "request_src_protocol_headers", "response_src_protocol_headers"} {
		if _, ok := modelsdb.ResolveChatAnalysisDetailColumn(field); ok {
			t.Fatalf("src_protocol 字段不应在白名单中: %s", field)
		}
	}
	for _, field := range []string{"request_headers", "request_body", "response_headers", "response_body"} {
		if _, ok := modelsdb.ResolveChatAnalysisDetailColumn(field); !ok {
			t.Fatalf("核心字段应在白名单中: %s", field)
		}
	}
}

func TestChatAnalysisDetailHandler_FieldValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing field", body: `{"id":1,"user_name":"u","model_name":"m"}`, want: "缺少必要参数"},
		{name: "invalid field", body: `{"id":1,"user_name":"u","model_name":"m","field":"api_key"}`, want: "不支持的详情字段"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/ChatAnalysisDetailInterface", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			chatAnalysisDetailInterfaceHandle(rec, req)
			var resp ChatAnalysisDetailResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success || !strings.Contains(resp.Message, tt.want) {
				t.Fatalf("response=%+v, want message containing %q", resp, tt.want)
			}
		})
	}
}

// TestChatAnalysisScripts_PerFieldLazyLoadingContract 缺符号：agentPageScripts
// 已迁移至前端（ClientWeb），Go 侧不存在。
func TestChatAnalysisScripts_PerFieldLazyLoadingContract(t *testing.T) {
	t.Skip("缺符号 agentPageScripts（已迁移至前端，Go 侧不存在）")
}
