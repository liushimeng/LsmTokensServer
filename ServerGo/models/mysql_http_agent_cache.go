package models

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"sync"
	"time"
)

// CachedAIRoute 内存缓存中的路由对象，预解析 DstEndPointIDList 为 []uint64
type CachedAIRoute struct {
	TAgentHttpAIRoute                  // 嵌入原结构
	DstEndPointIDs            []uint64 // 预解析的 ID 列表（内存缓存专用）
	DstEndPointIDStatuses     []int    // 预解析的可用状态列表（与 DstEndPointIDs 一一对应:1=启用,0=禁用）
	DstEndPointAlgorithmTypes []int    // 预解析的源站协议处理算法列表（与 DstEndPointIDs 一一对应）
}

// SelectedDstEndPointID 根据策略返回选中的目标源站 ID（策略1 = 第一个）
func (r *CachedAIRoute) SelectedDstEndPointID() (uint64, error) {
	if r == nil {
		return 0, fmt.Errorf("route is nil")
	}
	if len(r.DstEndPointIDs) > 0 {
		switch r.AlgorithmStrategyType {
		case AlgorithmStrategyType_FirstID:
			return r.DstEndPointIDs[0], nil
		default:
			return r.DstEndPointIDs[0], nil
		}
	}
	return 0, fmt.Errorf("no destination endpoint configured for route id=%d", r.ID)
}

// AlgorithmTypeForEndPointID 返回指定源站对应的协议处理算法类型
func (r *CachedAIRoute) AlgorithmTypeForEndPointID(endpointID uint64) int {
	if r == nil {
		return DstEndPointAlgorithmType_Direct
	}
	for i, id := range r.DstEndPointIDs {
		if id == endpointID && i < len(r.DstEndPointAlgorithmTypes) {
			algorithmType := r.DstEndPointAlgorithmTypes[i]
			if algorithmType == DstEndPointAlgorithmType_ProtocolConverter {
				return algorithmType
			}
			return DstEndPointAlgorithmType_Direct
		}
	}
	return DstEndPointAlgorithmType_Direct
}

func buildCachedAIRoute(r *TAgentHttpAIRoute) *CachedAIRoute {
	cached := &CachedAIRoute{TAgentHttpAIRoute: *r}
	if r.DstEndPointIDList != "" {
		cached.DstEndPointIDs, _ = ParseDstEndPointIDList(r.DstEndPointIDList)
	}
	if normalized, algorithmTypes, err := NormalizeDstEndPointAlgorithmTypeList(r.DstEndPointIDList, r.DstEndPointAlgorithmTypeList); err == nil {
		cached.DstEndPointAlgorithmTypeList = normalized
		cached.DstEndPointAlgorithmTypes = algorithmTypes
	}
	// 预解析状态列表，空时生成全1默认值
	if normalizedStatus, statuses, err := NormalizeDstEndPointIDStatusList(r.DstEndPointIDList, r.DstEndPointIDStatusList); err == nil {
		cached.DstEndPointIDStatusList = normalizedStatus
		cached.DstEndPointIDStatuses = statuses
	}
	return cached
}

// ChatHistoryItem 对话历史单条记录（服务器内存缓存）
type ChatHistoryItem struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// EndpointChatSession 单个源站的对话会话（服务器内存缓存）
type EndpointChatSession struct {
	SystemPrompt string            `json:"system_prompt"`
	Messages     []ChatHistoryItem `json:"messages"`
	UpdatedAt    int64             `json:"updated_at"`
}

