//go:build integration

package spider

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSpiderEngineCrawlCDP 集成测试：起一个 httptest 服务，启 Chrome 引擎，验证 CDP 抓取。
// 用 go build tag `integration` 隔离；CI 默认不跑。
// 运行：LsmSpiderCDPIntegration=1 go test -tags integration -run TestSpiderEngineCrawlCDP -v ./...
func TestSpiderEngineCrawlCDP(t *testing.T) {
	if os.Getenv("LsmSpiderCDPIntegration") != "1" {
		t.Skip("set LsmSpiderCDPIntegration=1 to run")
	}
	// 静态页
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Hello CDP</title></head>
<body><h1>Body</h1><p>integration test page</p>
<a class="link" href="/x">Click</a>
<form id="f"><input name="q" type="text" /></form>
</body></html>`)
	}))
	defer ts.Close()

	// 改端口避免冲突
	origPort := config.G.SpiderCDPPort
	config.G.SpiderCDPPort = 19222
	defer func() { config.G.SpiderCDPPort = origPort }()

	engine := GetSpiderEngine()
	if err := engine.Start(); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	defer engine.Stop()

	resp, err := engine.crawlWebDataCDP(nil, 0, ts.URL, 30, 5000)
	if err != nil {
		t.Fatalf("crawlWebDataCDP: %v", err)
	}
	if !strings.Contains(resp.Title, "Hello CDP") {
		t.Errorf("title=%q, want contains 'Hello CDP'", resp.Title)
	}
	if !strings.Contains(resp.RawHTML, "Body") {
		t.Errorf("html missing 'Body'")
	}
	if resp.CrawlTime.IsZero() {
		t.Error("crawl time not set")
	}
	// 给 engine 一点时间启动上下文
	_ = time.Second
}
