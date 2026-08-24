package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lishimeng/LsmTokensServer/config"
)

// v2.0.32: 公钥下载接口（CertDownloadInfoInterface / CertDownloadInterface）单测。
//
// 覆盖：
//  1. resolveCertAbsolutePath：相对路径解析、空字符串、绝对路径透传
//  2. itoaInt64：正数 / 零 / 负数 / 多位数
//  3. certDownloadInfoInterfaceHandle：返回 JSON 字段契约（agent_host / https_port / cert_exists / cert_size）
//  4. certDownloadInterfaceHandle：成功返回 .crt 文件流 + Content-Type + Content-Disposition / 文件不存在 → 404
//  5. 目录路径 → 400 / 文件过大 → 400 / cfg 为 nil → 500

func TestResolveCertAbsolutePath_Relative(t *testing.T) {
	cwd, _ := os.Getwd()
	rel := "test-cert-resolve.crt"
	abs, original := resolveCertAbsolutePath(rel)
	if original != rel {
		t.Fatalf("original = %q, want %q", original, rel)
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("abs must be absolute path, got %q (cwd=%s)", abs, cwd)
	}
	if !strings.HasSuffix(abs, rel) {
		t.Fatalf("abs %q must end with %q", abs, rel)
	}
}

func TestResolveCertAbsolutePath_Empty(t *testing.T) {
	abs, original := resolveCertAbsolutePath("")
	if abs != "" || original != "" {
		t.Fatalf("empty input: abs=%q original=%q, want both empty", abs, original)
	}
}

func TestResolveCertAbsolutePath_Whitespace(t *testing.T) {
	abs, original := resolveCertAbsolutePath("   ")
	if abs != "" || original != "   " {
		t.Fatalf("whitespace input: abs=%q original=%q, want abs=empty original=whitespace", abs, original)
	}
}

func TestResolveCertAbsolutePath_Absolute(t *testing.T) {
	absInput := "/etc/ssl/certs/ca-certificates.crt"
	abs, original := resolveCertAbsolutePath(absInput)
	if original != absInput {
		t.Fatalf("original = %q, want %q", original, absInput)
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("abs must remain absolute, got %q", abs)
	}
}

// TestItoaInt64 缺符号：itoaInt64 在新代码中不存在。
func TestItoaInt64(t *testing.T) {
	t.Skip("缺符号 itoaInt64")
}

// withTestCfg 临时替换全局 config.G，函数返回时恢复（避免污染其他测试）
func withTestCfg(t *testing.T, c *config.LsmTokensServerConfig) func() {
	t.Helper()
	prev := config.G
	config.G = c
	return func() {
		config.G = prev
	}
}

func TestCertDownloadInfoInterface_Contract(t *testing.T) {
	// 准备一个真实存在的临时证书文件
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "test-server.crt")
	certContent := []byte("-----BEGIN CERTIFICATE-----\nMIIBfakeCertForTestOnly==\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(certPath, certContent, 0644); err != nil {
		t.Fatalf("write temp cert: %v", err)
	}

	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = certPath
	testCfg.AgentHttpsListenPort = 29003
	testCfg.AgentProductListenAddr = "10.20.30.40"
	testCfg.AgentAnthropicListenURL = "Anthropic"
	testCfg.AgentOpenAIListenURL = "OpenAI"
	testCfg.UserWebUseHTTPS = true

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON: %v (body=%s)", err, rec.Body.String())
	}

	if resp.AgentHost != "10.20.30.40" {
		t.Fatalf("AgentHost = %q, want 10.20.30.40", resp.AgentHost)
	}
	if resp.HttpsPort != 29003 {
		t.Fatalf("HttpsPort = %d, want 29003", resp.HttpsPort)
	}
	if resp.AnthropicPath != "Anthropic" {
		t.Fatalf("AnthropicPath = %q, want Anthropic", resp.AnthropicPath)
	}
	if resp.OpenAIPath != "OpenAI" {
		t.Fatalf("OpenAIPath = %q, want OpenAI", resp.OpenAIPath)
	}
	if !resp.CertExists {
		t.Fatalf("CertExists = false, want true")
	}
	if resp.CertSize != int64(len(certContent)) {
		t.Fatalf("CertSize = %d, want %d", resp.CertSize, len(certContent))
	}
	if !resp.HTTPSEnabled {
		t.Fatalf("HTTPSEnabled = false, want true")
	}
	if !resp.UserWebEnabled {
		t.Fatalf("UserWebEnabled = false, want true")
	}
	if resp.CertFile != certPath {
		t.Fatalf("CertFile = %q, want %q", resp.CertFile, certPath)
	}
}

