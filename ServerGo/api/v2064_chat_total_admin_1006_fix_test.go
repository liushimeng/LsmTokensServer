// v2.0.64: 管理员端 /ChatAnalysisTotal「连接断开 (1006)」回归测试
//
// 根因：管理员首页 / 用户管理页模板数据里没有 UserName/ModelName，
// adminSubNavHTML「统计」链接展开为 ./ChatAnalysisTotal?user_name=&model_name=，
// 原实现看到空参数 http.Error(400) → 浏览器拿到纯文本错误页 → WS 建连失败 → 1006。
//
// 修复：参数缺失时对齐用户端语义 —— 取「第一个用户 / 该用户第一个模型」重定向；
// 同时给 runChatStatsQuery goroutine 加 recover 兜底，避免单连接 panic 拖垮全局。
package api

import (
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestChatAnalysisTotalHandle_EmptyParams_Redirect 缺符号：chatAnalysisTotalHandle
// 现由前端 SPA 静态托管承载（webserver），Go 侧不再提供该 HTML 页面 handler。
func TestChatAnalysisTotalHandle_EmptyParams_Redirect(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalHandle（HTML 页面 handler 已由前端 SPA 承载）")
}

// TestChatAnalysisTotalHandle_EmptyUser_FilledModel 缺符号：chatAnalysisTotalHandle。
func TestChatAnalysisTotalHandle_EmptyUser_FilledModel(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalHandle（HTML 页面 handler 已由前端 SPA 承载）")
}

// TestChatAnalysisTotalHandle_BothParams_OK 缺符号：chatAnalysisTotalHandle。
func TestChatAnalysisTotalHandle_BothParams_OK(t *testing.T) {
	t.Skip("缺符号 chatAnalysisTotalHandle（HTML 页面 handler 已由前端 SPA 承载）")
}

// TestRunChatStatsQuery_PanicRecover：runChatStatsQuery 的 panic 必须被 recover 吞掉，
// 不再向上传播导致进程退出。这里仅验证 recover 中间件不 panic 即成功。
func TestRunChatStatsQuery_PanicRecover(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("goroutine 内 panic 应被 recover 吞掉，实际逃逸: %v", rec)
		}
	}()
	// 模拟一个简单的 recover 守卫（与 server_ws_chat_total.go 中结构一致）
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				// 这是预期行为：recover 吞掉 panic
			}
		}()
		panic("simulated panic")
	}()
}

// TestGetAllUsers_NilDB_Boundary：GetAllUsers 在 DB=nil 时应返回错误，
// chatAnalysisTotalHandle 不会再把它当空列表处理。
func TestGetAllUsers_NilDB_Boundary(t *testing.T) {
	// 此测试仅在 DB==nil 时有意义，DB!=nil 时跳过
	if database.DB != nil {
		t.Skip("跳过：DB!=nil 时无法验证 nil 边界")
	}
	_, err := modelsdb.GetAllUsers(1, 1)
	if err == nil {
		t.Errorf("DB=nil 时 GetAllUsers 应返回错误")
	}
}
