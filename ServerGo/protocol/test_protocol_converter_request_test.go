package protocol

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// ============================================================================
// 测试辅助函数
// ============================================================================

// float64Ptr 返回 float64 指针（v2.0.72: Temperature/TopP 已改为 *float64）
func float64Ptr(v float64) *float64 {
	return &v
}

// loadJSONSamples 从 JSON 文件加载真实样本数据
func loadJSONSamples(t *testing.T, filepath string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("failed to read sample file %s: %v", filepath, err)
	}
	var samples []map[string]interface{}
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatalf("failed to unmarshal samples: %v", err)
	}
	return samples
}

// mustMarshal 将对象序列化为 JSON 字节
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return b
}

// mustUnmarshalOpenAIRequest 从 JSON 反序列化 OpenAI 请求
func mustUnmarshalOpenAIRequest(t *testing.T, data []byte) *OpenAIChatCompletionRequest {
	t.Helper()
	var req OpenAIChatCompletionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("failed to unmarshal OpenAI request: %v", err)
	}
	return &req
}

// mustUnmarshalAnthropicRequest 从 JSON 反序列化 Anthropic 请求
func mustUnmarshalAnthropicRequest(t *testing.T, data []byte) *AnthropicMessagesRequest {
	t.Helper()
	var req AnthropicMessagesRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("failed to unmarshal Anthropic request: %v", err)
	}
	return &req
}

// ============================================================================
// 基础转换测试
// ============================================================================

// TestConvertOpenAIToAnthropicRequest_Basic 测试基本的 OpenAI → Anthropic 请求转换
func TestConvertOpenAIToAnthropicRequest_Basic(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model:       "gpt-4",
		Temperature: float64Ptr(0.7),
		TopP:        float64Ptr(0.9),
		MaxTokens:   1024,
		Stream:      false,
		Messages: []OpenAIMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello, how are you?"},
		},
	}

	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// 验证基本字段映射
	if anthropicReq.Model != "gpt-4" {
		t.Errorf("model mismatch: got %s, want gpt-4", anthropicReq.Model)
	}
	if anthropicReq.Temperature == nil || *anthropicReq.Temperature != 0.7 {
		t.Errorf("temperature mismatch: got %v, want 0.7", anthropicReq.Temperature)
	}
	if anthropicReq.TopP == nil || *anthropicReq.TopP != 0.9 {
		t.Errorf("top_p mismatch: got %v, want 0.9", anthropicReq.TopP)
	}
	if anthropicReq.MaxTokens != 1024 {
		t.Errorf("max_tokens mismatch: got %d, want 1024", anthropicReq.MaxTokens)
	}
	if anthropicReq.Stream != false {
		t.Errorf("stream mismatch: got %v, want false", anthropicReq.Stream)
	}

	// 验证 system prompt 被提取到独立参数
	systemContent := extractStringContent(anthropicReq.System)
	if systemContent != "You are a helpful assistant." {
		t.Errorf("system prompt mismatch: got %q, want %q", systemContent, "You are a helpful assistant.")
	}

	// 验证消息转换
	if len(anthropicReq.Messages) != 1 {
		t.Fatalf("messages count mismatch: got %d, want 1", len(anthropicReq.Messages))
	}
	if anthropicReq.Messages[0].Role != "user" {
		t.Errorf("message role mismatch: got %s, want user", anthropicReq.Messages[0].Role)
	}
}

