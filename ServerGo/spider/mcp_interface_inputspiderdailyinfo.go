package spider

// ==================== MCP /InputSpiderDailyInfo 接口 ====================
//
// 用途：把 /SpiderWebData 抓取到的数据显式保存到 models.TSpiderDailyInfo 表（带分表）。
//
// 数据流：HTTP POST → MCPInputSpiderDailyInfoHandler → models.SaveSpiderDailyInfo (mysql_spider_model.go)
//
// 强制要求：data_source_id 必填；crawl_time 为空时取当前 UTC 时间。
// Agent 必须在拿到 /SpiderWebData 响应后显式调用本接口，否则数据不落库。

import (
	"encoding/json"
	"fmt"
	models "github.com/lishimeng/LsmTokensServer/models"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strings"
	"time"
)

// InputSpiderDailyInfoRequest 写入请求
type InputSpiderDailyInfoRequest struct {
	DataSourceID uint64    `json:"data_source_id"`
	PlatformName string    `json:"platform_name"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	RawData      string    `json:"raw_data"`
	CrawlTime    time.Time `json:"crawl_time"`
	URL          string    `json:"url"`
}

// MCPInputSpiderDailyInfoHandler /InputSpiderDailyInfo 接口处理
func MCPInputSpiderDailyInfoHandler(w http.ResponseWriter, r *http.Request) {
	// v2.0.47：handler 入口生成 RequestID，统一关联日志
	reqID, startTime := mcpLogMCPRequestStart("/InputSpiderDailyInfo", r.RemoteAddr)
	defer func() {
		mcpLogMCPRequestEnd(reqID, startTime, 200, true, "")
	}()
	w.Header().Set("Content-Type", "application/json")
	mcpSetNoCacheHeaders(w)

	mcpLogMCP("Received request for /InputSpiderDailyInfo from %s", r.RemoteAddr)
	mcpLogMCPWithTag(reqID, "request start method=%s content_length=%d", r.Method, r.ContentLength)

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "Method not allowed, use POST",
		})
		return
	}

	var req InputSpiderDailyInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mcpLogMCP("Failed to decode request: %v", err)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	if req.DataSourceID == 0 {
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "data_source_id is required",
		})
		return
	}

	// v2.0.24：拒绝空 payload —— 防止反爬 / login_wall / RSS fallback 等场景下
	// Agent 把空 Title/URL/Content 的占位记录写入 t_spider_daily_info，
	// 导致列表出现「空记录」、且删除时被前端误以为是「Info not found」。
	if strings.TrimSpace(req.Title) == "" {
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "title is required and must be non-empty",
		})
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "url is required and must be non-empty",
		})
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "content is required and must be non-empty",
		})
		return
	}
	if strings.TrimSpace(req.PlatformName) == "" {
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: "platform_name is required and must be non-empty",
		})
		return
	}

	if req.CrawlTime.IsZero() {
		req.CrawlTime = time.Now().UTC()
	} else {
		req.CrawlTime = req.CrawlTime.UTC()
	}

	info := &models.TSpiderDailyInfo{
		DataSourceID: req.DataSourceID,
		PlatformName: req.PlatformName,
		Title:        req.Title,
		Content:      req.Content,
		RawData:      req.RawData,
		CrawlTime:    req.CrawlTime,
		URL:          req.URL,
	}

	// P1: 服务端对 title 做截断保护（避免 varchar(512) 超限导致保存失败）
	if len(info.Title) > 500 {
		mcpLogMCP("Title too long (%d chars), truncating to 500", len(info.Title))
		info.Title = info.Title[:500] + "..."
	}

	if err := models.SaveSpiderDailyInfo(info); err != nil {
		mcpLogMCP("Failed to save daily info: %v", err)
		json.NewEncoder(w).Encode(MCPAPIResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to save daily info: %v", err),
		})
		return
	}

	// v2.0.24：写入后立即失效缓存，避免 2 分钟 TTL 窗口内前端继续看到旧列表
	// （含已删除的空记录 ID=544 等残留项）。
	modelsdb.InvalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")

	mcpLogMCP("Saved daily info: dataSourceID=%d, title=%s", req.DataSourceID, info.Title)

	json.NewEncoder(w).Encode(MCPAPIResponse{
		Success: true,
		Message: "Daily info saved",
	})
}
