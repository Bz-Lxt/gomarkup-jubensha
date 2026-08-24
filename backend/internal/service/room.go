package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
	"github.com/alkaid/jubensha-carpool/backend/internal/validate"
)

// RoomService 处理房间的创建与读取。席位写操作全部在 SlotService。
type RoomService struct{ d *Deps }

func NewRoomService(d *Deps) *RoomService { return &RoomService{d: d} }

// CreateRoomInput 是开车入参。
type CreateRoomInput struct {
	Title       string           `json:"title"`
	ScriptName  string           `json:"script_name"`
	VenueName   string           `json:"venue_name"`
	City        string           `json:"city"`
	Address     string           `json:"address"`
	RoomType    model.RoomType   `json:"room_type"`
	Difficulty  int              `json:"difficulty"`
	Theme       string           `json:"theme"`
	Notes       string           `json:"notes"`
	StartAt     time.Time        `json:"start_at"`
	MaleSeats   int              `json:"male_seats"`
	FemaleSeats int              `json:"female_seats"`
	AnySeats    int              `json:"any_seats"`
	MinViable   int              `json:"min_viable"`
	OwnerSeat   model.SeatGender `json:"owner_seat"`
}

// RoomCard 是拼车墙上的一张卡片。
type RoomCard struct {
	Room     *model.Room         `json:"room"`
	Snapshot model.SlotSnapshot  `json:"snapshot"`
	Owner    model.PublicProfile `json:"owner"`
	Members  []model.MemberView  `json:"members"`
	MyStatus model.MemberStatus  `json:"my_status"`
	AmOwner  bool                `json:"am_owner"`
	AmOnCar  bool                `json:"am_on_car"`
	MySeat   model.SeatGender    `json:"my_seat"`
	Tags     []model.PlayerTag   `json:"tags"`
	TypeName string              `json:"type_name"`
	Unread   int                 `json:"unread"`
}

