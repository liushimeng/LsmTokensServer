package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// ============================================================================
// v2.0.72 协议转换优化测试
// 锁定 docs/LsmHttpAgent_OpenAI_Anthropic协议转换优化方案_20260813_01.md 的修复项。
// 关键原则：全部用真实线格式 JSON 字符串作为输入，禁止用 Go struct 构造输入
// 绕过 unmarshal 路径（v2.0.72 之前的嵌套建模缺陷正是因此被既有测试放过的）。
// ============================================================================

// TestV2072_AnthropicWireFormat_ToolUseFlatUnmarshal 平铺 tool_use/tool_result 线格式必须能反序列化（P0-1 反向断言）
func TestV2072_AnthropicWireFormat_ToolUseFlatUnmarshal(t *testing.T) {
	wire := `{"type":"tool_use","id":"toolu_01ABC","name":"get_weather","input":{"location":"Beijing"}}`
	var block AnthropicContentBlock
	if err := json.Unmarshal([]byte(wire), &block); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if block.Type != "tool_use" || block.ID != "toolu_01ABC" || block.Name != "get_weather" {
		t.Errorf("flat tool_use fields lost: %+v", block)
	}
	if block.Input["location"] != "Beijing" {
		t.Errorf("tool_use input lost: %+v", block.Input)
	}

	wireResult := `{"type":"tool_result","tool_use_id":"toolu_01ABC","content":"Sunny","is_error":true}`
	var resultBlock AnthropicContentBlock
	if err := json.Unmarshal([]byte(wireResult), &resultBlock); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resultBlock.ToolUseID != "toolu_01ABC" || resultBlock.Content != "Sunny" || !resultBlock.IsError {
		t.Errorf("flat tool_result fields lost: %+v", resultBlock)
	}
}

