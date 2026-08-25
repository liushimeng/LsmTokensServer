package proxy

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

// initTestEnv 初始化测试环境（SQLite 内存数据库 + 日志 + 配置）
func initTestEnv(t *testing.T) func() {
	oldSkipBackfill, hadSkipBackfill := os.LookupEnv("LSM_SKIP_TASK_FEATURE_BACKFILL")
	os.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", "1")

	// 关闭用户信息日志写入：测试期间所有 fixture 操作不再污染 LsmTokensServerUsersInfo.log
	oldDisabled := logger.IsUserLogDisabled()
	logger.SetDisableUserLog(true)

	// 初始化配置
	config.G = config.DefaultConfig()
	config.G.DBMysqlSubTableNumber = 4 // 测试用较小分表数

	// 初始化日志到 stdout
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[TEST] ")

	// 创建内存 SQLite 数据库
	sqliteDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	database.DB = sqliteDB

	// 初始化所有表
	if err := modelsdb.InitAgentHttpUserInfoTable(); err != nil {
		t.Fatalf("failed to init user info table: %v", err)
	}
	if err := modelsdb.InitAgentHttpUserModelInfoTable(); err != nil {
		t.Fatalf("failed to init user model table: %v", err)
	}
	if err := modelsdb.InitAgentDstEndPointTable(); err != nil {
		t.Fatalf("failed to init dst endpoint table: %v", err)
	}
	if err := modelsdb.InitAgentHttpAIRouteTable(); err != nil {
		t.Fatalf("failed to init ai route table: %v", err)
	}
	if err := modelsdb.InitAgentHttpSubTables(config.G.DBMysqlSubTableNumber); err != nil {
		t.Fatalf("failed to init sub tables: %v", err)
	}
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("failed to load cache: %v", err)
	}

	// 返回清理函数
	return func() {
		sqlDB, _ := database.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = nil
		config.G = nil
		// 恢复 user log 开关（避免影响其他测试套件）
		logger.SetDisableUserLog(oldDisabled)
		if hadSkipBackfill {
			os.Setenv("LSM_SKIP_TASK_FEATURE_BACKFILL", oldSkipBackfill)
		} else {
			os.Unsetenv("LSM_SKIP_TASK_FEATURE_BACKFILL")
		}
	}
}

