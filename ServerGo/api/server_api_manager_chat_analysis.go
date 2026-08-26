package api

import (
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"math"
	"net/http"
	"strconv"
)

// AgentHttpQueryResult 统一分析页面查询结果
type AgentHttpQueryResult struct {
	Records     []modelsdb.TAgentHttpTransactionDataItem `json:"records"`
	TotalCount  int64                                    `json:"totalCount"`
	TotalPages  int                                      `json:"totalPages"`
	CurrentPage int                                      `json:"currentPage"`
	PageSize    int                                      `json:"pageSize"`
}

// ChatAnalysisInterfaceRequest 查询接口请求体
type ChatAnalysisInterfaceRequest struct {
	UserName                  string `json:"user_name"`
	ModelName                 string `json:"model_name"`
	Page                      int    `json:"page"`
	PageSize                  int    `json:"page_size"`
	FilterURL                 string `json:"filter_url"`
	FilterMethod              string `json:"filter_method"`
	FilterStatus              string `json:"filter_status"`
	FilterStatusNot           bool   `json:"filter_status_not"`
	FilterProtocolType        int    `json:"filter_protocol_type"`
	FilterDstModelName        string `json:"filter_dst_model_name"`
	FilterTools               string `json:"filter_tools"`
	FilterAgentToolName       string `json:"filter_agent_tool_name"`
	Days                      int    `json:"days"`
	FilterInputTokensNonzero  int    `json:"filter_input_tokens_nonzero"`  // 0=全部,1=非零,2=为零
	FilterOutputTokensNonzero int    `json:"filter_output_tokens_nonzero"` // 0=全部,1=非零,2=为零
	FilterAlgorithmType       int    `json:"filter_algorithm_type"`       // v2.0.7x 阶段AM：0=全部,1=协议直连,2=协议转换器
}

// ChatAnalysisInterfaceResponse 查询接口响应体
type ChatAnalysisInterfaceResponse struct {
	Success bool                  `json:"success"`
	Message string                `json:"message"`
	Data    *AgentHttpQueryResult `json:"data,omitempty"`
}

// normalizeChatAnalysisDays 把 /ChatAnalysis 接口的 days 参数限制到合法范围。
//
// v2.0.41：与 /AIRouteManage 对齐，单一 int 编码时间跨度（span）。
//   - days == 0：无限制（不过滤 created_at）
//   - days  > 0：最近 days 天
//   - days  < 0：最近 (-days) 小时，例如 -1=1小时、-12=12小时
//
// 20260826 时间跨度动态档位：白名单改为范围校验（小时 ≤720、天 ≤365），
// 配合前端动态 10 档（1 小时 ~ transactionRetentionDays+1 天）。
// 范围外的值统一回落到 3（保持与旧实现一致的默认值）。
func normalizeChatAnalysisDays(days int) int {
	if days == 0 {
		return 0
	}
	if days > 0 {
		if days > 365 {
			return 3
		}
		return days
	}
	if days < -720 {
		return 3
	}
	return days
}

// chatAnalysisInterfaceHandle 处理 /ChatAnalysisInterface API 查询请求
func chatAnalysisInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ChatAnalysisInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}

	page := req.Page
	if page < 1 {
		page = 1
	}

	pageSize := req.PageSize
	switch pageSize {
	case 1, 3, 5, 10, 15, 20, 50, 100:
		// 合法值
	default:
		pageSize = 3
	}
	days := normalizeChatAnalysisDays(req.Days)

	logger.Printf("[WEB] ChatAnalysisInterface user=%s model=%s page=%d size=%d days=%d url=%s method=%s status=%s statusNot=%v tools=%s algoType=%d",
		req.UserName, req.ModelName, page, pageSize, days, req.FilterURL, req.FilterMethod, req.FilterStatus, req.FilterStatusNot, req.FilterTools, req.FilterAlgorithmType)

	// 查询数据（从哈希分表按用户名+模型索引名称查询）
	records, total, err := modelsdb.QueryAgentHttpTransactions(
		req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, page, pageSize,
		req.FilterURL, req.FilterMethod, req.FilterStatus, req.FilterStatusNot, req.FilterProtocolType,
		req.FilterDstModelName, req.FilterTools, req.FilterAgentToolName, days,
		req.FilterInputTokensNonzero, req.FilterOutputTokensNonzero,
		req.FilterAlgorithmType,
	)
	if err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "查询失败: " + err.Error(),
		})
		return
	}

	totalPages := int(math.Ceil(float64(total) / float64(pageSize)))
	if total > 0 && page > totalPages {
		page = totalPages
	}

	logger.Printf("[WEB] ChatAnalysisInterface query result: user=%s model=%s total=%d records=%d", req.UserName, req.ModelName, total, len(records))

	result := &AgentHttpQueryResult{
		Records:     records,
		TotalCount:  total,
		TotalPages:  totalPages,
		CurrentPage: page,
		PageSize:    pageSize,
	}

	json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
		Success: true,
		Message: "查询成功",
		Data:    result,
	})
}

// ChatAnalysisDstModelsResponse 目标模型列表查询响应
type ChatAnalysisDstModelsResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Data    []string `json:"data,omitempty"`
}

