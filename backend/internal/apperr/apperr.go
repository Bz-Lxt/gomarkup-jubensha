// Package apperr 定义全项目统一的错误码体系。
//
// 设计要点：
//   - 每个错误同时携带「机器可读 code」「HTTP 状态」「面向用户的中文文案」。
//     前端按 code 分支，用户看 message，两者解耦。
//   - 错误码表在 docs/API.md 中完整列出，与本文件一一对应。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是机器可读的错误标识。
type Code string

const (
	// 通用
	CodeInternal     Code = "INTERNAL_ERROR"
	CodeBadRequest   Code = "BAD_REQUEST"
	CodeValidation   Code = "VALIDATION_FAILED"
	CodeNotFound     Code = "NOT_FOUND"
	CodeUnauthorized Code = "UNAUTHORIZED"
	CodeForbidden    Code = "FORBIDDEN"
	CodeRateLimited  Code = "RATE_LIMITED"
	CodeConflict     Code = "CONFLICT"

	// 认证
	CodeUsernameTaken    Code = "USERNAME_TAKEN"
	CodePhoneTaken       Code = "PHONE_TAKEN"
	CodeBadCredentials   Code = "BAD_CREDENTIALS"
	CodeTokenExpired     Code = "TOKEN_EXPIRED"
	CodeTokenInvalid     Code = "TOKEN_INVALID"
	CodeRefreshRejected  Code = "REFRESH_REJECTED"
	CodeTooManyUserTags  Code = "TOO_MANY_USER_TAGS"
	CodeUnknownPlayerTag Code = "UNKNOWN_PLAYER_TAG"

	// 房间 / 拼车
	CodeRoomNotFound      Code = "ROOM_NOT_FOUND"
	CodeRoomNotRecruiting Code = "ROOM_NOT_RECRUITING"
	CodeRoomClosed        Code = "ROOM_CLOSED"
	CodeSeatPlanInvalid   Code = "SEAT_PLAN_INVALID"
	CodeNotOwner          Code = "NOT_ROOM_OWNER"
	CodeOwnerCannotLeave  Code = "OWNER_CANNOT_LEAVE"
	CodeStartAtInPast     Code = "START_AT_IN_PAST"

	// 抢位（核心）
	CodeSlotFull        Code = "SLOT_FULL"
	CodeSeatGenderFull  Code = "SEAT_GENDER_FULL"
	CodeAlreadyOnBoard  Code = "ALREADY_ON_BOARD"
	CodeNotOnBoard      Code = "NOT_ON_BOARD"
	CodeSlotLockBusy    Code = "SLOT_LOCK_BUSY"
	CodeHoldExpired     Code = "HOLD_EXPIRED"
	CodeIllegalMemberTx Code = "ILLEGAL_MEMBER_TRANSITION"
	CodeIllegalRoomTx   Code = "ILLEGAL_ROOM_TRANSITION"

	// 聊天 / WS
	CodeNotRoomMember    Code = "NOT_ROOM_MEMBER"
	CodeMessageTooLong   Code = "MESSAGE_TOO_LONG"
	CodeMessageEmpty     Code = "MESSAGE_EMPTY"
	CodeUnknownWSType    Code = "UNKNOWN_WS_TYPE"
	CodeWSPayloadInvalid Code = "WS_PAYLOAD_INVALID"
)