// AgentCache 内存缓存，用于代理服务高并发场景下的快速查询
type AgentCache struct {
	users             map[uint64]*TAgentHttpUserInfo      // userID -> user
	usersByName       map[string]*TAgentHttpUserInfo      // userName -> user
	models            map[uint64]*TAgentHttpUserModelInfo // modelID -> model
	modelsByKey       map[string]*TAgentHttpUserModelInfo // apiKey -> model (proxy hot path)
	modelsByUserModel map[string]*TAgentHttpUserModelInfo // "userName:modelName" -> model (analysis hot path)
	routes            map[uint64][]*CachedAIRoute         // modelID -> cached routes[]
	endpoints         map[uint64]*TAgentDstEndPoint       // endpointID -> endpoint (proxy hot path)
	modelInfos        map[string]*TAgentModelInfo         // modelName -> model info (proxy hot path)
	modelInfosByID    map[uint64]*TAgentModelInfo         // id -> model info
	chatSessions      map[uint64]*EndpointChatSession     // endpointID -> 对话会话（仅内存，服务重启后重置）

	// Agent 工具信息缓存
	agentInfos     map[string]*TAgentHttpAgentInfo // agentToolName -> info
	agentToolNames []string                        // 缓存的 Agent 名称列表

	// 爬虫数据源缓存
	spiderDataSources      map[uint64]*TSpiderDataSource // id -> data source
	spiderDataSourcesList  []*TSpiderDataSource          // 完整列表
	spiderDataSourcesIndex map[uint64]int                // id -> index in list (for O(1) lookup)

	mu sync.RWMutex
}

var agentCache AgentCache

// LoadAgentCacheFromDB 从数据库加载所有数据到内存缓存
func LoadAgentCacheFromDB() error {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// 加载用户
	var users []TAgentHttpUserInfo
	if err := database.DB.Table(AgentHttpUserInfoTableName).
		Where("deleted_at IS NULL").
		Find(&users).Error; err != nil {
		return fmt.Errorf("failed to load users into cache: %w", err)
	}
	agentCache.users = make(map[uint64]*TAgentHttpUserInfo, len(users))
	agentCache.usersByName = make(map[string]*TAgentHttpUserInfo, len(users))
	for i := range users {
		u := &users[i]
		agentCache.users[u.ID] = u
		agentCache.usersByName[u.UserName] = u
	}

	// 加载模型
	var models []TAgentHttpUserModelInfo
	if err := database.DB.Table(AgentHttpUserModelInfoTableName).
		Where("deleted_at IS NULL").
		Find(&models).Error; err != nil {
		return fmt.Errorf("failed to load models into cache: %w", err)
	}
	agentCache.models = make(map[uint64]*TAgentHttpUserModelInfo, len(models))
	agentCache.modelsByKey = make(map[string]*TAgentHttpUserModelInfo, len(models))
	agentCache.modelsByUserModel = make(map[string]*TAgentHttpUserModelInfo, len(models))
	for i := range models {
		m := &models[i]
		agentCache.models[m.ID] = m
		if m.APIKey != "" {
			agentCache.modelsByKey[m.APIKey] = m
		}
		// 同时按 userName + modelName 建立索引（用于分析页面快速路由）
		if user, ok := agentCache.users[m.UserID]; ok {
			key := user.UserName + ":" + m.ModelName
			agentCache.modelsByUserModel[key] = m
		}
	}

	// 加载路由
	var routes []TAgentHttpAIRoute
	if err := database.DB.Table(AgentHttpAIRouteTableName).
		Where("deleted_at IS NULL").
		Find(&routes).Error; err != nil {
		return fmt.Errorf("failed to load routes into cache: %w", err)
	}
	agentCache.routes = make(map[uint64][]*CachedAIRoute)
	for i := range routes {
		r := &routes[i]
		cached := buildCachedAIRoute(r)
		agentCache.routes[r.UserModelID] = append(agentCache.routes[r.UserModelID], cached)
	}

	// 加载源站接入点
	var endpoints []TAgentDstEndPoint
	if err := database.DB.Table(AgentDstEndPointTableName).
		Where("deleted_at IS NULL").
		Find(&endpoints).Error; err != nil {
		return fmt.Errorf("failed to load dst endpoints into cache: %w", err)
	}
	agentCache.endpoints = make(map[uint64]*TAgentDstEndPoint, len(endpoints))
	for i := range endpoints {
		e := &endpoints[i]
		agentCache.endpoints[e.ID] = e
	}

	// 初始化对话会话缓存（仅内存，服务重启后重置）
	agentCache.chatSessions = make(map[uint64]*EndpointChatSession)

	// 加载模型信息
	var modelInfos []TAgentModelInfo
	if err := database.DB.Table(AgentModelInfoTableName).
		Where("deleted_at IS NULL").
		Find(&modelInfos).Error; err != nil {
		logger.Printf("[WARNING] Failed to load model infos into cache: %v", err)
	} else {
		agentCache.modelInfos = make(map[string]*TAgentModelInfo, len(modelInfos))
		agentCache.modelInfosByID = make(map[uint64]*TAgentModelInfo, len(modelInfos))
		for i := range modelInfos {
			mi := &modelInfos[i]
			agentCache.modelInfos[mi.ModelName] = mi
			agentCache.modelInfosByID[mi.ID] = mi
		}
	}

	// 加载 Agent 工具信息
	agentCache.agentInfos = make(map[string]*TAgentHttpAgentInfo)
	agentCache.agentToolNames = make([]string, 0)
	var agentInfos []TAgentHttpAgentInfo
	if err := database.DB.Table(AgentHttpAgentInfoTableName).
		Where("deleted_at IS NULL").
		Order("usage_count DESC").
		Find(&agentInfos).Error; err != nil {
		logger.Printf("[WARNING] Failed to load agent infos into cache: %v", err)
	} else {
		for i := range agentInfos {
			ai := &agentInfos[i]
			agentCache.agentInfos[ai.AgentToolName] = ai
			agentCache.agentToolNames = append(agentCache.agentToolNames, ai.AgentToolName)
		}
	}

	// 加载爬虫数据源
	agentCache.spiderDataSources = make(map[uint64]*TSpiderDataSource)
	agentCache.spiderDataSourcesList = make([]*TSpiderDataSource, 0)
	agentCache.spiderDataSourcesIndex = make(map[uint64]int)
	var spiderDss []TSpiderDataSource
	if err := database.DB.Find(&spiderDss).Error; err != nil {
		logger.Printf("[WARNING] Failed to load spider data sources into cache: %v", err)
	} else {
		for i := range spiderDss {
			ds := &spiderDss[i]
			agentCache.spiderDataSources[ds.ID] = ds
			agentCache.spiderDataSourcesList = append(agentCache.spiderDataSourcesList, ds)
			agentCache.spiderDataSourcesIndex[ds.ID] = i
		}
	}

	logger.Printf("[CACHE] Loaded %d users, %d models, %d routes, %d endpoints, %d model infos, %d agent infos, %d spider sources into memory",
		len(agentCache.users), len(agentCache.models), len(routes), len(endpoints), len(modelInfos), len(agentInfos), len(agentCache.spiderDataSourcesList))
	return nil
}

