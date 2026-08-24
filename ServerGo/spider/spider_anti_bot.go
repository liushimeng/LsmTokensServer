package spider

// ==================== v2.0.8 反反爬模块 ====================
//
// 该文件集中放置 v2.0.8 主动反反爬能力：
//   1. UA 轮换池（UAPool）：内置 12 个真实浏览器 UA + 用户自定义覆盖
//   2. 请求头随机化（BuildHeaderBundle）：Accept-Language / Sec-CH-UA-* / Sec-Fetch-* 一致性派生
//   3. 代理池（ProxyPool）：轮询 + per-data-source 覆盖
//   4. 指纹轮换（BuildFingerprint / BuildStealthScript / ApplyFingerprint）：UA + viewport + 硬件并发 + 设备内存 + 时区 + canvas noise
//   5. 行为抖动（JitterSleepMs）：chromedp.Sleep 区间随机延迟
//   6. 自适应重试（BuildRetryPlan / ShouldAutoRetry / AntiBotState）：仅 anti_bot / captcha 重试
//   7. 集成入口（prepareSessionNavigation / uaForSession / rerollSessionAntiBot）
//
// 设计原则：
//   - 默认行为与 v2.0.7 字节级一致：所有新功能都依赖配置开关
//   - per-session 状态冻结（UA / 代理 / 指纹 / 头）：session 首次 navigate 时构建，整个 TTL 内复用
//   - 纯函数优先：BuildFingerprint / BuildHeaderBundle / BuildStealthScript 无副作用，便于单测
//   - chromedp 集成通过 chromedp/cdproto/{network,emulation}，与现有 stealthInitScript 路径保持一致

import (
	"context"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	"hash/fnv"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ==================== UA 池 ====================

// UAPool 线程安全的 UA 轮换池
type UAPool struct {
	mu    sync.Mutex
	items []string
	idx   int
}

// defaultUAPool 内置 12 个真实浏览器 UA（覆盖 Linux/macOS/Windows Chrome 124-128 + Edge 126 + Safari 17）
// 选取原则：与当前 Chrome 版本跨度不过大（124-128），覆盖主流 OS 平台
func defaultUAPool() []string {
	return []string{
		// Linux Chrome 124-128
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		// macOS Chrome 126-128
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		// Windows Chrome 126-127
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		// Edge 126
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0",
		// Safari 17 macOS
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
	}
}

// LoadUAPool 加载 UA 池；custom 非空则用 custom，否则用内置 12 个
func LoadUAPool(custom []string) *UAPool {
	items := custom
	if len(items) == 0 {
		items = defaultUAPool()
	} else {
		// 过滤空字符串、trim
		cleaned := make([]string, 0, len(items))
		for _, ua := range items {
			ua = strings.TrimSpace(ua)
			if ua != "" {
				cleaned = append(cleaned, ua)
			}
		}
		items = cleaned
	}
	return &UAPool{items: items}
}

// Next 轮询取下一个 UA
func (p *UAPool) Next() string {
	if p == nil || len(p.items) == 0 {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ua := p.items[p.idx%len(p.items)]
	p.idx++
	return ua
}

// PeekAll 返回 UA 列表的拷贝（测试用）
func (p *UAPool) PeekAll() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.items))
	copy(out, p.items)
	return out
}

// ==================== 请求头 ====================

// HeaderBundle 派生请求头集合
// 所有字段都从 UA 派生，保持指纹一致性
type HeaderBundle struct {
	Accept          string
	AcceptLanguage  string
	AcceptEncoding  string
	SecCHUA         string
	SecCHPlatform   string
	SecFetchDest    string
	SecFetchMode    string
	SecFetchSite    string
	SecFetchUser    string
	Referer         string
	DNT             string
	UpgradeInsecure string
	Custom          map[string]string
}

// platformFromUA 从 UA 字符串提取平台（macOS / Linux / Windows）
func platformFromUA(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "mac os x") || strings.Contains(lower, "macintosh"):
		return "macOS"
	case strings.Contains(lower, "windows nt"):
		return "Windows"
	case strings.Contains(lower, "android"):
		return "Android"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad"):
		return "iOS"
	default:
		return "Linux"
	}
}

// buildAcceptLanguage 根据平台和 baseLang 派生 Accept-Language
func buildAcceptLanguage(platform, baseLang string) string {
	// baseLang 形如 "zh-CN,en-US,en" 或 "en-US,en;q=0.9"
	if baseLang == "" {
		switch platform {
		case "macOS", "Windows":
			baseLang = "en-US,en;q=0.9"
		default:
			baseLang = "zh-CN,zh;q=0.9,en;q=0.8"
		}
	}
	return baseLang
}

// buildSecCHUA 从 UA 构造 Sec-CH-UA（简化版，仅含 brand + version）
// 真实 Sec-CH-UA 形如 `"Chromium";v="126", "Not_A Brand";v="24", "Google Chrome";v="126"`
// 这里只输出主要 brand，足够欺骗多数反爬
func buildSecCHUA(ua string) string {
	if strings.Contains(ua, "Edg/") {
		// Edge
		return `"Chromium";v="126", "Not_A Brand";v="24", "Microsoft Edge";v="126"`
	}
	if strings.Contains(ua, "Safari/") && !strings.Contains(ua, "Chrome/") {
		// Safari 不发 Sec-CH-UA
		return ""
	}
	// Chrome / Chromium
	return `"Chromium";v="126", "Not_A Brand";v="24", "Google Chrome";v="126"`
}

