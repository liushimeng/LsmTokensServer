package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"github.com/lishimeng/LsmTokensServer/recognizer"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// extractAPIKey 从请求头提取 Bearer API Key
func extractAPIKey(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", fmt.Errorf("missing Authorization header")
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("invalid Authorization format, expected 'Bearer {api_key}'")
	}
	key := strings.TrimSpace(parts[1])
	if key == "" {
		return "", fmt.Errorf("empty API Key")
	}
	return key, nil
}

// isValidProtocol 验证用户是否启用了指定协议
func isValidProtocol(user *modelsdb.TAgentHttpUserInfo, protocolType int) bool {
	switch protocolType {
	case protocol.AgentProtocolType_Anthropic:
		return user.AnthropicEnabled
	case protocol.AgentProtocolType_OpenAI:
		return user.OpenAIEnabled
	default:
		return false
	}
}

// getRelativePath 提取相对路径（去掉 Anthropic 或 OpenAI 前缀）
func getRelativePath(path string, protocolType int) string {
	prefix := "/" + config.G.AgentAnthropicListenURL
	if protocolType == protocol.AgentProtocolType_OpenAI {
		prefix = "/" + config.G.AgentOpenAIListenURL
	}
	rel, ok := strings.CutPrefix(path, prefix)
	if ok {
		if rel == "" {
			return "/"
		}
		return rel
	}
	return path
}

// parseModelFromBody 从请求体 JSON 中解析 model 字段
func parseModelFromBody(body []byte) (string, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}
	model, ok := req["model"].(string)
	if !ok || model == "" {
		return "", fmt.Errorf("model field not found or empty")
	}
	return model, nil
}

// replaceModelInBody 字节级精准替换请求体 JSON 中的 model 字段
// 完全保留其他字段的原始格式、字段顺序、缩进、空格，不做任何 JSON 解析
func replaceModelInBody(body []byte, oldModel, newModel string) []byte {
	if oldModel == newModel {
		return body
	}

	// 处理 JSON 中 key-value 周围可能存在 0-N 个空格的各种情况
	// 先生成基础字符串，再转为 []byte
	patterns := []struct{ search, replace []byte }{
		{[]byte(`"model":"` + oldModel + `"`), []byte(`"model":"` + newModel + `"`)},
		{[]byte(`"model": "` + oldModel + `"`), []byte(`"model": "` + newModel + `"`)},
		{[]byte(`"model" :"` + oldModel + `"`), []byte(`"model" :"` + newModel + `"`)},
		{[]byte(`"model" : "` + oldModel + `"`), []byte(`"model" : "` + newModel + `"`)},
	}

	for _, p := range patterns {
		if bytes.Contains(body, p.search) {
			return bytes.Replace(body, p.search, p.replace, 1)
		}
	}

	return body
}

// parseTokensFromResponseBody 从响应体中解析 Tokens 使用量
// 支持非流式 JSON 和流式 SSE 两种格式
// Anthropic 协议: usage.input_tokens / usage.output_tokens
// OpenAI 协议: usage.prompt_tokens / usage.completion_tokens / usage.total_tokens
func parseTokensFromResponseBody(respBody string, protocolType int) (input, output, total uint64) {
	if respBody == "" || strings.Contains(respBody, "[truncated]") {
		return 0, 0, 0
	}

	// 1. 尝试直接解析 JSON（非流式响应）
	input, output, total = parseTokensFromJSONResponse(respBody, protocolType)
	if input > 0 || output > 0 || total > 0 {
		return
	}

	// 2. 尝试 SSE 解析（流式响应）
	input, output, total = parseTokensFromSSEResponse(respBody, protocolType)
	return
}

// parseTokensFromJSONResponse 从非流式 JSON 响应中解析 Tokens
func parseTokensFromJSONResponse(respBody string, protocolType int) (input, output, total uint64) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(respBody), &data); err != nil {
		return 0, 0, 0
	}

	usageRaw, ok := data["usage"]
	if !ok {
		return 0, 0, 0
	}
	usage, ok := usageRaw.(map[string]interface{})
	if !ok {
		return 0, 0, 0
	}

	switch protocolType {
	case protocol.AgentProtocolType_Anthropic:
		if v, ok := usage["input_tokens"].(float64); ok {
			input = uint64(v)
		}
		if v, ok := usage["output_tokens"].(float64); ok {
			output = uint64(v)
		}
	case protocol.AgentProtocolType_OpenAI:
		if v, ok := usage["prompt_tokens"].(float64); ok {
			input = uint64(v)
		}
		if v, ok := usage["completion_tokens"].(float64); ok {
			output = uint64(v)
		}
		if v, ok := usage["total_tokens"].(float64); ok {
			total = uint64(v)
		}
	}

	if total == 0 {
		total = input + output
	}
	return
}

