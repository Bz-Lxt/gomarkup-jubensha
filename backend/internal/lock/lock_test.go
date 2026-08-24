package lock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCarSlotLocalLock_ShardsRoundedToPowerOfTwo 断言分片数被规整为 2 的幂，
// 因为 index() 用掩码取模，非 2 的幂会导致部分分片永远用不到。
func TestCarSlotLocalLock_ShardsRoundedToPowerOfTwo(t *testing.T) {
	cases := map[int]int{
		0:   DefaultShards,
		-5:  DefaultShards,
		1:   1,
		3:   4,
		17:  32,
		256: 256,
	}
	for in, want := range cases {
		if got := NewCarSlotLocalLock(in).Shards(); got != want {
			t.Fatalf("NewCarSlotLocalLock(%d).Shards() = %d，期望 %d", in, got, want)
		}
	}
}

// TestCarSlotLocalLock_MutualExclusion 断言同一房间的临界区严格互斥。
//
// 这是 L1 的全部职责。测法是让 1000 个 goroutine 抢同一个房间，
// 每个都在临界区内做「读-加-写」；如果互斥失效，非原子的 counter++
// 会丢更新，最终值就不等于 1000。
func TestCarSlotLocalLock_MutualExclusion(t *testing.T) {
	l := NewCarSlotLocalLock(256)
	const goroutines = 1000
	const roomID = int64(42)

	counter := 0
	inside := int32(0)
	var overlaps int32

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			release := l.Acquire(roomID)
			defer release()

			// 同时进入临界区的 goroutine 数必须恒为 1。
			if atomic.AddInt32(&inside, 1) != 1 {
				atomic.AddInt32(&overlaps, 1)
			}
			counter++
			atomic.AddInt32(&inside, -1)
		}()
	}
	wg.Wait()

	if overlaps != 0 {
		t.Fatalf("检测到 %d 次临界区重叠，L1 互斥失效", overlaps)
	}
	if counter != goroutines {
		t.Fatalf("counter = %d，期望 %d（丢失更新说明互斥失效）", counter, goroutines)
	}
}

// TestCarSlotLocalLock_DifferentRoomsDoNotBlock 断言不同房间可并行。
//
// 分片的意义就在这里：如果用全局单锁，A 房间的抢位会阻塞 B 房间，
// 热门房间的排队会拖垮整站。
func TestCarSlotLocalLock_DifferentRoomsDoNotBlock(t *testing.T) {
	// 分片数为 2 时，找两个哈希落在不同分片的房间 ID。
	l := NewCarSlotLocalLock(64)
	var a, b int64 = -1, -1
	for id := int64(1); id < 200 && (a < 0 || b < 0); id++ {
		if l.index(id) == 0 && a < 0 {
			a = id
		}
		if l.index(id) == 1 && b < 0 {
			b = id
		}
	}
	if a < 0 || b < 0 {
		t.Skip("没找到落在不同分片的房间 ID，跳过")
	}

	releaseA := l.Acquire(a)
	defer releaseA()

	// 持有 A 的锁时，B 必须能立刻拿到。
	done := make(chan struct{})
	go func() {
		releaseB := l.Acquire(b)
		releaseB()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("房间 %d 的锁被房间 %d 阻塞了，分片失效", b, a)
	}
}

// TestCarSlotLocalLock_ReleaseIsIdempotent 断言重复 release 不 panic。
//
// KB [Go][WAL] 记录过同类事故：defer Close 加显式 Close 造成
// "close of closed channel"。锁的 release 同样需要 sync.Once 保护。
func TestCarSlotLocalLock_ReleaseIsIdempotent(t *testing.T) {
	l := NewCarSlotLocalLock(4)
	release := l.Acquire(1)
	release()
	release() // 第二次必须安全无操作
	release()

	// 锁确实已释放：能再次获取。
	done := make(chan struct{})
	go func() {
		r := l.Acquire(1)
		r()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("重复 release 后锁仍被持有，说明 unlock 次数不对")
	}
}

// TestRedisLock_DisabledIsNoop 断言 L2 关闭时 Acquire 直接放行。
//
// 这是 NFR-1 A-4 的前提：必须能一键关掉分布式锁，验证仅靠 L3
// 数据库悲观锁依然零超载。
func TestRedisLock_DisabledIsNoop(t *testing.T) {
	l := NewRedisLock(nil, time.Second, 3, false)
	if l.Enabled() {
		t.Fatal("rdb 为 nil 时 Enabled() 应为 false")
	}
	release, err := l.Acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("禁用状态下 Acquire 不应报错: %v", err)
	}
	if release == nil {
		t.Fatal("禁用状态下应返回可调用的空 release")
	}
	release()
}

// TestSlotGuard_SerializesSameRoom 断言 Guard 组合 L1+L2 后仍然严格串行，
// 且 fn 的返回值被原样透传（业务错误不能被锁层吞掉）。
func TestSlotGuard_SerializesSameRoom(t *testing.T) {
	g := NewSlotGuard(
		NewCarSlotLocalLock(64),
		NewRedisLock(nil, time.Second, 1, false), // L2 关闭：本用例只验证组合语义
		500*time.Millisecond,
	)

	var inside int32
	var overlaps int32
	seats := 0

	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Do(context.Background(), 7, func(context.Context) error {
				if atomic.AddInt32(&inside, 1) != 1 {
					atomic.AddInt32(&overlaps, 1)
				}
				seats++
				atomic.AddInt32(&inside, -1)
				return nil
			})
		}()
	}
	wg.Wait()

	if overlaps != 0 {
		t.Fatalf("Guard 临界区重叠 %d 次", overlaps)
	}
	if seats != 300 {
		t.Fatalf("seats = %d，期望 300", seats)
	}
}

// TestSlotGuard_PropagatesBusinessError 断言业务错误原样返回，
// 否则「席位已满」会被吞成成功，直接造成超载。
func TestSlotGuard_PropagatesBusinessError(t *testing.T) {
	g := NewSlotGuard(NewCarSlotLocalLock(4), NewRedisLock(nil, time.Second, 1, false), time.Second)
	want := errors.New("席位已满")
	got := g.Do(context.Background(), 1, func(context.Context) error { return want })
	if !errors.Is(got, want) {
		t.Fatalf("Do 应透传业务错误，实际 %v", got)
	}
}