// TestEndToEndProxyForwarding 测试完整代理转发链路
func TestEndToEndProxyForwarding(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 创建 mock 目标服务器
	var receivedAuth string
	var receivedBody string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"mock-resp","model":"claude-3-opus","content":"hello from mock"}`))
	}))
	defer mockServer.Close()

	// 1. 创建测试用户
	testUser := &modelsdb.TAgentHttpUserInfo{
		UserName:         "testuser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(testUser); err != nil {
		t.Fatalf("add user failed: %v", err)
	}

	// 2. 创建用户模型（API Key 自动生成）
	testModel := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    testUser.ID,
		ModelName: "claude-3-5-sonnet-test",
	}
	if err := modelsdb.AddUserModel(testModel); err != nil {
		t.Fatalf("add user model failed: %v", err)
	}
	if testModel.APIKey == "" {
		t.Fatal("model api key should be auto-generated")
	}

	// 3. 创建目标源站（指向 mock 服务器）
	testEndpoint := &modelsdb.TAgentDstEndPoint{
		UserID:       testUser.ID,
		PlatformName: "MockPlatform",
		ModelName:    "claude-3-opus",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   mockServer.URL,
		APIKey:       "sk-dst-apikey-987654321",
	}
	if err := modelsdb.AddDstEndPoint(testEndpoint); err != nil {
		t.Fatalf("add dst endpoint failed: %v", err)
	}

	// 4. 创建智能路由
	testRoute := &modelsdb.TAgentHttpAIRoute{
		UserID:                testUser.ID,
		UserModelID:           testModel.ID,
		ProtocolType:          protocol.AgentProtocolType_Anthropic,
		DstEndPointIDList:     strconv.FormatUint(testEndpoint.ID, 10),
		DstEndPointIDNumber:   1,
		AlgorithmStrategyType: modelsdb.AlgorithmStrategyType_FirstID,
	}
	if err := modelsdb.AddAIRoute(testRoute); err != nil {
		t.Fatalf("add ai route failed: %v", err)
	}

	// 加载缓存
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}

	// 5. 构造代理请求（使用模型的 API Key）
	reqBody := `{"model":"claude-3-5-sonnet-test","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/Anthropic/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+testModel.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TestAgent/1.0")

	rec := httptest.NewRecorder()

	// 6. 调用代理处理器
	handleAIProxyRequest(rec, req, protocol.AgentProtocolType_Anthropic)

	// 7. 验证响应状态
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 8. 验证 mock 服务器收到的请求
	if !strings.Contains(receivedAuth, "sk-dst-apikey-987654321") {
		t.Errorf("dst endpoint api key not injected correctly, got: %s", receivedAuth)
	}
	if !strings.Contains(receivedBody, "claude-3-opus") {
		t.Errorf("model name not replaced in request body, got: %s", receivedBody)
	}
	if strings.Contains(receivedBody, "claude-3-5-sonnet-test") {
		t.Errorf("original model name should be replaced, but found in: %s", receivedBody)
	}

	// 9. 验证响应体透传
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "hello from mock") {
		t.Errorf("response body not forwarded correctly, got: %s", respBody)
	}

	// 10. 验证日志是否写入数据库（异步，短轮询避免固定等待）
	var records []modelsdb.TAgentHttpTransactionDataItem
	var total int64
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		records, total, err = modelsdb.QueryAgentHttpTransactions("testuser", testModel.ModelName, config.G.DBMysqlSubTableNumber, 1, 10, "", "", "", false, 0, "", "", "", 3, 0, 0)
		if err != nil || total == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Errorf("query transaction log failed: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 transaction log, got %d", total)
	}
	if len(records) > 0 {
		r := records[0]
		if r.UserName != "testuser" {
			t.Errorf("expected user_name 'testuser', got '%s'", r.UserName)
		}
		if r.ModelName != testModel.ModelName {
			t.Errorf("expected model_name '%s', got '%s'", testModel.ModelName, r.ModelName)
		}
		if r.ToolIdentifier != "TestAgent/1.0" {
			t.Errorf("expected tool_identifier 'TestAgent/1.0', got '%s'", r.ToolIdentifier)
		}
		if r.RequestMethod != "POST" {
			t.Errorf("expected method POST, got '%s'", r.RequestMethod)
		}
	}
}

