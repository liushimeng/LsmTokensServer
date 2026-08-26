package api

// OpenClaw AI 爬虫 SSE 接口（迁移自旧工程 server_web_spider_crawl.go 后端部分）。
// 旧前端模态框 HTML/CSS/JS 由 ClientWeb SPA 实现（阶段 F），此处仅保留数据链路：
//   GET/POST /SpiderDataSourceCrawl?data_source_id=N  → SSE 流式回传 OpenClaw 输出。
// SSE 协议与旧版 1:1 兼容：默认 message 通道，data 为 JSON {type,content}，
// error 分支后必须追加 done 事件让 EventSource 正确收尾。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/system"
)

// SpiderCrawlSSEEvent - SSE 事件结构
type SpiderCrawlSSEEvent struct {
	Type    string `json:"type"` // "content", "reasoning", "error", "done"
	Content string `json:"content,omitempty"`
}

// defaultSpiderCrawlUserPromptTemplate 默认用户提示词模板（配置 OpenClawUserPromptTemplate
// 为空时使用）。占位符：{{.DataSourceID}} 会被替换为实际的数据源 ID。
const defaultSpiderCrawlUserPromptTemplate = `【强制语言规范】你的所有输出（包括分析、推理、日志、总结）必须全部使用中文，严禁输出英文正文。代码、API 路径、技术术语可保留英文，但所有描述和解释必须为中文。

## OpenClaw 自动化爬虫任务执行规范 - 单个数据源处理

### 目标数据源
- **数据源 ID**：{{.DataSourceID}}
- 本次任务**仅处理此 ID 对应的单条数据源记录**，不得操作其他数据源。

### 1. 上下文与全局约束
**工作目录**：固定为 ` + "`" + `/usr/local/LsmTokensServer/LsmTokensServer/` + "`" + ` ，不可变更。
**执行原则**：全程由主 Agent 独立完成所有任务环节，不启动、不调用任何 SubAgent 子代理，避免主进程等待阻塞导致无响应；仅允许调用 MCP 服务接口完成操作，严禁在执行过程中创建、修改或覆盖任何本地代码文件。
**异常处理机制**：
 - 在调用 MCP 服务接口时，若遇到错误、无响应、或执行效果不达预期的情况，可以主动分析相关代码的底层实现。
 - 切记只做代码分析，严禁直接修改代码。任务处理完成后，需将发现的问题与分析结果汇总，在 ` + "`" + `/usr/local/LsmTokensServer/LsmTokensServer/` + "`" + ` 目录下输出代码分析报告文件，命名格式为 ` + "`" + `代码分析报告_YYYYMMDD_HHMMSS.md` + "`" + `（时间精确到秒，例如：` + "`" + `代码分析报告_20260622_153025.md` + "`" + `）。
 - 如果所有功能均正常且符合预期，则不需要输出代码分析报告文件。

### 2. 任务执行架构与流程
所有流程步骤均由主 Agent 串行执行，无 SubAgent 分工环节。

#### 步骤一：定向查询目标数据源
**接口调用**：发起 GET ` + "`" + `/GetSpiderDataSource?id={{.DataSourceID}}` + "`" + ` 请求，**直接定向查询**目标数据源（无需拉取全量列表后再过滤）。
**关键校验**：确认返回记录的 id={{.DataSourceID}} 且 status=1（启用状态）；若 status≠1 或记录不存在，则立即终止任务并报告"目标数据源不可用"。

#### 步骤二：指令自解析与会话初始化
**指令分析**：分析目标数据源记录中 ` + "`" + `description` + "`" + ` 字段的任务指令内容，理性判断后续是否需要通过多轮交互来获取文章信息和完整的 URL 地址。
**首次握手请求**：向 ` + "`" + `/SpiderWebData` + "`" + ` 发送 POST 请求，请求体中必须携带 data_source_id={{.DataSourceID}}。
**会话凭证提取**：从首次请求的响应中提取 session_id 并妥善留存（注：session_id 有效期为 10 分钟），用作后续多轮交互的唯一上下文凭证。

#### 步骤三：多轮对话与内容总结（Action 阶段）
**会话连续性**：后续的所有交互请求，必须严格携带上一轮返回的 ` + "`" + `session_id` + "`" + `，以确保服务器端会话的连续性与状态同步。
**交互规范**：交互过程中的具体指令和参数定义，必须严格参考 ` + "`" + `MCP_SpiderWebData_def.md` + "`" + ` 接口定义文件。
**文章列表总结**：在多轮交互结束、成功获取到目标文章数据后，对获取到的文章列表进行深度总结与提炼，整理出标准化的日报内容。

#### 步骤四：结果确认与单一持久化（任务收尾）
**唯一持久化原则**：为确保数据的准确性与接口性能，每个独立的数据源在整个任务周期内只能调用一次 ` + "`" + `/InputSpiderDailyInfo` + "`" + ` 接口。
**调用时机与参数**：当抓取数据达标、文章列表已完成总结后，主动调用 ` + "`" + `/InputSpiderDailyInfo` + "`" + ` 接口，请求体中 data_source_id 必须等于 {{.DataSourceID}}，将总结后的数据一次性保存。严禁针对同一数据源进行多频次、重复的记录保存调用。`

