package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// 协议转换分析器：Anthropic → OpenAI
// ============================================================================

// ConvertAnthropicToOpenAIRequest 将 Anthropic 请求转换为 OpenAI 请求
func ConvertAnthropicToOpenAIRequest(anthropicReq *AnthropicMessagesRequest) (*OpenAIChatCompletionRequest, error) {
	if anthropicReq == nil {
		return nil, fmt.Errorf("anthropic request is nil")
	}

	openAIReq := &OpenAIChatCompletionRequest{
		Model:       anthropicReq.Model,
		Stream:      anthropicReq.Stream,
		Temperature: anthropicReq.Temperature,
		TopP:        anthropicReq.TopP,
	}

	// max_tokens 映射：o 系列模型（o1/o3/o4 等）不接受 max_tokens 字段，必须用 max_completion_tokens
	// （v2.0.73: 借鉴 cc-switch is_openai_o_series）
	if isOpenAIOSeries(openAIReq.Model) {
		if anthropicReq.MaxTokens > 0 {
			openAIReq.MaxCompletionTokens = anthropicReq.MaxTokens
		}
	} else {
		if anthropicReq.MaxTokens > 0 {
			openAIReq.MaxTokens = anthropicReq.MaxTokens
		}
	}

	// v2.0.72: metadata.user_id → OpenAI user 字段（映射表早已声称支持，补齐实现）
	if anthropicReq.Metadata != nil {
		if userID, ok := anthropicReq.Metadata["user_id"].(string); ok && userID != "" {
			openAIReq.User = userID
		}
	}

	// 转换 system prompt 为 system 消息
	// v2.0.72: system 支持字符串与 text 块数组两种形态（此前数组形态被 JSON dump 成字符串）
	var openAIMessages []OpenAIMessage

	if anthropicReq.System != nil {
		systemContent := extractTextPartsContent(anthropicReq.System)
		if systemContent != "" {
			openAIMessages = append(openAIMessages, OpenAIMessage{
				Role:    "system",
				Content: systemContent,
			})
		}
	}

	// v2.0.73: Anthropic thinking 配置映射为 OpenAI reasoning_effort（借鉴 cc-switch resolve_reasoning_effort）
	if effort := resolveReasoningEffort(anthropicReq); effort != "" {
		openAIReq.ReasoningEffort = &effort
	}

	// 转换消息（v2.0.72: 一条 Anthropic 消息可能拆成多条 OpenAI 消息——tool_result 块必须独立成 role=tool 消息）
	for _, msg := range anthropicReq.Messages {
		msgs, err := convertAnthropicMessageToOpenAI(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		openAIMessages = append(openAIMessages, msgs...)
	}

	openAIReq.Messages = openAIMessages

	// 转换工具定义
	if len(anthropicReq.Tools) > 0 {
		openAIReq.Tools = convertAnthropicToolsToOpenAI(anthropicReq.Tools)
	}

	// 转换 tool_choice
	if anthropicReq.ToolChoice != nil {
		openAIReq.ToolChoice = convertAnthropicToolChoiceToOpenAI(anthropicReq.ToolChoice)
	}

	if len(anthropicReq.StopSequences) > 0 {
		openAIReq.Stop = anthropicReq.StopSequences
	}

	return openAIReq, nil
}

// convertAnthropicMessageToOpenAI 将单条 Anthropic 消息转换为 OpenAI 消息（可能拆分出多条）
func convertAnthropicMessageToOpenAI(msg AnthropicMessage) ([]OpenAIMessage, error) {
	if msg.Content == nil {
		return []OpenAIMessage{{Role: msg.Role, Content: ""}}, nil
	}

	// 处理字符串内容
	if text, ok := msg.Content.(string); ok {
		return []OpenAIMessage{{Role: msg.Role, Content: text}}, nil
	}

	// 处理内容块数组
	if blocks, ok := msg.Content.([]AnthropicContentBlock); ok {
		return convertAnthropicContentBlocksToOpenAI(msg.Role, blocks)
	}

	// 处理 []interface{}（从 JSON 反序列化）
	if arr, ok := msg.Content.([]interface{}); ok {
		var blocks []AnthropicContentBlock
		for _, item := range arr {
			blockJSON, _ := json.Marshal(item)
			var block AnthropicContentBlock
			if err := json.Unmarshal(blockJSON, &block); err == nil {
				blocks = append(blocks, block)
			}
		}
		return convertAnthropicContentBlocksToOpenAI(msg.Role, blocks)
	}

	// 默认转为字符串
	return []OpenAIMessage{{Role: msg.Role, Content: fmt.Sprintf("%v", msg.Content)}}, nil
}

// convertAnthropicContentBlocksToOpenAI 将 Anthropic 内容块数组转为 OpenAI 消息序列
// v2.0.72: tool_result 块拆分为独立的 role=tool 消息（此前降级为拼进 user 消息的纯文本，
// tool_call_id 关联丢失，多轮工具对话历史被篡改）；image 块转为 image_url part（此前静默消失）
func convertAnthropicContentBlocksToOpenAI(role string, blocks []AnthropicContentBlock) ([]OpenAIMessage, error) {
	var textParts []string
	var contentParts []interface{} // 含 image 等多模态 part 时使用数组形态 content
	var toolCalls []OpenAIToolCall
	var toolMessages []OpenAIMessage

	flushText := func() {
		// 把累计的纯文本并入 contentParts（保持 text 与 image 的相对顺序）
		if len(textParts) > 0 {
			contentParts = append(contentParts, map[string]interface{}{
				"type": "text",
				"text": strings.Join(textParts, "\n"),
			})
			textParts = nil
		}
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}

		case "tool_use":
			// v2.0.72: 平铺线格式字段（此前嵌套建模导致真实 Anthropic JSON 反序列化后永远为 nil）
			if block.Name != "" || block.ID != "" {
				inputJSON, _ := json.Marshal(block.Input)
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   block.ID,
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      block.Name,
						Arguments: string(inputJSON),
					},
				})
			}

		case "tool_result":
			// 每个 tool_result 块 → 独立 role=tool 消息；is_error 时前缀 [ERROR] 保留语义
			content := flattenAnthropicToolResultContent(block.Content)
			if block.IsError {
				content = "[ERROR] " + content
			}
			toolMessages = append(toolMessages, OpenAIMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: block.ToolUseID,
			})

		case "image":
			// image 块 → image_url part（url 直传；base64 拼 data: URI）
			flushText()
			contentParts = append(contentParts, convertAnthropicImageBlockToOpenAIPart(block.Source))

		case "thinking":
			// thinking 块转为文本（OpenAI 侧无一等字段）
			if block.Thinking != "" {
				textParts = append(textParts, block.Thinking)
			}

		default:
			// 未知类型，尝试提取文本
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		}
	}
	flushText()

	// 构建主消息（原 role），tool 消息紧随其后
	var result []OpenAIMessage
	mainMsg := OpenAIMessage{Role: role}
	hasMainContent := len(contentParts) > 0 || len(toolCalls) > 0
	if len(toolCalls) > 0 {
		mainMsg.ToolCalls = toolCalls
	}
	// content：纯文本时用字符串形态（兼容性最好），含多模态 part 时用数组形态
	if len(contentParts) == 1 {
		if tp, ok := contentParts[0].(map[string]interface{}); ok && tp["type"] == "text" {
			mainMsg.Content = tp["text"]
		} else {
			mainMsg.Content = contentParts
		}
	} else if len(contentParts) > 1 {
		mainMsg.Content = contentParts
	} else {
		mainMsg.Content = ""
	}
	// 主消息有内容、或没有任何 tool 消息（避免出现零消息）时才输出主消息
	if hasMainContent || len(toolMessages) == 0 {
		result = append(result, mainMsg)
	}
	result = append(result, toolMessages...)
	return result, nil
}

