package api

// 工具类公共接口（迁移自旧工程 server_web_common_dialog_cert_handlers.go /
// server_web_common_wiki.go / server_web_common_dialog_handlers.go 的 JSON 接口部分）：
//   - /CertDownloadInfoInterface /CertDownloadInterface  HTTPS 证书信息与下载
//   - /WikiInterface                                     项目 Wiki 文档列表/内容
//   - /UserInfoLogInterface                              用户操作日志分页/搜索（数据库版）
// 前端弹窗（HTML/CSS/JS）由 ClientWeb SPA 实现。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/system"
)

// ============================================================================
// HTTPS 证书下载
// ============================================================================

// CertDownloadInfoResponse 公钥下载信息 JSON 响应
type CertDownloadInfoResponse struct {
	AgentHost      string `json:"agent_host"`
	HttpsPort      int    `json:"https_port"`
	AnthropicPath  string `json:"anthropic_path"`
	OpenAIPath     string `json:"openai_path"`
	CertFile       string `json:"cert_file"`
	CertExists     bool   `json:"cert_exists"`
	CertSize       int64  `json:"cert_size"`
	HTTPSEnabled   bool   `json:"https_enabled"`
	UserWebEnabled bool   `json:"user_web_https_enabled"`
}

// resolveCertAbsolutePath 解析证书绝对路径（相对路径基于可执行文件目录）
// 返回 (绝对路径, 原始字符串) —— 当解析失败时回退原始字符串。
func resolveCertAbsolutePath(raw string) (string, string) {
	original := raw
	if strings.TrimSpace(raw) == "" {
		return "", original
	}
	abs, err := config.ResolvePath(raw)
	if err != nil {
		return raw, original
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return raw, original
	}
	return abs, original
}

