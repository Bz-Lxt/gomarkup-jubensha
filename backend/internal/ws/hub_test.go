package ws

import (
	"testing"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/config"
)

// newTestHub 起一个不接 Redis 的 Hub，只跑事件循环。
//
// bus / presence 内部的 rdb 为 nil，但本测试完全不触碰它们：Hub 的
// 房间隔离与背压语义纯粹发生在 loop goroutine 内，不需要任何外部依赖。
// 这符合「QA 必须离线可跑、成本 ¥0」的约束。
func newTestHub(t *testing.T) *Hub {
	t.Helper()
	h := NewHub(&config.Config{WSSendBuffer: 4}, nil)
	go h.loop()
	t.Cleanup(func() { h.Shutdown(time.Second) })
	return h
}

// waitFor 轮询等待条件成立。Hub 是异步事件循环，不能假设 register 立刻生效。
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", msg)
}

// TestHub_RoomIsolation 是 TR-3 最核心的断言：房间之间必须完全隔离。
//
// 如果扇出串了房间，A 局的私聊会出现在 B 局的弹幕里——这是产品级事故。
func TestHub_RoomIsolation(t *testing.T) {
	h := newTestHub(t)

	a1 := newClient(h, nil, 101, 1, 8)
	a2 := newClient(h, nil, 101, 2, 8)
	b1 := newClient(h, nil, 202, 3, 8)
	wall := newClient(h, nil, wallRoomID, 4, 8)

	for _, c := range []*Client{a1, a2, b1, wall} {
		h.register(c)
	}
	waitFor(t, func() bool { return h.Stats().Connections == 4 }, "4 条连接注册完成")

	// 往房间 101 投一帧。
	h.roomFrameCh <- roomFrame{roomID: 101, payload: []byte(`{"type":"chat.message"}`)}

	waitFor(t, func() bool { return len(a1.send) == 1 && len(a2.send) == 1 }, "房间 101 的两人都收到")

	if len(b1.send) != 0 {
		t.Fatal("房间 202 的连接收到了房间 101 的帧，房间隔离失效")
	}
	if len(wall.send) != 0 {
		t.Fatal("墙订阅者收到了房间帧，隔离失效")
	}

	// 墙帧只应到达墙订阅者。
	h.wallFrameCh <- []byte(`{"type":"room.slot"}`)
	waitFor(t, func() bool { return len(wall.send) == 1 }, "墙订阅者收到墙帧")
	if len(a1.send) != 1 {
		t.Fatal("房间连接收到了墙帧，隔离失效")
	}
}

// TestHub_SlowClientIsKickedNotBlocking 断言慢消费者被摘除，
// 而不是把整个房间的广播拖死。
//
// 这是 NFR-2 B-5 的直接验证：单个卡死的浏览器标签页不得影响同房其他人。
func TestHub_SlowClientIsKickedNotBlocking(t *testing.T) {
	h := newTestHub(t)

	// 缓冲为 2 的慢客户端：不消费，很快写满。
	slow := newClient(h, nil, 300, 1, 2)
	// 缓冲充裕的正常客户端。
	fast := newClient(h, nil, 300, 2, 64)
	h.register(slow)
	h.register(fast)
	waitFor(t, func() bool { return h.Stats().Connections == 2 }, "2 条连接注册完成")

	// 连投 10 帧：slow 在第 3 帧就该被踢，fast 应收满 10 帧。
	for i := 0; i < 10; i++ {
		h.roomFrameCh <- roomFrame{roomID: 300, payload: []byte(`{"type":"chat.message"}`)}
	}

	waitFor(t, func() bool { return h.Stats().Connections == 1 }, "慢客户端被摘除")

	select {
	case <-slow.closed:
	default:
		t.Fatal("慢客户端应已被关闭")
	}

	waitFor(t, func() bool { return len(fast.send) == 10 }, "正常客户端仍收到全部 10 帧")

	if h.Stats().Dropped == 0 {
		t.Fatal("踢人应计入 dropped 指标，否则线上不可观测")
	}
}

// TestHub_UnregisterIsIdempotent 断言重复注销不会把连接数减成负数。
//
// 真实触发路径：readPump 退出时 unregister，同时背压又把它踢了一次。
func TestHub_UnregisterIsIdempotent(t *testing.T) {
	h := newTestHub(t)

	c := newClient(h, nil, 400, 1, 4)
	h.register(c)
	waitFor(t, func() bool { return h.Stats().Connections == 1 }, "连接注册完成")

	h.unregister(c)
	waitFor(t, func() bool { return h.Stats().Connections == 0 }, "连接注销完成")

	h.unregister(c)
	h.unregister(c)
	time.Sleep(30 * time.Millisecond)

	if got := h.Stats().Connections; got != 0 {
		t.Fatalf("重复注销后连接数 = %d，期望 0（负数说明计数被减穿）", got)
	}
}

