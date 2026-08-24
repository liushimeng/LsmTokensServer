package api

import (
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"sync"
	"time"
)

// verifyUserModelAccess 验证模型是否属于当前 JWT 用户
func verifyUserModelAccess(claims *UserTokenClaims, modelName string) error {
	if claims.UserID == 0 {
		return fmt.Errorf("未登录")
	}
	if modelName == "" {
		return fmt.Errorf("模型名称不能为空")
	}
	_, err := modelsdb.GetUserModelByUserIDAndModelName(claims.UserID, modelName)
	if err != nil {
		return fmt.Errorf("无权访问该模型")
	}
	return nil
}

// userChatAnalysisTotalInterfaceHandle 用户统计分析 API
func userChatAnalysisTotalInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
			Success: false,
			Message: "未登录",
		})
		return
	}

	var req ChatAnalysisTotalInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	// 安全：强制使用 JWT 中的 user_name，忽略请求体中的
	req.UserName = claims.UserName

	// 验证模型归属权
	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	action := req.Action
	if action == "" {
		// v2.0.48 默认走 insights_summary：首屏仅拉 time_stats + tokens_stats
		action = "insights_summary"
	}
	logger.Printf("[WEB] UserChatAnalysisTotalInterface user=%s model=%s days=%d action=%s",
		req.UserName, req.ModelName, req.Days, action)

	if action == "insights_summary" {
		lsmRunInsightsSummary(w, req.UserName, req.ModelName, req.Days)
		return
	}
	if action == "protocol_stats" {
		lsmRunProtocolStats(w, req.UserName, req.ModelName, req.Days)
		return
	}
	if action == "agent_stats" {
		lsmRunAgentStats(w, req.UserName, req.ModelName, req.Days)
		return
	}

	// 并发执行查询，减少总耗时
	var timeStats []modelsdb.TimeRangeStat
	var totalCount int64
	var protocolStats *modelsdb.ProtocolAnalysisStats
	var tokensStats []modelsdb.TokensRangeStat
	var tokensModel []modelsdb.TokensModelStat
	var tokensLatency []modelsdb.TokensLatencyStat
	var tokensSummary *TokensSummaryStat
	var agentStats *modelsdb.AgentToolStatsResponse
	var timeErr, protocolErr, tokensErr, agentErr error

	var wg sync.WaitGroup
	wg.Add(5)

	go func() {
		defer wg.Done()
		timeStats, timeErr = modelsdb.GetTimeRangeStats(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
	}()

	go func() {
		defer wg.Done()
		totalCount, _ = modelsdb.CountAgentHttpTransactions(req.UserName, req.ModelName, 0, config.G.DBMysqlSubTableNumber)
	}()

	go func() {
		defer wg.Done()
		protocolStats, protocolErr = modelsdb.GetProtocolAnalysisStats(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, 500)
	}()

	go func() {
		defer wg.Done()
		tokensStats, tokensErr = modelsdb.GetTokensRangeStats(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
		if tokensErr == nil {
			tokensModel, _ = modelsdb.GetTokensModelStats(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
			tokensLatency, _ = modelsdb.GetTokensLatencyStats(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
			// 计算汇总
			var totalInput, totalOutput, totalAll uint64
			var totalCnt int64
			var sumElapsed, sumTTFB, sumGen int64
			for _, s := range tokensStats {
				totalInput += s.TokensInput
				totalOutput += s.TokensOutput
				totalAll += s.TokensTotal
				totalCnt += s.Count
				sumElapsed += s.AvgElapsedMs * s.Count
				sumTTFB += s.AvgTTFBMs * s.Count
				sumGen += s.AvgGenerateMs * s.Count
			}
			var avgInput, avgOutput, avgAll uint64
			var avgElapsed, avgTTFB, avgGen int64
			if totalCnt > 0 {
				avgInput = totalInput / uint64(totalCnt)
				avgOutput = totalOutput / uint64(totalCnt)
				avgAll = totalAll / uint64(totalCnt)
				avgElapsed = sumElapsed / totalCnt
				avgTTFB = sumTTFB / totalCnt
				avgGen = sumGen / totalCnt
			}
			tokensSummary = &TokensSummaryStat{
				TotalInput:   totalInput,
				TotalOutput:  totalOutput,
				TotalAll:     totalAll,
				AvgInput:     avgInput,
				AvgOutput:    avgOutput,
				AvgAll:       avgAll,
				AvgElapsedMs: avgElapsed,
				AvgTTFBMs:    avgTTFB,
				AvgGenMs:     avgGen,
			}
		}
	}()

	go func() {
		defer wg.Done()
		agentStats, agentErr = modelsdb.GetAgentToolStatsByRange(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
	}()

	wg.Wait()

	if timeErr != nil {
		logger.Printf("[WARNING] UserChatAnalysisTotalInterface time stats failed: %v", timeErr)
		timeStats = nil
	}
	if protocolErr != nil {
		logger.Printf("[WARNING] UserChatAnalysisTotalInterface protocol stats failed: %v", protocolErr)
		protocolStats = nil
	}
	if tokensErr != nil {
		logger.Printf("[WARNING] UserChatAnalysisTotalInterface tokens stats failed: %v", tokensErr)
		tokensStats = nil
	}
	if agentErr != nil {
		logger.Printf("[WARNING] UserChatAnalysisTotalInterface agent stats failed: %v", agentErr)
		agentStats = nil
	}

	logger.Printf("[WEB] UserChatAnalysisTotalInterface result: user=%s model=%s total=%d timeRanges=%d protocolSample=%d tokensDays=%d",
		req.UserName, req.ModelName, totalCount, len(timeStats), func() int {
			if protocolStats != nil {
				return protocolStats.SampleCount
			}
			return 0
		}(), len(tokensStats))

	json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
		Success:       true,
		Message:       "查询成功",
		TimeStats:     timeStats,
		TotalCount:    totalCount,
		ProtocolStats: protocolStats,
		TokensStats:   tokensStats,
		TokensModel:   tokensModel,
		TokensLatency: tokensLatency,
		TokensSummary: tokensSummary,
		AgentStats:    agentStats,
	})
}

// userChatAnalysisTotalRangeReportHandle 用户端 brush 选区生成报告 API
// 与管理员端 chatAnalysisTotalRangeReportHandle 行为一致，但强制使用 JWT 中的 user_name
// v2.0.46: 同样支持 ?stream=1 走 SSE 流式推送
func userChatAnalysisTotalRangeReportHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		setNoCacheHeaders(w)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		w.Header().Set("Content-Type", "application/json")
		setNoCacheHeaders(w)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "未登录",
		})
		return
	}

	var req ChatAnalysisTotalRangeReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		setNoCacheHeaders(w)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "无效的请求体: " + err.Error(),
		})
		return
	}

	// 安全：强制使用 JWT 中的 user_name，忽略请求体中的
	req.UserName = claims.UserName

	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		w.Header().Set("Content-Type", "application/json")
		setNoCacheHeaders(w)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// v2.0.46: stream=1 走 SSE 流式推送；否则走老 JSON 同步路径
	if r.URL.Query().Get("stream") == "1" {
		chatAnalysisTotalRangeReportStreamHandle(w, r, req)
		return
	}

	// --- 老的 JSON 同步路径（保持不变） ---
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	start := time.UnixMilli(req.StartMs)
	end := time.UnixMilli(req.EndMs)
	if req.StartMs <= 0 || req.EndMs <= 0 || !end.After(start) {
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "无效的时间区间: start_ms / end_ms 必须为正整数且 end_ms > start_ms",
		})
		return
	}
	if end.Sub(start) > 365*24*time.Hour {
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "时间区间过长: 最大支持 1 年 (365 天)",
		})
		return
	}

	granularity := modelsdb.NormalizeTokensGranularity(req.Granularity)

	logger.Printf("[WEB] UserChatAnalysisTotalRangeReport user=%s model=%s range=%s~%s granularity=%s",
		req.UserName, req.ModelName, start.Format("2006-01-02 15:04"), end.Format("2006-01-02 15:04"), granularity)

	var rangeReport *modelsdb.TokensReportStat
	var agentDist *modelsdb.AgentToolStatsResponse
	var rangeErr, agentErr error

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rangeReport, rangeErr = modelsdb.GetTokensRangeReport(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, start, end, granularity)
	}()
	go func() {
		defer wg.Done()
		dayCount := int(end.Sub(start).Hours()/24) + 1
		if dayCount > 365 {
			dayCount = 365
		}
		agentDist, agentErr = modelsdb.GetAgentToolStatsByRange(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, dayCount)
	}()
	wg.Wait()

	if rangeErr != nil {
		logger.Printf("[WARNING] UserChatAnalysisTotalRangeReport tokens failed: %v", rangeErr)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "Tokens 区间统计失败: " + rangeErr.Error(),
		})
		return
	}
	if agentErr != nil {
		logger.Printf("[WARNING] UserChatAnalysisTotalRangeReport agent failed: %v", agentErr)
		agentDist = nil
	}

	logger.Printf("[WEB] UserChatAnalysisTotalRangeReport result: user=%s model=%s buckets=%d count=%d",
		req.UserName, req.ModelName, len(rangeReport.Series), rangeReport.TotalCount)

	json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
		Success:     true,
		Message:     "查询成功",
		RangeReport: rangeReport,
		AgentDist:   agentDist,
	})
}

