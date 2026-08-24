package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/middleware"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

// SlotHandler 处理上车 / 退车 / 车主操作。
type SlotHandler struct {
	slot  *service.SlotService
	rooms *service.RoomService
}

func NewSlotHandler(slot *service.SlotService, rooms *service.RoomService) *SlotHandler {
	return &SlotHandler{slot: slot, rooms: rooms}
}

// Join POST /api/rooms/:id/join
//
// 这是全项目并发压力最大的端点。参数只有席位类型，其余判断全部在
// SlotService 的三层锁临界区内完成。
func (h *SlotHandler) Join(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var in struct {
		SeatGender model.SeatGender `json:"seat_gender"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	res, err := h.slot.Join(c.Request.Context(), roomID, middleware.UserID(c), in.SeatGender)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// Leave POST /api/rooms/:id/leave
func (h *SlotHandler) Leave(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	snap, err := h.slot.Leave(c.Request.Context(), roomID, middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, snap)
}

// Kick POST /api/rooms/:id/kick
func (h *SlotHandler) Kick(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var in struct {
		UserID int64  `json:"user_id"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	if in.UserID <= 0 {
		response.Fail(c, apperr.ErrValidation.
			WithMessage("要移出的成员 ID 不合法").WithDetail("field", "user_id"))
		return
	}
	snap, err := h.slot.Kick(c.Request.Context(), roomID, middleware.UserID(c), in.UserID, in.Reason)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, snap)
}

// Confirm POST /api/rooms/:id/confirm
func (h *SlotHandler) Confirm(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var in struct {
		UserID int64 `json:"user_id"`
	}
	// 请求体可省略，缺省表示确认自己的占位。
	_ = c.ShouldBindJSON(&in)
	target := in.UserID
	if target <= 0 {
		target = middleware.UserID(c)
	}
	snap, err := h.slot.Confirm(c.Request.Context(), roomID, middleware.UserID(c), target)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, snap)
}

// Lock POST /api/rooms/:id/lock
func (h *SlotHandler) Lock(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	snap, err := h.slot.OwnerLock(c.Request.Context(), roomID, middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, snap)
}

// Cancel POST /api/rooms/:id/cancel
func (h *SlotHandler) Cancel(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&in)
	snap, err := h.slot.OwnerCancel(c.Request.Context(), roomID, middleware.UserID(c), in.Reason)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, snap)
}

// Audit GET /api/rooms/:id/audit
//
// 席位账目对账端点。把 rooms 的聚合计数与 room_members 的实际行数摆在一起，
// drift 必须恒为 0。QA 的 NFR-1 A-5 直接调用它做断言，无需 psql。
func (h *SlotHandler) Audit(c *gin.Context) {
	roomID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	agg, actual, drift, err := h.slot.AuditOccupancy(c.Request.Context(), roomID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{
		"room_id":          roomID,
		"aggregate_counts": agg,
		"actual_members":   actual,
		"drift":            drift,
		"consistent":       drift == 0,
	})
}
