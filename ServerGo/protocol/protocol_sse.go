package protocol

import (
	"encoding/json"
	"sort"
	"strings"
)

func AggregateOpenAISSEToResponse(events []SSEEvent) (*OpenAIChatCompletionResponse, []string) {
	resp := &OpenAIChatCompletionResponse{Object: "chat.completion", Choices: []OpenAIChoice{{Index: 0, Message: &OpenAIMessage{Role: "assistant"}}}}
	var warnings []string
	var content strings.Builder
	// v2.0.72: 分桶 key 改 (choiceIndex, toolCallIndex) 二元组，toolCallIndex 优先取协议 index 字段
	// （此前用 delta 数组位置下标 + choice.Index*1000+idx，idx>=1000 串桶）
	type toolCallBucket struct {
		choice int
		index  int
	}
	toolCalls := map[toolCallBucket]*OpenAIToolCall{}
	// v2.0.73: 流截断检测——记录是否收到终端事件 [DONE]（借鉴 cc-switch 流截断检测）
	var sawDone bool
	for _, ev := range events {
		data := strings.TrimSpace(ev.Data)
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				sawDone = true
			}
			continue
		}
		var chunk OpenAIStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			warnings = append(warnings, "skipped malformed OpenAI SSE data: "+err.Error())
			continue
		}
		if resp.ID == "" {
			resp.ID = chunk.ID
			resp.Model = chunk.Model
			resp.Created = chunk.Created
		}
		if chunk.Usage != nil {
			resp.Usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				resp.Choices[0].FinishReason = choice.FinishReason
			}
			if choice.Delta == nil {
				continue
			}
			if choice.Delta.Role != "" {
				resp.Choices[0].Message.Role = choice.Delta.Role
			}
			content.WriteString(extractStringContent(choice.Delta.Content))
			for idx, tc := range choice.Delta.ToolCalls {
				tcIndex := idx
				if tc.Index != nil {
					tcIndex = *tc.Index
				}
				key := toolCallBucket{choice: choice.Index, index: tcIndex}
				existing := toolCalls[key]
				if existing == nil {
					tcCopy := OpenAIToolCall{Type: "function"}
					existing = &tcCopy
					toolCalls[key] = existing
				}
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Type != "" {
					existing.Type = tc.Type
				}
				if tc.Function.Name != "" {
					existing.Function.Name += tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}
	}
	resp.Choices[0].Message.Content = content.String()
	if len(toolCalls) > 0 {
		keys := make([]toolCallBucket, 0, len(toolCalls))
		for key := range toolCalls {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].choice != keys[j].choice {
				return keys[i].choice < keys[j].choice
			}
			return keys[i].index < keys[j].index
		})
		for _, key := range keys {
			resp.Choices[0].Message.ToolCalls = append(resp.Choices[0].Message.ToolCalls, *toolCalls[key])
		}
	}
	// v2.0.73: 流截断检测——有实质内容但缺失 [DONE] 且无 finish_reason 时标记截断警告
	if !sawDone && resp.Choices[0].FinishReason == "" && (content.Len() > 0 || len(toolCalls) > 0) {
		warnings = append(warnings, "stream truncated: no [DONE] event received; response may be incomplete")
		resp.Choices[0].FinishReason = "length"
	}
	return resp, uniqueStrings(warnings)
}

