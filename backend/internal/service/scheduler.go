package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/fsm"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// Scheduler 是后台时间驱动器，负责三件事：
//
//  1. 回收超时占位（ReleaseExpiredHold）
//  2. 到点推进房间状态：成行 / 炸车 / 开局 / 结束
//  3. 发送开局前提醒
//
// 可靠性设计（对齐审核维度 8「异步任务可靠性」）：
//   - **全部状态都在数据库里**，进程重启后下一个 tick 会自然把积压的到期任务
//     全部处理掉，不存在「重启即丢失」。
//   - **每个动作都幂等**：状态流转带 from 条件；提醒的判重依据是数据库里
//     是否已存在对应系统消息，而不是内存标记。
//   - 席位回收走与用户请求完全相同的三层锁通路，因此不会和用户操作竞态。
type Scheduler struct {
	d     *Deps
	slot  *SlotService
	every time.Duration

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	// stats 供 /readyz 自陈运行状况。
	mu    sync.RWMutex
	stats SchedulerStats
}

// SchedulerStats 是调度器的可观测指标。
type SchedulerStats struct {
	Rounds        int64      `json:"rounds"`
	HoldsReleased int64      `json:"holds_released"`
	RoomsExpired  int64      `json:"rooms_expired"`
	RoomsStarted  int64      `json:"rooms_started"`
	RoomsFinished int64      `json:"rooms_finished"`
	Reminders     int64      `json:"reminders"`
	Errors        int64      `json:"errors"`
	LastRunAt     *time.Time `json:"last_run_at"`
}

// ReminderLead 是开局提醒的提前量。
const ReminderLead = time.Hour

// FinishGrace 是开局后自动收尾的宽限期。
const FinishGrace = 4 * time.Hour

func NewScheduler(d *Deps, slot *SlotService, every time.Duration) *Scheduler {
	if every <= 0 {
		every = 15 * time.Second
	}
	return &Scheduler{
		d: d, slot: slot, every: every,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start 启动后台循环。非阻塞。
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		defer close(s.doneCh)
		t := time.NewTicker(s.every)
		defer t.Stop()

		// 立刻跑一轮：进程刚重启时可能已经积压了一批到期任务，
		// 不应该干等一个完整周期。
		s.runOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				logger.Info("调度器随 context 取消而退出")
				return
			case <-s.stopCh:
				logger.Info("调度器已停止")
				return
			case <-t.C:
				s.runOnce(ctx)
			}
		}
	}()
	logger.Info("调度器已启动", "interval", s.every.String())
}

// Stop 停止循环并等待当前轮次结束。
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
	case <-time.After(10 * time.Second):
		logger.Warn("调度器停止超时，放弃等待")
	}
}

// Stats 返回运行指标快照。
func (s *Scheduler) Stats() SchedulerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// RunOnce 手动跑一轮，供测试与 /admin 触发使用。
func (s *Scheduler) RunOnce(ctx context.Context) { s.runOnce(ctx) }

func (s *Scheduler) runOnce(ctx context.Context) {
	// 每轮独立超时：单轮卡死不应该拖垮后续所有轮次。
	roundCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	now := timeutil.Now()
	released := s.reapExpiredHolds(roundCtx, now)
	expired, started, finished := s.advanceRooms(roundCtx, now)
	reminders := s.sendReminders(roundCtx, now)

	s.mu.Lock()
	s.stats.Rounds++
	s.stats.HoldsReleased += int64(released)
	s.stats.RoomsExpired += int64(expired)
	s.stats.RoomsStarted += int64(started)
	s.stats.RoomsFinished += int64(finished)
	s.stats.Reminders += int64(reminders)
	t := now
	s.stats.LastRunAt = &t
	s.mu.Unlock()

	if released+expired+started+finished+reminders > 0 {
		logger.Info("调度器完成一轮",
			"holds_released", released, "rooms_expired", expired,
			"rooms_started", started, "rooms_finished", finished, "reminders", reminders)
	}
}

func (s *Scheduler) bumpError() {
	s.mu.Lock()
	s.stats.Errors++
	s.mu.Unlock()
}

// reapExpiredHolds 回收 TTL 到期的占位。
func (s *Scheduler) reapExpiredHolds(ctx context.Context, now time.Time) int {
	holds, err := s.d.Members.ListExpiredHolds(ctx, s.d.Pool, now, 200)
	if err != nil {
		logger.C(ctx).Error("扫描超时占位失败", "error", err)
		s.bumpError()
		return 0
	}
	n := 0
	for _, h := range holds {
		ok, err := s.slot.ReleaseExpiredHold(ctx, h)
		if err != nil {
			logger.C(ctx).Error("回收超时占位失败",
				"room_id", h.RoomID, "user_id", h.UserID, "error", err)
			s.bumpError()
			continue
		}
		if ok {
			n++
		}
	}
	return n
}

// advanceRooms 推进到点房间的状态。
func (s *Scheduler) advanceRooms(ctx context.Context, now time.Time) (expired, started, finished int) {
	rooms, err := s.d.Rooms.ListDueForTransition(ctx, s.d.Pool, now, 200)
	if err != nil {
		logger.C(ctx).Error("扫描到点房间失败", "error", err)
		s.bumpError()
		return 0, 0, 0
	}

	for _, room := range rooms {
		kind, err := s.advanceOne(ctx, room.ID, now)
		if err != nil {
			logger.C(ctx).Error("推进房间状态失败", "room_id", room.ID, "error", err)
			s.bumpError()
			continue
		}
		switch kind {
		case "expired":
			expired++
		case "started":
			started++
		case "finished":
			finished++
		}
	}
	return expired, started, finished
}