// TestConvertAnthropicToOpenAIRequest_Basic 测试基本的 Anthropic → OpenAI 请求转换
func TestConvertAnthropicToOpenAIRequest_Basic(t *testing.T) {
	anthropicReq := &AnthropicMessagesRequest{
		Model:       "claude-3-sonnet",
		Temperature: float64Ptr(0.5),
		TopP:        float64Ptr(0.95),
		MaxTokens:   2048,
		Stream:      true,
		System:      "You are Claude, a helpful AI assistant.",
		Messages: []AnthropicMessage{
			{Role: "user", Content: "What is the capital of France?"},
		},
	}

	openAIReq, err := ConvertAnthropicToOpenAIRequest(anthropicReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// 验证基本字段映射
	if openAIReq.Model != "claude-3-sonnet" {
		t.Errorf("model mismatch: got %s, want claude-3-sonnet", openAIReq.Model)
	}
	if openAIReq.Temperature == nil || *openAIReq.Temperature != 0.5 {
		t.Errorf("temperature mismatch: got %v, want 0.5", openAIReq.Temperature)
	}
	if openAIReq.TopP == nil || *openAIReq.TopP != 0.95 {
		t.Errorf("top_p mismatch: got %v, want 0.95", openAIReq.TopP)
	}
	if openAIReq.MaxTokens != 2048 {
		t.Errorf("max_tokens mismatch: got %d, want 2048", openAIReq.MaxTokens)
	}
	if openAIReq.Stream != true {
		t.Errorf("stream mismatch: got %v, want true", openAIReq.Stream)
	}

	// 验证 system prompt 被转换为 system 消息
	if len(openAIReq.Messages) != 2 {
		t.Fatalf("messages count mismatch: got %d, want 2", len(openAIReq.Messages))
	}
	if openAIReq.Messages[0].Role != "system" {
		t.Errorf("first message role mismatch: got %s, want system", openAIReq.Messages[0].Role)
	}
	systemContent := extractStringContent(openAIReq.Messages[0].Content)
	if systemContent != "You are Claude, a helpful AI assistant." {
		t.Errorf("system content mismatch: got %q", systemContent)
	}

	if openAIReq.Messages[1].Role != "user" {
		t.Errorf("second message role mismatch: got %s, want user", openAIReq.Messages[1].Role)
	}
}

// ============================================================================
// 真实数据样本测试：OpenAI → Anthropic
// ============================================================================

// TestConvertOpenAIToAnthropicRequest_RealSamples 使用真实 OpenAI 样本测试转换
func TestConvertOpenAIToAnthropicRequest_RealSamples(t *testing.T) {
	samples := loadJSONSamples(t, "OpenAIAnalysis/OpenAIRawSamples.json")
	if len(samples) == 0 {
		t.Skip("no OpenAI samples available")
	}

	successCount := 0
	for i, sample := range samples {
		reqData, ok := sample["request"]
		if !ok {
			t.Logf("sample %d: no request data, skipping", i)
			continue
		}

		reqJSON, err := json.Marshal(reqData)
		if err != nil {
			t.Logf("sample %d: failed to marshal request: %v", i, err)
			continue
		}

		openAIReq := mustUnmarshalOpenAIRequest(t, reqJSON)
		anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
		if err != nil {
			t.Logf("sample %d: conversion failed: %v", i, err)
			continue
		}

		// 验证转换结果
		if anthropicReq.Model == "" {
			t.Errorf("sample %d: model is empty after conversion", i)
			continue
		}

		// 验证消息被正确转换
		if len(anthropicReq.Messages) == 0 {
			t.Errorf("sample %d: no messages after conversion", i)
			continue
		}

		// 验证 system prompt 被正确提取
		hasSystemInOpenAI := false
		for _, msg := range openAIReq.Messages {
			if msg.Role == "system" {
				hasSystemInOpenAI = true
				break
			}
		}
		if hasSystemInOpenAI {
			if anthropicReq.System == nil || extractStringContent(anthropicReq.System) == "" {
				t.Errorf("sample %d: OpenAI had system message but Anthropic system is empty", i)
				continue
			}
		}

		// 验证流式标志
		if anthropicReq.Stream != openAIReq.Stream {
			t.Errorf("sample %d: stream flag mismatch: OpenAI=%v, Anthropic=%v", i, openAIReq.Stream, anthropicReq.Stream)
			continue
		}

		successCount++
	}

	t.Logf("OpenAI→Anthropic real sample tests: %d/%d passed", successCount, len(samples))
	if successCount == 0 {
		t.Fatalf("all real sample conversions failed")
	}
}

// ============================================================================
// 真实数据样本测试：Anthropic → OpenAI
// ============================================================================

// TestConvertAnthropicToOpenAIRequest_RealSamples 使用真实 Anthropic 样本测试转换
func TestConvertAnthropicToOpenAIRequest_RealSamples(t *testing.T) {
	samples := loadJSONSamples(t, "AnthropicAnalysis/AnthropicRawSamples.json")
	if len(samples) == 0 {
		t.Skip("no Anthropic samples available")
	}

	successCount := 0
	for i, sample := range samples {
		reqData, ok := sample["request"]
		if !ok {
			t.Logf("sample %d: no request data, skipping", i)
			continue
		}

		reqJSON, err := json.Marshal(reqData)
		if err != nil {
			t.Logf("sample %d: failed to marshal request: %v", i, err)
			continue
		}

		anthropicReq := mustUnmarshalAnthropicRequest(t, reqJSON)
		openAIReq, err := ConvertAnthropicToOpenAIRequest(anthropicReq)
		if err != nil {
			t.Logf("sample %d: conversion failed: %v", i, err)
			continue
		}

		// 验证转换结果
		if openAIReq.Model == "" {
			t.Errorf("sample %d: model is empty after conversion", i)
			continue
		}

		// 验证消息被正确转换
		if len(openAIReq.Messages) == 0 {
			t.Errorf("sample %d: no messages after conversion", i)
			continue
		}

		// 验证 system prompt 被正确转换
		if anthropicReq.System != nil {
			hasSystemInOpenAI := false
			for _, msg := range openAIReq.Messages {
				if msg.Role == "system" {
					hasSystemInOpenAI = true
					break
				}
			}
			if !hasSystemInOpenAI {
				t.Errorf("sample %d: Anthropic had system but OpenAI has no system message", i)
				continue
			}
		}

		// 验证流式标志
		if openAIReq.Stream != anthropicReq.Stream {
			t.Errorf("sample %d: stream flag mismatch: Anthropic=%v, OpenAI=%v", i, anthropicReq.Stream, openAIReq.Stream)
			continue
		}

		successCount++
	}

	t.Logf("Anthropic→OpenAI real sample tests: %d/%d passed", successCount, len(samples))
	if successCount == 0 {
		t.Fatalf("all real sample conversions failed")
	}
}

// ============================================================================
// 双向往返测试（Round-trip）
// ============================================================================

// TestRoundTrip_OpenAI_Anthropic_OpenAI 测试 OpenAI → Anthropic → OpenAI 往返
func TestRoundTrip_OpenAI_Anthropic_OpenAI(t *testing.T) {
	original := &OpenAIChatCompletionRequest{
		Model:     "gpt-4",
		MaxTokens: 1024,
		Stream:    false,
		Messages: []OpenAIMessage{
			{Role: "system", Content: "Be helpful."},
			{Role: "user", Content: "Hello!"},
			{Role: "assistant", Content: "Hi there!"},
			{Role: "user", Content: "What's 2+2?"},
		},
	}

	// OpenAI → Anthropic
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(original)
	if err != nil {
		t.Fatalf("OpenAI→Anthropic failed: %v", err)
	}

	// Anthropic → OpenAI
	restored, err := ConvertAnthropicToOpenAIRequest(anthropicReq)
	if err != nil {
		t.Fatalf("Anthropic→OpenAI failed: %v", err)
	}

	// 验证关键字段
	if restored.Model != original.Model {
		t.Errorf("model mismatch after round-trip: got %s, want %s", restored.Model, original.Model)
	}
	if restored.MaxTokens != original.MaxTokens {
		t.Errorf("max_tokens mismatch after round-trip: got %d, want %d", restored.MaxTokens, original.MaxTokens)
	}
	if restored.Stream != original.Stream {
		t.Errorf("stream mismatch after round-trip: got %v, want %v", restored.Stream, original.Stream)
	}

	// 验证 system prompt 被保留
	hasSystem := false
	for _, msg := range restored.Messages {
		if msg.Role == "system" {
			hasSystem = true
			content := extractStringContent(msg.Content)
			if content != "Be helpful." {
				t.Errorf("system content mismatch after round-trip: got %q", content)
			}
			break
		}
	}
	if !hasSystem {
		t.Errorf("system message lost after round-trip")
	}

	// 验证 user/assistant 消息数量
	userCount := 0
	assistantCount := 0
	for _, msg := range restored.Messages {
		switch msg.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		}
	}
	if userCount != 2 {
		t.Errorf("user message count mismatch after round-trip: got %d, want 2", userCount)
	}
	if assistantCount != 1 {
		t.Errorf("assistant message count mismatch after round-trip: got %d, want 1", assistantCount)
	}
}

