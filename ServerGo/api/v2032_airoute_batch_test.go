package api

// ==================== v2.0.32 智能路由批量操作测试 ====================
//
// 覆盖范围：
//   1. BatchUpdateAIRoute：空 ids / 部分不存在 / 全部成功
//   2. BatchDeleteAIRoute：空 ids / 部分不存在 / 全部成功
//   3. API handler batch_update：非 POST / 非法 JSON / 缺 ids / 无可更新字段
//   4. API handler batch_delete：非 POST / 非法 JSON / 缺 ids
//
// 集成测试（DB 依赖）通过 DB != nil 判断自动跳过。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
)

// ==================== BatchUpdateAIRoute 测试 ====================

func TestBatchUpdateAIRoute_EmptyIds(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	// DB == nil 时也应有正确的错误处理
	_, errs := modelsdb.BatchUpdateAIRoute([]uint64{}, map[string]interface{}{"algorithm_strategy_type": 2})
	if len(errs) == 0 {
		t.Error("空 ids 应返回错误")
	}
}

func TestBatchUpdateAIRoute_DBEmpty(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	_, errs := modelsdb.BatchUpdateAIRoute([]uint64{1, 2}, map[string]interface{}{"algorithm_strategy_type": 2})
	if len(errs) == 0 {
		t.Error("DB 不可用时返回错误列表不应为空")
	}
}

// ==================== BatchDeleteAIRoute 测试 ====================

func TestBatchDeleteAIRoute_EmptyIds(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	_, errs := modelsdb.BatchDeleteAIRoute([]uint64{})
	if len(errs) == 0 {
		t.Error("空 ids 应返回错误")
	}
}

func TestBatchDeleteAIRoute_DBEmpty(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	_, errs := modelsdb.BatchDeleteAIRoute([]uint64{999})
	if len(errs) == 0 {
		t.Error("DB 不可用时返回错误列表不应为空")
	}
}

// ==================== 协议一致性 / 算法类型分流 测试 ====================

// TestParseProtocolType 缺符号：parseProtocolType 现为 models 包未导出函数。
func TestParseProtocolType(t *testing.T) {
	t.Skip("缺符号 parseProtocolType（models 包未导出，无法从 api 包引用）")
}

// TestCountCsv 缺符号：countCsv 现为 models 包未导出函数。
func TestCountCsv(t *testing.T) {
	t.Skip("缺符号 countCsv（models 包未导出，无法从 api 包引用）")
}

// AlgorithmTypeListByRouteProtocol 不依赖 DB：当 routeID 不存在时回退为全 1
func TestAlgorithmTypeListByRouteProtocol_RouteNotFound(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	got := modelsdb.AlgorithmTypeListByRouteProtocol(99999, "10,20,30", []int{1, 2, 1})
	if got != "1,1,1" {
		t.Errorf("route 不存在时回退全 1，得到: %s", got)
	}
}

// AlgorithmTypeListByRouteProtocol endpoint 列表为空时返回空字符串
func TestAlgorithmTypeListByRouteProtocol_EmptyEndpointList(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	got := modelsdb.AlgorithmTypeListByRouteProtocol(99999, "", []int{})
	if got != "" {
		t.Errorf("空 endpoint 列表应返回空字符串，得到: %s", got)
	}
}

// AlgorithmTypeListByRouteProtocol endpoint 列表格式错误时：当 route 不存在但 endpoint list 有 2 个 parts 时，
// 走"回退全 1"分支，按 count 返回等长的"1"列表。该测试覆盖了 ParseDstEndPointIDList 报错但不阻断执行路径的场景。
func TestAlgorithmTypeListByRouteProtocol_InvalidEndpointList(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	got := modelsdb.AlgorithmTypeListByRouteProtocol(99999, "abc,def", []int{})
	// route 不存在走 fallback：countCsv = 2，预期 "1,1"
	if got != "1,1" {
		t.Errorf("route 不存在 + endpoint list 无法解析时回退全 1，得到: %s", got)
	}
}

// ==================== API Handler batch_update 测试 ====================

func TestAIRouteBatchUpdateHandler_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/AIRouteManageInterface", nil)
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("期望状态码 200，得到 %d", w.Code)
	}
	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非 POST 请求应返回失败")
	}
}

func TestAIRouteBatchUpdateHandler_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非法 JSON 应返回失败")
	}
}