// flattenAnthropicToolResultContent 拍平 tool_result 的 content（可能是字符串或内容块数组）为文本
// v2.0.73: 递归提取 image 块为 data URI 文本引用（借鉴 cc-switch tool_media.rs 递归提取，深度上限 32），
// 避免 tool_result 中的多模态块被静默丢弃。
func flattenAnthropicToolResultContent(content interface{}) string {
	return flattenToolResultContentRecursive(content, 0, 32)
}

func flattenToolResultContentRecursive(content interface{}, depth, maxDepth int) string {
	if depth > maxDepth {
		return "[content too deep]"
	}
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	if arr, ok := content.([]interface{}); ok {
		var parts []string
		for _, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
				continue
			}
			switch m["type"] {
			case "text":
				if text, _ := m["text"].(string); text != "" {
					parts = append(parts, text)
				}
			case "image":
				if uri := extractImageAsDataURI(m); uri != "" {
					parts = append(parts, "[image: "+uri+"]")
				} else {
					if b, err := json.Marshal(item); err == nil {
						parts = append(parts, string(b))
					}
				}
			default:
				// 嵌套 content 数组（如 tool_result 内含子 content）递归展开，深度 +1
				if inner, ok := m["content"]; ok {
					if t := flattenToolResultContentRecursive(inner, depth+1, maxDepth); t != "" {
						parts = append(parts, t)
						continue
					}
				}
				if b, err := json.Marshal(item); err == nil {
					parts = append(parts, string(b))
				}
			}
		}
		return strings.Join(parts, " ")
	}
	return extractStringContent(content)
}

