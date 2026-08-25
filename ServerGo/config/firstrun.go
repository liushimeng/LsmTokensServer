package config

// v2.0.74 首次运行配置自动回写 + 超级管理员随机生成与禁用机制（阶段AL）
//
// 设计要点：
//   - 首次启动（无 conf / conf 解析失败）时自动生成含默认值的 LsmTokensServer.conf，
//     并随机生成 JWT 密钥、管理员账号密码；
//   - 启动后若检测到数据库已有业务用户，自动把管理员账号设为不可用（字符串"disable"），
//     并把 managerWebAuthDisabled 置为 true，由中间件/登录 handler 拒绝所有管理端业务接口。
//
// 本文件**只**做配置层面的回写与随机生成；是否需要禁用、由 main 在数据库就绪后决定。

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DisableSentinel 禁用哨兵：managerUserName / managerPassword 任一字段等于该字符串（忽略大小写），
// 即视为"管理端已被禁用"，由 api 包登录 handler 与中间件负责拒绝登录。
//
// 选用字符串而非空值，理由：
//   1) 配置文件内显式标注，方便运维同事一眼看出"是被自动禁用"；
//   2) 与现有"未配置"分支（空字符串）区分，便于日志告警文案差异化。
const DisableSentinel = "disable"

// RandomString 生成指定字节数的随机字符串，字符集 base62（[0-9A-Za-z]）。
// 使用 crypto/rand，失败时返回 error（**不**降级为时间戳等可预测熵源）。
func RandomString(length int) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	if length <= 0 {
		return "", fmt.Errorf("length must be positive")
	}
	// 拒绝采样避免 modulo 偏置（alphabet 长度 62 = 2*31，非 2 的幂）
	const max = byte(62)
	const rejectAbove = byte(256 - (256 % int(max))) // 248
	buf := make([]byte, length)
	written := 0
	for written < length {
		tmp := make([]byte, length-written)
		if _, err := rand.Read(tmp); err != nil {
			return "", fmt.Errorf("crypto/rand 失败: %w", err)
		}
		for _, b := range tmp {
			if b < rejectAbove && written < length {
				buf[written] = alphabet[b%max]
				written++
			}
		}
	}
	return string(buf), nil
}

// RandomUsername 生成管理员用户名，固定前缀 "adm-" + 8 位无歧义字符（去掉 0/O/1/I/l）。
func RandomUsername() (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijmnpqrstuvwxyz"
	const need = 8
	const max = byte(len(alphabet))               // 56
	const rejectAbove = byte(256 - (256 % int(max))) // 224
	buf := make([]byte, need)
	written := 0
	for written < need {
		tmp := make([]byte, need-written)
		if _, err := rand.Read(tmp); err != nil {
			return "", fmt.Errorf("crypto/rand 失败: %w", err)
		}
		for _, b := range tmp {
			if b < rejectAbove && written < need {
				buf[written] = alphabet[b%max]
				written++
			}
		}
	}
	return "adm-" + string(buf), nil
}

// RandomJWTSecret 生成 32 字节随机密钥，base64 编码后返回（与现有 getJWTSecret 兼容）。
func RandomJWTSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand 失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// IsManagerDisabled 判断当前 SecurityConfig 是否处于禁用态。
// 规则：managerUserName / managerPassword 任一字段等于 DisableSentinel（忽略大小写）。
func (s *SecurityConfig) IsManagerDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(s.ManagerUserName), DisableSentinel) ||
		strings.EqualFold(strings.TrimSpace(s.ManagerPassword), DisableSentinel)
}

