package models

import (
	"regexp"
)

// 敏感信息脱敏工具（自旧工程 server_http_ai_proxy_security.go 提取，
// models 层写库/日志时即需要脱敏，属公共基础能力；阶段3 迁移 proxy 安全模块时复用）

var authorizationBearerHeaderTextRegexp = regexp.MustCompile(`(?im)(^|[\r\n])([ \t]*Authorization[ \t]*:[ \t]*Bearer[ \t]+)([^\s\r\n]+)`)

const authorizationBearerAPIKeyMask = "************************"

// redactAuthorizationBearerHeaderText 将文本形式的请求头中的 Bearer Token 掩码
func redactAuthorizationBearerHeaderText(headers string) string {
	if headers == "" {
		return ""
	}
	return authorizationBearerHeaderTextRegexp.ReplaceAllString(headers, "${1}${2}"+authorizationBearerAPIKeyMask)
}