// BuildHeaderBundle 构造与 UA 一致的请求头集合
// baseLang 形如 "zh-CN,zh;q=0.9,en;q=0.8"（空 = 根据平台自动选）
// referer 可选（如从 referer 链派生）
// custom 是用户配置的额外头（spiderRequestHeaders）
// seed 用于确定 Accept 默认值
func BuildHeaderBundle(ua, baseLang, referer string, custom map[string]string, seed int64) HeaderBundle {
	platform := platformFromUA(ua)
	acceptLang := buildAcceptLanguage(platform, baseLang)

	// Accept: image/webp 等 — 简单轮转
	acceptOptions := []string{
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}
	accept := acceptOptions[seed%int64(len(acceptOptions))]

	bundle := HeaderBundle{
		Accept:          accept,
		AcceptLanguage:  acceptLang,
		AcceptEncoding:  "gzip, deflate, br",
		SecCHUA:         buildSecCHUA(ua),
		SecCHPlatform:   `"` + platform + `"`,
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "?1",
		Referer:         referer,
		DNT:             "1",
		UpgradeInsecure: "1",
		Custom:          custom,
	}
	return bundle
}

// ToNetworkHeaders 转换为 network.Headers 格式（用于 network.SetExtraHTTPHeaders）
// 跳过空值；Custom 覆盖同名头
func (b HeaderBundle) ToNetworkHeaders() network.Headers {
	headers := network.Headers{}
	if b.Accept != "" {
		headers["Accept"] = b.Accept
	}
	if b.AcceptLanguage != "" {
		headers["Accept-Language"] = b.AcceptLanguage
	}
	if b.AcceptEncoding != "" {
		headers["Accept-Encoding"] = b.AcceptEncoding
	}
	if b.SecCHUA != "" {
		headers["Sec-CH-UA"] = b.SecCHUA
	}
	if b.SecCHPlatform != "" {
		headers["Sec-CH-UA-Platform"] = b.SecCHPlatform
	}
	if b.SecFetchDest != "" {
		headers["Sec-Fetch-Dest"] = b.SecFetchDest
	}
	if b.SecFetchMode != "" {
		headers["Sec-Fetch-Mode"] = b.SecFetchMode
	}
	if b.SecFetchSite != "" {
		headers["Sec-Fetch-Site"] = b.SecFetchSite
	}
	if b.SecFetchUser != "" {
		headers["Sec-Fetch-User"] = b.SecFetchUser
	}
	if b.Referer != "" {
		headers["Referer"] = b.Referer
	}
	if b.DNT != "" {
		headers["DNT"] = b.DNT
	}
	if b.UpgradeInsecure != "" {
		headers["Upgrade-Insecure-Requests"] = b.UpgradeInsecure
	}
	for k, v := range b.Custom {
		headers[k] = v
	}
	return headers
}

// IsEmpty 检查是否没有任何头可发送（用于决定是否调用 network.SetExtraHTTPHeaders）
func (b HeaderBundle) IsEmpty() bool {
	return b.Accept == "" && b.AcceptLanguage == "" && b.AcceptEncoding == "" &&
		b.SecCHUA == "" && b.SecCHPlatform == "" && b.Referer == "" &&
		b.DNT == "" && b.UpgradeInsecure == "" && len(b.Custom) == 0
}

// ==================== 代理池 ====================

// ProxyHealth 单个代理的健康状态（v2.0.9）
type ProxyHealth struct {
	ConsecutiveFails int       // 连续失败次数
	LastFailType     string    // 上一次失败的错误类型（anti_bot/captcha/region_block/...）
	LastFailAt       time.Time // 上一次失败时间
	Dead             bool      // 是否被标记为死亡（从轮询池中移除）
	DeadSince        time.Time // 标记死亡的时间
}

// ProxyDescriptor 单个代理项
type ProxyDescriptor struct {
	URL        string
	DataSrcIDs []int // 空 = 全局
	Health     ProxyHealth
}

// ProxyPool 代理轮询池
type ProxyPool struct {
	mu    sync.Mutex
	items []ProxyDescriptor
	idx   int
}

// LoadProxyPool 加载代理池；过滤空 / scheme 不合法的项
func LoadProxyPool(items []string) *ProxyPool {
	cleaned := make([]ProxyDescriptor, 0, len(items))
	for _, raw := range items {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !tryProxyScheme(raw) {
			continue
		}
		cleaned = append(cleaned, ProxyDescriptor{URL: raw})
	}
	return &ProxyPool{items: cleaned}
}

// Next 轮询取下一个代理 URL（v2.0.9: 跳过 Dead 标记的代理）
func (p *ProxyPool) Next() string {
	if p == nil || len(p.items) == 0 {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// v2.0.9: 在最多 len(items) 步内查找一个 live 代理
	startIdx := p.idx
	for i := 0; i < len(p.items); i++ {
		idx := (startIdx + i) % len(p.items)
		p.idx = (idx + 1) % len(p.items)
		if !p.items[idx].Health.Dead {
			return p.items[idx].URL
		}
	}
	return ""
}

// PeekAll 返回代理 URL 列表的拷贝（测试用）
func (p *ProxyPool) PeekAll() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.items))
	for i, it := range p.items {
		out[i] = it.URL
	}
	return out
}

// tryProxyScheme 校验代理 URL scheme（http:// / https:// / socks5://）
func tryProxyScheme(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "socks5://")
}

// ResolveProxyForDataSource 解析给定 data_source_id 的代理
// 优先级：perSource[dsID] > pool.Next()
// 注意：当前实现仅在 Chrome 启动期生效（--proxy-server），per-source 切换依赖 health-check 重启
func ResolveProxyForDataSource(pool *ProxyPool, perSource map[int]string, dsID uint64) string {
	if dsID > 0 {
		if proxy, ok := perSource[int(dsID)]; ok && proxy != "" {
			return proxy
		}
	}
	if pool == nil {
		return ""
	}
	return pool.Next()
}

