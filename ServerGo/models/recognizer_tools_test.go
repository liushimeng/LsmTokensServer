package models

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// TestParseRequestToolsFromBody 测试 OpenAI / Anthropic 协议下的 tools 解析
// 覆盖单工具、多工具、空 tools、无 tools、明文存储、嵌套等场景
func TestParseRequestToolsFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string // 明文 body，函数内部会 base64 编码
		want string
	}{
		{
			name: "OpenAI 单工具（用户提供场景）",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"bash"}}]}`,
			want: "bash",
		},
		{
			name: "Anthropic 单工具",
			body: `{"model":"claude-3","tools":[{"name":"Bash","description":"x"}]}`,
			want: "Bash",
		},
		{
			name: "OpenAI 多工具",
			body: `{"model":"gpt-4o","tools":[{"type":"function","function":{"name":"bash"}},{"type":"function","function":{"name":"read_file"}}]}`,
			want: "bash,read_file",
		},
		{
			name: "Anthropic 多工具",
			body: `{"model":"claude-3","tools":[{"name":"Bash"},{"name":"Read"}]}`,
			want: "Bash,Read",
		},
		{
			name: "无 tools 字段",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
			want: "",
		},
		{
			name: "空 body",
			body: ``,
			want: "",
		},
		{
			name: "OpenAI 兜底 - 仅 messages[].tool_calls",
			body: `{"model":"gpt-4o","messages":[{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"bash"}}]}]}`,
			want: "bash",
		},
		{
			name: "OpenAI 混合 - tools + tool_calls 去重",
			body: `{"model":"gpt-4o","tools":[{"type":"function","function":{"name":"bash"}}],"messages":[{"role":"assistant","tool_calls":[{"function":{"name":"bash"}}]}]}`,
			want: "bash",
		},
		{
			name: "tool 元素是字符串",
			body: `{"tools":["bash","read_file"]}`,
			want: "bash,read_file",
		},
		{
			name: "描述里的 JSON 示例不应被误解析为工具名",
			body: `{"tools":[{"type":"function","function":{"description":"tool: {\"name\":\"test_case_when\"}","name":""}}]}`,
			want: "",
		},
		{
			name: "自定义字段 customName",
			body: `{"tools":[{"customName":"my_tool"}]}`,
			want: "my_tool",
		},
		{
			name: "metadata.tools 嵌套",
			body: `{"metadata":{"tools":[{"name":"meta_tool"}]}}`,
			want: "meta_tool",
		},
		{
			name: "parameters.tools 嵌套",
			body: `{"parameters":{"tools":[{"name":"param_tool"}]}}`,
			want: "param_tool",
		},
		{
			name: "外层 requestBody JSON 字符串",
			body: `{"requestBody":"{\"tools\":[{\"name\":\"wrapped_tool\"}]}"}`,
			want: "wrapped_tool",
		},
		{
			name: "外层 body JSON 对象",
			body: `{"body":{"tools":[{"name":"body_tool"}]}}`,
			want: "body_tool",
		},
		{
			name: "外层 payload JSON 字符串",
			body: `{"payload":"{\"tools\":[{\"name\":\"payload_tool\"}]}"}`,
			want: "payload_tool",
		},
		{
			name: "JSON 无效",
			body: `not a json`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte(tt.body))
			got := ParseRequestToolsFromBody(encoded)
			if got != tt.want {
				t.Errorf("ParseRequestToolsFromBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseRequestToolsFromBody_Plaintext 测试明文存储（未 base64 编码）的兼容
func TestParseRequestToolsFromBody_Plaintext(t *testing.T) {
	body := `{"model":"gpt-4o","tools":[{"type":"function","function":{"name":"bash"}}]}`
	got := ParseRequestToolsFromBody(body)
	if got != "bash" {
		t.Errorf("plaintext parse failed: got %q want %q", got, "bash")
	}
}

// TestParseRequestToolsFromBody_DoubleBase64Wrapper 测试外层包装字段里的二次 base64 body
func TestParseRequestToolsFromBody_DoubleBase64Wrapper(t *testing.T) {
	inner := base64.StdEncoding.EncodeToString([]byte(`{"tools":[{"name":"double_b64_tool"}]}`))
	body := fmt.Sprintf(`{"requestBody":%q}`, inner)
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	got := ParseRequestToolsFromBody(encoded)
	if got != "double_b64_tool" {
		t.Errorf("double-base64 wrapper parse failed: got %q want %q", got, "double_b64_tool")
	}
}

// TestParseRequestToolsFromBody_SSEFirstJSON 测试 SSE/混合文本中第一个 JSON 对象的兼容解析
func TestParseRequestToolsFromBody_SSEFirstJSON(t *testing.T) {
	body := `data: {"tools":[{"name":"sse_tool"}]}

data: {"ignored":true}`
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	got := ParseRequestToolsFromBody(encoded)
	if got != "sse_tool" {
		t.Errorf("SSE first JSON parse failed: got %q want %q", got, "sse_tool")
	}
}

// TestExtractToolNamesFromMap_Dedup 验证去重逻辑
func TestExtractToolNamesFromMap_Dedup(t *testing.T) {
	bodyStr := `{"tools":[
		{"type":"function","function":{"name":"bash"}},
		{"type":"function","function":{"name":"bash"}},
		{"type":"function","function":{"name":"read_file"}}
	]}`
	encoded := base64.StdEncoding.EncodeToString([]byte(bodyStr))
	got := ParseRequestToolsFromBody(encoded)
	if got != "bash,read_file" {
		t.Errorf("dedup failed: got %q want %q", got, "bash,read_file")
	}
	// 验证不含重复
	if strings.Count(got, "bash") != 1 {
		t.Errorf("expected single 'bash' after dedup, got %q", got)
	}
}

// TestTruncateRequestTools 验证工具列表落库前的长度截断策略
func TestTruncateRequestTools(t *testing.T) {
	var names []string
	for i := 0; i < 80; i++ {
		names = append(names, fmt.Sprintf("tool_%02d", i))
	}
	got := truncateRequestTools(strings.Join(names, ","))
	if len(got) > requestToolsMaxLen {
		t.Fatalf("truncated tools length = %d, want <= %d", len(got), requestToolsMaxLen)
	}
	if strings.Contains(got, "...") {
		t.Fatalf("truncated tools should not contain ellipsis marker, got %q", got)
	}
	if strings.HasSuffix(got, ",") {
		t.Fatalf("truncated tools should not end with comma, got %q", got)
	}
	if strings.Contains(got, "tool_79") {
		t.Fatalf("truncated tools unexpectedly contains tail item: %q", got)
	}
}
