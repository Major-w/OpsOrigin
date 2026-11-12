package user

import (
	"strings"
	"testing"
	"time"
)

func TestValidatePasswordPolicy(t *testing.T) {
	t.Cleanup(func() { SetPolicy(CurrentPolicy()) }) // 恢复

	SetPolicy(Policy{MinLen: 8, MinClasses: 2, MaxAgeDays: 0})

	cases := []struct {
		pw    string
		valid bool
		msg   string
	}{
		{"abc12345", true, ""},   // 长度 8，2 类
		{"abc123", false, "长度"},  // 太短
		{"abcdefgh", false, "类"}, // 仅 1 类
		{"Abcdefg1", true, ""},   // 3 类
		{"Ab1!", false, "长度"},    // 4 类但太短
		{"12345678", false, "类"}, // 仅数字
		{"abcd!@#$", true, ""},   // 2 类 + 符号
	}
	for _, c := range cases {
		err := ValidatePassword(c.pw)
		if c.valid && err != nil {
			t.Errorf("%q 应通过，实际 %v", c.pw, err)
		}
		if !c.valid && err == nil {
			t.Errorf("%q 应拒绝", c.pw)
		}
		if !c.valid && err != nil && !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%q 错误信息应含 %q，实际 %v", c.pw, c.msg, err)
		}
	}

	// 长度下限保护：配置 1 位会被拉回 4
	SetPolicy(Policy{MinLen: 1, MinClasses: 1})
	if p := CurrentPolicy(); p.MinLen != 4 {
		t.Fatalf("MinLen=1 应回退到下限 4，实际 %d", p.MinLen)
	}
}

func TestPasswordExpired(t *testing.T) {
	now := time.Now()
	p90 := Policy{MaxAgeDays: 90}
	p0 := Policy{MaxAgeDays: 0}

	if p0.PasswordExpired(now.AddDate(-1, 0, 0), now) {
		t.Fatal("MaxAgeDays=0 应永不过期")
	}
	if p90.PasswordExpired(now.AddDate(0, 0, -30), now) {
		t.Fatal("30 天前改密不应过期（90 天策略）")
	}
	if !p90.PasswordExpired(now.AddDate(0, 0, -91), now) {
		t.Fatal("91 天前改密应过期")
	}
	if p90.PasswordExpired(time.Time{}, now) {
		t.Fatal("零值时间（老数据）应视为不过期")
	}
}
