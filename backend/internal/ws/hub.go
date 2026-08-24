package ws

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
)

// wallRoomID 是「拼车墙订阅者」的伪房间号。墙上的席位变动需要推给所有正在
// 看墙的人，把它当成一个特殊房间处理，可以复用同一套扇出逻辑。
const wallRoomID int64 = 0

// Hub 是多房间连接注册中心。
//
// ★ 并发模型（Requirements TR-3 的核心）：
// rooms / wall 两张连接表**只被 loop 这一个 goroutine 访问**，
// 所有变更（注册 / 注销 / 广播 / 踢人 / 查询）都通过 channel 串行化。
//
// 为什么不用 mutex 保护 map：KB [Go][Mutex] 记录过一次真实事故——内存
// Registry 同时存在全局锁与 per-session 锁，某条路径在持有 session 锁时
// 又去拿 registry 锁，WS 断连后整个服务表现为 504 死锁。单 goroutine
// 事件循环从结构上消除了这类锁序问题：根本没有第二把锁。
type Hub struct {
	cfg      *config.Config
	bus      *Bus
	presence *Presence

	// 以下两张表只在 loop goroutine 内读写。
	rooms map[int64]map[*Client]struct{}
	wall  map[*Client]struct{}

	registerCh   chan *Client
	unregisterCh chan *Client
	roomFrameCh  chan roomFrame
	wallFrameCh  chan []byte
	kickCh       chan kickReq

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	connections atomic.Int64
	dropped     atomic.Int64
	delivered   atomic.Int64
}

type roomFrame struct {
	roomID  int64
	payload []byte
}

type kickReq struct {
	client *Client
	reason string
}

// HubStats 是对外可观测指标。
type HubStats struct {
	Connections int64 `json:"connections"`
	Delivered   int64 `json:"delivered"`
	Dropped     int64 `json:"dropped"`
}

