package models

import (
	"strings"
	"testing"
)

func TestProtocolConverterLearningOpenAIToAnthropicRecord(t *testing.T) {
	detail := &ProtocolConvertAnalyzerRecordDetail{
		ProtocolType:    2,
		RequestHeaders:  "Content-Type: application/json\nAuthorization: Bearer sk-test-secret\nOpenAI-Beta: assistants=v2",
		ResponseHeaders: "Content-Type: application/json\nopenai-processing-ms: 12",
		RequestBody: `{
			"model":"gpt-4.1",
			"messages":[
				{"role":"system","content":"be concise"},
				{"role":"user","content":"hello"}
			],
			"tools":[{"type":"function","function":{"name":"read_file","description":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}],
			"tool_choice":"auto",
			"max_tokens":128,
			"temperature":0.2
		}`,
		ResponseBody: `{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"model":"gpt-4.1",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}
		}`,
	}

	conversion := BuildProtocolConvertAnalyzerRecordConversion(detail, "o2a")
	if conversion.RequestBody.Error != "" {
		t.Fatalf("request body conversion failed: %s", conversion.RequestBody.Error)
	}
	if !strings.Contains(conversion.RequestBody.Output, `"system": "be concise"`) {
		t.Fatalf("system prompt not converted: %s", conversion.RequestBody.Output)
	}
	if !strings.Contains(conversion.RequestBody.Output, `"name": "read_file"`) || !strings.Contains(conversion.RequestBody.Output, `"input_schema"`) {
		t.Fatalf("tool schema not converted: %s", conversion.RequestBody.Output)
	}
	if strings.Contains(conversion.RequestHeaders.Input, "sk-test-secret") || !strings.Contains(conversion.RequestHeaders.Input, "Authorization: Bearer "+AuthorizationBearerAPIKeyMask) {
		t.Fatalf("authorization header should be masked in input: %s", conversion.RequestHeaders.Input)
	}
	if strings.Contains(conversion.RequestHeaders.Output, "sk-test-secret") || !strings.Contains(conversion.RequestHeaders.Output, "Authorization: Bearer "+AuthorizationBearerAPIKeyMask) {
		t.Fatalf("authorization header should be masked in output: %s", conversion.RequestHeaders.Output)
	}
	if !strings.Contains(conversion.ResponseBody.Output, `"type": "message"`) {
		t.Fatalf("response not converted: %s", conversion.ResponseBody.Output)
	}
}

func TestProtocolConverterLearningAnthropicToOpenAIRecord(t *testing.T) {
	detail := &ProtocolConvertAnalyzerRecordDetail{
		ProtocolType:    1,
		RequestHeaders:  "Content-Type: application/json\nx-api-key: sk-ant-secret\nAnthropic-Version: 2023-06-01",
		ResponseHeaders: "Content-Type: application/json\nanthropic-ratelimit-requests-limit: 100",
		RequestBody: `{
			"model":"claude-3-5-sonnet",
			"system":"be accurate",
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],
			"tools":[{"name":"grep","description":"search","input_schema":{"type":"object","properties":{"pattern":{"type":"string"}}}}],
			"max_tokens":256,
			"temperature":0.1
		}`,
		ResponseBody: `{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-3-5-sonnet",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":8,"output_tokens":2}
		}`,
	}

	conversion := BuildProtocolConvertAnalyzerRecordConversion(detail, "a2o")
	if conversion.RequestBody.Error != "" {
		t.Fatalf("request body conversion failed: %s", conversion.RequestBody.Error)
	}
	if !strings.Contains(conversion.RequestBody.Output, `"role": "system"`) {
		t.Fatalf("system message not converted: %s", conversion.RequestBody.Output)
	}
	if !strings.Contains(conversion.RequestBody.Output, `"type": "function"`) || !strings.Contains(conversion.RequestBody.Output, `"name": "grep"`) {
		t.Fatalf("tool not converted: %s", conversion.RequestBody.Output)
	}
	if !strings.Contains(conversion.RequestHeaders.Input, "x-api-key: sk-ant-secret") {
		t.Fatalf("x-api-key header should be displayed in input: %s", conversion.RequestHeaders.Input)
	}
	if !strings.Contains(conversion.RequestHeaders.Output, "X-Api-Key: sk-ant-secret") {
		t.Fatalf("x-api-key header should be preserved in output: %s", conversion.RequestHeaders.Output)
	}
	if !strings.Contains(conversion.ResponseBody.Output, `"chat.completion"`) {
		t.Fatalf("response not converted: %s", conversion.ResponseBody.Output)
	}
}
