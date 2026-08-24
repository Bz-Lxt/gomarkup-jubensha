package lock

import (
	"context"
	"errors"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// SlotGuard 把 L1 与 L2 组合成一次调用，供 SlotService 使用。
//
// 调用顺序固定为 L1 → L2，释放顺序相反。绝不允许颠倒：如果先拿 L2 再拿 L1，
// 同进程内的请求会在持有 Redis 锁的情况下排队等本地锁，白白占用分布式锁 TTL。
type SlotGuard struct {
	local     *CarSlotLocalLock
	dist      *RedisLock
	txBudget  time.Duration
	shardHint int
}

// NewSlotGuard 构造抢位守卫。txBudget 是单次临界区的时间预算，超出仅告警不中断
// （中断会让已提交的事务与告警不一致，反而更难排查）。
func NewSlotGuard(local *CarSlotLocalLock, dist *RedisLock, txBudget time.Duration) *SlotGuard {
	return &SlotGuard{local: local, dist: dist, txBudget: txBudget, shardHint: local.Shards()}
}

// DistEnabled 报告 L2 是否启用，用于 /readyz 与启动日志自陈。
func (g *SlotGuard) DistEnabled() bool { return g.dist.Enabled() }

// Do 在「L1 + L2」双重互斥下执行 fn。
//
// fn 内部必须自行开启数据库事务并使用 SELECT ... FOR UPDATE（L3）。
// 这是刻意的分工：Guard 只管互斥，不碰数据库，因此单元测试可以把 fn 换成
// 任意断言逻辑来验证互斥语义，不需要真实数据库。
func (g *SlotGuard) Do(ctx context.Context, roomID int64, fn func(context.Context) error) error {
	releaseLocal := g.local.Acquire(roomID)
	defer releaseLocal()

	releaseDist, err := g.dist.Acquire(ctx, roomID)
	if err != nil {
		if errors.Is(err, ErrNotAcquired) {
			return apperr.ErrSlotLockBusy.WithCause(err)
		}
		return apperr.From(err)
	}
	defer releaseDist()

	start := time.Now()
	fnErr := fn(ctx)
	elapsed := time.Since(start)

	if elapsed > g.txBudget {
		// 临界区超预算意味着 L2 的 TTL 可能已经过期，互斥性开始依赖 L3。
		// 这不是立刻的故障，但是必须被看见的风险信号。
		logger.C(ctx).Warn("抢位临界区超出时间预算，互斥性已退化为仅依赖 DB 悲观锁",
			"room_id", roomID,
			"elapsed_ms", elapsed.Milliseconds(),
			"budget_ms", g.txBudget.Milliseconds())
	}
	return fnErr
}
