package spider

import (
	"context"
	"github.com/lishimeng/LsmTokensServer/logger"
	models "github.com/lishimeng/LsmTokensServer/models"
	"sync"
	"time"
)

// ==================== 爬虫调度器 ====================

const (
	schedulerInterval  = 5 * time.Minute // 检查间隔
	maxConcurrentCrawl = 3               // 最大并发爬取数
)

// SpiderScheduler 爬虫调度器
type SpiderScheduler struct {
	isRunning bool
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	semaphore chan struct{} // 并发控制信号量
}

var (
	spiderScheduler     *SpiderScheduler
	spiderSchedulerOnce sync.Once
)

// GetSpiderScheduler 获取单例调度器
func GetSpiderScheduler() *SpiderScheduler {
	spiderSchedulerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		spiderScheduler = &SpiderScheduler{
			ctx:       ctx,
			cancel:    cancel,
			semaphore: make(chan struct{}, maxConcurrentCrawl),
		}
	})
	return spiderScheduler
}

// Start 启动调度器
func (s *SpiderScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return
	}

	logger.Printf("[SPIDER] Starting scheduler...")
	s.isRunning = true

	go s.schedulerLoop()

	logger.Printf("[SPIDER] Scheduler started")
}

// Stop 停止调度器
func (s *SpiderScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return
	}

	logger.Printf("[SPIDER] Stopping scheduler...")
	s.cancel()
	s.isRunning = false
	logger.Printf("[SPIDER] Scheduler stopped")
}

// TriggerCrawlNow 手动触发某个数据源立即爬取
func (s *SpiderScheduler) TriggerCrawlNow(dataSourceID uint64) error {
	ds, ok := models.GetCachedSpiderDataSourceByID(dataSourceID)
	if !ok {
		var err error
		ds, err = models.GetSpiderDataSourceByID(dataSourceID)
		if err != nil {
			return err
		}
		if ds == nil {
			return nil
		}
	}

	logger.Printf("[SPIDER] TriggerCrawlNow: dataSourceID=%d is deprecated, use MCP interface instead", dataSourceID)
	return nil
}

// schedulerLoop 调度器主循环
func (s *SpiderScheduler) schedulerLoop() {
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	s.checkAndCrawl()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkAndCrawl()
		}
	}
}

// checkAndCrawl 检查并执行需要爬取的数据源
func (s *SpiderScheduler) checkAndCrawl() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	dataSources, err := models.ListEnabledSpiderDataSources()
	if err != nil {
		logger.Printf("[SPIDER] Failed to list data sources: %v", err)
		return
	}

	if len(dataSources) == 0 {
		return
	}

	logger.Printf("[SPIDER] Checked %d data sources (scheduling deprecated, use MCP interface)", len(dataSources))
}
