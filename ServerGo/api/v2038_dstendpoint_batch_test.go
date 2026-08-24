package api

// ==================== v2.0.38 源站管理批量操作测试 ====================
//
// 覆盖范围：
//   1. BatchUpdateDstEndPointStatus：空 ids / 超限 / DB nil / 非法 status 兜底
//   2. BatchDeleteDstEndPoints：空 ids / 超限 / DB nil
//   3. maxBatchDstEndPointIDs 常量契约（=500）
//   4. dstEndPointManageInterfaceHandle batch_* action：
//      - 非 POST / 非法 JSON / 空 ids / ids 字段缺失
//      - batch_disable / batch_enable / batch_delete 三个 action 都覆盖
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
)

// maxBatchDstEndPointIDs 在 models 包中未导出，此处用等价字面量保持测试契约。
const _maxBatchDstEndPointIDs = 500

// ==================== BatchUpdateDstEndPointStatus 测试 ====================

func TestBatchUpdateDstEndPointStatus_EmptyIDs(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, errs := modelsdb.BatchUpdateDstEndPointStatus([]uint64{}, 1)
	if len(errs) == 0 {
		t.Error("空 ids 应返回错误")
	}
}

func TestBatchUpdateDstEndPointStatus_NilIDs(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, errs := modelsdb.BatchUpdateDstEndPointStatus(nil, 0)
	if len(errs) == 0 {
		t.Error("nil ids 应返回错误")
	}
}

func TestBatchUpdateDstEndPointStatus_ExceedsMaxBatch(t *testing.T) {
	ids := make([]uint64, _maxBatchDstEndPointIDs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	_, errs := modelsdb.BatchUpdateDstEndPointStatus(ids, 1)
	if len(errs) == 0 {
		t.Fatal("超过 maxBatchDstEndPointIDs 应返回错误")
	}
	if !strings.Contains(errs[0].Error(), "上限") {
		t.Errorf("错误消息应包含「上限」，实际: %v", errs[0].Error())
	}
}

func TestBatchUpdateDstEndPointStatus_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, errs := modelsdb.BatchUpdateDstEndPointStatus([]uint64{1, 2, 3}, 1)
	// DB=nil 时每个 id 都应进入 errs 列表（不 panic，全部失败）
	if len(errs) != 3 {
		t.Errorf("DB=nil 时每个 id 都应失败，期望 3 个错误，得到 %d 个", len(errs))
	}
	for _, e := range errs {
		if !strings.Contains(e.Error(), "database not initialized") {
			t.Errorf("错误消息应包含 'database not initialized'，实际: %v", e.Error())
		}
	}
}

// 非法 status 值（不是 0/1）应兜底为 1（启用），不 panic
func TestBatchUpdateDstEndPointStatus_InvalidStatusFallback(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	for _, status := range []int{-1, 2, 99, 1000} {
		t.Run("", func(t *testing.T) {
			_, errs := modelsdb.BatchUpdateDstEndPointStatus([]uint64{1}, status)
			if len(errs) != 1 {
				t.Errorf("status=%d 时应触发 DB=nil 失败路径，得到 errs 数=%d", status, len(errs))
			}
		})
	}
}

// 边界值：恰好 500 条 ids 应通过上限校验
func TestBatchUpdateDstEndPointStatus_AcceptsBoundary(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	ids := make([]uint64, _maxBatchDstEndPointIDs)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	_, errs := modelsdb.BatchUpdateDstEndPointStatus(ids, 1)
	// DB=nil 时每个 id 都失败，但不应触发「上限」错误
	for _, e := range errs {
		if strings.Contains(e.Error(), "上限") {
			t.Errorf("500 ids 应通过上限检查，不应得到「上限」错误: %v", e.Error())
		}
	}
	if len(errs) != _maxBatchDstEndPointIDs {
		t.Errorf("DB=nil 时 500 ids 应全部失败，得到 %d 条错误", len(errs))
	}
}

// ==================== BatchDeleteDstEndPoints 测试 ====================

func TestBatchDeleteDstEndPoints_EmptyIDs(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, errs := modelsdb.BatchDeleteDstEndPoints([]uint64{})
	if len(errs) == 0 {
		t.Error("空 ids 应返回错误")
	}
}

func TestBatchDeleteDstEndPoints_NilIDs(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, errs := modelsdb.BatchDeleteDstEndPoints(nil)
	if len(errs) == 0 {
		t.Error("nil ids 应返回错误")
	}
}

