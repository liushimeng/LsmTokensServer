package webserver

// v2.0.74 回归测试（自动化测试报告 20260826_201128 BUG-3/SUG-2）：
// 未注册的 API 形态路径（Interface 后缀 / ProtocolConvertAnalyzer 前缀家族）
// 返回 404 JSON 而非 200 + SPA index.html；非 API 路径的 SPA 回落行为不变。

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// buildTestSPAChain 构造与用户端一致的 mux + SPA + 前缀剥离链（鉴权层省略，
// 鉴权通过后请求才会进入 mux "/" 回落，不影响 spaFileServer 的 404 判定）
func buildTestSPAChain(t *testing.T) http.Handler {
	t.Helper()
	tmp := t.TempDir()
	dist := filepath.Join(tmp, "ClientWeb", "dist-user")
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>idx</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir := config.SetConfigDirForTest(tmp)
	t.Cleanup(func() { config.SetConfigDirForTest(oldDir) })

	cfg := &config.LsmTokensServerConfig{}
	mux := http.NewServeMux()
	RegisterAPIRoutes(mux)
	userDist := mountSPA(mux, cfg, "user")
	return prefixStripMiddleware(http.HandlerFunc(mux.ServeHTTP), mux, userDist)
}

// TestUnregisteredAPIPathReturns404JSON 用户端访问未注册的管理专属 API：
// 404 + JSON（接口不存在），不再回落 SPA index.html。
func TestUnregisteredAPIPathReturns404JSON(t *testing.T) {
	handler := buildTestSPAChain(t)

	// 报告 §5.3 实测的 4 条路径 + 尾斜杠变体
	for _, p := range []string{
		"/UserManageInterface",
		"/UserManageInterface/",
		"/ChatAnalysisBatchDeleteInterface",
		"/ProtocolConvertAnalyzerToggle",
		"/ProtocolConvertAnalyzerUsers",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: code = %d, want 404", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s: content-type = %q, want JSON", p, ct)
		}
		if !strings.Contains(rec.Body.String(), "接口不存在") {
			t.Fatalf("%s: body = %q, want 接口不存在", p, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: 不得回落 SPA HTML", p)
		}
	}

	// 网关子路径前缀下的未注册 API：剥前缀后同样 404 JSON
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/pfx/UserManageInterface", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "接口不存在") {
		t.Fatalf("子路径未注册 API 应 404 JSON: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestNonAPIPathSPAFallbackUnchanged 非 API 路径行为保持不变：
// 无 Accept 的未知路径仍回落 index.html（对应 TestPrefixStripMiddleware ⑦ 语义），
// text/html 导航仍 301 补尾斜杠，已注册路由正常。
func TestNonAPIPathSPAFallbackUnchanged(t *testing.T) {
	handler := buildTestSPAChain(t)

	// ① 无 Accept：SPA 回落（不 301、不 404）
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/UserLogin", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "idx") {
		t.Fatalf("非页面请求应回落 index.html: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// ② text/html 导航：301 补尾斜杠
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/UserLogin", nil)
	req.Header.Set("Accept", "text/html")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/UserLogin/" {
		t.Fatalf("text/html 导航应 301: code=%d loc=%s", rec.Code, rec.Header().Get("Location"))
	}

	// ③ 静态资源不受影响
	rec = httptest.NewRecorder()
	// buildTestSPAChain 未写 assets 文件，此处只验证已注册路由 /healthz
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("已注册路由 /healthz 应正常: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
