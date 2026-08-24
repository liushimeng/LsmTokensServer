package proxy

import "testing"

// TestIsUpstreamConnectError 验证上游连接类错误识别
func TestIsUpstreamConnectError(t *testing.T) {
	cases := []struct {
		errMsg string
		want   bool
	}{
		{"dial tcp 1.2.3.4:443: connect: connection refused", true},
		{"dial tcp: lookup foo.example: no such host", true},
		{"read tcp 1.2.3.4:443: i/o timeout", true},
		{"net/http: TLS handshake error", true},
		{"Get \"https://api.foo.com/v1/messages\": EOF", true},
		{"write tcp: broken pipe", true},
		{"Post \"https://x\": dial: connection reset by peer", true},
		{"endpoint 5 is disabled", false},
		{"all 3 endpoints failed: no destination endpoints configured", false},
		{"invalid dst URL scheme \"ftp\"", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isUpstreamConnectError(tc.errMsg); got != tc.want {
			t.Errorf("isUpstreamConnectError(%q) = %v, want %v", tc.errMsg, got, tc.want)
		}
	}
}

// TestJSONEscape 验证 JSON 字符串最小转义
func TestJSONEscape(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`hello`, `hello`},
		{`a"b`, `a\"b`},
		{"a\nb", `a\nb`},
		{"a\tb\rc", `a\tb\rc`},
		{"line1\nline2 with \"quotes\"", `line1\nline2 with \"quotes\"`},
		{"with\x01ctrl", `withctrl`}, // 控制字符被丢弃
		{"with\\back", `with\\back`},
		{"", ""},
	}
	for _, tc := range cases {
		if got := jsonEscape(tc.in); got != tc.want {
			t.Errorf("jsonEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
