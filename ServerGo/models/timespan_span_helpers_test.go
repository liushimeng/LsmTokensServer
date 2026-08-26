package models

// 20260826 时间跨度动态档位：统一 span 编码 helper 单元测试
//   - ClampStatsSpan：范围裁剪（0 透传 / >0 ≤365 / <0 ≥-720）
//   - SpanCutoffTime：cutoff 换算（0 不需要过滤）
//   - SpanHours：小时数换算（供桶粒度与展示窗口）

import (
	"testing"
	"time"
)

func TestClampStatsSpan(t *testing.T) {
	cases := []struct {
		in, want int
		desc     string
	}{
		{0, 0, "0 无限制透传"},
		{1, 1, "1 天"},
		{365, 365, "365 天上限"},
		{366, 365, "366 天裁到 365"},
		{1000, 365, "1000 天裁到 365"},
		{-1, -1, "1 小时"},
		{-720, -720, "720 小时下限"},
		{-721, -720, "721 小时裁到 720"},
		{-1000, -720, "1000 小时裁到 720"},
	}
	for _, c := range cases {
		if got := ClampStatsSpan(c.in); got != c.want {
			t.Errorf("%s: ClampStatsSpan(%d)=%d, want %d", c.desc, c.in, got, c.want)
		}
	}
}

func TestSpanCutoffTime(t *testing.T) {
	// 0 = 无限制：不需要过滤
	if _, ok := SpanCutoffTime(0); ok {
		t.Errorf("SpanCutoffTime(0) 应返回不需要过滤")
	}
	// 天：cutoff ≈ now - N*24h
	before := time.Now()
	cutoff, ok := SpanCutoffTime(3)
	after := time.Now()
	if !ok {
		t.Fatalf("SpanCutoffTime(3) 应返回需要过滤")
	}
	expectMin := before.AddDate(0, 0, -3)
	expectMax := after.AddDate(0, 0, -3)
	if cutoff.Before(expectMin) || cutoff.After(expectMax) {
		t.Errorf("SpanCutoffTime(3) cutoff=%v 不在 [%v, %v] 内", cutoff, expectMin, expectMax)
	}
	// 小时：cutoff ≈ now - 6h
	before = time.Now()
	cutoff, ok = SpanCutoffTime(-6)
	after = time.Now()
	if !ok {
		t.Fatalf("SpanCutoffTime(-6) 应返回需要过滤")
	}
	expectMin = before.Add(-6 * time.Hour)
	expectMax = after.Add(-6 * time.Hour)
	if cutoff.Before(expectMin) || cutoff.After(expectMax) {
		t.Errorf("SpanCutoffTime(-6) cutoff=%v 不在 [%v, %v] 内", cutoff, expectMin, expectMax)
	}
	// 超限裁剪：-1000 等价 -720
	c1, _ := SpanCutoffTime(-1000)
	c2, _ := SpanCutoffTime(-720)
	if c1.Sub(c2).Abs() > 2*time.Second {
		t.Errorf("SpanCutoffTime(-1000)=%v 应等价 SpanCutoffTime(-720)=%v", c1, c2)
	}
}

func TestSpanHours(t *testing.T) {
	cases := []struct {
		in, want int
		desc     string
	}{
		{0, 0, "0 无限制"},
		{1, 24, "1 天 = 24h"},
		{30, 720, "30 天 = 720h"},
		{400, 8760, "超上限裁到 365 天 = 8760h"},
		{-1, 1, "1 小时"},
		{-12, 12, "12 小时"},
		{-720, 720, "720 小时下限"},
		{-1000, 720, "超下限裁到 720h"},
	}
	for _, c := range cases {
		if got := SpanHours(c.in); got != c.want {
			t.Errorf("%s: SpanHours(%d)=%d, want %d", c.desc, c.in, got, c.want)
		}
	}
}
