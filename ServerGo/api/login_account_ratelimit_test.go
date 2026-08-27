package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// 阶段AP：账号维度登录防爆破单元测试（自动化测试报告 OBS-4）
// 验证三个层面：
//  1. IP 维度锁定行为不回归（同 IP 连续失败 3 次锁 10 分钟）
//  2. 账号维度锁定可跨 IP 生效（XFF 轮换绕过 IP 锁定时，同账号仍被锁）
//  3. loginAttempts 惰性清理（map 膨胀超阈值时清除过期条目）

// resetLoginAttempts 测试间隔离：清空全局登录失败记录
func resetLoginAttempts(t *testing.T) {
	t.Helper()
	loginAttemptsMu.Lock()
	loginAttempts = make(map[string]*loginAttempt)
	loginAttemptsMu.Unlock()
	t.Cleanup(func() {
		loginAttemptsMu.Lock()
		loginAttempts = make(map[string]*loginAttempt)
		loginAttemptsMu.Unlock()
	})
}

func TestLoginAttemptAccountKey(t *testing.T) {
	cases := []struct {
		loginType, userName, modelName, want string
	}{
		{"user", "liusm191", "", "user:liusm191"},
		{"user", "", "", ""}, // 空用户名不参与账号维度锁定
		{"model", "", "gpt-x", "model:gpt-x"},
		{"model", "ignored", "gpt-x", "model:gpt-x"},
		{"model", "", "", ""},
		{"other", "a", "b", ""}, // 未知登录类型
	}
	for _, c := range cases {
		got := loginAttemptAccountKey(c.loginType, c.userName, c.modelName)
		if got != c.want {
			t.Fatalf("loginAttemptAccountKey(%q,%q,%q)=%q, want %q", c.loginType, c.userName, c.modelName, got, c.want)
		}
	}
}

func TestLoginAttemptIPLockoutNoRegression(t *testing.T) {
	resetLoginAttempts(t)

	ip := "10.1.1.100"
	for i := 0; i < maxLoginFailures; i++ {
		recordLoginFailure(ip)
	}
	if err := checkLoginAttempt(ip); err == nil {
		t.Fatal("IP 维度锁定应生效：连续失败 3 次后 checkLoginAttempt 应报错")
	}
	// 其他 IP 不受影响（喷洒场景仍可被单 IP 锁定，不误伤他人）
	if err := checkLoginAttempt("10.1.1.200"); err != nil {
		t.Fatalf("其他 IP 不应被连带锁定: %v", err)
	}
	// 成功后清除
	clearLoginAttempt(ip)
	if err := checkLoginAttempt(ip); err != nil {
		t.Fatalf("成功登录后应清除失败记录: %v", err)
	}
}

func TestLoginAttemptAccountLockoutAcrossIPs(t *testing.T) {
	resetLoginAttempts(t)

	account := "user:victim"
	// 攻击者轮换 IP（XFF 伪造）对同一账号连续撞库：每次失败换一个 IP，
	// IP 维度永远只累计 1 次，但账号维度应累计并最终锁定。
	ips := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}
	for i, ip := range ips {
		if err := checkLoginAttempt(ip); err != nil {
			t.Fatalf("第 %d 次尝试前 IP %s 不应被锁: %v", i+1, ip, err)
		}
		recordLoginFailure(ip)      // IP 维度（每次只记 1 次，不触发）
		recordLoginFailure(account) // 账号维度（累计）
	}

	// 第 4 个 IP 未被锁定，但账号维度已锁 → 换 IP 也进不来
	if err := checkLoginAttempt("5.5.5.5"); err != nil {
		t.Fatalf("新 IP 不应被 IP 维度锁定: %v", err)
	}
	err := checkLoginAttempt(account)
	if err == nil {
		t.Fatal("账号维度锁定应跨 IP 生效：同账号失败 3 次后应报错")
	}
	if !strings.Contains(err.Error(), "登录过于频繁") {
		t.Fatalf("锁定提示不符预期: %v", err)
	}

	// 成功路径清除账号维度记录
	clearLoginAttempt(account)
	if err := checkLoginAttempt(account); err != nil {
		t.Fatalf("成功后应清除账号维度失败记录: %v", err)
	}
}

func TestLoginAttemptLazyCleanupOnBloat(t *testing.T) {
	resetLoginAttempts(t)

	// 塞入超阈值的「已过期」条目（锁定已解除且失败窗口已过）
	loginAttemptsMu.Lock()
	stale := time.Now().Add(-2 * loginLockDuration)
	for i := 0; i < loginAttemptsCleanupThreshold+50; i++ {
		loginAttempts[fmt.Sprintf("stale-%d", i)] = &loginAttempt{
			failedCount:    1,
			lastFailedTime: stale,
			lockedUntil:    stale,
		}
	}
	loginAttemptsMu.Unlock()

	// 再记录一次失败触发惰性清理
	recordLoginFailure("cleanup-trigger-ip")

	loginAttemptsMu.Lock()
	size := len(loginAttempts)
	loginAttemptsMu.Unlock()
	if size != 1 {
		t.Fatalf("惰性清理未生效：期望仅保留触发条目 1 条，实际 %d 条", size)
	}
	// 触发条目本身仍在失败窗口内，须保留且未锁定（仅 1 次失败 < maxLoginFailures）
	if err := checkLoginAttempt("cleanup-trigger-ip"); err != nil {
		t.Fatalf("触发条目不应被锁定（仅 1 次失败）: %v", err)
	}
}
