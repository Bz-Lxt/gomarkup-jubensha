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

// SlotService 实现 Requirements TR-1：强一致性拼车位并发抢占。
//
// 全部写路径统一走 withSlotLock，它保证了固定的加锁顺序：
//
//	L1 进程内分片锁 → L2 Redis 分布式锁 → L3 数据库事务 + SELECT FOR UPDATE
//
// 正确性只由 L3 承担。L1/L2 都是可失效的性能层，NFR-1 A-4 会主动关掉 L2
// 来证明这一点。
type SlotService struct {
	d *Deps
}

func NewSlotService(d *Deps) *SlotService { return &SlotService{d: d} }

// JoinResult 是上车的结果。
type JoinResult struct {
	Room       *model.Room        `json:"room"`
	Snapshot   model.SlotSnapshot `json:"snapshot"`
	Member     *model.RoomMember  `json:"member"`
	Idempotent bool               `json:"idempotent"`
}

// withSlotLock 是所有席位写操作的唯一入口。
//
// fn 在「L1+L2 互斥 + 数据库事务」内执行，拿到的 room 已被 FOR UPDATE 锁定。
// fn 返回的 pendingEvent 会在事务成功提交后才广播。
func (s *SlotService) withSlotLock(ctx context.Context, roomID int64,
	fn func(context.Context, repository.Querier, *model.Room) ([]pendingEvent, error)) error {

	var events []pendingEvent
	err := s.d.Guard.Do(ctx, roomID, func(ctx context.Context) error {
		events = nil
		return repository.InTx(ctx, s.d.Pool, func(q repository.Querier) error {
			room, err := s.d.Rooms.LockForUpdate(ctx, q, roomID)
			if err != nil {
				if repository.IsNoRows(err) {
					return apperr.ErrRoomNotFound
				}
				return err
			}
			ev, err := fn(ctx, q, room)
			if err != nil {
				return err
			}
			events = ev
			return nil
		})
	})
	if err != nil {
		return err
	}
	s.d.emit(ctx, events)
	return nil
}

