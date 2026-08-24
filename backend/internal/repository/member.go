package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// MemberRepo 是房间成员的数据访问。
type MemberRepo struct{}

func NewMemberRepo() *MemberRepo { return &MemberRepo{} }

const memberCols = `id, room_id, user_id, seat_gender, status, is_owner,
	hold_expires_at, joined_at, left_at, created_at, updated_at`

func scanMember(row interface{ Scan(...any) error }) (*model.RoomMember, error) {
	var m model.RoomMember
	err := row.Scan(&m.ID, &m.RoomID, &m.UserID, &m.SeatGender, &m.Status, &m.IsOwner,
		&m.HoldExpiresAt, &m.JoinedAt, &m.LeftAt, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = timeutil.In(m.CreatedAt)
	m.UpdatedAt = timeutil.In(m.UpdatedAt)
	m.HoldExpiresAt = inPtr(m.HoldExpiresAt)
	m.JoinedAt = inPtr(m.JoinedAt)
	m.LeftAt = inPtr(m.LeftAt)
	return &m, nil
}

func inPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := timeutil.In(*t)
	return &v
}

// Insert 新建成员记录（对应 fsm.EvHold 的「从无到有」）。
//
// 唯一索引 uq_members_active 会在同一用户重复占位时抛 23505，
// 调用方据此判定幂等命中，而不是先 SELECT 再 INSERT（那会有竞态窗口）。
func (r *MemberRepo) Insert(ctx context.Context, q Querier, m *model.RoomMember) error {
	now := timeutil.Now()
	err := q.QueryRowContext(ctx, `
		INSERT INTO room_members (room_id, user_id, seat_gender, status, is_owner,
			hold_expires_at, joined_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`,
		m.RoomID, m.UserID, m.SeatGender, m.Status, m.IsOwner,
		m.HoldExpiresAt, m.JoinedAt, now, now,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert room member: %w", err)
	}
	m.CreatedAt = timeutil.In(m.CreatedAt)
	m.UpdatedAt = timeutil.In(m.UpdatedAt)
	return nil
}

// GetActive 取该用户在该房间的「占席位」记录。查不到返回 ErrNoRows。
func (r *MemberRepo) GetActive(ctx context.Context, q Querier, roomID, userID int64) (*model.RoomMember, error) {
	m, err := scanMember(q.QueryRowContext(ctx, `
		SELECT `+memberCols+` FROM room_members
		 WHERE room_id = $1 AND user_id = $2
		   AND status = ANY($3)`,
		roomID, userID, activeStatuses()))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("get active member: %w", err)
	}
	return m, nil
}

// LockActiveForUpdate 在事务内锁定该用户的活跃成员行，用于退车/踢人路径。
func (r *MemberRepo) LockActiveForUpdate(ctx context.Context, q Querier, roomID, userID int64) (*model.RoomMember, error) {
	m, err := scanMember(q.QueryRowContext(ctx, `
		SELECT `+memberCols+` FROM room_members
		 WHERE room_id = $1 AND user_id = $2 AND status = ANY($3)
		 FOR UPDATE`,
		roomID, userID, activeStatuses()))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("lock active member: %w", err)
	}
	return m, nil
}

// activeStatuses 与 model.MemberStatus.OccupiesSeat() 必须保持一致。
// 两者一旦分叉，席位账目就会漂移，因此这里只有一处定义。
func activeStatuses() []string {
	out := []string{}
	for _, s := range []model.MemberStatus{
		model.MemberPending, model.MemberJoined, model.MemberReleased,
		model.MemberLeft, model.MemberKicked, model.MemberCheckedIn,
	} {
		if s.OccupiesSeat() {
			out = append(out, string(s))
		}
	}
	return out
}

