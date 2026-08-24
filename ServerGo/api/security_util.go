package api

import (
	"fmt"
	"strings"
)

// ValidateField 输入校验（自旧工程 server_web_security.go 提取）
// ValidateField 通用字段校验：长度限制 + 危险字符过滤
func ValidateField(input string, maxLen int, fieldName string) (string, error) {
	if len(input) > maxLen {
		return "", fmt.Errorf("%s 长度超过限制（最大 %d 字符）", fieldName, maxLen)
	}
	sanitized, _ := SanitizeInput(input)
	return sanitized, nil
}

// SanitizeInput 过滤输入中的危险 SQL 关键字，返回过滤后的字符串和是否包含危险字符
func SanitizeInput(input string) (string, bool) {
	lower := strings.ToLower(input)
	for _, keyword := range DangerousSQLKeywords {
		if strings.Contains(lower, keyword) {
			// 替换危险关键字为空格
			input = strings.ReplaceAll(input, keyword, "")
			input = strings.ReplaceAll(strings.ToLower(input), keyword, "")
		}
	}
	// 移除多余的空格
	input = strings.Join(strings.Fields(input), " ")
	return input, true
}

var DangerousSQLKeywords = []string{
	"--", ";--", "/*", "*/", "@@", "@",
	"char", "nchar", "varchar", "nvarchar",
	"alter", "begin", "cast", "create", "cursor", "declare", "delete", "drop", "end", "exec", "execute",
	"fetch", "insert", "kill", "open", "select", "sys", "sysobjects", "syscolumns",
	"table", "update", "union", "waitfor", "delay",
}
