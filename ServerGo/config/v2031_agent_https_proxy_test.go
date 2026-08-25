package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// v2.0.31: AI 代理 HTTPS 监听端口（agentHttpsListenPort，默认 29003）单测（config 侧）。
//
// 覆盖：
//  1. 默认配置 / 配置校验（0 / 越界 / 合法值）
//  2. WriteConfig ↔ LoadConfig 往返保真（含「旧配置无该字段 → 默认 29003」回归）

func TestDefaultConfig_AgentHttpsListenPort(t *testing.T) {
	cfg := getDefaultConfig()
	if cfg.AgentHttpsListenPort != DEFAULT_AGENT_HTTPS_LISTEN_PORT {
		t.Fatalf("default AgentHttpsListenPort = %d, want %d", cfg.AgentHttpsListenPort, DEFAULT_AGENT_HTTPS_LISTEN_PORT)
	}
	if cfg.AgentHttpsListenPort != 29003 {
		t.Fatalf("default AgentHttpsListenPort = %d, want 29003", cfg.AgentHttpsListenPort)
	}
	// 默认证书字段与 UserWeb 共用（v2.0.31 复用 userWebCertFile / userWebKeyFile）
	if cfg.UserWebCertFile == "" || cfg.UserWebKeyFile == "" {
		t.Fatalf("default cert/key must be non-empty: cert=%q key=%q", cfg.UserWebCertFile, cfg.UserWebKeyFile)
	}
}

func TestValidateAndFixConfig_AgentHttpsListenPort(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero_uses_default", 0, DEFAULT_AGENT_HTTPS_LISTEN_PORT},
		{"negative_uses_default", -1, DEFAULT_AGENT_HTTPS_LISTEN_PORT},
		{"too_large_uses_default", 70000, DEFAULT_AGENT_HTTPS_LISTEN_PORT},
		{"valid_preserved", 29005, 29005},
		{"valid_one_preserved", 1, 1},
		{"max_valid_preserved", 65535, 65535},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := getDefaultConfig()
			cfg.AgentHttpsListenPort = c.in
			// AgentListenPort 与 AgentHttpsListenPort 取不同值，避免下游启动阶段判定冲突
			cfg.AgentListenPort = 29000
			_ = validateAndFixConfig(cfg)
			if cfg.AgentHttpsListenPort != c.want {
				t.Fatalf("input %d -> AgentHttpsListenPort = %d, want %d", c.in, cfg.AgentHttpsListenPort, c.want)
			}
		})
	}
}

func TestLoadConfig_AgentHttpsListenPortRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LsmTokensServer.conf")

	// 1) 旧配置（无 agentHttpsListenPort 字段）→ 加载后应为默认 29003
	legacy := map[string]interface{}{
		"agentListenPort":         29000,
		"agentAnthropicListenURL": "Anthropic",
		"agentOpenAIListenURL":    "OpenAI",
	}
	raw, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig legacy: %v", err)
	}
	if cfg.AgentHttpsListenPort != DEFAULT_AGENT_HTTPS_LISTEN_PORT {
		t.Fatalf("legacy config AgentHttpsListenPort = %d, want default %d", cfg.AgentHttpsListenPort, DEFAULT_AGENT_HTTPS_LISTEN_PORT)
	}

	// 2) 显式 29005 → 往返保真
	explicit := getDefaultConfig()
	explicit.AgentHttpsListenPort = 29005
	if err := WriteConfig(path, explicit); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	cfg2, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig explicit: %v", err)
	}
	if cfg2.AgentHttpsListenPort != 29005 {
		t.Fatalf("explicit round-trip AgentHttpsListenPort = %d, want 29005", cfg2.AgentHttpsListenPort)
	}
}
