package protocol

import (
	"testing"
)

// ============================================================================
// 响应转换测试
// ============================================================================

// TestConvertAnthropicToOpenAIResponse_Basic 测试 Anthropic 响应 → OpenAI 响应
func TestConvertAnthropicToOpenAIResponse_Basic(t *testing.T) {
	anthropicResp := &AnthropicMessagesResponse{
		ID:    "msg_123",
		Type:  "message",
		Role:  "assistant",
		Model: "claude-3-sonnet",
		Content: []AnthropicContentBlock{
			{Type: "text", Text: "The capital of France is Paris."},
		},
		StopReason: "end_turn",
		Usage: &AnthropicUsage{
			InputTokens:  10,
			OutputTokens: 8,
		},
	}

	openAIResp, err := ConvertAnthropicToOpenAIResponse(anthropicResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	if openAIResp.ID != "chatcmpl_123" {
		t.Errorf("id mismatch: got %s, want chatcmpl_123 (msg_ 前缀改写)", openAIResp.ID)
	}
	if openAIResp.Model != "claude-3-sonnet" {
		t.Errorf("model mismatch: got %s, want claude-3-sonnet", openAIResp.Model)
	}
	if len(openAIResp.Choices) != 1 {
		t.Fatalf("choices count mismatch: got %d, want 1", len(openAIResp.Choices))
	}

	choice := openAIResp.Choices[0]
	if choice.FinishReason != "stop" {
		t.Errorf("finish_reason mismatch: got %s, want stop", choice.FinishReason)
	}

	content := extractStringContent(choice.Message.Content)
	if content != "The capital of France is Paris." {
		t.Errorf("content mismatch: got %q", content)
	}

	if openAIResp.Usage == nil {
		t.Fatalf("usage is nil")
	}
	if openAIResp.Usage.PromptTokens != 10 {
		t.Errorf("prompt_tokens mismatch: got %d, want 10", openAIResp.Usage.PromptTokens)
	}
	if openAIResp.Usage.CompletionTokens != 8 {
		t.Errorf("completion_tokens mismatch: got %d, want 8", openAIResp.Usage.CompletionTokens)
	}
	if openAIResp.Usage.TotalTokens != 18 {
		t.Errorf("total_tokens mismatch: got %d, want 18", openAIResp.Usage.TotalTokens)
	}
}

// TestConvertOpenAIToAnthropicResponse_Basic 测试 OpenAI 响应 → Anthropic 响应
func TestConvertOpenAIToAnthropicResponse_Basic(t *testing.T) {
	openAIResp := &OpenAIChatCompletionResponse{
		ID:     "chatcmpl_123",
		Object: "chat.completion",
		Model:  "gpt-4",
		Choices: []OpenAIChoice{
			{
				Index: 0,
				Message: &OpenAIMessage{
					Role:    "assistant",
					Content: "Hello! How can I help you?",
				},
				FinishReason: "stop",
			},
		},
		Usage: &OpenAIUsage{
			PromptTokens:     5,
			CompletionTokens: 7,
			TotalTokens:      12,
		},
	}

	anthropicResp, err := ConvertOpenAIToAnthropicResponse(openAIResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	if anthropicResp.ID != "msg_123" {
		t.Errorf("id mismatch: got %s, want msg_123 (chatcmpl_ 前缀改写)", anthropicResp.ID)
	}
	if anthropicResp.Type != "message" {
		t.Errorf("type mismatch: got %s, want message", anthropicResp.Type)
	}
	if anthropicResp.Model != "gpt-4" {
		t.Errorf("model mismatch: got %s, want gpt-4", anthropicResp.Model)
	}
	if anthropicResp.StopReason != "end_turn" {
		t.Errorf("stop_reason mismatch: got %s, want end_turn", anthropicResp.StopReason)
	}

	if len(anthropicResp.Content) != 1 {
		t.Fatalf("content count mismatch: got %d, want 1", len(anthropicResp.Content))
	}
	if anthropicResp.Content[0].Text != "Hello! How can I help you?" {
		t.Errorf("content text mismatch: got %q", anthropicResp.Content[0].Text)
	}

	if anthropicResp.Usage == nil {
		t.Fatalf("usage is nil")
	}
	if anthropicResp.Usage.InputTokens != 5 {
		t.Errorf("input_tokens mismatch: got %d, want 5", anthropicResp.Usage.InputTokens)
	}
	if anthropicResp.Usage.OutputTokens != 7 {
		t.Errorf("output_tokens mismatch: got %d, want 7", anthropicResp.Usage.OutputTokens)
	}
}

// TestConvertAnthropicToOpenAIResponse_WithToolUse 测试带工具调用的 Anthropic 响应转换
func TestConvertAnthropicToOpenAIResponse_WithToolUse(t *testing.T) {
	anthropicResp := &AnthropicMessagesResponse{
		ID:    "msg_456",
		Model: "claude-3-sonnet",
		Content: []AnthropicContentBlock{
			{Type: "text", Text: "I'll check the weather."},
			{
				Type: "tool_use",
				ID:   "tool_weather_1",
				Name: "get_weather",
				Input: map[string]interface{}{
					"location": "Beijing",
				},
			},
		},
		StopReason: "tool_use",
	}

	openAIResp, err := ConvertAnthropicToOpenAIResponse(anthropicResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	if len(openAIResp.Choices) != 1 {
		t.Fatalf("choices count mismatch: got %d, want 1", len(openAIResp.Choices))
	}

	choice := openAIResp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason mismatch: got %s, want tool_calls", choice.FinishReason)
	}

	msg := choice.Message
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls count mismatch: got %d, want 1", len(msg.ToolCalls))
	}

	if msg.ToolCalls[0].ID != "tool_weather_1" {
		t.Errorf("tool_call id mismatch: got %s, want tool_weather_1", msg.ToolCalls[0].ID)
	}
	if msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_call name mismatch: got %s, want get_weather", msg.ToolCalls[0].Function.Name)
	}
}
