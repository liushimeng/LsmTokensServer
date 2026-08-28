package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 默认配置常量
const (
	DEFAULT_MANAGER_WEB_PORT  = 9101
	DEFAULT_USER_WEB_PORT     = 29001
	DEFAULT_MCP_WEB_PORT      = 29002
	DEFAULT_LOG_SIZE          = 150 // MB
	DEFAULT_LOG_PATH          = "LsmTokensServer.log"
	DEFAULT_BUILD_LOG_PATH    = "LsmTokensServerBuildDateTime.log"
	DEFAULT_USER_LOG_PATH     = "LsmTokensServerUsersInfo.log"
	DEFAULT_TABLE_MAX_ROWS    = 20000000 // 2000万条
	DEFAULT_MYSQL_URL         = "127.0.0.1:3306"
	DEFAULT_MYSQL_DB          = "lsmDB"
	DEFAULT_MYSQL_USER        = ""
	DEFAULT_MYSQL_PWD         = ""
	DEFAULT_MYSQL_TIMEOUT     = 5
	DEFAULT_MYSQL_SHARD_KEY   = ""
	DEFAULT_MYSQL_SHARDS      = 0
	DEFAULT_MYSQL_PK_GEN      = 0
	DEFAULT_AGENT_LISTEN_PORT = 29000
	// v2.0.31: AI 代理 HTTPS 监听端口（与 HTTP 代理 agentListenPort 并存，复用同一套 handler）
	DEFAULT_AGENT_HTTPS_LISTEN_PORT  = 29003
	DEFAULT_SUB_TABLE_NUM            = 8
	DEFAULT_AGENT_PRODUCT_ADDR       = "127.0.0.1" // v2.0.56：默认本地地址，生产 IP 仅写入私有 conf
	DEFAULT_AGENT_ANTHROPIC_URL      = "Anthropic"
	DEFAULT_AGENT_OPENAI_URL         = "OpenAI"
	// v2.0.63: AgentPublicHost 客户端接入主机（公网/内网 IP 或域名），与监听地址 AgentProductListenAddr 解耦
	// 留空时运行期回退 AgentProductListenAddr，仍空则 127.0.0.1
	DEFAULT_AGENT_PUBLIC_HOST = ""
	DEFAULT_SPIDER_CDP_PORT          = 9222
	DEFAULT_SPIDER_CHROME_PATH       = "google-chrome-stable"
	DEFAULT_SPIDER_CHROME_DATA       = "/tmp/lsm-spider-chrome"
	DEFAULT_SPIDER_HEALTH_SEC        = 30
	DEFAULT_SPIDER_START_TO          = 30
	DEFAULT_SPIDER_HANDLER_TIMEOUT   = 180
	DEFAULT_SPIDER_RSS_FETCH_TIMEOUT = 15
	DEFAULT_SPIDER_MAX_CONCURRENCY   = 8
	DEFAULT_SPIDER_ACTION_WAIT_SEC   = 60
	// v2.0.47: 过期数据保留天数（默认 32 天；后台 goroutine 每天 03:30 自动清理）
	// 单行浏览记录 ≈ 4MB（4 个 longtext 字段），1 万条 ≈ 40GB；保留 32 天可覆盖
	// 大多数业务场景的合规审计需求，又避免无限膨胀。
	// v2.0.61: 保留天数从 70 天调整为 60 天。
	// v2.0.62: 保留天数从 60 天调整为 45 天（磁盘压力：8 张分表合计已达 280GB+）。
	// v2.0.63: 保留天数从 45 天调整为 32 天（磁盘压力仍在，进一步收缩窗口）。
	DEFAULT_TRANSACTION_RETENTION_DAYS = 32
	DEFAULT_CLEANUP_RUN_HOUR           = 3 // 业务低峰凌晨 3 点（可通过 LSM_CLEANUP_HOUR 环境变量覆盖）
	// v2.0.62: 每批删除条数从 5000 下调到 50。
	//
	// 依据（生产实测，不是拍脑袋）：过期行平均 body ≈ 459KB（4 个 longtext 字段），
	// 而本机 MariaDB `innodb_log_file_size` 仅 96MB。批次必须让「单事务 undo/redo」
	// 稳稳落在 redo log 之内，否则连接会被直接掐断（实测 500 行/批 ≈ 220MB
	// → `invalid connection`，shard_00 删到第 3500 行即中断）。
	//   500 行 ≈ 220MB ❌ 超 redo log
	//    50 行 ≈  22MB ✅ 留足余量
	// 仍可通过 LSM_CLEANUP_BATCH_SIZE 环境变量覆盖（大内存实例可调高）。
	DEFAULT_CLEANUP_BATCH_SIZE = 50
	// v2.0.4: OpenClaw AI 爬取默认配置
	DEFAULT_OPENCLAW_URL           = "http://127.0.0.1:18789/v1/chat/completions"
	DEFAULT_OPENCLAW_API_KEY       = "" // 脱敏删除
	DEFAULT_OPENCLAW_MODEL         = "openclaw"
	DEFAULT_OPENCLAW_SYSTEM_PROMPT = "你是一个专业的网络爬虫助手。请用中文回答所有问题，并按照用户给出的任务指令执行爬虫操作。"

	// v2.0.55: /ChatAnalysisTotalWS WebSocket 协议常量
	// 心跳与超时：服务端 30s ping，客户端 60s pong 容忍；写操作 10s 截止
	WS_WRITE_WAIT        = 10 * time.Second
	WS_PONG_WAIT         = 60 * time.Second
	WS_PING_PERIOD       = 30 * time.Second
	WS_MAX_MESSAGE_SIZE  = 4 * 1024 // 客户端最大消息字节（仅 query JSON，足够）
	WS_HANDSHAKE_TIMEOUT = 10 * time.Second
	// 全局 /ChatAnalysisTotalWS 连接上限 + 维度推送间隔
	CHAT_STATS_MAX_CONNS     = 128             // 并发 WS 连接上限（超出返回 503）
	CHAT_STATS_CHUNK_SPACING = 1 * time.Second // 旧维度块间间隔（v2.0.60 已改为单遍流式，仅保留常量兼容）
	// v2.0.60: 单遍分页流式聚合的累计快照推送节流间隔。
	// 首批立即推（首屏最快出数据），之后按此间隔节流，避免大表每批 5000 行都全量推 7 卡刷屏。
	CHAT_STATS_SNAPSHOT_MIN_INTERVAL = 500 * time.Millisecond
)

// DBMysqlConfig MySQL 数据库配置
type DBMysqlConfig struct {
	Url                 string `json:"Url"`
	DataBase            string `json:"DataBase"`
	User                string `json:"User"`
	Pwd                 string `json:"Pwd"`
	Timeout             int    `json:"Timeout"`
	ShardingKey         string `json:"ShardingKey"`
	NumberOfShards      int    `json:"NumberOfShards"`
	PrimaryKeyGenerator int    `json:"PrimaryKeyGenerator"`
}

