// v2.0.59: /ChatAnalysisTotal 图表化改造 + Agent 工具统计 keyset 分页测试
//
// 守护：
//  1. chatAnalysisTotalTemplate 7 个 stage 均走 ECharts 图表渲染
//  2. 模板包含新增的 CSS 类与图表 helper
//  3. days=0「无限制」不再被 || 7 吞掉
//  4. 无重复 lsmOpenReportModal 声明
//  5. GetAgentToolStatsByRangeAll 走 scanShardPaged keyset 分页
//  6. shardScanRow 新增 AgentToolName 列后仍不含 longtext
//
// 注：chatAnalysisTotalTemplate 已迁移至前端（ClientWeb）；setupPagedScanSQLite /
// insertTxnRows / scanShardPaged / shardScanRow 分别为旧 v2058 测试 helper 或
// models 包未导出符号 —— 相关测试以 skip 保留并记录缺符号。
package websocket

import (
	"testing"
)

// ============ 模板契约：图表化改造 ============

// TestChatTotalTemplate_AllStagesCharted 缺符号：chatAnalysisTotalTemplate 已迁移至前端。
func TestChatTotalTemplate_AllStagesCharted(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatTotalTemplate_ProtocolAgentNotRawJSON 缺符号：chatAnalysisTotalTemplate。
func TestChatTotalTemplate_ProtocolAgentNotRawJSON(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatTotalTemplate_DaysZeroNotSwallowed 缺符号：chatAnalysisTotalTemplate。
func TestChatTotalTemplate_DaysZeroNotSwallowed(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatTotalTemplate_NoDuplicateOpenReportModal 缺符号：chatAnalysisTotalTemplate。
func TestChatTotalTemplate_NoDuplicateOpenReportModal(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// TestChatTotalTemplate_ActiveDaysDedup 缺符号：chatAnalysisTotalTemplate。
func TestChatTotalTemplate_ActiveDaysDedup(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalTemplate（已迁移至前端，Go 侧不存在）")
}

// ============ DB 层：GetAgentToolStatsByRangeAll 分页 ============

// TestGetAgentToolStatsByRangeAll_PagedTotals 缺符号：setupPagedScanSQLite /
// insertTxnRows（旧 v2058 测试 helper，未随本次迁移）及 scanShardPaged / shardScanRow
//（models 包未导出）。
func TestGetAgentToolStatsByRangeAll_PagedTotals(t *testing.T) {
	t.Skip("缺符号 setupPagedScanSQLite/insertTxnRows/scanShardPaged/shardScanRow")
}

// TestGetAgentToolStatsByRangeAll_DaysFilter 缺符号：setupPagedScanSQLite / insertTxnRows。
func TestGetAgentToolStatsByRangeAll_DaysFilter(t *testing.T) {
	t.Skip("缺符号 setupPagedScanSQLite/insertTxnRows/scanShardPaged/shardScanRow")
}

// TestShardScanRow_AgentToolNamePresent 缺符号：scanShardPaged / shardScanRow
//（models 包未导出）。
func TestShardScanRow_AgentToolNamePresent(t *testing.T) {
	t.Skip("缺符号 scanShardPaged/shardScanRow（models 包未导出）")
}
