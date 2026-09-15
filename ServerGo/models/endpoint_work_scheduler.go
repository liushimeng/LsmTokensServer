package models

import (
	"context"
	"sync"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
)

// endpointWorkScheduler 源站工作时间定时调度器。
// 每分钟遍历缓存中的所有源站，根据 WorkPeriods 计算当前是否在工作时间内，
// 若 WorkStatus 与计算结果不一致则更新 DB + 缓存。
//
// 设计原则：
//   - 全天工作（默认）的源站：WorkStatus 恒为 1，不干预，status 由用户/管理员决定。
//   - 非全天工作的源站：工作时间内 WorkStatus=1，非工作时间 WorkStatus=0。
//   - 代理热路径 / 经济型算法通过判断 Status==1 && WorkStatus==1 决定可用性。
//   - 用户手动禁用（Status=0）优先：即使在工作时间内，Status=0 的源站也不可用。
var (
	endpointWorkScheduler struct {
		ctx    context.Context
		cancel context.CancelFunc
		mu     sync.Mutex
		// 用于测试注入
		tickInterval time.Duration
	}
)

// StartEndpointWorkScheduler 启动源站工作时间后台调度器。
// interval 为轮询间隔，建议 1 分钟；ctx 用于优雅退出。
func StartEndpointWorkScheduler(ctx context.Context, interval time.Duration) {
	endpointWorkScheduler.mu.Lock()
	defer endpointWorkScheduler.mu.Unlock()

	if endpointWorkScheduler.ctx != nil {
		logger.Printf("[WORK_SCHEDULER] 调度器已在运行，忽略重复启动")
		return
	}
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	endpointWorkScheduler.tickInterval = interval
	endpointWorkScheduler.ctx, endpointWorkScheduler.cancel = context.WithCancel(ctx)

	go endpointWorkSchedulerLoop()
	logger.Printf("[WORK_SCHEDULER] 源站工作时间调度器已启动，轮询间隔 %v", interval)
}

// StopEndpointWorkScheduler 停止后台调度器。
func StopEndpointWorkScheduler() {
	endpointWorkScheduler.mu.Lock()
	defer endpointWorkScheduler.mu.Unlock()

	if endpointWorkScheduler.cancel != nil {
		endpointWorkScheduler.cancel()
		endpointWorkScheduler.cancel = nil
		endpointWorkScheduler.ctx = nil
		logger.Printf("[WORK_SCHEDULER] 源站工作时间调度器已停止")
	}
}

// endpointWorkSchedulerLoop 调度器主循环：立即执行一次，然后按 ticker 轮询。
func endpointWorkSchedulerLoop() {
	// 启动时立即同步一次
	syncAllEndpointWorkStatus()

	interval := endpointWorkScheduler.tickInterval
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-endpointWorkScheduler.ctx.Done():
			logger.Printf("[WORK_SCHEDULER] 调度器退出")
			return
		case <-ticker.C:
			syncAllEndpointWorkStatus()
		}
	}
}

// syncAllEndpointWorkStatus 遍历所有缓存源站，同步 WorkStatus。
// 规则：
//   - 全天工作：WorkStatus 恒为 1。
//   - 非全天：当前时间在任一工作时间段内 → WorkStatus=1，否则 0。
func syncAllEndpointWorkStatus() {
	now := time.Now()
	endpoints := GetAllCachedDstEndPoints()
	if len(endpoints) == 0 {
		return
	}
	var changed int
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		shouldEnable, allDay := ShouldBeEnabledByWorkPeriods(ep.WorkPeriods, now)
		newStatus := 0
		if shouldEnable {
			newStatus = 1
		}
		if allDay && ep.WorkStatus != 1 {
			// 全天工作源站，确保 WorkStatus=1（兜底修复异常值）
			updateEndpointWorkStatus(ep.ID, 1)
			changed++
			continue
		}
		if !allDay && ep.WorkStatus != newStatus {
			updateEndpointWorkStatus(ep.ID, newStatus)
			changed++
		}
	}
	if changed > 0 {
		logger.Printf("[WORK_SCHEDULER] 同步源站工作时间状态：共 %d 个源站，%d 个状态变更", len(endpoints), changed)
	}
}

// updateEndpointWorkStatus 更新单个源站的 WorkStatus（DB + 缓存）。
// 注意：仅更新 work_status，不触碰 status（用户启停）。
func updateEndpointWorkStatus(id uint64, workStatus int) {
	if database.DB == nil {
		return
	}
	// 更新 DB
	result := database.DB.Table(AgentDstEndPointTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		Update("work_status", workStatus)
	if result.Error != nil {
		logger.Printf("[WARNING] 更新源站 %d work_status 失败: %v", id, result.Error)
		return
	}
	// 更新缓存（读-改-写，利用 cache 的 endpoint 对象是指针副本）
	if ep, ok := GetCachedDstEndPointByID(id); ok {
		ep.WorkStatus = workStatus
	}
}
