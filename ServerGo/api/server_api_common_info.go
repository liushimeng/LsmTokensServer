package api

// 通用信息接口（迁移自旧工程 server_web_common_dialog_handlers.go）
// 旧页面级 HTML handler 已废弃，仅迁移 JSON 数据接口，供 ClientWeb SPA 使用。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

// gitInfoInterfaceHandle 获取 git 仓库信息并返回 JSON
func gitInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	info, err := system.GetGitRepoInfo(0)
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
		// 安全检查：只允许读取项目目录下的 .go 文件
		if !strings.HasSuffix(req.FilePath, ".go") {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "只允许读取 .go 文件"})
			return
		}
		projectDir := system.GetProjectDir()
		absPath, err := filepath.Abs(req.FilePath)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "无效的文件路径"})
			return
		}
		absProjectDir, _ := filepath.Abs(projectDir)
		if !strings.HasPrefix(absPath, absProjectDir) {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "路径超出项目范围"})
			return
		}
		content, err := os.ReadFile(req.FilePath)
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

	readmePath := filepath.Join(system.GetProjectDir(), "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "读取 README.md 失败: " + err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"content": string(content)})
}
