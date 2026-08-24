package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ChatAnalysisTotalInterfaceRequest 统计接口请求体
type ChatAnalysisTotalInterfaceRequest struct {
	UserName  string `json:"user_name"`
	ModelName string `json:"model_name"`
	Days      int    `json:"days"`
	// Action 可选值："" / "full"（默认全量四块统计）；"insights_summary"（仅时间/Tokens，跳过协议/Agent，减少 IO）
	Action string `json:"action,omitempty"`
}

// AgentToolStat / modelsdb.AgentToolStatsResponse 已迁至 models 包（models/dto_agent_tool.go）

// ChatAnalysisTotalInterfaceResponse 统计接口响应体
type ChatAnalysisTotalInterfaceResponse struct {
	Success       bool                             `json:"success"`
	Message       string                           `json:"message"`
	TimeStats     []modelsdb.TimeRangeStat         `json:"time_stats,omitempty"`
	TotalCount    int64                            `json:"total_count"`
	ProtocolStats *modelsdb.ProtocolAnalysisStats  `json:"protocol_stats,omitempty"`
	TokensStats   []modelsdb.TokensRangeStat       `json:"tokens_stats,omitempty"`
	TokensModel   []modelsdb.TokensModelStat       `json:"tokens_model,omitempty"`
	TokensLatency []modelsdb.TokensLatencyStat     `json:"tokens_latency,omitempty"`
	TokensSummary *TokensSummaryStat               `json:"tokens_summary,omitempty"`
	AgentStats    *modelsdb.AgentToolStatsResponse `json:"agent_stats,omitempty"`
}

// TokensSummaryStat Tokens汇总统计
type TokensSummaryStat struct {
	TotalInput   uint64 `json:"total_input"`
	TotalOutput  uint64 `json:"total_output"`
	TotalAll     uint64 `json:"total_all"`
	AvgInput     uint64 `json:"avg_input"`
	AvgOutput    uint64 `json:"avg_output"`
	AvgAll       uint64 `json:"avg_all"`
	AvgElapsedMs int64  `json:"avg_elapsed_ms"`
	AvgTTFBMs    int64  `json:"avg_ttfb_ms"`
	AvgGenMs     int64  `json:"avg_gen_ms"`
}

// ChatAnalysisTotalFullHTTPResponse 管理员端「全量统计」HTTP 接口响应（v2.0.64）。
// 数据形状与 WS chunk 各 stage 完全对齐，前端 __lsmRenderStageHTML 可直接渲染。
// 当管理员 Web 服务被应用网关代理（网关不支持 WebSocket Upgrade）时，前端自动
// fallback 到本接口，用 All 变体拉全站数据（与 WS 路径同语义）。
type ChatAnalysisTotalFullHTTPResponse struct {
	Success       bool                             `json:"success"`
	Message       string                           `json:"message"`
	Days          int                              `json:"days"`
	KPI           map[string]interface{}           `json:"kpi,omitempty"`
	TimeStats     []modelsdb.TimeRangeStat         `json:"time_stats,omitempty"`
	TokensSummary map[string]interface{}           `json:"tokens_summary,omitempty"`
	ModelDist     []modelsdb.ModelNameUsageStat    `json:"model_distribution,omitempty"`
	Trend         []modelsdb.DailyStat             `json:"trend_chart,omitempty"`
	ProtocolStats *modelsdb.ProtocolAnalysisStats  `json:"protocol_stats,omitempty"`
	AgentStats    *modelsdb.AgentToolStatsResponse `json:"agent_stats,omitempty"`
}

