package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// mkRoom 造一个「3 小时后开局、6 人局、3 男 2 女 1 不限」的招募中房间。
func mkRoom(mut func(*Room)) *Room {
	r := &Room{
		ID:          1,
		Capacity:    6,
		MinViable:   4,
		MaleSeats:   3,
		FemaleSeats: 2,
		AnySeats:    1,
		Status:      RoomRecruiting,
		StartAt:     timeutil.Now().Add(3 * time.Hour),
	}
	if mut != nil {
		mut(r)
	}
	return r
}

// TestRoom_Headline 覆盖需求里点名的「5缺2」文案。
func TestRoom_Headline(t *testing.T) {
	cases := []struct {
		name   string
		joined int
		want   string
	}{
		{"空车", 0, "6缺6"},
		{"4 人已上车", 4, "6缺2"},
		{"满员", 6, "6人满员"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mkRoom(func(r *Room) { r.JoinedCount = tc.joined })
			if got := r.Headline(); got != tc.want {
				t.Fatalf("Headline() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestRoom_OccupiedCountsPending 断言占位中（PENDING）也算占席位。
//
// 这是防超载的语义地基：如果 Occupied 只数 JoinedCount，
// 最后一个位会被 N 个「占位中」的人同时抢到。
func TestRoom_OccupiedCountsPending(t *testing.T) {
	r := mkRoom(func(r *Room) {
		r.JoinedCount = 4
		r.PendingCount = 2
	})
	if got := r.Occupied(); got != 6 {
		t.Fatalf("Occupied() = %d，期望 6（4 已确认 + 2 占位中）", got)
	}
	if got := r.Remaining(); got != 0 {
		t.Fatalf("Remaining() = %d，期望 0", got)
	}
	if r.AcceptsJoin() {
		t.Fatal("席位已被占满（含占位中），不应再接受上车")
	}
}

// TestRoom_RemainingNeverNegative 断言账目异常时对外读数被夹紧。
//
// 数据库 CHECK 约束保证不会超载；但即使账目真的错了，
// 前端也不该看到「6缺-1」这种把 UI 打崩的数字。
func TestRoom_RemainingNeverNegative(t *testing.T) {
	r := mkRoom(func(r *Room) { r.JoinedCount = 99 })
	if got := r.Remaining(); got != 0 {
		t.Fatalf("Remaining() = %d，超载时应夹紧为 0", got)
	}
	if got := r.SeatRemaining(SeatMale); got != 3 {
		t.Fatalf("SeatRemaining(男) = %d，期望 3", got)
	}
	r.MaleTaken = 99
	if got := r.SeatRemaining(SeatMale); got != 0 {
		t.Fatalf("SeatRemaining(男) = %d，超额时应夹紧为 0", got)
	}
}

// TestRoom_SeatBucketsAlwaysThreeAndOrdered 断言席位桶顺序固定且不为 nil。
func TestRoom_SeatBucketsAlwaysThreeAndOrdered(t *testing.T) {
	r := mkRoom(nil)
	buckets := r.SeatBuckets()
	if len(buckets) != 3 {
		t.Fatalf("SeatBuckets() 长度 = %d，期望 3", len(buckets))
	}
	want := []SeatGender{SeatMale, SeatFemale, SeatAny}
	for i, g := range want {
		if buckets[i].Gender != g {
			t.Fatalf("第 %d 个席位桶是 %s，期望 %s（顺序必须固定，否则前端错位）", i, buckets[i].Gender, g)
		}
	}
	if buckets[0].Quota != 3 || buckets[1].Quota != 2 || buckets[2].Quota != 1 {
		t.Fatalf("配额读取错误: %+v", buckets)
	}
}

// TestRoom_SeatDetail 覆盖「已有3女」式明细，并断言零配额被隐藏。
func TestRoom_SeatDetail(t *testing.T) {
	r := mkRoom(func(r *Room) {
		r.FemaleSeats, r.FemaleTaken = 3, 3
		r.MaleSeats, r.MaleTaken = 3, 0
		r.AnySeats = 0
	})
	got := r.SeatDetail()
	// 文案用「角色席」而非「男生/女生」：这是需求里把性别名额重新定性为
	// 「剧本角色席位属性」的裁决结果，改文案会绕回歧视性表述。
	if !strings.Contains(got, SeatFemale.Label()+" 3/3") || !strings.Contains(got, SeatMale.Label()+" 0/3") {
		t.Fatalf("SeatDetail() = %q，缺少男女角色席明细", got)
	}
	if strings.Contains(got, SeatAny.Label()) {
		t.Fatalf("SeatDetail() = %q，零配额的席位类别不应出现", got)
	}
}

// TestRoom_IsAtRisk 覆盖「炸车预警」的全部触发与不触发条件。
//
// 这是需求里 “2小时后开局，再不来车就炸了” 的判据。
func TestRoom_IsAtRisk(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Room)
		want bool
	}{
		{
			name: "距开局 3 小时，超出预警窗",
			mut:  func(r *Room) { r.JoinedCount = 1 },
			want: false,
		},
		{
			name: "距开局 1 小时且人数不足，预警",
			mut:  func(r *Room) { r.StartAt = timeutil.Now().Add(time.Hour); r.JoinedCount = 1 },
			want: true,
		},
		{
			name: "距开局 1 小时但已达成行线，不预警",
			mut:  func(r *Room) { r.StartAt = timeutil.Now().Add(time.Hour); r.JoinedCount = 4 },
			want: false,
		},
		{
			name: "已过开局时间，不再预警（交由状态机置为炸车）",
			mut:  func(r *Room) { r.StartAt = timeutil.Now().Add(-time.Minute); r.JoinedCount = 1 },
			want: false,
		},
		{
			name: "已锁车，不预警",
			mut: func(r *Room) {
				r.Status = RoomLocked
				r.StartAt = timeutil.Now().Add(time.Hour)
				r.JoinedCount = 1
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mkRoom(tc.mut).IsAtRisk(); got != tc.want {
				t.Fatalf("IsAtRisk() = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestRoom_RiskHint 断言预警文案含「还差 N 人」，且终态文案正确。
func TestRoom_RiskHint(t *testing.T) {
	atRisk := mkRoom(func(r *Room) {
		r.StartAt = timeutil.Now().Add(90 * time.Minute)
		r.JoinedCount = 2
	})
	hint := atRisk.RiskHint()
	if !strings.Contains(hint, "还差 2 人") || !strings.Contains(hint, "车就炸了") {
		t.Fatalf("RiskHint() = %q，未命中炸车预警文案", hint)
	}

	expired := mkRoom(func(r *Room) { r.Status = RoomExpired })
	if got := expired.RiskHint(); !strings.Contains(got, "炸了") {
		t.Fatalf("炸车房 RiskHint() = %q", got)
	}
}

// TestRoom_AcceptsJoin 断言上车闸门的三个条件都生效。
func TestRoom_AcceptsJoin(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Room)
		want bool
	}{
		{"招募中且有位", nil, true},
		{"已满员", func(r *Room) { r.JoinedCount = 6 }, false},
		{"已锁车", func(r *Room) { r.Status = RoomLocked }, false},
		{"已炸车", func(r *Room) { r.Status = RoomExpired }, false},
		{"已过开局时间", func(r *Room) { r.StartAt = timeutil.Now().Add(-time.Second) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mkRoom(tc.mut).AcceptsJoin(); got != tc.want {
				t.Fatalf("AcceptsJoin() = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestRoom_SnapshotStartAtIsShanghai 断言快照里的时间是东八区。
// KB [Go][TZ]：直接回传 UTC 会让前端展示的开局时间差 8 小时。
func TestRoom_SnapshotStartAtIsShanghai(t *testing.T) {
	r := mkRoom(func(r *Room) { r.StartAt = r.StartAt.UTC() })
	snap := r.Snapshot()
	_, offset := snap.StartAt.Zone()
	if offset != 8*3600 {
		t.Fatalf("快照 StartAt 时区偏移 = %d 秒，期望 28800（东八区）", offset)
	}
	if snap.Seats == nil {
		t.Fatal("快照 Seats 不得为 nil")
	}
}

// TestEmptySlicesSerializeAsArrays 是 KB [Go][JSON] 那条教训的回归测试：
// 用 omitempty 修饰切片会让空集在 JSON 里整个字段消失，
// 前端 data.tags.map(...) 直接 TypeError。
func TestEmptySlicesSerializeAsArrays(t *testing.T) {
	t.Run("User.Tags", func(t *testing.T) {
		raw, err := json.Marshal(&User{ID: 1, Username: "u"})
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		if !strings.Contains(string(raw), `"tags":[]`) {
			t.Fatalf("空标签集应序列化为 \"tags\":[]，实际 %s", raw)
		}
	})

	t.Run("Backfill.Messages", func(t *testing.T) {
		bf := NewBackfill(nil, 0, 0, 0, 0, false)
		raw, err := json.Marshal(bf)
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		if !strings.Contains(string(raw), `"messages":[]`) {
			t.Fatalf("空消息集应序列化为 \"messages\":[]，实际 %s", raw)
		}
	})

	t.Run("PublicProfile.Tags", func(t *testing.T) {
		raw, err := json.Marshal((&User{ID: 1, Username: "u"}).Public())
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		if !strings.Contains(string(raw), `"tags":[]`) {
			t.Fatalf("公开档案的空标签集应序列化为 []，实际 %s", raw)
		}
	})
}

// TestEnumValidity 断言枚举白名单没有漏项，也没有误放非法值。
func TestEnumValidity(t *testing.T) {
	for _, g := range AllSeatGenders() {
		if !g.Valid() || g.Label() == "" {
			t.Fatalf("席位性别 %s 校验或文案缺失", g)
		}
	}
	if SeatGender("OTHER").Valid() {
		t.Fatal("未知席位性别不应通过校验")
	}
	for _, tag := range AllPlayerTags() {
		if !tag.Valid() || tag.Label() == "" || tag.Phrase() == "" {
			t.Fatalf("玩家标签 %s 校验或文案缺失", tag)
		}
	}
	if PlayerTag("XXX").Valid() {
		t.Fatal("未知玩家标签不应通过校验")
	}
	for _, rt := range []RoomType{TypeScript, TypeEscape} {
		if !rt.Valid() || rt.Label() == "" {
			t.Fatalf("房间类型 %s 校验或文案缺失", rt)
		}
	}
}

// TestMemberHoldExpiry 断言占位倒计时的判定与剩余秒数。
func TestMemberHoldExpiry(t *testing.T) {
	past := timeutil.Now().Add(-time.Minute)
	future := timeutil.Now().Add(2 * time.Minute)

	if !(&RoomMember{Status: MemberPending, HoldExpiresAt: &past}).HoldExpired() {
		t.Fatal("过期占位应判定为 HoldExpired")
	}
	if (&RoomMember{Status: MemberPending, HoldExpiresAt: &future}).HoldExpired() {
		t.Fatal("未到期占位不应判定为 HoldExpired")
	}
	if (&RoomMember{Status: MemberJoined, HoldExpiresAt: &past}).HoldExpired() {
		t.Fatal("已确认成员没有占位倒计时，不应判定为过期")
	}

	m := &RoomMember{Status: MemberPending, HoldExpiresAt: &future}
	if left := m.HoldSecondsLeft(); left <= 0 || left > 120 {
		t.Fatalf("HoldSecondsLeft() = %d，期望落在 (0,120]", left)
	}
	if got := (&RoomMember{Status: MemberPending, HoldExpiresAt: &past}).HoldSecondsLeft(); got != 0 {
		t.Fatalf("已过期占位剩余秒数应为 0，实际 %d", got)
	}
}
