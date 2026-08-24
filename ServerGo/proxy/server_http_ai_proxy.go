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
	"strconv"
	"strings"
	"time"
)

// buildAIProxyMux 构造 AI 代理服务的路由表（HTTP 与 HTTPS 两个 server 共用同一份 mux/handler，
// 保证两套协议对外行为完全一致）。
func buildAIProxyMux(cfg *config.LsmTokensServerConfig) *http.ServeMux {
	mux := http.NewServeMux()
	anthropicPath := "/" + config.G.AgentAnthropicListenURL
	openaiPath := "/" + config.G.AgentOpenAIListenURL
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

	mux := buildAIProxyMux(config.G)

	// HTTP 代理服务（保留原有 agentListenPort）
	port := config.G.AgentListenPort
	aiProxyServer = &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	logger.Printf("[PROXY] AI proxy server listening on :%d (HTTP)", port)
	go func() {
		if err := aiProxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Printf("[PROXY] AI proxy server error: %v", err)
		}
	}()

	// HTTPS 代理服务（agentHttpsListenPort，复用同一 mux/handler）
	// config.validateAndFixConfig 已保证 AgentHttpsListenPort > 0，此处仅做冲突/证书兜底。
	if config.G.AgentHttpsListenPort <= 0 {
		logger.Printf("[PROXY] AI proxy HTTPS disabled (agentHttpsListenPort <= 0)")
		return
	}
	httpsPort := config.G.AgentHttpsListenPort
	if httpsPort == port {
		logger.Printf("[PROXY] AgentHttpsListenPort (%d) equals AgentListenPort, skip HTTPS proxy", httpsPort)
		return
	}

	// 证书路径解析（支持相对路径，与 UserWeb HTTPS 一致）
	certFile, err := config.ResolvePath(config.G.UserWebCertFile)
	if err != nil {
		logger.Printf("[PROXY] Failed to resolve cert file path: %v, using original", err)
		certFile = config.G.UserWebCertFile
	}
	keyFile, err := config.ResolvePath(config.G.UserWebKeyFile)
	if err != nil {
		logger.Printf("[PROXY] Failed to resolve key file path: %v, using original", err)
		keyFile = config.G.UserWebKeyFile
	}
	if _, err := os.Stat(certFile); err != nil {
		logger.Printf("[PROXY] HTTPS proxy cert file not found (%s), skip HTTPS proxy on :%d: %v", certFile, httpsPort, err)
		return
	}
	if _, err := os.Stat(keyFile); err != nil {
		logger.Printf("[PROXY] HTTPS proxy key file not found (%s), skip HTTPS proxy on :%d: %v", keyFile, httpsPort, err)
		return
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
		// 检查是否所有源站都被禁用
		if strings.Contains(err.Error(), "is disabled") {
			http.Error(w, `{"error":"Forbidden","message":"All endpoints are disabled"}`, http.StatusForbidden)
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
