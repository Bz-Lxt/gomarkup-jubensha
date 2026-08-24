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
type ChatService struct {
	d        *Deps
	messages MessageStore
	members  MemberStore
}

func NewChatService(d *Deps) *ChatService {
	return &ChatService{
		d:        d,
		messages: d.Messages,
		members:  d.Members,
	}
}

// EnsureMember 是聊天室的准入判据：只有占着席位的人才能读写房内消息。
//
// WS 握手和每一条 HTTP 聊天请求都要过这一关。仅靠「前端不显示入口」是不够的，
// 那不是权限控制（NFR-4 越权防护）。
func (s *ChatService) EnsureMember(ctx context.Context, roomID, userID int64) error {
	ok, err := s.members.IsActiveMember(ctx, s.d.Pool, roomID, userID)
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
			if existing, err := s.messages.GetByClientMsgID(ctx, q, roomID, userID, clientMsgID); err == nil {
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
		if err := s.messages.Insert(ctx, q, msg); err != nil {
			if repository.IsUniqueViolation(err) {
				// 并发重发撞上 uq_msg_client，回读既有那条即可。
				if existing, gErr := s.messages.GetByClientMsgID(ctx, q, roomID, userID, clientMsgID); gErr == nil {
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
//
// 降级路径必须用 ListLatest 而非 ListRange：
// ListRange(fromSeq, latest, limit) 的 SQL 是 WHERE seq > fromSeq ORDER BY seq ASC LIMIT n，
// 返回的是 (fromSeq, fromSeq+n] ——即区间内**最老**的 n 条。用户落后 477 条时拿到的
// 是 seq 41..240，而 to_seq/latest_seq 却标成 517，前端据此把游标推进到 517，
// 导致 241..517 永远无法通过正常补齐取回。ListLatest 取最近 n 条并反转为升序，
// 保证返回区间以 latest 收尾，与 to_seq/latest_seq 一致。
func (s *ChatService) Backfill(ctx context.Context, roomID, userID, lastSeenSeq int64) (*model.Backfill, error) {
	if err := s.EnsureMember(ctx, roomID, userID); err != nil {
		return nil, err
	}
	if lastSeenSeq < 0 {
		lastSeenSeq = 0
	}

	latest, err := s.messages.LatestSeq(ctx, s.d.Pool, roomID)
	if err != nil {
		return nil, err
	}
	if lastSeenSeq >= latest {
		return model.NewBackfill(nil, lastSeenSeq, latest, latest, 0, false), nil
	}

	gap := latest - lastSeenSeq
	maxN := int64(s.d.Cfg.BackfillMax)

	if gap <= maxN {
		msgs, err := s.messages.ListRange(ctx, s.d.Pool, roomID, lastSeenSeq, latest, s.d.Cfg.BackfillMax)
		if err != nil {
			return nil, err
		}
		return model.NewBackfill(msgs, lastSeenSeq, latest, latest, gap, false), nil
	}

	logger.C(ctx).Info("离线消息过多，触发全量降级",
		"room_id", roomID, "user_id", userID, "gap", gap, "cap", maxN)
	// 降级：取最近 BackfillMax 条，而非 (lastSeenSeq, lastSeenSeq+BackfillMax]。
	// from_seq/to_seq 如实反映实际返回的消息边界，让前端知道断层在哪里。
	msgs, err := s.messages.ListLatest(ctx, s.d.Pool, roomID, s.d.Cfg.BackfillMax)
	if err != nil {
		return nil, err
	}
	from, to := lastSeenSeq, latest
	if len(msgs) > 0 {
		from = msgs[0].Seq - 1
		to = msgs[len(msgs)-1].Seq
	}
	return model.NewBackfill(msgs, from, to, latest, gap, true), nil
}

// Cursor 返回用户在该房间的已读水位。
func (s *ChatService) Cursor(ctx context.Context, roomID, userID int64) (int64, error) {
	return s.messages.GetCursor(ctx, s.d.Pool, roomID, userID)
}

// Ack 推进已读水位。游标只前进不后退（由 SQL 的 GREATEST 保证）。
func (s *ChatService) Ack(ctx context.Context, roomID, userID, seq int64) error {
	if seq < 0 {
		return apperr.ErrWSPayloadInvalid.WithDetail("field", "seq")
	}
	return s.messages.UpsertCursor(ctx, s.d.Pool, roomID, userID, seq)
}

// Unread 返回该用户所有在车房间的未读数。
func (s *ChatService) Unread(ctx context.Context, userID int64) ([]repository.UnreadCount, error) {
	return s.messages.UnreadByUser(ctx, s.d.Pool, userID)
}

// LatestSeq 返回房间的消息水位，供 WS 握手首帧使用。
func (s *ChatService) LatestSeq(ctx context.Context, roomID int64) (int64, error) {
	return s.messages.LatestSeq(ctx, s.d.Pool, roomID)
}