// extractImageAsDataURI 从 Anthropic image 块（map 形态）中提取 data URI。
// 支持 source.base64（base64+media_type）与 source.url（url 直传）两种形态。
func extractImageAsDataURI(m map[string]interface{}) string {
	source, ok := m["source"].(map[string]interface{})
	if !ok {
		return ""
	}
	srcType, _ := source["type"].(string)
	switch srcType {
	case "base64":
		mediaType, _ := source["media_type"].(string)
		if mediaType == "" {
			mediaType = "image/png"
		}
		data, _ := source["data"].(string)
		if data == "" {
			return ""
		}
		return "data:" + mediaType + ";base64," + data
	case "url":
		url, _ := source["url"].(string)
		return url
	}
	return ""
}

// convertAnthropicImageBlockToOpenAIPart 将 Anthropic image source 转为 OpenAI image_url part
func convertAnthropicImageBlockToOpenAIPart(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return map[string]interface{}{"type": "text", "text": "[image: missing source]"}
	}
	srcType, _ := source["type"].(string)
	switch srcType {
	case "url":
		url, _ := source["url"].(string)
		return map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": url},
		}
	case "base64":
		mediaType, _ := source["media_type"].(string)
		if mediaType == "" {
			mediaType = "image/png"
		}
		data, _ := source["data"].(string)
		return map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": "data:" + mediaType + ";base64," + data},
		}
	default:
		// 未知 source 形状：JSON 字符串化为文本（不丢数据）
		if b, err := json.Marshal(source); err == nil {
			return map[string]interface{}{"type": "text", "text": "[image: " + string(b) + "]"}
		}
		return map[string]interface{}{"type": "text", "text": "[image: unsupported source]"}
	}
}

// convertAnthropicToolsToOpenAI 将 Anthropic 工具定义转为 OpenAI 工具定义
func convertAnthropicToolsToOpenAI(tools []AnthropicTool) []OpenAITool {
	var openAITools []OpenAITool
	for _, tool := range tools {
		// v2.0.72: 无 name 的工具直接丢弃（转换产出必然非法，借鉴 Switchyard 同款策略）
		if tool.Name == "" {
			continue
		}
		openAITool := OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
			},
		}

		if tool.InputSchema != nil {
			openAITool.Function.Parameters = tool.InputSchema
		} else {
			openAITool.Function.Parameters = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		openAITools = append(openAITools, openAITool)
	}
	return openAITools
}

// convertAnthropicToolChoiceToOpenAI 转换 tool_choice
func convertAnthropicToolChoiceToOpenAI(toolChoice interface{}) interface{} {
	switch v := toolChoice.(type) {
	case string:
		switch v {
		case "auto":
			return "auto"
		case "none":
			return "none"
		case "any":
			return "required"
		default:
			return "auto"
		}
	case map[string]interface{}:
		// Anthropic: {"type": "tool", "name": "xxx"}
		// OpenAI: {"type": "function", "function": {"name": "xxx"}}
		if toolType, ok := v["type"].(string); ok && toolType == "tool" {
			if name, ok := v["name"].(string); ok {
				return map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": name,
					},
				}
			}
		}
		return "auto"
	default:
		return "auto"
	}
}

// ============================================================================
// a2o 辅助：o-series 检测 / reasoning_effort / system 规范化
// ============================================================================

// isOpenAIOSeries 检测 OpenAI o 系列模型（o1/o3/o4 等）：以 'o'/'O' 开头且第二位是数字
// （借鉴 cc-switch is_openai_o_series；o 系列不接受 max_tokens 字段，必须用 max_completion_tokens）
func isOpenAIOSeries(model string) bool {
	if len(model) < 2 {
		return false
	}
	b := model[0]
	if b != 'o' && b != 'O' {
		return false
	}
	return model[1] >= '0' && model[1] <= '9'
}

