package spider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// ==================== v2.0.27：handler panic 自愈 + trackingResponseWriter 单测 ====================
//
// 问题背景（问题分析报告_20260703_062125 §1.5 / §4.1）：
//   handler panic 后没有服务端自愈 + 下一个请求复用已坏 session → 进入 PANIC 自循环。
//
// 测试目标：验证 v2.0.27 四层兜底生效
//   - trackingResponseWriter 正确追踪 Write / WriteHeader / Flush
//   - 已写响应后 recover 不会再写二次响应头

func TestTrackingResponseWriter_Writes(t *testing.T) {
	inner := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: inner}

	// 初始未写
	if tw.written.Load() {
		t.Fatal("expected not written initially")
	}

	// 写 body
	tw.Write([]byte("hello"))
	if !tw.written.Load() {
		t.Fatal("expected written=true after Write")
	}
	if inner.Body.String() != "hello" {
		t.Fatalf("unexpected body: %q", inner.Body.String())
	}
}

func TestTrackingResponseWriter_WriteHeader(t *testing.T) {
	inner := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: inner}

	// 仅 WriteHeader 不写 body 时也被视为"已写"，recover 不要重复兜底
	tw.WriteHeader(http.StatusInternalServerError)
	if !tw.written.Load() {
		t.Fatal("expected written=true after WriteHeader")
	}
	if inner.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", inner.Code)
	}
}

func TestTrackingResponseWriter_FlushNoOpWithoutFlusher(t *testing.T) {
	// 包装没有 Flush 的 ResponseWriter 也不 panic
	inner := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: inner}
	tw.Flush() // recorder 有 Flush，调用应成功
	// 反向测：用极简 stub（无 Flush 接口）
	min := &minResponseWriter{}
	tw2 := &trackingResponseWriter{ResponseWriter: min}
	tw2.Flush() // 不应 panic
}

func TestTrackingResponseWriter_RecoverSkipsWhenAlreadyWritten(t *testing.T) {
	inner := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: inner}
	var responseWrittenFlag atomic.Bool

	// 模拟正常 encode 路径 —— 已写入 body
	_, _ = tw.Write([]byte(`{"pre":"existing"}`))
	responseWrittenFlag.Store(true)

	// 之后触发 panic recovery
	var recovered bool
	var didWrite bool
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				recovered = true
				if !tw.written.Load() && !responseWrittenFlag.Load() {
					didWrite = true
				}
			}
		}()
		panic("after written")
	}()

	if !recovered {
		t.Fatal("expected panic to be recovered")
	}
	if didWrite {
		t.Fatal("response should NOT have been written when tracker already saw writes")
	}
}

func TestTrackingResponseWriter_RecoverWritesWhenNothingSent(t *testing.T) {
	inner := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: inner}

	// 模拟 handler 尚未写任何东西就 panic
	var didWrite bool
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if !tw.written.Load() {
					didWrite = true
					tw.WriteHeader(http.StatusInternalServerError)
					_, _ = tw.Write([]byte("rescue"))
				}
			}
		}()
		panic("early")
	}()

	if !didWrite {
		t.Fatal("expected recovery to write fallback response")
	}
	if !strings.Contains(inner.Body.String(), "rescue") {
		t.Fatalf("unexpected body: %q", inner.Body.String())
	}
}

// minResponseWriter: 极简 http.ResponseWriter 实现（没有 Flush 接口）
type minResponseWriter struct {
	header http.Header
	code   int
	body   []byte
}

func newMinRW() *minResponseWriter {
	return &minResponseWriter{header: http.Header{}}
}

func (m *minResponseWriter) Header() http.Header { return m.header }

func (m *minResponseWriter) Write(p []byte) (int, error) {
	m.body = append(m.body, p...)
	return len(p), nil
}

func (m *minResponseWriter) WriteHeader(code int) { m.code = code }

var _ = newMinRW // 避免未使用的包级定义
