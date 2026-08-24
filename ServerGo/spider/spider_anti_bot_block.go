package spider

import (
	config "github.com/lishimeng/LsmTokensServer/config"
)

// ==================== v2.0.9 资源屏蔽 URL pattern 解析 ====================
//
// 提供：
//   - ResolveBlockURLPatterns(config.G) 根据 config.G 解析最终 block URL patterns 列表
//   - 返回空切片表示不屏蔽（保持 v2.0.8 行为）

import "strings"

// 默认 block patterns（CSS + 字体，不影响图片）
var defaultBlockPatterns = []string{
	"*.css",
	"*.woff",
	"*.woff2",
	"*.ttf",
	"*.otf",
}

// 图片 block patterns（仅在 SpiderBlockImageHeavy=true 时追加）
var imageBlockPatterns = []string{
	"*.png",
	"*.jpg",
	"*.jpeg",
	"*.gif",
	"*.webp",
	"*.svg",
	"*.ico",
}

// ResolveBlockURLPatterns 解析最终 block URL patterns
// 返回空切片表示不屏蔽（保持 v2.0.8 行为）
// 返回的列表已去重 + trim
func ResolveBlockURLPatterns(cfg *config.LsmTokensServerConfig) []string {
	if config.G == nil || !config.G.SpiderBlockResourcesEnabled {
		return nil
	}
	// 起始 = 内置默认
	out := make([]string, 0, len(defaultBlockPatterns)+len(imageBlockPatterns)+len(config.G.SpiderBlockedURLPatterns))
	out = append(out, defaultBlockPatterns...)
	if config.G.SpiderBlockImageHeavy {
		out = append(out, imageBlockPatterns...)
	}
	// 追加自定义（去重）
	seen := make(map[string]bool, len(out))
	for _, p := range out {
		seen[p] = true
	}
	for _, p := range config.G.SpiderBlockedURLPatterns {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
