package spider

import ()

import "testing"

// TestParseSelector 表驱动单测：parseSelector 行为
func TestParseSelector(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		strategy selectorStrategy
		query    string
		textKw   string
	}{
		{"class", ".foo", selCSS, ".foo", ""},
		{"id", "#bar", selCSS, "#bar", ""},
		{"tag", "div", selCSS, "div", ""},
		{"composite", "a.link", selCSS, "a.link", ""},
		{"text", "text:Submit", selText, "", "Submit"},
		{"text with space", "text: Sign in ", selText, "", "Sign in"},
		{"xpath", "xpath://div[@id='x']", selXPath, "//div[@id='x']", ""},
		{"empty", "", selCSS, "", ""},
		{"whitespace", "   ", selCSS, "", ""},
		{"xpath no slash prefix", "xpath:.//span", selXPath, ".//span", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseSelector(c.in)
			if got.Strategy != c.strategy {
				t.Errorf("strategy: got %v, want %v", got.Strategy, c.strategy)
			}
			if got.Query != c.query {
				t.Errorf("query: got %q, want %q", got.Query, c.query)
			}
			if got.TextKeyword != c.textKw {
				t.Errorf("textKw: got %q, want %q", got.TextKeyword, c.textKw)
			}
		})
	}
}

func TestParsedSelectorString(t *testing.T) {
	tests := []struct {
		ps  parsedSelector
		exp string
	}{
		{parsedSelector{Strategy: selCSS, Query: ".foo"}, ".foo"},
		{parsedSelector{Strategy: selXPath, Query: "//div"}, "xpath://div"},
		{parsedSelector{Strategy: selText, TextKeyword: "Submit"}, "text:Submit"},
	}
	for _, tt := range tests {
		if got := tt.ps.String(); got != tt.exp {
			t.Errorf("String() = %q, want %q", got, tt.exp)
		}
	}
}

// TestBuildNextPageURL 验证 URL 翻页模式识别仍正常工作
func TestBuildNextPageURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		exp  string
	}{
		{"page param", "https://x.com/list?page=1", "https://x.com/list?page=2"},
		{"p param", "https://x.com/list?p=3", "https://x.com/list?p=4"},
		{"offset param", "https://x.com/list?offset=10", "https://x.com/list?offset=11"},
		{"start param", "https://x.com/list?start=5", "https://x.com/list?start=6"},
		{"path page", "https://x.com/news/page/2", "https://x.com/news/page/3"},
		{"no pattern", "https://x.com/article/123", "https://x.com/article/123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNextPageURL(tt.in, map[string]interface{}{"times": 1.0})
			if got != tt.exp {
				t.Errorf("got %q, want %q", got, tt.exp)
			}
		})
	}
}

// TestResolveURL 验证相对 URL 解析
func TestResolveURL(t *testing.T) {
	tests := []struct {
		base, ref, exp string
	}{
		{"https://x.com/a/b", "/c", "https://x.com/c"},
		{"https://x.com/a/b", "c", "https://x.com/a/c"},
		{"https://x.com/a/b", "//cdn.com/x", "https://cdn.com/x"},
		{"https://x.com/a/b", "https://y.com/z", "https://y.com/z"},
	}
	for _, tt := range tests {
		got := resolveURL(tt.base, tt.ref)
		if got != tt.exp {
			t.Errorf("resolveURL(%q, %q) = %q, want %q", tt.base, tt.ref, got, tt.exp)
		}
	}
}

// TestTextSearchJS 验证 text 选择器的 JS 生成
func TestTextSearchJS(t *testing.T) {
	p := parseSelector("text:Hello")
	js := p.textSearchJS()
	if js == "" {
		t.Fatal("empty JS")
	}
	// 必须包含关键字和 find 函数
	if !contains(js, "Hello") {
		t.Errorf("JS missing keyword: %s", js)
	}
	if !contains(js, "find") {
		t.Errorf("JS missing find function: %s", js)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