// resolveReasoningEffort 将 Anthropic thinking 配置映射为 OpenAI reasoning_effort
// （借鉴 cc-switch resolve_reasoning_effort）
// 优先级：output_config.effort > thinking.type+budget_tokens
func resolveReasoningEffort(anthropicReq *AnthropicMessagesRequest) string {
	// 优先 output_config.effort
	if oc, ok := anthropicReq.OutputConfig.(map[string]interface{}); ok {
		if effort, ok := oc["effort"].(string); ok && effort != "" {
			return effort // low/medium/high/max，OpenAI 接受这些值
		}
	}
	thinking, ok := anthropicReq.Thinking.(map[string]interface{})
	if !ok {
		return ""
	}
	tType, _ := thinking["type"].(string)
	switch tType {
	case "adaptive":
		return "high"
	case "enabled":
		var budget float64
		switch b := thinking["budget_tokens"].(type) {
		case float64:
			budget = b
		case int:
			budget = float64(b)
		case int64:
			budget = float64(b)
		}
		if budget == 0 {
			return "high"
		}
		if budget < 4000 {
			return "low"
		}
		if budget < 16000 {
			return "medium"
		}
		return "high"
	}
	return ""
}

// ============================================================================
// 响应转换
// ============================================================================

// mapAnthropicStopReasonToOpenAI Anthropic stop_reason → OpenAI finish_reason
// v2.0.72: 补齐 refusal/pause_turn/model_context_window_exceeded；未识别值原样透传（此前直接消失）
func mapAnthropicStopReasonToOpenAI(stopReason string) string {
	switch stopReason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	case "pause_turn":
		return "stop"
	case "model_context_window_exceeded":
		return "length"
	case "":
		return ""
	default:
		return stopReason
	}
}

// mapOpenAIFinishReasonToAnthropic OpenAI finish_reason → Anthropic stop_reason
// v2.0.72: 补齐 function_call/content_filter；未识别值原样透传（此前直接消失）
func mapOpenAIFinishReasonToAnthropic(finishReason string) string {
	switch finishReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "function_call":
		return "tool_use"
	case "content_filter":
		return "refusal"
	case "":
		return ""
	default:
		return finishReason
	}
}

// rewriteAnthropicIDToOpenAI 响应 id 前缀改写：msg_xxx → chatcmpl_xxx（让 id 长得像目标格式）
func rewriteAnthropicIDToOpenAI(id string) string {
	if strings.HasPrefix(id, "msg_") {
		return "chatcmpl_" + strings.TrimPrefix(id, "msg_")
	}
	if id == "" {
		return "chatcmpl_converted"
	}
	return id
}

// rewriteOpenAIIDToAnthropic 响应 id 前缀改写：chatcmpl_xxx → msg_xxx
func rewriteOpenAIIDToAnthropic(id string) string {
	if strings.HasPrefix(id, "chatcmpl_") {
		return "msg_" + strings.TrimPrefix(id, "chatcmpl_")
	}
	if id == "" {
		return "msg_converted"
	}
	return id
}

