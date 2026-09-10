package middleware

// 阶段BZ：长查询 ctx 工具函数。
// 把 context.WithTimeout 的使用集中在本文件，便于测试与替换（生产环境若需要
// 把 ctx 改为带 trace ID 的 wrapper，只改本文件即可）。

import (
	"context"
	"time"
)

// newTimeoutContext 返回带 deadline 的 context 与取消函数。
// 当 deadline<=0 时原样返回（与 query_class=QStream 配套使用）。
func newTimeoutContext(parent context.Context, deadline time.Duration) (context.Context, context.CancelFunc) {
	if deadline <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, deadline)
}