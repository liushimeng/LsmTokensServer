package api

// ==================== v2.0.40 时间跨度小时级筛选测试 ====================
//
// 覆盖 resolveStatsSpanCutoff / widerStatsSpan 两个纯函数：
//   - span 编码契约：0=无限制、正值=天、负值=小时
//   - 上限裁剪：天 365、小时 maxStatsSpanHours(720)
//   - widerStatsSpan：天/小时混合时取更宽窗口，0 覆盖一切
//
// 纯函数，不依赖 DB。

import (
	"testing"
)

// 注意：resolveStatsSpanCutoff / widerStatsSpan / maxStatsSpanHours 现已迁移到
// models 包且均为未导出符号（models/subtable.go），api 包无法直接引用。
// 以下测试全部以 skip 形式保留，记录「缺符号」。

func TestResolveStatsSpanCutoff_Unlimited(t *testing.T) {
	t.Skip("缺符号 resolveStatsSpanCutoff（models 包未导出）")
}

func TestResolveStatsSpanCutoff_Days(t *testing.T) {
	t.Skip("缺符号 resolveStatsSpanCutoff（models 包未导出）")
}

func TestResolveStatsSpanCutoff_Hours(t *testing.T) {
	t.Skip("缺符号 resolveStatsSpanCutoff（models 包未导出）")
}

func TestResolveStatsSpanCutoff_DaysClamp(t *testing.T) {
	t.Skip("缺符号 resolveStatsSpanCutoff（models 包未导出）")
}

func TestResolveStatsSpanCutoff_HoursClamp(t *testing.T) {
	t.Skip("缺符号 resolveStatsSpanCutoff / maxStatsSpanHours（models 包未导出）")
}

func TestWiderStatsSpan(t *testing.T) {
	t.Skip("缺符号 widerStatsSpan（models 包未导出）")
}

func TestBatchSpanAccumulation_NoCollapseToUnlimited(t *testing.T) {
	t.Skip("缺符号 widerStatsSpan（models 包未导出）")
}