// EnsureDefaultConfig 确保指定路径存在合法配置：
//   - 文件不存在或读取失败 → 视为首次启动，生成含随机默认值的配置并落盘，返回 (cfg, true, nil)；
//   - 文件存在 → 仅做 validateAndFixConfig 修复，不覆盖，返回 (cfg, false, nil)。
//
// 注意：本函数不会因为"配置文件存在但密钥为空"而重置密钥——避免覆盖运维显式清空的操作。
func EnsureDefaultConfig(path string) (*LsmTokensServerConfig, bool, error) {
	cfg := getDefaultConfig()

	if _, err := os.Stat(path); err == nil {
		// 文件存在 → 走正常加载路径；不视为首次启动。
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("读取配置失败: %w", err)
		}
		// 复用 LoadConfig 内的字段映射逻辑。
		loaded, err := loadFromBytes(data)
		if err != nil {
			return nil, false, fmt.Errorf("解析配置失败: %w", err)
		}
		return loaded, false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("stat 配置失败: %w", err)
	}

	// 文件不存在 → 首次启动：填随机值。
	username, err := RandomUsername()
	if err != nil {
		return nil, false, fmt.Errorf("生成管理员用户名失败: %w", err)
	}
	password, err := RandomString(16)
	if err != nil {
		return nil, false, fmt.Errorf("生成管理员密码失败: %w", err)
	}
	jwtSecret, err := RandomJWTSecret()
	if err != nil {
		return nil, false, fmt.Errorf("生成 JWT 密钥失败: %w", err)
	}

	cfg.Security.JWTSecret = jwtSecret
	cfg.Security.ManagerUserName = username
	cfg.Security.ManagerPassword = password
	cfg.Security.ManagerWebAuthDisabled = false
	cfg.Security.TrustProxyHeaders = false

	if err := WriteConfig(path, cfg); err != nil {
		return nil, false, fmt.Errorf("写入配置失败: %w", err)
	}
	// 兜底收紧权限，避免 umask 让密码字段被同机用户读取。
	_ = os.Chmod(path, 0600)
	return cfg, true, nil
}

// DisableSuperAdmin 把 cfg 的管理员账号设为 disable，并把 managerWebAuthDisabled 置为 true。
// 返回值：是否对传入 cfg 做了修改（用于日志/审计）。
func DisableSuperAdmin(cfg *LsmTokensServerConfig) bool {
	if cfg == nil {
		return false
	}
	changed := false
	if !strings.EqualFold(strings.TrimSpace(cfg.Security.ManagerUserName), DisableSentinel) {
		cfg.Security.ManagerUserName = DisableSentinel
		changed = true
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Security.ManagerPassword), DisableSentinel) {
		cfg.Security.ManagerPassword = DisableSentinel
		changed = true
	}
	if !cfg.Security.ManagerWebAuthDisabled {
		cfg.Security.ManagerWebAuthDisabled = true
		changed = true
	}
	return changed
}

