package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ============================================================================
// 协议转换分析器：OpenAI → Anthropic
// ============================================================================

// o2aDefaultMaxTokens Anthropic max_tokens 必填，OpenAI 双字段均缺失时的默认值（v2.0.72）
// 取 8192 而非 Switchyard 的 64000：防止无上限请求造成意外高额计费
const o2aDefaultMaxTokens = 8192

// ConvertOpenAIToAnthropicRequest 将 OpenAI 请求转换为 Anthropic 请求
func ConvertOpenAIToAnthropicRequest(openAIReq *OpenAIChatCompletionRequest) (*AnthropicMessagesRequest, error) {
	if openAIReq == nil {
		return nil, fmt.Errorf("openAI request is nil")
	}

	anthropicReq := &AnthropicMessagesRequest{
		Model:       openAIReq.Model,
		Stream:      openAIReq.Stream,
		Temperature: openAIReq.Temperature,
		TopP:        openAIReq.TopP,
	}

	// max_tokens 映射：max_completion_tokens 优先，其次 max_tokens，都缺失时补默认值（Anthropic 必填）
	if openAIReq.MaxCompletionTokens > 0 {
		anthropicReq.MaxTokens = openAIReq.MaxCompletionTokens
	} else if openAIReq.MaxTokens > 0 {
		anthropicReq.MaxTokens = openAIReq.MaxTokens
	} else {
		anthropicReq.MaxTokens = o2aDefaultMaxTokens
	}

	// 转换消息和提取 system prompt
	var systemPrompts []string
	var anthropicMessages []AnthropicMessage

	for _, msg := range openAIReq.Messages {
		switch msg.Role {
		case "system", "developer":
			// OpenAI system/developer 消息转为 Anthropic system 参数
			systemContent := extractTextPartsContent(msg.Content)
			if systemContent != "" {
				systemPrompts = append(systemPrompts, systemContent)
			}

		case "user", "assistant":
			anthropicMsg, err := convertOpenAIMessageToAnthropic(msg)
			if err != nil {
				return nil, fmt.Errorf("failed to convert message: %w", err)
			}
			anthropicMessages = append(anthropicMessages, anthropicMsg)

		case "tool":
			// OpenAI tool 结果消息转为 Anthropic tool_result 内容块。
			// v2.0.72: 连续多条 tool 消息合并进同一条 user 消息（Anthropic 要求 user/assistant 交替，
			// 且 tool_result 块应尽量相邻——借鉴 Switchyard encode_anthropic_messages 的合并策略）
			block := convertOpenAIToolResultToAnthropicBlock(msg)
			if n := len(anthropicMessages); n > 0 && anthropicMessages[n-1].Role == "user" {
				if blocks, ok := anthropicMessages[n-1].Content.([]AnthropicContentBlock); ok && isToolResultOnlyBlocks(blocks) {
					anthropicMessages[n-1].Content = append(blocks, block)
					continue
				}
			}
			anthropicMessages = append(anthropicMessages, AnthropicMessage{
				Role:    "user",
				Content: []AnthropicContentBlock{block},
			})

		default:
			// v2.0.72: 未知角色归并为 user（原样透传会被 Anthropic 400 拒绝）
			coerced := msg
			coerced.Role = "user"
			anthropicMsg, err := convertOpenAIMessageToAnthropic(coerced)
			if err != nil {
				return nil, fmt.Errorf("failed to convert message with unknown role %q: %w", msg.Role, err)
			}
			anthropicMessages = append(anthropicMessages, anthropicMsg)
		}
	}

	// 合并 system prompt
	if len(systemPrompts) > 0 {
		anthropicReq.System = strings.Join(systemPrompts, "\n\n")
	}

	anthropicReq.Messages = anthropicMessages

	// v2.0.73: 保证第一条消息来自 user（Anthropic 约束，借鉴 cc-switch ensure_leading_user_message）
	anthropicReq.Messages = ensureLeadingUserMessage(anthropicReq.Messages)

	// v2.0.73: 丢弃不完成工具轮次（assistant tool_use 无匹配 tool_result 时 Anthropic 400，
	// 借鉴 cc-switch drop_incomplete_tool_turns）
	anthropicReq.Messages = dropIncompleteToolTurns(anthropicReq.Messages)

	// 转换工具定义
	if len(openAIReq.Tools) > 0 {
		anthropicReq.Tools = convertOpenAIToolsToAnthropic(openAIReq.Tools)
	}

	// 转换 tool_choice
	if openAIReq.ToolChoice != nil {
		anthropicReq.ToolChoice = convertOpenAIToolChoiceToAnthropic(openAIReq.ToolChoice)
	}

	if openAIReq.Stop != nil {
		anthropicReq.StopSequences = convertStopToStopSequences(openAIReq.Stop)
	}

	// v2.0.73: OpenAI reasoning_effort → Anthropic thinking 配置（借鉴 cc-switch thinking budget 档位）
	if openAIReq.ReasoningEffort != nil {
		anthropicReq.Thinking = reasoningEffortToThinking(*openAIReq.ReasoningEffort, anthropicReq.MaxTokens)
	}

	// v2.0.73: 强制 tool_choice + thinking 冲突处理（Anthropic 拒绝强制工具 + thinking 组合）
	if openAIReq.ToolChoice != nil {
		disableThinkingOnForcedToolChoice(anthropicReq, openAIReq.ToolChoice)
	}

	return anthropicReq, nil
}

