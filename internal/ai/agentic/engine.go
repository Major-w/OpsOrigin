package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/llm"
)

// StreamChunk SSE 流式事件单元。用于区分思考过程(token 或 thinking)与工具标注(tool/error),
// 使前端可据此分别渲染折叠式思维链、答案正文和工具调用面板。
type StreamChunk struct {
	Type    string // "thinking": 推理过程; "token": 正常回答; "tool": 工具调用标注; "error": 异常
	Content string
}

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
	llmClient atomic.Pointer[llm.Client] // 热更新场景下的并发安全读写(避免 data race)
	// router 多模型路由池(P3, 可选): 非 nil 时聊天/流式调用走路由(权重+健康+失败降级),
	// nil 时回退到 llmClient 单实例(行为与以往一致)。
	router *ModelRouter
	ready     bool
	tokenRecs []model.TokenRecord
	tokenMu   sync.Mutex
	semMu     sync.Mutex
	llmSem    chan struct{}

	// defaultSystemPrompt 是未配置 Agent 级 system_prompt 时的全局默认提示词(外置化,见 SetDefaultSystemPrompt)。
	defaultSystemPrompt string

	// toolCache 工具结果缓存(name+tenant+args -> 结果), 跨请求复用, 降低重复 LLM 工具往返成本。
	// 非幂等/有副作用工具(script_run/ticket_create)与内建工具不缓存(见 noCacheTools)。
	toolCache *ttlCache

	// historySummarizer 可选: 用 LLM 将超长历史的较早部分压缩为摘要,显著降低 Token 消耗。
	// 未设置时 compactHistory 回退到确定性压缩(保留首条 user 原始意图 + 最近窗口)。
	historySummarizer func(ctx context.Context, msgs []llm.Message) (string, error)

	// ——— 多智能体团队（Feature 1）———
	teams          map[string]*model.AgentTeam
	genMaxTokens   int
	genTemperature float64
	store          Store         // PG 持久化（可选,nil=仅内存）
	teamConvStore  TeamConvStore // 团队对话历史（可选,nil=无跨轮记忆）

	// ——— 主动巡检 Agent（P0）———
	inspections    map[string]*Inspection
	inspectionRuns map[string][]*InspectionRun
	inspectCancel  context.CancelFunc

	// CreateTicketFunc 由 cmd/core 注入的 ITSM 工单创建回调(nil=未装配, 巡检发现转工单不可用)。
	// 返回新建工单 ID; 错误时 EscalateInspectionRun 记录失败并跳过该发现。
	CreateTicketFunc func(ctx context.Context, tenantID, title, description string, labels map[string]string) (string, error)
	// InspectionNotifyFunc 由 cmd/core 注入的通知回调(nil=不通知), 巡检完成/失败后调用。
	InspectionNotifyFunc func(ctx context.Context, tenantID, event, title, content string) error
	// TicketMetricsProvider 由 cmd/core 注入的 ITSM 工单统计回调(nil=运营效果页不显示工单指标)。
	// 返回: 总数、按状态分布、平均处置时长(分钟)、处置效果评估分布(confirmed/false_positive)。
	TicketMetricsProvider func(ctx context.Context, tenantID string) (int, map[string]int, float64, map[string]int, error)
	// CreateCatalogItemFunc 由 cmd/core 注入的 ITSM 标准化工单目录创建回调(nil=场景包不自动建目录项)。
	// 场景启用时可选自动将推荐工单类型落库到 itsm_catalog, 打通"场景→处置标准"。
	CreateCatalogItemFunc func(ctx context.Context, tenantID, name, description, category string) error

	// ——— 敏感操作审批 (PRD 5.2.5) ———
	approvals *ApprovalManager
	// approvalPolicies 全局默认 + Agent 级覆盖策略: "default" 键为全局, 其余键为 agent_id。
	approvalPolicies map[string]ApprovalPolicy

	// ——— 自定义 MCP 工具 (PRD 5.2.2) ———
	// customTools 用户注册的自定义工具(ID -> 定义), enabled 的工具动态注册到 reg。
	customTools map[string]*CustomTool
}