// crawlUserLocks 每用户并发限制：同一用户同时只能有一个爬取任务在执行
var crawlUserLocks sync.Map

// SpiderDataSourceCrawlHandler - SSE 流式爬取处理
// 针对单个 data_source_id 的数据源执行 AI 爬取任务：
//  1. 解析并校验 data_source_id（GET query / POST body 双通道）
//  2. 校验数据源存在、状态启用、当前用户有权访问
//  3. 用 {{.DataSourceID}} 占位符渲染提示词
//  4. 调用 OpenClaw SSE 接口流式回传给浏览器
//
// 所有 error 分支均会写一个 "done" 事件，确保前端 EventSource 能正确收尾。
func SpiderDataSourceCrawlHandler(w http.ResponseWriter, r *http.Request, isAdmin bool, userID uint64) {
	// 解析 data_source_id —— 必须在任何响应写出之前执行：HTTP/1.1 下 handler 先写响应
	// （含 header/flush）会导致 net/http 关闭未读的 request body，POST body 通道随即
	// 读到 "invalid Read on closed Body"（管理端 9101 为 HTTP/1.1 受影响；用户端 HTTP/2 不受影响）
	dataSourceID := parseCrawlDataSourceID(r)

	// 设置 SSE 响应头（charset=utf-8 确保中文正确传输；retry 极大值抑制浏览器自动重连）
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// 写入 SSE retry 指令，抑制浏览器自动重连（避免网络抖动导致重复爬取）
	fmt.Fprintf(w, "retry: 999999999\n\n")
	flusher.Flush()

	// 每用户并发限制：同一用户同时只能有一个爬取任务
	lockKey := userID
	if isAdmin {
		lockKey = 0
	}
	ch, _ := crawlUserLocks.LoadOrStore(lockKey, make(chan struct{}, 1))
	lock := ch.(chan struct{})
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	default:
		writeCrawlSSEError(w, flusher, "已有爬取任务在执行中，请等待完成后再试")
		return
	}

	if dataSourceID == 0 {
		writeCrawlSSEError(w, flusher, "缺少 data_source_id 参数")
		return
	}

	logger.Printf("[OpenClaw] crawl start ds_id=%d user_id=%d isAdmin=%v", dataSourceID, userID, isAdmin)
	startTime := time.Now()

	// 验证数据源存在
	ds, err := modelsdb.GetSpiderDataSourceByID(dataSourceID)
	if err != nil || ds == nil {
		writeCrawlSSEError(w, flusher, fmt.Sprintf("数据源不存在 (id=%d)", dataSourceID))
		logger.Printf("[OpenClaw] crawl end ds_id=%d duration=%s reason=not_found", dataSourceID, time.Since(startTime))
		return
	}

	// 权限检查：非管理员只能访问公共(user_id=0)或自己的数据源
	if !isAdmin {
		if ds.UserID != 0 && ds.UserID != userID {
			writeCrawlSSEError(w, flusher, "无权访问该数据源")
			logger.Printf("[OpenClaw] crawl end ds_id=%d duration=%s reason=forbidden owner=%d caller=%d", dataSourceID, time.Since(startTime), ds.UserID, userID)
			return
		}
	}

	// 状态校验
	if ds.Status != 1 {
		writeCrawlSSEError(w, flusher, "数据源已禁用，无法爬取")
		logger.Printf("[OpenClaw] crawl end ds_id=%d duration=%s reason=disabled", dataSourceID, time.Since(startTime))
		return
	}

	// 构建用户提示词（用 {{.DataSourceID}} 占位符）
	template := defaultSpiderCrawlUserPromptTemplate
	if config.G != nil && config.G.OpenClawUserPromptTemplate != "" {
		template = config.G.OpenClawUserPromptTemplate
	}
	userPrompt := strings.ReplaceAll(template, "{{.DataSourceID}}", strconv.FormatUint(dataSourceID, 10))

	// 从配置读取 OpenClaw 参数
	apiURL, apiKey, model, systemPrompt := resolveOpenClawParams()

	// 创建带超时的上下文（15 分钟：OpenClaw 多轮 MCP 交互涉及页面加载、翻页、点击等，需要充足时间）
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	// 调用 OpenClaw 流式接口；session_id 使用纳秒精度避免同秒重试冲突
	sessionID := fmt.Sprintf("spider_crawl_%d_%d", dataSourceID, time.Now().UnixNano())
	var writeFailed bool // 检测 SSE 写入失败，提前终止 OpenClaw 调用
	streamErr := system.CallOpenClawStream(ctx, apiURL, apiKey, model, sessionID, systemPrompt, userPrompt,
		func(content string, reasoningContent string, done bool, err error) {
			if writeFailed {
				return // 写入已失败，跳过后续回调
			}
			if err != nil {
				if writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "error", Content: err.Error()}) != nil ||
					writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "done"}) != nil {
					writeFailed = true
					cancel()
				}
				return
			}
			if done {
				if writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "done"}) != nil {
					writeFailed = true
					cancel()
				}
				return
			}
			if content != "" {
				if writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "content", Content: content}) != nil {
					writeFailed = true
					cancel()
					return
				}
			}
			if reasoningContent != "" {
				if writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "reasoning", Content: reasoningContent}) != nil {
					writeFailed = true
					cancel()
				}
			}
		})

	if streamErr != nil {
		errMsg := streamErr.Error()
		if errors.Is(streamErr, context.DeadlineExceeded) {
			errMsg = "爬取任务超时（已等待 15 分钟），OpenClaw 多轮交互耗时较长，建议稍后重试或检查目标网站响应速度"
		} else if errors.Is(streamErr, context.Canceled) {
			errMsg = "爬取任务已取消"
		}
		writeCrawlSSEError(w, flusher, errMsg)
		logger.Printf("[OpenClaw] crawl end ds_id=%d duration=%s status=error err=%v", dataSourceID, time.Since(startTime), streamErr)
		return
	}

	logger.Printf("[OpenClaw] crawl end ds_id=%d duration=%s status=ok", dataSourceID, time.Since(startTime))
}

