// v2.0.60: /ChatAnalysisTotalWS 单次分页扫描 + 全维度增量流式聚合
//
// 背景（用户反馈：页面一直「加载中」，数据量大时首屏卡死）：
//
//	v2.0.55–v2.0.59 的 runChatStatsQuery 把 7 个维度拆成 7 次「各自完整扫全 8 张分表」
//	的独立查询，并在维度之间人为 sleep 1s。数据量大时（生产 160K+ 行 × 8 表）：
//	  - 每个维度都要重新分页扫一遍全表 → 总扫描量是单遍的 ~5 倍（KPI 额外还 COUNT+SUM+DISTINCT）
//	  - 累计耗时轻易超过 database.StatsDB() 25s ctx → 靠后的维度（trend/protocol/agent）直接被
//	    ctx 取消、never 推送 chunk → 对应卡片永远停在「加载中」
//	  - 「先扫完整个维度再推一块」= 前端要等一个维度整段扫完才有第一块数据
//
// v2.0.60 重构核心思路：「一次扫描，全维度同步聚合，按批增量推送累计快照」
//  1. 只做一遍 keyset 分页遍历（8 张分表，每批 modelsdb.StatsShardScanBatch 行）
//  2. 每一批 5000 行同时喂给 7 个维度的累加器（chatStatsAggregator）
//  3. 每处理若干批就把「当前累计快照」序列化成 7 个 stage 的 chunk 推给前端
//     → 前端第一批（几十 ms）就能把 7 张卡全部填上，之后随扫描进度实时刷新数字
//  4. 扫描完成后推最终快照 + done
//
// 好处：
//   - 首屏「立刻」有数据（第一批就填满 7 卡），不再等 7×全表
//   - 总 DB 工作量降到单遍扫描（内存有界，keyset 分页）
//   - 不会再出现「靠后维度被 ctx 取消 → 卡片永久加载中」
//   - 完全兼容既有前端 __lsmRenderStageHTML 的 7 个 stage 数据形状（增量快照与最终形状一致）
package websocket

