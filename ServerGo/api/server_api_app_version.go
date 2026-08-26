package api

// 应用版本信息接口（tmpPlan/管理员与用户Web工具栏版本号与编译时间显示方案-20260826-05.md）
// 供 ClientWeb 管理员 Web / 用户 Web 顶部工具栏展示产品名、版本号与前后端编译时间。
// 数据全部为编译期/启动期常量，无敏感信息，轻量只读。

import (
	"encoding/json"
	"net/http"
	"runtime"

	"github.com/lishimeng/LsmTokensServer/config"
)

// appVersionInfo /AppVersionInterface 响应结构
type appVersionInfo struct {
	AppName          string `json:"app_name"`           // 产品标识（LsmTokensServer）
	ProductName      string `json:"product_name"`       // 产品名称
	Version          string `json:"version"`            // 后端版本号（config.APP_VERSION）
	BackendBuildTime string `json:"backend_build_time"` // 后端编译时间（ldflags 注入，可能为空）
	GoVersion        string `json:"go_version"`        // 后端 Go 编译器版本
}

// appVersionInterfaceHandle 返回后端版本号与编译时间（GET）
// 注册于管理端（ManagerAuthMiddleware 保护）与用户端（UserAuthMiddleware）两套 mux。
func appVersionInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	json.NewEncoder(w).Encode(appVersionInfo{
		AppName:          config.APP_NAME,
		ProductName:      config.PRODUCT_NAME,
		Version:          config.APP_VERSION,
		BackendBuildTime: config.BuildTime,
		GoVersion:        runtime.Version(),
	})
}
