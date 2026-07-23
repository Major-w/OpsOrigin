package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/llm"
)

// Engine 是 AI 助手引擎的核心:管理 Agent 生命周期、编排 ExecutionPlan 的执行。
//
// 执行模型:
//   - 每个 Step 若指定了 Tool,则通过 mcp.Registry 执行对应 MCP 工具;
//   - 若 Step 未指定 Tool(视为 LLM 推理步),则调用 llm.Client 生成文本;
//   - 任一步骤失败即中止整个 Plan(运维操作需 fail-closed)。
//
// 存储当前为进程内 map(与仓库内 iam/notification 等服务一致)。
// 多副本部署时如需持久化,应将 agents/plans 落到 Postgres(outbox 模式)。
type Engine struct {
	mu          sync.RWMutex
	agents      map[string]*model.AgentInstance
	plans       map[string]*model.ExecutionPlan
	convStore   ConversationStore // PG 优先; nil 时回退内存 convs map
	convs       map[string]*Conversation
	reg         *mcp.Registry
	lim         ratelimit.RateLimiter
	llmClient   *llm.Client
	ready       bool
}

// NewEngine 构造引擎并注册内建工具。
// reg 中的业务工具应由调用方在 NewEngine 之后(或之前)通过 reg.Register 注入;
// 此处仅注册与引擎强耦合的、安全的内建工具。
func NewEngine(reg *mcp.Registry, lim ratelimit.RateLimiter, llmClient *llm.Client, store ConversationStore) *Engine {
	e := &Engine{
		agents:    make(map[string]*model.AgentInstance),
		plans:     make(map[string]*model.ExecutionPlan),
		convStore: store,
		convs:     make(map[string]*Conversation),
		reg:       reg,
		lim:      lim,
		llmClient: llmClient,
	}
	e.registerBuiltinTools()
	return e
}

// registerBuiltinTools 注册安全的内建 MCP 工具,使引擎开箱即可执行。
//
// 安全边界:
//   - llm_chat : 纯 LLM 推理,无副作用;
//   - http_get  : 仅只读 GET,不携带请求体,适合查询类集成;
// 如需 shell 执行 / 写操作类工具,必须经过显式安全评审后在此或业务侧注册。
func (e *Engine) registerBuiltinTools() {
	e.reg.Register(mcp.ToolDefinition{
		Name:        "llm_chat",
		Description: "调用底层 LLM 生成文本回复(自然语言推理/总结/建议),无副作用",
		Parameters: map[string]mcp.ParamSchema{
			"prompt": {Type: "string", Description: "提示词", Required: true},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			prompt, _ := input["prompt"].(string)
			if prompt == "" {
				return nil, fmt.Errorf("llm_chat: prompt is required")
			}
			resp, err := e.llmClient.Chat(ctx, llm.ChatRequest{
				Messages: []llm.Message{{Role: "user", Content: prompt}},
			})
			if err != nil {
				return nil, err
			}
			if len(resp.Choices) == 0 {
				return nil, fmt.Errorf("llm_chat: empty response")
			}
			return resp.Choices[0].Message.Content, nil
		},
	})

	e.reg.Register(mcp.ToolDefinition{
		Name:        "http_get",
		Description: "对指定 URL 发起只读 GET 请求(无请求体),适合查询类外部系统集成",
		Parameters: map[string]mcp.ParamSchema{
			"url": {Type: "string", Description: "目标 URL", Required: true},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			u, _ := input["url"].(string)
			if u == "" {
				return nil, fmt.Errorf("http_get: url is required")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return nil, fmt.Errorf("http_get: build request: %w", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("http_get: do request: %w", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("http_get: read body: %w", err)
			}
			return map[string]any{
				"status": resp.StatusCode,
				"body":   string(body),
			}, nil
		},
	})
}

// Start 启动引擎。当前无后台 goroutine(执行是同步的),仅标记就绪并输出诊断信息。
func (e *Engine) Start() error {
	e.mu.Lock()
	e.ready = true
	e.mu.Unlock()
	slog.Info("agentic engine started",
		"agents", e.agentCount(),
		"tools", len(e.reg.List()))
	return nil
}

// Chat 执行一次单轮(可带历史)的 LLM 推理,供 AI 助手聊天端点使用。
// 与 ExecutePlan 的"无 Tool 步骤"共用底层 llm.Client,但此处直接构造消息,
// 支持把对话历史透传进来,使多轮对话连贯。
func (e *Engine) Chat(ctx context.Context, agentID, prompt string, history []llm.Message) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		return "", fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}

	const sysPrompt = "你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"

	msgs := []llm.Message{{Role: "system", Content: sysPrompt}}
	// 透传历史(限制最近 20 条,避免超出上下文窗口)
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: prompt})

	resp, err := e.llmClient.Chat(ctx, llm.ChatRequest{
		Messages:    msgs,
		MaxTokens:   1024,
		Temperature: 0.7,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

// CreateAgent 注册一个 Agent 实例。若调用方未提供 ID/状态,此处补全默认值。
func (e *Engine) CreateAgent(ctx context.Context, a *model.AgentInstance) error {
	if a == nil {
		return fmt.Errorf("agent is nil")
	}
	if a.Name == "" {
		return fmt.Errorf("agent name is required")
	}
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	if a.Status == "" {
		a.Status = "active"
	}
	e.mu.Lock()
	e.agents[a.ID] = a
	e.mu.Unlock()
	slog.Info("agent created", "id", a.ID, "name", a.Name, "model", a.ModelName)
	return nil
}

// ListAgents 返回所有已注册的 Agent(调用方负责租户过滤)。
func (e *Engine) ListAgents() []*model.AgentInstance {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*model.AgentInstance, 0, len(e.agents))
	for _, a := range e.agents {
		out = append(out, a)
	}
	return out
}

// GetAgent 按 ID 查询 Agent。
func (e *Engine) GetAgent(id string) (*model.AgentInstance, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.agents[id]
	return a, ok
}

// ListPlans 返回所有已记录的 ExecutionPlan(调用方负责租户过滤)。
func (e *Engine) ListPlans() []*model.ExecutionPlan {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*model.ExecutionPlan, 0, len(e.plans))
	for _, p := range e.plans {
		out = append(out, p)
	}
	return out
}

