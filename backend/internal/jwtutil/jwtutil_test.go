package jwtutil

import (
	"testing"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
)

const testSecret = "unit-test-secret-at-least-32-bytes-long"

func TestIssueAndParse(t *testing.T) {
	m := NewManager(testSecret, time.Hour, 24*time.Hour)

	pair, err := m.Issue(42, "alice")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("两个 token 都不应为空")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("access 与 refresh 不应相同")
	}

	claims, err := m.Parse(pair.AccessToken, KindAccess)
	if err != nil {
		t.Fatalf("解析 access token 失败: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "alice" {
		t.Fatalf("claims 内容错误: %+v", claims)
	}
}

// TestKindConfusionRejected 是本文件最重要的断言：
// refresh token 绝不能当 access token 用。
//
// 若不校验 kind，攻击者可以拿长效的 refresh token 直接调用业务接口，
// 令 access token 的短过期时间形同虚设。
func TestKindConfusionRejected(t *testing.T) {
	m := NewManager(testSecret, time.Hour, 24*time.Hour)
	pair, err := m.Issue(1, "bob")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	if _, err := m.Parse(pair.RefreshToken, KindAccess); err == nil {
		t.Fatal("refresh token 不应能作为 access token 通过校验")
	}
	if _, err := m.Parse(pair.AccessToken, KindRefresh); err == nil {
		t.Fatal("access token 不应能作为 refresh token 通过校验")
	}
}

// TestWrongSecretRejected 断言换密钥签的 token 一律不认。
func TestWrongSecretRejected(t *testing.T) {
	issuer := NewManager(testSecret, time.Hour, time.Hour)
	pair, err := issuer.Issue(1, "bob")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	verifier := NewManager("a-completely-different-secret-32bytes!", time.Hour, time.Hour)
	if _, err := verifier.Parse(pair.AccessToken, KindAccess); err == nil {
		t.Fatal("换密钥后应校验失败")
	}
}

// TestExpiredTokenRejectedWithSpecificCode 断言过期与无效被区分开。
// 前端要靠 TOKEN_EXPIRED 决定「静默刷新」，靠 TOKEN_INVALID 决定「踢去登录页」。
func TestExpiredTokenRejectedWithSpecificCode(t *testing.T) {
	// 负 TTL：签发即过期。
	m := NewManager(testSecret, -time.Minute, time.Hour)
	pair, err := m.Issue(1, "bob")
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	_, err = m.Parse(pair.AccessToken, KindAccess)
	if err == nil {
		t.Fatal("过期 token 应被拒绝")
	}
	if !apperr.Is(err, apperr.CodeTokenExpired) {
		t.Fatalf("错误码应为 TOKEN_EXPIRED，实际 %v", err)
	}
}

// TestMalformedTokenRejected 断言各种畸形输入都被安全拒绝而非 panic。
func TestMalformedTokenRejected(t *testing.T) {
	m := NewManager(testSecret, time.Hour, time.Hour)
	for _, tok := range []string{
		"",
		"not-a-jwt",
		"a.b.c",
		"eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiIxIn0.", // alg=none
	} {
		if _, err := m.Parse(tok, KindAccess); err == nil {
			t.Fatalf("畸形 token %q 应被拒绝", tok)
		}
	}
}
