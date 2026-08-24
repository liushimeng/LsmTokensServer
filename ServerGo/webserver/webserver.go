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
	"strings"

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

// RegisterAPIRoutes 挂载后端 REST API 路由（阶段5 按旧版路径逐步补齐）
// 目前提供健康检查；API 处理器来自 api 包。
func RegisterAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","app":"LsmTokensServer"}`)
	})
}

// buildWebMux 构造 Web 端口公共 mux：API + SPA 静态资源
func buildWebMux() (*http.ServeMux, error) {
	mux := http.NewServeMux()
	RegisterAPIRoutes(mux)
	dist, err := clientWebDist()
	if err != nil {
		logger.Printf("[WEB] Warning: %v, Web UI unavailable (API only)", err)
	} else {
		mux.Handle("/", &spaFileServer{root: http.Dir(dist)})
		logger.Printf("[WEB] Serving ClientWeb dist: %s", dist)
	}
	return mux, nil
}

// StartManagerWebServer 启动管理员 Web 服务（管理后台，默认 9101）
func StartManagerWebServer(cfg *config.LsmTokensServerConfig) {
	mux, err := buildWebMux()
	if err != nil {
		logger.Printf("[ERROR] Manager web init failed: %v", err)
		return
	}
	addr := fmt.Sprintf(":%d", cfg.ManagerWebListenPort)
	logger.Printf("[WEB] Manager Web listening on http://localhost:%d/", cfg.ManagerWebListenPort)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Printf("[ERROR] Manager Web server failed: %v", err)
	}
}

// StartUserWebServer 启动用户 Web 服务（用户门户，默认 29001，支持 HTTPS）
func StartUserWebServer(cfg *config.LsmTokensServerConfig) {
	mux, err := buildWebMux()
	if err != nil {
		logger.Printf("[ERROR] User web init failed: %v", err)
		return
	}
	addr := fmt.Sprintf(":%d", cfg.UserWebListenPort)
	if cfg.UserWebUseHTTPS {
		certFile := cfg.UserWebCertFile
		keyFile := cfg.UserWebKeyFile
		// 相对路径基于配置文件目录/工作目录解析
		if !filepath.IsAbs(certFile) {
			if dir, err := config.GetExecutableDir(); err == nil {
				if _, err := os.Stat(filepath.Join(dir, certFile)); err == nil {
					certFile = filepath.Join(dir, certFile)
					keyFile = filepath.Join(dir, keyFile)
				}
			}
		}
		logger.Printf("[WEB] User Web listening on https://localhost:%d/", cfg.UserWebListenPort)
		if err := http.ListenAndServeTLS(addr, certFile, keyFile, mux); err != nil {
			logger.Printf("[ERROR] User Web server failed: %v", err)
		}
		return
	}
	logger.Printf("[WEB] User Web listening on http://localhost:%d/", cfg.UserWebListenPort)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Printf("[ERROR] User Web server failed: %v", err)
	}
}