// advanceOne 推进单个房间。走 SlotService 的锁通路，与用户操作互斥。
func (s *Scheduler) advanceOne(ctx context.Context, roomID int64, now time.Time) (string, error) {
	kind := ""
	err := s.slot.withSlotLock(ctx, roomID, func(ctx context.Context, q repository.Querier, room *model.Room) ([]pendingEvent, error) {
		if room.StartAt.After(now) {
			return nil, nil // 扫描到执行之间时间已变（例如车主改了开局时间）
		}
		events := make([]pendingEvent, 0, 3)

		switch room.Status {
		case model.RoomRecruiting:
			if room.IsViable() {
				changed, err := s.d.applyRoomEvent(ctx, q, room, fsm.EvDeadlineViable, nil, "到点且人数达标，自动成行")
				if err != nil || !changed {
					return nil, err
				}
				msg, err := s.d.appendSystemMessage(ctx, q, room.ID, model.SysStarting,
					fmt.Sprintf("到点了，%d 人成行，这局开！", room.Occupied()))
				if err != nil {
					return nil, err
				}
				events = append(events, roomEvt(room.ID, model.WSChatMessage, msg))
			} else {
				changed, err := s.d.applyRoomEvent(ctx, q, room, fsm.EvDeadlineFailed, nil,
					fmt.Sprintf("到点人数不足（%d/%d），炸车", room.Occupied(), room.MinViable))
				if err != nil || !changed {
					return nil, err
				}
				kind = "expired"
				msg, err := s.d.appendSystemMessage(ctx, q, room.ID, model.SysExpired,
					fmt.Sprintf("到点只凑到 %d 人（需要 %d 人），这车炸了，抱歉", room.Occupied(), room.MinViable))
				if err != nil {
					return nil, err
				}
				events = append(events, roomEvt(room.ID, model.WSChatMessage, msg))
			}

		case model.RoomLocked, model.RoomConfirmed:
			changed, err := s.d.applyRoomEvent(ctx, q, room, fsm.EvStart, nil, "到达开局时间")
			if err != nil || !changed {
				return nil, err
			}
			kind = "started"
			msg, err := s.d.appendSystemMessage(ctx, q, room.ID, model.SysStarted, "开局了，玩得开心")
			if err != nil {
				return nil, err
			}
			events = append(events, roomEvt(room.ID, model.WSChatMessage, msg))

		case model.RoomInProgress:
			if now.Sub(room.StartAt) < FinishGrace {
				return nil, nil
			}
			changed, err := s.d.applyRoomEvent(ctx, q, room, fsm.EvFinish, nil, "开局超过宽限期，自动收尾")
			if err != nil || !changed {
				return nil, err
			}
			kind = "finished"
			msg, err := s.d.appendSystemMessage(ctx, q, room.ID, model.SysFinished, "这局结束了，下次再约")
			if err != nil {
				return nil, err
			}
			events = append(events, roomEvt(room.ID, model.WSChatMessage, msg))

		default:
			return nil, nil
		}

		events = append(events,
			roomEvt(room.ID, model.WSRoomStatus, model.RoomStatusData{
				RoomID: room.ID, Status: room.Status, StatusLabel: room.Status.Label(),
				Event: "SCHEDULER", Reason: room.RiskHint(),
			}),
			pendingEvent{Scope: "wall", Payload: model.NewEnvelope(model.WSRoomSlot, room.Snapshot())},
		)
		return events, nil
	})
	return kind, err
}

// sendReminders 在开局前 ReminderLead 发一次提醒。
//
// 幂等依据是「数据库里是否已存在 room_starting 系统消息」，而不是内存标记。
// 这样即使进程反复重启，用户也只会收到一次提醒。
func (s *Scheduler) sendReminders(ctx context.Context, now time.Time) int {
	rooms, err := s.d.Rooms.ListStartingSoon(ctx, s.d.Pool, now, now.Add(ReminderLead))
	if err != nil {
		logger.C(ctx).Error("扫描即将开局房间失败", "error", err)
		s.bumpError()
		return 0
	}

	n := 0
	for _, room := range rooms {
		sent, err := s.d.Messages.HasSystemMessage(ctx, s.d.Pool, room.ID, model.SysStarting)
		if err != nil {
			logger.C(ctx).Error("检查提醒是否已发送失败", "room_id", room.ID, "error", err)
			s.bumpError()
			continue
		}
		if sent {
			continue
		}

		var msg *model.Message
		err = repository.InTx(ctx, s.d.Pool, func(q repository.Querier) error {
			// 事务内再查一次，避免两个副本同时通过了上面的检查。
			again, err := s.d.Messages.HasSystemMessage(ctx, q, room.ID, model.SysStarting)
			if err != nil || again {
				return err
			}
			msg, err = s.d.appendSystemMessage(ctx, q, room.ID, model.SysStarting, room.RiskHint())
			return err
		})
		if err != nil {
			logger.C(ctx).Error("发送开局提醒失败", "room_id", room.ID, "error", err)
			s.bumpError()
			continue
		}
		if msg != nil {
			s.d.emit(ctx, []pendingEvent{roomEvt(room.ID, model.WSChatMessage, msg)})
			n++
		}
	}
	return n
}

func roomEvt(roomID int64, t string, data any) pendingEvent {
	return pendingEvent{Scope: "room", RoomID: roomID, Payload: model.NewEnvelope(t, data)}
}
