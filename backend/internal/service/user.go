package service

import (
	"context"
	"strings"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/validate"
)

// UserService 处理资料与标签。
type UserService struct{ d *Deps }

func NewUserService(d *Deps) *UserService { return &UserService{d: d} }

// Me 返回当前用户（含标签）。
func (s *UserService) Me(ctx context.Context, userID int64) (*model.User, error) {
	u, err := s.d.Users.LoadUserWithTags(ctx, s.d.Pool, userID)
	if err != nil {
		if repository.IsNoRows(err) {
			return nil, apperr.ErrNotFound.WithMessage("账号不存在")
		}
		return nil, err
	}
	return u, nil
}

// UpdateProfileInput 是资料更新入参。指针字段为 nil 表示不改这一项。
type UpdateProfileInput struct {
	Nickname *string            `json:"nickname"`
	City     *string            `json:"city"`
	Bio      *string            `json:"bio"`
	Phone    *string            `json:"phone"`
	Tags     *[]model.PlayerTag `json:"tags"`
}

// UpdateProfile 局部更新资料。
func (s *UserService) UpdateProfile(ctx context.Context, userID int64, in UpdateProfileInput) (*model.User, error) {
	user, err := s.d.Users.GetByID(ctx, s.d.Pool, userID)
	if err != nil {
		if repository.IsNoRows(err) {
			return nil, apperr.ErrNotFound.WithMessage("账号不存在")
		}
		return nil, err
	}

	if in.Nickname != nil {
		v, err := validate.TextRange("nickname", *in.Nickname, 1, 16)
		if err != nil {
			return nil, err
		}
		user.Nickname = v
	}
	if in.City != nil {
		user.City = strings.TrimSpace(*in.City)
	}
	if in.Bio != nil {
		v, err := validate.TextRange("bio", *in.Bio, 0, 120)
		if err != nil {
			return nil, err
		}
		user.Bio = v
	}
	if in.Phone != nil {
		p := strings.TrimSpace(*in.Phone)
		if err := validate.Phone(p); err != nil {
			return nil, err
		}
		user.Phone = p
	}

	var tags []model.PlayerTag
	if in.Tags != nil {
		tags, err = normalizeTags(*in.Tags)
		if err != nil {
			return nil, err
		}
	}

	err = repository.InTx(ctx, s.d.Pool, func(q repository.Querier) error {
		if err := s.d.Users.UpdateProfile(ctx, q, user); err != nil {
			if repository.IsUniqueViolation(err) {
				return apperr.ErrPhoneTaken
			}
			return err
		}
		if in.Tags != nil {
			return s.d.Users.ReplaceTags(ctx, q, userID, tags)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Me(ctx, userID)
}

// TagOption 是标签的对外描述，供前端渲染选择器与一键发送按钮。
type TagOption struct {
	Code   model.PlayerTag `json:"code"`
	Label  string          `json:"label"`
	Phrase string          `json:"phrase"`
}

// TagCatalog 返回全部可选标签。这是「一键发送标签」按钮的数据来源，
// 由后端统一供给，避免前后端各维护一份枚举导致文案分叉。
func (s *UserService) TagCatalog() []TagOption {
	all := model.AllPlayerTags()
	out := make([]TagOption, 0, len(all))
	for _, t := range all {
		out = append(out, TagOption{Code: t, Label: t.Label(), Phrase: t.Phrase()})
	}
	return out
}
