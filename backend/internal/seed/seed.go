// Package seed 灌入演示数据。
//
// 幂等：以「是否已存在房间」为判据，重复启动不会重复灌数据。
// 目的是让 `docker compose up` 之后墙上立刻有内容——空墙无法验证任何
// 视觉与交互需求（拼车墙、倒计时、炸车预警、席位徽章都需要真实数据才看得见）。
package seed

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// DemoPassword 是全部演示账号的口令，同步写入 README「测试账号」章节。
const DemoPassword = "jbs12345"

type seedUser struct {
	Username string
	Nickname string
	City     string
	Bio      string
	Tags     []model.PlayerTag
}

type seedRoom struct {
	OwnerIdx   int
	Title      string
	Script     string
	Venue      string
	City       string
	Address    string
	Type       model.RoomType
	Difficulty int
	Theme      string
	Notes      string
	StartIn    time.Duration
	Male       int
	Female     int
	Any        int
	MinViable  int
	OwnerSeat  model.SeatGender
	// JoinerIdx 是除车主外要自动上车的用户下标，用来造出各种席位组合。
	JoinerIdx []int
	JoinSeats []model.SeatGender
}

var demoUsers = []seedUser{
	{"alice", "阿狸不睡觉", "上海", "三年剧本杀，情感本会哭，硬核本会算", []model.PlayerTag{model.TagEmotion, model.TagHardcore}},
	{"bob", "老鲍", "上海", "只玩恐怖本，越吓越爽", []model.PlayerTag{model.TagHorror, model.TagVeteran}},
	{"carol", "卡罗尔", "上海", "欢乐局氛围组，负责活跃气氛", []model.PlayerTag{model.TagFun}},
	{"dave", "大卫不说话", "上海", "刚入坑两个月，求大佬带", []model.PlayerTag{model.TagRookie}},
	{"erin", "小艾", "杭州", "机制本爱好者，喜欢算牌", []model.PlayerTag{model.TagHardcore, model.TagVeteran}},
	{"frank", "阿福", "杭州", "密室常客，体感机关都不怕", []model.PlayerTag{model.TagHorror, model.TagFun}},
	{"grace", "格雷斯", "北京", "情感本收割机，纸巾自带", []model.PlayerTag{model.TagEmotion, model.TagRookie}},
	{"henry", "亨利", "北京", "老玩家，主要来带新人", []model.PlayerTag{model.TagVeteran, model.TagFun}},
}

