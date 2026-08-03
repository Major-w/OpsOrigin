package agentic

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// 敏感操作审批策略 (PRD 5.2.5)
//
// 三种策略模式:
//   - always_ask     每次执行敏感操作都须用户在界面确认(弹窗)
//   - always_allow   始终允许, 敏感操作直接执行(高风险, 仅建议开发/测试环境)
//   - sensitive_only 仅对"敏感工具"触发确认, 其余直接执行(默认, 安全优先)
//
// 策略层级: Agent 级覆盖 > 全局默认。敏感操作触发后通过 SSE `ask` 事件
// 挂起执行, 等待用户批准/拒绝; 超时(AskTimeoutSec)视为拒绝(fail-closed)。
// ============================================================================

// ApprovalPolicyMode 审批策略模式。
type ApprovalPolicyMode string

const (
	ApprovalAlwaysAsk     ApprovalPolicyMode = "always_ask"
	ApprovalAlwaysAllow   ApprovalPolicyMode = "always_allow"
	ApprovalSensitiveOnly ApprovalPolicyMode = "sensitive_only"
)

// DefaultSensitiveTools 默认视为敏感的工具(写/执行类)。
// 只读工具(cmdb_query / knowledge_search / http_get)不在其中。
var DefaultSensitiveTools = []string{"script_run", "ticket_create"}

// ApprovalPolicy 一条审批策略(全局默认或某个 Agent 的覆盖)。
type ApprovalPolicy struct {
	Mode ApprovalPolicyMode `json:"mode"`
	// SensitiveTools 敏感工具名单; 为空时使用 DefaultSensitiveTools。
	SensitiveTools []string `json:"sensitive_tools"`
	// AskTimeoutSec 交互式确认超时(秒); <=0 时使用默认 60。
	AskTimeoutSec int `json:"ask_timeout_sec"`
}

// DefaultApprovalPolicy 全局默认策略: 仅敏感操作询问 + 60s 超时。
func DefaultApprovalPolicy() ApprovalPolicy {
	return ApprovalPolicy{
		Mode:           ApprovalSensitiveOnly,
		SensitiveTools: DefaultSensitiveTools,
		AskTimeoutSec:  60,
	}
}

// IsSensitive 判断工具是否命中敏感名单。
func (p *ApprovalPolicy) IsSensitive(tool string) bool {
	tools := p.SensitiveTools
	if len(tools) == 0 {
		tools = DefaultSensitiveTools
	}
	for _, t := range tools {
		if t == tool {
			return true
		}
	}
	return false
}

// NeedsApproval 判断指定工具在当前策略下是否需要交互式确认。
func (p *ApprovalPolicy) NeedsApproval(tool string) bool {
	switch p.Mode {
	case ApprovalAlwaysAllow:
		return false
	case ApprovalAlwaysAsk:
		return true
	default: // sensitive_only
		return p.IsSensitive(tool)
	}
}

// ——— 挂起的审批请求 ———

// approvalNotifyCtxKey 通过 context 传递"当前 SSE 连接的 ask 通知回调",
// 使审批请求只通知到发起它的那个流式连接(并发多连接互不干扰)。
type approvalNotifyCtxKey struct{}

// WithApprovalNotify 在 context 中注入审批请求通知回调(SSE 写入函数)。
func WithApprovalNotify(ctx context.Context, fn func(*PendingApproval)) context.Context {
	return context.WithValue(ctx, approvalNotifyCtxKey{}, fn)
}

func approvalNotifyFrom(ctx context.Context) func(*PendingApproval) {
	fn, _ := ctx.Value(approvalNotifyCtxKey{}).(func(*PendingApproval))
	return fn
}

// ApprovalResponse 用户对审批请求的响应。
type ApprovalResponse struct {
	Action  string `json:"action"` // "approve" | "reject"
	By      string `json:"by"`
	Comment string `json:"comment"`
	At      time.Time
}

// PendingApproval 一个等待用户确认的敏感操作。
type PendingApproval struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	Tool      string    `json:"tool"`
	Args      string    `json:"args"`    // 人类可读参数摘要(截断)
	Summary   string    `json:"summary"` // 人类可读操作描述
	CreatedAt time.Time `json:"created_at"`
	Deadline  time.Time `json:"deadline"`
	responded chan ApprovalResponse
	once      sync.Once
}

// ApprovalManager 管理挂起的审批请求: 创建、响应、超时。
type ApprovalManager struct {
	mu      sync.RWMutex
	pending map[string]*PendingApproval
	// defaultTimeout 无策略指定时使用的确认超时。
	defaultTimeout time.Duration
	// onRequest 审批请求创建时回调(service 层注入, 用于发 SSE ask 事件)。
	onRequest func(*PendingApproval)
}

// NewApprovalManager 构造审批管理器。
func NewApprovalManager() *ApprovalManager {
	return &ApprovalManager{
		pending:        make(map[string]*PendingApproval),
		defaultTimeout: 60 * time.Second,
	}
}

// SetOnRequest 注入审批请求回调(SSE ask 事件发射点)。
func (m *ApprovalManager) SetOnRequest(fn func(*PendingApproval)) {
	m.mu.Lock()
	m.onRequest = fn
	m.mu.Unlock()
}

// Request 创建一个审批请求并挂起等待响应。
// 返回 nil 表示被拒绝或超时(fail-closed); 返回响应且 Action=="approve" 表示批准。
// ctx 取消/超时同样视为拒绝并清理挂起状态。
func (m *ApprovalManager) Request(ctx context.Context, tenantID, agentID, tool string, argsJSON string, summary string, timeout time.Duration) *ApprovalResponse {
	if timeout <= 0 {
		timeout = m.defaultTimeout
	}
	pa := &PendingApproval{
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		AgentID:   agentID,
		Tool:      tool,
		Args:      truncateStr(argsJSON, 600),
		Summary:   summary,
		CreatedAt: time.Now(),
		Deadline:  time.Now().Add(timeout),
		responded: make(chan ApprovalResponse, 1),
	}

	m.mu.Lock()
	m.pending[pa.ID] = pa
	onReq := m.onRequest
	m.mu.Unlock()

	// 通知外部(SSE)有新的审批请求: 优先当前连接的上下文回调, 其次全局回调
	if fn := approvalNotifyFrom(ctx); fn != nil {
		fn(pa)
	} else if onReq != nil {
		onReq(pa)
	}

	select {
	case resp := <-pa.responded:
		m.remove(pa.ID)
		return &resp
	case <-time.After(timeout):
		m.remove(pa.ID)
		return nil // 超时视为拒绝
	case <-ctx.Done():
		m.remove(pa.ID)
		return nil
	}
}

// Respond 用户对审批请求做出响应。
func (m *ApprovalManager) Respond(id, action, by, comment string) bool {
	m.mu.RLock()
	pa, ok := m.pending[id]
	m.mu.RUnlock()
	if !ok {
		return false // 不存在或已超时
	}
	pa.once.Do(func() {
		pa.responded <- ApprovalResponse{
			Action:  action,
			By:      by,
			Comment: comment,
			At:      time.Now(),
		}
	})
	return true
}

// ListPending 列出当前挂起的审批请求(管理/审计用)。
func (m *ApprovalManager) ListPending(tenantID string) []*PendingApproval {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*PendingApproval, 0)
	for _, pa := range m.pending {
		if pa.TenantID == tenantID {
			out = append(out, pa)
		}
	}
	return out
}

func (m *ApprovalManager) remove(id string) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

// truncateStr 截断长字符串(用于摘要展示)。
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
