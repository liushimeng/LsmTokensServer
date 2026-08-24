package spider

// ==================== v2.0.24 MCP /InputSpiderDailyInfo 空记录防护测试（spider 侧） ====================
//
// 覆盖范围：
//   1. MCPInputSpiderDailyInfoHandler 拒绝空 payload（handler 层校验）
//   2. invalidateSpiderDailyInfoCache 写后失效（直接覆盖缓存 map 检查）
//
// （IsEmptySpiderDailyInfo / SaveSpiderDailyInfo 已拆至 models 包；
//   handleSpiderDailyInfoDelete / handleSpiderDailyInfoBatchDelete 已拆至 api 包。）

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
)

// ---------- MCPInputSpiderDailyInfoHandler 校验 ----------

func TestMCPInputSpiderDailyInfoHandler_RejectsEmptyPayload(t *testing.T) {
	// 仅在 database.DB 不可用时测试纯 handler 校验路径，避免污染生产数据
	if database.DB != nil {
		t.Skip("跳过：database.DB 已初始化，避免污染生产数据")
	}

	cases := []struct {
		name    string
		body    string
		wantMsg string
		wantOK  bool
	}{
		{
			name:    "empty-body",
			body:    `{}`,
			wantMsg: "data_source_id is required",
			wantOK:  false,
		},
		{
			name:    "missing-title",
			body:    `{"data_source_id":1,"url":"https://x","content":"c","platform_name":"p"}`,
			wantMsg: "title is required and must be non-empty",
			wantOK:  false,
		},
		{
			name:    "missing-url",
			body:    `{"data_source_id":1,"title":"t","content":"c","platform_name":"p"}`,
			wantMsg: "url is required and must be non-empty",
			wantOK:  false,
		},
		{
			name:    "missing-content",
			body:    `{"data_source_id":1,"title":"t","url":"https://x","platform_name":"p"}`,
			wantMsg: "content is required and must be non-empty",
			wantOK:  false,
		},
		{
			name:    "missing-platform",
			body:    `{"data_source_id":1,"title":"t","url":"https://x","content":"c"}`,
			wantMsg: "platform_name is required and must be non-empty",
			wantOK:  false,
		},
		{
			name:    "whitespace-title",
			body:    `{"data_source_id":1,"title":"   ","url":"https://x","content":"c","platform_name":"p"}`,
			wantMsg: "title is required and must be non-empty",
			wantOK:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/InputSpiderDailyInfo", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			MCPInputSpiderDailyInfoHandler(rr, req)

			var resp MCPAPIResponse
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success != c.wantOK {
				t.Errorf("success = %v, want %v (msg=%s)", resp.Success, c.wantOK, resp.Message)
			}
			if !strings.Contains(resp.Message, c.wantMsg) {
				t.Errorf("message = %q, want to contain %q", resp.Message, c.wantMsg)
			}
		})
	}
}

func TestMCPInputSpiderDailyInfoHandler_RejectsNonPOST(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/InputSpiderDailyInfo", nil)
	rr := httptest.NewRecorder()
	MCPInputSpiderDailyInfoHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestMCPInputSpiderDailyInfoHandler_RejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/InputSpiderDailyInfo", strings.NewReader("{not-json"))
	rr := httptest.NewRecorder()
	MCPInputSpiderDailyInfoHandler(rr, req)

	var resp MCPAPIResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Errorf("expected failure on invalid JSON")
	}
	if !strings.Contains(resp.Message, "Invalid request body") {
		t.Errorf("message = %q, want Invalid request body", resp.Message)
	}
}