// parseTokensFromSSEResponse 从 SSE 流式响应中解析 Tokens
func parseTokensFromSSEResponse(respBody string, protocolType int) (input, output, total uint64) {
	events := protocol.ParseSSEEvents(respBody)
	if len(events) == 0 {
		return 0, 0, 0
	}

	switch protocolType {
	case protocol.AgentProtocolType_Anthropic:
		input, output = extractTokensFromAnthropicSSE(events)
	case protocol.AgentProtocolType_OpenAI:
		input, output, total = extractTokensFromOpenAISSE(events)
	}

	if total == 0 {
		total = input + output
	}
	return
}

// parseSSEEvents 已迁至 protocol/sse.go，统一通过 protocol.ParseSSEEvents 调用。
// 旧工程迁移期遗留的重复定义已移除。

// extractTokensFromAnthropicSSE 从 Anthropic SSE 事件流中提取 Tokens
// 等效于 JS aggregateSSE 中的 usage 提取逻辑
func extractTokensFromAnthropicSSE(events []protocol.SSEEvent) (input, output uint64) {
	for _, ev := range events {
		if ev.Data == "" {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(ev.Data), &data); err != nil {
			continue
		}

		switch ev.Event {
		case "message_start":
			if msgRaw, ok := data["message"].(map[string]interface{}); ok {
				if usageRaw, ok := msgRaw["usage"].(map[string]interface{}); ok {
					if v, ok := usageRaw["input_tokens"].(float64); ok {
						input = uint64(v)
					}
				}
			}
		case "message_delta":
			if usageRaw, ok := data["usage"].(map[string]interface{}); ok {
				if v, ok := usageRaw["output_tokens"].(float64); ok {
					output = uint64(v)
				}
			}
		}
	}
	return
}

// extractTokensFromOpenAISSE 从 OpenAI SSE 事件流中提取 Tokens
// 等效于 JS aggregateOpenAI 中的 usage 提取逻辑
func extractTokensFromOpenAISSE(events []protocol.SSEEvent) (input, output, total uint64) {
	for _, ev := range events {
		if ev.Data == "" {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(ev.Data), &data); err != nil {
			continue
		}

		if usageRaw, ok := data["usage"].(map[string]interface{}); ok {
			if v, ok := usageRaw["prompt_tokens"].(float64); ok {
				input = uint64(v)
			}
			if v, ok := usageRaw["completion_tokens"].(float64); ok {
				output = uint64(v)
			}
			if v, ok := usageRaw["total_tokens"].(float64); ok {
				total = uint64(v)
			}
		}
	}
	return
}

// estimateTokensFromSize 根据请求/响应体大小估算 Tokens 数量（兜底策略）
// 粗略估算：约 4 字符 = 1 token
func estimateTokensFromSize(reqSize, respSize uint64) (input, output, total uint64) {
	const charsPerToken = 4
	if reqSize > 0 {
		input = reqSize / charsPerToken
	}
	if respSize > 0 {
		output = respSize / charsPerToken
	}
	total = input + output
	return
}