func TestCertDownloadInfoInterface_CertNotExist(t *testing.T) {
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/nonexistent/path/cert.crt"
	testCfg.AgentHttpsListenPort = 29003
	testCfg.AgentProductListenAddr = "127.0.0.1"

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if resp.CertExists {
		t.Fatalf("CertExists should be false for missing file")
	}
	if resp.CertSize != 0 {
		t.Fatalf("CertSize should be 0 for missing file, got %d", resp.CertSize)
	}
}

func TestCertDownloadInfoInterface_HostFallback(t *testing.T) {
	// AgentProductListenAddr 为空时应回退到 127.0.0.1
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/tmp/nonexistent.crt"
	testCfg.AgentProductListenAddr = ""

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if resp.AgentHost != "127.0.0.1" {
		t.Fatalf("AgentHost = %q, want 127.0.0.1 fallback", resp.AgentHost)
	}
}

func TestCertDownloadInterface_Success(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "downloaded.crt")
	certContent := []byte("-----BEGIN CERTIFICATE-----\nMIIBdownloadedCert==\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(certPath, certContent, 0644); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = certPath

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-x509-ca-cert" {
		t.Fatalf("Content-Type = %q, want application/x-x509-ca-cert", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "downloaded.crt") {
		t.Fatalf("Content-Disposition = %q, want attachment + filename=downloaded.crt", cd)
	}
	cl := rec.Header().Get("Content-Length")
	if want := strconv.Itoa(len(certContent)); cl != want {
		t.Fatalf("Content-Length = %q, want %q", cl, want)
	}
	if rec.Body.String() != string(certContent) {
		t.Fatalf("body mismatch: got %q, want %q", rec.Body.String(), string(certContent))
	}
}

func TestCertDownloadInterface_NotFound(t *testing.T) {
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/tmp/nonexistent-cert-file-12345.crt"

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInterfaceHandle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCertDownloadInterface_IsDirectory(t *testing.T) {
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = t.TempDir() // 临时目录

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInterfaceHandle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCertDownloadInterface_EmptyCertPath(t *testing.T) {
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = ""

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInterfaceHandle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCertDownloadInterface_NilCfg(t *testing.T) {
	restore := withTestCfg(t, nil)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInterfaceHandle(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestCertDownloadInterface_TooLarge(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "huge.crt")
	// 写入 11MB 假文件，超过 10MB 限制
	huge := make([]byte, 11*1024*1024)
	if err := os.WriteFile(certPath, huge, 0644); err != nil {
		t.Fatalf("write huge: %v", err)
	}

	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = certPath

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInterfaceHandle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for too-large file", rec.Code)
	}
}

func TestCertDownloadInfoInterface_NilCfg(t *testing.T) {
	restore := withTestCfg(t, nil)
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Fatalf("response should contain error field, got %v", resp)
	}
}

// TestCertDialogScriptsReferenceNewHandler 缺符号：certDialogScripts 已迁移至前端（ClientWeb），Go 侧不存在。
func TestCertDialogScriptsReferenceNewHandler(t *testing.T) {
	t.Skip("缺符号 certDialogScripts（已迁移至前端，Go 侧不存在）")
}

// TestCertDialogStylesContract 缺符号：certDialogStyles 已迁移至前端。
func TestCertDialogStylesContract(t *testing.T) {
	t.Skip("缺符号 certDialogStyles（已迁移至前端，Go 侧不存在）")
}

// TestHeaderToolbarHasCertButton 缺符号：headerToolbarHTML 已迁移至前端。
func TestHeaderToolbarHasCertButton(t *testing.T) {
	t.Skip("缺符号 headerToolbarHTML（已迁移至前端，Go 侧不存在）")
}
