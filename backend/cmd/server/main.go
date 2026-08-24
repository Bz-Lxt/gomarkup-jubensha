// Command server 是剧本杀拼车墙的后端进程。
//
// 启动顺序（每一步失败都直接退出，绝不带着残缺状态继续跑）：
//
//	配置 → 日志 → Postgres → 迁移 → Redis → 三层锁 → Hub → service → 路由
//	→ 种子数据 → 调度器 → HTTP 监听
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/api"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/handler"
	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/db"
	"github.com/alkaid/jubensha-carpool/backend/internal/jwtutil"
	"github.com/alkaid/jubensha-carpool/backend/internal/lock"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/seed"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
	"github.com/alkaid/jubensha-carpool/backend/internal/ws"
)

// version 由构建时的 -ldflags 注入，缺省为 dev。
var version = "dev"

func main() {
	if err := run(); err != nil {
		// 此时 logger 可能还没初始化，直接写 stderr 保证信息不丢。
		os.Stderr.WriteString("启动失败: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Init(cfg.LogLevel, cfg.IsProd(), os.Stdout)
	logger.Info("剧本杀拼车墙后端启动中",
		"version", version, "env", cfg.AppEnv, "addr", cfg.HTTPAddr,
		"timezone", timeutil.Shanghai.String(), "now", timeutil.Now().Format(time.RFC3339))

	bootCtx, bootCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer bootCancel()

	pool, err := db.OpenPostgres(bootCtx, cfg.DatabaseURL, cfg.DBMaxOpen, cfg.DBMaxIdle, cfg.DBConnMaxIdl)
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()

	if err := db.Migrate(bootCtx, pool); err != nil {
		return err
	}

	rdb, err := db.OpenRedis(bootCtx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	// ---- 三层锁 ----
	localLock := lock.NewCarSlotLocalLock(lock.DefaultShards)
	redisLock := lock.NewRedisLock(rdb, cfg.SlotLockTTL, cfg.SlotLockRetries, cfg.SlotRedisLockOn)
	guard := lock.NewSlotGuard(localLock, redisLock, cfg.SlotTxTimeout)
	logger.Info("抢位锁已就绪",
		"l1_shards", localLock.Shards(),
		"l2_redis_enabled", redisLock.Enabled(),
		"l2_ttl", cfg.SlotLockTTL.String(),
		"l3", "postgres SELECT FOR UPDATE + CHECK constraints")
	if !redisLock.Enabled() {
		// 这是 NFR-1 A-4 的验收场景，也可能是误配。无论哪种都必须显式可见。
		logger.Warn("L2 Redis 分布式锁已关闭，正确性完全依赖 L3 数据库悲观锁")
	}

	// ---- 依赖装配 ----
	deps := service.NewDeps(cfg, pool, guard)
	jwtMgr := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)

	authSvc := service.NewAuthService(deps, jwtMgr)
	userSvc := service.NewUserService(deps)
	roomSvc := service.NewRoomService(deps)
	slotSvc := service.NewSlotService(deps)
	chatSvc := service.NewChatService(deps)

	hub := ws.NewHub(cfg, rdb)
	// Hub 实现 service.Publisher，回注给 service 层完成闭环。
	// service 不 import ws，因此这里是唯一的接线点。
	deps.SetPublisher(hub)
	if err := hub.Start(bootCtx); err != nil {
		return err
	}
	wsServer := ws.NewServer(hub, cfg, chatSvc)

	scheduler := service.NewScheduler(deps, slotSvc, cfg.SchedulerEvery)

	router := api.NewRouter(cfg, rdb, authSvc, api.Handlers{
		Auth:   handler.NewAuthHandler(authSvc, userSvc),
		Room:   handler.NewRoomHandler(roomSvc),
		Slot:   handler.NewSlotHandler(slotSvc, roomSvc),
		Chat:   handler.NewChatHandler(chatSvc),
		WS:     handler.NewWSHandler(wsServer, chatSvc),
		Health: handler.NewHealthHandler(pool, rdb, hub, guard, scheduler, version),
	})

	if cfg.SeedOnBoot {
		if err := seed.Run(bootCtx, deps, roomSvc, slotSvc); err != nil {
			// 种子数据失败不该阻断服务：接口本身是好的，只是墙上空着。
			logger.Error("种子数据灌入失败，服务继续启动", "error", err)
		}
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	if cfg.SchedulerEnabled {
		scheduler.Start(appCtx)
	} else {
		logger.Warn("调度器已禁用：占位回收与炸车判定不会自动执行")
	}

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
		// WS 是长连接，因此不能设 WriteTimeout / ReadTimeout（那会在
		// 超时时直接掐断 WebSocket）。改用 ReadHeaderTimeout 防慢速头攻击。
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务开始监听", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		logger.Info("收到退出信号，开始优雅关闭", "signal", sig.String())
	}

	// 关闭顺序：先停 HTTP（不再接新请求）→ 停调度器 → 关 Hub 连接。
	// 反过来会让正在处理的请求找不到 Hub。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP 优雅关闭失败", "error", err)
	}
	appCancel()
	scheduler.Stop()
	hub.Shutdown(10 * time.Second)
	logger.Info("已完成优雅关闭")
	return nil
}
