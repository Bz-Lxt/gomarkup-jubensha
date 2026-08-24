package lock

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// releaseScript 只在 token 匹配时删除键。
//
// 为什么不能直接 DEL：如果业务事务超过了锁 TTL，锁会自动过期并被下一个请求
// 拿到；此时前一个请求的 DEL 就会误删「别人的锁」，两个请求同时进入临界区。
// 用 Lua 做「比对再删」是原子的，杜绝这种误删。
var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// ErrNotAcquired 表示在允许的重试次数内没抢到锁。
var ErrNotAcquired = errors.New("lock: 未获取到分布式锁")

// RedisLock 是 L2 跨副本互斥锁。
//
// Enabled 为 false 时 Acquire 直接返回「成功」但不做任何 Redis 操作，
// 使系统退化为「仅靠 L3 数据库悲观锁」。这不是偷懒的开关，而是
// NFR-1 A-4 的验收入口：必须能证明关掉 Redis 锁后依然零超载。
type RedisLock struct {
	rdb     *redis.Client
	ttl     time.Duration
	retries int
	enabled bool
}

// NewRedisLock 构造 L2 锁。
func NewRedisLock(rdb *redis.Client, ttl time.Duration, retries int, enabled bool) *RedisLock {
	if retries < 0 {
		retries = 0
	}
	return &RedisLock{rdb: rdb, ttl: ttl, retries: retries, enabled: enabled}
}

// Enabled 报告 L2 是否启用。
func (l *RedisLock) Enabled() bool { return l.enabled && l.rdb != nil }

// Acquire 获取房间级分布式锁，返回释放函数。
//
// 退避策略是有限次数的短退避（20/40/80ms + 抖动）。这里刻意**不做无限重试**：
// 抢位是用户正在盯着屏幕的同步操作，与其让请求挂住几秒，不如快速返回
// 「手速太快，再点一次」，把重试决定权交给用户。
func (l *RedisLock) Acquire(ctx context.Context, roomID int64) (release func(), err error) {
	if !l.Enabled() {
		return func() {}, nil
	}
	key := fmt.Sprintf("lock:room:%d", roomID)
	token := uuid.NewString()

	for attempt := 0; attempt <= l.retries; attempt++ {
		ok, setErr := l.rdb.SetNX(ctx, key, token, l.ttl).Result()
		if setErr != nil {
			// Redis 抖动不应该让抢位整体失败：L3 仍然保证正确性，
			// 因此这里降级放行并留下 WARN 供排查。
			logger.C(ctx).Warn("L2 分布式锁不可用，降级为仅 DB 悲观锁",
				"room_id", roomID, "attempt", attempt, "error", setErr)
			return func() {}, nil
		}
		if ok {
			return func() { l.release(key, token) }, nil
		}
		if attempt == l.retries {
			break
		}
		backoff := time.Duration(20<<attempt) * time.Millisecond
		jitter := time.Duration(rand.Int63n(int64(backoff/2) + 1))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}
	return nil, ErrNotAcquired
}

func (l *RedisLock) release(key, token string) {
	// 用独立的短超时 context：调用方的 ctx 此时可能已被取消（客户端断开），
	// 但锁必须无论如何都释放，否则会拖住后续所有抢位请求直到 TTL 到期。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := releaseScript.Run(ctx, l.rdb, []string{key}, token).Err(); err != nil && !errors.Is(err, redis.Nil) {
		logger.Error("释放 L2 分布式锁失败", "key", key, "error", err)
	}
}
