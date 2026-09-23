package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dchest/captcha"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lishimeng/LsmTokensServer/websocket"
)

// ========== 常量定义 ==========

const (
	userTokenExpireDuration = time.Hour * 24 * 1
	userLoginCookieName     = "lsm_user_token"
	maxLoginFailures        = 3
	loginLockDuration       = 10 * time.Minute
	loginFailureWindow      = time.Minute
	// 阶段AP：loginAttempts 惰性清理阈值，超过后在下一次失败记录时清理全部过期条目
	loginAttemptsCleanupThreshold = 1024
	// v2.0.78 安全加固：渐进式锁定封顶时长与登录失败恒定延迟
	maxLockDuration       = 60 * time.Minute
	loginFailureDelayBase = 200 * time.Millisecond
	// captcha_token 有效期（与验证码自身 10 分钟过期一致，留少许余量）
	captchaTokenLifetime  = 12 * time.Minute
)

// ========== JWT 密钥管理（v2.0.56 安全加固 + 持久化兜底） ==========

const jwtSecretFile = ".jwt_secret" // 已 gitignore，仅作配置缺失时的持久化兜底

var (
	runtimeJWTSecret  []byte // 配置未提供 jwtSecret 时持久化/随机生成
	runtimeJWTSecretO sync.Once
)

// getJWTSecretFilePath 返回 .jwt_secret 文件路径（与配置文件同目录）
func getJWTSecretFilePath() string {
	// 与 LsmTokensServer.conf 同目录（进程工作目录）
	return jwtSecretFile
}

// persistJWTSecret 将密钥持久化到文件（权限 600）
func persistJWTSecret(secret []byte) error {
	path := getJWTSecretFilePath()
	encoded := base64.StdEncoding.EncodeToString(secret)
	// 0600 权限：仅所有者可读写
	if err := os.WriteFile(path, []byte(encoded), 0600); err != nil {
		return fmt.Errorf("写入 JWT 密钥文件失败: %w", err)
	}
	// 确保文件权限（umask 可能影响初始创建）
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("设置 JWT 密钥文件权限失败: %w", err)
	}
	return nil
}

// loadPersistedJWTSecret 尝试从文件读取持久化的密钥
func loadPersistedJWTSecret() ([]byte, error) {
	path := getJWTSecretFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	encoded := strings.TrimSpace(string(data))
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("JWT 密钥文件内容无效: %w", err)
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT 密钥长度不足: %d 字节", len(secret))
	}
	return secret, nil
}

// getJWTSecret 获取 JWT 签名密钥：
// 1. 优先 conf 的 security.jwtSecret
// 2. 其次 .jwt_secret 持久化文件（服务重启后恢复原有密钥）
// 3. 最后 crypto/rand 生成 + 持久化（首次启动）
func getJWTSecret() []byte {
	if config.G != nil && config.G.Security.JWTSecret != "" {
		return []byte(config.G.Security.JWTSecret)
	}
	runtimeJWTSecretO.Do(func() {
		// 尝试加载持久化密钥
		if secret, err := loadPersistedJWTSecret(); err == nil {
			runtimeJWTSecret = secret
			logger.Printf("[SECURITY] 已从 %s 恢复 JWT 密钥（服务重启后登录态保持有效）", filepath.Base(jwtSecretFile))
			return
		} else if !os.IsNotExist(err) {
			logger.Printf("[SECURITY] 读取 JWT 密钥文件失败(%v)，将生成新密钥", err)
		}

		// 生成新密钥
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			// crypto/rand 失败属系统级异常，直接 panic（禁止降级为可预测密钥）
			panic("生成随机 JWT 密钥失败: " + err.Error())
		}
		runtimeJWTSecret = buf

		// 持久化到文件
		if err := persistJWTSecret(buf); err != nil {
			logger.Printf("[SECURITY] 警告: JWT 密钥持久化失败(%v)，重启后登录态将失效", err)
		} else {
			logger.Printf("[SECURITY] security.jwtSecret 未配置，已生成随机 JWT 密钥并持久化到 %s（重启后登录态保持有效）", filepath.Base(jwtSecretFile))
		}
	})
	return runtimeJWTSecret
}