import (
	"context"
	"errors"
	"fmt"
	config "github.com/lishimeng/LsmTokensServer/config"
	models "github.com/lishimeng/LsmTokensServer/models"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// streamScanRow 单遍扫描读取的行结构。仅含小字段（禁含 8 个 longtext，v2.0.42 白名单）。
// 覆盖 7 个维度所需的全部列；一次 SELECT 拉齐，避免多遍扫描。
// v2.0.68: 增加 user_name / dst_endpoint_id / dst_model_name 三列以支撑 stage 4 model_distribution 的
// 源站维度（按 dst_model_name 聚合，运维视角：本平台模型下实际请求到了哪些源站模型）。
// 全部是小字段（varchar(50) + uint64 + varchar(64)），不引入 longtext（CLAUDE.md 强制规则）。
type streamScanRow struct {
	ID                    uint64    `gorm:"column:id"`
	CreatedAt             time.Time `gorm:"column:created_at"`
	ModelName             string    `gorm:"column:model_name"`
	TokensInputSize       uint64    `gorm:"column:tokens_input_size"`
	TokensOutputSize      uint64    `gorm:"column:tokens_output_size"`
	TokensAllSize         uint64    `gorm:"column:tokens_all_size"`
	ElapsedMs             int64     `gorm:"column:elapsed_ms"`
	AgentToolName         string    `gorm:"column:agent_tool_name"`
	RequestMethod         string    `gorm:"column:request_method"`
	RequestURL            string    `gorm:"column:request_url"`
	ResponseStatus        string    `gorm:"column:response_status"`
	RequestContentLength  uint64    `gorm:"column:request_content_length"`
	ResponseContentLength uint64    `gorm:"column:response_content_length"`
	TaskModel             string    `gorm:"column:task_model"`
	IsStream              bool      `gorm:"column:is_stream"`
	HasSystemPrompt       bool      `gorm:"column:has_system_prompt"`
	HasToolCall           bool      `gorm:"column:has_tool_call"`
	UserMessageCount      int       `gorm:"column:user_message_count"`
	UserName              string    `gorm:"column:user_name"`       // v2.0.68 新增：用于 UserCount
	DstEndpointID         uint64    `gorm:"column:dst_endpoint_id"` // v2.0.68 新增：用于 DstEndpointCount/TopDstEndpoints
	DstModelName          string    `gorm:"column:dst_model_name"`  // v2.0.68 新增：stage 4 按此聚合（运维视角下的「目标源站模型分布」）
}

// streamScanColumns 单遍扫描的 SELECT 列表（含 id 作 keyset 游标；均为非 longtext 白名单列）。
// v2.0.68: 增加 user_name / dst_endpoint_id / dst_model_name（全部小字段，未引入 longtext）。
const streamScanColumns = "id, created_at, model_name, tokens_input_size, tokens_output_size, " +
	"tokens_all_size, elapsed_ms, agent_tool_name, request_method, request_url, response_status, " +
	"request_content_length, response_content_length, task_model, is_stream, has_system_prompt, " +
	"has_tool_call, user_message_count, user_name, dst_endpoint_id, dst_model_name"

// dstModelAgg 单个目标源站模型的增量累加器（v2.0.68 重命名 + 校正维度语义）
//
// 历史：v2.0.68 初版以「本平台模型名（model_name）」聚合（modelAgg），但这与页面顶部
// 已经选择的 (user_name, model_name) 上下文无关 —— 全站 GROUP BY 导致切换本平台模型
// 时 stage 4 数据不变，根本无法用于"本平台模型视角下的源站真实使用情况"分析。
//
// 校正：运维真正想看的是「同一本平台 model 下，实际请求到了哪些 dst_model_name
// （目标源站模型）」。比如本平台模型 `claude-haiku-4-5` 配置了 3 个源站，分别挂
// `claude-3-5-haiku-20241022` / `claude-haiku-4-5-20251001` / 等等 —— 运维要看到
// 这 3 个源站模型各自的调用次数/Tokens/占比，以便判断真实流量分布与源站健康度。
type dstModelAgg struct {
	name         string
	callCount    int64
	tokensInput  uint64
	tokensOutput uint64
	tokensTotal  uint64
	// v2.0.68：源站去重 + 源站→调用次数（Top 3 + DstEndpointCount）
	dstEndpointSet   map[uint64]struct{}
	dstEndpointCalls map[uint64]int64
}

// toolAgg 单个 Agent 工具的增量累加器
type toolAgg struct {
	name      string
	count     int64
	firstSeen time.Time
	lastSeen  time.Time
}

// chatStatsAggregator 7 个维度共享的单遍增量累加器。
// 一次扫描把每行同时喂给它，任意时刻都能 snapshot 出 7 个 stage 的当前累计数据。
// 阶段 7.5 新增：error_stats（错误率维度）聚合。
type chatStatsAggregator struct {
	days       int
	timeIsHour bool
	timeFmt    string

	// 全局计数（KPI / tokens_summary）
	totalCalls   int64
	totalInput   uint64
	totalOutput  uint64
	totalTokens  uint64
	totalElapsed int64

	// time_stats：时间桶 → 调用次数
	timeBuckets map[string]int64
	// trend_chart / tokens_summary 天桶：天 → (count,in,out,all,elapsed)
	dayBuckets map[string]*dayBucket
	// active_days：出现过的日期集合
	activeDays map[string]struct{}

	// model_distribution（v2.0.68 校正）：按 dst_model_name 聚合
	// 在「本平台 (user_name, model_name) 上下文」下的真实源站模型分布
	dstModels map[string]*dstModelAgg
	// agent_stats：agent_tool_name → 累加器
	tools map[string]*toolAgg

	// protocol_stats（全站取样：本实现改为全量累加，语义比原「最近 N 条取样」更准确）
	methodStats    map[string]int64
	urlStats       map[string]int64
	statusStats    map[string]int64
	modelStatsP    map[string]int64
	streamCount    int64
	nonStreamCount int64
	hasSystem      int64
	hasTool        int64
	multiTurn      int64
	singleTurn     int64
	minElapsed     int64
	maxElapsed     int64
	protoSamples   int64
	reqSizeTotal   int64
	respSizeTotal  int64

	// error_stats（阶段 7.5 新增）：按状态码分桶统计错误率
	error2xx   int64 // 2xx 成功
	error4xx   int64 // 4xx 客户端错误
	error5xx   int64 // 5xx 服务端错误
	errorNet   int64 // 网络错误（状态码为空或非标准）
}

type dayBucket struct {
	count     int64
	inTokens  uint64
	outTokens uint64
	allTokens uint64
	elapsedMs int64
}

// displayDays 把统一 span 编码换算成「展示用天数」：span>0 取天数；span<0（小时窗口）
// 向上取整天数（至少 1，语义为「今天至今」）；0 保持 0（无限制）。
func (a *chatStatsAggregator) displayDays() int {
	if a.days > 0 {
		return a.days
	}
	if a.days < 0 {
		d := (modelsdb.SpanHours(a.days) + 23) / 24
		if d < 1 {
			d = 1
		}
		return d
	}
	return 0
}

// newChatStatsAggregator 构造累加器；timeIsHour 决定 time_stats 桶粒度（与旧 models.GetTimeRangeStatsAll 对齐）。
// 20260826：days 为统一 span 编码（负值=最近 N 小时）。
func newChatStatsAggregator(days int) *chatStatsAggregator {
	timeIsHour := true
	if modelsdb.SpanHours(days) > modelsdb.TimeStatsMaxDays*24 {
		timeIsHour = false
	}
	timeFmt := "2006-01-02 15:04"
	if !timeIsHour {
		timeFmt = "2006-01-02"
	}
	return &chatStatsAggregator{
		days:        days,
		timeIsHour:  timeIsHour,
		timeFmt:     timeFmt,
		timeBuckets: make(map[string]int64),
		dayBuckets:  make(map[string]*dayBucket),
		activeDays:  make(map[string]struct{}),
		dstModels:   make(map[string]*dstModelAgg),
		tools:       make(map[string]*toolAgg),
		methodStats: make(map[string]int64),
		urlStats:    make(map[string]int64),
		statusStats: make(map[string]int64),
		modelStatsP: make(map[string]int64),
		minElapsed:  -1,
		maxElapsed:  -1,
	}
}

// addRow 把单行喂给全部 7 个维度累加器
func (a *chatStatsAggregator) addRow(r *streamScanRow) {
	// 全局
	a.totalCalls++
	a.totalInput += r.TokensInputSize
	a.totalOutput += r.TokensOutputSize
	a.totalTokens += r.TokensAllSize
	a.totalElapsed += r.ElapsedMs

	// time_stats 桶
	a.timeBuckets[r.CreatedAt.Format(a.timeFmt)]++

	// trend / tokens 天桶
	dayKey := r.CreatedAt.Format("2006-01-02")
	a.activeDays[dayKey] = struct{}{}
	db := a.dayBuckets[dayKey]
	if db == nil {
		db = &dayBucket{}
		a.dayBuckets[dayKey] = db
	}
	db.count++
	db.inTokens += r.TokensInputSize
	db.outTokens += r.TokensOutputSize
	db.allTokens += r.TokensAllSize
	db.elapsedMs += r.ElapsedMs

	// model_distribution（v2.0.68 校正）：按 dst_model_name 聚合
	// 在「本平台 (user_name, model_name) 上下文」下的真实源站模型分布
	if mn := strings.TrimSpace(r.DstModelName); mn != "" {
		m := a.dstModels[mn]
		if m == nil {
			m = &dstModelAgg{
				name:             mn,
				dstEndpointSet:   make(map[uint64]struct{}),
				dstEndpointCalls: make(map[uint64]int64),
			}
			a.dstModels[mn] = m
		}
		m.callCount++
		m.tokensInput += r.TokensInputSize
		m.tokensOutput += r.TokensOutputSize
		m.tokensTotal += r.TokensAllSize
		if r.DstEndpointID > 0 {
			m.dstEndpointSet[r.DstEndpointID] = struct{}{}
			m.dstEndpointCalls[r.DstEndpointID]++
		}
	}

	// agent_stats
	if tn := r.AgentToolName; tn != "" && tn != "unknown" {
		t := a.tools[tn]
		if t == nil {
			t = &toolAgg{name: tn, firstSeen: r.CreatedAt, lastSeen: r.CreatedAt}
			a.tools[tn] = t
		}
		t.count++
		if r.CreatedAt.Before(t.firstSeen) {
			t.firstSeen = r.CreatedAt
		}
		if r.CreatedAt.After(t.lastSeen) {
			t.lastSeen = r.CreatedAt
		}
	}

	// protocol_stats（全量累加）
	a.protoSamples++
	a.methodStats[r.RequestMethod]++
	urlPath := r.RequestURL
	if idx := strings.LastIndex(urlPath, "?"); idx > 0 {
		urlPath = urlPath[:idx]
	}
	a.urlStats[urlPath]++
	a.statusStats[r.ResponseStatus]++
	if r.TaskModel != "" {
		a.modelStatsP[r.TaskModel]++
	}
	if r.IsStream {
		a.streamCount++
	} else {
		a.nonStreamCount++
	}
	if r.HasSystemPrompt {
		a.hasSystem++
	}
	if r.HasToolCall {
		a.hasTool++
	}
	if r.UserMessageCount > 1 {
		a.multiTurn++
	} else {
		a.singleTurn++
	}
	if a.minElapsed < 0 || r.ElapsedMs < a.minElapsed {
		a.minElapsed = r.ElapsedMs
	}
	if a.maxElapsed < 0 || r.ElapsedMs > a.maxElapsed {
		a.maxElapsed = r.ElapsedMs
	}
	a.reqSizeTotal += int64(r.RequestContentLength)
	a.respSizeTotal += int64(r.ResponseContentLength)

	// error_stats（阶段 7.5 新增）：按状态码分桶统计
	status := strings.TrimSpace(r.ResponseStatus)
	if status == "" {
		a.errorNet++
	} else if strings.HasPrefix(status, "2") {
		a.error2xx++
	} else if strings.HasPrefix(status, "4") {
		a.error4xx++
	} else if strings.HasPrefix(status, "5") {
		a.error5xx++
	} else {
		// 非标准状态码（如网络错误导致的空响应）
		a.errorNet++
	}
}

// ---- 快照函数：任意时刻把当前累计状态转成各 stage 的数据形状 ----

// snapshotKPI KPI 卡（与 lsmBuildChatStatsKPI 输出形状一致）
func (a *chatStatsAggregator) snapshotKPI(modelName string) map[string]interface{} {
	// v2.0.68 校正：active_models 改用 dstModels 计数（即活跃目标源站模型数）
	activeModels := len(a.dstModels)
	if activeModels == 0 && len(a.activeDays) > 0 {
		activeModels = len(a.activeDays)
	}
	return map[string]interface{}{
		"total_calls":     a.totalCalls,
		"total_tokens":    a.totalTokens,
		"active_models":   activeModels,
		"active_days":     len(a.activeDays),
		"window_days":     a.displayDays(),
		"model_name":      modelName,
		"warnings":        []string{},
		"generated_at_ms": nowUnixMilliSafe(),
	}
}

// snapshotTimeStats 时间分布（补齐空槽位，与 models.GetTimeRangeStatsAll 一致）
func (a *chatStatsAggregator) snapshotTimeStats() []models.TimeRangeStat {
	var stats []models.TimeRangeStat
	now := time.Now()
	if a.timeIsHour {
		fillHours := modelsdb.SpanHours(a.days)
		if fillHours <= 0 {
			fillHours = 24 // span=0 时按当天 24 小时
		}
		startTime := now.Add(-time.Duration(fillHours) * time.Hour).Truncate(time.Hour)
		for t := startTime; !t.After(now); t = t.Add(time.Hour) {
			key := t.Format(a.timeFmt)
			stats = append(stats, models.TimeRangeStat{Date: key, Count: a.timeBuckets[key]})
		}
	} else {
		spanDays := a.displayDays()
		if spanDays <= 0 {
			spanDays = 30
		}
		for i := spanDays - 1; i >= 0; i-- {
			key := now.AddDate(0, 0, -i).Format(a.timeFmt)
			stats = append(stats, models.TimeRangeStat{Date: key, Count: a.timeBuckets[key]})
		}
	}
	return stats
}

// snapshotTokensSummary Tokens 概览（与 lsmBuildChatStatsTokensSummary 输出形状一致）
func (a *chatStatsAggregator) snapshotTokensSummary() map[string]interface{} {
	buckets := a.snapshotDaily(func(d *dayBucket, key string) models.TokensRangeStat {
		return models.TokensRangeStat{
			Date: key, Count: d.count,
			TokensInput: d.inTokens, TokensOutput: d.outTokens,
			TokensTotal: d.allTokens, AvgElapsedMs: d.elapsedMs,
		}
	})
	return map[string]interface{}{
		"buckets":         buckets,
		"total_count":     a.totalCalls,
		"total_input":     a.totalInput,
		"total_output":    a.totalOutput,
		"total_tokens":    a.totalTokens,
		"window_days":     a.displayDays(),
		"generated_at_ms": nowUnixMilliSafe(),
	}
}

// snapshotDaily 补齐天槽位的通用 helper
func (a *chatStatsAggregator) snapshotDaily(conv func(*dayBucket, string) models.TokensRangeStat) []models.TokensRangeStat {
	spanDays := a.displayDays()
	if spanDays <= 0 {
		spanDays = 30
	}
	now := time.Now()
	out := make([]models.TokensRangeStat, 0, spanDays)
	for i := spanDays - 1; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format("2006-01-02")
		d := a.dayBuckets[key]
		if d == nil {
			out = append(out, models.TokensRangeStat{Date: key})
			continue
		}
		out = append(out, conv(d, key))
	}
	return out
}

