package models

// ==================== v2.0.18 patch3：路由编辑顺序错位修复 测试 ====================
//
// 覆盖 4 个核心场景：
//   1. remapStatusListByIDs：禁用源站位置跟随真实 ID 重排
//   2. remapAlgorithmTypeListByIDs：算法列表同步重排
//   3. 顺序保持不变时：返回与旧值完全一致（不应重排）
//   4. 新增/移除源站：新加入 ID 默认启用；移除的不出现在结果里
//
// 回归 bug：路由编辑顺序 [A,B,C] (status 1,1,0) 调整为 [C,A,B]，
//   旧 status list "1,1,0" 直接沿用 → C 变启用（错位）。

import (
	"testing"
)

// TestRemapStatusListByIDs_ReorderWithDisabled
// 关键场景：禁用源站被移到不同位置，status 必须跟随 ID
func TestRemapStatusListByIDs_ReorderWithDisabled(t *testing.T) {
	// 原顺序 [A=10, B=20, C=30]，状态 [1=启用, 1=启用, 0=禁用] → "1,1,0"
	// 新顺序 [C=30, A=10, B=20] → 期望状态 [0, 1, 1]
	got := remapStatusListByIDs("10,20,30", "1,1,0", "30,10,20")
	if got != "0,1,1" {
		t.Fatalf("remapStatusListByIDs reorder failed: got %q, want %q", got, "0,1,1")
	}
}

// TestRemapStatusListByIDs_SameOrderReturnsSame
// 顺序未变：返回与旧值一致（不应改变）
func TestRemapStatusListByIDs_SameOrderReturnsSame(t *testing.T) {
	got := remapStatusListByIDs("10,20,30", "1,1,0", "10,20,30")
	if got != "1,1,0" {
		t.Fatalf("remapStatusListByIDs same-order failed: got %q, want %q", got, "1,1,0")
	}
}

// TestRemapStatusListByIDs_AddNewEndpointDefaultsTo1
// 新加入的 ID 默认启用（status=1）
func TestRemapStatusListByIDs_AddNewEndpointDefaultsTo1(t *testing.T) {
	// 原 [A,B]，新增 C，新顺序 [A,B,C]
	got := remapStatusListByIDs("10,20", "1,1", "10,20,30")
	if got != "1,1,1" {
		t.Fatalf("remapStatusListByIDs add-new failed: got %q, want %q", got, "1,1,1")
	}
}

// TestRemapStatusListByIDs_RemoveEndpoint
// 移除源站后：结果长度相应缩短，被移除的不应出现
func TestRemapStatusListByIDs_RemoveEndpoint(t *testing.T) {
	// 原 [A,B,C]，移除 B，新顺序 [A,C]
	got := remapStatusListByIDs("10,20,30", "1,0,1", "10,30")
	if got != "1,1" {
		t.Fatalf("remapStatusListByIDs remove failed: got %q, want %q", got, "1,1")
	}
}

// TestRemapStatusListByIDs_EmptyOldStatus
// 旧状态为空：按 1=启用 兜底
func TestRemapStatusListByIDs_EmptyOldStatus(t *testing.T) {
	got := remapStatusListByIDs("10,20", "", "20,10")
	if got != "1,1" {
		t.Fatalf("remapStatusListByIDs empty-old-status failed: got %q, want %q", got, "1,1")
	}
}

// TestRemapStatusListByIDs_AllDisabledRoundTrip
// 全禁用：调整顺序后仍全禁用
func TestRemapStatusListByIDs_AllDisabledRoundTrip(t *testing.T) {
	got := remapStatusListByIDs("10,20,30", "0,0,0", "30,20,10")
	if got != "0,0,0" {
		t.Fatalf("remapStatusListByIDs all-disabled failed: got %q, want %q", got, "0,0,0")
	}
}