// ========== JWT Claims ==========

// UserTokenClaims 用户登录 Token 声明
type UserTokenClaims struct {
	UserID    uint64 `json:"user_id"`
	UserName  string `json:"user_name"`
	ModelName string `json:"model_name,omitempty"` // 模型登录时填充
	LoginType string `json:"login_type"`           // "model" 或 "user"
	jwt.RegisteredClaims
}

// Valid 自定义校验
func (u *UserTokenClaims) Valid() error {
	now := time.Now()
	if u.ExpiresAt != nil && u.ExpiresAt.Time.Before(now) {
		return fmt.Errorf("token 已过期")
	}
	if u.UserID == 0 {
		return fmt.Errorf("用户 ID 无效")
	}
	if u.UserName == "" {
		return fmt.Errorf("用户名不能为空")
	}
	return nil
}

// ========== 请求/响应类型 ==========

type userLoginReq struct {
	LoginType    string `json:"login_type"`     // "model" 或 "user"
	ModelName    string `json:"model_name"`     // 模型登录用
	APIKey       string `json:"api_key"`        // 模型登录用
	UserName     string `json:"user_name"`      // 用户登录用
	Password     string `json:"password"`       // 用户登录用
	Phone        string `json:"phone"`          // 用户登录用
	CaptchaID    string `json:"captcha_id"`     // 验证码 ID
	CaptchaCode  string `json:"captcha_code"`   // 验证码输入
	CaptchaToken string `json:"captcha_token"`  // v2.0.78：验证码客户端绑定 token（增强式，可为空）
}

type userLoginResp struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ========== 暴力破解防护 ==========

type loginAttempt struct {
	failedCount    int
	lastFailedTime time.Time
	lockedUntil    time.Time
	// v2.0.78 安全加固：连续触发锁定次数（渐进式锁定升级用）
	lockCount int
}

var (
	loginAttempts   = make(map[string]*loginAttempt)
	loginAttemptsMu sync.Mutex
)

// getClientIP 获取客户端 IP
// GetClientIP 导出（webserver 安全中间件跨包使用）
func GetClientIP(r *http.Request) string {
	return getClientIP(r)
}

func getClientIP(r *http.Request) string {
	// v2.0.56 安全加固：仅显式配置信任反向代理时才读取 X-Forwarded-For / X-Real-IP，
	// 否则一律使用 TCP 对端地址，防止伪造请求头绕过防爆破锁定与限速。
	if config.G != nil && config.G.Security.TrustProxyHeaders {
		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.Header.Get("X-Real-IP")
		}
		if ip != "" {
			return strings.TrimSpace(strings.Split(ip, ",")[0])
		}
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}

// checkLoginAttempt 检查是否被锁定
func checkLoginAttempt(ip string) error {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	attempt, exists := loginAttempts[ip]
	if !exists {
		return nil
	}

	now := time.Now()
	// 如果已过锁定时间，重置
	if attempt.lockedUntil.After(now) {
		remainingMin := int(attempt.lockedUntil.Sub(now).Minutes()) + 1
		return fmt.Errorf("登录过于频繁，请 %d 分钟后再试", remainingMin)
	}
	// 如果超过 1 分钟窗口，重置失败次数
	if now.Sub(attempt.lastFailedTime) > loginFailureWindow {
		attempt.failedCount = 0
	}

	return nil
}

