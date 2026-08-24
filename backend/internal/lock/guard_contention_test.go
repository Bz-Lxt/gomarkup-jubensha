package lock_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	lockpkg "github.com/alkaid/jubensha-carpool/backend/internal/lock"
)

type contentionHook struct {
	setCalls atomic.Int32
}

func (h *contentionHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *contentionHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "set" {
			cmd.(*redis.BoolCmd).SetVal(h.setCalls.Add(1) > 1)
		}
		return nil
	}
}

func (h *contentionHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestSlotGuard_ReleasesLocalLockAfterDistributedContention(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "unused:0"})
	rdb.AddHook(&contentionHook{})
	t.Cleanup(func() { _ = rdb.Close() })

	guard := lockpkg.NewSlotGuard(
		lockpkg.NewCarSlotLocalLock(64),
		lockpkg.NewRedisLock(rdb, time.Minute, 0, true),
		time.Second,
	)

	firstRan := false
	err := guard.Do(context.Background(), 42, func(context.Context) error {
		firstRan = true
		return nil
	})
	if !apperr.Is(err, apperr.CodeSlotLockBusy) {
		t.Fatalf("首次竞争返回 %v，期望错误码 %s", err, apperr.CodeSlotLockBusy)
	}
	if firstRan {
		t.Fatal("未获取到分布式锁时不应执行临界区")
	}

	done := make(chan error, 1)
	secondRan := make(chan struct{}, 1)
	go func() {
		done <- guard.Do(context.Background(), 42, func(context.Context) error {
			secondRan <- struct{}{}
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("竞争结束后的同房间调用返回错误: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("竞争结束后的同房间调用仍被阻塞")
	}

	select {
	case <-secondRan:
	default:
		t.Fatal("竞争结束后未执行临界区")
	}
}