// TestRoundTrip_Anthropic_OpenAI_Anthropic 测试 Anthropic → OpenAI → Anthropic 往返
func TestRoundTrip_Anthropic_OpenAI_Anthropic(t *testing.T) {
	original := &AnthropicMessagesRequest{
		Model:     "claude-3-sonnet",
		MaxTokens: 2048,
		Stream:    true,
		System:    "You are Claude.",
		Messages: []AnthropicMessage{
			{Role: "user", Content: "Hello!"},
			{Role: "assistant", Content: []AnthropicContentBlock{{Type: "text", Text: "Hi!"}}},
			{Role: "user", Content: "How are you?"},
		},
	}

	// Anthropic → OpenAI
	openAIReq, err := ConvertAnthropicToOpenAIRequest(original)
	if err != nil {
		t.Fatalf("Anthropic→OpenAI failed: %v", err)
	}

	// OpenAI → Anthropic
	restored, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("OpenAI→Anthropic failed: %v", err)
	}

	// 验证关键字段
	if restored.Model != original.Model {
		t.Errorf("model mismatch after round-trip: got %s, want %s", restored.Model, original.Model)
	}
	if restored.MaxTokens != original.MaxTokens {
		t.Errorf("max_tokens mismatch after round-trip: got %d, want %d", restored.MaxTokens, original.MaxTokens)
	}
	if restored.Stream != original.Stream {
		t.Errorf("stream mismatch after round-trip: got %v, want %v", restored.Stream, original.Stream)
	}

	// 验证 system prompt 被保留
	systemContent := extractStringContent(restored.System)
	if systemContent != "You are Claude." {
		t.Errorf("system content mismatch after round-trip: got %q, want %q", systemContent, "You are Claude.")
	}

	// 验证消息数量（不含 system）
	if len(restored.Messages) != len(original.Messages) {
		t.Errorf("messages count mismatch after round-trip: got %d, want %d", len(restored.Messages), len(original.Messages))
	}
}

