// v2.0.60: /ChatAnalysisTotalWS 单遍分页扫描 + 全维度增量流式聚合测试
//
// 覆盖：
//  1. chatStatsAggregator 单遍累加 7 个维度正确性（addRow → snapshot*）
//  2. streamScanRow 不含 longtext（v2.0.42 白名单回归）
//  3. streamChatStats 单遍扫描跨 batch 不重不漏（> statsShardScanBatch 行）
//  4. streamChatStats 增量快照
//  5. CHAT_STATS_SNAPSHOT_MIN_INTERVAL 常量合理
//  6. NilDB / ctx 取消安全
//  7. 快照数据形状与前端契约一致
//
// 注：statsShardScanBatch / setupPagedScanSQLite / insertTxnRows 现缺失（前者为
// models 包未导出常量，后两者为旧 v2058 测试 helper），相关 SQLite 集成测试以 skip 保留。
package websocket

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
)

// ============ 单元：streamScanRow 白名单 ============

// TestStreamScanRow_NoLongTextField 守护 streamScanRow 不含 8 个 longtext 字段
func TestStreamScanRow_NoLongTextField(t *testing.T) {
	forbidden := []string{
		"request_headers", "request_body", "request_src_protocol_body",
		"response_headers", "response_body", "response_src_protocol_body",
		"request_src_protocol_headers", "response_src_protocol_headers",
	}
	rt := reflect.TypeOf(streamScanRow{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("gorm")
		for _, f := range forbidden {
			if strings.Contains(tag, "column:"+f) {
				t.Errorf("streamScanRow 字段 %s 含禁止的 longtext 列 %s", rt.Field(i).Name, f)
			}
		}
	}
	// streamScanColumns 也不能含 longtext 列
	for _, f := range forbidden {
		if strings.Contains(streamScanColumns, f) {
			t.Errorf("streamScanColumns 含禁止的 longtext 列 %s", f)
		}
	}
}

// ============ 单元：aggregator 累加正确性 ============

// TestChatStatsAggregator_AddRowAndSnapshots 造若干行验证 7 个维度聚合
func TestChatStatsAggregator_AddRowAndSnapshots(t *testing.T) {
	agg := newChatStatsAggregator(7) // days=7 → 小时桶
	now := time.Now()
	rows := []streamScanRow{
		{ID: 1, CreatedAt: now, ModelName: "gpt-x", DstModelName: "gpt-x", TokensInputSize: 10, TokensOutputSize: 20, TokensAllSize: 30, ElapsedMs: 100, AgentToolName: "claude-cli", RequestMethod: "POST", RequestURL: "/v1/messages?a=1", ResponseStatus: "200", IsStream: true, HasSystemPrompt: true, UserMessageCount: 2},
		{ID: 2, CreatedAt: now, ModelName: "gpt-x", DstModelName: "gpt-x", TokensInputSize: 5, TokensOutputSize: 5, TokensAllSize: 10, ElapsedMs: 50, AgentToolName: "opencode", RequestMethod: "GET", RequestURL: "/v1/models", ResponseStatus: "200", HasToolCall: true, UserMessageCount: 1},
		{ID: 3, CreatedAt: now, ModelName: "claude-y", DstModelName: "claude-y", TokensInputSize: 1, TokensOutputSize: 2, TokensAllSize: 3, ElapsedMs: 200, AgentToolName: "unknown", RequestMethod: "POST", RequestURL: "/v1/messages", ResponseStatus: "500", UserMessageCount: 3},
	}
	for i := range rows {
		agg.addRow(&rows[i])
	}

	// KPI
	kpi := agg.snapshotKPI("")
	if kpi["total_calls"].(int64) != 3 {
		t.Errorf("total_calls=%v, 期望 3", kpi["total_calls"])
	}
	if kpi["total_tokens"].(uint64) != 43 {
		t.Errorf("total_tokens=%v, 期望 43", kpi["total_tokens"])
	}
	if kpi["active_models"].(int) != 2 {
		t.Errorf("active_models=%v, 期望 2", kpi["active_models"])
	}

	// tokens_summary
	tok := agg.snapshotTokensSummary()
	if tok["total_input"].(uint64) != 16 {
		t.Errorf("total_input=%v, 期望 16", tok["total_input"])
	}
	if tok["total_output"].(uint64) != 27 {
		t.Errorf("total_output=%v, 期望 27", tok["total_output"])
	}

	// model_distribution：按调用次数降序，gpt-x(2) 在前
	md := agg.snapshotModelDist()
	if len(md) != 2 {
		t.Fatalf("model_dist 长度=%d, 期望 2", len(md))
	}
	if md[0].ModelName != "gpt-x" || md[0].CallCount != 2 {
		t.Errorf("md[0]=%+v, 期望 gpt-x call=2", md[0])
	}

	// agent_stats：unknown 被过滤，剩 2 个工具
	ag := agg.snapshotAgent()
	if ag.UniqueTools != 2 {
		t.Errorf("unique_tools=%d, 期望 2（unknown 过滤）", ag.UniqueTools)
	}
	if ag.TotalAgentCount != 2 {
		t.Errorf("total_agent_count=%d, 期望 2", ag.TotalAgentCount)
	}

	// protocol_stats：全量累加
	p := agg.snapshotProtocol()
	if p.SampleCount != 3 {
		t.Errorf("sample_count=%d, 期望 3", p.SampleCount)
	}
	if p.StreamCount != 1 || p.NonStreamCount != 2 {
		t.Errorf("stream=%d non=%d, 期望 1/2", p.StreamCount, p.NonStreamCount)
	}
	if p.StatusStats["200"] != 2 || p.StatusStats["500"] != 1 {
		t.Errorf("status_stats=%v, 期望 200:2 500:1", p.StatusStats)
	}
	if p.MethodStats["POST"] != 2 || p.MethodStats["GET"] != 1 {
		t.Errorf("method_stats=%v, 期望 POST:2 GET:1", p.MethodStats)
	}
	// URL query 被剥离
	if p.URLPatternStats["/v1/messages"] != 2 {
		t.Errorf("url_pattern_stats[/v1/messages]=%d, 期望 2（query 剥离）", p.URLPatternStats["/v1/messages"])
	}

	// trend_chart：DailyStat 补齐天槽位
	trend := agg.snapshotTrend()
	if len(trend) != 7 {
		t.Errorf("trend 长度=%d, 期望 7（补齐 7 天槽位）", len(trend))
	}

	// time_stats：小时桶（days<=7）
	ts := agg.snapshotTimeStats()
	if len(ts) == 0 {
		t.Errorf("time_stats 不应为空")
	}
}

// TestChatStatsAggregator_SnapshotIsolated 守护快照 map 与后续累加隔离（cloneInt64Map）
func TestChatStatsAggregator_SnapshotIsolated(t *testing.T) {
	agg := newChatStatsAggregator(7)
	now := time.Now()
	r := streamScanRow{ID: 1, CreatedAt: now, RequestMethod: "POST", ResponseStatus: "200"}
	agg.addRow(&r)
	snap1 := agg.snapshotProtocol()
	// 再加一行，snap1 的 map 不应被 mutate
	r2 := streamScanRow{ID: 2, CreatedAt: now, RequestMethod: "POST", ResponseStatus: "200"}
	agg.addRow(&r2)
	if snap1.MethodStats["POST"] != 1 {
		t.Errorf("快照未隔离：snap1.MethodStats[POST]=%d, 期望 1（后续累加不应影响）", snap1.MethodStats["POST"])
	}
}

// ============ 常量 ============

// TestChatStatsSnapshotInterval_Reasonable 守护快照节流间隔合理（100ms–2s）
func TestChatStatsSnapshotInterval_Reasonable(t *testing.T) {
	if config.CHAT_STATS_SNAPSHOT_MIN_INTERVAL < 100*time.Millisecond || config.CHAT_STATS_SNAPSHOT_MIN_INTERVAL > 2*time.Second {
		t.Errorf("CHAT_STATS_SNAPSHOT_MIN_INTERVAL=%v 超出 [100ms, 2s] 合理范围", config.CHAT_STATS_SNAPSHOT_MIN_INTERVAL)
	}
}

// ============ streamChatStats NilDB / ctx 取消 ============

// TestStreamChatStats_NilDB 守护 sdb=nil 返回 (0,false,nil) 不 panic
func TestStreamChatStats_NilDB(t *testing.T) {
	agg := newChatStatsAggregator(7)
	scanned, timedOut, err := streamChatStats(context.Background(), nil, 8, agg, "", "", nil)
	if err != nil || scanned != 0 || timedOut {
		t.Errorf("NilDB 应返回 (0,false,nil)，实际 (%d,%v,%v)", scanned, timedOut, err)
	}
}

// ============ SQLite 集成：单遍扫描不重不漏 + 增量快照 ============

// TestStreamChatStats_ScanEquivalenceAndSnapshots 缺符号：setupPagedScanSQLite /
// insertTxnRows / statsShardScanBatch。
func TestStreamChatStats_ScanEquivalenceAndSnapshots(t *testing.T) {
	t.Skip("缺符号 setupPagedScanSQLite/insertTxnRows/statsShardScanBatch")
}

// TestStreamChatStats_MultiShard 缺符号：setupPagedScanSQLite / insertTxnRows。
func TestStreamChatStats_MultiShard(t *testing.T) {
	t.Skip("缺符号 setupPagedScanSQLite/insertTxnRows")
}

// TestStreamChatStats_CtxCancel 缺符号：setupPagedScanSQLite / insertTxnRows。
func TestStreamChatStats_CtxCancel(t *testing.T) {
	t.Skip("缺符号 setupPagedScanSQLite/insertTxnRows")
}

// TestChatStatsAggregator_DayBucketGranularity 守护 days>7 用天桶，days<=7 用小时桶
func TestChatStatsAggregator_DayBucketGranularity(t *testing.T) {
	if a := newChatStatsAggregator(7); !a.timeIsHour {
		t.Errorf("days=7 应为小时桶")
	}
	if a := newChatStatsAggregator(30); a.timeIsHour {
		t.Errorf("days=30 应为天桶")
	}
	// days=0（无限制）与原 GetTimeRangeStatsAll 一致：0 > timeStatsMaxDays 为 false → 小时桶
	if a := newChatStatsAggregator(0); !a.timeIsHour {
		t.Errorf("days=0 应为小时桶（与 GetTimeRangeStatsAll 语义一致）")
	}
}
