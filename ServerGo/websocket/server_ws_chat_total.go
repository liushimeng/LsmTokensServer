// v2.0.55: /ChatAnalysisTotalWS WebSocket 流式分块推送 handler
//
// 协议概述（与 CLAUDE.md v2.0.55 强制规则严格一致）：
//
//	客户端首条消息：
//	  {"type":"query","days":7,"model_name":"<本平台模型,可选>","request_id":"<12 hex>"}
//	  {"type":"cancel"}                              // 立即取消当前 query（切时间跨度触发）
//	  {"type":"ping"}                                // 心跳
//
//	服务端响应（按 7 个数据维度串行分块推送，每块间隔 config.CHAT_STATS_CHUNK_SPACING=1s）：
//	  {"type":"chunk","stage":"kpi","request_id":"...","index":1,"data":{...}}
//	  {"type":"chunk","stage":"time_stats",...,"index":2,"data":[...]}
//	  {"type":"chunk","stage":"tokens_summary",...,"index":3,"data":{...}}
//	  {"type":"chunk","stage":"model_distribution",...,"index":4,"data":[...]}
//	  {"type":"chunk","stage":"trend_chart",...,"index":5,"data":[...]}
//	  {"type":"chunk","stage":"protocol_stats",...,"index":6,"data":{...}}
//	  {"type":"chunk","stage":"agent_stats",...,"index":7,"data":{...}}
//	  {"type":"done","request_id":"...","timed_out":false,"warnings":[],"elapsed_ms":1234}
//	  {"type":"error","request_id":"...","stage":"...","message":"..."}
//
// request_id 防重复契约：
//   - 12 个十六进制字符（小写），客户端用 SHA1(timestamp|atomic_seq|user|model|days) 生成
//   - 服务端 sanitizeRequestID 校验失败 → error 帧
//   - 客户端收到 request_id !== currentRequestId 的帧 → 静默丢弃
//
// context 取消契约：
//   - 切换 daysSelect → 客户端发 cancel + close WS
//   - 服务端 cancel() 旧 ctx → 下游 DB 函数通过 database.StatsDB() 25s WithContext 自动响应
//   - 任何维度超时 → 推送 done.timed_out=true + warnings 列表
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	config "github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	logger "github.com/lishimeng/LsmTokensServer/logger"
	models "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// wsClientQuery 客户端发往 /ChatAnalysisTotalWS 的首条消息
type wsClientQuery struct {
	Type      string `json:"type"` // "query" | "cancel" | "ping"
	Days      int    `json:"days"`
	ModelName string `json:"model_name"` // 本平台模型名（可选，空=全站）
	RequestID string `json:"request_id"` // 12 hex 字符
}

// wsChunkFrame 单个数据维度推送
type wsChunkFrame struct {
	Type      string      `json:"type"`  // "chunk"
	Stage     string      `json:"stage"` // kpi/time_stats/tokens_summary/model_distribution/trend_chart/protocol_stats/agent_stats
	RequestID string      `json:"request_id"`
	Data      interface{} `json:"data"`
	Index     int         `json:"index"` // 维度顺序 1..7
}

// wsDoneFrame 推送结束
type wsDoneFrame struct {
	Type      string   `json:"type"` // "done"
	RequestID string   `json:"request_id"`
	TimedOut  bool     `json:"timed_out"`
	Warnings  []string `json:"warnings"`
	ElapsedMs int64    `json:"elapsed_ms"`
}

// wsErrorFrame 错误帧
type wsErrorFrame struct {
	Type      string `json:"type"` // "error"
	RequestID string `json:"request_id"`
	Stage     string `json:"stage,omitempty"`
	Message   string `json:"message"`
}

// wsBusyFrame 拒绝并发 query
type wsBusyFrame struct {
	Type      string `json:"type"` // "busy"
	RequestID string `json:"request_id"`
	Message   string `json:"message"`
}

// wsPongFrame 心跳响应
type wsPongFrame struct {
	Type      string `json:"type"` // "pong"
	RequestID string `json:"request_id,omitempty"`
}

