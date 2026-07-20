// Package middleware 提供 Gin 中间件，包括 JWT 鉴权、跨域(CORS)等。
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/config"
	"zaima-backend/internal/pkg/response"
	"zaima-backend/internal/pkg/utils"
)

// JWTAuth JWT 鉴权中间件。
// 从请求 Header 的 Authorization 字段中提取 Bearer Token 并验证。
// 验证通过后会在 gin.Context 中设置 user_id、phone、role 供后续 Handler 使用。
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "缺少认证信息")
			c.Abort()
			return
		}

		// 支持 "Bearer <token>" 格式
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.Unauthorized(c, "认证格式错误，请使用 Bearer Token")
			c.Abort()
			return
		}

		claims, err := utils.ParseToken(parts[1], config.AppConfig.JWT.Secret)
		if err != nil {
			response.Unauthorized(c, "Token 无效或已过期")
			c.Abort()
			return
		}

		// 将用户信息注入上下文，供后续 Handler 直接读取
		c.Set("user_id", claims.UserID)
		c.Set("phone", claims.Phone)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// CORS 跨域中间件。
//
// 允许来源由配置 server.allow_origins 决定：
//   - 为空 → 允许所有来源 "*" (开发默认)
//   - 配置了白名单 → 仅回显命中白名单的 Origin (生产推荐)
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed := allowedOrigins()
		origin := c.GetHeader("Origin")

		if len(allowed) == 0 {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin != "" && originAllowed(origin, allowed) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

func allowedOrigins() []string {
	if config.AppConfig == nil {
		return nil
	}
	return config.AppConfig.Server.AllowOrigins
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}
