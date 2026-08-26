package api

// 20260826 时间跨度动态档位：/TimeSpanConfigInterface 与 timeSpanMaxDays 单元测试
// （ClampStatsSpan / SpanCutoffTime / SpanHours 的模型层测试见 models 包）

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// TestTimeSpanMaxDays 由保留天数推导档位上限的边界行为
func TestTimeSpanMaxDays(t *testing.T) {
	cases := []struct {
		retention, want int
		desc            string
	}{
		{32, 33, "默认 32 天保留 → 33 天上限"},
		{45, 46, "45 天保留 → 46 天上限"},
		{1, 2, "1 天保留 → 2 天上限"},
		{0, 365, "0=禁用清理 → 回落 365 天统计上限"},
		{-1, 365, "非法负值 → 视同禁用回落 365"},
		{364, 365, "364 天保留 → 365（恰好等于统计上限）"},
		{365, 365, "365 天保留 → 封顶 365（不超统计上限）"},
		{3650, 365, "10 年保留 → 仍封顶 365"},
	}
	for _, c := range cases {
		if got := timeSpanMaxDays(c.retention); got != c.want {
			t.Errorf("%s: timeSpanMaxDays(%d)=%d, want %d", c.desc, c.retention, got, c.want)
		}
	}
}

// TestTimeSpanConfigInterface_Handler GET 返回结构化档位配置
func TestTimeSpanConfigInterface_Handler(t *testing.T) {
	old := config.G
	config.G = &config.LsmTokensServerConfig{TransactionRetentionDays: 45}
	defer func() { config.G = old }()

	req := httptest.NewRequest(http.MethodGet, "/TimeSpanConfigInterface", nil)
	rec := httptest.NewRecorder()
	timeSpanConfigInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp timeSpanConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, body=%s", rec.Body.String())
	}
	if resp.TransactionRetentionDays != 45 {
		t.Errorf("transaction_retention_days = %d, want 45", resp.TransactionRetentionDays)
	}
	if resp.MaxSpanDays != 46 {
		t.Errorf("max_span_days = %d, want 46", resp.MaxSpanDays)
	}
	if resp.MaxSpanHours != 46*24 {
		t.Errorf("max_span_hours = %d, want %d", resp.MaxSpanHours, 46*24)
	}
	if resp.MinSpanHours != 1 {
		t.Errorf("min_span_hours = %d, want 1", resp.MinSpanHours)
	}
	if resp.Levels != 10 {
		t.Errorf("levels = %d, want 10", resp.Levels)
	}
}

// TestTimeSpanConfigInterface_NilConfig config.G 未初始化时不 panic，回落禁用语义
func TestTimeSpanConfigInterface_NilConfig(t *testing.T) {
	old := config.G
	config.G = nil
	defer func() { config.G = old }()

	req := httptest.NewRequest(http.MethodGet, "/TimeSpanConfigInterface", nil)
	rec := httptest.NewRecorder()
	timeSpanConfigInterfaceHandle(rec, req)

	var resp timeSpanConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if !resp.Success || resp.MaxSpanDays != 365 {
		t.Fatalf("nil config: success=%v max_span_days=%d, want true/365", resp.Success, resp.MaxSpanDays)
	}
}

// TestTimeSpanConfigInterface_MethodGuard 仅 GET
func TestTimeSpanConfigInterface_MethodGuard(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/TimeSpanConfigInterface", nil)
	rec := httptest.NewRecorder()
	timeSpanConfigInterfaceHandle(rec, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp["success"] == true {
		t.Fatalf("POST should be rejected, body=%s", rec.Body.String())
	}
}
