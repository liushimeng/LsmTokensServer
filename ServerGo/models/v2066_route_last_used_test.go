package models

// v2.0.66 遗留契约的收缩版回归测试。
//
// v2.0.71 重构后，「最后使用」列及其排序、以及 last_used_failed 前端契约
// 已随列删除而下线；本文件仅保留仍然成立的守护：
//  1. BatchGetRouteStatsByRouteIDs 只做记录数聚合，SQL 不得再 SELECT
//     MAX(created_at)（Days=0 时无法命中 idx_user_model_created，会超时）。
//  2. 统计类查询必须走 statsDB() 25s context，禁止裸 database.DB.Raw。
//  3. BatchGetRouteLastUsedTimes 的通用守护（空入参 / 并发度区间）。
//
// 「最后成功记录」「最后失败记录」两列的完整契约见
// v2071_route_last_success_failure_test.go。

import (
	"os"
	"strings"
	"testing"
)

// TestBatchGetRouteLastUsedTimes_EmptyItems 空入参安静返回空 map
func TestBatchGetRouteLastUsedTimes_EmptyItems(t *testing.T) {
	got := BatchGetRouteLastUsedTimes(nil, 8)
	if len(got) != 0 {
		t.Errorf("空入参应返回空 map, got %d 条", len(got))
	}
}

// TestBatchGetRouteLastUsedTimes_SkipsZeroRouteID route_id=0 是无效输入，直接跳过
func TestBatchGetRouteLastUsedTimes_SkipsZeroRouteID(t *testing.T) {
	items := []RouteBatchStatItem{
		{RouteID: 0, Key: RouteBatchStatKey{UserName: "u1", ModelName: "m1"}},
	}
	got := BatchGetRouteLastUsedTimes(items, 8)
	if len(got) != 0 {
		t.Errorf("route_id=0 应被跳过, got %d 条", len(got))
	}
}

// TestBatchLastUsedConcurrency_Contract 并发度必须留在合理区间：
// 太小则批量退化成串行，太大则与代理热路径抢 MySQL 连接（池上限 100）。
func TestBatchLastUsedConcurrency_Contract(t *testing.T) {
	if batchLastUsedConcurrency < 2 || batchLastUsedConcurrency > 32 {
		t.Errorf("batchLastUsedConcurrency=%d 应落在 [2,32]", batchLastUsedConcurrency)
	}
}

// TestBatchGetRouteStats_NoMaxCreatedAt 守护 v2.0.66 职责拆分：
// BatchGetRouteStatsByRouteIDs 只做记录数聚合，MAX(created_at) 必须移除。
// 该聚合在 Days=0 时无法命中 idx_user_model_created，是 v2.0.66 故障的根因之一。
func TestBatchGetRouteStats_NoMaxCreatedAt(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2066(t, string(src), "func BatchGetRouteStatsByRouteIDs(")
	// 只检查真实 SQL 字面量（以 "SELECT 开头的字符串），避免注释里提及
	// MAX(created_at) 时误报。
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, `"SELECT `) {
			continue
		}
		if strings.Contains(strings.ToUpper(line), "MAX(CREATED_AT)") {
			t.Errorf("BatchGetRouteStatsByRouteIDs 的 SQL 不得再 SELECT MAX(created_at) —— "+
				"最后记录已拆分到 BatchGetRouteLastUsedTimes 走索引快路径。命中行: %s",
				strings.TrimSpace(line))
		}
	}
}

// TestBatchGetRouteStats_UsesStatsDB 守护该函数走 statsDB() 25s context。
// 旧实现用裸 database.DB.Raw，超时只能等驱动 readTimeout(30s) 砍断 socket，
// 连接被标记 invalid 后污染连接池（日志实证 19 次）。
func TestBatchGetRouteStats_UsesStatsDB(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2066(t, string(src), "func BatchGetRouteStatsByRouteIDs(")
	if !strings.Contains(body, "StatsDB()") {
		t.Error("BatchGetRouteStatsByRouteIDs 必须走 statsDB() 绑定 25s context")
	}
	if strings.Contains(body, "database.DB.Raw(") {
		t.Error("BatchGetRouteStatsByRouteIDs 禁止使用裸 database.DB.Raw（无 context 保护）")
	}
}

// TestGetRouteLastUsedTime_UsesStatsDB 单条查询同样必须有 context 上界
func TestGetRouteLastUsedTime_UsesStatsDB(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2066(t, string(src), "func GetRouteLastUsedTime(")
	if !strings.Contains(body, "StatsDB()") {
		t.Error("GetRouteLastUsedTime 必须走 statsDB() 绑定 25s context")
	}
	if strings.Contains(body, "database.DB.Raw(") {
		t.Error("GetRouteLastUsedTime 禁止使用裸 database.DB.Raw（无 context 保护）")
	}
}

// extractFuncBodyV2066 截取从函数签名到下一个顶层 `\nfunc ` 之间的源码片段
func extractFuncBodyV2066(t *testing.T, src, signature string) string {
	t.Helper()
	idx := strings.Index(src, signature)
	if idx < 0 {
		t.Fatalf("未找到函数签名: %s", signature)
	}
	rest := src[idx+len(signature):]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		return rest[:end]
	}
	return rest
}
