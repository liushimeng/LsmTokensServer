package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// 默认 OpenClaw 配置常量
const (
	DefaultOpenClawURL          = "http://127.0.0.1:18789/v1/chat/completions"
	DefaultOpenClawAPIKey       = "" // 脱敏删除
	DefaultOpenClawModel        = "openclaw"
	DefaultOpenClawSystemPrompt = "你是一个专业的网络爬虫助手。你的所有输出内容（包括思考过程、分析说明、执行日志、结果报告）必须始终使用中文，严禁使用英文输出任何正文内容。代码片段、技术术语、API 路径和参数名可以保留英文原文，但所有描述、解释和总结部分必须为中文。请严格按照用户给出的任务指令执行爬虫操作。"
)

// ==================== OpenClaw 数据结构定义 ====================

// OpenClawMessage - 单条消息
type OpenClawMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenClawRequest - 请求结构体
type OpenClawRequest struct {
	SessionID   string            `json:"session_id"`
	Model       string            `json:"model"`
	Messages    []OpenClawMessage `json:"messages"`
	Temperature float64           `json:"temperature"`
	MaxTokens   int               `json:"max_tokens"`
	TopP        float64           `json:"top_p"`
	Stream      bool              `json:"stream"`
}

// OpenClawStreamResponse - 流式响应结构体
type OpenClawStreamResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string `json:"role,omitempty"`
			Content          string `json:"content,omitempty"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// OpenClawErrorResponse - 非流式错误响应
type OpenClawErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// OpenClawStreamCallback - 流式响应回调函数类型
// 参数:
//   - content: 当前收到的内容片段
//   - reasoningContent: 推理内容 (如果有)
//   - done: 是否完成
//   - err: 错误信息 (如果有)
type OpenClawStreamCallback func(content string, reasoningContent string, done bool, err error)

// openClawTransport 包级全局 Transport，复用连接池避免每次调用新建 Transport 导致连接泄漏
var openClawTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 60 * time.Second,
	}).DialContext,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: 10 * time.Minute,
	MaxIdleConns:          100,
	IdleConnTimeout:       24 * time.Hour,
}

// openClawClient 包级全局 Client，不设置 Timeout，完全由 ctx 控制生命周期
var openClawClient = &http.Client{Transport: openClawTransport}

// ==================== OpenClaw SSE 客户端 ====================

// CallOpenClawStream 调用 OpenClaw 服务并流式返回结果
//
// 参数:
//   - ctx: 上下文，用于取消请求
//   - apiURL: OpenClaw API 地址
//   - apiKey: OpenClaw API Key
//   - model: 模型名称
//   - sessionID: 会话ID
//   - systemPrompt: 系统提示词
//   - userPrompt: 用户提示词
//   - callback: 流式回调函数
func CallOpenClawStream(ctx context.Context, apiURL, apiKey, model, sessionID, systemPrompt, userPrompt string, callback OpenClawStreamCallback) error {
	if systemPrompt == "" {
		systemPrompt = DefaultOpenClawSystemPrompt
	}
	if userPrompt == "" {
		return fmt.Errorf("userPrompt cannot be empty")
	}

	// 构建请求体
	reqBody := OpenClawRequest{
		SessionID:   sessionID,
		Model:       model,
		Temperature: 0.6,
		MaxTokens:   32768,
		TopP:        0.95,
		Stream:      true,
		Messages: []OpenClawMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		if callback != nil {
			callback("", "", true, fmt.Errorf("json.Marshal: %v", err))
		}
		return fmt.Errorf("json.Marshal: %v", err)
	}

	// 创建带上下文的 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		if callback != nil {
			callback("", "", true, fmt.Errorf("http.NewRequestWithContext: %v", err))
		}
		return fmt.Errorf("http.NewRequestWithContext: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := openClawClient.Do(req)
	if err != nil {
		select {
		case <-ctx.Done():
			if callback != nil {
				callback("", "", true, ctx.Err())
			}
			return ctx.Err()
		default:
		}
		if callback != nil {
			callback("", "", true, fmt.Errorf("client.Do: %v", err))
		}
		return fmt.Errorf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) // 限制错误响应体 4KB 防止内存暴涨
		var errResp OpenClawErrorResponse
		var finalErr error
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			finalErr = fmt.Errorf("API error: %s (code: %s, type: %s)",
				errResp.Error.Message, errResp.Error.Code, errResp.Error.Type)
		} else {
			finalErr = fmt.Errorf("resp.StatusCode=%d, body=%s", resp.StatusCode, string(body))
		}
		if callback != nil {
			callback("", "", true, finalErr)
		}
		return finalErr
	}

	// 处理流式响应
	reader := bufio.NewReader(resp.Body)
	for {
		// 检查 ctx 是否已取消
		select {
		case <-ctx.Done():
			if callback != nil {
				callback("", "", true, ctx.Err())
			}
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			if callback != nil {
				callback("", "", true, fmt.Errorf("reader.ReadString: %v", err))
			}
			return fmt.Errorf("reader.ReadString: %v", err)
		}

		line = strings.TrimPrefix(line, "data: ")
		line = strings.TrimSpace(line)

		if line == "" || line == "[DONE]" {
			if line == "[DONE]" {
				break
			}
			continue
		}

		var streamResp OpenClawStreamResponse
		if err := json.Unmarshal([]byte(line), &streamResp); err != nil {
			continue // 忽略解析错误
		}

		var contentChunk string
		var reasoningChunk string
		for _, choice := range streamResp.Choices {
			if choice.Delta.Content != "" {
				contentChunk = choice.Delta.Content
			}
			if choice.Delta.ReasoningContent != "" {
				reasoningChunk = choice.Delta.ReasoningContent
			}
		}

		if contentChunk != "" || reasoningChunk != "" {
			if callback != nil {
				callback(contentChunk, reasoningChunk, false, nil)
			}
		}
	}

	// 通知完成
	if callback != nil {
		callback("", "", true, nil)
	}

	return nil
}
