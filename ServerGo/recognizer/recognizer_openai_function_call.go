package recognizer

import (
	"strings"
)

// ============================================================================
// OpenAI 协议 Function Call / Tools 识别
// ============================================================================
//
// 负责从 OpenAI 格式请求体中提取工具名称列表。
//
// OpenAI 标准格式：
//   tools[].type="function"
//   tools[].function.name
//
// 兜底格式（assistant message 中的 tool_calls）：
//   messages[].tool_calls[].function.name
//
// ============================================================================

// ExtractOpenAIToolNames 从 OpenAI 格式 body 中提取 tools 名称列表。
// 返回逗号分隔字符串，空时返回 ""。
func ExtractOpenAIToolNames(body map[string]interface{}) string {
	toolsRaw, ok := body["tools"].([]interface{})
	if !ok || len(toolsRaw) == 0 {
		return ""
	}
	var names []string
	seen := make(map[string]bool)
	for _, toolRaw := range toolsRaw {
		name := ExtractOpenAISingleToolName(toolRaw)
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

// ExtractOpenAISingleToolName 提取单个 OpenAI 格式 tool 的名称。
// 支持：tools[].function.name / tools[].name（兼容）/ 字符串 / 自定义字段。
func ExtractOpenAISingleToolName(toolRaw interface{}) string {
	// 场景 1：tool 元素直接是字符串
	if s, isStr := toolRaw.(string); isStr {
		return s
	}
	toolMap, isMap := toolRaw.(map[string]interface{})
	if !isMap {
		return ""
	}
	// 场景 2：OpenAI 标准格式 - tools[].function.name
	if fn, ok := toolMap["function"].(map[string]interface{}); ok {
		if n, ok := fn["name"].(string); ok && n != "" {
			return n
		}
	}
	// 场景 3：直接 name 字段（兼容简化格式）
	if n, ok := toolMap["name"].(string); ok && n != "" {
		return n
	}
	// 场景 4：自定义字段
	for _, k := range []string{"customName", "displayName", "id", "toolName"} {
		if n, ok := toolMap[k].(string); ok && n != "" {
			return n
		}
	}
	return ""
}

// ExtractOpenAIToolCallsFromMessages 从 messages[].tool_calls 兜底提取工具名。
// 用于请求体没有顶层 tools 字段，但 assistant message 中带有 tool_calls 的场景。
func ExtractOpenAIToolCallsFromMessages(body map[string]interface{}) string {
	messages, ok := body["messages"].([]interface{})
	if !ok {
		return ""
	}
	var names []string
	seen := make(map[string]bool)
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		tcs, ok := msgMap["tool_calls"].([]interface{})
		if !ok {
			continue
		}
		for _, tc := range tcs {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}
			var n string
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					n = name
				}
			}
			if n == "" {
				if name, ok := tcMap["name"].(string); ok && name != "" {
					n = name
				}
			}
			if n != "" && !seen[n] {
				names = append(names, n)
				seen[n] = true
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, ",")
}

// IsOpenAIFormatBody 判断 body 是否为 OpenAI 格式（含 tools[].function 或 messages[].tool_calls）。
func IsOpenAIFormatBody(body map[string]interface{}) bool {
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		if first, ok := tools[0].(map[string]interface{}); ok {
			if _, ok := first["function"]; ok {
				return true
			}
		}
	}
	if messages, ok := body["messages"].([]interface{}); ok && len(messages) > 0 {
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				if _, ok := msgMap["tool_calls"]; ok {
					return true
				}
			}
		}
	}
	return false
}
