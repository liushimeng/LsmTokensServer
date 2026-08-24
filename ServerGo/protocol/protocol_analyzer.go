package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ============================================================================
// 协议转换分析器统一入口
// ============================================================================

const (
	ProtocolAnalyzerSectionRequestHeaders  = "request_headers"
	ProtocolAnalyzerSectionResponseHeaders = "response_headers"
	ProtocolAnalyzerSectionRequestBody     = "request_body"
	ProtocolAnalyzerSectionResponseBody    = "response_body"
)

// ProtocolConvertAnalyzerTestRequest 协议转换分析器测试请求。
// request_type 为旧版字段，保留用于兼容原有 request/response body 调用。
type ProtocolConvertAnalyzerTestRequest struct {
	Direction   string          `json:"direction"`
	Section     string          `json:"section"`
	Format      string          `json:"format"`
	IsStream    bool            `json:"is_stream"`
	Input       json.RawMessage `json:"input"`
	TextInput   string          `json:"text_input"`
	RequestType string          `json:"request_type"`
}

// ProtocolConvertAnalyzerTestResponse 协议转换分析器测试响应。
type ProtocolConvertAnalyzerTestResponse struct {
	Success  bool               `json:"success"`
	Output   json.RawMessage    `json:"output,omitempty"`
	Text     string             `json:"text,omitempty"`
	Format   string             `json:"format,omitempty"`
	Metrics  *ConversionMetrics `json:"metrics,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
	Error    string             `json:"error,omitempty"`
}

// ConvertProtocolAnalyzerInput 按 HTTP 数据段统一分发协议转换。
func ConvertProtocolAnalyzerInput(req ProtocolConvertAnalyzerTestRequest) (*ProtocolConvertAnalyzerTestResponse, error) {
	req.Section = normalizeAnalyzerSection(req.Section, req.RequestType)
	if req.Format == "" {
		req.Format = "auto"
	}

	switch req.Section {
	case ProtocolAnalyzerSectionRequestHeaders:
		text := req.TextInput
		if text == "" {
			text = string(req.Input)
		}
		out, warnings, err := ConvertProtocolRequestHeaders(text, req.Direction)
		if err != nil {
			return nil, err
		}
		return &ProtocolConvertAnalyzerTestResponse{Success: true, Text: out, Format: "headers", Warnings: warnings}, nil
	case ProtocolAnalyzerSectionResponseHeaders:
		text := req.TextInput
		if text == "" {
			text = string(req.Input)
		}
		out, warnings, err := ConvertProtocolResponseHeaders(text, req.Direction, req.IsStream)
		if err != nil {
			return nil, err
		}
		return &ProtocolConvertAnalyzerTestResponse{Success: true, Text: out, Format: "headers", Warnings: warnings}, nil
	case ProtocolAnalyzerSectionRequestBody:
		out, metrics, warnings, err := ConvertProtocolRequestBody(req.Input, req.Direction)
		if err != nil {
			return nil, err
		}
		return marshalAnalyzerJSONResponse(out, metrics, warnings)
	case ProtocolAnalyzerSectionResponseBody:
		if req.IsStream || req.Format == "sse" {
			text := req.TextInput
			if text == "" {
				text = string(req.Input)
			}
			out, warnings, err := ConvertProtocolResponseSSE(text, req.Direction)
			if err != nil {
				return nil, err
			}
			outJSON, _ := json.Marshal(out)
			metrics, _ := CalculateConversionMetricsForSection(outJSON, req.Direction, ProtocolAnalyzerSectionResponseBody)
			return marshalAnalyzerJSONResponse(out, metrics, warnings)
		}
		out, metrics, warnings, err := ConvertProtocolResponseBody(req.Input, req.Direction)
		if err != nil {
			return nil, err
		}
		return marshalAnalyzerJSONResponse(out, metrics, warnings)
	default:
		return nil, fmt.Errorf("invalid section: %s", req.Section)
	}
}

func normalizeAnalyzerSection(section, requestType string) string {
	if section != "" {
		return section
	}
	switch requestType {
	case "request":
		return ProtocolAnalyzerSectionRequestBody
	case "response":
		return ProtocolAnalyzerSectionResponseBody
	default:
		return ProtocolAnalyzerSectionRequestBody
	}
}

func marshalAnalyzerJSONResponse(output interface{}, metrics *ConversionMetrics, warnings []string) (*ProtocolConvertAnalyzerTestResponse, error) {
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output: %w", err)
	}
	return &ProtocolConvertAnalyzerTestResponse{
		Success:  true,
		Output:   json.RawMessage(outputJSON),
		Format:   "json",
		Metrics:  metrics,
		Warnings: warnings,
	}, nil
}

func ConvertProtocolRequestBody(input []byte, direction string) (interface{}, *ConversionMetrics, []string, error) {
	warnings := collectBodyCompatibilityWarnings(input, direction, ProtocolAnalyzerSectionRequestBody)
	// 使用完整指标计算（含 structure_success_rate 与 field_conversion_rate），
	// 内部通过 convertRequestBodyOnly 拆分避免递归调用
	metrics, _ := CalculateConversionMetricsForSection(input, direction, ProtocolAnalyzerSectionRequestBody)
	switch direction {
	case "o2a":
		var openAIReq OpenAIChatCompletionRequest
		if err := json.Unmarshal(input, &openAIReq); err != nil {
			return nil, metrics, warnings, fmt.Errorf("failed to parse OpenAI request: %w", err)
		}
		output, err := ConvertOpenAIToAnthropicRequest(&openAIReq)
		return output, metrics, warnings, err
	case "a2o":
		var anthropicReq AnthropicMessagesRequest
		if err := json.Unmarshal(input, &anthropicReq); err != nil {
			return nil, metrics, warnings, fmt.Errorf("failed to parse Anthropic request: %w", err)
		}
		output, err := ConvertAnthropicToOpenAIRequest(&anthropicReq)
		return output, metrics, warnings, err
	default:
		return nil, metrics, warnings, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
}

func ConvertProtocolResponseBody(input []byte, direction string) (interface{}, *ConversionMetrics, []string, error) {
	warnings := collectBodyCompatibilityWarnings(input, direction, ProtocolAnalyzerSectionResponseBody)
	// 使用完整指标计算（含 structure_success_rate 与 field_conversion_rate），
	// 内部通过 convertResponseBodyOnly 拆分避免递归调用
	metrics, _ := CalculateConversionMetricsForSection(input, direction, ProtocolAnalyzerSectionResponseBody)
	switch direction {
	case "o2a":
		// v2.0.73: 检测上游错误信封（借鉴 cc-switch 上游错误信封检测）。
		// 非 2xx 状态码已由 convertProxyResponse 走 ConvertProtocolErrorResponseBody；
		// 此处防御 2xx 状态码但响应体为 {"error":{...}} 的异常场景。
		if errMsg, errType := extractOpenAIErrorEnvelope(input); errMsg != "" {
			warnings = append(warnings, "upstream OpenAI error envelope detected: "+errType+": "+errMsg)
			return nil, metrics, warnings, fmt.Errorf("upstream OpenAI error: %s", errMsg)
		}
		var openAIResp OpenAIChatCompletionResponse
		if err := json.Unmarshal(input, &openAIResp); err != nil {
			return nil, metrics, warnings, fmt.Errorf("failed to parse OpenAI response: %w", err)
		}
		if len(openAIResp.Choices) > 1 {
			warnings = append(warnings, "only choices[0] is converted; additional choices are not represented in Anthropic Messages response")
		}
		output, err := ConvertOpenAIToAnthropicResponse(&openAIResp)
		return output, metrics, warnings, err
	case "a2o":
		// v2.0.73: 检测 Anthropic 上游错误信封
		if errMsg, errType := extractAnthropicErrorEnvelope(input); errMsg != "" {
			warnings = append(warnings, "upstream Anthropic error envelope detected: "+errType+": "+errMsg)
			return nil, metrics, warnings, fmt.Errorf("upstream Anthropic error: %s", errMsg)
		}
		var anthropicResp AnthropicMessagesResponse
		if err := json.Unmarshal(input, &anthropicResp); err != nil {
			return nil, metrics, warnings, fmt.Errorf("failed to parse Anthropic response: %w", err)
		}
		output, err := ConvertAnthropicToOpenAIResponse(&anthropicResp)
		return output, metrics, warnings, err
	default:
		return nil, metrics, warnings, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
}

// extractOpenAIErrorEnvelope 检测 OpenAI 错误信封 {"error":{"message":...,"type":...}}，
// 返回 (message, type)；非错误响应返回 ("", "")。
func extractOpenAIErrorEnvelope(input []byte) (string, string) {
	var raw map[string]interface{}
	if err := json.Unmarshal(input, &raw); err != nil {
		return "", ""
	}
	errObj, ok := raw["error"].(map[string]interface{})
	if !ok {
		return "", ""
	}
	msg, _ := errObj["message"].(string)
	errType, _ := errObj["type"].(string)
	if msg == "" && errType == "" {
		return "", ""
	}
	return msg, errType
}

// extractAnthropicErrorEnvelope 检测 Anthropic 错误信封 {"type":"error","error":{...}} 或 {"error":{...}}，
// 返回 (message, type)；非错误响应返回 ("", "")。
func extractAnthropicErrorEnvelope(input []byte) (string, string) {
	var raw map[string]interface{}
	if err := json.Unmarshal(input, &raw); err != nil {
		return "", ""
	}
	if t, _ := raw["type"].(string); t == "error" {
		errObj, ok := raw["error"].(map[string]interface{})
		if ok {
			msg, _ := errObj["message"].(string)
			et, _ := errObj["type"].(string)
			if msg != "" || et != "" {
				return msg, et
			}
		}
	}
	errObj, ok := raw["error"].(map[string]interface{})
	if !ok {
		return "", ""
	}
	msg, _ := errObj["message"].(string)
	errType, _ := errObj["type"].(string)
	if msg == "" && errType == "" {
		return "", ""
	}
	return msg, errType
}

func ConvertProtocolErrorResponseBody(input []byte, direction string) (interface{}, []string, error) {
	message, errorType, code, warnings := extractProtocolErrorFields(input)
	if message == "" {
		message = "upstream protocol error"
	}
	if errorType == "" {
		errorType = "upstream_error"
	}
	switch direction {
	case "a2o":
		errorObj := map[string]interface{}{
			"message": message,
			"type":    errorType,
		}
		if code != "" {
			errorObj["code"] = code
		}
		return map[string]interface{}{"error": errorObj}, warnings, nil
	case "o2a":
		return map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    errorType,
				"message": message,
			},
		}, warnings, nil
	default:
		return nil, warnings, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
}

func extractProtocolErrorFields(input []byte) (string, string, string, []string) {
	var warnings []string
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		return "empty upstream error response", "upstream_error", "", warnings
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(input, &raw); err != nil {
		warnings = append(warnings, "upstream error response is not JSON; preserved as message")
		return trimmed, "upstream_error", "", warnings
	}
	message := stringFromMap(raw, "message")
	errorType := stringFromMap(raw, "type")
	code := stringFromMap(raw, "code")
	if nested, ok := raw["error"]; ok {
		switch errVal := nested.(type) {
		case map[string]interface{}:
			if v := stringFromMap(errVal, "message"); v != "" {
				message = v
			}
			if v := stringFromMap(errVal, "type"); v != "" {
				errorType = v
			}
			if v := stringFromMap(errVal, "code"); v != "" {
				code = v
			}
		case string:
			if message == "" {
				message = errVal
			}
		}
	}
	if message == "" {
		if b, err := json.Marshal(raw); err == nil {
			message = string(b)
		}
	}
	return message, errorType, code, warnings
}

func stringFromMap(values map[string]interface{}, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func ConvertProtocolRequestHeaders(input, direction string) (string, []string, error) {
	headers := ParseHeaderBlock(input)
	var out http.Header
	var warnings []string
	switch direction {
	case "o2a":
		out, warnings = ConvertOpenAIToAnthropicRequestHeaders(headers)
	case "a2o":
		out, warnings = ConvertAnthropicToOpenAIRequestHeaders(headers)
	default:
		return "", nil, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
	return FormatHeaderBlock(out), warnings, nil
}

func ConvertProtocolResponseHeaders(input, direction string, isStream bool) (string, []string, error) {
	headers := ParseHeaderBlock(input)
	var out http.Header
	var warnings []string
	switch direction {
	case "o2a":
		out, warnings = ConvertOpenAIToAnthropicResponseHeaders(headers, isStream)
	case "a2o":
		out, warnings = ConvertAnthropicToOpenAIResponseHeaders(headers, isStream)
	default:
		return "", nil, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
	return FormatHeaderBlock(out), warnings, nil
}

func ParseHeaderBlock(text string) http.Header {
	headers := http.Header{}
	text = strings.TrimSpace(text)
	if text == "" {
		return headers
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(text), &raw); err == nil {
		for key, value := range raw {
			switch v := value.(type) {
			case string:
				headers.Add(key, v)
			case []interface{}:
				for _, item := range v {
					headers.Add(key, fmt.Sprint(item))
				}
			default:
				headers.Add(key, fmt.Sprint(v))
			}
		}
		return headers
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if key != "" {
			headers.Add(key, value)
		}
	}
	return headers
}

func HeaderToDisplayMap(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for key, values := range h {
		copied := append([]string(nil), values...)
		out[http.CanonicalHeaderKey(key)] = copied
	}
	return out
}

func FormatHeaderBlock(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, http.CanonicalHeaderKey(key))
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		for _, value := range h.Values(key) {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(value)
		}
	}
	return b.String()
}

func ConvertOpenAIToAnthropicRequestHeaders(src http.Header) (http.Header, []string) {
	out, warnings := copyAnalyzerSafeHeaders(src, AgentProtocolType_Anthropic, true)
	if out.Get("Openai-Beta") != "" {
		out.Del("Openai-Beta")
		warnings = append(warnings, "dropped OpenAI-specific request header: OpenAI-Beta")
	}
	if out.Get("Anthropic-Version") == "" {
		out.Set("Anthropic-Version", "2023-06-01")
		warnings = append(warnings, "added required Anthropic-Version: 2023-06-01 header for Anthropic Messages API")
	}
	return out, warnings
}

func ConvertAnthropicToOpenAIRequestHeaders(src http.Header) (http.Header, []string) {
	out, warnings := copyAnalyzerSafeHeaders(src, AgentProtocolType_OpenAI, true)
	for _, key := range []string{"Anthropic-Version", "Anthropic-Beta"} {
		if out.Get(key) != "" {
			out.Del(key)
			warnings = append(warnings, "dropped Anthropic-specific request header: "+key)
		}
	}
	return out, warnings
}

func ConvertOpenAIToAnthropicResponseHeaders(src http.Header, isStream bool) (http.Header, []string) {
	out, warnings := copyAnalyzerResponseHeaders(src)
	if isStream {
		out.Set("Content-Type", "text/event-stream")
		if out.Get("Cache-Control") == "" {
			out.Set("Cache-Control", "no-cache")
		}
	}
	return out, warnings
}

func ConvertAnthropicToOpenAIResponseHeaders(src http.Header, isStream bool) (http.Header, []string) {
	out, warnings := copyAnalyzerResponseHeaders(src)
	for _, key := range []string{"Anthropic-Ratelimit-Requests-Limit", "Anthropic-Ratelimit-Tokens-Limit"} {
		if out.Get(key) != "" {
			out.Del(key)
			warnings = append(warnings, "dropped Anthropic-specific response header: "+key)
		}
	}
	if isStream {
		out.Set("Content-Type", "text/event-stream")
		if out.Get("Cache-Control") == "" {
			out.Set("Cache-Control", "no-cache")
		}
	}
	return out, warnings
}

func copyAnalyzerSafeHeaders(src http.Header, protocolType int, isRequest bool) (http.Header, []string) {
	out := http.Header{}
	var warnings []string
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		lower := strings.ToLower(canonical)
		if isAnalyzerHopByHopHeader(lower) || lower == "host" || lower == "cookie" || lower == "set-cookie" || lower == "content-length" {
			warnings = append(warnings, "dropped hop-by-hop or unsupported header: "+canonical)
			continue
		}
		if isRequest && !ShouldForwardProxyHeader(canonical, protocolType) && !isAnalyzerHeaderAllowed(lower) {
			warnings = append(warnings, "dropped unsupported request header: "+canonical)
			continue
		}
		for _, value := range values {
			out.Add(canonical, value)
		}
	}
	return out, warnings
}

func copyAnalyzerResponseHeaders(src http.Header) (http.Header, []string) {
	out := http.Header{}
	var warnings []string
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		lower := strings.ToLower(canonical)
		if isAnalyzerHopByHopHeader(lower) || lower == "host" || lower == "server" || lower == "via" || lower == "content-length" {
			warnings = append(warnings, "dropped hop-by-hop or internal response header: "+canonical)
			continue
		}
		if !isAnalyzerResponseHeaderAllowed(lower) {
			warnings = append(warnings, "dropped unsupported response header: "+canonical)
			continue
		}
		for _, value := range values {
			out.Add(canonical, value)
		}
	}
	return out, warnings
}

func isAnalyzerHopByHopHeader(lower string) bool {
	switch lower {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

func isAnalyzerHeaderAllowed(lower string) bool {
	return lower == "content-type" || lower == "accept" || lower == "user-agent" || lower == "x-request-id" || lower == "authorization" || lower == "x-api-key" || lower == "api-key" || lower == "anthropic-version" || lower == "anthropic-beta" || lower == "openai-beta" || strings.HasPrefix(lower, "x-stainless-") || strings.HasPrefix(lower, "openai-")
}

func isAnalyzerResponseHeaderAllowed(lower string) bool {
	return lower == "content-type" || lower == "cache-control" || lower == "request-id" || lower == "x-request-id" || lower == "retry-after" || strings.HasPrefix(lower, "x-ratelimit-") || strings.HasPrefix(lower, "openai-") || strings.HasPrefix(lower, "anthropic-ratelimit-")
}

// ============================================================================
// 工具函数
// ============================================================================

// extractStringContent 从 interface{} 中提取字符串内容
func extractStringContent(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	// 尝试 JSON 序列化
	b, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf("%v", content)
	}
	return string(b)
}

// extractTextPartsContent 提取文本内容（v2.0.72 新增）：
// 字符串直返；[]interface{} / []AnthropicContentBlock 逐块提取 text 字段用 "\n" 拼接；
// 其他类型回退 extractStringContent。
// 用于 system 数组形态、assistant content 数组+tool_calls 等场景——
// 此前这些场景被整体 JSON dump 成 "[{\"type\":\"text\",...}]" 形式的伪文本。
func extractTextPartsContent(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	if blocks, ok := content.([]AnthropicContentBlock); ok {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		return ""
	}
	if arr, ok := content.([]interface{}); ok {
		var parts []string
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, _ := m["text"].(string); text != "" {
						parts = append(parts, text)
					}
				}
			} else if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		return ""
	}
	return extractStringContent(content)
}

func convertStopToStopSequences(stop interface{}) []string {
	switch v := stop.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []interface{}:
		var stops []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				stops = append(stops, s)
			}
		}
		return stops
	case []string:
		return append([]string(nil), v...)
	}
	return nil
}

func collectBodyCompatibilityWarnings(input []byte, direction, section string) []string {
	var data map[string]interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		return nil
	}
	var warnings []string
	if section == ProtocolAnalyzerSectionRequestBody {
		if direction == "o2a" {
			for _, key := range []string{"presence_penalty", "frequency_penalty", "response_format", "seed", "logprobs", "top_logprobs"} {
				if _, ok := data[key]; ok {
					warnings = append(warnings, "OpenAI request field has no exact Anthropic equivalent: "+key)
				}
			}
			if _, ok := data["temperature"]; ok {
				warnings = append(warnings, "Claude Opus 4.7/4.8 reject temperature/top_p/top_k; analyzer preserves the field for protocol comparison")
			}
			if _, ok := data["top_p"]; ok {
				warnings = append(warnings, "Claude Opus 4.7/4.8 reject temperature/top_p/top_k; analyzer preserves the field for protocol comparison")
			}
		} else {
			for _, key := range []string{"top_k", "thinking", "metadata", "output_config"} {
				if _, ok := data[key]; ok {
					warnings = append(warnings, "Anthropic request field has no exact OpenAI equivalent: "+key)
				}
			}
		}
		return uniqueStrings(warnings)
	}
	if section == ProtocolAnalyzerSectionResponseBody {
		if direction == "a2o" {
			if content, ok := data["content"].([]interface{}); ok {
				for _, item := range content {
					block, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					if block["type"] == "thinking" {
						warnings = append(warnings, "Anthropic thinking content has no standard OpenAI chat completion response equivalent")
					}
				}
			}
		}
	}
	return uniqueStrings(warnings)
}

func uniqueStrings(items []string) []string {
	if len(items) < 2 {
		return items
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func ConvertProtocolResponseSSE(input, direction string) (interface{}, []string, error) {
	events := ParseSSEEvents(input)
	if len(events) == 0 {
		return nil, nil, fmt.Errorf("no SSE events found")
	}
	warnings := []string{"stream was aggregated before conversion; event timing and chunk boundaries are not preserved"}
	switch direction {
	case "o2a":
		openAIResp, aggregateWarnings := AggregateOpenAISSEToResponse(events)
		warnings = append(warnings, aggregateWarnings...)
		out, err := ConvertOpenAIToAnthropicResponse(openAIResp)
		return out, uniqueStrings(warnings), err
	case "a2o":
		anthropicResp, aggregateWarnings := AggregateAnthropicSSEToResponse(events)
		warnings = append(warnings, aggregateWarnings...)
		out, err := ConvertAnthropicToOpenAIResponse(anthropicResp)
		return out, uniqueStrings(warnings), err
	default:
		return nil, warnings, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
}
