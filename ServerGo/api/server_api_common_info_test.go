package api

// 阶段AA 顶部工具栏弹窗后端修复单测：safeProjectFilePath 越界/后缀/正常 + Git 接口参数

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeProjectFilePath(t *testing.T) {
	dir := t.TempDir()
	// 造一个正常 .md 文件
	good := filepath.Join(dir, "wiki.md")
	if err := os.WriteFile(good, []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"正常文件", "wiki.md", false},
		{"后缀不符", "wiki.txt", true},
		{"明文越界", "../secret.md", true},
		{"嵌套越界", "a/../../secret.md", true},
		{"绝对路径", "/etc/passwd.md", true},
		{"不存在", "nope.md", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := safeProjectFilePath(dir, c.rel, ".md")
			if (err != nil) != c.wantErr {
				t.Fatalf("rel=%q err=%v wantErr=%v", c.rel, err, c.wantErr)
			}
		})
	}

	// 前缀碰撞：/tmp/xxx 与 /tmp/xxx-sibling 前缀相同但越界，必须拒绝
	sibling := dir + "-sibling-md"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "evil.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := safeProjectFilePath(dir, "../"+filepath.Base(sibling)+"/evil.md", ".md"); err == nil {
		t.Fatal("前缀碰撞路径未被拒绝")
	}
}

func TestGitInfoInterfaceInvalidHash(t *testing.T) {
	// get_changes 分支：非法 hash 必须拒绝（防命令注入）
	req := httptest.NewRequest(http.MethodGet, "/GitInfoInterface?action=get_changes&hash=abc%3Brm%20-rf", nil)
	rec := httptest.NewRecorder()
	gitInfoInterfaceHandle(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "无效的 commit hash") {
		t.Fatalf("非法 hash 未被拒绝: %s", body)
	}
}

func TestGitInfoInterfaceHashPattern(t *testing.T) {
	bad := []string{"", "abc;rm -rf /", "HEAD~1", "zzzz", strings.Repeat("a", 41)}
	for _, h := range bad {
		if gitCommitHashPattern.MatchString(h) {
			t.Fatalf("hash %q 不应通过校验", h)
		}
	}
	good := []string{"abcd1234", strings.Repeat("a", 40), "A1B2C3"}
	for _, h := range good {
		if !gitCommitHashPattern.MatchString(h) {
			t.Fatalf("hash %q 应通过校验", h)
		}
	}
}