// RefreshSpiderDataSourceCache 刷新爬虫数据源缓存
func RefreshSpiderDataSourceCache() error {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	var spiderDss []TSpiderDataSource
	if err := database.DB.Find(&spiderDss).Error; err != nil {
		return fmt.Errorf("failed to load spider data sources: %w", err)
	}

	agentCache.spiderDataSources = make(map[uint64]*TSpiderDataSource)
	agentCache.spiderDataSourcesList = make([]*TSpiderDataSource, 0, len(spiderDss))
	agentCache.spiderDataSourcesIndex = make(map[uint64]int, len(spiderDss))
	for i := range spiderDss {
		ds := &spiderDss[i]
		agentCache.spiderDataSources[ds.ID] = ds
		agentCache.spiderDataSourcesList = append(agentCache.spiderDataSourcesList, ds)
		agentCache.spiderDataSourcesIndex[ds.ID] = i
	}

	return nil
}

// AddSpiderDataSourceToCache 增量添加数据源到缓存
func AddSpiderDataSourceToCache(ds *TSpiderDataSource) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	// 先制作一个副本避免外部修改影响缓存
	cachedDs := *ds
	agentCache.spiderDataSources[ds.ID] = &cachedDs
	agentCache.spiderDataSourcesIndex[ds.ID] = len(agentCache.spiderDataSourcesList)
	agentCache.spiderDataSourcesList = append(agentCache.spiderDataSourcesList, &cachedDs)
}

