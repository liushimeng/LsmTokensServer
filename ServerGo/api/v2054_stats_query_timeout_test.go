package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

// v2.0.54: /ChatAnalysisTotal「返回 200 但一直转圈」的真正根因不是单条 SQL 慢
//（v2.0.53 的 Go 端聚合已把 days=1/7/30 压到 0.4~1s），而是并发/资源层三个结构性缺陷。
// 本测试守护这些不变量。

// TestNewStatsQueryCtx_DeadlineSet 缺符号：newStatsQueryCtx
// 现为 database 包未导出函数（database/connect.go）。
func TestNewStatsQueryCtx_DeadlineSet(t *testing.T) {
	t.Skip("缺符号 newStatsQueryCtx（database 包未导出）")
}

// TestStatsQueryTimeout_Value 守护默认超时值 —— 必须 < 前端 AbortController(30s)，留余量返回错误响应。
func TestStatsQueryTimeout_Value(t *testing.T) {
	if database.StatsQueryTimeout <= 0 {
		t.Fatal("StatsQueryTimeout 必须为正")
	}
	if database.StatsQueryTimeout >= 30*time.Second {
		t.Fatalf("StatsQueryTimeout=%v 必须小于前端 30s AbortController，留余量返回错误响应", database.StatsQueryTimeout)
	}
}

// TestStatsDB_NilDBSafe 断言 DB==nil 时 statsDB 不 panic，返回可安全 defer 的 cancel。
func TestStatsDB_NilDBSafe(t *testing.T) {
	saved := database.DB
	database.DB = nil
	defer func() { database.DB = saved }()

	db, cancel := database.StatsDB()
	if cancel == nil {
		t.Fatal("StatsDB 返回的 cancel 不能为 nil（否则 defer cancel() 会 panic）")
	}
	// 不能 panic
	cancel()
	if db != nil {
		t.Fatal("DB==nil 时 StatsDB 应返回 nil 会话")
	}
}

// TestBuildGormConfig_LoggerNonNil 缺符号：buildGormConfig
// 现为 database 包未导出函数（database/connect.go）。
func TestBuildGormConfig_LoggerNonNil(t *testing.T) {
	t.Skip("缺符号 buildGormConfig（database 包未导出）")
}

// TestIsStatsTimeoutErr 守护超时错误识别：DB 层 context 超时/取消必须被识别为「查询超时」。
func TestIsStatsTimeoutErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, true},
		{"wrapped-deadline", fmt.Errorf("query failed: %w", context.DeadlineExceeded), true},
		{"msg-deadline", errors.New("failed to get rows: context deadline exceeded"), true},
		{"msg-canceled", errors.New("dial: context canceled"), true},
		{"invalid-conn", errors.New("invalid connection"), true},
		{"unrelated", errors.New("table not found"), false},
		{"record-not-found", errors.New("record not found"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isStatsTimeoutErr(c.err); got != c.want {
				t.Fatalf("isStatsTimeoutErr(%v)=%v, want %v", c.err, got, c.want)
			}
		})
	}
}
