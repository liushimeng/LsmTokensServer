package api

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"sync"
)

// agentInfoStatsData Agent 信息统计响应数据
type agentInfoStatsData struct {
	Summary *modelsdb.AgentInfoUsageSummary `json:"summary"`
	Agents  []modelsdb.AgentInfoUsageStat   `json:"agents"`
	Trend   []modelsdb.DailyStat            `json:"trend,omitempty"`
}

// agentInfoInterfaceHandle 管理员 Agent 信息统计 API（全站维度）
func agentInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}

	var req struct {
		Action string `json:"action"`
		Days   int    `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "", "stats":
		var summary *modelsdb.AgentInfoUsageSummary
		var agents []modelsdb.AgentInfoUsageStat
		var trend []modelsdb.DailyStat
		var statsErr, trendErr error

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			summary, agents, statsErr = modelsdb.GetAgentInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, req.Days)
		}()
		go func() {
			defer wg.Done()
			trend, trendErr = modelsdb.GetDailyStatsAll(config.G.DBMysqlSubTableNumber, req.Days)
		}()
		wg.Wait()

		if statsErr != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: statsErr.Error()})
			return
		}
		if trendErr != nil {
			logger.Printf("[WARNING] AgentInfoInterface trend stats failed: %v", trendErr)
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: "查询成功",
			Data:    agentInfoStatsData{Summary: summary, Agents: agents, Trend: trend},
		})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}
