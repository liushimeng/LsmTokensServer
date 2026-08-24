package spider

// spider_core.go 在 v2.0.0 精简为 helper 工具集。
// 真正的爬虫引擎（Chrome 进程 + chromedp 生命周期）已迁移到 spider_cdp_browser.go。
// 这里保留的 helper 仍被 server_mcp_spider.go 中部分内容回退路径使用。
// 计划在 v2.1.0 进一步删除，本版本先保留以确保编译通过。

import (
	"regexp"
	"strings"
)

// extractTitle 从 HTML 中提取标题（regex 兜底）
func extractTitle(html string) string {
	reTitle := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	if matches := reTitle.FindStringSubmatch(html); len(matches) >= 2 {
		title := strings.TrimSpace(matches[1])
		title = htmlUnescape(title)
		if len(title) > 0 {
			return title
		}
	}

	reH1 := regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	if matches := reH1.FindStringSubmatch(html); len(matches) >= 2 {
		title := strings.TrimSpace(removeHTMLTags(matches[1]))
		if len(title) > 0 {
			return title
		}
	}

	return "Untitled"
}

// extractContent 从 HTML 中提取内容摘要（regex 兜底）
func extractContent(html string) string {
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	reComment := regexp.MustCompile(`(?is)<!--.*?-->`)
	html = reComment.ReplaceAllString(html, "")

	text := removeHTMLTags(html)

	reWhitespace := regexp.MustCompile(`\s+`)
	text = reWhitespace.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	maxLen := 5000
	if len(text) > maxLen {
		text = text[:maxLen] + "..."
	}

	return text
}

// removeHTMLTags 移除 HTML 标签
func removeHTMLTags(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return re.ReplaceAllString(s, "")
}

// htmlUnescape 简单的 HTML 实体解码
func htmlUnescape(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	return s
}