// isToolResultOnlyBlocks 判断内容块数组是否全部为 tool_result 块
func isToolResultOnlyBlocks(blocks []AnthropicContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}

// convertOpenAIMessageToAnthropic 将单条 OpenAI 消息转换为 Anthropic 消息
func convertOpenAIMessageToAnthropic(msg OpenAIMessage) (AnthropicMessage, error) {
	anthropicMsg := AnthropicMessage{
		Role: msg.Role,
	}

	// 处理 tool_calls（assistant 消息中的工具调用）
	if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
		var contentBlocks []AnthropicContentBlock

		// 先添加文本内容（如果有）；content 为数组时提取其中的 text 部分，不再整体 JSON dump
		textContent := extractTextPartsContent(msg.Content)
		if textContent != "" {
			contentBlocks = append(contentBlocks, AnthropicContentBlock{
				Type: "text",
				Text: textContent,
			})
		}

		// 添加 tool_use 块
		for i, tc := range msg.ToolCalls {
			contentBlocks = append(contentBlocks, convertOpenAIToolCallToAnthropicBlock(tc, i))
		}

		anthropicMsg.Content = contentBlocks
		return anthropicMsg, nil
	}

	// 普通消息：处理 content
	content := msg.Content
	if content == nil {
		anthropicMsg.Content = ""
		return anthropicMsg, nil
	}

	// 如果 content 是字符串，转为 Anthropic 内容块数组
	if text, ok := content.(string); ok {
		anthropicMsg.Content = []AnthropicContentBlock{
			{Type: "text", Text: text},
		}
		return anthropicMsg, nil
	}

	// 如果 content 已经是数组，逐块转换
	if contentArr, ok := content.([]interface{}); ok {
		var blocks []AnthropicContentBlock
		for _, item := range contentArr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				blockType, _ := itemMap["type"].(string)
				switch blockType {
				case "text":
					text, _ := itemMap["text"].(string)
					blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: text})
				case "image_url":
					// v2.0.72: 完整转换 image source（此前产出无 source 的非法空 image 块，Anthropic 必 400）
					blocks = append(blocks, convertOpenAIImageURLToAnthropicBlock(itemMap["image_url"]))
				default:
					// 未知类型，尝试转为 text
					text, _ := itemMap["text"].(string)
					if text != "" {
						blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: text})
					}
				}
			}
		}
		if len(blocks) > 0 {
			anthropicMsg.Content = blocks
		} else {
			anthropicMsg.Content = ""
		}
		return anthropicMsg, nil
	}

	// 默认：转为字符串
	anthropicMsg.Content = []AnthropicContentBlock{
		{Type: "text", Text: fmt.Sprintf("%v", content)},
	}
	return anthropicMsg, nil
}

