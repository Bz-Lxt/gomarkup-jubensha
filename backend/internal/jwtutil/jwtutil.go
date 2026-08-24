// Package jwtutil 封装 Access / Refresh 双令牌的签发与校验。
package jwtutil

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// TokenKind 区分令牌用途，防止 refresh token 被当 access token 使用。
type TokenKind string

const (
	KindAccess  TokenKind = "access"
	KindRefresh TokenKind = "refresh"
)

// Claims 是本项目的自定义声明。
type Claims struct {
	UserID   int64     `json:"uid"`
	Username string    `json:"unm"`
	Kind     TokenKind `json:"knd"`
	jwt.RegisteredClaims
}

// Manager 负责签发与解析令牌。
type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewManager 构造签发器。
func NewManager(secret string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

// TokenPair 是一次登录返回的双令牌。
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Issue 签发一对令牌。
func (m *Manager) Issue(userID int64, username string) (*TokenPair, error) {
	at, err := m.sign(userID, username, KindAccess, m.accessTTL)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}
	rt, err := m.sign(userID, username, KindRefresh, m.refreshTTL)
	if err != nil {
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}
	return &TokenPair{AccessToken: at, RefreshToken: rt, ExpiresIn: int64(m.accessTTL.Seconds())}, nil
}

func (m *Manager) sign(userID int64, username string, kind TokenKind, ttl time.Duration) (string, error) {
	now := timeutil.Now()
	c := Claims{
		UserID:   userID,
		Username: username,
		Kind:     kind,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "jubensha-carpool",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.secret)
}

// Parse 解析并校验令牌，同时强校验 kind 匹配。
func (m *Manager) Parse(token string, want TokenKind) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithLeeway(10*time.Second))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, apperr.ErrTokenExpired.WithCause(err)
		}
		return nil, apperr.ErrTokenInvalid.WithCause(err)
	}
	if c.Kind != want {
		return nil, apperr.ErrTokenInvalid.WithCause(fmt.Errorf("token kind %q, want %q", c.Kind, want))
	}
	if c.UserID <= 0 {
		return nil, apperr.ErrTokenInvalid.WithCause(errors.New("empty subject"))
	}
	return &c, nil
}