// snapshotTrend 时序折线（models.DailyStat，补齐天槽位）
func (a *chatStatsAggregator) snapshotTrend() []models.DailyStat {
	spanDays := a.displayDays()
	if spanDays <= 0 {
		spanDays = 30
	}
	now := time.Now()
	out := make([]models.DailyStat, 0, spanDays)
	for i := spanDays - 1; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format("2006-01-02")
		d := a.dayBuckets[key]
		if d == nil {
			out = append(out, models.DailyStat{Date: key})
			continue
		}
		out = append(out, models.DailyStat{
			Date: key, Count: d.count,
			TokensInput: d.inTokens, TokensOutput: d.outTokens, TokensTotal: d.allTokens,
		})
	}
	return out
}

// snapshotModelDist 目标源站模型分布（v2.0.68 校正：按 dst_model_name 聚合）
//
// 在「本平台 (user_name, model_name) 上下文」下的真实源站模型分布。运维真正想看的
// 是：本平台模型下，实际请求到了哪些 dst_model_name（目标源站模型），各自调用
// 了多少 / Tokens 多少 / 占多少比例。例如本平台 `claude-haiku-4-5` 配置了 3 个
// 源站分别挂 `claude-3-5-haiku-20241022` / `claude-haiku-4-5-20251001` /
// `claude-haiku-4-5` 时，本函数输出这 3 个 dst_model_name 的分布。
//
// 输出形状保持 models.ModelNameUsageStat 兼容前端 stage 4 渲染（ModelName 字段此时承载
// 的是 dst_model_name；UserCount 始终为 0 —— 单 (user, model) 上下文下用户去重
// 无意义，前端可据此渲染 "—"）。
func (a *chatStatsAggregator) snapshotModelDist() []models.ModelNameUsageStat {
	stats := make([]models.ModelNameUsageStat, 0, len(a.dstModels))
	for _, m := range a.dstModels {
		// Top 3 源站（按 call_count desc → id asc）
		var topDst []models.DstEndpointUsage
		if len(m.dstEndpointCalls) > 0 {
			topDst = make([]models.DstEndpointUsage, 0, len(m.dstEndpointCalls))
			for id, cnt := range m.dstEndpointCalls {
				topDst = append(topDst, models.DstEndpointUsage{DstEndpointID: id, CallCount: cnt})
			}
			sort.Slice(topDst, func(i, j int) bool {
				if topDst[i].CallCount != topDst[j].CallCount {
					return topDst[i].CallCount > topDst[j].CallCount
				}
				return topDst[i].DstEndpointID < topDst[j].DstEndpointID
			})
			if len(topDst) > 3 {
				topDst = topDst[:3]
			}
		}

		stats = append(stats, models.ModelNameUsageStat{
			ModelName:        m.name, // 此时为 dst_model_name
			CallCount:        m.callCount,
			TokensInput:      m.tokensInput,
			TokensOutput:     m.tokensOutput,
			TokensTotal:      m.tokensTotal,
			UserCount:        0, // 单 user 上下文下恒为 0
			DstEndpointCount: len(m.dstEndpointSet),
			TopDstEndpoints:  topDst,
		})
	}
	for i := range stats {
		if a.totalCalls > 0 {
			stats[i].CallShare = float64(stats[i].CallCount) / float64(a.totalCalls) * 100.0
		}
		if a.totalTokens > 0 {
			stats[i].TokenShare = float64(stats[i].TokensTotal) / float64(a.totalTokens) * 100.0
		}
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].CallCount != stats[j].CallCount {
			return stats[i].CallCount > stats[j].CallCount
		}
		if stats[i].TokensTotal != stats[j].TokensTotal {
			return stats[i].TokensTotal > stats[j].TokensTotal
		}
		return stats[i].ModelName < stats[j].ModelName
	})
	return stats
}

