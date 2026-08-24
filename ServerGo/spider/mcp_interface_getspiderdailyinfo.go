package spider

// ==================== MCP /GetSpiderDailyInfo 接口 ====================
//
// 用途：查询已通过 /InputSpiderDailyInfo 保存的爬取数据。
//
// 数据流：HTTP POST/GET → MCPGetSpiderDailyInfoHandler → models.QuerySpiderDailyInfoForMCP (mysql_spider_model.go)
//
// 支持三种查询模式：
//   1. 单条查询：传 id，返回单条记录
//   2. 批量查询：传 ids（数组，最多 100 条），返回多条记录
//   3. 分页查询：传 page + page_size + 可选过滤条件，返回分页结果
//
// 默认值规则：0 / 空字符串 / nil 均不参与过滤。

import (
	"encoding/json"
	"fmt"
	models "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GetSpiderDailyInfoRequest 查询请求
type GetSpiderDailyInfoRequest struct {
	ID             uint64   `json:"id,omitempty"`
	IDs            []uint64 `json:"ids,omitempty"`
	DataSourceID   uint64   `json:"data_source_id,omitempty"`
	PlatformName   string   `json:"platform_name,omitempty"`
	Title          string   `json:"title,omitempty"`
	URL            string   `json:"url,omitempty"`
	CrawlTimeStart string   `json:"crawl_time_start,omitempty"`
	CrawlTimeEnd   string   `json:"crawl_time_end,omitempty"`
	IncludeRawData bool     `json:"include_raw_data,omitempty"`
	Page           int      `json:"page,omitempty"`
	PageSize       int      `json:"page_size,omitempty"`
}

// GetSpiderDailyInfoResponseItem 响应单条记录
type GetSpiderDailyInfoResponseItem struct {
	ID           uint64 `json:"id"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	DataSourceID uint64 `json:"data_source_id"`
	PlatformName string `json:"platform_name"`
	Title        string `json:"title"`
	TitleZh      string `json:"title_zh,omitempty"`
	Content      string `json:"content"`
	ContentZh    string `json:"content_zh,omitempty"`
	RawData      string `json:"raw_data,omitempty"`
	CrawlTime    string `json:"crawl_time"`
	URL          string `json:"url"`
	TranslatedAt string `json:"translated_at,omitempty"`
}

// GetSpiderDailyInfoData 响应分页数据
type GetSpiderDailyInfoData struct {
	Items      []GetSpiderDailyInfoResponseItem `json:"items"`
	TotalCount int64                            `json:"total_count"`
	Page       int                              `json:"page"`
	PageSize   int                              `json:"page_size"`
}

// MCPGetSpiderDailyInfoHandler /GetSpiderDailyInfo 接口处理
func MCPGetSpiderDailyInfoHandler(w http.ResponseWriter, r *http.Request) {
	// v2.0.47：handler 入口生成 RequestID，统一关联日志
	reqID, startTime := mcpLogMCPRequestStart("/GetSpiderDailyInfo", r.RemoteAddr)
	defer func() {
		mcpLogMCPRequestEnd(reqID, startTime, 200, true, "")
	}()
	w.Header().Set("Content-Type", "application/json")
	mcpSetNoCacheHeaders(w)

	mcpLogMCP("Received request for /GetSpiderDailyInfo from %s", r.RemoteAddr)
	mcpLogMCPWithTag(reqID, "request start method=%s", r.Method)

	var req GetSpiderDailyInfoRequest

	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mcpLogMCP("Failed to decode POST body: %v", err)
			json.NewEncoder(w).Encode(MCPAPIResponse{
				Success: false,
				Message: fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
	} else if r.Method == http.MethodGet {
		req = parseGetSpiderDailyInfoFromQuery(r)
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "Method not allowed, use POST or GET",
		})
		return
	}

	// 转换为 database.DB 层请求
	dbReq := models.MCPGetSpiderDailyInfoRequest{
		ID:             req.ID,
		IDs:            req.IDs,
		DataSourceID:   req.DataSourceID,
		PlatformName:   req.PlatformName,
		Title:          req.Title,
		URL:            req.URL,
		CrawlTimeStart: req.CrawlTimeStart,
		CrawlTimeEnd:   req.CrawlTimeEnd,
		IncludeRawData: req.IncludeRawData,
		Page:           req.Page,
		PageSize:       req.PageSize,
	}

	infos, totalCount, err := models.QuerySpiderDailyInfoForMCP(dbReq)
	if err != nil {
		mcpLogMCP("Failed to query daily info: %v", err)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to query daily info: %v", err),
		})
		return
	}

	// 构建响应
	items := make([]GetSpiderDailyInfoResponseItem, 0, len(infos))
	for _, info := range infos {
		item := GetSpiderDailyInfoResponseItem{
			ID:           info.ID,
			CreatedAt:    formatTime(info.CreatedAt),
			UpdatedAt:    formatTime(info.UpdatedAt),
			DataSourceID: info.DataSourceID,
			PlatformName: info.PlatformName,
			Title:        info.Title,
			TitleZh:      info.TitleZh,
			Content:      info.Content,
			ContentZh:    info.ContentZh,
			CrawlTime:    formatTime(info.CrawlTime),
			URL:          info.URL,
		}
		if req.IncludeRawData {
			item.RawData = info.RawData
		}
		if info.TranslatedAt != nil {
			item.TranslatedAt = formatTime(*info.TranslatedAt)
		}
		items = append(items, item)
	}

	// 单条/批量查询时 page/pageSize 保持请求值或默认
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}

	mcpLogMCP("Returned %d/%d daily info items (id=%d, ds=%d, platform=%s, title=%s)",
		len(items), totalCount, req.ID, req.DataSourceID, req.PlatformName, req.Title)

	json.NewEncoder(w).Encode(MCPAPIResponse{
		Success: true,
		Message: "Daily info retrieved",
		Data: GetSpiderDailyInfoData{
			Items:      items,
			TotalCount: totalCount,
			Page:       page,
			PageSize:   pageSize,
		},
	})
}

// parseGetSpiderDailyInfoFromQuery 从 GET 查询参数解析请求
func parseGetSpiderDailyInfoFromQuery(r *http.Request) GetSpiderDailyInfoRequest {
	var req GetSpiderDailyInfoRequest
	q := r.URL.Query()

	if idStr := q.Get("id"); idStr != "" {
		if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
			req.ID = id
		}
	}
	if idsStr := q.Get("ids"); idsStr != "" {
		for _, s := range strings.Split(idsStr, ",") {
			s = strings.TrimSpace(s)
			if id, err := strconv.ParseUint(s, 10, 64); err == nil {
				req.IDs = append(req.IDs, id)
			}
		}
	}
	if dsStr := q.Get("data_source_id"); dsStr != "" {
		if id, err := strconv.ParseUint(dsStr, 10, 64); err == nil {
			req.DataSourceID = id
		}
	}
	if v := q.Get("platform_name"); v != "" {
		req.PlatformName = v
	}
	if v := q.Get("title"); v != "" {
		req.Title = v
	}
	if v := q.Get("url"); v != "" {
		req.URL = v
	}
	if v := q.Get("crawl_time_start"); v != "" {
		req.CrawlTimeStart = v
	}
	if v := q.Get("crawl_time_end"); v != "" {
		req.CrawlTimeEnd = v
	}
	if v := q.Get("include_raw_data"); v != "" {
		req.IncludeRawData, _ = strconv.ParseBool(v)
	}
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			req.Page = n
		}
	}
	if v := q.Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			req.PageSize = n
		}
	}

	return req
}

// formatTime 格式化时间为 ISO 8601
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