// Join 执行「人数校验 → 锁位 → 扣减 → 履约」的原子事务。
func (s *SlotService) Join(ctx context.Context, roomID, userID int64, seat model.SeatGender) (*JoinResult, error) {
	if !seat.Valid() {
		return nil, apperr.ErrValidation.
			WithMessage("请选择一个合法的角色席位").
			WithDetail("field", "seat_gender")
	}

	var out *JoinResult
	err := s.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		// ---- 幂等校验（必须在房间状态校验之前）----
		// 用户连点 50 次也只能占一个位（NFR-1 A-3）。
		//
		// 顺序至关紧要：弱网下首个响应丢失后客户端重放时，房间可能已经
		// 因满员而自动锁车（LOCKED）。如果先检查房间状态，重放会收到
		// ROOM_NOT_RECRUITING 而非幂等成功，造成「第一次成功、第二次失败」
		// 的分叉——这正是移动端弱网复现的幂等分叉 bug。
		// 只要该用户仍占着席位（PENDING/JOINED/CHECKED_IN），就应返回与
		// 首次相同的结果，不受房间状态流转的影响。
		if existing, err := s.d.Members.GetActive(ctx, q, roomID, userID); err == nil {
			out = &JoinResult{Room: room, Snapshot: room.Snapshot(), Member: existing, Idempotent: true}
			return nil, nil
		} else if !repository.IsNoRows(err) {
			return nil, err
		}

		// ---- 房间状态校验（在 FOR UPDATE 保护下）----
		if room.Status != model.RoomRecruiting {
			return nil, apperr.ErrRoomNotRecruiting.
				WithMessage("这辆车当前是「"+room.Status.Label()+"」，上不了车").
				WithDetail("status", string(room.Status))
		}

		// ---- 时间与容量校验（都在 FOR UPDATE 保护下）----
		if timeutil.Until(room.StartAt) <= 0 {
			return nil, apperr.ErrRoomClosed.WithMessage("已经到开局时间了，来不及上车")
		}
		if room.Remaining() <= 0 {
			return nil, apperr.ErrSlotFull
		}
		if room.SeatRemaining(seat) <= 0 {
			return nil, apperr.ErrSeatGenderFull.
				WithMessage(seat.Label()+"已经满了，看看别的席位").
				WithDetail("seat_gender", string(seat)).
				WithDetail("seats", room.SeatBuckets())
		}

		// ---- 锁位：建成员记录 ----
		member := &model.RoomMember{
			RoomID:     roomID,
			UserID:     userID,
			SeatGender: seat,
			Status:     model.MemberPending,
		}
		hold := timeutil.Now().Add(s.d.Cfg.SlotHoldTTL)
		member.HoldExpiresAt = &hold
		if s.d.Cfg.SlotAutoConfirm {
			// 平台不涉及支付，占位没有等待理由，默认直接转正。
			// 保留 PENDING 通路是为了将来接入「车主审核制」时无需改动状态机。
			joined := timeutil.Now()
			member.Status = model.MemberJoined
			member.JoinedAt = &joined
			member.HoldExpiresAt = nil
		}

		if err := s.d.Members.Insert(ctx, q, member); err != nil {
			if repository.IsUniqueViolation(err) {
				// 同一用户的并发请求撞上了部分唯一索引 uq_members_active。
				// 这正是幂等屏障生效的表现，不是错误。
				//
				// 返回幂等成功而非 ErrAlreadyOnBoard：调用方依赖请求重放兜弱网，
				// 重放应当与首次成功得到完全一致的结果。无论是 GetActive 先行
				// 命中还是唯一索引兜底命中，两条路径的结果必须同构。
				logger.C(ctx).Info("幂等屏障命中：同一用户并发上车",
					"room_id", roomID, "user_id", userID)
				existing, gErr := s.d.Members.GetActive(ctx, q, roomID, userID)
				if gErr != nil {
					// 理论上不会走到：唯一索引冲突说明行存在，GetActive 必然能查到。
					// 真走到这里说明出现了严重的数据不一致，返回内部错误以便排查。
					return nil, apperr.ErrInternal.
						WithMessage("幂等屏障命中但无法读取已存在的成员记录").
						WithCause(gErr)
				}
				out = &JoinResult{Room: room, Snapshot: room.Snapshot(), Member: existing, Idempotent: true}
				return nil, nil
			}
			return nil, err
		}

		// ---- 扣减：席位账目 ----
		if err := s.d.adjustSeats(ctx, q, room, seat, model.MemberStatus(""), member.Status); err != nil {
			return nil, err
		}

		if err := s.d.Logs.Append(ctx, q, &model.StateLog{
			RoomID: roomID, MemberID: &member.ID, Scope: "member",
			FromStatus: "", ToStatus: string(member.Status),
			Event: string(fsm.EvHold), ActorID: &userID, Reason: "上车",
		}); err != nil {
			return nil, err
		}

		// ---- 履约：系统消息 + 满员自动锁车 + 广播 ----
		user, err := s.d.Users.GetByID(ctx, q, userID)
		if err != nil {
			return nil, err
		}
		events := make([]pendingEvent, 0, 4)

		sysMsg, err := s.d.appendSystemMessage(ctx, q, roomID, model.SysJoin,
			fmt.Sprintf("%s 上车了，坐 %s", user.DisplayName(), seat.Label()))
		if err != nil {
			return nil, err
		}
		events = append(events, pendingEvent{
			Scope: "room", RoomID: roomID,
			Payload: model.NewEnvelope(model.WSChatMessage, sysMsg),
		})

		if ev, changed, err := s.d.syncRoomOccupancy(ctx, q, room); err != nil {
			return nil, err
		} else if changed {
			lockMsg, err := s.d.appendSystemMessage(ctx, q, roomID, model.SysLocked,
				"人满了，这车稳了，等开局吧")
			if err != nil {
				return nil, err
			}
			events = append(events,
				pendingEvent{Scope: "room", RoomID: roomID,
					Payload: model.NewEnvelope(model.WSChatMessage, lockMsg)},
				pendingEvent{Scope: "room", RoomID: roomID,
					Payload: model.NewEnvelope(model.WSRoomStatus, model.RoomStatusData{
						RoomID: roomID, Status: room.Status, StatusLabel: room.Status.Label(),
						Event: string(ev), Reason: "席位坐满",
					})},
			)
		}

		snap := room.Snapshot()
		events = append(events,
			pendingEvent{Scope: "room", RoomID: roomID,
				Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
			pendingEvent{Scope: "wall",
				Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
		)

		out = &JoinResult{Room: room, Snapshot: snap, Member: member, Idempotent: false}
		return events, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Leave 执行退车：释放席位并在满员回流时把房间打回招募中。
func (s *SlotService) Leave(ctx context.Context, roomID, userID int64) (*model.SlotSnapshot, error) {
	var snap model.SlotSnapshot
	err := s.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		if room.OwnerID == userID {
			// 车主退车会让房间失去负责人，语义上应该是「解散」。
			// 这里拒绝而不是静默转成解散，避免用户误操作直接炸掉整车。
			return nil, apperr.ErrOwnerCannotLeave
		}
		if room.Status.IsTerminal() {
			return nil, apperr.ErrRoomClosed.
				WithMessage("这辆车已经「" + room.Status.Label() + "」，不用退了")
		}

		member, err := s.d.Members.LockActiveForUpdate(ctx, q, roomID, userID)
		if err != nil {
			if repository.IsNoRows(err) {
				return nil, apperr.ErrNotOnBoard
			}
			return nil, err
		}

		changed, err := s.d.applyMemberEvent(ctx, q, room, member, fsm.EvLeave, &userID, "主动退车")
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, apperr.ErrConflict.WithMessage("状态刚刚变过，请刷新后重试")
		}

		events, err := s.d.afterSeatRelease(ctx, q, room, userID, model.SysLeave, "退车了，位子空出来了")
		if err != nil {
			return nil, err
		}
		snap = room.Snapshot()
		return events, nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Kick 由车主把某个成员移出。
func (s *SlotService) Kick(ctx context.Context, roomID, ownerID, targetUserID int64, reason string) (*model.SlotSnapshot, error) {
	var snap model.SlotSnapshot
	err := s.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		if room.OwnerID != ownerID {
			return nil, apperr.ErrNotOwner
		}
		if targetUserID == ownerID {
			return nil, apperr.ErrForbidden.WithMessage("车主不能把自己踢下车")
		}
		if room.Status.IsTerminal() {
			return nil, apperr.ErrRoomClosed
		}

		member, err := s.d.Members.LockActiveForUpdate(ctx, q, roomID, targetUserID)
		if err != nil {
			if repository.IsNoRows(err) {
				return nil, apperr.ErrNotOnBoard.WithMessage("这个人不在车上")
			}
			return nil, err
		}
		changed, err := s.d.applyMemberEvent(ctx, q, room, member, fsm.EvKick, &ownerID, reason)
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, apperr.ErrConflict.WithMessage("状态刚刚变过，请刷新后重试")
		}

		note := "被车主请下车了"
		if reason != "" {
			note = "被车主请下车了（" + reason + "）"
		}
		events, err := s.d.afterSeatRelease(ctx, q, room, targetUserID, model.SysKick, note)
		if err != nil {
			return nil, err
		}
		snap = room.Snapshot()
		return events, nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// afterSeatRelease 收敛「席位被释放」之后的公共动作：
// 系统消息 + 满员回流流转 + 席位广播。退车与踢人共用，避免两处逻辑漂移。
func (d *Deps) afterSeatRelease(ctx context.Context, q repository.Querier, room *model.Room,
	userID int64, kind model.SystemMessageKind, phrase string) ([]pendingEvent, error) {

	user, err := d.Users.GetByID(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	events := make([]pendingEvent, 0, 4)

	sysMsg, err := d.appendSystemMessage(ctx, q, room.ID, kind, user.DisplayName()+" "+phrase)
	if err != nil {
		return nil, err
	}
	events = append(events, pendingEvent{Scope: "room", RoomID: room.ID,
		Payload: model.NewEnvelope(model.WSChatMessage, sysMsg)})

	if ev, changed, err := d.syncRoomOccupancy(ctx, q, room); err != nil {
		return nil, err
	} else if changed {
		reopenMsg, err := d.appendSystemMessage(ctx, q, room.ID, model.SysReopen,
			fmt.Sprintf("重新开放招募，%s", room.Headline()))
		if err != nil {
			return nil, err
		}
		events = append(events,
			pendingEvent{Scope: "room", RoomID: room.ID,
				Payload: model.NewEnvelope(model.WSChatMessage, reopenMsg)},
			pendingEvent{Scope: "room", RoomID: room.ID,
				Payload: model.NewEnvelope(model.WSRoomStatus, model.RoomStatusData{
					RoomID: room.ID, Status: room.Status, StatusLabel: room.Status.Label(),
					Event: string(ev), Reason: "有人退车",
				})},
		)
	}

	snap := room.Snapshot()
	events = append(events,
		pendingEvent{Scope: "room", RoomID: room.ID,
			Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
		pendingEvent{Scope: "wall",
			Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
	)
	return events, nil
}

// Confirm 把 PENDING 占位转正为 JOINED。
// 在 SLOT_AUTO_CONFIRM=true 的默认配置下用不到，保留以支持「车主审核制」。
func (s *SlotService) Confirm(ctx context.Context, roomID, actorID, targetUserID int64) (*model.SlotSnapshot, error) {
	var snap model.SlotSnapshot
	err := s.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		if room.OwnerID != actorID && actorID != targetUserID {
			return nil, apperr.ErrForbidden.WithMessage("只有车主或本人能确认这个占位")
		}
		member, err := s.d.Members.LockActiveForUpdate(ctx, q, roomID, targetUserID)
		if err != nil {
			if repository.IsNoRows(err) {
				return nil, apperr.ErrNotOnBoard
			}
			return nil, err
		}
		if member.HoldExpired() {
			return nil, apperr.ErrHoldExpired
		}
		changed, err := s.d.applyMemberEvent(ctx, q, room, member, fsm.EvConfirm, &actorID, "占位确认")
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, apperr.ErrConflict.WithMessage("状态刚刚变过，请刷新后重试")
		}
		snap = room.Snapshot()
		return []pendingEvent{
			{Scope: "room", RoomID: roomID, Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
			{Scope: "wall", Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// ReleaseExpiredHold 回收单条超时占位。
//
// 幂等设计（审核维度 8）：
//   - 走与用户请求完全相同的 withSlotLock + FOR UPDATE 路径，因此回收与
//     用户主动退车之间不会竞态。
//   - 如果用户已经自行退车，LockActiveForUpdate 会查不到记录，直接返回
//     released=false，不报错也不重复扣减。
func (s *SlotService) ReleaseExpiredHold(ctx context.Context, h repository.ExpiredHold) (bool, error) {
	released := false
	err := s.withSlotLock(ctx, h.RoomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		member, err := s.d.Members.LockActiveForUpdate(ctx, q, h.RoomID, h.UserID)
		if err != nil {
			if repository.IsNoRows(err) {
				return nil, nil // 已被处理过，幂等返回
			}
			return nil, err
		}
		if member.ID != h.MemberID || member.Status != model.MemberPending {
			return nil, nil
		}
		if !member.HoldExpired() {
			return nil, nil // TTL 在扫描到执行之间被刷新了
		}

		changed, err := s.d.applyMemberEvent(ctx, q, room, member, fsm.EvExpire, nil, "占位超时自动回收")
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, nil
		}
		released = true
		return s.d.afterSeatRelease(ctx, q, room, h.UserID, model.SysRelease, "占位超时，位子被系统收回")
	})
	if err != nil {
		return false, err
	}
	return released, nil
}

// OwnerLock 让车主在人数够但没坐满时提前锁车。
func (s *SlotService) OwnerLock(ctx context.Context, roomID, ownerID int64) (*model.SlotSnapshot, error) {
	var snap model.SlotSnapshot
	err := s.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		if room.OwnerID != ownerID {
			return nil, apperr.ErrNotOwner
		}
		if !room.IsViable() {
			return nil, apperr.ErrConflict.WithMessage(
				fmt.Sprintf("还没到最低成行人数（%d/%d），锁了也开不了", room.Occupied(), room.MinViable))
		}
		changed, err := s.d.applyRoomEvent(ctx, q, room, fsm.EvOwnerLock, &ownerID, "车主提前锁车")
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, apperr.ErrIllegalRoomTx.
				WithMessage("当前状态「" + room.Status.Label() + "」不能锁车")
		}
		msg, err := s.d.appendSystemMessage(ctx, q, roomID, model.SysLocked,
			"车主提前锁车了，就这些人开了")
		if err != nil {
			return nil, err
		}
		snap = room.Snapshot()
		return []pendingEvent{
			{Scope: "room", RoomID: roomID, Payload: model.NewEnvelope(model.WSChatMessage, msg)},
			{Scope: "room", RoomID: roomID, Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
			{Scope: "wall", Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// OwnerCancel 车主解散整车。
func (s *SlotService) OwnerCancel(ctx context.Context, roomID, ownerID int64, reason string) (*model.SlotSnapshot, error) {
	var snap model.SlotSnapshot
	err := s.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		if room.OwnerID != ownerID {
			return nil, apperr.ErrNotOwner
		}
		changed, err := s.d.applyRoomEvent(ctx, q, room, fsm.EvOwnerCancel, &ownerID, reason)
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, apperr.ErrIllegalRoomTx.
				WithMessage("当前状态「" + room.Status.Label() + "」不能解散")
		}
		note := "车主解散了这辆车"
		if reason != "" {
			note += "（" + reason + "）"
		}
		msg, err := s.d.appendSystemMessage(ctx, q, roomID, model.SysCancel, note)
		if err != nil {
			return nil, err
		}
		snap = room.Snapshot()
		return []pendingEvent{
			{Scope: "room", RoomID: roomID, Payload: model.NewEnvelope(model.WSChatMessage, msg)},
			{Scope: "room", RoomID: roomID, Payload: model.NewEnvelope(model.WSRoomStatus, model.RoomStatusData{
				RoomID: roomID, Status: room.Status, StatusLabel: room.Status.Label(),
				Event: string(fsm.EvOwnerCancel), Reason: reason,
			})},
			{Scope: "wall", Payload: model.NewEnvelope(model.WSRoomSlot, snap)},
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// AuditOccupancy 对账：比较 rooms 的聚合计数与 room_members 的实际行数。
//
// 这是 NFR-1 A-5「账目零漂移」的检测手段，同时也是 /readyz 的深度探测项。
// 返回 drift != 0 就说明席位账目出现了漂移，属于必须立刻排查的严重问题。
func (s *SlotService) AuditOccupancy(ctx context.Context, roomID int64) (aggregate, actual, drift int, err error) {
	room, err := s.d.Rooms.GetByID(ctx, s.d.Pool, roomID)
	if err != nil {
		return 0, 0, 0, err
	}
	actual, err = s.d.Members.CountActiveByRoom(ctx, s.d.Pool, roomID)
	if err != nil {
		return 0, 0, 0, err
	}
	aggregate = room.Occupied()
	return aggregate, actual, aggregate - actual, nil
}
