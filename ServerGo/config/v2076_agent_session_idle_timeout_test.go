package config

import "testing"

// TestValidateAgentSessionIdleTimeout v2.0.76 阶段BD：合成 Session 空闲超时校验
func TestValidateAgentSessionIdleTimeout(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"default 15 valid", 15, 15},
		{"min boundary 1", 1, 1},
		{"max boundary 1440", 1440, 1440},
		{"zero invalid → default", 0, 15},
		{"negative invalid → default", -10, 15},
		{"too large → default", 1441, 15},
		{"way too large → default", 100000, 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidateAgentSessionIdleTimeout(tc.in); got != tc.want {
				t.Errorf("ValidateAgentSessionIdleTimeout(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestDefaultConfigContainsAgentSessionIdleTimeout 默认配置含新字段且为 15
func TestDefaultConfigContainsAgentSessionIdleTimeout(t *testing.T) {
	cfg := GetDefaultConfig()
	if cfg.AgentSessionIdleTimeoutMinutes != DEFAULT_AGENT_SESSION_IDLE_TIMEOUT_MINUTES {
		t.Errorf("default AgentSessionIdleTimeoutMinutes = %d, want %d",
			cfg.AgentSessionIdleTimeoutMinutes, DEFAULT_AGENT_SESSION_IDLE_TIMEOUT_MINUTES)
	}
	if DEFAULT_AGENT_SESSION_IDLE_TIMEOUT_MINUTES != 15 {
		t.Errorf("DEFAULT_AGENT_SESSION_IDLE_TIMEOUT_MINUTES = %d, want 15", DEFAULT_AGENT_SESSION_IDLE_TIMEOUT_MINUTES)
	}
}
