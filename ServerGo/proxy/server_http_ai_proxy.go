package proxy

import (
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"github.com/lishimeng/LsmTokensServer/recognizer"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// buildAIProxyMux 构造 AI 代理服务的路由表（HTTP 与 HTTPS 两个 server 共用同一份 mux/handler，
// 保证两套协议对外行为完全一致）。
func buildAIProxyMux(cfg *config.LsmTokensServerConfig) *http.ServeMux {
	mux := http.NewServeMux()
	anthropicPath := "/" + cfg.AgentAnthropicListenURL
	openaiPath := "/" + cfg.AgentOpenAIListenURL
	mux.HandleFunc(anthropicPath+"/", anthropicProxyHandler)
	mux.HandleFunc(openaiPath+"/", openAIProxyHandler)
	mux.HandleFunc(anthropicPath, anthropicProxyHandler)
	mux.HandleFunc(openaiPath, openAIProxyHandler)
	// 其他路径返回 404，同时处理 CORS 预检请求
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "Not Found - use "+anthropicPath+" or "+openaiPath, http.StatusNotFound)
	})
	return mux
}

// StartAIProxyService 启动 AI 代理服务。
//
// v2.0.31 起，HTTP 代理（agentListenPort）与 HTTPS 代理（agentHttpsListenPort）并存，
// 二者复用同一 buildAIProxyMux 构造的 handler 实现。HTTPS 复用 userWebCertFile /
// userWebKeyFile 证书；证书缺失或端口与 HTTP 冲突时优雅跳过（仅记日志，不影响 HTTP 代理）。
func StartAIProxyService(cfg *config.LsmTokensServerConfig) {
	aiProxyMutex.Lock()
	defer aiProxyMutex.Unlock()

	if aiProxyServer != nil {
		logger.Printf("[PROXY] AI proxy server already running")
		return
	}

	mux := buildAIProxyMux(cfg)

	// HTTP 代理服务（保留原有 agentListenPort）
	port := cfg.AgentListenPort
	aiProxyServer = &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	logger.Printf("[PROXY] AI proxy server listening on :%d (HTTP)", port)
	logger.Printf("[PROXY] =====================================================================")
	logger.Printf("[PROXY] AI 代理端口迁移提示：旧工程 LsmHttpAgent 监听 29000/29003；")
	logger.Printf("[PROXY] 本工程 LsmTokensServer 监听 %d(HTTP) / %d(HTTPS)。", port, cfg.AgentHttpsListenPort)
	logger.Printf("[PROXY] 如果客户端 Claude Code / Cursor / Cline 等仍配置旧端口，请改为 %d。", port)
	logger.Printf("[PROXY] =====================================================================")
	go func() {
		if err := aiProxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Printf("[PROXY] AI proxy server error: %v", err)
		}
	}()

	// HTTPS 代理服务（agentHttpsListenPort，复用同一 mux/handler）
	// config.validateAndFixConfig 已保证 AgentHttpsListenPort > 0，此处仅做冲突/证书兜底。
	if cfg.AgentHttpsListenPort <= 0 {
		logger.Printf("[PROXY] AI proxy HTTPS disabled (agentHttpsListenPort <= 0)")
		return
	}
	httpsPort := cfg.AgentHttpsListenPort
	if httpsPort == port {
		logger.Printf("[PROXY] AgentHttpsListenPort (%d) equals AgentListenPort, skip HTTPS proxy", httpsPort)
		return
	}

	// 证书路径解析（支持相对路径，与 UserWeb HTTPS 一致）
	certFile, err := config.ResolvePath(cfg.UserWebCertFile)
	if err != nil {
		logger.Printf("[PROXY] Failed to resolve cert file path: %v, using original", err)
		certFile = cfg.UserWebCertFile
	}
	keyFile, err := config.ResolvePath(cfg.UserWebKeyFile)
	if err != nil {
		logger.Printf("[PROXY] Failed to resolve key file path: %v, using original", err)
		keyFile = cfg.UserWebKeyFile
	}
	// v2.0.73+ 迁移修复：
	// ResolvePath 基于可执行文件目录（ServerGo/）解析，但证书通常在工程根目录（../server.crt）。
	// 首次查找失败时回退到可执行文件父目录（工程根目录）查找，保持与旧工程行为一致。
	if _, err := os.Stat(certFile); err != nil {
		parentCert := filepath.Join(filepath.Dir(os.Args[0]), "..", cfg.UserWebCertFile)
		if _, err2 := os.Stat(parentCert); err2 == nil {
			certFile = parentCert
		} else {
			logger.Printf("[PROXY] HTTPS proxy cert file not found (%s), skip HTTPS proxy on :%d: %v", certFile, httpsPort, err)
			return
		}
	}
	if _, err := os.Stat(keyFile); err != nil {
		parentKey := filepath.Join(filepath.Dir(os.Args[0]), "..", cfg.UserWebKeyFile)
		if _, err2 := os.Stat(parentKey); err2 == nil {
			keyFile = parentKey
		} else {
			logger.Printf("[PROXY] HTTPS proxy key file not found (%s), skip HTTPS proxy on :%d: %v", keyFile, httpsPort, err)
			return
		}
	}

	aiProxyTLSServer = &http.Server{
		Addr:         ":" + strconv.Itoa(httpsPort),
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	logger.Printf("[PROXY] AI proxy server listening on :%d (HTTPS), cert=%s, key=%s", httpsPort, certFile, keyFile)
	go func() {
		if err := aiProxyTLSServer.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			logger.Printf("[PROXY] AI proxy HTTPS server error: %v", err)
		}
	}()
}

