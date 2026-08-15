package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// 本文件实现"连接器生命周期钩子", 参考 DeepSeek Harness 的插件生命周期:
//   plugin/register → capability 校验 → policy 播种 → plugin/unregister → 清理
// 把"连接器接入时该做什么"(能力校验 / 默认审批策略播种 / 审计接入)从自定义工具
// 的增删改逻辑中剥离为可组合钩子, 新增接入期能力只需 Add 一个 LifecycleHook,
// 无需改动 Create/Update/Delete/SyncCustomTools 主流程。
//
// 边界语义(fail-closed):
//   - OnRegister 任一钩子返回 error → 拒绝本次注册/更新(连接器不会被注入到 registry)。
//   - OnUnregister 任一钩子返回 error → 仅告警, 不阻断反注册(拆除阶段以清理为优先)。

// ConnectorInfo 注册/反注册边界透传给钩子的连接器元数据(只读快照)。
type ConnectorInfo struct {
	ID            string
	TenantID      string
	Name          string // 工具名(LLM function calling 名称)
	Type          string // "http" | "cli"
	Enabled       bool
	HTTPMethod    string   // http 类型的方法; cli 为空
	HTTPURL       string   // http 类型的 URL; cli 为空
	CLICommand    string   // cli 类型的命令模板
	AllowedAgents []string // 连接器可见的 Agent 白名单; 空 = 全部可见
}

// connectorInfoFromTool 从 CustomTool 构造注册边界快照(避免钩子直接持有可变对象)。
func connectorInfoFromTool(t *CustomTool) ConnectorInfo {
	return ConnectorInfo{
		ID:            t.ID,
		TenantID:      t.TenantID,
		Name:          t.Name,
		Type:          t.Type,
		Enabled:       t.Enabled,
		HTTPMethod:    t.HTTPMethod,
		HTTPURL:       t.HTTPURL,
		CLICommand:    t.CLICommand,
		AllowedAgents: t.AllowedAgents,
	}
}

// ConnectorOnRegister 注册期钩子: 返回 error 视为校验/准备失败, 拒绝注册。
type ConnectorOnRegister func(ctx context.Context, c ConnectorInfo) error

// ConnectorOnUnregister 反注册期钩子: 返回 error 仅告警, 不阻断拆除。
type ConnectorOnUnregister func(ctx context.Context, c ConnectorInfo) error

// LifecycleHook 一组可选的生命周期钩子; 未设置(nil)的字段在链中自动跳过。
type LifecycleHook struct {
	OnRegister   ConnectorOnRegister
	OnUnregister ConnectorOnUnregister
}

// LifecycleHookChain 连接器生命周期钩子链: 按注册顺序执行注册/反注册钩子。并发安全。
// 与 #404 的 HookChain(工具执行期)互补: 本链作用于"连接器接入/拆除"边界, 而非单次工具调用。
type LifecycleHookChain struct {
	mu     sync.Mutex
	regs   []ConnectorOnRegister
	unregs []ConnectorOnUnregister
}

// NewLifecycleHookChain 构造空生命周期钩子链。
func NewLifecycleHookChain() *LifecycleHookChain {
	return &LifecycleHookChain{}
}

// Add 追加一组生命周期钩子(按类型追加到链尾, 保持注册顺序)。
func (c *LifecycleHookChain) Add(h LifecycleHook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h.OnRegister != nil {
		c.regs = append(c.regs, h.OnRegister)
	}
	if h.OnUnregister != nil {
		c.unregs = append(c.unregs, h.OnUnregister)
	}
}