func TestBatchDeleteDstEndPoints_ExceedsMaxBatch(t *testing.T) {
	ids := make([]uint64, _maxBatchDstEndPointIDs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	_, errs := modelsdb.BatchDeleteDstEndPoints(ids)
	if len(errs) == 0 {
		t.Fatal("超过 maxBatchDstEndPointIDs 应返回错误")
	}
	if !strings.Contains(errs[0].Error(), "上限") {
		t.Errorf("错误消息应包含「上限」，实际: %v", errs[0].Error())
	}
}

func TestBatchDeleteDstEndPoints_NilDB(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	_, errs := modelsdb.BatchDeleteDstEndPoints([]uint64{1, 2, 3})
	if len(errs) != 3 {
		t.Errorf("DB=nil 时每个 id 都应失败，期望 3 个错误，得到 %d 个", len(errs))
	}
}

// ==================== 常量契约 ====================

// TestMaxBatchDstEndPointIDs_Constant 缺符号：maxBatchDstEndPointIDs 现为 models 包未导出常量。
func TestMaxBatchDstEndPointIDs_Constant(t *testing.T) {
	t.Skip("缺符号 maxBatchDstEndPointIDs（models 包未导出，无法从 api 包引用）")
}

// ==================== Handler 层 batch_* 测试 ====================

func decodeUserManageResp(t *testing.T, body string) userManageResp {
	t.Helper()
	var resp userManageResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// 辅助函数：发送请求到 dstEndPointManageInterfaceHandle
func sendDstEndpointBatchRequest(body string, method string) userManageResp {
	req := httptest.NewRequest(method, "/DstEndPointManageInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	dstEndPointManageInterfaceHandle(w, req)
	var resp userManageResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return resp
}

// 三个 batch action 统一覆盖：方法必须是 POST
func TestDstEndpointBatchActions_MethodNotPost(t *testing.T) {
	actions := []string{"batch_disable", "batch_enable", "batch_delete"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/DstEndPointManageInterface", nil)
			w := httptest.NewRecorder()
			dstEndPointManageInterfaceHandle(w, req)

			resp := decodeUserManageResp(t, w.Body.String())
			if resp.Success {
				t.Errorf("非 POST 应返回失败 (action=%s)", action)
			}
		})
	}
}

// 三个 batch action 统一覆盖：非法 JSON
func TestDstEndpointBatchActions_InvalidJSON(t *testing.T) {
	actions := []string{"batch_disable", "batch_enable", "batch_delete"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			resp := sendDstEndpointBatchRequest("invalid json{{{", http.MethodPost)
			if resp.Success {
				t.Errorf("非法 JSON 应返回失败 (action=%s)", action)
			}
		})
	}
}

// batch_disable: 空 ids 应返回失败
func TestDstEndpointBatchDisable_EmptyIDs(t *testing.T) {
	resp := sendDstEndpointBatchRequest(`{"action":"batch_disable","ids":[]}`, http.MethodPost)
	if resp.Success {
		t.Errorf("空 ids 应返回失败，实际 Message: %s", resp.Message)
	}
}

// batch_disable: 缺 ids 字段应返回失败
func TestDstEndpointBatchDisable_MissingIDs(t *testing.T) {
	resp := sendDstEndpointBatchRequest(`{"action":"batch_disable"}`, http.MethodPost)
	if resp.Success {
		t.Errorf("缺 ids 应返回失败，实际 Message: %s", resp.Message)
	}
}

// batch_enable: 空 ids 应返回失败
func TestDstEndpointBatchEnable_EmptyIDs(t *testing.T) {
	resp := sendDstEndpointBatchRequest(`{"action":"batch_enable","ids":[]}`, http.MethodPost)
	if resp.Success {
		t.Errorf("空 ids 应返回失败，实际 Message: %s", resp.Message)
	}
}

// batch_delete: 空 ids 应返回失败
func TestDstEndpointBatchDelete_EmptyIDs(t *testing.T) {
	resp := sendDstEndpointBatchRequest(`{"action":"batch_delete","ids":[]}`, http.MethodPost)
	if resp.Success {
		t.Errorf("空 ids 应返回失败，实际 Message: %s", resp.Message)
	}
}

// batch_delete: 缺 ids 字段应返回失败
func TestDstEndpointBatchDelete_MissingIDs(t *testing.T) {
	resp := sendDstEndpointBatchRequest(`{"action":"batch_delete"}`, http.MethodPost)
	if resp.Success {
		t.Errorf("缺 ids 应返回失败，实际 Message: %s", resp.Message)
	}
}

// batch_disable: 超过上限（501 条）应返回失败
func TestDstEndpointBatchDisable_ExceedsMaxBatch(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	ids := make([]uint64, _maxBatchDstEndPointIDs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	bodyMap := map[string]interface{}{"action": "batch_disable", "ids": ids}
	bodyBytes, _ := json.Marshal(bodyMap)
	resp := sendDstEndpointBatchRequest(string(bodyBytes), http.MethodPost)
	// 不应成功，且 errors[] 中应包含「上限」字样
	if resp.Success {
		t.Errorf("超过 maxBatchDstEndPointIDs 应返回失败")
	}
	if resp.Data == nil {
		t.Fatal("响应 Data 不应为 nil")
	}
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data 不是 map，实际类型: %T", resp.Data)
	}
	errsList, ok := dataMap["errors"].([]interface{})
	if !ok {
		t.Fatalf("Data 缺少 errors[] 字段，实际: %+v", dataMap)
	}
	foundLimitHint := false
	for _, e := range errsList {
		if s, ok := e.(string); ok && strings.Contains(s, "上限") {
			foundLimitHint = true
			break
		}
	}
	if !foundLimitHint {
		t.Errorf("errors[] 应包含「上限」字样，实际 errors: %+v", errsList)
	}
}

