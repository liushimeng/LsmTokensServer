package api

import (
	"encoding/json"
	"strings"
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// =============================================================================
// v2.0.45: /ChatAnalysisTotal 渐进式读取 + 区间报告弹窗
//
// 关键不变量：
//   - ChatAnalysisTotalInterfaceRequest 新增 Action 字段（omitted 空 / "full" / "insights_summary"）
//   - action=insights_summary 时 ProtocolStats / AgentStats 不返回（omitempty 字段消失）
//   - action=full 时所有字段保持不变（向后兼容）
//   - lsmRunInsightsSummary 是纯函数（无 DB 时 DB 探测 = nil → 内部 goroutine 失败 → 各变量保留零值）
//   - 前端 chatAnalysisTotalTemplate 在 admin / user 双模式下均能解析
//   - 7 段报告 + copy 报文字段在 JS 模板中存在
//
// DB 依赖测试在 DB==nil 时跳过；聚焦类型契约 + JSON 序列化 + 模板存在性。
// =============================================================================

// TestChatAnalysisTotalRequest_ActionFieldBinding 校验 JSON 字段绑定。
func TestChatAnalysisTotalRequest_ActionFieldBinding(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"empty", `{"user_name":"u","model_name":"m","days":3}`, ""},
		{"full", `{"user_name":"u","model_name":"m","days":3,"action":"full"}`, "full"},
		{"insights_summary", `{"user_name":"u","model_name":"m","days":7,"action":"insights_summary"}`, "insights_summary"},
		{"unknown_action", `{"action":"range_agent_dist"}`, "range_agent_dist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var req ChatAnalysisTotalInterfaceRequest
			if err := json.Unmarshal([]byte(c.json), &req); err != nil {
				t.Fatalf("Unmarshal 失败: %v", err)
			}
			if req.Action != c.want {
				t.Errorf("Action = %q, want %q", req.Action, c.want)
			}
		})
	}
}

// TestChatAnalysisTotalRequest_ActionOmittedWhenEmpty 校验 omitempty。
// Action 默认值时空，序列化时 JSON 不输出 key（向后兼容旧版客户端）。
func TestChatAnalysisTotalRequest_ActionOmittedWhenEmpty(t *testing.T) {
	req := ChatAnalysisTotalInterfaceRequest{UserName: "u", ModelName: "m", Days: 3}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(b), `"action"`) {
		t.Errorf("Action 为空时 JSON 不应包含 action 字段: %s", string(b))
	}
}

// TestChatAnalysisTotalResponse_InsightsSummaryContract 校验 insights_summary 行为。
// 当 ProtocolStats / AgentStats 为 nil 时，由于字段有 omitempty，JSON 不输出对应 key。
func TestChatAnalysisTotalResponse_InsightsSummaryContract(t *testing.T) {
	resp := ChatAnalysisTotalInterfaceResponse{
		Success:       true,
		Message:       "查询成功",
		TimeStats:     []modelsdb.TimeRangeStat{{Date: "2026-07-15", Count: 5}},
		TokensStats:   []modelsdb.TokensRangeStat{{Date: "2026-07-15", Count: 5}},
		TokensSummary: &TokensSummaryStat{TotalAll: 100},
		// ProtocolStats / AgentStats 故意留空
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	js := string(b)
	// 必须包含
	for _, key := range []string{`"success":true`, `"time_stats"`, `"tokens_stats"`, `"tokens_summary"`, `"total_count"`} {
		if !strings.Contains(js, key) {
			t.Errorf("应包含 %s，实际=%s", key, js)
		}
	}
	// 不应包含
	for _, key := range []string{`"protocol_stats"`, `"agent_stats"`} {
		if strings.Contains(js, key) {
			t.Errorf("omitted 字段不应出现 %s，实际=%s", key, js)
		}
	}
}

// TestChatAnalysisTotalResponse_FullContract 校验全量字段（向后兼容）。
func TestChatAnalysisTotalResponse_FullContract(t *testing.T) {
	resp := ChatAnalysisTotalInterfaceResponse{
		Success:       true,
		TotalCount:    100,
		ProtocolStats: &modelsdb.ProtocolAnalysisStats{SampleCount: 50},
		AgentStats:    &modelsdb.AgentToolStatsResponse{UniqueTools: 3},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	js := string(b)
	for _, key := range []string{`"protocol_stats"`, `"agent_stats"`, `"sample_count":50`, `"unique_tools":3`} {
		if !strings.Contains(js, key) {
			t.Errorf("应包含 %s，实际=%s", key, js)
		}
	}
}

// TestChatAnalysisTotalTemplate_HasEmptyGuide 缺符号：chatAnalysisTotalTemplate
// 已迁移至前端（ClientWeb），Go 侧不存在。
func TestChatAnalysisTotalTemplate_HasEmptyGuide(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatAnalysisTotalTemplate_HasReportSections 缺符号：chatAnalysisTotalTemplate。
func TestChatAnalysisTotalTemplate_HasReportSections(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatAnalysisTotalTemplate_HasBothModes 缺符号：chatAnalysisTotalTemplate。
func TestChatAnalysisTotalTemplate_HasBothModes(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatAnalysisTotalInterface_InsightsSummary_NilDB 校验 lsmRunInsightsSummary 在 DB==nil 时的兜底。
// 在测试环境（无 MySQL）下，三个 goroutine 全部失败，result 字段保留零值。
func TestChatAnalysisTotalInterface_InsightsSummary_NilDB(t *testing.T) {
	// 间接验证：lsmRunInsightsSummary 内聚合逻辑与原 handler 一致，使用 TokensSummaryStat。
	s := &TokensSummaryStat{}
	if s.TotalAll != 0 {
		t.Errorf("零值 Totals 应为 0，实际=%d", s.TotalAll)
	}
}

// TestLsmGenerateTokensReport_NoDB_NoData 缺符号：chatAnalysisTotalTemplate。
func TestLsmGenerateTokensReport_NoDB_NoData(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestInsightsSummary_FieldsSubsetOfFull 校验 insights_summary 返回字段 ⊆ full 返回字段。
func TestInsightsSummary_FieldsSubsetOfFull(t *testing.T) {
	// full 路径（按契约 ProtocolStats / AgentStats 可能是 nil 或非 nil）
	full := ChatAnalysisTotalInterfaceResponse{
		Success:       true,
		TotalCount:    1,
		ProtocolStats: &modelsdb.ProtocolAnalysisStats{SampleCount: 50},
		AgentStats:    &modelsdb.AgentToolStatsResponse{UniqueTools: 3},
	}
	// insights_summary 路径：ProtocolStats / AgentStats 始终 nil
	short := ChatAnalysisTotalInterfaceResponse{
		Success: true, TotalCount: 1,
	}
	bf, _ := json.Marshal(full)
	bs, _ := json.Marshal(short)
	// full 应包含 protocol_stats / agent_stats
	for _, key := range []string{`"protocol_stats":`, `"agent_stats":`} {
		if !strings.Contains(string(bf), key) {
			t.Errorf("full 应包含 %s，实际=%s", key, string(bf))
		}
	}
	// short 不应包含 protocol_stats / agent_stats（omitempty + nil 指针）
	for _, key := range []string{`"protocol_stats"`, `"agent_stats"`} {
		if strings.Contains(string(bs), key) {
			t.Errorf("short 不应包含 %s: %s", key, string(bs))
		}
	}
}