// WallResult 是拼车墙查询结果。
type WallResult struct {
	Items  []RoomCard `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// Create 开一辆新车。车主自动作为首位成员上车。
func (s *RoomService) Create(ctx context.Context, ownerID int64, in CreateRoomInput) (*RoomCard, error) {
	room, err := s.buildRoom(ownerID, in)
	if err != nil {
		return nil, err
	}
	ownerSeat := in.OwnerSeat
	if !ownerSeat.Valid() {
		ownerSeat = model.SeatAny
	}
	if room.SeatQuota(ownerSeat) == 0 {
		return nil, apperr.ErrValidation.
			WithMessage("车主选的「"+ownerSeat.Label()+"」在席位配置里是 0 个，选不了").
			WithDetail("field", "owner_seat")
	}

	err = repository.InTx(ctx, s.d.Pool, func(q repository.Querier) error {
		if err := s.d.Rooms.Create(ctx, q, room); err != nil {
			return err
		}
		// 车主即刻占一个席位。走和普通用户完全一样的账目通路，
		// 而不是给 rooms 表硬塞 joined_count=1，否则两条通路会分叉。
		joinedAt := timeutil.Now()
		owner := &model.RoomMember{
			RoomID:     room.ID,
			UserID:     ownerID,
			SeatGender: ownerSeat,
			Status:     model.MemberJoined,
			IsOwner:    true,
			JoinedAt:   &joinedAt,
		}
		if err := s.d.Members.Insert(ctx, q, owner); err != nil {
			return err
		}
		if err := s.d.adjustSeats(ctx, q, room, ownerSeat, model.MemberStatus(""), model.MemberJoined); err != nil {
			return err
		}
		if err := s.d.Logs.Append(ctx, q, &model.StateLog{
			RoomID: room.ID, Scope: "room",
			FromStatus: string(model.RoomDraft), ToStatus: string(model.RoomRecruiting),
			Event: "PUBLISH", ActorID: &ownerID, Reason: "开车",
		}); err != nil {
			return err
		}
		_, err := s.d.appendSystemMessage(ctx, q, room.ID, model.SysJoin,
			fmt.Sprintf("车主开车了：%s · %s，%s", room.ScriptName, room.VenueName, room.Headline()))
		return err
	})
	if err != nil {
		return nil, err
	}

	// 新车上墙，通知所有正在看墙的人。
	s.d.emit(ctx, []pendingEvent{
		{Scope: "wall", Payload: model.NewEnvelope(model.WSRoomSlot, room.Snapshot())},
	})
	return s.Detail(ctx, room.ID, ownerID)
}

func (s *RoomService) buildRoom(ownerID int64, in CreateRoomInput) (*model.Room, error) {
	title, err := validate.TextRange("title", in.Title, 2, 40)
	if err != nil {
		return nil, err
	}
	scriptName, err := validate.TextRange("script_name", in.ScriptName, 1, 40)
	if err != nil {
		return nil, err
	}
	venueName, err := validate.TextRange("venue_name", in.VenueName, 1, 40)
	if err != nil {
		return nil, err
	}
	city, err := validate.TextRange("city", in.City, 1, 20)
	if err != nil {
		return nil, err
	}
	address, err := validate.TextRange("address", in.Address, 0, 80)
	if err != nil {
		return nil, err
	}
	notes, err := validate.TextRange("notes", in.Notes, 0, 200)
	if err != nil {
		return nil, err
	}
	if !in.RoomType.Valid() {
		return nil, apperr.ErrValidation.
			WithMessage("局类型只能是剧本杀或密室逃脱").WithDetail("field", "room_type")
	}
	if err := validate.IntRange("difficulty", in.Difficulty, 1, 5); err != nil {
		return nil, err
	}

	capacity := in.MaleSeats + in.FemaleSeats + in.AnySeats
	if err := validate.IntRange("capacity", capacity, 2, 12); err != nil {
		return nil, err
	}
	if in.MaleSeats < 0 || in.FemaleSeats < 0 || in.AnySeats < 0 {
		return nil, apperr.ErrSeatPlanInvalid
	}
	if err := validate.IntRange("min_viable", in.MinViable, 1, capacity); err != nil {
		return nil, err
	}
	// 开局时间必须留出足够的招募窗口，否则车一上墙就进入炸车预警，毫无意义。
	if timeutil.Until(in.StartAt) < 15*time.Minute {
		return nil, apperr.ErrStartAtInPast.
			WithMessage("开局时间至少要比现在晚 15 分钟，不然没人来得及上车")
	}
	if timeutil.Until(in.StartAt) > 60*24*time.Hour {
		return nil, apperr.ErrValidation.
			WithMessage("开局时间不能超过 60 天以后").WithDetail("field", "start_at")
	}

	return &model.Room{
		OwnerID:     ownerID,
		Title:       title,
		ScriptName:  scriptName,
		VenueName:   venueName,
		City:        city,
		Address:     address,
		RoomType:    in.RoomType,
		Difficulty:  in.Difficulty,
		Theme:       strings.TrimSpace(in.Theme),
		Notes:       notes,
		StartAt:     timeutil.In(in.StartAt),
		Capacity:    capacity,
		MinViable:   in.MinViable,
		MaleSeats:   in.MaleSeats,
		FemaleSeats: in.FemaleSeats,
		AnySeats:    in.AnySeats,
		Status:      model.RoomRecruiting,
	}, nil
}

// Wall 查询拼车墙。
func (s *RoomService) Wall(ctx context.Context, f repository.WallFilter, viewerID int64) (*WallResult, error) {
	rooms, total, err := s.d.Rooms.ListWall(ctx, s.d.Pool, f)
	if err != nil {
		return nil, err
	}
	cards, err := s.buildCards(ctx, rooms, viewerID)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 24
	}
	return &WallResult{Items: cards, Total: total, Limit: limit, Offset: f.Offset}, nil
}

// MyRooms 返回当前用户参与中的车。
func (s *RoomService) MyRooms(ctx context.Context, userID int64) (*WallResult, error) {
	ids, err := s.d.Members.ListRoomIDsByUser(ctx, s.d.Pool, userID)
	if err != nil {
		return nil, err
	}
	rooms, err := s.d.Rooms.ListByIDs(ctx, s.d.Pool, ids)
	if err != nil {
		return nil, err
	}
	cards, err := s.buildCards(ctx, rooms, userID)
	if err != nil {
		return nil, err
	}
	return &WallResult{Items: cards, Total: len(cards), Limit: len(cards), Offset: 0}, nil
}

// Detail 返回单个房间的完整信息（含成员列表与在线状态）。
func (s *RoomService) Detail(ctx context.Context, roomID, viewerID int64) (*RoomCard, error) {
	room, err := s.d.Rooms.GetByID(ctx, s.d.Pool, roomID)
	if err != nil {
		if repository.IsNoRows(err) {
			return nil, apperr.ErrRoomNotFound
		}
		return nil, err
	}
	cards, err := s.buildCards(ctx, []*model.Room{room}, viewerID)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, apperr.ErrRoomNotFound
	}
	card := cards[0]

	// 详情页才叠加在线状态：墙上几十张卡片没必要为每张查一次在线集合。
	online := map[int64]bool{}
	for _, uid := range s.d.Pub.OnlineUserIDs(ctx, roomID) {
		online[uid] = true
	}
	for i := range card.Members {
		card.Members[i].Online = online[card.Members[i].User.ID]
	}
	return &card, nil
}

// buildCards 批量组装卡片，一次性把成员与用户资料查完，避免 N+1。
func (s *RoomService) buildCards(ctx context.Context, rooms []*model.Room, viewerID int64) ([]RoomCard, error) {
	cards := make([]RoomCard, 0, len(rooms))
	if len(rooms) == 0 {
		return cards, nil
	}

	membersByRoom := map[int64][]*model.RoomMember{}
	userIDSet := map[int64]struct{}{}
	for _, r := range rooms {
		ms, err := s.d.Members.ListByRoom(ctx, s.d.Pool, r.ID, true)
		if err != nil {
			return nil, err
		}
		membersByRoom[r.ID] = ms
		userIDSet[r.OwnerID] = struct{}{}
		for _, m := range ms {
			userIDSet[m.UserID] = struct{}{}
		}
	}
	userIDs := make([]int64, 0, len(userIDSet))
	for id := range userIDSet {
		userIDs = append(userIDs, id)
	}
	users, err := s.d.Users.ListByIDs(ctx, s.d.Pool, userIDs)
	if err != nil {
		return nil, err
	}

	unread := map[int64]int{}
	if viewerID > 0 {
		counts, err := s.d.Messages.UnreadByUser(ctx, s.d.Pool, viewerID)
		if err != nil {
			return nil, err
		}
		for _, c := range counts {
			unread[c.RoomID] = c.Unread
		}
	}

	for _, r := range rooms {
		card := RoomCard{
			Room:     r,
			Snapshot: r.Snapshot(),
			Members:  []model.MemberView{},
			Tags:     []model.PlayerTag{},
			TypeName: r.RoomType.Label(),
			MySeat:   "",
			Unread:   unread[r.ID],
			AmOwner:  r.OwnerID == viewerID,
		}
		if owner, ok := users[r.OwnerID]; ok {
			card.Owner = owner.Public()
		}
		tagSeen := map[model.PlayerTag]bool{}
		for _, m := range membersByRoom[r.ID] {
			u, ok := users[m.UserID]
			if !ok {
				continue
			}
			card.Members = append(card.Members, model.NewMemberView(m, u))
			// 卡片上聚合展示已上车成员的玩家标签，让人一眼看出这车的气质
			// （硬核局还是欢乐局）。
			for _, t := range u.Tags {
				if !tagSeen[t] {
					tagSeen[t] = true
					card.Tags = append(card.Tags, t)
				}
			}
			if m.UserID == viewerID {
				card.MyStatus = m.Status
				card.MySeat = m.SeatGender
				card.AmOnCar = true
			}
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// Cities 返回墙上出现过的城市列表。
func (s *RoomService) Cities(ctx context.Context) ([]string, error) {
	return s.d.Rooms.ListCities(ctx, s.d.Pool)
}

// History 返回房间的状态流转审计记录。
func (s *RoomService) History(ctx context.Context, roomID int64) ([]model.StateLog, error) {
	return s.d.Logs.ListByRoom(ctx, s.d.Pool, roomID, 50)
}
