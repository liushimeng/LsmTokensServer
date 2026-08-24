package spider

import (
	"fmt"
	"regexp"
	"strings"
)

// ==================== 多轮交互辅助函数 ====================

// buildNextPageURL 构造"下一页"URL（用于 scroll 动作）
func buildNextPageURL(currentURL string, params map[string]interface{}) string {
	step := 1
	if params != nil {
		if t, ok := params["times"]; ok {
			switch v := t.(type) {
			case float64:
				step = int(v)
			case int:
				step = v
			}
		}
	}
	if step < 1 {
		step = 1
	}

	patterns := []struct {
		prefix string
		format string
	}{
		{"?page=", "?page=%d"},
		{"?p=", "?p=%d"},
		{"?offset=", "?offset=%d"},
		{"?start=", "?start=%d"},
		{"/page/", "/page/%d"},
	}
	for _, p := range patterns {
		idx := strings.Index(currentURL, p.prefix)
		if idx < 0 {
			continue
		}
		tail := currentURL[idx+len(p.prefix):]
		endIdx := -1
		for i, r := range tail {
			if r < '0' || r > '9' {
				endIdx = i
				break
			}
		}
		if endIdx == 0 {
			continue
		}
		var num int
		if endIdx < 0 {
			fmt.Sscanf(tail, "%d", &num)
		} else {
			fmt.Sscanf(tail[:endIdx], "%d", &num)
		}
		newNum := num + step
		var newTail string
		if endIdx < 0 {
			newTail = fmt.Sprintf(p.format, newNum)
		} else {
			newTail = fmt.Sprintf(p.format, newNum) + tail[endIdx:]
		}
		return currentURL[:idx] + newTail
	}
	return currentURL
}

// resolveURL 把相对 URL 转绝对
func resolveURL(baseURL, ref string) string {
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if strings.HasPrefix(ref, "//") {
		if strings.HasPrefix(baseURL, "https://") {
			return "https:" + ref
		}
		return "http:" + ref
	}
	if strings.HasPrefix(ref, "/") {
		if idx := strings.Index(baseURL, "://"); idx > 0 {
			rest := baseURL[idx+3:]
			slash := strings.Index(rest, "/")
			if slash > 0 {
				return baseURL[:idx+3] + rest[:slash] + ref
			}
			return baseURL[:idx+3] + rest + ref
		}
		return ref
	}
	idx := strings.LastIndex(baseURL, "/")
	if idx > 0 {
		return baseURL[:idx+1] + ref
	}
	return ref
}

// findHrefBySelector 在 HTML 中查找匹配 selector 的第一个 a href
// Deprecated: v2.0.0 起 click action 通过 chromedp DOM.spider.querySelector + Element.click 实现，
// 此函数仅保留作为旧实现的兜底；请勿在新增代码中使用。
func findHrefBySelector(html, selector string) string {
	if html == "" || selector == "" {
		return ""
	}
	hrefRe := regexp.MustCompile(`(?is)<a[^>]*href="([^"]+)"[^>]*>([^<]*)`)
	matches := hrefRe.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		href := m[1]
		tagHTML := m[0]
		switch {
		case strings.HasPrefix(selector, "."):
			className := strings.TrimPrefix(selector, ".")
			if strings.Contains(tagHTML, `class="`+className+`"`) ||
				strings.Contains(tagHTML, ` class="`+className+`"`) ||
				strings.Contains(tagHTML, ` `+className+`"`) {
				return href
			}
		case strings.HasPrefix(selector, "#"):
			id := strings.TrimPrefix(selector, "#")
			if strings.Contains(tagHTML, `id="`+id+`"`) {
				return href
			}
		case strings.HasPrefix(selector, "text:"):
			kw := strings.TrimPrefix(selector, "text:")
			if strings.Contains(m[2], kw) {
				return href
			}
		default:
			tagName := selector
			if strings.HasPrefix(tagHTML, "<"+tagName) || strings.HasPrefix(tagHTML, "<"+tagName+" ") {
				return href
			}
		}
	}
	return ""
}
