// Package lock 实现 Requirements C-1 裁决的三层锁中的 L1 与 L2。
//
//	L1  CarSlotLocalLock  进程内分片互斥  —— 削峰，**不承担正确性**
//	L2  RedisLock         跨副本互斥      —— 可降级
//	L3  数据库悲观锁       在 repository/service 层  —— ★ 正确性最终防线
//
// 之所以保留 "Local Lock" 这个来自原始需求的命名，是因为它准确描述了 L1 的
// 职责边界：它只在单进程内生效。正确性由 L3 独立保证，禁止依赖 L1/L2。
package lock

import (
	"hash/fnv"
	"strconv"
	"sync"
)

// DefaultShards 是分片数。取 2 的幂便于用掩码取模。
const DefaultShards = 256

// CarSlotLocalLock 是按房间分片的进程内互斥锁。
//
// 为什么分片而不是全局一把锁：热门房间的抢位请求会在毫秒级堆积，如果用全局锁，
// A 房间的抢位会阻塞 B 房间，直接把并发度压成 1。分片后不同房间互不干扰。
//
// 为什么不用 map[int64]*sync.Mutex：那需要一把保护 map 的元锁，而持有 per-room
// 锁时再去拿元锁就会踩 KB [Go][Mutex] 记录的双锁死锁。固定长度的分片数组
// 没有元锁，结构上就不可能死锁。
type CarSlotLocalLock struct {
	mus []sync.Mutex
	// mask 用于把哈希值映射到分片下标；len(mus) 必须是 2 的幂。
	mask uint32
}

// NewCarSlotLocalLock 构造分片锁。shards 会被向上取整到 2 的幂，非法值回落到默认值。
func NewCarSlotLocalLock(shards int) *CarSlotLocalLock {
	if shards <= 0 {
		shards = DefaultShards
	}
	n := 1
	for n < shards {
		n <<= 1
	}
	return &CarSlotLocalLock{mus: make([]sync.Mutex, n), mask: uint32(n - 1)}
}

// index 把房间 ID 映射到分片下标。
func (l *CarSlotLocalLock) index(roomID int64) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(roomID, 10)))
	return h.Sum32() & l.mask
}

// Acquire 锁定该房间所属分片，返回释放函数。
//
// 用法固定为：
//
//	release := l.Acquire(roomID)
//	defer release()
//
// 严禁在持有某个分片锁的同时去 Acquire 另一个房间——那会在两个 goroutine
// 交叉持锁时死锁。本项目所有调用点都只锁单一房间。
func (l *CarSlotLocalLock) Acquire(roomID int64) func() {
	i := l.index(roomID)
	l.mus[i].Lock()
	var once sync.Once
	return func() {
		// sync.Once 防止调用方重复 release 导致 unlock of unlocked mutex panic。
		// KB [Go][WAL] 记录过同类问题：defer Close + 显式 Close 造成重复关闭。
		once.Do(func() { l.mus[i].Unlock() })
	}
}

// Shards 返回实际分片数，便于测试断言。
func (l *CarSlotLocalLock) Shards() int { return len(l.mus) }