// TestRemapStatusListByIDs_DuplicateIDFirstStatusWins
// 重复 ID：保留首次出现的位置状态
func TestRemapStatusListByIDs_DuplicateIDFirstStatusWins(t *testing.T) {
	// 原 [A,B,A]，状态 [1, 0, 1] → map: A=1, B=0
	// 新顺序 [A,B]：期望 [1, 0]
	got := remapStatusListByIDs("10,20,10", "1,0,1", "10,20")
	if got != "1,0" {
		t.Fatalf("remapStatusListByIDs duplicate-id failed: got %q, want %q", got, "1,0")
	}
}

// TestRemapAlgorithmTypeListByIDs_ReorderProtocolConverter
// 算法列表同样需要跟随 ID 重排（协议转换器场景）
func TestRemapAlgorithmTypeListByIDs_ReorderProtocolConverter(t *testing.T) {
	// 原 [A=直连, B=转换器]，调整顺序为 [B,A]
	got := remapAlgorithmTypeListByIDs("10,20", "1,2", "20,10")
	if got != "2,1" {
		t.Fatalf("remapAlgorithmTypeListByIDs reorder failed: got %q, want %q", got, "2,1")
	}
}

// TestRemapAlgorithmTypeListByIDs_AddNewDefaultsToDirect
// 新增源站默认协议直连
func TestRemapAlgorithmTypeListByIDs_AddNewDefaultsToDirect(t *testing.T) {
	got := remapAlgorithmTypeListByIDs("10,20", "1,2", "10,20,30")
	if got != "1,2,1" {
		t.Fatalf("remapAlgorithmTypeListByIDs add-new failed: got %q, want %q", got, "1,2,1")
	}
}

// TestRemapAlgorithmTypeListByIDs_EmptyOldListDefaultsToDirect
// 旧 algorithm list 为空：按全 1（协议直连）兜底
func TestRemapAlgorithmTypeListByIDs_EmptyOldListDefaultsToDirect(t *testing.T) {
	got := remapAlgorithmTypeListByIDs("10,20", "", "20,10")
	if got != "1,1" {
		t.Fatalf("remapAlgorithmTypeListByIDs empty-old failed: got %q, want %q", got, "1,1")
	}
}

// TestRemapAlgorithmTypeListByIDs_SameOrderReturnsSame
// 顺序未变：返回与旧值一致
func TestRemapAlgorithmTypeListByIDs_SameOrderReturnsSame(t *testing.T) {
	got := remapAlgorithmTypeListByIDs("10,20,30", "1,2,1", "10,20,30")
	if got != "1,2,1" {
		t.Fatalf("remapAlgorithmTypeListByIDs same-order failed: got %q, want %q", got, "1,2,1")
	}
}

// TestRemapStatusListByIDs_RealJiqizhixinScenario
// 模拟真实 bug 场景：3 个源站 [A=启用, B=启用, C=禁用]，
// 用户在 UI 把 C 拖到第一 → 新顺序 [C, A, B]
//   - 修复前：旧 status "1,1,0" 直接沿用 → C 变启用（错位！）
//   - 修复后：新 status 应为 "0,1,1" → C 仍禁用，A/B 仍启用
func TestRemapStatusListByIDs_RealJiqizhixinScenario(t *testing.T) {
	got := remapStatusListByIDs("100,200,300", "1,1,0", "300,100,200")
	want := "0,1,1"
	if got != want {
		t.Fatalf("bug fix regression: got %q, want %q (C should remain disabled)", got, want)
	}
}

// TestRemapStatusListByIDs_EmptyNewListDefaultsToEmpty
// 空新列表：返回空状态（触发 BuildDefaultDstEndPointIDStatusList(0)）
func TestRemapStatusListByIDs_EmptyNewListDefaultsToEmpty(t *testing.T) {
	got := remapStatusListByIDs("10,20", "1,1", "")
	// BuildDefaultDstEndPointIDStatusList(0) → ""
	if got != "" {
		t.Fatalf("remapStatusListByIDs empty-new failed: got %q, want empty", got)
	}
}
