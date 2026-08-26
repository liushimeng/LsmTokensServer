package api

// 20260826-05 工具栏版本号/编译时间接口单测：
// 字段齐全、版本号透出、buildTime 注入/未注入两种形态、非 GET 拒绝。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

func callAppVersion(t *testing.T, method string) appVersionInfo {
	t.Helper()
	req := httptest.NewRequest(method, "/AppVersionInterface", nil)
	rec := httptest.NewRecorder()
	appVersionInterfaceHandle(rec, req)

	var info appVersionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	return info
}

func TestAppVersionInterfaceFields(t *testing.T) {
	// 保存/恢复全局 BuildTime，避免影响其他用例
	saved := config.BuildTime
	defer func() { config.BuildTime = saved }()

	config.BuildTime = "2026-08-26_17:53:35"
	info := callAppVersion(t, http.MethodGet)

	if info.AppName != config.APP_NAME {
		t.Fatalf("app_name = %q, want %q", info.AppName, config.APP_NAME)
	}
	if info.ProductName != config.PRODUCT_NAME {
		t.Fatalf("product_name = %q, want %q", info.ProductName, config.PRODUCT_NAME)
	}
	if info.Version != config.APP_VERSION {
		t.Fatalf("version = %q, want %q", info.Version, config.APP_VERSION)
	}
	if !strings.HasPrefix(info.Version, "v") {
		t.Fatalf("版本号应带 v 前缀: %q", info.Version)
	}
	if info.BackendBuildTime != "2026-08-26_17:53:35" {
		t.Fatalf("backend_build_time = %q", info.BackendBuildTime)
	}
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Fatalf("go_version 应形如 go1.x: %q", info.GoVersion)
	}
}

func TestAppVersionInterfaceEmptyBuildTime(t *testing.T) {
	// go build（未经 rebuild 脚本）时 buildTime 为空，接口必须仍可用且字段不缺
	saved := config.BuildTime
	defer func() { config.BuildTime = saved }()

	config.BuildTime = ""
	info := callAppVersion(t, http.MethodGet)
	if info.BackendBuildTime != "" {
		t.Fatalf("未注入时 backend_build_time 应为空: %q", info.BackendBuildTime)
	}
	if info.Version == "" || info.AppName == "" {
		t.Fatalf("版本号/产品名不应为空: %+v", info)
	}
}

func TestAppVersionInterfaceRejectsPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/AppVersionInterface", nil)
	rec := httptest.NewRecorder()
	appVersionInterfaceHandle(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应返回 405, got %d", rec.Code)
	}
}

func TestAppVersionInterfaceRegisteredOnBothMuxes(t *testing.T) {
	// 双构建隔离红线：管理端与用户端 mux 都必须注册本接口。
	// MountAIProxyHandlers 会读 config.G，测试内补默认配置（保存/恢复）。
	savedG := config.G
	config.G = config.DefaultConfig()
	defer func() { config.G = savedG }()

	managerMux := http.NewServeMux()
	RegisterManagerAPIRoutes(managerMux)
	userMux := http.NewServeMux()
	RegisterUserAPIRoutes(userMux)

	for name, mux := range map[string]*http.ServeMux{"manager": managerMux, "user": userMux} {
		h, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/AppVersionInterface", nil))
		if pattern != "/AppVersionInterface" || h == nil {
			t.Fatalf("%s mux 未正确注册 /AppVersionInterface", name)
		}
	}
}
