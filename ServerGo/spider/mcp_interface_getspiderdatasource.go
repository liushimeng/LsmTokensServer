package spider

// ==================== MCP /GetSpiderDataSource 接口 ====================
//
// 用途：获取爬虫数据源列表（管理员/用户视角），返回平台名 + 目标 URL。
//
// 数据流：HTTP POST/GET → MCPGetSpiderDataSourceHandler → models.ListSpiderDataSources (mysql_spider_model.go)
//
// Agent 通常在工作流开头调用本接口，拿到 data_source_id 后再调用
// /SpiderWebData；数据源 ID 也用于 /InputSpiderDailyInfo 的写入。

import (
	"encoding/json"
	"fmt"
	models "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strconv"
)

// SpiderDataSourceResponse 数据源响应
type SpiderDataSourceResponse struct {
	ID           uint64 `json:"id"`
	UserID       uint64 `json:"user_id"`
	PlatformName string `json:"platform_name"`
	URLAddress   string `json:"url_address"`
	Description  string `json:"description"`
	Remark       string `json:"remark"`
	Status       int    `json:"status"`
}

// GetSpiderDataSourceRequest 数据源查询请求
type GetSpiderDataSourceRequest struct {
	UserID       uint64 `json:"user_id,omitempty"`
	IsAdmin      bool   `json:"is_admin,omitempty"`
	ID           uint64 `json:"id,omitempty"`
	PlatformName string `json:"platform_name,omitempty"`
	Status       *int   `json:"status,omitempty"`
}

// MCPGetSpiderDataSourceHandler /GetSpiderDataSource 接口处理
// 支持多维度查询：ID、PlatformName、Status 等
func MCPGetSpiderDataSourceHandler(w http.ResponseWriter, r *http.Request) {
	// v2.0.47：handler 入口生成 RequestID，统一关联日志
	reqID, startTime := mcpLogMCPRequestStart("/GetSpiderDataSource", r.RemoteAddr)
	defer func() {
		mcpLogMCPRequestEnd(reqID, startTime, 200, true, "")
	}()
	w.Header().Set("Content-Type", "application/json")
	mcpSetNoCacheHeaders(w)

	mcpLogMCP("Received request for /GetSpiderDataSource from %s", r.RemoteAddr)
	mcpLogMCPWithTag(reqID, "request start method=%s", r.Method)

	var req GetSpiderDataSourceRequest

	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// 解析失败时继续，使用默认值
			mcpLogMCP("Failed to decode POST body: %v, using defaults", err)
		}
	} else if r.Method == http.MethodGet {
		if userIDStr := r.URL.Query().Get("user_id"); userIDStr != "" {
			if id, err := strconv.ParseUint(userIDStr, 10, 64); err == nil {
				req.UserID = id
			}
		}
		if isAdminStr := r.URL.Query().Get("is_admin"); isAdminStr != "" {
			req.IsAdmin, _ = strconv.ParseBool(isAdminStr)
		}
		if idStr := r.URL.Query().Get("id"); idStr != "" {
			if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
				req.ID = id
			}
		}
		if platformName := r.URL.Query().Get("platform_name"); platformName != "" {
			req.PlatformName = platformName
		}
		if statusStr := r.URL.Query().Get("status"); statusStr != "" {
			if status, err := strconv.Atoi(statusStr); err == nil {
				req.Status = &status
			}
		}
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "Method not allowed, use POST or GET",
		})
		return
	}

	// 使用带过滤条件的查询
	dataSources, err := models.ListSpiderDataSourcesWithFilter(
		req.UserID,
		req.IsAdmin,
		req.ID,
		req.PlatformName,
		req.Status,
	)
	if err != nil {
		mcpLogMCP("Failed to get data sources: %v", err)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to get data sources: %v", err),
		})
		return
	}

	response := make([]SpiderDataSourceResponse, 0, len(dataSources))
	for _, ds := range dataSources {
		response = append(response, SpiderDataSourceResponse{
			ID:           ds.ID,
			UserID:       ds.UserID,
			PlatformName: ds.PlatformName,
			URLAddress:   ds.URLAddress,
			Description:  ds.Description,
			Remark:       ds.Remark,
			Status:       ds.Status,
		})
	}

	mcpLogMCP("Returned %d data sources (filter: id=%d, platform=%s, status=%v)",
		len(response), req.ID, req.PlatformName, req.Status)

	json.NewEncoder(w).Encode(MCPAPIResponse{
		Success: true,
		Message: "Data sources retrieved",
		Data:    response,
	})
}