func TestProtocolConverterOpenAIToAnthropicRewritesPathAndPreservesError(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	var receivedPath string
	var receivedAuth string
	var receivedBody string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"resource_not_found_error","message":"The requested resource was not found"}}`))
	}))
	defer mockServer.Close()

	testUser := &modelsdb.TAgentHttpUserInfo{
		UserName:      "converteruser",
		OpenAIEnabled: true,
	}
	if err := modelsdb.AddUser(testUser); err != nil {
		t.Fatalf("add user failed: %v", err)
	}

	testModel := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    testUser.ID,
		ModelName: "liusm191-ai-model",
		APIKey:    "sk-test-openai-model-key-1234567890",
	}
	if err := modelsdb.AddUserModel(testModel); err != nil {
		t.Fatalf("add user model failed: %v", err)
	}

	testEndpoint := &modelsdb.TAgentDstEndPoint{
		UserID:       testUser.ID,
		PlatformName: "Kimi-CodingPlan",
		ModelName:    "kimi-for-coding",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   mockServer.URL + "/coding",
		APIKey:       "sk-dst-kimi-secret",
		Status:       1,
	}
	if err := modelsdb.AddDstEndPoint(testEndpoint); err != nil {
		t.Fatalf("add dst endpoint failed: %v", err)
	}

	testRoute := &modelsdb.TAgentHttpAIRoute{
		UserID:                       testUser.ID,
		UserModelID:                  testModel.ID,
		ProtocolType:                 protocol.AgentProtocolType_OpenAI,
		DstEndPointIDList:            strconv.FormatUint(testEndpoint.ID, 10),
		DstEndPointAlgorithmTypeList: strconv.Itoa(modelsdb.DstEndPointAlgorithmType_ProtocolConverter),
		DstEndPointIDNumber:          1,
		AlgorithmStrategyType:        modelsdb.AlgorithmStrategyType_FirstID,
	}
	if err := modelsdb.AddAIRoute(testRoute); err != nil {
		t.Fatalf("add ai route failed: %v", err)
	}
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}

	reqBody := `{"model":"liusm191-ai-model","messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"hi"}],"stream":true,"max_completion_tokens":123}`
	req := httptest.NewRequest("POST", "/OpenAI/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+testModel.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/JS 6.26.0")
	rec := httptest.NewRecorder()

	handleAIProxyRequest(rec, req, protocol.AgentProtocolType_OpenAI)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d, body: %s", rec.Code, rec.Body.String())
	}
	if receivedPath != "/coding/v1/messages" {
		t.Fatalf("mock server path = %q, want /coding/v1/messages", receivedPath)
	}
	if receivedAuth != "Bearer sk-dst-kimi-secret" {
		t.Fatalf("destination authorization not injected, got %q", receivedAuth)
	}
	if !strings.Contains(receivedBody, `"model":"kimi-for-coding"`) {
		t.Fatalf("converted request body should contain destination model, got: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, `"max_tokens":123`) {
		t.Fatalf("converted request body should map max_completion_tokens to max_tokens, got: %s", receivedBody)
	}
	if strings.Contains(receivedBody, "chat/completions") {
		t.Fatalf("converted request body should not contain OpenAI path, got: %s", receivedBody)
	}
	if !strings.Contains(rec.Body.String(), "The requested resource was not found") {
		t.Fatalf("client response should preserve upstream error message, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "chat.completion") || strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("client error response should not become success completion or SSE, got: %s", rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	var records []modelsdb.TAgentHttpTransactionDataItem
	var total int64
	var err error
	for {
		records, total, err = modelsdb.QueryAgentHttpTransactions("converteruser", testModel.ModelName, config.G.DBMysqlSubTableNumber, 1, 10, "", "", "", false, 0, "", "", "", 3, 0, 0)
		if err != nil || total == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("query transaction log failed: %v", err)
	}
	if total != 1 || len(records) == 0 {
		t.Fatalf("expected 1 transaction log, got total=%d len=%d", total, len(records))
	}
	if records[0].DstEndPointAlgorithmType != modelsdb.DstEndPointAlgorithmType_ProtocolConverter {
		t.Fatalf("dst endpoint algorithm type = %d", records[0].DstEndPointAlgorithmType)
	}
	if !strings.Contains(records[0].RequestURL, "/coding/v1/messages") || strings.Contains(records[0].RequestURL, "/chat/completions") {
		t.Fatalf("logged request_url should use converted target path, got: %s", records[0].RequestURL)
	}
}

func TestProtocolConverterStreamRequestWrapsJSONResponseAsSSE(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_stream_wrap",
			"type":"message",
			"role":"assistant",
			"model":"kimi-for-coding",
			"content":[{"type":"text","text":"wrapped hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":7,"output_tokens":3}
		}`))
	}))
	defer mockServer.Close()

	testUser := &modelsdb.TAgentHttpUserInfo{UserName: "streamconverter", OpenAIEnabled: true}
	if err := modelsdb.AddUser(testUser); err != nil {
		t.Fatalf("add user failed: %v", err)
	}
	testModel := &modelsdb.TAgentHttpUserModelInfo{UserID: testUser.ID, ModelName: "stream-openai-model", APIKey: "sk-stream-openai-model-key-123456"}
	if err := modelsdb.AddUserModel(testModel); err != nil {
		t.Fatalf("add user model failed: %v", err)
	}
	testEndpoint := &modelsdb.TAgentDstEndPoint{
		UserID:       testUser.ID,
		PlatformName: "Kimi-CodingPlan",
		ModelName:    "kimi-for-coding",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   mockServer.URL + "/coding",
		APIKey:       "sk-dst-kimi-stream-secret",
		Status:       1,
	}
	if err := modelsdb.AddDstEndPoint(testEndpoint); err != nil {
		t.Fatalf("add dst endpoint failed: %v", err)
	}
	testRoute := &modelsdb.TAgentHttpAIRoute{
		UserID:                       testUser.ID,
		UserModelID:                  testModel.ID,
		ProtocolType:                 protocol.AgentProtocolType_OpenAI,
		DstEndPointIDList:            strconv.FormatUint(testEndpoint.ID, 10),
		DstEndPointAlgorithmTypeList: strconv.Itoa(modelsdb.DstEndPointAlgorithmType_ProtocolConverter),
		DstEndPointIDNumber:          1,
		AlgorithmStrategyType:        modelsdb.AlgorithmStrategyType_FirstID,
	}
	if err := modelsdb.AddAIRoute(testRoute); err != nil {
		t.Fatalf("add ai route failed: %v", err)
	}
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}

	reqBody := `{"model":"stream-openai-model","messages":[{"role":"user","content":"hi"}],"stream":true,"max_completion_tokens":123}`
	req := httptest.NewRequest("POST", "/OpenAI/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+testModel.APIKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handleAIProxyRequest(rec, req, protocol.AgentProtocolType_OpenAI)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data:") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream request should receive OpenAI SSE, got: %s", body)
	}
	if !strings.Contains(body, "chat.completion.chunk") || !strings.Contains(body, "wrapped hello") {
		t.Fatalf("SSE body missing converted content, got: %s", body)
	}

	deadline := time.Now().Add(2 * time.Second)
	var records []modelsdb.TAgentHttpTransactionDataItem
	var total int64
	var err error
	for {
		records, total, err = modelsdb.QueryAgentHttpTransactions("streamconverter", testModel.ModelName, config.G.DBMysqlSubTableNumber, 1, 10, "", "", "", false, 0, "", "", "", 3, 0, 0)
		if err != nil || total == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("query transaction log failed: %v", err)
	}
	if total != 1 || len(records) == 0 {
		t.Fatalf("expected 1 transaction log, got total=%d len=%d", total, len(records))
	}
	if records[0].ResponseContentLength == 0 {
		t.Fatalf("transaction should record streamed response content length")
	}
}

// TestProxyAuthFailures 测试代理认证失败场景
func TestProxyAuthFailures(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 预创建一个用户和模型供测试使用
	testUser := &modelsdb.TAgentHttpUserInfo{
		UserName:         "testuser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(testUser); err != nil {
		t.Fatalf("add user failed: %v", err)
	}
	testModel := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    testUser.ID,
		ModelName: "test-model",
	}
	if err := modelsdb.AddUserModel(testModel); err != nil {
		t.Fatalf("add user model failed: %v", err)
	}

	// 加载缓存
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}

	tests := []struct {
		name       string
		auth       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "missing auth header",
			auth:       "",
			body:       `{"model":"test","messages":[]}`,
			wantStatus: http.StatusUnauthorized,
			wantBody:   "missing Authorization header",
		},
		{
			name:       "invalid api key",
			auth:       "Bearer invalid-key-not-in-db",
			body:       `{"model":"test","messages":[]}`,
			wantStatus: http.StatusUnauthorized,
			wantBody:   "Invalid API Key",
		},
		{
			name:       "missing model in body",
			auth:       "Bearer " + testModel.APIKey,
			body:       `{"messages":[]}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   "Model not specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/Anthropic/v1/messages", strings.NewReader(tt.body))
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handleAIProxyRequest(rec, req, protocol.AgentProtocolType_Anthropic)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body does not contain %q, got: %s", tt.wantBody, rec.Body.String())
			}
		})
	}
}