// LsmTokensServerConfig 服务配置
type LsmTokensServerConfig struct {
	ManagerWebListenPort     int           `json:"managerWebListenPort"`
	UserWebListenPort        int           `json:"userWebListenPort"`
	McpWebListenPort         int           `json:"mcpWebListenPort"`
	UserWebUseHTTPS          bool          `json:"userWebUseHTTPS"`
	UserWebCertFile          string        `json:"userWebCertFile"`
	UserWebKeyFile           string        `json:"userWebKeyFile"`
	// 阶段T 双构建隔离：管理端/用户端静态产物目录可选覆盖（空 → 默认 ClientWeb/dist-manager / dist-user）
	ManagerWebStaticDir      string        `json:"managerWebStaticDir,omitempty"`
	UserWebStaticDir         string        `json:"userWebStaticDir,omitempty"`
	AgentListenPort          int           `json:"agentListenPort"`
	AgentHttpsListenPort     int           `json:"agentHttpsListenPort"`
	AgentProductListenAddr   string        `json:"agentProductListenAddr"`
	AgentPublicHost          string        `json:"agentPublicHost,omitempty"`
	AgentAnthropicListenURL  string        `json:"agentAnthropicListenURL"`
	AgentOpenAIListenURL     string        `json:"agentOpenAIListenURL"`
	LogFileURL               string        `json:"logFileURL"`
	LogMaxSizeMB             int           `json:"logMaxSizeMB"`
	DBMysql                  DBMysqlConfig `json:"DBMysql"`
	TableMaxRows             int64         `json:"tableMaxRows"`
	DBMysqlSubTableNumber    int           `json:"DBMysqlSubTableNumber"`
	UserInfoLogURL           string        `json:"userInfoLogURL"`
	EnableSpiderScheduler    bool          `json:"enableSpiderScheduler"`
	SpiderCDPPort            int           `json:"spiderCDPPort"`
	SpiderChromePath         string        `json:"spiderChromePath"`
	SpiderChromeUserDataDir  string        `json:"spiderChromeUserDataDir"`
	SpiderCDPHealthCheckSec  int           `json:"spiderCDPHealthCheckSec"`
	SpiderCDPStartTimeoutSec int           `json:"spiderCDPStartTimeoutSec"`
	SpiderHandlerTimeoutSec  int           `json:"spiderHandlerTimeoutSec"`
	SpiderMaxConcurrency     int           `json:"spiderMaxConcurrency"`
	SpiderChromeCustomArgs   []string      `json:"spiderChromeCustomArgs"` // 额外 Chrome 启动参数（如代理、stealth flags）
	// v2.0.3: 反爬增强配置
	SpiderUserAgent          string         `json:"spiderUserAgent,omitempty"`          // 全局自定义 UA（覆盖默认）
	SpiderUserAgentPerSource map[int]string `json:"spiderUserAgentPerSource,omitempty"` // data_source_id -> UA 映射
	SpiderProxy              string         `json:"spiderProxy,omitempty"`              // 代理服务器（http://host:port 或 socks5://host:port）
	SpiderActionWaitSec      int            `json:"spiderActionWaitSec,omitempty"`      // action click 后等待元素可见的超时秒数（默认 60）
	// v2.0.8: 主动反反爬配置
	SpiderEnableUAFlip          bool              `json:"spiderEnableUAFlip,omitempty"`
	SpiderUAFlipPool            []string          `json:"spiderUAFlipPool,omitempty"`
	SpiderProxyPool             []string          `json:"spiderProxyPool,omitempty"`
	SpiderPerSourceProxy        map[int]string    `json:"spiderPerSourceProxy,omitempty"`
	SpiderRequestHeaders        map[string]string `json:"spiderRequestHeaders,omitempty"`
	SpiderMinNavDelayMs         int               `json:"spiderMinNavDelayMs,omitempty"`
	SpiderMaxNavDelayMs         int               `json:"spiderMaxNavDelayMs,omitempty"`
	SpiderFingerprintPerSession bool              `json:"spiderFingerprintPerSession,omitempty"`
	SpiderAntiBotAutoRetry      int               `json:"spiderAntiBotAutoRetry,omitempty"`
	// v2.0.43: RSS fallback fetch timeout (seconds)
	SpiderRSSFetchTimeoutSec int    `json:"spiderRSSFetchTimeoutSec,omitempty"`
	SpiderStealthScript      string `json:"spiderStealthScript,omitempty"`
	// v2.0.9: Stealth Pro（深度浏览器指纹伪装：MediaDevices/字体/堆栈/chrome.runtime）
	SpiderStealthProMode  bool     `json:"spiderStealthProMode,omitempty"`  // 总开关（默认 false，保持 v2.0.8 行为）
	SpiderStealthProFonts []string `json:"spiderStealthProFonts,omitempty"` // 自定义字体列表（空 = 内置 ~30 个 Win/Mac/Linux 常见字体）
	// v2.0.9: Human-like Behavior（高斯思考延迟 / 贝塞尔鼠标 / 视口微动 / 阅读式滚动）
	SpiderHumanLikeEnabled    bool `json:"spiderHumanLikeEnabled,omitempty"`    // 总开关
	SpiderThinkingTimeMeanMs  int  `json:"spiderThinkingTimeMeanMs,omitempty"`  // 点击/提取前思考延迟均值 ms（0 = 禁用）
	SpiderThinkingTimeSigmaMs int  `json:"spiderThinkingTimeSigmaMs,omitempty"` // 标准差 ms（仅 mean>0 时生效，默认 250）
	SpiderBezierMouseMove     bool `json:"spiderBezierMouseMove,omitempty"`     // 用贝塞尔曲线移动鼠标替代瞬移
	SpiderMicroMouseMovements bool `json:"spiderMicroMouseMovements,omitempty"` // 在视口边缘做 1-2 次无意义微动
	SpiderReadingStyleScroll  bool `json:"spiderReadingStyleScroll,omitempty"`  // 阅读式滚动（仅影响 scroll action；不影响 scroll_to）
	// v2.0.9: Network Layer（CDP Network.setBlockedURLs 资源屏蔽）
	SpiderBlockResourcesEnabled bool     `json:"spiderBlockResourcesEnabled,omitempty"` // 启用资源屏蔽
	SpiderBlockedURLPatterns    []string `json:"spiderBlockedURLPatterns,omitempty"`    // 自定义 URL pattern（空 = 内置 *.css / *.woff* / *.ttf / *.otf）
	SpiderBlockImageHeavy       bool     `json:"spiderBlockImageHeavy,omitempty"`       // 追加屏蔽图片（*.png/jpg/jpeg/gif/webp/svg/ico）
	// v2.0.9: Proxy Pool 健康跟踪
	SpiderProxyDeadThreshold     int  `json:"spiderProxyDeadThreshold,omitempty"`     // 连续失败阈值（默认 3）
	SpiderProxyResurrectAfterSec int  `json:"spiderProxyResurrectAfterSec,omitempty"` // 死亡代理复活冷却（默认 300s，最小 60s）
	SpiderProxyBindPerSession    bool `json:"spiderProxyBindPerSession,omitempty"`    // session 级代理重新绑定（默认 true）
	// v2.0.9: Anti-bot Recovery（重试耗尽后的兜底恢复）
	SpiderAntiBotKillOnExhausted bool `json:"spiderAntiBotKillOnExhausted,omitempty"` // 重试耗尽后杀 Chrome 进程并重启（一次，重量）
	SpiderAntiBotKillTabOnRetry  bool `json:"spiderAntiBotKillTabOnRetry,omitempty"`  // 每次重试前 kill tab（轻量）
	// v2.0.4: OpenClaw AI 爬取配置
	OpenClawURL                string `json:"openClawURL,omitempty"`                // OpenClaw API 地址
	OpenClawAPIKey             string `json:"openClawAPIKey,omitempty"`             // OpenClaw API Key
	OpenClawModel              string `json:"openClawModel,omitempty"`              // OpenClaw 模型名称
	OpenClawSystemPrompt       string `json:"openClawSystemPrompt,omitempty"`       // 系统提示词（默认 "user"）
	OpenClawUserPromptTemplate string `json:"openClawUserPromptTemplate,omitempty"` // 用户提示词模板（含 XXXXXXXXX 占位符）
	// v2.0.47: 过期数据保留天数（浏览记录保留 N 天；0 = 禁用自动清理）
	TransactionRetentionDays int `json:"transactionRetentionDays"` // 默认 45 天，上限 3650 天（≈10 年）；显式 0 必须持久化
	// v2.0.56: 安全配置（见 SecurityConfig）
	Security SecurityConfig `json:"security"`
}

