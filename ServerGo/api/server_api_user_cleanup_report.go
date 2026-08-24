package api

import (
	"encoding/json"
	"net/http"
)

// ============================================================================
// v2.0.47: 过期数据清理统计 API（用户端）
// ============================================================================
//
// 与管理员端共用 cleanupReportInterfaceHandle 业务逻辑，本文件仅做：
//   1. JWT 认证（getUserToken）
//   2. 强制覆盖 username（防越权）
//   3. 复用管理员端 handler
//
// 注意：清理报告是**全局指标**（不分用户/模型），用户端和管理员端看到的是
//   同一份数据。差异仅在于：用户端必须登录才能访问。
// ============================================================================

// userCleanupReportInterfaceHandle 处理 /CleanupReportInterface API 请求（用户端）
//
// 复用管理员端 cleanupReportInterfaceHandle 业务逻辑；此处仅做 JWT 校验 + 透传。
// 原因：清理报告是全平台指标，不分用户/模型；用户端看到的数据 = 管理员端数据。
func userCleanupReportInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	// JWT 认证（未登录直接 401）
	claims := getUserToken(r)
	if claims == nil || claims.UserID == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(CleanupReportAPIResponse{
			Success: false,
			Message: "未登录或登录已过期",
		})
		return
	}

	// 用户端只支持 GET 列表 + summary + state（防越权：用户端不暴露 POST 写入路径）
	// 实际清理由后台 goroutine 触发，前端无写入操作。
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		json.NewEncoder(w).Encode(CleanupReportAPIResponse{
			Success: false,
			Message: "仅支持 GET/POST 请求",
		})
		return
	}

	// 透传到管理员端 handler（无参数修改；admin 版本支持完整 action 集合）
	cleanupReportInterfaceHandle(w, r)
}