// recordLoginFailure 记录登录失败
func recordLoginFailure(key string) {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	// 惰性清理：攻击者轮换 IP/账号名可制造大量一次性 key，map 超过阈值时
	// 清掉已过锁定且超出失败窗口的条目，防止长期运行内存缓慢增长。
	if len(loginAttempts) > loginAttemptsCleanupThreshold {
		now := time.Now()
		for k, a := range loginAttempts {
			if a.lockedUntil.Before(now) && now.Sub(a.lastFailedTime) > loginFailureWindow {
				delete(loginAttempts, k)
			}
		}
	}

	attempt, exists := loginAttempts[key]
	if !exists {
		attempt = &loginAttempt{}
		loginAttempts[key] = attempt
	}

	now := time.Now()
	// 超过窗口期重置
	if now.Sub(attempt.lastFailedTime) > loginFailureWindow {
		attempt.failedCount = 0
	}

	attempt.failedCount++
	attempt.lastFailedTime = now

	if attempt.failedCount >= maxLoginFailures {
		// v2.0.78 安全加固：渐进式锁定升级——连续被锁定次数越多，封禁越久。
		// 锁定时长 = min(base * 2^(lockCount-1), maxLockDuration)
		//   1 次触发 → 10min，2 次 → 20min，3 次 → 40min，4 次及以上 → 60min（封顶）
		attempt.lockCount++
		d := loginLockDuration
		for i := 1; i < attempt.lockCount && d < maxLockDuration; i++ {
			d *= 2
		}
		if d > maxLockDuration {
			d = maxLockDuration
		}
		attempt.lockedUntil = now.Add(d)
		// 重置失败计数（下一次窗口重新累计）
		attempt.failedCount = 0
	}
}

// clearLoginAttempt 清除登录失败记录（成功时调用），同时重置渐进式锁定计数
func clearLoginAttempt(ip string) {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	delete(loginAttempts, ip)
}

// ========== 验证码客户端绑定 Token（v2.0.78 安全加固） ==========

// clientFingerprint 计算客户端指纹（IP + User-Agent 的 SHA256 前 16 字节 hex）
// 用于绑定验证码到生成它的客户端，防止验证码 ID 被跨客户端/打码平台重用。
func clientFingerprint(r *http.Request) string {
	ip := getClientIP(r)
	ua := r.Header.Get("User-Agent")
	h := sha256.Sum256([]byte(ip + "|" + ua))
	return hex.EncodeToString(h[:16])
}

// generateCaptchaToken 生成验证码客户端绑定 token
// token = HMAC-SHA256(captcha_id + "|" + fingerprint + "|" + timestamp, jwtSecret) 前 16 字节 hex
// 服务端无状态校验：重新计算期望 token 做常量时间比较，无需额外存储。
func generateCaptchaToken(captchaID string, r *http.Request) string {
	fp := clientFingerprint(r)
	// 时间戳按 token 生命周期取整，允许客户端时钟少许偏差
	ts := time.Now().Unix() / int64(captchaTokenLifetime.Seconds())
	mac := hmac.New(sha256.New, getJWTSecret())
	mac.Write([]byte(fmt.Sprintf("%s|%s|%d", captchaID, fp, ts)))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// verifyCaptchaToken 校验验证码客户端绑定 token（常量时间比较）
// 返回 true 表示 token 有效。增强式策略：token 为空时返回 true（兼容旧客户端）。
// token 为 16 字节原始值的 hex 编码（32 个 hex 字符）。
func verifyCaptchaToken(captchaID, token string, r *http.Request) bool {
	if token == "" {
		// 增强式策略：不发送 token 的旧客户端 / API 调用仍兼容
		return true
	}
	if len(token) != 32 {
		return false
	}
	expected := generateCaptchaToken(captchaID, r)
	// 常量时间比较，避免时序侧信道
	return hmac.Equal([]byte(token), []byte(expected))
}

// loginFailureDelay 登录失败恒定延迟（消除时序侧信道，增加暴力破解时间成本）
// 基准 200ms + 微随机(0-50ms)，使用 crypto/rand 派生的轻量随机。
func loginFailureDelay() {
	// 微随机抖动：读取 1 字节，取模 50
	b := make([]byte, 1)
	n, _ := rand.Read(b)
	jitter := time.Duration(0)
	if n == 1 {
		jitter = time.Duration(b[0]%50) * time.Millisecond
	}
	time.Sleep(loginFailureDelayBase + jitter)
}

// loginAttemptAccountKey 计算账号维度防爆破 key（用户名或模型名）；未知登录类型返回空串（不参与账号维度锁定）
// 阶段AP（OBS-4）：trustProxyHeaders=true 时攻击者可轮换 X-Forwarded-For 绕过 IP 锁定，
// 账号维度计数与 IP 维度互补——IP 锁定防「喷洒」（同 IP 试多账号），账号锁定防「撞库」（换 IP 试同账号）。
// DoS 权衡：攻击者可借机锁定他人账号 10 分钟；因登录前置验证码（每次尝试均需打码），
// 恶意锁定成本远高于收益，且 IP 维度本就存在同类锁定，语义可接受。
func loginAttemptAccountKey(loginType, userName, modelName string) string {
	switch loginType {
	case "user":
		if userName == "" {
			return ""
		}
		return "user:" + userName
	case "model":
		if modelName == "" {
			return ""
		}
		return "model:" + modelName
	default:
		return ""
	}
}

// ========== 验证码接口 ==========

// captchaGenerateHandle 生成验证码
// v2.0.78 安全加固：响应中新增 captcha_token（客户端绑定 token），前端提交登录时需携带，
// 后端校验该 token 是否由同一客户端（IP+UA）生成，防止验证码 ID 被跨客户端/打码平台重用。
// 旧客户端不携带 captcha_token 时后端跳过校验，保持兼容。
func captchaGenerateHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	captchaID := captcha.NewLen(4)
	var buf strings.Builder
	// 阶段BQ：验证码图片 120x40 → 160x50，数字更大更清晰，降低肉眼误识别率
	err := captcha.WriteImage(&buf, captchaID, 160, 50)
	if err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "生成验证码失败"})
		return
	}

	imageBase64 := base64.StdEncoding.EncodeToString([]byte(buf.String()))
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"captcha_id":    captchaID,
		"captcha_token": generateCaptchaToken(captchaID, r),
		"image_url":     "data:image/png;base64," + imageBase64,
	})
}

