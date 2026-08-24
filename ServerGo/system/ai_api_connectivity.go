package system

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// stripHTMLTags 去除字符串中的 HTML 标签
func stripHTMLTags(s string) string {
	return htmlTagRe.ReplaceAllString(s, "")
}

// cleanErrorBody 清理错误响应体：去除 HTML 标签、合并空白、截断过长内容
func cleanErrorBody(body string) string {
	s := stripHTMLTags(body)
	s = strings.TrimSpace(s)
	// 合并连续空白
	fields := strings.Fields(s)
	s = strings.Join(fields, " ")
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// TestResult 连通性测试结果
type TestResult struct {
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	RequestURL      string `json:"request_url"`
	RequestHeaders  string `json:"request_headers"`
	RequestBody     string `json:"request_body"`
	ResponseHeaders string `json:"response_headers"`
	ResponseBody    string `json:"response_body"`
	StatusCode      int    `json:"status_code"`
	ElapsedMs       int64  `json:"elapsed_ms"`
}

// formatHeadersForDisplay 将 http.Header 序列化为多行 "Key: Value" 文本用于前端排查展示。
// 敏感头（Authorization / x-api-key / api-key / x-goog-api-key / proxy-authorization）做掩码，
// 与项目"禁止把完整 Bearer Token 传到前端"的约定一致（CLAUDE.md §2.5）。
func formatHeadersForDisplay(header http.Header) string {
	if len(header) == 0 {
		return ""
	}
	// 稳定输出顺序，便于测试与阅读
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		for _, v := range header[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(maskSensitiveHeaderValue(k, v))
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// maskSensitiveHeaderValue 对敏感请求头的值做掩码，非敏感头原样返回。
func maskSensitiveHeaderValue(key, value string) string {
	switch strings.ToLower(key) {
	case "authorization", "proxy-authorization":
		// 保留 "Bearer " 前缀（如有），仅掩码 token 部分
		if idx := strings.IndexByte(value, ' '); idx > 0 {
			return value[:idx+1] + "************************"
		}
		return "************************"
	case "x-api-key", "api-key", "x-goog-api-key":
		return "************************"
	default:
		return value
	}
}

// normalizeAPIKey 归一化用户填写的 API Key：
// 兼容用户从请求头整行粘贴的情况，剥离 "Authorization:" / "x-api-key:" / "api-key:"
// 头名前缀，以及 "Bearer " / "bearer " scheme 前缀，返回纯 token。
// Anthropic（x-api-key）与 OpenAI（Authorization: Bearer）两个协议共用，避免真实发出的
// 头里出现 "Authorization: Bearer Authorization: Bearer sk-xxx" 这类叠加导致源站 401。
//
// v2.0.24：额外剥离用户从「请求头」排查展示区复制回来的**掩码值**。
// 前端失败弹窗里 Authorization / x-api-key 的值被 maskSensitiveHeaderValue 打成
// 一串星号（modelsdb.authorizationBearerAPIKeyMask，24 个 '*'）。用户直接把这串「Authorization:
// Bearer ************************」粘回 API Key 输入框再保存时，旧实现会把纯星号当作
// 合法 token 发给源站 → 稳定 401。这里把「剥掉前缀后全是 '*' 的残留」判定为空 token，
// 交由上层 API Key 为空校验拦截，给用户明确的「API Key 为空」提示，而不是拿星号去打源站。
func normalizeAPIKey(raw string) string {
	s := strings.TrimSpace(raw)
	// 1. 剥离 header 名前缀（大小写不敏感）：authorization: / x-api-key: / api-key:
	for _, prefix := range []string{"authorization:", "x-api-key:", "api-key:"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	// 2. 剥离 Bearer scheme 前缀（大小写不敏感）
	if len(s) >= 7 && strings.EqualFold(s[:7], "bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	// 3. 掩码残留判定：剥掉前缀后若剩下的是纯星号（前端脱敏展示值），视为未填写
	if isMaskedAPIKeyValue(s) {
		return ""
	}
	return s
}

// isMaskedAPIKeyValue 判断给定字符串是否为脱敏掩码值（长度 >0 且全部由 '*' 组成）。
// 用于识别用户从前端排查展示区复制回来的星号占位串，避免其被当作真实 token 发出。
func isMaskedAPIKeyValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '*' {
			return false
		}
	}
	return true
}

// resolveAuthHeaders 按协议类型 + AuthType 解析认证头的 (name, value)。
//
//	protocolType: protocol.AgentProtocolType_Anthropic / protocol.AgentProtocolType_OpenAI
//	authType:     0=协议默认（Anthropic→x-api-key, OpenAI→Authorization Bearer），
//	              1=强制 x-api-key，2=强制 Authorization Bearer
//	apiKey:       纯 token（已 normalizeAPIKey）
//
// 边界：未知 authType 退回协议默认；未知 protocolType 默认 Authorization Bearer。
func resolveAuthHeaders(protocolType, authType int, apiKey string) (string, string) {
	switch authType {
	case 1:
		// 强制 x-api-key
		return "x-api-key", apiKey
	case 2:
		// 强制 Authorization Bearer
		return "Authorization", "Bearer " + apiKey
	default:
		// 0 / 未知：协议默认
		if protocolType == protocol.AgentProtocolType_Anthropic {
			return "x-api-key", apiKey
		}
		return "Authorization", "Bearer " + apiKey
	}
}

// TestDstEndPointConnectivity 测试源站接入点的 API 连通性
// 在添加或更新源站前调用，验证配置是否正确
func TestDstEndPointConnectivity(endpoint *modelsdb.TAgentDstEndPoint) error {
	if endpoint == nil {
		return fmt.Errorf("endpoint is nil")
	}

	urlAddr := strings.TrimSpace(endpoint.URLAddress)
	apiKey := normalizeAPIKey(endpoint.APIKey)
	modelName := strings.TrimSpace(endpoint.ModelName)

	if urlAddr == "" {
		return fmt.Errorf("URL 地址为空")
	}
	if apiKey == "" {
		return fmt.Errorf("API Key 为空")
	}
	if modelName == "" {
		return fmt.Errorf("模型名称为空")
	}

	switch endpoint.ProtocolType {
	case protocol.AgentProtocolType_Anthropic:
		_, err := testAnthropicEndpoint(urlAddr, apiKey, modelName, endpoint.AuthType)
		return err
	case protocol.AgentProtocolType_OpenAI:
		_, err := testOpenAIEndpoint(urlAddr, apiKey, modelName, endpoint.AuthType)
		return err
	default:
		return fmt.Errorf("未知的协议类型: %d", endpoint.ProtocolType)
	}
}

// TestDstEndPointConnectivityWithResult 测试源站接入点的 API 连通性并返回详细结果
// 测试完成后自动将请求/响应记录保存到 modelsdb.TAgentHttpTransactionDataItem 分表
// 源信息缺失时，自动使用目的信息填充（userName=modelName, apiKey=dstAPIKey）
func TestDstEndPointConnectivityWithResult(endpoint *modelsdb.TAgentDstEndPoint, userName string, userID uint64) *TestResult {
	if endpoint == nil {
		return &TestResult{Success: false, Message: "endpoint is nil"}
	}

	urlAddr := strings.TrimSpace(endpoint.URLAddress)
	apiKey := normalizeAPIKey(endpoint.APIKey)
	modelName := strings.TrimSpace(endpoint.ModelName)

	if urlAddr == "" {
		return &TestResult{Success: false, Message: "URL 地址为空"}
	}
	if apiKey == "" {
		return &TestResult{Success: false, Message: "API Key 为空"}
	}
	if modelName == "" {
		return &TestResult{Success: false, Message: "模型名称为空"}
	}

	start := time.Now()
	var result *TestResult
	var testErr error

	switch endpoint.ProtocolType {
	case protocol.AgentProtocolType_Anthropic:
		result, testErr = testAnthropicEndpoint(urlAddr, apiKey, modelName, endpoint.AuthType)
	case protocol.AgentProtocolType_OpenAI:
		result, testErr = testOpenAIEndpoint(urlAddr, apiKey, modelName, endpoint.AuthType)
	default:
		return &TestResult{Success: false, Message: fmt.Sprintf("未知的协议类型: %d", endpoint.ProtocolType)}
	}

	// 防御：某些早期失败路径（构造请求体/创建请求失败）返回 nil result，避免解引用 panic
	if result == nil {
		result = &TestResult{}
	}

	result.ElapsedMs = time.Since(start).Milliseconds()
	if testErr != nil {
		result.Success = false
		result.Message = testErr.Error()
	}

	// 异步保存测试记录到分表（源信息缺失时，使用目的信息填充）
	go saveTestRecordToSubTable(endpoint, result, userName, userID)

	return result
}

// saveTestRecordToSubTable 将连通性测试记录保存到哈希分表
// 规则：源信息缺失时，自动使用目的信息填充（userName=modelName, apiKey=dstAPIKey, modelName=dstModelName）
func saveTestRecordToSubTable(endpoint *modelsdb.TAgentDstEndPoint, result *TestResult, userName string, userID uint64) {
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("[WARNING] saveTestRecordToSubTable panic: %v", r)
		}
	}()

	if config.G == nil || config.G.DBMysqlSubTableNumber <= 0 {
		return
	}

	// 源信息缺失时，使用目的信息填充
	srcUserName := userName
	if srcUserName == "" {
		srcUserName = endpoint.ModelName
	}
	srcModelName := endpoint.ModelName
	srcAPIKey := endpoint.APIKey
	srcUserID := int64(userID)
	if srcUserID == 0 {
		srcUserID = int64(endpoint.UserID)
	}

	now := time.Now()
	statusStr := fmt.Sprintf("%d", result.StatusCode)
	if result.StatusCode == 0 {
		statusStr = "0"
	}

	// 构造请求头文本（与 testAnthropicEndpoint / testOpenAIEndpoint 实际发出的规范化头名一致：
	// Go http.Header.Set 会把 x-api-key / anthropic-version 规范化为 X-Api-Key / Anthropic-Version；
	// 认证头按 resolveAuthHeaders(protocolType, endpoint.AuthType) 拼写，与实际请求同步）
	pureKey := normalizeAPIKey(endpoint.APIKey)
	authName, authValue := resolveAuthHeaders(endpoint.ProtocolType, endpoint.AuthType, pureKey)
	var reqHeaders string
	if endpoint.ProtocolType == protocol.AgentProtocolType_Anthropic {
		reqHeaders = fmt.Sprintf("Anthropic-Version: 2023-06-01\nContent-Type: application/json\n%s: %s", authName, authValue)
	} else {
		reqHeaders = fmt.Sprintf("Content-Type: application/json\n%s: %s", authName, authValue)
	}

	// 构造响应头文本
	var respHeaders string
	if result.Success {
		respHeaders = "Content-Type: application/json"
	} else {
		respHeaders = fmt.Sprintf("Content-Type: application/json\nX-Test-Error: %s", result.Message)
	}

	err := modelsdb.SaveAgentHttpTransaction(
		srcUserName,
		srcModelName,
		srcUserID,
		srcAPIKey,
		endpoint.ID,
		modelsdb.DstEndPointAlgorithmType_Direct,
		endpoint.ModelName,
		endpoint.ProtocolType,
		http.MethodPost,
		result.RequestURL,
		"127.0.0.1",
		int64(len(result.RequestBody)),
		reqHeaders,
		reqHeaders,
		result.RequestBody,
		"", // srcBody: 连通性测试无原始请求体差异
		statusStr,
		int64(len(result.ResponseBody)),
		respHeaders,
		respHeaders,
		result.ResponseBody,
		"", // srcRespBody: 连通性测试无原始响应体差异
		now, now, now, now,
		result.ElapsedMs,
		"LsmTokensServer-EndpointTest",
		"",                   // agentToolName: 连通性测试使用空值
		"",                   // agentToolInfo: 连通性测试使用空值
		"unknown_session_id", // 连通性测试无客户端 body，使用占位值
		config.G.DBMysqlSubTableNumber,
		0, 0, 0,
	)
	if err != nil {
		logger.Printf("[WARNING] Failed to save test record to sub table: %v", err)
	}
}

// testAnthropicEndpoint 测试 Anthropic 协议端点，返回测试结果
func testAnthropicEndpoint(urlAddr, apiKey, modelName string, authType int) (*TestResult, error) {
	// 构造 Anthropic /v1/messages 请求体
	reqBody := map[string]interface{}{
		"model":      modelName,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello，请用中文回答你什么模型？都支持什么功能？200字以内？"},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("构造请求体失败: %w", err)
	}

	// 构造测试 URL：支持用户填写完整 URL 或基础地址
	testURL := strings.TrimSpace(urlAddr)
	testURL = strings.TrimSuffix(testURL, "/")
	if strings.HasSuffix(testURL, "/v1/messages") {
		// 用户已填写完整 URL，直接使用
	} else if strings.HasSuffix(testURL, "/v1") || strings.HasSuffix(testURL, "/v2") || strings.HasSuffix(testURL, "/v3") ||
		strings.HasSuffix(testURL, "/v4") || strings.HasSuffix(testURL, "/v5") || strings.HasSuffix(testURL, "/v6") {
		testURL = testURL + "/messages"
	} else {
		testURL = testURL + "/v1/messages"
	}

	req, err := http.NewRequest(http.MethodPost, testURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败 (URL: %s): %w", testURL, err)
	}

	req.Header.Set("Content-Type", "application/json")
	// 认证头：authType=0 用协议默认（x-api-key）；=2 强制 Authorization Bearer（兼容 LongCat
	// 等 URL 路径 Anthropic 但只认 Bearer 的代理站）
	authName, authValue := resolveAuthHeaders(protocol.AgentProtocolType_Anthropic, authType, apiKey)
	req.Header.Set(authName, authValue)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.ContentLength = int64(len(bodyBytes))

	// 采集请求头（API Key 掩码）用于失败排查展示
	reqHeadersText := formatHeadersForDisplay(req.Header)

	// 使用超时客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return &TestResult{
			Success:        false,
			RequestURL:     testURL,
			RequestHeaders: reqHeadersText,
			RequestBody:    string(bodyBytes),
		}, fmt.Errorf("请求发送失败 (URL: %s): %w", testURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	result := &TestResult{
		Success:         true,
		RequestURL:      testURL,
		RequestHeaders:  reqHeadersText,
		RequestBody:     string(bodyBytes),
		ResponseHeaders: formatHeadersForDisplay(resp.Header),
		StatusCode:      resp.StatusCode,
	}

	// Anthropic 成功状态码为 200，也可能是其他非错误状态码
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.ResponseBody = string(respBody)
		result.Message = "测试成功"
		return result, nil
	}

	result.ResponseBody = string(respBody)

	// 尝试解析错误响应中的详细信息
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Type string `json:"type"`
	}
	if json.Unmarshal(respBody, &errResp) == nil && (errResp.Error.Message != "" || errResp.Type != "") {
		msg := errResp.Error.Message
		if msg == "" {
			msg = errResp.Type
		}
		return result, fmt.Errorf("请求 URL: %s | API 返回错误 (HTTP %d): %s", testURL, resp.StatusCode, msg)
	}

	cleanBody := cleanErrorBody(string(respBody))
	return result, fmt.Errorf("请求 URL: %s | API 返回错误 (HTTP %d): %s", testURL, resp.StatusCode, cleanBody)
}

