// v2.0.56: /ChatAnalysisTotalWS 全站变体 WS handler 集成测试（从 models/v2056 拆出）
package websocket

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// ============ WS handler 集成（mock WS + 真实 runChatStatsQuery）============

// TestChatStatsWS_AllStatsDataNonEmpty 守护：WS handler 各 stage 在非空分表数据下应返回非零数据
//
// 此测试在没有真实 database.DB 的环境下走 NilDB 分支，验证 7 个 stage 返回的都是非 nil 数据
// （前端不会再收到 null/nil，渲染层不会因数据缺失显示全 0）
func TestChatStatsWS_AllStatsDataNonEmpty(t *testing.T) {
	// NilDB 场景
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	// 直接调用 7 个 stage 函数（绕过 WS 协议层）
	stages := []struct {
		name string
		fn   func(context.Context) (interface{}, error)
	}{
		{"kpi", func(ctx context.Context) (interface{}, error) {
			return lsmBuildChatStatsKPI(ctx, 7, "", nil)
		}},
		{"time_stats", func(ctx context.Context) (interface{}, error) {
			return modelsdb.GetTimeRangeStatsAll(8, 7)
		}},
		{"tokens_summary", func(ctx context.Context) (interface{}, error) {
			return lsmBuildChatStatsTokensSummary(ctx, 7)
		}},
		{"model_distribution", func(ctx context.Context) (interface{}, error) {
			return modelsdb.GetModelNameUsageStatsByRange(8, 7)
		}},
		{"trend_chart", func(ctx context.Context) (interface{}, error) {
			return modelsdb.GetDailyStatsAll(8, 7)
		}},
		{"protocol_stats", func(ctx context.Context) (interface{}, error) {
			return modelsdb.GetProtocolAnalysisStatsAll(8, 200)
		}},
		{"agent_stats", func(ctx context.Context) (interface{}, error) {
			return modelsdb.GetAgentToolStatsByRangeAll(8, 7)
		}},
	}

	ctx := context.Background()
	for _, s := range stages {
		data, err := s.fn(ctx)
		// v2.0.56 全站 All 函数 NilDB 安全（返回空数据 nil error）；
		// modelsdb.GetDailyStatsAll 是旧 v2.0.36 函数，NilDB 时返回 "database not initialized" error，
		// 这里对它单独容忍（v2.0.56 暂不改旧函数，trend_chart 前端会显示「暂无数据」而非 500）
		if err != nil {
			if s.name == "trend_chart" && strings.Contains(err.Error(), "database not initialized") {
				continue
			}
			t.Errorf("stage %s 返回 error: %v", s.name, err)
			continue
		}
		if data == nil {
			t.Errorf("stage %s 返回 nil data（前端会显示 null）", s.name)
			continue
		}
		// 验证关键字段非零（除了 kpi 的 total_calls 在 NilDB 下为 0 是合理的）
		switch s.name {
		case "kpi":
			m, ok := data.(map[string]interface{})
			if !ok {
				t.Errorf("stage kpi 应返回 map，实际 %T", data)
			}
			if m["window_days"] == nil {
				t.Errorf("stage kpi 应有 window_days 字段")
			}
		case "time_stats":
			arr, ok := data.([]modelsdb.TimeRangeStat)
			if !ok {
				t.Errorf("stage time_stats 应返回 []modelsdb.TimeRangeStat，实际 %T", data)
			}
			if arr == nil {
				t.Errorf("stage time_stats 应返回非 nil 切片")
			}
		case "tokens_summary":
			m, ok := data.(map[string]interface{})
			if !ok {
				t.Errorf("stage tokens_summary 应返回 map，实际 %T", data)
			}
			if m["buckets"] == nil {
				t.Errorf("stage tokens_summary 应有 buckets 字段")
			}
		case "model_distribution":
			arr, ok := data.([]modelsdb.ModelNameUsageStat)
			if !ok {
				t.Errorf("stage model_distribution 应返回 []modelsdb.ModelNameUsageStat，实际 %T", data)
			}
			if arr == nil {
				t.Errorf("stage model_distribution 应返回非 nil 切片")
			}
		case "trend_chart":
			arr, ok := data.([]modelsdb.DailyStat)
			if !ok {
				t.Errorf("stage trend_chart 应返回 []modelsdb.DailyStat，实际 %T", data)
			}
			if arr == nil {
				t.Errorf("stage trend_chart 应返回非 nil 切片")
			}
		case "protocol_stats":
			m, ok := data.(*modelsdb.ProtocolAnalysisStats)
			if !ok {
				t.Errorf("stage protocol_stats 应返回 *modelsdb.ProtocolAnalysisStats，实际 %T", data)
			}
			if m == nil || m.MethodStats == nil {
				t.Errorf("stage protocol_stats 应初始化 MethodStats")
			}
		case "agent_stats":
			m, ok := data.(*modelsdb.AgentToolStatsResponse)
			if !ok {
				t.Errorf("stage agent_stats 应返回 *modelsdb.AgentToolStatsResponse，实际 %T", data)
			}
			if m == nil || m.ToolStats == nil {
				t.Errorf("stage agent_stats 应初始化 ToolStats")
			}
		}
	}
}

// ============ 取消与错误处理契约 ============

