// Package command provides command whitelisting and dangerous command detection.
//
// DangerousPolicy defines built-in high-risk command patterns that trigger
// approval requirements when scripts are marked as public.
package command

import (
	"regexp"
	"strings"
)

// DangerousRule represents a high-risk command pattern.
type DangerousRule struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"` // regexp pattern matching the command line
	Level   string `json:"level"`    // "critical" | "high" | "medium"
	Desc    string `json:"description"`
	re      *regexp.Regexp
}

// DangerousMatch records a detected high-risk command.
type DangerousMatch struct {
	RuleID  string `json:"rule_id"`
	Level   string `json:"level"`
	Pattern string `json:"pattern"`
	Desc    string `json:"description"`
	Line    int    `json:"line,omitempty"`
	Match   string `json:"match"`
}

// DangerousPolicy is a collection of high-risk command rules.
type DangerousPolicy struct {
	Rules []DangerousRule `json:"rules"`
}

// BuiltinDangerousRules returns the built-in high-risk command rules.
func BuiltinDangerousRules() []DangerousRule {
	rules := []DangerousRule{
		{ID: "rm_rf_root", Pattern: `rm\s+(-[a-z]*rf|-[a-z]*fr|-rf|--recursive).*\s+/`, Level: "critical", Desc: "递归删除根目录/系统目录"},
		{ID: "rm_rf_star", Pattern: `rm\s+(-[a-z]*rf|-[a-z]*fr|-rf|--recursive).*\s+\*\s*$`, Level: "high", Desc: "递归通配删除"},
		{ID: "rm_rf_any", Pattern: `rm\s+(-[a-z]*rf|-[a-z]*fr|-rf|--recursive)`, Level: "high", Desc: "递归强制删除"},
		{ID: "dd_disk", Pattern: `dd\s+if=.*\s+of=/dev/(sd[a-z]|hd[a-z]|nvme|mmcblk|disk)`, Level: "critical", Desc: "dd 覆盖磁盘设备"},
		{ID: "mkfs", Pattern: `mkfs\.\S+\s+/dev/`, Level: "critical", Desc: "格式化磁盘设备"},
		{ID: "chmod_777", Pattern: `chmod\s+.*777`, Level: "high", Desc: "设置 777 宽松权限"},
		{ID: "chmod_suid", Pattern: `chmod\s+[0-7]*[4567][0-7][0-7][0-7]\s`, Level: "high", Desc: "设置 SUID/SGID 位"},
		{ID: "curl_pipe_bash", Pattern: `curl\s+.*\|\s*(sudo\s+)?(bash|sh|zsh)`, Level: "high", Desc: "curl 管道执行（绕过审查）"},
		{ID: "wget_pipe_shell", Pattern: `wget\s+.*\|\s*(sudo\s+)?(bash|sh|zsh)`, Level: "high", Desc: "wget 管道执行（绕过审查）"},
		{ID: "fork_bomb", Pattern: `:\(\)\s*\{.*\|.*&\s*\}`, Level: "critical", Desc: "Fork Bomb 模式"},
		{ID: "iptables_flush", Pattern: `iptables\s+-F`, Level: "high", Desc: "清空 iptables 规则"},
		{ID: "shutdown", Pattern: `(shutdown|halt|poweroff|reboot)\s`, Level: "high", Desc: "系统关机/重启"},
		{ID: "truncate_file", Pattern: `>+\s*/etc/|>+\s*/dev/`, Level: "critical", Desc: "覆盖系统关键文件"},
		{ID: "eval_danger", Pattern: `eval\s+\$`, Level: "medium", Desc: "eval 变量注入风险"},
		{ID: "passwd_edit", Pattern: `(passwd|usermod|useradd)\s.*root`, Level: "critical", Desc: "修改 root 用户或密码"},
		{ID: "sysctl_insecure", Pattern: `sysctl\s.*(net\.ipv4\.ip_forward|kernel\.randomize_va_space=0|net\.ipv4\.conf\.all\.rp_filter=0)`, Level: "medium", Desc: "降低系统安全配置"},
		{ID: "mount_fs", Pattern: `mount\s+-o\s.*remount.*/(?:\s|$)`, Level: "high", Desc: "重新挂载根文件系统"},
		{ID: "umount_force", Pattern: `umount\s+-f`, Level: "high", Desc: "强制卸载文件系统"},
	}
	// Pre-compile regexps
	for i := range rules {
		rules[i].re = regexp.MustCompile(rules[i].Pattern)
	}
	return rules
}

// ScanScript checks script content against all dangerous rules.
// Returns the list of matches found, ordered by line number.
func (dp *DangerousPolicy) ScanScript(content string) []DangerousMatch {
	var matches []DangerousMatch
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // skip empty lines and comments
		}
		for _, rule := range dp.Rules {
			if rule.re == nil {
				continue
			}
			if loc := rule.re.FindStringIndex(line); loc != nil {
				matches = append(matches, DangerousMatch{
					RuleID:  rule.ID,
					Level:   rule.Level,
					Pattern: rule.Pattern,
					Desc:    rule.Desc,
					Line:    lineNum + 1,
					Match:   strings.TrimSpace(line),
				})
				break // one match per line
			}
		}
	}
	return matches
}

// NewDangerousPolicy creates a policy with built-in rules. Extras can be appended.
func NewDangerousPolicy(extra ...DangerousRule) *DangerousPolicy {
	rules := BuiltinDangerousRules()
	rules = append(rules, extra...)
	return &DangerousPolicy{Rules: rules}
}