// ExecutePlan 编排执行一个处置计划。
// 任一步骤失败即中止(abort on first failure,fail-closed),Plan 状态置为 failed。
func (e *Engine) ExecutePlan(ctx context.Context, p *model.ExecutionPlan) error {
	if p == nil {
		return fmt.Errorf("plan is nil")
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.TenantID == "" {
		p.TenantID = "default"
	}

	// 按 Agent 维度限流:保护底层 LLM / 工具后端。
	if e.lim != nil && !e.lim.Allow(p.AgentID) {
		p.Status = "rejected"
		return fmt.Errorf("rate limit exceeded for agent %s", p.AgentID)
	}

	p.Status = "running"
	e.mu.Lock()
	e.plans[p.ID] = p
	e.mu.Unlock()

	slog.Info("plan execution started", "id", p.ID, "agent", p.AgentID, "steps", len(p.Steps), "risk", p.RiskLevel)

	var failed bool
	for i := range p.Steps {
		step := &p.Steps[i]
		step.Status = "running"
		out, err := e.runStep(ctx, step)
		if err != nil {
			step.Status = "failed"
			step.Error = err.Error()
			failed = true
			slog.Error("plan step failed", "plan", p.ID, "step", step.ID, "tool", step.Tool, "error", err)
			break
		}
		step.Status = "success"
		step.Output = out
	}

	if failed {
		p.Status = "failed"
	} else {
		p.Status = "completed"
	}
	slog.Info("plan execution finished", "id", p.ID, "status", p.Status)
	return nil
}

// runStep 执行单个步骤:有 Tool 走 MCP 工具,否则走 LLM 推理。
func (e *Engine) runStep(ctx context.Context, step *model.Step) (any, error) {
	if step.Tool != "" {
		input := normalizeInput(step.Input)
		return e.reg.Execute(ctx, step.Tool, input)
	}

	// 无 Tool:视为 LLM 推理步。Input 可携带 {"prompt": "..."}。
	prompt := extractPrompt(step.Input)
	if prompt == "" {
		return nil, fmt.Errorf("step has neither tool nor prompt")
	}
	resp, err := e.llmClient.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

// normalizeInput 将 Step.Input(any)规整为 map[string]any,供 reg.Execute 使用。
func normalizeInput(in any) map[string]any {
	switch v := in.(type) {
	case map[string]any:
		return v
	case []byte:
		m := map[string]any{}
		_ = json.Unmarshal(v, &m)
		return m
	case string:
		m := map[string]any{}
		_ = json.Unmarshal([]byte(v), &m)
		return m
	default:
		return map[string]any{}
	}
}

// extractPrompt 从 Input 中抽取 prompt 字段(若存在)。
func extractPrompt(in any) string {
	if m, ok := in.(map[string]any); ok {
		if p, ok := m["prompt"].(string); ok {
			return p
		}
	}
	return ""
}

func (e *Engine) agentCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.agents)
}

