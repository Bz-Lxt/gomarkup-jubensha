package service

import (
	"context"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/jwtutil"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/repository"
	"github.com/alkaid/jubensha-carpool/backend/internal/validate"
)

// bcryptCost 取 12。默认值 10 在现代硬件上偏快，12 大约 100ms 量级，
// 既抗离线爆破又不至于让登录接口明显变慢。
const bcryptCost = 12

// AuthService 处理注册、登录、令牌刷新。
type AuthService struct {
	d   *Deps
	jwt *jwtutil.Manager
}

func NewAuthService(d *Deps, jm *jwtutil.Manager) *AuthService {
	return &AuthService{d: d, jwt: jm}
}

// RegisterInput 是注册入参。
type RegisterInput struct {
	Username string            `json:"username"`
	Password string            `json:"password"`
	Phone    string            `json:"phone"`
	Nickname string            `json:"nickname"`
	City     string            `json:"city"`
	Tags     []model.PlayerTag `json:"tags"`
}

// AuthResult 是认证成功后的返回。
type AuthResult struct {
	User   *model.User        `json:"user"`
	Tokens *jwtutil.TokenPair `json:"tokens"`
}

// Register 创建账号。
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (*AuthResult, error) {
	in.Username = strings.TrimSpace(in.Username)
	in.Phone = strings.TrimSpace(in.Phone)

	if err := validate.Username(in.Username); err != nil {
		return nil, err
	}
	if err := validate.Password(in.Password); err != nil {
		return nil, err
	}
	if err := validate.Phone(in.Phone); err != nil {
		return nil, err
	}
	nickname, err := resolveNickname(in.Nickname, in.Username)
	if err != nil {
		return nil, err
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	user := &model.User{
		Username:     in.Username,
		Phone:        in.Phone,
		PasswordHash: string(hash),
		Nickname:     nickname,
		City:         strings.TrimSpace(in.City),
		Avatar:       defaultAvatar(in.Username),
		Reputation:   100,
	}

	err = repository.InTx(ctx, s.d.Pool, func(q repository.Querier) error {
		if err := s.d.Users.Create(ctx, q, user); err != nil {
			if repository.IsUniqueViolation(err) {
				// 唯一索引同时覆盖 username 与 phone，靠错误串区分是哪一个。
				if in.Phone != "" && strings.Contains(err.Error(), "uq_users_phone") {
					return apperr.ErrPhoneTaken
				}
				return apperr.ErrUsernameTaken
			}
			return err
		}
		return s.d.Users.ReplaceTags(ctx, q, user.ID, tags)
	})
	if err != nil {
		return nil, err
	}
	user.Tags = tags

	tokens, err := s.jwt.Issue(user.ID, user.Username)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	return &AuthResult{User: user, Tokens: tokens}, nil
}

// Login 校验口令并签发令牌。
func (s *AuthService) Login(ctx context.Context, username, password string) (*AuthResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, apperr.ErrBadCredentials
	}

	user, err := s.d.Users.GetByUsername(ctx, s.d.Pool, username)
	if err != nil {
		if repository.IsNoRows(err) {
			// 刻意对「用户不存在」和「密码错误」返回同一个错误：
			// 否则接口就成了用户名枚举器。
			return nil, apperr.ErrBadCredentials
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, apperr.ErrBadCredentials
	}

	tags, err := s.d.Users.LoadTags(ctx, s.d.Pool, user.ID)
	if err != nil {
		return nil, err
	}
	user.Tags = tags

	tokens, err := s.jwt.Issue(user.ID, user.Username)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	return &AuthResult{User: user, Tokens: tokens}, nil
}

// Refresh 用 Refresh Token 换一对新令牌。
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*AuthResult, error) {
	claims, err := s.jwt.Parse(refreshToken, jwtutil.KindRefresh)
	if err != nil {
		return nil, apperr.ErrRefreshRejected.WithCause(err)
	}
	user, err := s.d.Users.LoadUserWithTags(ctx, s.d.Pool, claims.UserID)
	if err != nil {
		if repository.IsNoRows(err) {
			return nil, apperr.ErrRefreshRejected.WithMessage("账号不存在，请重新登录")
		}
		return nil, err
	}
	tokens, err := s.jwt.Issue(user.ID, user.Username)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	return &AuthResult{User: user, Tokens: tokens}, nil
}

// ParseAccess 供中间件使用。
func (s *AuthService) ParseAccess(token string) (*jwtutil.Claims, error) {
	return s.jwt.Parse(token, jwtutil.KindAccess)
}

func resolveNickname(nickname, fallback string) (string, error) {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		return fallback, nil
	}
	return validate.TextRange("nickname", nickname, 1, 16)
}

func normalizeTags(tags []model.PlayerTag) ([]model.PlayerTag, error) {
	out := make([]model.PlayerTag, 0, len(tags))
	seen := map[model.PlayerTag]bool{}
	for _, t := range tags {
		if !t.Valid() {
			return nil, apperr.ErrUnknownPlayerTag.WithDetail("tag", string(t))
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > model.MaxUserTags {
		return nil, apperr.ErrTooManyUserTags.WithDetail("max", model.MaxUserTags)
	}
	return out, nil
}

// defaultAvatar 生成本地渲染的确定性头像标识，形如 "local:7"。
//
// 刻意不用 DiceBear / Gravatar 一类的外部头像服务：那会让项目在离线或内网
// 环境下头像全部裂图，也会和「本项目零外部 API 依赖」的声明自相矛盾。
// 前端把这个序号映射到内置的渐变色板 + 昵称首字，纯 CSS 渲染。
func defaultAvatar(seed string) string {
	var sum uint32
	for _, r := range seed {
		sum = sum*31 + uint32(r)
	}
	return "local:" + strconv.FormatUint(uint64(sum%avatarPaletteSize), 10)
}

// avatarPaletteSize 必须与前端 lib/avatar.ts 中的色板长度一致。
const avatarPaletteSize = 8