// UpdateSpiderDataSourceInCache 增量更新缓存中的数据源
func UpdateSpiderDataSourceInCache(ds *TSpiderDataSource) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	if existing, ok := agentCache.spiderDataSources[ds.ID]; ok {
		// 直接更新现有对象的字段（保留原指针）
		existing.PlatformName = ds.PlatformName
		existing.URLAddress = ds.URLAddress
		existing.Description = ds.Description
		existing.Remark = ds.Remark
		existing.Status = ds.Status
		existing.UpdatedAt = ds.UpdatedAt
	} else {
		// 不存在则新增
		cachedDs := *ds
		agentCache.spiderDataSources[ds.ID] = &cachedDs
		agentCache.spiderDataSourcesIndex[ds.ID] = len(agentCache.spiderDataSourcesList)
		agentCache.spiderDataSourcesList = append(agentCache.spiderDataSourcesList, &cachedDs)
	}
}

// RemoveSpiderDataSourceFromCache 从缓存中删除数据源
func RemoveSpiderDataSourceFromCache(id uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	delete(agentCache.spiderDataSources, id)

	// 使用索引快速定位并删除
	if idx, ok := agentCache.spiderDataSourcesIndex[id]; ok {
		// 从 list 中移除：用最后一个元素替换被删除的位置，然后截断
		lastIdx := len(agentCache.spiderDataSourcesList) - 1
		if idx < lastIdx {
			lastDs := agentCache.spiderDataSourcesList[lastIdx]
			agentCache.spiderDataSourcesList[idx] = lastDs
			agentCache.spiderDataSourcesIndex[lastDs.ID] = idx
		}
		agentCache.spiderDataSourcesList = agentCache.spiderDataSourcesList[:lastIdx]
		delete(agentCache.spiderDataSourcesIndex, id)
	} else {
		// 索引不存在时退化为线性查找（安全网）
		newList := make([]*TSpiderDataSource, 0, len(agentCache.spiderDataSourcesList))
		for _, ds := range agentCache.spiderDataSourcesList {
			if ds.ID != id {
				newList = append(newList, ds)
			}
		}
		agentCache.spiderDataSourcesList = newList
		// 重建索引
		agentCache.spiderDataSourcesIndex = make(map[uint64]int, len(newList))
		for i, ds := range newList {
			agentCache.spiderDataSourcesIndex[ds.ID] = i
		}
	}
}

// UpdateCachedSpiderDataSourceStatus 只更新缓存中的状态字段
func UpdateCachedSpiderDataSourceStatus(id uint64, status int) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	if ds, ok := agentCache.spiderDataSources[id]; ok {
		ds.Status = status
		// map 和 list 指向同一对象，无需额外更新 list
	}
}

// GetCachedSpiderDataSourceByID 根据ID从缓存获取爬虫数据源
func GetCachedSpiderDataSourceByID(id uint64) (*TSpiderDataSource, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	ds, ok := agentCache.spiderDataSources[id]
	return ds, ok
}

// GetCachedSpiderDataSourcesList 从缓存获取所有爬虫数据源列表
func GetCachedSpiderDataSourcesList() []*TSpiderDataSource {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	return agentCache.spiderDataSourcesList
}

// GetCachedUserByID 根据用户 ID 从缓存查询用户
func GetCachedUserByID(id uint64) (*TAgentHttpUserInfo, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	u, ok := agentCache.users[id]
	if !ok {
		return nil, false
	}
	return u, true
}

// GetCachedUserByName 根据用户名从缓存查询用户
func GetCachedUserByName(name string) (*TAgentHttpUserInfo, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	u, ok := agentCache.usersByName[name]
	if !ok {
		return nil, false
	}
	return u, true
}

