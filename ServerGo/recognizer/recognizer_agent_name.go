package recognizer

import (
	"strings"
)

// AgentToolRecognitionResult Agent 工具识别结果
type AgentToolRecognitionResult struct {
	AgentToolName string
	AgentToolInfo string
}

// knownAgentPrefixes 已知 Agent 工具的 UA 前缀到标准名称映射。
// 用于当 UA 解析出的 name 与实际 Agent 名称不一致时做规范化。
// key 为小写前缀，value 为标准名称。
// 注：OpenAI 系列走 refineOpenAIAgentName 专属路径，不在此表。
var knownAgentPrefixes = map[string]string{
	"claude-code":   "claude-code", // Claude Code CLI 新版 UA
	"anthropic-cli": "claude-code", // Anthropic CLI 变体
	"codex-cli":     "codex-cli",   // OpenAI Codex CLI
	"codex":         "codex-cli",   // OpenAI Codex CLI 短名
	"pi":            "pi",          // Pi AI（Anthropic 的 Pi 助手）
	"hermes":        "hermes",      // Hermes CLI
	"aider":         "aider",       // Aider AI pair programming
	"continue":      "continue",    // Continue IDE extension
	"cline":         "cline",       // Cline VS Code extension
	"windsurf":      "windsurf",    // Windsurf IDE (Codeium)
	"cursor":        "cursor",      // Cursor IDE
	"copilot":       "copilot",     // GitHub Copilot
	// v2.0.73 新增（借鉴 cc-switch AppType 枚举 + UA presets）
	"grok-build": "grok-build", // Grok Build (xAI)
	"grok":       "grok-build", // Grok Build 短名
	"opencode":   "opencode",   // OpenCode IDE
	"openclaw":   "openclaw",   // OpenClaw WebChat（独立于 OpenAI/JS UA 识别路径）
	"rovo":       "rovo",       // Rovo Dev CLI (Atlassian)
	"longcat":    "longcat",    // LongCat CLI (美团)
	"kilo-code":  "kilo-code",  // Kilo Code IDE
	"kilo":       "kilo-code",  // Kilo Code 短名
	"amp":        "amp",        // Amp (Sourcegraph)
}

// lookupKnownAgentPrefix 根据 name 查找已知 Agent 前缀映射。
// 先做小写完全匹配，再做前缀匹配（如 "claude-code/1.0" → "claude-code"）。
// 返回 (标准名称, true) 或 ("", false)。
func lookupKnownAgentPrefix(name string) (string, bool) {
	lower := strings.ToLower(name)
	if canonical, ok := knownAgentPrefixes[lower]; ok {
		return canonical, true
	}
	// 前缀匹配：如 "aider/0.60" → "aider"
	for prefix, canonical := range knownAgentPrefixes {
		if strings.HasPrefix(lower, prefix+"/") {
			return canonical, true
		}
	}
	return "", false
}

// RecognizeAgentTool 从 User-Agent 识别 Agent 工具
// 识别规则：只要 User-Agent 非空，遇到第一个 '/' 或 '\' 就分割
// 前面部分作为 AgentToolName，后面部分作为 AgentToolInfo
// 特殊规则：
//   - 如果识别出的 AgentToolName 是 "OpenAI"，则继续从原始字符串中
//     提取 "OpenAI" 之后直到遇到空格或换行符的完整子串作为 AgentToolName
//   - 如果识别出的 AgentToolName 命中已知前缀映射（knownAgentPrefixes），
//     则返回映射后的标准名称
//
// 例如："OpenAI/JS 4.0.0" → AgentToolName="OpenAI/JS", AgentToolInfo="4.0.0"
//
//	"aider/0.60.0" → AgentToolName="aider", AgentToolInfo="0.60.0"
//	"claude-code/1.0.5" → AgentToolName="claude-code", AgentToolInfo="1.0.5"
func RecognizeAgentTool(userAgent string) AgentToolRecognitionResult {
	userAgent = strings.TrimSpace(userAgent)

	if userAgent == "" {
		return AgentToolRecognitionResult{
			AgentToolName: "unknown",
			AgentToolInfo: "",
		}
	}

	// 查找第一个 '/' 或 '\' 的位置
	slashIdx := strings.IndexAny(userAgent, "/\\")
	if slashIdx > 0 {
		// 分割：前面作为 name，后面作为 info
		name := strings.TrimSpace(userAgent[:slashIdx])
		info := strings.TrimSpace(userAgent[slashIdx+1:])
		if name != "" {
			// 如果识别出的名称是 "OpenAI"，进一步细化识别
			if strings.EqualFold(name, "OpenAI") {
				return refineOpenAIAgentName(userAgent, name)
			}
			// 已知 Agent 前缀规范化（如 "aider/0.60" → "aider"）
			if canonical, ok := lookupKnownAgentPrefix(name); ok {
				return AgentToolRecognitionResult{
					AgentToolName: canonical,
					AgentToolInfo: info,
				}
			}
			return AgentToolRecognitionResult{
				AgentToolName: name,
				AgentToolInfo: info,
			}
		}
	}

	// 没有 '/' 或 '\' 分隔符，整个字符串作为 name，info 为空
	// 先检查已知前缀映射（如 "claude-code" → "claude-code"）
	fullName := userAgent
	if canonical, ok := lookupKnownAgentPrefix(fullName); ok {
		return AgentToolRecognitionResult{
			AgentToolName: canonical,
			AgentToolInfo: "",
		}
	}
	return AgentToolRecognitionResult{
		AgentToolName: fullName,
		AgentToolInfo: "",
	}
}

// refineOpenAIAgentName 细化 OpenAI 类 Agent 名称
// 从原始 User-Agent 字符串中提取 "OpenAI" 之后直到空格或换行的完整子串
// 例如："OpenAI/JS 4.0.0" → name="OpenAI/JS", info="4.0.0"
func refineOpenAIAgentName(userAgent string, originalName string) AgentToolRecognitionResult {
	// 查找 "OpenAI" 在原始字符串中的位置（忽略大小写）
	lowerUA := strings.ToLower(userAgent)
	openaiIdx := strings.Index(lowerUA, "openai")
	if openaiIdx < 0 {
		// 不应发生，回退到原始行为
		return AgentToolRecognitionResult{
			AgentToolName: originalName,
			AgentToolInfo: strings.TrimSpace(userAgent[len(originalName):]),
		}
	}

	// "OpenAI" 之后的起始位置
	startIdx := openaiIdx + 6 // len("openai") == 6
	if startIdx >= len(userAgent) {
		// "OpenAI" 是字符串末尾，没有后续内容
		return AgentToolRecognitionResult{
			AgentToolName: userAgent,
			AgentToolInfo: "",
		}
	}

	// 从 startIdx 开始，找到第一个空格或换行的位置
	endIdx := startIdx
	for endIdx < len(userAgent) {
		ch := userAgent[endIdx]
		if ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' {
			break
		}
		endIdx++
	}

	fullName := userAgent[:endIdx]
	afterName := ""
	if endIdx < len(userAgent) {
		afterName = strings.TrimSpace(userAgent[endIdx:])
	}

	return AgentToolRecognitionResult{
		AgentToolName: fullName,
		AgentToolInfo: afterName,
	}
}
