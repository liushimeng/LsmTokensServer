package api

// v2.0.56 安全加固：管理端登录与鉴权中间件。
// 此前管理端（managerWebListenPort）所有 API 无任何鉴权，任何人可直接增删用户/改密码，
// 属最高危缺陷。本文件引入：
//   - POST /ManagerLoginInterface  管理员登录（用户名+密码+验证码，防爆破）
//   - POST /ManagerLogoutInterface 管理员登出
//   - ManagerAuthMiddleware        管理端鉴权中间件（webserver 装配时套用）
//
// 管理员凭证存于已 gitignore 的 LsmTokensServer.conf（security.managerUserName/Password）；
// 未配置时所有管理端业务接口默认拒绝（不提供任何默认账号，避免默认口令后门）。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dchest/captcha"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
)

const (
	managerLoginCookieName = "lsm_manager_token"
	managerTokenDuration   = 24 * 60 * 60 // 秒（24h，与用户端一致）
)

// ManagerTokenClaims 管理员登录 Token 声明
type ManagerTokenClaims struct {
	ManagerName string `json:"manager_name"`
	LoginType   string `json:"login_type"` // 固定 "manager"
	jwt.RegisteredClaims
}

// managerLoginInterfaceHandle 管理员登录 API
func managerLoginInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "仅支持 POST 请求"})
		return
	}

	var req struct {
		UserName    string `json:"user_name"`
		Password    string `json:"password"`
		CaptchaID   string `json:"captcha_id"`
		CaptchaCode string `json:"captcha_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "请求解析失败"})
		return
	}

	clientIP := getClientIP(r)
	if err := checkLoginAttempt("manager:" + clientIP); err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: err.Error()})
		return
	}

	// 凭证未配置：默认全拒绝并告警（日志每分钟最多提示一次由调用频度自然限制）
	if config.G == nil || config.G.Security.ManagerUserName == "" || config.G.Security.ManagerPassword == "" {
		logger.Printf("[SECURITY] 拒绝管理端登录：LsmTokensServer.conf 未配置 security.managerUserName/Password")
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "管理端登录未配置，请在 LsmTokensServer.conf 的 security 段配置管理员账号后重启"})
		return
	}

	if req.CaptchaID == "" || req.CaptchaCode == "" || !captcha.VerifyString(req.CaptchaID, req.CaptchaCode) {
		recordLoginFailure("manager:" + clientIP)
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "验证码错误或已过期"})
		return
	}

	// 常量时间比较，避免时序侧信道
	if !subtleConstantTimeEq(strings.TrimSpace(req.UserName), config.G.Security.ManagerUserName) ||
		!subtleConstantTimeEq(req.Password, config.G.Security.ManagerPassword) {
		recordLoginFailure("manager:" + clientIP)
		logger.LogUserAction("MANAGER_LOGIN_FAIL", strings.TrimSpace(req.UserName), fmt.Sprintf("IP=%s", clientIP))
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "用户名或密码错误"})
		return
	}

	// 颁发管理员 JWT
	now := time.Now()
	claims := &ManagerTokenClaims{
		ManagerName: config.G.Security.ManagerUserName,
		LoginType:   "manager",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	tokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(getJWTSecret())
	if err != nil {
		json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "生成登录凭证失败"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     managerLoginCookieName,
		Value:    tokenStr,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   managerTokenDuration,
	})
	clearLoginAttempt("manager:" + clientIP)
	logger.LogUserAction("MANAGER_LOGIN", claims.ManagerName, fmt.Sprintf("IP=%s", clientIP))
	json.NewEncoder(w).Encode(userLoginResp{Success: true, Message: "登录成功"})
}

// managerLogoutInterfaceHandle 管理员登出 API
func managerLogoutInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	http.SetCookie(w, &http.Cookie{
		Name:     managerLoginCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
	json.NewEncoder(w).Encode(userLoginResp{Success: true, Message: "已登出"})
}

// getManagerToken 解析管理员 Token（仅接受 LoginType=manager）
func getManagerToken(r *http.Request) *ManagerTokenClaims {
	claims := &ManagerTokenClaims{}
	tokenStr := ""
	if cookie, err := r.Cookie(managerLoginCookieName); err == nil {
		tokenStr = cookie.Value
	}
	if tokenStr == "" {
		authHeader := r.Header.Get("Authorization")
		if parts := strings.SplitN(authHeader, " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			tokenStr = parts[1]
		}
	}
	if tokenStr == "" {
		return claims
	}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("不支持的签名算法: %v", token.Header["alg"])
		}
		return getJWTSecret(), nil
	})
	if err != nil || !token.Valid || claims.LoginType != "manager" || claims.ManagerName == "" {
		return &ManagerTokenClaims{}
	}
	return claims
}

// RegisterManagerLoginRoutes 挂载管理端公开路由（登录/验证码/登出）
func RegisterManagerLoginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/CaptchaGenerate", captchaGenerateHandle)
	mux.HandleFunc("/ManagerLoginInterface", managerLoginInterfaceHandle)
	mux.HandleFunc("/ManagerLogoutInterface", managerLogoutInterfaceHandle)
}

// isManagerAPIPath 判定管理端数据接口路径（必须鉴权；页面导航放行见 ManagerAuthMiddleware）
// 规则覆盖全部 REST 接口命名：*Interface 后缀、*WS 后缀、验证码、协议转换分析器 8 个接口。
// 未匹配的路径（如 /ChatAnalysis、/UserManage 等 SPA 前端路由）不属于数据接口。
func isManagerAPIPath(path string) bool {
	if strings.HasSuffix(path, "Interface") || strings.HasSuffix(path, "WS") {
		return true
	}
	if path == "/CaptchaGenerate" || path == "/CaptchaAudio" {
		return true
	}
	if strings.HasPrefix(path, "/ProtocolConvertAnalyzer") {
		return true
	}
	return false
}

// ManagerAuthMiddleware 管理端鉴权中间件（供 webserver 装配）
func ManagerAuthMiddleware(next http.Handler) http.Handler {
	// v2.0.58 网关代理部署：security.managerWebAuthDisabled=true 时全量放行。
	// 适用场景：管理员 Web 服务部署于已完成 Web 端鉴权的可信网关之后，网关侧登录墙
	// 即为鉴权边界，本服务不再叠加管理端 JWT。默认 false，安全红线不变。
	if config.G != nil && config.G.Security.ManagerWebAuthDisabled {
		logger.Printf("[SECURITY] managerWebAuthDisabled=true：管理端 Web 鉴权已关闭（信任网关侧鉴权），全量放行")
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 公开路由放行
		publicPaths := map[string]bool{
			"/ManagerLoginInterface":  true,
			"/ManagerLogoutInterface": true,
			"/CaptchaGenerate":        true,
			"/healthz":                true,
			"/UserLogin":              true,
			"/ManagerLogin":           true, // SPA 登录页路由（页面级）
		}
		// 静态资源放行（SPA 页面本身可加载，数据接口在中间件拦截）
		if strings.HasPrefix(r.URL.Path, "/static/") || strings.HasPrefix(r.URL.Path, "/assets/") ||
			isStaticFile(r.URL.Path) || r.URL.Path == "/" || r.URL.Path == "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		// Anthropic/OpenAI 代理路径放行（自带 API Key 认证，无需管理端 JWT）
		if config.G != nil {
			anthropicPrefix := "/" + config.G.AgentAnthropicListenURL + "/"
			openaiPrefix := "/" + config.G.AgentOpenAIListenURL + "/"
			if strings.HasPrefix(r.URL.Path, anthropicPrefix) || strings.HasPrefix(r.URL.Path, openaiPrefix) {
				next.ServeHTTP(w, r)
				return
			}
		}

		claims := getManagerToken(r)
		if claims.ManagerName == "" {
			// SPA 页面导航放行（Accept 含 text/html 且非数据接口）：页面外壳不含业务数据，
			// 回落 index.html 由前端路由接管；未登录时前端经 UserInfoInterface 401 自行跳登录页。
			// 兼容网关代理部署（网关侧已完成 Web 鉴权），旧版即页面直出、服务端不拦截页面路由。
			if strings.Contains(r.Header.Get("Accept"), "text/html") && !isManagerAPIPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			// 数据接口：页面型伪装请求 302 跳转管理端登录页（相对 Location，兼容网关子路径代理）；API 请求返回 401 JSON
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.Redirect(w, r, "ManagerLogin", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(userLoginResp{Success: false, Message: "未登录或登录已过期"})
			return
		}

		next.ServeHTTP(w, r)
	})
}
