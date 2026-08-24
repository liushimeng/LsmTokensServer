package models

// ==================== v2.0.24 MCP /InputSpiderDailyInfo 空记录防护测试（models 侧） ====================
// 从旧 v2024_spider_daily_info_validation_test.go 拆出依赖 models 包函数的部分：
//   - IsEmptySpiderDailyInfo 单元测试
//   - SaveSpiderDailyInfo database.DB 层兜底拒绝空记录

import (
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

// ---------- IsEmptySpiderDailyInfo ----------

func TestIsEmptySpiderDailyInfo(t *testing.T) {
	cases := []struct {
		name string
		info *TSpiderDailyInfo
		want bool
	}{
		{"nil-pointer", nil, true},
		{"all-empty", &TSpiderDailyInfo{DataSourceID: 1}, true},
		{"only-whitespace", &TSpiderDailyInfo{DataSourceID: 1, Title: "   ", URL: "\t\n", Content: " "}, true},
		{"only-title", &TSpiderDailyInfo{DataSourceID: 1, Title: "hello"}, true},
		{"only-url", &TSpiderDailyInfo{DataSourceID: 1, URL: "https://example.com"}, true},
		{"only-content", &TSpiderDailyInfo{DataSourceID: 1, Content: "lorem ipsum"}, true},
		{"missing-title", &TSpiderDailyInfo{DataSourceID: 1, URL: "u", Content: "c"}, true},
		{"missing-url", &TSpiderDailyInfo{DataSourceID: 1, Title: "t", Content: "c"}, true},
		{"missing-content", &TSpiderDailyInfo{DataSourceID: 1, Title: "t", URL: "u"}, true},
		{"all-present", &TSpiderDailyInfo{DataSourceID: 1, Title: "t", URL: "u", Content: "c"}, false},
		{"all-present-trimmed", &TSpiderDailyInfo{DataSourceID: 1, Title: "  hello  ", URL: "  https://x  ", Content: "  body  "}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsEmptySpiderDailyInfo(c.info)
			if got != c.want {
				t.Errorf("IsEmptySpiderDailyInfo(%+v) = %v, want %v", c.info, got, c.want)
			}
		})
	}
}

// ---------- SaveSpiderDailyInfo database.DB 层兜底 ----------

func TestSaveSpiderDailyInfo_RejectsEmptyAtDBLayer(t *testing.T) {
	if database.DB == nil {
		t.Skip("跳过：database.DB 未初始化")
	}
	// 即使绕过 handler 直接调 database.DB 层，也必须拒绝
	err := SaveSpiderDailyInfo(&TSpiderDailyInfo{
		DataSourceID: 1,
		Title:        "",
		URL:          "",
		Content:      "",
	})
	if err == nil {
		t.Errorf("expected SaveSpiderDailyInfo to reject empty record, got nil error")
	}
	if err != nil && !strings.Contains(err.Error(), "拒绝保存空记录") {
		t.Errorf("error = %q, want to contain 拒绝保存空记录", err.Error())
	}

	// 部分为空也应该拒绝
	err = SaveSpiderDailyInfo(&TSpiderDailyInfo{
		DataSourceID: 1,
		Title:        "hello",
		URL:          "https://example.com",
		// Content 缺失
	})
	if err == nil {
		t.Errorf("expected SaveSpiderDailyInfo to reject record missing content, got nil error")
	}
}

// ---------- invalidateSpiderDailyInfoCache 写后失效 ----------

func TestInvalidateSpiderDailyInfoCacheByPrefix_ClearsAllEntries(t *testing.T) {
	// 直接测试缓存失效逻辑（不依赖 database.DB）
	// 1. 写入几条缓存
	now := time.Now()
	spiderDailyInfoCache.mu.Lock()
	spiderDailyInfoCache.entries = map[string]*SpiderDailyInfoCacheEntry{
		"spider_daily_info:1:true:1:20:::":  {Infos: nil, Total: 5, CachedAt: now},
		"spider_daily_info:1:false:1:20:::": {Infos: nil, Total: 3, CachedAt: now},
		"other_prefix:foo":                  {Infos: nil, Total: 1, CachedAt: now},
	}
	spiderDailyInfoCache.mu.Unlock()

	// 2. 按前缀失效
	InvalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")

	// 3. 校验只有 spider_daily_info:* 被清，其它前缀保留
	spiderDailyInfoCache.mu.RLock()
	defer spiderDailyInfoCache.mu.RUnlock()
	if _, ok := spiderDailyInfoCache.entries["spider_daily_info:1:true:1:20:::"]; ok {
		t.Errorf("expected spider_daily_info:1:true:1:20::: to be invalidated")
	}
	if _, ok := spiderDailyInfoCache.entries["spider_daily_info:1:false:1:20:::"]; ok {
		t.Errorf("expected spider_daily_info:1:false:1:20::: to be invalidated")
	}
	if _, ok := spiderDailyInfoCache.entries["other_prefix:foo"]; !ok {
		t.Errorf("other_prefix:foo should NOT be invalidated")
	}
}

func TestInvalidateSpiderDailyInfoCacheByPrefix_HandlesNilMap(t *testing.T) {
	// 边界：缓存 map 为 nil 时不能 panic
	spiderDailyInfoCache.mu.Lock()
	spiderDailyInfoCache.entries = nil
	spiderDailyInfoCache.mu.Unlock()

	// 不应 panic
	InvalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")
	invalidateSpiderDailyInfoCache("any-key")
}
