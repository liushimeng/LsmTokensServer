package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// ============================================================================
// v2.0.73: cc-switch 借鉴增强测试
// 所有测试使用真实线格式 JSON 字符串作为输入（延续 v2.0.72 强制规则）
// ============================================================================

// ==================== P0-1: o-series 模型检测 ====================

func TestIsOpenAIOSeries_Boundary(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"o1", true}, {"o1-mini", true}, {"o1-preview", true},
		{"o3", true}, {"o3-mini", true}, {"o4-mini", true},
		{"O1", true}, // 大写 O 也识别
		{"gpt-4o", false}, {"gpt-4o-mini", false}, {"claude-3-opus", false},
		{"", false}, {"x", false}, {"openai", false}, {"ox", false},
	}
	for _, c := range cases {
		got := isOpenAIOSeries(c.model)
		if got != c.want {
			t.Errorf("isOpenAIOSeries(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestConvertAnthropicToOpenAIRequest_OSeries_UsesMaxCompletionTokens(t *testing.T) {
	// 真实线格式：Anthropic 请求，o1 模型
	input := `{"model":"o1","max_tokens":4096,"messages":[{"role":"user","content":"hello"}]}`
	var req AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := ConvertAnthropicToOpenAIRequest(&req)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out.MaxCompletionTokens != 4096 {
		t.Errorf("o-series: MaxCompletionTokens = %d, want 4096", out.MaxCompletionTokens)
	}
	if out.MaxTokens != 0 {
		t.Errorf("o-series: MaxTokens should be 0, got %d", out.MaxTokens)
	}
}

func TestConvertAnthropicToOpenAIRequest_NonOSeries_UsesMaxTokens(t *testing.T) {
	input := `{"model":"gpt-4o","max_tokens":2048,"messages":[{"role":"user","content":"hi"}]}`
	var req AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := ConvertAnthropicToOpenAIRequest(&req)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out.MaxTokens != 2048 {
		t.Errorf("non-o-series: MaxTokens = %d, want 2048", out.MaxTokens)
	}
	if out.MaxCompletionTokens != 0 {
		t.Errorf("non-o-series: MaxCompletionTokens should be 0, got %d", out.MaxCompletionTokens)
	}
}

// ==================== P1-5a: a2o reasoning_effort ====================

func TestResolveReasoningEffort_OutputConfigPriority(t *testing.T) {
	// output_config.effort 优先于 thinking
	req := &AnthropicMessagesRequest{
		Thinking:     map[string]interface{}{"type": "enabled", "budget_tokens": 1000},
		OutputConfig: map[string]interface{}{"effort": "high"},
	}
	if got := resolveReasoningEffort(req); got != "high" {
		t.Errorf("resolveReasoningEffort output_config priority = %q, want \"high\"", got)
	}
}

func TestResolveReasoningEffort_BudgetTiers(t *testing.T) {
	cases := []struct {
		budget float64
		want   string
	}{
		{0, "high"},      // 无 budget → high
		{1000, "low"},    // < 4000
		{4000, "medium"}, // 4000-15999
		{10000, "medium"},
		{16000, "high"}, // >= 16000
		{100000, "high"},
	}
	for _, c := range cases {
		req := &AnthropicMessagesRequest{
			Thinking: map[string]interface{}{"type": "enabled", "budget_tokens": c.budget},
		}
		got := resolveReasoningEffort(req)
		if got != c.want {
			t.Errorf("budget=%v: resolveReasoningEffort = %q, want %q", c.budget, got, c.want)
		}
	}
}

func TestResolveReasoningEffort_Adaptive(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Thinking: map[string]interface{}{"type": "adaptive"},
	}
	if got := resolveReasoningEffort(req); got != "high" {
		t.Errorf("adaptive: got %q, want \"high\"", got)
	}
}

func TestResolveReasoningEffort_None(t *testing.T) {
	req := &AnthropicMessagesRequest{}
	if got := resolveReasoningEffort(req); got != "" {
		t.Errorf("no thinking: got %q, want \"\"", got)
	}
}

// ==================== P1-5b/P1-6: o2a reasoning_effort → thinking + 钳制 ====================

func TestReasoningEffortToThinking_Tiers(t *testing.T) {
	cases := []struct {
		Effort     string
		wantBudget int
	}{
		{"low", 1024},
		{"medium", 4000},
		{"high", 16000},
		{"xhigh", 32000},
	}
	for _, c := range cases {
		got := reasoningEffortToThinking(c.Effort, 100000)
		m, ok := got.(map[string]interface{})
		if !ok {
			t.Errorf("effort=%s: not a map", c.Effort)
			continue
		}
		if m["type"] != "enabled" {
			t.Errorf("effort=%s: type=%v, want \"enabled\"", c.Effort, m["type"])
			continue
		}
		var gotBudget int
		switch v := m["budget_tokens"].(type) {
		case int:
			gotBudget = v
		case float64:
			gotBudget = int(v)
		}
		if gotBudget != c.wantBudget {
			t.Errorf("effort=%s: budget=%v, want %d", c.Effort, m["budget_tokens"], c.wantBudget)
		}
	}
}

func TestReasoningEffortToThinking_Empty_Nil(t *testing.T) {
	if got := reasoningEffortToThinking("", 100000); got != nil {
		t.Errorf("empty effort: got %v, want nil", got)
	}
}

func TestReasoningEffortToThinking_ClampedToHalfMaxTokens(t *testing.T) {
	// max_tokens=4000 → ceiling=2000; xhigh(32000) 应钳制到 2000
	got := reasoningEffortToThinking("xhigh", 4000)
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("clamped: not a map: %v", got)
	}
	var gotBudget int
	switch v := m["budget_tokens"].(type) {
	case int:
		gotBudget = v
	case float64:
		gotBudget = int(v)
	}
	if gotBudget != 2000 {
		t.Errorf("clamped budget = %v, want 2000 (max_tokens/2)", m["budget_tokens"])
	}
}

