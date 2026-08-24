package model

import "time"

// Message 是房内聊天消息。
//
// Seq 是「房间内」单调递增序号（不是全局），由 rooms.msg_seq 在同一事务中
// UPDATE ... RETURNING 原子发号，配合 UNIQUE(room_id, seq) 保证不重不跳。
type Message struct {
	ID       int64   `json:"id"`
	RoomID   int64   `json:"room_id"`
	Seq      int64   `json:"seq"`
	SenderID *int64  `json:"sender_id"` // 系统消息为 null
	MsgType  MsgType `json:"msg_type"`
	Content  string  `json:"content"`

	// TagCode 仅当 MsgType == MsgTag 时有值，前端据此渲染标签气泡。
	TagCode string `json:"tag_code"`

	// SenderName / SenderAvatar 是查询时 JOIN 出来的冗余展示字段，
	// 避免前端为每条消息再拉一次用户资料。
	SenderName   string `json:"sender_name"`
	SenderAvatar string `json:"sender_avatar"`

	// ClientMsgID 回显客户端生成的幂等 ID，用于前端乐观气泡的去重替换。
	ClientMsgID string    `json:"client_msg_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// Backfill 是重连后的历史消息补齐结果。
//
// Truncated 为 true 表示触发了「全量降级」：客户端落后太多，
// 服务端只回最近 BackfillMax 条，前端需展示历史断层分隔条。
type Backfill struct {
	Messages   []Message `json:"messages"`
	FromSeq    int64     `json:"from_seq"`
	ToSeq      int64     `json:"to_seq"`
	LatestSeq  int64     `json:"latest_seq"`
	Truncated  bool      `json:"truncated"`
	TotalGap   int64     `json:"total_gap"`
	UnreadHint int       `json:"unread_hint"`
}

// NewBackfill 构造补齐结果，保证 Messages 永不为 nil（KB [Go][JSON]）。
func NewBackfill(msgs []Message, fromSeq, toSeq, latestSeq, gap int64, truncated bool) *Backfill {
	if msgs == nil {
		msgs = []Message{}
	}
	return &Backfill{
		Messages:   msgs,
		FromSeq:    fromSeq,
		ToSeq:      toSeq,
		LatestSeq:  latestSeq,
		Truncated:  truncated,
		TotalGap:   gap,
		UnreadHint: len(msgs),
	}
}

// SystemMessageKind 枚举系统消息的语义类别，前端据此选图标。
type SystemMessageKind string

const (
	SysJoin     SystemMessageKind = "member_join"
	SysLeave    SystemMessageKind = "member_leave"
	SysKick     SystemMessageKind = "member_kick"
	SysRelease  SystemMessageKind = "hold_release"
	SysLocked   SystemMessageKind = "room_locked"
	SysReopen   SystemMessageKind = "room_reopen"
	SysExpired  SystemMessageKind = "room_expired"
	SysCancel   SystemMessageKind = "room_cancel"
	SysStarting SystemMessageKind = "room_starting"
	SysStarted  SystemMessageKind = "room_started"
	SysFinished SystemMessageKind = "room_finished"
)