// chatAnalysisTotalInterfaceHandle 处理 /ChatAnalysisTotalInterface API 统计请求
func chatAnalysisTotalInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
			Success: false,
			Message: "仅支持 POST 请求",
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

	// v2.0.64: 新增 action="full_http" —— 管理员端全量统计 HTTP fallback。
	// 当管理员 Web 服务被应用网关代理（网关不支持 WebSocket Upgrade）时，前端自动
	// fallback 到本接口，用 All 变体拉全站数据（与 WS 路径同语义）。
	// v2.0.68 校正：full_http 必须带 user_name/model_name，让 stage 4 等所有按 user/model
	// 维度的统计落在「本平台用户的某个模型」上下文（与 WS 路径 WHERE 完全对齐）。
	if req.Action == "full_http" {
		lsmHandleFullHTTP(w, req.UserName, req.ModelName, req.Days)
		return
	}

	// v2.0.68: 新增 action="model_distribution_full" —— stage 4 完整源站模型分布。
	// 当用户在 stage 4 点击"加载全部"按钮时调用，按 (user_name, model_name, days) 过滤
	// 后按 dst_model_name GROUP BY 一次性拉全量（无 Top 50 截断）。字段形状与 stage 4
	// model_distribution 完全一致（含 DstEndpointCount/TopDstEndpoints），前端可直接渲染。
	if req.Action == "model_distribution_full" {
		lsmHandleModelDistributionFull(w, req.UserName, req.ModelName, req.Days)
		return
	}

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}

	action := req.Action
	if action == "" {
		// v2.0.48 默认走 insights_summary：首屏仅拉 time_stats + tokens_stats，
		// protocol_stats / agent_stats 由前端点击对应卡片按需请求，避免 5 路并发 SQL 同时打同一分表。
		action = "insights_summary"
	}
	logger.Printf("[WEB] ChatAnalysisTotalInterface user=%s model=%s days=%d action=%s",
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
	// range_agent_dist 暂以 ChatAnalysisTotalRangeInterface 的 agent_dist 字段替代，统一走区间报告接口。

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
			var totalCount int64
			var sumElapsed, sumTTFB, sumGen int64
			for _, s := range tokensStats {
				totalInput += s.TokensInput
				totalOutput += s.TokensOutput
				totalAll += s.TokensTotal
				totalCount += s.Count
				sumElapsed += s.AvgElapsedMs * s.Count
				sumTTFB += s.AvgTTFBMs * s.Count
				sumGen += s.AvgGenerateMs * s.Count
			}
			var avgInput, avgOutput, avgAll uint64
			var avgElapsed, avgTTFB, avgGen int64
			if totalCount > 0 {
				avgInput = totalInput / uint64(totalCount)
				avgOutput = totalOutput / uint64(totalCount)
				avgAll = totalAll / uint64(totalCount)
				avgElapsed = sumElapsed / totalCount
				avgTTFB = sumTTFB / totalCount
				avgGen = sumGen / totalCount
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
		logger.Printf("[WARNING] ChatAnalysisTotalInterface time stats failed: %v", timeErr)
		timeStats = nil
	}
	if protocolErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface protocol stats failed: %v", protocolErr)
		protocolStats = nil
	}
	if tokensErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface tokens stats failed: %v", tokensErr)
		tokensStats = nil
	}
	if agentErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface agent stats failed: %v", agentErr)
		agentStats = nil
	}

	logger.Printf("[WEB] ChatAnalysisTotalInterface result: user=%s model=%s total=%d timeRanges=%d protocolSample=%d tokensDays=%d",
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

// lsmRunProtocolStats 按需读取：仅返回协议分析统计（点击"协议分析统计"卡片时调用）
// v2.0.48: 从全量接口拆出，避免首屏 5 路并发 SQL 同时打同一分表
func lsmRunProtocolStats(w http.ResponseWriter, userName, modelName string, days int) {
	// 协议分析统计与时间跨度无关（始终取最近 200 条有请求体的记录），days 参数仅保留签名兼容
	_ = days
	subTableNum := config.DEFAULT_SUB_TABLE_NUM
	if config.G != nil && config.G.DBMysqlSubTableNumber > 0 {
		subTableNum = config.G.DBMysqlSubTableNumber
	}
	protocolStats, protocolErr := modelsdb.GetProtocolAnalysisStats(userName, modelName, subTableNum, 200)
	if protocolErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface[protocol_stats] failed: %v", protocolErr)
		protocolStats = nil
	}
	logger.Printf("[WEB] ChatAnalysisTotalInterface[protocol_stats] user=%s model=%s sample=%d",
		userName, modelName, func() int {
			if protocolStats != nil {
				return protocolStats.SampleCount
			}
			return 0
		}())
	json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
		Success:       true,
		Message:       "查询成功",
		ProtocolStats: protocolStats,
	})
}