func AggregateAnthropicSSEToResponse(events []SSEEvent) (*AnthropicMessagesResponse, []string) {
	resp := &AnthropicMessagesResponse{Type: "message", Role: "assistant"}
	var warnings []string
	blocks := map[int]*AnthropicContentBlock{}
	partialJSON := map[int]string{} // 为每个 block 索引保留拼接的 partial_json 片段
	// v2.0.73: 流截断检测——记录是否收到 message_stop 终端事件（借鉴 cc-switch 流截断检测）
	var sawMessageStop bool
	for _, ev := range events {
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			warnings = append(warnings, "skipped malformed Anthropic SSE data: "+err.Error())
			continue
		}
		eventType, _ := payload["type"].(string)
		if eventType == "" {
			eventType = ev.Event
		}
		switch eventType {
		case "message_start":
			if msg, ok := payload["message"].(map[string]interface{}); ok {
				if id, ok := msg["id"].(string); ok {
					resp.ID = id
				}
				if model, ok := msg["model"].(string); ok {
					resp.Model = model
				}
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					resp.Usage = anthropicUsageFromMap(usage)
				}
			}
		case "content_block_start":
			idx := intFromAny(payload["index"])
			blockMap, _ := payload["content_block"].(map[string]interface{})
			blocks[idx] = anthropicContentBlockFromMap(blockMap)
			// 初始化 partial_json 为空
			partialJSON[idx] = ""
		case "content_block_delta":
			idx := intFromAny(payload["index"])
			block := blocks[idx]
			if block == nil {
				block = &AnthropicContentBlock{Type: "text"}
				blocks[idx] = block
			}
			if delta, ok := payload["delta"].(map[string]interface{}); ok {
				switch delta["type"] {
				case "text_delta":
					block.Type = "text"
					block.Text += stringFromAny(delta["text"])
				case "thinking_delta":
					block.Type = "thinking"
					block.Thinking += stringFromAny(delta["thinking"])
				case "signature_delta":
					// v2.0.72: thinking 块的 signature 保留（此前丢弃）
					block.Signature += stringFromAny(delta["signature"])
				case "input_json_delta":
					// v2.0.72: delta 先于 content_block_start 到达时按 tool_use 建块（此前静默丢弃）
					if block.Type != "tool_use" {
						block.Type = "tool_use"
						if block.Input == nil {
							block.Input = map[string]interface{}{}
						}
					}
					// 拼接 partial_json 片段
					partialJSON[idx] += stringFromAny(delta["partial_json"])
				}
			}
		case "message_delta":
			if delta, ok := payload["delta"].(map[string]interface{}); ok {
				resp.StopReason = stringFromAny(delta["stop_reason"])
				resp.StopSequence = stringFromAny(delta["stop_sequence"])
			}
			if usage, ok := payload["usage"].(map[string]interface{}); ok {
				parsed := anthropicUsageFromMap(usage)
				if resp.Usage == nil {
					resp.Usage = parsed
				} else if parsed != nil {
					if parsed.InputTokens > 0 {
						resp.Usage.InputTokens = parsed.InputTokens
					}
					if parsed.OutputTokens > 0 {
						resp.Usage.OutputTokens = parsed.OutputTokens
					}
					if parsed.CacheCreationInputTokens > 0 {
						resp.Usage.CacheCreationInputTokens = parsed.CacheCreationInputTokens
					}
					if parsed.CacheReadInputTokens > 0 {
						resp.Usage.CacheReadInputTokens = parsed.CacheReadInputTokens
					}
				}
			}
		case "message_stop":
			sawMessageStop = true
		case "error":
			warnings = append(warnings, "Anthropic SSE error event present")
		}
	}
	if len(blocks) > 0 {
		keys := make([]int, 0, len(blocks))
		for key := range blocks {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		for _, key := range keys {
			block := blocks[key]
			if block.Type == "tool_use" {
				if pj, ok := partialJSON[key]; ok && pj != "" {
					// 尝试将拼接的 partial_json 解析为实际的 input
					var input map[string]interface{}
					if err := json.Unmarshal([]byte(pj), &input); err == nil {
						block.Input = input
					} else {
						// 解析失败，保留原始字符串作为单个字段或记录警告
						block.Input = map[string]interface{}{"_raw_json": pj}
						warnings = append(warnings, "failed to parse tool_use input JSON: "+err.Error())
					}
				}
			}
			resp.Content = append(resp.Content, *block)
		}
	}
	// v2.0.73: 流截断检测——有实质内容但缺失 message_stop 时标记截断警告
	if !sawMessageStop && resp.StopReason == "" && len(resp.Content) > 0 {
		warnings = append(warnings, "stream truncated: no message_stop event received; response may be incomplete")
		if resp.StopReason == "" {
			resp.StopReason = "max_tokens"
		}
	}
	return resp, uniqueStrings(warnings)
}

func anthropicContentBlockFromMap(data map[string]interface{}) *AnthropicContentBlock {
	// v2.0.72: 平铺线格式字段（id/name/input/tool_use_id 直接在块顶层）
	block := &AnthropicContentBlock{Type: stringFromAny(data["type"])}
	switch block.Type {
	case "text":
		block.Text = stringFromAny(data["text"])
	case "thinking":
		block.Thinking = stringFromAny(data["thinking"])
		block.Signature = stringFromAny(data["signature"])
	case "tool_use":
		block.ID = stringFromAny(data["id"])
		block.Name = stringFromAny(data["name"])
		block.Input = map[string]interface{}{}
		if input, ok := data["input"].(map[string]interface{}); ok {
			block.Input = input
		}
	case "tool_result":
		block.ToolUseID = stringFromAny(data["tool_use_id"])
		block.Content = data["content"]
		block.IsError, _ = data["is_error"].(bool)
	}
	if block.Type == "" {
		block.Type = "text"
	}
	return block
}

func anthropicUsageFromMap(data map[string]interface{}) *AnthropicUsage {
	if data == nil {
		return nil
	}
	return &AnthropicUsage{
		InputTokens:              intFromAny(data["input_tokens"]),
		OutputTokens:             intFromAny(data["output_tokens"]),
		CacheCreationInputTokens: intFromAny(data["cache_creation_input_tokens"]),
		CacheReadInputTokens:     intFromAny(data["cache_read_input_tokens"]),
	}
}

func intFromAny(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func stringFromAny(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