// TestV2072_A2OResponse_ToolCallsSurvive 真实 Anthropic 响应 JSON → OpenAI 响应 tool_calls 不丢（P0-1）
func TestV2072_A2OResponse_ToolCallsSurvive(t *testing.T) {
	wire := `{
		"id": "msg_01XyZ", "type": "message", "role": "assistant", "model": "claude-sonnet-4-5",
		"content": [
			{"type": "text", "text": "Let me check."},
			{"type": "tool_use", "id": "toolu_01A", "name": "get_weather", "input": {"location": "Beijing"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 12, "output_tokens": 30}
	}`
	var anthropicResp AnthropicMessagesResponse
	if err := json.Unmarshal([]byte(wire), &anthropicResp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIResp, err := ConvertAnthropicToOpenAIResponse(&anthropicResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	choice := openAIResp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason mismatch: got %q, want tool_calls", choice.FinishReason)
	}
	// finish_reason=tool_calls 与 tool_calls 必须同时在场（此前 tool_calls 丢失、Agent 工具循环必断）
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls lost: got %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "toolu_01A" || tc.Function.Name != "get_weather" {
		t.Errorf("tool_call fields mismatch: %+v", tc)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || args["location"] != "Beijing" {
		t.Errorf("tool_call arguments mismatch: %q", tc.Function.Arguments)
	}
}

// TestV2072_O2AResponse_ToolUseFlatSerialize o2a 响应输出平铺 tool_use，无嵌套 "tool_use" key（P0-1）
func TestV2072_O2AResponse_ToolUseFlatSerialize(t *testing.T) {
	openAIResp := &OpenAIChatCompletionResponse{
		ID: "chatcmpl_abc", Object: "chat.completion", Model: "gpt-4",
		Choices: []OpenAIChoice{{
			Index: 0,
			Message: &OpenAIMessage{
				Role: "assistant",
				ToolCalls: []OpenAIToolCall{{
					ID: "call_1", Type: "function",
					Function: OpenAIFunctionCall{Name: "get_weather", Arguments: `{"location":"Beijing"}`},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
	anthropicResp, err := ConvertOpenAIToAnthropicResponse(openAIResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	out, err := json.Marshal(anthropicResp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	content, _ := raw["content"].([]interface{})
	if len(content) != 1 {
		t.Fatalf("content count mismatch: got %d, want 1; body=%s", len(content), out)
	}
	block, _ := content[0].(map[string]interface{})
	if block["type"] != "tool_use" {
		t.Fatalf("block type mismatch: %v", block["type"])
	}
	if _, nested := block["tool_use"]; nested {
		t.Errorf("nested tool_use key present (illegal wire format): %s", out)
	}
	if block["id"] == "" || block["name"] != "get_weather" {
		t.Errorf("flat id/name missing: %s", out)
	}
	input, _ := block["input"].(map[string]interface{})
	if input["location"] != "Beijing" {
		t.Errorf("flat input missing: %s", out)
	}
}

// TestV2072_A2ORequest_ToolResultSplitsToToolMessages 混合内容 Anthropic 消息 → 独立 role=tool 消息（P0-4）
func TestV2072_A2ORequest_ToolResultSplitsToToolMessages(t *testing.T) {
	wire := `{
		"model": "claude-sonnet-4-5", "max_tokens": 1024,
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_01A", "content": "Sunny, 25C"},
				{"type": "tool_result", "tool_use_id": "toolu_02B", "content": [{"type": "text", "text": "Rainy"}], "is_error": true},
				{"type": "text", "text": "What should I wear?"}
			]}
		]
	}`
	var anthropicReq AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(wire), &anthropicReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIReq, err := ConvertAnthropicToOpenAIRequest(&anthropicReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	var toolMsgs, userMsgs []OpenAIMessage
	for _, m := range openAIReq.Messages {
		switch m.Role {
		case "tool":
			toolMsgs = append(toolMsgs, m)
		case "user":
			userMsgs = append(userMsgs, m)
		}
	}
	if len(toolMsgs) != 2 {
		t.Fatalf("tool message count mismatch: got %d, want 2 (all: %+v)", len(toolMsgs), openAIReq.Messages)
	}
	if toolMsgs[0].ToolCallID != "toolu_01A" || toolMsgs[0].Content != "Sunny, 25C" {
		t.Errorf("first tool message mismatch: %+v", toolMsgs[0])
	}
	if toolMsgs[1].ToolCallID != "toolu_02B" {
		t.Errorf("second tool message tool_call_id mismatch: %+v", toolMsgs[1])
	}
	// is_error 语义保留 + 数组 content 拍平
	content, _ := toolMsgs[1].Content.(string)
	if !strings.HasPrefix(content, "[ERROR] ") || !strings.Contains(content, "Rainy") {
		t.Errorf("is_error/array content not preserved: %q", content)
	}
	if len(userMsgs) != 1 || userMsgs[0].Content != "What should I wear?" {
		t.Errorf("user text message mismatch: %+v", userMsgs)
	}
}

// TestV2072_O2ARequest_ImageURLToSource image_url 三形态 → 合法 image source（P0-5）
func TestV2072_O2ARequest_ImageURLToSource(t *testing.T) {
	wire := `{
		"model": "gpt-4o", "max_tokens": 100,
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "describe these"},
			{"type": "image_url", "image_url": {"url": "https://example.com/a.png"}},
			{"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,QUJD"}},
			{"type": "image_url", "image_url": "https://example.com/b.png"}
		]}]
	}`
	var openAIReq OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(wire), &openAIReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(&openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	blocks, ok := anthropicReq.Messages[0].Content.([]AnthropicContentBlock)
	if !ok || len(blocks) != 4 {
		t.Fatalf("blocks mismatch: %T %+v", anthropicReq.Messages[0].Content, anthropicReq.Messages[0].Content)
	}
	// url source
	if blocks[1].Type != "image" || blocks[1].Source["type"] != "url" || blocks[1].Source["url"] != "https://example.com/a.png" {
		t.Errorf("url image source mismatch: %+v", blocks[1])
	}
	// base64 source（data URI 拆解）
	if blocks[2].Type != "image" || blocks[2].Source["type"] != "base64" || blocks[2].Source["media_type"] != "image/jpeg" || blocks[2].Source["data"] != "QUJD" {
		t.Errorf("base64 image source mismatch: %+v", blocks[2])
	}
	// 裸字符串 url
	if blocks[3].Type != "image" || blocks[3].Source["url"] != "https://example.com/b.png" {
		t.Errorf("bare string image source mismatch: %+v", blocks[3])
	}
}

// TestV2072_A2ORequest_ImageBlockToImageURL Anthropic image 块 → OpenAI image_url part（P0-5 反向）
func TestV2072_A2ORequest_ImageBlockToImageURL(t *testing.T) {
	wire := `{
		"model": "claude-sonnet-4-5", "max_tokens": 100,
		"messages": [{"role": "user", "content": [
			{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "QUJD"}},
			{"type": "image", "source": {"type": "url", "url": "https://example.com/c.png"}}
		]}]
	}`
	var anthropicReq AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(wire), &anthropicReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIReq, err := ConvertAnthropicToOpenAIRequest(&anthropicReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	parts, ok := openAIReq.Messages[0].Content.([]interface{})
	if !ok || len(parts) != 2 {
		t.Fatalf("content parts mismatch: %T %+v", openAIReq.Messages[0].Content, openAIReq.Messages[0].Content)
	}
	part0, _ := parts[0].(map[string]interface{})
	imageURL0, _ := part0["image_url"].(map[string]interface{})
	if part0["type"] != "image_url" || imageURL0["url"] != "data:image/png;base64,QUJD" {
		t.Errorf("base64 → data URI mismatch: %+v", part0)
	}
	part1, _ := parts[1].(map[string]interface{})
	imageURL1, _ := part1["image_url"].(map[string]interface{})
	if imageURL1["url"] != "https://example.com/c.png" {
		t.Errorf("url passthrough mismatch: %+v", part1)
	}
}

// TestV2072_ParseSSEEvents_CRLF CRLF 流事件正确切分 + 流尾残留事件不丢（P0-6）
func TestV2072_ParseSSEEvents_CRLF(t *testing.T) {
	crlf := "event: message_start\r\ndata: {\"type\":\"message_start\"}\r\n\r\nevent: content_block_delta\r\ndata: {\"type\":\"content_block_delta\"}\r\n\r\n"
	events := ParseSSEEvents(crlf)
	if len(events) != 2 {
		t.Fatalf("CRLF events count mismatch: got %d, want 2 (%+v)", len(events), events)
	}
	if events[0].Event != "message_start" || events[1].Event != "content_block_delta" {
		t.Errorf("CRLF event types mismatch: %+v", events)
	}
	// 流尾无末尾空行：残留事件必须保留
	tail := "data: {\"type\":\"message_stop\"}"
	events = ParseSSEEvents(tail)
	if len(events) != 1 || !strings.Contains(events[0].Data, "message_stop") {
		t.Errorf("trailing event lost: %+v", events)
	}
	// LF 流不回归
	lf := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\n"
	events = ParseSSEEvents(lf)
	if len(events) != 2 {
		t.Errorf("LF events count mismatch: got %d, want 2", len(events))
	}
}


// TestV2072_O2ARequest_DefaultMaxTokens 双字段缺失 → max_tokens 默认值（P1-8）
func TestV2072_O2ARequest_DefaultMaxTokens(t *testing.T) {
	wire := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	var openAIReq OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(wire), &openAIReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(&openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if anthropicReq.MaxTokens != o2aDefaultMaxTokens {
		t.Errorf("max_tokens default mismatch: got %d, want %d", anthropicReq.MaxTokens, o2aDefaultMaxTokens)
	}
	// 序列化后 max_tokens 不得为 0（Anthropic 必填校验会拒绝 0/缺失）
	out, _ := json.Marshal(anthropicReq)
	var raw map[string]interface{}
	_ = json.Unmarshal(out, &raw)
	if mt, _ := raw["max_tokens"].(float64); mt <= 0 {
		t.Errorf("serialized max_tokens invalid: %v", raw["max_tokens"])
	}
}

// TestV2072_TemperatureZero_Preserved 显式 temperature:0 转换后仍为 0（P1-9）
func TestV2072_TemperatureZero_Preserved(t *testing.T) {
	wire := `{"model":"gpt-4","temperature":0,"top_p":0,"messages":[{"role":"user","content":"hi"}]}`
	var openAIReq OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(wire), &openAIReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if openAIReq.Temperature == nil || *openAIReq.Temperature != 0 {
		t.Fatalf("unmarshal lost explicit zero temperature: %+v", openAIReq.Temperature)
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(&openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	out, _ := json.Marshal(anthropicReq)
	var raw map[string]interface{}
	_ = json.Unmarshal(out, &raw)
	if temp, ok := raw["temperature"]; !ok || temp != 0.0 {
		t.Errorf("explicit temperature=0 dropped: %s", out)
	}
	if topP, ok := raw["top_p"]; !ok || topP != 0.0 {
		t.Errorf("explicit top_p=0 dropped: %s", out)
	}
}

// TestV2072_O2ARequest_ConsecutiveToolMessagesMerge 连续 tool 消息合并为一条 user 消息（P1-10）
func TestV2072_O2ARequest_ConsecutiveToolMessagesMerge(t *testing.T) {
	wire := `{
		"model": "gpt-4",
		"messages": [
			{"role": "user", "content": "weather everywhere?"},
			{"role": "assistant", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"location\":\"BJ\"}"}},
				{"id": "call_2", "type": "function", "function": {"name": "get_weather", "arguments": "{\"location\":\"SH\"}"}},
				{"id": "call_3", "type": "function", "function": {"name": "get_weather", "arguments": "{\"location\":\"GZ\"}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny"},
			{"role": "tool", "tool_call_id": "call_2", "content": "rainy"},
			{"role": "tool", "tool_call_id": "call_3", "content": "cloudy"}
		]
	}`
	var openAIReq OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(wire), &openAIReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(&openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	// 期望：user / assistant / 单条合并 user（3 个 tool_result 块）
	if len(anthropicReq.Messages) != 3 {
		t.Fatalf("messages count mismatch: got %d, want 3 (%+v)", len(anthropicReq.Messages), anthropicReq.Messages)
	}
	if anthropicReq.Messages[1].Role != "assistant" || anthropicReq.Messages[2].Role != "user" {
		t.Fatalf("role alternation broken: %+v", anthropicReq.Messages)
	}
	blocks, ok := anthropicReq.Messages[2].Content.([]AnthropicContentBlock)
	if !ok || len(blocks) != 3 {
		t.Fatalf("merged tool_result blocks mismatch: %T %+v", anthropicReq.Messages[2].Content, anthropicReq.Messages[2].Content)
	}
	for i, id := range []string{"call_1", "call_2", "call_3"} {
		if blocks[i].Type != "tool_result" || blocks[i].ToolUseID != id {
			t.Errorf("merged block %d mismatch: %+v", i, blocks[i])
		}
	}
}

// TestV2072_O2ARequest_BadArgumentsWrappedRaw 非法 JSON arguments 包 {"raw": ...}（P1-11）
func TestV2072_O2ARequest_BadArgumentsWrappedRaw(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model: "gpt-4",
		Messages: []OpenAIMessage{{
			Role: "assistant",
			ToolCalls: []OpenAIToolCall{
				{ID: "call_bad", Type: "function", Function: OpenAIFunctionCall{Name: "f", Arguments: `{not json`}},
				{ID: "call_empty", Type: "function", Function: OpenAIFunctionCall{Name: "g", Arguments: ""}},
			},
		}},
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	// v2.0.73: ensureLeadingUserMessage 可能前置 synthetic user 消息，按 role 查找 assistant
	blocks := assistantContentBlocks(t, anthropicReq)
	if len(blocks) != 2 {
		t.Fatalf("blocks count mismatch: %d", len(blocks))
	}
	if raw, ok := blocks[0].Input["raw"]; !ok || raw != `{not json` {
		t.Errorf("bad arguments should be wrapped as raw: %+v", blocks[0].Input)
	}
	if len(blocks[1].Input) != 0 {
		t.Errorf("empty arguments should produce empty object: %+v", blocks[1].Input)
	}
}

// TestV2072_O2ARequest_ToolUseIDSanitized 缺失/非法 id 处理（确定性 id + 非法字符清洗）
func TestV2072_O2ARequest_ToolUseIDSanitized(t *testing.T) {
	openAIReq := &OpenAIChatCompletionRequest{
		Model: "gpt-4",
		Messages: []OpenAIMessage{{
			Role: "assistant",
			ToolCalls: []OpenAIToolCall{
				{ID: "", Type: "function", Function: OpenAIFunctionCall{Name: "f", Arguments: "{}"}},
				{ID: "call abc:1", Type: "function", Function: OpenAIFunctionCall{Name: "g", Arguments: "{}"}},
			},
		}},
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	blocks := assistantContentBlocks(t, anthropicReq)
	if blocks[0].ID != "toolu_o2a_00000000" {
		t.Errorf("missing id should get deterministic id: %q", blocks[0].ID)
	}
	if blocks[1].ID != "call_abc_1" {
		t.Errorf("illegal chars should be replaced with _: %q", blocks[1].ID)
	}
}

// assistantContentBlocks 在转换后的 Anthropic 请求中查找 assistant 消息的内容块数组
// （v2.0.73: ensureLeadingUserMessage 可能前置 synthetic user 消息，按 role 查找更稳健）
func assistantContentBlocks(t *testing.T, req *AnthropicMessagesRequest) []AnthropicContentBlock {
	t.Helper()
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			blocks, ok := m.Content.([]AnthropicContentBlock)
			if !ok {
				t.Fatalf("assistant content is not []AnthropicContentBlock: %T", m.Content)
			}
			return blocks
		}
	}
	t.Fatalf("no assistant message found in %d messages", len(req.Messages))
	return nil
}

// TestV2072_A2OResponse_CreatedAndIDPrefix Created>0 且 id 前缀 chatcmpl_（P1-12）
func TestV2072_A2OResponse_CreatedAndIDPrefix(t *testing.T) {
	wire := `{"id":"msg_01XYZ","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	var anthropicResp AnthropicMessagesResponse
	if err := json.Unmarshal([]byte(wire), &anthropicResp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIResp, err := ConvertAnthropicToOpenAIResponse(&anthropicResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if openAIResp.Created <= 0 {
		t.Errorf("created should be current unix time: got %d", openAIResp.Created)
	}
	if !strings.HasPrefix(openAIResp.ID, "chatcmpl_") {
		t.Errorf("id prefix should be chatcmpl_: got %q", openAIResp.ID)
	}
	if openAIResp.ID != "chatcmpl_01XYZ" {
		t.Errorf("id rewrite mismatch: got %q", openAIResp.ID)
	}
}

// TestV2072_StopFinishReason_NewMappings 新增 stop/finish 映射 + 未识别透传（P1-13）
func TestV2072_StopFinishReason_NewMappings(t *testing.T) {
	cases := []struct{ anthropic, openai string }{
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "tool_calls"},
		{"stop_sequence", "stop"},
		{"refusal", "content_filter"},
		{"pause_turn", "stop"},
		{"model_context_window_exceeded", "length"},
	}
	for _, c := range cases {
		if got := mapAnthropicStopReasonToOpenAI(c.anthropic); got != c.openai {
			t.Errorf("a2o stop mapping %q: got %q, want %q", c.anthropic, got, c.openai)
		}
	}
	o2aCases := []struct{ openai, anthropic string }{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"function_call", "tool_use"},
		{"content_filter", "refusal"},
	}
	for _, c := range o2aCases {
		if got := mapOpenAIFinishReasonToAnthropic(c.openai); got != c.anthropic {
			t.Errorf("o2a finish mapping %q: got %q, want %q", c.openai, got, c.anthropic)
		}
	}
	// 未识别值原样透传（此前直接消失）
	if got := mapAnthropicStopReasonToOpenAI("some_future_reason"); got != "some_future_reason" {
		t.Errorf("unknown anthropic stop_reason should pass through: got %q", got)
	}
	if got := mapOpenAIFinishReasonToAnthropic("some_future_reason"); got != "some_future_reason" {
		t.Errorf("unknown openai finish_reason should pass through: got %q", got)
	}
	// 空串保持空串
	if got := mapAnthropicStopReasonToOpenAI(""); got != "" {
		t.Errorf("empty stop_reason should stay empty: got %q", got)
	}
}

// TestV2072_A2ORequest_SystemArrayForm 数组形态 system 拼接文本（P1-14）
func TestV2072_A2ORequest_SystemArrayForm(t *testing.T) {
	wire := `{
		"model": "claude-sonnet-4-5", "max_tokens": 100,
		"system": [
			{"type": "text", "text": "You are helpful.", "cache_control": {"type": "ephemeral"}},
			{"type": "text", "text": "Be concise."}
		],
		"messages": [{"role": "user", "content": "hi"}]
	}`
	var anthropicReq AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(wire), &anthropicReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIReq, err := ConvertAnthropicToOpenAIRequest(&anthropicReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if len(openAIReq.Messages) < 2 || openAIReq.Messages[0].Role != "system" {
		t.Fatalf("system message missing: %+v", openAIReq.Messages)
	}
	content, _ := openAIReq.Messages[0].Content.(string)
	if !strings.Contains(content, "You are helpful.") || !strings.Contains(content, "Be concise.") {
		t.Errorf("system array not flattened to text: %q", content)
	}
	if strings.Contains(content, "cache_control") || strings.Contains(content, `"type"`) {
		t.Errorf("system array was JSON-dumped instead of text-extracted: %q", content)
	}
}

// TestV2072_O2ARequest_UnknownRoleCoercesToUser 未知 role 归并为 user（P1-15）
func TestV2072_O2ARequest_UnknownRoleCoercesToUser(t *testing.T) {
	wire := `{"model":"gpt-4","messages":[{"role":"api","content":"legacy payload"}]}`
	var openAIReq OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(wire), &openAIReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	anthropicReq, err := ConvertOpenAIToAnthropicRequest(&openAIReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if len(anthropicReq.Messages) != 1 || anthropicReq.Messages[0].Role != "user" {
		t.Errorf("unknown role should coerce to user: %+v", anthropicReq.Messages)
	}
}

// TestV2072_OpenAISSE_ToolCallIndexField 稀疏/乱序 index 正确分桶（P1-16）
func TestV2072_OpenAISSE_ToolCallIndexField(t *testing.T) {
	// 乱序到达：index=1 的 delta 先于 index=0 的部分 delta
	sse := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_b\",\"type\":\"function\",\"function\":{\"name\":\"gb\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"type\":\"function\",\"function\":{\"name\":\"ga\",\"arguments\":\"{\"}}]}}]}\n\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"1\"}}]}}]}\n\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	events := ParseSSEEvents(sse)
	resp, warnings := AggregateOpenAISSEToResponse(events)
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	toolCalls := resp.Choices[0].Message.ToolCalls
	if len(toolCalls) != 2 {
		t.Fatalf("tool_calls count mismatch: got %d, want 2 (%+v)", len(toolCalls), toolCalls)
	}
	// 按 index 排序：call_a (index 0) 在前
	if toolCalls[0].ID != "call_a" || toolCalls[0].Function.Arguments != "{}" {
		t.Errorf("tool_call index 0 mismatch: %+v", toolCalls[0])
	}
	if toolCalls[1].ID != "call_b" || toolCalls[1].Function.Arguments != "1" {
		t.Errorf("tool_call index 1 mismatch: %+v", toolCalls[1])
	}
}

// TestV2072_Usage_CacheTokensBothDirections cache token 字段双向映射（P1-17）
func TestV2072_Usage_CacheTokensBothDirections(t *testing.T) {
	// a2o：input + cache_read + cache_creation 加回 prompt_tokens
	wire := `{"id":"msg_1","type":"message","role":"assistant","model":"c","content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":50}}`
	var anthropicResp AnthropicMessagesResponse
	if err := json.Unmarshal([]byte(wire), &anthropicResp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIResp, err := ConvertAnthropicToOpenAIResponse(&anthropicResp)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if openAIResp.Usage.PromptTokens != 180 {
		t.Errorf("prompt_tokens should include cache tokens: got %d, want 180", openAIResp.Usage.PromptTokens)
	}
	if openAIResp.Usage.PromptTokensDetails == nil || openAIResp.Usage.PromptTokensDetails.CachedTokens != 50 {
		t.Errorf("cached_tokens mismatch: %+v", openAIResp.Usage.PromptTokensDetails)
	}
	if openAIResp.Usage.TotalTokens != 200 {
		t.Errorf("total_tokens mismatch: got %d, want 200", openAIResp.Usage.TotalTokens)
	}

	// o2a：cached_tokens 从 prompt_tokens 拆出
	openAIResp2 := &OpenAIChatCompletionResponse{
		ID: "chatcmpl_1", Object: "chat.completion", Model: "m",
		Choices: []OpenAIChoice{{Index: 0, Message: &OpenAIMessage{Role: "assistant", Content: "x"}, FinishReason: "stop"}},
		Usage:   &OpenAIUsage{PromptTokens: 180, CompletionTokens: 20, TotalTokens: 200, PromptTokensDetails: &OpenAIPromptTokensDetails{CachedTokens: 50}},
	}
	anthropicResp2, err := ConvertOpenAIToAnthropicResponse(openAIResp2)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if anthropicResp2.Usage.InputTokens != 130 {
		t.Errorf("input_tokens should exclude cached: got %d, want 130", anthropicResp2.Usage.InputTokens)
	}
	if anthropicResp2.Usage.CacheReadInputTokens != 50 {
		t.Errorf("cache_read_input_tokens mismatch: got %d, want 50", anthropicResp2.Usage.CacheReadInputTokens)
	}
}

// TestV2072_A2ORequest_MetadataUserID metadata.user_id → user（映射表一致性）
func TestV2072_A2ORequest_MetadataUserID(t *testing.T) {
	wire := `{"model":"claude","max_tokens":10,"metadata":{"user_id":"u-123"},"messages":[{"role":"user","content":"hi"}]}`
	var anthropicReq AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(wire), &anthropicReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	openAIReq, err := ConvertAnthropicToOpenAIRequest(&anthropicReq)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if openAIReq.User != "u-123" {
		t.Errorf("metadata.user_id should map to user: got %q", openAIReq.User)
	}
}

// TestV2072_AnthropicSSE_ToolUseAggregation Anthropic SSE 聚合产出平铺 tool_use（P0-1 流式路径回归）
func TestV2072_AnthropicSSE_ToolUseAggregation(t *testing.T) {
	sse := "event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"location\\\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\":\\\"BJ\\\"}\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":9}}\n\n"
	events := ParseSSEEvents(sse)
	resp, warnings := AggregateAnthropicSSEToResponse(events)
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" {
		t.Fatalf("tool_use block missing: %+v", resp.Content)
	}
	block := resp.Content[0]
	if block.ID != "toolu_1" || block.Name != "get_weather" {
		t.Errorf("flat tool_use fields mismatch: %+v", block)
	}
	if block.Input["location"] != "BJ" {
		t.Errorf("partial_json reassembly failed: %+v", block.Input)
	}
	// 聚合结果再转 OpenAI：tool_calls 必须在场（端到端不断链）
	openAIResp, err := ConvertAnthropicToOpenAIResponse(resp)
	if err != nil {
		t.Fatalf("a2o response conversion failed: %v", err)
	}
	if len(openAIResp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls lost after SSE aggregate + convert: %+v", openAIResp.Choices[0].Message)
	}
}
