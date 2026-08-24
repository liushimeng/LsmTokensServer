package spider

// ==================== v2.0.9 代理池单例 + 会话级代理绑定 ====================
//
// 提供：
//   - GetProxyPool() 单例 ProxyPool（首次调用时从 config.G 初始化）
//   - BindProxyForSession(session, dsID) session 首次调用时绑定代理；
//     后续调用若 config.G.SpiderProxyBindPerSession=true 则重新分配（健康代理切换）

import (
	"github.com/lishimeng/LsmTokensServer/config"
	"sync"
	"time"
)

var (
	proxyPoolOnce sync.Once
	proxyPool     *ProxyPool
)

// GetProxyPool 返回单例 ProxyPool（首次调用时从 config.G 加载）
// config.G==nil 或 SpiderProxyPool 为空时返回 nil（调用方需处理 nil）
func GetProxyPool() *ProxyPool {
	proxyPoolOnce.Do(func() {
		if config.G == nil {
			proxyPool = &ProxyPool{items: nil}
			return
		}
		proxyPool = LoadProxyPool(config.G.SpiderProxyPool)
		// 同时填充 perSource 信息（虽然 Next() 不消费，但 HealthSnapshot 可观测）
		for dsID, url := range config.G.SpiderPerSourceProxy {
			if !tryProxyScheme(url) {
				continue
			}
			proxyPool.items = append(proxyPool.items, ProxyDescriptor{URL: url, DataSrcIDs: []int{dsID}})
		}
	})
	return proxyPool
}

// BindProxyForSession 为 session 绑定代理
//   - 首次调用：根据 dsID 优先级（perSource[dsID] > pool.Next()）选择代理并写入 session.boundProxy
//   - 后续调用：若 config.G.SpiderProxyBindPerSession==true 且 pool 非空，分配新代理（用于 anti-bot 重试时切换）
//   - 返回绑定后的代理 URL（空字符串表示无代理）
func BindProxyForSession(session *SpiderSession, dsID uint64) string {
	if session == nil {
		return ""
	}
	session.BoundProxyMu.Lock()
	defer session.BoundProxyMu.Unlock()

	// 首次绑定
	if session.BoundProxy == "" {
		proxy := ResolveProxyForDataSource(GetProxyPool(), config.G.SpiderPerSourceProxy, dsID)
		session.BoundProxy = proxy
		session.BoundProxyAt = time.Now()
		return proxy
	}

	// 后续：仅当允许重新绑定时返回下一个代理（用于 anti-bot 切换）
	if config.G == nil || !config.G.SpiderProxyBindPerSession {
		return session.BoundProxy
	}
	pool := GetProxyPool()
	if pool == nil || len(pool.items) == 0 {
		return session.BoundProxy
	}
	// 选择一个不等于当前绑定的代理
	newProxy := pool.Next()
	if newProxy == "" {
		return session.BoundProxy
	}
	// 避免和当前绑定相同（万一池中只有一个）
	if newProxy == session.BoundProxy {
		// 再取一次
		second := pool.Next()
		if second != "" && second != session.BoundProxy {
			newProxy = second
		}
	}
	session.BoundProxy = newProxy
	session.BoundProxyAt = time.Now()
	return newProxy
}
