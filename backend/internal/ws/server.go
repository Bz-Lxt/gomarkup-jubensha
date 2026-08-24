package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// maxIncomingFrame 是入站帧的字节上限。500 字文本 + 信封开销，8KB 绰绰有余。
// 设上限是为了防止恶意客户端用一个巨帧打爆内存。
const maxIncomingFrame = 8 << 10

// Server 负责 HTTP → WebSocket 升级，以及入站帧的解析与派发。
type Server struct {
	hub      *Hub
	cfg      *config.Config
	chat     *service.ChatService
	upgrader websocket.Upgrader
}

// NewServer 构造 WS 服务端。allowedOrigins 为空表示放行（开发便利），
// 非空时严格校验 Origin，防止跨站 WebSocket 劫持。
func NewServer(hub *Hub, cfg *config.Config, chat *service.ChatService) *Server {
	allowed := map[string]bool{}
	for _, o := range cfg.CORSOrigins {
		allowed[o] = true
	}
	return &Server{
		hub:  hub,
		cfg:  cfg,
		chat: chat,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					// 非浏览器客户端（压测脚本、移动端）不带 Origin。
					return true
				}
				if len(allowed) == 0 {
					return true
				}
				return allowed[origin]
			},
		},
	}
}

// ServeRoom 处理房间聊天室连接。调用方必须已完成 JWT 鉴权与成员校验。
func (s *Server) ServeRoom(w http.ResponseWriter, r *http.Request, roomID, userID int64) {
	s.serve(w, r, roomID, userID)
}

// ServeWall 处理拼车墙订阅连接（只收席位广播，不收聊天内容）。
func (s *Server) ServeWall(w http.ResponseWriter, r *http.Request, userID int64) {
	s.serve(w, r, wallRoomID, userID)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, roomID, userID int64) {
	// KB [Go][WS]：如果中间件包装了 ResponseWriter 而没有透传 http.Hijacker，
	// Upgrade 会直接 500。本项目的中间件一律不包装 ResponseWriter，
	// gin.ResponseWriter 原生实现了 Hijacker，因此这里能安全升级。
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.C(r.Context()).Warn("WS 升级失败",
			"room_id", roomID, "user_id", userID, "error", err)
		return
	}

	client := newClient(s.hub, conn, roomID, userID, s.cfg.WSSendBuffer)
	conn.SetReadLimit(maxIncomingFrame)
	_ = conn.SetReadDeadline(timeutil.Now().Add(s.cfg.WSPongWait))
	conn.SetPongHandler(func(string) error {
		// 收到 pong 就把读超时往后推。60s 内没有任何 pong 即判定断连。
		return conn.SetReadDeadline(timeutil.Now().Add(s.cfg.WSPongWait))
	})

	s.hub.register(client)
	go client.writePump(s.cfg.WSPingInterval, s.cfg.WSWriteWait)

	ctx := logger.WithUserID(context.Background(), userID)
	// 先登记在线，再发 hello。反过来的话，客户端收到 hello 后立刻查询
	// presence 有可能查不到自己——而 hello 正是它判断「我已入场」的信号。
	// 登记在广播之前，广播才能带上自己。
	if roomID != wallRoomID {
		s.hub.presence.Touch(ctx, roomID, userID)
	}
	s.sendHello(ctx, client)
	if roomID != wallRoomID {
		s.broadcastPresence(ctx, roomID)
	}

	s.readPump(ctx, client)
}

// sendHello 发送握手首帧，让前端立刻知道当前水位与自己的游标。
func (s *Server) sendHello(ctx context.Context, c *Client) {
	data := model.HelloData{
		RoomID:     c.roomID,
		UserID:     c.userID,
		ServerTime: timeutil.Now().Format(time.RFC3339),
	}
	if c.roomID != wallRoomID {
		if latest, err := s.chat.LatestSeq(ctx, c.roomID); err == nil {
			data.LatestSeq = latest
		}
		if cursor, err := s.chat.Cursor(ctx, c.roomID, c.userID); err == nil {
			data.CursorSeq = cursor
		}
	}
	c.sendEnvelope(model.NewEnvelope(model.WSHello, data))
}

