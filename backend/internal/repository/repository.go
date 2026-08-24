// Package repository 是纯数据访问层。
//
// 铁律：
//  1. 本层不含任何业务判断（不判断「能不能上车」，只负责「按条件读写」）。
//  2. 每个方法都接受 Querier，因此同一份 SQL 既能在自动提交下用，
//     也能在事务里用。抢位路径必须走事务版本。
//  3. 参数化查询，零字符串拼接。占位符严格连续 $1..$n
//     （KB [Go][Postgres]：写成 $1,$3,$5 会报 42P18，即使实参数量对得上）。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Querier 抽象 *sql.DB 与 *sql.Tx 的公共能力。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// InTx 在事务中执行 fn，出错回滚，panic 也回滚后重新抛出。
//
// 隔离级别用默认的 READ COMMITTED：抢位的正确性由 SELECT ... FOR UPDATE 的
// 行锁保证，不需要 SERIALIZABLE。上调隔离级别只会引入序列化失败重试的复杂度，
// 换不来额外的安全性。
func InTx(ctx context.Context, pool *sql.DB, fn func(Querier) error) error {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

// IsUniqueViolation 判断错误是否为唯一约束冲突（Postgres 23505）。
//
// 不引入 pgconn 的类型断言是刻意的：pgx 在 database/sql 模式下返回的错误类型
// 会随驱动版本变化，字符串匹配 SQLSTATE 更稳。
func IsUniqueViolation(err error) bool {
	return err != nil && containsSQLState(err.Error(), "23505")
}

// IsCheckViolation 判断错误是否为 CHECK 约束冲突（Postgres 23514）。
//
// 这个判断很重要：如果它触发了，说明应用层的锁没拦住非法写入，
// 是数据库屏障在兜底。必须当作严重事件记录。
func IsCheckViolation(err error) bool {
	return err != nil && containsSQLState(err.Error(), "23514")
}

func containsSQLState(msg, code string) bool {
	// pgx 的错误串形如：ERROR: ... (SQLSTATE 23505)
	for i := 0; i+len(code) <= len(msg); i++ {
		if msg[i:i+len(code)] == code {
			return true
		}
	}
	return false
}

// ErrNoRows 转发 sql.ErrNoRows，避免上层再 import database/sql。
var ErrNoRows = sql.ErrNoRows

// IsNoRows 判断是否为「查不到」。
func IsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
