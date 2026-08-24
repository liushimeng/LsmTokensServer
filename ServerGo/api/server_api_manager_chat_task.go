package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
)

// ChatAnalysisTaskInterfaceRequest Task 分析接口请求体
type ChatAnalysisTaskInterfaceRequest struct {
	UserName  string `json:"user_name"`
	ModelName string `json:"model_name"`
	Days      int    `json:"days"`
}

// ChatAnalysisTaskInterfaceResponse Task 分析接口响应体
type ChatAnalysisTaskInterfaceResponse struct {
	Success bool                         `json:"success"`
	Message string                       `json:"message"`
	Data    *modelsdb.TaskAnalysisResult `json:"data,omitempty"`
}

// chatAnalysisTaskInterfaceHandle 处理 Task 分析 API 请求
func chatAnalysisTaskInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ChatAnalysisTaskInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}

	logger.Printf("[WEB] ChatAnalysisTaskInterface user=%s model=%s days=%d", req.UserName, req.ModelName, req.Days)

	result, err := modelsdb.GetTaskAnalysis(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
	if err != nil {
		logger.Printf("[WARNING] ChatAnalysisTaskInterface failed: %v", err)
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "分析失败: " + err.Error(),
		})
		return
	}

	logger.Printf("[WEB] ChatAnalysisTaskInterface result: user=%s model=%s tasks=%d models=%d",
		req.UserName, req.ModelName, result.TotalTasks, len(result.ModelStats))

	json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
		Success: true,
		Message: "分析成功",
		Data:    result,
	})
}
