// Package validate 提供全平台统一的输入校验规则。
//
// 名称类字段(脚本名/工作流名/定时任务名/角色名/CI 名等)统一约束:
// 仅允许 字母/数字/横杠(-)/下划线(_),不允许空格与其他特殊字符,长度 1-64。
// 主机类名称(Agent/Proxy)在此基础上额外放行点号(.),以兼容 FQDN 主机名。
package validate

import (
	"fmt"
	"regexp"
)

const (
	// MaxNameLen 名称最大长度。
	MaxNameLen = 64

	// namePatternText 供前端/文档展示的规则说明。
	nameRuleText = "仅允许字母、数字、横杠(-)和下划线(_),不允许空格及其他特殊字符,长度 1-64"
	hostRuleText = "仅允许字母、数字、点(.)、横杠(-)和下划线(_),不允许空格及其他特殊字符,长度 1-64"
)

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// Name 校验用户输入的名称字段。field 用于错误消息(如 "脚本名称")。
func Name(field, v string) error {
	if v == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	if len(v) > MaxNameLen {
		return fmt.Errorf("%s过长(最多 %d 字符)", field, MaxNameLen)
	}
	if !nameRe.MatchString(v) {
		return fmt.Errorf("%s格式非法: %s", field, nameRuleText)
	}
	return nil
}

// NameOptional 同 Name,但允许为空(用于 PATCH/Update 中未提交的字段)。
func NameOptional(field, v string) error {
	if v == "" {
		return nil
	}
	return Name(field, v)
}

// HostName 校验主机类名称(Agent/Proxy),额外允许点号以兼容 FQDN。
func HostName(field, v string) error {
	if v == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	if len(v) > MaxNameLen {
		return fmt.Errorf("%s过长(最多 %d 字符)", field, MaxNameLen)
	}
	if !hostRe.MatchString(v) {
		return fmt.Errorf("%s格式非法: %s", field, hostRuleText)
	}
	return nil
}

// HostNameOptional 同 HostName,但允许为空。
func HostNameOptional(field, v string) error {
	if v == "" {
		return nil
	}
	return HostName(field, v)
}