// ==================== 指纹 ====================

// Fingerprint per-session 指纹快照
// 同 session 内固定；不同 session 不同（通过 sessionSeed 派生）
type Fingerprint struct {
	UA                  string
	Platform            string
	ViewportW           int
	ViewportH           int
	DeviceScaleFactor   float64
	HardwareConcurrency int
	DeviceMemoryGB      int
	Languages           []string
	TimezoneOffsetMin   int
	CanvasNoiseSeed     int64
	Seed                int64
}

// viewportOptions 候选 viewport 尺寸
var viewportOptions = []struct{ W, H int }{
	{1366, 768},
	{1440, 900},
	{1536, 864},
	{1600, 900},
	{1680, 1050},
	{1920, 1080},
}

// hardwareOptions 候选硬件并发数
var hardwareOptions = []int{2, 4, 8, 16}

// memoryOptions 候选设备内存 (GB)
var memoryOptions = []int{2, 4, 8}

// BuildFingerprint 基于 sessionSeed 确定性构造指纹
// sessionSeed 可用 time.Now().UnixNano()（新 session）或 session.SessionID 哈希（确定 session）
func BuildFingerprint(ua string, sessionSeed int64) *Fingerprint {
	if ua == "" {
		ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	}
	platform := platformFromUA(ua)
	// 用 sessionSeed 派生各字段
	r := rand.New(rand.NewSource(sessionSeed))
	vp := viewportOptions[r.Intn(len(viewportOptions))]
	hc := hardwareOptions[r.Intn(len(hardwareOptions))]
	mem := memoryOptions[r.Intn(len(memoryOptions))]
	// 时区偏移 -720..+720 分钟，步长 60
	tz := (r.Intn(25) - 12) * 60
	// languages
	var langs []string
	switch platform {
	case "macOS", "Windows":
		langs = []string{"en-US", "en"}
	default:
		langs = []string{"zh-CN", "en-US", "en"}
	}
	return &Fingerprint{
		UA:                  ua,
		Platform:            platform,
		ViewportW:           vp.W,
		ViewportH:           vp.H,
		DeviceScaleFactor:   1.0,
		HardwareConcurrency: hc,
		DeviceMemoryGB:      mem,
		Languages:           langs,
		TimezoneOffsetMin:   tz,
		CanvasNoiseSeed:     sessionSeed,
		Seed:                sessionSeed,
	}
}

// BuildStealthScript 构造 stealth JS 字符串
// fp == nil 时返回与 v2.0.7 字节级一致的 baseJS（保持向后兼容）
// fp != nil 时附加 canvas noise / hardware / memory / timezone 注入
// userPrefix 非空时拼接到 JS 顶部（最大 16KB）
func BuildStealthScript(fp *Fingerprint, userPrefix string) string {
	const maxPrefix = 16 * 1024
	prefix := userPrefix
	if len(prefix) > maxPrefix {
		logger.Printf("[SPIDER] SpiderStealthScript truncated from %d to %d bytes", len(prefix), maxPrefix)
		prefix = prefix[:maxPrefix]
	}
	base := buildStealthScriptBase()
	if fp == nil {
		if prefix == "" {
			return base
		}
		return prefix + ";\n" + base
	}
	// 扩展 stealth 脚本：覆盖 hardwareConcurrency / deviceMemory / screen / timezone / canvas noise
	extra := buildStealthFingerprintJS(fp)
	if prefix == "" {
		return base + ";\n" + extra
	}
	return prefix + ";\n" + base + ";\n" + extra
}

// buildStealthFingerprintJS 构造指纹扩展 JS
func buildStealthFingerprintJS(fp *Fingerprint) string {
	langs := make([]string, len(fp.Languages))
	for i, l := range fp.Languages {
		langs[i] = fmt.Sprintf("%q", l)
	}
	langsArr := "[" + strings.Join(langs, ",") + "]"

	// canvas noise: 基于 seed 注入 1-2 像素随机偏移（仅当 CanvasNoiseSeed != 0）
	canvasPatch := ""
	if fp.CanvasNoiseSeed != 0 {
		r := rand.New(rand.NewSource(fp.CanvasNoiseSeed))
		noise1 := r.Intn(5) - 2 // -2..+2
		noise2 := r.Intn(5) - 2
		canvasPatch = fmt.Sprintf(`
	// canvas fingerprint noise (subtle)
	(function(){
		const orig = CanvasRenderingContext2D.prototype.getImageData;
		CanvasRenderingContext2D.prototype.getImageData = function(x, y, w, h) {
			const data = orig.call(this, x, y, w, h);
			if (data && data.data && data.data.length > 0) {
				data.data[0] = Math.min(255, Math.max(0, data.data[0] + %d));
				if (data.data.length > 1) {
					data.data[1] = Math.min(255, Math.max(0, data.data[1] + %d));
				}
			}
			return data;
		};
	})();
`, noise1, noise2)
	}

	return fmt.Sprintf(`
	// v2.0.8: per-session fingerprint override
	(function(){
		// hardwareConcurrency
		Object.defineProperty(navigator, 'hardwareConcurrency', { get: () => %d });
		// deviceMemory
		Object.defineProperty(navigator, 'deviceMemory', { get: () => %d });
		// languages
		Object.defineProperty(navigator, 'languages', { get: () => %s });
		// platform
		Object.defineProperty(navigator, 'platform', { get: () => %q });
		// screen
		Object.defineProperty(screen, 'width', { get: () => %d });
		Object.defineProperty(screen, 'height', { get: () => %d });
		Object.defineProperty(screen, 'availWidth', { get: () => %d });
		Object.defineProperty(screen, 'availHeight', { get: () => %d });
		// timezone via Intl
		const tzOffset = %d;
		const origResolvedOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
		Intl.DateTimeFormat.prototype.resolvedOptions = function() {
			const r = origResolvedOptions.call(this);
			r.timeZone = 'UTC' + (tzOffset >= 0 ? '+' : '-') + String(Math.abs(tzOffset)/60).padStart(2,'0') + ':' + String(Math.abs(tzOffset)%%60).padStart(2,'0');
			return r;
		};
		const origGetTimezoneOffset = Date.prototype.getTimezoneOffset;
		Date.prototype.getTimezoneOffset = function() { return -tzOffset; };
	})();
	%s
`, fp.HardwareConcurrency, fp.DeviceMemoryGB, langsArr, fp.Platform,
		fp.ViewportW, fp.ViewportH, fp.ViewportW, fp.ViewportH,
		fp.TimezoneOffsetMin, canvasPatch)
}