// ============================================================================
// 工具调用转换测试
// ============================================================================

// TestConvertOpenAIToAnthropicRequest_WithToolCalls 测试带工具调用的 OpenAI → Anthropic 转换
func TestConvertOpenAIToAnthropicRequest_WithToolCalls(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model: "gpt-4",
		Messages: []OpenAIMessage{
			{Role: "user", Content: "What's the weather?"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []OpenAIToolCall{
					{
						ID:   "call_123",
						Type: "function",
						Function: OpenAIFunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"Beijing"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				Content:    "Sunny, 25°C",
				ToolCallID: "call_123",
			},
		},
		Tools: []OpenAITool{
			{
				Type: "function",
				Function: OpenAIFunction{
					Name:        "get_weather",
					Description: "Get weather for a location",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"location": map[string]interface{}{
								"type": "string",
							},
						},
						"required": []interface{}{"location"},
					},
				},
			},
		},
	}

	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// 验证工具定义被转换
	if len(anthropicReq.Tools) != 1 {
		t.Fatalf("tools count mismatch: got %d, want 1", len(anthropicReq.Tools))
	}
	if anthropicReq.Tools[0].Name != "get_weather" {
		t.Errorf("tool name mismatch: got %s, want get_weather", anthropicReq.Tools[0].Name)
	}

	// 验证 assistant 消息中的 tool_calls 被转为 tool_use 内容块
	var assistantMsg *AnthropicMessage
	for i := range anthropicReq.Messages {
		if anthropicReq.Messages[i].Role == "assistant" {
			assistantMsg = &anthropicReq.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message found")
	}

	blocks, ok := assistantMsg.Content.([]AnthropicContentBlock)
	if !ok {
		t.Fatalf("assistant content is not blocks: %T", assistantMsg.Content)
	}

	var foundToolUse bool
	for _, block := range blocks {
		if block.Type == "tool_use" {
			foundToolUse = true
			if block.Name != "get_weather" {
				t.Errorf("tool_use name mismatch: got %s, want get_weather", block.Name)
			}
			if block.ID != "call_123" {
				t.Errorf("tool_use id mismatch: got %s, want call_123", block.ID)
			}
		}
	}
	if !foundToolUse {
		t.Errorf("tool_use block not found in assistant message")
	}

	// 验证 tool 结果消息被转为 tool_result 内容块
	var toolResultMsg *AnthropicMessage
	for i := range anthropicReq.Messages {
		if anthropicReq.Messages[i].Role == "user" {
			// 最后一条 user 消息应该是 tool_result
			toolResultMsg = &anthropicReq.Messages[i]
		}
	}
	if toolResultMsg == nil {
		t.Fatalf("no tool result message found")
	}

	resultBlocks, ok := toolResultMsg.Content.([]AnthropicContentBlock)
	if !ok {
		t.Fatalf("tool result content is not blocks: %T", toolResultMsg.Content)
	}

	var foundToolResult bool
	for _, block := range resultBlocks {
		if block.Type == "tool_result" {
			foundToolResult = true
			if block.ToolUseID != "call_123" {
				t.Errorf("tool_result tool_use_id mismatch: got %s, want call_123", block.ToolUseID)
			}
		}
	}
	if !foundToolResult {
		t.Errorf("tool_result block not found")
	}
}

