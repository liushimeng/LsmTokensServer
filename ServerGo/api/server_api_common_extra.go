package api

// 工具类公共接口（迁移自旧工程 server_web_common_dialog_cert_handlers.go /
// server_web_common_wiki.go / server_web_common_dialog_handlers.go 的 JSON 接口部分）：
//   - /CertDownloadInfoInterface /CertDownloadInterface  HTTPS 证书信息与下载
//   - /WikiInterface                                     项目 Wiki 文档列表/内容
//   - /UserInfoLogInterface                              用户操作日志分页/搜索（数据库版）
// 前端弹窗（HTML/CSS/JS）由 ClientWeb SPA 实现。

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/system"
)

// ============================================================================
// HTTPS 证书下载
// ============================================================================

// CertDownloadInfoResponse 公钥下载信息 JSON 响应
// v2.0.63：补 PublicHost / HttpPort / 完整 URL 字段（避免前端字符串拼装错漏），
// 以及证书元信息（Subject/Issuer/有效期/SHA-256/序列号）。
type CertDownloadInfoResponse struct {
	// 旧字段（保持兼容）
	AgentHost      string `json:"agent_host"`
	HttpsPort      int    `json:"https_port"`
	AnthropicPath  string `json:"anthropic_path"`
	OpenAIPath     string `json:"openai_path"`
	CertFile       string `json:"cert_file"`
	CertExists     bool   `json:"cert_exists"`
	CertSize       int64  `json:"cert_size"`
	HTTPSEnabled   bool   `json:"https_enabled"`
	UserWebEnabled bool   `json:"user_web_https_enabled"`

	// v2.0.63 新增
	PublicHost        string `json:"public_host"`          // 客户端接入主机（agentPublicHost 优先，旧字段兜底）
	HttpPort          int    `json:"http_port"`            // 29000
	PublicAnthropicURL string `json:"public_anthropic_url"` // https://{public_host}:{https_port}/Anthropic
	PublicOpenAIURL    string `json:"public_openai_url"`    // https://{public_host}:{https_port}/OpenAI
	HttpAnthropicURL   string `json:"http_anthropic_url"`   // http://{public_host}:{http_port}/Anthropic
	HttpOpenAIURL      string `json:"http_openai_url"`      // http://{public_host}:{http_port}/OpenAI
	CertSubject        string `json:"cert_subject"`         // Subject（CN=...,O=...）
	CertIssuer         string `json:"cert_issuer"`          // Issuer
	CertNotBefore      string `json:"cert_not_before"`      // RFC3339 UTC
	CertNotAfter       string `json:"cert_not_after"`       // RFC3339 UTC
	CertSHA256         string `json:"cert_sha256"`          // 冒号分隔的大写十六进制（与 openssl x509 -fingerprint -sha256 一致）
	CertSerial         string `json:"cert_serial"`          // 大写十六进制
	CertExpired        bool   `json:"cert_expired"`         // true = 已过期或未生效
}

// resolveAccessHost 解析客户端接入主机（v2.0.63）：
// 优先使用 agentPublicHost，否则回退 agentProductListenAddr，仍空则 127.0.0.1。
func resolveAccessHost() string {
	if h := strings.TrimSpace(config.G.AgentPublicHost); h != "" {
		return h
	}
	if h := strings.TrimSpace(config.G.AgentProductListenAddr); h != "" {
		return h
	}
	return "127.0.0.1"
}

// buildAccessURL 拼装接入地址（端口为 0 时返回空字符串）
// defaultPort == 80/443 时省略端口号
func buildAccessURL(scheme, host string, port int, pathSeg string) string {
	if host == "" || port <= 0 {
		return ""
	}
	pathSeg = strings.TrimLeft(pathSeg, "/")
	portPart := ""
	if (scheme == "http" && port != 80) || (scheme == "https" && port != 443) {
		portPart = ":" + strconv.Itoa(port)
	}
	if pathSeg == "" {
		return scheme + "://" + host + portPart
	}
	return scheme + "://" + host + portPart + "/" + pathSeg
}