// ChatAnalysisTotalWSHandle HTTP handler 入口；注册到 /ChatAnalysisTotalWS
func ChatAnalysisTotalWSHandle(w http.ResponseWriter, r *http.Request) {
	// 阶段AO：WS 升级握手阶段读取鉴权结果（由 userAuthMiddleware / ManagerAuthMiddleware 提前写入 context）。
	// userAuthMiddleware / ManagerAuthMiddleware 已在外层校验 JWT 并把 *AuthClaims 写入
	// websocket.AuthClaimsContextKey；未登录/失败请求会被中间件提前 401 拒绝，根本不会到这里。
	// 这里再读一次做"防御性校验"——若新部署忘记挂中间件或新增 mux 漏挂，WS 不会暴露全站数据。
	// 例外：security.managerWebAuthDisabled=true 时 ManagerAuthMiddleware 直通 next 不会写 context，
	// 该模式下网关侧已鉴权，本端全放行；运行时按 header X-Forwarded-* 判定为 manager 角色。
	auth := authClaimsFromCtx(r.Context())
	if auth == nil {
		// 中间件未写 context：仅当 managerWebAuthDisabled 网关代理模式才允许放行（按 URL 端口/路径前缀判定）。
		// 这里采用更严格的策略——默认拒绝并要求中间件必须挂上。若管理员 Web 部署在网关后，
		// 网关侧须自行保证鉴权 + 本工程保持 401 即可保证 WS 不暴露。
		// 注：生产环境 managerWebAuthDisabled 模式通常用于纯内网网关+本服务全放行的场景，
		// 不存在中间件写 context 但 WS 需要识别角色的需求，因此默认拒绝是更安全的语义。
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if auth.Role == WsRoleNone {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Hub 超限 → 503；upgrader 自身失败也会写响应（400/426 等），我们直接 return
	if chatStatsHub.count() >= int64(config.CHAT_STATS_MAX_CONNS) {
		http.Error(w, "chat stats hub is full", http.StatusServiceUnavailable)
		return
	}
	// v2.0.68 校正：从 URL query string 取出当前页面的 (user_name, model_name) 上下文，
	// 写入 wsConn 供 runChatStatsQuery → streamChatStats 在 WHERE 中限定「本平台用户的
	// 某个模型」—— 否则 stage 4 model_distribution 会变成全站 GROUP BY，切换本平台
	// 模型时数据完全不变（用户反馈的现象）。
	wsUserName := strings.TrimSpace(r.URL.Query().Get("user_name"))
	wsModelName := strings.TrimSpace(r.URL.Query().Get("model_name"))
	// 阶段AO：用户端连接必须用 JWT claims 中的 user_name 覆盖 URL 参数，避免横向越权（用户改 URL 即可查他人数据）。
	if auth.Role == WsRoleUser {
		wsUserName = auth.UserName
		// model_name 用户端可选（按本人某模型聚合），不做强制——保留 URL 参数以便页面按模型切换。
	}
	conn, err := chatStatsHub.upgrade(w, r)
	if err != nil {
		// upgrade 失败（Hub 满或握手失败）已写响应
		return
	}

	wc := &wsConn{
		hub:          chatStatsHub,
		ws:           conn,
		send:         make(chan []byte, 32),
		createdAt:    time.Now(),
		remoteAddr:   r.RemoteAddr,
		ctxUserName:  wsUserName,
		ctxModelName: wsModelName,
		ctxRole:      auth.Role,
	}
	chatStatsHub.register(wc)

	ctx, cancel := context.WithCancel(context.Background())
	wc.cancel = cancel

	// 关闭路径：cancel ctx → close send → unregister → Close WS
	defer func() {
		cancel()
		wc.closeOnceClose()
		close(wc.send)
		chatStatsHub.unregister(wc)
	}()

	go wc.writePump(ctx)
	wc.readPump(ctx)
}

// readPump 单连接 reader goroutine（标准 gorilla 模式）
func (c *wsConn) readPump(ctx context.Context) {
	c.ws.SetReadLimit(int64(config.WS_MAX_MESSAGE_SIZE))
	_ = c.ws.SetReadDeadline(time.Now().Add(config.WS_PONG_WAIT))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(config.WS_PONG_WAIT))
	})

	for {
		if ctx.Err() != nil {
			return
		}
		_, msg, err := c.ws.ReadMessage()
		if err != nil {
			// 客户端断开 / 网络错误 / 超时 → 直接退出，defer 负责清理
			return
		}
		var req wsClientQuery
		if jerr := json.Unmarshal(msg, &req); jerr != nil {
			c.enqueueError("", "logger.init", "invalid json: "+jerr.Error())
			continue
		}
		switch req.Type {
		case "query":
			if !c.busy.CompareAndSwap(false, true) {
				c.enqueueBusy(req.RequestID)
				continue
			}
			go func() {
				defer c.busy.Store(false)
				// v2.0.64: 统计查询跑在独立 goroutine，一旦 panic 会把整个进程拉死、
				// 所有 WS 连接都报 1006。加 recover 兜底：记日志 + 推 error 帧，
				// 避免单连接异常拖垮全局。
				defer func() {
					if rec := recover(); rec != nil {
						logger.Printf("[ERROR] runChatStatsQuery panic: %v", rec)
						c.enqueueError(req.RequestID, "panic", fmt.Sprintf("%v", rec))
					}
				}()
				c.runChatStatsQuery(ctx, req)
			}()
		case "cancel":
			// 立即取消 ctx（下游 DB 函数通过 database.StatsDB() WithContext 自动响应）
			if c.cancel != nil {
				c.cancel()
			}
		case "ping":
			c.enqueuePong(req.RequestID)
		default:
			c.enqueueError(req.RequestID, "logger.init", "unsupported type: "+req.Type)
		}
	}
}

