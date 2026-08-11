package itsm

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/pkg/principal"
)

// actorOf 解析 ITSM 业务主体名。
//
// ITSM 的 assignee / created_by / @提及 全部以 username 为准, 而平台鉴权链
// 注入 context 的是 user_id(UUID), 两者直接比对几乎不会命中。因此取值优先级为:
// 显式入参 → ?viewer= 查询参数 → 主体展示名(= users.username) → user_id 兜底。
func actorOf(r *http.Request, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("viewer")); v != "" {
		return v
	}
	if n := strings.TrimSpace(principal.Get(r.Context()).Name()); n != "" {
		return n
	}
	return getUserID(r, "")
}

// 站内消息分类,取值与 internal/message 的 Category 对齐。
// 这里用字符串常量而非直接引用消息包的类型,是为了让 itsm 只依赖一个
// 极小的投递接口 —— 消息模块的模型演进不会波及工单主流程。
const (
	msgCategoryTicket   = "ticket"
	msgCategoryApproval = "approval"
	msgCategoryMention  = "mention"
	msgCategorySubtask  = "subtask"
	msgCategorySLA      = "sla"
)

const (
	msgLevelInfo     = "info"
	msgLevelWarning  = "warning"
	msgLevelCritical = "critical"
)

// InboxMessage 一条待投递的站内消息(字段与 message.Message 对齐)。
type InboxMessage struct {
	Category string
	Level    string
	Title    string
	Content  string
	Link     string
	RefType  string
	RefID    string
	Actor    string
}

// MessageSender 站内消息投递能力(由 cmd/core 桥接到 message.Service)。
// 只用基础类型表达收件人 —— ITSM 侧业务主体一直是 username
// (assignee / created_by / @提及),不引入平台 user_id 的转换负担。
type MessageSender interface {
	SendToUsers(ctx context.Context, tenantID string, usernames []string, m InboxMessage) error
}

// SetMessageSender 注入站内消息投递出口(nil = 关闭站内消息)。
func (s *Service) SetMessageSender(m MessageSender) { s.messages = m }

// ticketLink 工单详情页深链,供消息中心点击跳转。
func ticketLink(ticketID string) string {
	return "/itsm/tickets?id=" + ticketID
}

// sendInbox 投递站内消息。收件人会先做「去重 + 去空 + 剔除触发者本人」处理 ——
// 自己操作产生的消息不该再回推给自己,否则铃铛会被自己的动作刷屏。
func (s *Service) sendInbox(ctx context.Context, tenantID string, recipients []string, m InboxMessage) {
	if s.messages == nil {
		return
	}
	seen := make(map[string]bool, len(recipients))
	var to []string
	for _, name := range recipients {
		name = strings.TrimSpace(name)
		if name == "" || name == "system" || name == "anonymous" {
			continue
		}
		if m.Actor != "" && strings.EqualFold(name, m.Actor) {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		to = append(to, name)
	}
	if len(to) == 0 {
		return
	}
	if err := s.messages.SendToUsers(ctx, tenantID, to, m); err != nil {
		slog.Warn("itsm: deliver inbox message failed", "title", m.Title, "error", err)
	}
}

// notifyTicketInbox 工单类消息的通用投递入口。
func (s *Service) notifyTicketInbox(ctx context.Context, t *model.Ticket, category, level, title, content, actor string, recipients ...string) {
	if t == nil {
		return
	}
	s.sendInbox(ctx, t.TenantID, recipients, InboxMessage{
		Category: category,
		Level:    level,
		Title:    title,
		Content:  content,
		Link:     ticketLink(t.ID),
		RefType:  "ticket",
		RefID:    t.ID,
		Actor:    actor,
	})
}

// mentionPattern 匹配评论正文里的 @用户名。用户名允许字母/数字/下划线/连字符/点/中文,
// 与前端 @ 选择器插入的格式一致(插入后紧跟一个空格)。
var mentionPattern = regexp.MustCompile(`@([\w.\-\x{4e00}-\x{9fa5}]+)`)

// extractMentions 从评论正文提取被 @ 的用户名。
// 显式传入的 explicit 列表优先(前端选人时给出的精确值),正文解析作为兜底 ——
// 邮件入站 / 机器人入站这些通道不会带结构化 mentions 字段。
func extractMentions(content string, explicit []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, name)
	}
	for _, name := range explicit {
		add(name)
	}
	for _, m := range mentionPattern.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	return out
}

// optionalComment 把可选备注拼成「 / 备注: xxx」,为空时返回空串,
// 避免消息正文出现「关闭人: alice / 备注: 」这种半截尾巴。
func optionalComment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return " / 备注: " + truncate(s, 120)
}

// truncate 截断消息正文,避免把整篇变更方案塞进铃铛列表。
func truncate(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}

// assigneeAndOwner 返回工单的负责人与申请人(用于关单/重开这类双方都该知情的事件)。
func assigneeAndOwner(t *model.Ticket) []string {
	if t == nil {
		return nil
	}
	return []string{t.Assignee, t.CreatedBy}
}

// subtaskLink 子任务锚点仍落在工单详情页(子任务是工单的从属对象)。
func subtaskLink(ticketID, subtaskID string) string {
	return fmt.Sprintf("/itsm/tickets?id=%s&subtask=%s", ticketID, subtaskID)
}

// notifySubtaskInbox 子任务类消息投递。链接带 subtask 锚点,点击可直接展开对应任务卡片。
func (s *Service) notifySubtaskInbox(ctx context.Context, t *model.Ticket, st *model.Subtask, level, title, content, actor string, recipients ...string) {
	if t == nil || st == nil {
		return
	}
	sid := strconv.FormatInt(st.ID, 10)
	s.sendInbox(ctx, t.TenantID, recipients, InboxMessage{
		Category: msgCategorySubtask,
		Level:    level,
		Title:    title,
		Content:  content,
		Link:     subtaskLink(t.ID, sid),
		RefType:  "subtask",
		RefID:    sid,
		Actor:    actor,
	})
}
