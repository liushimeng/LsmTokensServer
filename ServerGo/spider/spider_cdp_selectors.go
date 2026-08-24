package spider

import (
	"fmt"
	"strings"
)

// ==================== 选择器解析器 ====================
// 把 Agent 传入的 selector 字符串翻译为 chromedp 可用的 query。
// 语法：
//   - ".foo"        CSS class
//   - "#bar"        CSS id
//   - "tag"         CSS tag
//   - "tag.cls"     CSS 复合
//   - "text:keyword" 文本包含（生成 JS 包装器）
//   - "xpath://div"  XPath

// selectorStrategy 选择器类型
type selectorStrategy int

const (
	selCSS   selectorStrategy = iota // CSS selector（默认）
	selXPath                         // XPath
	selText                          // 文本包含
)

// parsedSelector 解析后的选择器
type parsedSelector struct {
	Raw      string
	Strategy selectorStrategy
	Query    string
	// TextKeyword 仅 selText 时使用
	TextKeyword string
}

// parseSelector 解析选择器字符串
func parseSelector(in string) parsedSelector {
	in = strings.TrimSpace(in)
	if in == "" {
		return parsedSelector{Raw: in, Strategy: selCSS, Query: ""}
	}
	if strings.HasPrefix(in, "xpath:") {
		return parsedSelector{
			Raw:      in,
			Strategy: selXPath,
			Query:    strings.TrimSpace(strings.TrimPrefix(in, "xpath:")),
		}
	}
	if strings.HasPrefix(in, "text:") {
		kw := strings.TrimSpace(strings.TrimPrefix(in, "text:"))
		return parsedSelector{
			Raw:         in,
			Strategy:    selText,
			Query:       "",
			TextKeyword: kw,
		}
	}
	return parsedSelector{
		Raw:      in,
		Strategy: selCSS,
		Query:    in,
	}
}

// String 返回可读形式（用于日志）
func (p parsedSelector) String() string {
	switch p.Strategy {
	case selXPath:
		return fmt.Sprintf("xpath:%s", p.Query)
	case selText:
		return fmt.Sprintf("text:%s", p.TextKeyword)
	default:
		return p.Query
	}
}

// textSearchJS 生成 selText 对应的 JS 选择器
// 在 document 中查找第一个 textContent 包含关键字且无子元素的节点
func (p parsedSelector) textSearchJS() string {
	// 用 quote 处理转义
	return fmt.Sprintf(`(function(){
  const kw = %q;
  function find(node) {
    if (!node) return null;
    if (node.nodeType === 3) { // text
      return node.textContent.includes(kw) ? node.parentElement : null;
    }
    for (const c of node.childNodes) {
      const r = find(c);
      if (r) return r;
    }
    return null;
  }
  return find(document.body);
})()`, p.TextKeyword)
}
