package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/middleware"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

// RoomHandler 处理房间的读取与创建。
type RoomHandler struct {
	rooms *service.RoomService
}

func NewRoomHandler(rooms *service.RoomService) *RoomHandler {
	return &RoomHandler{rooms: rooms}
}

// Wall GET /api/rooms
func (h *RoomHandler) Wall(c *gin.Context) {
	f := repository.WallFilter{
		City:         strings.TrimSpace(c.Query("city")),
		RoomType:     strings.ToUpper(strings.TrimSpace(c.Query("room_type"))),
		Theme:        strings.TrimSpace(c.Query("theme")),
		Keyword:      strings.TrimSpace(c.Query("q")),
		OnlyJoinable: c.Query("joinable") == "1" || c.Query("joinable") == "true",
		Limit:        queryInt(c, "limit", 24),
		Offset:       queryInt(c, "offset", 0),
	}
	if f.RoomType != "" && !model.RoomType(f.RoomType).Valid() {
		response.Fail(c, apperr.ErrValidation.
			WithMessage("局类型筛选值不合法").WithDetail("field", "room_type"))
		return
	}
	if st := strings.TrimSpace(c.Query("status")); st != "" {
		for _, s := range strings.Split(st, ",") {
			s = strings.ToUpper(strings.TrimSpace(s))
			if s != "" {
				f.Statuses = append(f.Statuses, s)
			}
		}
	}

	res, err := h.rooms.Wall(c.Request.Context(), f, middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// Detail GET /api/rooms/:id
func (h *RoomHandler) Detail(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	card, err := h.rooms.Detail(c.Request.Context(), id, middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, card)
}

// Create POST /api/rooms
func (h *RoomHandler) Create(c *gin.Context) {
	var in service.CreateRoomInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	card, err := h.rooms.Create(c.Request.Context(), middleware.UserID(c), in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, card)
}

// Mine GET /api/rooms/mine
func (h *RoomHandler) Mine(c *gin.Context) {
	res, err := h.rooms.MyRooms(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// Cities GET /api/meta/cities
func (h *RoomHandler) Cities(c *gin.Context) {
	cities, err := h.rooms.Cities(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, cities)
}

// History GET /api/rooms/:id/history
//
// 暴露状态机审计日志。这不是调试残留：炸车/踢人产生纠纷时，
// 成员需要能自己看到「系统在什么时候判定了什么」。
func (h *RoomHandler) History(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	logs, err := h.rooms.History(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, logs)
}

// Meta GET /api/meta/enums
//
// 一次性把前端需要的枚举（席位类型、局类型、状态、难度）全给出去，
// 前端不再硬编码中文文案，杜绝两端文案不一致。
func (h *RoomHandler) Meta(c *gin.Context) {
	type option struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	seats := []option{}
	for _, g := range model.AllSeatGenders() {
		seats = append(seats, option{Code: string(g), Label: g.Label()})
	}
	types := []option{
		{Code: string(model.TypeScript), Label: model.TypeScript.Label()},
		{Code: string(model.TypeEscape), Label: model.TypeEscape.Label()},
	}
	statuses := []option{}
	for _, s := range []model.RoomStatus{
		model.RoomRecruiting, model.RoomLocked, model.RoomConfirmed,
		model.RoomInProgress, model.RoomCompleted, model.RoomExpired, model.RoomCancelled,
	} {
		statuses = append(statuses, option{Code: string(s), Label: s.Label()})
	}
	themes := []option{
		{Code: "硬核推理", Label: "硬核推理"},
		{Code: "情感沉浸", Label: "情感沉浸"},
		{Code: "恐怖惊悚", Label: "恐怖惊悚"},
		{Code: "欢乐撕逼", Label: "欢乐撕逼"},
		{Code: "机制阵营", Label: "机制阵营"},
		{Code: "解谜逃脱", Label: "解谜逃脱"},
	}
	response.OK(c, gin.H{
		"seat_genders": seats,
		"room_types":   types,
		"statuses":     statuses,
		"themes":       themes,
		"max_tags":     model.MaxUserTags,
	})
}

// ---------------------------------------------------------------- 工具

func pathID(c *gin.Context) (int64, error) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.ErrBadRequest.
			WithMessage("路径中的 ID 不是合法的正整数").WithDetail("id", raw)
	}
	return id, nil
}

func queryInt(c *gin.Context, key string, def int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}