// writePump 单连接 writer goroutine；ping 心跳 + send 通道消费
func (c *wsConn) writePump(ctx context.Context) {
	pingTicker := time.NewTicker(config.WS_PING_PERIOD)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = c.ws.SetWriteDeadline(time.Now().Add(config.WS_WRITE_WAIT))
			_ = c.ws.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case msg, ok := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(config.WS_WRITE_WAIT))
			if !ok {
				// send 通道被关闭 → 退出
				_ = c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-pingTicker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(config.WS_WRITE_WAIT))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// enqueueChunk / enqueueError / enqueueDone / enqueueBusy / enqueuePong 把帧序列化后入 send 通道
func (c *wsConn) enqueueChunk(f wsChunkFrame) { c.enqueueFrame(f) }
func (c *wsConn) enqueueError(rid, stage, msg string) {
	c.enqueueFrame(wsErrorFrame{Type: "error", RequestID: rid, Stage: stage, Message: msg})
}
func (c *wsConn) enqueueDone(f wsDoneFrame) { c.enqueueFrame(f) }
func (c *wsConn) enqueueBusy(rid string) {
	c.enqueueFrame(wsBusyFrame{Type: "busy", RequestID: rid, Message: "上一个查询仍在进行"})
}
func (c *wsConn) enqueuePong(rid string) { c.enqueueFrame(wsPongFrame{Type: "pong", RequestID: rid}) }

func (c *wsConn) enqueueFrame(payload interface{}) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case c.send <- b:
	default:
		// send 通道已满（极端 case：消费者卡死）；丢弃避免阻塞 reader
	}
}