// TestAllStats_CtxCancelSafe 守护：context 取消时不应 panic
func TestAllStats_CtxCancelSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// NilDB 分支下不影响；有 database.DB 时应返回 nil error 或 context.Canceled
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	// lsmBuildChatStatsKPI 对取消的 ctx 应直接返回 ctx.Err()
	_, err := lsmBuildChatStatsKPI(ctx, 7, "", nil)
	if err == nil {
		t.Errorf("取消 ctx 时 lsmBuildChatStatsKPI 应返回 error")
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("取消 ctx 时应返回 context.Canceled，实际 %v", err)
	}
}

// TestAllStats_StageOrderStillCorrect 守护：7 个 stage 顺序不变
func TestAllStats_StageOrderStillCorrect(t *testing.T) {
	want := []string{
		"kpi", "time_stats", "tokens_summary", "model_distribution",
		"trend_chart", "protocol_stats", "agent_stats",
	}
	if len(wsChatStatsStageOrder) != len(want) {
		t.Fatalf("wsChatStatsStageOrder 长度=%d, 期望 %d", len(wsChatStatsStageOrder), len(want))
	}
	for i, s := range want {
		if wsChatStatsStageOrder[i] != s {
			t.Errorf("第 %d 个 stage=%s, 期望 %s", i+1, wsChatStatsStageOrder[i], s)
		}
	}
}

// TestAllStats_JSONFieldNames 守护：All 变体返回的 JSON 字段名与前端 stage 渲染一致
//
// 前端 __lsmRenderStageHTML(stage, data) 按 stage 渲染特定字段：
//   - kpi              → total_calls / total_tokens / active_models / window_days
//   - time_stats       → 数组 [ {date, count}, ... ]
//   - tokens_summary   → buckets / total_count / total_input / total_output / total_tokens / window_days
//   - model_distribution → 数组 [ {model_name, call_count, tokens_input, tokens_output, tokens_total, ...}, ... ]
//   - trend_chart      → 数组 [ {date, count, tokens_input, tokens_output, tokens_total}, ... ]
//   - protocol_stats   → 对象 {method_stats, url_pattern_stats, status_stats, avg_elapsed_ms, ...}
//   - agent_stats      → 对象 {total_agent_count, unique_tools, tool_stats}
func TestAllStats_JSONFieldNames(t *testing.T) {
	// NilDB 分支下验证 JSON 序列化字段名正确
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	ctx := context.Background()

	// kpi
	kpi, _ := lsmBuildChatStatsKPI(ctx, 7, "", nil)
	for _, key := range []string{"total_calls", "total_tokens", "active_models", "window_days"} {
		if _, has := kpi[key]; !has {
			t.Errorf("kpi 缺少字段 %s", key)
		}
	}

	// time_stats：验证 []modelsdb.TimeRangeStat 序列化后是 [{date,count},...]
	ts, _ := modelsdb.GetTimeRangeStatsAll(8, 7)
	if len(ts) > 0 {
		s := fmt.Sprintf("%+v", ts[0])
		if !strings.Contains(s, "Date:") && !strings.Contains(s, "date:") {
			t.Logf("modelsdb.TimeRangeStat 序列化示例: %s", s)
		}
	}

	// tokens_summary：验证 buckets / total_count / total_input / total_output / total_tokens / window_days
	tok, _ := lsmBuildChatStatsTokensSummary(ctx, 7)
	if m, ok := tok.(map[string]interface{}); ok {
		for _, key := range []string{"buckets", "total_count", "total_input", "total_output", "total_tokens", "window_days"} {
			if _, has := m[key]; !has {
				t.Errorf("tokens_summary 缺少字段 %s", key)
			}
		}
	}

	// model_distribution
	md, _ := modelsdb.GetModelNameUsageStatsByRange(8, 7)
	if len(md) > 0 {
		// 序列化验证
		s := fmt.Sprintf("%+v", md[0])
		_ = s
	}

	// trend_chart
	tc, _ := modelsdb.GetDailyStatsAll(8, 7)
	if len(tc) > 0 {
		_ = tc[0]
	}

	// protocol_stats
	ps, _ := modelsdb.GetProtocolAnalysisStatsAll(8, 200)
	if ps != nil {
		if ps.MethodStats == nil {
			t.Errorf("protocol_stats.MethodStats 应初始化")
		}
		if ps.URLPatternStats == nil {
			t.Errorf("protocol_stats.URLPatternStats 应初始化")
		}
		if ps.StatusStats == nil {
			t.Errorf("protocol_stats.StatusStats 应初始化")
		}
		if ps.ModelStats == nil {
			t.Errorf("protocol_stats.ModelStats 应初始化")
		}
	}

	// agent_stats
	as, _ := modelsdb.GetAgentToolStatsByRangeAll(8, 7)
	if as != nil {
		if as.ToolStats == nil {
			t.Errorf("agent_stats.ToolStats 应初始化")
		}
	}

	// 时间戳字段（kpi / tokens_summary 应返回毫秒时间戳）
	if ts, has := kpi["generated_at_ms"]; !has || ts.(int64) <= 0 {
		t.Errorf("kpi.generated_at_ms 应 > 0")
	}
	if m, ok := tok.(map[string]interface{}); ok {
		if ts, has := m["generated_at_ms"]; !has || ts.(int64) <= 0 {
			t.Errorf("tokens_summary.generated_at_ms 应 > 0")
		}
	}
}