// TestConvertAnthropicToOpenAIRequest_WithToolUse 测试带工具使用的 Anthropic → OpenAI 转换
func TestConvertAnthropicToOpenAIRequest_WithToolUse(t *testing.T) {
	anthropicReq := &AnthropicMessagesRequest{
		Model: "claude-3-sonnet",
		Messages: []AnthropicMessage{
			{Role: "user", Content: "What's the weather?"},
			{
				Role: "assistant",
				Content: []AnthropicContentBlock{
					{Type: "text", Text: "I'll check the weather for you."},
					{
						Type: "tool_use",
						ID:   "tool_abc",
						Name: "get_weather",
						Input: map[string]interface{}{
							"location": "Beijing",
						},
					},
				},
			},
			{
				Role: "user",
				Content: []AnthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "tool_abc",
						Content:   "Sunny, 25°C",
					},
				},
			},
		},
		Tools: []AnthropicTool{
			{
				Name:        "get_weather",
				Description: "Get weather for a location",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type": "string",
						},
					},
					"required": []interface{}{"location"},
				},
			},
		},
	}

	openAIReq, err := ConvertAnthropicToOpenAIRequest(anthropicReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// 验证工具定义被转换
	if len(openAIReq.Tools) != 1 {
		t.Fatalf("tools count mismatch: got %d, want 1", len(openAIReq.Tools))
	}
	if openAIReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tool name mismatch: got %s, want get_weather", openAIReq.Tools[0].Function.Name)
	}

	// 验证 assistant 消息中的 tool_use 被转为 tool_calls
	var assistantMsg *OpenAIMessage
	for i := range openAIReq.Messages {
		if openAIReq.Messages[i].Role == "assistant" {
			assistantMsg = &openAIReq.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message found")
	}

	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("tool_calls count mismatch: got %d, want 1", len(assistantMsg.ToolCalls))
	}

	if assistantMsg.ToolCalls[0].ID != "tool_abc" {
		t.Errorf("tool_call id mismatch: got %s, want tool_abc", assistantMsg.ToolCalls[0].ID)
	}
	if assistantMsg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_call name mismatch: got %s, want get_weather", assistantMsg.ToolCalls[0].Function.Name)
	}
}

// ============================================================================
// 边界场景测试
// ============================================================================

