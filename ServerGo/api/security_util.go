package api

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	modelsdb "github.com/lishimeng/LsmTokensServer/models"
)

// ValidateField 通用字段校验：仅做长度限制（SQL 注入防护由 GORM 参数化查询保证，
// 不再做关键字黑名单替换——旧实现对含 "end"/"select" 等子串的密码会静默改写，已移除）
func ValidateField(input string, maxLen int, fieldName string) (string, error) {
	if len(input) > maxLen {
		return "", fmt.Errorf("%s 长度超过限制（最大 %d 字符）", fieldName, maxLen)
	}
	return input, nil
}

// ========== 密码哈希（v2.0.56 安全加固） ==========

// HashPassword 使用 bcrypt 哈希密码（cost 10）
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// IsPasswordHashed 判断存储值是否已是 bcrypt 哈希（$2a$/$2b$/$2y$ 前缀）
func IsPasswordHashed(stored string) bool {
	return strings.HasPrefix(stored, "$2")
}

// VerifyPassword 校验密码：支持 bcrypt 哈希与旧版明文（返回 isPlainLegacy=true 表示命中旧明文，调用方可择机升级哈希）
func VerifyPassword(stored, input string) (ok bool, isPlainLegacy bool) {
	if IsPasswordHashed(stored) {
		err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(input))
		return err == nil, false
	}
	// 旧版明文：常量时间比较
	ok = subtleConstantTimeEq(stored, input)
	return ok, ok // 命中即视为旧明文，需升级
}

// subtleConstantTimeEq 常量时间字符串比较
func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// MaskPhone 手机号掩码（138****1234；长度不足 7 位时全掩码）
func MaskPhone(phone string) string {
	if len(phone) < 7 {
		if phone == "" {
			return ""
		}
		return "****"
	}
	return phone[:3] + "****" + phone[len(phone)-4:]
}

// MaskAPIKey 模型 API Key 掩码（前 8 位 + ****；不足 8 位全掩码）。
// v2 安全加固（测试报告 20260826 BUG-4/SUG-1）：列表与 config 响应默认脱敏，
// 完整 Key 仅经 reveal_key 显式获取（前端展示前缀与旧版打码格式一致）。
func MaskAPIKey(key string) string {
	if len(key) > 8 {
		return key[:8] + "****"
	}
	if key != "" {
		return "****"
	}
	return ""
}

// maskUserModelAPIKeys 原地脱敏模型列表的 API Key（查询函数每次返回全新切片，无缓存共享）
func maskUserModelAPIKeys(models []modelsdb.TAgentHttpUserModelInfo) {
	for i := range models {
		models[i].APIKey = MaskAPIKey(models[i].APIKey)
	}
}