// SecurityConfig 安全配置：JWT 密钥与管理端登录凭证（仅存在于已 gitignore 的 LsmTokensServer.conf）
type SecurityConfig struct {
	// JWTSecret 用户端/管理端 JWT 签名密钥；为空时启动阶段自动生成随机密钥（重启后登录态失效）
	JWTSecret string `json:"jwtSecret,omitempty"`
	// ManagerUserName / ManagerPassword 管理端登录凭证；两者均未配置时管理端业务接口默认拒绝
	ManagerUserName string `json:"managerUserName,omitempty"`
	ManagerPassword string `json:"managerPassword,omitempty"`
	// TrustProxyHeaders 是否信任 X-Forwarded-For/X-Real-IP（仅部署于可信反向代理后开启）
	TrustProxyHeaders bool `json:"trustProxyHeaders,omitempty"`
	// ManagerWebAuthDisabled 是否关闭管理端 Web 登录鉴权（v2.0.58 网关代理部署）：
	// 管理员 Web 服务部署于已完成 Web 端鉴权的可信网关之后时置 true，
	// ManagerAuthMiddleware 全量放行（登录页/管理端 JWT 不再要求）。默认 false 保持安全红线。
	ManagerWebAuthDisabled bool `json:"managerWebAuthDisabled,omitempty"`
}

// rawLsmTokensServerConfig 用于读取旧版配置格式，实现向后兼容
type rawLsmTokensServerConfig struct {
	WebListenPort            int           `json:"webListenPort"`
	ManagerWebListenPort     int           `json:"managerWebListenPort"`
	UserWebListenPort        int           `json:"userWebListenPort"`
	McpWebListenPort         int           `json:"mcpWebListenPort"`
	UserWebUseHTTPS          bool          `json:"userWebUseHTTPS"`
	UserWebCertFile          string        `json:"userWebCertFile"`
	UserWebKeyFile           string        `json:"userWebKeyFile"`
	ManagerWebStaticDir      string        `json:"managerWebStaticDir,omitempty"`
	UserWebStaticDir         string        `json:"userWebStaticDir,omitempty"`
	AgentListenPort          int           `json:"agentListenPort"`
	AgentHttpsListenPort     int           `json:"agentHttpsListenPort"`
	AgentProductListenAddr   string        `json:"agentProductListenAddr"`
	AgentPublicHost          string        `json:"agentPublicHost,omitempty"`
	AgentAnthropicListenURL  string        `json:"agentAnthropicListenURL"`
	AgentOpenAIListenURL     string        `json:"agentOpenAIListenURL"`
	LogFileURL               string        `json:"logFileURL"`
	LogMaxSizeMB             int           `json:"logMaxSizeMB"`
	DBMysql                  DBMysqlConfig `json:"DBMysql"`
	TableMaxRows             int64         `json:"tableMaxRows"`
	DBMysqlSubTableNumber    int           `json:"DBMysqlSubTableNumber"`
	UserInfoLogURL           string        `json:"userInfoLogURL"`
	EnableSpiderScheduler    bool          `json:"enableSpiderScheduler"`
	SpiderCDPPort            int           `json:"spiderCDPPort"`
	SpiderChromePath         string        `json:"spiderChromePath"`
	SpiderChromeUserDataDir  string        `json:"spiderChromeUserDataDir"`
	SpiderCDPHealthCheckSec  int           `json:"spiderCDPHealthCheckSec"`
	SpiderCDPStartTimeoutSec int           `json:"spiderCDPStartTimeoutSec"`
	SpiderHandlerTimeoutSec  int           `json:"spiderHandlerTimeoutSec"`
	SpiderMaxConcurrency     int           `json:"spiderMaxConcurrency"`
	SpiderChromeCustomArgs   []string      `json:"spiderChromeCustomArgs"` // 额外 Chrome 启动参数（如代理、stealth flags）
	// v2.0.3: 反爬增强配置
	SpiderUserAgent          string         `json:"spiderUserAgent,omitempty"`          // 全局自定义 UA（覆盖默认）
	SpiderUserAgentPerSource map[int]string `json:"spiderUserAgentPerSource,omitempty"` // data_source_id -> UA 映射
	SpiderProxy              string         `json:"spiderProxy,omitempty"`              // 代理服务器（http://host:port 或 socks5://host:port）
	SpiderActionWaitSec      int            `json:"spiderActionWaitSec,omitempty"`      // action click 后等待元素可见的超时秒数（默认 60）
	// v2.0.8: 主动反反爬配置
	SpiderEnableUAFlip          bool              `json:"spiderEnableUAFlip,omitempty"`
	SpiderUAFlipPool            []string          `json:"spiderUAFlipPool,omitempty"`
	SpiderProxyPool             []string          `json:"spiderProxyPool,omitempty"`
	SpiderPerSourceProxy        map[int]string    `json:"spiderPerSourceProxy,omitempty"`
	SpiderRequestHeaders        map[string]string `json:"spiderRequestHeaders,omitempty"`
	SpiderMinNavDelayMs         int               `json:"spiderMinNavDelayMs,omitempty"`
	SpiderMaxNavDelayMs         int               `json:"spiderMaxNavDelayMs,omitempty"`
	SpiderFingerprintPerSession bool              `json:"spiderFingerprintPerSession,omitempty"`
	SpiderAntiBotAutoRetry      int               `json:"spiderAntiBotAutoRetry,omitempty"`
	// v2.0.43: RSS fallback fetch timeout (seconds)
	SpiderRSSFetchTimeoutSec int    `json:"spiderRSSFetchTimeoutSec,omitempty"`
	SpiderStealthScript      string `json:"spiderStealthScript,omitempty"`
	// v2.0.9: Stealth Pro（深度浏览器指纹伪装：MediaDevices/字体/堆栈/chrome.runtime）
	SpiderStealthProMode  bool     `json:"spiderStealthProMode,omitempty"`  // 总开关（默认 false，保持 v2.0.8 行为）
	SpiderStealthProFonts []string `json:"spiderStealthProFonts,omitempty"` // 自定义字体列表（空 = 内置 ~30 个 Win/Mac/Linux 常见字体）
	// v2.0.9: Human-like Behavior（高斯思考延迟 / 贝塞尔鼠标 / 视口微动 / 阅读式滚动）
	SpiderHumanLikeEnabled    bool `json:"spiderHumanLikeEnabled,omitempty"`    // 总开关
	SpiderThinkingTimeMeanMs  int  `json:"spiderThinkingTimeMeanMs,omitempty"`  // 点击/提取前思考延迟均值 ms（0 = 禁用）
	SpiderThinkingTimeSigmaMs int  `json:"spiderThinkingTimeSigmaMs,omitempty"` // 标准差 ms（仅 mean>0 时生效，默认 250）
	SpiderBezierMouseMove     bool `json:"spiderBezierMouseMove,omitempty"`     // 用贝塞尔曲线移动鼠标替代瞬移
	SpiderMicroMouseMovements bool `json:"spiderMicroMouseMovements,omitempty"` // 在视口边缘做 1-2 次无意义微动
	SpiderReadingStyleScroll  bool `json:"spiderReadingStyleScroll,omitempty"`  // 阅读式滚动（仅影响 scroll action；不影响 scroll_to）
	// v2.0.9: Network Layer（CDP Network.setBlockedURLs 资源屏蔽）
	SpiderBlockResourcesEnabled bool     `json:"spiderBlockResourcesEnabled,omitempty"` // 启用资源屏蔽
	SpiderBlockedURLPatterns    []string `json:"spiderBlockedURLPatterns,omitempty"`    // 自定义 URL pattern（空 = 内置 *.css / *.woff* / *.ttf / *.otf）
	SpiderBlockImageHeavy       bool     `json:"spiderBlockImageHeavy,omitempty"`       // 追加屏蔽图片（*.png/jpg/jpeg/gif/webp/svg/ico）
	// v2.0.9: Proxy Pool 健康跟踪
	SpiderProxyDeadThreshold     int  `json:"spiderProxyDeadThreshold,omitempty"`     // 连续失败阈值（默认 3）
	SpiderProxyResurrectAfterSec int  `json:"spiderProxyResurrectAfterSec,omitempty"` // 死亡代理复活冷却（默认 300s，最小 60s）
	SpiderProxyBindPerSession    bool `json:"spiderProxyBindPerSession,omitempty"`    // session 级代理重新绑定（默认 true）
	// v2.0.9: Anti-bot Recovery（重试耗尽后的兜底恢复）
	SpiderAntiBotKillOnExhausted bool `json:"spiderAntiBotKillOnExhausted,omitempty"` // 重试耗尽后杀 Chrome 进程并重启（一次，重量）
	SpiderAntiBotKillTabOnRetry  bool `json:"spiderAntiBotKillTabOnRetry,omitempty"`  // 每次重试前 kill tab（轻量）
	// v2.0.4: OpenClaw AI 爬取配置
	OpenClawURL                string `json:"openClawURL,omitempty"`                // OpenClaw API 地址
	OpenClawAPIKey             string `json:"openClawAPIKey,omitempty"`             // OpenClaw API Key
	OpenClawModel              string `json:"openClawModel,omitempty"`              // OpenClaw 模型名称
	OpenClawSystemPrompt       string `json:"openClawSystemPrompt,omitempty"`       // 系统提示词（默认 "user"）
	OpenClawUserPromptTemplate string `json:"openClawUserPromptTemplate,omitempty"` // 用户提示词模板（含 XXXXXXXXX 占位符）
	Security                   SecurityConfig `json:"security,omitempty"`
	// v2.0.47: 过期数据保留天数（向后兼容：旧配置无该字段时为 0，由 validateAndFixConfig 修复为默认 60）
	TransactionRetentionDays int `json:"transactionRetentionDays,omitempty"`
}