// anthropicProxyHandler Anthropic 协议代理处理
func anthropicProxyHandler(w http.ResponseWriter, r *http.Request) {
	handleAIProxyRequest(w, r, protocol.AgentProtocolType_Anthropic)
}

// openAIProxyHandler OpenAI 协议代理处理
func openAIProxyHandler(w http.ResponseWriter, r *http.Request) {
	handleAIProxyRequest(w, r, protocol.AgentProtocolType_OpenAI)
}

// isUpstreamConnectError 判断错误消息是否是上游连接类错误
// （DNS / TCP 拒绝 / TLS / 超时 / 握手 / 连接重置等非 HTTP 响应错误）。
// 用于将这类错误以 502 + JSON 形式返回给客户端，便于 Claude Code 等 IDE 插件展示。
func isUpstreamConnectError(errMsg string) bool {
	keys := []string{
		"connection refused",
		"connection reset",
		"connection closed",
		"no such host",
		"i/o timeout",
		"TLS handshake",
		"handshake timeout",
		"network is unreachable",
		"broken pipe",
		"EOF",
		"EOF occurred in violation of protocol",
		"dial tcp",
		"dial:",
		"connectex",
		"DNS",
	}
	for _, k := range keys {
		if strings.Contains(errMsg, k) {
			return true
		}
	}
	return false
}

// jsonEscape 将字符串转义为合法 JSON 字符串字面量（最小实现，仅处理控制字符与双引号）。
// 仅用于错误消息嵌入 http.Error 的 JSON body；不含 \u 转义，仅保证基本安全。
func jsonEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				continue // 丢弃不可打印控制字符
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

type proxyForwardResult struct {
	Response                 *http.Response
	DstEndpoint              *modelsdb.TAgentDstEndPoint
	TargetURL                string
	ProxyRequestBody         []byte
	SrcRequestBody           []byte
	DstEndPointAlgorithmType int
	ProxyRequestHeadersText  string
	SrcRequestHeadersText    string
}

