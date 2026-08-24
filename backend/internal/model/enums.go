package model

import "encoding/json"

// RoomStatus 是拼车房间的生命周期状态。流转规则见 internal/fsm。
type RoomStatus string

const (
	RoomDraft      RoomStatus = "DRAFT"       // 草稿，未发布到墙上
	RoomRecruiting RoomStatus = "RECRUITING"  // 招募中，可上车
	RoomLocked     RoomStatus = "LOCKED"      // 满员锁定，等开局
	RoomConfirmed  RoomStatus = "CONFIRMED"   // 达到最低成行人数并已成行
	RoomInProgress RoomStatus = "IN_PROGRESS" // 开局中
	RoomCompleted  RoomStatus = "COMPLETED"   // 已完成
	RoomExpired    RoomStatus = "EXPIRED"     // 炸车：到点人数不足
	RoomCancelled  RoomStatus = "CANCELLED"   // 车主取消
)

// RoomStatusLabel 返回面向用户的中文状态文案。
func (s RoomStatus) Label() string {
	switch s {
	case RoomDraft:
		return "草稿"
	case RoomRecruiting:
		return "招募中"
	case RoomLocked:
		return "已满员"
	case RoomConfirmed:
		return "已成行"
	case RoomInProgress:
		return "开局中"
	case RoomCompleted:
		return "已完成"
	case RoomExpired:
		return "已炸车"
	case RoomCancelled:
		return "已取消"
	default:
		return string(s)
	}
}

// IsOpen 表示该状态下墙上还应展示为可交互（可上车或可围观）。
func (s RoomStatus) IsOpen() bool {
	return s == RoomRecruiting || s == RoomLocked || s == RoomConfirmed
}

// IsTerminal 表示该状态为终态，不再发生任何流转。
func (s RoomStatus) IsTerminal() bool {
	return s == RoomCompleted || s == RoomExpired || s == RoomCancelled
}

// MemberStatus 是成员在某辆车上的状态。
type MemberStatus string

const (
	MemberPending   MemberStatus = "PENDING"    // 占位中，有 TTL
	MemberJoined    MemberStatus = "JOINED"     // 已确认上车
	MemberReleased  MemberStatus = "RELEASED"   // 占位超时被系统回收
	MemberLeft      MemberStatus = "LEFT"       // 主动退车
	MemberKicked    MemberStatus = "KICKED"     // 被车主踢出
	MemberCheckedIn MemberStatus = "CHECKED_IN" // 到店签到
)

// OccupiesSeat 表示该状态是否占用一个席位。
// 这是席位账目的唯一判据，Repository 的计数 SQL 必须与之保持一致。
func (s MemberStatus) OccupiesSeat() bool {
	return s == MemberPending || s == MemberJoined || s == MemberCheckedIn
}

func (s MemberStatus) Label() string {
	switch s {
	case MemberPending:
		return "占位中"
	case MemberJoined:
		return "已上车"
	case MemberReleased:
		return "占位超时"
	case MemberLeft:
		return "已退车"
	case MemberKicked:
		return "已被移出"
	case MemberCheckedIn:
		return "已到店"
	default:
		return string(s)
	}
}

// SeatGender 是「剧本角色席位」的性别属性。
//
// 重要（Requirements C-4 裁决）：这是剧本角色设定，不是用户身份准入条件。
// 文案一律表述为「男角色席」而非「限男生」。
type SeatGender string

const (
	SeatMale   SeatGender = "MALE"
	SeatFemale SeatGender = "FEMALE"
	SeatAny    SeatGender = "ANY"
)

func (g SeatGender) Label() string {
	switch g {
	case SeatMale:
		return "男角色席"
	case SeatFemale:
		return "女角色席"
	case SeatAny:
		return "不限性别席"
	default:
		return string(g)
	}
}

// Valid 校验席位性别取值合法。
func (g SeatGender) Valid() bool {
	return g == SeatMale || g == SeatFemale || g == SeatAny
}

// AllSeatGenders 返回全部席位类型，顺序固定用于 UI 渲染。
func AllSeatGenders() []SeatGender {
	return []SeatGender{SeatMale, SeatFemale, SeatAny}
}

// PlayerTags 是玩家标签集合。
//
// 存在的唯一理由：nil 切片会被 encoding/json 序列化成 null，
// 而前端对 tags 一律做 .map() / .length。KB [Go][JSON] 那条教训写的是
// 「切片字段禁用 omitempty」，但那只解决了「字段消失」，没解决「字段为 null」——
// 两者对前端是同一个 TypeError。用一个具名类型带上 MarshalJSON，
// 就不必依赖每个构造点的人工记性。
type PlayerTags []PlayerTag

// MarshalJSON 保证空集永远输出 []，绝不输出 null。
func (t PlayerTags) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]PlayerTag(t))
}

// RoomType 区分剧本杀与密室逃脱。
type RoomType string

const (
	TypeScript RoomType = "SCRIPT" // 剧本杀
	TypeEscape RoomType = "ESCAPE" // 密室逃脱
)

func (t RoomType) Label() string {
	switch t {
	case TypeScript:
		return "剧本杀"
	case TypeEscape:
		return "密室逃脱"
	default:
		return string(t)
	}
}

func (t RoomType) Valid() bool { return t == TypeScript || t == TypeEscape }

// PlayerTag 是玩家自我标签，也是聊天室「一键发送标签」的取值域。
type PlayerTag string

const (
	TagHardcore PlayerTag = "HARDCORE" // 硬核
	TagEmotion  PlayerTag = "EMOTION"  // 情感
	TagHorror   PlayerTag = "HORROR"   // 恐怖
	TagFun      PlayerTag = "FUN"      // 欢乐
	TagRookie   PlayerTag = "ROOKIE"   // 新手
	TagVeteran  PlayerTag = "VETERAN"  // 老手
)

// MaxUserTags 是单个用户可选的标签上限。
const MaxUserTags = 3

var playerTagMeta = map[PlayerTag]struct {
	Label  string
	Phrase string // 一键发送到聊天室时的自我介绍话术
}{
	TagHardcore: {"硬核", "我是硬核派，冲着还原和推理来的"},
	TagEmotion:  {"情感", "我玩情感本会哭，别笑我"},
	TagHorror:   {"恐怖", "恐怖本我顶得住，别的不敢说"},
	TagFun:      {"欢乐", "欢乐局带我，主要图个乐"},
	TagRookie:   {"新手", "我是新手，求大佬带飞"},
	TagVeteran:  {"老手", "老手一枚，可以帮带新人"},
}

func (t PlayerTag) Label() string {
	if m, ok := playerTagMeta[t]; ok {
		return m.Label
	}
	return string(t)
}

// Phrase 返回一键标签消息的正文。
func (t PlayerTag) Phrase() string {
	if m, ok := playerTagMeta[t]; ok {
		return m.Phrase
	}
	return string(t)
}

func (t PlayerTag) Valid() bool {
	_, ok := playerTagMeta[t]
	return ok
}

// AllPlayerTags 返回全部标签，顺序固定用于 UI 渲染。
func AllPlayerTags() []PlayerTag {
	return []PlayerTag{TagHardcore, TagEmotion, TagHorror, TagFun, TagRookie, TagVeteran}
}

// MsgType 区分消息种类。
type MsgType string

const (
	MsgText   MsgType = "TEXT"   // 普通文本
	MsgTag    MsgType = "TAG"    // 一键玩家标签
	MsgSystem MsgType = "SYSTEM" // 系统事件
)

func (t MsgType) Valid() bool { return t == MsgText || t == MsgTag || t == MsgSystem }