// ApplyFingerprint 在 chromedp context 中应用 fingerprint
// 调用顺序：emulation.SetUserAgentOverride / network.SetExtraHTTPHeaders / Emulation.setDeviceMetricsOverride / Emulation.setTimezoneOverride / stealth JS eval
// v2.0.19: 新增 userPrefix 参数，将 config.G.SpiderStealthScript 传入 BuildStealthScript，
// 解决 post-navigation buildSessionStealthJS 与 pre-navigation ApplyFingerprint 注入不一致的问题。
func ApplyFingerprint(ctx context.Context, fp *Fingerprint, bundle HeaderBundle, userPrefix string) error {
	if fp == nil {
		return nil
	}
	// 1. UA override (cdproto emulation)
	if err := chromedp.Run(ctx, emulation.SetUserAgentOverride(fp.UA)); err != nil {
		return fmt.Errorf("set user agent override: %w", err)
	}
	// 2. Extra headers（仅当非空）
	if !bundle.IsEmpty() {
		if err := chromedp.Run(ctx, network.SetExtraHTTPHeaders(bundle.ToNetworkHeaders())); err != nil {
			return fmt.Errorf("set extra headers: %w", err)
		}
	}
	// 3. Device metrics
	if err := chromedp.Run(ctx, emulation.SetDeviceMetricsOverride(
		int64(fp.ViewportW), int64(fp.ViewportH), fp.DeviceScaleFactor, false,
	)); err != nil {
		return fmt.Errorf("set device metrics: %w", err)
	}
	// 4. Timezone override（忽略错误，不致命）
	_ = chromedp.Run(ctx, emulation.SetTimezoneOverride(
		fmt.Sprintf("UTC%+d:%02d", fp.TimezoneOffsetMin/60, absInt(fp.TimezoneOffsetMin)%60),
	))
	// 5. Stealth JS（v2.0.19: 传入 userPrefix 确保自定义脚本也被注入）
	js := BuildStealthScript(fp, userPrefix)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, nil)); err != nil {
		return fmt.Errorf("evaluate stealth script: %w", err)
	}
	return nil
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ==================== 行为抖动 ====================

// JitterSleepMs 在 chromedp context 中执行区间随机 Sleep
// minMs <= 0 时 no-op（立即返回，保持 v2.0.7 行为）
// maxMs < minMs 时自动交换
func JitterSleepMs(ctx context.Context, minMs, maxMs int) error {
	if minMs <= 0 && maxMs <= 0 {
		return nil
	}
	if maxMs < minMs {
		minMs, maxMs = maxMs, minMs
	}
	range_ := maxMs - minMs
	var delay int
	if range_ <= 0 {
		delay = minMs
	} else {
		delay = minMs + rand.Intn(range_+1)
	}
	return chromedp.Run(ctx, chromedp.Sleep(time.Duration(delay)*time.Millisecond))
}

// ==================== 自适应重试 ====================

// RetryPlan 重试计划
type RetryPlan struct {
	MaxAttempts int   // 最多额外重试次数（不含首次）
	BackoffMs   []int // 每次重试前的退避（ms），长度 >= MaxAttempts+1
}

// AntiBotState 一次 handler 调用的反爬状态
type AntiBotState struct {
	mu          sync.Mutex
	Attempts    int
	LastErrType string
	LastSignals []string
}

// NewAntiBotState 构造新状态
func NewAntiBotState() *AntiBotState {
	return &AntiBotState{}
}

// Record 记录一次 attempt
func (s *AntiBotState) Record(attempts int, errType string, signals []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Attempts = attempts
	s.LastErrType = errType
	s.LastSignals = signals
}

// BuildRetryPlan 构造重试计划
// maxAttempts=0 → MaxAttempts=0（禁用，等价 v2.0.7）
// maxAttempts 上限 5
func BuildRetryPlan(maxAttempts int) RetryPlan {
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	if maxAttempts > 5 {
		maxAttempts = 5
	}
	return RetryPlan{
		MaxAttempts: maxAttempts,
		BackoffMs:   []int{0, 1500, 4000, 8000, 12000, 18000}, // 索引 i = 第 i 次重试前
	}
}

// ShouldAutoRetry 判断 error_type 是否可重试
// 仅 anti_bot / captcha 返回 true；其他（region_block / timeout / rate_limit / unknown）不重试
func ShouldAutoRetry(errType string, plan RetryPlan) bool {
	if plan.MaxAttempts <= 0 {
		return false
	}
	return errType == "anti_bot" || errType == "captcha"
}

// ==================== 集成入口 ====================

