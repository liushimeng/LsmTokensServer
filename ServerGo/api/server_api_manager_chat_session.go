package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
)

// ChatAnalysisSessionInterfaceRequest Session 分析接口请求体
type ChatAnalysisSessionInterfaceRequest struct {
	UserName  string `json:"user_name"`
	ModelName string `json:"model_name"`
	Days      int    `json:"days"`
}

// ChatAnalysisSessionInterfaceResponse Session 分析接口响应体
type ChatAnalysisSessionInterfaceResponse struct {
	Success bool                            `json:"success"`
	Message string                          `json:"message"`
	Data    *modelsdb.SessionAnalysisResult `json:"data,omitempty"`
}

// chatAnalysisSessionInterfaceHandle 处理 Session 分析 API 请求
func chatAnalysisSessionInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ChatAnalysisSessionInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}

	logger.Printf("[WEB] ChatAnalysisSessionInterface user=%s model=%s days=%d", req.UserName, req.ModelName, req.Days)

	result, err := modelsdb.GetSessionAnalysis(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
	if err != nil {
		logger.Printf("[WARNING] ChatAnalysisSessionInterface failed: %v", err)
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "分析失败: " + err.Error(),
		})
		return
	}

	logger.Printf("[WEB] ChatAnalysisSessionInterface result: user=%s model=%s sessions=%d tasks=%d",
		req.UserName, req.ModelName, result.TotalSessions, result.TotalTasks)

	json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
		Success: true,
		Message: "分析成功",
		Data:    result,
	})
}
