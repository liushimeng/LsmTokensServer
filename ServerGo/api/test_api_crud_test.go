package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/protocol"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// TestUserCRUD 测试用户管理 CRUD
func TestUserCRUD(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// Create
	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "cruduser",
		AnthropicEnabled: true,
		OpenAIEnabled:    true,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("user id should be assigned after create")
	}

	// Read by ID
	found, err := modelsdb.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("get user by id failed: %v", err)
	}
	if found.UserName != "cruduser" {
		t.Errorf("username mismatch: %s", found.UserName)
	}

	// Update
	user.UserName = "cruduser-updated"
	if err := modelsdb.UpdateUser(user); err != nil {
		t.Fatalf("update user failed: %v", err)
	}
	updated, _ := modelsdb.GetUserByID(user.ID)
	if updated.UserName != "cruduser-updated" {
		t.Errorf("update not applied: %s", updated.UserName)
	}

	// Delete
	if err := modelsdb.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete user failed: %v", err)
	}
	_, err = modelsdb.GetUserByID(user.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

// TestUserModelCRUD 测试用户模型管理 CRUD
func TestUserModelCRUD(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 先创建用户
	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "modeluser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}

	// Create model
	model := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    user.ID,
		ModelName: "gpt-4-turbo-test",
	}
	if err := modelsdb.AddUserModel(model); err != nil {
		t.Fatalf("add user model failed: %v", err)
	}
	if model.APIKey == "" {
		t.Fatal("model api key should be auto-generated")
	}

	// Read by user ID
	models, err := modelsdb.GetUserModelsByUserID(user.ID)
	if err != nil {
		t.Fatalf("get user models failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}

	// Read by API Key
	found, err := modelsdb.GetUserModelByAPIKey(model.APIKey)
	if err != nil {
		t.Fatalf("get model by api key failed: %v", err)
	}
	if found.ModelName != "gpt-4-turbo-test" {
		t.Errorf("model name mismatch: %s", found.ModelName)
	}

	// Update
	model.ModelName = "gpt-4o-test"
	if err := modelsdb.UpdateUserModel(model); err != nil {
		t.Fatalf("update model failed: %v", err)
	}
	updated, _ := modelsdb.GetUserModelByID(model.ID)
	if updated == nil || updated.ModelName != "gpt-4o-test" {
		t.Error("model update not applied")
	}

	// Delete
	if err := modelsdb.DeleteUserModel(model.ID); err != nil {
		t.Fatalf("delete model failed: %v", err)
	}
	_, err = modelsdb.GetUserModelByID(model.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

// TestDstEndPointCRUD 测试源站接入点管理 CRUD
func TestDstEndPointCRUD(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "endpointuser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}

	// Create
	endpoint := &modelsdb.TAgentDstEndPoint{
		UserID:       user.ID,
		PlatformName: "TestPlatform",
		ModelName:    "claude-3-opus",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   "https://api.test.com/v1",
		APIKey:       "sk-test-dst-key-12345",
	}
	if err := modelsdb.AddDstEndPoint(endpoint); err != nil {
		t.Fatalf("add endpoint failed: %v", err)
	}

	// Read by ID
	found, err := modelsdb.GetDstEndPointByID(endpoint.ID)
	if err != nil {
		t.Fatalf("get endpoint failed: %v", err)
	}
	if found.URLAddress != "https://api.test.com/v1" {
		t.Errorf("url mismatch: %s", found.URLAddress)
	}

	// Read by user ID
	endpoints, err := modelsdb.GetDstEndPointsByUserID(user.ID)
	if err != nil {
		t.Fatalf("get endpoints by user failed: %v", err)
	}
	if len(endpoints) != 1 {
		t.Errorf("expected 1 endpoint, got %d", len(endpoints))
	}

	// Update
	endpoint.URLAddress = "https://api.updated.com/v2"
	if err := modelsdb.UpdateDstEndPoint(endpoint); err != nil {
		t.Fatalf("update endpoint failed: %v", err)
	}
	updated, _ := modelsdb.GetDstEndPointByID(endpoint.ID)
	if updated.URLAddress != "https://api.updated.com/v2" {
		t.Errorf("update not applied: %s", updated.URLAddress)
	}

	// Delete
	if err := modelsdb.DeleteDstEndPoint(endpoint.ID); err != nil {
		t.Fatalf("delete endpoint failed: %v", err)
	}
	_, err = modelsdb.GetDstEndPointByID(endpoint.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

// TestAIRouteCRUD 测试智能路由管理 CRUD
func TestAIRouteCRUD(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "routeuser",
		AnthropicEnabled: true,
		OpenAIEnabled:    false,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}

	model := &modelsdb.TAgentHttpUserModelInfo{
		UserID:    user.ID,
		ModelName: "claude-3-5-sonnet",
	}
	if err := modelsdb.AddUserModel(model); err != nil {
		t.Fatalf("add model failed: %v", err)
	}

	endpoint := &modelsdb.TAgentDstEndPoint{
		UserID:       user.ID,
		PlatformName: "Anthropic",
		ModelName:    "claude-3-5-sonnet-20241022",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   "https://api.anthropic.com",
		APIKey:       "sk-anthropic-key-12345",
	}
	if err := modelsdb.AddDstEndPoint(endpoint); err != nil {
		t.Fatalf("add endpoint failed: %v", err)
	}

	// Create route
	route := &modelsdb.TAgentHttpAIRoute{
		UserID:                user.ID,
		UserModelID:           model.ID,
		ProtocolType:          protocol.AgentProtocolType_Anthropic,
		DstEndPointIDList:     strconv.FormatUint(endpoint.ID, 10),
		DstEndPointIDNumber:   1,
		AlgorithmStrategyType: modelsdb.AlgorithmStrategyType_FirstID,
	}
	if err := modelsdb.AddAIRoute(route); err != nil {
		t.Fatalf("add route failed: %v", err)
	}

	// Read by user model ID and protocol
	found, err := modelsdb.GetAIRouteByUserModelIDAndProtocol(model.ID, protocol.AgentProtocolType_Anthropic)
	if err != nil {
		t.Fatalf("get route by model id failed: %v", err)
	}
	selectedID, _ := modelsdb.GetFirstDstEndPointIDFromRoute(found)
	if selectedID != endpoint.ID {
		t.Errorf("endpoint id mismatch: %d", selectedID)
	}

	// Read with details
	routeDetail, endpointDetail, err := modelsdb.GetAIRouteWithDetails(model.ID, protocol.AgentProtocolType_Anthropic)
	if err != nil {
		t.Fatalf("get route with details failed: %v", err)
	}
	if routeDetail == nil || endpointDetail == nil {
		t.Fatal("route or endpoint detail is nil")
	}
	if endpointDetail.URLAddress != "https://api.anthropic.com" {
		t.Errorf("endpoint url mismatch: %s", endpointDetail.URLAddress)
	}

	// Read by user ID
	routes, err := modelsdb.GetAIRoutesByUserID(user.ID)
	if err != nil {
		t.Fatalf("get routes by user failed: %v", err)
	}
	if len(routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(routes))
	}

	// Update: create another endpoint and switch to it
	endpoint2 := &modelsdb.TAgentDstEndPoint{
		UserID:       user.ID,
		PlatformName: "OpenRouter",
		ModelName:    "claude-3-5-sonnet",
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   "https://openrouter.ai",
		APIKey:       "sk-openrouter-key-12345",
	}
	if err := modelsdb.AddDstEndPoint(endpoint2); err != nil {
		t.Fatalf("add endpoint2 failed: %v", err)
	}

	route.DstEndPointIDList = strconv.FormatUint(endpoint2.ID, 10)
	if err := modelsdb.UpdateAIRoute(route); err != nil {
		t.Fatalf("update route failed: %v", err)
	}
	updated, _ := modelsdb.GetAIRouteByUserModelIDAndProtocol(model.ID, protocol.AgentProtocolType_Anthropic)
	updatedSelectedID, _ := modelsdb.GetFirstDstEndPointIDFromRoute(updated)
	if updatedSelectedID != endpoint2.ID {
		t.Errorf("route update not applied: %d", updatedSelectedID)
	}

	// Delete
	if err := modelsdb.DeleteAIRoute(route.ID); err != nil {
		t.Fatalf("delete route failed: %v", err)
	}
	_, err = modelsdb.GetAIRouteByUserModelIDAndProtocol(model.ID, protocol.AgentProtocolType_Anthropic)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

// TestGetFirstDstEndPointIDFromRoute 测试路由目标源站 ID 解析
func TestGetFirstDstEndPointIDFromRoute(t *testing.T) {
	// Case 1: 新字段 populated
	route1 := &modelsdb.TAgentHttpAIRoute{
		DstEndPointIDList:     "3,5,7",
		DstEndPointIDNumber:   3,
		AlgorithmStrategyType: modelsdb.AlgorithmStrategyType_FirstID,
	}
	id, err := modelsdb.GetFirstDstEndPointIDFromRoute(route1)
	if err != nil || id != 3 {
		t.Errorf("expected 3, got %d, err=%v", id, err)
	}

	// Case 2: 只有旧字段（已删除，改为空列表测试）
	route2 := &modelsdb.TAgentHttpAIRoute{
		DstEndPointIDList: "42",
	}
	id, err = modelsdb.GetFirstDstEndPointIDFromRoute(route2)
	if err != nil || id != 42 {
		t.Errorf("expected 42, got %d, err=%v", id, err)
	}

	// Case 3: 多 ID 列表
	route3 := &modelsdb.TAgentHttpAIRoute{
		DstEndPointIDList:     "7,8,9",
		DstEndPointIDNumber:   3,
		AlgorithmStrategyType: modelsdb.AlgorithmStrategyType_FirstID,
	}
	id, err = modelsdb.GetFirstDstEndPointIDFromRoute(route3)
	if err != nil || id != 7 {
		t.Errorf("expected 7 (first ID), got %d, err=%v", id, err)
	}

	// Case 4: 都为空
	route4 := &modelsdb.TAgentHttpAIRoute{ID: 999}
	_, err = modelsdb.GetFirstDstEndPointIDFromRoute(route4)
	if err == nil {
		t.Error("expected error for empty route")
	}
}

// TestUserManageInterfaceAPI 测试用户管理 Web API
func TestUserManageInterfaceAPI(t *testing.T) {
	cleanup := initTestEnv(t)
	defer cleanup()

	// 先添加一个用户
	user := &modelsdb.TAgentHttpUserInfo{
		UserName:         "apiuser",
		AnthropicEnabled: true,
		OpenAIEnabled:    true,
	}
	if err := modelsdb.AddUser(user); err != nil {
		t.Fatalf("add user failed: %v", err)
	}

	// 测试 list 接口
	req := httptest.NewRequest("POST", "/UserManageInterface", strings.NewReader(`{"action":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var listResp struct {
		Success bool                       `json:"success"`
		Data    []modelsdb.TAgentHttpUserInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response failed: %v", err)
	}
	if !listResp.Success {
		t.Fatalf("list success = false")
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 user, got %d", len(listResp.Data))
	}

	// 测试 add 接口
	addPayload, _ := json.Marshal(map[string]interface{}{
		"action":            "add",
		"user_name":         "newapiuser",
		"anthropic_enabled": true,
		"openai_enabled":    false,
	})
	req = httptest.NewRequest("POST", "/UserManageInterface", bytes.NewReader(addPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var addResp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("unmarshal add response failed: %v", err)
	}
	if !addResp.Success {
		t.Fatalf("add success = false, body: %s", rec.Body.String())
	}

	// 验证总数变为 2
	req = httptest.NewRequest("POST", "/UserManageInterface", strings.NewReader(`{"action":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	userManageInterfaceHandle(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if len(listResp.Data) != 2 {
		t.Fatalf("expected 2 users after add, got %d", len(listResp.Data))
	}
}