// uaForSession 决定 session 首次 navigate 使用的 UA
// 优先级：SpiderUserAgentPerSource[dsID] > SpiderUAFlipPool.Next() > 内置池（若 enable flip）> SpiderUserAgent > engine.resolveUserAgent()
func uaForSession(dsID uint64, engine *SpiderEngine) string {
	if config.G == nil {
		return ""
	}
	// 1. per-source UA（修复 v2.0.7 bug）
	if dsID > 0 {
		if ua, ok := config.G.SpiderUserAgentPerSource[int(dsID)]; ok && ua != "" {
			return ua
		}
	}
	// 2. 自定义 UA 池
	if len(config.G.SpiderUAFlipPool) > 0 {
		if ua := LoadUAPool(config.G.SpiderUAFlipPool).Next(); ua != "" {
			return ua
		}
	}
	// 3. 内置 UA 池
	if config.G.SpiderEnableUAFlip {
		if ua := LoadUAPool(nil).Next(); ua != "" {
			return ua
		}
	}
	// 4. 全局 SpiderUserAgent
	if config.G.SpiderUserAgent != "" {
		return config.G.SpiderUserAgent
	}
	// 5. 引擎默认 UA 探测链
	if engine != nil {
		return engine.resolveUserAgent()
	}
	return ""
}

// sessionSeedFromID 派生 session 种子（用 sessionID FNV 哈希）
func sessionSeedFromID(sessionID string) int64 {
	if sessionID == "" {
		return time.Now().UnixNano()
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	return int64(h.Sum64())
}

// prepareSessionNavigation 在 chromedp ctx 中准备 session 导航
// 行为：
//   - session != nil && !session.fpApplied 时构建 fingerprint 并 ApplyFingerprint
//   - 始终执行 JitterSleepMs（pre-nav 抖动）
//   - session == nil 时仅执行 JitterSleepMs（首次爬取，无 fingerprint）
func prepareSessionNavigation(ctx context.Context, session *SpiderSession, dsID uint64, engine *SpiderEngine) error {
	// 1. 决定是否构建/应用 fingerprint
	// v2.0.19: session.OverrideUA 非空时也触发（移动端降级场景）
	if session != nil && config.G != nil && (config.G.SpiderEnableUAFlip || config.G.SpiderFingerprintPerSession || len(config.G.SpiderUAFlipPool) > 0 || len(config.G.SpiderUserAgentPerSource) > 0 || session.OverrideUA != "") {
		session.fpMu.Lock()
		if !session.fpApplied {
			// v2.0.19: session.OverrideUA 优先级最高（移动端降级时临时设置）
			ua := session.OverrideUA
			if ua == "" {
				ua = uaForSession(dsID, engine)
			}
			seed := sessionSeedFromID(session.SessionID)
			fp := BuildFingerprint(ua, seed)
			bundle := BuildHeaderBundle(ua, "", "", config.G.SpiderRequestHeaders, seed)
			session.fingerprint = fp
			session.fpApplied = true
			session.fpMu.Unlock()

			// Apply 在锁外做（chromedp 操作可能阻塞）
			// v2.0.19: 传入 config.G.SpiderStealthScript 确保用户自定义 stealth 脚本被注入
			userPrefix := ""
			if config.G != nil {
				userPrefix = config.G.SpiderStealthScript
			}
			if err := ApplyFingerprint(ctx, fp, bundle, userPrefix); err != nil {
				logger.Printf("[SPIDER] ApplyFingerprint failed for session %s: %v", session.SessionID, err)
				// 不返回 error，fallback 到 base stealth
				_ = chromedp.Run(ctx, chromedp.Evaluate(buildStealthScriptBase(), nil))
			}
		} else {
			session.fpMu.Unlock()
		}
	} else if session == nil {
		// 无 session 模式（首次爬取）：仅 JitterSleepMs
	}

	// 2. Pre-nav 抖动
	if config.G != nil {
		if err := JitterSleepMs(ctx, config.G.SpiderMinNavDelayMs, config.G.SpiderMaxNavDelayMs); err != nil {
			logger.Printf("[SPIDER] JitterSleepMs failed: %v", err)
		}
	}
	return nil
}

// rerollSessionAntiBot 重roll session 的 fingerprint + UA 池游标
// 仅由 MCPSpiderWebDataHandler 重试循环调用
func rerollSessionAntiBot(session *SpiderSession, dsID uint64, engine *SpiderEngine) {
	if session == nil {
		return
	}
	session.fpMu.Lock()
	session.fpApplied = false
	session.fingerprint = nil
	session.fpMu.Unlock()
	// UA 池游标自然 advance（每次 LoadUAPool().Next() 都 advance）
	// 实际下次 prepareSessionNavigation 会调用 uaForSession，其中内置/自定义池会 Next()
	logger.Printf("[SPIDER] Re-rolled anti-bot state for session %s (next UA will advance)", session.SessionID)
}

// rotateSessionID v2.0.18 patch2：captcha 场景下主动轮换 session_id
// 设计动机：
//   - 问题分析报告_20260629_152341.md §2.2 指出"同一 session_id 复用容易出现 CDP context canceled"
//   - 该现象本质是目标站点 session 维度的 fingerprint 已被反爬系统标记（UA + 代理 + cookie 指纹聚合），
//     仅 reroll UA 池游标不能改变站点端的"会话指纹"识别
//   - 故 captcha 命中时，把 session.SessionID 后缀加上 _r<N>，让站点看到"全新会话"，
//     同时保留原 session 的 fingerprint / proxy / cookie 状态供 Agent 后续诊断复用
//
// 行为：
//   - session 为 nil 时 no-op
//   - 在原 sessionID 末尾追加 _r<attempt> 后缀（attempt 从 1 开始）
//   - 同时调用 rerollSessionAntiBot 让 UA 池游标推进
//   - 返回新 sessionID 字符串（便于调用方打日志）
func rotateSessionID(session *SpiderSession, attempt int) string {
	if session == nil {
		return ""
	}
	old := session.SessionID
	newID := fmt.Sprintf("%s_r%d", old, attempt)
	session.SessionID = newID
	// UA 池游标同步推进（与 rerollSessionAntiBot 等价）
	session.fpMu.Lock()
	session.fpApplied = false
	session.fingerprint = nil
	session.fpMu.Unlock()
	if logger.Ready() {
		logger.Printf("[SPIDER] Rotated session_id %s -> %s (after captcha/anti_bot on attempt %d)", old, newID, attempt)
	}
	return newID
}

// ==================== v2.0.18 patch2：移动端 UA 池（登录墙降级） ====================
//
// 设计动机：问题分析报告_20260629_152341.md §6.1 #4 建议"使用移动端 UA 尝试
// m.jiqizhixin.com 旧版页面"。大量国内 AI 媒体（机器之心 / 36kr / 虎嗅）在
// 桌面端部署登录墙时，会保留 m.xxx.com 移动版无登录墙访问能力。

// MobileUAPool 内置移动端 UA 池（覆盖主流 Android Chrome / iOS Safari）
var MobileUAPool = []string{
	// Android Chrome 126-128
	"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-G998B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36",
	// iOS Safari
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
	// Android WebView
	"Mozilla/5.0 (Linux; Android 13; V2207A Build/TP1A.220624.014) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/126.0.0.0 Mobile Safari/537.36",
}

// PickMobileUA 返回一个移动端 UA（轮询索引）
// idx 由调用方传入以保证多次调用均匀分布
func PickMobileUA(idx int) string {
	if len(MobileUAPool) == 0 {
		return ""
	}
	if idx < 0 {
		idx = 0
	}
	return MobileUAPool[idx%len(MobileUAPool)]
}

// MobileFallbackURL v2.0.18 patch2：根据桌面 URL 派生移动端 URL
// 规则：
//   - www.x.com / x.com → m.x.com
//   - 已含 m.x.com 时不变
//   - 含 path 子路径（/articles）时保留，仅替换 host
//
// 失败时返回空字符串（调用方走兜底）
//
// v2.0.19 补丁（基于问题分析报告_20260630_095236 §3.1）：
//   - 边界检查：即便输入格式异常（如只剩 "/" / 协议前缀后为空），也返回 ""
//     而不是派生出一个会让后续 chromedp navigate 失败的怪异 URL
//   - 防御性返回：如果去掉协议/路径后 host 为空，返回 "" 而非构造 "m.https://" 这种死循环 URL
func MobileFallbackURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u := rawURL
	var scheme = "https"
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "https://") {
		u = u[len("https://"):]
	} else if strings.HasPrefix(lower, "http://") {
		scheme = "http"
		u = u[len("http://"):]
	} else {
		return ""
	}
	// 边界：剥协议后为空（如 "https://" 单独输入）直接返回空
	if u == "" {
		return ""
	}
	// 分离 host 和 path
	host := u
	path := ""
	if idx := strings.Index(u, "/"); idx >= 0 {
		host = u[:idx]
		path = u[idx:]
	}
	hostLower := strings.ToLower(host)
	// 已经以 m. 开头则跳过
	if strings.HasPrefix(hostLower, "m.") {
		return ""
	}
	// 边界：host 为空（如 "/foo"）直接返回空，避免拼出 "m./foo" 这种死链
	if host == "" {
		return ""
	}
	// 去掉 www. 前缀再加 m.
	if strings.HasPrefix(hostLower, "www.") {
		host = host[len("www."):]
	}
	// 边界：剥 www 后为空（异常 URL）兜底
	if host == "" {
		return ""
	}
	mobileHost := "m." + host
	return scheme + "://" + mobileHost + path
}