// GetDefaultConfig 返回默认配置（导出版，供测试/初始化全局配置使用）
func GetDefaultConfig() *LsmTokensServerConfig {
	return getDefaultConfig()
}

// getDefaultConfig 返回默认配置
func getDefaultConfig() *LsmTokensServerConfig {
	return &LsmTokensServerConfig{
		ManagerWebListenPort:    DEFAULT_MANAGER_WEB_PORT,
		UserWebListenPort:       DEFAULT_USER_WEB_PORT,
		McpWebListenPort:        DEFAULT_MCP_WEB_PORT,
		UserWebUseHTTPS:         false,
		UserWebCertFile:         "server.crt",
		UserWebKeyFile:          "server.key",
		AgentListenPort:         DEFAULT_AGENT_LISTEN_PORT,
		AgentHttpsListenPort:    DEFAULT_AGENT_HTTPS_LISTEN_PORT,
		AgentProductListenAddr:  DEFAULT_AGENT_PRODUCT_ADDR,
		AgentPublicHost:         DEFAULT_AGENT_PUBLIC_HOST,
		AgentAnthropicListenURL: DEFAULT_AGENT_ANTHROPIC_URL,
		AgentOpenAIListenURL:    DEFAULT_AGENT_OPENAI_URL,
		LogFileURL:              DEFAULT_LOG_PATH,
		LogMaxSizeMB:            DEFAULT_LOG_SIZE,
		DBMysql: DBMysqlConfig{
			Url:                 DEFAULT_MYSQL_URL,
			DataBase:            DEFAULT_MYSQL_DB,
			User:                DEFAULT_MYSQL_USER,
			Pwd:                 DEFAULT_MYSQL_PWD,
			Timeout:             DEFAULT_MYSQL_TIMEOUT,
			ShardingKey:         DEFAULT_MYSQL_SHARD_KEY,
			NumberOfShards:      DEFAULT_MYSQL_SHARDS,
			PrimaryKeyGenerator: DEFAULT_MYSQL_PK_GEN,
		},
		TableMaxRows:             DEFAULT_TABLE_MAX_ROWS,
		DBMysqlSubTableNumber:    DEFAULT_SUB_TABLE_NUM,
		UserInfoLogURL:           DEFAULT_USER_LOG_PATH,
		EnableSpiderScheduler:    false,
		SpiderCDPPort:            DEFAULT_SPIDER_CDP_PORT,
		SpiderChromePath:         DEFAULT_SPIDER_CHROME_PATH,
		SpiderChromeUserDataDir:  DEFAULT_SPIDER_CHROME_DATA,
		SpiderCDPHealthCheckSec:  DEFAULT_SPIDER_HEALTH_SEC,
		SpiderCDPStartTimeoutSec: DEFAULT_SPIDER_START_TO,
		SpiderHandlerTimeoutSec:  DEFAULT_SPIDER_HANDLER_TIMEOUT,
		SpiderMaxConcurrency:     DEFAULT_SPIDER_MAX_CONCURRENCY,
		SpiderChromeCustomArgs:   []string{},
		// v2.0.3: 反爬增强默认值
		SpiderUserAgent:          "",
		SpiderUserAgentPerSource: map[int]string{},
		SpiderProxy:              "",
		SpiderActionWaitSec:      DEFAULT_SPIDER_ACTION_WAIT_SEC,
		// v2.0.8: 主动反反爬默认值
		// v2.0.19: 默认启用核心反反爬能力（UA 轮换 + 指纹 + Stealth Pro + 导航抖动 + 资源屏蔽）
		SpiderEnableUAFlip:          true,
		SpiderUAFlipPool:            []string{},
		SpiderProxyPool:             []string{},
		SpiderPerSourceProxy:        map[int]string{},
		SpiderRequestHeaders:        map[string]string{},
		SpiderMinNavDelayMs:         200,
		SpiderMaxNavDelayMs:         800,
		SpiderFingerprintPerSession: true,
		SpiderAntiBotAutoRetry:      3,
		// v2.0.43: RSS fallback fetch timeout default 15s (slow international links)
		SpiderRSSFetchTimeoutSec: DEFAULT_SPIDER_RSS_FETCH_TIMEOUT,
		SpiderStealthScript:      "",
		// v2.0.9: Stealth Pro 默认值
		// v2.0.19: 默认启用 Stealth Pro 深度伪装
		SpiderStealthProMode:  true,
		SpiderStealthProFonts: []string{},
		// v2.0.9: Human-like Behavior 默认值
		SpiderHumanLikeEnabled:    false,
		SpiderThinkingTimeMeanMs:  0,
		SpiderThinkingTimeSigmaMs: 250,
		SpiderBezierMouseMove:     false,
		SpiderMicroMouseMovements: false,
		SpiderReadingStyleScroll:  false,
		// v2.0.9: Network Layer 默认值
		// v2.0.19: 默认启用资源屏蔽（CSS/字体减少特征暴露）
		SpiderBlockResourcesEnabled: true,
		SpiderBlockedURLPatterns:    []string{},
		SpiderBlockImageHeavy:       false,
		// v2.0.9: Proxy Pool 健康跟踪默认值
		SpiderProxyDeadThreshold:     3,
		SpiderProxyResurrectAfterSec: 300,
		SpiderProxyBindPerSession:    true,
		// v2.0.9: Anti-bot Recovery 默认值
		// v2.0.19: 默认启用重试前 kill tab（强制新 tab，清除站点端 session 追踪）
		SpiderAntiBotKillOnExhausted: false,
		SpiderAntiBotKillTabOnRetry:  true,
		// v2.0.4: OpenClaw AI 爬取默认配置
		OpenClawURL:                DEFAULT_OPENCLAW_URL,
		OpenClawAPIKey:             DEFAULT_OPENCLAW_API_KEY,
		OpenClawModel:              DEFAULT_OPENCLAW_MODEL,
		OpenClawSystemPrompt:       DEFAULT_OPENCLAW_SYSTEM_PROMPT,
		OpenClawUserPromptTemplate: "", // 空字符串表示使用内置默认模板
		// v2.0.47: 过期数据保留天数默认值 45 天（v2.0.61 从 70 调整为 60；v2.0.62 从 60 调整为 45）
		TransactionRetentionDays: DEFAULT_TRANSACTION_RETENTION_DAYS,
	}
}