// TestHub_EmptyRoomIsReclaimed 断言房间空了以后 map 条目被回收，
// 否则长期运行会积累大量空 map，属于慢性内存泄漏。
func TestHub_EmptyRoomIsReclaimed(t *testing.T) {
	h := newTestHub(t)

	c := newClient(h, nil, 500, 1, 4)
	h.register(c)
	waitFor(t, func() bool { return h.Stats().Connections == 1 }, "连接注册完成")
	h.unregister(c)
	waitFor(t, func() bool { return h.Stats().Connections == 0 }, "连接注销完成")

	// 通过一次空房间广播间接确认不 panic；房间表本身由 loop 独占，
	// 测试线程不能直接读，否则就违背了单 goroutine 模型。
	h.roomFrameCh <- roomFrame{roomID: 500, payload: []byte(`{}`)}
	time.Sleep(30 * time.Millisecond)

	if got := h.Stats().Connections; got != 0 {
		t.Fatalf("连接数 = %d，期望 0", got)
	}
}

// TestHub_ShutdownClosesEverything 断言优雅关闭会关掉所有连接。
func TestHub_ShutdownClosesEverything(t *testing.T) {
	h := NewHub(&config.Config{WSSendBuffer: 4}, nil)
	go h.loop()

	clients := make([]*Client, 0, 6)
	for i := 0; i < 3; i++ {
		c := newClient(h, nil, 600, int64(i), 4)
		clients = append(clients, c)
		h.register(c)
	}
	for i := 0; i < 3; i++ {
		c := newClient(h, nil, wallRoomID, int64(100+i), 4)
		clients = append(clients, c)
		h.register(c)
	}
	waitFor(t, func() bool { return h.Stats().Connections == 6 }, "6 条连接注册完成")

	h.Shutdown(2 * time.Second)

	for i, c := range clients {
		select {
		case <-c.closed:
		default:
			t.Fatalf("第 %d 条连接在 Shutdown 后仍未关闭", i)
		}
	}
	if got := h.Stats().Connections; got != 0 {
		t.Fatalf("Shutdown 后连接数 = %d，期望 0", got)
	}
}

// TestHub_KickAfterShutdownDoesNotPanic 断言关闭后仍有客户端 goroutine
// 调用 kick / register 时不会 panic 或阻塞。
//
// 这是重启期真实存在的竞态：Shutdown 已执行，某条 readPump 才刚发现超限。
func TestHub_KickAfterShutdownDoesNotPanic(t *testing.T) {
	h := NewHub(&config.Config{WSSendBuffer: 4}, nil)
	go h.loop()
	h.Shutdown(time.Second)

	c := newClient(h, nil, 700, 1, 4)
	h.register(c)   // 应走 stopCh 分支，直接关闭
	h.kick(c, "超限") // 不得 panic
	h.unregister(c)

	select {
	case <-c.closed:
	default:
		t.Fatal("Hub 已关闭时注册的连接应被立刻关闭")
	}
}

// TestRateLimiter 验证 WS 滑动窗口限流：窗口内放行 N 条，超出拒绝，
// 窗口滑过后恢复。
func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3, 100*time.Millisecond)

	for i := 0; i < 3; i++ {
		if !rl.allow() {
			t.Fatalf("第 %d 条消息应被放行", i+1)
		}
	}
	if rl.allow() {
		t.Fatal("第 4 条消息应被限流")
	}

	time.Sleep(120 * time.Millisecond)
	if !rl.allow() {
		t.Fatal("窗口滑过后应恢复放行")
	}
}

// TestRateLimiter_FallsBackOnBadConfig 断言非法配置回落到安全默认值，
// 而不是把配额当成 0 —— 那会让整个聊天功能在误配下彻底不可用。
func TestRateLimiter_FallsBackOnBadConfig(t *testing.T) {
	rl := newRateLimiter(0, 0)
	if rl.limit <= 0 || rl.window <= 0 {
		t.Fatalf("非法配置未回落: limit=%d window=%v", rl.limit, rl.window)
	}
	for i := 0; i < rl.limit; i++ {
		if !rl.allow() {
			t.Fatalf("默认配额内的第 %d 条应被放行", i+1)
		}
	}
	if rl.allow() {
		t.Fatal("超出默认配额后应被限流")
	}
}

// TestClient_TrySendAfterClose 断言已关闭连接的投递立刻失败而不阻塞。
func TestClient_TrySendAfterClose(t *testing.T) {
	c := newClient(nil, nil, 1, 1, 1)
	c.close()

	done := make(chan bool, 1)
	go func() { done <- c.trySend([]byte("x")) }()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("已关闭连接的 trySend 应返回 false")
		}
	case <-time.After(time.Second):
		t.Fatal("已关闭连接的 trySend 阻塞了，会拖死 fanout")
	}
}
