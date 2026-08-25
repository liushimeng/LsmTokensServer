package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
)

// v2.0.63: 证书安装指南窗口重构 — PublicHost / HTTP 接入地址 / 证书元信息 单测

// generateTestCert 用 ECDSA 自签一个测试证书，写到 t.TempDir()，返回绝对路径。
// notBefore 设为 now-1h，notAfter 设为 now+24h（有效证书）。
func generateTestCert(t *testing.T, cn string, expired bool) string {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "test-cert.crt")
	keyPath := filepath.Join(dir, "test-cert.key")

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := time.Now().Add(24 * time.Hour)
	if expired {
		notBefore = time.Now().Add(-48 * time.Hour)
		notAfter = time.Now().Add(-24 * time.Hour)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(0x123456789abcdef),
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"LsmTokensServer Test Org"},
		},
		Issuer: pkix.Name{
			CommonName:   cn,
			Organization: []string{"LsmTokensServer Test Org"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           nil,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		t.Fatalf("writecert: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(priv)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatalf("writekey: %v", err)
	}
	return certPath
}

func TestCertDownloadInfo_PublicHost(t *testing.T) {
	certPath := generateTestCert(t, "public-host-test", false)

	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = certPath
	testCfg.AgentPublicHost = "8.130.85.252"
	testCfg.AgentProductListenAddr = "0.0.0.0" // 监听地址仍应为 0.0.0.0，但接入主机应使用 PublicHost
	testCfg.AgentHttpsListenPort = 29003
	testCfg.AgentListenPort = 29000
	testCfg.AgentAnthropicListenURL = "Anthropic"
	testCfg.AgentOpenAIListenURL = "OpenAI"

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
		t.Fatalf("decode JSON: %v (body=%s)", err, rec.Body.String())
	}

	if resp.PublicHost != "8.130.85.252" {
		t.Fatalf("PublicHost = %q, want 8.130.85.252", resp.PublicHost)
	}
	if resp.AgentHost != "0.0.0.0" {
		t.Fatalf("AgentHost (listen) = %q, want 0.0.0.0", resp.AgentHost)
	}
	wantAnthropic := "https://8.130.85.252:29003/Anthropic"
	if resp.PublicAnthropicURL != wantAnthropic {
		t.Fatalf("PublicAnthropicURL = %q, want %q", resp.PublicAnthropicURL, wantAnthropic)
	}
	wantOpenAI := "https://8.130.85.252:29003/OpenAI"
	if resp.PublicOpenAIURL != wantOpenAI {
		t.Fatalf("PublicOpenAIURL = %q, want %q", resp.PublicOpenAIURL, wantOpenAI)
	}
}

func TestCertDownloadInfo_PublicHostFallback(t *testing.T) {
	// AgentPublicHost 为空时应回退到 AgentProductListenAddr
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/nonexistent/path"
	testCfg.AgentPublicHost = ""
	testCfg.AgentProductListenAddr = "10.20.30.40"

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PublicHost != "10.20.30.40" {
		t.Fatalf("PublicHost = %q, want fallback to AgentProductListenAddr=10.20.30.40", resp.PublicHost)
	}
}

func TestCertDownloadInfo_PublicHostDoubleFallback(t *testing.T) {
	// AgentPublicHost 与 AgentProductListenAddr 都为空 → 127.0.0.1
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/nonexistent/path"
	testCfg.AgentPublicHost = ""
	testCfg.AgentProductListenAddr = ""

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PublicHost != "127.0.0.1" {
		t.Fatalf("PublicHost = %q, want 127.0.0.1 double fallback", resp.PublicHost)
	}
}

