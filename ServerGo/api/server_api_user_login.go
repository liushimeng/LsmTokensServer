package api

import (
	"crypto/rand"
	"encoding/base64"
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
)

// ========== 常量定义 ==========

const (
	userTokenExpireDuration = time.Hour * 24 * 1
	userLoginCookieName     = "lsm_user_token"
	maxLoginFailures        = 3
	loginLockDuration       = 10 * time.Minute
	loginFailureWindow      = time.Minute
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
	LoginType   string `json:"login_type"`   // "model" 或 "user"
	ModelName   string `json:"model_name"`   // 模型登录用
	APIKey      string `json:"api_key"`      // 模型登录用
	UserName    string `json:"user_name"`    // 用户登录用
	Password    string `json:"password"`     // 用户登录用
	Phone       string `json:"phone"`        // 用户登录用
	CaptchaID   string `json:"captcha_id"`   // 验证码 ID
	CaptchaCode string `json:"captcha_code"` // 验证码输入
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
		return fmt.Errorf("登录过于频繁，请 %d 分钟后重试", int(attempt.lockedUntil.Sub(now).Minutes())+1)
	}
	// 如果超过 1 分钟窗口，重置失败次数
	if now.Sub(attempt.lastFailedTime) > loginFailureWindow {
		attempt.failedCount = 0
	}

	return nil
}

// recordLoginFailure 记录登录失败
func recordLoginFailure(ip string) {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	attempt, exists := loginAttempts[ip]
	if !exists {
		attempt = &loginAttempt{}
		loginAttempts[ip] = attempt
	}

	now := time.Now()
	// 超过窗口期重置
	if now.Sub(attempt.lastFailedTime) > loginFailureWindow {
		attempt.failedCount = 0
	}

	attempt.failedCount++
	attempt.lastFailedTime = now

	if attempt.failedCount >= maxLoginFailures {
		attempt.lockedUntil = now.Add(loginLockDuration)
	}
}

// clearLoginAttempt 清除登录失败记录（成功时调用）
func clearLoginAttempt(ip string) {
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	delete(loginAttempts, ip)
}

// ========== 验证码接口 ==========

// captchaGenerateHandle 生成验证码
func captchaGenerateHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	captchaID := captcha.NewLen(4)
	var buf strings.Builder
	err := captcha.WriteImage(&buf, captchaID, 120, 40)
	if err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "生成验证码失败"})
		return
	}

	imageBase64 := base64.StdEncoding.EncodeToString([]byte(buf.String()))
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"captcha_id": captchaID,
		"image_url":  "data:image/png;base64," + imageBase64,
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

	// 检查是否被锁定
	if err := checkLoginAttempt(clientIP); err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: err.Error()})
		return
	}

	// 校验验证码
	if req.CaptchaID == "" || req.CaptchaCode == "" {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "请输入验证码"})
		return
	}
	if !captcha.VerifyString(req.CaptchaID, req.CaptchaCode) {
		recordLoginFailure(clientIP)
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
		recordLoginFailure(clientIP)
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
func doModelLogin(req userLoginReq) (*modelsdb.TAgentHttpUserInfo, error) {
	if req.ModelName == "" || req.APIKey == "" {
		return nil, fmt.Errorf("模型名称和 API Key 不能为空")
	}

	// 通过 API Key 查找模型（复用缓存+数据库查询）
	model, err := modelsdb.GetUserModelByAPIKey(req.APIKey)
	if err != nil {
		return nil, fmt.Errorf("API Key 无效")
	}

	// 校验模型名称是否匹配
	if model.ModelName != strings.TrimSpace(req.ModelName) {
		return nil, fmt.Errorf("模型名称不匹配")
	}

	// 检查模型是否被禁用
	if model.Status == modelsdb.UserModelStatus_Disabled {
		return nil, fmt.Errorf("模型已被禁用")
	}

	// 通过 UserID 获取用户信息（优先缓存）
	user, ok := modelsdb.GetCachedUserByID(model.UserID)
	if !ok {
		// 缓存未命中，从数据库查询
		user, err = modelsdb.GetUserByID(model.UserID)
		if err != nil {
			return nil, fmt.Errorf("用户不存在")
		}
	}

	// 检查用户状态
	if user.Status == modelsdb.UserStatus_Disabled {
		return nil, fmt.Errorf("用户已被禁用")
	}

	return user, nil
}

// doUserLogin 用户登录验证
func doUserLogin(req userLoginReq) (*modelsdb.TAgentHttpUserInfo, error) {
	if req.UserName == "" || req.Password == "" || req.Phone == "" {
		return nil, fmt.Errorf("用户名、密码和手机号不能为空")
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

	// 校验手机号（错误信息与密码一致，避免枚举探测）
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
	if path == "/CaptchaGenerate" || path == "/CaptchaAudio" || path == "/SpiderDataSourceCrawl" {
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

// userAuthMiddleware 用户认证中间件
func userAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 尾斜杠归一化（仅判定用，不得改写 r.URL.Path——会传导给后续 handler 触发 301 循环）
		path := r.URL.Path
		if n := strings.TrimSuffix(path, "/"); n != "" {
			path = n
		}
		// 公开路由放行
		publicPaths := map[string]bool{
			"/UserLogin":          true,
			"/CaptchaGenerate":    true,
			"/UserLoginInterface": true,
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
		if publicPaths[path] {
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
