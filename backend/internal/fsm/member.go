package fsm

import (
	"sort"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
)

// MemberEvent 是驱动成员状态变化的事件。
type MemberEvent string

const (
	EvHold    MemberEvent = "HOLD"     // 抢到位，进入占位
	EvConfirm MemberEvent = "CONFIRM"  // 占位确认为正式上车
	EvExpire  MemberEvent = "EXPIRE"   // 占位 TTL 到期，系统回收
	EvLeave   MemberEvent = "LEAVE"    // 主动退车
	EvKick    MemberEvent = "KICK"     // 车主踢出
	EvCheckIn MemberEvent = "CHECK_IN" // 到店签到
)

func (e MemberEvent) Label() string {
	switch e {
	case EvHold:
		return "占位上车"
	case EvConfirm:
		return "确认上车"
	case EvExpire:
		return "占位超时回收"
	case EvLeave:
		return "主动退车"
	case EvKick:
		return "被车主移出"
	case EvCheckIn:
		return "到店签到"
	default:
		return string(e)
	}
}

type memberKey struct {
	from  model.MemberStatus
	event MemberEvent
}

// memberTable 是成员状态机的唯一真相来源。
//
//	(新建) ──HOLD──► PENDING ──CONFIRM──► JOINED ──CHECK_IN──► CHECKED_IN
//	                    │                    │
//	                 EXPIRE               LEAVE / KICK
//	                    ▼                    ▼
//	                RELEASED             LEFT / KICKED
//
// 终态（RELEASED / LEFT / KICKED）不可再流转；用户若想重新上车，
// 走 HOLD 新建一条记录，历史记录原样保留用于审计与信誉计算。
var memberTable = map[memberKey]model.MemberStatus{
	{model.MemberPending, EvConfirm}: model.MemberJoined,
	{model.MemberPending, EvExpire}:  model.MemberReleased,
	{model.MemberPending, EvLeave}:   model.MemberLeft,
	{model.MemberPending, EvKick}:    model.MemberKicked,

	{model.MemberJoined, EvLeave}:   model.MemberLeft,
	{model.MemberJoined, EvKick}:    model.MemberKicked,
	{model.MemberJoined, EvCheckIn}: model.MemberCheckedIn,

	{model.MemberCheckedIn, EvKick}: model.MemberKicked,
}

// NextMember 求解成员的目标状态。非法流转返回 apperr.ErrIllegalMemberTx。
//
// EvHold 不在表中：它是「从无到有」的创建动作，由 SlotService 直接建记录，
// 因为不存在源状态可供流转。
func NextMember(from model.MemberStatus, ev MemberEvent) (model.MemberStatus, error) {
	if ev == EvHold {
		return "", apperr.ErrIllegalMemberTx.
			WithMessage("HOLD 是创建动作，不能作为流转事件").
			WithDetail("event", string(ev))
	}
	to, ok := memberTable[memberKey{from, ev}]
	if !ok {
		return "", apperr.ErrIllegalMemberTx.
			WithMessage("当前成员状态「"+from.Label()+"」无法执行「"+ev.Label()+"」").
			WithDetail("from", string(from)).
			WithDetail("event", string(ev))
	}
	return to, nil
}

// CanMember 判断成员流转是否合法。
func CanMember(from model.MemberStatus, ev MemberEvent) bool {
	_, ok := memberTable[memberKey{from, ev}]
	return ok
}

// MemberTransition 是一条成员转移记录。
type MemberTransition struct {
	From  model.MemberStatus `json:"from"`
	Event MemberEvent        `json:"event"`
	To    model.MemberStatus `json:"to"`
}

// MemberTransitions 返回全部合法成员转移，稳定排序。
func MemberTransitions() []MemberTransition {
	out := make([]MemberTransition, 0, len(memberTable))
	for k, v := range memberTable {
		out = append(out, MemberTransition{From: k.from, Event: k.event, To: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Event < out[j].Event
	})
	return out
}

// AllMemberStatuses 返回全部成员状态，供测试穷举。
func AllMemberStatuses() []model.MemberStatus {
	return []model.MemberStatus{
		model.MemberPending, model.MemberJoined, model.MemberReleased,
		model.MemberLeft, model.MemberKicked, model.MemberCheckedIn,
	}
}

// AllMemberEvents 返回全部成员事件，供测试穷举。
func AllMemberEvents() []MemberEvent {
	return []MemberEvent{EvHold, EvConfirm, EvExpire, EvLeave, EvKick, EvCheckIn}
}

// SeatDelta 返回一次成员流转对「席位占用数」的影响：
// -1 表示释放一个席位，0 表示席位归属不变。
//
// 席位账目的加减只认这个函数，避免各处手写 ±1 导致账目漂移（NFR-1 A-5）。
func SeatDelta(from, to model.MemberStatus) int {
	before, after := 0, 0
	if from.OccupiesSeat() {
		before = 1
	}
	if to.OccupiesSeat() {
		after = 1
	}
	return after - before
}
