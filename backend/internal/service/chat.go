package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
)

// ChatService 处理房内消息的落库、广播与离线补齐。
type ChatService struct{ d *Deps }

func NewChatService(d *Deps) *ChatService { return &ChatService{d: d} }

// EnsureMember 是聊天室的准入判据：只有占着席位的人才能读写房内消息。
//
// WS 握手和每一条 HTTP 聊天请求都要过这一关。仅靠「前端不显示入口」是不够的，
// 那不是权限控制（NFR-4 越权防护）。
func (s *ChatService) EnsureMember(ctx context.Context, roomID, userID int64) error {
	ok, err := s.d.Members.IsActiveMember(ctx, s.d.Pool, roomID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.ErrNotRoomMember
	}
	return nil
}

// Send 落库并广播一条用户消息。
func (s *ChatService) Send(ctx context.Context, roomID, userID int64, in model.ChatSendData) (*model.Message, error) {
	if err := s.EnsureMember(ctx, roomID, userID); err != nil {
		return nil, err
	}

	msgType := model.MsgType(strings.ToUpper(strings.TrimSpace(string(in.MsgType))))
	if msgType == "" {
		msgType = model.MsgText
	}
	if msgType == model.MsgSystem {
		// 系统消息只能由服务端产生，否则用户可以伪造「车主已锁车」这类公告。
		return nil, apperr.ErrForbidden.WithMessage("不能手动发送系统消息")
	}
	if !msgType.Valid() {
		return nil, apperr.ErrWSPayloadInvalid.WithDetail("field", "msg_type")
	}

	content := strings.TrimSpace(in.Content)
	tagCode := ""

	if msgType == model.MsgTag {
		// 一键标签消息：正文由服务端按标签生成，不采信客户端传的文案，
		// 否则「标签气泡」就成了任意富文本注入点。
		tag := model.PlayerTag(strings.ToUpper(strings.TrimSpace(in.TagCode)))
		if !tag.Valid() {
			return nil, apperr.ErrUnknownPlayerTag.WithDetail("tag_code", in.TagCode)
		}
		tagCode = string(tag)
		content = tag.Phrase()
	}

	if content == "" {
		return nil, apperr.ErrMessageEmpty
	}
	if utf8.RuneCountInString(content) > s.d.Cfg.MessageMaxLen {
		return nil, apperr.ErrMessageTooLong.WithDetail("max", s.d.Cfg.MessageMaxLen)
	}

	clientMsgID := strings.TrimSpace(in.ClientMsgID)
	if utf8.RuneCountInString(clientMsgID) > 64 {
		return nil, apperr.ErrWSPayloadInvalid.WithDetail("field", "client_msg_id")
	}

	var out *model.Message
	err := repository.InTx(ctx, s.d.Pool, func(q repository.Querier) error {
		// 重发幂等：网络抖动导致客户端重试时，同一 client_msg_id 只落一条。
		if clientMsgID != "" {
			if existing, err := s.d.Messages.GetByClientMsgID(ctx, q, roomID, userID, clientMsgID); err == nil {
				out = existing
				return nil
			} else if !repository.IsNoRows(err) {
				return err
			}
		}

		seq, err := s.d.Rooms.NextMsgSeq(ctx, q, roomID)
		if err != nil {
			return err
		}
		msg := &model.Message{
			RoomID:      roomID,
			Seq:         seq,
			SenderID:    &userID,
			MsgType:     msgType,
			Content:     content,
			TagCode:     tagCode,
			ClientMsgID: clientMsgID,
		}
		if err := s.d.Messages.Insert(ctx, q, msg); err != nil {
			if repository.IsUniqueViolation(err) {
				// 并发重发撞上 uq_msg_client，回读既有那条即可。
				if existing, gErr := s.d.Messages.GetByClientMsgID(ctx, q, roomID, userID, clientMsgID); gErr == nil {
					out = existing
					return nil
				}
			}
			return err
		}
		// 发送者自己的昵称/头像补上，让广播出去的消息自带展示信息。
		if u, err := s.d.Users.GetByID(ctx, q, userID); err == nil {
			msg.SenderName = u.DisplayName()
			msg.SenderAvatar = u.Avatar
		}
		out = msg
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 提交后广播。发送者自己也会通过 WS 收到这条，用 client_msg_id 替换乐观气泡。
	s.d.Pub.PublishRoom(ctx, roomID, model.NewEnvelope(model.WSChatMessage, out))
	return out, nil
}

// Backfill 实现 Requirements C-3 裁决的「游标增量 + 阈值降级全量」。
//
//	gap == 0          → 无需拉取
//	gap <= BackfillMax → 增量：返回 (lastSeenSeq, latest] 全部消息
//	gap >  BackfillMax → 降级：只返回最近 BackfillMax 条，并置 truncated=true
//
// 阈值存在的理由：用户离线三天后回来，房间里可能积了几千条消息。一次性推给
// 前端既拖慢首屏又没人会往上翻那么远，不如明确告诉他「中间省略了」。
func (s *ChatService) Backfill(ctx context.Context, roomID, userID, lastSeenSeq int64) (*model.Backfill, error) {
	if err := s.EnsureMember(ctx, roomID, userID); err != nil {
		return nil, err
	}
	if lastSeenSeq < 0 {
		lastSeenSeq = 0
	}

	latest, err := s.d.Messages.LatestSeq(ctx, s.d.Pool, roomID)
	if err != nil {
		return nil, err
	}
	if lastSeenSeq >= latest {
		return model.NewBackfill(nil, lastSeenSeq, latest, latest, 0, false), nil
	}

	gap := latest - lastSeenSeq
	maxN := int64(s.d.Cfg.BackfillMax)

	if gap <= maxN {
		msgs, err := s.d.Messages.ListRange(ctx, s.d.Pool, roomID, lastSeenSeq, latest, s.d.Cfg.BackfillMax)
		if err != nil {
			return nil, err
		}
		return model.NewBackfill(msgs, lastSeenSeq, latest, latest, gap, false), nil
	}

	logger.C(ctx).Info("离线消息过多，触发全量降级",
		"room_id", roomID, "user_id", userID, "gap", gap, "cap", maxN)
	msgs, err := s.d.Messages.ListLatest(ctx, s.d.Pool, roomID, s.d.Cfg.BackfillMax)
	if err != nil {
		return nil, err
	}
	from := lastSeenSeq
	if len(msgs) > 0 {
		from = msgs[0].Seq - 1
	}
	return model.NewBackfill(msgs, from, latest, latest, gap, true), nil
}

// Cursor 返回用户在该房间的已读水位。
func (s *ChatService) Cursor(ctx context.Context, roomID, userID int64) (int64, error) {
	return s.d.Messages.GetCursor(ctx, s.d.Pool, roomID, userID)
}

// Ack 推进已读水位。游标只前进不后退（由 SQL 的 GREATEST 保证）。
func (s *ChatService) Ack(ctx context.Context, roomID, userID, seq int64) error {
	if seq < 0 {
		return apperr.ErrWSPayloadInvalid.WithDetail("field", "seq")
	}
	return s.d.Messages.UpsertCursor(ctx, s.d.Pool, roomID, userID, seq)
}

// Unread 返回该用户所有在车房间的未读数。
func (s *ChatService) Unread(ctx context.Context, userID int64) ([]repository.UnreadCount, error) {
	return s.d.Messages.UnreadByUser(ctx, s.d.Pool, userID)
}

// LatestSeq 返回房间的消息水位，供 WS 握手首帧使用。
func (s *ChatService) LatestSeq(ctx context.Context, roomID int64) (int64, error) {
	return s.d.Messages.LatestSeq(ctx, s.d.Pool, roomID)
}
