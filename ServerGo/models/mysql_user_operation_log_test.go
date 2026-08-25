package models

import (
	"testing"
)

// ============================================================================
// 用户操作日志表 TAgentUserOperationLog 单元测试
// 注意：涉及数据库操作的函数（Init/Add/Query/Cleanup）需要数据库连接，
// 此处测试纯逻辑部分（常量定义、结构体字段等）。
// ============================================================================

// TestUserOperationLogTableConstants 验证表名和容量常量正确
func TestUserOperationLogTableConstants(t *testing.T) {
	if UserOperationLogTableName != "TAgentUserOperationLog" {
		t.Fatalf("unexpected table name: %s", UserOperationLogTableName)
	}
	if userOpLogMaxRecords != 100000 {
		t.Fatalf("unexpected max records: %d", userOpLogMaxRecords)
	}
	if userOpLogCleanupBatch != 10000 {
		t.Fatalf("unexpected cleanup batch: %d", userOpLogCleanupBatch)
	}
	if userOpLogCheckInterval != 100 {
		t.Fatalf("unexpected check interval: %d", userOpLogCheckInterval)
	}
}

// TestUserOperationLogStructFields 验证结构体字段类型正确
func TestUserOperationLogStructFields(t *testing.T) {
	record := TAgentUserOperationLog{
		ActionType: "LOGIN",
		UserName:   "testuser",
		Details:    "密码登录 IP=127.0.0.1",
	}
	if record.ActionType != "LOGIN" {
		t.Fatalf("unexpected action type: %s", record.ActionType)
	}
	if record.UserName != "testuser" {
		t.Fatalf("unexpected user name: %s", record.UserName)
	}
	if record.Details != "密码登录 IP=127.0.0.1" {
		t.Fatalf("unexpected details: %s", record.Details)
	}
}

// TestAddUserOperationLogWithoutDB 验证数据库未初始化时写入不崩溃
func TestAddUserOperationLogWithoutDB(t *testing.T) {
	// database.DB 为 nil 时应该静默返回，不 panic
	AddUserOperationLog("LOGIN", "testuser", "test details")
}

// TestQueryUserOperationLogsWithoutDB 验证数据库未初始化时查询返回错误
func TestQueryUserOperationLogsWithoutDB(t *testing.T) {
	_, _, err := QueryUserOperationLogs(1, 20, "", "", "")
	if err == nil {
		t.Fatal("expected error when database is nil")
	}
}

// TestCleanupOldOperationLogsWithoutDB 验证数据库未初始化时清理不崩溃
func TestCleanupOldOperationLogsWithoutDB(t *testing.T) {
	// database.DB 为 nil 时应该静默返回，不 panic
	CleanupOldOperationLogs()
}
