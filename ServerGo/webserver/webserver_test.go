package webserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// 阶段T 双构建隔离：clientWebDist 必须按角色定位 dist-manager / dist-user，
// 且不回落到共享 dist（两端产物严禁互取）。
func TestClientWebDistRoleIsolation(t *testing.T) {
	tmp := t.TempDir()
	clientWeb := filepath.Join(tmp, "ClientWeb")
	for _, role := range []string{"manager", "user"} {
		if err := os.MkdirAll(filepath.Join(clientWeb, "dist-"+role), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 旧共享目录存在也不得被任何角色选中
	if err := os.MkdirAll(filepath.Join(clientWeb, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDir := config.SetConfigDirForTest(tmp)
	defer config.SetConfigDirForTest(oldDir)

	cfg := &config.LsmTokensServerConfig{}
	for _, role := range []string{"manager", "user"} {
		got, err := clientWebDist(cfg, role)
		if err != nil {
			t.Fatalf("role %s: %v", role, err)
		}
		if want := filepath.Join(clientWeb, "dist-"+role); got != want {
			t.Fatalf("role %s: got %s, want %s", role, got, want)
		}
		if strings.HasSuffix(got, string(filepath.Separator)+"dist") {
			t.Fatalf("role %s: 不得回落共享 dist 目录: %s", role, got)
		}
	}

	// 配置覆盖优先
	cfg.ManagerWebStaticDir = filepath.Join(tmp, "custom-manager-dist")
	if err := os.MkdirAll(cfg.ManagerWebStaticDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := clientWebDist(cfg, "manager")
	if err != nil || got != cfg.ManagerWebStaticDir {
		t.Fatalf("override: got %s err=%v, want %s", got, err, cfg.ManagerWebStaticDir)
	}
}

// 角色目录缺失时报错（API-only 模式），不得静默改用另一角色目录。
func TestClientWebDistMissingRoleDir(t *testing.T) {
	tmp := t.TempDir()
	clientWeb := filepath.Join(tmp, "ClientWeb")
	if err := os.MkdirAll(filepath.Join(clientWeb, "dist-manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDir := config.SetConfigDirForTest(tmp)
	defer config.SetConfigDirForTest(oldDir)

	cfg := &config.LsmTokensServerConfig{}
	if _, err := clientWebDist(cfg, "user"); err == nil {
		t.Fatal("dist-user 缺失时应返回错误")
	}
}

// v2.0.58 网关代理支持：prefixStripMiddleware 子路径前缀剥离 + SPA 回落尾斜杠 301
func TestPrefixStripMiddleware(t *testing.T) {
	tmp := t.TempDir()
	dist := filepath.Join(tmp, "ClientWeb", "dist-manager")
	assets := filepath.Join(dist, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>idx</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir := config.SetConfigDirForTest(tmp)
	defer config.SetConfigDirForTest(oldDir)

	cfg := &config.LsmTokensServerConfig{}
	mux := http.NewServeMux()
	RegisterAPIRoutes(mux)
	mux.HandleFunc("/UserManageInterface", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true}`)
	})
	managerDist := mountSPA(mux, cfg, "manager")
	authNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟鉴权中间件：公开路由/静态资源放行至 mux，其余 302 登录页
		// （验证剥离后的有效路径对鉴权层可见，子路径公开接口不被误拦）
		p := r.URL.Path
		if n := strings.TrimSuffix(p, "/"); n != "" {
			p = n
		}
		if p == "/UserLogin" || p == "/" || strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/static/") ||
			r.URL.Path == "/UserManageInterface" || r.URL.Path == "/healthz" ||
			r.URL.Path == "/UserLogin" || r.URL.Path == "/" {
			mux.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "UserLogin", http.StatusFound)
	})
	handler := prefixStripMiddleware(authNext, mux, managerDist)
	// ① 子路径静态资源：剥前缀命中真实 JS（而非 SPA 回落的 HTML）
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/ChatAnalysis/assets/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("子路径静态资源应命中真实 JS: code=%d ct=%s body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	// ② 子路径 API：剥前缀命中 JSON 接口
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/ChatAnalysis/UserManageInterface", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success"`) {
		t.Fatalf("子路径 API 应命中 JSON: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// ③ 多级前缀：/a/b/assets/app.js 剥到 /assets/app.js
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/a/b/assets/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("多级前缀静态资源应命中: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// ④ 根级路径不受影响
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("根级静态资源应命中: %d", rec.Code)
	}

	// ⑤ 公开页面导航无尾斜杠：301 补斜杠（相对资源以目录为基准）
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/UserLogin", nil)
	req.Header.Set("Accept", "text/html")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/UserLogin/" {
		t.Fatalf("无尾斜杠页面应 301 补斜杠: code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
	}

	// ⑥ 尾斜杠页面路由：鉴权归一化后放行，SPA 回落 index.html
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/UserLogin/", nil)
	req.Header.Set("Accept", "text/html")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "idx") {
		t.Fatalf("页面路由应回落 index.html: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// ⑦ 非页面请求（无 Accept text/html）不 301，直接 SPA 回落
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/UserLogin", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "idx") {
		t.Fatalf("非页面请求应回落 index.html 而非 301: code=%d", rec.Code)
	}
}