// forwardWithRetry 带透明重试的代理转发
// 根据路由的算法策略选择源站，遇到可切换错误时自动重试下一个源站
// 返回：最终响应、使用的目标源站、目标URL、转发请求体、错误
func forwardWithRetry(
	r *http.Request,
	cachedRoute *modelsdb.CachedAIRoute,
	protocolType int,
	bodyBytes []byte,
	reqModelName string,
	relativePath string,
) (*proxyForwardResult, error) {
	selector := modelsdb.GetAlgorithmSelector(cachedRoute.AlgorithmStrategyType)
	stableSelector, isStable := selector.(*modelsdb.StableAlgorithmSelector)
	economicSelector, isEconomic := selector.(*modelsdb.EconomicAlgorithmSelector)

	// 经济型算法：通过 Session 识别层按协议类型解析 session_id
	var economicSessionID string
	// economicKBRequest: true 表示走「知识问答」分支（v2.0.17），从 DstEndPointIDs 随机挑源站
	var economicKBRequest bool
	if isEconomic {
		economicSessionID = recognizer.RecognizeSessionID(bodyBytes, protocolType, r.Header)
		if economicSessionID == "" {
			// v2.0.20: opencode / OpenAI/Python 合成 session —— 按 userName+modelName
			// 维度缓存合成 session_id，让连续请求走 SelectForSession 的 session 粘性路径。
			ua := r.Header.Get("User-Agent")
			if ua == "" {
				ua = r.Header.Get("X-Client-Name")
			}
			agentName := recognizer.RecognizeAgentTool(ua).AgentToolName
			if modelsdb.IsSyntheticSessionEligibleAgent(agentName) {
				if user, ok := modelsdb.GetCachedUserByID(cachedRoute.UserID); ok {
					if synthID, synthOK := modelsdb.GetOrSynthesizeSessionID(user.UserName, reqModelName); synthOK {
						economicSessionID = synthID
						logger.Printf("[ECONOMIC] Route %d: synthesized session_id=%s for agent=%q, user=%s, model=%s",
							cachedRoute.ID, synthID, agentName, user.UserName, reqModelName)
					}
				}
			}
			if economicSessionID == "" {
				logger.Printf("[ECONOMIC] Route %d: no session_id in body (protocol=%d), fallback to round-robin logic", cachedRoute.ID, protocolType)
				// v2.0.17：识别本次请求是否属于「知识问答」（无 session / 无 tool / 非高阶 Agent）
				toolNames := modelsdb.ExtractRequestToolNamesForAlgorithm(bodyBytes)
				if modelsdb.IsKnowledgeBaseRequest(economicSessionID, toolNames, agentName) {
					economicKBRequest = true
					logger.Printf("[ECONOMIC] Route %d: KB request detected (ua=%q, agent=%q), will pick random endpoint from DstEndPointIDs", cachedRoute.ID, ua, agentName)
				}
			}
		}
	}

	var lastErr error
	maxRetries := len(cachedRoute.DstEndPointIDs)
	if maxRetries == 0 {
		return nil, fmt.Errorf("no destination endpoints configured")
	}

	// 经济型算法：OnEndpointFailure 返回 shouldRemove 时，
	// 记录需要移除的 endpoint ID；循环结束后统一调 modelsdb.RemoveEndpointFromAIRoute + modelsdb.SyncEconomicRouteEndpoints。
	// 这样既避免算法层直接调 database.DB（锁交叉），又保证本次失败请求被中止、由上游 SDK 重试。
	var economicRemoveID uint64
	economicShouldRemove := false

	// reportFailure 统一处理一次失败的副作用：调用对应算法的失败回调，
	// 并在需要时把 economicRemoveID 标记上。返回 true 表示应终止当前 retry 循环。
	reportFailure := func(endpointID uint64) bool {
		if isStable {
			stableSelector.OnRequestFailure(cachedRoute.ID, cachedRoute)
		} else if isEconomic {
			if shouldRemove, removedID := economicSelector.OnEndpointFailure(cachedRoute.ID, endpointID); shouldRemove {
				economicShouldRemove = true
				economicRemoveID = removedID
				return true
			}
		}
		return false
	}

	// 循环结束后的收尾：经济型若标记了移除，同步 database.DB 与算法层。
	defer func() {
		if economicShouldRemove {
			if err := modelsdb.RemoveEndpointFromAIRoute(cachedRoute.ID, economicRemoveID); err != nil {
				logger.Printf("[ECONOMIC] Route %d: failed to remove endpoint %d: %v", cachedRoute.ID, economicRemoveID, err)
				return
			}
			// 重新从缓存读取最新源站列表（modelsdb.updateRouteInCache 已替换对象）
			if latest, ok := modelsdb.GetCachedRouteByModelIDAndProtocol(cachedRoute.UserModelID, cachedRoute.ProtocolType); ok {
				modelsdb.SyncEconomicRouteEndpoints(cachedRoute.ID, latest.DstEndPointIDs)
			}
		}
	}()

	for attempt := 0; attempt < maxRetries; attempt++ {
		// 选择目标源站
		var selectedID uint64
		var ok bool
		switch {
		case isEconomic && economicSessionID != "":
			// 有 session：走 session 粘性分配（livePool 消费语义）
			selectedID, ok = economicSelector.SelectForSession(cachedRoute, economicSessionID)
		case isEconomic && economicKBRequest:
			// v2.0.17：知识问答分支，从 DstEndPointIDs 随机挑可用源站，不消费 livePool
			selectedID, ok = economicSelector.SelectForKBRequest(cachedRoute)
		default:
			// 兜底：稳定型 / 指定型 / 无 session 的经济型走原逻辑
			selectedID, ok = selector.Select(cachedRoute)
		}
		if !ok {
			return nil, fmt.Errorf("failed to select endpoint")
		}

		// 查询目标源站：代理热路径只允许读取内存缓存，禁止回退 database.DB。
		dstEndpoint, ok := modelsdb.GetCachedDstEndPointByID(selectedID)
		if !ok {
			err := fmt.Errorf("dst endpoint (id=%d) not found in cache", selectedID)
			logger.Printf("[PROXY] Dst endpoint not found in cache: id=%d", selectedID)
			lastErr = err
			if reportFailure(selectedID) {
				break
			}
			continue
		}

		// 检查源站是否被禁用
		if dstEndpoint.Status == 0 {
			logger.Printf("[PROXY] Dst endpoint is disabled: id=%d", selectedID)
			lastErr = fmt.Errorf("endpoint %d is disabled", selectedID)
			if reportFailure(selectedID) {
				break
			}
			continue
		}

		endpointAlgorithmType := cachedRoute.AlgorithmTypeForEndPointID(selectedID)

		// 替换模型名称
		newBodyBytes := replaceModelInBody(bodyBytes, reqModelName, dstEndpoint.ModelName)

		// 拼接目标 URL；协议转换器需要同步改写 API 路径，避免只转换 Body/Header 但仍转发到源协议路径。
		targetURL, err := buildProtocolAwareTargetURL(dstEndpoint.URLAddress, relativePath, r.URL.RawQuery, protocolType, dstEndpoint.ProtocolType, endpointAlgorithmType)
		if err != nil {
			logger.Printf("[PROXY] Invalid dst URL: %s, err=%v", dstEndpoint.URLAddress, err)
			lastErr = err
			if reportFailure(selectedID) {
				break
			}
			continue
		}

		// 创建转发请求
		proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(newBodyBytes))
		if err != nil {
			logger.Printf("[PROXY] Failed to create proxy request: %v", err)
			lastErr = err
			if reportFailure(selectedID) {
				break
			}
			continue
		}

		// 复制安全请求头，丢弃 hop-by-hop、伪造转发链、Cookie 和客户端密钥头。
		copySafeProxyRequestHeaders(proxyReq.Header, r.Header, getProxyClientIP(r), protocolType)
		proxyReq.Header.Set("Authorization", "Bearer "+dstEndpoint.APIKey)
		if endpointAlgorithmType == modelsdb.DstEndPointAlgorithmType_ProtocolConverter {
			convertedBody, convertedHeaders, err := convertProxyRequest(newBodyBytes, proxyReq.Header, protocolType, dstEndpoint.ProtocolType)
			if err != nil {
				logger.Printf("[PROXY] Request protocol conversion failed for endpointID=%d: %v", selectedID, err)
				lastErr = err
				if reportFailure(selectedID) {
					break
				}
				continue
			}
			newBodyBytes = convertedBody
			proxyReq.Body = io.NopCloser(bytes.NewReader(newBodyBytes))
			proxyReq.Header = convertedHeaders
			proxyReq.Header.Set("Authorization", "Bearer "+dstEndpoint.APIKey)
		}
		proxyReq.ContentLength = int64(len(newBodyBytes))
		proxyReq.Header.Set("Content-Length", strconv.Itoa(len(newBodyBytes)))
		proxyReqHeadersText := formatRawHeaders(proxyReq.Header)
		srcReqHeadersText := formatRawHeaders(r.Header)

		logger.Printf("[PROXY] Attempt %d/%d: forwarding to endpointID=%d (%s), endpointAlgorithm=%d", attempt+1, maxRetries, selectedID, dstEndpoint.URLAddress, endpointAlgorithmType)

		// 转发请求
		resp, err := sharedHTTPClient.Do(proxyReq)
		if err != nil {
			logger.Printf("[PROXY] Forward failed for endpointID=%d: %v", selectedID, err)
			lastErr = err
			if reportFailure(selectedID) {
				break
			}
			continue
		}

		// 检查是否需要故障转移（仅在收到响应头后、开始透传数据前）
		if modelsdb.IsFailoverError(resp.StatusCode, nil) {
			logger.Printf("[PROXY] Failover error %d from endpointID=%d, will retry next", resp.StatusCode, selectedID)
			resp.Body.Close()
			lastErr = fmt.Errorf("endpoint %d returned status %d", selectedID, resp.StatusCode)
			if reportFailure(selectedID) {
				break
			}
			continue
		}

		// 请求成功
		if isStable {
			stableSelector.OnRequestSuccess(cachedRoute.ID)
		} else if isEconomic {
			economicSelector.OnRequestSuccess(cachedRoute.ID)
		}
		return &proxyForwardResult{
			Response:                 resp,
			DstEndpoint:              dstEndpoint,
			TargetURL:                targetURL,
			ProxyRequestBody:         newBodyBytes,
			SrcRequestBody:           bodyBytes,
			DstEndPointAlgorithmType: endpointAlgorithmType,
			ProxyRequestHeadersText:  proxyReqHeadersText,
			SrcRequestHeadersText:    srcReqHeadersText,
		}, nil
	}

	return nil, fmt.Errorf("all %d endpoints failed: %w", maxRetries, lastErr)
}

