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
		// 单请求总超时（覆盖 DNS + TCP + TLS + 请求 + 响应），保留 300s 以兼容长流式响应
		Timeout: 300 * time.Second,
		Transport: &http.Transport{
			// 拨号阶段：单次 TCP 连接最多等待 10s（防止 DNS 阻塞拖垮整个连接池）
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			// TLS 握手：防止上游证书异常时挂死
			TLSHandshakeTimeout: 10 * time.Second,
			// 首字节超时：与 aiProxyServer.ReadTimeout 对齐（60s），防止上游接收请求后长时间不回响应头
			ResponseHeaderTimeout: 60 * time.Second,
			// 100-continue 等待：1s 内无 ACK 则放弃等待
			ExpectContinueTimeout: 1 * time.Second,
			// 连接池
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			MaxConnsPerHost:     0, // 0 表示不限制单 host 总并发
			DisableKeepAlives:   false,
			DisableCompression:  false,
			ForceAttemptHTTP2:   true, // 优先 h2（与 Claude / Kilo 等现代客户端一致）
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
