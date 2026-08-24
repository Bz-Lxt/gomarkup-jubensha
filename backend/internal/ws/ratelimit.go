package ws

import (
	"sync"
	"time"
)

// rateLimiter 是每连接的滑动窗口限流器。
//
// 用「窗口内计数」而不是令牌桶：聊天限流的语义是「每分钟最多 N 条」，
// 窗口计数与这句话一一对应，读代码的人不需要在脑内换算桶容量和填充速率。
//
// 每个连接一个实例，因此不存在跨连接争用；加锁只为了 readPump 与将来可能的
// 其他调用者共存。
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	start  time.Time
	count  int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if limit <= 0 {
		limit = 20
	}
	if window <= 0 {
		window = time.Minute
	}
	return &rateLimiter{limit: limit, window: window, start: time.Now()}
}

// allow 报告本次操作是否被允许，并在允许时计数。
func (l *rateLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.start) >= l.window {
		l.start = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}