func protocolConvertDirection(srcProtocolType, dstProtocolType int) (string, error) {
	if srcProtocolType == protocol.AgentProtocolType_OpenAI && dstProtocolType == protocol.AgentProtocolType_Anthropic {
		return "o2a", nil
	}
	if srcProtocolType == protocol.AgentProtocolType_Anthropic && dstProtocolType == protocol.AgentProtocolType_OpenAI {
		return "a2o", nil
	}
	return "", fmt.Errorf("unsupported protocol conversion: %d -> %d", srcProtocolType, dstProtocolType)
}

// MarshalConvertedProtocolBody 已迁至 protocol/marshal.go

func convertProxyRequest(body []byte, headers http.Header, srcProtocolType, dstProtocolType int) ([]byte, http.Header, error) {
	direction, err := protocolConvertDirection(srcProtocolType, dstProtocolType)
	if err != nil {
		return nil, nil, err
	}
	converted, _, warnings, err := protocol.ConvertProtocolRequestBody(body, direction)
	if len(warnings) > 0 {
		logger.Printf("[PROXY] Request protocol conversion warnings: %s", strings.Join(warnings, "; "))
	}
	if err != nil {
		return nil, nil, err
	}
	convertedBody, err := protocol.MarshalConvertedProtocolBody(converted)
	if err != nil {
		return nil, nil, err
	}
	var convertedHeaders http.Header
	switch direction {
	case "o2a":
		convertedHeaders, warnings = protocol.ConvertOpenAIToAnthropicRequestHeaders(headers)
	case "a2o":
		convertedHeaders, warnings = protocol.ConvertAnthropicToOpenAIRequestHeaders(headers)
	}
	if len(warnings) > 0 {
		logger.Printf("[PROXY] Request header conversion warnings: %s", strings.Join(warnings, "; "))
	}
	return convertedBody, convertedHeaders, nil
}

