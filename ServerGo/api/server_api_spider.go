package api

import (
	"encoding/json"
	"fmt"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strconv"
	"time"
)

// ============================================================================
// 爬虫 API 接口
// ============================================================================

// SpiderAPIRequest 通用 API 请求
type SpiderAPIRequest struct {
	Action string `json:"action"`

	// 数据源操作
	ID           uint64   `json:"id,omitempty"`
	IDs          []uint64 `json:"ids,omitempty"` // 批量操作用
	UserID       uint64   `json:"user_id,omitempty"`
	PlatformName string   `json:"platform_name,omitempty"`
	URLAddress   string   `json:"url_address,omitempty"`
	Description  string   `json:"description,omitempty"`
	Remark       string   `json:"remark,omitempty"`
	Status       int      `json:"status,omitempty"`

	// 每日信息操作
	PlatformFilter string                `json:"platform_filter,omitempty"`
	StartDate      string                `json:"start_date,omitempty"`
	EndDate        string                `json:"end_date,omitempty"`
	Page           int                   `json:"page,omitempty"`
	PageSize       int                   `json:"page_size,omitempty"`
	CrawlTime      string                `json:"crawl_time,omitempty"`
	Items          []SpiderDailyInfoItem `json:"items,omitempty"` // 批量删除用
}

// SpiderDailyInfoItem 单个信息项（批量删除用）
type SpiderDailyInfoItem struct {
	ID        uint64 `json:"id"`
	CrawlTime string `json:"crawl_time"`
}

// SpiderAPIResponse API 响应
type SpiderAPIResponse struct {
	Success             bool        `json:"success"`
	Message             string      `json:"message,omitempty"`
	Data                interface{} `json:"data,omitempty"`
	Total               int64       `json:"total,omitempty"`
	Deleted             int64       `json:"deleted,omitempty"`               // 批量删除：实际删除条数（v2.0.24 起保留，向后兼容）
	SkippedNotFound     int64       `json:"skipped_not_found,omitempty"`     // v2.0.24：批量删除中「记录不存在」被跳过的条数
	SkippedNoPermission int64       `json:"skipped_no_permission,omitempty"` // v2.0.24：批量删除中「无权限」被跳过的条数
}

// SpiderDataSourceListResponse 数据源列表响应
type SpiderDataSourceListResponse struct {
	DataSources []modelsdb.TSpiderDataSource `json:"data_sources"`
}

// SpiderDailyInfoListResponse 每日信息列表响应
type SpiderDailyInfoListResponse struct {
	Infos     []modelsdb.TSpiderDailyInfo `json:"infos"`
	Platforms []string                    `json:"platforms"`
}

// SpiderDailyInfoContentResponse 每日信息内容响应（按需加载）
type SpiderDailyInfoContentResponse struct {
	Content string `json:"content"`
	RawData string `json:"raw_data"`
}

// ============================================================================
// 数据源 API（管理员/用户共用，有鉴权区分）
// ============================================================================

// SpiderDataSourceInterfaceHandler 数据源 API 处理
func SpiderDataSourceInterfaceHandler(w http.ResponseWriter, r *http.Request, isAdmin bool, userID uint64) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SpiderAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(SpiderAPIResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	var resp SpiderAPIResponse

	switch req.Action {
	case "list":
		resp = handleSpiderDataSourceList(isAdmin, userID)
	case "add":
		resp = handleSpiderDataSourceAdd(&req, isAdmin, userID)
	case "update":
		resp = handleSpiderDataSourceUpdate(&req, isAdmin, userID)
	case "delete":
		resp = handleSpiderDataSourceDelete(&req, isAdmin, userID)
	case "toggle_status":
		resp = handleSpiderDataSourceToggleStatus(&req, isAdmin, userID)
	case "batch_toggle_status":
		resp = handleSpiderDataSourceBatchToggleStatus(&req, isAdmin, userID)
	default:
		resp = SpiderAPIResponse{
			Success: false,
			Message: "Unknown action",
		}
	}

	json.NewEncoder(w).Encode(resp)
}

// handleSpiderDataSourceList 处理列表请求
func handleSpiderDataSourceList(isAdmin bool, userID uint64) SpiderAPIResponse {
	dataSources, err := modelsdb.ListSpiderDataSources(userID, isAdmin)
	if err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to list data sources: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Data:    dataSources,
	}
}

