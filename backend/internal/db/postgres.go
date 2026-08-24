// Package db 负责 PostgreSQL / Redis 的连接管理与数据库迁移。
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx 的 database/sql 驱动

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

//go:embed all:migrations
var migrationFS embed.FS

// advisoryLockKey 是迁移互斥用的 advisory lock 键（任意常量，需全项目唯一）。
const advisoryLockKey int64 = 0x4A42_5343 // "JBSC"

// OpenPostgres 建立连接池并等待数据库就绪。
func OpenPostgres(ctx context.Context, dsn string, maxOpen, maxIdle int, connMaxIdle time.Duration) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	pool.SetMaxOpenConns(maxOpen)
	pool.SetMaxIdleConns(maxIdle)
	pool.SetConnMaxIdleTime(connMaxIdle)
	pool.SetConnMaxLifetime(time.Hour)

	if err := waitReady(ctx, pool); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}

// waitReady 轮询 Ping，容忍容器启动竞态。
func waitReady(ctx context.Context, pool *sql.DB) error {
	const attempts = 30
	var lastErr error
	for i := 1; i <= attempts; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		lastErr = pool.PingContext(pingCtx)
		cancel()
		if lastErr == nil {
			logger.Info("postgres 连接就绪", "attempt", i)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("postgres 在 %d 次尝试后仍不可用: %w", attempts, lastErr)
}

// Migrate 按文件名顺序执行 migrations/*.sql，已执行过的跳过。
//
// KB 教训 [Go][Postgres]（两条，都在这里踩过）：
//  1. 双副本同时跑 CREATE TABLE IF NOT EXISTS 仍可能在 pg_type 上撞 23505，
//     必须用 advisory lock 串行化 DDL。
//  2. advisory lock 若走 sql.DB.Exec，连接池会把 lock 和 unlock 分到不同 session，
//     锁永远不释放，后起的副本卡死在启动阶段、端口不监听。因此必须用
//     sql.DB.Conn 取到**同一个物理连接**，在其上 lock / unlock，并 defer Close。
func Migrate(ctx context.Context, pool *sql.DB) error {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: 获取独占连接失败: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("migrate: 获取 advisory lock 失败: %w", err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey); err != nil {
			logger.Error("释放迁移 advisory lock 失败", "error", err)
		}
	}()

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("migrate: 创建 schema_migrations 失败: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("migrate: 读取已应用版本失败: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return fmt.Errorf("migrate: 扫描版本失败: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("migrate: 遍历版本失败: %w", err)
	}
	_ = rows.Close()

	files, err := listMigrations()
	if err != nil {
		return err
	}

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			logger.Debug("迁移已应用，跳过", "version", version)
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("migrate: 读取 %s 失败: %w", name, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate: 开启事务失败 (%s): %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: 执行 %s 失败: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: 记录版本 %s 失败: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate: 提交 %s 失败: %w", version, err)
		}
		logger.Info("迁移已应用", "version", version)
	}
	return nil
}

func listMigrations() ([]string, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("migrate: 读取迁移目录失败: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("migrate: 没有找到任何 .sql 迁移文件")
	}
	return out, nil
}
