package webserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// 阶段T 双构建隔离：clientWebDist 必须按角色定位 dist-manager / dist-user，
// 且不回落到共享 dist（两端产物严禁互取）。
func TestClientWebDistRoleIsolation(t *testing.T) {
	tmp := t.TempDir()
	clientWeb := filepath.Join(tmp, "ClientWeb")
	for _, role := range []string{"manager", "user"} {
		if err := os.MkdirAll(filepath.Join(clientWeb, "dist-"+role), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 旧共享目录存在也不得被任何角色选中
	if err := os.MkdirAll(filepath.Join(clientWeb, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDir := config.SetConfigDirForTest(tmp)
	defer config.SetConfigDirForTest(oldDir)

	cfg := &config.LsmTokensServerConfig{}
	for _, role := range []string{"manager", "user"} {
		got, err := clientWebDist(cfg, role)
		if err != nil {
			t.Fatalf("role %s: %v", role, err)
		}
		if want := filepath.Join(clientWeb, "dist-"+role); got != want {
			t.Fatalf("role %s: got %s, want %s", role, got, want)
		}
		if strings.HasSuffix(got, string(filepath.Separator)+"dist") {
			t.Fatalf("role %s: 不得回落共享 dist 目录: %s", role, got)
		}
	}

	// 配置覆盖优先
	cfg.ManagerWebStaticDir = filepath.Join(tmp, "custom-manager-dist")
	if err := os.MkdirAll(cfg.ManagerWebStaticDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := clientWebDist(cfg, "manager")
	if err != nil || got != cfg.ManagerWebStaticDir {
		t.Fatalf("override: got %s err=%v, want %s", got, err, cfg.ManagerWebStaticDir)
	}
}

// 角色目录缺失时报错（API-only 模式），不得静默改用另一角色目录。
func TestClientWebDistMissingRoleDir(t *testing.T) {
	tmp := t.TempDir()
	clientWeb := filepath.Join(tmp, "ClientWeb")
	if err := os.MkdirAll(filepath.Join(clientWeb, "dist-manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDir := config.SetConfigDirForTest(tmp)
	defer config.SetConfigDirForTest(oldDir)

	cfg := &config.LsmTokensServerConfig{}
	if _, err := clientWebDist(cfg, "user"); err == nil {
		t.Fatal("dist-user 缺失时应返回错误")
	}
}