// validateAndFixConfig 检查配置参数合法性，自动修正不合法值
func validateAndFixConfig(cfg *LsmTokensServerConfig) bool {
	fixed := false
	defaults := getDefaultConfig()

	if cfg.ManagerWebListenPort <= 0 || cfg.ManagerWebListenPort > 65535 {
		fmt.Printf("[CONFIG] ManagerWebListenPort %d invalid, using default %d\n", cfg.ManagerWebListenPort, defaults.ManagerWebListenPort)
		cfg.ManagerWebListenPort = defaults.ManagerWebListenPort
		fixed = true
	}

	if cfg.UserWebListenPort <= 0 || cfg.UserWebListenPort > 65535 {
		fmt.Printf("[CONFIG] UserWebListenPort %d invalid, using default %d\n", cfg.UserWebListenPort, defaults.UserWebListenPort)
		cfg.UserWebListenPort = defaults.UserWebListenPort
		fixed = true
	}

	if cfg.UserWebUseHTTPS {
		if cfg.UserWebCertFile == "" {
			fmt.Printf("[CONFIG] UserWebCertFile empty, using default %s\n", defaults.UserWebCertFile)
			cfg.UserWebCertFile = defaults.UserWebCertFile
			fixed = true
		}
		if cfg.UserWebKeyFile == "" {
			fmt.Printf("[CONFIG] UserWebKeyFile empty, using default %s\n", defaults.UserWebKeyFile)
			cfg.UserWebKeyFile = defaults.UserWebKeyFile
			fixed = true
		}
	}

	if cfg.AgentListenPort <= 0 || cfg.AgentListenPort > 65535 {
		fmt.Printf("[CONFIG] AgentListenPort %d invalid, using default %d\n", cfg.AgentListenPort, defaults.AgentListenPort)
		cfg.AgentListenPort = defaults.AgentListenPort
		fixed = true
	}

	// v2.0.31: AI 代理 HTTPS 监听端口校验。0 或越界时回落默认 29003
	// （旧配置文件无此字段，JSON 反序列化为 0 → 自动启用 HTTPS 代理，符合「默认 29003」语义）。
	// 若与 AgentListenPort 相同则在启动阶段跳过 HTTPS 监听（见 StartAIProxyService）。
	if cfg.AgentHttpsListenPort <= 0 || cfg.AgentHttpsListenPort > 65535 {
		fmt.Printf("[CONFIG] AgentHttpsListenPort %d invalid, using default %d\n", cfg.AgentHttpsListenPort, defaults.AgentHttpsListenPort)
		cfg.AgentHttpsListenPort = defaults.AgentHttpsListenPort
		fixed = true
	}

	if cfg.McpWebListenPort <= 0 || cfg.McpWebListenPort > 65535 {
		fmt.Printf("[CONFIG] McpWebListenPort %d invalid, using default %d\n", cfg.McpWebListenPort, defaults.McpWebListenPort)
		cfg.McpWebListenPort = defaults.McpWebListenPort
		fixed = true
	}

	if cfg.AgentProductListenAddr == "" {
		fmt.Printf("[CONFIG] AgentProductListenAddr empty, using default %s\n", defaults.AgentProductListenAddr)
		cfg.AgentProductListenAddr = defaults.AgentProductListenAddr
		fixed = true
	}

	if cfg.AgentAnthropicListenURL == "" {
		fmt.Printf("[CONFIG] AgentAnthropicListenURL empty, using default %s\n", defaults.AgentAnthropicListenURL)
		cfg.AgentAnthropicListenURL = defaults.AgentAnthropicListenURL
		fixed = true
	}

	if cfg.AgentOpenAIListenURL == "" {
		fmt.Printf("[CONFIG] AgentOpenAIListenURL empty, using default %s\n", defaults.AgentOpenAIListenURL)
		cfg.AgentOpenAIListenURL = defaults.AgentOpenAIListenURL
		fixed = true
	}

	if cfg.LogFileURL == "" {
		fmt.Printf("[CONFIG] LogFileURL empty, using default %s\n", defaults.LogFileURL)
		cfg.LogFileURL = defaults.LogFileURL
		fixed = true
	}

	if cfg.UserInfoLogURL == "" {
		fmt.Printf("[CONFIG] UserInfoLogURL empty, using default %s\n", defaults.UserInfoLogURL)
		cfg.UserInfoLogURL = defaults.UserInfoLogURL
		fixed = true
	}

	if cfg.LogMaxSizeMB <= 0 {
		fmt.Printf("[CONFIG] LogMaxSizeMB %d invalid, using default %d\n", cfg.LogMaxSizeMB, defaults.LogMaxSizeMB)
		cfg.LogMaxSizeMB = defaults.LogMaxSizeMB
		fixed = true
	}
	if cfg.LogMaxSizeMB > 1024 {
		fmt.Printf("[CONFIG] LogMaxSizeMB %d too large, capping at 1024 MB\n", cfg.LogMaxSizeMB)
		cfg.LogMaxSizeMB = 1024
		fixed = true
	}

	if cfg.TableMaxRows <= 0 {
		fmt.Printf("[CONFIG] TableMaxRows %d invalid, using default %d\n", cfg.TableMaxRows, defaults.TableMaxRows)
		cfg.TableMaxRows = defaults.TableMaxRows
		fixed = true
	}

	if cfg.DBMysqlSubTableNumber <= 0 {
		fmt.Printf("[CONFIG] DBMysqlSubTableNumber %d invalid, using default %d\n", cfg.DBMysqlSubTableNumber, defaults.DBMysqlSubTableNumber)
		cfg.DBMysqlSubTableNumber = defaults.DBMysqlSubTableNumber
		fixed = true
	}
	if cfg.DBMysqlSubTableNumber > 100 {
		fmt.Printf("[CONFIG] DBMysqlSubTableNumber %d too large, capping at 100\n", cfg.DBMysqlSubTableNumber)
		cfg.DBMysqlSubTableNumber = 100
		fixed = true
	}

	if cfg.DBMysql.Url == "" {
		fmt.Printf("[CONFIG] DBMysql.Url empty, using default %s\n", defaults.DBMysql.Url)
		cfg.DBMysql.Url = defaults.DBMysql.Url
		fixed = true
	}
	if cfg.DBMysql.DataBase == "" {
		fmt.Printf("[CONFIG] DBMysql.DataBase empty, using default %s\n", defaults.DBMysql.DataBase)
		cfg.DBMysql.DataBase = defaults.DBMysql.DataBase
		fixed = true
	}
	if cfg.DBMysql.Timeout <= 0 {
		fmt.Printf("[CONFIG] DBMysql.Timeout %d invalid, using default %d\n", cfg.DBMysql.Timeout, defaults.DBMysql.Timeout)
		cfg.DBMysql.Timeout = defaults.DBMysql.Timeout
		fixed = true
	}

	if cfg.SpiderCDPPort <= 0 || cfg.SpiderCDPPort > 65535 {
		fmt.Printf("[CONFIG] SpiderCDPPort %d invalid, using default %d\n", cfg.SpiderCDPPort, defaults.SpiderCDPPort)
		cfg.SpiderCDPPort = defaults.SpiderCDPPort
		fixed = true
	}
	if cfg.SpiderChromePath == "" {
		fmt.Printf("[CONFIG] SpiderChromePath empty, using default %s\n", defaults.SpiderChromePath)
		cfg.SpiderChromePath = defaults.SpiderChromePath
		fixed = true
	}
	if cfg.SpiderChromeUserDataDir == "" {
		fmt.Printf("[CONFIG] SpiderChromeUserDataDir empty, using default %s\n", defaults.SpiderChromeUserDataDir)
		cfg.SpiderChromeUserDataDir = defaults.SpiderChromeUserDataDir
		fixed = true
	}
	if cfg.SpiderCDPHealthCheckSec <= 0 {
		fmt.Printf("[CONFIG] SpiderCDPHealthCheckSec %d invalid, using default %d\n", cfg.SpiderCDPHealthCheckSec, defaults.SpiderCDPHealthCheckSec)
		cfg.SpiderCDPHealthCheckSec = defaults.SpiderCDPHealthCheckSec
		fixed = true
	}
	if cfg.SpiderCDPStartTimeoutSec <= 0 {
		fmt.Printf("[CONFIG] SpiderCDPStartTimeoutSec %d invalid, using default %d\n", cfg.SpiderCDPStartTimeoutSec, defaults.SpiderCDPStartTimeoutSec)
		cfg.SpiderCDPStartTimeoutSec = defaults.SpiderCDPStartTimeoutSec
		fixed = true
	}
	if cfg.SpiderHandlerTimeoutSec <= 0 {
		fmt.Printf("[CONFIG] SpiderHandlerTimeoutSec %d invalid, using default %d\n", cfg.SpiderHandlerTimeoutSec, defaults.SpiderHandlerTimeoutSec)
		cfg.SpiderHandlerTimeoutSec = defaults.SpiderHandlerTimeoutSec
		fixed = true
	}
	if cfg.SpiderHandlerTimeoutSec > 300 {
		fmt.Printf("[CONFIG] SpiderHandlerTimeoutSec %d too large, capping at 300\n", cfg.SpiderHandlerTimeoutSec)
		cfg.SpiderHandlerTimeoutSec = 300
		fixed = true
	}
	// v2.0.43: RSS fallback fetch timeout validation (5-60s, default 15s)
	if cfg.SpiderRSSFetchTimeoutSec <= 0 {
		fmt.Printf("[CONFIG] SpiderRSSFetchTimeoutSec %d invalid, using default %d\n", cfg.SpiderRSSFetchTimeoutSec, defaults.SpiderRSSFetchTimeoutSec)
		cfg.SpiderRSSFetchTimeoutSec = defaults.SpiderRSSFetchTimeoutSec
		fixed = true
	}
	if cfg.SpiderRSSFetchTimeoutSec < 5 {
		fmt.Printf("[CONFIG] SpiderRSSFetchTimeoutSec %d too small, capping at 5\n", cfg.SpiderRSSFetchTimeoutSec)
		cfg.SpiderRSSFetchTimeoutSec = 5
		fixed = true
	}
	if cfg.SpiderRSSFetchTimeoutSec > 60 {
		fmt.Printf("[CONFIG] SpiderRSSFetchTimeoutSec %d too large, capping at 60\n", cfg.SpiderRSSFetchTimeoutSec)
		cfg.SpiderRSSFetchTimeoutSec = 60
		fixed = true
	}
	if cfg.SpiderMaxConcurrency <= 0 {
		fmt.Printf("[CONFIG] SpiderMaxConcurrency %d invalid, using default %d\n", cfg.SpiderMaxConcurrency, defaults.SpiderMaxConcurrency)
		cfg.SpiderMaxConcurrency = defaults.SpiderMaxConcurrency
		fixed = true
	}
	if cfg.SpiderMaxConcurrency > 64 {
		fmt.Printf("[CONFIG] SpiderMaxConcurrency %d too large, capping at 64\n", cfg.SpiderMaxConcurrency)
		cfg.SpiderMaxConcurrency = 64
		fixed = true
	}
	// SpiderChromeCustomArgs 无需校验，允许空

	// v2.0.3: 反爬增强配置校验
	if cfg.SpiderActionWaitSec <= 0 {
		fmt.Printf("[CONFIG] SpiderActionWaitSec %d invalid, using default %d\n", cfg.SpiderActionWaitSec, defaults.SpiderActionWaitSec)
		cfg.SpiderActionWaitSec = defaults.SpiderActionWaitSec
		fixed = true
	}
	if cfg.SpiderActionWaitSec > 300 {
		fmt.Printf("[CONFIG] SpiderActionWaitSec %d too large, capping at 300\n", cfg.SpiderActionWaitSec)
		cfg.SpiderActionWaitSec = 300
		fixed = true
	}
	if cfg.SpiderUserAgentPerSource == nil {
		cfg.SpiderUserAgentPerSource = map[int]string{}
	}

	// v2.0.8: 主动反反爬配置校验
	if cfg.SpiderUAFlipPool == nil {
		cfg.SpiderUAFlipPool = []string{}
	}
	if cfg.SpiderProxyPool == nil {
		cfg.SpiderProxyPool = []string{}
	}
	if cfg.SpiderPerSourceProxy == nil {
		cfg.SpiderPerSourceProxy = map[int]string{}
	}
	if cfg.SpiderRequestHeaders == nil {
		cfg.SpiderRequestHeaders = map[string]string{}
	}
	// SpiderMinNavDelayMs / SpiderMaxNavDelayMs 范围校验
	if cfg.SpiderMinNavDelayMs < 0 {
		fmt.Printf("[CONFIG] SpiderMinNavDelayMs %d < 0, reset to 0\n", cfg.SpiderMinNavDelayMs)
		cfg.SpiderMinNavDelayMs = 0
		fixed = true
	}
	if cfg.SpiderMaxNavDelayMs < 0 {
		fmt.Printf("[CONFIG] SpiderMaxNavDelayMs %d < 0, reset to 0\n", cfg.SpiderMaxNavDelayMs)
		cfg.SpiderMaxNavDelayMs = 0
		fixed = true
	}
	if cfg.SpiderMinNavDelayMs > 60000 {
		fmt.Printf("[CONFIG] SpiderMinNavDelayMs %d > 60000, clamping\n", cfg.SpiderMinNavDelayMs)
		cfg.SpiderMinNavDelayMs = 60000
		fixed = true
	}
	if cfg.SpiderMaxNavDelayMs > 60000 {
		fmt.Printf("[CONFIG] SpiderMaxNavDelayMs %d > 60000, clamping\n", cfg.SpiderMaxNavDelayMs)
		cfg.SpiderMaxNavDelayMs = 60000
		fixed = true
	}
	if cfg.SpiderMaxNavDelayMs < cfg.SpiderMinNavDelayMs && cfg.SpiderMaxNavDelayMs > 0 {
		cfg.SpiderMinNavDelayMs, cfg.SpiderMaxNavDelayMs = cfg.SpiderMaxNavDelayMs, cfg.SpiderMinNavDelayMs
		fixed = true
	}
	// SpiderAntiBotAutoRetry 范围校验
	if cfg.SpiderAntiBotAutoRetry < 0 {
		cfg.SpiderAntiBotAutoRetry = 0
		fixed = true
	}
	if cfg.SpiderAntiBotAutoRetry > 5 {
		fmt.Printf("[CONFIG] SpiderAntiBotAutoRetry %d > 5, clamping\n", cfg.SpiderAntiBotAutoRetry)
		cfg.SpiderAntiBotAutoRetry = 5
		fixed = true
	}
	// SpiderStealthScript 长度校验
	if len(cfg.SpiderStealthScript) > 16*1024 {
		fmt.Printf("[CONFIG] SpiderStealthScript %d bytes > 16KB, truncating\n", len(cfg.SpiderStealthScript))
		cfg.SpiderStealthScript = cfg.SpiderStealthScript[:16*1024]
		fixed = true
	}

	// v2.0.9: 新增字段校验
	if cfg.SpiderStealthProFonts == nil {
		cfg.SpiderStealthProFonts = []string{}
	}
	if cfg.SpiderThinkingTimeMeanMs < 0 {
		fmt.Printf("[CONFIG] SpiderThinkingTimeMeanMs %d < 0, reset to 0\n", cfg.SpiderThinkingTimeMeanMs)
		cfg.SpiderThinkingTimeMeanMs = 0
		fixed = true
	}
	if cfg.SpiderThinkingTimeMeanMs > 60000 {
		fmt.Printf("[CONFIG] SpiderThinkingTimeMeanMs %d > 60000, clamping\n", cfg.SpiderThinkingTimeMeanMs)
		cfg.SpiderThinkingTimeMeanMs = 60000
		fixed = true
	}
	if cfg.SpiderThinkingTimeSigmaMs <= 0 && cfg.SpiderThinkingTimeMeanMs > 0 {
		cfg.SpiderThinkingTimeSigmaMs = 250
		fixed = true
	}
	if cfg.SpiderBlockedURLPatterns == nil {
		cfg.SpiderBlockedURLPatterns = []string{}
	}
	// 过滤空 pattern
	if len(cfg.SpiderBlockedURLPatterns) > 0 {
		cleaned := make([]string, 0, len(cfg.SpiderBlockedURLPatterns))
		for _, p := range cfg.SpiderBlockedURLPatterns {
			p = strings.TrimSpace(p)
			if p != "" {
				cleaned = append(cleaned, p)
			}
		}
		if len(cleaned) != len(cfg.SpiderBlockedURLPatterns) {
			cfg.SpiderBlockedURLPatterns = cleaned
			fixed = true
		}
	}
	if cfg.SpiderProxyDeadThreshold < 0 {
		fmt.Printf("[CONFIG] SpiderProxyDeadThreshold %d < 0, reset to 3\n", cfg.SpiderProxyDeadThreshold)
		cfg.SpiderProxyDeadThreshold = 3
		fixed = true
	}
	if cfg.SpiderProxyDeadThreshold > 100 {
		fmt.Printf("[CONFIG] SpiderProxyDeadThreshold %d > 100, clamping\n", cfg.SpiderProxyDeadThreshold)
		cfg.SpiderProxyDeadThreshold = 100
		fixed = true
	}
	if cfg.SpiderProxyResurrectAfterSec > 0 && cfg.SpiderProxyResurrectAfterSec < 60 {
		fmt.Printf("[CONFIG] SpiderProxyResurrectAfterSec %d < 60, clamping to 60\n", cfg.SpiderProxyResurrectAfterSec)
		cfg.SpiderProxyResurrectAfterSec = 60
		fixed = true
	}
	// 代理池 URL 校验
	cleaned := make([]string, 0, len(cfg.SpiderProxyPool))
	for _, p := range cfg.SpiderProxyPool {
		if tryProxyScheme(p) {
			cleaned = append(cleaned, p)
		} else if p != "" {
			fmt.Printf("[CONFIG] dropped invalid proxy URL: %s\n", p)
			fixed = true
		}
	}
	cfg.SpiderProxyPool = cleaned
	// per-source proxy URL 校验
	for dsID, p := range cfg.SpiderPerSourceProxy {
		if !tryProxyScheme(p) && p != "" {
			fmt.Printf("[CONFIG] dropped invalid per-source proxy (dsID=%d): %s\n", dsID, p)
			delete(cfg.SpiderPerSourceProxy, dsID)
			fixed = true
		}
	}

	// v2.0.4: OpenClaw 配置校验
	if cfg.OpenClawURL == "" {
		fmt.Printf("[CONFIG] OpenClawURL empty, using default %s\n", defaults.OpenClawURL)
		cfg.OpenClawURL = defaults.OpenClawURL
		fixed = true
	}
	if cfg.OpenClawAPIKey == "" {
		fmt.Printf("[CONFIG] OpenClawAPIKey empty, using default\n")
		cfg.OpenClawAPIKey = defaults.OpenClawAPIKey
		fixed = true
	}
	if cfg.OpenClawModel == "" {
		fmt.Printf("[CONFIG] OpenClawModel empty, using default %s\n", defaults.OpenClawModel)
		cfg.OpenClawModel = defaults.OpenClawModel
		fixed = true
	}
	if cfg.OpenClawSystemPrompt == "" {
		fmt.Printf("[CONFIG] OpenClawSystemPrompt empty, using default %s\n", defaults.OpenClawSystemPrompt)
		cfg.OpenClawSystemPrompt = defaults.OpenClawSystemPrompt
		fixed = true
	}

	// v2.0.47: 过期数据保留天数校验（0=禁用自动清理；1-3650 合法；>3650 截断）
	// 注意：旧配置缺失字段的场景已在 LoadConfig 中通过 rawBytesContainsField 检测并
	// 提前设置为默认 45 天；此处只校验"显式非法值"。
	// 用户显式设 0 表示禁用（运维保留全部数据用于审计），不视为非法。
	if cfg.TransactionRetentionDays < 0 {
		fmt.Printf("[CONFIG] TransactionRetentionDays %d < 0, using default %d\n", cfg.TransactionRetentionDays, DEFAULT_TRANSACTION_RETENTION_DAYS)
		cfg.TransactionRetentionDays = DEFAULT_TRANSACTION_RETENTION_DAYS
		fixed = true
	}
	if cfg.TransactionRetentionDays > 3650 {
		fmt.Printf("[CONFIG] TransactionRetentionDays %d too large, capping at 3650\n", cfg.TransactionRetentionDays)
		cfg.TransactionRetentionDays = 3650
		fixed = true
	}

	return fixed
}