// ConvertAnthropicToOpenAIResponse 将 Anthropic 非流式响应转为 OpenAI 响应
func ConvertAnthropicToOpenAIResponse(anthropicResp *AnthropicMessagesResponse) (*OpenAIChatCompletionResponse, error) {
	if anthropicResp == nil {
		return nil, fmt.Errorf("anthropic response is nil")
	}

	openAIResp := &OpenAIChatCompletionResponse{
		ID:      rewriteAnthropicIDToOpenAI(anthropicResp.ID),
		Object:  "chat.completion",
		Created: time.Now().Unix(), // v2.0.72: 此前硬编码 0，客户端收到 "created":0
		Model:   anthropicResp.Model,
	}

	// 转换内容块为消息内容
	var content string
	var toolCalls []OpenAIToolCall

	for _, block := range anthropicResp.Content {
		switch block.Type {
		case "text":
			if content != "" {
				content += "\n"
			}
			content += block.Text

		case "tool_use":
			// v2.0.72: 平铺线格式字段（此前嵌套建模导致 tool_calls 整体丢失、
			// finish_reason=tool_calls 与空 tool_calls 同时在场，Agent 工具循环必断）
			if block.Name != "" || block.ID != "" {
				inputJSON, _ := json.Marshal(block.Input)
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   block.ID,
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      block.Name,
						Arguments: string(inputJSON),
					},
				})
			}
		}
	}

	message := &OpenAIMessage{
		Role:    "assistant",
		Content: content,
	}
	if len(toolCalls) > 0 {
		message.ToolCalls = toolCalls
	}

	finishReason := mapAnthropicStopReasonToOpenAI(anthropicResp.StopReason)

	openAIResp.Choices = []OpenAIChoice{
		{
			Index:        0,
			Message:      message,
			FinishReason: finishReason,
		},
	}

	if anthropicResp.Usage != nil {
		// v2.0.72: cache token 明细映射——prompt_tokens 把缓存部分加回来（OpenAI 语义），
		// cached_tokens 只计 cache_read（OpenAI cached_tokens 语义=读缓存命中）
		promptTokens := anthropicResp.Usage.InputTokens + anthropicResp.Usage.CacheReadInputTokens + anthropicResp.Usage.CacheCreationInputTokens
		openAIResp.Usage = &OpenAIUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      promptTokens + anthropicResp.Usage.OutputTokens,
		}
		if anthropicResp.Usage.CacheReadInputTokens > 0 {
			openAIResp.Usage.PromptTokensDetails = &OpenAIPromptTokensDetails{
				CachedTokens: anthropicResp.Usage.CacheReadInputTokens,
			}
		}
	}

	return openAIResp, nil
}

// ConvertOpenAIToAnthropicResponse 将 OpenAI 非流式响应转为 Anthropic 响应
func ConvertOpenAIToAnthropicResponse(openAIResp *OpenAIChatCompletionResponse) (*AnthropicMessagesResponse, error) {
	if openAIResp == nil {
		return nil, fmt.Errorf("openAI response is nil")
	}

	anthropicResp := &AnthropicMessagesResponse{
		ID:    rewriteOpenAIIDToAnthropic(openAIResp.ID),
		Type:  "message",
		Role:  "assistant",
		Model: openAIResp.Model,
	}

	if len(openAIResp.Choices) == 0 {
		anthropicResp.Content = []AnthropicContentBlock{}
		return anthropicResp, nil
	}

	choice := openAIResp.Choices[0]
	message := choice.Message
	if message == nil {
		message = choice.Delta
	}

	var contentBlocks []AnthropicContentBlock

	// 文本内容
	if message != nil && message.Content != nil {
		text := extractTextPartsContent(message.Content)
		if text != "" {
			contentBlocks = append(contentBlocks, AnthropicContentBlock{
				Type: "text",
				Text: text,
			})
		}
	}

	// v2.0.73: 消息级 refusal 字段（o 系列模型安全拒绝时 content 为 nil，refusal 含拒绝原因文本；
	// 借鉴 cc-switch message-level refusal → text block）。content 有值时 refusal 忽略（content 优先）。
	if len(contentBlocks) == 0 && message != nil && message.Refusal != "" {
		contentBlocks = append(contentBlocks, AnthropicContentBlock{
			Type: "text",
			Text: message.Refusal,
		})
	}

	// 工具调用（平铺线格式输出）
	if message != nil && len(message.ToolCalls) > 0 {
		for i, tc := range message.ToolCalls {
			contentBlocks = append(contentBlocks, convertOpenAIToolCallToAnthropicBlock(tc, i))
		}
	}

	anthropicResp.Content = contentBlocks

	// stop_reason 映射
	anthropicResp.StopReason = mapOpenAIFinishReasonToAnthropic(choice.FinishReason)

	if openAIResp.Usage != nil {
		// v2.0.72: input_tokens 语义 = 非缓存输入 token（从 prompt_tokens 减去 cached 部分，防负饱和）；
		// cached_tokens → cache_read_input_tokens
		inputTokens := openAIResp.Usage.PromptTokens
		cacheRead := 0
		if openAIResp.Usage.PromptTokensDetails != nil {
			cacheRead = openAIResp.Usage.PromptTokensDetails.CachedTokens
		}
		inputTokens -= cacheRead
		if inputTokens < 0 {
			inputTokens = 0
		}
		anthropicResp.Usage = &AnthropicUsage{
			InputTokens:          inputTokens,
			OutputTokens:         openAIResp.Usage.CompletionTokens,
			CacheReadInputTokens: cacheRead,
		}
	}

	return anthropicResp, nil
}
