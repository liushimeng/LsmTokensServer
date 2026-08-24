package spider

// ==================== v2.0.9 Human-like Behavior 单元测试 ====================
//
// 覆盖 GaussianDelayMs / BezierPathPoints / ReadingStyleScrollVar 等纯函数

import (
	"math"
	"math/rand"
	"testing"
)

// TestGaussianDelayMs_ZeroMean 验证 mean<=0 时返回 0
func TestGaussianDelayMs_ZeroMean(t *testing.T) {
	if got := GaussianDelayMs(0, 100); got != 0 {
		t.Errorf("GaussianDelayMs(0, 100) = %d, want 0", got)
	}
	if got := GaussianDelayMs(-1, 100); got != 0 {
		t.Errorf("GaussianDelayMs(-1, 100) = %d, want 0", got)
	}
}

// TestGaussianDelayMs_Distribution 验证分布范围（Box-Muller + 截断到 mean+3σ）
func TestGaussianDelayMs_Distribution(t *testing.T) {
	mean := 800
	sigma := 200
	maxAllowed := mean + 3*sigma
	for i := 0; i < 1000; i++ {
		d := GaussianDelayMs(mean, sigma)
		if d < 0 {
			t.Errorf("GaussianDelayMs returned negative: %d", d)
		}
		if d > maxAllowed {
			t.Errorf("GaussianDelayMs exceeded mean+3σ cap: %d > %d", d, maxAllowed)
		}
	}
}

// TestBezierPathPoints_StartEnd 验证贝塞尔路径起点/终点正确
func TestBezierPathPoints_StartEnd(t *testing.T) {
	points := BezierPathPoints(0, 0, 100, 100, 12, false)
	if len(points) != 13 {
		t.Errorf("len(points) = %d, want 13 (steps+1)", len(points))
	}
	if points[0].X != 0 || points[0].Y != 0 {
		t.Errorf("start point: got (%v,%v), want (0,0)", points[0].X, points[0].Y)
	}
	if math.Abs(points[12].X-100) > 0.01 || math.Abs(points[12].Y-100) > 0.01 {
		t.Errorf("end point: got (%v,%v), want (~100,~100)", points[12].X, points[12].Y)
	}
}

// TestBezierPathPoints_StepCount 验证步数参数生效
func TestBezierPathPoints_StepCount(t *testing.T) {
	for _, steps := range []int{1, 5, 20, 50} {
		points := BezierPathPoints(0, 0, 100, 50, steps, false)
		if len(points) != steps+1 {
			t.Errorf("steps=%d: got %d points, want %d", steps, len(points), steps+1)
		}
	}
}

// TestBezierPathPoints_MonotonicX 验证 X 坐标单调递增（水平向右移动时）
func TestBezierPathPoints_MonotonicX(t *testing.T) {
	points := BezierPathPoints(0, 0, 1000, 0, 50, false)
	for i := 1; i < len(points); i++ {
		if points[i].X < points[i-1].X {
			t.Errorf("X should be non-decreasing along path: points[%d].X=%v < points[%d].X=%v",
				i, points[i].X, i-1, points[i-1].X)
		}
	}
}

// TestReadingStyleScrollVar_WithinRange 验证滚动距离在 [0.6*vh, 1.2*vh] 范围内
func TestReadingStyleScrollVar_WithinRange(t *testing.T) {
	vh := 800
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		d := ReadingStyleScrollVar(vh, rng)
		// 含 ±15% 抖动，理论最大值约 1.2*1.075 = 1.29
		if d < int(0.6*float64(vh)*0.85) || d > int(1.2*float64(vh)*1.15)+1 {
			t.Errorf("scroll distance out of expected range: got %d for vh=%d", d, vh)
		}
	}
}

// TestReadingStyleScrollVar_ZeroViewport 验证 vh<=0 返回 0
func TestReadingStyleScrollVar_ZeroViewport(t *testing.T) {
	if got := ReadingStyleScrollVar(0, nil); got != 0 {
		t.Errorf("ReadingStyleScrollVar(0, nil) = %d, want 0", got)
	}
	if got := ReadingStyleScrollVar(-1, nil); got != 0 {
		t.Errorf("ReadingStyleScrollVar(-1, nil) = %d, want 0", got)
	}
}