// lsmRunAgentStats 按需读取：仅返回 Agent 工具统计（点击"Agent 工具统计"卡片时调用）
// v2.0.48: 从全量接口拆出，避免首屏 5 路并发 SQL 同时打同一分表
func lsmRunAgentStats(w http.ResponseWriter, userName, modelName string, days int) {
	subTableNum := config.DEFAULT_SUB_TABLE_NUM
	if config.G != nil && config.G.DBMysqlSubTableNumber > 0 {
		subTableNum = config.G.DBMysqlSubTableNumber
	}
	agentStats, agentErr := modelsdb.GetAgentToolStatsByRange(userName, modelName, subTableNum, days)
	if agentErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface[agent_stats] failed: %v", agentErr)
		agentStats = nil
	}
	logger.Printf("[WEB] ChatAnalysisTotalInterface[agent_stats] user=%s model=%s days=%d tools=%d",
		userName, modelName, days, func() int {
			if agentStats != nil {
				return agentStats.UniqueTools
			}
			return 0
		}())
	json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
		Success:    true,
		Message:    "查询成功",
		AgentStats: agentStats,
	})
}

// lsmRunInsightsSummary 渐进式读取：仅返回时间 + Tokens，跳过协议分析 + Agent
// 预计对 100k 行表的首屏耗时从 8s 降至 2s 以内（仅 3 个并发 SQL）
// v2.0.51: 增加 per-query duration 日志 + 慢查询 warning，便于定位白屏根因
// v2.0.53: handler 层曾用 goroutine + time.After(25s) 做超时兜底。
// v2.0.54: 超时已下沉到 database.DB 层（statsDB() 绑定 25s context，超时时驱动真正 KILL 查询并释放连接），
//
//	因此这里**移除**了旧的「裸 goroutine + time.After 放弃等待」包装——那种写法超时后
//	底层 SQL 仍占着 MySQL 连接不释放，反复超时会耗尽连接池（SetMaxOpenConns=100）导致
//	「接口返回 200 但新请求一直转圈」。现在直接调用统计函数即可，超时会以
//	context.DeadlineExceeded 形式返回 error，据此置 timedOut 并返回明确文案。
func lsmRunInsightsSummary(w http.ResponseWriter, userName, modelName string, days int) {
	// 防御性：config.G 未初始化（极端测试环境）时用默认分表数，避免 nil panic。
	subTableNum := config.DEFAULT_SUB_TABLE_NUM
	if config.G != nil && config.G.DBMysqlSubTableNumber > 0 {
		subTableNum = config.G.DBMysqlSubTableNumber
	}
	var timeStats []modelsdb.TimeRangeStat
	var totalCount int64
	var tokensStats []modelsdb.TokensRangeStat
	var tokensModel []modelsdb.TokensModelStat
	var tokensLatency []modelsdb.TokensLatencyStat
	var tokensSummary *TokensSummaryStat
	var timeErr, tokensErr error
	var timedOut bool // v2.0.54: 任一查询触及 database.DB 层超时（context.DeadlineExceeded）时置位

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		start := time.Now()
		timeStats, timeErr = modelsdb.GetTimeRangeStats(userName, modelName, subTableNum, days)
		if isStatsTimeoutErr(timeErr) {
			timedOut = true
			logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] modelsdb.GetTimeRangeStats timeout (user=%s model=%s days=%d)", userName, modelName, days)
		}
		logStatsDuration("modelsdb.GetTimeRangeStats", userName, modelName, days, time.Since(start))
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		var countErr error
		totalCount, countErr = modelsdb.CountAgentHttpTransactions(userName, modelName, 0, subTableNum)
		if isStatsTimeoutErr(countErr) {
			timedOut = true
			logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] modelsdb.CountAgentHttpTransactions timeout (user=%s model=%s days=%d)", userName, modelName, days)
		}
		logStatsDuration("modelsdb.CountAgentHttpTransactions", userName, modelName, days, time.Since(start))
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		tokensStats, tokensErr = modelsdb.GetTokensRangeStats(userName, modelName, subTableNum, days)
		if isStatsTimeoutErr(tokensErr) {
			timedOut = true
			logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] modelsdb.GetTokensRangeStats timeout (user=%s model=%s days=%d)", userName, modelName, days)
		}
		logStatsDuration("modelsdb.GetTokensRangeStats", userName, modelName, days, time.Since(start))
		if tokensErr == nil {
			mStart := time.Now()
			var mErr, lErr error
			tokensModel, mErr = modelsdb.GetTokensModelStats(userName, modelName, subTableNum, days)
			tokensLatency, lErr = modelsdb.GetTokensLatencyStats(userName, modelName, subTableNum, days)
			if isStatsTimeoutErr(mErr) || isStatsTimeoutErr(lErr) {
				timedOut = true
				logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] modelsdb.GetTokensModelStats+Latency timeout (user=%s model=%s days=%d)", userName, modelName, days)
			}
			logStatsDuration("modelsdb.GetTokensModelStats+Latency", userName, modelName, days, time.Since(mStart))
			var totalInput, totalOutput, totalAll uint64
			var tc int64
			var sumElapsed, sumTTFB, sumGen int64
			for _, s := range tokensStats {
				totalInput += s.TokensInput
				totalOutput += s.TokensOutput
				totalAll += s.TokensTotal
				tc += s.Count
				sumElapsed += s.AvgElapsedMs * s.Count
				sumTTFB += s.AvgTTFBMs * s.Count
				sumGen += s.AvgGenerateMs * s.Count
			}
			var avgInput, avgOutput, avgAll uint64
			var avgElapsed, avgTTFB, avgGen int64
			if tc > 0 {
				avgInput = totalInput / uint64(tc)
				avgOutput = totalOutput / uint64(tc)
				avgAll = totalAll / uint64(tc)
				avgElapsed = sumElapsed / tc
				avgTTFB = sumTTFB / tc
				avgGen = sumGen / tc
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
	wg.Wait()

	if timeErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] time stats failed: %v", timeErr)
		timeStats = nil
	}
	if tokensErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] tokens stats failed: %v", tokensErr)
		tokensStats = nil
	}

	logger.Printf("[WEB] ChatAnalysisTotalInterface[insights_summary] user=%s model=%s total=%d timeRanges=%d tokensDays=%d",
		userName, modelName, totalCount, len(timeStats), len(tokensStats))

	// v2.0.53: 整体超时场景下，返回明确的超时消息，让前端区分「数据库慢」与「真没数据」。
	msg := "查询成功"
	if timedOut {
		msg = "查询超时（>25s），已返回部分结果。建议缩短时间跨度或联系管理员检查数据库索引。"
	}
	json.NewEncoder(w).Encode(ChatAnalysisTotalInterfaceResponse{
		Success:       !timedOut,
		Message:       msg,
		TimeStats:     timeStats,
		TotalCount:    totalCount,
		TokensStats:   tokensStats,
		TokensModel:   tokensModel,
		TokensLatency: tokensLatency,
		TokensSummary: tokensSummary,
		// ProtocolStats / AgentStats 故意省略：留到用户主动二次读取时再返回
	})
}

