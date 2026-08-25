package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// v2.0.57 Web 迁移回归护栏：固化「页面遍历」为可重复执行的测试。
// 两个断言维度：
//  1. 路由注册对等性：管理端全部接口在 mux 上可解析（ServeMux.Handler 不触达 handler，无需 DB）；
//  2. 安全红线：上述全部接口未携带 JWT 时被 ManagerAuthMiddleware 统一 401 拦截。
//
// 与旧工程 LsmTokensServer server_web_manager.go 的路由清单一一对应（同源代理前缀由 proxy 包挂载，不在此列）。

var e2eManagerRoutes = []string{
	// 对话分析
	"/ChatAnalysisInterface", "/ChatAnalysisDstModelsInterface", "/ChatAnalysisAgentToolsInterface",
	"/ChatAnalysisDetailInterface", "/ChatAnalysisBatchDeleteInterface", "/ChatAnalysisTotalInterface",
	"/ChatAnalysisTotalRangeInterface", "/ChatAnalysisTotalWS", "/ChatAnalysisSessionInterface",
	"/ChatAnalysisTaskInterface",
	// 管理业务
	"/UserManageInterface", "/UserModelManageInterface", "/DstEndPointManageInterface",
	"/AIRouteManageInterface", "/ModelInfoInterface", "/ModelInfoManageInterface", "/AgentInfoInterface",
	// 首页
	"/UserInfoInterface", "/UserModelListInterface",
	// 系统信息
	"/GitInfoInterface", "/SystemInfoInterface",
	"/WikiInterface", "/UserInfoLogInterface",
	// 对话
	"/ChatDialogInterface",
	// 爬虫
	"/SpiderDataSourceInterface", "/SpiderDailyInfoInterface", "/SpiderDataSourceCrawl",
	// 清理报告
	"/CleanupReportInterface",
	// 协议转换分析器
	"/ProtocolConvertAnalyzerStatus", "/ProtocolConvertAnalyzerToggle", "/ProtocolConvertAnalyzerTest",
	"/ProtocolConvertAnalyzerRecords", "/ProtocolConvertAnalyzerRecordDetail",
	"/ProtocolConvertAnalyzerUsers", "/ProtocolConvertAnalyzerMapping",
	// 工具
	"/CertDownloadInfoInterface", "/CertDownloadInterface",
}

func TestManagerRoutesRegisteredAndProtected(t *testing.T) {
	mux := http.NewServeMux()
	if config.G == nil {
		config.G = config.DefaultConfig()
	}
	RegisterManagerAPIRoutes(mux)

	// 1) 路由注册对等性：全部路径必须能在 mux 上解析（"" 表示命中 "/" 兜底，视为未注册）
	for _, p := range e2eManagerRoutes {
		_, pattern := mux.Handler(httptest.NewRequest("POST", p, nil))
		if pattern == "" || pattern == "/" {
			t.Errorf("路由未注册: %s", p)
		}
	}

	// 2) 安全红线：全部业务接口未鉴权 → 401，且不触达业务 handler
	protected := ManagerAuthMiddleware(mux)
	for _, p := range e2eManagerRoutes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", p, nil)
		req.Header.Set("Content-Type", "application/json")
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("路由 %s 未被鉴权拦截: got %d", p, rec.Code)
		}
	}
}

// 登录相关公开路由必须在登录 mux 上可解析（/CaptchaGenerate、/ManagerLoginInterface）
func TestManagerLoginRoutesRegisteredPublic(t *testing.T) {
	mux := http.NewServeMux()
	RegisterManagerLoginRoutes(mux)
	for _, p := range []string{"/CaptchaGenerate", "/ManagerLoginInterface", "/ManagerLogoutInterface"} {
		_, pattern := mux.Handler(httptest.NewRequest("POST", p, nil))
		if pattern == "" || pattern == "/" {
			t.Errorf("登录路由未注册: %s", p)
		}
	}
}

// e2eUserRoutes 用户端（userWebListenPort=29001）mux 必须注册的接口全量清单。
// 护栏：防止前端已按用户角色调用（如 UserAIRouteInterface），后端却漏挂路由。
var e2eUserRoutes = []string{
	"/CaptchaGenerate", "/UserLoginInterface",
	"/UserInfoInterface", "/UserModelListInterface", "/UserLogoutInterface",
	"/ChatAnalysisInterface", "/ChatAnalysisDstModelsInterface", "/ChatAnalysisAgentToolsInterface",
	"/ChatAnalysisDetailInterface", "/ChatAnalysisTotalInterface", "/ChatAnalysisTotalRangeInterface",
	"/ChatAnalysisTotalWS", "/ChatAnalysisSessionInterface", "/ChatAnalysisTaskInterface",
	"/UserAIRouteInterface", "/DstEndPointManageInterface", "/ModelInfoInterface", "/AgentInfoInterface",
	"/GitInfoInterface", "/SystemInfoInterface",
	"/ChatDialogInterface",
	"/SpiderDataSourceInterface", "/SpiderDailyInfoInterface", "/SpiderDataSourceCrawl",
	"/CleanupReportInterface",
	"/ProtocolConvertAnalyzerStatus", "/ProtocolConvertAnalyzerTest", "/ProtocolConvertAnalyzerRecords",
	"/ProtocolConvertAnalyzerRecordDetail", "/ProtocolConvertAnalyzerMapping",
	"/CertDownloadInfoInterface", "/CertDownloadInterface", "/WikiInterface", "/UserInfoLogInterface",
}

// 用户端路由注册对等性：前端用户角色依赖的关键接口必须全部可解析，
// 且管理端独占接口（AIRouteManageInterface/UserManageInterface/批量删除）不得挂到用户 mux。
func TestUserRoutesRegistered(t *testing.T) {
	mux := http.NewServeMux()
	if config.G == nil {
		config.G = config.DefaultConfig()
	}
	RegisterUserAPIRoutes(mux)

	for _, p := range e2eUserRoutes {
		_, pattern := mux.Handler(httptest.NewRequest("POST", p, nil))
		if pattern == "" || pattern == "/" {
			t.Errorf("用户端路由未注册: %s", p)
		}
	}
	for _, p := range []string{"/AIRouteManageInterface", "/UserManageInterface", "/ChatAnalysisBatchDeleteInterface"} {
		_, pattern := mux.Handler(httptest.NewRequest("POST", p, nil))
		if pattern != "" && pattern != "/" {
			t.Errorf("管理端独占接口泄漏到用户 mux: %s", p)
		}
	}
}