func convertProxyResponse(resp *http.Response, clientProtocolType, dstProtocolType int, clientWantsStream bool) ([]byte, http.Header, []byte, error) {
	direction, err := protocolConvertDirection(dstProtocolType, clientProtocolType)
	if err != nil {
		return nil, nil, nil, err
	}
	srcBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, nil, err
	}
	isStream := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	var converted interface{}
	var warnings []string
	wrapAsSSE := false
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		converted, warnings, err = protocol.ConvertProtocolErrorResponseBody(srcBody, direction)
		isStream = false
	} else if isStream {
		converted, warnings, err = protocol.ConvertProtocolResponseSSE(string(srcBody), direction)
		// v2.0.72: 上游 SSE 聚合转换后是单个完整响应对象，必须重新包装成客户端协议的
		// 合法 SSE 事件流再写出（此前直接把 JSON blob 配上 text/event-stream 发给客户端，协议自相矛盾）
		wrapAsSSE = true
	} else {
		converted, _, warnings, err = protocol.ConvertProtocolResponseBody(srcBody, direction)
		wrapAsSSE = clientWantsStream
	}
	if len(warnings) > 0 {
		logger.Printf("[PROXY] Response protocol conversion warnings: %s", strings.Join(warnings, "; "))
	}
	if err != nil {
		return nil, nil, nil, err
	}
	convertedBody, err := marshalConvertedProtocolBody(converted)
	if err != nil {
		return nil, nil, nil, err
	}
	if wrapAsSSE {
		convertedBody, err = wrapConvertedResponseAsSSE(convertedBody, clientProtocolType)
		if err != nil {
			return nil, nil, nil, err
		}
		isStream = true
	}
	var convertedHeaders http.Header
	switch direction {
	case "o2a":
		convertedHeaders, warnings = protocol.ConvertOpenAIToAnthropicResponseHeaders(resp.Header, isStream)
	case "a2o":
		convertedHeaders, warnings = protocol.ConvertAnthropicToOpenAIResponseHeaders(resp.Header, isStream)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		convertedHeaders.Set("Content-Type", "application/json")
	}
	if len(warnings) > 0 {
		logger.Printf("[PROXY] Response header conversion warnings: %s", strings.Join(warnings, "; "))
	}
	return convertedBody, convertedHeaders, srcBody, nil
}

func requestBodyWantsStream(body []byte) bool {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	stream, _ := payload["stream"].(bool)
	return stream
}

func wrapConvertedResponseAsSSE(body []byte, clientProtocolType int) ([]byte, error) {
	switch clientProtocolType {
	case protocol.AgentProtocolType_OpenAI:
		return wrapOpenAIResponseAsSSE(body)
	case protocol.AgentProtocolType_Anthropic:
		return wrapAnthropicResponseAsSSE(body)
	default:
		return body, nil
	}
}