// logStatsDuration 统一的统计查询耗时日志：<=5s 打 WEB 常规日志，>5s 打 WARNING 慢查询日志
// v2.0.51: 配合前端 30s 超时，让运维能从日志定位到底是哪条 SQL 拖慢了首屏
func logStatsDuration(queryName, userName, modelName string, days int, elapsed time.Duration) {
	if elapsed > 5*time.Second {
		logger.Printf("[WARNING] ChatAnalysisTotalInterface[insights_summary] slow query: %s user=%s model=%s days=%d took %v",
			queryName, userName, modelName, days, elapsed)
	} else {
		logger.Printf("[WEB] ChatAnalysisTotalInterface[insights_summary] %s took %v", queryName, elapsed)
	}
}

// isStatsTimeoutErr 判断统计查询 error 是否由 database.DB 层超时（context 取消）触发。
// v2.0.54: statsDB() 绑定的 25s context 超时后，GORM 返回 context.DeadlineExceeded；
// go-sql-driver 网络层超时也会包成 context.Canceled / 含 "context" 字样的 error。
// 据此让 handler 返回明确的「查询超时」文案，而不是误报「无数据」。
func isStatsTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "invalid connection")
}

// ChatAnalysisTotalRangeReportRequest brush 选区生成报告的请求体
type ChatAnalysisTotalRangeReportRequest struct {
	UserName    string `json:"user_name"`
	ModelName   string `json:"model_name"`
	StartMs     int64  `json:"start_ms"`
	EndMs       int64  `json:"end_ms"`
	Granularity string `json:"granularity"`
}