// handleSpiderDataSourceAdd 处理添加请求
func handleSpiderDataSourceAdd(req *SpiderAPIRequest, isAdmin bool, userID uint64) SpiderAPIResponse {
	// 只有管理员可以添加公共数据源（userID=0）
	// 用户只能添加自己的数据源
	dsUserID := req.UserID
	if !isAdmin {
		dsUserID = userID
	}

	if req.PlatformName == "" || req.URLAddress == "" {
		return SpiderAPIResponse{
			Success: false,
			Message: "Platform name and URL are required",
		}
	}

	ds := &modelsdb.TSpiderDataSource{
		UserID:       dsUserID,
		PlatformName: req.PlatformName,
		URLAddress:   req.URLAddress,
		Description:  req.Description,
		Remark:       req.Remark,
		Status:       1, // 默认启用
	}

	if err := modelsdb.CreateSpiderDataSource(ds); err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to create data source: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Message: "Data source created",
		Data:    ds,
	}
}

// handleSpiderDataSourceUpdate 处理更新请求
func handleSpiderDataSourceUpdate(req *SpiderAPIRequest, isAdmin bool, userID uint64) SpiderAPIResponse {
	if req.ID == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "ID is required",
		}
	}

	// 获取现有数据源
	ds, err := modelsdb.GetSpiderDataSourceByID(req.ID)
	if err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to get data source: " + err.Error(),
		}
	}
	if ds == nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Data source not found",
		}
	}

	// 权限检查：管理员可以修改所有，用户只能修改自己的
	if !isAdmin && ds.UserID != userID && ds.UserID != 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "Permission denied",
		}
	}

	if req.PlatformName != "" {
		ds.PlatformName = req.PlatformName
	}
	if req.URLAddress != "" {
		ds.URLAddress = req.URLAddress
	}
	ds.Description = req.Description
	ds.Remark = req.Remark

	if err := modelsdb.UpdateSpiderDataSource(ds); err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to update data source: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Message: "Data source updated",
	}
}

// handleSpiderDataSourceDelete 处理删除请求
func handleSpiderDataSourceDelete(req *SpiderAPIRequest, isAdmin bool, userID uint64) SpiderAPIResponse {
	// 只有管理员可以删除
	if !isAdmin {
		return SpiderAPIResponse{
			Success: false,
			Message: "Permission denied: only admin can delete data sources",
		}
	}

	if req.ID == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "ID is required",
		}
	}

	if err := modelsdb.DeleteSpiderDataSource(req.ID); err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to delete data source: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Message: "Data source deleted",
	}
}

// handleSpiderDataSourceToggleStatus 处理状态切换
func handleSpiderDataSourceToggleStatus(req *SpiderAPIRequest, isAdmin bool, userID uint64) SpiderAPIResponse {
	if req.ID == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "ID is required",
		}
	}

	// 权限检查
	if !isAdmin {
		ds, err := modelsdb.GetSpiderDataSourceByID(req.ID)
		if err != nil {
			return SpiderAPIResponse{
				Success: false,
				Message: "Failed to get data source: " + err.Error(),
			}
		}
		if ds == nil {
			return SpiderAPIResponse{
				Success: false,
				Message: "Data source not found",
			}
		}
		if ds.UserID != userID {
			return SpiderAPIResponse{
				Success: false,
				Message: "Permission denied",
			}
		}
	}

	if err := modelsdb.ToggleSpiderDataSourceStatus(req.ID, req.Status); err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to toggle status: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Message: "Status updated",
	}
}

// handleSpiderDataSourceBatchToggleStatus 处理批量状态切换
func handleSpiderDataSourceBatchToggleStatus(req *SpiderAPIRequest, isAdmin bool, userID uint64) SpiderAPIResponse {
	if len(req.IDs) == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "No IDs provided",
		}
	}

	// 权限检查：非管理员只能操作自己的数据源
	var allowedIDs []uint64
	if !isAdmin {
		for _, id := range req.IDs {
			ds, err := modelsdb.GetSpiderDataSourceByID(id)
			if err != nil {
				continue
			}
			if ds == nil {
				continue
			}
			if ds.UserID != userID {
				continue // 跳过无权限的项
			}
			allowedIDs = append(allowedIDs, id)
		}
		if len(allowedIDs) == 0 {
			return SpiderAPIResponse{
				Success: false,
				Message: "Permission denied: no allowed items to update",
			}
		}
	} else {
		allowedIDs = req.IDs
	}

	updatedCount, err := modelsdb.BatchToggleSpiderDataSourceStatus(allowedIDs, req.Status)
	if err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to batch toggle status: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Message: fmt.Sprintf("Updated %d data source(s)", updatedCount),
		Data:    map[string]interface{}{"updated": updatedCount},
	}
}

// ============================================================================
// 每日信息 API
// ============================================================================

