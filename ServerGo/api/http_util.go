package api

import (
	"net/http"
)

// SetNoCacheHeaders 设置禁止浏览器缓存的响应头
// （自旧工程 server_web_common.go 提取的 HTTP 公共工具；保持原函数名 setNoCacheHeaders 语义，
//
//	导出供 api 包内及后续 web 层复用）
func setNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}