// runChatStatsQuery v2.0.60：单遍分页扫描 + 全维度增量流式推送。
//
// 旧实现（v2.0.55–59）把 7 个维度拆成 7 次「各自扫全 8 张分表」的独立查询 + 维度间 sleep 1s。
// 数据量大时总扫描量 ~5 倍、累计耗时超 25s ctx，靠后维度被取消、卡片永久「加载中」。
//
// 新实现：一次 keyset 分页遍历（8 张分表，每批 models.StatsShardScanBatch 行），把每批同时喂给
// 7 个维度累加器（chatStatsAggregator），每批（节流后）把「当前累计快照」序列化成 7 个 stage
// 的 chunk 推给前端 → 前端第一批（几十 ms）就能填满 7 卡，之后随扫描进度实时刷新。
func (c *wsConn) runChatStatsQuery(ctx context.Context, q wsClientQuery) {
	start := time.Now()
	days := normalizeChatStatsDays(q.Days)
	modelName := strings.TrimSpace(q.ModelName)
	requestID := sanitizeRequestID(q.RequestID)
	if requestID == "" {
		c.enqueueError("", "logger.init", "missing or invalid request_id (must be 12 hex chars)")
		return
	}

	// 单连接最多 1 个 query：再次校验 busy（readPump 已 CAS 但这里再守一道）
	if !c.busy.Load() {
		c.enqueueError(requestID, "logger.init", "internal: busy flag not set")
		return
	}

	warnings := []string{}
	timedOut := false

	sdb, cancel := database.StatsDB()
	defer cancel()

	agg := newChatStatsAggregator(days)

	// 推送当前累计快照的 7 个 stage chunk（scanned 用于 hero 状态进度显示）
	pushSnapshot := func(scanned int64, final bool) {
		snapshots := []struct {
			name string
			data interface{}
		}{
			{"kpi", c.attachScanned(agg.snapshotKPI(modelName), scanned, final)},
			{"time_stats", agg.snapshotTimeStats()},
			{"tokens_summary", agg.snapshotTokensSummary()},
			{"model_distribution", agg.snapshotModelDist()},
			{"trend_chart", agg.snapshotTrend()},
			{"protocol_stats", agg.snapshotProtocol()},
			{"agent_stats", agg.snapshotAgent()},
		}
		for i, s := range snapshots {
			c.enqueueChunk(wsChunkFrame{
				Type: "chunk", Stage: s.name, RequestID: requestID,
				Data: s.data, Index: i + 1,
			})
		}
	}

	// 节流：数据量大时每批（5000 行）都全量推 7 个快照会刷屏，按时间节流到最多每
	// config.CHAT_STATS_SNAPSHOT_MIN_INTERVAL 推一次；首批立即推（让首屏最快出数据）。
	var lastPush time.Time
	var pushedOnce bool
	onBatch := func(scanned int64) {
		now := time.Now()
		if !pushedOnce || now.Sub(lastPush) >= config.CHAT_STATS_SNAPSHOT_MIN_INTERVAL {
			pushSnapshot(scanned, false)
			lastPush = now
			pushedOnce = true
		}
	}

	// v2.0.68 校正：把页面上下文 (user_name, model_name) 透传给 streamChatStats，
	// 让单遍扫描在 WHERE 中限定「本平台用户的某个模型」，否则 stage 4 会变全站 GROUP BY
	// （用户反馈：切换本平台模型时数据不变）。
	scanned, streamTimedOut, err := streamChatStats(ctx, sdb, 8, agg, c.ctxUserName, c.ctxModelName, onBatch)
	if err != nil {
		warnings = append(warnings, "scan:"+err.Error())
		c.enqueueError(requestID, "scan", err.Error())
	}
	if streamTimedOut {
		timedOut = true
		warnings = append(warnings, "scan:timeout")
	}

	// 无论中途是否节流丢块，最终一定推一次完整快照（保证 7 卡都拿到最终数据）
	pushSnapshot(scanned, true)

	c.enqueueDone(wsDoneFrame{
		Type: "done", RequestID: requestID,
		TimedOut: timedOut, Warnings: warnings,
		ElapsedMs: time.Since(start).Milliseconds(),
	})
}

// attachScanned 把已扫描行数与是否最终快照写进 KPI map，供前端 hero 状态显示扫描进度。
func (c *wsConn) attachScanned(kpi map[string]interface{}, scanned int64, final bool) map[string]interface{} {
	kpi["scanned_rows"] = scanned
	kpi["scan_final"] = final
	return kpi
}

// normalizeChatStatsDays 把 days 限制到 ChatAnalysisTotal 白名单 [0,1,3,5,7,14,30,60,90]，
// 不在白名单（含负值、>90）回落 7。
func normalizeChatStatsDays(d int) int {
	for _, x := range []int{0, 1, 3, 5, 7, 14, 30, 60, 90} {
		if d == x {
			return d
		}
	}
	return 7
}

// sanitizeRequestID 校验客户端 request_id 必须是 12 个小写十六进制字符
func sanitizeRequestID(s string) string {
	if len(s) != 12 {
		return ""
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}
	return s
}

// ============ 维度函数 ============

