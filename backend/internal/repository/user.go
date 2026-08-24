package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// UserRepo 是用户与用户标签的数据访问。
type UserRepo struct{}

// NewUserRepo 构造仓库。仓库无状态，Querier 每次调用传入。
func NewUserRepo() *UserRepo { return &UserRepo{} }

const userCols = `id, username, phone, password_hash, nickname, avatar, city, bio, reputation, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.Phone, &u.PasswordHash, &u.Nickname,
		&u.Avatar, &u.City, &u.Bio, &u.Reputation, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = timeutil.In(u.CreatedAt)
	u.UpdatedAt = timeutil.In(u.UpdatedAt)
	u.Tags = []model.PlayerTag{}
	return &u, nil
}

// Create 插入用户并回填自增 ID 与时间戳。
func (r *UserRepo) Create(ctx context.Context, q Querier, u *model.User) error {
	now := timeutil.Now()
	err := q.QueryRowContext(ctx, `
		INSERT INTO users (username, phone, password_hash, nickname, avatar, city, bio, reputation, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at`,
		u.Username, u.Phone, u.PasswordHash, u.Nickname, u.Avatar, u.City, u.Bio, u.Reputation, now, now,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	u.CreatedAt = timeutil.In(u.CreatedAt)
	u.UpdatedAt = timeutil.In(u.UpdatedAt)
	return nil
}

// GetByID 按主键查询。
func (r *UserRepo) GetByID(ctx context.Context, q Querier, id int64) (*model.User, error) {
	u, err := scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// GetByUsername 按用户名查询（大小写不敏感，与唯一索引 lower(username) 一致）。
func (r *UserRepo) GetByUsername(ctx context.Context, q Querier, username string) (*model.User, error) {
	u, err := scanUser(q.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE lower(username) = lower($1)`, username))
	if err != nil {
		if IsNoRows(err) {
			return nil, err
		}
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

// ListByIDs 批量查询，用于装载房间成员的用户资料，避免 N+1。
func (r *UserRepo) ListByIDs(ctx context.Context, q Querier, ids []int64) (map[int64]*model.User, error) {
	out := make(map[int64]*model.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT `+userCols+` FROM users WHERE id = ANY($1)`, int64Array(ids))
	if err != nil {
		return nil, fmt.Errorf("list users by ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out[u.ID] = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	if err := r.attachTags(ctx, q, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateProfile 更新可编辑的资料字段。
func (r *UserRepo) UpdateProfile(ctx context.Context, q Querier, u *model.User) error {
	_, err := q.ExecContext(ctx, `
		UPDATE users
		   SET nickname = $1, avatar = $2, city = $3, bio = $4, phone = $5, updated_at = $6
		 WHERE id = $7`,
		u.Nickname, u.Avatar, u.City, u.Bio, u.Phone, timeutil.Now(), u.ID)
	if err != nil {
		return fmt.Errorf("update user profile: %w", err)
	}
	return nil
}

// AdjustReputation 调整信誉分并夹紧到 [0, 200]，避免撞 CHECK 约束。
func (r *UserRepo) AdjustReputation(ctx context.Context, q Querier, userID int64, delta int) error {
	_, err := q.ExecContext(ctx, `
		UPDATE users
		   SET reputation = LEAST(200, GREATEST(0, reputation + $1)), updated_at = $2
		 WHERE id = $3`, delta, timeutil.Now(), userID)
	if err != nil {
		return fmt.Errorf("adjust reputation: %w", err)
	}
	return nil
}

// ReplaceTags 全量替换用户标签（先删后插，在同一事务内保证原子）。
func (r *UserRepo) ReplaceTags(ctx context.Context, q Querier, userID int64, tags []model.PlayerTag) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM user_tags WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear user tags: %w", err)
	}
	if len(tags) == 0 {
		return nil
	}
	// 构造 VALUES ($1,$2),($1,$3)... 形式的批量插入。
	// 占位符编号连续递增，符合 KB [Go][Postgres] 的要求。
	args := make([]any, 0, len(tags)+1)
	args = append(args, userID)
	var sb strings.Builder
	sb.WriteString(`INSERT INTO user_tags (user_id, tag) VALUES `)
	for i, t := range tags {
		if i > 0 {
			sb.WriteString(", ")
		}
		args = append(args, string(t))
		fmt.Fprintf(&sb, "($1, $%d)", len(args))
	}
	sb.WriteString(` ON CONFLICT (user_id, tag) DO NOTHING`)
	if _, err := q.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("insert user tags: %w", err)
	}
	return nil
}

// LoadTags 装载单个用户的标签。返回值永不为 nil。
func (r *UserRepo) LoadTags(ctx context.Context, q Querier, userID int64) ([]model.PlayerTag, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT tag FROM user_tags WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("load user tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []model.PlayerTag{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("scan user tag: %w", err)
		}
		out = append(out, model.PlayerTag(t))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user tags: %w", err)
	}
	return out, nil
}

// attachTags 批量装载标签，避免逐用户查询。
func (r *UserRepo) attachTags(ctx context.Context, q Querier, users map[int64]*model.User) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	rows, err := q.QueryContext(ctx,
		`SELECT user_id, tag FROM user_tags WHERE user_id = ANY($1) ORDER BY user_id, created_at`,
		int64Array(ids))
	if err != nil {
		return fmt.Errorf("batch load user tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uid int64
		var tag string
		if err := rows.Scan(&uid, &tag); err != nil {
			return fmt.Errorf("scan batch user tag: %w", err)
		}
		if u, ok := users[uid]; ok {
			u.Tags = append(u.Tags, model.PlayerTag(tag))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate batch user tags: %w", err)
	}
	return nil
}

// LoadUserWithTags 是「查用户 + 装标签」的常用组合。
func (r *UserRepo) LoadUserWithTags(ctx context.Context, q Querier, id int64) (*model.User, error) {
	u, err := r.GetByID(ctx, q, id)
	if err != nil {
		return nil, err
	}
	tags, err := r.LoadTags(ctx, q, id)
	if err != nil {
		return nil, err
	}
	u.Tags = tags
	return u, nil
}
