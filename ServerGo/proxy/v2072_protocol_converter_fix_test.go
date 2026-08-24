package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/protocol"
)

// ============================================================================
// v2.0.72: 从 protocol/v2072_protocol_converter_fix_test.go 拆分出的 proxy 侧测试，
// 因 wrapAnthropicResponseAsSSE / wrapConvertedResponseAsSSE 位于 proxy 包。
// ============================================================================

// TestV2072_WrapAnthropicResponseAsSSE_ToolUse 伪流式包装 tool_use 携带 id/name/input_json_delta（P0-7）
func TestV2072_WrapAnthropicResponseAsSSE_ToolUse(t *testing.T) {
	resp := protocol.AnthropicMessagesResponse{
		ID: "msg_x", Type: "message", Role: "assistant", Model: "claude",
		Content: []protocol.AnthropicContentBlock{{
			Type: "tool_use", ID: "toolu_1", Name: "get_weather",
			Input: map[string]interface{}{"location": "Beijing"},
		}},
		StopReason: "tool_use",
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	sse, err := wrapAnthropicResponseAsSSE(body)
	if err != nil {
		t.Fatalf("wrap failed: %v", err)
	}
	out := string(sse)
	if !strings.Contains(out, `"id":"toolu_1"`) || !strings.Contains(out, `"name":"get_weather"`) {
		t.Errorf("content_block_start missing id/name:\n%s", out)
	}
	if !strings.Contains(out, "input_json_delta") || !strings.Contains(out, "Beijing") {
		t.Errorf("input_json_delta missing:\n%s", out)
	}
	// 事件序列必须可被 protocol.ParseSSEEvents 解析且含完整生命周期
	events := protocol.ParseSSEEvents(out)
	var sawStart, sawDelta, sawStop, sawMsgStop bool
	for _, ev := range events {
		switch ev.Event {
		case "message_start":
			sawStart = true
		case "content_block_delta":
			sawDelta = true
		case "content_block_stop":
			sawStop = true
		case "message_stop":
			sawMsgStop = true
		}
	}
	if !sawStart || !sawDelta || !sawStop || !sawMsgStop {
		t.Errorf("incomplete event lifecycle: start=%v delta=%v stop=%v msgStop=%v", sawStart, sawDelta, sawStop, sawMsgStop)
	}
}

// TestV2072_WrapAnthropicResponseAsSSE_EmptyContent 空 content 补空 text 块（合法 Anthropic 流）
func TestV2072_WrapAnthropicResponseAsSSE_EmptyContent(t *testing.T) {
	sse, err := wrapAnthropicResponseAsSSE([]byte(`{"id":"msg_e","type":"message","role":"assistant","model":"c","content":[],"stop_reason":"end_turn"}`))
	if err != nil {
		t.Fatalf("wrap failed: %v", err)
	}
	out := string(sse)
	if !strings.Contains(out, "content_block_start") || !strings.Contains(out, `"type":"text"`) {
		t.Errorf("empty content should emit an empty text block:\n%s", out)
	}
}

// TestV2072_ConvertProxyResponse_StreamToStream_SelfConsistent 上游 SSE → 客户端收到合法目标协议 SSE（P0-3）
func TestV2072_ConvertProxyResponse_StreamToStream_SelfConsistent(t *testing.T) {
	// Anthropic 上游 SSE → OpenAI 客户端（a2o 响应方向）
	upstreamSSE := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	converted, warnings, err := protocol.ConvertProtocolResponseSSE(upstreamSSE, "a2o")
	if err != nil {
		t.Fatalf("SSE conversion failed: %v", err)
	}
	_ = warnings
	convertedBody, err := protocol.MarshalConvertedProtocolBody(converted)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	// 模拟 convertProxyResponse 的包装分支：必须产出合法 OpenAI SSE（含 [DONE]）
	wrapped, err := wrapConvertedResponseAsSSE(convertedBody, protocol.AgentProtocolType_OpenAI)
	if err != nil {
		t.Fatalf("wrap failed: %v", err)
	}
	out := string(wrapped)
	if !strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Errorf("OpenAI SSE must end with [DONE]:\n%s", out)
	}
	events := protocol.ParseSSEEvents(out)
	if len(events) == 0 {
		t.Fatalf("no parseable SSE events in wrapped output:\n%s", out)
	}
	for _, ev := range events {
		if strings.TrimSpace(ev.Data) == "[DONE]" {
			continue
		}
		var chunk protocol.OpenAIStreamResponse
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			t.Errorf("invalid OpenAI chunk JSON: %v; data=%s", err, ev.Data)
		}
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("converted content missing from SSE:\n%s", out)
	}

	// 反向：OpenAI 上游 SSE → Anthropic 客户端（o2a 响应方向）
	openAISSE := "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	converted2, _, err := protocol.ConvertProtocolResponseSSE(openAISSE, "o2a")
	if err != nil {
		t.Fatalf("SSE conversion failed: %v", err)
	}
	convertedBody2, err := protocol.MarshalConvertedProtocolBody(converted2)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	wrapped2, err := wrapConvertedResponseAsSSE(convertedBody2, protocol.AgentProtocolType_Anthropic)
	if err != nil {
		t.Fatalf("wrap failed: %v", err)
	}
	out2 := string(wrapped2)
	events2 := protocol.ParseSSEEvents(out2)
	var sawMsgStart, sawMsgStop2 bool
	for _, ev := range events2 {
		if ev.Event == "message_start" {
			sawMsgStart = true
		}
		if ev.Event == "message_stop" {
			sawMsgStop2 = true
		}
	}
	if !sawMsgStart || !sawMsgStop2 {
		t.Errorf("Anthropic SSE lifecycle incomplete: start=%v stop=%v\n%s", sawMsgStart, sawMsgStop2, out2)
	}
	if !strings.Contains(out2, "Hi") {
		t.Errorf("converted content missing from Anthropic SSE:\n%s", out2)
	}
}
