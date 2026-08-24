package spider

// ==================== v2.0.9 Stealth Pro 单元测试 ====================
//
// 覆盖 buildStealthProJS / defaultStealthProFonts；不依赖 Chrome / 网络

import (
	"strings"
	"testing"
)

// TestBuildStealthProJS_ContainsMediaDevices 验证 MediaDevices 模拟已注入
func TestBuildStealthProJS_ContainsMediaDevices(t *testing.T) {
	js := buildStealthProJS(nil, nil)
	if !strings.Contains(js, "enumerateDevices") {
		t.Error("stealthPro JS should contain 'enumerateDevices'")
	}
	if !strings.Contains(js, "audioinput") || !strings.Contains(js, "videoinput") {
		t.Error("stealthPro JS should contain audioinput/videoinput device kinds")
	}
}

// TestBuildStealthProJS_ContainsFontList 验证字体列表已注入
func TestBuildStealthProJS_ContainsFontList(t *testing.T) {
	js := buildStealthProJS(nil, nil)
	if !strings.Contains(js, "document.fonts") {
		t.Error("stealthPro JS should reference document.fonts")
	}
	// 默认字体列表至少 20 个
	count := 0
	for _, f := range defaultStealthProFonts {
		if strings.Contains(js, f) {
			count++
		}
	}
	if count < 20 {
		t.Errorf("expected ≥20 default fonts in JS, got %d", count)
	}
}

// TestBuildStealthProJS_ContainsStackSanitize 验证错误堆栈净化已注入
func TestBuildStealthProJS_ContainsStackSanitize(t *testing.T) {
	js := buildStealthProJS(nil, nil)
	if !strings.Contains(js, "Error.prototype.toString") {
		t.Error("stealthPro JS should patch Error.prototype.toString")
	}
	if !strings.Contains(js, "puppeteer") || !strings.Contains(js, "cdp") {
		t.Error("stealthPro JS should reference 'puppeteer' and 'cdp' in stack redaction")
	}
}

// TestBuildStealthProJS_ContainsChromeRuntime 验证 chrome.runtime 强化
func TestBuildStealthProJS_ContainsChromeRuntime(t *testing.T) {
	js := buildStealthProJS(nil, nil)
	if !strings.Contains(js, "chrome.runtime") {
		t.Error("stealthPro JS should patch chrome.runtime")
	}
	if !strings.Contains(js, "onMessage") {
		t.Error("chrome.runtime.onMessage should be augmented")
	}
}

// TestDefaultStealthProFonts_NonEmpty 验证默认字体列表非空
func TestDefaultStealthProFonts_NonEmpty(t *testing.T) {
	if len(defaultStealthProFonts) < 20 {
		t.Errorf("defaultStealthProFonts length: got %d, want ≥20", len(defaultStealthProFonts))
	}
}

// TestBuildStealthProJS_CustomFonts 验证自定义字体覆盖
func TestBuildStealthProJS_CustomFonts(t *testing.T) {
	custom := []string{"FooBarFont", "BazFont"}
	js := buildStealthProJS(nil, custom)
	for _, f := range custom {
		if !strings.Contains(js, f) {
			t.Errorf("custom font %q should appear in JS", f)
		}
	}
	// 默认字体应被替换（不应全部出现）
	if strings.Contains(js, "Liberation Sans") {
		t.Error("custom font list should REPLACE default list, not append")
	}
}