// ChatAnalysisTotalRangeReportResponse brush 选区生成报告的响应体
type ChatAnalysisTotalRangeReportResponse struct {
	Success     bool                       `json:"success"`
	Message     string                     `json:"message"`
	RangeReport *modelsdb.TokensReportStat `json:"range_report,omitempty"`
	// AgentDist 区间内 agent 工具分布（与全局 agent_stats 相区分）
	AgentDist *modelsdb.AgentToolStatsResponse `json:"agent_dist,omitempty"`
}

// chatAnalysisTotalRangeReportHandle 处理 /ChatAnalysisTotalRangeInterface API
// 为前端 brush 选区提供基于任意时间区间 + 可选颗粒度的深度分析报告
// v2.0.46: 增加 ?stream=1 走 SSE 流式推送（progress 事件 → done 事件），保留原 JSON 同步模式作为兜底
func chatAnalysisTotalRangeReportHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		setNoCacheHeaders(w)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	// 先解析 body 拿到入参，决定走哪种响应模式
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

	// v2.0.46: stream=1 走 SSE 流式推送；其余走老的 JSON 同步路径（向后兼容）
	if r.URL.Query().Get("stream") == "1" {
		chatAnalysisTotalRangeReportStreamHandle(w, r, req)
		return
	}

	// --- 老的 JSON 同步路径（保持不变） ---
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if req.UserName == "" || req.ModelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "缺少 user_name 或 model_name 参数",
		})
		return
	}

	// 解析时间戳，并做合理性校验
	start := time.UnixMilli(req.StartMs)
	end := time.UnixMilli(req.EndMs)
	if req.StartMs <= 0 || req.EndMs <= 0 || !end.After(start) {
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "无效的时间区间: start_ms / end_ms 必须为正整数且 end_ms > start_ms",
		})
		return
	}
	// 最长允许 1 年，避免全表扫描
	if end.Sub(start) > 365*24*time.Hour {
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "时间区间过长: 最大支持 1 年 (365 天)",
		})
		return
	}

	// 校验并回落颗粒度
	granularity := modelsdb.NormalizeTokensGranularity(req.Granularity)

	logger.Printf("[WEB] ChatAnalysisTotalRangeReport user=%s model=%s range=%s~%s granularity=%s",
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
		// agent 后端目前仅支持 Days 维度，按区间天数近似换算（向上取整）
		dayCount := int(end.Sub(start).Hours()/24) + 1
		if dayCount > 365 {
			dayCount = 365
		}
		agentDist, agentErr = modelsdb.GetAgentToolStatsByRange(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, dayCount)
	}()
	wg.Wait()

	if rangeErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalRangeReport tokens failed: %v", rangeErr)
		json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
			Success: false,
			Message: "Tokens 区间统计失败: " + rangeErr.Error(),
		})
		return
	}
	if agentErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalRangeReport agent failed: %v", agentErr)
		agentDist = nil
	}

	logger.Printf("[WEB] ChatAnalysisTotalRangeReport result: user=%s model=%s buckets=%d count=%d",
		req.UserName, req.ModelName, len(rangeReport.Series), rangeReport.TotalCount)

	json.NewEncoder(w).Encode(ChatAnalysisTotalRangeReportResponse{
		Success:     true,
		Message:     "查询成功",
		RangeReport: rangeReport,
		AgentDist:   agentDist,
	})
}

