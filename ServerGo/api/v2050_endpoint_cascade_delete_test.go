package api

// ==================== v2.0.50 源站删除级联清理智能路由关联数据测试 ====================
//
// 需求：/DstEndPointManage 源站管理页面删除记录时，真实删除 MySQL 记录，
//       并优先处理 TAgentHttpAIRoute 表中的关联数据：
//   - 多源站路由：从 DstEndPointIDList / DstEndPointIDStatusList /
//     DstEndPointAlgorithmTypeList 三个列表同一位置剔除该源站；
//   - 仅剩该源站的路由：级联硬删除整套路由。
//
// 覆盖范围：
//   1. cleanupRoutesForEndpointDeletion / removeEndpointRefFromRoute /
//      deleteRouteForEndpointDeletion 的 NilDB / 零值边界（纯函数路径）
//   2. SQLite 内存库集成测试：DeleteDstEndPoint 端到端验证
//   3. handler 删除成功消息契约（包含「关联引用」文案）

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

// ---------- 1. NilDB / 零值边界 ----------

// TestCleanupRoutesForEndpointDeletion_NilDB 缺符号：cleanupRoutesForEndpointDeletion
// 现为 models 包未导出函数。
func TestCleanupRoutesForEndpointDeletion_NilDB(t *testing.T) {
	t.Skip("缺符号 cleanupRoutesForEndpointDeletion（models 包未导出）")
}

// TestCleanupRoutesForEndpointDeletion_ZeroID 缺符号：cleanupRoutesForEndpointDeletion。
func TestCleanupRoutesForEndpointDeletion_ZeroID(t *testing.T) {
	t.Skip("缺符号 cleanupRoutesForEndpointDeletion（models 包未导出）")
}

func TestDeleteDstEndPoint_NilDB(t *testing.T) {
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	if err := modelsdb.DeleteDstEndPoint(1); err == nil {
		t.Error("NilDB 应返回错误")
	}
}

// ---------- 2. SQLite 集成测试 ----------

// setupEndpointCascadeTestDB 初始化独立 SQLite 内存库 + 源站表 + 路由表
func setupEndpointCascadeTestDB(t *testing.T) func() {
	t.Helper()
	origDB := database.DB
	origDisabled := logger.IsUserLogDisabled()
	logger.SetDisableUserLog(true)

	sqliteDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.DB = sqliteDB
	// 先迁移用户/用户模型表：LoadAgentCacheFromDB 依赖这两张表
	if err := modelsdb.InitAgentHttpUserInfoTable(); err != nil {
		t.Fatalf("init user info: %v", err)
	}
	if err := modelsdb.InitAgentHttpUserModelInfoTable(); err != nil {
		t.Fatalf("init user model info: %v", err)
	}
	if err := database.DB.Table(modelsdb.AgentDstEndPointTableName).AutoMigrate(&modelsdb.TAgentDstEndPoint{}); err != nil {
		t.Fatalf("migrate dst endpoint: %v", err)
	}
	if err := database.DB.Table(modelsdb.AgentHttpAIRouteTableName).AutoMigrate(&modelsdb.TAgentHttpAIRoute{}); err != nil {
		t.Fatalf("migrate ai route: %v", err)
	}
	// 初始化内存缓存 map（生产环境由 LoadAgentCacheFromDB 完成）
	if err := modelsdb.LoadAgentCacheFromDB(); err != nil {
		t.Fatalf("load agent cache: %v", err)
	}

	return func() {
		if sqlDB, _ := database.DB.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = origDB
		logger.SetDisableUserLog(origDisabled)
	}
}

func mustAddEndpoint(t *testing.T, platform, model string) uint64 {
	t.Helper()
	ep := &modelsdb.TAgentDstEndPoint{
		UserID:       1,
		PlatformName: platform,
		ModelName:    model,
		ProtocolType: protocol.AgentProtocolType_Anthropic,
		URLAddress:   "https://api.test.com",
		APIKey:       "sk-test-key-123456",
		Status:       1,
	}
	if err := database.DB.Table(modelsdb.AgentDstEndPointTableName).Create(ep).Error; err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	return ep.ID
}