// ——— 热更新 ———

// UpdateClient 热更新底层 LLM 客户端(sysconfig 修改 LLM 配置后调用)。
func (e *Engine) UpdateClient(c *llm.Client) {
	e.mu.Lock()
	e.llmClient = c
	e.mu.Unlock()
	slog.Info("engine llm client updated")
}

// ——— 流式输出 ———

// ChatStream 非流式 Chat 的流式版本:逐 token 推送到返回的 channel。
// 调用方负责消费 channel 直到关闭; ctx 取消时后台收束。
func (e *Engine) ChatStream(ctx context.Context, agentID, prompt string, history []llm.Message) (<-chan string, error) {
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		return nil, fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}

	const sysPrompt = "你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"
	msgs := []llm.Message{{Role: "system", Content: sysPrompt}}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: prompt})

	chunkCh, err := e.llmClient.StreamChat(ctx, llm.ChatRequest{
		Messages:    msgs,
		MaxTokens:   1024,
		Temperature: 0.7,
	})
	if err != nil {
		return nil, err
	}
	out := make(chan string, 32)
	go func() {
		defer close(out)
		for c := range chunkCh {
			if c.Content != "" {
				out <- c.Content
			}
		}
	}()
	return out, nil
}

// ——— 对话持久(内存) ———

// Conversation 一次完整对话(多轮)。
type Conversation struct {
	ID        string       `json:"id"`
	TenantID  string       `json:"tenant_id"`
	AgentID   string       `json:"agent_id"`
	Title     string       `json:"title"`
	Messages  []llm.Message `json:"messages"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// SaveConversation 保存/更新对话。ID 为空时自动生成; title 取首条 user 消息前 40 字。
func (e *Engine) SaveConversation(ctx context.Context, conv *Conversation) error {
	if conv.TenantID == "" {
		conv.TenantID = "default"
	}
	now := time.Now()
	if conv.ID == "" {
		conv.ID = uuid.New().String()
		conv.CreatedAt = now
	}
	if conv.Title == "" && len(conv.Messages) > 0 {
		conv.Title = trunc(conv.Messages[0].Content, 40)
	}
	conv.UpdatedAt = now

	if e.convStore != nil {
		return e.convStore.Save(ctx, conv)
	}
	e.mu.Lock()
	e.convs[conv.ID] = conv
	e.mu.Unlock()
	return nil
}

// LoadConversations 按 tenant + agent 加载最近 n 条对话摘要(不含 Messages, 需按单条 GetConversation 取详情)。
func (e *Engine) LoadConversations(tenantID, agentID string, limit int) []*Conversation {
	if e.convStore != nil {
		convs, _ := e.convStore.List(context.Background(), tenantID, agentID, limit)
		return convs
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*Conversation
	for _, c := range e.convs {
		if c.TenantID != tenantID {
			continue
		}
		if agentID != "" && c.AgentID != agentID {
			continue
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// GetConversation 取单条对话详情(含 Messages)。
func (e *Engine) GetConversation(id string) (*Conversation, error) {
	if e.convStore != nil {
		c, err := e.convStore.Get(context.Background(), id)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, fmt.Errorf("conversation not found: %s", id)
		}
		return c, nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.convs[id]
	if !ok {
		return nil, fmt.Errorf("conversation not found: %s", id)
	}
	return c, nil
}

func trunc(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
