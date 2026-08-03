// Package command 提供 Agent 命令执行白名单(§6 命令白名单硬需求)。
//
// 设计原则(fail-closed):
//   - Whitelist 非 nil 且 rules 为空  => Verify 拒绝所有命令(已启用但未配置规则)。
//   - Whitelist 为 nil              => Verify 不拦截(由调用方依 enforce 标志自行决定)。
//   - 任一 rule 命中即放行;全不命中则拒绝。
//
// Rule 类型:
//
//	exact  : 命令首 token(可执行)与 Pattern 完全相等。
//	prefix : 命令首 token 以 Pattern 开头(用于限定某命令族,如 /usr/bin/ 下)。
//	regex  : 整条命令匹配正则(用于带参数的精确约束,如 ^systemctl (start|stop) \S+$)。
//	path   : 命令首 token 须为绝对路径且等于 Pattern(锁定可执行文件路径,防 PATH 劫持)。
package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ErrDenied 命令未命中白名单时被返回。
var ErrDenied = errors.New("command denied by whitelist")

// RuleType 标识白名单规则的匹配方式。
type RuleType string

const (
	RuleExact  RuleType = "exact"
	RulePrefix RuleType = "prefix"
	RuleRegex  RuleType = "regex"
	RulePath   RuleType = "path"
)

// Rule 单条白名单规则。
type Rule struct {
	Type    RuleType `json:"type"`
	Pattern string   `json:"pattern"`
	re      *regexp.Regexp
}

// Whitelist 命令白名单。Mode 仅用于前端展示/语义说明,匹配逻辑由 Rules 决定。
type Whitelist struct {
	Mode  string `json:"mode"` // "enforce" | "advisory"
	Rules []Rule `json:"rules"`

	mu sync.RWMutex
}

// New 编译正则并构造白名单。pattern 非法时返回错误(不让错误规则静默生效)。
func New(mode string, rules []Rule) (*Whitelist, error) {
	w := &Whitelist{Mode: mode, Rules: make([]Rule, 0, len(rules))}
	for _, r := range rules {
		if r.Type == RuleRegex {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex rule %q: %w", r.Pattern, err)
			}
			r.re = re
		}
		w.Rules = append(w.Rules, r)
	}
	return w, nil
}

// FromJSON 从持久化 JSON(库表 rules 字段)反序列化。
func FromJSON(mode string, rulesJSON string) (*Whitelist, error) {
	var raw []struct {
		Type    RuleType `json:"type"`
		Pattern string   `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(rulesJSON), &raw); err != nil {
		return nil, fmt.Errorf("parse whitelist rules: %w", err)
	}
	rules := make([]Rule, 0, len(raw))
	for _, r := range raw {
		rules = append(rules, Rule{Type: r.Type, Pattern: r.Pattern})
	}
	return New(mode, rules)
}

// RawRules 返回白名单规则副本(供 API 序列化下发)。
func (w *Whitelist) RawRules() []Rule {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Rule, len(w.Rules))
	copy(out, w.Rules)
	return out
}

// Verify 校验命令是否被白名单允许。
//   - w == nil        : 返回 nil(不拦截,调用方依 enforce 决定)。
//   - rules 为空       : 返回 ErrDenied(已启用但无规则 = 拒绝一切)。
//   - 任一规则命中    : 返回 nil(放行)。
//   - 全不命中        : 返回 ErrDenied。
func (w *Whitelist) Verify(command string) error {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	rules := w.Rules
	w.mu.RUnlock()

	if len(rules) == 0 {
		return fmt.Errorf("%w: whitelist enabled but empty (no rules configured)", ErrDenied)
	}

	token := firstToken(command)
	for _, r := range rules {
		if matchRule(r, token, command) {
			return nil
		}
	}
	return fmt.Errorf("%w: command %q not matched by any allow-rule", ErrDenied, truncate(command, 80))
}

// matchRule 按规则类型匹配。
func matchRule(r Rule, token, command string) bool {
	switch r.Type {
	case RuleExact:
		return token == r.Pattern
	case RulePrefix:
		return strings.HasPrefix(token, r.Pattern)
	case RuleRegex:
		if r.re == nil {
			return false
		}
		return r.re.MatchString(command)
	case RulePath:
		// 仅绝对路径可执行被允许,且必须等于/前缀于 Pattern。
		if !strings.HasPrefix(token, "/") {
			return false
		}
		return token == r.Pattern || strings.HasPrefix(token, r.Pattern+"/")
	default:
		return false
	}
}

// firstToken 取命令首 token(可执行),按空白切分。
func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