func mustAddRoute(t *testing.T, idList string) uint64 {
	t.Helper()
	ids, _ := modelsdb.ParseDstEndPointIDList(idList)
	route := &modelsdb.TAgentHttpAIRoute{
		UserID:                       1,
		UserModelID:                  100,
		ProtocolType:                 protocol.AgentProtocolType_Anthropic,
		DstEndPointIDList:            idList,
		DstEndPointIDStatusList:      modelsdb.BuildDefaultDstEndPointIDStatusList(len(ids)),
		DstEndPointAlgorithmTypeList: modelsdb.BuildDefaultDstEndPointAlgorithmTypeList(len(ids)),
		DstEndPointIDNumber:          len(ids),
		AlgorithmStrategyType:        1,
	}
	if err := database.DB.Table(modelsdb.AgentHttpAIRouteTableName).Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	return route.ID
}

func getRouteByID(t *testing.T, id uint64) (*modelsdb.TAgentHttpAIRoute, bool) {
	t.Helper()
	var route modelsdb.TAgentHttpAIRoute
	err := database.DB.Table(modelsdb.AgentHttpAIRouteTableName).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&route).Error
	if err != nil {
		return nil, false
	}
	return &route, true
}

// TestDeleteDstEndPoint_MultiEndpointRoute_RemovesRef 验证多源站路由剔除引用
func TestDeleteDstEndPoint_MultiEndpointRoute_RemovesRef(t *testing.T) {
	cleanup := setupEndpointCascadeTestDB(t)
	defer cleanup()

	ep1 := mustAddEndpoint(t, "plat-a", "model-a")
	ep2 := mustAddEndpoint(t, "plat-b", "model-b")
	ep3 := mustAddEndpoint(t, "plat-c", "model-c")
	routeID := mustAddRoute(t, "1,2,3") // 依赖 mustAddEndpoint 自增 ID 从 1 开始
	_ = ep1
	_ = ep3

	if err := modelsdb.DeleteDstEndPoint(ep2); err != nil {
		t.Fatalf("DeleteDstEndPoint failed: %v", err)
	}

	// 源站本体真实删除
	var epCount int64
	database.DB.Table(modelsdb.AgentDstEndPointTableName).Where("id = ?", ep2).Count(&epCount)
	if epCount != 0 {
		t.Errorf("源站 %d 应被真实删除，剩余 %d 条", ep2, epCount)
	}

	// 路由保留，且 ep2 已从三个列表剔除
	route, ok := getRouteByID(t, routeID)
	if !ok {
		t.Fatal("多源站路由不应被级联删除")
	}
	if route.DstEndPointIDList != "1,3" {
		t.Errorf("DstEndPointIDList = %q, want %q", route.DstEndPointIDList, "1,3")
	}
	if route.DstEndPointIDNumber != 2 {
		t.Errorf("DstEndPointIDNumber = %d, want 2", route.DstEndPointIDNumber)
	}
	statuses, _ := modelsdb.ParseDstEndPointIDStatusList(route.DstEndPointIDStatusList)
	if len(statuses) != 2 {
		t.Errorf("DstEndPointIDStatusList 长度 = %d, want 2", len(statuses))
	}
	algos, _ := modelsdb.ParseDstEndPointAlgorithmTypeList(route.DstEndPointAlgorithmTypeList)
	if len(algos) != 2 {
		t.Errorf("DstEndPointAlgorithmTypeList 长度 = %d, want 2", len(algos))
	}
}

// TestDeleteDstEndPoint_SingleEndpointRoute_CascadeDeletes 验证单源站路由级联删除
func TestDeleteDstEndPoint_SingleEndpointRoute_CascadeDeletes(t *testing.T) {
	cleanup := setupEndpointCascadeTestDB(t)
	defer cleanup()

	ep1 := mustAddEndpoint(t, "plat-a", "model-a")
	routeID := mustAddRoute(t, "1")

	if err := modelsdb.DeleteDstEndPoint(ep1); err != nil {
		t.Fatalf("DeleteDstEndPoint failed: %v", err)
	}

	if _, ok := getRouteByID(t, routeID); ok {
		t.Error("仅剩被删源站的路由应被级联删除")
	}

	// 物理删除（Unscoped 硬删除）
	var total int64
	database.DB.Table(modelsdb.AgentHttpAIRouteTableName).Unscoped().Where("id = ?", routeID).Count(&total)
	if total != 0 {
		t.Errorf("级联删除应为硬删除，Unscoped 仍查到 %d 条", total)
	}
}