func wrapOpenAIResponseAsSSE(body []byte) ([]byte, error) {
	var resp protocol.OpenAIChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var b strings.Builder
	if len(resp.Choices) == 0 {
		b.WriteString("data: ")
		b.Write(body)
		b.WriteString("\n\n")
	} else {
		for _, choice := range resp.Choices {
			chunk := protocol.OpenAIStreamResponse{ID: resp.ID, Object: "chat.completion.chunk", Created: resp.Created, Model: resp.Model}
			delta := protocol.OpenAIMessage{Role: "assistant"}
			if choice.Message != nil {
				delta.Content = choice.Message.Content
				delta.ToolCalls = choice.Message.ToolCalls
			}
			chunk.Choices = []protocol.OpenAIChoice{{Index: choice.Index, Delta: &delta, FinishReason: choice.FinishReason}}
			chunkBody, err := json.Marshal(chunk)
			if err != nil {
				return nil, err
			}
			b.WriteString("data: ")
			b.Write(chunkBody)
			b.WriteString("\n\n")
		}
		if resp.Usage != nil {
			usageChunk := protocol.OpenAIStreamResponse{ID: resp.ID, Object: "chat.completion.chunk", Created: resp.Created, Model: resp.Model, Choices: []protocol.OpenAIChoice{}, Usage: resp.Usage}
			usageBody, err := json.Marshal(usageChunk)
			if err != nil {
				return nil, err
			}
			b.WriteString("data: ")
			b.Write(usageBody)
			b.WriteString("\n\n")
		}
	}
	b.WriteString("data: [DONE]\n\n")
	return []byte(b.String()), nil
}

func wrapAnthropicResponseAsSSE(body []byte) ([]byte, error) {
	var resp protocol.AnthropicMessagesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var b strings.Builder
	start := map[string]interface{}{"type": "message_start", "message": resp}
	startBody, err := json.Marshal(start)
	if err != nil {
		return nil, err
	}
	b.WriteString("event: message_start\n")
	b.WriteString("data: ")
	b.Write(startBody)
	b.WriteString("\n\n")

	// v2.0.72: 空 content 响应补一对空 text 块（否则不是合法 Anthropic 流——借鉴 Switchyard finish_anthropic_stream）
	content := resp.Content
	if len(content) == 0 {
		content = []protocol.AnthropicContentBlock{{Type: "text", Text: ""}}
	}

	writeEvent := func(eventType string, payload map[string]interface{}) error {
		payloadBody, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		b.WriteString("event: " + eventType + "\n")
		b.WriteString("data: ")
		b.Write(payloadBody)
		b.WriteString("\n\n")
		return nil
	}

	for i, block := range content {
		// content_block_start 必须携带块的关键字段（v2.0.72: 此前 tool_use 只发 {"type":"tool_use"}，id/name/input 全丢）
		var startContentBlock map[string]interface{}
		switch block.Type {
		case "tool_use":
			startContentBlock = map[string]interface{}{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": map[string]interface{}{},
			}
		case "thinking":
			startContentBlock = map[string]interface{}{"type": "thinking", "thinking": ""}
		default:
			startContentBlock = map[string]interface{}{"type": block.Type, "text": ""}
		}
		if err := writeEvent("content_block_start", map[string]interface{}{"type": "content_block_start", "index": i, "content_block": startContentBlock}); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
			if block.Text != "" {
				if err := writeEvent("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": i, "delta": map[string]interface{}{"type": "text_delta", "text": block.Text}}); err != nil {
					return nil, err
				}
			}
		case "tool_use":
			// input 经 input_json_delta 发出（Anthropic 流式契约）
			inputJSON, err := json.Marshal(block.Input)
			if err != nil {
				return nil, err
			}
			if len(inputJSON) > 0 && string(inputJSON) != "null" {
				if err := writeEvent("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": i, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": string(inputJSON)}}); err != nil {
					return nil, err
				}
			}
		case "thinking":
			if block.Thinking != "" {
				if err := writeEvent("content_block_delta", map[string]interface{}{"type": "content_block_delta", "index": i, "delta": map[string]interface{}{"type": "thinking_delta", "thinking": block.Thinking}}); err != nil {
					return nil, err
				}
			}
		}
		if err := writeEvent("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": i}); err != nil {
			return nil, err
		}
	}
	usagePayload := interface{}(resp.Usage)
	if usagePayload == nil {
		usagePayload = map[string]interface{}{"output_tokens": 0}
	}
	if err := writeEvent("message_delta", map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": resp.StopReason, "stop_sequence": resp.StopSequence}, "usage": usagePayload}); err != nil {
		return nil, err
	}
	b.WriteString("event: message_stop\n")
	b.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	return []byte(b.String()), nil
}

