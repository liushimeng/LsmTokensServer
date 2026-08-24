package spider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
)

// TestLookupRSSFallbackSources_InternationalHosts verifies that the v2.0.43
// international sites have built-in RSS feed candidates.
func TestLookupRSSFallbackSources_InternationalHosts(t *testing.T) {
	cases := []struct {
		url      string
		wantFeed string
	}{
		{"https://www.technologyreview.com/", "technologyreview.com/feed"},
		{"https://www.wired.com/", "wired.com/feed"},
		{"https://www.marktechpost.com/", "marktechpost.com/feed"},
		{"https://www.deeplearning.ai/the-batch/", "deeplearning.ai/the-batch"},
		{"https://www.therundown.ai/", "therundown.ai"},
		{"https://openreview.net/", "openreview.net"},
	}
	for _, tc := range cases {
		sources, err := LookupRSSFallbackSources(tc.url)
		if err != nil {
			t.Errorf("LookupRSSFallbackSources(%q) error: %v", tc.url, err)
			continue
		}
		if !sources.Known {
			t.Errorf("LookupRSSFallbackSources(%q) Known=false, want true", tc.url)
		}
		if len(sources.Candidates) == 0 {
			t.Errorf("LookupRSSFallbackSources(%q) no candidates", tc.url)
			continue
		}
		found := false
		for _, c := range sources.Candidates {
			if strings.Contains(c, tc.wantFeed) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("LookupRSSFallbackSources(%q) candidates %v do not contain %q", tc.url, sources.Candidates, tc.wantFeed)
		}
	}
}

// TestGetSpiderRSSFetchTimeout_DefaultAndBounds verifies the v2.0.43 RSS
// fetch timeout helper.
func TestGetSpiderRSSFetchTimeout_DefaultAndBounds(t *testing.T) {
	saved := config.G
	defer func() { config.G = saved }()

	// nil config.G -> default 15s
	config.G = nil
	if got := getSpiderRSSFetchTimeout(); got != 15*time.Second {
		t.Errorf("nil config.G: got %v, want 15s", got)
	}

	// explicit value within range
	config.G = &config.LsmTokensServerConfig{SpiderRSSFetchTimeoutSec: 25}
	if got := getSpiderRSSFetchTimeout(); got != 25*time.Second {
		t.Errorf("25s config.G: got %v, want 25s", got)
	}

	// below min clamped to 5s
	config.G = &config.LsmTokensServerConfig{SpiderRSSFetchTimeoutSec: 3}
	if got := getSpiderRSSFetchTimeout(); got != 5*time.Second {
		t.Errorf("3s config.G: got %v, want 5s", got)
	}

	// above max clamped to 60s
	config.G = &config.LsmTokensServerConfig{SpiderRSSFetchTimeoutSec: 120}
	if got := getSpiderRSSFetchTimeout(); got != 60*time.Second {
		t.Errorf("120s config.G: got %v, want 60s", got)
	}
}

// TestFetchRSSTries_UsesConfiguredTimeout verifies that FetchRSSTries honors
// the configured RSS fetch timeout when using the default http.Client.
func TestFetchRSSTries_UsesConfiguredTimeout(t *testing.T) {
	saved := config.G
	defer func() { config.G = saved }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0"><channel><item><title>X</title><link>https://x/1</link></item></channel></rss>`))
	}))
	defer server.Close()

	sources := RSSFallbackSource{Candidates: []string{server.URL + "/feed"}, Known: true}

	// short ctx deadline < server delay -> should fail
	config.G = nil
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()
	res := FetchRSSTries(shortCtx, sources, nil, 10)
	if res.Success {
		t.Errorf("short ctx: expected failure due to timeout, got success")
	}

	// long ctx deadline > server delay -> should succeed
	longCtx, longCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer longCancel()
	res = FetchRSSTries(longCtx, sources, nil, 10)
	if !res.Success {
		t.Errorf("long ctx: expected success, got errorType=%s err=%s", res.ErrorType, res.ErrorMsg)
	}
}