// TestProxyProtocolForbidden 测试协议未启用时返回 403
func TestProxyProtocolForbidden(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 用户仅启用 Anthropic，测试 OpenAI 请求
	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "protouser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}
	model := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    user.ID,
		ModelName: "test-model",
	}
	if err := modelsdb.AddUserModel(model); err != nil {
		t.Fatalf("add model failed: %v", err)
	}

	// 加载缓存
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}

	reqBody := `{"model":"test-model","messages":[]}`
	req := httptest.NewRequest("POST", "/OpenAI/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+model.APIKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handleAIProxyRequest(rec, req, protocol.AgentProtocolType_OpenAI)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d, body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Protocol not enabled") {
		t.Errorf("body does not contain 'Protocol not enabled', got: %s", rec.Body.String())
	}
}

// TestProxyRejectsDisabledEndpoint 测试代理在源站被禁用时返回 403
// 验证：modelsdb.TAgentDstEndPoint.Status=0 的源站，请求会被代理拦截并返回 403 Forbidden
func TestProxyRejectsDisabledEndpoint(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 用户 + 用户模型（合法授权链路）
	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "disableduser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}
	model := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    user.ID,
		ModelName: "claude-3-5-sonnet-disabled",
	}
	if err := modelsdb.AddUserModel(model); err != nil {
		t.Fatalf("add user model failed: %v", err)
	}

	// 预创建一条 modelsdb.TAgentDstEndPoint 记录，先添加再禁用
	if err := modelsdb.InitAgentDstEndPointTable(); err != nil {
		t.Fatalf("init dst endpoint table failed: %v", err)
	}
	endpoint := &modelsdb.TAgentDstEndPoint{
		UserID:       user.ID,
		PlatformName: "TestPlatform",
		ModelName:    "test-model",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   "https://api.test.com/v1",
		APIKey:       "test-api-key-123",
	}
	if err := modelsdb.AddDstEndPoint(endpoint); err != nil {
		t.Fatalf("add dst endpoint failed: %v", err)
	}
	// 显式禁用（modelsdb.AddDstEndPoint 默认启用，需要后续更新）
	if err := modelsdb.UpdateDstEndPointStatus(endpoint.ID, 0); err != nil {
		t.Fatalf("disable dst endpoint failed: %v", err)
	}

	// 创建智能路由，指向该禁用的源站
	if err := modelsdb.InitAgentHttpAIRouteTable(); err != nil {
		t.Fatalf("init ai route table failed: %v", err)
	}
	route := &modelsdb.TAgentHttpAIRoute{
		UserID:                user.ID,
		UserModelID:           model.ID,
		ProtocolType:          protocol.AgentProtocolType_Anthropic,
		DstEndPointIDList:     strconv.FormatUint(endpoint.ID, 10),
		AlgorithmStrategyType: modelsdb.AlgorithmStrategyType_FirstID,
	}
	if err := modelsdb.AddAIRoute(route); err != nil {
		t.Fatalf("add ai route failed: %v", err)
	}

	// 重新加载缓存
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}

	// 构造合法授权的代理请求
	reqBody := `{"model":"claude-3-5-sonnet-disabled","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/Anthropic/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+model.APIKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// 调用代理处理器
	handleAIProxyRequest(rec, req, protocol.AgentProtocolType_Anthropic)

	// 断言：返回 403 Forbidden（因为源站被禁用）
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d, body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	// 断言：响应体包含 "disabled"
	if !strings.Contains(rec.Body.String(), "disabled") {
		t.Errorf("body does not contain 'disabled', got: %s", rec.Body.String())
	}
	// 断言：响应体包含 "Forbidden" 错误码
	if !strings.Contains(rec.Body.String(), "Forbidden") {
		t.Errorf("body does not contain 'Forbidden', got: %s", rec.Body.String())
	}

	// 反向验证：把源站启用后，相同请求应能继续走到下一步（转发到目标源站）
	if err := modelsdb.UpdateDstEndPointStatus(endpoint.ID, 1); err != nil {
		t.Fatalf("update dst endpoint status failed: %v", err)
	}
	// 重新加载缓存
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load cache failed: %v", err)
	}
	req2 := httptest.NewRequest("POST", "/Anthropic/v1/messages", strings.NewReader(reqBody))
	req2.Header.Set("Authorization", "Bearer "+model.APIKey)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handleAIProxyRequest(rec2, req2, protocol.AgentProtocolType_Anthropic)
	if rec2.Code == http.StatusForbidden {
		t.Errorf("after enabling, status should not be 403, got: %s", rec2.Body.String())
	}
}
