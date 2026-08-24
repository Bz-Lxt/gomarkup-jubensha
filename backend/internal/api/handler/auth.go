// Package handler 只做三件事：解析入参、调用 service、渲染响应。
//
// 铁律：本包不写 SQL，不开事务，不做业务判断。任何 `if` 只应该是
// 「参数解析失败」这类协议层判断。
package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/api/middleware"
	"github.com/alkaid/jubensha-carpool/backend/internal/api/response"
	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

// AuthHandler 处理注册、登录、刷新。
type AuthHandler struct {
	auth  *service.AuthService
	users *service.UserService
}

func NewAuthHandler(auth *service.AuthService, users *service.UserService) *AuthHandler {
	return &AuthHandler{auth: auth, users: users}
}

// Register POST /api/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var in service.RegisterInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	res, err := h.auth.Register(c.Request.Context(), in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, res)
}

// Login POST /api/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	res, err := h.auth.Login(c.Request.Context(), in.Username, in.Password)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// Refresh POST /api/auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	res, err := h.auth.Refresh(c.Request.Context(), in.RefreshToken)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// Me GET /api/users/me
func (h *AuthHandler) Me(c *gin.Context) {
	u, err := h.users.Me(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, u)
}

// UpdateMe PATCH /api/users/me
func (h *AuthHandler) UpdateMe(c *gin.Context) {
	var in service.UpdateProfileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, apperr.ErrBadRequest.WithCause(err))
		return
	}
	u, err := h.users.UpdateProfile(c.Request.Context(), middleware.UserID(c), in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, u)
}

// TagCatalog GET /api/meta/tags
//
// 标签目录由后端供给，前端不再维护一份枚举，避免文案两边分叉。
func (h *AuthHandler) TagCatalog(c *gin.Context) {
	response.OK(c, h.users.TagCatalog())
}