// GetCachedModelByID 根据模型 ID 从缓存查询模型
func GetCachedModelByID(id uint64) (*TAgentHttpUserModelInfo, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	m, ok := agentCache.models[id]
	if !ok {
		return nil, false
	}
	return m, true
}

// GetCachedModelByAPIKey 根据 API Key 从缓存查询模型（代理热路径）
func GetCachedModelByAPIKey(apiKey string) (*TAgentHttpUserModelInfo, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	m, ok := agentCache.modelsByKey[apiKey]
	if !ok {
		return nil, false
	}
	return m, true
}

// GetCachedModelByUserAndModelName 根据用户名和模型名称从缓存查询模型（分析热路径）
func GetCachedModelByUserAndModelName(userName, modelName string) (*TAgentHttpUserModelInfo, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	key := userName + ":" + modelName
	m, ok := agentCache.modelsByUserModel[key]
	if !ok {
		return nil, false
	}
	return m, true
}

// GetCachedRoutesByModelID 根据模型 ID 从缓存查询所有路由
func GetCachedRoutesByModelID(modelID uint64) ([]*CachedAIRoute, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	routes, ok := agentCache.routes[modelID]
	if !ok || len(routes) == 0 {
		return nil, false
	}
	return routes, true
}

// GetCachedRouteByModelIDAndProtocol 根据模型 ID 和协议类型从缓存查询路由
func GetCachedRouteByModelIDAndProtocol(modelID uint64, protocolType int) (*CachedAIRoute, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	routes, ok := agentCache.routes[modelID]
	if !ok {
		return nil, false
	}
	for _, r := range routes {
		if r.ProtocolType == protocolType {
			return r, true
		}
	}
	return nil, false
}

// invalidateUserCache 从缓存中移除指定用户及其在 modelsByUserModel 中的索引
func invalidateUserCache(id uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if u, ok := agentCache.users[id]; ok {
		delete(agentCache.usersByName, u.UserName)
		delete(agentCache.users, id)
	}
	// 同时清理 modelsByUserModel 中该用户的条目（用户名变更后 key 会失效）
	for key, m := range agentCache.modelsByUserModel {
		if m.UserID == id {
			delete(agentCache.modelsByUserModel, key)
		}
	}
}

// invalidateModelCache 从缓存中移除指定模型
func invalidateModelCache(id uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if m, ok := agentCache.models[id]; ok {
		delete(agentCache.modelsByKey, m.APIKey)
		if user, uok := agentCache.users[m.UserID]; uok {
			delete(agentCache.modelsByUserModel, user.UserName+":"+m.ModelName)
		}
		delete(agentCache.models, id)
		delete(agentCache.routes, id)
	}
}

// UpdateCachedUserModelStatus 只更新缓存中的模型状态字段
func UpdateCachedUserModelStatus(id uint64, status int) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()

	if m, ok := agentCache.models[id]; ok {
		m.Status = status
	}
}

// GetCachedDstEndPointByID 根据源站 ID 从缓存查询源站接入点（代理热路径）
func GetCachedDstEndPointByID(id uint64) (*TAgentDstEndPoint, bool) {
	agentCache.mu.RLock()
	defer agentCache.mu.RUnlock()
	ep, ok := agentCache.endpoints[id]
	if !ok {
		return nil, false
	}
	return ep, true
}

// addDstEndPointToCache 将源站接入点添加到缓存
func addDstEndPointToCache(e *TAgentDstEndPoint) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	agentCache.endpoints[e.ID] = e
}

// invalidateDstEndPointCache 从缓存中移除指定源站接入点
func invalidateDstEndPointCache(id uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	delete(agentCache.endpoints, id)
}

// addUserToCache 将用户添加到缓存
func addUserToCache(u *TAgentHttpUserInfo) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	agentCache.users[u.ID] = u
	agentCache.usersByName[u.UserName] = u
}