// convertOpenAIToolCallToAnthropicBlock 将单个 OpenAI tool_call 转为 Anthropic tool_use 内容块（平铺线格式）
// seq 为 tool_call 在消息内的序号，用于缺失 id 时生成确定性 id
func convertOpenAIToolCallToAnthropicBlock(tc OpenAIToolCall, seq int) AnthropicContentBlock {
	// v2.0.72: Arguments 解析失败包 {"raw": 原文}（宁可包装也不丢数据，借鉴 Switchyard anthropic_tool_input）；
	// 空串 → 空 object（Anthropic 要求 input 必须是 object）
	input := map[string]interface{}{}
	if tc.Function.Arguments != "" {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err == nil && parsed != nil {
			input = parsed
		} else {
			input = map[string]interface{}{"raw": tc.Function.Arguments}
		}
	}
	return AnthropicContentBlock{
		Type:  "tool_use",
		ID:    sanitizeAnthropicToolUseID(tc.ID, seq),
		Name:  tc.Function.Name,
		Input: input,
	}
}

// sanitizeAnthropicToolUseID 清洗 tool_use id：Anthropic 要求 [a-zA-Z0-9_-]，非法字符替换为 _；
// 空 id 生成确定性 id（借鉴 Switchyard sanitize_anthropic_tool_use_id / deterministic_ids 策略）
func sanitizeAnthropicToolUseID(id string, seq int) string {
	if id == "" {
		return fmt.Sprintf("toolu_o2a_%08d", seq)
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// convertOpenAIImageURLToAnthropicBlock 将 OpenAI image_url 内容块转为 Anthropic image 块
// 兼容三种形态：{"url": "https://..."} / {"url": "data:<media>;base64,<data>"} / 裸 url 字符串
func convertOpenAIImageURLToAnthropicBlock(imageURL interface{}) AnthropicContentBlock {
	url := ""
	switch v := imageURL.(type) {
	case string:
		url = v
	case map[string]interface{}:
		url, _ = v["url"].(string)
	}
	if url == "" {
		return AnthropicContentBlock{Type: "text", Text: "[image: unsupported image_url payload]"}
	}
	// data URI → base64 source
	if strings.HasPrefix(url, "data:") {
		rest := url[len("data:"):]
		semi := strings.Index(rest, ";base64,")
		if semi > 0 {
			mediaType := rest[:semi]
			data := rest[semi+len(";base64,"):]
			if mediaType != "" && data != "" {
				return AnthropicContentBlock{
					Type: "image",
					Source: map[string]interface{}{
						"type":       "base64",
						"media_type": mediaType,
						"data":       data,
					},
				}
			}
		}
		return AnthropicContentBlock{Type: "text", Text: "[image: malformed data URI]"}
	}
	// 普通 url → url source
	return AnthropicContentBlock{
		Type: "image",
		Source: map[string]interface{}{
			"type": "url",
			"url":  url,
		},
	}
}

// convertOpenAIToolResultToAnthropicBlock 将 OpenAI tool 结果消息转为 Anthropic tool_result 内容块（平铺线格式）
func convertOpenAIToolResultToAnthropicBlock(msg OpenAIMessage) AnthropicContentBlock {
	content := extractTextPartsContent(msg.Content)
	return AnthropicContentBlock{
		Type:      "tool_result",
		ToolUseID: msg.ToolCallID,
		Content:   content,
		IsError:   false,
	}
}

// convertOpenAIToolsToAnthropic 将 OpenAI 工具定义转为 Anthropic 工具定义
func convertOpenAIToolsToAnthropic(tools []OpenAITool) []AnthropicTool {
	var anthropicTools []AnthropicTool
	for _, tool := range tools {
		anthropicTool := AnthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
		}

		// 转换参数格式
		if tool.Function.Parameters != nil {
			anthropicTool.InputSchema = convertOpenAIParametersToAnthropicSchema(tool.Function.Parameters)
		} else {
			anthropicTool.InputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		anthropicTools = append(anthropicTools, anthropicTool)
	}
	return anthropicTools
}

// convertOpenAIParametersToAnthropicSchema 将 OpenAI function parameters 转为 Anthropic input_schema
func convertOpenAIParametersToAnthropicSchema(params map[string]interface{}) map[string]interface{} {
	// Anthropic 的 input_schema 基本兼容 JSON Schema，直接透传
	// 但需确保 type 字段存在
	result := make(map[string]interface{})
	for k, v := range params {
		result[k] = v
	}
	if _, ok := result["type"]; !ok {
		result["type"] = "object"
	}
	return result
}

// convertOpenAIToolChoiceToAnthropic 转换 tool_choice
func convertOpenAIToolChoiceToAnthropic(toolChoice interface{}) interface{} {
	switch v := toolChoice.(type) {
	case string:
		switch v {
		case "auto":
			return map[string]interface{}{"type": "auto"}
		case "none":
			return map[string]interface{}{"type": "none"}
		case "required":
			return map[string]interface{}{"type": "any"}
		default:
			return map[string]interface{}{"type": "auto"}
		}
	case map[string]interface{}:
		// OpenAI: {"type": "function", "function": {"name": "xxx"}}
		// Anthropic: {"type": "tool", "name": "xxx"}
		if funcData, ok := v["function"].(map[string]interface{}); ok {
			if name, ok := funcData["name"].(string); ok {
				return map[string]interface{}{
					"type": "tool",
					"name": name,
				}
			}
		}
		return map[string]interface{}{"type": "auto"}
	default:
		return map[string]interface{}{"type": "auto"}
	}
}

// ============================================================================
// o2a 辅助：首条 user 消息保证 / 不完成工具轮次丢弃 / thinking 注入 / 冲突处理
// （均借鉴 cc-switch 同名策略）
// ============================================================================

// ensureLeadingUserMessage 保证第一条消息来自 user（Anthropic 约束）。
// 若历史以 assistant/tool 开头，前置合成 "(continuing the conversation)" user 消息，
// 避免 Anthropic 上游 400（借鉴 cc-switch ensure_leading_user_message）。
func ensureLeadingUserMessage(msgs []AnthropicMessage) []AnthropicMessage {
	if len(msgs) == 0 {
		return msgs
	}
	if msgs[0].Role == "user" {
		return msgs
	}
	leading := AnthropicMessage{
		Role:    "user",
		Content: []AnthropicContentBlock{{Type: "text", Text: "(continuing the conversation)"}},
	}
	return append([]AnthropicMessage{leading}, msgs...)
}

// dropIncompleteToolTurns 校验 assistant tool_use 块在紧随的 user 消息中有匹配 tool_result。
// 不完成轮次整体丢弃，避免 Anthropic 400（借鉴 cc-switch drop_incomplete_tool_turns）。
// 策略：
//   - 末尾未匹配的 assistant（当前工具调用轮次，尚无结果）保留——它是正在执行的轮次。
//   - assistant 后紧跟的 user 消息若未能提供匹配全部 tool_use id 的 tool_result，丢弃该 assistant。
func dropIncompleteToolTurns(msgs []AnthropicMessage) []AnthropicMessage {
	if len(msgs) == 0 {
		return msgs
	}
	result := make([]AnthropicMessage, 0, len(msgs))
	// pendingIdx/pendingIDs 记录最近一个有待匹配 tool_use 的 assistant 在 result 中的位置
	var pendingIdx int = -1
	var pendingIDs map[string]bool

	// dropPending 丢弃 pending assistant（及紧随的纯孤立 tool_result user）
	dropPending := func() {
		if pendingIdx < 0 {
			return
		}
		result = append(result[:pendingIdx], result[pendingIdx+1:]...)
		pendingIdx = -1
		pendingIDs = nil
	}

	for _, msg := range msgs {
		if msg.Role == "assistant" {
			// 前一个 assistant 有待匹配但未完成（被非匹配 user 跟随）→ 丢弃
			dropPending()
			ids := toolUseIDsFromBlocks(msg)
			if len(ids) > 0 {
				pendingIdx = len(result)
				pendingIDs = ids
			}
			result = append(result, msg)
		} else if msg.Role == "user" && pendingIdx >= 0 && len(pendingIDs) > 0 {
			// 用户消息紧随有待匹配 tool_use 的 assistant：检查是否提供了匹配的 tool_result
			if blocks, ok := msg.Content.([]AnthropicContentBlock); ok {
				for _, b := range blocks {
					if b.Type == "tool_result" && b.ToolUseID != "" && pendingIDs[b.ToolUseID] {
						delete(pendingIDs, b.ToolUseID)
					}
				}
			}
			if len(pendingIDs) == 0 {
				// 全部匹配 → 轮次完成
				pendingIdx = -1
				pendingIDs = nil
				result = append(result, msg)
			} else {
				// 未完全匹配 → assistant tool_use 孤立，丢弃 assistant
				dropPending()
				// 该 user 消息若纯为孤立 tool_result 则一并丢弃，否则保留（含其他内容）
				if blocks, ok := msg.Content.([]AnthropicContentBlock); ok && isToolResultOnlyBlocks(blocks) {
					// 纯 tool_result，且未匹配 → 全部属于被丢弃 assistant，丢弃
					// （不追加到 result）
				} else {
					result = append(result, msg)
				}
			}
		} else {
			result = append(result, msg)
		}
	}
	// 末尾未匹配的 assistant 保留（当前轮次，客户端将在下次请求返回 tool_result）
	return result
}

// toolUseIDsFromBlocks 从 assistant 消息的内容块中提取 tool_use id 集合
func toolUseIDsFromBlocks(msg AnthropicMessage) map[string]bool {
	ids := make(map[string]bool)
	blocks, ok := msg.Content.([]AnthropicContentBlock)
	if !ok {
		return ids
	}
	for _, b := range blocks {
		if b.Type == "tool_use" && b.ID != "" {
			ids[b.ID] = true
		}
	}
	return ids
}

// reasoningEffortToThinking 将 OpenAI reasoning_effort 映射为 Anthropic thinking 配置
// （借鉴 cc-switch thinking budget 档位 + 钳制到 max_tokens/2）
func reasoningEffortToThinking(effort string, maxTokens int) interface{} {
	if effort == "" {
		return nil
	}
	budgetMap := map[string]int{"low": 1024, "medium": 4000, "high": 16000, "xhigh": 32000}
	budget, ok := budgetMap[effort]
	if !ok {
		budget = 4000
	}
	// 钳制到 max_tokens/2，确保可见答案有足够空间
	ceiling := maxTokens / 2
	if budget > ceiling {
		budget = ceiling
	}
	if budget < 1024 {
		return nil // 预算过小，禁用 thinking
	}
	return map[string]interface{}{"type": "enabled", "budget_tokens": budget}
}

// disableThinkingOnForcedToolChoice 当 tool_choice 为强制模式（required/指定工具）时禁用 thinking。
// Anthropic 拒绝强制工具 + thinking 组合（借鉴 cc-switch forced tool_choice vs thinking 冲突处理）。
func disableThinkingOnForcedToolChoice(req *AnthropicMessagesRequest, toolChoice interface{}) {
	forced := false
	switch tc := toolChoice.(type) {
	case string:
		forced = tc == "required"
	case map[string]interface{}:
		if t, _ := tc["type"].(string); t == "tool" {
			forced = true
		}
	}
	if !forced {
		return
	}
	req.Thinking = nil
	req.OutputConfig = nil
}