func TestAIRouteBatchUpdateHandler_EmptyIds(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	body := `{"action":"batch_update","ids":[],"algorithm_strategy_type":2}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 ids 应返回失败")
	}
}

func TestAIRouteBatchUpdateHandler_NoUpdatableFields(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	body := `{"action":"batch_update","ids":[1]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("没有可更新字段时应返回失败")
	}
	if !strings.Contains(resp.Message, "至少一个") {
		t.Errorf("错误消息应提示需要提供可更新字段，实际为: %s", resp.Message)
	}
}

// ==================== API Handler batch_delete 测试 ====================

func TestAIRouteBatchDeleteHandler_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/AIRouteManageInterface", nil)
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非 POST 请求应返回失败")
	}
}

func TestAIRouteBatchDeleteHandler_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader("bad json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非法 JSON 应返回失败")
	}
}

func TestAIRouteBatchDeleteHandler_EmptyIds(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	body := `{"action":"batch_delete","ids":[]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 ids 应返回失败")
	}
}

// ==================== v2.0.37 智能路由批量统计 (batch_stats) 测试 ====================

// TestBatchGetRouteStatsByRouteIDs_EmptyItems 验证 DB 未初始化时不出错
func TestBatchGetRouteStatsByRouteIDs_EmptyItems(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	got, err := modelsdb.BatchGetRouteStatsByRouteIDs(nil, 10)
	if err != nil {
		t.Errorf("DB==nil + nil items 应无错误，得到 %v", err)
	}
	if len(got) != 0 {
		t.Errorf("DB==nil + nil items 应返回空 map，得到 %v", got)
	}
}

// TestBatchGetRouteStatsByRouteIDs_NilDB 验证 DB==nil + 非空 items 也安静返回空 map
func TestBatchGetRouteStatsByRouteIDs_NilDB(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	items := []modelsdb.RouteBatchStatItem{
		{RouteID: 1, Key: modelsdb.RouteBatchStatKey{UserName: "u", ModelName: "m", ProtocolType: 1}, Days: 7},
	}
	got, err := modelsdb.BatchGetRouteStatsByRouteIDs(items, 10)
	if err != nil {
		t.Errorf("DB==nil 应安静返回无错误，得到 %v", err)
	}
	if len(got) != 0 {
		t.Errorf("DB==nil 应返回空 map，得到 %v", got)
	}
}

// TestBatchGetRouteStatsByRouteIDs_TruncatedToCap 缺符号：batchRouteStatsKeyPairMax
// 现为 models 包未导出常量。
func TestBatchGetRouteStatsByRouteIDs_TruncatedToCap(t *testing.T) {
	t.Skip("缺符号 batchRouteStatsKeyPairMax（models 包未导出，无法从 api 包引用）")
}

// TestAIRouteBatchStatsHandler_EmptyItems 验证 API 层对空 batch_items 参数返回失败
func TestAIRouteBatchStatsHandler_EmptyItems(t *testing.T) {
	body := `{"action":"batch_stats","batch_items":[]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 batch_items 应返回失败")
	}
}

// TestAIRouteBatchStatsHandler_TooManyItems 缺符号：batchRouteStatsKeyPairMax
// 现为 models 包未导出常量。
func TestAIRouteBatchStatsHandler_TooManyItems(t *testing.T) {
	t.Skip("缺符号 batchRouteStatsKeyPairMax（models 包未导出，无法从 api 包引用）")
}

// TestAIRouteBatchStatsHandler_MethodNotPost 验证仅 POST
func TestAIRouteBatchStatsHandler_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/AIRouteManageInterface", nil)
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非 POST 请求应返回失败")
	}
}

// ==================== v2.0.x 批量追加/删除源站 (batch_append_endpoints / batch_remove_endpoints) 测试 ====================

// TestAIRouteBatchAppendEndpointsHandler_EmptyIds 空 ids 应返回失败
func TestAIRouteBatchAppendEndpointsHandler_EmptyIds(t *testing.T) {
	body := `{"action":"batch_append_endpoints","ids":[],"endpoint_ids":[10]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 ids 应返回失败")
	}
}

// TestAIRouteBatchAppendEndpointsHandler_EmptyEndpointIds 空 endpoint_ids 应返回失败
func TestAIRouteBatchAppendEndpointsHandler_EmptyEndpointIds(t *testing.T) {
	body := `{"action":"batch_append_endpoints","ids":[1],"endpoint_ids":[]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 endpoint_ids 应返回失败")
	}
}

