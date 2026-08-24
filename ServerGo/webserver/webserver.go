// Package webserver - 前端静态资源服务与 Web 端口装配
//
// 新架构：前端由 ClientWeb（React+Vite）构建，产物 dist/ 由本包托管；
// 后端 REST API 由 api 包提供（阶段5 逐步挂载路由，路径与旧版保持一致）。
// 旧工程 server_web_manager.go / server_web_user.go 的 Go 内嵌 HTML 已废弃。
package webserver

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"strings"

	"github.com/lishimeng/LsmTokensServer/api"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
)

// clientWebDist 定位前端构建产物目录（ServerGo/../ClientWeb/dist）
func clientWebDist() (string, error) {
	execDir, err := config.GetExecutableDir()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(execDir, "..", "ClientWeb", "dist"),
		filepath.Join(execDir, "ClientWeb", "dist"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("ClientWeb/dist not found (candidates: %v)", candidates)
}

// spaFileServer SPA 静态文件服务：存在则返回文件，否则回落 index.html
type spaFileServer struct {
	root http.FileSystem
}

func (s *spaFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	f, err := s.root.Open(path)
	if err != nil {
		// 回落到 index.html（前端路由）
		if f2, err2 := s.root.Open("index.html"); err2 == nil {
			f = f2
		} else {
			http.NotFound(w, r)
			return
		}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	// http.ServeContent 需要 io.Seeker
	if seeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, st.Name(), st.ModTime(), seeker)
		return
	}
	http.NotFound(w, r)
}

// RegisterAPIRoutes 挂载健康检查（阶段5：其余路由见 buildManagerMux / buildUserMux）
func RegisterAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","app":"LsmTokensServer"}`)
	})
}

// mountSPA 在 mux 上挂载前端静态资源（找不到 dist 时仅记日志，API 不受影响）
func mountSPA(mux *http.ServeMux) {
	dist, err := clientWebDist()
	if err != nil {
		logger.Printf("[WEB] Warning: %v, Web UI unavailable (API only)", err)
		return
	}
	mux.Handle("/", &spaFileServer{root: http.Dir(dist)})
	logger.Printf("[WEB] Serving ClientWeb dist: %s", dist)
}

// buildManagerMux 构造管理端 mux：管理 API + SPA（安全链在 Start 时套上）
func buildManagerMux() *http.ServeMux {
	mux := http.NewServeMux()
	RegisterAPIRoutes(mux)
	api.RegisterManagerAPIRoutes(mux)
	mountSPA(mux)
	return mux
}

// buildUserMux 构造用户端 mux：用户 API（含登录）+ SPA（鉴权中间件在 Start 时套上）
func buildUserMux() *http.ServeMux {
	mux := http.NewServeMux()
	RegisterAPIRoutes(mux)
	api.RegisterUserAPIRoutes(mux)
	mountSPA(mux)
	return mux
}

// StartManagerWebServer 启动管理员 Web 服务（管理后台，默认 49101）
func StartManagerWebServer(cfg *config.LsmTokensServerConfig) {
	mux := buildManagerMux()
	addr := fmt.Sprintf(":%d", cfg.ManagerWebListenPort)
	server := &http.Server{
		Addr:         addr,
		Handler:      ManagerSecurityChain(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	logger.Printf("[WEB] Manager Web listening on http://localhost:%d/", cfg.ManagerWebListenPort)
	if err := server.ListenAndServe(); err != nil {
		logger.Printf("[ERROR] Manager Web server failed: %v", err)
	}
}

// StartUserWebServer 启动用户 Web 服务（用户门户，默认 42901，支持 HTTPS）
// 与旧版一致：UserSecurityChain(userAuthMiddleware(mux))
func StartUserWebServer(cfg *config.LsmTokensServerConfig) {
	mux := buildUserMux()
	handler := UserSecurityChain(api.UserAuthMiddleware(mux))
	addr := fmt.Sprintf(":%d", cfg.UserWebListenPort)
	if cfg.UserWebUseHTTPS {
		certFile := cfg.UserWebCertFile
		keyFile := cfg.UserWebKeyFile
		// 相对路径查找顺序：① 可执行文件目录 ② 可执行文件父目录（工程根目录） ③ 当前工作目录
		if !filepath.IsAbs(certFile) {
			if dir, err := config.GetExecutableDir(); err == nil {
				if _, err := os.Stat(filepath.Join(dir, certFile)); err == nil {
					certFile = filepath.Join(dir, certFile)
					keyFile = filepath.Join(dir, keyFile)
				} else {
					parentCert := filepath.Join(dir, "..", certFile)
					if _, err := os.Stat(parentCert); err == nil {
						certFile = parentCert
						keyFile = filepath.Join(dir, "..", keyFile)
					}
				}
			}
		}
		logger.Printf("[WEB] User Web listening on https://localhost:%d/", cfg.UserWebListenPort)
		if err := http.ListenAndServeTLS(addr, certFile, keyFile, handler); err != nil {
			logger.Printf("[ERROR] User Web server failed: %v", err)
		}
		return
	}
	logger.Printf("[WEB] User Web listening on http://localhost:%d/", cfg.UserWebListenPort)
	if err := http.ListenAndServe(addr, handler); err != nil {
		logger.Printf("[ERROR] User Web server failed: %v", err)
	}
}
