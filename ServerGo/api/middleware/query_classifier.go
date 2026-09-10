package middleware

// 阶段BZ / v2.0.79：
// 对接 ClientWeb 阶段BX+ 提出的「超长耗时查询兼容 + AIRouteManage 超时优化」需求。
// 把原来写死的 `database.StatsQueryTimeout = 25s` 扩展为按 URL 分类的多档 ctx 超时，
// 解决两个生产痛点：
//  1. 跨分表全站聚合（8 张分表 GROUP BY）在 shard_00 133GB 下首屏 25s 必超时；
//  2. AIRouteManage / CleanupReport 等页面整页 POST 因 5s 前端超时反复刷新导致
//     "请求超时，服务可能正在重启，请刷新页面重试" 的无限重试风暴。
//
// 设计原则：
//   - 不引入新中间件框架，仅在原有 mux 外层套一个轻量包装；
//   - 分类按 URL 路径字符串（不依赖路由变量 / 参数），单次正则匹配；
//   - ctx 超时与 connection-level readTimeout/writeTimeout 双重保险；
//   - 保持向后兼容：handler 不感知 ctx 来自中间件，仍可通过
//     `r.Context()` 拿到（Gin 风格 r.Context()，HTTP 标准库亦支持）。

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

// QueryClass 决定 ctx 超时上限（与前端 `shared/api.js` 的 `timeoutFor(path)` 对齐）。
type QueryClass int

const (
	// QShort 10s：用户信息 / 配置 / 小字典
	QShort QueryClass = iota
	// QNormal 30s：列表分页 / 单条元数据 / 统计短查询（原 StatsQueryTimeout 默认值）
	QNormal
	// QDetail 60s：单行详情大字段按需加载（request_body / response_body 等 longtext）
	QDetail
	// QLong 300s：跨分表全站聚合、AIRouteManage batch_stats、CleanupReport 列表
	QLong
	// QStream 不超时：SSE / WebSocket；客户端断连由 ctx.Done() 自动取消
	QStream
)

// String 输出便于日志识别。
func (q QueryClass) String() string {
	switch q {
	case QShort:
		return "short"
	case QNormal:
		return "normal"
	case QDetail:
		return "detail"
	case QLong:
		return "long"
	case QStream:
		return "stream"
	default:
		return "unknown"
	}
}

// Deadline 返回该分类对应的 ctx 超时上限（QStream=0 表示无限）。
func (q QueryClass) Deadline() time.Duration {
	switch q {
	case QShort:
		return 10 * time.Second
	case QNormal:
		return 30 * time.Second
	case QDetail:
		return 60 * time.Second
	case QLong:
		return 300 * time.Second
	case QStream:
		return 0
	default:
		return 30 * time.Second
	}
}

// longURLPatterns 命中「长查询」分类的 URL 后缀或前缀正则。
// 仅放确知 IO 重的接口，避免一刀切导致短查询被拖到 300s。
var longURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ChatAnalysisTotalRangeInterface$`),
	regexp.MustCompile(`ChatAnalysisTotalWS$`),
	regexp.MustCompile(`CleanupReportInterface$`),
	regexp.MustCompile(`AIRouteManageInterface$`),
	regexp.MustCompile(`UserAIRouteInterface$`),
	regexp.MustCompile(`ModelInfoInterface$`),
	regexp.MustCompile(`AgentInfoInterface$`),
	regexp.MustCompile(`ChatAnalysisTotalInterface$`),
	regexp.MustCompile(`ProtocolConvertAnalyzerRecords$`),
	regexp.MustCompile(`ProtocolConvertAnalyzerUsers$`),
}

// detailURLPatterns 命中「大字段按需加载」分类。
var detailURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ChatAnalysisDetailInterface$`),
	regexp.MustCompile(`ProtocolConvertAnalyzerRecordDetail$`),
	regexp.MustCompile(`ProtocolConvertAnalyzerTest$`),
}

// shortURLPatterns 命中「短查询 / 配置」分类。
var shortURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`TimeSpanConfigInterface$`),
	regexp.MustCompile(`UserInfoInterface$`),
	regexp.MustCompile(`UserModelListInterface$`),
	regexp.MustCompile(`UserModelOptionsInterface$`),
	regexp.MustCompile(`AppVersionInterface$`),
	regexp.MustCompile(`GitInfoInterface$`),
	regexp.MustCompile(`SystemInfoInterface$`),
}

// streamURLPatterns 命中「流式 / SSE / WS」分类（ctx 不超时）。
// 注：?stream=1 走 ChatAnalysisTotalRangeInterface 的 SSE 分支，前端 longFetch 期望不超时。
var streamURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ChatAnalysisTotalRangeInterface.*stream=1`),
	regexp.MustCompile(`/ws`),
}

// ClassifyQuery 按 URL 路径决定 ctx 超时分类。
// 优先匹配更具体的规则（detail > short > long > stream），未命中回退 QNormal。
// stream 检测必须在剥 query string 前执行——?stream=1 是 SSE 触发的关键标志。
func ClassifyQuery(path string) QueryClass {
	if matchAny(streamURLPatterns, path) {
		return QStream
	}

	// 去掉 query string，仅按 path 分类
	p := path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}

	if matchAny(detailURLPatterns, p) {
		return QDetail
	}
	if matchAny(shortURLPatterns, p) {
		return QShort
	}
	if matchAny(longURLPatterns, p) {
		return QLong
	}
	return QNormal
}

func matchAny(patterns []*regexp.Regexp, s string) bool {
	for _, re := range patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// QueryContextMiddleware 按 URL 分类设置 r.Context() 超时，并附带 query_class 头。
// 调用方应在 webserver.Start{Manager,User}WebServer 中套到 mux 外层（在 prefixStrip 之前，
// 确保所有路径——包括网关剥离后的——都被正确分类）。
func QueryContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		klass := ClassifyQuery(r.URL.Path)
		deadline := klass.Deadline()
		if deadline > 0 {
			ctx, cancel := newTimeoutContext(r.Context(), deadline)
			defer cancel()
			r = r.WithContext(ctx)
		}
		w.Header().Set("X-Lsm-Query-Class", klass.String())
		next.ServeHTTP(w, r)
	})
}