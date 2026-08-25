package config

// v2.0.74 阶段AL：首次运行配置自动回写 + 超级管理员禁用机制 单元测试。
// 覆盖：
//   - RandomString / RandomUsername / RandomJWTSecret 字符集与长度；
//   - IsManagerDisabled 大小写不敏感 + 前后空格容忍；
//   - EnsureDefaultConfig 在文件不存在时生成、存在时不覆盖；
//   - DisableSuperAdmin 写入 disable 字符串 + managerWebAuthDisabled=true。

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRandomString_LengthAndCharset(t *testing.T) {
	for _, n := range []int{8, 16, 32, 64} {
		s, err := RandomString(n)
		if err != nil {
			t.Fatalf("RandomString(%d) err=%v", n, err)
		}
		if len(s) != n {
			t.Fatalf("RandomString(%d) len=%d", n, len(s))
		}
		for i, r := range s {
			if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
				t.Fatalf("RandomString(%d) char[%d]=%q not base62", n, i, r)
			}
		}
	}
}

func TestRandomString_RejectZeroLength(t *testing.T) {
	if _, err := RandomString(0); err == nil {
		t.Fatal("expected error for length=0")
	}
	if _, err := RandomString(-3); err == nil {
		t.Fatal("expected error for negative length")
	}
}

func TestRandomUsername(t *testing.T) {
	for i := 0; i < 16; i++ {
		u, err := RandomUsername()
		if err != nil {
			t.Fatalf("RandomUsername err=%v", err)
		}
		if !strings.HasPrefix(u, "adm-") {
			t.Fatalf("RandomUsername=%q missing adm- prefix", u)
		}
		// 去掉前缀 8 位无歧义字符
		body := strings.TrimPrefix(u, "adm-")
		if len(body) != 8 {
			t.Fatalf("RandomUsername=%q body len=%d want 8", u, len(body))
		}
		for _, r := range body {
			// 不允许 0/O/1/I/l
			if r == '0' || r == 'O' || r == '1' || r == 'I' || r == 'l' {
				t.Fatalf("RandomUsername=%q has ambiguous char %q", u, r)
			}
		}
	}
}

func TestRandomJWTSecret(t *testing.T) {
	s, err := RandomJWTSecret()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 decode err=%v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded len=%d want 32", len(decoded))
	}
}

func TestIsManagerDisabled(t *testing.T) {
	cases := []struct {
		name     string
		sec      SecurityConfig
		disabled bool
	}{
		{"both empty", SecurityConfig{}, false},
		{"normal creds", SecurityConfig{ManagerUserName: "admin", ManagerPassword: "secret"}, false},
		{"username disable", SecurityConfig{ManagerUserName: "disable", ManagerPassword: "secret"}, true},
		{"password disable", SecurityConfig{ManagerUserName: "admin", ManagerPassword: "DISABLE"}, true},
		{"both disable with whitespace", SecurityConfig{ManagerUserName: " disable ", ManagerPassword: "DISABLE"}, true},
	}
	for _, tc := range cases {
		if got := tc.sec.IsManagerDisabled(); got != tc.disabled {
			t.Errorf("%s: got=%v want=%v", tc.name, got, tc.disabled)
		}
	}
}

func TestEnsureDefaultConfig_GeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LsmTokensServer.conf")

	cfg, isFirst, err := EnsureDefaultConfig(path)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !isFirst {
		t.Fatal("isFirst should be true for missing file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if !strings.HasPrefix(cfg.Security.ManagerUserName, "adm-") {
		t.Errorf("username=%q should start with adm-", cfg.Security.ManagerUserName)
	}
	if len(cfg.Security.ManagerPassword) < 16 {
		t.Errorf("password too short: %d", len(cfg.Security.ManagerPassword))
	}
	if len(cfg.Security.JWTSecret) == 0 {
		t.Error("jwtSecret empty")
	}

	// 验证文件权限（仅 Linux 生效；macOS 默认 0644 也通过）
	info, _ := os.Stat(path)
	if info != nil && info.Mode().Perm()&0077 != 0 {
		t.Errorf("conf permissions %v include group/other bits", info.Mode().Perm())
	}
}

func TestEnsureDefaultConfig_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LsmTokensServer.conf")

	// 第一次写入
	first, _, err := EnsureDefaultConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	// 第二次调用：文件已存在，**不**重新生成
	second, isFirst, err := EnsureDefaultConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if isFirst {
		t.Fatal("isFirst should be false on second call")
	}
	if second.Security.ManagerUserName != first.Security.ManagerUserName {
		t.Errorf("username should be preserved, got %q vs %q",
			second.Security.ManagerUserName, first.Security.ManagerUserName)
	}
	if second.Security.ManagerPassword != first.Security.ManagerPassword {
		t.Error("password should be preserved on subsequent calls")
	}
}

func TestDisableSuperAdmin(t *testing.T) {
	cfg := getDefaultConfig()
	cfg.Security.ManagerUserName = "admin"
	cfg.Security.ManagerPassword = "secret1234567890"
	cfg.Security.ManagerWebAuthDisabled = false

	if !DisableSuperAdmin(cfg) {
		t.Error("expected change=true on first call")
	}
	if !cfg.Security.IsManagerDisabled() {
		t.Errorf("IsManagerDisabled should be true after DisableSuperAdmin: %+v", cfg.Security)
	}
	if !cfg.Security.ManagerWebAuthDisabled {
		t.Error("ManagerWebAuthDisabled should be true after DisableSuperAdmin")
	}

	// 第二次调用：已处于禁用态，应返回 false（幂等）
	if DisableSuperAdmin(cfg) {
		t.Error("expected change=false when already disabled (idempotent)")
	}
}

func TestDisableSuperAdmin_NilSafe(t *testing.T) {
	if DisableSuperAdmin(nil) {
		t.Error("DisableSuperAdmin(nil) should return false")
	}
}