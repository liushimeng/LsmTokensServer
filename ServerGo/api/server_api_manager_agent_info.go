package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
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
		Action    string `json:"action"`
		Days      int    `json:"days"`
		Hours     int    `json:"hours"`     // trend 用：1~720；<=0 视为 24
		UserName  string `json:"user_name"` // 阶段BV：可选；指定后按单用户单模型视角聚合
		ModelName string `json:"model_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}
	req.UserName = strings.TrimSpace(req.UserName)
	req.ModelName = strings.TrimSpace(req.ModelName)
	// 阶段BV：仅 user_name + model_name 同时指定才走单用户单模型分支；
	// 仅指定 user_name 不指定 model_name 时回退全站聚合（避免无索引大表扫描失控）
	scoped := req.UserName != "" && req.ModelName != ""

	switch req.Action {
	case "", "stats":
		var summary *modelsdb.AgentInfoUsageSummary
		var agents []modelsdb.AgentInfoUsageStat
		var trend []modelsdb.DailyStat
		var statsErr, trendErr error

		var wg sync.WaitGroup
		wg.Add(2)
		if scoped {
			// 单用户单模型视角无「日折线」趋势（K 线由 trend action 提供）
			trend = []modelsdb.DailyStat{}
			wg.Add(-1) // 不再等待 trend 协程
			go func() {
				defer wg.Done()
				summary, agents, statsErr = modelsdb.GetAgentInfoUsageStatsByUserModel(
					req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Days)
			}()
		} else {
			go func() {
				defer wg.Done()
				summary, agents, statsErr = modelsdb.GetAgentInfoUsageStatsAll(config.G.DBMysqlSubTableNumber, req.Days)
			}()
			go func() {
				defer wg.Done()
				trend, trendErr = modelsdb.GetDailyStatsAll(config.G.DBMysqlSubTableNumber, req.Days)
			}()
		}
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
	case "trend":
		// 小时粒度 K 线图数据：与 stats 分离，前端按窗口分批请求 24h/72h/168h/720h。
		var res *modelsdb.HourlyTrendResult
		var err error
		if scoped {
			res, err = modelsdb.GetHourlyTrendByUserModel(req.UserName, req.ModelName, config.G.DBMysqlSubTableNumber, req.Hours)
		} else {
			res, err = modelsdb.GetHourlyTrendAll(config.G.DBMysqlSubTableNumber, req.Hours)
		}
		if err != nil {
			logger.Printf("[WARNING] AgentInfoInterface trend hourly failed: %v", err)
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Message: "查询成功",
			Data:    res,
		})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}