// ==================== v2.0.9 扩展 ====================

// ----- ProxyPool 健康跟踪（维度四） -----

// RecordFailure 记录某个代理的失败；连续 threshold 次失败后标记为 Dead
// proxyURL 为空时 no-op（无代理场景不参与）
func (p *ProxyPool) RecordFailure(proxyURL, errType string) {
	if p == nil || proxyURL == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].URL == proxyURL {
			p.items[i].Health.ConsecutiveFails++
			p.items[i].Health.LastFailType = errType
			p.items[i].Health.LastFailAt = time.Now()
			// 阈值取自 config.G；缺省 3
			threshold := 3
			if config.G != nil && config.G.SpiderProxyDeadThreshold > 0 {
				threshold = config.G.SpiderProxyDeadThreshold
			}
			if !p.items[i].Health.Dead && p.items[i].Health.ConsecutiveFails >= threshold {
				p.items[i].Health.Dead = true
				p.items[i].Health.DeadSince = time.Now()
				logger.Printf("[SPIDER] proxy marked DEAD: %s (fails=%d, lastType=%s)", proxyURL, p.items[i].Health.ConsecutiveFails, errType)
			}
			return
		}
	}
}

// RecordSuccess 重置某个代理的连续失败计数（成功后调用）
func (p *ProxyPool) RecordSuccess(proxyURL string) {
	if p == nil || proxyURL == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].URL == proxyURL {
			if p.items[i].Health.ConsecutiveFails > 0 {
				p.items[i].Health.ConsecutiveFails = 0
			}
			return
		}
	}
}