// certDownloadInfoInterfaceHandle 公钥下载信息接口（POST/GET）
// 返回 HTTPS 代理地址 + 证书文件元信息，供前端弹窗动态渲染。
func certDownloadInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if config.G == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "config not available",
		})
		return
	}

	certPath, certOriginal := resolveCertAbsolutePath(config.G.UserWebCertFile)
	var certExists bool
	var certSize int64
	if certPath != "" {
		if info, err := os.Stat(certPath); err == nil && !info.IsDir() {
			certExists = true
			certSize = info.Size()
		}
	}

	host := strings.TrimSpace(config.G.AgentProductListenAddr)
	if host == "" {
		host = "127.0.0.1"
	}

	resp := CertDownloadInfoResponse{
		AgentHost:      host,
		HttpsPort:      config.G.AgentHttpsListenPort,
		AnthropicPath:  config.G.AgentAnthropicListenURL,
		OpenAIPath:     config.G.AgentOpenAIListenURL,
		CertFile:       certOriginal,
		CertExists:     certExists,
		CertSize:       certSize,
		HTTPSEnabled:   config.G.AgentHttpsListenPort > 0,
		UserWebEnabled: config.G.UserWebUseHTTPS,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// certDownloadInterfaceHandle 公钥文件下载接口（GET）
// 直接返回 userWebCertFile 指向的公钥文件流。
// 浏览器访问后会触发文件下载；Content-Type 按证书类型给出 application/x-x509-ca-cert。
func certDownloadInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	if config.G == nil {
		http.Error(w, "config not available", http.StatusInternalServerError)
		return
	}

	certPath, certOriginal := resolveCertAbsolutePath(config.G.UserWebCertFile)
	if certPath == "" {
		http.Error(w, "userWebCertFile is empty in config", http.StatusNotFound)
		return
	}

	info, err := os.Stat(certPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "cert file not found: "+certOriginal, http.StatusNotFound)
			return
		}
		http.Error(w, "stat cert file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "cert path is a directory, not a file", http.StatusBadRequest)
		return
	}

	// 简单的路径合法性兜底：必须存在 + 是普通文件 + 文件大小 < 10MB（避免误传大文件）
	if info.Size() > 10*1024*1024 {
		http.Error(w, "cert file too large (>10MB)", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(certPath)
	if err != nil {
		http.Error(w, "read cert file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 设置下载头：使用原始文件名（certOriginal），避免暴露绝对路径
	fileName := filepath.Base(certOriginal)
	if fileName == "" || fileName == "." || fileName == "/" {
		fileName = "server.crt"
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// ============================================================================
// Wiki 文档接口
// ============================================================================

// WikiFileInfo Wiki文件信息
type WikiFileInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// getWikiFiles 递归获取项目目录下所有 .md 文件
// 排除 go-web-debug-tool 目录和隐藏目录（以 . 开头的目录）
func getWikiFiles() ([]WikiFileInfo, error) {
	var files []WikiFileInfo
	projectDir := system.GetProjectDir()

	err := filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
		}
		if info.IsDir() {
			// 跳过隐藏目录（以 . 开头）
			if strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			// 跳过 go-web-debug-tool 目录
			if info.Name() == "go-web-debug-tool" {
				return filepath.SkipDir
			}
			return nil
		}
		// 只收集 .md 文件
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		// 转换为相对路径
		relPath, _ := filepath.Rel(projectDir, path)
		if relPath == "" {
			relPath = path
		}
		// 统一使用正斜杠
		relPath = strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		files = append(files, WikiFileInfo{
			Path: relPath,
			Name: info.Name(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 按路径排序
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	return files, nil
}

// wikiInterfaceHandle Wiki接口
// 支持 action: list（列表）和 get_content（获取单个文件内容，Markdown 由前端渲染）
func wikiInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	var req struct {
		Action   string `json:"action"`
		FilePath string `json:"file_path"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if req.Action == "get_content" && req.FilePath != "" {
		// 安全校验统一走 safeProjectFilePath（filepath.Rel 判定，防前缀碰撞越界）
		absPath, err := safeProjectFilePath(system.GetProjectDir(), req.FilePath, ".md")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "读取文件失败: " + err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"path":    req.FilePath,
			"content": string(content),
		})
		return
	}

	// 默认返回文件列表
	files, err := getWikiFiles()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
		"count": len(files),
	})
}

// ============================================================================
// 用户操作日志（数据库持久化，支持结构化分页查询）
// ============================================================================

// userInfoLogInterfaceHandle 查询用户操作日志（数据库版，支持分页/关键词/类型/用户筛选）
func userInfoLogInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	// 解析请求参数
	var req struct {
		PageNum       int    `json:"page_num"`
		PageSize      int    `json:"page_size"`
		SearchKeyword string `json:"search_keyword"`
		ActionType    string `json:"action_type"`
		UserName      string `json:"user_name"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// 分页参数校验
	pageSize := req.PageSize
	switch pageSize {
	case 10, 20, 50, 100:
		// 允许的分页大小
	default:
		pageSize = 20 // 默认 20 条
	}
	pageNum := req.PageNum
	if pageNum <= 0 {
		pageNum = 1
	}

	// 查询数据库
	records, totalCount, err := modelsdb.QueryUserOperationLogs(pageNum, pageSize, req.SearchKeyword, req.ActionType, req.UserName)
	if err != nil {
		// 数据库未初始化或其他错误，返回空结果
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"records":     []interface{}{},
			"total_count": 0,
			"total_pages": 0,
			"page_num":    pageNum,
			"page_size":   pageSize,
		})
		return
	}

	// 计算总页数
	totalPages := 0
	if totalCount > 0 {
		totalPages = int((totalCount + int64(pageSize) - 1) / int64(pageSize))
	}
	if pageNum > totalPages && totalPages > 0 {
		pageNum = totalPages
	}

	// 格式化记录为前端需要的格式
	type logRecord struct {
		ID         uint64 `json:"id"`
		CreatedAt  string `json:"created_at"`
		ActionType string `json:"action_type"`
		UserName   string `json:"user_name"`
		Details    string `json:"details"`
	}
	formatted := make([]logRecord, 0, len(records))
	for _, rec := range records {
		formatted = append(formatted, logRecord{
			ID:         rec.ID,
			CreatedAt:  rec.CreatedAt.Format("2006-01-02 15:04:05"),
			ActionType: rec.ActionType,
			UserName:   rec.UserName,
			Details:    rec.Details,
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"records":     formatted,
		"total_count": totalCount,
		"total_pages": totalPages,
		"page_num":    pageNum,
		"page_size":   pageSize,
	})
}