// readPump 是入站帧的唯一读者。退出即代表连接结束。
func (s *Server) readPump(ctx context.Context, c *Client) {
	limiter := newRateLimiter(s.cfg.RateChatPerMin, time.Minute)

	defer func() {
		if c.roomID != wallRoomID {
			s.hub.presence.Leave(ctx, c.roomID, c.userID)
			s.broadcastPresence(ctx, c.roomID)
		}
		s.hub.unregister(c)
		c.close()
	}()

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				logger.C(ctx).Debug("WS 异常断开",
					"room_id", c.roomID, "user_id", c.userID, "error", err)
			}
			return
		}

		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			c.sendError(string(apperr.CodeWSPayloadInvalid), "消息不是合法的 JSON")
			continue
		}

		// 心跳与墙订阅都不需要走业务逻辑，先处理掉。
		if env.Type == model.WSPing {
			c.sendEnvelope(model.NewEnvelope(model.WSPong, struct{}{}))
			if c.roomID != wallRoomID {
				s.hub.presence.Touch(ctx, c.roomID, c.userID)
			}
			continue
		}
		if c.roomID == wallRoomID {
			c.sendError(string(apperr.CodeUnknownWSType), "墙订阅连接只接受心跳")
			continue
		}

		s.dispatch(ctx, c, env.Type, env.Data, limiter)
	}
}

func (s *Server) dispatch(ctx context.Context, c *Client, msgType string, data json.RawMessage, limiter *rateLimiter) {
	switch msgType {
	case model.WSChatSend:
		if !limiter.allow() {
			c.sendError(string(apperr.CodeRateLimited), "发得太快了，慢一点")
			return
		}
		var in model.ChatSendData
		if err := json.Unmarshal(data, &in); err != nil {
			c.sendError(string(apperr.CodeWSPayloadInvalid), "消息体格式不正确")
			return
		}
		if _, err := s.chat.Send(ctx, c.roomID, c.userID, in); err != nil {
			e := apperr.From(err)
			c.sendError(string(e.Code), e.Message)
		}
		// 成功不在这里回帧：消息会通过 Bus 绕一圈广播回来，
		// 发送者用 client_msg_id 把乐观气泡替换成服务端版本。

	case model.WSChatPull:
		var in model.ChatPullData
		if err := json.Unmarshal(data, &in); err != nil {
			c.sendError(string(apperr.CodeWSPayloadInvalid), "拉取参数格式不正确")
			return
		}
		bf, err := s.chat.Backfill(ctx, c.roomID, c.userID, in.LastSeenSeq)
		if err != nil {
			e := apperr.From(err)
			c.sendError(string(e.Code), e.Message)
			return
		}
		c.sendEnvelope(model.NewEnvelope(model.WSChatBackfill, bf))

	case model.WSChatAck:
		var in model.ChatAckData
		if err := json.Unmarshal(data, &in); err != nil {
			c.sendError(string(apperr.CodeWSPayloadInvalid), "已读参数格式不正确")
			return
		}
		if err := s.chat.Ack(ctx, c.roomID, c.userID, in.Seq); err != nil {
			e := apperr.From(err)
			c.sendError(string(e.Code), e.Message)
		}

	case model.WSPresenceQ:
		s.sendPresenceTo(ctx, c)

	default:
		c.sendError(string(apperr.CodeUnknownWSType), "不认识的消息类型: "+msgType)
	}
}

func (s *Server) broadcastPresence(ctx context.Context, roomID int64) {
	users := s.hub.presence.List(ctx, roomID)
	s.hub.PublishRoom(ctx, roomID, model.NewEnvelope(model.WSPresence, model.PresenceData{
		RoomID: roomID, Count: len(users), Users: users,
	}))
}

func (s *Server) sendPresenceTo(ctx context.Context, c *Client) {
	users := s.hub.presence.List(ctx, c.roomID)
	c.sendEnvelope(model.NewEnvelope(model.WSPresence, model.PresenceData{
		RoomID: c.roomID, Count: len(users), Users: users,
	}))
}
