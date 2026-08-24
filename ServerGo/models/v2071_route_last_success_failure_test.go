package models

// v2.0.71: /AIRouteManage「最后记录信息列」重构的回归测试。
//
// 被锁住的契约：
//  1. 「最后使用」列的 Web 前端排序三件套（data-sort-key / validColumns /
//     renderSortIndicators keys / getSortedRoutes 比较器）已全部移除。
//  2. 「最后成功记录」「最后失败记录」两列由服务端按 response_status 是否
//     2xx 分组产出：success=LIKE '2%'，failure=NOT LIKE '2%'（空串=传输错误
//     归入失败，与 isResponseSuccess(0)=false 语义一致）。
//  3. 两列均复用 BatchGetRouteLastUsedTimes 批量链路（每 key 两次
//     getRouteLastRecordByStatus），禁止另起独立批量查询轮次。
//  4. 每组字段（时间/响应状态/目标模型）严格来自同一条记录（同一次 SQL）。
//  5. 查询失败（红「查询失败」）与暂无记录（灰「暂无成功/失败记录」）必须在
//     数据结构（*Failed）与页面上都可区分，禁止故障静默降级。
//  6. 两列均不可排序（不写 data-sort-key）。

import (
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
)

// ---------- 1. NilDB / 缺失输入守护 ----------

// TestBatchGetRouteLastUsedTimes_LastRecordNilDB database.DB==nil 时每条路由两组字段
// 都必须标记查询失败，禁止伪装成「暂无记录」。
func TestBatchGetRouteLastUsedTimes_LastRecordNilDB(t *testing.T) {
	origDB := database.DB
	database.DB = nil
	defer func() { database.DB = origDB }()

	items := []RouteBatchStatItem{
		{RouteID: 1, Key: RouteBatchStatKey{UserName: "u1", ModelName: "m1", ProtocolType: 1}},
	}
	got := BatchGetRouteLastUsedTimes(items, 8)
	r, ok := got[1]
	if !ok {
		t.Fatal("route 1 缺失")
	}
	if !r.LastSuccessFailed || !r.LastFailureFailed {
		t.Errorf("database.DB==nil 时 LastSuccessFailed/LastFailureFailed 必须均为 true, got %+v", r)
	}
	if r.LastSuccessHasRecord || r.LastFailureHasRecord {
		t.Errorf("查询失败时 HasRecord 必须为 false（禁止伪装成有记录）, got %+v", r)
	}
}

// TestBatchGetRouteLastUsedTimes_MissingUserModel_MarksLastRecordFailed
// user/model 缺失说明 lookupRouteModelName 没解析出模型名，属真实故障，
// 两组字段都必须标记失败而非「暂无记录」。
func TestBatchGetRouteLastUsedTimes_MissingUserModel_MarksLastRecordFailed(t *testing.T) {
	items := []RouteBatchStatItem{
		{RouteID: 10, Key: RouteBatchStatKey{UserName: "", ModelName: "m1"}},
		{RouteID: 11, Key: RouteBatchStatKey{UserName: "u1", ModelName: ""}},
	}
	got := BatchGetRouteLastUsedTimes(items, 8)
	for _, rid := range []uint64{10, 11} {
		r, ok := got[rid]
		if !ok {
			t.Fatalf("route %d 缺失", rid)
		}
		if !r.LastSuccessFailed || !r.LastFailureFailed {
			t.Errorf("route %d: user/model 缺失应标记两组 Failed=true, got %+v", rid, r)
		}
	}
}

// ---------- 2. database.DB 层源码契约 ----------

