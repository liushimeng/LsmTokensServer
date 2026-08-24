// v2.0.55: /ChatAnalysisTotalWS WebSocket Hub
//
// 连接注册中心：维护所有活跃 /ChatAnalysisTotalWS 连接，提供：
//   - 并发安全 register / unregister（幂等，sync.RWMutex）
//   - 全局连接上限（config.CHAT_STATS_MAX_CONNS=128，超出返回 errHubFull）
//   - closeOnce 幂等关闭，防止 goroutine 泄漏
//
// 参考 v2.0.33 spider.spiderSessions 注册表同款 RWMutex + map 模式。
package websocket

import (
	"context"
	"errors"
	config "github.com/lishimeng/LsmTokensServer/config"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// errHubFull Hub 已满（config.CHAT_STATS_MAX_CONNS=128），HTTP 层返回 503
var errHubFull = errors.New("chat stats hub is full")

// wsHub WebSocket 连接注册中心
type wsHub struct {
	mu       sync.RWMutex
	conns    map[*wsConn]struct{}
	counter  atomic.Int64 // 活跃连接数（与 len(conns) 一致，便于无锁观测）
	upgrader websocket.Upgrader
}

// wsConn 单个 WS 连接（生命周期 = ChatAnalysisTotalWSHandle 一次调用）
type wsConn struct {
	hub        *wsHub
	ws         *websocket.Conn
	send       chan []byte // 序列化 outgoing 帧；writePump 唯一消费者
	busy       atomic.Bool // 单连接防并发 query
	cancel     context.CancelFunc
	closeOnce  sync.Once
	createdAt  time.Time
	userName   string
	remoteAddr string
	// v2.0.68 校正：页面上下文的 user_name / model_name（来自 URL query），供
	// runChatStatsQuery → streamChatStats 在 WHERE 中限定「本平台用户的某个模型」。
	// 切换本平台模型时 stage 4 数据必须随之变化（用户反馈的核心痛点）。
	ctxUserName  string
	ctxModelName string
}

// chatStatsHub /ChatAnalysisTotalWS 全局 Hub
var chatStatsHub = &wsHub{
	conns: make(map[*wsConn]struct{}),
	upgrader: websocket.Upgrader{
		HandshakeTimeout: config.WS_HANDSHAKE_TIMEOUT,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		// 项目为内部局域网代理；前端与后端同源部署（manager / user web 服务），
		// 启用宽松策略便于本地开发。如未来对外暴露，需收紧 origin 校验。
		CheckOrigin: func(r *http.Request) bool { return true },
	},
}

// register 注册连接（持写锁；幂等：重复注册会覆盖 map entry 但不影响计数）
func (h *wsHub) register(c *wsConn) {
	h.mu.Lock()
	if _, exists := h.conns[c]; !exists {
		h.conns[c] = struct{}{}
		h.counter.Add(1)
	}
	h.mu.Unlock()
}

// unregister 注销连接（持写锁；幂等：重复注销不会让计数变负）
func (h *wsHub) unregister(c *wsConn) {
	h.mu.Lock()
	if _, exists := h.conns[c]; exists {
		delete(h.conns, c)
		h.counter.Add(-1)
	}
	h.mu.Unlock()
}

// count 当前活跃连接数（持读锁）
func (h *wsHub) count() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return int64(len(h.conns))
}

// upgrade 升级 HTTP 连接为 WebSocket。超 config.CHAT_STATS_MAX_CONNS 返回 errHubFull。
// upgrader 自身在失败时已写响应（400/426 等），调用方无需额外写。
func (h *wsHub) upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	if h.count() >= int64(config.CHAT_STATS_MAX_CONNS) {
		return nil, errHubFull
	}
	return h.upgrader.Upgrade(w, r, nil)
}

// closeOnceClose 幂等关闭 send channel + 底层 WS 连接
func (c *wsConn) closeOnceClose() {
	c.closeOnce.Do(func() {
		// 先 close channel 通知 writePump 退出；再 Close 底层 conn
		// 注意：send channel 可能从未被关闭（如果 readPump 因错误先退出），
		// closeOnce 保证这里只执行一次，避免 double-close panic
		_ = c.ws.Close()
	})
}

// chatStatsHubCountForTest 测试辅助：返回当前活跃连接数（导出方法以供 _test.go 调用）
func chatStatsHubCountForTest() int64 { return chatStatsHub.count() }