var demoRooms = []seedRoom{
	{
		OwnerIdx: 0, Title: "周五晚场，缺两个人就开", Script: "《年轮》", Venue: "谜想岛剧本杀（人民广场店）",
		City: "上海", Address: "黄浦区南京东路 300 号 5F", Type: model.TypeScript,
		Difficulty: 3, Theme: "情感沉浸", Notes: "情感本，容易哭，建议带纸巾。新手也能玩，DM 会带。",
		StartIn: 30 * time.Hour, Male: 3, Female: 3, Any: 0, MinViable: 5, OwnerSeat: model.SeatFemale,
		JoinerIdx: []int{2, 6}, JoinSeats: []model.SeatGender{model.SeatFemale, model.SeatFemale},
	},
	{
		OwnerIdx: 1, Title: "恐怖本急招，就差一个男角色", Script: "《雾岛》", Venue: "深夜怪谈馆（静安店）",
		City: "上海", Address: "静安区愚园路 88 号 2F", Type: model.TypeScript,
		Difficulty: 4, Theme: "恐怖惊悚", Notes: "真人 NPC 会跳脸，心脏不好的慎入。凌晨场，注意打车。",
		StartIn: 90 * time.Minute, Male: 3, Female: 2, Any: 0, MinViable: 5, OwnerSeat: model.SeatMale,
		JoinerIdx: []int{0, 2, 3}, JoinSeats: []model.SeatGender{model.SeatFemale, model.SeatFemale, model.SeatMale},
	},
	{
		OwnerIdx: 2, Title: "欢乐撕逼局，人齐就走", Script: "《甜蜜复仇》", Venue: "笑场剧本杀（徐家汇店）",
		City: "上海", Address: "徐汇区肇嘉浜路 1000 号 3F", Type: model.TypeScript,
		Difficulty: 2, Theme: "欢乐撕逼", Notes: "轻松本，全程笑点，适合朋友一起来。",
		StartIn: 5 * time.Hour, Male: 2, Female: 2, Any: 2, MinViable: 4, OwnerSeat: model.SeatAny,
		JoinerIdx: []int{3}, JoinSeats: []model.SeatGender{model.SeatMale},
	},
	{
		OwnerIdx: 4, Title: "硬核机制本，只收有经验的", Script: "《千岛谜城》", Venue: "推理协会（武林广场店）",
		City: "杭州", Address: "下城区延安路 600 号 4F", Type: model.TypeScript,
		Difficulty: 5, Theme: "机制阵营", Notes: "六小时长本，硬核机制，建议 5 本以上经验再来。",
		StartIn: 52 * time.Hour, Male: 4, Female: 2, Any: 0, MinViable: 6, OwnerSeat: model.SeatMale,
		JoinerIdx: []int{5}, JoinSeats: []model.SeatGender{model.SeatMale},
	},
	{
		OwnerIdx: 5, Title: "密室双人本，找个搭子", Script: "《失落回廊》", Venue: "零号密室（滨江店）",
		City: "杭州", Address: "滨江区江南大道 20 号 B1", Type: model.TypeEscape,
		Difficulty: 3, Theme: "解谜逃脱", Notes: "纯解谜无恐怖元素，两人刚好。",
		StartIn: 20 * time.Hour, Male: 0, Female: 0, Any: 2, MinViable: 2, OwnerSeat: model.SeatAny,
	},
	{
		OwnerIdx: 6, Title: "情感本，女孩子多一点更好哭", Script: "《长夜将明》", Venue: "月光剧本社（三里屯店）",
		City: "北京", Address: "朝阳区三里屯太古里北区 3F", Type: model.TypeScript,
		Difficulty: 3, Theme: "情感沉浸", Notes: "眼泪本，DM 很会带情绪，别化妆来。",
		StartIn: 44 * time.Hour, Male: 2, Female: 4, Any: 0, MinViable: 5, OwnerSeat: model.SeatFemale,
		JoinerIdx: []int{7}, JoinSeats: []model.SeatGender{model.SeatMale},
	},
	{
		OwnerIdx: 7, Title: "满员局，围观可以但上不了车", Script: "《暗河》", Venue: "谜盒剧本杀（国贸店）",
		City: "北京", Address: "朝阳区建国门外大街 1 号 6F", Type: model.TypeScript,
		Difficulty: 4, Theme: "硬核推理", Notes: "已经满员了，留着给大家看满员卡片长什么样。",
		StartIn: 8 * time.Hour, Male: 2, Female: 1, Any: 0, MinViable: 3, OwnerSeat: model.SeatMale,
		JoinerIdx: []int{6, 4}, JoinSeats: []model.SeatGender{model.SeatFemale, model.SeatMale},
	},
	{
		OwnerIdx: 3, Title: "快炸了！一小时后开局还差三个", Script: "《孤岛惊魂》", Venue: "谜想岛剧本杀（五角场店）",
		City: "上海", Address: "杨浦区淞沪路 100 号 4F", Type: model.TypeScript,
		Difficulty: 3, Theme: "恐怖惊悚", Notes: "真的快炸了，谁来救一下。",
		StartIn: 70 * time.Minute, Male: 3, Female: 3, Any: 0, MinViable: 5, OwnerSeat: model.SeatMale,
		JoinerIdx: []int{1}, JoinSeats: []model.SeatGender{model.SeatMale},
	},
}