// handleAIProxyRequest 通用 AI 代理请求处理
func handleAIProxyRequest(w http.ResponseWriter, r *http.Request, protocolType int) {
	requestStartAt := time.Now()

	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	clientIP := getProxyClientIP(r)
	if proxyAuthLimiter.isLimited(clientIP, time.Now()) {
		writeProxyRateLimited(w)
		return
	}

	// 1. 提取 API Key，并在访问缓存/数据库前做格式校验，拦截明显无效的暴力猜测。
	apiKey, err := extractAPIKey(r)
	if err != nil {
		logger.Printf("[PROXY] Auth failed from %s: %v", clientIP, err)
		writeProxyAuthFailure(w, r, err.Error())
		return
	}
	if !isValidProxyAPIKeyFormat(apiKey) {
		logger.Printf("[PROXY] Invalid API Key format from %s", clientIP)
		writeProxyAuthFailure(w, r, "Invalid API Key")
		return
	}

	// 2. 通过 API Key 查询模型：代理热路径只走内存缓存，严禁失败时回退 DB。
	userModel, ok := modelsdb.GetCachedModelByAPIKey(apiKey)
	if !ok {
		logger.Printf("[PROXY] Model not found in cache for API Key from %s", clientIP)
		writeProxyAuthFailure(w, r, "Invalid API Key")
		return
	}
	proxyAuthLimiter.reset(clientIP)

	// 2.1 检查模型是否被禁用
	if userModel.Status == modelsdb.UserModelStatus_Disabled {
		logger.Printf("[PROXY] Model disabled for API Key from %s, model: %s", clientIP, userModel.ModelName)
		writeProxyAuthFailure(w, r, "Model disabled")
		return
	}

	// 3. 通过模型关联的用户 ID 查询用户信息（内存缓存优先）
	user, ok := modelsdb.GetCachedUserByID(userModel.UserID)
	if !ok {
		logger.Printf("[PROXY] User not found in cache for model userID=%d", userModel.UserID)
		http.Error(w, `{"error":"Internal Server Error","message":"User not found"}`, http.StatusInternalServerError)
		return
	}

	// 4. 校验协议是否启用
	if !isValidProtocol(user, protocolType) {
		logger.Printf("[PROXY] Protocol not enabled for user %s (type=%d)", user.UserName, protocolType)
		http.Error(w, `{"error":"Forbidden","message":"Protocol not enabled"}`, http.StatusForbidden)
		return
	}

	// 4. 提取相对 URL（去掉 /Anthropic 或 /OpenAI 前缀）
	relativePath := getRelativePath(r.URL.Path, protocolType)

	// 5. 读取请求 Body。读取 MaxRequestBodySize+1 字节，超限立即拒绝，避免静默截断。
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBodySize+1))
	if err != nil {
		logger.Printf("[PROXY] Failed to read request body: %v", err)
		http.Error(w, `{"error":"Bad Request","message":"Failed to read body"}`, http.StatusBadRequest)
		return
	}
	r.Body.Close()
	if len(bodyBytes) > MaxRequestBodySize {
		http.Error(w, `{"error":"Payload Too Large","message":"Request body too large"}`, http.StatusRequestEntityTooLarge)
		return
	}

	// 6. 解析模型名称
	reqModelName, err := parseModelFromBody(bodyBytes)
	if err != nil || reqModelName == "" {
		logger.Printf("[PROXY] Failed to parse model from body: %v", err)
		http.Error(w, `{"error":"Bad Request","message":"Model not specified in request body"}`, http.StatusBadRequest)
		return
	}

	// 7. 查询智能路由（按模型 ID + 协议类型）—— 代理热路径直接从缓存获取 modelsdb.CachedAIRoute
	cachedRoute, err := modelsdb.GetCachedAIRouteByUserModelIDAndProtocol(userModel.ID, protocolType)
	if err != nil {
		logger.Printf("[PROXY] AI route not found: userModelID=%d, protocol=%d, err=%v", userModel.ID, protocolType, err)
		http.Error(w, `{"error":"Bad Request","message":"No route configured for model '`+userModel.ModelName+`' protocol"}`, http.StatusBadRequest)
		return
	}

	// 8. 使用算法选择器进行转发（支持透明重试）
	forwardResult, err := forwardWithRetry(
		r, cachedRoute, protocolType, bodyBytes, reqModelName, relativePath,
	)
	if err != nil {
		// 所有重试都失败了
		logger.Printf("[PROXY] All endpoints failed for route id=%d: %v", cachedRoute.ID, err)
		errMsg := err.Error()
		// 检查是否所有源站都被禁用
		if strings.Contains(errMsg, "is disabled") {
			http.Error(w, `{"error":"Forbidden","message":"All endpoints are disabled"}`, http.StatusForbidden)
			return
		}
		// 上游连接类错误（DNS / TCP / TLS / timeout / reset）—— 返回 502 + JSON，
		// 客户端拿到的是可解析的错误而不是裸 connection error。
		if isUpstreamConnectError(errMsg) {
			clientIP := getProxyClientIP(r)
			logger.Printf("[PROXY][CONNECT_FAIL] client=%s route=%d model=%s upstream_connect_failed err=%q", clientIP, cachedRoute.ID, userModel.ModelName, errMsg)
			http.Error(w, `{"error":"Bad Gateway","message":"upstream connect failed: `+jsonEscape(errMsg)+`"}`, http.StatusBadGateway)
			return
		}
		http.Error(w, `{"error":"Service Unavailable","message":"All endpoints failed"}`, http.StatusServiceUnavailable)
		return
	}
	resp := forwardResult.Response
	dstEndpoint := forwardResult.DstEndpoint
	defer resp.Body.Close()

	responseStatus := resp.Status
	responseStatusCode := resp.StatusCode
	srcRespHeadersText := formatRedactedHeaders(resp.Header)
	clientRespHeaders := resp.Header.Clone()
	var respBodyStr string
	var srcRespBodyStr string
	var respContentLength int64
	var responseEndAt time.Time

	if forwardResult.DstEndPointAlgorithmType == modelsdb.DstEndPointAlgorithmType_ProtocolConverter {
		convertedBody, convertedHeaders, srcBody, err := convertProxyResponse(resp, protocolType, dstEndpoint.ProtocolType, requestBodyWantsStream(bodyBytes))
		responseEndAt = time.Now()
		if err != nil {
			logger.Printf("[PROXY] Response protocol conversion failed: %v", err)
			http.Error(w, `{"error":"Bad Gateway","message":"Protocol conversion failed"}`, http.StatusBadGateway)
			return
		}
		clientRespHeaders = convertedHeaders
		respBodyStr = string(convertedBody)
		srcRespBodyStr = string(srcBody)
		writeProxyResponseHeaders(w, clientRespHeaders, protocolType, int64(len(convertedBody)))
		w.WriteHeader(responseStatusCode)
		if _, err := w.Write(convertedBody); err != nil {
			logger.Printf("[PROXY] Error writing converted response body: %v", err)
		}
		respContentLength = int64(len(convertedBody))
	} else {
		writeProxyResponseHeaders(w, clientRespHeaders, protocolType, resp.ContentLength)
		w.WriteHeader(responseStatusCode)
		logWriter := newCappedLogWriter(10 * 1024 * 1024) // 10 MB
		respWriter := io.MultiWriter(w, logWriter)
		_, copyErr := io.Copy(respWriter, resp.Body)
		if copyErr != nil {
			logger.Printf("[PROXY] Error copying response body: %v", copyErr)
		}
		responseEndAt = time.Now()
		respBodyStr = logWriter.String()
		srcRespBodyStr = respBodyStr
		respContentLength = resp.ContentLength
		if respContentLength < 0 {
			respContentLength = logWriter.Len()
		}
	}

	elapsedMs := responseEndAt.Sub(requestStartAt).Milliseconds()
	respHeadersText := formatRedactedHeaders(clientRespHeaders)

	// 11. 解析响应体中的 Tokens 使用量
	tokensInputSize, tokensOutputSize, tokensAllSize := parseTokensFromResponseBody(respBodyStr, protocolType)
	if tokensInputSize == 0 && tokensOutputSize == 0 {
		tokensInputSize, tokensOutputSize, tokensAllSize = estimateTokensFromSize(uint64(len(forwardResult.ProxyRequestBody)), uint64(respContentLength))
	}

	// 11.5 识别 session_id（所有请求，不仅经济算法）
	sessionID := recognizer.RecognizeSessionID(bodyBytes, protocolType, r.Header)
	if sessionID == "" {
		// v2.0.20: 日志路径复用合成 session（与 forwardWithRetry 共享缓存）
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			ua = r.Header.Get("X-Client-Name")
		}
		agentName := recognizer.RecognizeAgentTool(ua).AgentToolName
		if modelsdb.IsSyntheticSessionEligibleAgent(agentName) {
			if user, ok := modelsdb.GetCachedUserByID(cachedRoute.UserID); ok {
				if synthID, synthOK := modelsdb.GetOrSynthesizeSessionID(user.UserName, reqModelName); synthOK {
					sessionID = synthID
				}
			}
		}
		if sessionID == "" {
			sessionID = "unknown_session_id"
		}
	}

	// 12. 记录日志
	go logAIProxyTransaction(
		user, userModel, &cachedRoute.TAgentHttpAIRoute,
		dstEndpoint.ID, forwardResult.DstEndPointAlgorithmType, dstEndpoint.ModelName,
		r, forwardResult.TargetURL,
		forwardResult.ProxyRequestBody, forwardResult.SrcRequestBody, r.ContentLength,
		forwardResult.ProxyRequestHeadersText, forwardResult.SrcRequestHeadersText,
		responseStatus, respHeadersText, srcRespHeadersText, respBodyStr, srcRespBodyStr,
		respContentLength,
		requestStartAt, requestStartAt, requestStartAt, responseEndAt,
		elapsedMs,
		protocolType,
		sessionID,
		tokensInputSize, tokensOutputSize, tokensAllSize,
	)

	logger.Printf("[PROXY] %s %s -> %s (model: %s -> %s, status: %s, elapsed: %dms, endpoints: %d, endpointAlgorithm: %d)",
		r.Method, relativePath, dstEndpoint.URLAddress,
		reqModelName, dstEndpoint.ModelName,
		responseStatus, elapsedMs, cachedRoute.DstEndPointIDNumber, forwardResult.DstEndPointAlgorithmType)
}