// chatAnalysisTotalRangeReportStreamHandle v2.0.46 SSE 流式进度推送
// 复用 chatAnalysisTotalRangeReportRequest 入参；返回 text/event-stream
// 5 个 progress 阶段（validate → series → model_dist → latency_dist → agent_dist） + 1 个 done 事件
func chatAnalysisTotalRangeReportStreamHandle(w http.ResponseWriter, r *http.Request, req ChatAnalysisTotalRangeReportRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if req.UserName == "" || req.ModelName == "" {
		writeChatAnalysisRangeSSEError(w, flusher, "缺少 user_name 或 model_name 参数")
		return
	}

	start := time.UnixMilli(req.StartMs)
	end := time.UnixMilli(req.EndMs)
	if req.StartMs <= 0 || req.EndMs <= 0 || !end.After(start) {
		writeChatAnalysisRangeSSEError(w, flusher, "无效的时间区间: start_ms / end_ms 必须为正整数且 end_ms > start_ms")
		return
	}
	if end.Sub(start) > 365*24*time.Hour {
		writeChatAnalysisRangeSSEError(w, flusher, "时间区间过长: 最大支持 1 年 (365 天)")
		return
	}

	granularity := modelsdb.NormalizeTokensGranularity(req.Granularity)

	logger.Printf("[WEB] ChatAnalysisTotalRangeReport stream user=%s model=%s range=%s~%s granularity=%s",
		req.UserName, req.ModelName, start.Format("2006-01-02 15:04"), end.Format("2006-01-02 15:04"), granularity)

	// 1) validate 阶段
	writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
		"stage":   "validate",
		"title":   "校验时间区间",
		"message": start.Format("2006-01-02 15:04") + " ~ " + end.Format("2006-01-02 15:04") + " (" + granularity + ")",
		"percent": 5,
	})

	// 2) series 阶段：拉取时序数据
	writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
		"stage":   "series",
		"title":   "拉取时序数据",
		"message": "聚合时序桶中…",
		"percent": 15,
	})

	rangeReport, rangeErr := modelsdb.GetTokensRangeReport(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, start, end, granularity)
	if rangeErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalRangeReport stream tokens failed: %v", rangeErr)
		writeChatAnalysisRangeSSEError(w, flusher, "Tokens 区间统计失败: "+rangeErr.Error())
		return
	}

	// series 完成
	writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
		"stage":   "series",
		"title":   "拉取时序数据",
		"message": fmt.Sprintf("已聚合 %d 桶", len(rangeReport.Series)),
		"percent": 55,
		"done":    len(rangeReport.Series),
		"total":   len(rangeReport.Series),
	})

	// 3) model_dist 完成（已在 modelsdb.GetTokensRangeReport 内部聚合）
	writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
		"stage":   "model_dist",
		"title":   "聚合模型分布",
		"message": fmt.Sprintf("%d 个模型", len(rangeReport.ModelDist)),
		"percent": 70,
	})

	// 4) latency_dist 完成
	writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
		"stage":   "latency_dist",
		"title":   "聚时延分布",
		"message": fmt.Sprintf("%d 段", len(rangeReport.LatencyDist)),
		"percent": 82,
	})

	// 5) agent_dist 阶段
	writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
		"stage":   "agent_dist",
		"title":   "扫描 Agent 工具",
		"message": "扫描中…",
		"percent": 88,
	})
	dayCount := int(end.Sub(start).Hours()/24) + 1
	if dayCount > 365 {
		dayCount = 365
	}
	agentDist, agentErr := modelsdb.GetAgentToolStatsByRange(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, dayCount)
	if agentErr != nil {
		logger.Printf("[WARNING] ChatAnalysisTotalRangeReport stream agent failed: %v", agentErr)
		agentDist = nil
	}
	if agentDist != nil {
		writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
			"stage":   "agent_dist",
			"title":   "扫描 Agent 工具",
			"message": fmt.Sprintf("%d 种工具", agentDist.UniqueTools),
			"percent": 96,
		})
	} else {
		writeChatAnalysisRangeSSE(w, flusher, "progress", map[string]interface{}{
			"stage":   "agent_dist",
			"title":   "扫描 Agent 工具",
			"message": "（无 Agent 工具数据）",
			"percent": 96,
		})
	}

	// 6) done 事件，附带完整报告
	logger.Printf("[WEB] ChatAnalysisTotalRangeReport stream done user=%s model=%s buckets=%d count=%d",
		req.UserName, req.ModelName, len(rangeReport.Series), rangeReport.TotalCount)

	writeChatAnalysisRangeSSE(w, flusher, "done", map[string]interface{}{
		"stage": "done",
		"title": "完成汇总",
		"message": fmt.Sprintf("共 %d 桶 / %d 模型 / %d 段 / %d 工具", len(rangeReport.Series), len(rangeReport.ModelDist), len(rangeReport.LatencyDist), func() int {
			if agentDist != nil {
				return agentDist.UniqueTools
			}
			return 0
		}()),
		"percent":      100,
		"range_report": rangeReport,
		"agent_dist":   agentDist,
	})
}