// Run 灌入演示数据。已有房间时直接返回。
func Run(ctx context.Context, d *service.Deps, rooms *service.RoomService, slot *service.SlotService) error {
	count, err := d.Rooms.CountAll(ctx, d.Pool)
	if err != nil {
		return fmt.Errorf("seed: 统计既有房间失败: %w", err)
	}
	if count > 0 {
		logger.Info("已有房间数据，跳过种子灌入", "rooms", count)
		return nil
	}

	userIDs, err := ensureUsers(ctx, d)
	if err != nil {
		return err
	}

	created := 0
	for i, r := range demoRooms {
		ownerID, ok := userIDs[demoUsers[r.OwnerIdx].Username]
		if !ok {
			continue
		}
		card, err := rooms.Create(ctx, ownerID, service.CreateRoomInput{
			Title:       r.Title,
			ScriptName:  r.Script,
			VenueName:   r.Venue,
			City:        r.City,
			Address:     r.Address,
			RoomType:    r.Type,
			Difficulty:  r.Difficulty,
			Theme:       r.Theme,
			Notes:       r.Notes,
			StartAt:     timeutil.Now().Add(r.StartIn),
			MaleSeats:   r.Male,
			FemaleSeats: r.Female,
			AnySeats:    r.Any,
			MinViable:   r.MinViable,
			OwnerSeat:   r.OwnerSeat,
		})
		if err != nil {
			logger.Error("种子房间创建失败", "index", i, "title", r.Title, "error", err)
			continue
		}
		created++

		// 让其他人上车，造出「5缺2」「男席已满」「满员」等各种真实形态。
		for j, idx := range r.JoinerIdx {
			uid, ok := userIDs[demoUsers[idx].Username]
			if !ok {
				continue
			}
			seat := model.SeatAny
			if j < len(r.JoinSeats) {
				seat = r.JoinSeats[j]
			}
			if _, err := slot.Join(ctx, card.Room.ID, uid, seat); err != nil {
				logger.Warn("种子成员上车失败（通常是席位配置不匹配，可忽略）",
					"room_id", card.Room.ID, "user_id", uid, "seat", seat, "error", err)
			}
		}
	}

	logger.Info("种子数据灌入完成", "users", len(userIDs), "rooms", created)
	return nil
}

func ensureUsers(ctx context.Context, d *service.Deps) (map[string]int64, error) {
	out := make(map[string]int64, len(demoUsers))
	hash, err := bcrypt.GenerateFromPassword([]byte(DemoPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("seed: 生成口令哈希失败: %w", err)
	}

	for _, su := range demoUsers {
		// 已存在就复用，让种子逻辑在部分失败后可以重跑。
		if existing, err := d.Users.GetByUsername(ctx, d.Pool, su.Username); err == nil {
			out[su.Username] = existing.ID
			continue
		} else if !repository.IsNoRows(err) {
			return nil, fmt.Errorf("seed: 查询用户 %s 失败: %w", su.Username, err)
		}

		u := &model.User{
			Username:     su.Username,
			PasswordHash: string(hash),
			Nickname:     su.Nickname,
			City:         su.City,
			Bio:          su.Bio,
			Avatar:       localAvatar(su.Username),
			Reputation:   100,
		}
		err := repository.InTx(ctx, d.Pool, func(q repository.Querier) error {
			if err := d.Users.Create(ctx, q, u); err != nil {
				return err
			}
			return d.Users.ReplaceTags(ctx, q, u.ID, su.Tags)
		})
		if err != nil {
			return nil, fmt.Errorf("seed: 创建用户 %s 失败: %w", su.Username, err)
		}
		out[su.Username] = u.ID
	}
	return out, nil
}

// localAvatar 与 service 层的 defaultAvatar 保持一致的本地渲染约定。
func localAvatar(seed string) string {
	var sum uint32
	for _, r := range seed {
		sum = sum*31 + uint32(r)
	}
	return fmt.Sprintf("local:%d", sum%8)
}
