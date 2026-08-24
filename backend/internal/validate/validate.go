// Package validate 提供业务级输入校验。
//
// 对齐 KB [Robustness]：外部输入不能只依赖调用处的简单格式检查，必须校验
// 字段存在性、类型与边界值。WS 消息体同样走这里，不能因为「不是 HTTP」就放过。
package validate

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
)

var (
	reUsername = regexp.MustCompile(`^[a-zA-Z0-9_]{3,24}$`)
	rePhone    = regexp.MustCompile(`^1[3-9]\d{9}$`)
)

// Username 校验用户名：3–24 位字母数字下划线。
func Username(s string) error {
	if !reUsername.MatchString(s) {
		return apperr.ErrValidation.WithMessage("用户名需为 3-24 位字母、数字或下划线").WithDetail("field", "username")
	}
	return nil
}

// Phone 校验中国大陆手机号。允许留空（手机号为选填）。
func Phone(s string) error {
	if s == "" {
		return nil
	}
	if !rePhone.MatchString(s) {
		return apperr.ErrValidation.WithMessage("手机号格式不正确").WithDetail("field", "phone")
	}
	return nil
}

// Password 校验密码强度：8–72 字节（bcrypt 上限 72），且至少含字母与数字。
func Password(s string) error {
	if len(s) < 8 || len(s) > 72 {
		return apperr.ErrValidation.WithMessage("密码长度需为 8-72 个字符").WithDetail("field", "password")
	}
	var hasLetter, hasDigit bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	if !hasLetter || !hasDigit {
		return apperr.ErrValidation.WithMessage("密码需同时包含字母和数字").WithDetail("field", "password")
	}
	return nil
}

// TextRange 校验文本的可见字符数落在 [min, max]，并返回 TrimSpace 后的值。
func TextRange(field, s string, lo, hi int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < lo || n > hi {
		return "", apperr.ErrValidation.
			WithMessage(fieldLabel(field)+"长度需在 "+strconv.Itoa(lo)+"-"+strconv.Itoa(hi)+" 字之间").
			WithDetail("field", field)
	}
	return s, nil
}

// IntRange 校验整数落在 [min, max]。
func IntRange(field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return apperr.ErrValidation.
			WithMessage(fieldLabel(field)+"需在 "+strconv.Itoa(lo)+"-"+strconv.Itoa(hi)+" 之间").
			WithDetail("field", field)
	}
	return nil
}

// OneOf 校验取值在允许集合内。
func OneOf(field, v string, allowed ...string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return apperr.ErrValidation.
		WithMessage(fieldLabel(field)+"取值不合法").
		WithDetail("field", field).
		WithDetail("allowed", allowed)
}

var labels = map[string]string{
	"title":       "标题",
	"script_name": "剧本名",
	"venue_name":  "店铺名",
	"address":     "地址",
	"city":        "城市",
	"notes":       "备注",
	"nickname":    "昵称",
	"capacity":    "总人数",
	"min_viable":  "最低成行人数",
	"content":     "消息内容",
	"difficulty":  "难度",
	"room_type":   "局类型",
	"seat_gender": "角色席位",
}

func fieldLabel(f string) string {
	if l, ok := labels[f]; ok {
		return l
	}
	return f
}
