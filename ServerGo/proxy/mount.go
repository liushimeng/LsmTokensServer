package proxy

// MountAIProxyHandlers 将 Anthropic/OpenAI 代理 handler 挂载到 Web 端口 mux 上
// （同进程转发，迁移自旧工程 server_web_manager.go / server_web_user.go 的挂载逻辑）。
// 让浏览器 JS 可以用相对 URL 同源访问，解决跨端口 CORS / Mixed Content 问题。
// 代理路径自带 API Key 认证，无需 JWT。
import (
	"net/http"

	"github.com/lishimeng/LsmTokensServer/config"
)

// MountAIProxyHandlers 在 mux 上挂载 /<AgentAnthropicListenURL>/ 与 /<AgentOpenAIListenURL>/ 转发
func MountAIProxyHandlers(mux *http.ServeMux) {
	anthropicPath := "/" + config.G.AgentAnthropicListenURL
	openaiPath := "/" + config.G.AgentOpenAIListenURL
	mux.HandleFunc(anthropicPath+"/", anthropicProxyHandler)
	mux.HandleFunc(openaiPath+"/", openAIProxyHandler)
	mux.HandleFunc(anthropicPath, anthropicProxyHandler)
	mux.HandleFunc(openaiPath, openAIProxyHandler)
}
