package fsm

import (
	"testing"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
)

// TestRoomTransitions_HappyPaths 覆盖房间状态机的全部业务主线。
func TestRoomTransitions_HappyPaths(t *testing.T) {
	cases := []struct {
		name  string
		from  model.RoomStatus
		event RoomEvent
		want  model.RoomStatus
	}{
		{"发布上墙", model.RoomDraft, EvPublish, model.RoomRecruiting},
		{"坐满自动锁车", model.RoomRecruiting, EvFilled, model.RoomLocked},
		{"车主提前锁车", model.RoomRecruiting, EvOwnerLock, model.RoomLocked},
		{"到点成行", model.RoomRecruiting, EvDeadlineViable, model.RoomConfirmed},
		{"到点炸车", model.RoomRecruiting, EvDeadlineFailed, model.RoomExpired},
		{"招募中被解散", model.RoomRecruiting, EvOwnerCancel, model.RoomCancelled},
		{"满员后有人退车回流", model.RoomLocked, EvSlotFreed, model.RoomRecruiting},
		{"满员到点开局", model.RoomLocked, EvStart, model.RoomInProgress},
		{"成行到点开局", model.RoomConfirmed, EvStart, model.RoomInProgress},
		{"开局后结束", model.RoomInProgress, EvFinish, model.RoomCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextRoom(tc.from, tc.event)
			if err != nil {
				t.Fatalf("NextRoom(%s, %s) 意外报错: %v", tc.from, tc.event, err)
			}
			if got != tc.want {
				t.Fatalf("NextRoom(%s, %s) = %s，期望 %s", tc.from, tc.event, got, tc.want)
			}
		})
	}
}

// TestRoomTransitions_TerminalStatesAreDeadEnds 断言终态不可再流转。
//
// 这一条很重要：如果炸车后还能被推进成 CONFIRMED，成员会在「车已经炸了」
// 之后又收到「成行了」，用户体验直接崩坏。
func TestRoomTransitions_TerminalStatesAreDeadEnds(t *testing.T) {
	terminals := []model.RoomStatus{model.RoomCompleted, model.RoomExpired, model.RoomCancelled}
	for _, st := range terminals {
		if !st.IsTerminal() {
			t.Fatalf("%s 应被判定为终态", st)
		}
		for _, ev := range AllRoomEvents() {
			if CanRoom(st, ev) {
				t.Fatalf("终态 %s 不应接受事件 %s", st, ev)
			}
			if _, err := NextRoom(st, ev); err == nil {
				t.Fatalf("终态 %s 执行 %s 应报错", st, ev)
			} else if !apperr.Is(err, apperr.CodeIllegalRoomTx) {
				t.Fatalf("错误码应为 %s，实际 %v", apperr.CodeIllegalRoomTx, err)
			}
		}
	}
}

// TestRoomTransitions_ExhaustiveTableMatchesCanRoom 穷举全部 (状态 × 事件)
// 组合，确认 CanRoom 与 NextRoom 的判断完全一致——两者分叉会让某些路径
// 「检查说可以，执行却报错」。
func TestRoomTransitions_ExhaustiveTableMatchesCanRoom(t *testing.T) {
	legal := 0
	for _, st := range AllRoomStatuses() {
		for _, ev := range AllRoomEvents() {
			can := CanRoom(st, ev)
			_, err := NextRoom(st, ev)
			if can != (err == nil) {
				t.Fatalf("CanRoom(%s,%s)=%v 与 NextRoom 的结果不一致 (err=%v)", st, ev, can, err)
			}
			if can {
				legal++
			}
		}
	}
	if legal != len(RoomTransitions()) {
		t.Fatalf("合法组合数 %d 与转移表长度 %d 不一致", legal, len(RoomTransitions()))
	}
	if legal == 0 {
		t.Fatal("转移表为空，状态机没有任何可用路径")
	}
}

