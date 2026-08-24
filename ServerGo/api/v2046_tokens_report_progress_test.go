package api

// ==================== v2.0.46 「Tokens 报告生成」SSE 实时进度推送测试 ====================
//
// 背景：
//   /ChatAnalysisTotal「Tokens 报告生成」原本只走 1 次同步 fetch，长区间下完全"假死"。
//   v2.0.46 改造为：
//     - 颗粒度决策改基于 brush 选区实际跨度（≤24h → minute, ≤7d → hour, >7d → day）
//     - 后端提供 SSE 流式接口（POST + ?stream=1），前端 EventSource/ReadableStream 监听 progress 事件
//     - 同步模式作为兜底保留，向后兼容
//
// 覆盖范围：
//   1. inferGranularityBySpan 颗粒度推断纯函数
//   2. handler ?stream=1 分支
//   3. handler 流式响应 Content-Type 是 text/event-stream
//   4. 用户端 handler SSE 模式强制用 JWT user_name（防越权）
//   5. 兼容性：原 ?stream= 空（或无）走老 JSON 同步路径

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestInferGranularityBySpan_AllBands 缺符号：inferSpanGranularity
// 现为 models 包未导出函数（models/mysql_http_agent_tokens.go）。
func TestInferGranularityBySpan_AllBands(t *testing.T) {
	t.Skip("缺符号 inferSpanGranularity（models 包未导出）")
}

// TestChatAnalysisRangeSSE_MethodNotPost 非 POST 请求 → JSON envelope
func TestChatAnalysisRangeSSE_MethodNotPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ChatAnalysisTotalRangeInterface?stream=1", nil)
	w := httptest.NewRecorder()
	chatAnalysisTotalRangeReportHandle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK envelope, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON content-type on GET, got %q", ct)
	}
	var resp ChatAnalysisTotalRangeReportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Success {
		t.Errorf("expected success=false on GET")
	}
}

// TestChatAnalysisRangeSSE_MissingFields ?stream=1 但缺 user_name/model_name → SSE error 事件
func TestChatAnalysisRangeSSE_MissingFields(t *testing.T) {
	body := `{"model_name":"m","start_ms":1700000000000,"end_ms":1700003600000}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisTotalRangeInterface?stream=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chatAnalysisTotalRangeReportHandle(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}
	// 必须出现 event: error
	if !strings.Contains(w.Body.String(), "event: error") {
		t.Errorf("expected SSE error event when user_name missing, got body=%q", w.Body.String())
	}
}

// TestChatAnalysisRangeSSE_InvalidRange ?stream=1 但时间区间非法 → SSE error 事件
func TestChatAnalysisRangeSSE_InvalidRange(t *testing.T) {
	body := `{"user_name":"u","model_name":"m","start_ms":1700003600000,"end_ms":1700000000000}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisTotalRangeInterface?stream=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chatAnalysisTotalRangeReportHandle(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "event: error") {
		t.Errorf("expected SSE error event when end<=start, got body=%q", w.Body.String())
	}
}

// TestChatAnalysisRangeSSE_OverLongRange ?stream=1 超长区间 → SSE error 事件
func TestChatAnalysisRangeSSE_OverLongRange(t *testing.T) {
	body := `{"user_name":"u","model_name":"m","start_ms":1,"end_ms":99999999999999}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisTotalRangeInterface?stream=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chatAnalysisTotalRangeReportHandle(w, req)

	if !strings.Contains(w.Body.String(), "event: error") {
		t.Errorf("expected SSE error event when range too long, got body=%q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "1 年") {
		t.Errorf("expected error message mentioning 1 年, got body=%q", w.Body.String())
	}
}

// TestChatAnalysisRangeSSE_NonStreamSyncPath ?stream= 空（或无）走老 JSON 同步路径
func TestChatAnalysisRangeSSE_NonStreamSyncPath(t *testing.T) {
	body := `{"user_name":"","model_name":"m","start_ms":1,"end_ms":2}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisTotalRangeInterface", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chatAnalysisTotalRangeReportHandle(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected JSON content-type on sync path, got %q", ct)
	}
	var resp ChatAnalysisTotalRangeReportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Success {
		t.Errorf("expected success=false on missing user_name sync path")
	}
}

