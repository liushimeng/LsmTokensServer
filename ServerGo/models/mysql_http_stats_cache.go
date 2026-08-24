package models

import (
	"fmt"
	"sync"
	"time"
)

// StatsCacheEntry 统计缓存条目
type StatsCacheEntry struct {
	Data     interface{}
	CachedAt time.Time
	Key      string
}

// IsExpired 检查缓存是否过期（默认TTL 5分钟）
func (e *StatsCacheEntry) IsExpired(ttl time.Duration) bool {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return time.Since(e.CachedAt) > ttl
}

// StatsCache 统计查询内存缓存
// 为 /ChatAnalysisTotal 等页面的聚合查询提供缓存，避免每次刷新都直接查询MySQL分表
type StatsCache struct {
	entries map[string]*StatsCacheEntry
	mu      sync.RWMutex
}

var statsCache StatsCache

// statsCacheTTL 统计缓存默认TTL
const statsCacheTTL = 5 * time.Minute

// initStatsCache 初始化统计缓存
func InitStatsCache() {
	statsCache.mu.Lock()
	defer statsCache.mu.Unlock()
	statsCache.entries = make(map[string]*StatsCacheEntry)
}

// makeStatsCacheKey 生成缓存键
// 格式: "funcName:userName:modelName:subTableNum:days:extra"
func makeStatsCacheKey(funcName, userName, modelName string, subTableNum, days int, extra string) string {
	if extra != "" {
		return fmt.Sprintf("%s:%s:%s:%d:%d:%s", funcName, userName, modelName, subTableNum, days, extra)
	}
	return fmt.Sprintf("%s:%s:%s:%d:%d", funcName, userName, modelName, subTableNum, days)
}

// getStatsFromCache 从缓存获取统计结果
func getStatsFromCache(key string) (interface{}, bool) {
	statsCache.mu.RLock()
	defer statsCache.mu.RUnlock()

	if statsCache.entries == nil {
		return nil, false
	}

	entry, ok := statsCache.entries[key]
	if !ok || entry == nil {
		return nil, false
	}

	if entry.IsExpired(statsCacheTTL) {
		return nil, false
	}

	return entry.Data, true
}

// setStatsToCache 将统计结果写入缓存
func setStatsToCache(key string, data interface{}) {
	statsCache.mu.Lock()
	defer statsCache.mu.Unlock()

	if statsCache.entries == nil {
		statsCache.entries = make(map[string]*StatsCacheEntry)
	}

	statsCache.entries[key] = &StatsCacheEntry{
		Data:     data,
		CachedAt: time.Now(),
		Key:      key,
	}
}

// invalidateStatsCache 使指定键的缓存失效
func invalidateStatsCache(key string) {
	statsCache.mu.Lock()
	defer statsCache.mu.Unlock()

	if statsCache.entries == nil {
		return
	}

	delete(statsCache.entries, key)
}

// invalidateStatsCacheByPrefix 按前缀使缓存失效（用于数据写入后批量清除相关缓存）
func invalidateStatsCacheByPrefix(prefix string) {
	statsCache.mu.Lock()
	defer statsCache.mu.Unlock()

	if statsCache.entries == nil {
		return
	}

	for key := range statsCache.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(statsCache.entries, key)
		}
	}
}

// invalidateStatsCacheByUserModel 当某用户+模型的数据发生变化时，清除该用户+模型的所有统计缓存
func invalidateStatsCacheByUserModel(userName, modelName string) {
	prefix := fmt.Sprintf(":%s:%s:", userName, modelName)
	statsCache.mu.Lock()
	defer statsCache.mu.Unlock()

	if statsCache.entries == nil {
		return
	}

	for key := range statsCache.entries {
		// 检查键中是否包含该用户和模型
		if containsSubstring(key, prefix) {
			delete(statsCache.entries, key)
		}
	}
}

// containsSubstring 检查字符串是否包含子串（简单实现）
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// getStatsCacheStats 获取缓存统计信息（用于调试）
func getStatsCacheStats() (total int, expired int) {
	statsCache.mu.RLock()
	defer statsCache.mu.RUnlock()

	if statsCache.entries == nil {
		return 0, 0
	}

	total = len(statsCache.entries)
	now := time.Now()
	for _, entry := range statsCache.entries {
		if now.Sub(entry.CachedAt) > statsCacheTTL {
			expired++
		}
	}
	return total, expired
}

// cleanExpiredStatsCache 清理过期缓存条目
func cleanExpiredStatsCache() {
	statsCache.mu.Lock()
	defer statsCache.mu.Unlock()

	if statsCache.entries == nil {
		return
	}

	now := time.Now()
	for key, entry := range statsCache.entries {
		if now.Sub(entry.CachedAt) > statsCacheTTL {
			delete(statsCache.entries, key)
		}
	}
}