// parseCrawlDataSourceID 从请求中解析 data_source_id，优先 query 后 body
func parseCrawlDataSourceID(r *http.Request) uint64 {
	// 1. 先从 query 拿
	if idStr := r.URL.Query().Get("data_source_id"); idStr != "" {
		if id, err := strconv.ParseUint(idStr, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	// 2. POST 时再从 body 拿（限制 1KB 防止恶意大请求体）
	if r.Method == http.MethodPost && r.Body != nil {
		r.Body = http.MaxBytesReader(nil, r.Body, 1024)
		var req struct {
			DataSourceID uint64 `json:"data_source_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.DataSourceID > 0 {
			return req.DataSourceID
		}
	}
	return 0
}

// resolveOpenClawParams 从全局配置读取 OpenClaw 参数，缺省时回落到默认值
func resolveOpenClawParams() (apiURL, apiKey, model, systemPrompt string) {
	if config.G != nil {
		apiURL = config.G.OpenClawURL
		apiKey = config.G.OpenClawAPIKey
		model = config.G.OpenClawModel
		systemPrompt = config.G.OpenClawSystemPrompt
	}
	if apiURL == "" {
		apiURL = system.DefaultOpenClawURL
	}
	if apiKey == "" {
		apiKey = system.DefaultOpenClawAPIKey
	}
	if model == "" {
		model = system.DefaultOpenClawModel
	}
	if systemPrompt == "" {
		systemPrompt = system.DefaultOpenClawSystemPrompt
	}
	return
}

// writeCrawlSSEError 写入 error 事件并紧随一个 done 事件，确保前端 EventSource 收尾
func writeCrawlSSEError(w http.ResponseWriter, flusher http.Flusher, msg string) {
	writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "error", Content: msg})
	writeCrawlSSEEvent(w, flusher, SpiderCrawlSSEEvent{Type: "done"})
}

// writeCrawlSSEEvent 写入 SSE 事件，返回写入错误（供调用方检测客户端断连）
func writeCrawlSSEEvent(w http.ResponseWriter, flusher http.Flusher, event SpiderCrawlSSEEvent) error {
	data, _ := json.Marshal(event)
	_, err := fmt.Fprintf(w, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}