// ========== 登录接口 ==========

// userLoginInterfaceHandle 用户登录 API
func userLoginInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "仅支持 POST 请求"})
		return
	}

	var req userLoginReq
	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		// 表单提交（支持 Chrome 密码管理器自动填充）
		if err := r.ParseForm(); err != nil {
			json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "表单解析失败: " + err.Error()})
			return
		}
		req.LoginType = r.PostFormValue("login_type")
		req.ModelName = r.PostFormValue("model_name")
		req.APIKey = r.PostFormValue("api_key")
		req.UserName = r.PostFormValue("user_name")
		req.Password = r.PostFormValue("password")
		req.Phone = r.PostFormValue("phone")
		req.CaptchaID = r.PostFormValue("captcha_id")
		req.CaptchaCode = r.PostFormValue("captcha_code")
		req.CaptchaToken = r.PostFormValue("captcha_token")
	} else {
		// JSON 提交（API 调用）
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if err.Error() == "http: request body too large" {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "请求体超过大小限制"})
				return
			}
			json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "请求解析失败: " + err.Error()})
			return
		}
	}

	clientIP := getClientIP(r)
	accountKey := loginAttemptAccountKey(req.LoginType, req.UserName, req.ModelName)

	// 检查是否被锁定（IP 维度 + 账号维度，任一命中即拒绝）
	if err := checkLoginAttempt(clientIP); err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: err.Error()})
		return
	}
	if accountKey != "" {
		if err := checkLoginAttempt(accountKey); err != nil {
			json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: err.Error()})
			return
		}
	}

	// 校验验证码
	if req.CaptchaID == "" || req.CaptchaCode == "" {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "请输入验证码"})
		return
	}
	// v2.0.78 安全加固：校验验证码客户端绑定 token（增强式——仅当请求携带时校验）
	if !verifyCaptchaToken(req.CaptchaID, req.CaptchaToken, r) {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "验证码已失效，请刷新重试"})
		return
	}
	if !captcha.VerifyString(req.CaptchaID, req.CaptchaCode) {
		// 阶段BQ：验证码错误不再计入防爆破锁定——验证码本身就是防机器打码手段，
		// 输错验证码多为真人肉眼辨认失误（图片扭曲），计入失败会把正常用户锁死
		// （3 次锁 10 分钟），陷入"看错→锁定→再看错"死循环。仅凭据错误才计数。
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "验证码错误或已过期"})
		return
	}

	// 根据登录类型处理
	var user *modelsdb.TAgentHttpUserInfo
	var err error

	switch req.LoginType {
	case "model":
		user, err = doModelLogin(req)
	case "user":
		user, err = doUserLogin(req)
	default:
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "未知的登录类型"})
		return
	}

	isFormSubmit := strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")

	if err != nil {
		// 凭据错误：IP 与账号双维度记录（验证码已通过，属真实撞库尝试）
		recordLoginFailure(clientIP)
		if accountKey != "" {
			recordLoginFailure(accountKey)
		}
		// v2.0.78 安全加固：恒定延迟消除时序侧信道，同时增加暴力破解时间成本
		loginFailureDelay()
		if isFormSubmit {
			http.Redirect(w, r, "UserLogin?error="+url.QueryEscape(err.Error()), http.StatusFound)
		} else {
			json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: err.Error()})
		}
		return
	}

	// 登录成功，生成 Token
	tokenStr, err := generateUserToken(user, req.LoginType, req.ModelName)
	if err != nil {
		if isFormSubmit {
			http.Redirect(w, r, "UserLogin?error="+url.QueryEscape("生成登录凭证失败"), http.StatusFound)
		} else {
			json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "生成登录凭证失败: " + err.Error()})
		}
		return
	}

	// 设置 Cookie（v2.0.56：HTTPS 部署时自动带 Secure 标志）
	cookie := &http.Cookie{
		Name:     userLoginCookieName,
		Value:    tokenStr,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(userTokenExpireDuration.Seconds()),
	}
	http.SetCookie(w, cookie)

	clearLoginAttempt(clientIP)
	if accountKey != "" {
		clearLoginAttempt(accountKey)
	}

	// 记录登录日志
	loginTypeStr := "用户登录"
	if req.LoginType == "model" {
		loginTypeStr = fmt.Sprintf("模型登录(%s)", req.ModelName)
	}
	logger.LogUserAction("LOGIN", user.UserName, fmt.Sprintf("登录方式=%s IP=%s", loginTypeStr, clientIP))

	if isFormSubmit {
		http.Redirect(w, r, "./", http.StatusFound)
	} else {
		json.NewEncoder(w).Encode(userLoginResp{
			Success: true,
			Message: "登录成功",
			Data: map[string]interface{}{
				"user_id":    user.ID,
				"user_name":  user.UserName,
				"login_type": req.LoginType,
			},
		})
	}
}