// NewHub 构造 Hub。
func NewHub(cfg *config.Config, rdb *redis.Client) *Hub {
	return &Hub{
		cfg:      cfg,
		bus:      NewBus(rdb),
		presence: NewPresence(rdb),
		rooms:    make(map[int64]map[*Client]struct{}),
		wall:     make(map[*Client]struct{}),
		// 缓冲适度：注册/注销是低频事件，广播是高频事件。
		registerCh:   make(chan *Client, 64),
		unregisterCh: make(chan *Client, 64),
		roomFrameCh:  make(chan roomFrame, 1024),
		wallFrameCh:  make(chan []byte, 512),
		kickCh:       make(chan kickReq, 128),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start 启动事件循环并订阅广播总线。
func (h *Hub) Start(ctx context.Context) error {
	go h.loop()
	// 订阅回调只是往 channel 塞数据，因此可以安全地在 Bus 的 goroutine 中执行。
	return h.bus.Start(ctx,
		func(roomID int64, payload []byte) {
			select {
			case h.roomFrameCh <- roomFrame{roomID: roomID, payload: payload}:
			case <-h.stopCh:
			default:
				// 广播队列积压说明扇出跟不上产出。丢弃并计数，
				// 而不是阻塞 Bus 的 goroutine 让 Redis 连接堆积。
				h.dropped.Add(1)
				logger.Warn("Hub 广播队列已满，丢弃房间帧", "room_id", roomID)
			}
		},
		func(payload []byte) {
			select {
			case h.wallFrameCh <- payload:
			case <-h.stopCh:
			default:
				h.dropped.Add(1)
			}
		})
}

// loop 是唯一操作连接表的 goroutine。
func (h *Hub) loop() {
	defer close(h.doneCh)
	for {
		select {
		case c := <-h.registerCh:
			h.doRegister(c)
		case c := <-h.unregisterCh:
			h.doUnregister(c)
		case req := <-h.kickCh:
			h.doKick(req)
		case f := <-h.roomFrameCh:
			h.fanout(h.roomClients(f.roomID), f.payload)
		case p := <-h.wallFrameCh:
			h.fanout(h.wall, p)
		case <-h.stopCh:
			h.closeAll()
			return
		}
	}
}

func (h *Hub) roomClients(roomID int64) map[*Client]struct{} {
	if roomID == wallRoomID {
		return h.wall
	}
	return h.rooms[roomID]
}

func (h *Hub) doRegister(c *Client) {
	if c.roomID == wallRoomID {
		h.wall[c] = struct{}{}
	} else {
		set, ok := h.rooms[c.roomID]
		if !ok {
			set = make(map[*Client]struct{}, 8)
			h.rooms[c.roomID] = set
		}
		set[c] = struct{}{}
	}
	n := h.connections.Add(1)
	logger.Debug("WS 连接注册", "room_id", c.roomID, "user_id", c.userID, "total", n)
}

func (h *Hub) doUnregister(c *Client) {
	if c.roomID == wallRoomID {
		if _, ok := h.wall[c]; !ok {
			return
		}
		delete(h.wall, c)
	} else {
		set, ok := h.rooms[c.roomID]
		if !ok {
			return
		}
		if _, ok := set[c]; !ok {
			return
		}
		delete(set, c)
		// 空房间及时回收，否则长期运行会积累大量空 map。
		if len(set) == 0 {
			delete(h.rooms, c.roomID)
		}
	}
	h.connections.Add(-1)
	c.close()
	logger.Debug("WS 连接注销", "room_id", c.roomID, "user_id", c.userID)
}

func (h *Hub) doKick(req kickReq) {
	// kick 路径会打日志，而单元测试可能没调用 logger.Init。
	// logger.L() 在未初始化时返回丢弃型 Logger，因此这里不会 nil panic
	// （KB [Go][WS] 记录过这个坑）。
	logger.Warn("踢掉 WS 连接",
		"room_id", req.client.roomID, "user_id", req.client.userID, "reason", req.reason)
	h.dropped.Add(1)
	h.doUnregister(req.client)
}

// fanout 把一帧投递给一组客户端。
//
// 这里直接调用 trySend 而不是 Client.sendEnvelope：sendEnvelope 在失败时会
// 往 kickCh 发消息，而本函数运行在 loop goroutine（kickCh 的唯一读者）内，
// 那样会自己等自己，直接死锁。失败连接在这里就地摘除。
func (h *Hub) fanout(set map[*Client]struct{}, payload []byte) {
	if len(set) == 0 {
		return
	}
	var slow []*Client
	for c := range set {
		if c.trySend(payload) {
			h.delivered.Add(1)
			continue
		}
		slow = append(slow, c)
	}
	// 摘除必须在遍历之后：Go 允许遍历中删除，但这里还要调用 close()，
	// 分开写更清楚也更安全。
	for _, c := range slow {
		logger.Warn("WS 客户端消费过慢被踢",
			"room_id", c.roomID, "user_id", c.userID, "buffer", cap(c.send))
		h.dropped.Add(1)
		h.doUnregister(c)
	}
}

func (h *Hub) closeAll() {
	total := 0
	for _, set := range h.rooms {
		for c := range set {
			c.close()
			total++
		}
	}
	for c := range h.wall {
		c.close()
		total++
	}
	h.rooms = make(map[int64]map[*Client]struct{})
	h.wall = make(map[*Client]struct{})
	h.connections.Store(0)
	logger.Info("Hub 已关闭全部连接", "count", total)
}

// register 把新连接交给 loop。
func (h *Hub) register(c *Client) {
	select {
	case h.registerCh <- c:
	case <-h.stopCh:
		c.close()
	}
}

// unregister 把连接从 loop 中摘除。
func (h *Hub) unregister(c *Client) {
	select {
	case h.unregisterCh <- c:
	case <-h.stopCh:
		c.close()
	}
}

// kick 由客户端 goroutine 调用，请求踢掉自己。
func (h *Hub) kick(c *Client, reason string) {
	select {
	case h.kickCh <- kickReq{client: c, reason: reason}:
	case <-h.stopCh:
		c.close()
	default:
		// kickCh 满了就直接本地关闭。连接最终会由 readPump 退出时的
		// unregister 清理，不会泄漏。
		c.close()
	}
}

// Shutdown 优雅关闭：停止接受新连接、向客户端发 close frame、等待循环退出。
func (h *Hub) Shutdown(timeout time.Duration) {
	h.stopOnce.Do(func() { close(h.stopCh) })
	select {
	case <-h.doneCh:
	case <-time.After(timeout):
		logger.Warn("Hub 关闭超时，强制返回")
	}
	h.bus.Stop()
}

// Stats 返回运行指标。
func (h *Hub) Stats() HubStats {
	return HubStats{
		Connections: h.connections.Load(),
		Delivered:   h.delivered.Load(),
		Dropped:     h.dropped.Load(),
	}
}

// ---------------------------------------------- service.Publisher 实现

// PublishRoom 把事件发到房间频道。本节点的连接也是通过订阅回来才收到，
// 因此单副本与多副本的代码路径完全一致。
func (h *Hub) PublishRoom(ctx context.Context, roomID int64, env model.Envelope) {
	payload, err := Marshal(env)
	if err != nil {
		logger.C(ctx).Error("房间事件序列化失败", "room_id", roomID, "type", env.Type, "error", err)
		return
	}
	h.bus.PublishRoom(ctx, roomID, payload)
}

// PublishWall 把事件发到墙频道。
func (h *Hub) PublishWall(ctx context.Context, env model.Envelope) {
	payload, err := Marshal(env)
	if err != nil {
		logger.C(ctx).Error("墙事件序列化失败", "type", env.Type, "error", err)
		return
	}
	h.bus.PublishWall(ctx, payload)
}

// OnlineUserIDs 返回房间在线用户（跨节点）。
func (h *Hub) OnlineUserIDs(ctx context.Context, roomID int64) []int64 {
	return h.presence.List(ctx, roomID)
}
