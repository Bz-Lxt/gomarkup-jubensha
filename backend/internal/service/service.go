// Package service 承载全部业务规则与事务边界。
//
// 分层铁律：
//   - handler 不写 SQL，service 不碰 HTTP，repository 不做业务判断。
//   - 事务只在本层开启。跨表写入必须在同一事务内完成。
//   - 广播只在事务**提交之后**发出。事务内广播会导致回滚后客户端已经收到
//     一个从未发生的事件，这类幽灵状态极难排查。
package service

import (
	"context"
	"database/sql"

	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/lock"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
)

// Publisher 是广播出口的抽象，由 ws.Hub 实现。
//
// 用接口而不是直接依赖 ws 包，是为了打断 service ↔ ws 的依赖环，
// 同时让 service 的单元测试可以塞一个记录型假实现来断言「该广播的都广播了」。
type Publisher interface {
	// PublishRoom 把事件发布到房间频道。实现方负责跨节点分发 + 本地扇出。
	PublishRoom(ctx context.Context, roomID int64, env model.Envelope)
	// PublishWall 把事件发布到拼车墙公共频道（席位变动需要让墙上所有人看到）。
	PublishWall(ctx context.Context, env model.Envelope)
	// OnlineUserIDs 返回该房间当前在线的用户 ID。
	OnlineUserIDs(ctx context.Context, roomID int64) []int64
}

// noopPublisher 在 Hub 尚未注入时兜底，避免 nil 调用 panic。
type noopPublisher struct{}

func (noopPublisher) PublishRoom(context.Context, int64, model.Envelope) {}
func (noopPublisher) PublishWall(context.Context, model.Envelope)        {}
func (noopPublisher) OnlineUserIDs(context.Context, int64) []int64       { return []int64{} }

// Deps 是所有 service 共享的依赖集合。
type Deps struct {
	Cfg      *config.Config
	Pool     *sql.DB
	Guard    *lock.SlotGuard
	Users    *repository.UserRepo
	Rooms    *repository.RoomRepo
	Members  *repository.MemberRepo
	Messages *repository.MessageRepo
	Logs     *repository.StateLogRepo
	Pub      Publisher
}

// NewDeps 组装依赖，并为 Publisher 提供安全默认值。
func NewDeps(cfg *config.Config, pool *sql.DB, guard *lock.SlotGuard) *Deps {
	return &Deps{
		Cfg:      cfg,
		Pool:     pool,
		Guard:    guard,
		Users:    repository.NewUserRepo(),
		Rooms:    repository.NewRoomRepo(),
		Members:  repository.NewMemberRepo(),
		Messages: repository.NewMessageRepo(),
		Logs:     repository.NewStateLogRepo(),
		Pub:      noopPublisher{},
	}
}

// SetPublisher 在 Hub 构造完成后注入。
func (d *Deps) SetPublisher(p Publisher) {
	if p != nil {
		d.Pub = p
	}
}