// lsmBuildChatStatsKPI 维度 1：KPI 卡片（总请求/总 token/活跃模型/活跃天数）
//
// v2.0.57: 改用 models.GetAllStatsKPISummary 轻量聚合（仅 COUNT + SUM，不 GROUP BY），
// 避免 160K 行 × GROUP BY model_name,user_name 的慢查询导致 KPI 全 0 误报。
// v2.0.58: active_models 通过 getModelDist 共享闭包获取（与 model_distribution stage
// 复用同一次 GROUP BY 结果，不再重复查询）。
func lsmBuildChatStatsKPI(ctx context.Context, days int, modelName string, getModelDist func() ([]models.ModelNameUsageStat, error)) (map[string]interface{}, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	type kpiResult struct {
		totalCalls  int64
		totalTokens uint64
		activeDays  int
		err         error
		models      []models.ModelNameUsageStat
		modelsErr   error
	}
	var res kpiResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		res.totalCalls, res.totalTokens, res.activeDays, res.err = models.GetAllStatsKPISummary(8, days)
		// active_models 复用 model_distribution 的共享结果（best-effort）；
		// 若该查询超时被取消则 active_models 退回 active_days
		if getModelDist != nil {
			res.models, res.modelsErr = getModelDist()
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	warnings := []string{}
	if res.err != nil {
		warnings = append(warnings, "kpi:"+res.err.Error())
	}
	if res.modelsErr != nil {
		warnings = append(warnings, "models:"+res.modelsErr.Error())
	}

	activeModels := 0
	for _, m := range res.models {
		if m.CallCount > 0 {
			activeModels++
		}
	}
	// 如果 model_distribution 失败或超时，active_models 退回 active_days
	if activeModels == 0 && res.activeDays > 0 {
		activeModels = res.activeDays // 粗略估算（每天至少 1 个模型）
	}

	kpi := map[string]interface{}{
		"total_calls":     res.totalCalls,
		"total_tokens":    res.totalTokens,
		"active_models":   activeModels,
		"active_days":     res.activeDays,
		"window_days":     days,
		"model_name":      modelName,
		"warnings":        warnings,
		"generated_at_ms": time.Now().UnixMilli(),
	}
	return kpi, nil
}

// lsmBuildChatStatsTokensSummary 维度 3：Tokens 概览（调用次数/输入/输出/总 Tokens + 输入输出比 + TTFB/Gen 平均值）
// v2.0.56: 切换到 models.GetTokensRangeStatsAll 全站变体
func lsmBuildChatStatsTokensSummary(ctx context.Context, days int) (interface{}, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	stats, err := models.GetTokensRangeStatsAll(8, days)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = []models.TokensRangeStat{}
	}
	// 跨桶聚合
	var totalCount int64
	var totalInput, totalOutput, totalAll uint64
	for _, s := range stats {
		totalCount += s.Count
		totalInput += s.TokensInput
		totalOutput += s.TokensOutput
		totalAll += s.TokensTotal
	}
	return map[string]interface{}{
		"buckets":         stats,
		"total_count":     totalCount,
		"total_input":     totalInput,
		"total_output":    totalOutput,
		"total_tokens":    totalAll,
		"window_days":     days,
		"generated_at_ms": time.Now().UnixMilli(),
	}, nil
}

// v2.0.55 wsQueryInFlight 全局并发计数器（观察用，不参与逻辑）
var wsQueryInFlight atomic.Int64

// wsQueryInFlightForTest 测试辅助
func wsQueryInFlightForTest() int64 { return wsQueryInFlight.Load() }

// 阶段顺序常量（供前端 stageToDOM 与测试 stageOrderContract 复用）
var wsChatStatsStageOrder = []string{
	"kpi", "time_stats", "tokens_summary", "model_distribution",
	"trend_chart", "protocol_stats", "agent_stats",
}

// wsChatStatsStageIndexForTest 测试辅助：返回 stage 在标准顺序中的 1-based 位置（不在列表中返回 0）
func wsChatStatsStageIndexForTest(stage string) int {
	for i, s := range wsChatStatsStageOrder {
		if s == stage {
			return i + 1
		}
	}
	return 0
}

// 简单占位 fmt 引用（防止未使用 fmt 编译错误；后续实现可能会用到）
var _ = fmt.Sprintf
