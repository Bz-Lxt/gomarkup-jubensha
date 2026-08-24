// Package api 组装路由。
package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/handler"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/middleware"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

// Handlers 是路由需要的全部 handler。
type Handlers struct {
	Auth   *handler.AuthHandler
	Room   *handler.RoomHandler
	Slot   *handler.SlotHandler
	Chat   *handler.ChatHandler
	WS     *handler.WSHandler
	Health *handler.HealthHandler
}

// NewRouter 构建 gin 引擎。
func NewRouter(cfg *config.Config, rdb *redis.Client, auth *service.AuthService, h Handlers) *gin.Engine {
	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}
	// gin.New() 而不是 gin.Default()：Default 会挂上 gin 自己的 Logger，
	// 输出格式与项目统一 Logger 不一致，违反 KB [Logging] 的单一日志出口要求。
	r := gin.New()
	r.Use(
		middleware.RequestID(),
		middleware.Recovery(),
		middleware.SecurityHeaders(),
		middleware.CORS(cfg.CORSOrigins),
		middleware.AccessLog(),
	)

	r.NoRoute(func(c *gin.Context) {
		response.Fail(c, apperr.ErrNotFound.WithMessage("接口不存在: "+c.Request.URL.Path))
	})

	// 探测端点不鉴权、不限流：负载均衡与 docker healthcheck 要访问它们。
	r.GET("/healthz", h.Health.Healthz)
	r.GET("/readyz", h.Health.Readyz)

	requireAuth := middleware.RequireAuth(auth)
	optionalAuth := middleware.OptionalAuth(auth)

	loginLimit := middleware.RateLimit(rdb, "login", cfg.RateLoginPerMin, time.Minute, middleware.ScopeIP)
	joinLimit := middleware.RateLimit(rdb, "join", cfg.RateJoinPerMin, time.Minute, middleware.ScopeUser)
	chatLimit := middleware.RateLimit(rdb, "chat", cfg.RateChatPerMin, time.Minute, middleware.ScopeUser)
	writeLimit := middleware.RateLimit(rdb, "write", 30, time.Minute, middleware.ScopeUser)

	api := r.Group("/api")
	{
		auth := api.Group("/auth")
		{
			// 注册也限流：否则可以脚本批量灌垃圾账号。
			auth.POST("/register", loginLimit, h.Auth.Register)
			auth.POST("/login", loginLimit, h.Auth.Login)
			auth.POST("/refresh", h.Auth.Refresh)
		}

		meta := api.Group("/meta")
		{
			meta.GET("/tags", h.Auth.TagCatalog)
			meta.GET("/enums", h.Room.Meta)
			meta.GET("/cities", h.Room.Cities)
		}

		users := api.Group("/users", requireAuth)
		{
			users.GET("/me", h.Auth.Me)
			users.PATCH("/me", writeLimit, h.Auth.UpdateMe)
		}

		rooms := api.Group("/rooms")
		{
			// 墙对未登录用户开放：新用户先看到内容才有注册动力。
			// OptionalAuth 让已登录用户额外拿到「我在这车上」的标记。
			rooms.GET("", optionalAuth, h.Room.Wall)
			rooms.GET("/mine", requireAuth, h.Room.Mine)
			rooms.GET("/unread", requireAuth, h.Chat.Unread)
			rooms.POST("", requireAuth, writeLimit, h.Room.Create)

			rooms.GET("/:id", optionalAuth, h.Room.Detail)
			rooms.GET("/:id/history", optionalAuth, h.Room.History)
			rooms.GET("/:id/audit", optionalAuth, h.Slot.Audit)

			rooms.POST("/:id/join", requireAuth, joinLimit, h.Slot.Join)
			rooms.POST("/:id/leave", requireAuth, joinLimit, h.Slot.Leave)
			rooms.POST("/:id/confirm", requireAuth, writeLimit, h.Slot.Confirm)
			rooms.POST("/:id/kick", requireAuth, writeLimit, h.Slot.Kick)
			rooms.POST("/:id/lock", requireAuth, writeLimit, h.Slot.Lock)
			rooms.POST("/:id/cancel", requireAuth, writeLimit, h.Slot.Cancel)

			rooms.GET("/:id/messages", requireAuth, h.Chat.History)
			rooms.POST("/:id/messages", requireAuth, chatLimit, h.Chat.Send)
			rooms.POST("/:id/read", requireAuth, h.Chat.Ack)
		}
	}

	// WebSocket 端点不挂限流中间件：限流在连接内部按帧计数（ws/ratelimit.go），
	// 对长连接来说「每次请求」的语义不成立。
	wsGroup := r.Group("/ws")
	{
		wsGroup.GET("/rooms/:id", requireAuth, h.WS.Room)
		wsGroup.GET("/wall", optionalAuth, h.WS.Wall)
	}

	return r
}
