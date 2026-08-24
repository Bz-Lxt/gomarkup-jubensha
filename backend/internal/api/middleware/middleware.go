// Package middleware 提供 HTTP 中间件。
//
// ★ 全局纪律（KB [Go][WS] 教训）：本包**任何中间件都不得包装
// http.ResponseWriter**。gorilla/websocket 的 Upgrade 需要 ResponseWriter
// 实现 http.Hijacker，一旦某个中间件用自定义结构体包了一层却忘记透传
// Hijacker（和 Flusher），/ws 端点会直接 500，而且报错信息与中间件毫无关联，
// 极难定位。需要记录状态码时统一读 gin 的 c.Writer.Status()。
package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

const (
	// CtxUserID 是鉴权后写入 gin.Context 的用户 ID 键。
	CtxUserID = "auth_user_id"
	// CtxUsername 是鉴权后写入的用户名键。
	CtxUsername = "auth_username"
	// CtxRequestID 是请求追踪 ID 键。
	CtxRequestID = "request_id"

	headerRequestID = "X-Request-Id"
)

// RequestID 生成/透传请求 ID，并把它注入 context 供日志自动携带。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(headerRequestID)
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Set(CtxRequestID, rid)
		c.Writer.Header().Set(headerRequestID, rid)
		c.Request = c.Request.WithContext(logger.WithRequestID(c.Request.Context(), rid))
		c.Next()
	}
}

// AccessLog 记录访问日志。
//
// 只读 c.Writer.Status()，不包装 Writer —— 见本包顶部纪律说明。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()

		status := c.Writer.Status()
		elapsed := time.Since(start)
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"elapsed_ms", elapsed.Milliseconds(),
			"ip", c.ClientIP(),
		}
		switch {
		case status >= 500:
			logger.C(c.Request.Context()).Error("HTTP 请求异常", attrs...)
		case status >= 400:
			logger.C(c.Request.Context()).Debug("HTTP 请求被拒", attrs...)
		default:
			logger.C(c.Request.Context()).Info("HTTP 请求", attrs...)
		}
	}
}

// Recovery 捕获 panic，返回统一错误信封而不是 gin 默认的裸 500。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.C(c.Request.Context()).Error("请求处理 panic",
					"path", c.Request.URL.Path, "panic", rec)
				response.Fail(c, apperr.ErrInternal)
			}
		}()
		c.Next()
	}
}

// CORS 按白名单放行跨域请求。
func CORS(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowed[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, "+headerRequestID)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
			h.Set("Vary", "Origin")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// SecurityHeaders 附加基础安全响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

// UserID 从 gin.Context 取出鉴权用户 ID，未登录返回 0。
func UserID(c *gin.Context) int64 {
	if v, ok := c.Get(CtxUserID); ok {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}
