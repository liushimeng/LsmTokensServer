package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"math"
	"net/http"
	"strconv"
)

// userChatAnalysisInterfaceHandle 用户浏览记录查询 API
func userChatAnalysisInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "未登录",
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

	// 强制使用 JWT 中的用户名，忽略请求体中的 user_name
	req.UserName = claims.UserName

	if req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: "缺少 model_name 参数",
		})
		return
	}

	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisInterfaceResponse{
			Success: false,
			Message: err.Error(),
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

	logger.Printf("[WEB] UserChatAnalysisInterface user=%s model=%s page=%d size=%d days=%d url=%s method=%s status=%s statusNot=%v tools=%s algoType=%d",
		req.UserName, req.ModelName, page, pageSize, days, req.FilterURL, req.FilterMethod, req.FilterStatus, req.FilterStatusNot, req.FilterTools, req.FilterAlgorithmType)

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

// userChatAnalysisDstModelsInterfaceHandle 用户端目标模型列表查询 API
func userChatAnalysisDstModelsInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "未登录",
		})
		return
	}

	var req struct {
		ModelName string `json:"model_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	if req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: "缺少 model_name 参数",
		})
		return
	}

	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisDstModelsResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	names, err := modelsdb.GetDistinctDstModelNames(claims.UserName, req.ModelName, config.G.DBMysqlSubTableNumber)
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

// userChatAnalysisAgentToolsInterfaceHandle 用户端Agent工具列表查询 API
func userChatAnalysisAgentToolsInterfaceHandle(w http.ResponseWriter, r *http.Request) {
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

// userChatAnalysisDetailInterfaceHandle 用户端单条记录字段查询
func userChatAnalysisDetailInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: "未登录",
		})
		return
	}

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
		req.ModelName = r.URL.Query().Get("model_name")
		req.Field = r.URL.Query().Get("field")
	} else {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: "仅支持 POST 或 GET 请求",
		})
		return
	}

	req.UserName = claims.UserName

	if req.ModelName == "" || req.ID == 0 || req.Field == "" {
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

	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisDetailResponse{
			Success: false,
			Message: err.Error(),
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