// ResurrectDeadProxies 复活超过 afterSec 的死亡代理
func (p *ProxyPool) ResurrectDeadProxies(afterSec int) int {
	if p == nil {
		return 0
	}
	if afterSec <= 0 {
		afterSec = 300
	}
	now := time.Now()
	count := 0
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].Health.Dead && !p.items[i].Health.DeadSince.IsZero() && now.Sub(p.items[i].Health.DeadSince) >= time.Duration(afterSec)*time.Second {
			p.items[i].Health.Dead = false
			p.items[i].Health.ConsecutiveFails = 0
			p.items[i].Health.DeadSince = time.Time{}
			count++
		}
	}
	return count
}

// HealthSnapshot 返回所有代理的健康快照（测试 / 调试用）
func (p *ProxyPool) HealthSnapshot() map[string]ProxyHealth {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]ProxyHealth, len(p.items))
	for _, it := range p.items {
		out[it.URL] = it.Health
	}
	return out
}

// startResurrector 启动后台 goroutine，每 30s 调用一次 ResurrectDeadProxies
// afterSec：复活冷却（最小 60s）
func (p *ProxyPool) startResurrector(afterSec int) {
	if p == nil || len(p.items) == 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			n := p.ResurrectDeadProxies(afterSec)
			if n > 0 {
				logger.Printf("[SPIDER] resurrected %d dead proxies (after %ds cooldown)", n, afterSec)
			}
		}
	}()
}

// ----- Stealth Pro（维度一） -----

// defaultStealthProFonts 内置常见系统字体（Win/Mac/Linux 各 ~10 个）
// 不依赖真实系统环境，用于屏蔽字体指纹探测
var defaultStealthProFonts = []string{
	// Windows
	"Arial", "Arial Black", "Calibri", "Cambria", "Consolas", "Courier New", "Georgia",
	"Impact", "Lucida Console", "Microsoft Sans Serif", "Segoe UI", "Tahoma", "Times New Roman", "Verdana",
	// macOS
	"American Typewriter", "Avenir", "Avenir Next", "Didot", "Futura", "Geneva", "Helvetica",
	"Helvetica Neue", "Lucida Grande", "Menlo", "Monaco", "Optima", "Palatino", "Snell Roundhand",
	// Linux / 跨平台
	"DejaVu Sans", "DejaVu Serif", "Liberation Sans", "Liberation Mono", "Ubuntu", "Noto Sans",
}

// buildStealthProJS 构造 Stealth Pro JS 注入（v2.0.9 维度一）
// 在 buildStealthScriptBase() 之后追加：MediaDevices 模拟、字体列表、错误堆栈净化、chrome.runtime 强化
// fp 当前未使用（保留以便将来基于 fingerprint 进一步定制）；customFonts 为空时使用内置默认
func buildStealthProJS(fp *Fingerprint, customFonts []string) string {
	_ = fp // 保留参数便于未来扩展
	fonts := customFonts
	if len(fonts) == 0 {
		fonts = defaultStealthProFonts
	}
	// 构造 JS 数组字面量
	fontsArr := "["
	for i, f := range fonts {
		if i > 0 {
			fontsArr += ","
		}
		fontsArr += fmt.Sprintf("%q", f)
	}
	fontsArr += "]"

	return `(function(){
	// ===== MediaDevices 模拟 =====
	try {
		if (navigator.mediaDevices && navigator.mediaDevices.enumerateDevices) {
			const fakeDevices = [
				{ kind: 'audioinput',  label: 'Built-in Microphone',  deviceId: 'default' },
				{ kind: 'audiooutput', label: 'Built-in Speakers',    deviceId: 'communications' },
				{ kind: 'videoinput',  label: 'Built-in FaceTime HD Camera', deviceId: 'camera1' }
			];
			navigator.mediaDevices.enumerateDevices = function() {
				return Promise.resolve(fakeDevices);
			};
		}
	} catch(e) {}

	// ===== 字体列表 =====
	try {
		const stealthFonts = ` + fontsArr + `;
		// 监听 document.fonts.check（探测请求）
		if (document.fonts && document.fonts.check) {
			const origCheck = document.fonts.check.bind(document.fonts);
			document.fonts.check = function(font, text) {
				try {
					const family = (font || '').toLowerCase();
					for (let i = 0; i < stealthFonts.length; i++) {
						if (family.indexOf(stealthFonts[i].toLowerCase()) !== -1) return true;
					}
				} catch(e) {}
				return origCheck.apply(this, arguments);
			};
		}
	} catch(e) {}

	// ===== 错误堆栈净化（剔除 puppeteer / cdp / chromedp 痕迹）=====
	try {
		const origToString = Error.prototype.toString;
		Error.prototype.toString = function() {
			try {
				const s = origToString.call(this);
				return s.replace(/\/(puppeteer|cdp|chromedp)[^\s)]*/gi, '/[REDACTED]');
			} catch(e) {
				return origToString.call(this);
			}
		};
	} catch(e) {}

	// ===== chrome.runtime 强化 =====
	try {
		if (window.chrome && window.chrome.runtime) {
			if (typeof window.chrome.runtime.onMessage !== 'object') {
				window.chrome.runtime.onMessage = { addListener: function(){}, removeListener: function(){} };
			}
			if (typeof window.chrome.runtime.sendMessage !== 'function') {
				window.chrome.runtime.sendMessage = function() {};
			}
			if (typeof window.chrome.runtime.connect !== 'function') {
				window.chrome.runtime.connect = function() { return { onMessage: { addListener: function(){} }, postMessage: function() {}, disconnect: function() {} }; };
			}
		}
	} catch(e) {}
})();`
}

// ----- Human-like Behavior（维度二） -----

