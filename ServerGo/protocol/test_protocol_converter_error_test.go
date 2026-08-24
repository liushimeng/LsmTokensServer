package protocol_test

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"github.com/lishimeng/LsmTokensServer/proxy"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestConvertProtocolErrorResponseBodyAnthropicToOpenAI(t *testing.T) {
	input := []byte(`{"type":"error","error":{"type":"resource_not_found_error","message":"The requested resource was not found"}}`)
	converted, warnings, err := protocol.ConvertProtocolErrorResponseBody(input, "a2o")
	if err != nil {
		t.Fatalf("protocol.ConvertProtocolErrorResponseBody() error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	body, err := protocol.MarshalConvertedProtocolBody(converted)
	if err != nil {
		t.Fatalf("marshal converted error: %v", err)
	}
	var out map[string]map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal converted error: %v", err)
	}
	if out["error"]["message"] != "The requested resource was not found" {
		t.Fatalf("message = %#v", out["error"]["message"])
	}
	if out["error"]["type"] != "resource_not_found_error" {
		t.Fatalf("type = %#v", out["error"]["type"])
	}
	if strings.Contains(string(body), "chat.completion") {
		t.Fatalf("error response should not become success completion: %s", body)
	}
}

func TestConvertProtocolErrorResponseBodyOpenAIToAnthropic(t *testing.T) {
	input := []byte(`{"error":{"message":"model not found","type":"invalid_request_error","code":"model_not_found"}}`)
	converted, warnings, err := protocol.ConvertProtocolErrorResponseBody(input, "o2a")
	if err != nil {
		t.Fatalf("protocol.ConvertProtocolErrorResponseBody() error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	body, err := protocol.MarshalConvertedProtocolBody(converted)
	if err != nil {
		t.Fatalf("marshal converted error: %v", err)
	}
	var out struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal converted error: %v", err)
	}
	if out.Type != "error" {
		t.Fatalf("type = %q", out.Type)
	}
	if out.Error.Message != "model not found" || out.Error.Type != "invalid_request_error" {
		t.Fatalf("converted error = %#v", out.Error)
	}
}

func TestConvertProtocolErrorResponseBodyPlainText(t *testing.T) {
	converted, warnings, err := protocol.ConvertProtocolErrorResponseBody([]byte("upstream unavailable"), "a2o")
	if err != nil {
		t.Fatalf("protocol.ConvertProtocolErrorResponseBody() error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected non-json warning")
	}
	body, err := protocol.MarshalConvertedProtocolBody(converted)
	if err != nil {
		t.Fatalf("marshal converted error: %v", err)
	}
	if !strings.Contains(string(body), "upstream unavailable") {
		t.Fatalf("plain-text message not preserved: %s", body)
	}
}

func TestConvertProxyResponseWrapsNonStreamSuccessAsSSEForStreamRequest(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"kimi-for-coding",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":2}
		}`)),
	}
	convertedBody, convertedHeaders, _, err := proxy.ConvertProxyResponseForTest(resp, protocol.AgentProtocolType_OpenAI, protocol.AgentProtocolType_Anthropic, true)
	if err != nil {
		t.Fatalf("proxy.ConvertProxyResponseForTest() error: %v", err)
	}
	body := string(convertedBody)
	if !strings.Contains(convertedHeaders.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q", convertedHeaders.Get("Content-Type"))
	}
	if !strings.Contains(body, "data:") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("converted response is not OpenAI SSE: %s", body)
	}
	if !strings.Contains(body, "chat.completion.chunk") || !strings.Contains(body, "hello") {
		t.Fatalf("OpenAI SSE does not contain expected chunk content: %s", body)
	}
}

func TestConvertProxyResponseNon2xxPreservesErrorMessage(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"resource_not_found_error","message":"The requested resource was not found"}}`)),
	}
	convertedBody, convertedHeaders, srcBody, err := proxy.ConvertProxyResponseForTest(resp, protocol.AgentProtocolType_OpenAI, protocol.AgentProtocolType_Anthropic, false)
	if err != nil {
		t.Fatalf("proxy.ConvertProxyResponseForTest() error: %v", err)
	}
	if !strings.Contains(string(srcBody), "The requested resource was not found") {
		t.Fatalf("source body not preserved: %s", srcBody)
	}
	if !strings.Contains(string(convertedBody), "The requested resource was not found") {
		t.Fatalf("converted body does not preserve error message: %s", convertedBody)
	}
	if strings.Contains(string(convertedBody), "chat.completion") {
		t.Fatalf("non-2xx body should not become success completion: %s", convertedBody)
	}
	if !strings.Contains(convertedHeaders.Get("Content-Type"), "application/json") {
		t.Fatalf("content-type = %q", convertedHeaders.Get("Content-Type"))
	}
}