// extractFuncBodyV2071 截取从函数签名到下一个顶层 `\nfunc ` 之间的源码片段
func extractFuncBodyV2071(t *testing.T, src, signature string) string {
	t.Helper()
	idx := strings.Index(src, signature)
	if idx < 0 {
		t.Fatalf("未找到函数签名: %s", signature)
	}
	rest := src[idx+len(signature):]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestGetRouteLastRecordByStatus_SQLShape 守护成功/失败判定的 SQL 形态：
// success 用 LIKE ?（参数 "2%"），failure 用 NOT LIKE ?，且必须同时 SELECT 三列
// （created_at, response_status, dst_model_name 同源同行）。
// LIKE 模式必须走参数化占位符 —— 直接内联 '2%' 会被 fmt.Sprintf 把 % 当动词
// 展开成 %!(...)（SQLite/MySQL 均报语法错误，本版本实测踩中）。
func TestGetRouteLastRecordByStatus_SQLShape(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2071(t, string(src), "func GetRouteLastRecordByStatus(")
	if !strings.Contains(body, `AND response_status LIKE ?`) {
		t.Error("success 分支必须使用 response_status LIKE ? 参数化判定 2xx 成功")
	}
	if !strings.Contains(body, `AND response_status NOT LIKE ?`) {
		t.Error("failure 分支必须使用 response_status NOT LIKE ?（空串归入失败）")
	}
	if !strings.Contains(body, `"2%"`) {
		t.Error(`LIKE 模式必须以参数 "2%" 传入，禁止内联进 SQL 字符串（% 会被 Sprintf 当动词）`)
	}
	if !strings.Contains(body, "SELECT created_at, response_status, dst_model_name FROM") {
		t.Error("SQL 必须同时 SELECT created_at, response_status, dst_model_name 三列（同源同行）")
	}
	if !strings.Contains(body, "ORDER BY id DESC LIMIT 1") {
		t.Error("SQL 必须走 ORDER BY id DESC LIMIT 1 快路径")
	}
}

// TestGetRouteLastRecordByStatus_UsesStatsDB 守护 statsDB() 25s context，
// 禁止裸 database.DB.Raw（v2.0.66 同源陷阱：超时砍断 socket 污染连接池）。
func TestGetRouteLastRecordByStatus_UsesStatsDB(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2071(t, string(src), "func GetRouteLastRecordByStatus(")
	if !strings.Contains(body, "StatsDB()") {
		t.Error("getRouteLastRecordByStatus 必须走 statsDB() 绑定 25s context")
	}
	if strings.Contains(body, "database.DB.Raw(") {
		t.Error("getRouteLastRecordByStatus 禁止使用裸 database.DB.Raw（无 context 保护）")
	}
}

// TestGetRouteLastRecordByStatus_CacheKeyIsolation 守护缓存 key 独立前缀 +
// success/failure 区分，禁止污染旧 GetRouteLastRecord / GetRouteLastUsedTime 缓存。
func TestGetRouteLastRecordByStatus_CacheKeyIsolation(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2071(t, string(src), "func GetRouteLastRecordByStatus(")
	if !strings.Contains(body, `"GetRouteLastRecordByStatus"`) {
		t.Error("cache key 必须使用独立前缀 GetRouteLastRecordByStatus")
	}
	if !strings.Contains(body, `"success"`) || !strings.Contains(body, `"failure"`) {
		t.Error("cache key extra 段必须区分 success / failure")
	}
	// 旧函数必须已删除（避免两条语义相近的查询路径并存）
	if strings.Contains(string(src), "func getRouteLastRecord(") {
		t.Error("旧 getRouteLastRecord 应已删除，统一走 getRouteLastRecordByStatus")
	}
}

// TestRouteBatchStatResult_LastRecordJSONTags 前端依赖的 JSON 字段契约
func TestRouteBatchStatResult_LastRecordJSONTags(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	s := string(src)
	for _, tag := range []string{
		`json:"last_success_at,omitempty"`,
		`json:"last_success_status,omitempty"`,
		`json:"last_success_status_code,omitempty"`,
		`json:"last_success_dst_model_name,omitempty"`,
		`json:"last_success_has_record,omitempty"`,
		`json:"last_success_failed,omitempty"`,
		`json:"last_failure_at,omitempty"`,
		`json:"last_failure_status,omitempty"`,
		`json:"last_failure_status_code,omitempty"`,
		`json:"last_failure_dst_model_name,omitempty"`,
		`json:"last_failure_has_record,omitempty"`,
		`json:"last_failure_failed,omitempty"`,
	} {
		if !strings.Contains(s, tag) {
			t.Errorf("RouteBatchStatResult 缺少 JSON tag %s（前端契约）", tag)
		}
	}
	// 旧三列字段必须已移除
	for _, old := range []string{
		`json:"last_used_at"`,
		`json:"last_used_failed,omitempty"`,
		`json:"last_response_status,omitempty"`,
		`json:"last_dst_model_name,omitempty"`,
	} {
		if strings.Contains(s, old) {
			t.Errorf("旧字段 %s 应已从 RouteBatchStatResult 移除", old)
		}
	}
}

// TestBatchGetRouteLastUsedTimes_ReusesByStatusQuery 守护批量链路的内层
// 调用 getRouteLastRecordByStatus 两次（success+failure），禁止另起独立批量轮次。
func TestBatchGetRouteLastUsedTimes_ReusesByStatusQuery(t *testing.T) {
	src, err := os.ReadFile("subtable.go")
	if err != nil {
		t.Fatalf("读取源文件失败: %v", err)
	}
	body := extractFuncBodyV2071(t, string(src), "func BatchGetRouteLastUsedTimes(")
	if strings.Count(body, "GetRouteLastRecordByStatus(") < 2 {
		t.Errorf("BatchGetRouteLastUsedTimes 内层必须调用 getRouteLastRecordByStatus 至少两次（success+failure），got %d 次",
			strings.Count(body, "GetRouteLastRecordByStatus("))
	}
}

// ---------- 5. SQLite 集成：真实数据往返 ----------

func setupLastRecordTestDB(t *testing.T) func() {
	t.Helper()
	origDB := database.DB
	origDisabled := logger.IsUserLogDisabled()
	logger.SetDisableUserLog(true)
	t.Setenv("LSM_SKIP_BACKGROUND_BACKFILL", "1")

	sqliteDB, err := gorm.Open(sqlite.Open("file:v2071lastrecord?mode=memory&cache=shared"), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.DB = sqliteDB
	InitStatsCache()

	return func() {
		if sqlDB, _ := database.DB.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		database.DB = origDB
		logger.SetDisableUserLog(origDisabled)
		InitStatsCache()
	}
}

// insertLastRecordRows 向指定分表插入测试行
func insertLastRecordRows(t *testing.T, tableName string, rows []TAgentHttpTransactionDataItem) {
	t.Helper()
	for i := range rows {
		if err := database.DB.Table(tableName).Create(&rows[i]).Error; err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
}

// TestBatchGetRouteLastUsedTimes_SuccessAndFailure 核心：同一 (user, model, protocol)
// 下交错插入成功/失败记录，两列必须各自取到对应分组的最后一条。
func TestBatchGetRouteLastUsedTimes_SuccessAndFailure(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071"
		modelName = "m_v2071"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}

	base := time.Now().Truncate(time.Second)
	// 时间线（旧→新）：500 → 200 → 空串(传输错误) → 200
	// 期望：success 列取最新的 200；failure 列取最新的空串（传输错误）。
	failOld := base.Add(-4 * time.Hour)
	succOld := base.Add(-3 * time.Hour)
	failNew := base.Add(-2 * time.Hour)
	succNew := base.Add(-1 * time.Hour)
	insertLastRecordRows(t, tableName, []TAgentHttpTransactionDataItem{
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: failOld, ResponseStatus: "500 Internal Server Error", DstModelName: "gpt-4o-old"},
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: succOld, ResponseStatus: "200 OK", DstModelName: "gpt-4o-mini"},
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: failNew, ResponseStatus: "", DstModelName: "gpt-4o-timeout"},
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: succNew, ResponseStatus: "200 OK", DstModelName: "gpt-4o-2024-08-06"},
	})

	items := []RouteBatchStatItem{
		{RouteID: 401, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
	}
	got := BatchGetRouteLastUsedTimes(items, subTables)
	r, ok := got[401]
	if !ok {
		t.Fatal("结果缺失")
	}
	if r.LastSuccessFailed || r.LastFailureFailed {
		t.Fatalf("不应标记查询失败: %+v", r)
	}
	// 成功列：最新 2xx = succNew
	if !r.LastSuccessHasRecord {
		t.Fatal("LastSuccessHasRecord 应为 true")
	}
	if diff := r.LastSuccessAt.Sub(succNew); diff > time.Second || diff < -time.Second {
		t.Errorf("LastSuccessAt = %v, want ≈ %v", r.LastSuccessAt, succNew)
	}
	if r.LastSuccessStatus != "200 OK" || r.LastSuccessStatusCode != 200 {
		t.Errorf("LastSuccessStatus = %q code=%d, want \"200 OK\"/200", r.LastSuccessStatus, r.LastSuccessStatusCode)
	}
	if r.LastSuccessDstModelName != "gpt-4o-2024-08-06" {
		t.Errorf("LastSuccessDstModelName = %q, want gpt-4o-2024-08-06（同源同行）", r.LastSuccessDstModelName)
	}
	// 失败列：最新非 2xx = failNew（空串传输错误）
	if !r.LastFailureHasRecord {
		t.Fatal("LastFailureHasRecord 应为 true（空串传输错误必须归入失败）")
	}
	if diff := r.LastFailureAt.Sub(failNew); diff > time.Second || diff < -time.Second {
		t.Errorf("LastFailureAt = %v, want ≈ %v", r.LastFailureAt, failNew)
	}
	if r.LastFailureStatus != "" || r.LastFailureStatusCode != 0 {
		t.Errorf("LastFailureStatus = %q code=%d, want 空串/0（传输错误）", r.LastFailureStatus, r.LastFailureStatusCode)
	}
	if r.LastFailureDstModelName != "gpt-4o-timeout" {
		t.Errorf("LastFailureDstModelName = %q, want gpt-4o-timeout（同源同行）", r.LastFailureDstModelName)
	}
}