// Point 二维点（用于贝塞尔曲线）
type Point struct {
	X, Y float64
}

// GaussianDelayMs 产生均值 meanMs 标准差 sigmaMs 的非负高斯延迟（ms）
// 用 Box-Muller；max = mean+3σ，min = 0
// mean<=0 时返回 0（no-op 兼容 v2.0.7）
func GaussianDelayMs(meanMs, sigmaMs int) int {
	if meanMs <= 0 {
		return 0
	}
	if sigmaMs <= 0 {
		sigmaMs = meanMs / 2
		if sigmaMs <= 0 {
			sigmaMs = 1
		}
	}
	// Box-Muller
	u1 := rand.Float64()
	u2 := rand.Float64()
	if u1 < 1e-9 {
		u1 = 1e-9
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	d := meanMs + int(float64(sigmaMs)*z)
	if d < 0 {
		d = 0
	}
	if d > meanMs+3*sigmaMs {
		d = meanMs + 3*sigmaMs
	}
	return d
}

// BezierPathPoints 生成从 (sx,sy) 到 (ex,ey) 的二阶贝塞尔曲线采样点
// 控制点加随机扰动；steps = 采样点数（含起点，不含终点之外的额外点）
// microJitter=true 时每步附加 ±2px 抖动
func BezierPathPoints(sx, sy, ex, ey float64, steps int, microJitter bool) []Point {
	if steps <= 0 {
		steps = 12
	}
	// 控制点：在起点和终点之间偏移（制造弧度）
	midX := (sx + ex) / 2
	midY := (sy + ey) / 2
	dx := ex - sx
	dy := ey - sy
	// 旋转 90 度 + 随机长度
	perpX := -dy
	perpY := dx
	length := math.Sqrt(perpX*perpX + perpY*perpY)
	if length > 0 {
		perpX /= length
		perpY /= length
	}
	jitterMag := math.Min(math.Sqrt(dx*dx+dy*dy)*0.3, 60)
	cx := midX + perpX*jitterMag*(rand.Float64()*0.6+0.4)
	cy := midY + perpY*jitterMag*(rand.Float64()*0.6+0.4)

	out := make([]Point, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		// 二阶贝塞尔 B(t) = (1-t)²P0 + 2(1-t)t·P1 + t²P2
		inv := 1 - t
		x := inv*inv*sx + 2*inv*t*cx + t*t*ex
		y := inv*inv*sy + 2*inv*t*cy + t*t*ey
		if microJitter && i > 0 && i < steps {
			x += float64(rand.Intn(5) - 2) // -2..+2
			y += float64(rand.Intn(5) - 2)
		}
		out[i] = Point{X: x, Y: y}
	}
	return out
}

// SmoothMouseMove 通过连续 Input.dispatchMouseEvent 沿贝塞尔路径移动鼠标
// jitterMs：每步间隔（默认 8ms；0=无延迟）
func SmoothMouseMove(ctx context.Context, sx, sy, ex, ey float64, jitterMs int) error {
	if jitterMs <= 0 {
		jitterMs = 8
	}
	points := BezierPathPoints(sx, sy, ex, ey, 12, true)
	for _, p := range points {
		if err := chromedp.Run(ctx, chromedp.MouseEvent("mouseMoved", p.X, p.Y)); err != nil {
			return err
		}
		if jitterMs > 0 {
			if err := chromedp.Run(ctx, chromedp.Sleep(time.Duration(jitterMs)*time.Millisecond)); err != nil {
				return err
			}
		}
	}
	return nil
}

// MicroMouseMovements 在 (tx,ty) 之前的视口内随机微动 1-2 次（贝塞尔短距离）
// 用于模拟人类"手滑"或"浏览"行为；点均不等于目标
// vw<=0 或 vh<=0 时 no-op
func MicroMouseMovements(ctx context.Context, vw, vh, tx, ty int) error {
	if vw <= 20 || vh <= 20 {
		return nil
	}
	count := 1 + rand.Intn(2) // 1-2 次
	for i := 0; i < count; i++ {
		// 在视口内选一个不等于目标的随机点
		var rx, ry float64
		for tries := 0; tries < 5; tries++ {
			rx = float64(20 + rand.Intn(vw-40))
			ry = float64(20 + rand.Intn(vh-40))
			if int(rx) != tx && int(ry) != ty {
				break
			}
		}
		// 从视口中心开始短距离贝塞尔移动
		if err := SmoothMouseMove(ctx, float64(vw)/2, float64(vh)/2, rx, ry, 4); err != nil {
			return err
		}
	}
	return nil
}

// ThinkingSleep 在 chromedp ctx 中执行高斯分布思考延迟
// mean<=0 时 no-op；sigma<=0 默认为 mean/2
func ThinkingSleep(ctx context.Context, meanMs, sigmaMs int) error {
	if meanMs <= 0 {
		return nil
	}
	delay := GaussianDelayMs(meanMs, sigmaMs)
	if delay <= 0 {
		return nil
	}
	return chromedp.Run(ctx, chromedp.Sleep(time.Duration(delay)*time.Millisecond))
}

// ReadingStyleScrollVar 计算阅读式滚动距离（视口高度的 0.6-1.2 倍 + ±15% 抖动）
// vh<=0 时返回 0
func ReadingStyleScrollVar(viewportH int, rng *rand.Rand) int {
	if viewportH <= 0 {
		return 0
	}
	r := rng
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	base := float64(viewportH) * (0.6 + r.Float64()*0.6) // 0.6..1.2
	jitter := 1.0 + (r.Float64()-0.5)*0.3                // ±15%
	return int(base * jitter)
}