// parseCertMeta 解析证书 PEM 文件，返回 Subject/Issuer/有效期/SHA-256/Serial/是否过期。
// 解析失败时所有字符串字段返回空，certExpired 返回 false（前端展示 -）。
func parseCertMeta(certPath string) (subject, issuer, notBefore, notAfter, sha256fp, serial string, expired bool) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	subject = cert.Subject.String()
	issuer = cert.Issuer.String()
	notBefore = cert.NotBefore.UTC().Format(time.RFC3339)
	notAfter = cert.NotAfter.UTC().Format(time.RFC3339)

	// SHA-256 指纹（按 OpenSSL 风格冒号分隔的大写十六进制）
	sum := sha256.Sum256(cert.Raw)
	hexSum := strings.ToUpper(hex.EncodeToString(sum[:]))
	var groups []string
	for i := 0; i < len(hexSum); i += 2 {
		end := i + 2
		if end > len(hexSum) {
			end = len(hexSum)
		}
		groups = append(groups, hexSum[i:end])
	}
	sha256fp = strings.Join(groups, ":")

	// 序列号（去除前导 0 的大写十六进制，与 openssl x509 -serial -noout 一致）
	if cert.SerialNumber != nil {
		serialBytes := cert.SerialNumber.Bytes()
		if len(serialBytes) > 0 {
			serial = strings.ToUpper(hex.EncodeToString(serialBytes))
		}
	}

	now := time.Now()
	expired = now.After(cert.NotAfter) || now.Before(cert.NotBefore)
	return
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
	var certSubject, certIssuer, certNotBefore, certNotAfter, certSHA256, certSerial string
	var certExpired bool
	if certPath != "" {
		if info, err := os.Stat(certPath); err == nil && !info.IsDir() {
			certExists = true
			certSize = info.Size()
			// 解析证书元信息（解析失败不影响主流程，字段留空即可）
			certSubject, certIssuer, certNotBefore, certNotAfter, certSHA256, certSerial, certExpired = parseCertMeta(certPath)
		}
	}

	listenHost := strings.TrimSpace(config.G.AgentProductListenAddr)
	if listenHost == "" {
		listenHost = "127.0.0.1"
	}
	publicHost := resolveAccessHost()

	anthropicPath := config.G.AgentAnthropicListenURL
	if anthropicPath == "" {
		anthropicPath = "Anthropic"
	}
	openaiPath := config.G.AgentOpenAIListenURL
	if openaiPath == "" {
		openaiPath = "OpenAI"
	}

	resp := CertDownloadInfoResponse{
		// 旧字段
		AgentHost:      listenHost,
		HttpsPort:      config.G.AgentHttpsListenPort,
		AnthropicPath:  anthropicPath,
		OpenAIPath:     openaiPath,
		CertFile:       certOriginal,
		CertExists:     certExists,
		CertSize:       certSize,
		HTTPSEnabled:   config.G.AgentHttpsListenPort > 0,
		UserWebEnabled: config.G.UserWebUseHTTPS,
		// 新字段
		PublicHost:         publicHost,
		HttpPort:           config.G.AgentListenPort,
		PublicAnthropicURL: buildAccessURL("https", publicHost, config.G.AgentHttpsListenPort, anthropicPath),
		PublicOpenAIURL:    buildAccessURL("https", publicHost, config.G.AgentHttpsListenPort, openaiPath),
		HttpAnthropicURL:   buildAccessURL("http", publicHost, config.G.AgentListenPort, anthropicPath),
		HttpOpenAIURL:      buildAccessURL("http", publicHost, config.G.AgentListenPort, openaiPath),
		CertSubject:        certSubject,
		CertIssuer:         certIssuer,
		CertNotBefore:      certNotBefore,
		CertNotAfter:       certNotAfter,
		CertSHA256:         certSHA256,
		CertSerial:         certSerial,
		CertExpired:        certExpired,
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
// Wiki 文档接口（阶段AG：树形目录 + 惰性分块读取）
// ============================================================================

// WikiNode 树节点（文件 / 目录 / 其它）
// 目录节点的 Children 非空；非 .md 文件（type=other）仍出现在目录里用于导航可见性，
// 但 content 接口会拒绝对其调用。
type WikiNode struct {
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	Type         string     `json:"type"` // "dir" / "file" / "other"
	Size         int64      `json:"size"`
	ModifiedTime string     `json:"modified_time,omitempty"`
	ChildCount   int        `json:"child_count"`
	Children     []WikiNode `json:"children,omitempty"`
}

// wikiExcludedDirNames 阶段AG：项目根下需要整树跳过的目录
// （保留与 getWikiFiles 阶段AA 行为一致）
var wikiExcludedDirNames = map[string]bool{
	"go-web-debug-tool":          true,
	"python-generate-image-tool": true,
}

// wikiShouldSkipDir 判定目录是否应跳过遍历
func wikiShouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return wikiExcludedDirNames[name]
}