func TestReasoningEffortToThinking_Below1024_Disabled(t *testing.T) {
	// max_tokens=1000 → ceiling=500 < 1024 → 禁用（返回 nil）
	if got := reasoningEffortToThinking("low", 1000); got != nil {
		t.Errorf("below 1024: got %v, want nil (disabled)", got)
	}
}

// ==================== P1-7: 强制 tool_choice + thinking 冲突 ====================

func TestDisableThinkingOnForcedToolChoice_Required_Disabled(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Thinking:     map[string]interface{}{"type": "enabled", "budget_tokens": 4000},
		OutputConfig: map[string]interface{}{"effort": "high"},
	}
	disableThinkingOnForcedToolChoice(req, "required")
	if req.Thinking != nil {
		t.Errorf("required: thinking should be nil, got %v", req.Thinking)
	}
	if req.OutputConfig != nil {
		t.Errorf("required: output_config should be nil, got %v", req.OutputConfig)
	}
}

func TestDisableThinkingOnForcedToolChoice_NamedTool_Disabled(t *testing.T) {
	req := &AnthropicMessagesRequest{
		Thinking: map[string]interface{}{"type": "enabled", "budget_tokens": 4000},
	}
	disableThinkingOnForcedToolChoice(req, map[string]interface{}{
		"type": "tool", "name": "search",
	})
	if req.Thinking != nil {
		t.Errorf("named tool: thinking should be nil, got %v", req.Thinking)
	}
}

func TestDisableThinkingOnForcedToolChoice_Auto_Kept(t *testing.T) {
	thinking := map[string]interface{}{"type": "enabled", "budget_tokens": 4000}
	req := &AnthropicMessagesRequest{Thinking: thinking}
	disableThinkingOnForcedToolChoice(req, "auto")
	if req.Thinking == nil {
		t.Errorf("auto: thinking should be kept, got nil")
	}
}

// ==================== P0-2: 首条 user 消息保证 ====================

func TestEnsureLeadingUserMessage_AssistantFirst_PrependsUser(t *testing.T) {
	msgs := []AnthropicMessage{
		{Role: "assistant", Content: []AnthropicContentBlock{{Type: "text", Text: "hi"}}},
	}
	out := ensureLeadingUserMessage(msgs)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("first role = %q, want \"user\"", out[0].Role)
	}
	if out[1].Role != "assistant" {
		t.Errorf("second role = %q, want \"assistant\"", out[1].Role)
	}
}

func TestEnsureLeadingUserMessage_UserFirst_NoOp(t *testing.T) {
	msgs := []AnthropicMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	out := ensureLeadingUserMessage(msgs)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("first role = %q, want \"user\"", out[0].Role)
	}
}

