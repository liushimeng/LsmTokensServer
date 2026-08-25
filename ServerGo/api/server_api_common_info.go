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