// writeChatAnalysisRangeSSE 写入单个 SSE 事件（progress / done / error）
func writeChatAnalysisRangeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload map[string]interface{}) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if flusher != nil {
		flusher.Flush()
	}
}

// writeChatAnalysisRangeSSEError 写入 error 事件并以空 done 收尾，确保 EventSource 自动关闭
func writeChatAnalysisRangeSSEError(w http.ResponseWriter, flusher http.Flusher, msg string) {
	writeChatAnalysisRangeSSE(w, flusher, "error", map[string]interface{}{
		"stage":   "error",
		"error":   msg,
		"message": msg,
		"percent": 0,
	})
	writeChatAnalysisRangeSSE(w, flusher, "done", map[string]interface{}{
		"stage":   "done",
		"title":   "结束",
		"message": msg,
		"percent": 100,
	})
}

// lsmHandleFullHTTP 管理员端全量统计 HTTP fallback（v2.0.64）。
//
// 当管理员 Web 服务被应用网关代理（网关不支持 WebSocket Upgrade）时，前端自动
// fallback 到本接口，用 All 变体拉全站数据（与 WS 路径同语义）。
// 返回数据形状与 WS chunk 各 stage 完全对齐（kpi/time_stats/tokens_summary/
// model_distribution/trend_chart/protocol_stats/agent_stats），前端
// __lsmRenderStageHTML 可直接渲染。
func lsmHandleFullHTTP(w http.ResponseWriter, userName, modelName string, days int) {
	if days <= 0 {
		days = 7
	}
	if userName == "" || modelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisTotalFullHTTPResponse{
			Success: false,
			Message: "full_http 必须带 user_name + model_name（v2.0.68 校正：让 stage 4 等所有 user/model 维度统计落在正确上下文）",
		})
		return
	}
	subTableNum := config.DEFAULT_SUB_TABLE_NUM
	if config.G != nil && config.G.DBMysqlSubTableNumber > 0 {
		subTableNum = config.G.DBMysqlSubTableNumber
	}

	var (
		timeStats     []modelsdb.TimeRangeStat
		tokensStats   []modelsdb.TokensRangeStat
		modelDist     []modelsdb.ModelNameUsageStat
		trend         []modelsdb.DailyStat
		protocolStats *modelsdb.ProtocolAnalysisStats
		agentStats    *modelsdb.AgentToolStatsResponse
		totalCalls    int64
		totalTokens   uint64
		activeDays    int
		errs          []string
	)

	var wg sync.WaitGroup
	wg.Add(7)

	go func() {
		defer wg.Done()
		if v, err := modelsdb.GetTimeRangeStatsAll(subTableNum, days); err != nil {
			errs = append(errs, "time_stats:"+err.Error())
		} else {
			timeStats = v
		}
	}()
	go func() {
		defer wg.Done()
		if v, err := modelsdb.GetTokensRangeStatsAll(subTableNum, days); err != nil {
			errs = append(errs, "tokens_stats:"+err.Error())
		} else {
			tokensStats = v
		}
	}()
	go func() {
		defer wg.Done()
		// v2.0.68 校正：必须带 (user_name, model_name)，按 dst_model_name GROUP BY
		// 否则 stage 4 变全站 GROUP BY，切换本平台模型时数据不变
		if v, err := modelsdb.GetDstModelUsageStatsByUserModel(userName, modelName, subTableNum, days); err != nil {
			errs = append(errs, "model_dist:"+err.Error())
		} else {
			modelDist = v
		}
	}()
	go func() {
		defer wg.Done()
		if v, err := modelsdb.GetDailyStatsAll(subTableNum, days); err != nil {
			errs = append(errs, "trend:"+err.Error())
		} else {
			trend = v
		}
	}()
	go func() {
		defer wg.Done()
		if v, err := modelsdb.GetProtocolAnalysisStatsAll(subTableNum, 500); err != nil {
			errs = append(errs, "protocol_stats:"+err.Error())
		} else {
			protocolStats = v
		}
	}()
	go func() {
		defer wg.Done()
		if v, err := modelsdb.GetAgentToolStatsByRangeAll(subTableNum, days); err != nil {
			errs = append(errs, "agent_stats:"+err.Error())
		} else {
			agentStats = v
		}
	}()
	go func() {
		defer wg.Done()
		if v, tok, ad, err := modelsdb.GetAllStatsKPISummary(subTableNum, days); err != nil {
			errs = append(errs, "kpi:"+err.Error())
		} else {
			totalCalls = v
			totalTokens = tok
			activeDays = ad
		}
	}()
	wg.Wait()

	// 由 tokensStats 跨桶聚合出 tokens_summary
	tokensSummary := map[string]interface{}{
		"total_count":     0,
		"total_input":     uint64(0),
		"total_output":    uint64(0),
		"total_tokens":    uint64(0),
		"window_days":     days,
		"generated_at_ms": time.Now().UnixMilli(),
	}
	var tc int64
	var ti, to, tal uint64
	for _, s := range tokensStats {
		tc += s.Count
		ti += s.TokensInput
		to += s.TokensOutput
		tal += s.TokensTotal
	}
	tokensSummary["total_count"] = tc
	tokensSummary["total_input"] = ti
	tokensSummary["total_output"] = to
	tokensSummary["total_tokens"] = tal
	_ = time.Now()

	kpi := map[string]interface{}{
		"total_calls":     totalCalls,
		"total_tokens":    totalTokens,
		"active_models":   len(modelDist),
		"active_days":     activeDays,
		"window_days":     days,
		"model_name":      "",
		"warnings":        []string{},
		"generated_at_ms": time.Now().UnixMilli(),
	}

	msg := "查询成功"
	if len(errs) > 0 {
		msg = "部分查询失败"
		logger.Printf("[WARNING] ChatAnalysisTotal[full_http] 部分失败: %v", errs)
	}

	json.NewEncoder(w).Encode(ChatAnalysisTotalFullHTTPResponse{
		Success:       true,
		Message:       msg,
		Days:          days,
		KPI:           kpi,
		TimeStats:     timeStats,
		TokensSummary: tokensSummary,
		ModelDist:     modelDist,
		Trend:         trend,
		ProtocolStats: protocolStats,
		AgentStats:    agentStats,
	})
}

