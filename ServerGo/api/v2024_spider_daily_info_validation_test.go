package api

// ==================== v2.0.24 MCP /InputSpiderDailyInfo 空记录防护测试（api 侧） ====================
// 从旧 v2024_spider_daily_info_validation_test.go 拆出依赖 api 包未导出 handler 的部分：
//   - handleSpiderDailyInfoDelete 管理员跳过权限预检
//   - handleSpiderDailyInfoBatchDelete 显式返回 skipped_not_found / skipped_no_permission

import (
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
)

// ---------- handleSpiderDailyInfoDelete 管理员跳过权限预检 ----------

func TestHandleSpiderDailyInfoDelete_AdminBypassesPermissionCheck(t *testing.T) {
	// 关键不变量：管理员路径不应调用 GetSpiderDailyInfoByID 也不应调用
	// GetSpiderDataSourceByID。直接验证 isAdmin=true 分支不会回退到 GetByID
	// 失败时的 "Info not found" 错误信息。
	//
	// 这里只能验证逻辑分支：构造一个不存在的 ID，管理员应该直接返回 success
	// （GORM Unscoped Delete 不报错），非管理员应回 "Info not found"。
	// 由于依赖真实 database.DB，本测试在无 database.DB 环境 skip。

	if database.DB == nil {
		t.Skip("跳过：database.DB 未初始化")
	}

	t.Run("admin-deletes-nonexistent-id-still-succeeds", func(t *testing.T) {
		req := &SpiderAPIRequest{ID: 999999999} // 不存在的 ID
		resp := handleSpiderDailyInfoDelete(req, 1, true)
		if !resp.Success {
			t.Errorf("admin delete should succeed for any id (including non-existent), got msg=%q", resp.Message)
		}
		if resp.Message == "Info not found" {
			t.Errorf("admin should NOT see 'Info not found' error message, got: %s", resp.Message)
		}
	})

	t.Run("non-admin-nonexistent-id-returns-not-found", func(t *testing.T) {
		req := &SpiderAPIRequest{ID: 999999999}
		resp := handleSpiderDailyInfoDelete(req, 1, false)
		if resp.Success {
			t.Errorf("non-admin deleting non-existent id should fail, got success")
		}
		if !strings.Contains(resp.Message, "Info not found") {
			t.Errorf("non-admin should get 'Info not found', got: %s", resp.Message)
		}
	})
}

// ---------- handleSpiderDailyInfoBatchDelete 显式计数 ----------

func TestHandleSpiderDailyInfoBatchDelete_RejectsEmptyItems(t *testing.T) {
	req := &SpiderAPIRequest{Items: nil}
	resp := handleSpiderDailyInfoBatchDelete(req, 1, true)
	if resp.Success {
		t.Errorf("empty items should fail, got success")
	}
	if !strings.Contains(resp.Message, "No items to delete") {
		t.Errorf("message = %q, want No items to delete", resp.Message)
	}
}

func TestHandleSpiderDailyInfoBatchDelete_RejectsInvalidItemIDs(t *testing.T) {
	if database.DB == nil {
		t.Skip("跳过：database.DB 未初始化")
	}
	// items 里全是 ID=0 的无效项，应全部计入 skipped_not_found
	req := &SpiderAPIRequest{Items: []SpiderDailyInfoItem{
		{ID: 0, CrawlTime: "2026-07-02T10:00:00"},
		{ID: 0, CrawlTime: "2026-07-02T10:00:01"},
	}}
	resp := handleSpiderDailyInfoBatchDelete(req, 1, true)
	if !resp.Success {
		t.Errorf("batch with all-zero IDs should still return success (zero deleted), got: %s", resp.Message)
	}
	if resp.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", resp.Deleted)
	}
	if resp.SkippedNotFound != 2 {
		t.Errorf("SkippedNotFound = %d, want 2", resp.SkippedNotFound)
	}
	if resp.SkippedNoPermission != 0 {
		t.Errorf("SkippedNoPermission = %d, want 0", resp.SkippedNoPermission)
	}
}

func TestHandleSpiderDailyInfoBatchDelete_MessageContainsBreakdown(t *testing.T) {
	if database.DB == nil {
		t.Skip("跳过：database.DB 未初始化")
	}
	req := &SpiderAPIRequest{Items: []SpiderDailyInfoItem{
		{ID: 0},
		{ID: 999999999}, // 不存在
	}}
	resp := handleSpiderDailyInfoBatchDelete(req, 1, true)
	// 期望 message 含 3 类计数
	if !strings.Contains(resp.Message, "deleted") ||
		!strings.Contains(resp.Message, "not found") ||
		!strings.Contains(resp.Message, "no permission") {
		t.Errorf("message should contain breakdown, got: %s", resp.Message)
	}
}
