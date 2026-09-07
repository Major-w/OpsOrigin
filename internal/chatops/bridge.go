package chatops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/ai/agentic"
)

// 桥接参数: IM 问答引擎限时与回复长度上限。
const (
	qaTimeout     = 90 * time.Second // IM 场景同步等待上限(超过则回复降级提示, AI 侧继续跑完落库)
	replyMaxRunes = 3800             // 飞书 text 消息体面长度(平台硬限远大于此, 取可读上限)
	promptMaxLen  = 8000             // 入站提问长度上限(防灌水)
)

// bridge 桥接实现: 适配器 ↔ Engine。
type bridge struct {
	st     *store
	engine *agentic.Engine
	// botAgentID 未绑定显式 agent 时使用的默认 Agent(空=引擎默认路由)。
	botAgentID string
}

// OnUserMessage 问答桥: 绑定解析 → Engine.Chat(续接会话) → 回复文本。
// 满足 Dispatcher 接口。永远返回非空回复(出错也回复用户), 网络层不静默。
func (b *bridge) OnUserMessage(ctx context.Context, ch *Channel, msg *InboundMessage) string {
	prompt := strings.TrimSpace(msg.Text)
	if prompt == "" {
		return ""
	}
	if len(prompt) > promptMaxLen {
		prompt = prompt[:promptMaxLen]
	}

	// 1. 绑定解析: (platform, chat_id) → tenant/agent/conversation
	bd, err := b.st.getBinding(ctx, msg.Platform, msg.ChatID)
	if err != nil {
		slog.Warn("chatops: load binding failed", "error", err)
		return "服务暂时不可用, 请稍后重试。"
	}
	if bd == nil {
		bd = &Binding{Platform: msg.Platform, ChatID: msg.ChatID, TenantID: ch.TenantID}
	}
	// 渠道配置的默认 agent 优先于历史绑定(改渠道配置即时生效)
	agentID := b.botAgentID
	if ch.Platform == PlatformFeishu {
		var cfg feishuSettings
		if err := unmarshalSettings(ch.Settings, &cfg); err == nil && cfg.DefaultAgentID != "" {
			agentID = cfg.DefaultAgentID
		}
	}
	if agentID == "" {
		agentID = bd.AgentID
	}

	// 2. 调引擎: 传 conversationID 续接服务端持久历史(含 tool 消息); 首次为空自动新建。
	//    租户上下文手动注入(回调链路无 JWT)。
	qctx, cancel := context.WithTimeout(ctx, qaTimeout)
	defer cancel()
	qctx = context.WithValue(qctx, "tenant_id", bd.TenantID)

	content, convID, err := b.engine.Chat(qctx, agentID, prompt, nil, bd.ConversationID, "", false)
	if err != nil {
		slog.Warn("chatops: engine chat failed", "platform", msg.Platform, "chat_id", msg.ChatID, "error", err)
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return "问题较复杂, 仍在后台处理中, 稍后可再问我处理结果。"
		}
		return "处理失败: " + truncForIM(err.Error(), 200)
	}

	// 3. 回写绑定(首次会话 convID 才产生; agent 变更也落库)
	if bd.ConversationID != convID || bd.AgentID != agentID {
		bd.ConversationID, bd.AgentID = convID, agentID
		if err := b.st.upsertBinding(context.WithoutCancel(ctx), bd); err != nil {
			slog.Warn("chatops: save binding failed", "error", err)
		}
	}
	return clipForIM(content)
}

// OnApprovalAction 审批桥: IM 卡片按钮 → ApprovalManager.Respond。
func (b *bridge) OnApprovalAction(ctx context.Context, ch *Channel, approvalID, action, by, comment string) string {
	switch action {
	case "approve", "reject":
	default:
		return "未知的审批动作"
	}
	if !b.engine.Approvals().Respond(approvalID, action, "im:"+ch.Platform+":"+by, comment) {
		return "审批单不存在或已处理"
	}
	if action == "approve" {
		return "已批准, 操作执行中"
	}
	return "已拒绝"
}

// ResolveChatsForApproval 审批推送目标: 绑定了该渠道平台且租户匹配的会话。
// MVP 策略: 推送到该租户该平台的所有绑定会话(通常运维群即审批群); 后续可加 per-binding 开关。
func (b *bridge) ResolveChatsForApproval(ctx context.Context, ch *Channel, tenantID string) []string {
	bindings, err := b.st.listBindings(ctx, tenantID)
	if err != nil {
		return nil
	}
	var out []string
	for _, bd := range bindings {
		if bd.Platform == ch.Platform {
			out = append(out, bd.ChatID)
		}
	}
	return out
}

// OnApprovalRequest 审批推送钩子: 注册到 ApprovalManager.SetOnRequest。
// 将 PendingApproval 推送到同租户所有启用渠道的绑定会话(飞书卡片, 其余平台 text)。
// 注意: ApprovalManager.onRequest 是单槽回调, 当前 ai SSE 未占用;
// 若未来需要多订阅方, 在此改为链式分发。
func (s *Service) OnApprovalRequest(pa *agentic.PendingApproval) {
	if pa == nil || s.engine == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	channels, err := s.st.listEnabledChannels(ctx)
	if err != nil {
		slog.Warn("chatops: list channels for approval failed", "error", err)
		return
	}
	card := &ApprovalCard{ID: pa.ID, Tool: pa.Tool, Summary: pa.Summary, Args: pa.Args, AgentID: pa.AgentID}
	for _, ch := range channels {
		if ch.TenantID != pa.TenantID {
			continue
		}
		p := platformOf(ch.Platform)
		if p == nil {
			continue
		}
		for _, chatID := range s.bridge.ResolveChatsForApproval(ctx, ch, pa.TenantID) {
			if _, err := p.SendApprovalCard(ctx, ch, chatID, card); err != nil {
				slog.Warn("chatops: push approval card failed", "platform", ch.Platform, "chat_id", chatID, "error", err)
			}
		}
	}
}

// clipForIM 回复裁剪: 超长截断加提示(IM 里贴几千行 Markdown 没有可读性)。
func clipForIM(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= replyMaxRunes {
		return s
	}
	return string(runes[:replyMaxRunes]) + fmt.Sprintf("\n\n…(内容过长已截断, 完整结果见平台会话页 %d 字)", len(runes))
}

func truncForIM(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func unmarshalSettings(raw json.RawMessage, v any) error { return json.Unmarshal(raw, v) }