// NewEngine 构造引擎并注册内建工具。
// reg 中的业务工具应由调用方在 NewEngine 之后(或之前)通过 reg.Register 注入;
// 此处仅注册与引擎强耦合的、安全的内建工具。
func NewEngine(reg *mcp.Registry, lim ratelimit.RateLimiter, llmClient *llm.Client, store ConversationStore) *Engine {
	e := &Engine{
		agents:              make(map[string]*model.AgentInstance),
		plans:               make(map[string]*model.ExecutionPlan),
		convStore:           store,
		convs:               make(map[string]*Conversation),
		teams:               make(map[string]*model.AgentTeam),
		inspections:         make(map[string]*Inspection),
		inspectionRuns:      make(map[string][]*InspectionRun),
		reg:                 reg,
		lim:                 lim,
		semMu:               sync.Mutex{},
		llmSem:              make(chan struct{}, 32),
		defaultSystemPrompt: DefaultSystemPrompt,
		toolCache:           newTTLCache(toolCacheTTL, 2048),
	}
	e.llmClient.Store(llmClient)
	e.registerBuiltinTools()
	SeedPresetTeams(e) // Feature 1: 预置平台交付团队（按 Name 幂等）
	// 敏感操作审批: 全局默认"仅敏感询问", 可由 API/管理员调整为每次询问或始终允许。
	e.approvals = NewApprovalManager()
	e.approvalPolicies = map[string]ApprovalPolicy{
		"default": DefaultApprovalPolicy(),
	}
	// 自定义 MCP 工具(启动后由 Start/SyncCustomTools 从存储加载并注册)。
	e.customTools = make(map[string]*CustomTool)
	// 接入内置 LLM 历史摘要器(Token 预算压缩的"智能摘要"路径); 仅在客户端就绪时启用,
	// 否则 compactHistory 自动回退到确定性压缩(保留原始意图 + 最近窗口)。
	if llmClient != nil {
		e.SetHistorySummarizer(e.defaultSummarize)
	}
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
			resp, err := e.chatLLM(ctx, llm.ChatRequest{
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

// SetModelRouter 注入多模型路由池(P3, 可选)。设置后聊天/流式调用走路由
// (权重+健康状态+失败降级), 否则回退到单 llmClient。
func (e *Engine) SetModelRouter(r *ModelRouter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.router = r
}

// chatLLM 聊天调用统一入口: router 优先(多模型路由+降级), 否则单 client。
func (e *Engine) chatLLM(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if e.router != nil {
		return e.router.Chat(ctx, req)
	}
	return e.llmClient.Load().Chat(ctx, req)
}

// streamLLM 流式调用统一入口: router 优先, 否则单 client。
func (e *Engine) streamLLM(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if e.router != nil {
		return e.router.StreamChat(ctx, req)
	}
	return e.llmClient.Load().StreamChat(ctx, req)
}

// GetModels 返回模型池状态(供 /api/v1/ai/models 展示)。
// 未配置 router 时返回主 provider 单条信息。
func (e *Engine) GetModels() []ModelStatus {
	e.mu.RLock()
	r := e.router
	lc := e.llmClient.Load()
	e.mu.RUnlock()
	if r != nil {
		return r.Models()
	}
	if lc == nil {
		return []ModelStatus{}
	}
	return []ModelStatus{{Name: "default", Model: "deepseek-chat", Healthy: true}}
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
	// 加载并注册自定义 MCP 工具(PRD 5.2.2)
	if err := e.SyncCustomTools(context.Background()); err != nil {
		slog.Warn("failed to sync custom tools", "err", err)
	}
	// 主动巡检调度器(P0): 启动进程内定时循环, 扫描到期启用的巡检任务。
	e.StartInspectionScheduler()
	slog.Info("agentic engine started",
		"agents", e.agentCount(),
		"tools", len(e.reg.List()))
	return nil
}

// Stop 停止引擎后台活动: 取消巡检调度器循环, 供测试 t.Cleanup 与优雅退出复用。
// 幂等(未 Start 或已 Stop 均安全); 不销毁内存数据(进程退出即释放)。
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.inspectCancel
	e.inspectCancel = nil
	e.ready = false
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		slog.Info("agentic engine stopped")
	}
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
			KnowledgeCategories: a.KnowledgeCategories,
			Tools:               a.Tools,
			Skills:              a.Skills,
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
	// 主动巡检任务(P0): 恢复定义与历史运行记录, 保证重启后排程不丢。
	inspections, err := e.store.ListInspections(ctx, "")
	if err != nil {
		slog.Warn("failed to load inspections", "err", err)
		return nil
	}
	for _, in := range inspections {
		// 重启后若已过期(NextRunAt 落在过去), 顺延一个周期避免启动瞬间雪崩式补跑。
		if in.Enabled && !in.NextRunAt.After(time.Now()) {
			in.NextRunAt = e.nextRunAt(in.Schedule, time.Now())
		}
		e.inspections[in.ID] = in
		if runs, err := e.store.ListInspectionRuns(ctx, in.ID, in.TenantID); err == nil {
			// store 按时间倒序返回, 内存中统一保持正序(追加语义)。
			for i := len(runs) - 1; i >= 0; i-- {
				e.inspectionRuns[in.ID] = append(e.inspectionRuns[in.ID], runs[i])
			}
			if len(runs) > 0 {
				slog.Info("restored inspection runs", "inspection", in.ID, "tenant", in.TenantID, "runs", len(runs))
			}
		} else {
			slog.Warn("failed to restore inspection runs", "inspection", in.ID, "tenant", in.TenantID, "err", err)
		}
	}
	return nil
}

// Chat 执行一次单轮(可带历史)的 LLM 推理,供 AI 助手聊天端点使用。
// 支持 Function Calling:模型可自主调用已注册的运维工具(CMDB查询/工单/脚本执行/知识库检索)。
func (e *Engine) Chat(ctx context.Context, agentID, prompt string, history []llm.Message, conversationID string) (content string, convID string, err error) {
	content, _, _, convID, err = e.chatWithToolsConv(ctx, agentID, prompt, history, false, conversationID)
	return
}

// ChatReasoning 思维链模式:返回思考过程和最终答案。
// 优先返回模型原生 reasoning_content(DeepSeek-R1/Qwen 等),缺失时降级到 prompt 伪思维链。
func (e *Engine) ChatReasoning(ctx context.Context, agentID, prompt string, history []llm.Message, conversationID string) (thinking string, answer string, convID string, err error) {
	answer, reasoning, _, convID, err := e.chatWithToolsConv(ctx, agentID, prompt, history, true, conversationID)
	if err != nil {
		return "", "", "", err
	}
	if reasoning != "" {
		return reasoning, answer, convID, nil
	}
	thinking, answer = parseCoT(answer)
	return thinking, answer, convID, nil
}

// ToolInvocation 记录一次 Function Calling 循环中模型发起的工具调用及其结果。
type ToolInvocation struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Result    any            `json:"result"`
}

