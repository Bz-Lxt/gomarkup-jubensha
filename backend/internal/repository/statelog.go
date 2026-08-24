package repository

import (
	"context"
	"fmt"

	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// StateLogRepo 是状态机流转的审计日志访问层。只追加，不修改，不删除。
type StateLogRepo struct{}

func NewStateLogRepo() *StateLogRepo { return &StateLogRepo{} }

// Append 写入一条流转记录。
//
// 每次状态机流转都必须调用（Requirements TR-2 硬性约束）。审计的价值在
// 「炸车纠纷」场景：谁什么时候退的车、系统什么时候判定人数不足，都要能追溯。
func (r *StateLogRepo) Append(ctx context.Context, q Querier, l *model.StateLog) error {
	err := q.QueryRowContext(ctx, `
		INSERT INTO room_state_logs (room_id, member_id, scope, from_status, to_status, event, actor_id, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at`,
		l.RoomID, l.MemberID, l.Scope, l.FromStatus, l.ToStatus, l.Event, l.ActorID, l.Reason, timeutil.Now(),
	).Scan(&l.ID, &l.CreatedAt)
	if err != nil {
		return fmt.Errorf("append state log: %w", err)
	}
	l.CreatedAt = timeutil.In(l.CreatedAt)
	return nil
}

// ListByRoom 读取房间的流转历史，最新在前。
func (r *StateLogRepo) ListByRoom(ctx context.Context, q Querier, roomID int64, limit int) ([]model.StateLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, room_id, member_id, scope, from_status, to_status, event, actor_id, reason, created_at
		  FROM room_state_logs
		 WHERE room_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, roomID, limit)
	if err != nil {
		return nil, fmt.Errorf("list state logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []model.StateLog{}
	for rows.Next() {
		var l model.StateLog
		if err := rows.Scan(&l.ID, &l.RoomID, &l.MemberID, &l.Scope, &l.FromStatus,
			&l.ToStatus, &l.Event, &l.ActorID, &l.Reason, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan state log: %w", err)
		}
		l.CreatedAt = timeutil.In(l.CreatedAt)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate state logs: %w", err)
	}
	return out, nil
}
