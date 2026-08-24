package recognizer

import (
	"testing"
)

func TestRecognizeAgentTool_EmptyString(t *testing.T) {
	result := RecognizeAgentTool("")
	if result.AgentToolName != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "" {
		t.Errorf("expected empty info, got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_WhitespaceOnly(t *testing.T) {
	result := RecognizeAgentTool("   \n  ")
	if result.AgentToolName != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result.AgentToolName)
	}
}

func TestRecognizeAgentTool_NoSlash(t *testing.T) {
	result := RecognizeAgentTool("claude-cli")
	if result.AgentToolName != "claude-cli" {
		t.Errorf("expected 'claude-cli', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "" {
		t.Errorf("expected empty info, got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_NormalSlash(t *testing.T) {
	result := RecognizeAgentTool("openclaw/1.0.0")
	if result.AgentToolName != "openclaw" {
		t.Errorf("expected 'openclaw', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "1.0.0" {
		t.Errorf("expected '1.0.0', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_Backslash(t *testing.T) {
	result := RecognizeAgentTool("MyTool\\2.0")
	if result.AgentToolName != "MyTool" {
		t.Errorf("expected 'MyTool', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "2.0" {
		t.Errorf("expected '2.0', got '%s'", result.AgentToolInfo)
	}
}

// --- OpenAI 细化识别测试 ---

func TestRecognizeAgentTool_OpenAI_JS(t *testing.T) {
	// "OpenAI/JS 4.0.0" → name="OpenAI/JS", info="4.0.0"
	result := RecognizeAgentTool("OpenAI/JS 4.0.0")
	if result.AgentToolName != "OpenAI/JS" {
		t.Errorf("expected 'OpenAI/JS', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "4.0.0" {
		t.Errorf("expected '4.0.0', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_Python(t *testing.T) {
	// "OpenAI-Python/1.50.0" → name="OpenAI-Python", info="1.50.0"
	result := RecognizeAgentTool("OpenAI-Python/1.50.0")
	if result.AgentToolName != "OpenAI-Python" {
		t.Errorf("expected 'OpenAI-Python', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "1.50.0" {
		t.Errorf("expected '1.50.0', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_PythonWithSpaces(t *testing.T) {
	// "OpenAI-Python/1.50.0 langchain/0.1" → name="OpenAI-Python", info="1.50.0 langchain/0.1"
	result := RecognizeAgentTool("OpenAI-Python/1.50.0 langchain/0.1")
	if result.AgentToolName != "OpenAI-Python" {
		t.Errorf("expected 'OpenAI-Python', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "1.50.0 langchain/0.1" {
		t.Errorf("expected '1.50.0 langchain/0.1', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_Plain(t *testing.T) {
	// "OpenAI/v1" → name="OpenAI/v1", info=""
	result := RecognizeAgentTool("OpenAI/v1")
	if result.AgentToolName != "OpenAI/v1" {
		t.Errorf("expected 'OpenAI/v1', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "" {
		t.Errorf("expected empty info, got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_WithNewline(t *testing.T) {
	// "OpenAI/Node\nextra" → name="OpenAI/Node", info="extra"
	result := RecognizeAgentTool("OpenAI/Node\nextra")
	if result.AgentToolName != "OpenAI/Node" {
		t.Errorf("expected 'OpenAI/Node', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "extra" {
		t.Errorf("expected 'extra', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_SlashOnly(t *testing.T) {
	// "OpenAI/ 1.0" → name="OpenAI/", info="1.0" (slash非空格，属于name的一部分)
	result := RecognizeAgentTool("OpenAI/ 1.0")
	if result.AgentToolName != "OpenAI/" {
		t.Errorf("expected 'OpenAI/', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "1.0" {
		t.Errorf("expected '1.0', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_EndOfString(t *testing.T) {
	// "OpenAI/" → name="OpenAI/", info=""（末尾无空格）
	result := RecognizeAgentTool("OpenAI/")
	if result.AgentToolName != "OpenAI/" {
		t.Errorf("expected 'OpenAI/', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "" {
		t.Errorf("expected empty info, got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_OpenAI_CaseInsensitive(t *testing.T) {
	// "openai/JS 2.0" → name="openai/JS", info="2.0"（保留原始大小写）
	result := RecognizeAgentTool("openai/JS 2.0")
	if result.AgentToolName != "openai/JS" {
		t.Errorf("expected 'openai/JS', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "2.0" {
		t.Errorf("expected '2.0', got '%s'", result.AgentToolInfo)
	}
}

func TestRecognizeAgentTool_NonOpenAI_Unchanged(t *testing.T) {
	// 确保非 OpenAI 名称不受影响
	result := RecognizeAgentTool("claude-cli/1.2.3")
	if result.AgentToolName != "claude-cli" {
		t.Errorf("expected 'claude-cli', got '%s'", result.AgentToolName)
	}
	if result.AgentToolInfo != "1.2.3" {
		t.Errorf("expected '1.2.3', got '%s'", result.AgentToolInfo)
	}
}
