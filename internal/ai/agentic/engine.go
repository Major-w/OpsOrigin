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
	mu        sync.RWMutex
	agents    map[string]*model.AgentInstance
	plans     map[string]*model.ExecutionPlan
	convStore ConversationStore
	convs     map[string]*Conversation
	reg       *mcp.Registry
	lim       ratelimit.RateLimiter
	llmClient *llm.Client
	ready     bool
	tokenRecs []model.TokenRecord
	tokenMu   sync.Mutex
	semMu     sync.Mutex
	llmSem    chan struct{}

	// ——— 多智能体团队（Feature 1）———
	teams          map[string]*model.AgentTeam
	genMaxTokens   int
	genTemperature float64
	store          Store         // PG 持久化（可选,nil=仅内存）
	teamConvStore  TeamConvStore // 团队对话历史（可选,nil=无跨轮记忆）
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
		teams:     make(map[string]*model.AgentTeam),
		reg:       reg,
		lim:       lim,
		llmClient: llmClient,
		semMu:     sync.Mutex{},
		llmSem:    make(chan struct{}, 32),
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
//
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
	if e.store != nil {
		if err := e.loadFromStore(); err != nil {
			slog.Warn("failed to load agents from store", "err", err)
		}
	}
	e.mu.Unlock()
	slog.Info("agentic engine started",
		"agents", e.agentCount(),
		"tools", len(e.reg.List()))
	return nil
}

func (e *Engine) SetStore(s Store) { e.store = s }

func (e *Engine) loadFromStore() error {
	ctx := context.Background()
	agents, err := e.store.ListAgents(ctx, "default")
	if err != nil {
		return err
	}
	for _, a := range agents {
		e.agents[a.ID] = &model.AgentInstance{
			ID: a.ID, TenantID: a.TenantID, Name: a.Name,
			ModelName: a.ModelName, SystemPrompt: a.SystemPrompt,
			Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		}
	}
	teams, err := e.store.ListTeams(ctx, "default")
	if err != nil {
		return err
	}
	for _, t := range teams {
		e.teams[t.ID] = &model.AgentTeam{ID: t.ID, Name: t.Name, Mode: t.Mode, SystemPrompt: t.Prompt}
		if t.Members != nil {
			json.Unmarshal(t.Members, &e.teams[t.ID].Members)
		}
	}
	return nil
}

// Chat 执行一次单轮(可带历史)的 LLM 推理,供 AI 助手聊天端点使用。
// 支持 Function Calling:模型可自主调用已注册的运维工具(CMDB查询/工单/脚本执行/知识库检索)。
func (e *Engine) Chat(ctx context.Context, agentID, prompt string, history []llm.Message) (string, error) {
	out, _, _, err := e.chatWithTools(ctx, agentID, prompt, history, false)
	return out, err
}

// ChatReasoning 思维链模式:返回思考过程和最终答案。
// 优先返回模型原生 reasoning_content(DeepSeek-R1/Qwen 等),缺失时降级到 prompt 伪思维链。
func (e *Engine) ChatReasoning(ctx context.Context, agentID, prompt string, history []llm.Message) (thinking string, answer string, err error) {
	answer, reasoning, _, err := e.chatWithTools(ctx, agentID, prompt, history, true)
	if err != nil {
		return "", "", err
	}
	if reasoning != "" {
		return reasoning, answer, nil
	}
	thinking, answer = parseCoT(answer)
	return
}

// ToolInvocation 记录一次 Function Calling 循环中模型发起的工具调用及其结果。
type ToolInvocation struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Result    any            `json:"result"`
}

// ChatWithTools 执行带 Function Calling 的对话,返回最终答案与本次触发的所有工具调用。
// 供需要审计/展示工具使用情况的调用方使用(如团队编排后的汇总)。
func (e *Engine) ChatWithTools(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool) (string, []ToolInvocation, error) {
	out, _, inv, err := e.chatWithTools(ctx, agentID, prompt, history, reasoning)
	return out, inv, err
}

