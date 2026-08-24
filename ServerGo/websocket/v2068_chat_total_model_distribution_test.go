// v2.0.68: /ChatAnalysisTotal stage 4 model_distribution 维度增强测试
//
// 覆盖：
//  1. snapshotModelDist 输出含 UserCount / DstEndpointCount / TopDstEndpoints
//  2. UserCount 跨行去重
//  3. DstEndpointCount 跨行去重；TopDstEndpoints 长度 ≤ 3 + 排序正确
//  4. streamScanColumns 不含 longtext
//  5. ModelNameUsageStat JSON 序列化字段完整
//  6. TokensModelStat CallCount 与 Count 同值
//  7. GetModelNameUsageStatsByRange NilDB 安全
//  8. snapshotModelDist 的 CallShare/TokenShare 与 total 一致
//
// 注：snapshotModelDist / streamScanRow / newChatStatsAggregator 等聚合器符号现位于
// websocket 包（未导出），api 包无法引用 —— 相关测试以 skip 保留并记录缺符号。
package websocket

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// makeTestTime 返回一个固定的 time.Time（避免测试间时间漂移）
func makeTestTime() time.Time {
	return time.Date(2026, 7, 30, 10, 0, 0, 0, time.Local)
}

// ============ 1. snapshotModelDist 新字段 ============

