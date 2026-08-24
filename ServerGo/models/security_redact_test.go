package models

// Bearer 头脱敏测试（迁移自旧工程 test_header_redaction_test.go）

import (
	"strings"
	"testing"
)

func TestRedactAuthorizationBearerHeaderText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "standard authorization bearer",
			in:   "Authorization: Bearer sk-live-abc\nContent-Type: application/json",
			want: "Authorization: Bearer ************************\nContent-Type: application/json",
		},
		{
			name: "mixed case authorization and bearer",
			in:   "authorization: bearer sk-live-abc",
			want: "authorization: bearer ************************",
		},
		{
			name: "leading whitespace and flexible spacing",
			in:   "  Authorization :   Bearer   sk-live-abc",
			want: "  Authorization :   Bearer   ************************",
		},
		{
			name: "crlf multiline",
			in:   "Host: example.com\r\nAuthorization: Bearer sk-live-abc\r\nContent-Type: application/json",
			want: "Host: example.com\r\nAuthorization: Bearer ************************\r\nContent-Type: application/json",
		},
		{
			name: "multiple authorization lines",
			in:   "Authorization: Bearer key1\nAuthorization: Bearer key2",
			want: "Authorization: Bearer ************************\nAuthorization: Bearer ************************",
		},
		{
			name: "non bearer authorization unchanged",
			in:   "Authorization: Basic abc",
			want: "Authorization: Basic abc",
		},
		{
			name: "proxy authorization unchanged",
			in:   "Proxy-Authorization: Bearer abc",
			want: "Proxy-Authorization: Bearer abc",
		},
		{
			name: "already masked is idempotent",
			in:   "Authorization: Bearer ************************",
			want: "Authorization: Bearer ************************",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactAuthorizationBearerHeaderText(c.in)
			if got != c.want {
				t.Fatalf("RedactAuthorizationBearerHeaderText() = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "sk-live-abc") || strings.Contains(got, "key1") || strings.Contains(got, "key2") {
				t.Fatalf("redacted header still contains raw key: %q", got)
			}
		})
	}
}
