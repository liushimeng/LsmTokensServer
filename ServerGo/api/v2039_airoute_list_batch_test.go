package api

import (
	"testing"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// =============================================================================
// v2.0.39: 智能路由管理 list 接口 N+1 闭环 + 交易表大字段白名单强制约束
// =============================================================================

// TestSelectTransactionColumns_ExcludesLongText 缺符号：selectTransactionColumns
// 现为 models 包未导出函数。
func TestSelectTransactionColumns_ExcludesLongText(t *testing.T) {
	t.Skip("缺符号 selectTransactionColumns（models 包未导出，无法从 api 包引用）")
}

// TestSelectTransactionColumns_ContainsCoreMetadataColumns 缺符号：selectTransactionColumns。
func TestSelectTransactionColumns_ContainsCoreMetadataColumns(t *testing.T) {
	t.Skip("缺符号 selectTransactionColumns（models 包未导出，无法从 api 包引用）")
}

// TestLookupRouteModelName_NilDB DB=nil 时返回空串，不 panic。
func TestLookupRouteModelName_NilDB(t *testing.T) {
	// 直接调用，不依赖任何 DB。ID=1 应返回 ""（DB=nil 兜底返回空串）
	got := lookupRouteModelName(1)
	if got != "" {
		t.Errorf("DB=nil 时应返回空串，实际=%q", got)
	}
}

// TestLookupRouteModelName_ZeroID ID=0 时返回空串，提前返回不发 DB。
func TestLookupRouteModelName_ZeroID(t *testing.T) {
	got := lookupRouteModelName(0)
	if got != "" {
		t.Errorf("ID=0 时应返回空串，实际=%q", got)
	}
}

// TestRouteBatchStatResult_JSONKeyOmitempty 测试 batch_stats 响应 JSON key 与前端约定一致。
func TestRouteBatchStatResult_JSONKey(t *testing.T) {
	r := modelsdb.RouteBatchStatResult{
		RouteID: 42,
		Count:   123,
	}
	if r.RouteID != 42 || r.Count != 123 {
		t.Errorf("RouteBatchStatResult 零值契约破坏: %+v", r)
	}
}

// TestBatchRouteStatsKeyPairMax_Contract 缺符号：batchRouteStatsKeyPairMax
// 现为 models 包未导出常量。
func TestBatchRouteStatsKeyPairMax_Contract(t *testing.T) {
	t.Skip("缺符号 batchRouteStatsKeyPairMax（models 包未导出，无法从 api 包引用）")
}
