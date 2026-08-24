package recognizer

import (
	"strings"
)

// ============================================================================
// Anthropic 协议 Tool Call 识别
// ============================================================================
//
// 负责从 Anthropic 格式请求体中提取工具名称列表。
//
// Anthropic 标准格式：
//   tools[].name
//
// 与 OpenAI 的区别：
//   - Anthropic 工具没有 .function 嵌套，直接是 tools[].name
//   - 也没有 messages[].tool_calls 兜底
//
// ============================================================================

// ExtractAnthropicToolNames 从 Anthropic 格式 body 中提取 tools 名称列表。
// 返回逗号分隔字符串，空时返回 ""。
func ExtractAnthropicToolNames(body map[string]interface{}) string {
	toolsRaw, ok := body["tools"].([]interface{})
	if !ok || len(toolsRaw) == 0 {
		return ""
	}
	var names []string
	seen := make(map[string]bool)
	for _, toolRaw := range toolsRaw {
		name := ExtractAnthropicSingleToolName(toolRaw)
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, ",")
}

// ExtractAnthropicSingleToolName 提取单个 Anthropic 格式 tool 的名称。
// 支持：tools[].name / 字符串 / 自定义字段。
func ExtractAnthropicSingleToolName(toolRaw interface{}) string {
	// 场景 1：tool 元素直接是字符串
	if s, isStr := toolRaw.(string); isStr {
		return s
	}
	toolMap, isMap := toolRaw.(map[string]interface{})
	if !isMap {
		return ""
	}
	// 场景 2：Anthropic 标准格式 - tools[].name
	if n, ok := toolMap["name"].(string); ok && n != "" {
		return n
	}
	// 场景 3：自定义字段
	for _, k := range []string{"customName", "displayName", "id", "toolName"} {
		if n, ok := toolMap[k].(string); ok && n != "" {
			return n
		}
	}
	return ""
}

// IsAnthropicFormatBody 判断 body 是否为 Anthropic 格式（含 tools[].name 但不含 function 嵌套）。
func IsAnthropicFormatBody(body map[string]interface{}) bool {
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		if first, ok := tools[0].(map[string]interface{}); ok {
			// Anthropic 格式：有 name 但没有 function 嵌套
			if _, hasName := first["name"]; hasName {
				if _, hasFunction := first["function"]; !hasFunction {
					return true
				}
			}
		}
	}
	return false
}
