// Package fsm 实现拼车状态机（Requirements TR-2）。
//
// 设计契约：
//  1. 状态流转**只能**通过本包的 NextRoom / NextMember 求解，业务代码禁止
//     手写 `if status == X { status = Y }`。这样新增状态时只改一张表。
//  2. 转移表是显式声明的数据，可被测试穷举（见 fsm_test.go），
//     避免「某条分支从没人走过」的死代码。
//  3. 非法流转返回带错误码的领域错误，直接可以回给前端。
package fsm

import (
	"sort"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
)

// RoomEvent 是驱动房间状态变化的事件。
type RoomEvent string

const (
	EvPublish        RoomEvent = "PUBLISH"         // 车主发布
	EvFilled         RoomEvent = "FILLED"          // 席位坐满
	EvSlotFreed      RoomEvent = "SLOT_FREED"      // 满员后有人退车，回流招募
	EvOwnerLock      RoomEvent = "OWNER_LOCK"      // 车主提前锁车（人够了就走）
	EvDeadlineViable RoomEvent = "DEADLINE_VIABLE" // 到点且达最低成行人数
	EvDeadlineFailed RoomEvent = "DEADLINE_FAILED" // 到点但人数不足 → 炸车
	EvOwnerCancel    RoomEvent = "OWNER_CANCEL"    // 车主解散
	EvStart          RoomEvent = "START"           // 进入开局
	EvFinish         RoomEvent = "FINISH"          // 结束
)

// Label 返回事件的中文说明，用于审计日志与系统消息。
func (e RoomEvent) Label() string {
	switch e {
	case EvPublish:
		return "发布上墙"
	case EvFilled:
		return "席位坐满"
	case EvSlotFreed:
		return "有人退车，重新招募"
	case EvOwnerLock:
		return "车主提前锁车"
	case EvDeadlineViable:
		return "到点成行"
	case EvDeadlineFailed:
		return "到点人数不足，炸车"
	case EvOwnerCancel:
		return "车主解散"
	case EvStart:
		return "开局"
	case EvFinish:
		return "结束"
	default:
		return string(e)
	}
}

type roomKey struct {
	from  model.RoomStatus
	event RoomEvent
}

// roomTable 是房间状态机的唯一真相来源。
//
//	DRAFT ──PUBLISH──► RECRUITING ──FILLED / OWNER_LOCK──► LOCKED
//	                        │  ▲                             │
//	     DEADLINE_VIABLE    │  └──────SLOT_FREED─────────────┘
//	            ▼           │
//	        CONFIRMED       └──DEADLINE_FAILED──► EXPIRED（炸车）
//	            │
//	          START ──► IN_PROGRESS ──FINISH──► COMPLETED
var roomTable = map[roomKey]model.RoomStatus{
	{model.RoomDraft, EvPublish}: model.RoomRecruiting,

	{model.RoomRecruiting, EvFilled}:         model.RoomLocked,
	{model.RoomRecruiting, EvOwnerLock}:      model.RoomLocked,
	{model.RoomRecruiting, EvDeadlineViable}: model.RoomConfirmed,
	{model.RoomRecruiting, EvDeadlineFailed}: model.RoomExpired,
	{model.RoomRecruiting, EvOwnerCancel}:    model.RoomCancelled,

	{model.RoomLocked, EvSlotFreed}:   model.RoomRecruiting,
	{model.RoomLocked, EvStart}:       model.RoomInProgress,
	{model.RoomLocked, EvOwnerCancel}: model.RoomCancelled,

	{model.RoomConfirmed, EvStart}:       model.RoomInProgress,
	{model.RoomConfirmed, EvSlotFreed}:   model.RoomRecruiting,
	{model.RoomConfirmed, EvOwnerCancel}: model.RoomCancelled,

	{model.RoomInProgress, EvFinish}: model.RoomCompleted,
}

// NextRoom 求解房间的目标状态。非法流转返回 apperr.ErrIllegalRoomTx。
func NextRoom(from model.RoomStatus, ev RoomEvent) (model.RoomStatus, error) {
	to, ok := roomTable[roomKey{from, ev}]
	if !ok {
		return "", apperr.ErrIllegalRoomTx.
			WithMessage("当前状态「"+from.Label()+"」无法执行「"+ev.Label()+"」").
			WithDetail("from", string(from)).
			WithDetail("event", string(ev))
	}
	return to, nil
}

// CanRoom 判断流转是否合法，不产生错误对象。
func CanRoom(from model.RoomStatus, ev RoomEvent) bool {
	_, ok := roomTable[roomKey{from, ev}]
	return ok
}

// RoomTransition 是一条转移记录，用于自省与测试。
type RoomTransition struct {
	From  model.RoomStatus `json:"from"`
	Event RoomEvent        `json:"event"`
	To    model.RoomStatus `json:"to"`
}

// RoomTransitions 返回全部合法转移，按 from/event 稳定排序。
func RoomTransitions() []RoomTransition {
	out := make([]RoomTransition, 0, len(roomTable))
	for k, v := range roomTable {
		out = append(out, RoomTransition{From: k.from, Event: k.event, To: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Event < out[j].Event
	})
	return out
}

// AllRoomStatuses 返回全部房间状态，供测试穷举。
func AllRoomStatuses() []model.RoomStatus {
	return []model.RoomStatus{
		model.RoomDraft, model.RoomRecruiting, model.RoomLocked, model.RoomConfirmed,
		model.RoomInProgress, model.RoomCompleted, model.RoomExpired, model.RoomCancelled,
	}
}

// AllRoomEvents 返回全部房间事件，供测试穷举。
func AllRoomEvents() []RoomEvent {
	return []RoomEvent{
		EvPublish, EvFilled, EvSlotFreed, EvOwnerLock,
		EvDeadlineViable, EvDeadlineFailed, EvOwnerCancel, EvStart, EvFinish,
	}
}
