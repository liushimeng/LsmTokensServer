package api

// v2.0.83: 源站大模型服务质量监控 API
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
// v2.0.86 阶段DA：统计化重构（时段画像 / 时段带 / 峰谷预测）+ 手动刷新旁路缓存
// v2.0.87 阶段DB：多窗口统计（近 1 天 / 1 周 / 1 月）+ 源站横向差异
// 方案：docs/项目迁移解决方案/源站模型服务质量监控统计化与峰谷预测方案_20261022_143602.md
//
// 管理端（9101）与用户端（29001）共用 /QoSMonitorInterface：
//   - 管理端：全站维度（8 张分表全量，不带用户过滤）
//   - 用户端：user_name 强制取 JWT claims（前端传了也无效），数据仅覆盖
//     本人请求命中的源站模型（阶段BV 越权防护同款语义）
//
// 响应单次返回全部视图数据（24h/7d/30d 三窗口画像 + 时段带 + 峰谷 +
// 24 小时 / 7 天 / 30 天桶 + 7×24 热力图 + 周 P50/P95 + 固定窗口统计 +
// 运行经验预测），前端一次拉取驱动全部图表，窗口切换在客户端完成。
//
// v2.0.86：页面已移除 30s 自动刷新（需求「删除自动刷新相关功能」），
// 手动刷新按钮传 {"refresh": true} 绕过服务端 60s 缓存，确保拿到新快照；
// 默认（无 refresh）走缓存，保护服务端免受高频 8 分表全量扫描。

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// qosMonitorRequest QoS 报告请求体。
type qosMonitorRequest struct {
	// Refresh 为 true 时绕过服务端缓存强制重算（手动刷新）。
	Refresh bool `json:"refresh"`
}

// qosMonitorRespond QoS 报告统一响应封装（管理端/用户端共用）。
// refresh=true 时跳过缓存；否则命中 60s 缓存。
func qosMonitorRespond(w http.ResponseWriter, r *http.Request, userName string, refresh bool) {
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}

	var report *modelsdb.QoSMonitorReport
	var err error
	if refresh {
		report, err = modelsdb.GetQoSMonitorReportFresh(userName, config.G.DBMysqlSubTableNumber)
	} else {
		report, err = modelsdb.GetQoSMonitorReport(userName, config.G.DBMysqlSubTableNumber)
	}
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

// parseQoSMonitorRefresh 解析请求体中的 refresh 标志（空体/非法 JSON 视为 false）。
func parseQoSMonitorRefresh(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || len(body) == 0 {
		return false
	}
	var req qosMonitorRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	return req.Refresh
}

// qosMonitorInterfaceHandle 管理端服务质量监控 API（RegisterManagerAPIRoutes 内
// 注册即受 ManagerAuthMiddleware 保护）。
func qosMonitorInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	qosMonitorRespond(w, r, "", parseQoSMonitorRefresh(r))
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
	qosMonitorRespond(w, r, claims.UserName, parseQoSMonitorRefresh(r))
}