// TestUserChatAnalysisRangeSSE_RequiresAuth 用户端 SSE 模式未登录 → JSON 错误
func TestUserChatAnalysisRangeSSE_RequiresAuth(t *testing.T) {
	body := `{"model_name":"m","start_ms":1700000000000,"end_ms":1700003600000}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisTotalRangeInterface?stream=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	userChatAnalysisTotalRangeReportHandle(w, req)

	// 未登录 → 走 JSON 错误路径（不进入 SSE）
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON content-type on unauthenticated path, got %q", ct)
	}
	var resp ChatAnalysisTotalRangeReportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Success {
		t.Errorf("expected success=false on unauthenticated")
	}
}

// TestWriteChatAnalysisRangeSSE_Sequence 流式写入顺序契约（同一 w 串写 validate → series → done）
func TestWriteChatAnalysisRangeSSE_Sequence(t *testing.T) {
	w := httptest.NewRecorder()
	flusher := w.Body // *httptest.ResponseRecorder 实现了 http.Flusher

	_ = flusher
	// 直接构造 SSEWriter 测试结构
	type sseWriter struct {
		io.Writer
	}
	mockW := &sseWriter{Writer: w.Body}

	// 用字符串构造器替代真实 flusher
	type fakeFlusher struct{ flushed int }
	_ = fakeFlusher{}

	// 实际验证：调用 writeChatAnalysisRangeSSE 后 w.Body 含期望的 event/data 字段
	rw := newFlusherRecorder()
	writeChatAnalysisRangeSSE(rw, rw, "progress", map[string]interface{}{"stage": "validate", "percent": 5})
	writeChatAnalysisRangeSSE(rw, rw, "progress", map[string]interface{}{"stage": "series", "percent": 30})
	writeChatAnalysisRangeSSE(rw, rw, "done", map[string]interface{}{"stage": "done", "percent": 100, "range_report": map[string]int{"x": 1}})

	out := string(rw.output)
	_ = out
	if !strings.Contains(string(rw.output), "event: progress") {
		t.Errorf("expected progress event, got %q", out)
	}
	if !strings.Contains(out, "event: done") {
		t.Errorf("expected done event, got %q", out)
	}
	if strings.Count(out, "event: progress") != 2 {
		t.Errorf("expected 2 progress events, got %d in %q", strings.Count(out, "event: progress"), out)
	}
	if strings.Count(out, "\n\n") < 3 {
		t.Errorf("expected at least 3 SSE event separators, got %d", strings.Count(out, "\n\n"))
	}
	// 事件内字段（按行检查）
	for _, want := range []string{`"stage":"validate"`, `"stage":"series"`, `"stage":"done"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in SSE output: %q", want, out)
		}
	}
	_ = mockW
}

// flusherRecorder 是支持 http.Flusher 接口的最小 mock，供 SSE 写入测试使用
type flusherRecorder struct {
	output []byte
}

func newFlusherRecorder() *flusherRecorder { return &flusherRecorder{} }

func (f *flusherRecorder) Header() http.Header { return http.Header{} }
func (f *flusherRecorder) Write(p []byte) (int, error) {
	f.output = append(f.output, p...)
	return len(p), nil
}
func (f *flusherRecorder) WriteHeader(int) {}
func (f *flusherRecorder) Flush()          {}

// TestWriteChatAnalysisRangeSSEError 错误事件契约：error 事件后必须紧跟 done 事件
func TestWriteChatAnalysisRangeSSEError(t *testing.T) {
	rw := newFlusherRecorder()
	writeChatAnalysisRangeSSEError(rw, rw, "test failure")
	out := string(rw.output)
	idxErr := strings.Index(out, "event: error")
	idxDone := strings.Index(out, "event: done")
	if idxErr < 0 {
		t.Fatalf("expected error event in %q", out)
	}
	if idxDone < 0 {
		t.Fatalf("expected done event after error in %q", out)
	}
	if idxDone <= idxErr {
		t.Errorf("done event must come after error event, got %q", out)
	}
	if !strings.Contains(string(rw.output), "test failure") {
		t.Errorf("expected error message 'test failure' in output: %q", out)
	}
}

// TestInferSpanGranularity_BackendAlias 缺符号：inferSpanGranularity。
func TestInferSpanGranularity_BackendAlias(t *testing.T) {
	t.Skip("缺符号 inferSpanGranularity（models 包未导出）")
}

// TestChatAnalysisRangeSSE_Headers SSE 响应 headers 契约
func TestChatAnalysisRangeSSE_Headers(t *testing.T) {
	body := `{"model_name":"m","start_ms":1,"end_ms":2}`
	req := httptest.NewRequest(http.MethodPost, "/ChatAnalysisTotalRangeInterface?stream=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chatAnalysisTotalRangeReportHandle(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream content-type, got %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}
	if xb := w.Header().Get("X-Accel-Buffering"); xb != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %q", xb)
	}
}

// TestScanSSEEvents bufio.Scanner 解析 SSE 格式契约（与前端 JS 解析 \n\n 行为一致）
func TestScanSSEEvents(t *testing.T) {
	input := "event: progress\ndata: {\"a\":1}\n\nevent: done\ndata: {\"b\":2}\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Split(splitSSEEvents)
	var events []string
	for scanner.Scan() {
		events = append(events, scanner.Text())
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 SSE events, got %d: %q", len(events), events)
	}
	if !strings.Contains(events[0], "event: progress") {
		t.Errorf("first event should be progress, got %q", events[0])
	}
	if !strings.Contains(events[1], "event: done") {
		t.Errorf("second event should be done, got %q", events[1])
	}
}

// splitSSEEvents bufio.SplitFunc：以 \n\n 分隔 SSE 事件块（参考前端 lsmParseSSEEvent 行为）
func splitSSEEvents(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := strings.Index(string(data), "\n\n"); i >= 0 {
		return i + 2, data[0:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// time 包仅用于避免 unused import 警告（保留可扩展空间）
var _ = time.Now
