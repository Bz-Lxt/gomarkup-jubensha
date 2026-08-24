package db

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// OpenRedis 建立 Redis 客户端并等待就绪。
func OpenRedis(ctx context.Context, addr, password string, database int) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:            addr,
		Password:        password,
		DB:              database,
		DialTimeout:     3 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolSize:        30,
		MinIdleConns:    4,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	const attempts = 30
	var lastErr error
	for i := 1; i <= attempts; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		lastErr = rdb.Ping(pingCtx).Err()
		cancel()
		if lastErr == nil {
			logger.Info("redis 连接就绪", "attempt", i, "addr", addr)
			return rdb, nil
		}
		select {
		case <-ctx.Done():
			_ = rdb.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	_ = rdb.Close()
	return nil, fmt.Errorf("redis 在 %d 次尝试后仍不可用: %w", attempts, lastErr)
}
