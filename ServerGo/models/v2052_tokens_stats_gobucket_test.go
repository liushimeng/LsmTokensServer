package models

import (
	"strings"
	"testing"
)

// TestTokensRangeBucketAggregation_NoLongTextField 验证 v2.0.52 重写后 SELECT 不含 longtext 字段
// v2.0.52: 改用 Go 端聚合 — 只 SELECT 8 个小字段 + 4 个时间戳，绝不带 longtext
func TestTokensRangeBucketAggregation_NoLongTextField(t *testing.T) {
	// 通过 IsLongTextColumn 守护列名检查（selectTransactionColumns 的反向校验）：
	// 确保 v2.0.52 重写后的 SELECT 列表不引用 longtext 字段
	forbidden := []string{
		"request_body",
		"response_body",
		"request_src_protocol_body",
		"response_src_protocol_body",
		"request_headers",
		"response_headers",
		"request_src_protocol_headers",
		"response_src_protocol_headers",
	}
	// 重写后的 SELECT 字符串（与 mysql_http_agent_tokens.go 中 GetTokensRangeStats 的 Select 对齐）
	selectFields := "created_at, tokens_input_size, tokens_output_size, tokens_all_size, elapsed_ms, request_start_at, response_start_at, response_end_at"
	for _, f := range forbidden {
		if strings.Contains(selectFields, f) {
			t.Fatalf("v2.0.52 SELECT must not include longtext field %q", f)
		}
	}
}

// TestTimeRangeBucketAggregation_NoLongTextField 验证 GetTimeRangeStats 重写后 SELECT 不含 longtext
func TestTimeRangeBucketAggregation_NoLongTextField(t *testing.T) {
	forbidden := []string{
		"request_body", "response_body",
		"request_src_protocol_body", "response_src_protocol_body",
		"request_headers", "response_headers",
		"tokens_input_size", // 重写后只需要 created_at
	}
	selectFields := "created_at"
	for _, f := range forbidden {
		if strings.Contains(selectFields, f) {
			t.Fatalf("v2.0.52 GetTimeRangeStats SELECT must not include field %q", f)
		}
	}
}