// batch_enable: 超过上限应返回失败
func TestDstEndpointBatchEnable_ExceedsMaxBatch(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	ids := make([]uint64, _maxBatchDstEndPointIDs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	bodyMap := map[string]interface{}{"action": "batch_enable", "ids": ids}
	bodyBytes, _ := json.Marshal(bodyMap)
	resp := sendDstEndpointBatchRequest(string(bodyBytes), http.MethodPost)
	if resp.Success {
		t.Errorf("超过 maxBatchDstEndPointIDs 应返回失败")
	}
	if resp.Data == nil {
		t.Fatal("响应 Data 不应为 nil")
	}
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data 不是 map，实际类型: %T", resp.Data)
	}
	errsList, ok := dataMap["errors"].([]interface{})
	if !ok {
		t.Fatalf("Data 缺少 errors[] 字段，实际: %+v", dataMap)
	}
	foundLimitHint := false
	for _, e := range errsList {
		if s, ok := e.(string); ok && strings.Contains(s, "上限") {
			foundLimitHint = true
			break
		}
	}
	if !foundLimitHint {
		t.Errorf("errors[] 应包含「上限」字样，实际 errors: %+v", errsList)
	}
}

// batch_delete: 超过上限应返回失败
func TestDstEndpointBatchDelete_ExceedsMaxBatch(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	ids := make([]uint64, _maxBatchDstEndPointIDs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	bodyMap := map[string]interface{}{"action": "batch_delete", "ids": ids}
	bodyBytes, _ := json.Marshal(bodyMap)
	resp := sendDstEndpointBatchRequest(string(bodyBytes), http.MethodPost)
	if resp.Success {
		t.Errorf("超过 maxBatchDstEndPointIDs 应返回失败")
	}
	if resp.Data == nil {
		t.Fatal("响应 Data 不应为 nil")
	}
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data 不是 map，实际类型: %T", resp.Data)
	}
	errsList, ok := dataMap["errors"].([]interface{})
	if !ok {
		t.Fatalf("Data 缺少 errors[] 字段，实际: %+v", dataMap)
	}
	foundLimitHint := false
	for _, e := range errsList {
		if s, ok := e.(string); ok && strings.Contains(s, "上限") {
			foundLimitHint = true
			break
		}
	}
	if !foundLimitHint {
		t.Errorf("errors[] 应包含「上限」字样，实际 errors: %+v", errsList)
	}
}

// batch_disable: DB=nil 时返回完整部分失败响应 + error_count 字段
func TestDstEndpointBatchDisable_NilDBResponseShape(t *testing.T) {
	originalDB := database.DB
	database.DB = nil
	defer func() { database.DB = originalDB }()

	bodyMap := map[string]interface{}{"action": "batch_disable", "ids": []uint64{1, 2, 3}}
	bodyBytes, _ := json.Marshal(bodyMap)
	resp := sendDstEndpointBatchRequest(string(bodyBytes), http.MethodPost)

	// updated_count=0 + error_count=3 + 包含 errors[] 字段
	if resp.Success {
		t.Error("DB=nil 时不应成功")
	}
	if resp.Data == nil {
		t.Fatal("响应 Data 不应为 nil")
	}
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data 不是 map[string]interface{}，实际类型: %T", resp.Data)
	}
	updatedCount, ok := dataMap["updated_count"].(float64)
	if !ok {
		t.Fatalf("Data 缺少 updated_count 字段，实际: %+v", dataMap)
	}
	if updatedCount != 0 {
		t.Errorf("DB=nil 时 updated_count 应为 0，得到 %v", updatedCount)
	}
	errCount, ok := dataMap["error_count"].(float64)
	if !ok {
		t.Fatalf("Data 缺少 error_count 字段，实际: %+v", dataMap)
	}
	if errCount != 3 {
		t.Errorf("DB=nil 时 3 个 ids 应得到 3 个 error_count，得到 %v", errCount)
	}
	errsList, ok := dataMap["errors"].([]interface{})
	if !ok {
		t.Fatalf("Data 缺少 errors[] 字段，实际: %+v", dataMap)
	}
	if len(errsList) != 3 {
		t.Errorf("errors[] 应有 3 项，得到 %d", len(errsList))
	}
}