// TestConvertOpenAIToAnthropicRequest_EmptyMessages 测试空消息场景
func TestConvertOpenAIToAnthropicRequest_EmptyMessages(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []OpenAIMessage{},
	}

	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	if len(anthropicReq.Messages) != 0 {
		t.Errorf("messages should be empty: got %d", len(anthropicReq.Messages))
	}
}

// TestConvertOpenAIToAnthropicRequest_NilRequest 测试 nil 请求
func TestConvertOpenAIToAnthropicRequest_NilRequest(t *testing.T) {
	_, err := ConvertOpenAIToAnthropicRequest(nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// TestConvertAnthropicToOpenAIRequest_NilRequest 测试 nil 请求
func TestConvertAnthropicToOpenAIRequest_NilRequest(t *testing.T) {
	_, err := ConvertAnthropicToOpenAIRequest(nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// TestConvertOpenAIToAnthropicRequest_MaxCompletionTokens 测试 max_completion_tokens 映射
func TestConvertOpenAIToAnthropicRequest_MaxCompletionTokens(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model:               "gpt-4",
		MaxCompletionTokens: 4096,
		MaxTokens:           1024,
		Messages: []OpenAIMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// max_completion_tokens 优先于 max_tokens
	if anthropicReq.MaxTokens != 4096 {
		t.Errorf("max_tokens should use max_completion_tokens: got %d, want 4096", anthropicReq.MaxTokens)
	}
}

// TestConvertOpenAIToAnthropicRequest_MultipleSystemMessages 测试多条 system 消息
func TestConvertOpenAIToAnthropicRequest_MultipleSystemMessages(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model: "gpt-4",
		Messages: []OpenAIMessage{
			{Role: "system", Content: "System prompt 1."},
			{Role: "system", Content: "System prompt 2."},
			{Role: "user", Content: "Hello"},
		},
	}

	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	systemContent := extractStringContent(anthropicReq.System)
	expected := "System prompt 1.\n\nSystem prompt 2."
	if systemContent != expected {
		t.Errorf("multiple system prompts not merged correctly: got %q, want %q", systemContent, expected)
	}

	// 验证 messages 中不包含 system 消息
	for _, msg := range anthropicReq.Messages {
		if msg.Role == "system" {
			t.Errorf("system message should not be in messages: %v", msg)
		}
	}
}

// TestConvertOpenAIToAnthropicRequest_ToolChoice 测试 tool_choice 转换
func TestConvertOpenAIToAnthropicRequest_ToolChoice(t *testing.T) {
	tests := []struct {
		name       string
		toolChoice interface{}
		expected   string
	}{
		{"auto string", "auto", "auto"},
		{"none string", "none", "none"},
		{"required string", "required", "any"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openAIReq := &OpenAIChatCompletionRequest{
				Model:      "gpt-4",
				ToolChoice: tt.toolChoice,
				Messages: []OpenAIMessage{
					{Role: "user", Content: "Hello"},
				},
			}

			anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
			if err != nil {
				t.Fatalf("conversion failed: %v", err)
			}

			choiceMap, ok := anthropicReq.ToolChoice.(map[string]interface{})
			if !ok {
				t.Fatalf("tool_choice is not map: %T", anthropicReq.ToolChoice)
			}

			choiceType, _ := choiceMap["type"].(string)
			if choiceType != tt.expected {
				t.Errorf("tool_choice type mismatch: got %s, want %s", choiceType, tt.expected)
			}
		})
	}
}

// TestConvertAnthropicToOpenAIRequest_ToolChoice 测试 Anthropic tool_choice 转换
func TestConvertAnthropicToOpenAIRequest_ToolChoice(t *testing.T) {
	tests := []struct {
		name       string
		toolChoice interface{}
		expected   string
	}{
		{"auto string", "auto", "auto"},
		{"none string", "none", "none"},
		{"any string", "any", "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anthropicReq := &AnthropicMessagesRequest{
				Model:      "claude-3",
				ToolChoice: tt.toolChoice,
				Messages: []AnthropicMessage{
					{Role: "user", Content: "Hello"},
				},
			}

			openAIReq, err := ConvertAnthropicToOpenAIRequest(anthropicReq)
			if err != nil {
				t.Fatalf("conversion failed: %v", err)
			}

			choiceStr, ok := openAIReq.ToolChoice.(string)
			if !ok {
				t.Fatalf("tool_choice is not string: %T", openAIReq.ToolChoice)
			}

			if choiceStr != tt.expected {
				t.Errorf("tool_choice mismatch: got %s, want %s", choiceStr, tt.expected)
			}
		})
	}
}