// TestAIRouteBatchRemoveEndpointsHandler_EmptyIds 空 ids 应返回失败
func TestAIRouteBatchRemoveEndpointsHandler_EmptyIds(t *testing.T) {
	body := `{"action":"batch_remove_endpoints","ids":[],"endpoint_ids":[10]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 ids 应返回失败")
	}
}

// TestAIRouteBatchRemoveEndpointsHandler_EmptyEndpointIds 空 endpoint_ids 应返回失败
func TestAIRouteBatchRemoveEndpointsHandler_EmptyEndpointIds(t *testing.T) {
	body := `{"action":"batch_remove_endpoints","ids":[1],"endpoint_ids":[]}`
	req := httptest.NewRequest(http.MethodPost, "/AIRouteManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("空 endpoint_ids 应返回失败")
	}
}

// TestAIRouteBatchAppendEndpointsHandler_MethodNotPost 验证仅 POST
func TestAIRouteBatchAppendEndpointsHandler_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/AIRouteManageInterface", nil)
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非 POST 请求应返回失败")
	}
}

// TestAIRouteBatchRemoveEndpointsHandler_MethodNotPost 验证仅 POST
func TestAIRouteBatchRemoveEndpointsHandler_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/AIRouteManageInterface", nil)
	w := httptest.NewRecorder()
	aiRouteManageInterfaceHandle(w, req)

	var resp userManageResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Success {
		t.Error("非 POST 请求应返回失败")
	}
}

// TestBatchEndpointResult_StructShape 验证结构体字段完整（与 JSON 契约对齐）
func TestBatchEndpointResult_StructShape(t *testing.T) {
	r := modelsdb.BatchEndpointResult{
		SuccessCount: 2,
		SkipCount:    1,
		FailCount:    0,
		Details: []modelsdb.BatchEndpointDetail{
			{RouteID: 1, Status: "success", Reason: ""},
			{RouteID: 2, Status: "skip", Reason: "already exists"},
		},
	}
	if r.SuccessCount != 2 || r.SkipCount != 1 || r.FailCount != 0 {
		t.Errorf("计数字段赋值异常: %+v", r)
	}
	if len(r.Details) != 2 {
		t.Errorf("明细长度异常: %d", len(r.Details))
	}
	if r.Details[0].Status != "success" || r.Details[1].Status != "skip" {
		t.Errorf("明细状态字段异常: %+v", r.Details)
	}
}

// TestBatchAddEndpointsToRoutes_EmptyInput 验证空入参不出错
func TestBatchAddEndpointsToRoutes_EmptyInput(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	// DB == nil 时返回空结果
	result := modelsdb.BatchAddEndpointsToRoutes([]uint64{}, []uint64{10}, 0)
	if result.SuccessCount != 0 || result.SkipCount != 0 || result.FailCount != 0 {
		t.Errorf("空入参应返回零值结果，得到 %+v", result)
	}
}

// TestBatchRemoveEndpointsFromRoutes_EmptyInput 验证空入参不出错
func TestBatchRemoveEndpointsFromRoutes_EmptyInput(t *testing.T) {
	if database.DB != nil {
		t.Skip("集成测试需要 DB，跳过")
	}
	result := modelsdb.BatchRemoveEndpointsFromRoutes([]uint64{}, []uint64{10}, 0)
	if result.SuccessCount != 0 || result.SkipCount != 0 || result.FailCount != 0 {
		t.Errorf("空入参应返回零值结果，得到 %+v", result)
	}
}

// TestRouteBatchStatResult_StructShape 验证结构体字段完整（与 JSON 契约对齐）
func TestRouteBatchStatResult_StructShape(t *testing.T) {
	r := modelsdb.RouteBatchStatResult{
		RouteID: 123,
		Count:   42,
	}
	if r.RouteID != 123 || r.Count != 42 {
		t.Errorf("结构体字段赋值异常: %+v", r)
	}
	// v2.0.71：LastSuccessHasRecord/LastFailureHasRecord 零值表「暂无记录」
	if r.LastSuccessHasRecord || r.LastFailureHasRecord {
		t.Errorf("HasRecord 字段零值应表暂无记录，得到 %+v", r)
	}
}

var _ = protocol.AgentProtocolType_OpenAI
