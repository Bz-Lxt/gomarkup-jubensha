package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// 频道命名。wallChannel 是墙上席位变动的公共频道。
const (
	roomChannelPrefix = "jbs:room:"
	wallChannel       = "jbs:wall"
)

// Bus 是 Requirements C-2 裁决落地的跨节点广播总线。
//
// 关键设计：**本节点自己产生的事件也走一圈 Redis 再回来**。
// 这样「本地扇出」只有一条代码路径（订阅回调），不会出现单副本和多副本
// 行为不一致的情况——单副本跑通就等于多副本跑通，不存在只在生产才暴露的分支。
//
// 代价是本地消息多了一次 Redis 往返（同机房 < 1ms），换来的是
// `docker compose up --scale backend=2` 零改动即正确。
type Bus struct {
	rdb    *redis.Client
	sub    *redis.PubSub
	onRoom func(roomID int64, payload []byte)
	onWall func(payload []byte)

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	// started 表示 loop goroutine 是否真的跑起来了。
	// 只有跑起来了，Stop 才有权去等 doneCh —— 否则就是等一个
	// 永远不会被关闭的 channel，整个进程卡死在优雅关闭阶段。
	started atomic.Bool
}

func NewBus(rdb *redis.Client) *Bus {
	return &Bus{
		rdb:    rdb,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func roomChannel(roomID int64) string {
	return roomChannelPrefix + strconv.FormatInt(roomID, 10)
}

// Start 订阅频道并开始转发。onRoom / onWall 在独立 goroutine 中被调用，
// 实现方必须自己保证线程安全（这里都是往 Hub 的 channel 里塞，天然安全）。
func (b *Bus) Start(ctx context.Context, onRoom func(int64, []byte), onWall func([]byte)) error {
	if b.rdb == nil {
		return fmt.Errorf("bus: redis client 为空")
	}
	b.onRoom = onRoom
	b.onWall = onWall

	// 用模式订阅：房间是动态创建的，没法预先枚举所有频道。
	b.sub = b.rdb.PSubscribe(ctx, roomChannelPrefix+"*", wallChannel)
	if _, err := b.sub.Receive(ctx); err != nil {
		return fmt.Errorf("bus: 订阅失败: %w", err)
	}

	b.started.Store(true)
	go b.loop(b.sub.Channel())
	logger.Info("WS 广播总线已订阅", "pattern", roomChannelPrefix+"*", "wall", wallChannel)
	return nil
}

func (b *Bus) loop(ch <-chan *redis.Message) {
	defer close(b.doneCh)
	for {
		select {
		case <-b.stopCh:
			return
		case msg, ok := <-ch:
			if !ok {
				logger.Warn("WS 广播总线频道已关闭")
				return
			}
			b.dispatch(msg)
		}
	}
}

func (b *Bus) dispatch(msg *redis.Message) {
	payload := []byte(msg.Payload)
	if msg.Channel == wallChannel {
		if b.onWall != nil {
			b.onWall(payload)
		}
		return
	}
	if !strings.HasPrefix(msg.Channel, roomChannelPrefix) {
		return
	}
	roomID, err := strconv.ParseInt(strings.TrimPrefix(msg.Channel, roomChannelPrefix), 10, 64)
	if err != nil {
		logger.Warn("WS 广播总线收到无法解析的频道名", "channel", msg.Channel)
		return
	}
	if b.onRoom != nil {
		b.onRoom(roomID, payload)
	}
}

// PublishRoom 把已序列化的帧发布到房间频道。
func (b *Bus) PublishRoom(ctx context.Context, roomID int64, payload []byte) {
	if b.rdb == nil {
		return
	}
	if err := b.rdb.Publish(ctx, roomChannel(roomID), payload).Err(); err != nil {
		logger.C(ctx).Error("发布房间事件失败", "room_id", roomID, "error", err)
	}
}

// PublishWall 把已序列化的帧发布到墙频道。
func (b *Bus) PublishWall(ctx context.Context, payload []byte) {
	if b.rdb == nil {
		return
	}
	if err := b.rdb.Publish(ctx, wallChannel, payload).Err(); err != nil {
		logger.C(ctx).Error("发布墙事件失败", "error", err)
	}
}

// Marshal 是发布前的序列化辅助。
func Marshal(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("bus: 序列化失败: %w", err)
	}
	return payload, nil
}

// Stop 停止订阅循环。
//
// 必须能安全应对两种「没跑起来」的情形，否则进程会永久卡在优雅关闭：
//   - Start 从未被调用（单元测试、或 Redis 不可用时的降级启动）；
//   - Start 在 Receive 阶段就报错返回，loop 根本没起。
//
// 这两种情况下 doneCh 永远不会被关闭，无条件 <-b.doneCh 就是死等。
// 同时用 sync.Once 兜住重复调用，避免 close of closed channel
// （KB [Go][WAL] 记录过同类事故）。
func (b *Bus) Stop() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
		if b.sub != nil {
			_ = b.sub.Close()
		}
		if !b.started.Load() {
			return
		}
		select {
		case <-b.doneCh:
		case <-time.After(3 * time.Second):
			logger.Warn("WS 广播总线关闭超时，强制返回")
		}
	})
}