func TestCertDownloadInfo_HTTPPaths(t *testing.T) {
	// 验证 HTTP 接入地址（HTTPS 端口 29003，HTTP 端口 29000）
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/nonexistent/path"
	testCfg.AgentPublicHost = "example.com"
	testCfg.AgentHttpsListenPort = 29003
	testCfg.AgentListenPort = 29000
	testCfg.AgentAnthropicListenURL = "Anthropic"
	testCfg.AgentOpenAIListenURL = "OpenAI"

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	wantHTTPAnthropic := "http://example.com:29000/Anthropic"
	if resp.HttpAnthropicURL != wantHTTPAnthropic {
		t.Fatalf("HttpAnthropicURL = %q, want %q", resp.HttpAnthropicURL, wantHTTPAnthropic)
	}
	wantHTTPOpenAI := "http://example.com:29000/OpenAI"
	if resp.HttpOpenAIURL != wantHTTPOpenAI {
		t.Fatalf("HttpOpenAIURL = %q, want %q", resp.HttpOpenAIURL, wantHTTPOpenAI)
	}
	wantHTTPSAnthropic := "https://example.com:29003/Anthropic"
	if resp.PublicAnthropicURL != wantHTTPSAnthropic {
		t.Fatalf("PublicAnthropicURL = %q, want %q", resp.PublicAnthropicURL, wantHTTPSAnthropic)
	}
}

func TestCertDownloadInfo_HTTPDisabled(t *testing.T) {
	// HTTP 端口为 0 时，HTTP 接入地址应为空
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/nonexistent/path"
	testCfg.AgentPublicHost = "example.com"
	testCfg.AgentListenPort = 0
	testCfg.AgentHttpsListenPort = 29003

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.HttpAnthropicURL != "" {
		t.Fatalf("HttpAnthropicURL = %q, want empty when AgentListenPort=0", resp.HttpAnthropicURL)
	}
	if resp.HttpOpenAIURL != "" {
		t.Fatalf("HttpOpenAIURL = %q, want empty when AgentListenPort=0", resp.HttpOpenAIURL)
	}
	if resp.PublicAnthropicURL == "" {
		t.Fatalf("PublicAnthropicURL should be set when HTTPS port=29003")
	}
}

func TestCertDownloadInfo_HTTPSDisabled(t *testing.T) {
	// HTTPS 端口为 0 时，HTTPS 接入地址应为空，HTTP 不受影响
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = "/nonexistent/path"
	testCfg.AgentPublicHost = "example.com"
	testCfg.AgentListenPort = 29000
	testCfg.AgentHttpsListenPort = 0

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PublicAnthropicURL != "" {
		t.Fatalf("PublicAnthropicURL = %q, want empty when HTTPS port=0", resp.PublicAnthropicURL)
	}
	if resp.PublicOpenAIURL != "" {
		t.Fatalf("PublicOpenAIURL = %q, want empty when HTTPS port=0", resp.PublicOpenAIURL)
	}
	if resp.HttpAnthropicURL == "" {
		t.Fatalf("HttpAnthropicURL should be set when HTTP port=29000")
	}
}

func TestCertDownloadInfo_CertMetaValid(t *testing.T) {
	certPath := generateTestCert(t, "lsm-test-server", false)
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = certPath

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.CertExists {
		t.Fatalf("CertExists = false, want true")
	}
	if !strings.Contains(resp.CertSubject, "lsm-test-server") {
		t.Fatalf("CertSubject = %q, want contains 'lsm-test-server'", resp.CertSubject)
	}
	if resp.CertIssuer == "" {
		t.Fatalf("CertIssuer should not be empty")
	}
	if resp.CertNotBefore == "" {
		t.Fatalf("CertNotBefore should not be empty")
	}
	if resp.CertNotAfter == "" {
		t.Fatalf("CertNotAfter should not be empty")
	}
	if resp.CertSHA256 == "" {
		t.Fatalf("CertSHA256 should not be empty")
	}
	// SHA-256 应该是 32 字节（64 hex char），按 2 字符一组加冒号 → 95 字符
	if len(resp.CertSHA256) != 95 {
		t.Fatalf("CertSHA256 len = %d, want 95 (32 bytes SHA-256 in colon-separated hex)", len(resp.CertSHA256))
	}
	// 验证冒号分隔格式
	parts := strings.Split(resp.CertSHA256, ":")
	if len(parts) != 32 {
		t.Fatalf("SHA256 groups = %d, want 32", len(parts))
	}
	if resp.CertSerial == "" {
		t.Fatalf("CertSerial should not be empty")
	}
	if resp.CertExpired {
		t.Fatalf("CertExpired should be false for valid cert")
	}
}

