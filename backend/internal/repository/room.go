package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// RoomRepo 是房间的数据访问。
type RoomRepo struct{}

func NewRoomRepo() *RoomRepo { return &RoomRepo{} }

const roomCols = `id, owner_id, title, script_name, venue_name, city, address, room_type,
	difficulty, theme, notes, start_at, capacity, min_viable, joined_count, pending_count,
	male_seats, female_seats, any_seats, male_taken, female_taken, any_taken,
	status, msg_seq, created_at, updated_at`

func scanRoom(row interface{ Scan(...any) error }) (*model.Room, error) {
	var r model.Room
	err := row.Scan(
		&r.ID, &r.OwnerID, &r.Title, &r.ScriptName, &r.VenueName, &r.City, &r.Address, &r.RoomType,
		&r.Difficulty, &r.Theme, &r.Notes, &r.StartAt, &r.Capacity, &r.MinViable,
		&r.JoinedCount, &r.PendingCount,
		&r.MaleSeats, &r.FemaleSeats, &r.AnySeats, &r.MaleTaken, &r.FemaleTaken, &r.AnyTaken,
		&r.Status, &r.MsgSeq, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	// 一律转到北京时区再交给上层，避免各处重复 In() 且漏掉一处就错日
	// （KB [Go][TZ]）。
	r.StartAt = timeutil.In(r.StartAt)
	r.CreatedAt = timeutil.In(r.CreatedAt)
	r.UpdatedAt = timeutil.In(r.UpdatedAt)
	return &r, nil
}

// Create 插入房间。
func (r *RoomRepo) Create(ctx context.Context, q Querier, room *model.Room) error {
	now := timeutil.Now()
	err := q.QueryRowContext(ctx, `
		INSERT INTO rooms (owner_id, title, script_name, venue_name, city, address, room_type,
			difficulty, theme, notes, start_at, capacity, min_viable,
			joined_count, pending_count, male_seats, female_seats, any_seats,
			male_taken, female_taken, any_taken, status, msg_seq, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18,
			$19, $20, $21, $22, $23, $24, $25)
		RETURNING id, created_at, updated_at`,
		room.OwnerID, room.Title, room.ScriptName, room.VenueName, room.City, room.Address, room.RoomType,
		room.Difficulty, room.Theme, room.Notes, room.StartAt, room.Capacity, room.MinViable,
		room.JoinedCount, room.PendingCount, room.MaleSeats, room.FemaleSeats, room.AnySeats,
		room.MaleTaken, room.FemaleTaken, room.AnyTaken, room.Status, room.MsgSeq, now, now,
	).Scan(&room.ID, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create room: %w", err)
	}
	room.CreatedAt = timeutil.In(room.CreatedAt)
	room.UpdatedAt = timeutil.In(room.UpdatedAt)
	return nil
}

// GetByID 普通读取（不加锁），用于展示路径。
func (r *RoomRepo) GetByID(ctx context.Context, q Querier, id int64) (*model.Room, error) {
	room, err := scanRoom(q.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE id = $1`, id))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("get room: %w", err)
	}
	return room, nil
}

// LockForUpdate 是 L3 —— 三层锁体系里唯一承担正确性的那一层。
//
// SELECT ... FOR UPDATE 会在事务结束前独占该房间行的写锁，把「读人数 →
// 判断 → 扣减」压缩成一个不可打断的临界区。即使 Redis 锁完全失效
// （NFR-1 A-4 会主动关掉它来验证），并发抢位在这里依然会被串行化。
//
// 必须在 InTx 内调用；在自动提交模式下 FOR UPDATE 拿到的锁会立刻释放，
// 等于没加锁。
func (r *RoomRepo) LockForUpdate(ctx context.Context, q Querier, id int64) (*model.Room, error) {
	room, err := scanRoom(q.QueryRowContext(ctx,
		`SELECT `+roomCols+` FROM rooms WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("lock room for update: %w", err)
	}
	return room, nil
}

// ApplySeatDelta 原子调整席位账目。
//
// 关键点：这条 UPDATE 的 WHERE 子句自带「不会超载」的守卫条件。
// 即使调用方算错了，数据库也只会更新 0 行（返回 false），而不是写入非法状态。
// 再往下还有 CHECK 约束兜底。三重保险，任意一层单独成立即可保证零超载。
//
// deltaJoined / deltaPending / 分桶 delta 由 service 层依据 fsm.SeatDelta 计算。
func (r *RoomRepo) ApplySeatDelta(ctx context.Context, q Querier, roomID int64,
	deltaJoined, deltaPending int, g model.SeatGender, deltaSeat int) (bool, error) {

	seatCol, err := seatColumn(g)
	if err != nil {
		return false, err
	}
	// seatCol 来自白名单映射，不是用户输入，因此拼接是安全的；
	// 其余全部走占位符。
	query := fmt.Sprintf(`
		UPDATE rooms
		   SET joined_count  = joined_count  + $1,
		       pending_count = pending_count + $2,
		       %s            = %s            + $3,
		       updated_at    = $4
		 WHERE id = $5
		   AND joined_count  + $1 >= 0
		   AND pending_count + $2 >= 0
		   AND joined_count + pending_count + $1 + $2 <= capacity
		   AND %s + $3 >= 0
		   AND %s + $3 <= %s`, seatCol, seatCol, seatCol, seatCol, seatQuotaColumn(g))

	res, err := q.ExecContext(ctx, query,
		deltaJoined, deltaPending, deltaSeat, timeutil.Now(), roomID)
	if err != nil {
		return false, fmt.Errorf("apply seat delta: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("apply seat delta rows: %w", err)
	}
	return n == 1, nil
}

func seatColumn(g model.SeatGender) (string, error) {
	switch g {
	case model.SeatMale:
		return "male_taken", nil
	case model.SeatFemale:
		return "female_taken", nil
	case model.SeatAny:
		return "any_taken", nil
	}
	return "", fmt.Errorf("unknown seat gender %q", g)
}

func seatQuotaColumn(g model.SeatGender) string {
	switch g {
	case model.SeatMale:
		return "male_seats"
	case model.SeatFemale:
		return "female_seats"
	default:
		return "any_seats"
	}
}

// UpdateStatus 写入新状态。带 from 条件做乐观校验，防止并发下状态被覆盖回退。
func (r *RoomRepo) UpdateStatus(ctx context.Context, q Querier, roomID int64,
	from, to model.RoomStatus) (bool, error) {

	res, err := q.ExecContext(ctx, `
		UPDATE rooms SET status = $1, updated_at = $2 WHERE id = $3 AND status = $4`,
		to, timeutil.Now(), roomID, from)
	if err != nil {
		return false, fmt.Errorf("update room status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update room status rows: %w", err)
	}
	return n == 1, nil
}

// NextMsgSeq 在同一事务内原子发号，返回该房间的下一条消息序号。
//
// UPDATE ... RETURNING 是单条语句，天然原子。这是「房内 seq 严格单调递增、
// 不重不跳」（NFR-2 B-4）的实现基础，配合 UNIQUE(room_id, seq) 双保险。
func (r *RoomRepo) NextMsgSeq(ctx context.Context, q Querier, roomID int64) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, `
		UPDATE rooms SET msg_seq = msg_seq + 1 WHERE id = $1 RETURNING msg_seq`, roomID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("next msg seq: %w", err)
	}
	return seq, nil
}

