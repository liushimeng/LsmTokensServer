package api

// v2.0.74 BUG-1 回归测试：SpiderDataSourceCrawlHandler 在写出 SSE 响应（header/flush）
// 之前必须先解析 POST body，否则 HTTP/1.1 下 net/http 会关闭未读的 request body，
// body 通道拿到 "invalid Read on closed Body" → 报「缺少 data_source_id 参数」。
// 复现条件：管理端 9101 为 HTTP/1.1（用户端 HTTP/2 不受影响），源码审查与纯函数最小复现
// 均无法发现；httptest.NewRecorder 不经过真实服务端也无法复现，必须用 httptest.NewServer
// （真实 TCP + HTTP/1.1）走完整链路验证。

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// crawlBodyChannelCase 走真实 HTTP/1.1 服务端发送请求，断言 body 通道解析成功：
// 响应不得包含「缺少 data_source_id 参数」，且应推进到「数据源不存在」分支（nil DB 下必然命中）
func crawlBodyChannelCase(t *testing.T, method, target, body string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SpiderDataSourceCrawlHandler(w, r, true, 0)
	}))
	defer ts.Close()

	req, err := http.NewRequest(method, ts.URL+target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	respText := string(b)

	if strings.Contains(respText, "缺少 data_source_id 参数") {
		t.Errorf("POST body 通道解析失败（body 应在 SSE 写出前解析，HTTP/1.1 下先写响应会关闭未读 body）\n响应: %s", respText)
	}
	if !strings.Contains(respText, "数据源不存在 (id=99999)") {
		t.Errorf("未推进到数据源校验分支，body 通道可能仍被提前关闭\n响应: %s", respText)
	}
}

// TestSpiderCrawlPostBodyParsesBeforeSSEWrite 管理端（isAdmin=true）POST body 通道
func TestSpiderCrawlPostBodyParsesBeforeSSEWrite(t *testing.T) {
	crawlBodyChannelCase(t, "POST", "/SpiderDataSourceCrawl", `{"data_source_id":99999}`)
}

// TestSpiderCrawlQueryChannelStillWorks query 通道回归（确保修复不破坏原通道）
func TestSpiderCrawlQueryChannelStillWorks(t *testing.T) {
	crawlBodyChannelCase(t, "GET", "/SpiderDataSourceCrawl?data_source_id=99999", "")
}

// TestSpiderCrawlMissingIDStillRejected 无任何通道携带 id 时仍应报缺少参数
func TestSpiderCrawlMissingIDStillRejected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SpiderDataSourceCrawlHandler(w, r, true, 0)
	}))
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/SpiderDataSourceCrawl", "application/json",
		strings.NewReader(fmt.Sprintf(`{"other":1}`)))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(b), "缺少 data_source_id 参数") {
		t.Errorf("无 id 的 body 应报缺少参数\n响应: %s", string(b))
	}
}