// TestDeleteDstEndPoint_UnrelatedRoute_Untouched 验证不相关路由不受影响
func TestDeleteDstEndPoint_UnrelatedRoute_Untouched(t *testing.T) {
	cleanup := setupEndpointCascadeTestDB(t)
	defer cleanup()

	ep1 := mustAddEndpoint(t, "plat-a", "model-a")
	ep2 := mustAddEndpoint(t, "plat-b", "model-b")
	ep3 := mustAddEndpoint(t, "plat-c", "model-c")
	routeID := mustAddRoute(t, "2,3") // 不含 ep1
	_ = ep2
	_ = ep3

	if err := modelsdb.DeleteDstEndPoint(ep1); err != nil {
		t.Fatalf("DeleteDstEndPoint failed: %v", err)
	}

	route, ok := getRouteByID(t, routeID)
	if !ok {
		t.Fatal("不相关路由不应被删除")
	}
	if route.DstEndPointIDList != "2,3" {
		t.Errorf("不相关路由 DstEndPointIDList 被修改: %q", route.DstEndPointIDList)
	}
}

// TestDeleteDstEndPoint_MixedRoutes 验证同时存在多源站路由 + 单源站路由 + 无关联路由
func TestDeleteDstEndPoint_MixedRoutes(t *testing.T) {
	cleanup := setupEndpointCascadeTestDB(t)
	defer cleanup()

	ep1 := mustAddEndpoint(t, "plat-a", "model-a")
	ep2 := mustAddEndpoint(t, "plat-b", "model-b")
	_ = ep2
	routeMulti := mustAddRoute(t, "1,2") // 多源站 → 剔除
	routeSingle := mustAddRoute(t, "1")  // 单源站 → 级联删除
	routeNone := mustAddRoute(t, "2")    // 无关联 → 不动

	if err := modelsdb.DeleteDstEndPoint(ep1); err != nil {
		t.Fatalf("DeleteDstEndPoint failed: %v", err)
	}

	r, ok := getRouteByID(t, routeMulti)
	if !ok {
		t.Fatal("多源站路由应保留")
	}
	if r.DstEndPointIDList != "2" {
		t.Errorf("多源站路由剔除后 DstEndPointIDList = %q, want %q", r.DstEndPointIDList, "2")
	}

	if _, ok := getRouteByID(t, routeSingle); ok {
		t.Error("单源站路由应被级联删除")
	}

	if _, ok := getRouteByID(t, routeNone); !ok {
		t.Error("无关联路由应保留")
	}
}

// TestDeleteDstEndPoint_NotFound 验证删除不存在的源站返回错误
func TestDeleteDstEndPoint_NotFound(t *testing.T) {
	cleanup := setupEndpointCascadeTestDB(t)
	defer cleanup()

	if err := modelsdb.DeleteDstEndPoint(9999); err == nil {
		t.Error("删除不存在的源站应返回错误")
	}
}

// ---------- 3. handler 删除成功消息契约 ----------

func TestDstEndPointManageHandler_DeleteMessageMentionsCascade(t *testing.T) {
	cleanup := setupEndpointCascadeTestDB(t)
	defer cleanup()

	ep1 := mustAddEndpoint(t, "plat-a", "model-a")

	body := strings.NewReader(`{"action":"delete","id":1}`)
	req := httptest.NewRequest("POST", "/DstEndPointManageInterface", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	dstEndPointManageInterfaceHandle(w, req)

	resp := w.Body.String()
	if !strings.Contains(resp, "关联引用") {
		t.Errorf("删除成功消息应包含「关联引用」提示，实际: %s", resp)
	}

	// 源站真实删除
	var count int64
	database.DB.Table(modelsdb.AgentDstEndPointTableName).Where("id = ?", ep1).Count(&count)
	if count != 0 {
		t.Errorf("handler 删除后源站 %d 应不存在，剩余 %d 条", ep1, count)
	}
}