// doModelLogin 模型登录验证
// 阶段AO：模型登录错误信息统一为"模型名称或 API Key 错误"，消除"模型名称不匹配 / API Key 无效 /
// 模型已被禁用"等细分提示带来的枚举探测（攻击者可借此识别哪些模型名存在但被禁用）。
// 安全语义对齐 doUserLogin（用户名/密码错误合并为统一提示）。
func doModelLogin(req userLoginReq) (*modelsdb.TAgentHttpUserInfo, error) {
	if req.ModelName == "" || req.APIKey == "" {
		return nil, fmt.Errorf("模型名称和 API Key 不能为空")
	}

	// 通过 API Key 查找模型（复用缓存+数据库查询）
	model, err := modelsdb.GetUserModelByAPIKey(req.APIKey)
	if err != nil {
		return nil, fmt.Errorf("模型名称或 API Key 错误")
	}

	// 校验模型名称是否匹配
	if model.ModelName != strings.TrimSpace(req.ModelName) {
		return nil, fmt.Errorf("模型名称或 API Key 错误")
	}

	// 检查模型是否被禁用（合并为统一提示，避免枚举）
	if model.Status == modelsdb.UserModelStatus_Disabled {
		return nil, fmt.Errorf("模型名称或 API Key 错误")
	}

	// 通过 UserID 获取用户信息（优先缓存）
	user, ok := modelsdb.GetCachedUserByID(model.UserID)
	if !ok {
		// 缓存未命中，从数据库查询
		user, err = modelsdb.GetUserByID(model.UserID)
		if err != nil {
			return nil, fmt.Errorf("模型名称或 API Key 错误")
		}
	}

	// 检查用户状态（用户被禁用也合并为同一提示，避免通过用户端登录探测用户存在性）
	if user.Status == modelsdb.UserStatus_Disabled {
		return nil, fmt.Errorf("模型名称或 API Key 错误")
	}

	return user, nil
}