func writeProxyResponseHeaders(w http.ResponseWriter, headers http.Header, protocolType int, contentLength int64) {
	for key, values := range headers {
		if !protocol.ShouldForwardProxyHeader(key, protocolType) {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	if contentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	} else {
		w.Header().Del("Content-Length")
	}
}

func rewriteProtocolConvertedRelativePath(relativePath string, srcProtocolType, dstProtocolType int) string {
	cleanPath := "/" + strings.TrimPrefix(relativePath, "/")
	switch {
	case srcProtocolType == protocol.AgentProtocolType_OpenAI && dstProtocolType == protocol.AgentProtocolType_Anthropic:
		if cleanPath == "/chat/completions" || cleanPath == "/v1/chat/completions" {
			return "/v1/messages"
		}
	case srcProtocolType == protocol.AgentProtocolType_Anthropic && dstProtocolType == protocol.AgentProtocolType_OpenAI:
		if cleanPath == "/messages" || cleanPath == "/v1/messages" {
			return "/v1/chat/completions"
		}
	}
	return relativePath
}

func buildProtocolAwareTargetURL(dstURLStr, relativePath, rawQuery string, srcProtocolType, dstProtocolType, endpointAlgorithmType int) (string, error) {
	if endpointAlgorithmType == modelsdb.DstEndPointAlgorithmType_ProtocolConverter {
		relativePath = rewriteProtocolConvertedRelativePath(relativePath, srcProtocolType, dstProtocolType)
	}
	return buildTargetURL(dstURLStr, relativePath, rawQuery)
}

// buildTargetURL 拼接目标 URL
func buildTargetURL(dstURLStr, relativePath, rawQuery string) (string, error) {
	dstURL, err := url.Parse(dstURLStr)
	if err != nil {
		return "", err
	}
	if dstURL.Scheme != "https" && dstURL.Scheme != "http" {
		return "", fmt.Errorf("unsupported dst URL scheme %q", dstURL.Scheme)
	}
	if dstURL.Host == "" {
		return "", fmt.Errorf("missing dst URL host")
	}
	basePath := dstURL.Path
	if basePath == "" {
		basePath = "/"
	}
	if relativePath == "" {
		relativePath = "/"
	}
	canonicalRelativePath := "/" + strings.TrimPrefix(relativePath, "/")
	if strings.HasSuffix(strings.TrimRight(basePath, "/"), canonicalRelativePath) {
		// 源站 basePath 已包含客户端请求的整段路径（如 basePath=/coding/v1/messages + /chat/completions），
		// 直接采用 basePath，避免重复拼接。
		dstURL.Path = strings.TrimRight(basePath, "/")
	} else {
		// 去重 basePath 末段的版本前缀：当 basePath 以 /v1 结尾（如 /openai/v1、/v1），
		// 且 relativePath 也以 /v1/ 开头（如 /v1/responses、/v1/chat/completions、/v1/messages），
		// 剥掉 relativePath 的 /v1 前缀，避免产生 /openai/v1/v1/responses 这种重复 /v1 的 404 路径。
		// 兼容 /v1（不含尾斜杠，恰好等于 basePath 末段）的等价情形；
		// 但 basePath 是其他版本（/v2、/v3beta）时不剥，避免误伤。
		trimmedBase := strings.TrimRight(basePath, "/")
		if (strings.HasSuffix(trimmedBase, "/v1") || trimmedBase == "/v1") &&
			(strings.HasPrefix(canonicalRelativePath, "/v1/") || canonicalRelativePath == "/v1") {
			// 从 canonicalRelativePath（首部必带 /）剥掉 /v1 前缀：
			//   /v1/responses -> /responses
			//   /v1           -> ""        → 整体置为 /（覆盖 basePath 自身已含 /v1 的整段去重场景）
			relativePath = canonicalRelativePath[len("/v1"):]
			if relativePath == "" {
				relativePath = "/"
			}
		}
		if strings.HasSuffix(basePath, "/") && strings.HasPrefix(relativePath, "/") {
			relativePath = strings.TrimPrefix(relativePath, "/")
		}
		dstURL.Path = basePath + relativePath
	}
	if rawQuery != "" {
		if dstURL.RawQuery != "" {
			dstURL.RawQuery += "&" + rawQuery
		} else {
			dstURL.RawQuery = rawQuery
		}
	}
	return dstURL.String(), nil
}

// validateRequestModelName 校验请求中的 model 名称是否与 API Key 对应的模型名称匹配
func validateRequestModelName(reqModelName, expectedModelName string) error {
	if reqModelName != expectedModelName {
		return fmt.Errorf("model mismatch: request model=%q, expected model=%q", reqModelName, expectedModelName)
	}
	return nil
}

// setCORSHeaders 设置跨域访问响应头，允许 Web 管理页面的 JavaScript 请求
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, anthropic-version, x-api-key")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// logAIProxyTransaction 异步记录代理请求日志到分表
func logAIProxyTransaction(
	user *modelsdb.TAgentHttpUserInfo,
	userModel *modelsdb.TAgentHttpUserModelInfo,
	route *modelsdb.TAgentHttpAIRoute,
	dstEndPointID uint64,
	dstEndPointAlgorithmType int,
	dstModelName string,
	r *http.Request,
	targetURL string,
	proxyReqBody []byte,
	srcReqBody []byte,
	proxyReqContentLength int64,
	proxyReqHeaders string,
	srcReqHeaders string,
	respStatus string,
	respHeaders string,
	srcRespHeaders string,
	respBody string,
	srcRespBody string,
	respContentLength int64,
	requestStartAt, requestEndAt, responseStartAt, responseEndAt time.Time,
	elapsedMs int64,
	protocolType int,
	sessionID string,
	tokensInputSize, tokensOutputSize, tokensAllSize uint64,
) {
	if database.DB == nil || config.G == nil {
		return
	}

	// base64 编码实际转发请求的 body（协议转换后的目标请求体），落库前脱敏 JSON 中的密钥字段。
	redactedProxyReqBody := redactSensitiveJSONBody(string(proxyReqBody))
	reqBodyEncoded := base64.StdEncoding.EncodeToString([]byte(redactedProxyReqBody))

	// base64 编码原始请求体（未经协议转换的客户端原始请求体），与源协议字段一起用于解析 stream 等客户端原始特征。
	var srcBodyEncoded string
	var redactedSrcReqBody string
	if len(srcReqBody) > 0 {
		redactedSrcReqBody = redactSensitiveJSONBody(string(srcReqBody))
		srcBodyEncoded = base64.StdEncoding.EncodeToString([]byte(redactedSrcReqBody))
	}
	if redactedSrcReqBody != "" && requestBodyWantsStream([]byte(redactedSrcReqBody)) && !requestBodyWantsStream([]byte(redactedProxyReqBody)) {
		redactedProxyReqBody = redactedSrcReqBody
		reqBodyEncoded = srcBodyEncoded
	}
	respBody = redactSensitiveJSONBody(respBody)
	srcRespBody = redactSensitiveJSONBody(srcRespBody)

	// 提取 User-Agent 作为工具标识
	toolIdentifier := r.Header.Get("User-Agent")
	if toolIdentifier == "" {
		toolIdentifier = r.Header.Get("X-Client-Name")
	}

	// 识别 Agent 工具
	agentResult := recognizer.RecognizeAgentTool(toolIdentifier)

	if srcReqHeaders == "" {
		srcReqHeaders = formatRawHeaders(r.Header)
	}
	if proxyReqHeaders == "" {
		proxyReqHeaders = srcReqHeaders
	}
	if srcRespHeaders == "" {
		srcRespHeaders = respHeaders
	}
	if dstEndPointAlgorithmType == 0 {
		dstEndPointAlgorithmType = modelsdb.DstEndPointAlgorithmType_Direct
	}

	err := modelsdb.SaveAgentHttpTransaction(
		user.UserName,
		userModel.ModelName,
		int64(user.ID),
		redactAPIKey(userModel.APIKey),
		dstEndPointID,
		dstEndPointAlgorithmType,
		dstModelName,
		protocolType,
		r.Method,
		targetURL,
		r.RemoteAddr,
		proxyReqContentLength,
		proxyReqHeaders,
		srcReqHeaders,
		reqBodyEncoded,
		srcBodyEncoded,
		respStatus,
		respContentLength,
		respHeaders,
		srcRespHeaders,
		respBody,
		srcRespBody,
		requestStartAt, requestEndAt, responseStartAt, responseEndAt,
		elapsedMs,
		toolIdentifier,
		agentResult.AgentToolName,
		agentResult.AgentToolInfo,
		sessionID,
		config.G.DBMysqlSubTableNumber,
		tokensInputSize, tokensOutputSize, tokensAllSize,
	)

	// 更新 Agent 工具使用统计（异步不阻塞）
	modelsdb.UpdateAgentInfoUsage(agentResult.AgentToolName, requestStartAt)
	if err != nil {
		logger.Printf("[PROXY] Failed to save transaction log: %v", err)
	}
}

// marshalConvertedProtocolBody 转换后 body 的 marshal（委托 protocol 包实现）
func marshalConvertedProtocolBody(v interface{}) ([]byte, error) {
	return protocol.MarshalConvertedProtocolBody(v)
}

// ConvertProxyResponseForTest 导出 convertProxyResponse 供跨包测试使用
// （protocol 包的转换错误路径测试需要；生产代码请直接使用 convertProxyResponse）
func ConvertProxyResponseForTest(resp *http.Response, clientProtocolType, dstProtocolType int, clientWantsStream bool) ([]byte, http.Header, []byte, error) {
	return convertProxyResponse(resp, clientProtocolType, dstProtocolType, clientWantsStream)
}
