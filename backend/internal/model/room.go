package model

import (
	"fmt"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// AtRiskWindow 是「炸车预警」的时间窗：距开局不足该时长且仍未达成行人数，
// 卡片进入危险视觉态。
const AtRiskWindow = 2 * time.Hour

// Room 是一辆「拼车」，即一场待成行的线下局。
//
// 席位账目由三组字段共同描述，其一致性由数据库 CHECK 约束强制：
//   - Capacity                       总席位
//   - Male/Female/AnySeats           席位性别配额，三者之和 == Capacity
//   - Male/Female/AnyTaken           各配额已占用数，三者之和 == Joined+Pending
//   - JoinedCount / PendingCount     聚合已确认 / 占位中，之和 <= Capacity
type Room struct {
	ID         int64    `json:"id"`
	OwnerID    int64    `json:"owner_id"`
	Title      string   `json:"title"`
	ScriptName string   `json:"script_name"`
	VenueName  string   `json:"venue_name"`
	City       string   `json:"city"`
	Address    string   `json:"address"`
	RoomType   RoomType `json:"room_type"`
	Difficulty int      `json:"difficulty"`
	Theme      string   `json:"theme"`
	Notes      string   `json:"notes"`

	StartAt   time.Time `json:"start_at"`
	Capacity  int       `json:"capacity"`
	MinViable int       `json:"min_viable"`

	JoinedCount  int `json:"joined_count"`
	PendingCount int `json:"pending_count"`

	MaleSeats   int `json:"male_seats"`
	FemaleSeats int `json:"female_seats"`
	AnySeats    int `json:"any_seats"`
	MaleTaken   int `json:"male_taken"`
	FemaleTaken int `json:"female_taken"`
	AnyTaken    int `json:"any_taken"`

	Status    RoomStatus `json:"status"`
	MsgSeq    int64      `json:"msg_seq"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Occupied 返回当前占用的席位总数（已确认 + 占位中）。
func (r *Room) Occupied() int { return r.JoinedCount + r.PendingCount }

// Remaining 返回剩余席位数。
func (r *Room) Remaining() int {
	n := r.Capacity - r.Occupied()
	if n < 0 {
		return 0
	}
	return n
}

// SeatQuota 返回指定性别席位的配额。
func (r *Room) SeatQuota(g SeatGender) int {
	switch g {
	case SeatMale:
		return r.MaleSeats
	case SeatFemale:
		return r.FemaleSeats
	case SeatAny:
		return r.AnySeats
	}
	return 0
}

// SeatTaken 返回指定性别席位的已占用数。
func (r *Room) SeatTaken(g SeatGender) int {
	switch g {
	case SeatMale:
		return r.MaleTaken
	case SeatFemale:
		return r.FemaleTaken
	case SeatAny:
		return r.AnyTaken
	}
	return 0
}

// SeatRemaining 返回指定性别席位的剩余数。
func (r *Room) SeatRemaining(g SeatGender) int {
	n := r.SeatQuota(g) - r.SeatTaken(g)
	if n < 0 {
		return 0
	}
	return n
}

// SeatBucket 是单类席位的对外快照。
type SeatBucket struct {
	Gender    SeatGender `json:"gender"`
	Label     string     `json:"label"`
	Quota     int        `json:"quota"`
	Taken     int        `json:"taken"`
	Remaining int        `json:"remaining"`
}

// SeatBuckets 返回三类席位的完整快照，顺序固定（男/女/不限），
// 空配额也会返回条目，前端据此决定是否隐藏。禁止返回 nil。
func (r *Room) SeatBuckets() []SeatBucket {
	out := make([]SeatBucket, 0, 3)
	for _, g := range AllSeatGenders() {
		out = append(out, SeatBucket{
			Gender:    g,
			Label:     g.Label(),
			Quota:     r.SeatQuota(g),
			Taken:     r.SeatTaken(g),
			Remaining: r.SeatRemaining(g),
		})
	}
	return out
}

// Headline 生成「5缺2」式的一句话席位摘要，即拼车墙卡片的主标数字。
func (r *Room) Headline() string {
	if r.Remaining() == 0 {
		return fmt.Sprintf("%d人满员", r.Capacity)
	}
	return fmt.Sprintf("%d缺%d", r.Capacity, r.Remaining())
}

// SeatDetail 生成「已有3女，还缺1男」式的席位明细文案。
func (r *Room) SeatDetail() string {
	var parts []string
	for _, g := range AllSeatGenders() {
		if r.SeatQuota(g) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d", g.Label(), r.SeatTaken(g), r.SeatQuota(g)))
	}
	if len(parts) == 0 {
		return "席位未配置"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}

// IsViable 表示当前人数是否已达最低成行门槛。
func (r *Room) IsViable() bool { return r.Occupied() >= r.MinViable }

// SecondsToStart 返回距开局的剩余秒数，已过期为 0。
// 走 timeutil 而非 UTC，避免 KB [Go][TZ] 记录的日界错位。
func (r *Room) SecondsToStart() int64 {
	return int64(timeutil.Until(r.StartAt).Seconds())
}

// IsAtRisk 表示卡片应进入「炸车预警」视觉态：
// 仍在招募、距开局不足预警窗、且人数还没到最低成行线。
func (r *Room) IsAtRisk() bool {
	if r.Status != RoomRecruiting {
		return false
	}
	if r.IsViable() {
		return false
	}
	left := timeutil.Until(r.StartAt)
	return left > 0 && left <= AtRiskWindow
}

// AcceptsJoin 表示当前是否还能上车（不含席位性别维度的判断）。
func (r *Room) AcceptsJoin() bool {
	return r.Status == RoomRecruiting && r.Remaining() > 0 && timeutil.Until(r.StartAt) > 0
}

// RiskHint 生成倒计时区域的动态提示文案。
func (r *Room) RiskHint() string {
	left := timeutil.Until(r.StartAt)
	switch {
	case r.Status == RoomExpired:
		return "人没凑齐，这车已经炸了"
	case r.Status == RoomCancelled:
		return "车主已解散这辆车"
	case r.Status == RoomInProgress:
		return "正在开局中"
	case r.Status == RoomCompleted:
		return "这局已经结束"
	case left <= 0:
		return "已到开局时间"
	case r.IsAtRisk():
		return fmt.Sprintf("%s后开局，还差 %d 人，再不来车就炸了", humanizeDuration(left), r.MinViable-r.Occupied())
	case r.Remaining() == 0:
		return fmt.Sprintf("已满员，%s后开局", humanizeDuration(left))
	default:
		return fmt.Sprintf("%s后开局，还有 %d 个位", humanizeDuration(left), r.Remaining())
	}
}

func humanizeDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%d天", int(d.Hours())/24)
	case d >= time.Hour:
		return fmt.Sprintf("%d小时", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%d分钟", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d秒", int(d.Seconds()))
	}
}

// SlotSnapshot 是席位变动的轻量广播载荷。WS 推送与 HTTP 响应共用同一形状，
// 前端只需一套解析逻辑。
type SlotSnapshot struct {
	RoomID       int64        `json:"room_id"`
	Status       RoomStatus   `json:"status"`
	StatusLabel  string       `json:"status_label"`
	Capacity     int          `json:"capacity"`
	MinViable    int          `json:"min_viable"`
	JoinedCount  int          `json:"joined_count"`
	PendingCount int          `json:"pending_count"`
	Occupied     int          `json:"occupied"`
	Remaining    int          `json:"remaining"`
	Seats        []SeatBucket `json:"seats"`
	Headline     string       `json:"headline"`
	SeatDetail   string       `json:"seat_detail"`
	RiskHint     string       `json:"risk_hint"`
	AtRisk       bool         `json:"at_risk"`
	Viable       bool         `json:"viable"`
	AcceptsJoin  bool         `json:"accepts_join"`
	StartAt      time.Time    `json:"start_at"`
	SecondsLeft  int64        `json:"seconds_left"`
}

// Snapshot 生成席位快照。
func (r *Room) Snapshot() SlotSnapshot {
	return SlotSnapshot{
		RoomID:       r.ID,
		Status:       r.Status,
		StatusLabel:  r.Status.Label(),
		Capacity:     r.Capacity,
		MinViable:    r.MinViable,
		JoinedCount:  r.JoinedCount,
		PendingCount: r.PendingCount,
		Occupied:     r.Occupied(),
		Remaining:    r.Remaining(),
		Seats:        r.SeatBuckets(),
		Headline:     r.Headline(),
		SeatDetail:   r.SeatDetail(),
		RiskHint:     r.RiskHint(),
		AtRisk:       r.IsAtRisk(),
		Viable:       r.IsViable(),
		AcceptsJoin:  r.AcceptsJoin(),
		StartAt:      timeutil.In(r.StartAt),
		SecondsLeft:  r.SecondsToStart(),
	}
}
