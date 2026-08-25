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

	"github.com/lishimeng/LsmTokensServer/system"
)

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

// ---- 构建日志接口（阶段AK） ----

// buildLogEntry 单条构建日志记录（对应 JSON-lines 每行）
type buildLogEntry struct {
	Time       string `json:"time"`
	GitHash    string `json:"git_hash"`
	GitMsg     string `json:"git_msg"`
	Mode       string `json:"mode"`
	WebOk      *bool  `json:"web_ok"`      // nil=跳过, true=成功, false=失败
	BackendOk  *bool  `json:"backend_ok"`   // nil=未知, true=成功, false=失败
}

// buildLogInterfaceHandle 读取构建日志文件，返回分页的结构化构建记录（阶段AK）
// 支持 JSON-lines 格式（新）和纯时间戳行（旧，降级兼容）。
// 参数：page_num（默认1）、page_size（默认20，范围10-100）
func buildLogInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	projectDir := system.GetProjectDir()
	// 日志文件位于工程根目录（可执行文件所在目录或其上级），逐级向上查找
	logPath := findBuildLogPath(projectDir)

	// 读取日志文件
	allLines, err := os.ReadFile(logPath)
	if err != nil {
		// 文件不存在或不可读，返回空结果
		json.NewEncoder(w).Encode(map[string]interface{}{
			"records":     []buildLogEntry{},
			"totalCount":  0,
			"totalPages":  0,
			"currentPage": 1,
		})
		return
	}

	// 逐行解析
	rawLines := strings.Split(strings.TrimSpace(string(allLines)), "\n")
	var entries []buildLogEntry
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry buildLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			entries = append(entries, entry)
		} else {
			// 降级兼容：旧格式纯时间戳行
			entries = append(entries, buildLogEntry{Time: line})
		}
	}

	// 解析分页参数
	pageNum := 1
	pageSize := 20
	if v, e := strconv.Atoi(r.URL.Query().Get("page_num")); e == nil && v > 0 {
		pageNum = v
	}
	if v, e := strconv.Atoi(r.URL.Query().Get("page_size")); e == nil && v >= 10 && v <= 100 {
		pageSize = v
	}

	total := len(entries)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	if pageNum > totalPages && totalPages > 0 {
		pageNum = totalPages
	}

	// 分页切片
	start := (pageNum - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	pageRecords := entries[start:end]
	if pageRecords == nil {
		pageRecords = []buildLogEntry{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"records":     pageRecords,
		"totalCount":  total,
		"totalPages":  totalPages,
		"currentPage": pageNum,
	})
}

// findBuildLogPath 查找构建日志文件路径（工程根目录，可执行文件所在目录或其上级）
func findBuildLogPath(projectDir string) string {
	candidates := []string{
		filepath.Join(projectDir, "LsmTokensServerBuildDateTime.log"),
		filepath.Join(projectDir, "..", "LsmTokensServerBuildDateTime.log"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 默认返回第一个候选（即使不存在，让后续 ReadFile 返回空结果）
	return candidates[0]
}