// snapshotProtocol 协议分析（与 models.ProtocolAnalysisStats 形状一致；全量累加）
func (a *chatStatsAggregator) snapshotProtocol() *models.ProtocolAnalysisStats {
	p := &models.ProtocolAnalysisStats{
		MethodStats:     cloneInt64Map(a.methodStats),
		URLPatternStats: cloneInt64Map(a.urlStats),
		StatusStats:     cloneInt64Map(a.statusStats),
		ModelStats:      cloneInt64Map(a.modelStatsP),
		StreamCount:     a.streamCount,
		NonStreamCount:  a.nonStreamCount,
		HasSystemPrompt: a.hasSystem,
		HasToolCall:     a.hasTool,
		MultiTurnCount:  a.multiTurn,
		SingleTurnCount: a.singleTurn,
		SampleCount:     int(a.protoSamples),
		SampleLimit:     0, // 0 = 全量（非取样）
	}
	if a.protoSamples > 0 {
		p.AvgElapsedMs = a.totalElapsed / a.protoSamples
		p.MinElapsedMs = a.minElapsed
		p.MaxElapsedMs = a.maxElapsed
		p.AvgReqSize = a.reqSizeTotal / a.protoSamples
		p.AvgRespSize = a.respSizeTotal / a.protoSamples
	} else {
		p.MinElapsedMs = 0
		p.MaxElapsedMs = 0
	}
	return p
}

