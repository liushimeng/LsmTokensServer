package spider

// ==================== v2.0.9 资源屏蔽 URL pattern 解析 单元测试 ====================
//
// 覆盖 ResolveBlockURLPatterns：enabled/disabled、custom list、imageHeavy

import (
	config "github.com/lishimeng/LsmTokensServer/config"
	"sort"
	"strings"
	"testing"
)

// TestResolveBlockURLPatterns_Disabled 验证禁用时返回 nil（v2.0.8 行为）
func TestResolveBlockURLPatterns_Disabled(t *testing.T) {
	oldG := config.G
	defer func() { config.G = oldG }()
	config.G = &config.LsmTokensServerConfig{
		SpiderBlockResourcesEnabled: false,
		SpiderBlockedURLPatterns:    []string{"*.css"},
	}
	got := ResolveBlockURLPatterns(config.G)
	if got != nil {
		t.Errorf("disabled config.G should return nil, got %v", got)
	}
}

// TestResolveBlockURLPatterns_NilConfig 验证 nil config.G 返回 nil
func TestResolveBlockURLPatterns_NilConfig(t *testing.T) {
	if got := ResolveBlockURLPatterns(nil); got != nil {
		t.Errorf("nil config.G should return nil, got %v", got)
	}
}

// TestResolveBlockURLPatterns_Default 验证启用 + 空自定义时返回内置默认
func TestResolveBlockURLPatterns_Default(t *testing.T) {
	oldG := config.G
	defer func() { config.G = oldG }()
	config.G = &config.LsmTokensServerConfig{
		SpiderBlockResourcesEnabled: true,
		SpiderBlockedURLPatterns:    nil,
	}
	got := ResolveBlockURLPatterns(config.G)
	if len(got) < 4 {
		t.Errorf("default patterns length: got %d, want >= 4", len(got))
	}
	// 应包含 *.css / *.woff* / *.ttf / *.otf
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "*.css") {
		t.Error("default should contain *.css")
	}
	if !strings.Contains(joined, "*.ttf") {
		t.Error("default should contain *.ttf")
	}
}

// TestResolveBlockURLPatterns_CustomPreserved 验证自定义 pattern 被保留 + 去重
func TestResolveBlockURLPatterns_CustomPreserved(t *testing.T) {
	oldG := config.G
	defer func() { config.G = oldG }()
	config.G = &config.LsmTokensServerConfig{
		SpiderBlockResourcesEnabled: true,
		SpiderBlockedURLPatterns:    []string{"*.example.com", "*.test.io", "*.css"}, // *.css 重复
	}
	got := ResolveBlockURLPatterns(config.G)
	// 检查自定义项存在
	found := false
	for _, p := range got {
		if p == "*.example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Error("custom pattern *.example.com should be preserved")
	}
	// 去重：*.css 应只出现 1 次
	cssCount := 0
	for _, p := range got {
		if p == "*.css" {
			cssCount++
		}
	}
	if cssCount != 1 {
		t.Errorf("*.css deduplication: got %d, want 1", cssCount)
	}
}

// TestResolveBlockURLPatterns_ImageHeavy 验证 image heavy 追加图片 patterns
func TestResolveBlockURLPatterns_ImageHeavy(t *testing.T) {
	oldG := config.G
	defer func() { config.G = oldG }()
	config.G = &config.LsmTokensServerConfig{
		SpiderBlockResourcesEnabled: true,
		SpiderBlockImageHeavy:       true,
	}
	got := ResolveBlockURLPatterns(config.G)
	joined := strings.Join(got, ",")
	imagePatterns := []string{"*.png", "*.jpg", "*.webp"}
	for _, p := range imagePatterns {
		if !strings.Contains(joined, p) {
			t.Errorf("imageHeavy missing %s", p)
		}
	}
	// 排序确认非空
	sort.Strings(got)
	if len(got) < 10 {
		t.Errorf("expected ≥10 patterns with imageHeavy, got %d", len(got))
	}
}

// TestResolveBlockURLPatterns_EmptyStringsTrimmed 验证空字符串被过滤
func TestResolveBlockURLPatterns_EmptyStringsTrimmed(t *testing.T) {
	oldG := config.G
	defer func() { config.G = oldG }()
	config.G = &config.LsmTokensServerConfig{
		SpiderBlockResourcesEnabled: true,
		SpiderBlockedURLPatterns:    []string{"", "  ", "*.foo.com", "   "},
	}
	got := ResolveBlockURLPatterns(config.G)
	for _, p := range got {
		if strings.TrimSpace(p) == "" {
			t.Errorf("empty pattern leaked: %q", p)
		}
	}
}