// UpdateMeta 更新车主可编辑的房间描述性字段（不含席位与状态）。
func (r *RoomRepo) UpdateMeta(ctx context.Context, q Querier, room *model.Room) error {
	_, err := q.ExecContext(ctx, `
		UPDATE rooms
		   SET title = $1, script_name = $2, venue_name = $3, city = $4, address = $5,
		       difficulty = $6, theme = $7, notes = $8, updated_at = $9
		 WHERE id = $10`,
		room.Title, room.ScriptName, room.VenueName, room.City, room.Address,
		room.Difficulty, room.Theme, room.Notes, timeutil.Now(), room.ID)
	if err != nil {
		return fmt.Errorf("update room meta: %w", err)
	}
	return nil
}

// WallFilter 是拼车墙的查询条件。零值表示不过滤。
type WallFilter struct {
	City         string
	RoomType     string
	Theme        string
	Keyword      string
	OnlyJoinable bool
	Statuses     []string
	Limit        int
	Offset       int
}

// ListWall 查询拼车墙。返回结果切片永不为 nil。
func (r *RoomRepo) ListWall(ctx context.Context, q Querier, f WallFilter) ([]*model.Room, int, error) {
	var (
		conds []string
		args  []any
	)
	add := func(cond string, vals ...any) {
		// 每次 add 都按当前 args 长度生成连续占位符编号，
		// 从结构上杜绝 $1,$3,$5 这类跳号（KB [Go][Postgres] 42P18）。
		placeholders := make([]any, 0, len(vals))
		for _, v := range vals {
			args = append(args, v)
			placeholders = append(placeholders, len(args))
		}
		conds = append(conds, fmt.Sprintf(cond, placeholders...))
	}

	if f.City != "" {
		add("city = $%d", f.City)
	}
	if f.RoomType != "" {
		add("room_type = $%d", f.RoomType)
	}
	if f.Theme != "" {
		add("theme = $%d", f.Theme)
	}
	if f.Keyword != "" {
		kw := "%" + strings.ToLower(f.Keyword) + "%"
		add("(lower(script_name) LIKE $%d OR lower(venue_name) LIKE $%d OR lower(title) LIKE $%d)", kw, kw, kw)
	}
	if len(f.Statuses) > 0 {
		add("status = ANY($%d)", f.Statuses)
	} else {
		add("status = ANY($%d)", []string{
			string(model.RoomRecruiting), string(model.RoomLocked), string(model.RoomConfirmed),
		})
	}
	if f.OnlyJoinable {
		add("status = $%d AND joined_count + pending_count < capacity AND start_at > $%d",
			string(model.RoomRecruiting), timeutil.Now())
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM rooms`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count wall: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 24
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	// 排序意图：先按开局时间升序（快开局的排前面，制造紧迫感），
	// 同一时间再按剩余席位少的排前（差一个人的车最需要曝光）。
	query := `SELECT ` + roomCols + ` FROM rooms` + where + fmt.Sprintf(`
		ORDER BY start_at ASC, (capacity - joined_count - pending_count) ASC, id DESC
		LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list wall: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*model.Room{}
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan wall room: %w", err)
		}
		out = append(out, room)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate wall: %w", err)
	}
	return out, total, nil
}