// ============================================================================
// JSON 序列化/反序列化测试
// ============================================================================

// TestOpenAIRequestJSONSerialization 测试 OpenAI 请求 JSON 序列化
func TestOpenAIRequestJSONSerialization(t *testing.T) {
	req := &OpenAIChatCompletionRequest{
		Model: "gpt-4",
		Messages: []OpenAIMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var restored OpenAIChatCompletionRequest
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.Model != req.Model {
		t.Errorf("model mismatch after serialization")
	}
	if len(restored.Messages) != 1 {
		t.Errorf("messages count mismatch after serialization")
	}
}

// TestAnthropicRequestJSONSerialization 测试 Anthropic 请求 JSON 序列化
func TestAnthropicRequestJSONSerialization(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Model: "claude-3",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContentBlock{{Type: "text", Text: "Hello"}}},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var restored AnthropicMessagesRequest
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.Model != req.Model {
		t.Errorf("model mismatch after serialization")
	}
}

// ============================================================================
// 协议转换分析器扩展测试
// ============================================================================

func TestConvertProtocolAnalyzerInput_RequestHeaders(t *testing.T) {
	resp, err := ConvertProtocolAnalyzerInput(ProtocolConvertAnalyzerTestRequest{
		Direction: "o2a",
		Section:   ProtocolAnalyzerSectionRequestHeaders,
		TextInput: "Authorization: Bearer sk-test\nContent-Type: application/json\nOpenAI-Beta: assistants=v2",
	})
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if resp.Format != "headers" {
		t.Fatalf("format mismatch: %s", resp.Format)
	}
	if !strings.Contains(resp.Text, "Authorization: Bearer sk-test") {
		t.Fatalf("authorization header should be preserved: %s", resp.Text)
	}
	if !strings.Contains(resp.Text, "Anthropic-Version: 2023-06-01") {
		t.Fatalf("required anthropic version missing: %s", resp.Text)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected warnings for dropped OpenAI-specific header")
	}
}

func TestConvertAnthropicToOpenAIRequestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Version", "2023-06-01")
	h.Set("Anthropic-Beta", "tools-2024-01-01")
	h.Set("Content-Type", "application/json")
	out, warnings := ConvertAnthropicToOpenAIRequestHeaders(h)
	if out.Get("Anthropic-Version") != "" || out.Get("Anthropic-Beta") != "" {
		t.Fatalf("anthropic headers should be dropped: %v", out)
	}
	if out.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type should be preserved: %v", out)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected dropped-header warnings")
	}
}

func TestConvertProtocolAnalyzerInput_ResponseHeaders(t *testing.T) {
	resp, err := ConvertProtocolAnalyzerInput(ProtocolConvertAnalyzerTestRequest{
		Direction: "a2o",
		Section:   ProtocolAnalyzerSectionResponseHeaders,
		IsStream:  true,
		TextInput: "Content-Type: application/json\nSet-Cookie: secret=1\nCache-Control: no-cache",
	})
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if !strings.Contains(resp.Text, "Content-Type: text/event-stream") {
		t.Fatalf("stream content-type not set: %s", resp.Text)
	}
	if strings.Contains(strings.ToLower(resp.Text), "set-cookie") {
		t.Fatalf("set-cookie leaked: %s", resp.Text)
	}
}

func TestAggregateOpenAISSEToResponse_Text(t *testing.T) {
	events := ParseSSEEvents("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	resp, warnings := AggregateOpenAISSEToResponse(events)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if resp.Choices[0].Message.Content != "Hello" {
		t.Fatalf("content mismatch: %#v", resp.Choices[0].Message.Content)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage mismatch: %#v", resp.Usage)
	}
}