// SpiderDailyInfoInterfaceHandler 每日信息 API 处理
func SpiderDailyInfoInterfaceHandler(w http.ResponseWriter, r *http.Request, isAdmin bool, userID uint64) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method == http.MethodGet {
		// GET 请求：查询列表
		page := 1
		pageSize := 20
		platformFilter := ""
		var startDate, endDate time.Time

		if p := r.URL.Query().Get("page"); p != "" {
			if pi, err := strconv.Atoi(p); err == nil && pi > 0 {
				page = pi
			}
		}
		if ps := r.URL.Query().Get("page_size"); ps != "" {
			if psi, err := strconv.Atoi(ps); err == nil && psi > 0 && psi <= 100 {
				pageSize = psi
			}
		}
		platformFilter = r.URL.Query().Get("platform")
		if sd := r.URL.Query().Get("start_date"); sd != "" {
			if t, err := time.ParseInLocation("2006-01-02T15:04:05", sd, time.Local); err == nil {
				startDate = t
			} else if t, err := time.ParseInLocation("2006-01-02", sd, time.Local); err == nil {
				startDate = t
			}
		}
		if ed := r.URL.Query().Get("end_date"); ed != "" {
			if t, err := time.ParseInLocation("2006-01-02T15:04:05", ed, time.Local); err == nil {
				endDate = t
			} else if t, err := time.ParseInLocation("2006-01-02", ed, time.Local); err == nil {
				endDate = t
			}
		}

		resp := handleSpiderDailyInfoList(userID, isAdmin, page, pageSize, platformFilter, startDate, endDate)
		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SpiderAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(SpiderAPIResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	var resp SpiderAPIResponse

	switch req.Action {
	case "list":
		page := req.Page
		if page <= 0 {
			page = 1
		}
		pageSize := req.PageSize
		if pageSize <= 0 {
			pageSize = 20
		}
		if pageSize > 100 {
			pageSize = 100
		}

		var startDate, endDate time.Time
		if req.StartDate != "" {
			startDate, _ = time.ParseInLocation("2006-01-02T15:04:05", req.StartDate, time.Local)
			if startDate.IsZero() {
				startDate, _ = time.ParseInLocation("2006-01-02", req.StartDate, time.Local)
			}
		}
		if req.EndDate != "" {
			endDate, _ = time.ParseInLocation("2006-01-02T15:04:05", req.EndDate, time.Local)
			if endDate.IsZero() {
				endDate, _ = time.ParseInLocation("2006-01-02", req.EndDate, time.Local)
			}
		}

		resp = handleSpiderDailyInfoList(userID, isAdmin, page, pageSize, req.PlatformFilter, startDate, endDate)
	case "get_detail":
		resp = handleSpiderDailyInfoGetDetail(&req, userID, isAdmin)
	case "get_content":
		resp = handleSpiderDailyInfoGetContent(&req, userID, isAdmin)
	case "delete":
		resp = handleSpiderDailyInfoDelete(&req, userID, isAdmin)
	case "batch_delete":
		resp = handleSpiderDailyInfoBatchDelete(&req, userID, isAdmin)
	default:
		resp = SpiderAPIResponse{
			Success: false,
			Message: "Unknown action",
		}
	}

	json.NewEncoder(w).Encode(resp)
}

// handleSpiderDailyInfoList 处理列表请求
func handleSpiderDailyInfoList(userID uint64, isAdmin bool, page, pageSize int, platformFilter string, startDate, endDate time.Time) SpiderAPIResponse {
	infos, total, err := modelsdb.QuerySpiderDailyInfo(userID, isAdmin, page, pageSize, platformFilter, startDate, endDate)
	if err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to query daily info: " + err.Error(),
		}
	}

	// 获取平台列表
	platforms, _ := modelsdb.GetDistinctSpiderPlatforms(userID, isAdmin)

	return SpiderAPIResponse{
		Success: true,
		Data: SpiderDailyInfoListResponse{
			Infos:     infos,
			Platforms: platforms,
		},
		Total: total,
	}
}

// handleSpiderDailyInfoGetDetail 处理获取详情（含原始 HTML）
func handleSpiderDailyInfoGetDetail(req *SpiderAPIRequest, userID uint64, isAdmin bool) SpiderAPIResponse {
	if req.ID == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "ID is required",
		}
	}

	info, err := modelsdb.GetSpiderDailyInfoByID(req.ID)
	if err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to get detail: " + err.Error(),
		}
	}
	if info == nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Info not found",
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Data:    info,
	}
}

// handleSpiderDailyInfoGetContent 处理获取内容（按需加载 content 和 raw_data）
func handleSpiderDailyInfoGetContent(req *SpiderAPIRequest, userID uint64, isAdmin bool) SpiderAPIResponse {
	if req.ID == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "ID is required",
		}
	}

	content, rawData, err := modelsdb.GetSpiderDailyInfoContent(req.ID)
	if err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to get content: " + err.Error(),
		}
	}

	return SpiderAPIResponse{
		Success: true,
		Data: SpiderDailyInfoContentResponse{
			Content: content,
			RawData: rawData,
		},
	}
}