func LoadConfig(path string) (*LsmTokensServerConfig, error) {
	cfg := getDefaultConfig()

	// 记录配置文件所在目录（工程根目录），供 ResolvePath 解析相对运行时产物路径。
	// 传入的 path 通常已由 loadConfigSafe 解析为绝对路径；相对路径时先转绝对再取目录。
	if absPath, err := filepath.Abs(path); err == nil {
		configDir = filepath.Dir(absPath)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("[CONFIG] Config file not found, creating default: %s\n", path)
		_ = WriteConfig(path, cfg)
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("[CONFIG] Failed to read config file: %v, using defaults\n", err)
		_ = WriteConfig(path, cfg)
		return cfg, nil
	}

	var raw rawLsmTokensServerConfig
	if err := json.Unmarshal(data, &raw); err == nil {
		if raw.WebListenPort != 0 && raw.ManagerWebListenPort == 0 {
			raw.ManagerWebListenPort = raw.WebListenPort
			fmt.Printf("[CONFIG] Migrating webListenPort=%d to managerWebListenPort\n", raw.WebListenPort)
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
		// v2.0.3: 反爬增强配置从 raw 读取
		cfg.SpiderUserAgent = raw.SpiderUserAgent
		cfg.SpiderUserAgentPerSource = raw.SpiderUserAgentPerSource
		cfg.SpiderProxy = raw.SpiderProxy
		cfg.SpiderActionWaitSec = raw.SpiderActionWaitSec
		// v2.0.8: 主动反反爬配置从 raw 读取
		cfg.SpiderEnableUAFlip = raw.SpiderEnableUAFlip
		cfg.SpiderUAFlipPool = raw.SpiderUAFlipPool
		cfg.SpiderProxyPool = raw.SpiderProxyPool
		cfg.SpiderPerSourceProxy = raw.SpiderPerSourceProxy
		cfg.SpiderRequestHeaders = raw.SpiderRequestHeaders
		cfg.SpiderMinNavDelayMs = raw.SpiderMinNavDelayMs
		cfg.SpiderMaxNavDelayMs = raw.SpiderMaxNavDelayMs
		cfg.SpiderFingerprintPerSession = raw.SpiderFingerprintPerSession
		cfg.SpiderAntiBotAutoRetry = raw.SpiderAntiBotAutoRetry
		// v2.0.43: RSS fallback fetch timeout from raw config
		cfg.SpiderRSSFetchTimeoutSec = raw.SpiderRSSFetchTimeoutSec
		cfg.SpiderStealthScript = raw.SpiderStealthScript
		// v2.0.9: 新配置从 raw 读取（全部 omitempty，缺失时保持默认值）
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
		// v2.0.4: OpenClaw AI 爬取配置从 raw 读取
		cfg.OpenClawURL = raw.OpenClawURL
		cfg.OpenClawAPIKey = raw.OpenClawAPIKey
		cfg.OpenClawModel = raw.OpenClawModel
		cfg.OpenClawSystemPrompt = raw.OpenClawSystemPrompt
		cfg.OpenClawUserPromptTemplate = raw.OpenClawUserPromptTemplate
		// v2.0.56: 安全配置（jwtSecret/管理端凭证/代理头信任）
		cfg.Security = raw.Security
		// v2.0.47: 过期数据保留天数从 raw 读取
		// 语义：raw.TransactionRetentionDays < 0 → 非法值由 validateAndFixConfig 修复；
		//      raw.TransactionRetentionDays == 0 → 旧配置缺失字段，强制启用默认 45 天；
		//      raw.TransactionRetentionDays > 0 → 用户显式设置（保留原值，含 0=禁用的语义）
		// 实现：通过与 getDefaultConfig() 默认值对比判断"是否显式设置"。
		defaults := getDefaultConfig()
		if rawBytesContainsField(data, "transactionRetentionDays") {
			cfg.TransactionRetentionDays = raw.TransactionRetentionDays
		} else {
			// 旧配置无该字段：强制启用默认 45 天
			cfg.TransactionRetentionDays = defaults.TransactionRetentionDays
			fmt.Printf("[CONFIG] transactionRetentionDays missing, enabling default %d days\n", defaults.TransactionRetentionDays)
		}
	} else {
		fmt.Printf("[CONFIG] Failed to parse JSON config: %v, using defaults\n", err)
	}

	fixed := validateAndFixConfig(cfg)
	if fixed {
		fmt.Printf("[CONFIG] Configuration fixed and rewriting to: %s\n", path)
		_ = WriteConfig(path, cfg)
	} else {
		_ = WriteConfig(path, cfg)
	}

	return cfg, nil
}

func WriteConfig(path string, cfg *LsmTokensServerConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	dirPath := filepath.Dir(path)
	if dirPath != "" && dirPath != "." {
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	return os.WriteFile(path, data, 0644)
}

// rawBytesContainsField 检查原始 JSON 字节流是否包含指定字段名（v2.0.47）
//
// 用途：判断配置字段是"用户显式设置"还是"旧配置缺失"。
//   - 存在 "key": → 用户显式设置（值可能是 0 表示禁用）
//   - 不存在 "key": → 旧配置缺失（按"先保数据再删"语义启用默认值）
//
// 实现：朴素字符串扫描（性能不重要，配置文件通常 < 64KB）。
// 注意：此函数**不**解析 JSON（避开 Unmarshal 的零值覆盖），仅按字面量匹配。
func rawBytesContainsField(data []byte, fieldName string) bool {
	if len(data) == 0 {
		return false
	}
	// 匹配两种格式：有空格/无空格（缩进风格差异）
	patterns := []string{
		fmt.Sprintf("\"%s\":", fieldName),
		fmt.Sprintf("\"%s\" :", fieldName), // 防御性：有些 JSON 库会加空格
	}
	for _, p := range patterns {
		if strings.Contains(string(data), p) {
			return true
		}
	}
	return false
}

// tryProxyScheme 校验代理 URL scheme（http:// / https:// / socks5://）
// （自旧工程 spider_anti_bot.go 提取的公共小工具，config 校验代理配置时使用）
func tryProxyScheme(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "socks5://")
}

// DefaultConfig 返回默认配置（导出供测试与初始化使用）
func DefaultConfig() *LsmTokensServerConfig {
	return getDefaultConfig()
}

// G 运行时全局配置（由 main 在 LoadConfig 后赋值，替代旧工程 package main 的全局 cfg）
var G *LsmTokensServerConfig

// APP_VERSION 应用版本（自旧工程 main.go 迁移）
const APP_VERSION = "v2.0.76"

// APP_NAME / PRODUCT_NAME 产品标识（自 main.go 迁出为单一事实来源，
// 供 api 包 /AppVersionInterface 等跨包复用；main.go 以常量别名引用）
const (
	APP_NAME     = "LsmTokensServer"
	PRODUCT_NAME = "Lsm AI Tokens 代理服务"
)

// BuildTime 后端编译时间（由 rebuild_restart_app.sh 经 -ldflags 注入 main.buildTime，
// main 启动时赋值到此处供 api 包读取；未注入时为空字符串）
var BuildTime string
