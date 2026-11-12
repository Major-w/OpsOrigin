package user

// 密码策略：强度校验（最小长度 + 最少字符类别数）+ 有效期检查。
// 策略来源 sysconfig（security 类别）热更推送；未接线时用默认值。
// 校验点：创建用户 / 修改密码 / 批量导入（手工填写的密码）。

import (
	"fmt"
	"sync/atomic"
	"time"
	"unicode"
)

// Policy 密码策略。
type Policy struct {
	MinLen     int // 最小长度（默认 8，最低 4）
	MinClasses int // 至少覆盖的字符类别数（小写/大写/数字/符号，默认 2，上限 4）
	MaxAgeDays int // 密码有效期天数（0=不过期）
}

var defaultPolicy = Policy{MinLen: 8, MinClasses: 2, MaxAgeDays: 0}

var policyAtomic atomic.Value // 存 Policy

// SetPolicy 由 sysconfig 装配/热更时调用（覆盖内存策略）。
func SetPolicy(p Policy) {
	if p.MinLen <= 0 {
		p.MinLen = defaultPolicy.MinLen
	}
	if p.MinLen < 4 {
		p.MinLen = 4 // 下限保护：不允许配置出"长度 1"这类无效策略
	}
	if p.MinClasses <= 0 {
		p.MinClasses = defaultPolicy.MinClasses
	}
	if p.MinClasses > 4 {
		p.MinClasses = 4
	}
	if p.MaxAgeDays < 0 {
		p.MaxAgeDays = 0
	}
	policyAtomic.Store(p)
}

// CurrentPolicy 返回当前策略（未配置时为默认值）。
func CurrentPolicy() Policy {
	if v, ok := policyAtomic.Load().(Policy); ok {
		return v
	}
	return defaultPolicy
}

// ValidatePassword 按策略校验密码强度，不符合返回可读错误（nil=通过）。
func ValidatePassword(pw string) error {
	p := CurrentPolicy()
	if len([]rune(pw)) < p.MinLen {
		return fmt.Errorf("密码长度不能少于 %d 位", p.MinLen)
	}
	var hasLower, hasUpper, hasDigit, hasSpecial bool
	for _, r := range pw {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}
	classes := 0
	for _, b := range []bool{hasLower, hasUpper, hasDigit, hasSpecial} {
		if b {
			classes++
		}
	}
	if classes < p.MinClasses {
		return fmt.Errorf("密码至少需包含 %d 类字符（小写/大写/数字/符号），当前仅 %d 类", p.MinClasses, classes)
	}
	return nil
}

// PasswordExpired 判断密码是否已过期（MaxAgeDays=0 永不过期；LDAP 目录账号本地密码随机不参与登录，同样按过期处理提示）。
func (p Policy) PasswordExpired(changedAt time.Time, now time.Time) bool {
	if p.MaxAgeDays <= 0 || changedAt.IsZero() {
		return false
	}
	return now.Sub(changedAt) > time.Duration(p.MaxAgeDays)*24*time.Hour
}