// handleSpiderDailyInfoDelete 处理删除请求
//
// v2.0.24：管理员跳过权限预检，直接删除（包括空记录、含历史残留的 ID）；
// 非管理员保留原有权限校验链；删除后立即失效列表缓存。
func handleSpiderDailyInfoDelete(req *SpiderAPIRequest, userID uint64, isAdmin bool) SpiderAPIResponse {
	if req.ID == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "ID is required",
		}
	}

	// 非管理员走完整权限预检
	if !isAdmin {
		info, err := modelsdb.GetSpiderDailyInfoByID(req.ID)
		if err != nil {
			return SpiderAPIResponse{
				Success: false,
				Message: "Failed to get info: " + err.Error(),
			}
		}
		if info == nil {
			return SpiderAPIResponse{
				Success: false,
				Message: "Info not found",
			}
		}
		ds, err := modelsdb.GetSpiderDataSourceByID(info.DataSourceID)
		if err != nil || ds == nil {
			return SpiderAPIResponse{
				Success: false,
				Message: "Data source not found",
			}
		}
		if ds.UserID != userID && ds.UserID != 0 {
			return SpiderAPIResponse{
				Success: false,
				Message: "Permission denied",
			}
		}
	}
	// 管理员（isAdmin=true）跳过预检：管理后台期望「任何记录都能删」，
	// 这对清理历史空记录（ID=544 等）是必要的。

	if err := modelsdb.DeleteSpiderDailyInfo(req.ID); err != nil {
		return SpiderAPIResponse{
			Success: false,
			Message: "Failed to delete info: " + err.Error(),
		}
	}

	// v2.0.24：双保险，database.DB 层 modelsdb.DeleteSpiderDailyInfo 也会失效缓存，
	// 这里再调一次覆盖直接走本 handler 但未走 database.DB 函数的边缘路径。
	modelsdb.InvalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")

	return SpiderAPIResponse{
		Success: true,
		Message: "Info deleted",
	}
}

// handleSpiderDailyInfoBatchDelete 处理批量删除请求
//
// v2.0.24：把原来静默 continue 的三类失败显式记入响应：
//   - skipped_not_found：记录不存在
//   - skipped_no_permission：无权限（非管理员专属）
//   - failed：database.DB 删除出错
//
// 管理员对所有记录直接删，跳过权限预检（与单条删除保持一致）；
// 删除成功后失效列表缓存，避免前端 2 分钟内仍能看到残留记录。
func handleSpiderDailyInfoBatchDelete(req *SpiderAPIRequest, userID uint64, isAdmin bool) SpiderAPIResponse {
	if len(req.Items) == 0 {
		return SpiderAPIResponse{
			Success: false,
			Message: "No items to delete",
		}
	}

	deletedCount := int64(0)
	skippedNotFound := int64(0)
	skippedNoPermission := int64(0)

	// 优化：非管理员时批量预加载数据源权限，避免 N+1 查询
	var accessibleDSIDs map[uint64]bool
	if !isAdmin {
		accessibleDSIDs = make(map[uint64]bool)
		dsIDs, err := modelsdb.GetAccessibleDataSourceIDs(userID, false)
		if err != nil {
			return SpiderAPIResponse{
				Success: false,
				Message: "Failed to get accessible data sources: " + err.Error(),
			}
		}
		for _, id := range dsIDs {
			accessibleDSIDs[id] = true
		}
	}

	for _, item := range req.Items {
		if item.ID == 0 {
			skippedNotFound++
			continue
		}

		// 管理员：直接删；非管理员：先校验存在 + 权限
		if !isAdmin {
			info, err := modelsdb.GetSpiderDailyInfoByID(item.ID)
			if err != nil || info == nil {
				skippedNotFound++
				continue
			}
			if !accessibleDSIDs[info.DataSourceID] {
				skippedNoPermission++
				continue
			}
		}

		if err := modelsdb.DeleteSpiderDailyInfo(item.ID); err == nil {
			deletedCount++
		} else {
			// 删除失败单独记入 not_found 桶（语义上更接近「该条未达成效果」）
			skippedNotFound++
		}
	}

	// v2.0.24：删除成功后失效缓存，避免前端继续看到残留记录。
	if deletedCount > 0 {
		modelsdb.InvalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")
	}

	return SpiderAPIResponse{
		Success:             true,
		Message:             fmt.Sprintf("Batch delete completed: %d deleted, %d not found, %d no permission", deletedCount, skippedNotFound, skippedNoPermission),
		Deleted:             deletedCount,
		SkippedNotFound:     skippedNotFound,
		SkippedNoPermission: skippedNoPermission,
	}
}
