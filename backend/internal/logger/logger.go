// Package logger 提供全项目唯一的结构化日志入口。
//
// 对齐 KB [Logging]：禁止散落 fmt.Println / log.Println，生产环境自动屏蔽 debug。
// 对齐 KB [Go][WS]：Hub 的 kick / 广播路径也会打日志，而单元测试可能未调用 Init，
// 因此所有导出函数在 base == nil 时必须安全降级，绝不 nil panic。
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

type ctxKey string

const (
	ctxKeyRequestID ctxKey = "request_id"
	ctxKeyUserID    ctxKey = "user_id"
)

var (
	mu   sync.RWMutex
	base *slog.Logger
)

// Init 初始化全局 Logger。level 取 debug|info|warn|error，非法值降级为 info。
func Init(level string, jsonOutput bool, out io.Writer) {
	if out == nil {
		out = os.Stdout
	}
	opts := &slog.HandlerOptions{
		Level: parseLevel(level),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// 日志时间戳统一走北京时区，避免与业务时间对不上。
			if a.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, timeutil.Now().Format("2006-01-02T15:04:05.000+08:00"))
			}
			return a
		},
	}
	var h slog.Handler
	if jsonOutput {
		h = slog.NewJSONHandler(out, opts)
	} else {
		h = slog.NewTextHandler(out, opts)
	}
	mu.Lock()
	base = slog.New(h)
	mu.Unlock()
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// L 返回全局 Logger。未 Init 时返回丢弃型 Logger 而非 nil，
// 使测试与库代码可以无条件调用。
func L() *slog.Logger {
	mu.RLock()
	l := base
	mu.RUnlock()
	if l == nil {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return l
}

// WithRequestID 把 request_id 注入 context，供后续日志自动携带。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// WithUserID 把 user_id 注入 context。
func WithUserID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

// C 返回带 context 字段（request_id / user_id）的 Logger。
func C(ctx context.Context) *slog.Logger {
	l := L()
	if ctx == nil {
		return l
	}
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok && v != "" {
		l = l.With("request_id", v)
	}
	if v, ok := ctx.Value(ctxKeyUserID).(int64); ok && v != 0 {
		l = l.With("user_id", v)
	}
	return l
}

func Debug(msg string, args ...any) { L().Debug(msg, args...) }
func Info(msg string, args ...any)  { L().Info(msg, args...) }
func Warn(msg string, args ...any)  { L().Warn(msg, args...) }
func Error(msg string, args ...any) { L().Error(msg, args...) }