// ChatWithTools 执行带 Function Calling 的对话,返回最终答案与本次触发的所有工具调用。
// 供需要审计/展示工具使用情况的调用方使用(如团队编排后的汇总)。
func (e *Engine) ChatWithTools(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID string) (out string, inv []ToolInvocation, convID string, err error) {
	out, _, inv, convID, err = e.chatWithToolsConv(ctx, agentID, prompt, history, reasoning, conversationID)
	return out, inv, convID, err
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
// chatWithToolsConv 实现 Function Calling 循环, 并支持会话持久化(单智能体跨轮记忆):
//   - conversationID 非空时, 优先从服务端加载可信历史(含 tool 消息)替代客户端 history;
//   - 对话成功后把本轮回合(用户问题 + assistant + tool 结果)增量追加回该会话并落库。
//
// 返回 (最终答案, 原生推理内容, 工具调用记录, 会话ID, error)。
func (e *Engine) chatWithToolsConv(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID string) (string, string, []ToolInvocation, string, error) {
	if prompt == "" {
		return "", "", nil, "", fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return "", "", nil, "", fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}
	// 把 AI agent 身份写入上下文, 工具执行链可据此读取 Agent 级审批策略
	ctx = withAgentID(ctx, agentID)

	// 会话加载: 提供 conversationID 时优先用服务端持久历史(可信, 含 tool 消息), 否则用客户端 history。
	var conv *Conversation
	baseHistory := history
	if conversationID != "" {
		if c, e2 := e.GetConversation(conversationID); e2 == nil {
			conv = c
			baseHistory = c.Messages
		} else if strings.Contains(e2.Error(), "not found") {
			conv = &Conversation{ID: conversationID, AgentID: agentID, TenantID: tenantOf(ctx)}
		}
	}

	userPrompt := prompt
	if reasoning {
		userPrompt = "请按以下格式回答：\n\n<think>\n先进行分步推理分析\n</think>\n<answer>\n再给出最终答案\n</answer>\n\n用户问题：" + prompt
	}
	// 从客户端历史提取 service 层注入的 system 补充(仅 RAG 上下文; 客户端 system 已被 SanitizeHistory 剥离),
	// 避免续接会话时丢失知识库检索结果。服务端会话历史(baseHistory)不含 system。
	var sysExtra string
	for _, m := range history {
		if m.Role == "system" {
			if sysExtra != "" {
				sysExtra += "\n\n"
			}
			sysExtra += m.Content
		}
	}
	msgs, roundStart := e.buildMessages(ctx, agentID, userPrompt, baseHistory, sysExtra)
	tools := e.buildToolDefs(agentID)
	var invocations []ToolInvocation

	var content, reasoningAcc string
	var completed bool
	var err error
	for round := 0; round < maxToolRounds; round++ {
		slot, ok := e.acquireLLMSlot(ctx)
		if !ok {
			err = fmt.Errorf("llm concurrency limit reached")
			break
		}
		resp, cerr := e.chatLLM(ctx, llm.ChatRequest{
			Model:       e.modelForAgent(agentID),
			Messages:    msgs,
			Tools:       tools,
			MaxTokens:   e.genMaxTokensVal(),
			Temperature: e.genTemperatureVal(),
		})
		e.releaseLLMSlot(slot)
		if cerr != nil {
			err = cerr
			break
		}
		if len(resp.Choices) == 0 {
			err = fmt.Errorf("llm: empty response")
			break
		}
		e.recordTokens(agentID, resp)

		choiceMsg := resp.Choices[0].Message
		msgs = append(msgs, choiceMsg)

		if len(choiceMsg.ToolCalls) == 0 {
			content = choiceMsg.Content
			reasoningAcc = choiceMsg.ReasoningContent
			completed = true
			break
		}

		// 执行每个工具调用(经 execToolChecked 做 RBAC/限流/截断)
		for _, tc := range choiceMsg.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			AIToolCalls.Inc()
			result, _ := e.execToolChecked(ctx, tc)
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
	if !completed {
		err = fmt.Errorf("function calling loop exceeded max rounds (%d)", maxToolRounds)
	}

	// 持久化本轮(仅成功且 conv 已加载/新建时)
	if conv != nil && err == nil {
		roundMsgs := msgs[roundStart:]
		conv.Messages = append(conv.Messages, roundMsgs...)
		conv.AgentID = agentID
		if conv.TenantID == "" {
			conv.TenantID = tenantOf(ctx)
		}
		if serr := e.SaveConversation(ctx, conv); serr != nil {
			slog.Warn("ai: save conversation failed", "err", serr)
		}
		conversationID = conv.ID
	}
	return content, reasoningAcc, invocations, conversationID, err
}

// buildMessages 拼装 system(+可选 sysExtra 补充, 如 RAG 注入) + 历史(经 Token 预算压缩) + 用户问题,
// 并返回"用户消息之后的起始下标"(roundStart), 调用方可用 msgs[roundStart:] 精确裁剪出"本轮回合",
// 便于增量持久化会话。
func (e *Engine) buildMessages(ctx context.Context, agentID, userPrompt string, history []llm.Message, sysExtra string) ([]llm.Message, int) {
	sys := e.resolveSystemPrompt(agentID)
	// 注入该 Agent 定义的 Skills(可复用能力指令, PRD 5.2.2)
	if s := e.renderSkills(agentID); s != "" {
		sys += "\n\n" + s
	}
	// 注入引用标注指引(RAG grounding): 让模型在引用知识库/工具结果时标注来源。
	sys += "\n\n" + citationGuidance
	if sysExtra != "" {
		sys += "\n\n" + sysExtra
	}
	msgs := []llm.Message{{Role: "system", Content: sys}}
	// Token 预算压缩: 超长历史压缩为"摘要(可选) + 最近窗口 + 原始意图锚点", 避免撑爆上下文。
	history = e.compactHistory(ctx, history)
	msgs = append(msgs, history...)
	// roundStart 指向"本轮用户消息"在 msgs 中的下标: 持久化时 msgs[roundStart:] 即本轮回合(user + assistant/tool),
	// 跳过 system 与历史, 避免重复累积。
	roundStart := len(msgs)
	msgs = append(msgs, llm.Message{Role: "user", Content: userPrompt})
	return msgs, roundStart
}

// renderSkills 将 Agent 定义的 Skills 渲染为 system prompt 片段(能力指令块)。
// 未定义技能或 agent 不存在时返回空串。
func (e *Engine) renderSkills(agentID string) string {
	a, ok := e.GetAgent(agentID)
	if !ok || len(a.Skills) == 0 {
		return ""
	}
	var parts []string
	for _, sk := range a.Skills {
		name := strings.TrimSpace(sk.Name)
		if name == "" {
			continue
		}
		block := "### 技能: " + name
		if d := strings.TrimSpace(sk.Description); d != "" {
			block += "\n用途: " + d
		}
		if ins := strings.TrimSpace(sk.Instructions); ins != "" {
			block += "\n使用说明: " + ins
		}
		parts = append(parts, block)
	}
	if len(parts) == 0 {
		return ""
	}
	return "【你可用的技能】\n" + strings.Join(parts, "\n\n")
}

// buildInitialMessages 兼容旧调用: 仅返回拼装后的消息切片(不暴露 roundStart/sysExtra)。
func (e *Engine) buildInitialMessages(ctx context.Context, agentID, userPrompt string, history []llm.Message) []llm.Message {
	msgs, _ := e.buildMessages(ctx, agentID, userPrompt, history, "")
	return msgs
}

// SetHistorySummarizer 设置可选的 LLM 历史摘要器(用于 Token 预算压缩的"智能摘要"路径)。
// 传入 nil 关闭, 回退到确定性压缩(保留首条 user 原始意图 + 最近窗口)。
func (e *Engine) SetHistorySummarizer(fn func(ctx context.Context, msgs []llm.Message) (string, error)) {
	e.historySummarizer = fn
}

// compactHistory 对超长历史做 Token 预算压缩:
//   - 不超过 historyKeepRecent 条时原样返回;
//   - 超过时保留最近 historyKeepRecent 条;
//   - 较早部分优先用 historySummarizer 生成摘要(若已配置且成功), 否则回退为"保留首条非空 user 消息(原始意图锚点)"的确定性方案。
//
// 该压缩对服务端持久化历史(baseHistory, 可能很长)尤其重要; 压缩后的摘要消息会被持久化回会话, 实现跨轮累积压缩。
func (e *Engine) compactHistory(ctx context.Context, history []llm.Message) []llm.Message {
	if len(history) <= historyKeepRecent {
		return history
	}
	overflow := history[:len(history)-historyKeepRecent]
	recent := history[len(history)-historyKeepRecent:]

	if e.historySummarizer != nil {
		sum, serr := e.historySummarizer(ctx, overflow)
		if serr == nil && strings.TrimSpace(sum) != "" {
			AIHistoryCompactions.Inc()
			return append([]llm.Message{{Role: "system", Content: "[历史对话摘要]\n" + sum}}, recent...)
		}
		if serr != nil {
			slog.Warn("history summarizer failed, falling back to deterministic compaction", "err", serr)
		}
	}

	// 确定性回退: 保留 overflow 中首条非空 user 消息作为原始意图锚点, 丢弃中间轮次。
	for _, m := range overflow {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			AIHistoryCompactions.Inc()
			return append([]llm.Message{m}, recent...)
		}
	}
	AIHistoryCompactions.Inc()
	return recent
}

// defaultSummarize 内置的 LLM 历史摘要器: 将较早历史压缩为一段简短要点摘要,
// 用于 Token 预算压缩的"智能摘要"路径。llmClient 未就绪时返回 error 以触发确定性回退。
func (e *Engine) defaultSummarize(ctx context.Context, msgs []llm.Message) (string, error) {
	if e.llmClient.Load() == nil && e.router == nil {
		return "", fmt.Errorf("llm client not ready")
	}
	var b strings.Builder
	b.WriteString("请用不超过 200 字,以要点形式总结以下运维对话的关键结论、已执行操作与待办事项,保留可用于后续排查的具体指标/命令/错误。不要编造内容:\n\n")
	for _, m := range msgs {
		role := m.Role
		if role == "" {
			role = "user"
		}
		content := m.Content
		if len(content) > 1000 {
			content = content[:1000] + "...(已截断)"
		}
		b.WriteString(fmt.Sprintf("[%s] %s\n", role, content))
	}
	resp, err := e.chatLLM(ctx, llm.ChatRequest{
		Messages:    []llm.Message{{Role: "user", Content: b.String()}},
		MaxTokens:   300,
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty summary response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// ——— 历史清洗(防御提示注入, P0-1) ———
const (
	maxHistoryMessages = 20   // SanitizeHistory 的总条数上限(防御注入 + 兜底)
	maxHistoryMsgLen   = 8000 // 单条历史消息最大长度, 防止超长内容撑爆上下文
	historyKeepRecent  = 16   // 超过该窗口时较早历史被压缩(保留该窗口 + 原始意图锚点/摘要)
)

// SanitizeHistory 清洗客户端提交的对话历史, 防止提示注入:
//   - 仅保留 user / assistant 角色, 丢弃 system / tool 等可绕过安全约束或伪造工具结果的角色;
//   - 剥离 assistant 消息中的 tool_calls, 阻止客户端注入"待执行工具调用";
//   - 限制单条长度与总条数。
//
// 注意: 服务端注入的 system 消息(如 RAG 上下文)应在调用本函数之后再追加,
// 以免在 buildInitialMessages 中被当作客户端历史误删。
func SanitizeHistory(h []llm.Message) []llm.Message {
	if len(h) == 0 {
		return nil
	}
	out := make([]llm.Message, 0, len(h))
	for _, m := range h {
		switch m.Role {
		case "user", "assistant":
			cm := llm.Message{Role: m.Role, Content: clampStr(m.Content, maxHistoryMsgLen)}
			if m.Role == "assistant" {
				cm.ReasoningContent = clampStr(m.ReasoningContent, maxHistoryMsgLen)
			}
			out = append(out, cm)
		default:
			// 丢弃 system / tool / 其他角色
			continue
		}
	}
	if len(out) > maxHistoryMessages {
		out = out[len(out)-maxHistoryMessages:]
	}
	return out
}

func clampStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// builtinToolDenyList 不被暴露给模型的内建工具(避免自我递归或噪音调用)。
var builtinToolDenyList = map[string]bool{
	"llm_chat": true,
	"http_get": true,
}

// noCacheTools 不参与工具结果缓存的工具: 有副作用/写操作(script_run/ticket_create)与内建辅助工具。
// 缓存仅对幂等/只读的运维工具(cmdb_query/knowledge_search 等)生效, 避免缓存写操作或污染内部调用。
var noCacheTools = map[string]bool{
	"script_run":    true,
	"ticket_create": true,
	"llm_chat":      true,
	"http_get":      true,
}

// buildToolDefs 将 Registry 中的运维工具转换为 LLM Function Calling 所需的 ToolDef。
// 跳过内建工具与未注册 Handler 的工具。
// agentID 非空且该 Agent 配置了 Tools 白名单时, 仅返回白名单内的工具; 否则返回全部。
func (e *Engine) buildToolDefs(agentID string) []llm.ToolDef {
	defs := e.reg.List()
	// Agent 工具白名单(空 = 全部可用)
	var allowlist map[string]bool
	if agentID != "" {
		if a, ok := e.GetAgent(agentID); ok && len(a.Tools) > 0 {
			allowlist = make(map[string]bool, len(a.Tools))
			for _, t := range a.Tools {
				if t = strings.TrimSpace(t); t != "" {
					allowlist[t] = true
				}
			}
		}
	}
	out := make([]llm.ToolDef, 0, len(defs))
	for _, d := range defs {
		if builtinToolDenyList[d.Name] {
			continue
		}
		if allowlist != nil && !allowlist[d.Name] {
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

// maxToolRoundsExplore 自主模式(explore=true)的轮次上限。
// 自主模式需要"规划→逐步执行→检查→总结"多轮迭代, 上限放宽; 仍由限流器约束总调用频次。
const maxToolRoundsExplore = 12

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

// ——— 自定义 MCP 工具 (PRD 5.2.2) ———

// CreateCustomTool 创建自定义工具并注册到 registry(启用时)。
func (e *Engine) CreateCustomTool(ctx context.Context, t *CustomTool) error {
	if t == nil {
		return fmt.Errorf("custom tool is nil")
	}
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	e.mu.Lock()
	e.customTools[t.ID] = t
	e.syncCustomToolLocked(t)
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.CreateCustomTool(ctx, t); err != nil {
			return err
		}
	}
	slog.Info("custom tool created", "id", t.ID, "name", t.Name, "type", t.Type, "enabled", t.Enabled)
	return nil
}

// UpdateCustomTool 更新自定义工具并同步 registry。
func (e *Engine) UpdateCustomTool(ctx context.Context, t *CustomTool) error {
	if t.ID == "" {
		return fmt.Errorf("custom tool id is required")
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	e.mu.Lock()
	existing, ok := e.customTools[t.ID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("custom tool not found: %s", t.ID)
	}
	// 反注册旧定义, 再按新定义注册
	e.reg.Unregister(existing.Name)
	t.TenantID = existing.TenantID
	e.customTools[t.ID] = t
	e.syncCustomToolLocked(t)
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.UpdateCustomTool(ctx, t); err != nil {
			return err
		}
	}
	slog.Info("custom tool updated", "id", t.ID, "name", t.Name, "enabled", t.Enabled)
	return nil
}

// DeleteCustomTool 删除自定义工具并从 registry 反注册。
func (e *Engine) DeleteCustomTool(ctx context.Context, id, tenantID string) error {
	e.mu.Lock()
	existing, ok := e.customTools[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("custom tool not found: %s", id)
	}
	e.reg.Unregister(existing.Name)
	delete(e.customTools, id)
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.DeleteCustomTool(ctx, id, tenantID); err != nil {
			return err
		}
	}
	slog.Info("custom tool deleted", "id", id, "name", existing.Name)
	return nil
}

// ListCustomTools 列出当前引擎持有的自定义工具(含禁用)。
func (e *Engine) ListCustomTools(tenantID string) []*CustomTool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*CustomTool, 0)
	for _, t := range e.customTools {
		if t.TenantID == tenantID {
			out = append(out, t)
		}
	}
	return out
}

// SyncCustomTools 从持久化存储全量同步自定义工具到 registry(启动/热刷新时调用)。
func (e *Engine) SyncCustomTools(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 反注册全部现有自定义工具
	for _, t := range e.customTools {
		e.reg.Unregister(t.Name)
	}
	e.customTools = make(map[string]*CustomTool)
	if e.store == nil {
		return nil
	}
	tools, err := e.store.ListCustomTools(ctx, "default")
	if err != nil {
		return err
	}
	for _, t := range tools {
		e.customTools[t.ID] = t
		if t.Enabled {
			e.reg.Register(t.toToolDefinition())
		}
	}
	slog.Info("custom tools synced", "count", len(tools))
	return nil
}

// syncCustomToolLocked 按当前 enabled 状态注册/反注册单个工具(调用方须持有 e.mu)。
func (e *Engine) syncCustomToolLocked(t *CustomTool) {
	e.reg.Unregister(t.Name)
	if t.Enabled {
		e.reg.Register(t.toToolDefinition())
	}
}

// ——— 自定义 MCP 工具 End ———

// ——— 工具执行安全边界(P3-0 / P3-2) ———

// aiAgentCtxKey 用于把当前 AI agent 写入 context, 使工具执行链(reg.Execute)能读取发起会话的 agent。
type aiAgentCtxKey struct{}

func withAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, aiAgentCtxKey{}, agentID)
}

func agentIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(aiAgentCtxKey{}).(string)
	return s
}

