package models

import "testing"

// ============================================================================
// v2.0.73: 高阶 Agent 白名单 / 合成 Session 白名单扩展测试
// （原 v2073_agent_detection_enhance_test.go 中依赖 models 包函数的部分，
//   因 models→recognizer 依赖方向，从 recognizer 测试中拆出落到本包。）
// ============================================================================

func TestIsAdvancedAgentToolName_Expanded(t *testing.T) {
	// 所有新增 Agent 都应返回 true
	newAgents := []string{
		"claude-code", "codex-cli", "pi", "hermes", "aider",
		"continue", "cline", "windsurf", "cursor", "copilot",
	}
	for _, name := range newAgents {
		if !IsAdvancedAgentToolName(name) {
			t.Errorf("IsAdvancedAgentToolName(%q) = false, want true", name)
		}
	}
	// 带版本号也应返回 true
	versioned := []string{
		"claude-code/1.0.5", "codex-cli/0.1.0", "aider/0.60.0",
	}
	for _, name := range versioned {
		if !IsAdvancedAgentToolName(name) {
			t.Errorf("IsAdvancedAgentToolName(%q) = false, want true (versioned)", name)
		}
	}
}

func TestIsAdvancedAgentToolName_StillRejectsUnknown(t *testing.T) {
	unknowns := []string{"unknown-bot", "random-client", "test-agent"}
	for _, name := range unknowns {
		if IsAdvancedAgentToolName(name) {
			t.Errorf("IsAdvancedAgentToolName(%q) = true, want false", name)
		}
	}
}

func TestIsSyntheticSessionEligibleAgent_Expanded(t *testing.T) {
	eligible := []string{
		"codex-cli", "hermes", "aider", "continue", "cline",
	}
	for _, name := range eligible {
		if !IsSyntheticSessionEligibleAgent(name) {
			t.Errorf("IsSyntheticSessionEligibleAgent(%q) = false, want true", name)
		}
	}
	// 带版本号
	if !IsSyntheticSessionEligibleAgent("codex-cli/0.1.0") {
		t.Errorf("IsSyntheticSessionEligibleAgent(%q) = false, want true", "codex-cli/0.1.0")
	}
}

func TestIsSyntheticSessionEligibleAgent_OldAgentsStillWork(t *testing.T) {
	old := []string{"opencode", "openai/python"}
	for _, name := range old {
		if !IsSyntheticSessionEligibleAgent(name) {
			t.Errorf("IsSyntheticSessionEligibleAgent(%q) = false, want true (old agent)", name)
		}
	}
}
