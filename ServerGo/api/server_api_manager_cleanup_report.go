package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strconv"
)

// ============================================================================
// v2.0.47: 过期数据清理统计 API（管理员端）
// ============================================================================
//
// 三个 action：
//   - "list"（默认）：分页查询清理报告 + 当日累计 KPI + 时序聚合数据
//   - "summary"：仅返回累计 KPI（用于首页快速预览）
//   - "state"：返回清理服务运行状态（enabled/running/last_run_at/next_run_at）
//
// POST body：{"action":"list|summary|state", "page":1, "page_size":20, "days":30}
// ============================================================================

// CleanupReportAPIRequest 清理报告 API 请求体
type CleanupReportAPIRequest struct {
	Action   string `json:"action,omitempty"`    // list / summary / state / tables（默认 list）
	Page     int    `json:"page,omitempty"`      // 分页（仅 list 生效）
	PageSize int    `json:"page_size,omitempty"` // 每页条数（仅 list 生效，默认 20）
	Days     int    `json:"days,omitempty"`      // 时间筛选天数（list 默认 30；0 = 无限制）
	Table    string `json:"table,omitempty"`     // v2.0.63: tables action 精确计数的目标表名（空 = 仅元数据）
}

// CleanupReportAPIResponse 清理报告 API 响应体
type CleanupReportAPIResponse struct {
	Success        bool                                          `json:"success"`
	Message        string                                        `json:"message"`
	Action         string                                        `json:"action,omitempty"`
	Reports        []modelsdb.TAgentHttpTransactionCleanupReport `json:"reports,omitempty"`
	Total          int64                                         `json:"total,omitempty"`
	Page           int                                           `json:"page,omitempty"`
	PageSize       int                                           `json:"page_size,omitempty"`
	TotalSummary   *modelsdb.CleanupReportsTotalSummary          `json:"total_summary,omitempty"`
	DailySummaries []modelsdb.CleanupReportsDailySummary         `json:"daily_summaries,omitempty"`
	State          map[string]interface{}                        `json:"state,omitempty"`
	Tables         []modelsdb.SubTableInspectorInfo              `json:"tables,omitempty"`          // v2.0.63: 分表元数据快照
	ExactTable     string                                        `json:"exact_table,omitempty"`     // v2.0.63: 精确计数的目标表名
	ExactRowCount  int64                                         `json:"exact_row_count,omitempty"` // v2.0.63: 精确计数结果
}

// cleanupReportInterfaceHandle 处理 /CleanupReportInterface API 请求（管理员端）
func cleanupReportInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(CleanupReportAPIResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req CleanupReportAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(CleanupReportAPIResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	// action 默认值
	if req.Action == "" {
		req.Action = "list"
	}

	// 参数校验
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}
	// 20260826：days 升级为统一 span 编码（负值=最近 N 小时）；范围外回落 30
	if req.Days < -720 || req.Days > 365 {
		req.Days = 30
	}

	resp := CleanupReportAPIResponse{
		Success: true,
		Action:  req.Action,
	}

	switch req.Action {
	case "list":
		reports, total, err := modelsdb.QueryCleanupReports(req.Page, req.PageSize, req.Days)
		if err != nil {
			resp.Success = false
			resp.Message = "查询清理报告失败: " + err.Error()
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp.Reports = reports
		resp.Total = total
		resp.Page = req.Page
		resp.PageSize = req.PageSize

		// 附带当日累计 KPI + 时序聚合数据（同一接口一站式返回）
		if summary, err := modelsdb.GetCleanupReportsTotalSummary(); err == nil {
			resp.TotalSummary = &summary
		} else {
			logger.Printf("[WARNING] CleanupReport list: modelsdb.GetCleanupReportsTotalSummary failed: %v", err)
		}
		days := req.Days
		if days == 0 {
			days = 90 // 默认 90 天时序
		}
		if summaries, err := modelsdb.GetCleanupReportsDailySummary(days); err == nil {
			resp.DailySummaries = summaries
		} else {
			logger.Printf("[WARNING] CleanupReport list: modelsdb.GetCleanupReportsDailySummary failed: %v", err)
		}

	case "summary":
		summary, err := modelsdb.GetCleanupReportsTotalSummary()
		if err != nil {
			resp.Success = false
			resp.Message = "查询累计汇总失败: " + err.Error()
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp.TotalSummary = &summary

		days := req.Days
		if days == 0 {
			days = 90
		}
		summaries, err := modelsdb.GetCleanupReportsDailySummary(days)
		if err != nil {
			resp.Success = false
			resp.Message = "查询时序聚合失败: " + err.Error()
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp.DailySummaries = summaries

	case "state":
		resp.State = modelsdb.GetCleanupStateSnapshot()

	case "tables":
		// v2.0.63: 分表 Schema Inspector —— 元数据走 information_schema（近似值，毫秒级）；
		// 带 table 参数时对该表执行 COUNT(*) 精确计数（走 statsDB() 25s ctx）。
		// 与项目其他统计路径一致：直接用 config.DEFAULT_SUB_TABLE_NUM（生产配置恒为 8）
		entries, err := modelsdb.GetSubTableInspector(config.DEFAULT_SUB_TABLE_NUM)
		if err != nil {
			resp.Success = false
			resp.Message = "查询分表元数据失败: " + err.Error()
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp.Tables = entries

		if req.Table != "" {
			// 表名白名单校验：必须是 TAgentHttpTransactionDataItem_%02d 且在元数据结果里存在，
			// 防止调用方拼任意表名打别的表。
			allowed := false
			for _, e := range entries {
				if e.Exists && e.TableName == req.Table {
					allowed = true
					break
				}
			}
			if !allowed {
				resp.Success = false
				resp.Message = "未知分表名: " + req.Table
				json.NewEncoder(w).Encode(resp)
				return
			}
			exact, err := modelsdb.CountSubTableRowsExact(req.Table)
			if err != nil {
				// 精确计数超时/失败不阻断：仍返回近似元数据，前端提示降级
				resp.Message = "精确计数失败（已返回近似值）: " + err.Error()
			} else {
				resp.ExactTable = req.Table
				resp.ExactRowCount = exact
			}
		}

	default:
		resp.Success = false
		resp.Message = "未知 action: " + req.Action + "（支持 list/summary/state/tables）"
	}

	logger.Printf("[WEB] CleanupReportInterface action=%s page=%d/%d days=%d total=%d",
		req.Action, req.Page, req.PageSize, req.Days, resp.Total)

	json.NewEncoder(w).Encode(resp)
}

// 防止 strconv 未使用（保留供后续扩展）
var _ = strconv.Itoa