// SetApprovalPolicy 设置审批策略。agentID 为空或 "default" 时设置全局默认, 否则设置该 Agent 的覆盖。
func (e *Engine) SetApprovalPolicy(agentID string, p ApprovalPolicy) {
	key := agentID
	if key == "" {
		key = "default"
	}
	e.mu.Lock()
	e.approvalPolicies[key] = p
	e.mu.Unlock()
}

// GetApprovalPolicy 读取指定 Agent 生效的审批策略(Agent 级覆盖优先, 否则全局默认)。
func (e *Engine) GetApprovalPolicy(agentID string) ApprovalPolicy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if agentID != "" {
		if p, ok := e.approvalPolicies[agentID]; ok {
			return p
		}
	}
	if p, ok := e.approvalPolicies["default"]; ok {
		return p
	}
	return DefaultApprovalPolicy()
}

// Approvals 返回审批管理器(service 层用于响应审批请求 / 挂起列表)。
func (e *Engine) Approvals() *ApprovalManager {
	return e.approvals
}

// ScriptApprovalChecker 引擎层的外部审批实现, 注入到 MCPToolSet.scriptRun:
//   - 按策略(Agent 级 > 全局)判断 script_run 是否需要交互式确认;
//   - 需要时发起 SSE ask 事件并挂起等待用户批准/拒绝(超时视为拒绝, fail-closed);
//   - 批准返回 (true, ""), 拒绝/超时返回 (false, 人类可读原因), 策略放行直接返回 (true, "")。
func (e *Engine) ScriptApprovalChecker(ctx context.Context, scriptID, targetAgentID string, args map[string]any) (bool, string) {
	policy := e.GetApprovalPolicy(agentIDFrom(ctx))
	if !policy.NeedsApproval("script_run") {
		return true, ""
	}

	// 构造人类可读的操作描述
	summary := "在主机 " + targetAgentID + " 上执行脚本 " + scriptID
	if name, _ := args["script_name"].(string); name != "" {
		summary = "在主机 " + targetAgentID + " 上执行脚本 " + name + " (" + scriptID + ")"
	}
	argsJSON, _ := json.Marshal(args)
	timeout := time.Duration(policy.AskTimeoutSec) * time.Second

	resp := e.approvals.Request(ctx, tenantOf(ctx), agentIDFrom(ctx), "script_run", string(argsJSON), summary, timeout)
	if resp == nil {
		return false, "操作未获批准(审批超时或已取消), 已停止执行"
	}
	if resp.Action == "approve" {
		slog.Info("ai script approved by human", "script", scriptID, "agent", targetAgentID, "by", resp.By)
		return true, ""
	}
	return false, "操作被用户拒绝" + suffixComment(resp.Comment)
}