// TestBatchGetRouteLastUsedTimes_OnlySuccess 只有成功记录时：失败列必须是
// 「暂无失败记录」（HasRecord=false 且 Failed=false），禁止伪装成查询失败。
func TestBatchGetRouteLastUsedTimes_OnlySuccess(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071_ok"
		modelName = "m_v2071_ok"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}
	at := time.Now().Truncate(time.Second).Add(-time.Hour)
	insertLastRecordRows(t, tableName, []TAgentHttpTransactionDataItem{
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: at, ResponseStatus: "200 OK", DstModelName: "m1"},
	})

	got := BatchGetRouteLastUsedTimes([]RouteBatchStatItem{
		{RouteID: 402, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
	}, subTables)
	r := got[402]
	if !r.LastSuccessHasRecord {
		t.Error("有成功记录时 LastSuccessHasRecord 应为 true")
	}
	if r.LastFailureHasRecord {
		t.Error("无失败记录时 LastFailureHasRecord 应为 false（暂无失败记录）")
	}
	if r.LastFailureFailed {
		t.Error("无失败记录不等于查询失败，LastFailureFailed 必须为 false")
	}
}

// TestBatchGetRouteLastUsedTimes_OnlyFailure 只有失败记录时：成功列必须是
// 「暂无成功记录」。
func TestBatchGetRouteLastUsedTimes_OnlyFailure(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071_ng"
		modelName = "m_v2071_ng"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}
	at := time.Now().Truncate(time.Second).Add(-time.Hour)
	insertLastRecordRows(t, tableName, []TAgentHttpTransactionDataItem{
		{UserName: userName, ModelName: modelName, ProtocolType: 2, CreatedAt: at, ResponseStatus: "429 Too Many Requests", DstModelName: "m2"},
	})

	got := BatchGetRouteLastUsedTimes([]RouteBatchStatItem{
		{RouteID: 403, Protocol: 2, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 2}},
	}, subTables)
	r := got[403]
	if r.LastSuccessHasRecord {
		t.Error("无成功记录时 LastSuccessHasRecord 应为 false（暂无成功记录）")
	}
	if r.LastSuccessFailed {
		t.Error("无成功记录不等于查询失败，LastSuccessFailed 必须为 false")
	}
	if !r.LastFailureHasRecord {
		t.Error("有失败记录时 LastFailureHasRecord 应为 true")
	}
	if r.LastFailureStatusCode != 429 {
		t.Errorf("LastFailureStatusCode = %d, want 429", r.LastFailureStatusCode)
	}
}