// ChatAnalysisTotalModelDistFullResponse v2.0.68 stage 4 全量数据响应
type ChatAnalysisTotalModelDistFullResponse struct {
	Success       bool                          `json:"success"`
	Message       string                        `json:"message"`
	Days          int                           `json:"days"`
	Models        []modelsdb.ModelNameUsageStat `json:"models"`
	GeneratedAtMs int64                         `json:"generated_at_ms"`
}

// lsmHandleModelDistributionFull v2.0.68: 返回完整 stage 4 model_distribution 数据
// （无 50 行上限），用于用户在 stage 4 表格点击"加载全部"按钮时调用。
//
// v2.0.68 校正：必须带 (user_name, model_name)，按 dst_model_name GROUP BY。
// 否则 stage 4 变全站 GROUP BY，切换本平台模型时数据不变（用户反馈的核心痛点）。
func lsmHandleModelDistributionFull(w http.ResponseWriter, userName, modelName string, days int) {
	if days <= 0 {
		days = 7
	}
	if userName == "" || modelName == "" {
		json.NewEncoder(w).Encode(ChatAnalysisTotalModelDistFullResponse{
			Success: false,
			Message: "model_distribution_full 必须带 user_name + model_name",
		})
		return
	}
	subTableNum := config.DEFAULT_SUB_TABLE_NUM
	if config.G != nil && config.G.DBMysqlSubTableNumber > 0 {
		subTableNum = config.G.DBMysqlSubTableNumber
	}

	modelStats, err := modelsdb.GetDstModelUsageStatsByUserModel(userName, modelName, subTableNum, days)
	if err != nil {
		logger.Printf("[WARNING] ChatAnalysisTotal[model_distribution_full] failed: %v", err)
	}
	if modelStats == nil {
		modelStats = []modelsdb.ModelNameUsageStat{}
	}

	logger.Printf("[WEB] ChatAnalysisTotal[model_distribution_full] user=%s model=%s days=%d count=%d",
		userName, modelName, days, len(modelStats))

	json.NewEncoder(w).Encode(ChatAnalysisTotalModelDistFullResponse{
		Success:       true,
		Message:       "查询成功",
		Days:          days,
		Models:        modelStats,
		GeneratedAtMs: time.Now().UnixMilli(),
	})
}
