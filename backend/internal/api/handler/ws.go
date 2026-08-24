package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/middleware"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
	"github.com/alkaid/jubensha-carpool/backend/internal/ws"
)

// WSHandler 把 HTTP 请求交给 WebSocket 服务端。
type WSHandler struct {
	server *ws.Server
	chat   *service.ChatService
}

func NewWSHandler(server *ws.Server, chat *service.ChatService) *WSHandler {
	return &WSHandler{server: server, chat: chat}
}

// Room GET /ws/rooms/:id
//
// 鉴权是两道：
//  1. RequireAuth 中间件校验 JWT（WS 从 ?access_token= 取，因为浏览器的
//     WebSocket API 不支持自定义请求头）。
//  2. 这里再校验「该用户在该房间占着席位」。缺了这一道，任何登录用户
//     都能订阅任意房间的聊天内容（NFR-2 B-5）。
func (h *WSHandler) Room(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	userID := middleware.UserID(c)
	if err := h.chat.EnsureMember(c.Request.Context(), roomID, userID); err != nil {
		response.Fail(c, err)
		return
	}
	// 注意这里传的是原始 http.ResponseWriter（gin.ResponseWriter 实现了
	// http.Hijacker）。项目里没有任何中间件包装它，因此 Upgrade 不会
	// 踩 KB [Go][WS] 记录的 "does not implement http.Hijacker" 500。
	h.server.ServeRoom(c.Writer, c.Request, roomID, userID)
}

// Wall GET /ws/wall
//
// 墙订阅只推席位快照，不含任何聊天内容，因此无需房间成员校验。
func (h *WSHandler) Wall(c *gin.Context) {
	h.server.ServeWall(c.Writer, c.Request, middleware.UserID(c))
}