// buildWikiTreeNode 递归构建子树
// 仅收集 .md 文件（type=file）；非 .md 文件以 type=other 出现但不展开内容接口。
// 目录按字典序排序（目录在前，文件在后），便于前端稳定展示。
func buildWikiTreeNode(projectDir, absDir, relDir string) (WikiNode, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return WikiNode{}, err
	}

	var dirs []os.DirEntry
	var files []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name()) < strings.ToLower(dirs[j].Name()) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name()) < strings.ToLower(files[j].Name()) })

	node := WikiNode{
		Type: "dir",
		Name: filepath.Base(absDir),
		Path: relDir,
	}

	var maxMod time.Time
	for _, d := range dirs {
		if wikiShouldSkipDir(d.Name()) {
			continue
		}
		childAbs := filepath.Join(absDir, d.Name())
		childRel := d.Name()
		if relDir != "" {
			childRel = relDir + "/" + d.Name()
		}
		child, err := buildWikiTreeNode(projectDir, childAbs, childRel)
		if err != nil {
			continue
		}
		node.Children = append(node.Children, child)
		// 目录时间取子树内最大修改时间
		if t, err := getWikiLatestModTime(childAbs); err == nil {
			if maxMod.IsZero() || t.After(maxMod) {
				maxMod = t
			}
		}
	}

	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			continue
		}
		rel := f.Name()
		if relDir != "" {
			rel = relDir + "/" + f.Name()
		}
		entry := WikiNode{
			Path: rel,
			Name: f.Name(),
			Size: info.Size(),
		}
		// 仅 .md 视作可读取的"file"，其它以"other"出现但不可点
		if strings.HasSuffix(strings.ToLower(f.Name()), ".md") {
			entry.Type = "file"
		} else {
			entry.Type = "other"
		}
		entry.ModifiedTime = info.ModTime().UTC().Format(time.RFC3339)
		if maxMod.IsZero() || info.ModTime().After(maxMod) {
			maxMod = info.ModTime()
		}
		node.Children = append(node.Children, entry)
	}

	// 目录自身的 ModifiedTime 取子树最大 mtime；根目录的 Name 使用项目根 basename
	if !maxMod.IsZero() {
		node.ModifiedTime = maxMod.UTC().Format(time.RFC3339)
	}
	node.ChildCount = len(node.Children)
	return node, nil
}

// getWikiLatestModTime 取得目录内（含子目录）最大 mtime；不可访问时返回零值
func getWikiLatestModTime(dir string) (time.Time, error) {
	var latest time.Time
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// 跳过被排除目录
		if info.IsDir() {
			if path != dir && wikiShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
		}
		if latest.IsZero() || info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest, err
}

// getWikiTreeWithDir 构建指定目录的 Wiki 树（可注入 projectDir 用于单测）
func getWikiTreeWithDir(projectDir string) (WikiNode, int, int, error) {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return WikiNode{}, 0, 0, err
	}
	rootName := filepath.Base(absProject)
	tree, err := buildWikiTreeNode(absProject, absProject, "")
	if err != nil {
		return WikiNode{}, 0, 0, err
	}
	tree.Name = rootName
	tree.Path = ""
	files, dirs := countWikiTree(tree)
	return tree, files, dirs, nil
}

