package spider

import (
	"os"
	"testing"
)

// TestDetectModuleScriptInjection_ModuleScript 检测 createElement + type=module 注入
func TestDetectModuleScriptInjection_ModuleScript(t *testing.T) {
	cases := []struct {
		expr     string
		expected bool
	}{
		// 应被拦截：createElement + type=module
		{
			expr:     `(function(){ var s = document.createElement('script'); s.type = 'module'; s.src = 'https://example.com/main.js'; document.head.appendChild(s); })()`,
			expected: true,
		},
		// 应被拦截：双引号版本
		{
			expr:     `var s=document.createElement("script");s.type="module";document.head.appendChild(s);`,
			expected: true,
		},
		// 应被拦截：innerHTML 含 script type=module（用反引号避免转义问题）
		{
			expr:     `document.body.innerHTML = ` + "`<script type=\"module\" src=\"x.js\"></script>`;",
			expected: true,
		},
		// 应被拦截：insertAdjacentHTML
		{
			expr:     `document.head.insertAdjacentHTML('beforeend', ` + "`<script type=module src=\"x.js\"></script>`);",
			expected: true,
		},
		// 不应被拦截：普通 eval 表达式
		{
			expr:     `return document.title;`,
			expected: false,
		},
		// 不应被拦截：普通 script 但不带 module
		{
			expr:     `var s = document.createElement('script'); s.src = 'https://example.com/main.js'; document.head.appendChild(s);`,
			expected: false,
		},
		// 不应被拦截：包含 script 但不包含 module
		{
			expr:     `document.querySelectorAll('script').length`,
			expected: false,
		},
		// 不应被拦截：包含 module 但不包含 script
		{
			expr:     `return typeof module !== 'undefined';`,
			expected: false,
		},
		// 不应被拦截：空字符串
		{
			expr:     "",
			expected: false,
		},
		// 应被拦截：document.write + script type=module
		{
			expr:     `document.write(` + "`<script type=module src=\"x.js\"></script>`);",
			expected: true,
		},
	}

	for _, tc := range cases {
		got := detectModuleScriptInjection(tc.expr)
		if got != tc.expected {
			t.Errorf("detectModuleScriptInjection(%q) = %v, want %v", tc.expr, got, tc.expected)
		}
	}
}

// TestDetectModuleScriptInjection_CaseInsensitive 验证检测是大小写不敏感的
func TestDetectModuleScriptInjection_CaseInsensitive(t *testing.T) {
	cases := []struct {
		expr     string
		expected bool
	}{
		// 大写 CREATEELEMENT
		{
			expr:     `document.CREATEELEMENT('script').type='module'`,
			expected: true,
		},
		// 大写 MODULE
		{
			expr:     `document.createElement('script').type='MODULE'`,
			expected: true,
		},
		// 混合大小写
		{
			expr:     `document.CreateElement('script').Type='Module'`,
			expected: true,
		},
	}

	for _, tc := range cases {
		got := detectModuleScriptInjection(tc.expr)
		if got != tc.expected {
			t.Errorf("detectModuleScriptInjection(%q) = %v, want %v", tc.expr, got, tc.expected)
		}
	}
}

// TestDetectModuleScriptInjection_EdgeCases 边界情况
func TestDetectModuleScriptInjection_EdgeCases(t *testing.T) {
	// 包含 script 和 module 但不在同一上下文
	expr := `var module = 'test'; var script = 'test'; return module + script;`
	if detectModuleScriptInjection(expr) {
		t.Errorf("should not detect injection for unrelated script+module usage: %s", expr)
	}

	// 包含 appendChild 和 module 但不包含 script
	expr2 := `var el = document.createElement('div'); el.textContent = 'module'; document.body.appendChild(el);`
	if detectModuleScriptInjection(expr2) {
		t.Errorf("should not detect injection for non-script appendChild: %s", expr2)
	}
}

// TestCleanupChromeUserDataDir_SafePaths 只清理临时目录
func TestCleanupChromeUserDataDir_SafePaths(t *testing.T) {
	// 非临时目录不应被清理
	nonTempDirs := []string{
		"/home/user/.config/google-chrome",
		"/opt/chrome-data",
		"./chrome-data",
		"chrome-data",
	}
	for _, dir := range nonTempDirs {
		// 不应 panic 或返回错误
		if err := cleanupChromeUserDataDir(dir); err != nil {
			t.Errorf("cleanupChromeUserDataDir(%q) should return nil for non-temp dir, got %v", dir, err)
		}
	}
}

// TestCleanupChromeUserDataDir_TempDir 清理临时目录
func TestCleanupChromeUserDataDir_TempDir(t *testing.T) {
	// 创建临时目录结构
	tmpDir := t.TempDir()
	chromeDir := tmpDir + "/chrome-test"

	// 创建一些子目录
	subDirs := []string{
		"Default/Cache",
		"Default/Code Cache",
		"Default/Cookies", // 不应被清理
		"ShaderCache",
		"blob_storage",
	}
	for _, sub := range subDirs {
		path := chromeDir + "/" + sub
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		// 写个文件进去
		if err := os.WriteFile(path+"/test.txt", []byte("test"), 0644); err != nil {
			t.Fatalf("write %s: %v", path+"/test.txt", err)
		}
	}

	// 执行清理
	if err := cleanupChromeUserDataDir(chromeDir); err != nil {
		t.Fatalf("cleanupChromeUserDataDir failed: %v", err)
	}

	// 验证被清理的目录不存在了
	for _, sub := range []string{"Default/Cache", "Default/Code Cache", "ShaderCache", "blob_storage"} {
		path := chromeDir + "/" + sub
		if _, err := os.Stat(path); err == nil {
			t.Errorf("expected %s to be removed, but still exists", path)
		}
	}

	// 验证不应被清理的目录仍然存在
	cookiesPath := chromeDir + "/Default/Cookies"
	if _, err := os.Stat(cookiesPath); err != nil {
		t.Errorf("expected %s to remain, but was removed", cookiesPath)
	}
}
