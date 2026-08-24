package proxy

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/logger"
	"net"
	"net/http"
	"sync"
	"time"
)

const MaxRequestBodySize = 50 * 1024 * 1024 // 50 MB

var (
	aiProxyServer    *http.Server
	aiProxyTLSServer *http.Server // v2.0.31: AI 代理 HTTPS 服务（与 HTTP 复用同一 mux/handler）
	aiProxyMutex     sync.RWMutex
	sharedHTTPClient = &http.Client{
		Timeout: 300 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
)

// isPortAvailable 检查指定端口是否可被监听（未被其他进程占用）
func isPortAvailable(port int) bool {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// StopAIProxyService 停止 AI 代理服务（HTTP + HTTPS 同时关闭）
func StopAIProxyService() {
	aiProxyMutex.Lock()
	defer aiProxyMutex.Unlock()

	if aiProxyServer != nil {
		_ = aiProxyServer.Close()
		logger.Printf("[PROXY] AI proxy server stopped")
	}
	aiProxyServer = nil
	if aiProxyTLSServer != nil {
		_ = aiProxyTLSServer.Close()
		logger.Printf("[PROXY] AI proxy HTTPS server stopped")
	}
	aiProxyTLSServer = nil
}
