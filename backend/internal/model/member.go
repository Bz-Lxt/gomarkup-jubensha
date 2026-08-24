package model

import (
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// RoomMember 是「某个用户在某辆车上的一条成员记录」。
//
// 幂等由数据库部分唯一索引保证：
//
//	UNIQUE (room_id, user_id) WHERE status IN ('PENDING','JOINED','CHECKED_IN')
//
// 即同一用户在同一房间最多只能有一条「占席位」的记录，
// 但退车后可以再次上车（历史记录以 LEFT/RELEASED/KICKED 形态保留）。
type RoomMember struct {
	ID         int64        `json:"id"`
	RoomID     int64        `json:"room_id"`
	UserID     int64        `json:"user_id"`
	SeatGender SeatGender   `json:"seat_gender"`
	Status     MemberStatus `json:"status"`
	IsOwner    bool         `json:"is_owner"`

	// HoldExpiresAt 仅在 PENDING 状态下有意义。
	HoldExpiresAt *time.Time `json:"hold_expires_at"`
	JoinedAt      *time.Time `json:"joined_at"`
	LeftAt        *time.Time `json:"left_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// HoldExpired 判断占位是否已超时。非 PENDING 状态恒为 false。
func (m *RoomMember) HoldExpired() bool {
	if m.Status != MemberPending || m.HoldExpiresAt == nil {
		return false
	}
	return timeutil.Now().After(*m.HoldExpiresAt)
}

// HoldSecondsLeft 返回占位剩余秒数，非 PENDING 或已过期返回 0。
func (m *RoomMember) HoldSecondsLeft() int64 {
	if m.Status != MemberPending || m.HoldExpiresAt == nil {
		return 0
	}
	return int64(timeutil.Until(*m.HoldExpiresAt).Seconds())
}

// MemberView 是成员记录 + 用户公开资料的合成视图，供房间详情与在线列表使用。
type MemberView struct {
	MemberID        int64         `json:"member_id"`
	Status          MemberStatus  `json:"status"`
	StatusLabel     string        `json:"status_label"`
	SeatGender      SeatGender    `json:"seat_gender"`
	SeatLabel       string        `json:"seat_label"`
	IsOwner         bool          `json:"is_owner"`
	HoldSecondsLeft int64         `json:"hold_seconds_left"`
	JoinedAt        *time.Time    `json:"joined_at"`
	User            PublicProfile `json:"user"`
	Online          bool          `json:"online"`
}

// NewMemberView 合成视图。
func NewMemberView(m *RoomMember, u *User) MemberView {
	return MemberView{
		MemberID:        m.ID,
		Status:          m.Status,
		StatusLabel:     m.Status.Label(),
		SeatGender:      m.SeatGender,
		SeatLabel:       m.SeatGender.Label(),
		IsOwner:         m.IsOwner,
		HoldSecondsLeft: m.HoldSecondsLeft(),
		JoinedAt:        m.JoinedAt,
		User:            u.Public(),
	}
}

// StateLog 是状态机流转的审计记录（仅追加）。
type StateLog struct {
	ID         int64     `json:"id"`
	RoomID     int64     `json:"room_id"`
	MemberID   *int64    `json:"member_id"`
	Scope      string    `json:"scope"` // "room" | "member"
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Event      string    `json:"event"`
	ActorID    *int64    `json:"actor_id"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}
