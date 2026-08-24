package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/middleware"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

// ChatHandler 提供聊天的 HTTP 通路。
//
// 为什么 WS 之外还要 HTTP：首屏加载走 HTTP 拿到历史消息，比等 WS 握手完成
// 再拉一轮更快；同时给测试脚本一条不依赖 WebSocket 的验证路径。
type ChatHandler struct {
	chat *service.ChatService
}

func NewChatHandler(chat *service.ChatService) *ChatHandler {
	return &ChatHandler{chat: chat}
}

// History GET /api/rooms/:id/messages?since=<seq>
//
// since 语义与 WS 的 chat.pull 完全一致：返回 (since, latest] 区间，
// 落后超过阈值时降级为最近 N 条并置 truncated。
func (h *ChatHandler) History(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	since := int64(queryInt(c, "since", 0))
	bf, err := h.chat.Backfill(c.Request.Context(), roomID, middleware.UserID(c), since)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, bf)
}

// Send POST /api/rooms/:id/messages
func (h *ChatHandler) Send(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var in model.ChatSendData
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	msg, err := h.chat.Send(c.Request.Context(), roomID, middleware.UserID(c), in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, msg)
}

// Ack POST /api/rooms/:id/read
func (h *ChatHandler) Ack(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var in model.ChatAckData
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	if err := h.chat.Ack(c.Request.Context(), roomID, middleware.UserID(c), in.Seq); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"room_id": roomID, "last_seen_seq": in.Seq})
}

// Unread GET /api/rooms/unread
func (h *ChatHandler) Unread(c *gin.Context) {
	counts, err := h.chat.Unread(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, counts)
}