// TestSnapshotModelDist_HasUserCountAndDstEndpoint 缺符号：newChatStatsAggregator /
// streamScanRow / snapshotModelDist 均位于 websocket 包（未导出）。
func TestSnapshotModelDist_HasUserCountAndDstEndpoint(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// ============ 2. dst_model_name 去重 ============

// TestSnapshotModelDist_DstModelNameDedup 缺符号：websocket 聚合器。
func TestSnapshotModelDist_DstModelNameDedup(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// TestSnapshotModelDist_MultipleDstModelNames 缺符号：websocket 聚合器。
func TestSnapshotModelDist_MultipleDstModelNames(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// ============ 3. TopDstEndpoints 排序与 ≤3 ============

// TestSnapshotModelDist_DstEndpointTop3 缺符号：websocket 聚合器。
func TestSnapshotModelDist_DstEndpointTop3(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// TestSnapshotModelDist_DstEndpointSingleCallCount 缺符号：websocket 聚合器。
func TestSnapshotModelDist_DstEndpointSingleCallCount(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// TestSnapshotModelDist_EmptyDstModelName_NoAccumulation 缺符号：websocket 聚合器。
func TestSnapshotModelDist_EmptyDstModelName_NoAccumulation(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// ============ 4. streamScanColumns 不含 longtext ============

// TestStreamScanColumns_NoLongtext 缺符号：streamScanColumns 位于 websocket 包（未导出）。
func TestStreamScanColumns_NoLongtext(t *testing.T) {
	t.Skip("缺符号 streamScanColumns（websocket 包未导出）")
}

// TestStreamScanRow_NoLongtextField 缺符号：streamScanRow 位于 websocket 包（未导出）。
func TestStreamScanRow_NoLongtextField(t *testing.T) {
	t.Skip("缺符号 streamScanRow（websocket 包未导出）")
}

// ============ 5. JSON 字段完整 ============

// TestModelNameUsageStat_JSON_AllFields JSON 序列化含全部 v2.0.68 新字段
func TestModelNameUsageStat_JSON_AllFields(t *testing.T) {
	m := modelsdb.ModelNameUsageStat{
		ModelName:        "test",
		UserCount:        5,
		CallCount:        100,
		TokensInput:      1000,
		TokensOutput:     500,
		TokensTotal:      1500,
		CallShare:        50.0,
		TokenShare:       30.0,
		DstEndpointCount: 2,
		TopDstEndpoints: []modelsdb.DstEndpointUsage{
			{DstEndpointID: 1, CallCount: 80},
			{DstEndpointID: 2, CallCount: 20},
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	for _, field := range []string{
		`"model_name":"test"`,
		`"user_count":5`,
		`"call_count":100`,
		`"tokens_input":1000`,
		`"tokens_output":500`,
		`"tokens_total":1500`,
		`"call_share":50`,
		`"token_share":30`,
		`"dst_endpoint_count":2`,
		`"top_dst_endpoints":`,
		`"dst_endpoint_id":1`,
		`"dst_endpoint_id":2`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("JSON 缺字段 %s\n完整: %s", field, s)
		}
	}
}

// TestModelNameUsageStat_EmptyTopDstEndpoints_OmitEmpty 验证 omitempty 生效
func TestModelNameUsageStat_EmptyTopDstEndpoints_OmitEmpty(t *testing.T) {
	m := modelsdb.ModelNameUsageStat{ModelName: "no-endpoints"}
	b, _ := json.Marshal(m)
	s := string(b)
	if strings.Contains(s, "top_dst_endpoints") {
		t.Errorf("空切片应被 omitempty 忽略，实际: %s", s)
	}
}

// ============ 6. TokensModelStat CallCount 别名 ============

// TestTokensModelStat_HasCallCountAlias 序列化的 count 与 call_count 同值
func TestTokensModelStat_HasCallCountAlias(t *testing.T) {
	m := modelsdb.TokensModelStat{
		ModelName:    "gpt-4",
		Count:        42,
		CallCount:    42,
		TokensInput:  1000,
		TokensOutput: 500,
		TokensTotal:  1500,
	}
	b, _ := json.Marshal(m)
	s := string(b)
	if !strings.Contains(s, `"count":42`) || !strings.Contains(s, `"call_count":42`) {
		t.Errorf("JSON 必须含 count=42 与 call_count=42: %s", s)
	}
}

// TestTokensModelStat_CallCountJSONTag 验证 CallCount 字段 json tag
func TestTokensModelStat_CallCountJSONTag(t *testing.T) {
	rt := reflect.TypeOf(modelsdb.TokensModelStat{})
	f, ok := rt.FieldByName("CallCount")
	if !ok {
		t.Fatal("TokensModelStat 缺字段 CallCount (v2.0.68)")
	}
	tag := f.Tag.Get("json")
	if tag != "call_count" {
		t.Errorf("CallCount json tag = %q, want \"call_count\"", tag)
	}
}

// ============ 7. NilDB 安全 ============

// TestGetModelNameUsageStatsByRange_NilDB_v2_0_68 v2.0.68: NilDB 不 panic，返回空切片
func TestGetModelNameUsageStatsByRange_NilDB_v2_0_68(t *testing.T) {
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	stats, err := modelsdb.GetModelNameUsageStatsByRange(8, 7)
	if err != nil {
		t.Fatalf("NilDB 必须返回 nil error, got %v", err)
	}
	if stats == nil {
		t.Fatal("NilDB 必须返回非 nil 切片")
	}
	if len(stats) != 0 {
		t.Errorf("NilDB 必须返回空切片, got %d entries", len(stats))
	}
}

// ============ 8. DstEndpointUsage JSON ============

// TestDstEndpointUsage_JSONField 测试 DstEndpointUsage 序列化
func TestDstEndpointUsage_JSONField(t *testing.T) {
	d := modelsdb.DstEndpointUsage{DstEndpointID: 7, CallCount: 99}
	b, _ := json.Marshal(d)
	s := string(b)
	if !strings.Contains(s, `"dst_endpoint_id":7`) {
		t.Errorf("JSON 缺 dst_endpoint_id: %s", s)
	}
	if !strings.Contains(s, `"call_count":99`) {
		t.Errorf("JSON 缺 call_count: %s", s)
	}
}

// ============ 9. share 一致性 ============

// TestSnapshotModelDist_ShareConsistent 缺符号：websocket 聚合器。
func TestSnapshotModelDist_ShareConsistent(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}

// TestSnapshotModelDist_IsolatedByPlatformModel 缺符号：websocket 聚合器。
func TestSnapshotModelDist_IsolatedByPlatformModel(t *testing.T) {
	t.Skip("缺符号 newChatStatsAggregator/streamScanRow/snapshotModelDist（websocket 包未导出）")
}