// loadFromBytes 是 LoadConfig 内的字段拷贝逻辑抽取（避免 firstrun 包反向依赖 LoadConfig 自身）。
// 当配置存在但解析失败时，调用方应使用默认配置而不是报错——避免运维误删半个 JSON 字段导致服务拒启动。
func loadFromBytes(data []byte) (*LsmTokensServerConfig, error) {
	cfg := getDefaultConfig()
	if len(data) == 0 {
		return cfg, nil
	}
	var raw rawLsmTokensServerConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	// 字段映射（与 LoadConfig 完全一致）
	if raw.WebListenPort != 0 && raw.ManagerWebListenPort == 0 {
		raw.ManagerWebListenPort = raw.WebListenPort
	}
	cfg.ManagerWebListenPort = raw.ManagerWebListenPort
	cfg.UserWebListenPort = raw.UserWebListenPort
	cfg.McpWebListenPort = raw.McpWebListenPort
	cfg.UserWebUseHTTPS = raw.UserWebUseHTTPS
	cfg.UserWebCertFile = raw.UserWebCertFile
	cfg.UserWebKeyFile = raw.UserWebKeyFile
	cfg.ManagerWebStaticDir = raw.ManagerWebStaticDir
	cfg.UserWebStaticDir = raw.UserWebStaticDir
	cfg.AgentListenPort = raw.AgentListenPort
	cfg.AgentHttpsListenPort = raw.AgentHttpsListenPort
	cfg.AgentProductListenAddr = raw.AgentProductListenAddr
	cfg.AgentPublicHost = raw.AgentPublicHost
	cfg.AgentAnthropicListenURL = raw.AgentAnthropicListenURL
	cfg.AgentOpenAIListenURL = raw.AgentOpenAIListenURL
	cfg.LogFileURL = raw.LogFileURL
	cfg.LogMaxSizeMB = raw.LogMaxSizeMB
	cfg.DBMysql = raw.DBMysql
	cfg.TableMaxRows = raw.TableMaxRows
	cfg.DBMysqlSubTableNumber = raw.DBMysqlSubTableNumber
	cfg.UserInfoLogURL = raw.UserInfoLogURL
	cfg.EnableSpiderScheduler = raw.EnableSpiderScheduler
	cfg.SpiderCDPPort = raw.SpiderCDPPort
	cfg.SpiderChromePath = raw.SpiderChromePath
	cfg.SpiderChromeUserDataDir = raw.SpiderChromeUserDataDir
	cfg.SpiderCDPHealthCheckSec = raw.SpiderCDPHealthCheckSec
	cfg.SpiderCDPStartTimeoutSec = raw.SpiderCDPStartTimeoutSec
	cfg.SpiderHandlerTimeoutSec = raw.SpiderHandlerTimeoutSec
	cfg.SpiderMaxConcurrency = raw.SpiderMaxConcurrency
	cfg.SpiderChromeCustomArgs = raw.SpiderChromeCustomArgs
	cfg.SpiderUserAgent = raw.SpiderUserAgent
	cfg.SpiderUserAgentPerSource = raw.SpiderUserAgentPerSource
	cfg.SpiderProxy = raw.SpiderProxy
	cfg.SpiderActionWaitSec = raw.SpiderActionWaitSec
	cfg.SpiderEnableUAFlip = raw.SpiderEnableUAFlip
	cfg.SpiderUAFlipPool = raw.SpiderUAFlipPool
	cfg.SpiderProxyPool = raw.SpiderProxyPool
	cfg.SpiderPerSourceProxy = raw.SpiderPerSourceProxy
	cfg.SpiderRequestHeaders = raw.SpiderRequestHeaders
	cfg.SpiderMinNavDelayMs = raw.SpiderMinNavDelayMs
	cfg.SpiderMaxNavDelayMs = raw.SpiderMaxNavDelayMs
	cfg.SpiderFingerprintPerSession = raw.SpiderFingerprintPerSession
	cfg.SpiderAntiBotAutoRetry = raw.SpiderAntiBotAutoRetry
	cfg.SpiderRSSFetchTimeoutSec = raw.SpiderRSSFetchTimeoutSec
	cfg.SpiderStealthScript = raw.SpiderStealthScript
	cfg.SpiderStealthProMode = raw.SpiderStealthProMode
	cfg.SpiderStealthProFonts = raw.SpiderStealthProFonts
	cfg.SpiderHumanLikeEnabled = raw.SpiderHumanLikeEnabled
	cfg.SpiderThinkingTimeMeanMs = raw.SpiderThinkingTimeMeanMs
	cfg.SpiderThinkingTimeSigmaMs = raw.SpiderThinkingTimeSigmaMs
	cfg.SpiderBezierMouseMove = raw.SpiderBezierMouseMove
	cfg.SpiderMicroMouseMovements = raw.SpiderMicroMouseMovements
	cfg.SpiderReadingStyleScroll = raw.SpiderReadingStyleScroll
	cfg.SpiderBlockResourcesEnabled = raw.SpiderBlockResourcesEnabled
	cfg.SpiderBlockedURLPatterns = raw.SpiderBlockedURLPatterns
	cfg.SpiderBlockImageHeavy = raw.SpiderBlockImageHeavy
	cfg.SpiderProxyDeadThreshold = raw.SpiderProxyDeadThreshold
	cfg.SpiderProxyResurrectAfterSec = raw.SpiderProxyResurrectAfterSec
	cfg.SpiderProxyBindPerSession = raw.SpiderProxyBindPerSession
	cfg.SpiderAntiBotKillOnExhausted = raw.SpiderAntiBotKillOnExhausted
	cfg.SpiderAntiBotKillTabOnRetry = raw.SpiderAntiBotKillTabOnRetry
	cfg.OpenClawURL = raw.OpenClawURL
	cfg.OpenClawAPIKey = raw.OpenClawAPIKey
	cfg.OpenClawModel = raw.OpenClawModel
	cfg.OpenClawSystemPrompt = raw.OpenClawSystemPrompt
	cfg.OpenClawUserPromptTemplate = raw.OpenClawUserPromptTemplate
	cfg.Security = raw.Security
	if rawBytesContainsField(data, "transactionRetentionDays") {
		cfg.TransactionRetentionDays = raw.TransactionRetentionDays
	} else {
		cfg.TransactionRetentionDays = getDefaultConfig().TransactionRetentionDays
	}
	_ = validateAndFixConfig(cfg)
	return cfg, nil
}