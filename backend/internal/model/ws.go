package model

// WS 消息信封与事件类型定义。
//
// 放在 model 而不是 ws 包里，是为了打断依赖环：service 层需要构造广播载荷，
// ws 层需要解析与扇出，两者都依赖 model，但 service 不依赖 ws。

// Envelope 是所有 WebSocket 帧的统一外层结构。
type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// 客户端 → 服务端
const (
	WSChatSend  = "chat.send" // 发送消息
	WSChatPull  = "chat.pull" // 携带游标拉取历史
	WSChatAck   = "chat.ack"  // 上报已读水位
	WSPing      = "ping"      // 应用层心跳
	WSPresenceQ = "presence.query"
)

// 服务端 → 客户端
const (
	WSChatMessage  = "chat.message"  // 单条新消息
	WSChatBackfill = "chat.backfill" // 历史补齐
	WSRoomSlot     = "room.slot"     // 席位快照变更
	WSRoomStatus   = "room.status"   // 房间状态变更
	WSPresence     = "presence"      // 在线成员
	WSError        = "error"         // 错误
	WSPong         = "pong"
	WSHello        = "hello" // 握手完成后的首帧，携带房间与自身信息
)

// NewEnvelope 构造信封。
func NewEnvelope(t string, data any) Envelope { return Envelope{Type: t, Data: data} }

// WSError 载荷。
type WSErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ChatSendData 是客户端发消息的载荷。
type ChatSendData struct {
	Content     string  `json:"content"`
	MsgType     MsgType `json:"msg_type"`
	TagCode     string  `json:"tag_code"`
	ClientMsgID string  `json:"client_msg_id"`
}

// ChatPullData 是客户端拉取历史的载荷。
type ChatPullData struct {
	LastSeenSeq int64 `json:"last_seen_seq"`
}

// ChatAckData 是客户端上报已读的载荷。
type ChatAckData struct {
	Seq int64 `json:"seq"`
}

// PresenceData 是在线成员载荷。Users 永不为 nil。
type PresenceData struct {
	RoomID int64   `json:"room_id"`
	Count  int     `json:"count"`
	Users  []int64 `json:"users"`
}

// RoomStatusData 是房间状态变更载荷。
type RoomStatusData struct {
	RoomID      int64      `json:"room_id"`
	Status      RoomStatus `json:"status"`
	StatusLabel string     `json:"status_label"`
	Event       string     `json:"event"`
	Reason      string     `json:"reason"`
}

// HelloData 是连接建立后的首帧。
type HelloData struct {
	RoomID     int64  `json:"room_id"`
	UserID     int64  `json:"user_id"`
	LatestSeq  int64  `json:"latest_seq"`
	CursorSeq  int64  `json:"cursor_seq"`
	ServerTime string `json:"server_time"`
}