// getWikiProjectRoot 返回 Wiki 扫描的根目录。
// 二进制位于 ServerGo/，而 docs/ 位于其父目录（工程根），因此 Wiki 以工程根为扫描起点，
// 确保 docs/ 子树可见；若父目录不可访问则回退到 system.GetProjectDir()。
func getWikiProjectRoot() string {
	binDir := system.GetProjectDir()
	parent := filepath.Dir(binDir)
	if parent == "" || parent == binDir {
		return binDir
	}
	// 父目录需存在且包含 docs/ 子目录（避免误用不相关的上级目录）
	if info, err := os.Stat(filepath.Join(parent, "docs")); err == nil && info.IsDir() {
		return parent
	}
	return binDir
}

// getWikiTree 构建项目根目录的 Wiki 树
func getWikiTree() (WikiNode, int, int, error) {
	return getWikiTreeWithDir(getWikiProjectRoot())
}

func countWikiTree(n WikiNode) (files, dirs int) {
	for _, c := range n.Children {
		switch c.Type {
		case "file":
			files++
		case "dir":
			dirs++
			f, d := countWikiTree(c)
			files += f
			dirs += d
		}
	}
	return
}

// wikiContentDefaultLimit 单次默认返回行数；前端按此值追加加载
const wikiContentDefaultLimit = 400

// wikiContentMaxLimit 单次最大行数（防 DoS，约 64KB UTF-8 文本）
const wikiContentMaxLimit = 4000

// wikiInterfaceHandle Wiki接口（阶段AG 重构）
//   - 无 action / action=list: 返回项目根目录的 Markdown 文件树（含目录/文件元信息）
//   - action=get_content: 读取单个 .md 文件内容，支持 offset/limit 按行分块
func wikiInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	wikiInterfaceHandleWithDir(w, r, getWikiProjectRoot())
}

// wikiInterfaceHandleWithDir 允许注入 projectDir，便于单测
func wikiInterfaceHandleWithDir(w http.ResponseWriter, r *http.Request, projectDir string) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	var req struct {
		Action   string `json:"action"`
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// 分支 1：读取文件内容
	if req.Action == "get_content" && req.FilePath != "" {
		absPath, err := safeProjectFilePath(projectDir, req.FilePath, ".md")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "读取文件失败: " + err.Error()})
			return
		}

		// 文本按 \n 切行；容忍 \r\n（Windows 文本）
		normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
		allLines := strings.Split(normalized, "\n")
		totalLines := len(allLines)

		// offset/limit 边界
		offset := req.Offset
		if offset < 0 {
			offset = 0
		}
		if offset > totalLines {
			offset = totalLines
		}
		limit := req.Limit
		switch {
		case limit <= 0:
			limit = wikiContentDefaultLimit
		case limit > wikiContentMaxLimit:
			limit = wikiContentMaxLimit
		}
		end := offset + limit
		if end > totalLines {
			end = totalLines
		}

		chunk := strings.Join(allLines[offset:end], "\n")
		hasMore := end < totalLines

		info, _ := os.Stat(absPath)
		var modTime string
		if info != nil {
			modTime = info.ModTime().UTC().Format(time.RFC3339)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"path":          req.FilePath,
			"name":          filepath.Base(req.FilePath),
			"size":          len(data),
			"total_lines":   totalLines,
			"offset":        offset,
			"limit":         end - offset,
			"has_more":      hasMore,
			"modified_time": modTime,
			"content":       chunk,
		})
		return
	}

	// 分支 2：返回树
	tree, totalFiles, totalDirs, err := getWikiTreeWithDir(projectDir)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"tree":        tree,
		"total_files": totalFiles,
		"total_dirs":  totalDirs,
		"scanned_at":  time.Now().UTC().Format(time.RFC3339),
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
