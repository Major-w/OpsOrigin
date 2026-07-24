package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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
	convStore   ConversationStore
	convs       map[string]*Conversation
	reg         *mcp.Registry
	lim         ratelimit.RateLimiter
	llmClient   *llm.Client
	ready       bool
	tokenRecs   []model.TokenRecord
	tokenMu     sync.Mutex
	semMu       sync.Mutex
	llmSem      chan struct{}

	// ——— 多智能体团队（Feature 1）———
	teams          map[string]*model.AgentTeam
	genMaxTokens   int     // 生成参数（可热更新）；0 表示使用默认值
	genTemperature float64 // 0 表示使用默认值
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
		semMu:     sync.Mutex{},
		llmSem:    make(chan struct{}, 32), // F4-C 决策 C: 物理 LLM 并发上限, 默认 32
		teams:     make(map[string]*model.AgentTeam),
	}
	e.registerBuiltinTools()
	SeedPresetTeams(e) // Feature 1: 预置平台交付团队（按 Name 幂等）
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
	return e.chatImpl(ctx, agentID, prompt, history, false)
}

// ChatReasoning 思维链模式:返回思考过程和最终答案。
func (e *Engine) ChatReasoning(ctx context.Context, agentID, prompt string, history []llm.Message) (thinking string, answer string, err error) {
	full, err := e.chatImpl(ctx, agentID, prompt, history, true)
	if err != nil {
		return "", "", err
	}
	thinking, answer = parseCoT(full)
	return
}

