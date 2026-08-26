package api

import (
	"encoding/json"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"net/http"
	"time"
)

// userInfoInterfaceHandle 获取当前登录用户信息
func userInfoInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	claims, ok := requireUserClaimsOr401(w, r)
	if !ok {
		return
	}

	json.NewEncoder(w).Encode(userLoginResp{
		Success: true,
		Data: map[string]interface{}{
			"user_id":    claims.UserID,
			"user_name":  claims.UserName,
			"login_type": claims.LoginType,
			"model_name": claims.ModelName,
		},
	})
}

// userModelListInterfaceHandle 获取当前用户的模型列表
func userModelListInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userLoginResp{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	claims, ok := requireUserClaimsOr401(w, r)
	if !ok {
		return
	}

	models, err := modelsdb.GetUserModelsByUserID(claims.UserID)
	if err != nil {
		json.NewEncoder(w).Encode(userLoginResp{
			Success: false,
			Message: "获取模型列表失败: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(userLoginResp{
		Success: true,
		Data:    models,
	})
}

// userLogoutInterfaceHandle 用户登出
func userLogoutInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	cookie := &http.Cookie{
		Name:     userLoginCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Now().Add(-1 * time.Hour),
	}
	http.SetCookie(w, cookie)

	json.NewEncoder(w).Encode(userLoginResp{
		Success: true,
		Message: "已退出登录",
	})
}