// Error 是本项目的领域错误。
type Error struct {
	Code    Code
	Status  int
	Message string
	Details map[string]any
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause 附加底层原因（仅进日志，不返回给用户）。
func (e *Error) WithCause(err error) *Error {
	c := *e
	c.cause = err
	return &c
}

// WithDetail 附加结构化细节（会返回给前端，用于精确提示）。
func (e *Error) WithDetail(k string, v any) *Error {
	c := *e
	c.Details = map[string]any{}
	for key, val := range e.Details {
		c.Details[key] = val
	}
	c.Details[k] = v
	return &c
}

// WithMessage 覆盖面向用户的文案。
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

func def(code Code, status int, msg string) *Error {
	return &Error{Code: code, Status: status, Message: msg}
}

// 预定义错误。业务代码直接引用，禁止散落裸 errors.New。
var (
	ErrInternal     = def(CodeInternal, http.StatusInternalServerError, "服务器打了个盹，请稍后重试")
	ErrBadRequest   = def(CodeBadRequest, http.StatusBadRequest, "请求格式不正确")
	ErrValidation   = def(CodeValidation, http.StatusUnprocessableEntity, "提交的内容没通过校验")
	ErrNotFound     = def(CodeNotFound, http.StatusNotFound, "找不到这个资源")
	ErrUnauthorized = def(CodeUnauthorized, http.StatusUnauthorized, "请先登录")
	ErrForbidden    = def(CodeForbidden, http.StatusForbidden, "你没有权限做这个操作")
	ErrRateLimited  = def(CodeRateLimited, http.StatusTooManyRequests, "操作太频繁，歇一口气再试")
	ErrConflict     = def(CodeConflict, http.StatusConflict, "状态已变化，请刷新后重试")

	ErrUsernameTaken    = def(CodeUsernameTaken, http.StatusConflict, "这个用户名已经被占了")
	ErrPhoneTaken       = def(CodePhoneTaken, http.StatusConflict, "这个手机号已经注册过了")
	ErrBadCredentials   = def(CodeBadCredentials, http.StatusUnauthorized, "用户名或密码不对")
	ErrTokenExpired     = def(CodeTokenExpired, http.StatusUnauthorized, "登录已过期，请重新登录")
	ErrTokenInvalid     = def(CodeTokenInvalid, http.StatusUnauthorized, "登录凭证无效")
	ErrRefreshRejected  = def(CodeRefreshRejected, http.StatusUnauthorized, "刷新凭证已失效，请重新登录")
	ErrTooManyUserTags  = def(CodeTooManyUserTags, http.StatusUnprocessableEntity, "玩家标签最多选 3 个")
	ErrUnknownPlayerTag = def(CodeUnknownPlayerTag, http.StatusUnprocessableEntity, "不认识这个玩家标签")

	ErrRoomNotFound      = def(CodeRoomNotFound, http.StatusNotFound, "这个车不存在或已被删除")
	ErrRoomNotRecruiting = def(CodeRoomNotRecruiting, http.StatusConflict, "这个车已经不在招募中了")
	ErrRoomClosed        = def(CodeRoomClosed, http.StatusConflict, "这个车已经收工了")
	ErrSeatPlanInvalid   = def(CodeSeatPlanInvalid, http.StatusUnprocessableEntity, "席位配置不合法：男席+女席+不限席必须等于总人数")
	ErrNotOwner          = def(CodeNotOwner, http.StatusForbidden, "只有车主能做这个操作")
	ErrOwnerCannotLeave  = def(CodeOwnerCannotLeave, http.StatusConflict, "车主不能退车，如需解散请直接取消整车")
	ErrStartAtInPast     = def(CodeStartAtInPast, http.StatusUnprocessableEntity, "开局时间必须晚于当前时间")

	ErrSlotFull        = def(CodeSlotFull, http.StatusConflict, "车已经满了，晚了一步")
	ErrSeatGenderFull  = def(CodeSeatGenderFull, http.StatusConflict, "这一类角色席位已经满了")
	ErrAlreadyOnBoard  = def(CodeAlreadyOnBoard, http.StatusConflict, "你已经在这辆车上了")
	ErrNotOnBoard      = def(CodeNotOnBoard, http.StatusConflict, "你不在这辆车上")
	ErrSlotLockBusy    = def(CodeSlotLockBusy, http.StatusTooManyRequests, "抢位的人太多了，再点一次")
	ErrHoldExpired     = def(CodeHoldExpired, http.StatusConflict, "占位已超时释放，需要重新上车")
	ErrIllegalMemberTx = def(CodeIllegalMemberTx, http.StatusConflict, "成员状态无法完成这个流转")
	ErrIllegalRoomTx   = def(CodeIllegalRoomTx, http.StatusConflict, "房间状态无法完成这个流转")

	ErrNotRoomMember    = def(CodeNotRoomMember, http.StatusForbidden, "只有上车的人才能进这个房间的聊天室")
	ErrMessageTooLong   = def(CodeMessageTooLong, http.StatusUnprocessableEntity, "消息太长了，最多 500 字")
	ErrMessageEmpty     = def(CodeMessageEmpty, http.StatusUnprocessableEntity, "消息不能为空")
	ErrUnknownWSType    = def(CodeUnknownWSType, http.StatusBadRequest, "不认识这个消息类型")
	ErrWSPayloadInvalid = def(CodeWSPayloadInvalid, http.StatusBadRequest, "消息体格式不正确")
)

// From 把任意 error 归一化为 *Error。非领域错误统一收敛为 ErrInternal，
// 避免把数据库细节泄漏给客户端。
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return ErrInternal.WithCause(err)
}

// Is 判断 err 是否为指定 code。
func Is(err error, code Code) bool {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code == code
	}
	return false
}
