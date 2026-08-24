package model

import "time"

// User 是平台用户。PasswordHash 带 json:"-"，任何路径都不会序列化出去。
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	Nickname     string    `json:"nickname"`
	Avatar       string    `json:"avatar"`
	City         string    `json:"city"`
	Bio          string    `json:"bio"`
	Reputation   int       `json:"reputation"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Tags 由 Repository 单独装载。类型是 PlayerTags 而非 []PlayerTag：
	// 去掉 omicempty 只能保证字段不消失，nil 切片仍会序列化成 null，
	// 前端 user.tags.map 照样 TypeError。PlayerTags 用自定义
	// MarshalJSON 把这件事在类型层面钉死（KB [Go][JSON]）。
	Tags PlayerTags `json:"tags"`
}

// PublicProfile 是可以安全暴露给同房其他成员的用户视图。
type PublicProfile struct {
	ID         int64      `json:"id"`
	Username   string     `json:"username"`
	Nickname   string     `json:"nickname"`
	Avatar     string     `json:"avatar"`
	City       string     `json:"city"`
	Reputation int        `json:"reputation"`
	Tags       PlayerTags `json:"tags"`
}

// Public 把 User 降级为对外视图，剔除手机号与口令哈希。
func (u *User) Public() PublicProfile {
	return PublicProfile{
		ID:         u.ID,
		Username:   u.Username,
		Nickname:   u.DisplayName(),
		Avatar:     u.Avatar,
		City:       u.City,
		Reputation: u.Reputation,
		Tags:       u.Tags,
	}
}

// DisplayName 优先用昵称，缺失时回落到用户名，保证 UI 永远有东西可渲染。
func (u *User) DisplayName() string {
	if u.Nickname != "" {
		return u.Nickname
	}
	return u.Username
}

// MaskedPhone 返回脱敏手机号，用于「我的资料」回显。
func (u *User) MaskedPhone() string {
	if len(u.Phone) != 11 {
		return ""
	}
	return u.Phone[:3] + "****" + u.Phone[7:]
}
