package repository

import (
	"context"
	"fmt"

	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// MessageRepo 是房内消息与阅读游标的数据访问。
type MessageRepo struct{}

func NewMessageRepo() *MessageRepo { return &MessageRepo{} }

// 查询一律 JOIN 用户表带出昵称与头像，避免前端为每条消息再拉一次资料。
// COALESCE 处理两种情况：系统消息 sender_id 为 NULL，以及用户被删除后
// ON DELETE SET NULL 留下的孤儿消息。
const messageSelect = `
	SELECT m.id, m.room_id, m.seq, m.sender_id, m.msg_type, m.content, m.tag_code,
	       m.client_msg_id, m.created_at,
	       COALESCE(NULLIF(u.nickname, ''), u.username, '') AS sender_name,
	       COALESCE(u.avatar, '') AS sender_avatar
	  FROM room_messages m
	  LEFT JOIN users u ON u.id = m.sender_id`

func scanMessage(row interface{ Scan(...any) error }) (*model.Message, error) {
	var m model.Message
	err := row.Scan(&m.ID, &m.RoomID, &m.Seq, &m.SenderID, &m.MsgType, &m.Content,
		&m.TagCode, &m.ClientMsgID, &m.CreatedAt, &m.SenderName, &m.SenderAvatar)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = timeutil.In(m.CreatedAt)
	if m.MsgType == model.MsgSystem {
		m.SenderName = "系统"
	}
	return &m, nil
}

// Insert 落库一条消息。seq 必须由 RoomRepo.NextMsgSeq 在同一事务内发号。
func (r *MessageRepo) Insert(ctx context.Context, q Querier, m *model.Message) error {
	now := timeutil.Now()
	err := q.QueryRowContext(ctx, `
		INSERT INTO room_messages (room_id, seq, sender_id, msg_type, content, tag_code, client_msg_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`,
		m.RoomID, m.Seq, m.SenderID, m.MsgType, m.Content, m.TagCode, m.ClientMsgID, now,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	m.CreatedAt = timeutil.In(m.CreatedAt)
	return nil
}

// GetByClientMsgID 用于客户端重发去重。
func (r *MessageRepo) GetByClientMsgID(ctx context.Context, q Querier,
	roomID, senderID int64, clientMsgID string) (*model.Message, error) {

	if clientMsgID == "" {
		return nil, ErrNoRows
	}
	m, err := scanMessage(q.QueryRowContext(ctx, messageSelect+`
		 WHERE m.room_id = $1 AND m.sender_id = $2 AND m.client_msg_id = $3`,
		roomID, senderID, clientMsgID))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("get message by client id: %w", err)
	}
	return m, nil
}

// ListRange 取 (fromSeq, toSeq] 区间的消息，升序返回。用于增量拉取。
func (r *MessageRepo) ListRange(ctx context.Context, q Querier, roomID, fromSeq, toSeq int64, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.QueryContext(ctx, messageSelect+`
		 WHERE m.room_id = $1 AND m.seq > $2 AND m.seq <= $3
		 ORDER BY m.seq ASC
		 LIMIT $4`, roomID, fromSeq, toSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list message range: %w", err)
	}
	return collectMessages(rows)
}

// ListLatest 取最近 limit 条消息，返回时已按 seq 升序（便于前端直接追加渲染）。
// 用于「全量降级」路径。
func (r *MessageRepo) ListLatest(ctx context.Context, q Querier, roomID int64, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.QueryContext(ctx, messageSelect+`
		 WHERE m.room_id = $1
		 ORDER BY m.seq DESC
		 LIMIT $2`, roomID, limit)
	if err != nil {
		return nil, fmt.Errorf("list latest messages: %w", err)
	}
	msgs, err := collectMessages(rows)
	if err != nil {
		return nil, err
	}
	// DESC 取到最近 N 条后就地反转为 ASC，比在 SQL 里套子查询更直白。
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func collectMessages(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]model.Message, error) {
	defer func() { _ = rows.Close() }()
	out := []model.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}

// HasSystemMessage 判断该房间是否已发过某类系统消息。
//
// 这是 Scheduler 幂等的关键：开局提醒之类的一次性通知，判重依据必须落在
// 数据库里而不是内存或 Redis，否则进程重启后会重复轰炸用户
// （审核维度 8「重启后不重复副作用」）。
func (r *MessageRepo) HasSystemMessage(ctx context.Context, q Querier, roomID int64, kind model.SystemMessageKind) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM room_messages
			 WHERE room_id = $1 AND msg_type = $2 AND tag_code = $3
		)`, roomID, string(model.MsgSystem), string(kind)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has system message: %w", err)
	}
	return exists, nil
}

// LatestSeq 返回房间当前的消息序号水位。
func (r *MessageRepo) LatestSeq(ctx context.Context, q Querier, roomID int64) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(max(seq), 0) FROM room_messages WHERE room_id = $1`, roomID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("latest seq: %w", err)
	}
	return seq, nil
}

// ---------------------------------------------------------------- 阅读游标

// GetCursor 返回用户在该房间的已读水位，无记录时返回 0。
func (r *MessageRepo) GetCursor(ctx context.Context, q Querier, roomID, userID int64) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(
			(SELECT last_seen_seq FROM message_cursors WHERE room_id = $1 AND user_id = $2),
			0)`, roomID, userID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("get cursor: %w", err)
	}
	return seq, nil
}

// UpsertCursor 推进已读水位。
//
// GREATEST 保证游标只前进不后退：网络乱序导致的旧 ack 到达时，
// 不会把已读位置拉回去，否则用户会反复看到「未读」。
func (r *MessageRepo) UpsertCursor(ctx context.Context, q Querier, roomID, userID, seq int64) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO message_cursors (room_id, user_id, last_seen_seq, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (room_id, user_id)
		DO UPDATE SET last_seen_seq = GREATEST(message_cursors.last_seen_seq, EXCLUDED.last_seen_seq),
		              updated_at    = EXCLUDED.updated_at`,
		roomID, userID, seq, timeutil.Now())
	if err != nil {
		return fmt.Errorf("upsert cursor: %w", err)
	}
	return nil
}

// UnreadCount 是单个房间的未读数投影。
type UnreadCount struct {
	RoomID int64 `json:"room_id"`
	Unread int   `json:"unread"`
}

// UnreadByUser 一次性算出用户所有在车房间的未读数，供墙上角标使用。
func (r *MessageRepo) UnreadByUser(ctx context.Context, q Querier, userID int64) ([]UnreadCount, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.id,
		       GREATEST(0, r.msg_seq - COALESCE(c.last_seen_seq, 0))::int AS unread
		  FROM room_members m
		  JOIN rooms r ON r.id = m.room_id
		  LEFT JOIN message_cursors c ON c.room_id = r.id AND c.user_id = m.user_id
		 WHERE m.user_id = $1 AND m.status = ANY($2)`,
		userID, activeStatuses())
	if err != nil {
		return nil, fmt.Errorf("unread by user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []UnreadCount{}
	for rows.Next() {
		var u UnreadCount
		if err := rows.Scan(&u.RoomID, &u.Unread); err != nil {
			return nil, fmt.Errorf("scan unread: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unread: %w", err)
	}
	return out, nil
}