// chatWithTools 实现 Function Calling 循环:
//  1. 构造系统提示 + 历史 + 用户问题 + 工具定义,调用 LLM(非流式);
//  2. 若模型返回 tool_calls,逐个经 mcp.Registry 执行,把结果作为 tool 角色消息回填;
//  3. 回到步骤 1,直至模型不再请求工具(finish_reason=stop)或达到最大轮次。
//
// 返回 (最终答案, 原生推理内容, 工具调用记录, error)。原生推理内容(reasoning_content)
// 在模型支持时非空,否则为空(调用方降级到 prompt 伪思维链或忽略)。
//
// 内建工具 llm_chat / http_get 不暴露给模型,以避免自我递归/无副作用调用噪音。
func (e *Engine) chatWithTools(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool) (string, string, []ToolInvocation, error) {
	if prompt == "" {
		return "", "", nil, fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return "", "", nil, fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}

	// 1. 构造初始消息
	userPrompt := prompt
	if reasoning {
		userPrompt = "请按以下格式回答：\n\n<think>\n先进行分步推理分析\n</think>\n<answer>\n再给出最终答案\n</answer>\n\n用户问题：" + prompt
	}
	msgs := e.buildInitialMessages(agentID, userPrompt, history)

	tools := e.buildToolDefs()
	var invocations []ToolInvocation

	// 2. 工具调用循环
	for round := 0; round < maxToolRounds; round++ {
		if !e.acquireLLMSlot(ctx) {
			return "", "", invocations, fmt.Errorf("llm concurrency limit reached")
		}
		resp, err := e.llmClient.Chat(ctx, llm.ChatRequest{
			Messages:    msgs,
			Tools:       tools,
			MaxTokens:   e.genMaxTokensVal(),
			Temperature: e.genTemperatureVal(),
		})
		e.releaseLLMSlot()
		if err != nil {
			return "", "", invocations, err
		}
		if len(resp.Choices) == 0 {
			return "", "", invocations, fmt.Errorf("llm: empty response")
		}
		e.recordTokens(agentID, resp)

		choiceMsg := resp.Choices[0].Message
		// 把本次 assistant 消息(含 tool_calls)追加进上下文,供下一轮回填
		msgs = append(msgs, choiceMsg)

		// 无工具调用 -> 这是最终答案
		if len(choiceMsg.ToolCalls) == 0 {
			return choiceMsg.Content, choiceMsg.ReasoningContent, invocations, nil
		}

		// 执行每个工具调用
		for _, tc := range choiceMsg.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			AIToolCalls.Inc()
			slog.Info("ai function call", "tool", tc.Function.Name, "args", truncateJSON(args))
			result, execErr := e.reg.Execute(ctx, tc.Function.Name, args)
			if execErr != nil {
				result = map[string]any{"error": execErr.Error()}
				slog.Warn("ai tool execution failed", "tool", tc.Function.Name, "error", execErr)
			}
			invocations = append(invocations, ToolInvocation{
				Name:      tc.Function.Name,
				Arguments: args,
				Result:    result,
			})
			resBytes, _ := json.Marshal(result)
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				Content:    string(resBytes),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	return "", "", invocations, fmt.Errorf("function calling loop exceeded max rounds (%d)", maxToolRounds)
}

// buildInitialMessages 拼装 system + 历史(最近 20 条) + 用户问题。
func (e *Engine) buildInitialMessages(agentID, userPrompt string, history []llm.Message) []llm.Message {
	msgs := []llm.Message{{Role: "system", Content: e.resolveSystemPrompt(agentID)}}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: userPrompt})
	return msgs
}

// builtinToolDenyList 不被暴露给模型的内建工具(避免自我递归或噪音调用)。
var builtinToolDenyList = map[string]bool{
	"llm_chat": true,
	"http_get": true,
}

// buildToolDefs 将 Registry 中的运维工具转换为 LLM Function Calling 所需的 ToolDef。
// 跳过内建工具与未注册 Handler 的工具。
func (e *Engine) buildToolDefs() []llm.ToolDef {
	defs := e.reg.List()
	out := make([]llm.ToolDef, 0, len(defs))
	for _, d := range defs {
		if builtinToolDenyList[d.Name] {
			continue
		}
		props := map[string]any{}
		required := []string{}
		for pname, ps := range d.Parameters {
			prop := map[string]any{"type": ps.Type, "description": ps.Description}
			if ps.Enum != nil {
				prop["enum"] = ps.Enum
			}
			if ps.Default != nil {
				prop["default"] = ps.Default
			}
			props[pname] = prop
			if ps.Required {
				required = append(required, pname)
			}
		}
		out = append(out, llm.ToolDef{
			Type: "function",
			Function: llm.ToolFunc{
				Name:        d.Name,
				Description: d.Description,
				Parameters: map[string]any{
					"type":       "object",
					"properties": props,
					"required":   required,
				},
			},
		})
	}
	return out
}