func TestAggregateAnthropicSSEToResponse_Text(t *testing.T) {
	events := ParseSSEEvents("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-test\",\"usage\":{\"input_tokens\":4}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
	resp, warnings := AggregateAnthropicSSEToResponse(events)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if resp.ID != "msg_1" || resp.Model != "claude-test" {
		t.Fatalf("metadata mismatch: %#v", resp)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "Hi" {
		t.Fatalf("content mismatch: %#v", resp.Content)
	}
	if resp.Usage == nil || resp.Usage.InputTokens != 4 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("usage mismatch: %#v", resp.Usage)
	}
}

func TestConvertProtocolAnalyzerInput_BackwardCompatibleRequestType(t *testing.T) {
	input := json.RawMessage(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp, err := ConvertProtocolAnalyzerInput(ProtocolConvertAnalyzerTestRequest{Direction: "o2a", RequestType: "request", Input: input})
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if resp.Output == nil || resp.Metrics == nil {
		t.Fatalf("expected output and metrics: %#v", resp)
	}
}

// TestConvertProtocolRequestBody_MetricsIncludesStructureAndFieldRates 验证
// ConvertProtocolRequestBody 返回的 metrics 同时包含 structure_success_rate
// 与 field_conversion_rate（修复前两者始终为 0）。
func TestConvertProtocolRequestBody_MetricsIncludesStructureAndFieldRates(t *testing.T) {
	openAISample := []byte(`{
		"model": "gpt-4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hi"}
		],
		"max_tokens": 256,
		"temperature": 0.7
	}`)

	_, metrics, warnings, err := ConvertProtocolRequestBody(openAISample, "o2a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics == nil {
		t.Fatal("metrics should not be nil")
	}
	if metrics.StructureSuccessRate <= 0 {
		t.Errorf("structure_success_rate should be > 0, got %v", metrics.StructureSuccessRate)
	}
	if metrics.FieldConversionRate <= 0 {
		t.Errorf("field_conversion_rate should be > 0, got %v", metrics.FieldConversionRate)
	}
	if !metrics.ParsedOK {
		t.Error("parsed_ok should be true")
	}
	if !metrics.ConvertedOK {
		t.Error("converted_ok should be true")
	}
	if !metrics.OutputValid {
		t.Error("output_valid should be true")
	}
	_ = warnings
}

// TestConvertProtocolResponseBody_MetricsIncludesStructureAndFieldRates 同上，验证响应体
func TestConvertProtocolResponseBody_MetricsIncludesStructureAndFieldRates(t *testing.T) {
	openAIResp := []byte(`{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4",
		"choices": [
			{
				"index": 0,
				"message": {"role": "assistant", "content": "Hello!"},
				"finish_reason": "stop"
			}
		],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)

	_, metrics, _, err := ConvertProtocolResponseBody(openAIResp, "o2a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics == nil {
		t.Fatal("metrics should not be nil")
	}
	if metrics.StructureSuccessRate <= 0 {
		t.Errorf("structure_success_rate should be > 0, got %v", metrics.StructureSuccessRate)
	}
	if metrics.FieldConversionRate <= 0 {
		t.Errorf("field_conversion_rate should be > 0, got %v", metrics.FieldConversionRate)
	}
}

// TestConvertProtocolRequestBody_MetricsAnthropicToOpenAI 验证 a2o 方向同样有完整指标
func TestConvertProtocolRequestBody_MetricsAnthropicToOpenAI(t *testing.T) {
	anthropicSample := []byte(`{
		"model": "claude-3-5-sonnet-20241022",
		"max_tokens": 1024,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": "Hello"}
		]
	}`)

	_, metrics, _, err := ConvertProtocolRequestBody(anthropicSample, "a2o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics == nil {
		t.Fatal("metrics should not be nil")
	}
	if metrics.StructureSuccessRate <= 0 {
		t.Errorf("structure_success_rate should be > 0, got %v", metrics.StructureSuccessRate)
	}
	if metrics.FieldConversionRate <= 0 {
		t.Errorf("field_conversion_rate should be > 0, got %v", metrics.FieldConversionRate)
	}
}
