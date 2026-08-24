package handler

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/lock"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
	"github.com/alkaid/jubensha-carpool/backend/internal/ws"
)

// HealthHandler 提供存活与就绪探测。
//
// 两者刻意分开：
//   - /healthz 只回答「进程还活着吗」，不碰任何外部依赖，因此数据库挂了
//     容器也不会被反复重启（重启并不能修好数据库）。
//   - /readyz 真实探测依赖，是负载均衡摘流量的依据。
type HealthHandler struct {
	pool      *sql.DB
	rdb       *redis.Client
	hub       *ws.Hub
	guard     *lock.SlotGuard
	scheduler *service.Scheduler
	startedAt time.Time
	version   string
}

func NewHealthHandler(pool *sql.DB, rdb *redis.Client, hub *ws.Hub,
	guard *lock.SlotGuard, sch *service.Scheduler, version string) *HealthHandler {
	return &HealthHandler{
		pool: pool, rdb: rdb, hub: hub, guard: guard,
		scheduler: sch, startedAt: timeutil.Now(), version: version,
	}
}

// Healthz GET /healthz
func (h *HealthHandler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"status":     "alive",
		"version":    h.version,
		"uptime_sec": int64(timeutil.Now().Sub(h.startedAt).Seconds()),
		"now":        timeutil.Now().Format(time.RFC3339),
		"timezone":   timeutil.Shanghai.String(),
	})
}

// Readyz GET /readyz
func (h *HealthHandler) Readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	checks := gin.H{}
	ready := true

	if err := h.pool.PingContext(ctx); err != nil {
		checks["postgres"] = gin.H{"ok": false, "error": err.Error()}
		ready = false
	} else {
		checks["postgres"] = gin.H{"ok": true}
	}

	if h.rdb == nil {
		checks["redis"] = gin.H{"ok": false, "error": "client not configured"}
		ready = false
	} else if err := h.rdb.Ping(ctx).Err(); err != nil {
		checks["redis"] = gin.H{"ok": false, "error": err.Error()}
		ready = false
	} else {
		checks["redis"] = gin.H{"ok": true}
	}

	body := gin.H{
		"ok":     ready,
		"checks": checks,
		// 自陈锁与调度的运行状态：出问题时先看这里，
		// 比翻日志快得多。
		"slot_lock": gin.H{
			"l1_local_shards":   256,
			"l2_redis_enabled":  h.guard.DistEnabled(),
			"l3_db_pessimistic": true,
		},
		"websocket": h.hub.Stats(),
		"scheduler": h.scheduler.Stats(),
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, body)
}
