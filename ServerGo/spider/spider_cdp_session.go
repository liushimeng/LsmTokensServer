package spider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// ==================== SpiderSession CDP 扩展 ====================
// v2.0.0 重构：每个 session 拥有一个独立 Chrome tab（target），共享 rootCtx 的 browser。
// 通过 cdpTarget (targetID) 复用同一 tab，避免每次 action 都开新 tab 撑爆 sem。
//
// 资源管理：
//   - engine.sem（4 个槽）：每个 session 一个 tab 占一个槽
//   - 首次 attachCDPContext：开新 tab + acquire sem + 记 targetID
//   - 后续 attachCDPContext：用 chromedp.WithTargetID 复用同 tab，不 acquire 新 sem
//   - TTL 到期时 cleanupLoop 调 cdpCancel → 关 tab + 释放 sem（sync.Once 保护，幂等）

// attachCDPContext 懒分配 session 的 chromedp context
// 首次调用：开新 tab + acquire sem
// 后续调用：复用同 target（不 acquire 新 sem）
// 如果 cdpCtx 已死：清理 + 重新开新 tab
func attachCDPContext(s *SpiderSession, engine *SpiderEngine, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	// 已有有效 ctx 且有 targetID：复用
	if s.cdpCtx != nil && s.cdpTarget != "" {
		select {
		case <-s.cdpCtx.Done():
			// 旧的已死，清理
			_ = s.cdpCancel
			s.cdpCtx = nil
			s.cdpCancel = nil
			s.cdpTarget = ""
			// 落到下面重建
		default:
			// 还活着，复用：但需确认 chromedp.FromContext 仍能解析 target
			if target := chromedp.FromContext(s.cdpCtx); target != nil && target.Target != nil {
				return s.cdpCtx, s.cdpCancel, nil
			}
			// target 已失效，强制清理后重建
			if s.cdpCancel != nil {
				s.cdpCancel()
			}
			s.cdpCtx = nil
			s.cdpCancel = nil
			s.cdpTarget = ""
			// 落到下面重建
		}
	}

	if err := engine.waitReady(3 * time.Second); err != nil {
		return nil, nil, err
	}
	if err := engine.acquireSem(5 * time.Second); err != nil {
		return nil, nil, err
	}
	// P0: 确保无论后续发生什么（panic / error / 超时），sem 都会被释放
	semReleased := false
	releaseSemOnce := func() {
		if !semReleased {
			semReleased = true
			engine.releaseSem()
		}
	}
	defer releaseSemOnce()

	// 创建新 tab
	ctx, ctxCancel := context.WithTimeout(engine.rootCtx, timeout)
	cdpCtx, cancel := chromedp.NewContext(ctx)

	// 首次开 tab 时主动 navigate 到 session.CurrentURL，让 tab 反映当前 session 状态
	if s.CurrentURL != "" {
		if err := chromedp.Run(cdpCtx, chromedp.Navigate(s.CurrentURL)); err != nil {
			// navigate 失败：立即清理，释放 sem，避免资源泄漏
			cancel()
			ctxCancel()
			return nil, nil, fmt.Errorf("attachCDPContext navigate failed: %w", err)
		}
	}

	// 拿到 targetID 用于后续复用
	// chromedp 在第一次 chromedp.Run 后会设置 cdpCtx 内的 Target；通过 chromedp.FromContext 拿
	//
	// v2.0.22 防御（基于问题分析报告_20260701_060527 §3.2 #4）：在 DNS
	// 解析失败 / 浏览器半挂状态下，chromedp.FromContext 可能返回 target
	// 但 target.Target 内部字段未初始化（nil）；直接解引用
	// target.Target.TargetID 会在 goroutine 内触发
	// "runtime error: index out of range [-1]" panic，进而让外层
	// recover 把整个 handler 拉成
	// "spider goroutine panic: runtime error: index out of range [-1]"
	// 失败（详见报告 §1.2 第一次尝试的 message）。这里把嵌套解引用
	// 拆成 3 段 nil-check，任一为空就让 cdpTarget 留空，下面
	// detachCDPContext 重置路径会自然处理掉，调用方拿到 nil ctx 不影响
	// 错误分类（errType=dns_unresolved / cdp_dns_unresolved）。
	if target := chromedp.FromContext(cdpCtx); target != nil && target.Target != nil {
		s.cdpTarget = string(target.Target.TargetID)
	}

	var cdpCancelOnce sync.Once
	s.cdpCtx = cdpCtx
	s.cdpCancel = func() {
		cdpCancelOnce.Do(func() {
			cancel()
			ctxCancel()
			releaseSemOnce()
		})
	}
	return cdpCtx, s.cdpCancel, nil
}

// detachCDPContext 主动释放 session 的 CDP 资源
//
// v2.0.35（基于问题分析报告_20260709_174800 §4.3 / 推测的 Bug C / 修复优先级 P0 #3）：
// 原实现只 cancel chromedp context + 清字段，未主动调用 target.CloseTarget 关闭
// Chrome tab。cancel context 只是断开 chromedp 与目标 tab 的通信连接，Chrome
// 进程侧 target 仍在运行；多次 panic 后会累积到 10+ 个 Chrome 进程残留，
// 进而耗尽 CDP WebSocket 连接池，让整个 spider 僵死。
// 本版本在 cancel 之前先通过 CDP Target.closeTarget 主动通知 Chrome 关闭
// 当前 target（异步命令，失败不阻塞 detach 流程）。失败仅记日志，不返回
// error（detach 路径必须幂等）。
func detachCDPContext(s *SpiderSession) {
	if s == nil {
		return
	}
	// 先尝试关闭 Chrome target，释放服务端进程资源
	if s.cdpCtx != nil && s.cdpTarget != "" {
		if cdpCtxHolder := chromedp.FromContext(s.cdpCtx); cdpCtxHolder != nil && cdpCtxHolder.Target != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer closeCancel()
			closeDone := make(chan error, 1)
			go func() {
				closeDone <- target.CloseTarget(target.ID(s.cdpTarget)).Do(cdp.WithExecutor(closeCtx, cdpCtxHolder.Target))
			}()
			select {
			case err := <-closeDone:
				if err != nil {
					mcpLogMCP("[SPIDER] detachCDPContext CloseTarget failed (will still cancel ctx): session=%s err=%v", s.SessionID, err)
				}
			case <-closeCtx.Done():
				mcpLogMCP("[SPIDER] detachCDPContext CloseTarget timeout after 2s (will still cancel ctx): session=%s", s.SessionID)
			}
		}
	}
	if s.cdpCancel != nil {
		s.cdpCancel()
		s.cdpCancel = nil
	}
	s.cdpCtx = nil
	s.cdpTarget = ""
}