// doUserLogin 用户登录验证
func doUserLogin(req userLoginReq) (*modelsdb.TAgentHttpUserInfo, error) {
	// v2.0.56：用户名与密码必填，手机号改选填（管理员可能清空 phone）；
	// 统一对外仍返回"用户名、密码或手机号错误"，避免枚举探测 phone 字段状态。
	if req.UserName == "" || req.Password == "" {
		return nil, fmt.Errorf("用户名和密码不能为空")
	}

	// 通过用户名获取用户（优先缓存）
	user, ok := modelsdb.GetCachedUserByName(strings.TrimSpace(req.UserName))
	if !ok {
		// 缓存未命中，从数据库查询
		var err error
		user, err = modelsdb.GetUserByName(strings.TrimSpace(req.UserName))
		if err != nil {
			return nil, fmt.Errorf("用户名或密码错误")
		}
	}

	// 校验密码（v2.0.56：bcrypt 哈希校验，兼容旧明文并自动升级）
	pwOK, isLegacy := VerifyPassword(user.Password, strings.TrimSpace(req.Password))
	if !pwOK {
		return nil, fmt.Errorf("用户名、密码或手机号错误")
	}

	// 校验手机号（错误信息与密码一致，避免枚举探测）：
	//   - 当 DB 中 phone 为空时，req.Phone 也必须为空才允许通过；
	//   - 否则按严格等值校验。
	if subtleConstantTimeEq(user.Phone, strings.TrimSpace(req.Phone)) != true {
		return nil, fmt.Errorf("用户名、密码或手机号错误")
	}

	// 旧明文密码命中：自动升级为 bcrypt 哈希后落库（平滑迁移，无感）
	if isLegacy {
		if hashed, err := HashPassword(strings.TrimSpace(req.Password)); err == nil {
			_ = modelsdb.UpdateUserPasswordHashed(user.ID, hashed)
			user.Password = hashed
			// 刷新用户缓存，避免缓存中残留明文
			if refreshed, err := modelsdb.GetUserByID(user.ID); err == nil {
				modelsdb.AddUserToCache(refreshed)
			}
		} else {
			logger.Printf("[SECURITY] Warning: 用户 %s 密码哈希升级失败: %v", user.UserName, err)
		}
	}

	// 检查用户状态
	if user.Status == modelsdb.UserStatus_Disabled {
		return nil, fmt.Errorf("用户已被禁用")
	}

	return user, nil
}

// ========== JWT Token 管理 ==========

// generateUserToken 生成用户 JWT Token
func generateUserToken(user *modelsdb.TAgentHttpUserInfo, loginType, modelName string) (string, error) {
	now := time.Now()
	claims := &UserTokenClaims{
		UserID:    user.ID,
		UserName:  user.UserName,
		ModelName: modelName,
		LoginType: loginType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(userTokenExpireDuration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getJWTSecret())
}

// getUserToken 从请求中解析用户 Token
func getUserToken(r *http.Request) *UserTokenClaims {
	claims := &UserTokenClaims{}
	tokenStr := ""

	// 1. 优先从 Cookie 获取
	cookie, err := r.Cookie(userLoginCookieName)
	if err == nil {
		tokenStr = cookie.Value
	}

	// 2.  fallback 到 Authorization header
	if tokenStr == "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				tokenStr = parts[1]
			}
		}
	}

	// 3. 解析并验证 JWT
	if tokenStr != "" {
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("不支持的签名算法: %v", token.Header["alg"])
			}
			return getJWTSecret(), nil
		})
		if err != nil || !token.Valid {
			return &UserTokenClaims{}
		}
	}

	return claims
}

