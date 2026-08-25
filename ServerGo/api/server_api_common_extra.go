package api

// 工具类公共接口（迁移自旧工程 server_web_common_dialog_cert_handlers.go /
// server_web_common_wiki.go / server_web_common_dialog_handlers.go 的 JSON 接口部分）：
//   - /CertDownloadInfoInterface /CertDownloadInterface  HTTPS 证书信息与下载
//   - /WikiInterface                                     项目 Wiki 文档列表/内容
//   - /UserInfoLogInterface                              用户操作日志分页/搜索
// 前端弹窗（HTML/CSS/JS）由 ClientWeb SPA 实现。

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
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
// 用户操作日志
// ============================================================================

// logEntry 日志条目（包含原始行和解析后的时间戳）
type logEntry struct {
	line      string    // 原始日志行
	timestamp time.Time // 解析后的时间戳
	valid     bool      // 时间戳是否有效
}

// UserLogReader 大日志文件读取器（支持分页和流式读取）
type UserLogReader struct {
	filePath   string
	file       *os.File
	entries    []logEntry // 解析后的日志条目（按时间倒序排列）
	totalLines int        // 总行数
	fileSize   int64      // 文件大小
	mu         sync.RWMutex
	isClosed   bool
}

// parseLogTimestamp 从日志行解析时间戳
// 日志格式: [2006-01-02 15:04:05] actionType username details
func parseLogTimestamp(line string) (time.Time, bool) {
	if len(line) < 21 {
		return time.Time{}, false
	}
	if line[0] != '[' || line[20] != ']' {
		return time.Time{}, false
	}
	timeStr := line[1:20]
	t, err := time.ParseInLocation("2006-01-02 15:04:05", timeStr, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// NewUserLogReader 创建日志读取器
func NewUserLogReader(filePath string) (*UserLogReader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	lr := &UserLogReader{
		filePath: filePath,
		file:     file,
	}

	if err := lr.buildLogEntries(); err != nil {
		file.Close()
		return nil, err
	}

	return lr, nil
}

// Close 关闭日志读取器
func (lr *UserLogReader) Close() error {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	if lr.isClosed {
		return nil
	}
	lr.isClosed = true
	return lr.file.Close()
}

// TotalLines 获取总行数
func (lr *UserLogReader) TotalLines() int {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	return lr.totalLines
}

// buildLogEntries 读取所有日志行，解析时间戳并排序
func (lr *UserLogReader) buildLogEntries() error {
	// 重置文件指针到开头
	if _, err := lr.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	scanner := bufio.NewScanner(lr.file)
	// 增加缓冲区大小，处理超长行（最大支持 10MB 行）
	buf := make([]byte, 1024*1024)    // 1MB 缓冲区
	scanner.Buffer(buf, 1024*1024*10) // 最大支持 10MB 行

	var entries []logEntry
	var offset int64

	for scanner.Scan() {
		line := scanner.Text()
		lineLength := len(scanner.Bytes()) + 1 // +1 是换行符
		offset += int64(lineLength)

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		timestamp, valid := parseLogTimestamp(line)
		entries = append(entries, logEntry{
			line:      line,
			timestamp: timestamp,
			valid:     valid,
		})
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	// 按时间倒序排序（最新的在前）
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].valid && entries[j].valid {
			// 都有有效时间戳，按时间倒序
			return entries[i].timestamp.After(entries[j].timestamp)
		}
		if entries[i].valid {
			// i 有时间戳，j 没有，i 排前面
			return true
		}
		if entries[j].valid {
			// j 有时间戳，i 没有，j 排前面
			return false
		}
		// 都没有时间戳，保持相对顺序（倒序）
		return i > j
	})

	lr.entries = entries
	lr.totalLines = len(entries)
	lr.fileSize = offset

	// 重新设置文件指针到开头
	_, err := lr.file.Seek(0, io.SeekStart)
	return err
}

// ReadPage 读取指定页的日志（已按时间倒序排列，pageNum 从 1 开始）
func (lr *UserLogReader) ReadPage(pageNum, pageSize int) ([]string, int, bool, error) {
	lr.mu.RLock()
	defer lr.mu.RUnlock()

	if lr.isClosed {
		return nil, 0, false, nil
	}

	if pageSize <= 0 {
		pageSize = 100 // 默认每页 100 行
	}
	if pageNum <= 0 {
		pageNum = 1
	}

	startLine := (pageNum - 1) * pageSize
	if startLine >= lr.totalLines {
		return []string{}, lr.totalLines, false, nil
	}

	endLine := startLine + pageSize
	if endLine > lr.totalLines {
		endLine = lr.totalLines
	}

	// 从已排序的 entries 中提取行
	lines := make([]string, 0, endLine-startLine)
	for i := startLine; i < endLine; i++ {
		lines = append(lines, lr.entries[i].line)
	}

	return lines, lr.totalLines, endLine < lr.totalLines, nil
}

// SearchEntries 搜索匹配关键词的条目，返回匹配的索引列表
func (lr *UserLogReader) SearchEntries(keyword string) []int {
	lr.mu.RLock()
	defer lr.mu.RUnlock()

	if lr.isClosed || keyword == "" {
		return []int{}
	}

	keywordLower := strings.ToLower(keyword)
	var matchIndices []int

	for i, entry := range lr.entries {
		if strings.Contains(strings.ToLower(entry.line), keywordLower) {
			matchIndices = append(matchIndices, i)
		}
	}

	return matchIndices
}