// userChatAnalysisSessionInterfaceHandle 用户 Session 分析 API
func userChatAnalysisSessionInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "未登录",
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

	req.UserName = claims.UserName

	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	logger.Printf("[WEB] UserChatAnalysisSessionInterface user=%s model=%s days=%d", req.UserName, req.ModelName, req.Days)

	result, err := modelsdb.GetSessionAnalysis(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
	if err != nil {
		logger.Printf("[WARNING] UserChatAnalysisSessionInterface failed: %v", err)
		json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
			Success: false,
			Message: "分析失败: " + err.Error(),
		})
		return
	}

	logger.Printf("[WEB] UserChatAnalysisSessionInterface result: user=%s model=%s sessions=%d tasks=%d",
		req.UserName, req.ModelName, result.TotalSessions, result.TotalTasks)

	json.NewEncoder(w).Encode(ChatAnalysisSessionInterfaceResponse{
		Success: true,
		Message: "分析成功",
		Data:    result,
	})
}

// userChatAnalysisTaskInterfaceHandle 用户 Task 分析 API
func userChatAnalysisTaskInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims := getUserToken(r)
	if claims.UserID == 0 {
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "未登录",
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

	req.UserName = claims.UserName

	if err := verifyUserModelAccess(claims, req.ModelName); err != nil {
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	logger.Printf("[WEB] UserChatAnalysisTaskInterface user=%s model=%s days=%d", req.UserName, req.ModelName, req.Days)

	result, err := modelsdb.GetTaskAnalysis(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
	if err != nil {
		logger.Printf("[WARNING] UserChatAnalysisTaskInterface failed: %v", err)
		json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
			Success: false,
			Message: "分析失败: " + err.Error(),
		})
		return
	}

	logger.Printf("[WEB] UserChatAnalysisTaskInterface result: user=%s model=%s tasks=%d models=%d",
		req.UserName, req.ModelName, result.TotalTasks, len(result.ModelStats))

	json.NewEncoder(w).Encode(ChatAnalysisTaskInterfaceResponse{
		Success: true,
		Message: "分析成功",
		Data:    result,
	})
}