// maxToolRounds 单次对话中 Function Calling 的最大轮次,防止模型无限循环请求工具。
const maxToolRounds = 5

// truncateJSON 安全地把任意参数序列化为单行摘要(用于日志)。
func truncateJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// summarizeToolResult 把工具结果压缩为单行摘要,供流式输出展示。
func summarizeToolResult(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

// persistTokenRecord 将一条 token 记录同时写入内存环形缓冲(便捷查询)与 PG(持久化统计)。
// 内存缓冲仅保留最近 2000 条,PG 为真实可靠来源;store 为 nil 时只留内存。
func (e *Engine) persistTokenRecord(r model.TokenRecord) {
	e.tokenMu.Lock()
	e.tokenRecs = append(e.tokenRecs, r)
	if len(e.tokenRecs) > 2000 {
		e.tokenRecs = e.tokenRecs[len(e.tokenRecs)-2000:]
	}
	e.tokenMu.Unlock()
	if e.store != nil {
		_ = e.store.SaveTokenRecord(context.Background(), &r)
	}
}

// recordTokens 记录本次 LLM 调用的 token 用量。
func (e *Engine) recordTokens(agentID string, resp *llm.ChatResponse) {
	if resp.Usage == nil {
		return
	}
	e.persistTokenRecord(model.TokenRecord{
		Time:       time.Now(),
		AgentID:    agentID,
		Model:      resp.Model,
		Prompt:     resp.Usage.PromptTokens,
		Completion: resp.Usage.CompletionTokens,
		Total:      resp.Usage.TotalTokens,
	})
}

// GetTokenStats 返回 token 用量统计。
// 优先从 PG(store)读取真实记录(避免内存重启丢失与重复计数),store 为 nil 时回退内存缓冲。
func (e *Engine) GetTokenStats(period string) *model.TokenStats {
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

	var recs []model.TokenRecord
	if e.store != nil {
		recs, _ = e.store.TokenRecordsSince(context.Background(), cutoff)
	} else {
		e.tokenMu.Lock()
		recs = make([]model.TokenRecord, len(e.tokenRecs))
		copy(recs, e.tokenRecs)
		e.tokenMu.Unlock()
	}

	return computeTokenStats(period, cutoff, recs)
}

// computeTokenStats 从记录切片聚合出统计。会按 cutoff 过滤(内存路径需自滤)。
func computeTokenStats(period string, cutoff time.Time, recs []model.TokenRecord) *model.TokenStats {
	stats := &model.TokenStats{Period: period}
	hourMap := map[string]*model.HourBucket{}
	dayMap := map[string]*model.DayBucket{}

	for _, r := range recs {
		if r.Time.Before(cutoff) {
			continue
		}
		stats.TotalCalls++
		stats.Prompt += r.Prompt
		stats.Completion += r.Completion
		stats.Total += r.Total

		hk := r.Time.Format("15:00")
		if _, ok := hourMap[hk]; !ok {
			hourMap[hk] = &model.HourBucket{Hour: hk}
		}
		hourMap[hk].Total += r.Total
		hourMap[hk].Calls++

		dk := r.Time.Format("01-02")
		if _, ok := dayMap[dk]; !ok {
			dayMap[dk] = &model.DayBucket{Date: dk}
		}
		dayMap[dk].Total += r.Total
		dayMap[dk].Calls++
	}

	for h := 0; h < 24; h++ {
		hk := fmt.Sprintf("%02d:00", h)
		if _, ok := hourMap[hk]; !ok {
			hourMap[hk] = &model.HourBucket{Hour: hk}
		}
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
	if e.store != nil {
		_ = e.store.CreateAgent(ctx, &AgentRecord{ID: a.ID, TenantID: a.TenantID, Name: a.Name, ModelName: a.ModelName, SystemPrompt: a.SystemPrompt, Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt})
	}
	slog.Info("agent created", "id", a.ID, "name", a.Name, "model", a.ModelName)
	return nil
}

// UpdateAgent 更新 Agent 的 system_prompt, model 等字段。
func (e *Engine) UpdateAgent(ctx context.Context, a *model.AgentInstance) error {
	if a.ID == "" {
		return fmt.Errorf("agent id is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, ok := e.agents[a.ID]
	if !ok {
		return fmt.Errorf("agent not found: %s", a.ID)
	}
	if a.Name != "" {
		existing.Name = a.Name
	}
	if a.ModelName != "" {
		existing.ModelName = a.ModelName
	}
	existing.SystemPrompt = a.SystemPrompt
	existing.UpdatedAt = time.Now()
	if e.store != nil {
		_ = e.store.UpdateAgent(ctx, &AgentRecord{ID: existing.ID, TenantID: existing.TenantID, Name: existing.Name, ModelName: existing.ModelName, SystemPrompt: existing.SystemPrompt, Status: existing.Status, UpdatedAt: existing.UpdatedAt})
	}
	slog.Info("agent updated", "id", a.ID, "name", existing.Name)
	return nil
}

// DeleteAgent 删除 Agent。
func (e *Engine) DeleteAgent(ctx context.Context, id, tenantID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.agents[id]
	if !ok {
		return fmt.Errorf("agent not found: %s", id)
	}
	delete(e.agents, id)
	if e.store != nil {
		_ = e.store.DeleteAgent(ctx, id, tenantID)
	}
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
	if e.store != nil {
		membersJSON, _ := json.Marshal(t.Members)
		_ = e.store.CreateTeam(ctx, &TeamRecord{ID: t.ID, TenantID: t.TenantID, Name: t.Name, Members: membersJSON, Mode: t.Mode, Prompt: t.SystemPrompt, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt})
	}
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
	if e.store != nil {
		membersJSON, _ := json.Marshal(existing.Members)
		_ = e.store.UpdateTeam(ctx, &TeamRecord{ID: existing.ID, TenantID: existing.TenantID, Name: existing.Name, Members: membersJSON, Mode: existing.Mode, Prompt: existing.SystemPrompt, UpdatedAt: existing.UpdatedAt})
	}
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
	if e.store != nil {
		_ = e.store.DeleteTeam(ctx, id, tenantID)
	}
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

// chatStreamImpl 在后台 goroutine 中执行 Function Calling 循环,并把过程与最终答案
// 以文本块形式推送到 channel,使 SSE 流既能展示工具调用又保留流式观感。
// 工具活动以可读文本(🔧 调用 / ↳ 结果)呈现,最终答案按词切分模拟流式输出。
func (e *Engine) chatStreamImpl(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool) (<-chan string, error) {
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return nil, fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}

	out := make(chan string, 32)
	go func() {
		defer close(out)

		final, reasoning, invocations, err := e.chatWithTools(ctx, agentID, prompt, history, reasoning)
		if err != nil {
			out <- "⚠️ " + err.Error()
			return
		}

		// 展示工具调用过程
		for _, inv := range invocations {
			out <- fmt.Sprintf("\n\n🔧 **调用工具 `%s`**\n", inv.Name)
			if len(inv.Arguments) > 0 {
				if b, e := json.MarshalIndent(inv.Arguments, "", "  "); e == nil {
					out <- "```json\n" + string(b) + "\n```\n"
				}
			}
			out <- fmt.Sprintf("\n↳ **结果**: %s\n\n", summarizeToolResult(inv.Result))
		}

		// 原生推理内容(若模型提供)作为独立块展示
		if reasoning != "" {
			out <- "\n\n> 💭 **推理过程**\n\n" + reasoning + "\n\n"
		}

		// 流式输出最终答案(按词切分,模拟 token 流)
		streamWords(out, final)
	}()
	return out, nil
}

// streamWords 将文本按空白切分,逐词推入 channel 以模拟流式输出。
func streamWords(out chan<- string, text string) {
	for _, w := range strings.Fields(text) {
		out <- w + " "
	}
}

// ——— 对话持久(内存) ———

// Conversation 一次完整对话(多轮)。
type Conversation struct {
	ID        string        `json:"id"`
	TenantID  string        `json:"tenant_id"`
	AgentID   string        `json:"agent_id"`
	Title     string        `json:"title"`
	Messages  []llm.Message `json:"messages"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
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
		thinking = strings.TrimSpace(full[thinkStart+len("<think>") : thinkEnd])
	}
	if answerStart >= 0 && answerEnd > answerStart {
		answer = strings.TrimSpace(full[answerStart+len("<answer>") : answerEnd])
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