// ReadSearchResultPage 读取搜索结果的指定页
func (lr *UserLogReader) ReadSearchResultPage(matchIndices []int, pageNum, pageSize int) ([]string, int, int, bool, error) {
	lr.mu.RLock()
	defer lr.mu.RUnlock()

	if lr.isClosed {
		return nil, 0, 0, false, nil
	}

	if pageSize <= 0 {
		pageSize = 100
	}
	if pageNum <= 0 {
		pageNum = 1
	}

	matchCount := len(matchIndices)
	if matchCount == 0 {
		return []string{}, 0, 0, false, nil
	}

	startIdx := (pageNum - 1) * pageSize
	if startIdx >= matchCount {
		return []string{}, matchCount, 0, false, nil
	}

	endIdx := startIdx + pageSize
	if endIdx > matchCount {
		endIdx = matchCount
	}

	totalPages := (matchCount + pageSize - 1) / pageSize
	hasMore := endIdx < matchCount

	lines := make([]string, 0, endIdx-startIdx)
	for i := startIdx; i < endIdx; i++ {
		entryIdx := matchIndices[i]
		lines = append(lines, lr.entries[entryIdx].line)
	}

	return lines, matchCount, totalPages, hasMore, nil
}

// userLogReaderPool 用户日志读取器连接池，避免重复构建行索引
var userLogReaderPool = struct {
	reader   *UserLogReader
	mu       sync.RWMutex
	logPath  string
	fileSize int64
	modTime  int64
}{}

// getOrCreateUserLogReader 获取或创建日志读取器（带缓存和文件变化检测）
func getOrCreateUserLogReader(logPath string) (*UserLogReader, error) {
	userLogReaderPool.mu.Lock()
	defer userLogReaderPool.mu.Unlock()

	// 获取文件信息以检测变化
	fileInfo, err := os.Stat(logPath)
	if err != nil {
		return nil, err
	}

	currentSize := fileInfo.Size()
	currentModTime := fileInfo.ModTime().Unix()

	// 检查缓存是否有效
	if userLogReaderPool.reader != nil &&
		userLogReaderPool.logPath == logPath &&
		userLogReaderPool.fileSize == currentSize &&
		userLogReaderPool.modTime == currentModTime {
		return userLogReaderPool.reader, nil
	}

	// 文件发生变化，关闭旧读取器
	if userLogReaderPool.reader != nil {
		userLogReaderPool.reader.Close()
	}

	// 创建新读取器
	reader, err := NewUserLogReader(logPath)
	if err != nil {
		return nil, err
	}

	userLogReaderPool.reader = reader
	userLogReaderPool.logPath = logPath
	userLogReaderPool.fileSize = currentSize
	userLogReaderPool.modTime = currentModTime

	return reader, nil
}

// userInfoLogInterfaceHandle 读取用户操作日志文件并返回 JSON（支持分页和搜索）
func userInfoLogInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	logPath := config.G.UserInfoLogURL

	// 解析分页和搜索参数
	var req struct {
		PageNum       int    `json:"page_num"`
		PageSize      int    `json:"page_size"`
		SearchKeyword string `json:"search_keyword"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if req.PageSize <= 0 || req.PageSize > 500 {
		req.PageSize = 100 // 默认每页 100 行，最大 500
	}
	if req.PageNum <= 0 {
		req.PageNum = 1
	}

	// 获取日志读取器
	reader, err := getOrCreateUserLogReader(logPath)
	if err != nil {
		// 文件不存在时返回空结果
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"lines":          []string{},
			"count":          0,
			"page_num":       req.PageNum,
			"page_size":      req.PageSize,
			"total_pages":    0,
			"has_more":       false,
			"is_search":      false,
			"search_keyword": "",
			"match_count":    0,
		})
		return
	}

	// 检查是否为搜索模式
	isSearch := req.SearchKeyword != ""

	if isSearch {
		// 搜索模式
		matchIndices := reader.SearchEntries(req.SearchKeyword)
		lines, matchCount, totalPages, hasMore, err := reader.ReadSearchResultPage(matchIndices, req.PageNum, req.PageSize)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"lines":          lines,
			"count":          reader.TotalLines(),
			"page_num":       req.PageNum,
			"page_size":      req.PageSize,
			"total_pages":    totalPages,
			"has_more":       hasMore,
			"is_search":      true,
			"search_keyword": req.SearchKeyword,
			"match_count":    matchCount,
		})
	} else {
		// 普通模式
		lines, totalLines, hasMore, err := reader.ReadPage(req.PageNum, req.PageSize)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}

		totalPages := 0
		if totalLines > 0 {
			totalPages = (totalLines + req.PageSize - 1) / req.PageSize
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"lines":          lines,
			"count":          totalLines,
			"page_num":       req.PageNum,
			"page_size":      req.PageSize,
			"total_pages":    totalPages,
			"has_more":       hasMore,
			"is_search":      false,
			"search_keyword": "",
			"match_count":    0,
		})
	}
}