// chatAnalysisDstModelsInterfaceHandle 处理目标模型列表查询请求
func chatAnalysisDstModelsInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req struct {
		UserName  string `json:"user_name"`
		ModelName string `json:"model_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}

	names, err := modelsdb.GetDistinctDstModelNames(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber)
	if err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "查询失败: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
		Success: true,
		Message: "查询成功",
		Data:    names,
	})
}

// ChatAnalysisAgentToolsResponse Agent工具列表查询响应
type ChatAnalysisAgentToolsResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Data    []string `json:"data,omitempty"`
}

// chatAnalysisAgentToolsInterfaceHandle 处理Agent工具列表查询请求
func chatAnalysisAgentToolsInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisAgentToolsResponse{
			Success: false,
			Message: "仅支持 GET 或 POST 请求",
		})
		return
	}

	names, err := modelsdb.GetDistinctAgentToolNames()
	if err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisAgentToolsResponse{
			Success: false,
			Message: "查询失败: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(ChatAnalysisAgentToolsResponse{
		Success: true,
		Message: "查询成功",
		Data:    names,
	})
}

// ChatAnalysisDetailRequest 单条记录字段查询请求
// field 必须是 modelsdb.chatAnalysisDetailFieldColumns 中的固定字段名。
type ChatAnalysisDetailRequest struct {
	ID        uint64 `json:"id"`
	UserName  string `json:"user_name"`
	ModelName string `json:"model_name"`
	Field     string `json:"field"`
}

// ChatAnalysisDetailResponse 单条记录字段查询响应。
type ChatAnalysisDetailResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Value   string `json:"value,omitempty"`
}

// chatAnalysisDetailInterfaceHandle 处理单条记录字段查询。
func chatAnalysisDetailInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	var req ChatAnalysisDetailRequest
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
				Success: false,
				Message: "无效的请求体: " + err.Error(),
			})
			return
		}
	} else if r.Method == http.MethodGet {
		idStr := r.URL.Query().Get("id")
		if idStr != "" {
			id, err := strconv.ParseUint(idStr, 10, 64)
			if err == nil {
				req.ID = id
			}
		}
		req.UserName = r.URL.Query().Get("user_name")
		req.ModelName = r.URL.Query().Get("model_name")
		req.Field = r.URL.Query().Get("field")
	} else {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: "仅支持 POST 或 GET 请求",
		})
		return
	}

	if req.UserName == "" || req.ModelName == "" || req.ID == 0 || req.Field == "" {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: "缺少必要参数",
		})
		return
	}
	if _, ok := modelsdb.ResolveChatAnalysisDetailColumn(req.Field); !ok {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: "不支持的详情字段",
		})
		return
	}

	value, err := modelsdb.GetAgentHttpTransactionFieldByID(
		req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.ID, req.Field,
	)
	if err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: "查询失败: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
		Success: true,
		Message: "查询成功",
		Field:   req.Field,
		Value:   value,
	})
}

// ============================================================================
// v2.0.29: 浏览记录批量删除（仅管理员端）
// ============================================================================

// ChatAnalysisBatchDeleteRequest 批量删除请求体
//
// 与查询接口对称：传一次 user_name + model_name + ids，调用一次即可。
// （管理员 UI 当前一次只浏览一个 (user, model)，保持与查询接口相同的扁平结构；
//
//	若未来管理员支持跨 user 多选，前端再改为按 user+model 分组循环调用本接口。）
type ChatAnalysisBatchDeleteRequest struct {
	UserName  string   `json:"user_name"`
	ModelName string   `json:"model_name"`
	IDs       []uint64 `json:"ids"`
}

// ChatAnalysisBatchDeleteResponse 批量删除响应
//
// 两桶统计：deleted（已删除）+ skipped_not_found（ID 不存在/不属于该 user+model）。
// 管理员跳过权限预检，所以无 skipped_no_permission 桶。
type ChatAnalysisBatchDeleteResponse struct {
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	Deleted         int64  `json:"deleted"`
	SkippedNotFound int64  `json:"skipped_not_found"`
}

// chatAnalysisBatchDeleteInterfaceHandle 处理 /ChatAnalysisBatchDeleteInterface
//
// v2.0.29 管理员端批量删除：单 SQL `WHERE id IN ?` + Unscoped() 硬删除，
// 最多 500 条/次。删除后由 modelsdb.DeleteAgentHttpTransactions 内部失效
// modelsdb.invalidateStatsCacheByUserModel，/ChatAnalysisTotal 统计页同步刷新。
func chatAnalysisBatchDeleteInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisBatchDeleteResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ChatAnalysisBatchDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisBatchDeleteResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisBatchDeleteResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}
	if len(req.IDs) == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisBatchDeleteResponse{
			Success: false,
			Message: "未选择任何记录",
		})
		return
	}

	logger.Printf("[WEB] ChatAnalysisBatchDelete user=%s model=%s ids=%d",
		req.UserName, req.ModelName, len(req.IDs))

	deleted, err := modelsdb.DeleteAgentHttpTransactions(
		req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.IDs)
	if err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisBatchDeleteResponse{
			Success: false,
			Message: "删除失败: " + err.Error(),
		})
		return
	}

	skippedNotFound := int64(len(req.IDs)) - deleted
	logger.Printf("[WEB] ChatAnalysisBatchDelete completed user=%s model=%s deleted=%d skipped=%d",
		req.UserName, req.ModelName, deleted, skippedNotFound)

	json.NewEncoder(w).Encode(ChatAnalysisBatchDeleteResponse{
		Success:         true,
		Message:         fmt.Sprintf("批量删除完成：%d 条已删除，%d 条不存在", deleted, skippedNotFound),
		Deleted:         deleted,
		SkippedNotFound: skippedNotFound,
	})
}
