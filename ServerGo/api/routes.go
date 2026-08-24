package api

// 阶段5 路由挂载：按旧工程 server_web_manager.go / server_web_user.go 的路径 1:1 挂载
// 全部 REST API（Interface 后缀的 JSON 接口）。旧页面级 HTML handler 已废弃（由 ClientWeb
// SPA 替代），页面路径由 webserver 的 SPA 回落机制处理。
//
// 用户端鉴权由 webserver 层的 UserAuthMiddleware（本包导出）+ UserSecurityChain 统一套上，
// 与旧工程 Server: Handler: UserSecurityChain(userAuthMiddleware(mux)) 结构一致。

import (
	"net/http"

	"github.com/lishimeng/LsmTokensServer/proxy"
	"github.com/lishimeng/LsmTokensServer/websocket"
)

// UserAuthMiddleware 导出用户认证中间件（供 webserver 装配用户端 mux 使用）
func UserAuthMiddleware(next http.Handler) http.Handler {
	return userAuthMiddleware(next)
}

// RegisterManagerAPIRoutes 挂载管理端（managerWebListenPort）REST API 路由
func RegisterManagerAPIRoutes(mux *http.ServeMux) {
	// 对话分析
	mux.HandleFunc("/ChatAnalysisInterface", chatAnalysisInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisDstModelsInterface", chatAnalysisDstModelsInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisAgentToolsInterface", chatAnalysisAgentToolsInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisDetailInterface", chatAnalysisDetailInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisBatchDeleteInterface", chatAnalysisBatchDeleteInterfaceHandle) // v2.0.29 批量删除
	mux.HandleFunc("/ChatAnalysisTotalInterface", chatAnalysisTotalInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisTotalRangeInterface", chatAnalysisTotalRangeReportHandle)
	mux.HandleFunc("/ChatAnalysisTotalWS", websocket.ChatAnalysisTotalWSHandle) // v2.0.55 WebSocket 流式分块推送
	mux.HandleFunc("/ChatAnalysisSessionInterface", chatAnalysisSessionInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisTaskInterface", chatAnalysisTaskInterfaceHandle)

	// 用户/模型/端点/路由管理
	mux.HandleFunc("/UserManageInterface", userManageInterfaceHandle)
	mux.HandleFunc("/UserModelManageInterface", userModelManageInterfaceHandle)
	mux.HandleFunc("/DstEndPointManageInterface", dstEndPointManageInterfaceHandle)
	mux.HandleFunc("/AIRouteManageInterface", aiRouteManageInterfaceHandle)
	mux.HandleFunc("/ModelInfoInterface", modelInfoInterfaceHandle)
	mux.HandleFunc("/ModelInfoManageInterface", modelInfoManageInterfaceHandle)
	mux.HandleFunc("/AgentInfoInterface", agentInfoInterfaceHandle)

	// 管理端首页信息（无登录态，返回管理员标识）
	mux.HandleFunc("/UserInfoInterface", managerInfoInterfaceHandle)
	mux.HandleFunc("/UserModelListInterface", managerModelListInterfaceHandle)

	// 系统信息
	mux.HandleFunc("/BuildTimeLogInterface", buildTimeLogInterfaceHandle)
	mux.HandleFunc("/GitInfoInterface", gitInfoInterfaceHandle)
	mux.HandleFunc("/SystemInfoInterface", systemInfoInterfaceHandle)
	mux.HandleFunc("/SourceCodeInterface", sourceCodeInterfaceHandle)
	mux.HandleFunc("/ReadmeInterface", readmeInterfaceHandle)

	// 对话页面数据接口
	mux.HandleFunc("/ChatDialogInterface", chatDialogInterfaceHandle)

	// Agent 代理路径挂载（同进程转发，让 JS 可以用相对 URL 同源访问，
	// 解决跨端口 CORS / Mixed Content 导致 fetch 被浏览器静默阻止的问题）
	proxy.MountAIProxyHandlers(mux)

	// 爬虫功能 API（管理端：isAdmin=true, userID=0）
	mux.HandleFunc("/SpiderDataSourceInterface", func(w http.ResponseWriter, r *http.Request) {
		SpiderDataSourceInterfaceHandler(w, r, true, 0)
	})
	mux.HandleFunc("/SpiderDailyInfoInterface", func(w http.ResponseWriter, r *http.Request) {
		SpiderDailyInfoInterfaceHandler(w, r, true, 0)
	})
	mux.HandleFunc("/SpiderDataSourceCrawl", func(w http.ResponseWriter, r *http.Request) {
		SpiderDataSourceCrawlHandler(w, r, true, 0)
	})

	// v2.0.47: 过期数据清理报告
	mux.HandleFunc("/CleanupReportInterface", cleanupReportInterfaceHandle)

	// 协议转换分析器（管理端 7 条）
	mux.HandleFunc("/ProtocolConvertAnalyzerStatus", protocolConvertAnalyzerStatusInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerToggle", protocolConvertAnalyzerToggleInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerTest", protocolConvertAnalyzerTestInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerRecords", protocolConvertAnalyzerRecordsInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerRecordDetail", protocolConvertAnalyzerRecordDetailInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerUsers", protocolConvertAnalyzerUsersInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerMapping", protocolConvertAnalyzerMappingInterface)

	// 工具类公共接口（证书下载 / Wiki / 用户操作日志）
	mux.HandleFunc("/CertDownloadInfoInterface", certDownloadInfoInterfaceHandle)
	mux.HandleFunc("/CertDownloadInterface", certDownloadInterfaceHandle)
	mux.HandleFunc("/WikiInterface", wikiInterfaceHandle)
	mux.HandleFunc("/UserInfoLogInterface", userInfoLogInterfaceHandle)
}

// RegisterUserAPIRoutes 挂载用户端（userWebListenPort）REST API 路由
func RegisterUserAPIRoutes(mux *http.ServeMux) {
	// 登录相关 API（公开，userAuthMiddleware 内已放行）
	mux.HandleFunc("/CaptchaGenerate", captchaGenerateHandle)
	mux.HandleFunc("/UserLoginInterface", userLoginInterfaceHandle)

	// 用户首页 API
	mux.HandleFunc("/UserInfoInterface", userInfoInterfaceHandle)
	mux.HandleFunc("/UserModelListInterface", userModelListInterfaceHandle)
	mux.HandleFunc("/UserLogoutInterface", userLogoutInterfaceHandle)

	// 用户分析 API（与管理员同名，不同端口）
	mux.HandleFunc("/ChatAnalysisInterface", userChatAnalysisInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisDstModelsInterface", userChatAnalysisDstModelsInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisAgentToolsInterface", userChatAnalysisAgentToolsInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisDetailInterface", userChatAnalysisDetailInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisTotalInterface", userChatAnalysisTotalInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisTotalRangeInterface", userChatAnalysisTotalRangeReportHandle)
	mux.HandleFunc("/ChatAnalysisTotalWS", websocket.ChatAnalysisTotalWSHandle) // 用户端复用同一 handler
	mux.HandleFunc("/ChatAnalysisSessionInterface", userChatAnalysisSessionInterfaceHandle)
	mux.HandleFunc("/ChatAnalysisTaskInterface", userChatAnalysisTaskInterfaceHandle)

	// 端点/路由/模型/Agent 信息
	mux.HandleFunc("/UserAIRouteInterface", userAIRouteInterfaceHandle)
	mux.HandleFunc("/DstEndPointManageInterface", userDstEndPointInterfaceHandle)
	mux.HandleFunc("/ModelInfoInterface", userModelInfoInterfaceHandle)
	mux.HandleFunc("/AgentInfoInterface", userAgentInfoInterfaceHandle)

	// 系统信息
	mux.HandleFunc("/BuildTimeLogInterface", buildTimeLogInterfaceHandle)
	mux.HandleFunc("/GitInfoInterface", gitInfoInterfaceHandle)
	mux.HandleFunc("/SystemInfoInterface", systemInfoInterfaceHandle)
	mux.HandleFunc("/SourceCodeInterface", sourceCodeInterfaceHandle)
	mux.HandleFunc("/ReadmeInterface", readmeInterfaceHandle)

	// 对话页面数据接口
	mux.HandleFunc("/ChatDialogInterface", userChatDialogInterfaceHandle)

	// Agent 代理路径挂载（同进程转发，同上）
	proxy.MountAIProxyHandlers(mux)

	// 爬虫功能 API（用户端：isAdmin=false，userID 从登录态解析）
	mux.HandleFunc("/SpiderDataSourceInterface", func(w http.ResponseWriter, r *http.Request) {
		claims := getUserToken(r)
		SpiderDataSourceInterfaceHandler(w, r, false, claims.UserID)
	})
	mux.HandleFunc("/SpiderDailyInfoInterface", func(w http.ResponseWriter, r *http.Request) {
		claims := getUserToken(r)
		SpiderDailyInfoInterfaceHandler(w, r, false, claims.UserID)
	})
	mux.HandleFunc("/SpiderDataSourceCrawl", func(w http.ResponseWriter, r *http.Request) {
		claims := getUserToken(r)
		SpiderDataSourceCrawlHandler(w, r, false, claims.UserID)
	})

	// v2.0.47: 过期数据清理报告（用户端）
	mux.HandleFunc("/CleanupReportInterface", userCleanupReportInterfaceHandle)

	// 协议转换分析器（用户端 5 条，无 Toggle/Users；记录强制按登录用户过滤）
	mux.HandleFunc("/ProtocolConvertAnalyzerStatus", protocolConvertAnalyzerStatusInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerTest", protocolConvertAnalyzerTestInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerRecords", userProtocolConvertAnalyzerRecordsInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerRecordDetail", userProtocolConvertAnalyzerRecordDetailInterface)
	mux.HandleFunc("/ProtocolConvertAnalyzerMapping", protocolConvertAnalyzerMappingInterface)

	// 工具类公共接口（证书下载 / Wiki / 用户操作日志）
	mux.HandleFunc("/CertDownloadInfoInterface", certDownloadInfoInterfaceHandle)
	mux.HandleFunc("/CertDownloadInterface", certDownloadInterfaceHandle)
	mux.HandleFunc("/WikiInterface", wikiInterfaceHandle)
	mux.HandleFunc("/UserInfoLogInterface", userInfoLogInterfaceHandle)
}
