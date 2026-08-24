// Package ws 是手写的多房间 WebSocket Hub（Requirements TR-3）。
//
// 刻意不引入 socket.io / centrifugo 一类的成品消息层：连接注册、房间隔离、
// 广播扇出、心跳、背压、优雅关闭全部在本包内实现。跨节点可达性由
// Redis Pub/Sub（bus.go）提供，因此 `--scale backend=2` 无需改代码。
package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
)

// Client 是一条 WebSocket 连接。
//
// 并发模型：每个 Client 恰好有两个 goroutine —— 一个 readPump，一个 writePump。
// gorilla/websocket 禁止并发写同一个连接，因此**所有**出站帧都必须经由
// send channel 交给 writePump 串行发送，任何地方都不得直接调用 conn.WriteMessage。
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	roomID int64
	userID int64

	// send 是出站缓冲。容量由 WS_SEND_BUFFER 控制（默认 256）。
	// 写满意味着这个客户端消费不过来，会被踢掉而不是拖慢整个房间。
	send chan []byte

	closeOnce sync.Once
	// closed 在关闭后被 close，用于让 trySend 立刻放弃而不是阻塞。
	closed chan struct{}
}

func newClient(hub *Hub, conn *websocket.Conn, roomID, userID int64, buf int) *Client {
	if buf <= 0 {
		buf = 256
	}
	return &Client{
		hub:    hub,
		conn:   conn,
		roomID: roomID,
		userID: userID,
		send:   make(chan []byte, buf),
		closed: make(chan struct{}),
	}
}

// UserID 返回连接所属用户。
func (c *Client) UserID() int64 { return c.userID }

// RoomID 返回连接所属房间。
func (c *Client) RoomID() int64 { return c.roomID }

// trySend 非阻塞投递。返回 false 表示缓冲已满，调用方应踢掉该连接。
//
// 这是背压策略的核心（NFR-2 B-6）：宁可牺牲一个慢消费者，
// 也不能让它阻塞房间广播、把整个房间拖死。
func (c *Client) trySend(payload []byte) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	select {
	case c.send <- payload:
		return true
	default:
		return false
	}
}

// close 幂等地关闭连接。
//
// sync.Once 是必需的：unregister 路径、readPump 退出、writePump 退出、
// Hub 优雅关闭都可能调用它。KB [Go][WAL] 记录过重复 close channel 的 panic。
func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		// conn 判空：单元测试会构造不带真实连接的 Client 来验证 Hub 的
		// 扇出与背压语义。这与 KB [Go][WS] 那条「kick 路径必须判空，
		// 否则测试里 nil panic」是同一类防御。
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}

// writePump 独占 conn 的写端，并负责发送心跳 ping。
func (c *Client) writePump(pingInterval, writeWait time.Duration) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				logger.Debug("WS 写入失败，关闭连接",
					"room_id", c.roomID, "user_id", c.userID, "error", err)
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.closed:
			// 尽最大努力发一个 close frame，让浏览器知道是正常关闭而不是网络故障。
			_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server closing"))
			return
		}
	}
}

// sendEnvelope 序列化后投递。序列化失败只记日志，不影响连接。
func (c *Client) sendEnvelope(env model.Envelope) {
	payload, err := json.Marshal(env)
	if err != nil {
		logger.Error("WS 信封序列化失败", "type", env.Type, "error", err)
		return
	}
	if !c.trySend(payload) {
		c.hub.kick(c, "send buffer full")
	}
}

// sendError 向客户端回一个错误帧。
func (c *Client) sendError(code, message string) {
	c.sendEnvelope(model.NewEnvelope(model.WSError, model.WSErrorData{Code: code, Message: message}))
}