func TestEnsureLeadingUserMessage_Empty_NoOp(t *testing.T) {
	out := ensureLeadingUserMessage(nil)
	if len(out) != 0 {
		t.Errorf("empty: len = %d, want 0", len(out))
	}
}

// ==================== P0-3: 不完成工具轮次丢弃 ====================

func TestDropIncompleteToolTurns_CompleteTurn_Kept(t *testing.T) {
	msgs := []AnthropicMessage{
		{Role: "user", Content: "call tool"},
		{Role: "assistant", Content: []AnthropicContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "search", Input: map[string]interface{}{"q": "x"}},
		}},
		{Role: "user", Content: []AnthropicContentBlock{
			{Type: "tool_result", ToolUseID: "tu1", Content: "result"},
		}},
	}
	out := dropIncompleteToolTurns(msgs)
	if len(out) != 3 {
		t.Errorf("complete turn: len = %d, want 3 (all kept)", len(out))
	}
}

func TestDropIncompleteToolTurns_IncompleteTurn_Dropped(t *testing.T) {
	msgs := []AnthropicMessage{
		{Role: "user", Content: "call tool"},
		{Role: "assistant", Content: []AnthropicContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "search", Input: map[string]interface{}{}},
		}},
		// 无 tool_result → 不完成
		{Role: "user", Content: "nevermind"},
	}
	out := dropIncompleteToolTurns(msgs)
	// assistant 应被丢弃
	for _, m := range out {
		if m.Role == "assistant" {
			t.Errorf("incomplete turn: assistant should be dropped, got %+v", m)
		}
	}
}

func TestDropIncompleteToolTurns_NoTools_NoOp(t *testing.T) {
	msgs := []AnthropicMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	out := dropIncompleteToolTurns(msgs)
	if len(out) != 2 {
		t.Errorf("no tools: len = %d, want 2", len(out))
	}
}

func TestDropIncompleteToolTurns_TrailingIncomplete_Kept(t *testing.T) {
	// 末尾 assistant tool_use 无匹配 tool_result → 保留（当前轮次，客户端将在下次请求返回结果）
	msgs := []AnthropicMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: []AnthropicContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "f", Input: map[string]interface{}{}},
		}},
	}
	out := dropIncompleteToolTurns(msgs)
	var hasAssistant bool
	for _, m := range out {
		if m.Role == "assistant" {
			hasAssistant = true
		}
	}
	if !hasAssistant {
		t.Errorf("trailing incomplete: assistant (current turn) should be KEPT, but was dropped")
	}
}

// ==================== P1-8: o2a 响应 refusal → text 块 ====================

func TestConvertOpenAIToAnthropicResponse_Refusal_ToTextBlock(t *testing.T) {
	// o 系列模型安全拒绝：content 为 nil，refusal 含原因
	resp := &OpenAIChatCompletionResponse{
		ID: "chatcmpl_x", Model: "o1",
		Choices: []OpenAIChoice{{
			Message: &OpenAIMessage{Role: "assistant", Refusal: "I cannot do that."},
		}},
	}
	out, err := ConvertOpenAIToAnthropicResponse(resp)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(out.Content))
	}
	if out.Content[0].Type != "text" || out.Content[0].Text != "I cannot do that." {
		t.Errorf("refusal not converted: %+v", out.Content[0])
	}
}

func TestConvertOpenAIToAnthropicResponse_ContentAndRefusal_ContentWins(t *testing.T) {
	resp := &OpenAIChatCompletionResponse{
		ID: "chatcmpl_x", Model: "o1",
		Choices: []OpenAIChoice{{
			Message: &OpenAIMessage{Role: "assistant", Content: "normal reply", Refusal: "ignored"},
		}},
	}
	out, err := ConvertOpenAIToAnthropicResponse(resp)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out.Content) != 1 || out.Content[0].Text != "normal reply" {
		t.Errorf("content should win: %+v", out.Content)
	}
}

// ==================== P1-9: SSE 流截断检测 ====================