// suffixComment 拼接审批备注(可选)。
func suffixComment(comment string) string {
	if comment == "" {
		return ""
	}
	return ": " + comment
}

// privilegedTools 需要 ai:tool:exec 权限的高风险/写操作工具。
// 仅 script_run(在受管主机执行脚本) 与 ticket_create(创建工单) 受此约束;
// 只读工具(cmdb_query / knowledge_search) 任何已认证用户均可经 AI 调用。
var privilegedTools = map[string]bool{
	"script_run":    true,
	"ticket_create": true,
}

// maxToolResultLen 工具结果回填上下文前的最大长度; 超出则截断并标注, 防止大输出撑爆上下文窗口。
const maxToolResultLen = 4096

// execToolChecked 在 Function Calling 循环中执行单个工具, 并做纵深防御:
//  1. 高风险工具(写/执行类)校验 RBAC 权限 ai:tool:exec, 越权不真正执行, 而是回填错误供模型向用户解释;
//  2. 每工具令牌桶限流(与对话限流解耦), 超限同样回填错误;
//  3. 结果长度上限截断(P3-2)。
//
// 返回 (结果, 是否真的发生了执行错误)。越权/限流属于"已受控", 以结构化错误结果返回而非 panic。
func (e *Engine) execToolChecked(ctx context.Context, tc llm.ToolCall) (any, bool) {
	name := tc.Function.Name
	if privilegedTools[name] {
		role := auth.RoleFromContext(ctx)
		if role == "" || !auth.HasPermission(role, auth.PermissionAIToolExec) {
			AIToolExecDenied.Inc()
			slog.Warn("ai tool exec denied by RBAC", "tool", name, "role", role)
			return map[string]any{"error": fmt.Sprintf("permission denied: tool %q requires permission %s", name, auth.PermissionAIToolExec)}, false
		}
	}
	if e.lim != nil && !e.lim.Allow("tool:"+name) {
		AIToolExecDenied.Inc()
		slog.Warn("ai tool exec rate limited", "tool", name)
		return map[string]any{"error": "rate limit exceeded for tool execution: " + name}, false
	}

	var args map[string]any
	if tc.Function.Arguments != "" {
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
	}

	// 工具结果缓存: 仅对幂等/只读工具生效, 跨请求复用, 减少重复后端/LLM 工具往返。
	// 缓存未命中才走真实执行与限流; 命中缓存不消耗限流额度(本就未触达后端)。
	// key 含租户, 杜绝跨租户串味。
	if e.toolCache != nil && !noCacheTools[name] {
		ckey := name + "\x1f" + tenantOf(ctx) + "\x1f" + canonicalArgsKey(args)
		if cached, ok := e.toolCache.get(ckey); ok {
			AIToolCacheHits.Inc()
			slog.Debug("ai tool result served from cache", "tool", name)
			return cached, false
		}
	}

	if e.lim != nil && !e.lim.Allow("tool:"+name) {
		AIToolExecDenied.Inc()
		slog.Warn("ai tool exec rate limited", "tool", name)
		return map[string]any{"error": "rate limit exceeded for tool execution: " + name}, false
	}

	result, execErr := e.reg.Execute(ctx, name, args)
	if execErr != nil {
		slog.Warn("ai tool execution failed", "tool", name, "error", execErr)
		return map[string]any{"error": execErr.Error()}, true
	}
	truncated := e.truncateToolResult(result)
	if e.toolCache != nil && !noCacheTools[name] {
		ckey := name + "\x1f" + tenantOf(ctx) + "\x1f" + canonicalArgsKey(args)
		e.toolCache.set(ckey, truncated)
	}
	return truncated, false
}

