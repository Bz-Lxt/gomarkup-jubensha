package service

import (
	"context"
	"fmt"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/fsm"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// 本文件是 Requirements TR-2「状态流转必须集中在单一 Transition 函数内」的落地。
// 全项目的状态变更只能走 applyRoomEvent / applyMemberEvent 两个入口，
// 它们各自完成：求解目标态 → 条件更新 → 写审计日志。

// pendingEvent 是「事务提交后才允许发出」的广播事件。
type pendingEvent struct {
	Scope   string // "room" | "wall"
	RoomID  int64
	Payload model.Envelope
}

// applyRoomEvent 执行房间状态流转。
//
// 返回 changed=false 表示流转在当前状态下不适用（例如 Scheduler 重复处理
// 同一个到期房间）。这不是错误：幂等要求重复执行不报错也不产生副作用。
func (d *Deps) applyRoomEvent(ctx context.Context, q repository.Querier, room *model.Room,
	ev fsm.RoomEvent, actorID *int64, reason string) (changed bool, err error) {

	if !fsm.CanRoom(room.Status, ev) {
		return false, nil
	}
	to, err := fsm.NextRoom(room.Status, ev)
	if err != nil {
		return false, err
	}
	ok, err := d.Rooms.UpdateStatus(ctx, q, room.ID, room.Status, to)
	if err != nil {
		return false, err
	}
	if !ok {
		// 并发下别的事务先改了状态。带 from 条件的 UPDATE 让我们安全地
		// 发现这件事，而不是盲写覆盖。
		logger.C(ctx).Debug("房间状态流转被并发抢先，跳过",
			"room_id", room.ID, "from", room.Status, "event", ev)
		return false, nil
	}

	if err := d.Logs.Append(ctx, q, &model.StateLog{
		RoomID:     room.ID,
		Scope:      "room",
		FromStatus: string(room.Status),
		ToStatus:   string(to),
		Event:      string(ev),
		ActorID:    actorID,
		Reason:     reason,
	}); err != nil {
		return false, err
	}

	logger.C(ctx).Info("房间状态流转",
		"room_id", room.ID, "from", room.Status, "to", to, "event", ev)
	room.Status = to
	return true, nil
}

// applyMemberEvent 执行成员状态流转，并同步调整房间席位账目。
//
// 席位增减一律由 fsm.SeatDelta 计算，绝不在调用点手写 ±1。
// NFR-1 A-5 的「账目零漂移」就是靠这条纪律成立的。
func (d *Deps) applyMemberEvent(ctx context.Context, q repository.Querier,
	room *model.Room, m *model.RoomMember, ev fsm.MemberEvent,
	actorID *int64, reason string) (changed bool, err error) {

	to, err := fsm.NextMember(m.Status, ev)
	if err != nil {
		return false, err
	}
	ok, err := d.Members.UpdateStatus(ctx, q, m.ID, m.Status, to)
	if err != nil {
		return false, err
	}
	if !ok {
		logger.C(ctx).Debug("成员状态流转被并发抢先，跳过",
			"member_id", m.ID, "from", m.Status, "event", ev)
		return false, nil
	}

	if err := d.adjustSeats(ctx, q, room, m.SeatGender, m.Status, to); err != nil {
		return false, err
	}

	if err := d.Logs.Append(ctx, q, &model.StateLog{
		RoomID:     room.ID,
		MemberID:   &m.ID,
		Scope:      "member",
		FromStatus: string(m.Status),
		ToStatus:   string(to),
		Event:      string(ev),
		ActorID:    actorID,
		Reason:     reason,
	}); err != nil {
		return false, err
	}

	logger.C(ctx).Info("成员状态流转",
		"room_id", room.ID, "member_id", m.ID, "user_id", m.UserID,
		"from", m.Status, "to", to, "event", ev)
	m.Status = to
	return true, nil
}

// adjustSeats 依据成员状态迁移调整房间的席位账目。
func (d *Deps) adjustSeats(ctx context.Context, q repository.Querier, room *model.Room,
	g model.SeatGender, from, to model.MemberStatus) error {

	delta := fsm.SeatDelta(from, to)
	if delta == 0 {
		// 例如 JOINED -> CHECKED_IN：两者都占席位，账目不动。
		return nil
	}

	var dJoined, dPending int
	// 离开原状态：把原状态占的那一格还回去。
	switch from {
	case model.MemberPending:
		dPending--
	case model.MemberJoined, model.MemberCheckedIn:
		dJoined--
	}
	// 进入新状态：占上新状态的那一格。
	switch to {
	case model.MemberPending:
		dPending++
	case model.MemberJoined, model.MemberCheckedIn:
		dJoined++
	}

	ok, err := d.Rooms.ApplySeatDelta(ctx, q, room.ID, dJoined, dPending, g, delta)
	if err != nil {
		if repository.IsCheckViolation(err) {
			// 走到这里意味着 L1/L2/L3 全都没拦住一次非法写入，是数据库
			// CHECK 约束在做最后兜底。这是设计上「不该发生」的事件，
			// 必须以 ERROR 级别记录并带完整上下文，供事后复盘。
			logger.C(ctx).Error("数据库屏障拦下了非法席位写入，应用层锁存在缺陷",
				"room_id", room.ID, "seat", g, "from", from, "to", to,
				"d_joined", dJoined, "d_pending", dPending, "error", err)
			return apperr.ErrSlotFull.WithCause(err)
		}
		return err
	}
	if !ok {
		return apperr.ErrSlotFull.WithCause(
			fmt.Errorf("seat delta rejected: room=%d seat=%s dj=%d dp=%d", room.ID, g, dJoined, dPending))
	}

	room.JoinedCount += dJoined
	room.PendingCount += dPending
	switch g {
	case model.SeatMale:
		room.MaleTaken += delta
	case model.SeatFemale:
		room.FemaleTaken += delta
	case model.SeatAny:
		room.AnyTaken += delta
	}
	return nil
}

// syncRoomOccupancy 在席位变动后推进「满员 / 回流」两个自动流转。
//
// 这两个流转不是用户显式触发的，而是席位数字变化的必然结果，
// 因此收敛在一个函数里，避免上车路径和退车路径各写一遍判断。
func (d *Deps) syncRoomOccupancy(ctx context.Context, q repository.Querier, room *model.Room) (fsm.RoomEvent, bool, error) {
	switch {
	case room.Remaining() == 0 && room.Status == model.RoomRecruiting:
		changed, err := d.applyRoomEvent(ctx, q, room, fsm.EvFilled, nil, "席位坐满，自动锁车")
		return fsm.EvFilled, changed, err
	case room.Remaining() > 0 && (room.Status == model.RoomLocked || room.Status == model.RoomConfirmed):
		changed, err := d.applyRoomEvent(ctx, q, room, fsm.EvSlotFreed, nil, "有人退车，重新开放招募")
		return fsm.EvSlotFreed, changed, err
	}
	return "", false, nil
}

// appendSystemMessage 在事务内写入一条系统消息，并返回它。
//
// 与用户消息共用同一套 seq 发号，因此「XX 上车了」这条系统消息在时间轴上
// 与聊天内容严格有序，不会出现「先看到有人说欢迎，才看到那人上车」。
func (d *Deps) appendSystemMessage(ctx context.Context, q repository.Querier,
	roomID int64, kind model.SystemMessageKind, content string) (*model.Message, error) {

	seq, err := d.Rooms.NextMsgSeq(ctx, q, roomID)
	if err != nil {
		return nil, err
	}
	msg := &model.Message{
		RoomID:    roomID,
		Seq:       seq,
		MsgType:   model.MsgSystem,
		Content:   content,
		TagCode:   string(kind),
		CreatedAt: timeutil.Now(),
	}
	if err := d.Messages.Insert(ctx, q, msg); err != nil {
		return nil, err
	}
	msg.SenderName = "系统"
	return msg, nil
}

// emit 在事务提交后统一发出广播。
func (d *Deps) emit(ctx context.Context, events []pendingEvent) {
	for _, e := range events {
		switch e.Scope {
		case "wall":
			d.Pub.PublishWall(ctx, e.Payload)
		default:
			d.Pub.PublishRoom(ctx, e.RoomID, e.Payload)
		}
	}
}