// RunOnRegister 顺序执行注册钩子; 任一返回 error 即短路返回(注册失败-closed)。
func (c *LifecycleHookChain) RunOnRegister(ctx context.Context, info ConnectorInfo) error {
	c.mu.Lock()
	hs := append([]ConnectorOnRegister(nil), c.regs...)
	c.mu.Unlock()
	for _, h := range hs {
		if err := h(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

// RunOnUnregister 顺序执行反注册钩子; 收集首个 error(仍跑完全部钩子), 拆除阶段不阻断。
func (c *LifecycleHookChain) RunOnUnregister(ctx context.Context, info ConnectorInfo) error {
	c.mu.Lock()
	hs := append([]ConnectorOnUnregister(nil), c.unregs...)
	c.mu.Unlock()
	var firstErr error
	for _, h := range hs {
		if err := h(ctx, info); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// fireOnRegister 触发注册钩子链(nil 链安全)。
// 注册失败时回滚本次尝试中由钩子自动播种的策略与 scope(防泄漏): 仅移除"本次尝试新加入"的项,
// 既存的自动播种/人工覆盖不受影响。
func (e *Engine) fireOnRegister(ctx context.Context, c ConnectorInfo) error {
	if e.connHooks == nil {
		return nil
	}
	e.mu.Lock()
	beforePol := make(map[string]bool, len(e.autoSeededPolicies))
	for k := range e.autoSeededPolicies {
		beforePol[k] = true
	}
	beforeScope := make(map[string]bool, len(e.autoSeededScopes))
	for k := range e.autoSeededScopes {
		beforeScope[k] = true
	}
	e.mu.Unlock()
	if err := e.connHooks.RunOnRegister(ctx, c); err != nil {
		e.mu.Lock()
		for k := range e.autoSeededPolicies {
			if !beforePol[k] {
				delete(e.toolPolicies, k)
				delete(e.autoSeededPolicies, k)
			}
		}
		for k := range e.autoSeededScopes {
			if !beforeScope[k] {
				delete(e.toolScopes, k)
				delete(e.autoSeededScopes, k)
			}
		}
		e.mu.Unlock()
		return err
	}
	return nil
}

// fireOnUnregister 触发反注册钩子链(nil 链安全)。
func (e *Engine) fireOnUnregister(ctx context.Context, c ConnectorInfo) error {
	if e.connHooks == nil {
		return nil
	}
	return e.connHooks.RunOnUnregister(ctx, c)
}

// RegisterConnectorLifecycleHook 向连接器生命周期链追加一组钩子(校验/策略/审计等)。
// 注意: 必须在调用方不持 e.mu 时调用(钩子内部可能回读引擎字段)。
func (e *Engine) RegisterConnectorLifecycleHook(h LifecycleHook) {
	if e.connHooks == nil {
		e.connHooks = NewLifecycleHookChain()
	}
	e.connHooks.Add(h)
}

// ——— 内置默认生命周期钩子 ———

// validationOnRegister 能力校验钩子: 在连接器接入前做 fail-closed 校验——
// 类型合法、http 须带合法 http(s) URL 与方法、cli 须带命令模板。
// 早于 policySeed 注册, 因此校验失败不会留下播种的策略残留。
func (e *Engine) validationOnRegister(_ context.Context, c ConnectorInfo) error {
	if c.Name == "" {
		return fmt.Errorf("connector name is required")
	}
	switch c.Type {
	case "http":
		if c.HTTPMethod != "" {
			up := strings.ToUpper(c.HTTPMethod)
			switch up {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
			default:
				return fmt.Errorf("http connector unsupported method %q", c.HTTPMethod)
			}
		}
		if c.HTTPURL == "" {
			return fmt.Errorf("http connector %q requires http_url", c.Name)
		}
		if !strings.HasPrefix(c.HTTPURL, "http://") && !strings.HasPrefix(c.HTTPURL, "https://") {
			return fmt.Errorf("http connector %q url must start with http(s)://", c.Name)
		}
	case "cli":
		if c.CLICommand == "" {
			return fmt.Errorf("cli connector %q requires cli_command", c.Name)
		}
	default:
		return fmt.Errorf("connector %q unsupported type %q (want http|cli)", c.Name, c.Type)
	}
	return nil
}

// policySeedOnRegister 策略播种钩子: 连接器接入时按类型自动播种安全的默认审批策略,
// 免去运维逐个配置(参考 Harness 把 approval policy 做成可配置插件的"自动默认"分支)。
// 仅当该工具名尚无策略时播种, 并标记为自动播种, 反注册时只清理这些、不误删人工覆盖。
func (e *Engine) policySeedOnRegister(_ context.Context, c ConnectorInfo) error {
	if c.Name == "" {
		return nil
	}
	e.mu.Lock()
	if _, exists := e.toolPolicies[c.Name]; !exists {
		if e.toolPolicies == nil {
			e.toolPolicies = map[string]ToolPolicy{}
		}
		if e.autoSeededPolicies == nil {
			e.autoSeededPolicies = map[string]bool{}
		}
		e.toolPolicies[c.Name] = defaultPolicyForConnector(c)
		e.autoSeededPolicies[c.Name] = true
	}
	e.mu.Unlock()
	return nil
}

// policySeedOnUnregister 策略清理钩子: 仅清理本引擎自动播种的策略, 保留人工覆盖。
func (e *Engine) policySeedOnUnregister(_ context.Context, c ConnectorInfo) error {
	if c.Name == "" {
		return nil
	}
	e.mu.Lock()
	if e.autoSeededPolicies[c.Name] {
		delete(e.toolPolicies, c.Name)
		delete(e.autoSeededPolicies, c.Name)
	}
	e.mu.Unlock()
	return nil
}

// defaultPolicyForConnector 按连接器形态推导安全默认审批策略:
//   - cli(服务端 shell 执行): 最高风险 → PolicyApproval;
//   - http GET/HEAD(只读): 免审 → PolicyAllow;
//   - http DELETE(破坏性写): → PolicyApproval;
//   - http POST/PUT/PATCH(写): → PolicyRBAC(需 ai:tool:exec)。
func defaultPolicyForConnector(c ConnectorInfo) ToolPolicy {
	if c.Type == "cli" {
		return PolicyApproval
	}
	switch strings.ToUpper(c.HTTPMethod) {
	case "GET", "HEAD", "":
		return PolicyAllow
	case "DELETE":
		return PolicyApproval
	default:
		return PolicyRBAC
	}
}

// scopeSeedOnRegister 可见性播种钩子: 连接器接入时按自定义工具声明的 AllowedAgents
// 自动播种"该连接器仅对指定 Agent 可见"的 scope(参考 Harness 按 agent scope 隔离)。
// 仅当该工具名尚无 scope 时播种, 并标记为自动播种, 反注册时只清理这些、不误删人工覆盖。
func (e *Engine) scopeSeedOnRegister(_ context.Context, c ConnectorInfo) error {
	if c.Name == "" || len(c.AllowedAgents) == 0 {
		return nil // 未声明白名单 = 对全部 Agent 可见, 不播种
	}
	e.mu.Lock()
	if _, exists := e.toolScopes[c.Name]; !exists {
		if e.toolScopes == nil {
			e.toolScopes = map[string][]string{}
		}
		if e.autoSeededScopes == nil {
			e.autoSeededScopes = map[string]bool{}
		}
		agents := make([]string, 0, len(c.AllowedAgents))
		for _, a := range c.AllowedAgents {
			if a = strings.TrimSpace(a); a != "" {
				agents = append(agents, a)
			}
		}
		e.toolScopes[c.Name] = agents
		e.autoSeededScopes[c.Name] = true
	}
	e.mu.Unlock()
	return nil
}

// scopeSeedOnUnregister 可见性清理钩子: 仅清理本引擎自动播种的 scope, 保留人工覆盖。
func (e *Engine) scopeSeedOnUnregister(_ context.Context, c ConnectorInfo) error {
	if c.Name == "" {
		return nil
	}
	e.mu.Lock()
	if e.autoSeededScopes[c.Name] {
		delete(e.toolScopes, c.Name)
		delete(e.autoSeededScopes, c.Name)
	}
	e.mu.Unlock()
	return nil
}

// SetToolScope 热插拔单个连接器的 Agent 可见性白名单(参考 Harness scope 实例隔离的可配置化)。
// agents 为空或 nil 表示对全部 Agent 可见(取消隔离)。人工覆盖会清除该名的"自动播种"标记,
// 使其反注册时不再被生命周期清理钩子误删。
func (e *Engine) SetToolScope(name string, agents []string) {
	if name == "" {
		return
	}
	e.mu.Lock()
	if e.toolScopes == nil {
		e.toolScopes = map[string][]string{}
	}
	if e.autoSeededScopes == nil {
		e.autoSeededScopes = map[string]bool{}
	}
	if len(agents) == 0 {
		delete(e.toolScopes, name)
	} else {
		clean := make([]string, 0, len(agents))
		for _, a := range agents {
			if a = strings.TrimSpace(a); a != "" {
				clean = append(clean, a)
			}
		}
		if len(clean) == 0 {
			delete(e.toolScopes, name)
		} else {
			e.toolScopes[name] = clean
		}
	}
	delete(e.autoSeededScopes, name)
	e.mu.Unlock()
}

// GetToolScope 读取某连接器的 Agent 可见性白名单; 空切片表示对全部 Agent 可见。
func (e *Engine) GetToolScope(name string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	scope, ok := e.toolScopes[name]
	if !ok || len(scope) == 0 {
		return nil
	}
	out := make([]string, len(scope))
	copy(out, scope)
	return out
}

// toolVisibleToAgent 判断某连接器(工具名)对指定 Agent 是否可见。
// 与 Agent 自身 Tools 白名单是"与"关系(调用方在 buildToolDefs 中先判 allowlist)。
// 规则: 连接器未声明 scope(或为空) → 对所有 agent 可见; 否则仅在白名单内的 agent 可见。
// agentID 为空视为"未绑定具体 agent"(如内部调用), 此时任何声明了 scope 的连接器都不可见。
func (e *Engine) toolVisibleToAgent(name, agentID string) bool {
	e.mu.RLock()
	scope, ok := e.toolScopes[name]
	e.mu.RUnlock()
	if !ok || len(scope) == 0 {
		return true // 无 scope 限制
	}
	if agentID == "" {
		return false // 声明了 scope 但调用方未绑定 agent: 不可见(纵深防御)
	}
	for _, a := range scope {
		if a == agentID {
			return true
		}
	}
	return false
}
