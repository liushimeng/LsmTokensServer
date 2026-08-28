package models

// 阶段AU（v2.0.75）：response_body 详情读取 base64 解码修复的单测。
//
// 背景：SaveAgentHttpTransaction 对 respBody 统一 base64 编码落库（规避 MySQL utf8
// Error 1366）；GetAgentHttpTransactionFieldByID 此前只解码 request_body，response_body
// 漏解码导致 /ChatAnalysis 前端显示 Base64 乱码、SSE/聚合解析失效。
// 本文件覆盖 tryDecodeBase64Text 的边界行为（纯函数，不依赖 DB）。

import (
	"encoding/base64"
	"testing"
)

func TestTryDecodeBase64Text(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
	}{
		{"空串原样返回", "", ""},
		{"合法 base64 JSON 解码", base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)), `{"ok":true}`},
		{"合法 base64 SSE 文本解码", base64.StdEncoding.EncodeToString([]byte("data: {\"type\":\"message_start\"}\n\n")), "data: {\"type\":\"message_start\"}\n\n"},
		{"明文 JSON 含非法字符不误解码", `{"model":"claude-3"}`, `{"model":"claude-3"}`},
		{"明文 SSE 含冒号不误解码", "data: hello\n\n", "data: hello\n\n"},
		{"长度非 4 倍数原样返回", "abc", "abc"},
		{"非法字符集原样返回", "a===b", "a===b"},
	}
	for _, c := range cases {
		if got := tryDecodeBase64Text(c.in); got != c.want {
			t.Errorf("%s: tryDecodeBase64Text(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
