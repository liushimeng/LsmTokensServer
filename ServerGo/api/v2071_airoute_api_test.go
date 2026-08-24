package api

// v2.0.71: AIRoute API 最后成功/失败记录字段契约测试。
// 原 v2071_route_last_success_failure_test.go 第 3 节（源码契约守护），
// 适配：getRouteLastRecordByStatus 迁移后已导出为 GetRouteLastRecordByStatus。

import (
	"os"
	"strings"
	"testing"
)

func extractFuncBodyV2071(t *testing.T, src, signature string) string {
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

func TestEnrichRoute_ModelLookupFailureMarksLastRecordFailed(t *testing.T) {
	src, err := os.ReadFile("server_api_manager_ai_route.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2071(t, string(src), "func enrichRoute(")
	idx := strings.Index(body, "GetUserModelByID(")
	if idx < 0 {
		t.Fatal("未找到 GetUserModelByID 调用（前置条件变了，请同步本测试）")
	}
	tail := body[idx:]
	elseIdx := strings.Index(tail, "\t} else {")
	if elseIdx < 0 {
		t.Fatal("enrichRoute 的模型查找失败分支缺失：必须显式标记两组 *_failed")
	}
	elseBody := tail[elseIdx:]
	if end := strings.Index(elseBody, "\n\t}\n"); end >= 0 {
		elseBody = elseBody[:end]
	}
	for _, key := range []string{`result["last_success_failed"] = true`, `result["last_failure_failed"] = true`} {
		if !strings.Contains(elseBody, key) {
			t.Errorf("模型查找失败时必须置 %s", key)
		}
	}
	if !strings.Contains(elseBody, `"查询失败"`) {
		t.Error("模型查找失败时必须渲染「查询失败」，禁止留空让前端 fallback")
	}
}

func TestAIRouteAPI_EmitsLastRecordFields(t *testing.T) {
	for _, f := range []string{"server_api_manager_ai_route.go", "server_api_user_ai_route.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", f, err)
		}
		s := string(src)
		for _, key := range []string{
			`result["last_success_at_text"]`,
			`result["last_failure_at_text"]`,
			`result["last_success_has_record"]`,
			`result["last_failure_has_record"]`,
			`result["last_success_dst_model_name"]`,
			`result["last_failure_dst_model_name"]`,
		} {
			if !strings.Contains(s, key) {
				t.Errorf("%s: 必须输出 %s", f, key)
			}
		}
		for _, text := range []string{`"暂无成功记录"`, `"暂无失败记录"`, `"查询失败"`} {
			if !strings.Contains(s, text) {
				t.Errorf("%s: 缺少文案 %s", f, text)
			}
		}
		if !strings.Contains(s, "BatchGetRouteLastUsedTimes(") {
			t.Errorf("%s: 最后记录必须走 BatchGetRouteLastUsedTimes 批量链路", f)
		}
		// 单条兜底也必须走新函数（迁移后已导出为 GetRouteLastRecordByStatus）
		if !strings.Contains(s, "GetRouteLastRecordByStatus(") {
			t.Errorf("%s: 单条兜底必须走 GetRouteLastRecordByStatus", f)
		}
	}
}