func TestCertDownloadInfo_CertExpired(t *testing.T) {
	certPath := generateTestCert(t, "expired-cert", true)
	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = certPath

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.CertExpired {
		t.Fatalf("CertExpired = false, want true for expired cert")
	}
}

func TestCertDownloadInfo_CertMetaInvalid(t *testing.T) {
	// 证书文件存在但不是合法 PEM 时，元信息字段应为空字符串，cert_expired=false
	tmp := t.TempDir()
	badCertPath := filepath.Join(tmp, "bad.crt")
	if err := os.WriteFile(badCertPath, []byte("not a valid PEM file"), 0644); err != nil {
		t.Fatalf("write bad cert: %v", err)
	}

	testCfg := config.DefaultConfig()
	testCfg.UserWebCertFile = badCertPath

	restore := withTestCfg(t, testCfg)
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/CertDownloadInfoInterface", nil)
	rec := httptest.NewRecorder()
	certDownloadInfoInterfaceHandle(rec, req)

	var resp CertDownloadInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.CertExists {
		t.Fatalf("CertExists should be true (file exists)")
	}
	if resp.CertSubject != "" {
		t.Fatalf("CertSubject should be empty for invalid PEM, got %q", resp.CertSubject)
	}
	if resp.CertSHA256 != "" {
		t.Fatalf("CertSHA256 should be empty for invalid PEM, got %q", resp.CertSHA256)
	}
	if resp.CertExpired {
		t.Fatalf("CertExpired should be false (parse failed)")
	}
}

func TestResolveAccessHost(t *testing.T) {
	tests := []struct {
		name        string
		publicHost  string
		listenAddr  string
		want        string
	}{
		{"public_host_overrides", "1.2.3.4", "0.0.0.0", "1.2.3.4"},
		{"public_host_trims_whitespace", "  1.2.3.4  ", "0.0.0.0", "1.2.3.4"},
		{"fallback_to_listen", "", "10.0.0.1", "10.0.0.1"},
		{"double_fallback", "", "", "127.0.0.1"},
		{"whitespace_only_fallback_to_listen", "   ", "10.0.0.1", "10.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.AgentPublicHost = tt.publicHost
			cfg.AgentProductListenAddr = tt.listenAddr
			restore := withTestCfg(t, cfg)
			defer restore()
			got := resolveAccessHost()
			if got != tt.want {
				t.Fatalf("resolveAccessHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAccessURL(t *testing.T) {
	tests := []struct {
		scheme string
		host   string
		port   int
		path   string
		want   string
	}{
		{"https", "1.2.3.4", 29003, "Anthropic", "https://1.2.3.4:29003/Anthropic"},
		{"http", "example.com", 29000, "OpenAI", "http://example.com:29000/OpenAI"},
		{"https", "1.2.3.4", 443, "Anthropic", "https://1.2.3.4/Anthropic"},
		{"http", "1.2.3.4", 80, "Anthropic", "http://1.2.3.4/Anthropic"},
		{"https", "1.2.3.4", 0, "Anthropic", ""},
		{"https", "", 29003, "Anthropic", ""},
		{"https", "1.2.3.4", 29003, "/Anthropic/", "https://1.2.3.4:29003/Anthropic/"},
		{"https", "1.2.3.4", 29003, "", "https://1.2.3.4:29003"},
	}
	for _, tt := range tests {
		t.Run(tt.scheme+"_"+tt.host, func(t *testing.T) {
			got := buildAccessURL(tt.scheme, tt.host, tt.port, tt.path)
			if got != tt.want {
				t.Fatalf("buildAccessURL(%q, %q, %d, %q) = %q, want %q",
					tt.scheme, tt.host, tt.port, tt.path, got, tt.want)
			}
		})
	}
}