// addModelToCache 将模型添加到缓存
func addModelToCache(m *TAgentHttpUserModelInfo) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	agentCache.models[m.ID] = m
	if m.APIKey != "" {
		agentCache.modelsByKey[m.APIKey] = m
	}
	if user, ok := agentCache.users[m.UserID]; ok {
		agentCache.modelsByUserModel[user.UserName+":"+m.ModelName] = m
	}
}

// addRouteToCache 将路由添加到缓存
func addRouteToCache(r *TAgentHttpAIRoute) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	cached := buildCachedAIRoute(r)
	agentCache.routes[r.UserModelID] = append(agentCache.routes[r.UserModelID], cached)
}

// updateRouteInCache 更新缓存中指定模型的指定路由
// 如果路由的 UserModelID 发生变化，会自动从旧模型桶移除并添加到新模型桶。
//
// 稳定型算法的 "当前生效源站" 等于 DstEndPointIDList[0]，由 RotateAIRouteEndpointList
// 直接修改列表本身，不再通过内存里的 ActiveIndex 间接维护。
func updateRouteInCache(r *TAgentHttpAIRoute) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	cached := buildCachedAIRoute(r)

	// 先检查是否需要跨模型移动（UserModelID 变更）
	found := false
	for modelID, routes := range agentCache.routes {
		for i, existing := range routes {
			if existing.ID == r.ID {
				found = true
				if modelID != r.UserModelID {
					// 从旧模型桶移除
					agentCache.routes[modelID] = append(routes[:i], routes[i+1:]...)
					// 添加到新模型桶
					agentCache.routes[r.UserModelID] = append(agentCache.routes[r.UserModelID], cached)
				} else {
					// 同一模型桶内更新
					routes[i] = cached
				}
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		// 若未找到则追加到新模型桶
		agentCache.routes[r.UserModelID] = append(agentCache.routes[r.UserModelID], cached)
	}
}

// removeRouteFromCache 从缓存中移除指定路由（按模型ID+路由ID精确删除）
func removeRouteFromCache(modelID, routeID uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	routes := agentCache.routes[modelID]
	for i, r := range routes {
		if r.ID == routeID {
			agentCache.routes[modelID] = append(routes[:i], routes[i+1:]...)
			return
		}
	}
}

// GetOrCreateChatSession 获取或创建源站的对话会话（服务器内存缓存）
func GetOrCreateChatSession(endpointID uint64) *EndpointChatSession {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.chatSessions == nil {
		agentCache.chatSessions = make(map[uint64]*EndpointChatSession)
	}
	if session, ok := agentCache.chatSessions[endpointID]; ok {
		return session
	}
	session := &EndpointChatSession{
		Messages:  make([]ChatHistoryItem, 0),
		UpdatedAt: time.Now().Unix(),
	}
	agentCache.chatSessions[endpointID] = session
	return session
}

// UpdateChatSession 更新源站的对话会话（服务器内存缓存）
func UpdateChatSession(endpointID uint64, systemPrompt string, messages []ChatHistoryItem) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.chatSessions == nil {
		agentCache.chatSessions = make(map[uint64]*EndpointChatSession)
	}
	agentCache.chatSessions[endpointID] = &EndpointChatSession{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		UpdatedAt:    time.Now().Unix(),
	}
}

// ClearChatSession 清空源站的对话会话（服务器内存缓存）
func ClearChatSession(endpointID uint64) {
	agentCache.mu.Lock()
	defer agentCache.mu.Unlock()
	if agentCache.chatSessions != nil {
		delete(agentCache.chatSessions, endpointID)
	}
}

// generateAPIKey 生成模型 API Key。
// 使用 crypto/rand 生成高熵随机值，避免从用户名、模型名或时间戳反推出密钥。
func generateAPIKey(userName, modelName string) string {
	_ = userName
	_ = modelName
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下保留可用性；调用方仍会在数据库唯一约束下处理冲突。
		return fmt.Sprintf("sk-%d000000000000000000000000", time.Now().UnixNano())
	}
	return "sk-" + hex.EncodeToString(buf)
}