func (e *Engine) chatImpl(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return "", fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}

	msgs := []llm.Message{{Role: "system", Content: e.resolveSystemPrompt(agentID)}}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	msgs = append(msgs, history...)

	userPrompt := prompt
	if reasoning {
		userPrompt = "请按以下格式回答：\n\n<think>\n先进行分步推理分析\n</think>\n<answer>\n再给出最终答案\n</answer>\n\n用户问题：" + prompt
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: userPrompt})

	if !e.acquireLLMSlot(ctx) {
		return "", fmt.Errorf("llm concurrency limit reached")
	}
	defer e.releaseLLMSlot()

	resp, err := e.llmClient.Chat(ctx, llm.ChatRequest{
		Messages:    msgs,
		MaxTokens:   e.genMaxTokensVal(),
		Temperature: e.genTemperatureVal(),
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	e.recordTokens(agentID, resp)
	return resp.Choices[0].Message.Content, nil
}

// recordTokens 记录本次 LLM 调用的 token 用量。
func (e *Engine) recordTokens(agentID string, resp *llm.ChatResponse) {
	if resp.Usage == nil { return }
	e.tokenMu.Lock()
	e.tokenRecs = append(e.tokenRecs, model.TokenRecord{
		Time:       time.Now(),
		AgentID:    agentID,
		Model:      resp.Model,
		Prompt:     resp.Usage.PromptTokens,
		Completion: resp.Usage.CompletionTokens,
		Total:      resp.Usage.TotalTokens,
	})
	if len(e.tokenRecs) > 2000 {
		e.tokenRecs = e.tokenRecs[len(e.tokenRecs)-2000:]
	}
	e.tokenMu.Unlock()
}

// GetTokenStats 返回 token 用量统计。
func (e *Engine) GetTokenStats(period string) *model.TokenStats {
	e.tokenMu.Lock()
	recs := make([]model.TokenRecord, len(e.tokenRecs))
	copy(recs, e.tokenRecs)
	e.tokenMu.Unlock()

	now := time.Now()
	var cutoff time.Time
	switch period {
	case "today":
		y, m, d := now.Date()
		cutoff = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	case "24h":
		cutoff = now.Add(-24 * time.Hour)
	case "7d":
		cutoff = now.Add(-7 * 24 * time.Hour)
	default:
		cutoff = now.Add(-24 * time.Hour)
	}

	stats := &model.TokenStats{Period: period}
	hourMap := map[string]*model.HourBucket{}
	dayMap := map[string]*model.DayBucket{}

	for _, r := range recs {
		if r.Time.Before(cutoff) { continue }
		stats.TotalCalls++
		stats.Prompt += r.Prompt
		stats.Completion += r.Completion
		stats.Total += r.Total

		hk := r.Time.Format("15:00")
		if _, ok := hourMap[hk]; !ok { hourMap[hk] = &model.HourBucket{Hour: hk} }
		hourMap[hk].Total += r.Total
		hourMap[hk].Calls++

		dk := r.Time.Format("01-02")
		if _, ok := dayMap[dk]; !ok { dayMap[dk] = &model.DayBucket{Date: dk} }
		dayMap[dk].Total += r.Total
		dayMap[dk].Calls++
	}

	for h := 0; h < 24; h++ {
		hk := fmt.Sprintf("%02d:00", h)
		if _, ok := hourMap[hk]; !ok { hourMap[hk] = &model.HourBucket{Hour: hk} }
		stats.ByHour = append(stats.ByHour, *hourMap[hk])
	}
	for _, d := range dayMap {
		stats.ByDay = append(stats.ByDay, *d)
	}
	return stats
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

// UpdateAgent 更新 Agent 的 system_prompt, model 等字段。
func (e *Engine) UpdateAgent(ctx context.Context, a *model.AgentInstance) error {
	if a.ID == "" { return fmt.Errorf("agent id is required") }
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, ok := e.agents[a.ID]
	if !ok { return fmt.Errorf("agent not found: %s", a.ID) }
	if a.Name != "" { existing.Name = a.Name }
	if a.ModelName != "" { existing.ModelName = a.ModelName }
	existing.SystemPrompt = a.SystemPrompt
	existing.UpdatedAt = time.Now()
	slog.Info("agent updated", "id", a.ID, "name", existing.Name)
	return nil
}

// DeleteAgent 删除 Agent。
func (e *Engine) DeleteAgent(ctx context.Context, id, tenantID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.agents[id]
	if !ok { return fmt.Errorf("agent not found: %s", id) }
	delete(e.agents, id)
	slog.Info("agent deleted", "id", id)
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
	if !e.acquireLLMSlot(ctx) {
		return nil, fmt.Errorf("llm concurrency limit reached")
	}
	defer e.releaseLLMSlot()
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

// resolveSystemPrompt 按 agentID 查找 Agent 的 system_prompt, 未设时返回默认值。
func (e *Engine) resolveSystemPrompt(agentID string) string {
	if agentID != "" {
		e.mu.RLock()
		a, ok := e.agents[agentID]
		e.mu.RUnlock()
		if ok && a.SystemPrompt != "" {
			return a.SystemPrompt
		}
	}
	return "你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"
}

// ——— 热更新 ———

// UpdateClient 热更新底层 LLM 客户端(sysconfig 修改 LLM 配置后调用)。
func (e *Engine) UpdateClient(c *llm.Client) {
	e.mu.Lock()
	e.llmClient = c
	e.mu.Unlock()
	slog.Info("engine llm client updated")
}

// ——— LLM 并发信号量(决策 C) ———
// 逻辑会话(如 100 路 SSE)与物理 LLM 调用解耦: 前者只受会话数约束,
// 后者受 llmSem 容量约束(默认 32), 防止底层 LLM 后端被瞬时打爆。

// SetLLMMaxInflight 设置物理 LLM 调用的最大并发槽位数。
func (e *Engine) SetLLMMaxInflight(n int) {
	if n < 1 {
		n = 1
	}
	e.semMu.Lock()
	e.llmSem = make(chan struct{}, n)
	e.semMu.Unlock()
	slog.Info("engine llm max-inflight updated", "max", n)
}

// acquireLLMSlot 抢占一个物理 LLM 调用槽位; ctx 取消时返回 false。
func (e *Engine) acquireLLMSlot(ctx context.Context) bool {
	e.semMu.Lock()
	ch := e.llmSem
	e.semMu.Unlock()
	select {
	case ch <- struct{}{}:
		AILLMInFlight.Inc()
		return true
	case <-ctx.Done():
		return false
	}
}

// releaseLLMSlot 释放一个物理 LLM 调用槽位。
func (e *Engine) releaseLLMSlot() {
	e.semMu.Lock()
	ch := e.llmSem
	e.semMu.Unlock()
	select {
	case <-ch:
		AILLMInFlight.Dec()
	default:
	}
}

// ——— 生成参数热更新（Feature 1）———
// SetGenParams 设置默认生成参数（maxTokens / temperature），0 表示保持引擎内置默认。
func (e *Engine) SetGenParams(maxTokens int, temperature float64) {
	e.mu.Lock()
	e.genMaxTokens = maxTokens
	e.genTemperature = temperature
	e.mu.Unlock()
	slog.Info("engine gen params updated", "max_tokens", maxTokens, "temperature", temperature)
}

// genMaxTokensVal 返回生效的 max_tokens（0 时用引擎默认 1024）。
func (e *Engine) genMaxTokensVal() int {
	if e.genMaxTokens > 0 {
		return e.genMaxTokens
	}
	return 1024
}

// genTemperatureVal 返回生效的 temperature（0 时用引擎默认 0.7）。
func (e *Engine) genTemperatureVal() float64 {
	if e.genTemperature > 0 {
		return e.genTemperature
	}
	return 0.7
}

// ——— 多智能体团队 CRUD（Feature 1）———

// CreateTeam 创建团队。ID/时间戳由引擎补全；名称必填；至少含一名成员。
func (e *Engine) CreateTeam(ctx context.Context, t *model.AgentTeam) error {
	if t == nil {
		return fmt.Errorf("team is nil")
	}
	if t.Name == "" {
		return fmt.Errorf("team name is required")
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team must have at least one member")
	}
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Mode == "" {
		t.Mode = model.TeamModeSupervisor
	}
	e.mu.Lock()
	e.teams[t.ID] = t
	e.mu.Unlock()
	slog.Info("team created", "id", t.ID, "name", t.Name, "mode", t.Mode, "members", len(t.Members))
	return nil
}

// UpdateTeam 更新团队（名称 / 描述 / 模式 / 成员 / 团队提示词）。
func (e *Engine) UpdateTeam(ctx context.Context, t *model.AgentTeam) error {
	if t.ID == "" {
		return fmt.Errorf("team id is required")
	}
	if t.Name == "" {
		return fmt.Errorf("team name is required")
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team must have at least one member")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, ok := e.teams[t.ID]
	if !ok {
		return fmt.Errorf("team not found: %s", t.ID)
	}
	existing.Name = t.Name
	existing.Description = t.Description
	existing.Mode = t.Mode
	existing.Members = t.Members
	existing.SystemPrompt = t.SystemPrompt
	existing.UpdatedAt = time.Now()
	slog.Info("team updated", "id", t.ID, "name", existing.Name)
	return nil
}

// DeleteTeam 删除团队。
func (e *Engine) DeleteTeam(ctx context.Context, id, tenantID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.teams[id]
	if !ok {
		return fmt.Errorf("team not found: %s", id)
	}
	if tenantID != "" && t.TenantID != tenantID {
		return fmt.Errorf("team not found: %s", id)
	}
	delete(e.teams, id)
	slog.Info("team deleted", "id", id)
	return nil
}

// GetTeam 按 ID 查询团队。
func (e *Engine) GetTeam(id string) (*model.AgentTeam, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.teams[id]
	return t, ok
}

// ListTeams 返回指定租户的团队（tenantID 为空返回全部）。
func (e *Engine) ListTeams(tenantID string) []*model.AgentTeam {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*model.AgentTeam, 0, len(e.teams))
	for _, t := range e.teams {
		if tenantID != "" && t.TenantID != tenantID {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ——— 流式输出 ———

// ChatStream 非流式 Chat 的流式版本:逐 token 推送到返回的 channel。
// 调用方负责消费 channel 直到关闭; ctx 取消时后台收束。
func (e *Engine) ChatStream(ctx context.Context, agentID, prompt string, history []llm.Message) (<-chan string, error) {
	return e.chatStreamImpl(ctx, agentID, prompt, history, false)
}

func (e *Engine) chatStreamImpl(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool) (<-chan string, error) {
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return nil, fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}

	sysPrompt := e.resolveSystemPrompt(agentID)
	msgs := []llm.Message{{Role: "system", Content: sysPrompt}}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	msgs = append(msgs, history...)
	userPrompt := prompt
	if reasoning {
		userPrompt = "请按以下格式回答：\n\n<think>\n先进行分步推理分析\n</think>\n<answer>\n再给出最终答案\n</answer>\n\n用户问题：" + prompt
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: userPrompt})

	if !e.acquireLLMSlot(ctx) {
		return nil, fmt.Errorf("llm concurrency limit reached")
	}
	chunkCh, err := e.llmClient.StreamChat(ctx, llm.ChatRequest{
		Messages:    msgs,
		MaxTokens:   e.genMaxTokensVal(),
		Temperature: e.genTemperatureVal(),
	})
	if err != nil {
		e.releaseLLMSlot()
		return nil, err
	}
	out := make(chan string, 32)
	go func() {
		defer close(out)
		defer e.releaseLLMSlot()
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

// parseCoT 从 CoT 格式的回复中提取思考过程和最终答案。
func parseCoT(full string) (thinking string, answer string) {
	// 尝试解析 <think>...</think> 和 <answer>...</answer>
	thinkStart := strings.Index(full, "<think>")
	thinkEnd := strings.Index(full, "</think>")
	answerStart := strings.Index(full, "<answer>")
	answerEnd := strings.Index(full, "</answer>")

	if thinkStart >= 0 && thinkEnd > thinkStart {
		thinking = strings.TrimSpace(full[thinkStart+len("<think>"): thinkEnd])
	}
	if answerStart >= 0 && answerEnd > answerStart {
		answer = strings.TrimSpace(full[answerStart+len("<answer>"): answerEnd])
	}
	if answer == "" {
		// 没有 <answer> 标签: 把 <think> 之外的部分当做答案
		if thinkEnd > 0 && thinkEnd < len(full) {
			answer = strings.TrimSpace(full[thinkEnd+len("</think>"):])
		} else {
			answer = full
		}
	}
	return
}

func trunc(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
