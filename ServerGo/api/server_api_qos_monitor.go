package api

// v2.0.83: 源站大模型服务质量监控 API
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
//
// 管理端（9101）与用户端（29001）共用 /QoSMonitorInterface：
//   - 管理端：全站维度（8 张分表全量，不带用户过滤）
//   - 用户端：user_name 强制取 JWT claims（前端传了也无效），数据仅覆盖
//     本人请求命中的源站模型（阶段BV 越权防护同款语义）
//
// 响应单次返回全部视图数据（实时卡 + 60 分钟 + 24 小时 + 7 天 + 7×24 热力图 +
// 周 P50/P95），前端一次拉取驱动四类图表，窗口切换在客户端完成。

import (
	"encoding/json"
	"net/http"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// qosMonitorRespond QoS 报告统一响应封装（管理端/用户端共用）。
func qosMonitorRespond(w http.ResponseWriter, r *http.Request, userName string) {
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}

	report, err := modelsdb.GetQoSMonitorReport(userName, config.G.DBMysqlSubTableNumber)
	if err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
		return
	}
	json.NewEncoder(w).Encode(userManageResp{
		Success: true,
		Message: "查询成功",
		Data:    report,
	})
}

// qosMonitorInterfaceHandle 管理端服务质量监控 API（RegisterManagerAPIRoutes 内
// 注册即受 ManagerAuthMiddleware 保护）。
func qosMonitorInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	qosMonitorRespond(w, r, "")
}

// userQoSMonitorInterfaceHandle 用户端服务质量监控 API（JWT claims 过滤）。
func userQoSMonitorInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未登录"})
		return
	}
	qosMonitorRespond(w, r, claims.UserName)
}