// ListByIDs 批量取房间，用于「我的车」列表。
func (r *RoomRepo) ListByIDs(ctx context.Context, q Querier, ids []int64) ([]*model.Room, error) {
	out := []*model.Room{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT `+roomCols+` FROM rooms WHERE id = ANY($1) ORDER BY start_at ASC`, int64Array(ids))
	if err != nil {
		return nil, fmt.Errorf("list rooms by ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("scan room: %w", err)
		}
		out = append(out, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rooms: %w", err)
	}
	return out, nil
}

// ListDueForTransition 查出「到点需要推进状态」的房间，供 Scheduler 使用。
// 只取还没到终态的房间，且 start_at 已过。
func (r *RoomRepo) ListDueForTransition(ctx context.Context, q Querier, now time.Time, limit int) ([]*model.Room, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.QueryContext(ctx, `
		SELECT `+roomCols+` FROM rooms
		 WHERE status = ANY($1) AND start_at <= $2
		 ORDER BY start_at ASC
		 LIMIT $3`,
		[]string{
			string(model.RoomRecruiting), string(model.RoomLocked),
			string(model.RoomConfirmed), string(model.RoomInProgress),
		}, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due rooms: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*model.Room{}
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due room: %w", err)
		}
		out = append(out, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due rooms: %w", err)
	}
	return out, nil
}

// ListStartingSoon 查出即将开局、需要发提醒的房间。
func (r *RoomRepo) ListStartingSoon(ctx context.Context, q Querier, from, to time.Time) ([]*model.Room, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+roomCols+` FROM rooms
		 WHERE status = ANY($1) AND start_at > $2 AND start_at <= $3
		 ORDER BY start_at ASC`,
		[]string{string(model.RoomRecruiting), string(model.RoomLocked), string(model.RoomConfirmed)},
		from, to)
	if err != nil {
		return nil, fmt.Errorf("list starting soon: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*model.Room{}
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("scan starting soon: %w", err)
		}
		out = append(out, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate starting soon: %w", err)
	}
	return out, nil
}

// ListCities 返回墙上出现过的城市，供筛选下拉。
func (r *RoomRepo) ListCities(ctx context.Context, q Querier) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT DISTINCT city FROM rooms WHERE city <> '' ORDER BY city`)
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scan city: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cities: %w", err)
	}
	return out, nil
}

// CountAll 返回房间总数，用于种子数据的幂等判断。
func (r *RoomRepo) CountAll(ctx context.Context, q Querier) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM rooms`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count rooms: %w", err)
	}
	return n, nil
}