// clearUserLoginCookie 清除用户登录 Cookie
func clearUserLoginCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     userLoginCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		MaxAge:   -1, // 立即删除
	})
}

// checkUserAndModelStatus 检查用户和模型状态是否有效
// 返回: isValid, shouldRedirect
func checkUserAndModelStatus(claims *UserTokenClaims) (bool, bool) {
	if claims.UserID == 0 {
		return false, true
	}

	// 检查用户是否存在且状态正常
	user, ok := modelsdb.GetCachedUserByID(claims.UserID)
	if !ok {
		// 缓存未命中，尝试从数据库加载
		var err error
		user, err = modelsdb.GetUserByID(claims.UserID)
		if err != nil {
			// 用户不存在
			return false, true
		}
		// 加载到缓存
		modelsdb.AddUserToCache(user)
	}

	// 检查用户状态
	if user.Status == modelsdb.UserStatus_Disabled {
		return false, true
	}

	// 如果是模型登录，检查模型状态
	if claims.LoginType == "model" && claims.ModelName != "" {
		model, ok := modelsdb.GetCachedModelByUserAndModelName(user.UserName, claims.ModelName)
		if !ok {
			// 缓存未命中，尝试从数据库加载
			var err error
			model, err = modelsdb.GetUserModelByUserIDAndModelName(claims.UserID, claims.ModelName)
			if err != nil {
				// 模型不存在
				return false, true
			}
			// 加载到缓存
			modelsdb.AddModelToCache(model)
		}

		// 检查模型状态
		if model.Status == modelsdb.UserModelStatus_Disabled {
			return false, true
		}
	}

	return true, false
}

// isUserAPIPath 判定用户端数据接口路径（与管理端 isManagerAPIPath 同规则）
// *Interface / *WS 后缀、验证码、协议转换分析器、SSE 爬虫接口；未匹配的 SPA 前端路由不属于数据接口。
func isUserAPIPath(path string) bool {
	if strings.HasSuffix(path, "Interface") || strings.HasSuffix(path, "WS") {
		return true
	}
	if path == "/CaptchaGenerate" || path == "/SpiderDataSourceCrawl" {
		return true
	}
	if strings.HasPrefix(path, "/ProtocolConvertAnalyzer") {
		return true
	}
	return false
}

// writeUserAuthFail 用户端鉴权失败统一输出：API 请求返回 401 JSON（前端 api.js 捕获后自动跳登录页）
func writeUserAuthFail(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: message})
}

// requireUserClaimsOr401 业务 handler 内部纵深防御：解析 JWT，失败/无登录态 → 直接 401 JSON，
// 行为与 userAuthMiddleware 一致。返回 (claims, true) 表示已登录可继续；返回 (nil, false) 表示
// 已写 401 响应，调用方应 return。
//
// 设计动机：userAuthMiddleware 是第一道防线（路由级），理论上请求进入 handler 时 claims 必非空；
// 但 handler 内部仍做二次校验是为了纵深防御——若未来新增 mux 路径忘记挂中间件、或中间件被旁路，
// 业务接口仍能正确拒绝未登录请求。本次把所有"claims.UserID == 0"分支统一为 401 JSON，
// 与 userAuthMiddleware 行为对齐，避免出现"中间件漏拦的请求返回 HTTP 200 success:false 误导前端"的死角。
func requireUserClaimsOr401(w http.ResponseWriter, r *http.Request) (*UserTokenClaims, bool) {
	claims := getUserToken(r)
	if claims == nil || claims.UserID == 0 {
		writeUserAuthFail(w, "未登录或登录已过期")
		return nil, false
	}
	return claims, true
}