func TestAggregateOpenAISSEToResponse_Truncated_NoDone_Warning(t *testing.T) {
	events := []SSEEvent{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`},
		{Data: `{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`},
		// 无 [DONE]
	}
	resp, warnings := AggregateOpenAISSEToResponse(events)
	if resp.Choices[0].FinishReason != "length" {
		t.Errorf("truncated: finish_reason = %q, want \"length\"", resp.Choices[0].FinishReason)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "stream truncated") {
			found = true
		}
	}
	if !found {
		t.Errorf("truncated: expected 'stream truncated' warning, got %v", warnings)
	}
}

func TestAggregateOpenAISSEToResponse_Complete_NoWarning(t *testing.T) {
	events := []SSEEvent{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`},
		{Data: `{"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":"stop"}]}`},
		{Data: `[DONE]`},
	}
	_, warnings := AggregateOpenAISSEToResponse(events)
	for _, w := range warnings {
		if strings.Contains(w, "stream truncated") {
			t.Errorf("complete: unexpected truncation warning: %v", warnings)
		}
	}
}

func TestAggregateAnthropicSSEToResponse_Truncated_NoMessageStop_Warning(t *testing.T) {
	events := []SSEEvent{
		{Event: "message_start", Data: `{"type":"message_start","message":{"id":"m1","model":"claude","usage":{"input_tokens":10,"output_tokens":5}}}`},
		{Event: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{Event: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`},
		// 无 message_stop
	}
	resp, warnings := AggregateAnthropicSSEToResponse(events)
	if resp.StopReason != "max_tokens" {
		t.Errorf("truncated: stop_reason = %q, want \"max_tokens\"", resp.StopReason)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "stream truncated") {
			found = true
		}
	}
	if !found {
		t.Errorf("truncated: expected 'stream truncated' warning, got %v", warnings)
	}
}

func TestAggregateAnthropicSSEToResponse_Complete_NoWarning(t *testing.T) {
	events := []SSEEvent{
		{Event: "message_start", Data: `{"type":"message_start","message":{"id":"m1","model":"claude","usage":{"input_tokens":10,"output_tokens":5}}}`},
		{Event: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{Event: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`},
		{Event: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`},
		{Event: "message_stop", Data: ``},
	}
	_, warnings := AggregateAnthropicSSEToResponse(events)
	for _, w := range warnings {
		if strings.Contains(w, "stream truncated") {
			t.Errorf("complete: unexpected truncation warning: %v", warnings)
		}
	}
}

// ==================== P1-10: UTF-8 跨 chunk 边界 ====================

func TestTrimIncompleteTrailingUTF8_Complete(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"你好", "你好"},               // 完整 3-byte
		{"😀", "😀"},                 // 完整 4-byte
		{"hello\xE4\xBD", "hello"}, // 不完整 3-byte（缺最后一字节）→ 截断
		{"hello\xE4", "hello"},     // 不完整起始字节 → 截断
		{"你好\xE4\xBD", "你好"},       // 前面完整，末尾不完整
	}
	for _, c := range cases {
		got := trimIncompleteTrailingUTF8(c.input)
		if got != c.want {
			t.Errorf("trimIncompleteTrailingUTF8(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseSSEEvents_UTF8Multibyte_NotCorrupted(t *testing.T) {
	// 完整 UTF-8 字符不应被截断
	input := "event: message_start\ndata: {\"text\":\"你好世界\"}\n\n"
	events := ParseSSEEvents(input)
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Data, "你好世界") {
		t.Errorf("UTF-8 corrupted: data = %q", events[0].Data)
	}
}

// ==================== P2-11: 上游错误信封检测 ====================

func TestExtractOpenAIErrorEnvelope_Detected(t *testing.T) {
	body := []byte(`{"error":{"message":"rate limited","type":"tokens","code":429}}`)
	msg, errType := extractOpenAIErrorEnvelope(body)
	if msg != "rate limited" {
		t.Errorf("msg = %q, want \"rate limited\"", msg)
	}
	if errType != "tokens" {
		t.Errorf("type = %q, want \"tokens\"", errType)
	}
}

func TestExtractOpenAIErrorEnvelope_NormalResponse(t *testing.T) {
	body := []byte(`{"id":"chatcmpl_x","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	msg, errType := extractOpenAIErrorEnvelope(body)
	if msg != "" || errType != "" {
		t.Errorf("normal response should not be detected as error: msg=%q type=%q", msg, errType)
	}
}

func TestExtractAnthropicErrorEnvelope_Detected(t *testing.T) {
	body := []byte(`{"type":"error","error":{"message":"invalid api key","type":"authentication_error"}}`)
	msg, errType := extractAnthropicErrorEnvelope(body)
	if msg != "invalid api key" {
		t.Errorf("msg = %q, want \"invalid api key\"", msg)
	}
	if errType != "authentication_error" {
		t.Errorf("type = %q, want \"authentication_error\"", errType)
	}
}

func TestExtractAnthropicErrorEnvelope_NormalResponse(t *testing.T) {
	body := []byte(`{"id":"msg_x","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}]}`)
	msg, errType := extractAnthropicErrorEnvelope(body)
	if msg != "" || errType != "" {
		t.Errorf("normal response should not be detected as error: msg=%q type=%q", msg, errType)
	}
}

// ==================== P2-12: 工具输出媒体递归提取 ====================

func TestFlattenAnthropicToolResultContent_ImageBlock_AsDataURI(t *testing.T) {
	content := []interface{}{
		map[string]interface{}{
			"type": "image",
			"source": map[string]interface{}{
				"type": "base64", "media_type": "image/png", "data": "abc123",
			},
		},
	}
	got := flattenAnthropicToolResultContent(content)
	want := "[image: data:image/png;base64,abc123]"
	if got != want {
		t.Errorf("image as data URI = %q, want %q", got, want)
	}
}

func TestFlattenAnthropicToolResultContent_ImageURL_Passthrough(t *testing.T) {
	content := []interface{}{
		map[string]interface{}{
			"type":   "image",
			"source": map[string]interface{}{"type": "url", "url": "https://example.com/img.png"},
		},
	}
	got := flattenAnthropicToolResultContent(content)
	want := "[image: https://example.com/img.png]"
	if got != want {
		t.Errorf("image url = %q, want %q", got, want)
	}
}

func TestFlattenAnthropicToolResultContent_Mixed_TextAndImage(t *testing.T) {
	content := []interface{}{
		map[string]interface{}{"type": "text", "text": "here is the image:"},
		map[string]interface{}{
			"type":   "image",
			"source": map[string]interface{}{"type": "base64", "media_type": "image/jpeg", "data": "xyz"},
		},
	}
	got := flattenAnthropicToolResultContent(content)
	if !strings.Contains(got, "here is the image:") {
		t.Errorf("text missing: %q", got)
	}
	if !strings.Contains(got, "data:image/jpeg;base64,xyz") {
		t.Errorf("image missing: %q", got)
	}
}

func TestFlattenAnthropicToolResultContent_Nested_DepthLimited(t *testing.T) {
	// 构造深度嵌套的 content 数组（每层是一个 tool_result 块的 content 字段指向下一层数组）
	var nested interface{} = []interface{}{
		map[string]interface{}{"type": "text", "text": "leaf"},
	}
	for i := 0; i < 35; i++ {
		nested = []interface{}{
			map[string]interface{}{"type": "tool_result", "content": nested},
		}
	}
	got := flattenAnthropicToolResultContent(nested)
	if !strings.Contains(got, "[content too deep]") {
		t.Errorf("depth limit not triggered: got = %q", got)
	}
}

// ==================== o2a 端到端：首条 user + thinking 注入 ====================

func TestConvertOpenAIToAnthropicRequest_LeadingUserMessageGuaranteed(t *testing.T) {
	// OpenAI 历史以 assistant 开头
	input := `{"model":"claude-3","messages":[{"role":"assistant","content":"hi"}],"max_tokens":1024}`
	var req OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := ConvertOpenAIToAnthropicRequest(&req)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out.Messages) < 1 || out.Messages[0].Role != "user" {
		t.Errorf("leading user message not guaranteed: first role = %v", out.Messages)
	}
}

func TestConvertOpenAIToAnthropicRequest_ReasoningEffort_ToThinking(t *testing.T) {
	effort := "high"
	input := `{"model":"claude-3","messages":[{"role":"user","content":"think"}],"max_tokens":10000}`
	var req OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	req.ReasoningEffort = &effort
	out, err := ConvertOpenAIToAnthropicRequest(&req)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	thinking, ok := out.Thinking.(map[string]interface{})
	if !ok {
		t.Fatalf("thinking not set: %v", out.Thinking)
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking type = %v, want \"enabled\"", thinking["type"])
	}
	// high → 16000, 钳制到 max_tokens/2 = 5000
	var gotBudget int
	switch v := thinking["budget_tokens"].(type) {
	case int:
		gotBudget = v
	case float64:
		gotBudget = int(v)
	}
	if gotBudget != 5000 {
		t.Errorf("thinking budget = %v, want 5000 (clamped to max_tokens/2)", thinking["budget_tokens"])
	}
}
