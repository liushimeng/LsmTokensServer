package models

import (
	"strings"
)

// normalizeAPIKey 归一化 API Key（自旧工程 ai_api_connectivity.go 提取的公共工具）
// 1. 剥离 header 名前缀（authorization: / x-api-key: / api-key:）
// 2. 剥离 Bearer scheme 前缀
// 3. 掩码残留判定：纯星号（前端脱敏展示值）视为未填写
func normalizeAPIKey(raw string) string {
	s := strings.TrimSpace(raw)
	for _, prefix := range []string{"authorization:", "x-api-key:", "api-key:"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	if len(s) >= 7 && strings.EqualFold(s[:7], "bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	if isMaskedAPIKeyValue(s) {
		return ""
	}
	return s
}

// isMaskedAPIKeyValue 判断是否为纯星号掩码值
func isMaskedAPIKeyValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '*' {
			return false
		}
	}
	return true
}