// TestBatchGetRouteLastUsedTimes_NeverUsed 从未使用：两组都是暂无记录且无失败标记。
func TestBatchGetRouteLastUsedTimes_NeverUsed(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071_new"
		modelName = "m_v2071_new"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}

	got := BatchGetRouteLastUsedTimes([]RouteBatchStatItem{
		{RouteID: 404, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
	}, subTables)
	r, ok := got[404]
	if !ok {
		t.Fatal("结果缺失")
	}
	if r.LastSuccessHasRecord || r.LastFailureHasRecord {
		t.Errorf("从未使用应两组 HasRecord=false, got %+v", r)
	}
	if r.LastSuccessFailed || r.LastFailureFailed {
		t.Errorf("表存在但无记录属正常状态，不应标记 Failed, got %+v", r)
	}
}

// TestBatchGetRouteLastUsedTimes_PerProtocolIsolationV2071 核心回归（沿用 v2.0.66）：
// 同一 (user, model) 下 Anthropic 与 OpenAI 两条路由必须各自拿到自己协议的记录。
func TestBatchGetRouteLastUsedTimes_PerProtocolIsolationV2071(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071_pp"
		modelName = "m_v2071_pp"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}

	base := time.Now().Truncate(time.Second)
	anthropicAt := base.Add(-2 * time.Hour)
	openaiAt := base.Add(-30 * time.Hour)
	insertLastRecordRows(t, tableName, []TAgentHttpTransactionDataItem{
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: anthropicAt, ResponseStatus: "200 OK", DstModelName: "claude-x"},
		{UserName: userName, ModelName: modelName, ProtocolType: 2, CreatedAt: openaiAt, ResponseStatus: "200 OK", DstModelName: "gpt-x"},
	})

	got := BatchGetRouteLastUsedTimes([]RouteBatchStatItem{
		{RouteID: 405, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
		{RouteID: 406, Protocol: 2, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 2}},
	}, subTables)

	r1 := got[405]
	r2 := got[406]
	if r1.LastSuccessDstModelName != "claude-x" {
		t.Errorf("Anthropic 路由 LastSuccessDstModelName = %q, want claude-x", r1.LastSuccessDstModelName)
	}
	if r2.LastSuccessDstModelName != "gpt-x" {
		t.Errorf("OpenAI 路由 LastSuccessDstModelName = %q, want gpt-x", r2.LastSuccessDstModelName)
	}
	if r1.LastSuccessAt.Equal(r2.LastSuccessAt) {
		t.Error("Anthropic 与 OpenAI 路由的 LastSuccessAt 相同 —— 协议扇出 bug 回归")
	}
}