// UpdateStatus 流转成员状态。带 from 条件，防止并发下的状态覆盖。
func (r *MemberRepo) UpdateStatus(ctx context.Context, q Querier, memberID int64,
	from, to model.MemberStatus) (bool, error) {

	now := timeutil.Now()
	// joinedAt / leftAt 用 any 而非 time.Time：nil 时配合 COALESCE 保留原值，
	// 避免「退车」把之前的 joined_at 抹掉，那会让审计断链。
	var joinedAt, leftAt any
	switch to {
	case model.MemberJoined:
		joinedAt = now
	case model.MemberReleased, model.MemberLeft, model.MemberKicked:
		leftAt = now
	}
	// 只有 PENDING 才需要 TTL。离开 PENDING 就必须清空，否则回收器
	// 会把已经转正的成员当成超时占位再处理一遍。
	clearHold := to != model.MemberPending

	res, err := q.ExecContext(ctx, `
		UPDATE room_members
		   SET status          = $1,
		       joined_at       = COALESCE($2, joined_at),
		       left_at         = COALESCE($3, left_at),
		       hold_expires_at = CASE WHEN $4 THEN NULL ELSE hold_expires_at END,
		       updated_at      = $5
		 WHERE id = $6 AND status = $7`,
		to, joinedAt, leftAt, clearHold, now, memberID, from)
	if err != nil {
		return false, fmt.Errorf("update member status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update member status rows: %w", err)
	}
	return n == 1, nil
}

// ListByRoom 列出房间的全部成员记录（含历史），按创建时间升序。
func (r *MemberRepo) ListByRoom(ctx context.Context, q Querier, roomID int64, onlyActive bool) ([]*model.RoomMember, error) {
	query := `SELECT ` + memberCols + ` FROM room_members WHERE room_id = $1`
	args := []any{roomID}
	if onlyActive {
		query += ` AND status = ANY($2)`
		args = append(args, activeStatuses())
	}
	query += ` ORDER BY is_owner DESC, created_at ASC`

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list room members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*model.RoomMember{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("scan room member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate room members: %w", err)
	}
	return out, nil
}

// ListRoomIDsByUser 返回该用户参与中的房间 ID，用于「我的车」。
func (r *MemberRepo) ListRoomIDsByUser(ctx context.Context, q Querier, userID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT room_id FROM room_members
		 WHERE user_id = $1 AND status = ANY($2)`,
		userID, activeStatuses())
	if err != nil {
		return nil, fmt.Errorf("list user room ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan user room id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user room ids: %w", err)
	}
	return out, nil
}

// IsActiveMember 是 WS 握手鉴权的判据：只有占着席位的人才能进房间聊天室。
func (r *MemberRepo) IsActiveMember(ctx context.Context, q Querier, roomID, userID int64) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM room_members
			 WHERE room_id = $1 AND user_id = $2 AND status = ANY($3)
		)`, roomID, userID, activeStatuses()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check active member: %w", err)
	}
	return exists, nil
}

// ExpiredHold 是一条超时占位的轻量投影。
type ExpiredHold struct {
	MemberID   int64
	RoomID     int64
	UserID     int64
	SeatGender model.SeatGender
}

// ListExpiredHolds 查出 TTL 已到期的占位记录，供 Scheduler 回收。
//
// 这里刻意不用 FOR UPDATE SKIP LOCKED：回收动作本身会重新走完整的
// SlotGuard + FOR UPDATE 流程，本查询只是「候选清单」。
// 清单里的条目在真正回收时可能已被用户主动退车，届时状态条件会拦住重复处理，
// 这就是幂等（审核维度 8 的要求）。
func (r *MemberRepo) ListExpiredHolds(ctx context.Context, q Querier, now time.Time, limit int) ([]ExpiredHold, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, room_id, user_id, seat_gender FROM room_members
		 WHERE status = $1 AND hold_expires_at IS NOT NULL AND hold_expires_at <= $2
		 ORDER BY hold_expires_at ASC
		 LIMIT $3`, string(model.MemberPending), now, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired holds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ExpiredHold{}
	for rows.Next() {
		var h ExpiredHold
		if err := rows.Scan(&h.MemberID, &h.RoomID, &h.UserID, &h.SeatGender); err != nil {
			return nil, fmt.Errorf("scan expired hold: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired holds: %w", err)
	}
	return out, nil
}

// CountActiveByRoom 直接从成员表统计实际占席位人数。
//
// 这不是给业务路径用的，而是给 NFR-1 A-5 的账目一致性校验用的：
// 拿它和 rooms.joined_count + pending_count 对账，任何漂移都能被发现。
func (r *MemberRepo) CountActiveByRoom(ctx context.Context, q Querier, roomID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM room_members
		 WHERE room_id = $1 AND status = ANY($2)`, roomID, activeStatuses()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active members: %w", err)
	}
	return n, nil
}