// userAuthMiddleware 用户认证中间件
func userAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 尾斜杠归一化（仅判定用，不得改写 r.URL.Path——会传导给后续 handler 触发 301 循环）
		path := r.URL.Path
		if n := strings.TrimSuffix(path, "/"); n != "" {
			path = n
		}
		// 阶段AO：公开路由精确匹配前做大小写归一化（防御反向代理改写路径大小写的极端情况）；
		// 仅用于 map 查询，不改写 r.URL.Path。
		lookupPath := strings.ToLower(path)
		// 公开路由放行
		publicPaths := map[string]bool{
			"/userlogin":          true,
			"/captchagenerate":    true,
			"/userlogininterface": true,
		}
		// 放行静态资源（旧版 /static/ + Vite 构建产物 /assets/ + 根目录静态文件）
		if len(path) > 8 && (path[:8] == "/static/" || path[:8] == "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		if isStaticFile(path) {
			next.ServeHTTP(w, r)
			return
		}
		if publicPaths[lookupPath] {
			next.ServeHTTP(w, r)
			return
		}
		// Anthropic/OpenAI 代理路径放行（自带 API Key 认证，无需 JWT）
		anthropicPrefix := "/" + config.G.AgentAnthropicListenURL + "/"
		openaiPrefix := "/" + config.G.AgentOpenAIListenURL + "/"
		if strings.HasPrefix(path, anthropicPrefix) || strings.HasPrefix(path, openaiPrefix) {
			next.ServeHTTP(w, r)
			return
		}

		// 验证 Token
		claims := getUserToken(r)
		if claims.UserID == 0 {
			// v2.0.75：对齐管理端行为——API 请求返回 401 JSON（fetch 自动跟随 302 会拿到登录页
			// HTML 且 HTTP 200，导致前端误判为"获取失败"而非跳转登录页）；
			// 页面导航放行由前端路由接管，页面型伪装请求 302 跳转登录页（相对 Location，网关子路径代理兼容）
			clearUserLoginCookie(w)
			acceptHTML := strings.Contains(r.Header.Get("Accept"), "text/html")
			if acceptHTML && !isUserAPIPath(path) {
				next.ServeHTTP(w, r)
				return
			}
			if acceptHTML {
				http.Redirect(w, r, "UserLogin", http.StatusFound)
				return
			}
			writeUserAuthFail(w, "未登录或登录已过期")
			return
		}

		// 检查用户和模型状态
		isValid, shouldRedirect := checkUserAndModelStatus(claims)
		if !isValid {
			clearUserLoginCookie(w)
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				if shouldRedirect {
					http.Redirect(w, r, "UserLogin", http.StatusFound) // 相对 Location
				} else {
					next.ServeHTTP(w, r)
				}
			} else {
				writeUserAuthFail(w, "登录状态已失效（用户或模型被禁用/删除）")
			}
			return
		}

		// 阶段AO：把鉴权结果写入 r.Context()，供后续 handler（特别是 WebSocket 升级）识别调用者角色。
		// userAuthMiddleware 不会拦截 WS upgrade（upgrader 是 Hijacker），但 middleware 仍会运行；
		// 把 claims 透传给 WS handler 即可，无需 WS 自行重复解析 JWT（也避开了"api ↔ websocket 循环依赖"）。
		r = r.WithContext(context.WithValue(r.Context(), websocket.AuthClaimsContextKey{}, &websocket.AuthClaims{
			Role:      websocket.WsRoleUser, // 由 authWSUpgrade 解读；外部包见 server_ws_hub.go
			UserID:    claims.UserID,
			UserName:  claims.UserName,
			ModelName: claims.ModelName,
			LoginType: claims.LoginType,
		}))

		next.ServeHTTP(w, r)
	})
}

// isStaticFile 判断是否为 SPA 静态资源文件（favicon、JS、CSS、图片等）
func isStaticFile(path string) bool {
	exts := []string{".svg", ".png", ".ico", ".js", ".css", ".woff", ".woff2", ".ttf", ".map"}
	for _, ext := range exts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}