// truncateToolResult 对工具结果做长度上限截断, 避免大输出(如脚本日志)撑爆上下文窗口。
func (e *Engine) truncateToolResult(result any) any {
	b, err := json.Marshal(result)
	if err != nil {
		return result
	}
	if len(b) <= maxToolResultLen {
		return result
	}
	s := string(b)
	return map[string]any{
		"_truncated":   true,
		"original_len": len(b),
		"preview":      s[:maxToolResultLen] + "...(truncated)",
	}
}

// recordStreamTokens 估算并记录流式调用的 token 消耗(StreamChat 不回传 usage, 故按字符数粗估)。
func (e *Engine) recordStreamTokens(agentID, content string) {
	est := len([]rune(content)) / 4
	if est < 1 {
		est = 1
	}
	e.persistTokenRecord(model.TokenRecord{
		Time:       time.Now(),
		AgentID:    agentID,
		Model:      "stream",
		Prompt:     0,
		Completion: est,
		Total:      est,
	})
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
		_ = e.store.CreateAgent(ctx, &AgentRecord{ID: a.ID, TenantID: a.TenantID, Name: a.Name, ModelName: a.ModelName, SystemPrompt: a.SystemPrompt, Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, KnowledgeCategories: a.KnowledgeCategories, Tools: a.Tools, Skills: a.Skills})
	}
	slog.Info("agent created", "id", a.ID, "name", a.Name, "model", a.ModelName, "knowledge", a.KnowledgeCategories, "tools", a.Tools)
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
	// 知识库绑定: 显式传入(非 nil)才覆盖, 避免未传字段的清空误伤
	if a.KnowledgeCategories != nil {
		existing.KnowledgeCategories = a.KnowledgeCategories
	}
	// 工具白名单: 显式传入(非 nil)才覆盖
	if a.Tools != nil {
		existing.Tools = a.Tools
	}
	// Skills: 显式传入(非 nil)才覆盖
	if a.Skills != nil {
		existing.Skills = a.Skills
	}
	existing.UpdatedAt = time.Now()
	if e.store != nil {
		_ = e.store.UpdateAgent(ctx, &AgentRecord{ID: existing.ID, TenantID: existing.TenantID, Name: existing.Name, ModelName: existing.ModelName, SystemPrompt: existing.SystemPrompt, Status: existing.Status, UpdatedAt: existing.UpdatedAt, KnowledgeCategories: existing.KnowledgeCategories, Tools: existing.Tools, Skills: existing.Skills})
	}
	slog.Info("agent updated", "id", a.ID, "name", existing.Name)
	return nil
}

