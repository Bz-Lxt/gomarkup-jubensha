package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/jwtutil"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// TokenParser 是鉴权中间件的依赖，由 service.AuthService 实现。
type TokenParser interface {
	ParseAccess(token string) (*jwtutil.Claims, error)
}

// RequireAuth 强制鉴权。
func RequireAuth(p TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := parseFromRequest(c, p)
		if err != nil {
			response.Fail(c, err)
			return
		}
		bind(c, claims)
		c.Next()
	}
}

// OptionalAuth 可选鉴权：带了有效令牌就注入身份，没带也放行。
//
// 拼车墙对未登录用户也应该可见（否则新用户看不到内容就不会注册），
// 但登录用户要能在墙上看到「我在这车上」的标记，因此需要这个中间件。
func OptionalAuth(p TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims, err := parseFromRequest(c, p); err == nil {
			bind(c, claims)
		}
		c.Next()
	}
}

func bind(c *gin.Context, claims *jwtutil.Claims) {
	c.Set(CtxUserID, claims.UserID)
	c.Set(CtxUsername, claims.Username)
	c.Request = c.Request.WithContext(logger.WithUserID(c.Request.Context(), claims.UserID))
}

// parseFromRequest 依次尝试 Authorization 头与 access_token 查询参数。
//
// 查询参数是为 WebSocket 准备的：浏览器的 WebSocket API 不支持自定义请求头，
// 前端无法在握手时带 Authorization。这是行业通行做法，代价是令牌会进
// 访问日志——因此 access token 的 TTL 只有 2 小时。
func parseFromRequest(c *gin.Context, p TokenParser) (*jwtutil.Claims, error) {
	raw := ""
	if h := c.GetHeader("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			raw = strings.TrimSpace(parts[1])
		} else {
			raw = strings.TrimSpace(h)
		}
	}
	if raw == "" {
		raw = strings.TrimSpace(c.Query("access_token"))
	}
	if raw == "" {
		return nil, apperr.ErrUnauthorized
	}
	return p.ParseAccess(raw)
}
