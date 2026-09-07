package chatops

import (
	"context"
	"errors"
	"net/http"
)

// 平台标识常量。
const (
	PlatformFeishu = "feishu"
	PlatformSlack  = "slack"
	PlatformWecom  = "wecom"
)

var errNotFound = errors.New("chatops: not found")

// InboundMessage 平台适配器解析出的一条用户入站消息(已验签/解密/幂等)。
type InboundMessage struct {
	Platform string // feishu / slack / wecom
	ChatID   string // 群/单聊会话标识(绑定键)
	UserID   string // 发言人平台侧 ID(审批 by 记录用)
	Text     string // 纯文本内容(已剥离 @机器人 标记)
	EventID  string // 平台事件幂等键(空则由 handler 生成)
}

// OutboundMessage 平台侧待回复消息。
type OutboundMessage struct {
	Text     string // markdown 文本(平台侧自行降级)
	Bindings map[string]string
}

// ApprovalCard 审批请求卡片(推送 + 交互回调)。
type ApprovalCard struct {
	ID      string `json:"id"` // PendingApproval.ID
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Args    string `json:"args"`
	AgentID string `json:"agent_id,omitempty"`
}

// Platform 平台适配器接口。实现方负责: 验签/解密、事件解析、消息回复、卡片交互。
// 新增平台(slack/wecom)实现本接口并注册到 Service 即可, 桥接层零改动。
type Platform interface {
	Name() string
	// HandleWebhook 处理平台回调请求。返回:
	//   - msg 非nil: 一条用户消息, 走问答桥
	//   - approval 非nil: 一次审批动作(卡片按钮), 走审批桥
	//   - 都为nil: 事件已消费(如 url_verification 已在函数内完成响应, 或非目标事件)
	// handler 需在函数内自行写 HTTP 响应(平台对响应体格式各有要求)。
	HandleWebhook(w http.ResponseWriter, r *http.Request, ch *Channel, dispatch Dispatcher) error
	// SendText 向指定会话发送文本消息(问答回复/审批推送/测试消息)。
	SendText(ctx context.Context, ch *Channel, chatID, text string) error
	// SendApprovalCard 推送审批卡片; 返回平台侧卡片/消息标识(幂等去重用, 可为空)。
	SendApprovalCard(ctx context.Context, ch *Channel, chatID string, card *ApprovalCard) (string, error)
}

// Dispatcher 桥接层回调接口: 适配器解析出消息/审批动作后交回桥接层处理。
// 以接口而非具体 *Service 暴露, 适配器测试可 mock。
type Dispatcher interface {
	// OnUserMessage 处理用户问答(同步; 调用方适配器自行决定响应策略)。
	OnUserMessage(ctx context.Context, ch *Channel, msg *InboundMessage) (reply string)
	// OnApprovalAction 处理 IM 侧审批动作, 返回提示文案。
	OnApprovalAction(ctx context.Context, ch *Channel, approvalID, action, by, comment string) string
	// ResolveChatsForApproval 解析审批应推送的目标会话(绑定该租户且启用审批推送的 chat_id 列表)。
	ResolveChatsForApproval(ctx context.Context, ch *Channel, tenantID string) []string
}

// registry 平台适配器注册表(编译期注册, init 时 Platform 实现自挂)。
var registry = map[string]Platform{}

func registerPlatform(p Platform) { registry[p.Name()] = p }

// platformOf 取平台适配器。
func platformOf(name string) Platform { return registry[name] }

// Platforms 返回已注册平台名列表(管理接口展示用)。
func Platforms() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
