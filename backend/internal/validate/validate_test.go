package validate

import (
	"strings"
	"testing"

	"github.com/alkaid/jubensha-carpool/backend/internal/apperr"
)

func TestUsername(t *testing.T) {
	ok := []string{"abc", "user_01", "A1_", strings.Repeat("a", 24)}
	for _, s := range ok {
		if err := Username(s); err != nil {
			t.Fatalf("Username(%q) 应通过，实际 %v", s, err)
		}
	}
	bad := []string{"", "ab", strings.Repeat("a", 25), "有中文", "with space", "dash-not-ok", "at@sign"}
	for _, s := range bad {
		if err := Username(s); err == nil {
			t.Fatalf("Username(%q) 应被拒绝", s)
		} else if !apperr.Is(err, apperr.CodeValidation) {
			t.Fatalf("Username(%q) 错误码应为 VALIDATION_FAILED，实际 %v", s, err)
		}
	}
}

func TestPhone(t *testing.T) {
	if err := Phone(""); err != nil {
		t.Fatalf("手机号选填，空值应通过，实际 %v", err)
	}
	for _, s := range []string{"13800138000", "19912345678"} {
		if err := Phone(s); err != nil {
			t.Fatalf("Phone(%q) 应通过，实际 %v", s, err)
		}
	}
	for _, s := range []string{"12800138000", "1380013800", "138001380000", "abcdefghijk", "+8613800138000"} {
		if err := Phone(s); err == nil {
			t.Fatalf("Phone(%q) 应被拒绝", s)
		}
	}
}

func TestPassword(t *testing.T) {
	for _, s := range []string{"abcd1234", "P@ssw0rd!", "a1" + strings.Repeat("x", 70)} {
		if err := Password(s); err != nil {
			t.Fatalf("Password(长度 %d) 应通过，实际 %v", len(s), err)
		}
	}
	bad := map[string]string{
		"太短":           "abc123",
		"纯字母":          "abcdefgh",
		"纯数字":          "12345678",
		"空":            "",
		"超过 bcrypt 上限": "a1" + strings.Repeat("x", 71),
	}
	for name, s := range bad {
		if err := Password(s); err == nil {
			t.Fatalf("Password 用例「%s」应被拒绝", name)
		}
	}
}

// TestPasswordBcryptCeiling 明确守住 72 字节这条线。
// bcrypt 会静默截断超长密码，若不在入口拦住，用户会以为设了长密码
// 而实际只有前 72 字节生效。
func TestPasswordBcryptCeiling(t *testing.T) {
	if err := Password("ab1" + strings.Repeat("x", 69)); err != nil { // 恰好 72
		t.Fatalf("72 字节应通过，实际 %v", err)
	}
	if err := Password("ab1" + strings.Repeat("x", 70)); err == nil { // 73
		t.Fatal("73 字节应被拒绝")
	}
}

func TestTextRange(t *testing.T) {
	got, err := TextRange("title", "  周末剧本杀  ", 2, 40)
	if err != nil {
		t.Fatalf("应通过，实际 %v", err)
	}
	if got != "周末剧本杀" {
		t.Fatalf("TextRange 应返回 TrimSpace 后的值，实际 %q", got)
	}

	// 中文按「字」计数而不是字节：5 个汉字是 15 字节，
	// 若按 len() 计数，上限 10 会把正常标题误杀。
	if _, err := TextRange("title", "周末剧本杀", 1, 5); err != nil {
		t.Fatalf("5 个汉字应满足上限 5（须按 rune 计数），实际 %v", err)
	}
	if _, err := TextRange("title", "周末剧本杀啊", 1, 5); err == nil {
		t.Fatal("6 个汉字应超出上限 5")
	}
	// 纯空白等价于空。
	if _, err := TextRange("title", "     ", 1, 10); err == nil {
		t.Fatal("纯空白应被判为空值并拒绝")
	}
}

// TestTextRangeErrorCarriesFieldName 断言错误里带上字段名与中文标签，
// 前端才能把提示挂到正确的输入框上。
func TestTextRangeErrorCarriesFieldName(t *testing.T) {
	_, err := TextRange("script_name", "", 2, 40)
	if err == nil {
		t.Fatal("应报错")
	}
	ae := apperr.From(err)
	if ae.Details["field"] != "script_name" {
		t.Fatalf("错误细节应含 field=script_name，实际 %v", ae.Details)
	}
	if !strings.Contains(ae.Message, "剧本名") {
		t.Fatalf("错误文案应使用中文标签「剧本名」，实际 %q", ae.Message)
	}
}

func TestIntRange(t *testing.T) {
	if err := IntRange("capacity", 6, 2, 12); err != nil {
		t.Fatalf("边界内应通过，实际 %v", err)
	}
	if err := IntRange("capacity", 2, 2, 12); err != nil {
		t.Fatalf("下边界应含入，实际 %v", err)
	}
	if err := IntRange("capacity", 12, 2, 12); err != nil {
		t.Fatalf("上边界应含入，实际 %v", err)
	}
	if err := IntRange("capacity", 1, 2, 12); err == nil {
		t.Fatal("低于下界应被拒绝")
	}
	if err := IntRange("capacity", 13, 2, 12); err == nil {
		t.Fatal("高于上界应被拒绝")
	}
}

func TestOneOf(t *testing.T) {
	if err := OneOf("room_type", "JUBENSHA", "JUBENSHA", "ESCAPE"); err != nil {
		t.Fatalf("白名单内应通过，实际 %v", err)
	}
	err := OneOf("room_type", "KTV", "JUBENSHA", "ESCAPE")
	if err == nil {
		t.Fatal("白名单外应被拒绝")
	}
	ae := apperr.From(err)
	if ae.Details["allowed"] == nil {
		t.Fatalf("错误细节应列出 allowed 集合，实际 %v", ae.Details)
	}
}

// TestAppErrSingletonsAreImmutable 是最容易被忽视的一条：
// 预定义错误是包级单例，WithMessage / WithDetail 必须返回副本。
// 若就地修改，第一个校验失败的请求会永久污染全局错误文案，
// 后续所有用户都会看到别人的报错信息，并且存在数据竞争。
func TestAppErrSingletonsAreImmutable(t *testing.T) {
	original := apperr.ErrValidation.Message

	_, _ = TextRange("title", "", 5, 10)
	_ = Username("!!")
	_ = OneOf("room_type", "X", "Y")

	if apperr.ErrValidation.Message != original {
		t.Fatalf("单例错误文案被污染: %q -> %q", original, apperr.ErrValidation.Message)
	}
	if apperr.ErrValidation.Details != nil {
		t.Fatalf("单例错误的 Details 被写入: %v", apperr.ErrValidation.Details)
	}
}
