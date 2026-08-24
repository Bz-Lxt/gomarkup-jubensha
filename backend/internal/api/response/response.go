// Package response 定义 HTTP 统一响应信封。
//
// 前端只需要一套解析逻辑：先看 ok，再取 data 或 error.code。
// 契约冻结在 docs/.meta/api_contracts.md，任何新增端点都必须遵守。
package response

import (
	"github.com/gin-gonic/gin"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
)

// Envelope 是统一响应结构。
type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data"`
	Error *Error `json:"error"`
}

// Error 是错误详情。
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// OK 返回成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(200, Envelope{OK: true, Data: data})
}

// Created 返回 201。
func Created(c *gin.Context, data any) {
	c.JSON(201, Envelope{OK: true, Data: data})
}

// Fail 把领域错误转成响应。
//
// 5xx 才记 ERROR 级日志并隐藏内部细节；4xx 是正常的业务拒绝（车满了、
// 密码错了），只记 DEBUG，否则日志会被用户的正常误操作淹没。
func Fail(c *gin.Context, err error) {
	e := apperr.From(err)
	if e.Status >= 500 {
		logger.C(c.Request.Context()).Error("请求处理失败",
			"path", c.FullPath(), "method", c.Request.Method,
			"code", string(e.Code), "error", e.Error())
	} else {
		logger.C(c.Request.Context()).Debug("请求被拒绝",
			"path", c.FullPath(), "code", string(e.Code), "message", e.Message)
	}

	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	c.AbortWithStatusJSON(e.Status, Envelope{
		OK: false,
		Error: &Error{
			Code:    string(e.Code),
			Message: e.Message,
			Details: details,
		},
	})
}