// TestMemberTransitions_HappyPaths 覆盖成员状态机主线。
func TestMemberTransitions_HappyPaths(t *testing.T) {
	cases := []struct {
		name  string
		from  model.MemberStatus
		event MemberEvent
		want  model.MemberStatus
	}{
		{"占位转正", model.MemberPending, EvConfirm, model.MemberJoined},
		{"占位超时回收", model.MemberPending, EvExpire, model.MemberReleased},
		{"占位期间退车", model.MemberPending, EvLeave, model.MemberLeft},
		{"占位期间被踢", model.MemberPending, EvKick, model.MemberKicked},
		{"已上车退车", model.MemberJoined, EvLeave, model.MemberLeft},
		{"已上车被踢", model.MemberJoined, EvKick, model.MemberKicked},
		{"到店签到", model.MemberJoined, EvCheckIn, model.MemberCheckedIn},
		{"签到后被踢", model.MemberCheckedIn, EvKick, model.MemberKicked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextMember(tc.from, tc.event)
			if err != nil {
				t.Fatalf("NextMember(%s, %s) 意外报错: %v", tc.from, tc.event, err)
			}
			if got != tc.want {
				t.Fatalf("NextMember(%s, %s) = %s，期望 %s", tc.from, tc.event, got, tc.want)
			}
		})
	}
}

// TestMemberTransitions_HoldIsNotATransition 断言 HOLD 不能当流转事件用。
func TestMemberTransitions_HoldIsNotATransition(t *testing.T) {
	for _, st := range AllMemberStatuses() {
		if _, err := NextMember(st, EvHold); err == nil {
			t.Fatalf("NextMember(%s, HOLD) 应报错：HOLD 是创建动作", st)
		}
	}
}

// TestMemberTransitions_TerminalStatesAreDeadEnds 断言成员终态不可复活。
func TestMemberTransitions_TerminalStatesAreDeadEnds(t *testing.T) {
	for _, st := range []model.MemberStatus{model.MemberReleased, model.MemberLeft, model.MemberKicked} {
		for _, ev := range AllMemberEvents() {
			if CanMember(st, ev) {
				t.Fatalf("成员终态 %s 不应接受事件 %s", st, ev)
			}
		}
	}
}

// TestSeatDelta 是席位账目的唯一计算入口，必须精确。
//
// 这个测试的价值：任何人改动 MemberStatus.OccupiesSeat() 都会在这里被拦住。
// 席位账目算错的后果是超载上车或永久锁死席位，属于最严重的一类 bug。
func TestSeatDelta(t *testing.T) {
	cases := []struct {
		name string
		from model.MemberStatus
		to   model.MemberStatus
		want int
	}{
		{"新建占位，占一个席位", "", model.MemberPending, 1},
		{"新建直接上车，占一个席位", "", model.MemberJoined, 1},
		{"占位转正，席位不变", model.MemberPending, model.MemberJoined, 0},
		{"占位超时，释放席位", model.MemberPending, model.MemberReleased, -1},
		{"占位退车，释放席位", model.MemberPending, model.MemberLeft, -1},
		{"上车后退车，释放席位", model.MemberJoined, model.MemberLeft, -1},
		{"上车后被踢，释放席位", model.MemberJoined, model.MemberKicked, -1},
		{"上车后签到，席位不变", model.MemberJoined, model.MemberCheckedIn, 0},
		{"签到后被踢，释放席位", model.MemberCheckedIn, model.MemberKicked, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SeatDelta(tc.from, tc.to); got != tc.want {
				t.Fatalf("SeatDelta(%q, %q) = %d，期望 %d", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// TestOccupiesSeatConsistency 断言「占席位」的判据没有遗漏新增状态。
func TestOccupiesSeatConsistency(t *testing.T) {
	want := map[model.MemberStatus]bool{
		model.MemberPending:   true,
		model.MemberJoined:    true,
		model.MemberCheckedIn: true,
		model.MemberReleased:  false,
		model.MemberLeft:      false,
		model.MemberKicked:    false,
	}
	all := AllMemberStatuses()
	if len(all) != len(want) {
		t.Fatalf("成员状态数量变了（%d vs %d），请同步更新本测试与 activeStatuses()", len(all), len(want))
	}
	for _, st := range all {
		if st.OccupiesSeat() != want[st] {
			t.Fatalf("%s.OccupiesSeat() = %v，期望 %v", st, st.OccupiesSeat(), want[st])
		}
	}
}
