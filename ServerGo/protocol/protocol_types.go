package protocol

// ============================================================================
// OpenAI 协议结构体定义
// ============================================================================

// OpenAIChatCompletionRequest OpenAI 聊天完成请求
type OpenAIChatCompletionRequest struct {
	Model               string               `json:"model"`
	Messages            []OpenAIMessage      `json:"messages"`
	Stream              bool                 `json:"stream,omitempty"`
	StreamOptions       *OpenAIStreamOptions `json:"stream_options,omitempty"`
	MaxTokens           int                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	// v2.0.72: Temperature/TopP 改为 *float64，显式 0 值可透传（omitempty 下 float64 的 0 会被静默丢弃）
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	// v2.0.73: reasoning_effort（o 系列模型思考强度，借鉴 cc-switch resolve_reasoning_effort）
	ReasoningEffort  *string      `json:"reasoning_effort,omitempty"`
	Tools            []OpenAITool `json:"tools,omitempty"`
	ToolChoice       interface{}  `json:"tool_choice,omitempty"`
	ResponseFormat   interface{}  `json:"response_format,omitempty"`
	PresencePenalty  float64      `json:"presence_penalty,omitempty"`
	FrequencyPenalty float64      `json:"frequency_penalty,omitempty"`
	Stop             interface{}  `json:"stop,omitempty"`
	User             string       `json:"user,omitempty"`
}

// OpenAIMessage OpenAI 消息
type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	// v2.0.73: o 系列模型安全拒绝时 content 为 nil，refusal 含拒绝原因文本
	Refusal string `json:"refusal,omitempty"`
}

// OpenAITool OpenAI 工具定义
type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

// OpenAIFunction OpenAI 函数定义
type OpenAIFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// OpenAIToolCall OpenAI 工具调用
type OpenAIToolCall struct {
	// v2.0.72: Index 仅在流式 delta 中出现（*int 区分「未携带」与 0），非流式序列化时省略
	Index    *int               `json:"index,omitempty"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall OpenAI 函数调用详情
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAIStreamOptions OpenAI 流式选项
type OpenAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// OpenAIChatCompletionResponse OpenAI 聊天完成响应（非流式）
type OpenAIChatCompletionResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

// OpenAIChoice OpenAI 选择项
type OpenAIChoice struct {
	Index        int            `json:"index"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	Delta        *OpenAIMessage `json:"delta,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
}

// OpenAIUsage OpenAI Token 使用统计
type OpenAIUsage struct {
	PromptTokens            int                            `json:"prompt_tokens"`
	CompletionTokens        int                            `json:"completion_tokens"`
	TotalTokens             int                            `json:"total_tokens"`
	PromptTokensDetails     *OpenAIPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *OpenAICompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// OpenAIPromptTokensDetails OpenAI prompt token 明细（v2.0.72 新增，承载缓存 token）
type OpenAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// OpenAICompletionTokensDetails OpenAI completion token 明细（v2.0.72 新增）
type OpenAICompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// OpenAIStreamResponse OpenAI 流式响应事件
type OpenAIStreamResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

// ============================================================================
// Anthropic 协议结构体定义
// ============================================================================

// AnthropicMessagesRequest Anthropic Messages API 请求
type AnthropicMessagesRequest struct {
	Model     string             `json:"model"`
	Messages  []AnthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
	System    interface{}        `json:"system,omitempty"`
	// v2.0.72: Temperature/TopP 改为 *float64，显式 0 值可透传
	Temperature   *float64               `json:"temperature,omitempty"`
	TopP          *float64               `json:"top_p,omitempty"`
	TopK          int                    `json:"top_k,omitempty"`
	Tools         []AnthropicTool        `json:"tools,omitempty"`
	ToolChoice    interface{}            `json:"tool_choice,omitempty"`
	Thinking      interface{}            `json:"thinking,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	StopSequences []string               `json:"stop_sequences,omitempty"`
	OutputConfig  interface{}            `json:"output_config,omitempty"`
}

// AnthropicMessage Anthropic 消息
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// AnthropicContentBlock Anthropic 内容块
// v2.0.72: 改为与真实线格式一致的平铺建模。
// tool_use 块：{"type":"tool_use","id":"toolu_...","name":"...","input":{...}}
// tool_result 块：{"type":"tool_result","tool_use_id":"...","content":...,"is_error":...}
// 此前嵌套建模（{"type":"tool_use","tool_use":{...}}）与线格式不符，
// 导致真实 Anthropic JSON 反序列化后 tool_use/tool_result 永远为 nil、整体丢失。
type AnthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// tool_use 平铺字段
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
	// tool_result 平铺字段
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
	// image 块 source（{"type":"url","url":...} 或 {"type":"base64","media_type":...,"data":...}）
	Source map[string]interface{} `json:"source,omitempty"`
	// thinking 块
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// AnthropicTool Anthropic 工具定义
type AnthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// AnthropicMessagesResponse Anthropic Messages API 响应（非流式）
type AnthropicMessagesResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []AnthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason,omitempty"`
	StopSequence string                  `json:"stop_sequence,omitempty"`
	Usage        *AnthropicUsage         `json:"usage,omitempty"`
}

// AnthropicUsage Anthropic Token 使用统计
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// v2.0.72 新增：缓存 token 明细
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}