// snapshotAgent Agent 工具统计（按调用次数降序 → name 升序）
func (a *chatStatsAggregator) snapshotAgent() *models.AgentToolStatsResponse {
	var totalCount int64
	for _, t := range a.tools {
		totalCount += t.count
	}
	stats := make([]models.AgentToolStat, 0, len(a.tools))
	for _, t := range a.tools {
		var pct float64
		if totalCount > 0 {
			pct = float64(t.count) / float64(totalCount) * 100.0
		}
		stats = append(stats, models.AgentToolStat{
			AgentToolName: t.name,
			Count:         t.count,
			FirstSeenAt:   t.firstSeen.Format("2006-01-02 15:04:05"),
			LastSeenAt:    t.lastSeen.Format("2006-01-02 15:04:05"),
			Percentage:    pct,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Count != stats[j].Count {
			return stats[i].Count > stats[j].Count
		}
		return stats[i].AgentToolName < stats[j].AgentToolName
	})
	return &models.AgentToolStatsResponse{
		TotalAgentCount: totalCount,
		UniqueTools:     len(a.tools),
		ToolStats:       stats,
	}
}

// ---- 阶段 7.5 新增：错误率快照 ----

// ErrorStats 错误率统计（阶段 7.5 新增）
type ErrorStats struct {
	TotalCalls int64   `json:"total_calls"`
	Success2xx int64   `json:"success_2xx"`
	Error4xx   int64   `json:"error_4xx"`
	Error5xx   int64   `json:"error_5xx"`
	ErrorNet   int64   `json:"error_net"` // 网络错误（状态码为空或非标准)
	SuccessPct float64 `json:"success_pct"`
	ErrorPct   float64 `json:"error_pct"`
}

// snapshotErrorStats 错误率快照（阶段 7.5 新增）
func (a *chatStatsAggregator) snapshotErrorStats() *ErrorStats {
	total := a.totalCalls
	var successPct, errorPct float64
	if total > 0 {
		successPct = float64(a.error2xx) / float64(total) * 100.0
		errorPct = float64(a.error4xx+a.error5xx+a.errorNet) / float64(total) * 100.0
	}
	return &ErrorStats{
		TotalCalls: total,
		Success2xx: a.error2xx,
		Error4xx:   a.error4xx,
		Error5xx:   a.error5xx,
		ErrorNet:   a.errorNet,
		SuccessPct: successPct,
		ErrorPct:   errorPct,
	}
}

// cloneInt64Map 复制 map（快照与后续累加隔离，避免前端持有的引用被后续 mutate）
func cloneInt64Map(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// nowUnixMilliSafe 返回当前毫秒时间戳（封装便于测试；生产直接用 time.Now）
func nowUnixMilliSafe() int64 { return time.Now().UnixMilli() }

// streamChatStats 单遍分页扫描 8 张分表，按批调用 onBatch 推送累计快照。
//
// v2.0.68 校正：增加 userName/modelName 入参，在 WHERE 中限定「本平台用户的某个模型」
// 上下文。否则 stage 4 会变成全站 GROUP BY，切换本平台模型时数据完全不变（用户反馈）。
//
// onBatch(scannedRows) 在每处理完 snapshotEveryBatches 批（或每张分表结束、或最终）时被调用，
// 调用方据此把累加器的当前快照序列化为 chunk 推给前端。
// 返回 (扫描总行数, timedOut, err)。ctx 取消视为 timedOut（返回已累计的部分结果）。
func streamChatStats(
	ctx context.Context,
	sdb *gorm.DB,
	subTableNum int,
	agg *chatStatsAggregator,
	userName string,
	modelName string,
	onBatch func(scanned int64),
) (int64, bool, error) {
	if sdb == nil {
		return 0, false, nil
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}

	// 20260826：days 升级为统一 span 编码（负值=最近 N 小时），cutoff 统一走 models.SpanCutoffTime
	var cutoff time.Time
	filterTime := false
	if agg.days != 0 {
		cutoff, filterTime = modelsdb.SpanCutoffTime(agg.days)
	}

	// v2.0.68 校正：用户端模式下 userName 取自 claims，不能从 URL 拿（用户端永远携带）。
	// 这里 userName/modelName 都允许为空（兼容全站模式 + 旧调用方）。
	hasUserFilter := strings.TrimSpace(userName) != ""
	hasModelFilter := strings.TrimSpace(modelName) != ""

	var scanned int64
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !models.IsTableExists(tableName) {
			continue
		}
		var lastID uint64
		for {
			if ctx.Err() != nil {
				return scanned, true, nil
			}
			var rows []streamScanRow
			q := sdb.Table(tableName).Select(streamScanColumns).Where("id > ?", lastID)
			if filterTime {
				q = q.Where("created_at >= ?", cutoff)
			}
			// v2.0.68：限定「本平台用户的某个模型」上下文（命中 idx_user_model_created 前缀）
			if hasUserFilter {
				q = q.Where("user_name = ?", userName)
			}
			if hasModelFilter {
				q = q.Where("model_name = ?", modelName)
			}
			if err := q.Order("id ASC").Limit(modelsdb.StatsShardScanBatch).Find(&rows).Error; err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return scanned, true, nil
				}
				return scanned, false, fmt.Errorf("failed to scan %s: %w", tableName, err)
			}
			if len(rows) > 0 {
				for idx := range rows {
					agg.addRow(&rows[idx])
				}
				scanned += int64(len(rows))
				lastID = rows[len(rows)-1].ID
				// 每批推一次累计快照（第一批就让前端填满 7 卡）
				if onBatch != nil {
					onBatch(scanned)
				}
			}
			if len(rows) < modelsdb.StatsShardScanBatch {
				break
			}
		}
	}
	return scanned, false, nil
}
