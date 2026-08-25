package api

// 阶段AG Wiki 知识库重构单测：
//   - 树构建（隐藏目录跳过 / go-web-debug-tool 跳过 / .md 与 other 区分）
//   - 分块读取（offset/limit 边界、limit 截断为 total_lines-offset）

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildWikiTreeBasic 构造临时项目根，验证：
//   - 目录与 .md / other 文件分类正确
//   - 隐藏目录与 go-web-debug-tool 被跳过
func TestBuildWikiTreeBasic(t *testing.T) {
	root := t.TempDir()

	// 写一个隐藏目录（应跳过）、一个被排除目录、一个正常目录与若干文件
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "ignored.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "go-web-debug-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go-web-debug-tool", "secret.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "子目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "INDEX.md"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "sub.md"), []byte("sub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "子目录", "中文.md"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "image.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	tree, files, dirs, err := getWikiTreeWithDir(root)
	if err != nil {
		t.Fatalf("getWikiTreeWithDir: %v", err)
	}
	// 期望 .md 文件：README, INDEX, sub, 中文 = 4
	if files != 4 {
		t.Fatalf("files=%d want=4", files)
	}
	// 期望目录：docs, 子目录 = 2
	if dirs != 2 {
		t.Fatalf("dirs=%d want=2", dirs)
	}
	// root 应有 docs 子目录与 README.md
	var docsNode *WikiNode
	var readmeNode *WikiNode
	for i := range tree.Children {
		c := &tree.Children[i]
		switch c.Name {
		case "docs":
			docsNode = c
		case "README.md":
			readmeNode = c
		}
	}
	if docsNode == nil {
		t.Fatal("缺少 docs 目录节点")
	}
	if docsNode.Type != "dir" {
		t.Fatalf("docs type=%q want dir", docsNode.Type)
	}
	if readmeNode == nil {
		t.Fatal("缺少 README.md 节点")
	}
	if readmeNode.Type != "file" {
		t.Fatalf("README.md type=%q want file", readmeNode.Type)
	}
	// .git 与 go-web-debug-tool 顶层不可见
	for _, c := range tree.Children {
		if c.Name == ".git" || c.Name == "go-web-debug-tool" {
			t.Fatalf("不应包含被排除目录: %s", c.Name)
		}
	}
	// docs/image.png 顶层应是 other
	var imageNode *WikiNode
	for i := range docsNode.Children {
		c := &docsNode.Children[i]
		if c.Name == "image.png" {
			imageNode = c
		}
	}
	if imageNode == nil {
		t.Fatal("docs/image.png 应存在")
	}
	if imageNode.Type != "other" {
		t.Fatalf("image.png type=%q want other", imageNode.Type)
	}
}

// TestWikiInterfaceGetContentChunk 验证 get_content 分块读取：
//   - 默认 limit=400
//   - limit 上限 4000 截断
//   - offset 越界归零到 total_lines
func TestWikiInterfaceGetContentChunk(t *testing.T) {
	root := t.TempDir()

	// 构造一个 1500 行的文件
	var buf bytes.Buffer
	for i := 0; i < 1500; i++ {
		buf.WriteString("line-")
		buf.WriteString(strings.Repeat("x", 10))
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(root, "big.md"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	type body struct {
		Action   string `json:"action"`
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	send := func(b body) map[string]interface{} {
		raw, _ := json.Marshal(b)
		req := httptest.NewRequest(http.MethodPost, "/WikiInterface", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		wikiInterfaceHandleWithDir(rec, req, root)
		out, _ := io.ReadAll(rec.Body)
		var m map[string]interface{}
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, string(out))
		}
		return m
	}

	// 1) 默认 limit（无参数）应 <= 400 行
	// 1500 行文件以 \n 结尾，Split 后 total_lines=1501（末尾空字符串），属预期。
	r1 := send(body{Action: "get_content", FilePath: "big.md"})
	if v, ok := r1["total_lines"]; !ok {
		t.Fatalf("缺少 total_lines: %v", r1)
	} else if int(v.(float64)) != 1501 {
		t.Fatalf("total_lines=%v want 1501（1500 行 + 末尾空字符串）", v)
	}
	if v, ok := r1["limit"]; !ok {
		t.Fatalf("缺少 limit: %v", r1)
	} else if int(v.(float64)) != 400 {
		t.Fatalf("默认 limit=%v want 400", v)
	}
	if v, ok := r1["has_more"]; !ok {
		t.Fatalf("缺少 has_more: %v", r1)
	} else if v.(bool) != true {
		t.Fatal("has_more 应为 true")
	}
	chunk := r1["content"].(string)
	if strings.Count(chunk, "\n") > 400 {
		t.Fatalf("首块超过 400 行: %d", strings.Count(chunk, "\n"))
	}

	// 2) limit 上限 4000（>1501 时取 1501）
	r2 := send(body{Action: "get_content", FilePath: "big.md", Offset: 0, Limit: 9999})
	if int(r2["limit"].(float64)) != 1501 {
		t.Fatalf("limit 截断失败: %v want 1501", r2["limit"])
	}
	if r2["has_more"].(bool) != false {
		t.Fatal("末尾块 has_more 应为 false")
	}

	// 3) offset 越界
	r3 := send(body{Action: "get_content", FilePath: "big.md", Offset: 9999, Limit: 100})
	if int(r3["offset"].(float64)) != 1501 {
		t.Fatalf("offset 越界归零失败: %v", r3["offset"])
	}
	if r3["has_more"].(bool) != false {
		t.Fatal("越界 offset 后 has_more 应为 false")
	}

	// 4) 路径越界
	r4 := send(body{Action: "get_content", FilePath: "../etc/passwd.md"})
	if _, ok := r4["error"]; !ok {
		t.Fatalf("路径越界应返回 error，实际: %v", r4)
	}
}

// TestWikiInterfaceListNoAction 验证默认分支返回 tree
func TestWikiInterfaceListNoAction(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/WikiInterface", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	wikiInterfaceHandleWithDir(rec, req, root)
	var m map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["tree"]; !ok {
		t.Fatal("响应应包含 tree 字段")
	}
	if int(m["total_files"].(float64)) != 1 {
		t.Fatalf("total_files=%v want 1", m["total_files"])
	}
	if _, ok := m["scanned_at"]; !ok {
		t.Fatal("响应应包含 scanned_at")
	}
}

// TestWikiExcludedDirNames 验证排除清单
func TestWikiExcludedDirNames(t *testing.T) {
	for _, name := range []string{".git", ".vscode", "go-web-debug-tool", "python-generate-image-tool"} {
		if !wikiShouldSkipDir(name) {
			t.Fatalf("应跳过: %s", name)
		}
	}
	for _, name := range []string{"docs", "ServerGo", "ClientWeb", "README.md"} {
		if wikiShouldSkipDir(name) {
			t.Fatalf("不应跳过: %s", name)
		}
	}
}
