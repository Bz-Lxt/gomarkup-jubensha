package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// RateScope 决定限流的计数维度。
type RateScope string

const (
	// ScopeIP 按客户端 IP 计数，用于登录这类未鉴权端点。
	ScopeIP RateScope = "ip"
	// ScopeUser 按用户 ID 计数，用于上车、发消息这类已鉴权端点。
	ScopeUser RateScope = "user"
)

// RateLimit 基于 Redis 的固定窗口限流。
//
// 为什么用 Redis 而不是进程内计数：多副本部署时进程内计数会让实际限额
// 变成 N 倍。INCR + EXPIRE 两条命令在 pipeline 里发出，窗口内第一次
// INCR 返回 1 时才设 TTL。
//
// 降级策略：Redis 故障时**放行**并记 WARN。限流是保护措施，不是安全边界；
// 让它在缓存抖动时把整个站点锁死，是本末倒置。
func RateLimit(rdb *redis.Client, name string, limit int, window time.Duration, scope RateScope) gin.HandlerFunc {
	if limit <= 0 {
		limit = 60
	}
	if window <= 0 {
		window = time.Minute
	}
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}
		var key string
		switch scope {
		case ScopeUser:
			uid := UserID(c)
			if uid == 0 {
				// 未鉴权就落回 IP 维度，避免所有匿名请求共用一个 "user:0" 桶。
				key = fmt.Sprintf("rl:%s:ip:%s", name, c.ClientIP())
			} else {
				key = fmt.Sprintf("rl:%s:user:%d", name, uid)
			}
		default:
			key = fmt.Sprintf("rl:%s:ip:%s", name, c.ClientIP())
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 500*time.Millisecond)
		defer cancel()

		pipe := rdb.Pipeline()
		incr := pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, window)
		if _, err := pipe.Exec(ctx); err != nil {
			logger.C(c.Request.Context()).Warn("限流器不可用，本次放行",
				"limiter", name, "error", err)
			c.Next()
			return
		}

		count := incr.Val()
		if count > int64(limit) {
			c.Writer.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
			response.Fail(c, apperr.ErrRateLimited.
				WithDetail("limit", limit).
				WithDetail("window_seconds", int(window.Seconds())))
			return
		}
		c.Next()
	}
}
