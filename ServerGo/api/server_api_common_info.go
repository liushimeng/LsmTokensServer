package api

// 通用信息接口（迁移自旧工程 server_web_common_dialog_handlers.go）
// 旧页面级 HTML handler 已废弃，仅迁移 JSON 数据接口，供 ClientWeb SPA 使用。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/system"
)

// buildTimeLogInterfaceHandle 读取编译时间日志文件并返回 JSON
func buildTimeLogInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	logPath := config.G.BuildDateTimeLogURL
	lines := []string{}

	if data, err := os.ReadFile(logPath); err == nil {
		allLines := strings.Split(strings.TrimSpace(string(data)), "\n")
		for _, line := range allLines {
			line = strings.TrimSpace(line)
			if line != "" {
				lines = append(lines, line)
			}
		}
	}

	resp := struct {
		Lines []string `json:"lines"`
		Count int      `json:"count"`
	}{
		Lines: lines,
		Count: len(lines),
	}
	json.NewEncoder(w).Encode(resp)
}

// safeProjectFilePath 项目内文件读取统一安全校验（阶段AA）：
// 后缀必须是 ext；相对路径不得越出 projectDir（用 filepath.Rel 判定，彻底消除
// 旧 HasPrefix 方案在同级前缀碰撞目录上的绕过）；必须是 <5MB 的普通文件。
// 校验通过返回绝对路径，否则返回错误。
func safeProjectFilePath(projectDir, relPath, ext string) (string, error) {
	if !strings.HasSuffix(relPath, ext) {
		return "", fmt.Errorf("只允许读取 %s 文件", ext)
	}
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("无效的项目目录")
	}
	absPath, err := filepath.Abs(filepath.Join(absProjectDir, relPath))
	if err != nil {
		return "", fmt.Errorf("无效的文件路径")
	}
	rel, err := filepath.Rel(absProjectDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("路径超出项目范围")
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("路径是目录而非文件")
	}
	if info.Size() > 5*1024*1024 {
		return "", fmt.Errorf("文件过大（>5MB）")
	}
	return absPath, nil
}

// gitCommitHashPattern 合法 commit hash（4~40 位十六进制），防命令注入
var gitCommitHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// gitInfoInterfaceHandle 获取 git 仓库信息并返回 JSON（阶段AA 重构）：
//   - 默认返回轻量提交列表（单次 git log，默认 100 条，limit 参数可调，上限 500），
//     不再附带每提交文件变更（旧实现 N+1 次 git show 子进程）；
//   - action=get_changes&hash=xxx 惰性返回单次提交的文件变更。
func gitInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 惰性查询单提交文件变更
	if r.URL.Query().Get("action") == "get_changes" {
		hash := strings.TrimSpace(r.URL.Query().Get("hash"))
		if !gitCommitHashPattern.MatchString(hash) {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "无效的 commit hash"})
			return
		}
		changes, err := system.GetGitCommitChanges(hash)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"hash": hash, "changes": changes})
		return
	}

	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 500 {
		limit = 500
	}

	info, err := system.GetGitRepoInfoLight(limit)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   err.Error(),
			"branch":  "unknown",
			"commits": []interface{}{},
			"count":   0,
		})
		return
	}
	json.NewEncoder(w).Encode(info)
}

// systemInfoInterfaceHandle 获取系统信息并返回 JSON
func systemInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	info, err := system.GetSystemInfo()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(info)
}

// sourceCodeInterfaceHandle 获取源码统计信息（action: list / get_content）
func sourceCodeInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Action   string `json:"action"`
		FilePath string `json:"file_path"`
	}
	if r.Method == http.MethodPost {
		json.NewDecoder(r.Body).Decode(&req)
	}

	if req.Action == "get_content" && req.FilePath != "" {
		// 安全校验统一走 safeProjectFilePath（filepath.Rel 判定，防前缀碰撞越界）
		absPath, err := safeProjectFilePath(system.GetProjectDir(), req.FilePath, ".go")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "读取文件失败: " + err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"content": string(content)})
		return
	}

	stats, err := system.GetSourceCodeStats()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(stats)
}

// readmeInterfaceHandle 读取 README.md 内容返回 JSON（前端自行渲染 Markdown）
func readmeInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 与旧工程一致：读取工程根目录 README.md。
	// 二进制可能位于 ServerGo/ 子目录，故先取可执行文件目录，找不到再回退上一级（工程根）。
	projectDir := system.GetProjectDir()
	content, err := os.ReadFile(filepath.Join(projectDir, "README.md"))
	if err != nil {
		content, err = os.ReadFile(filepath.Join(filepath.Dir(projectDir), "README.md"))
	}
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "读取 README.md 失败: " + err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"content": string(content)})
}