// AgentKnowledgeCategories 返回指定 Agent 绑定的知识库分类列表; 为空表示绑定全部。
func (e *Engine) AgentKnowledgeCategories(agentID string) []string {
	if agentID == "" {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if a, ok := e.agents[agentID]; ok {
		return a.KnowledgeCategories
	}
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
	slot, ok := e.acquireLLMSlot(ctx)
	if !ok {
		return nil, fmt.Errorf("llm concurrency limit reached")
	}
	defer e.releaseLLMSlot(slot)
	resp, err := e.chatLLM(ctx, llm.ChatRequest{
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

// DefaultSystemPrompt 是全局默认 system prompt 的内置常量(外置化的兜底值)。
// 运维可通过 LLMConfig.DefaultSystemPrompt(环境变量 OPSNEXUS_LLM_DEFAULT_SYSTEM_PROMPT)覆盖,
// 实现"提示词外置/版本化": 不必改代码即可调整助手人设与行为边界。
const DefaultSystemPrompt = "你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"

// citationGuidance 注入到 system prompt 的引用标注指引(实现 RAG grounding)。
// 要求模型在引用知识库/工具结果时用 [^<id>] 标注,并在文末列出引用来源。
const citationGuidance = `引用与溯源要求:
- 当你依据知识库文章(knowledge_search 返回结果的 id 字段)或工具返回数据作答时,必须在正文中用 [^<文章ID>] 标注引用,例如 [^a1b2c3]。
- 回答结尾用 "## 引用来源" 列出所有被引用文章的标题与 ID。
- 未引用任何资料时无需列出。
- 禁止编造不存在的文章 ID;只能引用工具实际返回给你的资料 ID。`

// explorePrompt 自主模式(explore=true)附加到 system prompt 的执行方法论。
// 核心: 先规划 → 逐步执行(可多轮工具调用) → 检查 → 总结报告。
// 通过 <plan>/<step>/<report> 标签让前端能够结构化解析渲染。
const explorePrompt = `【自主模式执行方法论】
用户将给你一个运维目标,而不是单个问题。你必须以"自主 Agent"方式完成:

1. 规划: 先用 <plan> 标签输出执行计划,格式:
   <plan>
   1. 步骤描述 (涉及工具: xxx)
   2. 步骤描述 (涉及工具: xxx)
   ...
   </plan>

2. 执行: 按计划逐步执行。每一步:
   - 可以用 <step> 标签标注当前步骤名,如 <step>检查主机状态</step>;
   - 需要数据/操作时调用对应工具(cmake 查询、knowledge_search 检索等);
   - 工具结果不理想时,自主调整下一步骤(允许偏离原计划,但需简短说明原因)。

3. 检查: 每一步完成后核对结果是否符合目标,缺失信息主动补查。

4. 总结: 全部完成或确认无法完成后,用 <report> 标签输出最终报告:
   <report>
   ## 目标
   ## 执行过程(简述各步骤与工具结果)
   ## 结论 / 发现
   ## 建议的后续动作(如需人工介入,明确说明)
   </report>

安全边界(必须遵守):
- 优先使用只读工具(cmdb_query / knowledge_search / http_get)完成任务;
- script_run 等执行类工具受审批保护,若调用被拒绝,不要重试强攻,改为报告现状并建议人工处理;
- 严禁执行与目标无关的操作;拿不准就停下来问用户。`

// SetDefaultSystemPrompt 设置全局默认 system prompt(外置化入口,可由 sysconfig 热更新调用)。
// 传入空串时保留内置常量 DefaultSystemPrompt 作为兜底。
func (e *Engine) SetDefaultSystemPrompt(p string) {
	if p == "" {
		p = DefaultSystemPrompt
	}
	e.mu.Lock()
	e.defaultSystemPrompt = p
	e.mu.Unlock()
}

// resolveSystemPrompt 按 agentID 查找 Agent 的 system_prompt, 未设时回退到外置化的全局默认值。
func (e *Engine) resolveSystemPrompt(agentID string) string {
	if agentID != "" {
		e.mu.RLock()
		a, ok := e.agents[agentID]
		e.mu.RUnlock()
		if ok && a.SystemPrompt != "" {
			return a.SystemPrompt
		}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.defaultSystemPrompt
}

// citationRE 匹配模型回答中的引用标注 [^<id>]。
var citationRE = regexp.MustCompile(`\[\^([^\]]+)\]`)

// ExtractCitations 从模型回答中解析引用标注 [^<id>], 返回去重后的文章 ID 列表。
// 用于在 API 响应/落库中暴露"引用来源", 实现 RAG 回答的可溯源(grounding)。
func ExtractCitations(text string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, m := range citationRE.FindAllStringSubmatch(text, -1) {
		id := strings.TrimSpace(m[1])
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// modelForAgent 按 agentID 查找该 Agent 配置的目标模型; 未配置时返回空串,
// 由底层 llm.Client 回退到全局默认模型。用于在多 Agent 场景下让不同 Agent 走不同 LLM。
func (e *Engine) modelForAgent(agentID string) string {
	if agentID != "" {
		e.mu.RLock()
		a, ok := e.agents[agentID]
		e.mu.RUnlock()
		if ok && a.ModelName != "" {
			return a.ModelName
		}
	}
	return ""
}

// ——— 热更新 ———

// UpdateClient 热更新底层 LLM 客户端(sysconfig 修改 LLM 配置后调用)。
func (e *Engine) UpdateClient(c *llm.Client) {
	if c == nil {
		return
	}
	e.llmClient.Store(c)
	slog.Info("engine llm client updated")
}

// ——— LLM 并发信号量(决策 C) ———
// 逻辑会话(如 100 路 SSE)与物理 LLM 调用解耦: 前者只受会话数约束,
// 后者受 llmSem 容量约束(默认 32), 防止底层 LLM 后端被瞬时打爆。

// SetLLMMaxInflight 设置物理 LLM 调用的最大并发槽位数。
// 注意: 热更新会创建新的 channel; 仍在途的请求在其捕获的旧 channel 上释放,
// 不会丢失配额, AILLLMInFlight 计数保持准确(修复 P0-3 的计数器漂移)。
func (e *Engine) SetLLMMaxInflight(n int) {
	if n < 1 {
		n = 1
	}
	e.semMu.Lock()
	e.llmSem = make(chan struct{}, n)
	e.semMu.Unlock()
	slog.Info("engine llm max-inflight updated", "max", n)
}

// acquireLLMSlot 抢占一个物理 LLM 调用槽位; ctx 取消时返回 (nil, false)。
// 返回的 channel 必须在释放时原样传回, 确保槽位配额在热更新(channel 重建)后不丢失。
func (e *Engine) acquireLLMSlot(ctx context.Context) (chan struct{}, bool) {
	e.semMu.Lock()
	ch := e.llmSem
	e.semMu.Unlock()
	select {
	case ch <- struct{}{}:
		AILLMInFlight.Inc()
		return ch, true
	case <-ctx.Done():
		return nil, false
	}
}

// releaseLLMSlot 释放一个物理 LLM 调用槽位(须传入 acquireLLMSlot 返回的 channel)。
func (e *Engine) releaseLLMSlot(ch chan struct{}) {
	if ch == nil {
		return
	}
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

// ChatStream 流式输出(SSE)。service 在 req.stream=true 时调用本方法, 返回的文本块经 SSE 推给前端。
// 基于 llmClient.StreamChat 实现真实流式: 文本/推理逐 token 推送, 工具调用在发生时实时发射 🔧/↳ 标记,
// 循环内边收边发(与团队模式一致), 而非先跑完整个循环再按词切分(伪流式)。
func (e *Engine) ChatStream(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID string, explore bool) (<-chan StreamChunk, string, error) {
	if prompt == "" {
		return nil, "", fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return nil, "", fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}
	out := make(chan StreamChunk, 64)
	go func() {
		defer close(out)
		err := e.chatWithToolsStream(ctx, agentID, prompt, history, reasoning, conversationID, explore, func(typ string, text string) {
			out <- StreamChunk{Type: typ, Content: text}
		})
		if err != nil {
			out <- StreamChunk{Type: "error", Content: err.Error()}
		}
	}()
	// 仅当显式提供 conversationID 时才持久化(用该 ID 作为会话标识; not found 时按同 ID 新建)。
	return out, conversationID, nil
}

// chatWithToolsStream 真实流式版 Function Calling 循环:
//  1. 每轮用 StreamChat 边收边推(token/推理实时发往前端);
//  2. 若本轮模型请求工具, 在流结束后实时发射 🔧 调用 / ↳ 结果 标记, 再执行工具并把结果回填;
//  3. 回到步骤 1, 直至模型不再请求工具(终轮,答案已流式推送)或达到最大轮次。
//
// 与 chatWithTools 共享 execToolChecked, 因此同样受 RBAC / 限流 / 结果截断约束。
func (e *Engine) chatWithToolsStream(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID string, explore bool, emit func(typ string, text string)) error {
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		return fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}
	// 把 AI agent 身份写入上下文, 工具执行链可据此读取 Agent 级审批策略
	ctx = withAgentID(ctx, agentID)

	// 会话加载: 提供 conversationID 时优先用服务端持久历史(可信, 含 tool 消息), 否则用客户端 history。
	var conv *Conversation
	baseHistory := history
	if conversationID != "" {
		if c, e2 := e.GetConversation(conversationID); e2 == nil {
			conv = c
			baseHistory = c.Messages
		} else if strings.Contains(e2.Error(), "not found") {
			conv = &Conversation{ID: conversationID, AgentID: agentID, TenantID: tenantOf(ctx)}
		}
	}

	userPrompt := prompt
	if reasoning {
		userPrompt = "请按以下格式回答：\n\n<think>\n先进行分步推理分析\n</think>\n<answer>\n再给出最终答案\n</answer>\n\n用户问题：" + prompt
	}
	if explore {
		// 自主模式: 追加执行方法论, 并把用户输入声明为"目标"
		userPrompt = "【运维目标】\n" + prompt
	}
	// 从客户端历史提取 service 层注入的 system 补充(仅 RAG 上下文; 客户端 system 已被 SanitizeHistory 剥离),
	// 避免续接会话时丢失知识库检索结果。服务端会话历史(baseHistory)不含 system。
	var sysExtra string
	for _, m := range history {
		if m.Role == "system" {
			if sysExtra != "" {
				sysExtra += "\n\n"
			}
			sysExtra += m.Content
		}
	}
	if explore {
		// 自主模式: 注入执行方法论到 system prompt
		if sysExtra != "" {
			sysExtra += "\n\n"
		}
		sysExtra += explorePrompt
	}
	msgs, roundStart := e.buildMessages(ctx, agentID, userPrompt, baseHistory, sysExtra)
	tools := e.buildToolDefs(agentID)

	var err error
	completed := false
	// 自主模式需要更长的工具循环上限(多步规划执行), 普通对话保持原上限。
	maxRounds := maxToolRounds
	if explore {
		maxRounds = maxToolRoundsExplore
	}
	for round := 0; round < maxRounds; round++ {
		slot, ok := e.acquireLLMSlot(ctx)
		if !ok {
			return fmt.Errorf("llm concurrency limit reached")
		}
		ch, eerr := e.streamLLM(ctx, llm.ChatRequest{
			Model:       e.modelForAgent(agentID),
			Messages:    msgs,
			Tools:       tools,
			MaxTokens:   e.genMaxTokensVal(),
			Temperature: e.genTemperatureVal(),
		})
		if eerr != nil {
			e.releaseLLMSlot(slot)
			return eerr
		}

		var content, reasoningAcc string
		var toolCalls []llm.ToolCall
		for sc := range ch {
			if sc.Content != "" {
				emit("token", sc.Content)
			}
			if sc.Reasoning != "" {
				emit("thinking", sc.Reasoning)
			}
			content += sc.Content
			reasoningAcc += sc.Reasoning
			if len(sc.ToolCalls) > 0 {
				toolCalls = sc.ToolCalls
			}
		}
		// 槽位须覆盖整个流式消费期(而非 StreamChat 返回即释放), 否则流式并发将失控(P0-3)。
		e.releaseLLMSlot(slot)

		msgs = append(msgs, llm.Message{Role: "assistant", Content: content, ReasoningContent: reasoningAcc, ToolCalls: toolCalls})

		if len(toolCalls) == 0 {
			// 终轮: 答案已通过流式推送给前端
			e.recordStreamTokens(agentID, content)
			completed = true
			break
		}

		// 执行本轮工具调用, 实时发射进度标记
		for _, tc := range toolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			AIToolCalls.Inc()
			slog.Info("ai function call", "tool", tc.Function.Name, "args", truncateJSON(args))
			emit("tool", fmt.Sprintf("\n\n🔧 **调用工具 `%s`**\n", tc.Function.Name))
			if len(args) > 0 {
				if b, e := json.MarshalIndent(args, "", "  "); e == nil {
					emit("tool", "```json\n"+string(b)+"\n```\n")
				}
			}
			result, _ := e.execToolChecked(ctx, tc)
			emit("tool", fmt.Sprintf("\n↳ **结果**: %s\n\n", summarizeToolResult(result)))

			resBytes, _ := json.Marshal(result)
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				Content:    string(resBytes),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}
	if !completed {
		if err == nil {
			err = fmt.Errorf("function calling loop exceeded max rounds (%d)", maxRounds)
		}
	}

	// 持久化本轮(仅成功且 conv 已加载/新建时)
	if conv != nil && err == nil {
		roundMsgs := msgs[roundStart:]
		conv.Messages = append(conv.Messages, roundMsgs...)
		conv.AgentID = agentID
		if conv.TenantID == "" {
			conv.TenantID = tenantOf(ctx)
		}
		if serr := e.SaveConversation(ctx, conv); serr != nil {
			slog.Warn("ai: save conversation failed", "err", serr)
		}
	}
	return err
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