// TestBatchGetRouteLastUsedTimes_SharedKeyFansOutV2071 同一 (user, model, protocol)
// 下的多条路由共享一次查询结果。
func TestBatchGetRouteLastUsedTimes_SharedKeyFansOutV2071(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071_share"
		modelName = "m_v2071_share"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}
	at := time.Now().Truncate(time.Second).Add(-time.Hour)
	insertLastRecordRows(t, tableName, []TAgentHttpTransactionDataItem{
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: at, ResponseStatus: "200 OK", DstModelName: "m-shared"},
	})

	got := BatchGetRouteLastUsedTimes([]RouteBatchStatItem{
		{RouteID: 407, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
		{RouteID: 408, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
	}, subTables)
	for _, rid := range []uint64{407, 408} {
		r, ok := got[rid]
		if !ok {
			t.Fatalf("route %d 缺失", rid)
		}
		if !r.LastSuccessHasRecord || r.LastSuccessDstModelName != "m-shared" {
			t.Errorf("route %d 共享 key 应扇出同一结果, got %+v", rid, r)
		}
	}
}

// TestBatchGetRouteLastUsedTimes_StatusAndTimeFromSameRowV2071 同源同行契约：
// 时间/状态/目标模型必须来自同一条记录（同一次 SQL），禁止串行两轮。
func TestBatchGetRouteLastUsedTimes_StatusAndTimeFromSameRowV2071(t *testing.T) {
	cleanup := setupLastRecordTestDB(t)
	defer cleanup()

	const (
		userName  = "u_v2071_row"
		modelName = "m_v2071_row"
		subTables = 8
	)
	tableName := GetAgentHttpTableName(userName, modelName, subTables)
	if err := database.DB.Table(tableName).AutoMigrate(&TAgentHttpTransactionDataItem{}); err != nil {
		t.Fatalf("migrate %s: %v", tableName, err)
	}

	base := time.Now().Truncate(time.Second)
	oldAt := base.Add(-2 * time.Hour)
	newAt := base.Add(-1 * time.Hour)
	// 两条都是成功：最新一条的 status+model 必须与最新时间同行
	insertLastRecordRows(t, tableName, []TAgentHttpTransactionDataItem{
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: oldAt, ResponseStatus: "201 Created", DstModelName: "m-old"},
		{UserName: userName, ModelName: modelName, ProtocolType: 1, CreatedAt: newAt, ResponseStatus: "200 OK", DstModelName: "m-new"},
	})

	got := BatchGetRouteLastUsedTimes([]RouteBatchStatItem{
		{RouteID: 409, Protocol: 1, Key: RouteBatchStatKey{UserName: userName, ModelName: modelName, ProtocolType: 1}},
	}, subTables)
	r := got[409]
	if diff := r.LastSuccessAt.Sub(newAt); diff > time.Second || diff < -time.Second {
		t.Errorf("LastSuccessAt = %v, want ≈ %v", r.LastSuccessAt, newAt)
	}
	if r.LastSuccessStatus != "200 OK" || r.LastSuccessDstModelName != "m-new" {
		t.Errorf("状态/模型必须与最新时间同源同行: status=%q model=%q, want 200 OK/m-new",
			r.LastSuccessStatus, r.LastSuccessDstModelName)
	}
}