// testOpenAIEndpoint 测试 OpenAI 协议端点，返回测试结果
func testOpenAIEndpoint(urlAddr, apiKey, modelName string, authType int) (*TestResult, error) {
	// 构造 OpenAI /v1/chat/completions 请求体
	reqBody := map[string]interface{}{
		"model":      modelName,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello，请用中文回答你什么模型？都支持什么功能？200字以内？"},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("构造请求体失败: %w", err)
	}

	// 构造测试 URL：OpenAI 协议由用户自行管理版本路径，仅追加 /chat/completions
	testURL := strings.TrimSpace(urlAddr)
	testURL = strings.TrimSuffix(testURL, "/")
	if strings.HasSuffix(testURL, "/chat/completions") {
		// 用户已填写完整 URL，直接使用
	} else {
		testURL = testURL + "/chat/completions"
	}

	req, err := http.NewRequest(http.MethodPost, testURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败 (URL: %s): %w", testURL, err)
	}

	req.Header.Set("Content-Type", "application/json")
	// 认证头：OpenAI 默认 Authorization Bearer；authType=1 强制 x-api-key（极少见，留个口子）
	authName, authValue := resolveAuthHeaders(protocol.AgentProtocolType_OpenAI, authType, apiKey)
	req.Header.Set(authName, authValue)
	req.ContentLength = int64(len(bodyBytes))

	// 采集请求头（API Key 掩码）用于失败排查展示
	reqHeadersText := formatHeadersForDisplay(req.Header)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return &TestResult{
			Success:        false,
			RequestURL:     testURL,
			RequestHeaders: reqHeadersText,
			RequestBody:    string(bodyBytes),
		}, fmt.Errorf("请求发送失败 (URL: %s): %w", testURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	result := &TestResult{
		Success:         true,
		RequestURL:      testURL,
		RequestHeaders:  reqHeadersText,
		RequestBody:     string(bodyBytes),
		ResponseHeaders: formatHeadersForDisplay(resp.Header),
		StatusCode:      resp.StatusCode,
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.ResponseBody = string(respBody)
		result.Message = "测试成功"
		return result, nil
	}

	result.ResponseBody = string(respBody)

	// 尝试解析 OpenAI 格式的错误响应
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
		return result, fmt.Errorf("请求 URL: %s | API 返回错误 (HTTP %d): %s", testURL, resp.StatusCode, errResp.Error.Message)
	}

	cleanBody := cleanErrorBody(string(respBody))
	return result, fmt.Errorf("请求 URL: %s | API 返回错误 (HTTP %d): %s", testURL, resp.StatusCode, cleanBody)
}
