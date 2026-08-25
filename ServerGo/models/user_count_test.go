package models

// v2.0.74 阶段AL：CountActiveUsers 单元测试。
// 覆盖：
//   - 空表返回 0；
//   - 含 1 条有效用户 → 返回 1；
//   - 1 条有效 + 1 条软删除 → 仍返回 1（软删除过滤生效）；
//   - database.DB == nil → 返回错误而非 panic。

import (
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
)

func TestCountActiveUsers_Empty(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	count, err := CountActiveUsers()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if count != 0 {
		t.Errorf("empty table count=%d want 0", count)
	}
}

func TestCountActiveUsers_WithRows(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 插入 1 条有效用户
	u1 := &TAgentHttpUserInfo{
		UserName:          "alice",
		Password:          "hash1",
		AnthropicEnabled:  true,
		OpenAIEnabled:     true,
		Status:            UserStatus_Enabled,
	}
	if err := database.DB.Table(AgentHttpUserInfoTableName).Create(u1).Error; err != nil {
		t.Fatalf("insert alice err=%v", err)
	}

	count, err := CountActiveUsers()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if count != 1 {
		t.Errorf("count=%d want 1", count)
	}
}

func TestCountActiveUsers_FiltersSoftDeleted(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	u1 := &TAgentHttpUserInfo{
		UserName:         "alive",
		Password:         "h1",
		AnthropicEnabled: true,
		Status:           UserStatus_Enabled,
	}
	u2 := &TAgentHttpUserInfo{
		UserName:         "deleted",
		Password:         "h2",
		AnthropicEnabled: true,
		Status:           UserStatus_Enabled,
	}
	if err := database.DB.Table(AgentHttpUserInfoTableName).Create(u1).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Table(AgentHttpUserInfoTableName).Create(u2).Error; err != nil {
		t.Fatal(err)
	}
	// 软删除 u2（GORM 自动写入 deleted_at）
	if err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("user_name = ?", "deleted").
		Delete(&TAgentHttpUserInfo{}).Error; err != nil {
		t.Fatal(err)
	}

	count, err := CountActiveUsers()
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if count != 1 {
		t.Errorf("count=%d want 1 (soft-deleted should be excluded)", count)
	}
}

func TestCountActiveUsers_DBNotInitialized(t *testing.T) {
	original := database.DB
	defer func() { database.DB = original }()
	database.DB = nil

	if _, err := CountActiveUsers(); err == nil {
		t.Fatal("expected error when database.DB is nil")
	}
}