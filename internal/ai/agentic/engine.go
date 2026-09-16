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
	"unicode"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/llm"
	"github.com/opsorigin/core/pkg/principal"
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
	convMu    sync.Map // 按会话 ID 串行化"读历史→追加→写回", 避免并发续接同一会话导致后写覆盖前写(PG 整条替换)及内存存储的切片 data race
	reg       *mcp.Registry
	lim       ratelimit.RateLimiter
	llmClient atomic.Pointer[llm.Client] // 热更新场景下的并发安全读写(避免 data race)
	// router 多模型路由池(P3, 可选): 非 nil 时聊天/流式调用走路由(权重+健康+失败降级),
	// nil 时回退到 llmClient 单实例(行为与以往一致)。
	router    *ModelRouter
	ready     bool
	tokenRecs []model.TokenRecord
	tokenMu   sync.Mutex
	semMu     sync.Mutex
	llmSem    chan struct{}

	// finetuneTasks 反馈驱动微调任务(L3): 进程内存储(与 agents/plans 同策略)。
	// FinetuneRunnerFunc 由 cmd/core 注入真实微调后端; nil=未配置微调后端。
	finetuneTasks      map[string]*FinetuneTask
	FinetuneRunnerFunc FinetuneRunnerFunc

	// faultInjections 故障仿真演练记录(L3-2 仿真验证飞轮): 进程内存储。
	faultInjections map[string]*FaultInjectionRun

	// remediations 自动修复动作(L1 分级处置闭环): 进程内存储。
	// RemediationRunnerFunc 由 cmd/core 注入真实 Agent/脚本执行; nil=内置演示执行器。
	// RemediationVerifierFunc 修复后验证回调; nil=默认视为验证通过。
	remediations            map[string]*RemediationAction
	RemediationRunnerFunc   RemediationRunnerFunc
	RemediationVerifierFunc RemediationVerifierFunc

	// runbooks 沉淀的可复用修复剧本(P0-2): 由成功且验证通过的修复抽取。
	runbooks map[string]*Runbook

	// autonomy 自治分级(notify/suggest/act_with_approval/autonomous)。
	autonomy AutonomyLevel

	// rcaRecords RCA 基准记录(人工标注准确率形成证据)。
	rcaRecords map[string]*RCARecord

	// postmortems 事故复盘沉淀(复盘学习, 回灌记忆/runbook)。
	postmortems map[string]*Postmortem

	// memories Agent 长期记忆(P0-1): 进程内存储(与 agents/plans 同策略)。
	memories map[string]*ExperienceMemory
	// memoryPersist 记忆落库回调(由 cmd/core 注入 DB 实现, nil=仅内存态重启清空)。
	memoryPersist MemoryPersist

	// defaultSystemPrompt 是未配置 Agent 级 system_prompt 时的全局默认提示词(外置化,见 SetDefaultSystemPrompt)。
	defaultSystemPrompt string

	// toolCache 工具结果缓存(name+tenant+args -> 结果), 跨请求复用, 降低重复 LLM 工具往返成本。
	// 非幂等/有副作用工具(script_run/ticket_create)与内建工具不缓存(见 noCacheTools)。
	toolCache *ttlCache

	// historySummarizer 可选: 用 LLM 将超长历史的较早部分压缩为摘要,显著降低 Token 消耗。
	// 未设置时 compactHistory 回退到确定性压缩(保留首条 user 原始意图 + 最近窗口)。
	historySummarizer func(ctx context.Context, msgs []llm.Message) (string, error)

	// partResolver 可选: 多模态片段解析器(attachment:// 引用 → data URL/正文)。
	// 由 ai 层注入(它持有对象存储与附件表), 引擎不反向依赖 ai 包。见 resolvePartsForSend。
	partResolver PartResolver

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
	// disposalCaseProvider 由 cmd/core 注入的知识库处置案例回调(nil=训练集不导出处置案例)。
	disposalCaseProvider DisposalCaseProvider
	// workspaceSink 由 cmd/core 注入的会话工作区写入回调(nil=不收集工具产物)。
	// 工具执行产生文件类输出(script_run/script_detail)时自动沉淀到会话工作区, 供前端展示/下载。
	workspaceSink func(ctx context.Context, conversationID, toolName, fileName string, content []byte) error

	// ——— 敏感操作审批 (PRD 5.2.5) ———
	approvals *ApprovalManager
	// approvalPolicies 全局默认 + Agent 级覆盖策略: "default" 键为全局, 其余键为 agent_id。
	approvalPolicies map[string]ApprovalPolicy

	// ——— 自定义 MCP 工具 (PRD 5.2.2) ———
	// customTools 用户注册的自定义工具(ID -> 定义), enabled 的工具动态注册到 reg。
	customTools map[string]*CustomTool

	// ——— 连接器审批策略 / 受控创作模式 (参考 Harness 连接器 policy 插件化 + Creator 模式) ———
	// toolPolicies 按工具名配置的审批策略(数据驱动、可插拔), 缺省回落 privilegedTools。
	toolPolicies map[string]ToolPolicy
	// controlledMode 受控创作模式: 开启后 agent 自造工具(custom tool)执行前需二次审批。
	controlledMode bool

	// ——— 引擎级运行模式 (参考 DeepSeek Harness minimal/standard/creative 分层) ———
	// runMode 把"安全隔离/自治程度"抽象为一等公民, 经钩子链(dryRunGate/controlledGate)生效;
	// 默认 standard, 不改变既有行为。
	runMode RunMode

	// ——— 轨迹回放 (参考 DeepSeek Harness append-only trajectory) ———
	// traj 记录每次 agent 运行的 turn/step/tool 轨迹, 支持回放/复盘; nil=不记录。
	traj TrajectoryRecorder

	// ——— 钩子链 (参考 Harness agent/pre-step → tool/call → tool/result → step/end 扩展点) ———
	// hookChain 承载审批闸门/护栏/观测等横切关注点, nil 安全。
	hookChain *HookChain

	// ——— 连接器生命周期钩子 (参考 Harness 插件 OnRegister/OnUnregister) ———
	// connHooks 在连接器(自定义 MCP 工具)注册/反注册边界触发: 能力校验、策略自动播种、审计等; nil 安全。
	connHooks *LifecycleHookChain
	// autoSeededPolicies 记录由生命周期钩子自动播种的工具策略名, 反注册时仅清理这些、不误删人工覆盖。
	autoSeededPolicies map[string]bool

	// ——— 连接器按 agent scope 实例隔离 (参考 Harness isolate: fs: true 的 scope 隔离) ———
	// toolScopes 记录每个连接器(工具名)可见的 Agent ID 白名单; 键不存在或值为空 = 对全部 Agent 可见。
	// 与 Agent 自身 Tools 白名单是"与"关系(见 buildToolDefs), 形成"连接器只给指定 Agent 看"的实例隔离。
	toolScopes map[string][]string
	// autoSeededScopes 记录由生命周期钩子自动播种的 scope(来自 CustomTool.AllowedAgents),
	// 反注册时仅清理这些、不误删人工经 SetToolScope 的覆盖。
	autoSeededScopes map[string]bool

	// ——— 记忆回流驱动技能演化 (参考 Harness dsh-memory-evolve, S1) ———
	// skillEvolver 把每次运行结果回流: 成功蒸馏经验记忆, 失败累积触发技能补丁建议;
	// nil=不演化。
	skillEvolver SkillEvolver

	// ——— 技能 overlay 组合 (参考 Harness Seam overlay, D) ———
	// skillOverlays 按 id 叠加 base 技能的差异补丁(场景变体), 渲染时合并、移除即还原。
	skillOverlays *SkillOverlayRegistry
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
		remediations:        make(map[string]*RemediationAction),
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
	// 连接器审批策略表(按工具名), 数据驱动、可经 SetToolPolicy 热插拔。
	e.toolPolicies = defaultToolPolicies()
	// 钩子链: 注册内置 RBAC 审批闸门(复刻原 execToolChecked 特权校验) + 受控创作护栏;
	// 后续护栏/观测/计费上限以 Hook 追加, 无需改动主循环。
	e.hookChain = NewHookChain()
	e.hookChain.Add(Hook{PreToolCall: e.rbacGate})
	e.hookChain.Add(Hook{PreToolCall: e.controlledGate})
	e.hookChain.Add(Hook{PreToolCall: e.dryRunGate})
	// 连接器生命周期钩子链: 注册时先能力校验(fail-closed)再自动播种安全默认审批策略,
	// 反注册时清理播种的策略(参考 Harness 插件生命周期 OnRegister/OnUnregister)。
	e.connHooks = NewLifecycleHookChain()
	e.autoSeededPolicies = make(map[string]bool)
	e.toolScopes = make(map[string][]string)
	e.autoSeededScopes = make(map[string]bool)
	e.connHooks.Add(LifecycleHook{OnRegister: e.validationOnRegister})
	e.connHooks.Add(LifecycleHook{OnRegister: e.policySeedOnRegister, OnUnregister: e.policySeedOnUnregister})
	e.connHooks.Add(LifecycleHook{OnRegister: e.scopeSeedOnRegister, OnUnregister: e.scopeSeedOnUnregister})
	e.skillOverlays = NewSkillOverlayRegistry()
	// 引擎级运行模式默认 standard(既有行为); 可由 SetRunMode 切到 dry-run-safe/autonomous。
	e.runMode = RunModeStandard
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

// SetFinetuneRunner 注入微调执行回调(cmd/core 装配; nil=关闭微调执行, 任务停留 pending)。
func (e *Engine) SetFinetuneRunner(fn FinetuneRunnerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.FinetuneRunnerFunc = fn
}

// SetModelRouter 注入多模型路由池(P3, 可选)。设置后聊天/流式调用走路由
// (权重+健康状态+失败降级), 否则回退到单 llmClient。
func (e *Engine) SetModelRouter(r *ModelRouter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.router = r
}

// chatLLM 聊天调用统一入口: router 优先(多模型路由+降级), 否则单 client。
// 注入当前用户与期望模型类型, 供路由做可见性/类型过滤(auto 模式生效)。
func (e *Engine) chatLLM(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	req.User = userIDFromContext(ctx)
	req.Messages = e.resolvePartsForSend(ctx, req.Messages)
	req.PreferType = preferTypeOf(req.Messages)
	if e.router != nil {
		return e.router.Chat(ctx, req)
	}
	return e.llmClient.Load().Chat(ctx, req)
}

// streamLLM 流式调用统一入口: router 优先, 否则单 client。
// 注入当前用户与期望模型类型, 供路由做可见性/类型过滤(auto 模式生效)。
func (e *Engine) streamLLM(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	req.User = userIDFromContext(ctx)
	req.Messages = e.resolvePartsForSend(ctx, req.Messages)
	req.PreferType = preferTypeOf(req.Messages)
	if e.router != nil {
		return e.router.StreamChat(ctx, req)
	}
	return e.llmClient.Load().StreamChat(ctx, req)
}

// PartResolver 多模态片段解析器: 输入持久化态引用(attachment://<id>#<name>),
// 输出模型可消费的片段(图片→data URL 的 image_url; 文本类→正文 text)。
// 返回 false 表示该引用无法解析(调用方退化为占位文本)。
type PartResolver func(ctx context.Context, url string) (llm.ContentPart, bool)

// SetPartResolver 注入附件解析器(由 ai 层持有对象存储与附件表, 引擎不反向依赖)。
// 未注入时多模态片段原样下发(公网直链场景仍可用, 仅 attachment:// 引用无法解析)。
func (e *Engine) SetPartResolver(fn PartResolver) {
	e.partResolver = fn
}

// resolvePartsForSend 发送前把消息中的 attachment:// 引用替换为真实内容。
// 返回副本而不修改入参: 入参同时用于会话持久化, 必须保持"引用态"(不然 base64 会写进会话表)。
func (e *Engine) resolvePartsForSend(ctx context.Context, msgs []llm.Message) []llm.Message {
	if e.partResolver == nil || len(msgs) == 0 {
		return msgs
	}
	out := msgs
	cloned := false
	for i := range msgs {
		parts := msgs[i].ContentParts
		if len(parts) == 0 {
			continue
		}
		hit := false
		next := make([]llm.ContentPart, 0, len(parts))
		for _, p := range parts {
			u := partRefURL(p)
			if u == "" || !strings.HasPrefix(u, "attachment://") {
				next = append(next, p)
				continue
			}
			rp, ok := e.partResolver(ctx, u)
			if !ok {
				next = append(next, p)
				continue
			}
			next = append(next, rp)
			hit = true
		}
		if hit {
			if !cloned { // 惰性拷贝: 无引用命中时零分配
				out = make([]llm.Message, len(msgs))
				copy(out, msgs)
				cloned = true
			}
			out[i].ContentParts = next
		}
	}
	return out
}

// partRefURL 取片段中的引用 URL(图片/文件), 非引用片段返回空串。
func partRefURL(p llm.ContentPart) string {
	switch {
	case p.Type == "image_url" && p.ImageURL != nil:
		return p.ImageURL.URL
	case p.Type == "file_url" && p.FileURL != nil:
		return p.FileURL.URL
	}
	return ""
}

// userIDFromContext 从上下文解析当前用户 ID(供路由可见性过滤)。空=系统/内部调用。
func userIDFromContext(ctx context.Context) string {
	if pr, ok := principal.FromContext(ctx); ok {
		return pr.ID
	}
	return ""
}

// preferTypeOf 按消息内容推导期望的模型能力类型。当前消息以纯文本为主,
// 若检测到图片数据(多模态)返回 "image", 否则 "text"。
// 视觉模型(glm-4v 等)具备文本能力, 故 text 请求仍可命中 image 类型模型;
// 仅纯视频模型会被 text 请求排除。
func preferTypeOf(msgs []llm.Message) string {
	for _, m := range msgs {
		if strings.Contains(m.Content, "data:image") {
			return "image"
		}
		// 多模态片段: 含图片时请求类型升为 image, 让路由优先挑选支持视觉的模型
		// (纯文本模型收到图片会静默忽略, 用户会看到"模型没看图")。
		for _, p := range m.ContentParts {
			if p.Type == "image_url" && p.ImageURL != nil {
				return "image"
			}
		}
	}
	return "text"
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
	// 预置技能市场内置技能包(按名+版本幂等)。必须在 store 就绪之后, 故放在此处而非 NewEngine。
	SeedSkillPackages(context.Background(), e)
	// 预置提示词库(按固定 ID 幂等), 并把默认系统提示词回灌到引擎兜底值(用户改过的优先)。
	SeedPromptTemplates(context.Background(), e)
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
	// 闭环产物(G4): 重启后恢复修复动作/剧本/RCA/复盘/自治档位, 避免内存态丢失。
	// 注意: Start() 在调用 loadFromStore 前已持有 e.mu, 此函数内不得重复加锁。
	e.restoreClosedLoopFromStore(ctx)
	return nil
}

// restoreClosedLoopFromStore 恢复闭环产物到内存 map(调用方须已持有 e.mu, 不重复加锁)。
func (e *Engine) restoreClosedLoopFromStore(ctx context.Context) {
	if e.store == nil {
		return
	}
	if recs, err := e.store.ListRemediations(ctx, ""); err == nil {
		for _, r := range recs {
			e.remediations[r.ID] = r
		}
	} else {
		slog.Warn("failed to restore remediations", "err", err)
	}
	if rbs, err := e.store.ListRunbooks(ctx, ""); err == nil {
		if e.runbooks == nil {
			e.runbooks = map[string]*Runbook{}
		}
		for _, rb := range rbs {
			e.runbooks[rb.ID] = rb
		}
	} else {
		slog.Warn("failed to restore runbooks", "err", err)
	}
	if rs, err := e.store.ListRCARecords(ctx, ""); err == nil {
		if e.rcaRecords == nil {
			e.rcaRecords = map[string]*RCARecord{}
		}
		for _, rec := range rs {
			e.rcaRecords[rec.ID] = rec
		}
	} else {
		slog.Warn("failed to restore rca records", "err", err)
	}
	if ps, err := e.store.ListPostmortems(ctx, ""); err == nil {
		if e.postmortems == nil {
			e.postmortems = map[string]*Postmortem{}
		}
		for _, pm := range ps {
			e.postmortems[pm.ID] = pm
		}
	} else {
		slog.Warn("failed to restore postmortems", "err", err)
	}
	if lvl, err := e.store.GetAutonomy(ctx, "default"); err == nil && lvl != "" {
		e.autonomy = lvl
	}
}

// Chat 执行一次单轮(可带历史)的 LLM 推理,供 AI 助手聊天端点使用。
// 支持 Function Calling:模型可自主调用已注册的运维工具(CMDB查询/工单/脚本执行/知识库检索)。
// Chat 执行一次单轮(可带历史)的 LLM 推理,供 AI 助手聊天端点使用。
// model 为可选的运行级模型覆盖(按会话/请求指定);为空时回退 agent 配置模型,再回退引擎默认。
// 支持 Function Calling:模型可自主调用已注册的运维工具(CMDB查询/工单/脚本执行/知识库检索)。
// parts 为本轮用户消息的多模态片段(图片/文件); 由 ai 层把附件解析为 base64 data URL 后传入, 无附件时传 nil。
func (e *Engine) Chat(ctx context.Context, agentID, prompt string, history []llm.Message, conversationID, model string, explore bool, parts ...llm.ContentPart) (content string, convID string, err error) {
	content, _, _, convID, err = e.chatWithToolsConv(ctx, agentID, prompt, history, false, conversationID, model, explore, parts)
	return
}

// ChatReasoning 思维链模式:返回思考过程和最终答案。
// nativeReasoning=true 时信任模型通过 reasoning_content 输出思维链,不在 prompt 中强制 <think> 格式,
// 避免原生推理模型(DeepSeek-R1/glm-4 等)把推理文本重复写入 content。
// nativeReasoning=false 时保留原行为:prompt 要求 <think>...</think><answer>...</answer>,
// 模型若未输出 reasoning_content 则从 content 中解析 CoT。
func (e *Engine) ChatReasoning(ctx context.Context, agentID, prompt string, history []llm.Message, conversationID, model string, explore bool, nativeReasoning bool, parts ...llm.ContentPart) (thinking string, answer string, convID string, err error) {
	forcePrompt := !nativeReasoning
	answer, reasoning, _, convID, err := e.chatWithToolsConv(ctx, agentID, prompt, history, forcePrompt, conversationID, model, explore, parts)
	if err != nil {
		return "", "", "", err
	}
	if reasoning != "" {
		return reasoning, answer, convID, nil
	}
	if nativeReasoning {
		// 原生推理模型未输出 reasoning_content 时直接返回正文,不尝试从 content 中剥离 CoT
		return "", answer, convID, nil
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
func (e *Engine) ChatWithTools(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID, model string, explore bool) (out string, inv []ToolInvocation, convID string, err error) {
	out, _, inv, convID, err = e.chatWithToolsConv(ctx, agentID, prompt, history, reasoning, conversationID, model, explore, nil)
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
// toolCallSig 计算一轮工具调用的稳定签名(工具名+参数),用于检测模型反复发起相同工具调用导致的死循环。
// thinkTagOpen/thinkTagClose 推理模型的思维链标记。部分兼容端点(如 GLM)会把思维链
// 混进正文 content, 或只留下孤立的结束标签, 需在对用户输出前剥离, 避免泄漏到回答中。
const (
	thinkTagOpen  = "<think>"
	thinkTagClose = "</think>"
)

// stripThinkTags 剥离文本中的思维链标记与其包裹的内容(用于非流式整段清洗)。
func stripThinkTags(s string) string {
	if !strings.Contains(s, thinkTagOpen) && !strings.Contains(s, thinkTagClose) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	rest := s
	for {
		iOpen, iClose := strings.Index(rest, thinkTagOpen), strings.Index(rest, thinkTagClose)
		switch {
		case iOpen < 0 && iClose < 0:
			b.WriteString(rest)
			return b.String()
		case iClose >= 0 && (iOpen < 0 || iClose < iOpen):
			// 孤立结束标签(思维链起始在 reasoning_content 中): 仅丢弃标签本身
			b.WriteString(rest[:iClose])
			rest = rest[iClose+len(thinkTagClose):]
		default:
			b.WriteString(rest[:iOpen])
			rest = rest[iOpen+len(thinkTagOpen):]
			// 丢弃到最近的结束标签; 没有结束标签则丢弃剩余全部(思维链未闭合)
			if i := strings.Index(rest, thinkTagClose); i >= 0 {
				rest = rest[i+len(thinkTagClose):]
			} else {
				rest = ""
			}
		}
	}
}

// thinkFilter 流式场景下的增量思维链过滤器: token 是逐块到达的, 标签可能被切在两个 chunk 之间,
// 因此需缓存尾部"可能是不完整标签"的若干字节, 待下个 chunk 再判定。
// 同时回吐 (clean, think) 两段: clean 是 <think> 之外的可见内容, think 是本次分片中已闭合的
// 思维链正文(未闭合部分留在内部, 由 flush 兜底丢弃或回收)。caller 把 think emit 为
// "event: thinking", 即可在前端 ThinkingBlock 独立折叠展示, 避免与答案混在同一个气泡里。
type thinkFilter struct {
	pending string
	inThink bool
}

func newThinkFilter() *thinkFilter { return &thinkFilter{} }

const thinkTagMaxLen = max(len(thinkTagOpen), len(thinkTagClose)) // 当前标签为中文, 按 UTF-8 字节数计算
// utf8SafeKeepSuffix 将 s 截成「长度不超过 maxKeep 的后缀」, 并保证 suffix 从有效的 UTF-8
// 字符边界开始(避免把多字节字符切成两半)。返回被切走的前缀与保留的后缀。
func utf8SafeKeepSuffix(s string, maxKeep int) (prefix, suffix string) {
	if len(s) <= maxKeep {
		return "", s
	}
	cut := len(s) - maxKeep
	// 若 cut 落在一个多字节字符的 continuation byte 上, 则把 cut 向后推到该字符起始字节,
	// 让这个不完整字符也被保留在 suffix 中, prefix 绝不以半截字符结尾。
	for cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut++
	}
	if cut > len(s) {
		return "", s
	}
	return s[:cut], s[cut:]
}

// feed 输入一个流式分片, 返回 (clean, think):
//   - clean: <think> 之外的可见文本, 可直接 emit("token", ...);
//   - think: 本次分片中已完整闭合的 <think> 内的思维链正文(可能为空, 表示本片未闭合或无 <think>)。
//
// 跨 chunk 标签切分: pending 缓存尾部可能是不完整标签的若干字节, 待下个 chunk 再判定。
func (f *thinkFilter) feed(s string) (clean, think string) {
	f.pending += s
	var outC, outT strings.Builder
	for {
		iOpen, iClose := strings.Index(f.pending, thinkTagOpen), strings.Index(f.pending, thinkTagClose)
		if f.inThink {
			if iClose < 0 {
				// 仍在思维链内: 累计已确认部分到 think, 仅保留可能是不完整结束标签的尾部
				if len(f.pending) > thinkTagMaxLen-1 {
					var safe string
					safe, f.pending = utf8SafeKeepSuffix(f.pending, thinkTagMaxLen-1)
					outT.WriteString(safe)
				}
				break
			}
			outT.WriteString(f.pending[:iClose])
			f.inThink = false
			f.pending = f.pending[iClose+len(thinkTagClose):]
			continue
		}
		if iOpen < 0 && iClose < 0 {
			// 无标签: 输出安全部分, 保留可能是不完整标签的尾部
			keep := thinkTagMaxLen - 1
			if len(f.pending) <= keep {
				break
			}
			var safe string
			safe, f.pending = utf8SafeKeepSuffix(f.pending, keep)
			outC.WriteString(safe)
			break
		}
		if iClose >= 0 && (iOpen < 0 || iClose < iOpen) {
			outC.WriteString(f.pending[:iClose])
			f.pending = f.pending[iClose+len(thinkTagClose):]
			continue
		}
		outC.WriteString(f.pending[:iOpen])
		f.pending = f.pending[iOpen+len(thinkTagOpen):]
		f.inThink = true
	}
	return outC.String(), outT.String()
}

// flush 流结束时输出残留缓冲, 返回 (clean, think):
//   - 思维链已闭合: clean 取 </think> 之后的部分, think 取闭合的 <think> 内容;
//   - 思维链未闭合(缺少结束标签): 丢弃, 避免把推理过程泄漏给用户(返回 "", "")。
func (f *thinkFilter) flush() (clean, think string) {
	rest := f.pending
	wasInThink := f.inThink
	f.pending = ""
	f.inThink = false
	if wasInThink {
		// 未闭合: 整段丢弃, 不返回任何内容
		return "", ""
	}
	if iOpen := strings.Index(rest, thinkTagOpen); iOpen >= 0 {
		// 残留里同时含 <think> 起点与 </think> 终点: 取中间作为 think, 之后部分作为 clean
		after := rest[iOpen+len(thinkTagOpen):]
		if iClose := strings.Index(after, thinkTagClose); iClose >= 0 {
			return after[iClose+len(thinkTagClose):], after[:iClose]
		}
		// 仅 <think> 起点无对应终点: 起点之后整段丢弃(类似未闭合)
		return "", ""
	}
	if iClose := strings.Index(rest, thinkTagClose); iClose >= 0 {
		return rest[iClose+len(thinkTagClose):], ""
	}
	return rest, ""
}

// ============================================================
// 中文分步推理结构剥离(与 thinkFilter 互补)
// ============================================================
// 背景: 原生推理模型(如 glm-4.6v-flash)在用户 prompt 含「先分步推理分析再给答案」类指令时,
// 会把推理过程以可见双段格式写进 content: 以「分步推理分析 / 思考过程」开头, 以「再给出最终答案 / 最终答案」
// 分隔——之前是推理、之后是答案。此时 content 无 <think> 标签, thinkFilter 无法折叠, 前端整段当答案。
// cnCoTFilter 在流式循环里把这种中文双段结构的前段物理折叠为 thinking(emit 给前端), 后段作为答案,
// 与是否开启「深度思考」开关、是否走 reasoning_content 通道无关。
type cnCoTFilter struct {
	matched bool            // 首个 chunk 是否以推理前缀开头, 进入 CoT 提取模式
	done    bool            // 是否已越过分隔符进入答案区
	buf     strings.Builder // 分隔符前的累积缓冲(用于跨 chunk 查找分隔符)
}

func newCnCoTFilter() *cnCoTFilter { return &cnCoTFilter{} }

var cnCoTPrefixes = []string{
	"先进行分步推理分析", "先分步推理分析", "分步推理分析",
	"思考过程", "推理分析", "先进行推理分析",
}

var cnCoTSeps = []string{
	"再给出最终答案", "再给出最终回答", "最终答案", "最终回答",
	"最终答案如下", "以下是最终答案",
}

// feed 输入一个流式分片, 返回 (clean, think):
//   - clean: 本次分片中分隔符之后的答案文本(可能为空)
//   - think: 本次分片中分隔符之前的推理文本(增量, 可能为空)
//
// 仅当 content 以推理前缀开头时进入提取模式; 否则原样透传(不误伤普通回答)。
// 分隔符跨 chunk 切分时, buf 缓存保证仍能识别。
func (f *cnCoTFilter) feed(s string) (clean, think string) {
	if f.done {
		return s, ""
	}
	if !f.matched {
		t := strings.TrimLeft(s, "\n\t ")
		for _, p := range cnCoTPrefixes {
			if strings.HasPrefix(t, p) {
				f.matched = true
				break
			}
		}
		if !f.matched {
			return s, ""
		}
	}
	prevLen := f.buf.Len()
	f.buf.WriteString(s)
	text := f.buf.String()
	sepIdx, sepLen := -1, 0
	for _, sep := range cnCoTSeps {
		if i := strings.Index(text, sep); i >= 0 {
			sepIdx, sepLen = i, len(sep)
			break
		}
	}
	if sepIdx < 0 {
		// 分隔符未出现: 本次分片整段作为 thinking 增量实时 emit
		return "", s
	}
	f.done = true
	f.buf.Reset()
	thinkPart := ""
	if sepIdx > prevLen {
		thinkPart = strings.TrimRight(text[prevLen:sepIdx], "\n\t ")
	}
	return strings.TrimLeft(text[sepIdx+sepLen:], "\n\t "), thinkPart
}

// flush 流结束时若已匹配但未找到分隔符, 之前已增量 emit 所有分片作为 thinking, 无需再发。
func (f *cnCoTFilter) flush() (clean, think string) {
	return "", ""
}

// answerTagOpen/answerTagClose 答案标签: 与 CoT prompt 配套, 模型应把最终答案包在这两个标签里。
// 引擎在 thinkFilter 之外对 clean 内容做一次轻量清洗, 把标签自身剥离、保留内容。
// 不做跨 chunk 缓存: 标签极短, 切分概率低, 极端情况下残留半个标签不影响答案内容完整性。
const (
	answerTagOpen  = "<answer>"
	answerTagClose = "</answer>"
)

// stripAnswerTags 去除答案正文里的 <answer>...</answer> 标签(保留内容)。
func stripAnswerTags(s string) string {
	if !strings.Contains(s, answerTagOpen) && !strings.Contains(s, answerTagClose) {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, answerTagOpen, ""), answerTagClose, "")
}

func toolCallSig(tcs []llm.ToolCall) string {
	parts := make([]string, 0, len(tcs))
	for _, tc := range tcs {
		parts = append(parts, tc.Function.Name+"\x1f"+tc.Function.Arguments)
	}
	return strings.Join(parts, "\x1f\x1f")
}

func (e *Engine) chatWithToolsConv(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID, model string, explore bool, parts []llm.ContentPart) (string, string, []ToolInvocation, string, error) {
	if prompt == "" {
		return "", "", nil, "", fmt.Errorf("prompt is required")
	}
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		AILLMRateLimitHits.Inc()
		return "", "", nil, "", fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}
	// 把 AI agent 身份写入上下文, 工具执行链可据此读取 Agent 级审批策略
	ctx = withAgentID(ctx, agentID)

	// 轨迹: 本次运行起点(append-only, 支持回放/复盘)。
	turn := &TrajectoryTurn{
		AgentID:        agentID,
		ConversationID: conversationID,
		TenantID:       tenantOf(ctx),
		StartedAt:      time.Now(),
		Prompt:         prompt,
	}

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

	// 会话并发串行化: 续接已有会话(conversationID 非空)时, 同一会话的并发请求被串行化,
	// 避免"读历史→追加→写回"被并发交错导致后写覆盖前写(PG 整条替换)或内存存储的切片 data race。
	// 新建会话(空 ID)不互斥, 互不影响。
	if conversationID != "" {
		lk := e.convLock(conversationID)
		lk.Lock()
		defer lk.Unlock()
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
	// P0-1 长期记忆: 检索该 Agent 的历史处置经验注入 system prompt(跨会话复用)
	if mems := e.RetrieveMemories(tenantOf(ctx), agentID, prompt); len(mems) > 0 {
		if sysExtra != "" {
			sysExtra += "\n\n"
		}
		sysExtra += memoriesPrompt(mems)
	}
	// 执行方法论: 绑定了工具的执行者(数字员工/智能体)统一按「分析→规划→执行→总结」方式工作;
	// 纯对话(无工具可用)不注入, 节省 token。模式概念已移除: 原 explore 参数仅作 API 兼容保留, 不再产生行为差异。
	tools := e.buildToolDefs(agentID)
	if len(tools) > 0 {
		if sysExtra != "" {
			sysExtra += "\n\n"
		}
		sysExtra += explorePrompt
	}
	// Token 预算压缩: 超长历史压缩为"滚动摘要 + 最近窗口"(条数或估算 token 任一超限即触发;
	// 摘要跨轮复用并回写会话, 见 Conversation.Summary/SummarizedCount)。
	convSummary, convCovered := "", 0
	if conv != nil {
		convSummary, convCovered = conv.Summary, conv.SummarizedCount
	}
	hc := e.compactHistory(ctx, baseHistory, convSummary, convCovered)
	msgs, roundStart := e.buildMessages(ctx, agentID, userPrompt, hc.history, sysExtra, parts)
	var invocations []ToolInvocation

	var content, reasoningAcc string
	var completed bool
	var err error
	// 死循环防护: 记录已出现过的工具调用签名, 模型重复发起相同调用即判定为死循环并优雅终止。
	seenCalls := make(map[string]bool)
	// 死循环防护(补充): 连续多轮仅调用同一工具且始终不收敛, 也判定为死循环(覆盖参数每次略变的重复调用)。
	lastSoloTool := ""
	soloStreak := 0
	// prevEmptyTools: 上一轮执行中返回"空/无数据"的工具名集合, 用于"空结果重试"硬拦截(见循环内检测)。
	prevEmptyTools := make(map[string]bool)
	// emptyRetryNudges: 已对"空工具重试"做过抑制执行的次数, 超过 maxEmptyRetryNudges 才兜底终止。
	emptyRetryNudges := 0

	// 运行 PreStep 钩子(如拒绝/改写); 短路则不调用 LLM, 仍记录本次轨迹。
	if e.hookChain != nil {
		if skip, perr := e.hookChain.RunPreStep(ctx, agentID, prompt); skip || perr != nil {
			if perr != nil {
				err = perr
			}
			turn.FinalContent = content
			turn.Error = errString(err)
			e.recordTrajectory(ctx, turn)
			return content, reasoningAcc, invocations, conversationID, err
		}
	}

toolLoop:
	for round := 0; round < maxToolRounds; round++ {
		slot, ok := e.acquireLLMSlot(ctx)
		if !ok {
			err = fmt.Errorf("llm concurrency limit reached")
			break
		}
		resp, cerr := e.chatLLM(ctx, llm.ChatRequest{
			Model:       e.modelForAgent(agentID, model),
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
			// 深度思考模型偶发仅吐 reasoning_content 或正文被安全策略拦截为空: 不硬失败(避免 502),
			// 返回友好提示让用户重试。
			content = "（模型本次未返回有效内容，可能是内容被安全策略拦截或生成超时；请换个问法重试）"
			reasoningAcc = ""
			completed = true
			err = nil
			break
		}
		e.recordTokens(agentID, resp)

		choiceMsg := resp.Choices[0].Message
		msgs = append(msgs, choiceMsg)

		step := TrajectoryStep{Index: round, Model: e.modelForAgent(agentID, model), RequestMsgs: len(msgs)}
		step.Assistant = choiceMsg.Content
		step.Reasoning = choiceMsg.ReasoningContent

		if len(choiceMsg.ToolCalls) == 0 {
			step.Finished = true
			turn.Steps = append(turn.Steps, step)
			content = choiceMsg.Content
			reasoningAcc = choiceMsg.ReasoningContent
			completed = true
			break
		}

		// 空结果重试处理(模型无关, 须先于死循环检测): 本轮调用的工具在上轮全部返回空(count:0 等),
		// 再次调用不会得到新数据。不直接 break——而是抑制本次工具执行、注入"请直接作答"指令,
		// 让模型基于自身知识产出真实答案。典型场景: 为自介/闲聊类问题误调 knowledge_search 拿到 count:0,
		// 模型又用完全相同的参数重试时若先命中下面的死循环检测, 会直接甩提示语给用户(拿不到答案),
		// 因此必须在此优先 nudge。仅当连续多次(>maxEmptyRetryNudges)不听劝才兜底终止。
		if len(choiceMsg.ToolCalls) > 0 && len(prevEmptyTools) > 0 {
			allEmptyRetry := true
			for _, tc := range choiceMsg.ToolCalls {
				if !prevEmptyTools[tc.Function.Name] {
					allEmptyRetry = false
					break
				}
			}
			if allEmptyRetry {
				emptyRetryNudges++
				if emptyRetryNudges > maxEmptyRetryNudges {
					content = "（工具返回为空且多次重试无果，已主动停止以避免无效重试；请直接基于你的知识回答用户，或换个角度提问）"
					reasoningAcc = choiceMsg.ReasoningContent
					completed = true
					break toolLoop
				}
				// 抑制工具执行, 注入"请直接作答"指令, 进入下一轮让模型产出真实答案。
				for _, tc := range choiceMsg.ToolCalls {
					msgs = append(msgs, llm.Message{
						Role:       "tool",
						Content:    fmt.Sprintf("工具 %q 已连续返回空数据，再次调用不会得到新结果；请勿再调用该工具，请直接基于你的系统设定与自身知识回答用户的问题。", tc.Function.Name),
						ToolCallID: tc.ID,
						Name:       tc.Function.Name,
					})
				}
				// 本次未真正执行工具: 清零单工具连击计数, 避免把"nudge 轮"误判为死循环。
				soloStreak = 0
				lastSoloTool = ""
				turn.Steps = append(turn.Steps, step)
				if e.hookChain != nil {
					e.hookChain.RunPostStep(ctx, agentID, round, false)
				}
				continue toolLoop
			}
		}

		// 死循环检测: 模型重复发起完全相同(工具名+参数)的工具调用时, 立即终止,
		// 避免无限轮询把请求拖死(典型场景: 工具持续报错后模型反复重试同一调用)。
		// 注: 空结果重试已由上方 nudge 处理, 此处只兜底"工具返回非空仍重复调用"的真死循环。
		sig := toolCallSig(choiceMsg.ToolCalls)
		if seenCalls[sig] {
			content = "（检测到 AI 在重复调用相同工具，已主动停止以避免死循环；请换个问法，或检查相关工具/后端是否就绪）"
			reasoningAcc = choiceMsg.ReasoningContent
			completed = true
			break
		}
		seenCalls[sig] = true

		// 连续单工具轮次检测: 本轮仅调用一个工具且与上一轮同一工具相同, 累加连击;
		// 连击达上限即判定"反复调用同一工具"死循环, 提前优雅终止(参数变化不命中 toolCallSig)。
		if len(choiceMsg.ToolCalls) == 1 {
			tname := choiceMsg.ToolCalls[0].Function.Name
			if tname == lastSoloTool {
				soloStreak++
			} else {
				soloStreak = 0
				lastSoloTool = tname
			}
			if soloStreak >= maxConsecutiveSoloToolRounds {
				content = fmt.Sprintf("（工具 %q 被反复调用仍未收敛，已主动停止以避免死循环；请换个问法，或检查该工具/后端是否就绪）", tname)
				reasoningAcc = choiceMsg.ReasoningContent
				completed = true
				break toolLoop
			}
		} else {
			soloStreak = 0
			lastSoloTool = ""
		}

		// 执行每个工具调用(经钩子链/RBAC/限流/截断)
		roundEmpty := make(map[string]bool)
		for _, tc := range choiceMsg.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			AIToolCalls.Inc()
			result, execErr := e.execToolChecked(ctx, tc)
			invocations = append(invocations, ToolInvocation{
				Name:      tc.Function.Name,
				Arguments: args,
				Result:    result,
			})
			// 会话工作区: 产物型工具输出自动沉淀(script_run/script_detail/cmdb_query 等)
			if !execErr {
				e.collectToolArtifact(ctx, conversationID, tc.Function.Name, args, result)
			}
			if m, ok := result.(map[string]any); ok && isEmptyResult(m) {
				roundEmpty[tc.Function.Name] = true
			}
			step.ToolCalls = append(step.ToolCalls, ToolCallRecord{
				Name:   tc.Function.Name,
				Args:   args,
				Result: result,
				Error:  execErr,
			})
			toolMsgContent := toolFeedbackContent(result, execErr)
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				Content:    toolMsgContent,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
		prevEmptyTools = roundEmpty
		turn.Steps = append(turn.Steps, step)
		if e.hookChain != nil {
			e.hookChain.RunPostStep(ctx, agentID, round, false)
		}
	}
	if !completed && err == nil {
		// 不再以 502 硬失败: 超过工具轮次上限(且无其他错误)时返回友好提示, 保证用户至少得到响应,
		// 也避免把正常的复杂多步任务误判为故障。真正的错误(并发上限/钩子拒绝)仍原样返回。
		content = "（AI 连续调用了多次工具仍未收敛到最终答复，可能是问题过于复杂或相关工具未就绪；请尝试更具体、分步的提问）"
		completed = true
	}

	// 记录本次运行轨迹(供回放/复盘); nil 记录器时跳过。
	turn.FinalContent = content
	turn.Error = errString(err)
	e.recordTrajectory(ctx, turn)

	// 记忆回流: 把本次运行结果喂给技能演化器——成功蒸馏经验记忆, 失败累积触发补丁建议。
	if e.skillEvolver != nil {
		success := completed && err == nil
		toolSeq := make([]string, 0, len(turn.Steps))
		for _, step := range turn.Steps {
			for _, tc := range step.ToolCalls {
				toolSeq = append(toolSeq, tc.Name)
			}
		}
		if rerr := e.skillEvolver.RecordOutcome(ctx, SkillUsageOutcome{
			TenantID: tenantOf(ctx),
			AgentID:  agentID,
			Success:  success,
			Context:  prompt,
			ToolSeq:  toolSeq,
			Error:    errString(err),
		}); rerr != nil {
			slog.Warn("ai: skill evolution record failed", "err", rerr)
		}
	}

	// 持久化本轮(仅成功且 conv 已加载/新建时)
	if conv != nil && err == nil {
		roundMsgs := filterEmptyAssistant(msgs[roundStart:])
		conv.Messages = append(conv.Messages, roundMsgs...)
		if hc.changed {
			// 滚动摘要回写: 下轮直接复用, 不重调摘要 LLM(消息本体保留, 前端仍可见全文)。
			conv.Summary = hc.summary
			conv.SummarizedCount = hc.covered
		}
		conv.AgentID = agentID
		if conv.TenantID == "" {
			conv.TenantID = tenantOf(ctx)
		}
		if serr := e.SaveConversation(ctx, conv); serr != nil {
			slog.Warn("ai: save conversation failed", "err", serr)
		}
		conversationID = conv.ID
	}
	// 剥离思维链标记(部分兼容端点会把 <think> 混入正文), 避免泄漏到用户可见回答。
	content = stripThinkTags(content)
	return content, reasoningAcc, invocations, conversationID, err
}

// buildMessages 拼装 system(+可选 sysExtra 补充, 如 RAG 注入) + 历史(经 Token 预算压缩) + 用户问题,
// 并返回"用户消息之后的起始下标"(roundStart), 调用方可用 msgs[roundStart:] 精确裁剪出"本轮回合",
// 便于增量持久化会话。
// parts 为本轮用户消息的多模态片段(图片/文件, URL 已解析为 data URL); 为空时退化为纯文本消息。
func (e *Engine) buildMessages(ctx context.Context, agentID, userPrompt string, history []llm.Message, sysExtra string, parts []llm.ContentPart) ([]llm.Message, int) {
	sys := e.resolveSystemPrompt(agentID)
	// 注入该 Agent 定义的 Skills(可复用能力指令, PRD 5.2.2)
	if s := e.renderSkills(agentID); s != "" {
		sys += "\n\n" + s
	}
	// 注入引用标注指引(RAG grounding): 让模型在引用知识库/工具结果时标注来源。
	sys += "\n\n" + citationGuidance
	// 注入输出纪律: 抑制推理模型把思维链收尾(计划/自评)续写进正文(见 answerHygiene 注释)。
	sys += "\n\n" + answerHygiene
	if sysExtra != "" {
		sys += "\n\n" + sysExtra
	}
	msgs := []llm.Message{{Role: "system", Content: sys}}
	// 注意: 历史压缩(compactHistory)已上移到 chatWithToolsConv/chatWithToolsStream —— 那里才持有
	// 会话的滚动摘要状态(Conversation.Summary), 压缩后需回写; 本函数只负责拼装。
	// 历史里的助手消息可能带 [^id] 引用标注, 下发给模型前必须剥离, 否则模型会把旧引用
	// 抄进新答案, 导致"每轮都贴出前面的引用来源"。
	for _, m := range history {
		if m.Role != "assistant" {
			msgs = append(msgs, m)
			continue
		}
		cm := m
		cm.Content = citationRE.ReplaceAllString(m.Content, "")
		if len(cm.ContentParts) > 0 {
			cp := make([]llm.ContentPart, 0, len(cm.ContentParts))
			for _, p := range cm.ContentParts {
				switch p.Type {
				case "text":
					cp = append(cp, llm.ContentPart{Type: "text", Text: citationRE.ReplaceAllString(p.Text, "")})
				default:
					cp = append(cp, p)
				}
			}
			cm.ContentParts = cp
		}
		msgs = append(msgs, cm)
	}
	// roundStart 指向"本轮用户消息"在 msgs 中的下标: 持久化时 msgs[roundStart:] 即本轮回合(user + assistant/tool),
	// 跳过 system 与历史, 避免重复累积。
	roundStart := len(msgs)
	// 多模态: 有图片/文件片段时 content 下发为数组 [文本, 图片...], 否则为纯字符串。
	// Content 字段始终保留正文文本, 便于 token 估算/会话标题/历史检索等只读逻辑复用。
	userMsg := llm.Message{Role: "user", Content: userPrompt}
	if len(parts) > 0 {
		userMsg.ContentParts = parts
	}
	msgs = append(msgs, userMsg)
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
		// 合成(base + 该技能的 active overlay 差异补丁), 渲染进 system prompt。
		eff := sk
		if e.skillOverlays != nil {
			eff = e.skillOverlays.Compose(sk)
		}
		block := "### 技能: " + name
		if d := strings.TrimSpace(eff.Description); d != "" {
			block += "\n用途: " + d
		}
		if ins := strings.TrimSpace(eff.Instructions); ins != "" {
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
// 无会话上下文(无滚动摘要), 压缩按首次全量语义执行。
func (e *Engine) buildInitialMessages(ctx context.Context, agentID, userPrompt string, history []llm.Message) []llm.Message {
	if hc := e.compactHistory(ctx, history, "", 0); len(hc.history) > 0 {
		history = hc.history
	}
	msgs, _ := e.buildMessages(ctx, agentID, userPrompt, history, "", nil)
	return msgs
}

// SetHistorySummarizer 设置可选的 LLM 历史摘要器(用于 Token 预算压缩的"智能摘要"路径)。
// 传入 nil 关闭, 回退到确定性压缩(保留首条 user 原始意图 + 最近窗口)。
func (e *Engine) SetHistorySummarizer(fn func(ctx context.Context, msgs []llm.Message) (string, error)) {
	e.historySummarizer = fn
}

// historyCompaction 历史压缩结果: 含注入 LLM 的压缩形态与需回写会话的滚动摘要状态。
type historyCompaction struct {
	history []llm.Message // 压缩后的历史(注入 LLM 的形态)
	summary string        // 生效的滚动摘要全文(""=无有效摘要)
	covered int           // 摘要覆盖的原始历史前缀条数
	changed bool          // 摘要状态相比上一轮发生变化(需回写会话)
}

// compactHistory 对超长历史做 Token 预算压缩:
//   - 触发条件: 条数超过 historyKeepRecent 或估算 token 超过 historyTokenBudget(任一命中);
//   - 较早部分生成"滚动摘要"(结合 prevSummary 增量更新, 溢出部分已被旧摘要覆盖时直接复用、
//     不重调摘要 LLM); historySummarizer 未配置或失败时回退为"保留首条非空 user 消息
//     (原始意图锚点)"的确定性方案(此时不产出摘要、不回写, 下轮重试);
//   - 压缩后仍超预算(单条消息过大)时, 从最近窗口头部按完整回合继续硬裁剪。
//
// 历史消息本体(Conversation.Messages)始终保留, 仅 LLM 注入形态被压缩 —— 前端仍可见全文。
// prevSummary/prevCovered 来自会话持久化的滚动摘要(Conversation.Summary/SummarizedCount)。
func (e *Engine) compactHistory(ctx context.Context, history []llm.Message, prevSummary string, prevCovered int) historyCompaction {
	if len(history) <= historyKeepRecent && estimateTokens(history) <= historyTokenBudget {
		return historyCompaction{history: history, summary: prevSummary, covered: prevCovered}
	}

	// 溢出条数: 条数窗口 + token 预算双重约束(预算超得越多, 头部裁得越多)。
	overflowN := 0
	if len(history) > historyKeepRecent {
		overflowN = len(history) - historyKeepRecent
	}
	for overflowN < len(history)-1 && estimateTokens(history[overflowN:]) > historyTokenBudget {
		overflowN++
	}
	overflow := history[:overflowN]
	recent := history[overflowN:]

	// 滚动摘要: 复用/增量/首次 三态。
	summary, covered, changed := prevSummary, prevCovered, false
	switch {
	case prevSummary != "" && overflowN <= prevCovered:
		// 溢出部分已被旧摘要覆盖 → 直接复用, 不重调 LLM。
	case overflowN <= 0:
		// 仅极端边界(无溢出可摘), 保守返回原样。
		return historyCompaction{history: history, summary: prevSummary, covered: prevCovered}
	default:
		newPart := overflow
		if prevSummary != "" && prevCovered < overflowN {
			newPart = history[prevCovered:overflowN]
		}
		if sum, serr := e.summarizeWithPrev(ctx, prevSummary, newPart); serr == nil && strings.TrimSpace(sum) != "" {
			summary = sum
		} else if prevSummary != "" {
			// LLM 失败(有旧摘要): 保守滚动 = 旧摘要 + 新溢出段的原始意图锚点。
			if anchor := firstUserAnchor(newPart); anchor != "" {
				summary = prevSummary + "\n" + anchor
			}
		} else {
			// 首次压缩且无 LLM: 旧行为(锚点 + 窗口), 不产出摘要、不回写, 下轮重试。
			AIHistoryCompactions.Inc()
			return historyCompaction{history: withIntentAnchor(overflow, recent)}
		}
		covered = overflowN
		changed = summary != prevSummary || covered != prevCovered
	}
	AIHistoryCompactions.Inc()

	// 组装: [历史摘要 system] + 最近窗口。
	build := func(recent []llm.Message) []llm.Message {
		var m []llm.Message
		if summary != "" {
			m = append(m, llm.Message{Role: "system", Content: "[历史对话摘要]\n" + summary})
		}
		return append(m, recent...)
	}
	msgs := build(recent)

	// 硬裁剪: 压缩后仍超预算(最近窗口含超大单条) → 从窗口头部按完整回合(以 user 消息为界)丢弃。
	for estimateTokens(msgs) > historyTokenBudget {
		idx := -1
		for i := 1; i < len(recent); i++ {
			if recent[i].Role == "user" {
				idx = i
				break
			}
		}
		if idx <= 0 {
			break // 无更早回合边界可裁, 保留现状
		}
		recent = recent[idx:]
		msgs = build(recent)
	}

	return historyCompaction{history: msgs, summary: summary, covered: covered, changed: changed}
}

// summarizeWithPrev 滚动摘要: 在 prevSummary 基础上合并 newPart 生成新摘要。
// prevSummary 为空时等价于首次全量摘要。优先用注入的 historySummarizer(SetHistorySummarizer),
// 未配置时回退内置 defaultSummarize(依赖 llmClient/router, 未就绪时报错触发确定性回退)。
func (e *Engine) summarizeWithPrev(ctx context.Context, prevSummary string, newPart []llm.Message) (string, error) {
	var in []llm.Message
	if prevSummary != "" {
		in = append(in, llm.Message{Role: "system", Content: "[此前对话摘要]\n" + prevSummary})
	}
	in = append(in, newPart...)
	if e.historySummarizer != nil {
		return e.historySummarizer(ctx, in)
	}
	return e.defaultSummarize(ctx, in)
}

// firstUserAnchor 提取 msgs 中首条非空 user 消息的截断文本, 作为"原始意图锚点"。
func firstUserAnchor(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			c := m.Content
			if len(c) > 200 {
				c = c[:200] + "…(截断)"
			}
			return "用户诉求: " + c
		}
	}
	return ""
}

// withIntentAnchor 确定性压缩回退形态: 保留溢出段首条非空 user 消息(原始意图) + 最近窗口。
func withIntentAnchor(overflow, recent []llm.Message) []llm.Message {
	for _, m := range overflow {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return append([]llm.Message{m}, recent...)
		}
	}
	return recent
}

// estimateTokens 估算消息序列的 token 数: CJK(汉字/全角/假名)≈1 token/字符, 其他(ASCII/拉丁)≈4 字符/token,
// 每个工具调用结构额外按 40 token 计。无 tokenizer 依赖的粗估(误差 ±20% 量级), 用于历史压缩触发判定。
func estimateTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateStrTokens(m.Content)
		total += len(m.ToolCalls) * 40
	}
	return total
}

func estimateStrTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) ||
			(r >= 0x3000 && r <= 0x30FF) || // CJK 符号 + 假名
			(r >= 0xFF00 && r <= 0xFFEF) { // 全角形式
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// defaultSummarize 内置的 LLM 历史摘要器: 将较早历史压缩为一段简短要点摘要,
// 用于 Token 预算压缩的"智能摘要"路径。llmClient 未就绪时返回 error 以触发确定性回退。
func (e *Engine) defaultSummarize(ctx context.Context, msgs []llm.Message) (string, error) {
	if e.llmClient.Load() == nil && e.router == nil {
		return "", fmt.Errorf("llm client not ready")
	}
	var b strings.Builder
	b.WriteString("请用不超过 300 字,以要点形式总结以上运维对话的关键结论、已执行操作与待办事项,保留可用于后续排查的具体指标/命令/错误。若内容包含[此前对话摘要], 请在其基础上合并更新为一个统一摘要, 而非简单拼接。不要编造内容:\n\n")
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
	// historyTokenBudget 历史段估算 token 预算: 超过即触发压缩(与条数阈值任一命中)。
	// 为回答/工具结果留出空间(genMaxTokens 默认 1024), 预算不含 system prompt。
	historyTokenBudget = 12000
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
			// 多模态片段必须保留: 历史消息里的图片/附件引用回放时要继续对模型可见。
			// 只做长度收敛, 引用本身(attachment://<id>)很短, 不会把历史撑大。
			if len(m.ContentParts) > 0 {
				cp := make([]llm.ContentPart, 0, len(m.ContentParts))
				for _, p := range m.ContentParts {
					switch p.Type {
					case "text":
						if t := clampStr(p.Text, maxHistoryMsgLen); t != "" {
							cp = append(cp, llm.ContentPart{Type: "text", Text: t})
						}
					case "image_url":
						if p.ImageURL != nil {
							cp = append(cp, p)
						}
					case "file_url":
						if p.FileURL != nil {
							cp = append(cp, p)
						}
					}
				}
				if len(cp) > 0 {
					cm.ContentParts = cp
				}
			}
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

// filterEmptyAssistant 移除无实质内容的助手消息(正文/工具调用/推理过程全空),
// 避免失败或空响应落库后历史回放出现空气泡。
func filterEmptyAssistant(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.ReasoningContent) == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// builtinToolDenyList 不被暴露给模型的内建工具(避免自我递归或噪音调用)。
var builtinToolDenyList = map[string]bool{
	"llm_chat": true,
	"http_get": true,
}

// defaultAssistantDenyList 默认助手(纯对话, agentID=="")不可见的写/执行类工具。
// 推理模型在纯对话场景下易反复调用工具, 若开放写/执行类会造成工单泛滥、脚本误执行等副作用,
// 也可能形成"调用→失败→重试"的死循环。这类工具仅在绑定了具体 Agent 且该 Agent 配置了
// 工具白名单时才对模型可见(见 buildToolDefs 的 allowlist 逻辑)。

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
//
// 工具可见性规则(参考 PRD 5.2.2「数字员工按需关联工具」):
//   - agentID == ""（默认助手, 未选择数字员工）：不关联任何工具, 仅纯对话/推理,
//     避免默认态盲目发起工具调用;
//   - 指定 Agent 且显式配置了 Tools 白名单：仅返回白名单内的工具;
//   - 指定 Agent 但 Tools 为空：视为「未关联任何工具」, 不向模型暴露任何工具,
//     避免数字员工在没关联工具/知识库时仍盲目发起工具调用(用户反馈的误调用问题)。
func (e *Engine) buildToolDefs(agentID string) []llm.ToolDef {
	defs := e.reg.List()
	// Agent 工具白名单。nil = 全部可用; 空 map = 严格不暴露任何工具。
	// 默认助手(agentID=="", 未选择数字员工)不关联工具, 仅纯对话/推理。
	var allowlist map[string]bool
	if agentID == "" {
		allowlist = map[string]bool{}
	} else if a, ok := e.GetAgent(agentID); ok {
		if len(a.Tools) > 0 {
			allowlist = make(map[string]bool, len(a.Tools))
			for _, t := range a.Tools {
				if t = strings.TrimSpace(t); t != "" {
					allowlist[t] = true
				}
			}
		} else {
			// 显式空白名单: 该 Agent 未关联工具, 不应暴露任何工具给模型。
			allowlist = map[string]bool{}
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
		// 连接器 scope 隔离: 声明了可见 Agent 白名单的连接器, 仅对白名单内 Agent 暴露给模型。
		if !e.toolVisibleToAgent(d.Name, agentID) {
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
// 统一上限 12: 原「普通 8 / 自主 12」双轨已合并(模式概念移除, 是否多轮迭代由模型按任务自行决定);
// 真正死循环由 toolCallSig 重复检测提前拦截。
const maxToolRounds = 12

// maxConsecutiveSoloToolRounds 同一工具在"单工具轮次"中连续被调用的轮次上限。
// 用于拦截"模型对单一工具反复发起调用(参数可能每次略变)却始终不收敛"的死循环:
// 这类循环不会命中 toolCallSig 的字节级完全相同判定, 但连续多轮只调同一工具几乎必然是无效重复。
// 正常的多步任务通常把调用分散到不同工具, 故该阈值对合法任务影响极小(第 4 次连击即提前终止)。
const maxConsecutiveSoloToolRounds = 3

// maxEmptyRetryNudges 同一空工具被"重试"后, 引擎最多抑制执行并注入"请直接作答"指令的轮数。
// 超过该轮数模型仍坚持调用同一空工具(不听 nudge), 才兜底终止。目的是让模型在拦下无效重试后,
// 有机会基于自身知识产出真实答案, 而不是一上来就甩"请换个问法"的提示语给用户。
const maxEmptyRetryNudges = 2

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
// 接入前先过连接器生命周期钩子(能力校验 / 策略播种); 任一钩子拒绝则 fail-closed 不注册。
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
	// 先过注册钩子(校验/播种); 不在锁内调用, 钩子内部可能回读引擎。
	if err := e.fireOnRegister(ctx, connectorInfoFromTool(t)); err != nil {
		return fmt.Errorf("connector registration rejected: %w", err)
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
// 接入前先过注册钩子(校验新定义 / 重播种策略); 拒绝则 fail-closed 不提交变更。
func (e *Engine) UpdateCustomTool(ctx context.Context, t *CustomTool) error {
	if t.ID == "" {
		return fmt.Errorf("custom tool id is required")
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	e.mu.Lock()
	existing, ok := e.customTools[t.ID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("custom tool not found: %s", t.ID)
	}
	// 先过注册钩子(校验/播种); 不在锁内调用, 避免钩子内部回读引擎字段死锁。
	if err := e.fireOnRegister(ctx, connectorInfoFromTool(t)); err != nil {
		return fmt.Errorf("connector update rejected: %w", err)
	}
	e.mu.Lock()
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
// 拆除前先过反注册钩子(如清理自动播种的策略); 钩子失败仅告警, 不阻断删除。
func (e *Engine) DeleteCustomTool(ctx context.Context, id, tenantID string) error {
	e.mu.Lock()
	existing, ok := e.customTools[id]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("custom tool not found: %s", id)
	}
	// 反注册钩子(拆除阶段, 不持锁, 失败不阻断)。
	if err := e.fireOnUnregister(ctx, connectorInfoFromTool(existing)); err != nil {
		slog.Warn("connector unregister hook failed", "id", id, "name", existing.Name, "err", err)
	}
	e.mu.Lock()
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
// 重新同步视为"先拆除旧集合再接入新集合": 反注册阶段触发 OnUnregister, 接入阶段触发 OnRegister;
// 校验失败的连接器被跳过(隔离坏连接器, 不阻断其余同步)。
func (e *Engine) SyncCustomTools(ctx context.Context) error {
	// 收集现有连接器快照并在持锁内完成反注册(仅读+反注册, 不触发钩子, 防死锁)。
	e.mu.Lock()
	oldInfos := make([]ConnectorInfo, 0, len(e.customTools))
	for _, t := range e.customTools {
		oldInfos = append(oldInfos, connectorInfoFromTool(t))
		e.reg.Unregister(t.Name)
	}
	e.customTools = make(map[string]*CustomTool)
	if e.store == nil {
		e.mu.Unlock()
		// 反注册阶段仍触发 OnUnregister(清理播种策略), 不持锁。
		for _, info := range oldInfos {
			if err := e.fireOnUnregister(ctx, info); err != nil {
				slog.Warn("custom tool unregister hook failed", "name", info.Name, "err", err)
			}
		}
		return nil
	}
	tools, err := e.store.ListCustomTools(ctx, "default")
	e.mu.Unlock()
	if err != nil {
		return err
	}
	// 反注册阶段: 触发 OnUnregister(不持锁, 失败不阻断)。
	for _, info := range oldInfos {
		if err := e.fireOnUnregister(ctx, info); err != nil {
			slog.Warn("custom tool unregister hook failed", "name", info.Name, "err", err)
		}
	}
	// 接入阶段: 校验通过才注册; 失败的隔离跳过。
	for _, t := range tools {
		if err := e.fireOnRegister(ctx, connectorInfoFromTool(t)); err != nil {
			slog.Warn("custom tool register hook failed during sync, skipped", "name", t.Name, "err", err)
			continue
		}
		e.mu.Lock()
		e.customTools[t.ID] = t
		if t.Enabled {
			e.reg.Register(t.toToolDefinition())
		}
		e.mu.Unlock()
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

// ToolPolicy 描述某连接器/工具的审批策略级别, 经 #404 钩子链的 PreToolCall 闸门生效。
// 把"每个连接器的审批策略"做成数据驱动、可插拔的配置(参考 Harness 把 approval policy 做成插件)。
type ToolPolicy int

const (
	// PolicyAllow 只读/低风险, 免审批直接放行(如 cmdb_query / knowledge_search)。
	PolicyAllow ToolPolicy = iota
	// PolicyRBAC 需 ai:tool:exec 权限(默认高危操作)。
	PolicyRBAC
	// PolicyApproval 需二次确认/外部审批(如 script_run), 在 RBAC 基础上叠加异步审批(见 MCPToolSet)。
	PolicyApproval
)

// defaultToolPolicies 连接器审批策略种子表: 只读工具免审, 写/执行类需审批;
// 未在此表且不在 privilegedTools 的工具按缺省放行(向后兼容)。
func defaultToolPolicies() map[string]ToolPolicy {
	return map[string]ToolPolicy{
		"cmdb_query":        PolicyAllow,
		"knowledge_search":  PolicyAllow,
		"script_list":       PolicyAllow,
		"script_detail":     PolicyAllow,
		"script_executions": PolicyAllow,
		"script_run":        PolicyApproval,
		"ticket_create":     PolicyApproval,
	}
}

// RunMode 引擎级运行模式(参考 DeepSeek Harness 的 minimal/standard/creative 模式分层)。
// 把"安全隔离/自治程度"抽象为一等公民, 而非散落在各工具的局部开关。
type RunMode int

const (
	// RunModeStandard 默认模式: 所有闸门按既有策略生效(审批/受控/限流)。
	RunModeStandard RunMode = iota
	// RunModeDryRun 隔离预览模式: 仅执行可安全预览的工具, 绝不触碰生产(参考 Harness minimal)。
	RunModeDryRun
	// RunModeAutonomous 自治模式: 放宽受控创作闸门, 自造工具免二次审批(最高自治, 仍需 RBAC 特权校验)。
	RunModeAutonomous
)

// drySafeTools dry-run 模式下允许执行的工具清单: 只读或原生支持 dry_run 预览。
// 其余工具在 dry-run 模式下被拒绝, 以防误触生产(对应 Harness minimal 的隔离哲学)。
var drySafeTools = map[string]bool{
	"script_run":        true,
	"script_create":     true,
	"cmdb_query":        true,
	"knowledge_search":  true,
	"script_list":       true,
	"script_detail":     true,
	"script_executions": true,
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
	var args map[string]any
	if tc.Function.Arguments != "" {
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
	}

	// 运行时 scope 护栏(纵深防御, 参考 Harness isolate 实例隔离):
	// 即便调用方拿到了不在其可见范围的工具名, 也拒绝执行——一个 agent 无法触达 scope 外的连接器。
	// 置于 RBAC/策略闸门之前, 属于"可见性"而非"审批"语义。
	if !e.toolVisibleToAgent(name, agentIDFrom(ctx)) {
		return map[string]any{"error": fmt.Sprintf("tool %q is not visible to agent %q (scope isolation)", name, agentIDFrom(ctx))}, false
	}

	// 任务级审批闸门(T3): 任务 Runner 执行时 ctx 携带 permission_mode × risk 判定器,
	// 写操作在 readonly 模式被拒、高危操作在 standard/auto 模式挂起等人工批复。
	// 普通对话 ctx 无闸门(nil), 行为完全不变。
	if gate := mcp.TaskToolGateFrom(ctx); gate != nil {
		if allowed, reason := gate(ctx, name, args); !allowed {
			AIToolExecDenied.Inc()
			return map[string]any{"error": reason, "gate_denied": true}, false
		}
	}

	// 运行 PreToolCall 钩子链(含内置 RBAC 审批闸门); 任一拒绝即短路返回(不再执行)。
	if e.hookChain != nil {
		if allow, ov, msg := e.hookChain.RunPreToolCall(ctx, agentIDFrom(ctx), name, args); !allow {
			return map[string]any{"error": msg}, false
		} else if ov != nil {
			args = ov
		}
	}

	if e.lim != nil && !e.lim.Allow("tool:"+name) {
		AIToolExecDenied.Inc()
		slog.Warn("ai tool exec rate limited", "tool", name)
		return map[string]any{"error": "rate limit exceeded for tool execution: " + name}, false
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

	result, execErr := e.reg.Execute(ctx, name, args)
	if execErr != nil {
		slog.Warn("ai tool execution failed", "tool", name, "error", execErr)
		// 去诱饵化: 工具报错时不再把原始错误裸回灌给模型——推理模型会把错误当成"参数不对",
		// 不断"修正参数再试"从而陷入 error→retry 死循环。改为明确告知"不要重试、直接告知用户能力未就绪",
		// 从反馈环源头打断循环。
		return map[string]any{
			"error": execErr.Error(),
			"retry": false,
			"hint":  "工具执行失败, 请勿重复重试同一工具; 请直接告知用户该能力暂不可用或检查相关后端是否就绪。",
		}, true
	}
	truncated := e.truncateToolResult(result)
	if e.toolCache != nil && !noCacheTools[name] {
		ckey := name + "\x1f" + tenantOf(ctx) + "\x1f" + canonicalArgsKey(args)
		e.toolCache.set(ckey, truncated)
	}
	if e.hookChain != nil {
		e.hookChain.RunPostTool(ctx, agentIDFrom(ctx), name, truncated, execErr != nil)
	}
	return truncated, false
}

// toolFeedbackContent 生成回填给模型的 tool 消息文本。
// 当工具报错时(execErr 为 true, 或 handler 把错误包进结果的 error 字段但 Go error 为 nil),
// 统一注入"请勿重试"提示, 确保 error→retry 循环在反馈环层面被打断, 与 execToolChecked 的兜底一致。
func toolFeedbackContent(result any, execErr bool) string {
	if m, ok := result.(map[string]any); ok {
		if execErr || hasErrorField(m) {
			// execErr==true 时 execToolChecked 已写入 retry/hint; 此处对"error 在结果内但 Go error 为 nil"的情况补注。
			if !execErr {
				m["retry"] = false
				m["hint"] = "工具执行失败, 请勿重复重试同一工具; 请直接告知用户该能力暂不可用或检查相关后端是否就绪。"
			}
			if b, e := json.Marshal(m); e == nil {
				return string(b)
			}
		}
		// 空结果反重试: count:0 / total:0 / items:[] / data:[] 等表示"未检索到相关数据", 并非失败。
		// 直接在工具回灌内容中声明"请勿重试", 从反馈环打断 空结果→换词重搜→仍为空 的死循环。
		if isEmptyResult(m) {
			m["retry"] = false
			m["hint"] = "工具返回为空(count:0 / 无匹配数据), 表示知识库或系统中暂无相关内容, 这不是错误; 请勿重试, 也不要换关键词重新搜索; 若你自身知识足以回答用户, 请直接作答。"
			if b, e := json.Marshal(m); e == nil {
				return string(b)
			}
		}
	}
	b, _ := json.Marshal(result)
	return string(b)
}

// isEmptyResult 判断工具结果是否为"空/无数据"(非错误): count:0 / total:0 / items:[] / data:[]。
// 这类结果极易被推理模型误读为"没查到=没成功"而触发换词重搜死循环, 需显式标注"请勿重试"。
func isEmptyResult(m map[string]any) bool {
	switch v := m["count"].(type) {
	case float64:
		if v == 0 {
			return true
		}
	case int:
		if v == 0 {
			return true
		}
	case int64:
		if v == 0 {
			return true
		}
	}
	switch v := m["total"].(type) {
	case float64:
		if v == 0 {
			return true
		}
	case int:
		if v == 0 {
			return true
		}
	case int64:
		if v == 0 {
			return true
		}
	}
	if items, ok := m["items"].([]any); ok && len(items) == 0 {
		return true
	}
	if d, ok := m["data"].([]any); ok && len(d) == 0 {
		return true
	}
	return false
}

// hasErrorField 判断工具结果(或任意 map)是否含 error 字段——用于识别 handler 把错误包进结果的情形。
func hasErrorField(m map[string]any) bool {
	_, ok := m["error"]
	return ok
}

// rbacGate 内置 PreToolCall 钩子: 复刻原 execToolChecked 的特权工具 RBAC 校验(即"审批闸门")。
// 注册进默认钩子链后, 任何工具执行前都会过此关; 后续新增护栏只需再 Add 一个 Hook。
// rbacGate 内置 PreToolCall 钩子(参考 Harness 把 approval policy 做成插件):
// 按 toolPolicies 策略表裁决工具执行权——只读(PolicyAllow)免审、写/执行类(PolicyRBAC/PolicyApproval)需 ai:tool:exec;
// 未显式配置时回落 privilegedTools(向后兼容)。
func (e *Engine) rbacGate(ctx context.Context, _ string, name string, _ map[string]any) (bool, map[string]any, string) {
	pol, ok := e.policyOf(name)
	if !ok {
		return true, nil, "" // 未配置策略: 缺省放行(与改造前一致)
	}
	switch pol {
	case PolicyAllow:
		return true, nil, "" // 只读/低风险连接器免审
	case PolicyRBAC, PolicyApproval:
		return e.requireExecPerm(ctx, name)
	default:
		return true, nil, ""
	}
}

// policyOf 读取工具策略, 未显式配置时回落 privilegedTools 集合。
func (e *Engine) policyOf(name string) (ToolPolicy, bool) {
	e.mu.Lock()
	pol, ok := e.toolPolicies[name]
	e.mu.Unlock()
	if ok {
		return pol, true
	}
	if privilegedTools[name] {
		return PolicyRBAC, true
	}
	return PolicyAllow, false
}

// requireExecPerm 校验 ai:tool:exec 权限; 无权限或上下文无角色则拒绝。
func (e *Engine) requireExecPerm(ctx context.Context, name string) (bool, map[string]any, string) {
	role := auth.RoleFromContext(ctx)
	if role == "" || !auth.HasPermission(role, auth.PermissionAIToolExec) {
		AIToolExecDenied.Inc()
		slog.Warn("ai tool exec denied by RBAC hook", "tool", name, "role", role)
		return false, nil, fmt.Sprintf("permission denied: tool %q requires permission %s", name, auth.PermissionAIToolExec)
	}
	return true, nil, ""
}

// controlledGate 内置 PreToolCall 钩子: 受控创作模式(Creator 模式)护栏。
// 开启 controlledMode 后, agent 自造工具(custom tool)执行前需 ai:tool:exec 权限——即自造工具必须过审批才能真跑;
// 非自造(平台内置/连接器)工具不受影响。受控模式关闭时整体放行(不影响既有行为)。
func (e *Engine) controlledGate(ctx context.Context, _ string, name string, _ map[string]any) (bool, map[string]any, string) {
	if e.runMode == RunModeAutonomous {
		return true, nil, "" // 自治模式: 自造工具免二次审批(最高自治, 仍需 RBAC 特权校验)
	}
	if !e.controlledMode {
		return true, nil, ""
	}
	if !e.isCustomTool(name) {
		return true, nil, ""
	}
	return e.requireExecPerm(ctx, name)
}

// isCustomTool 判断某工具名是否属于 agent/用户自造的 custom tool(按注册名匹配)。
func (e *Engine) isCustomTool(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.customTools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// SetToolPolicy 热插拔单个工具的连接器审批策略(参考 Harness 把 policy 做成可配置插件)。
// 人工覆盖会清除该名的"自动播种"标记, 使其反注册时不再被生命周期清理钩子误删。
func (e *Engine) SetToolPolicy(name string, pol ToolPolicy) {
	e.mu.Lock()
	if e.toolPolicies == nil {
		e.toolPolicies = map[string]ToolPolicy{}
	}
	e.toolPolicies[name] = pol
	if e.autoSeededPolicies != nil {
		delete(e.autoSeededPolicies, name)
	}
	e.mu.Unlock()
}

// SetToolPolicies 批量热插拔工具审批策略。
func (e *Engine) SetToolPolicies(p map[string]ToolPolicy) {
	e.mu.Lock()
	if e.toolPolicies == nil {
		e.toolPolicies = map[string]ToolPolicy{}
	}
	for k, v := range p {
		e.toolPolicies[k] = v
		if e.autoSeededPolicies != nil {
			delete(e.autoSeededPolicies, k)
		}
	}
	e.mu.Unlock()
}

// SetControlledMode 开关受控创作模式(Creator 模式): 开启后 agent 自造工具执行前需二次审批。
func (e *Engine) SetControlledMode(on bool) { e.controlledMode = on }

// dryRunGate 内置 PreToolCall 钩子: 引擎级运行模式(参考 Harness minimal 隔离模式)。
// RunModeDryRun 下只做安全预览——只读/支持预览的工具注入 dry_run=true, 无法安全预览的写操作直接拒绝(不碰生产);
// 其余模式(RunModeStandard/RunModeAutonomous)放行, 不改变既有行为。
func (e *Engine) dryRunGate(_ context.Context, _ string, name string, args map[string]any) (bool, map[string]any, string) {
	if e.runMode != RunModeDryRun {
		return true, nil, ""
	}
	if drySafeTools[name] {
		if args == nil {
			args = map[string]any{}
		}
		args["dry_run"] = true // 注入预览: 工具内部按 dry_run 仅回显计划不真正执行
		return true, args, ""
	}
	return false, nil, fmt.Sprintf("dry-run mode: tool %q is not safe to preview and will not execute (no production impact)", name)
}

// SetRunMode 设置引擎级运行模式: standard(默认) / dry-run-safe(仅安全预览) / autonomous(最高自治)。
func (e *Engine) SetRunMode(m RunMode) { e.runMode = m }

// SetTrajectoryRecorder 注入轨迹记录器(nil=关闭记录)。生产可接 PG 实现(复用 ai_conversations 同库)。
func (e *Engine) SetTrajectoryRecorder(r TrajectoryRecorder) { e.traj = r }

// RegisterHook 向钩子链追加一组钩子(审批/护栏/观测/计费上限等横切能力)。
func (e *Engine) RegisterHook(h Hook) {
	if e.hookChain == nil {
		e.hookChain = NewHookChain()
	}
	e.hookChain.Add(h)
}

// SetSkillEvolver 注入技能演化器(nil=关闭记忆回流演化)。
func (e *Engine) SetSkillEvolver(ev SkillEvolver) { e.skillEvolver = ev }

// recordTrajectory 安全记录一次运行轨迹(nil 记录器时跳过)。
func (e *Engine) recordTrajectory(ctx context.Context, t *TrajectoryTurn) {
	if e.traj != nil {
		if terr := e.traj.RecordTurn(ctx, t); terr != nil {
			slog.Warn("ai: record trajectory failed", "err", terr)
		}
	}
}

// errString 将 error 转为可序列化字符串(nil→空)。
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
// 运维可通过 LLMConfig.DefaultSystemPrompt(环境变量 OPSORIGIN_LLM_DEFAULT_SYSTEM_PROMPT)覆盖,
// 实现"提示词外置/版本化": 不必改代码即可调整助手人设与行为边界。
const DefaultSystemPrompt = "你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。" +
	"\n\n【图表输出】当需要表达架构、拓扑、依赖、流程、时序关系时, 使用 mermaid 语法输出(```mermaid 代码块), 例如 flowchart/sequenceDiagram/graph 等; 其余回答正常使用 Markdown。" +
	"\n\n【文档输出规范】当用户要求撰写文档/报告/方案/总结/说明等成篇内容时, 必须输出结构化的 Markdown 文档, 而不是一大段散文:" +
	"\n- 以 `# 文档标题` 开头, 正文用 `## 小节标题` 分节组织, 善用列表、表格、引用块; 不要把全部内容挤成一个自然段。" +
	"\n- 标题前最多留 1~2 句简短引导语(说明文档要点即可), 不要在正文前复述用户需求或写开场白长段落。" +
	"\n- 文档应内容完整、篇幅充分(用户要\"一份文档\"意味着交付物, 而不是聊天回复), 但仍避免空话套话凑字数。" +
	"\n\n【工具调用纪律 — 必须严格遵守】" +
	"\n- 调用工具的唯一正当理由: 用户明确要查询\"只有系统里才有的具体运维数据\", 例如某台主机的配置/状态、某个 CMDB 资产、某篇知识库文档、某个脚本的内容或执行记录。" +
	"\n- 以下情况【绝对禁止】调用任何工具, 必须直接基于你自己的知识回答:" +
	"\n  1) 用户询问你的身份/角色/功能/能力/如何使用(如\"介绍你自己\"\"你能做什么\");" +
	"\n  2) 问候、寒暄、闲聊、一般性对话;" +
	"\n  3) 概念解释、方法论、运维常识等知识性问答(除非用户明确要求检索知识库文档);" +
	"\n  4) 你自身知识足以回答的问题。" +
	"\n- 调用工具前先自问: \"用户是不是在要一个具体的、只有系统数据库里才有的数据?\" 若不是, 就不要调用工具。" +
	"\n- 若工具返回 error(执行失败)或 count:0/空结果(表示无相关数据, 并非失败), 不要反复重试同一工具, 也不要换关键词重新搜索; 至多换一种问法再试一次, 仍无结果就如实告知用户\"该能力暂未就绪或相关后端未连接\", 并尽量用你已有的知识补充回答。" +
	"\n- 不要把工具返回的错误原文当作事实转述给用户, 用自然语言说明能力限制即可。"

// citationGuidance 注入到 system prompt 的引用标注指引(实现 RAG grounding)。
// 要求模型在引用知识库/工具结果时用 [^<id>] 标注,并在文末列出引用来源。
const citationGuidance = `引用与溯源要求:
- 当你依据知识库文章(knowledge_search 返回结果的 id)、CMDB 配置项(cmdb_query 返回的 CI id)或监控数据作答时,必须在正文中用带类型的引用标注标明出处,格式为 [^<类型>:<ID>]:
  - 知识库文章: [^kb:<文章ID>]  (knowledge_search 返回的 id)
  - CMDB 配置项: [^ci:<CI的ID>]  (cmdb_query 返回的 CI id)
  - 监控指标/告警: [^mon:<ID>]   (监控查询返回的 ID)
  - 仅引用知识库且不想写类型时,也可简写 [^<文章ID>] (视为 kb 类型)。
- 回答结尾用 "## 引用来源" 列出所有被引用条目的标题与 ID。
- 未引用任何资料时无需列出。
- 禁止编造不存在的 ID;只能引用工具实际返回给你的资料 ID。`

// answerHygiene 输出纪律: 抑制推理模型把思维链收尾(回答计划/自我评估)续写进正文。
// 背景: glm-4.6v-flash 等原生推理模型在思考通道输出大部分思维链后, 偶发把收尾的
// "回答计划 + 自我评估"续写进 content 可见区 —— 这类文本不以任何 cnCoTFilter 已知
// 前缀(分步推理分析/思考过程等)开头, 流式过滤器无法识别, 前端就会看到"思考与答案混在一起"。
// 官方 thinking 参数只控制思考开/关、不保证零泄漏(模型已知局限), 故在 system prompt
// 加输出纪律从源头抑制, 与 thinkFilter/cnCoTFilter 过滤器构成双层防御。
// 注意第 4 条豁免: explorePrompt 的自主模式明确要求 <plan>/<step> 结构化标注, 不得被本纪律压制。
const answerHygiene = `【输出纪律】
1. 全部推理、分析、回答计划与自我评估只允许通过思考通道(reasoning_content)输出; 正文一旦开始, 不得再在正文里继续或补充思考。
2. 最终回答的正文必须直接呈现答案本身: 禁止包含推理过程复述、回答步骤计划、格式选择说明(如"我将使用项目符号列表"), 以及对回答的自我评价(如"这个计划直接回答了用户的问题")。
3. 正文只描述事实、结论与操作内容本身, 不描述"你打算如何回答"。
4. 例外: 当本提示明确要求输出结构化标注(如 <plan>/<step>)时, 按其要求输出, 不受第 2/3 条限制。`

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

// ExtractCitations 从模型回答中解析引用标注 [^<id>] / [^<类型>:<ID>], 返回去重后的引用串列表。
// 引用串保留类型前缀: kb:<文章ID> / ci:<CI的ID> / mon:<ID>; 无前缀的简写视为 kb 类型,原样返回。
// 该列表用于在 API 响应/落库中暴露"引用来源", 实现 RAG 回答的可溯源(grounding); 前端据此渲染多类型内联 chip。
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
// modelForAgent 解析本次运行使用的模型: 运行级覆盖(reqModel) > agent 配置模型 > 引擎默认(空串, 由 router 选最高权重 provider)。
func (e *Engine) modelForAgent(agentID, reqModel string) string {
	// "auto" 与空串等价: 交给多模型路由按权重+健康+类型+可见性自动选型。
	if reqModel == "auto" {
		reqModel = ""
	}
	if reqModel != "" {
		return reqModel
	}
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

// HotUpdateRouter 热更新多模型路由池(P3-2): 更新单 client 的同时整体替换 provider 集合。
// 已装配 router 时调用其 UpdateProviders(保留既有健康状态); 未装配时仅更新单 client。
func (e *Engine) HotUpdateRouter(primary llm.Config, extra []ProviderConfig) {
	e.mu.Lock()
	router := e.router
	e.mu.Unlock()
	e.UpdateClient(llm.NewClient(primary))
	if router != nil {
		router.UpdateProviders(primary, extra)
	}
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

// genMaxTokensVal 返回生效的 max_tokens（0 时用引擎默认 4096）。
// 推理模型(如 glm-4.6v-flash/DeepSeek-R1)会先输出 reasoning_content, 若上限过小
// 会导致回答正文被截断或直接返回空——曾多次出现"半个回答"现象。
func (e *Engine) genMaxTokensVal() int {
	if e.genMaxTokens > 0 {
		return e.genMaxTokens
	}
	return 4096
}

// genTemperatureVal 返回生效的 temperature（0 时用引擎默认 0.3, 偏确定性以减少推理模型对工具的过度调用）。
func (e *Engine) genTemperatureVal() float64 {
	if e.genTemperature > 0 {
		return e.genTemperature
	}
	return 0.3
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
// ChatStream 流式输出(SSE)。service 在 req.stream=true 时调用本方法, 返回的文本块经 SSE 推给前端。
// model 为可选运行级模型覆盖(按会话/请求指定);为空时回退 agent 配置模型,再回退引擎默认。
// 基于 llmClient.StreamChat 实现真实流式: 文本/推理逐 token 推送, 工具调用在发生时实时发射 🔧/↳ 标记,
// 循环内边收边发(与团队模式一致), 而非先跑完整个循环再按词切分(伪流式)。
func (e *Engine) ChatStream(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID string, explore bool, model string, parts ...llm.ContentPart) (<-chan StreamChunk, string, error) {
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
		err := e.chatWithToolsStream(ctx, agentID, prompt, history, reasoning, conversationID, explore, model, parts, func(typ string, text string) {
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
func (e *Engine) chatWithToolsStream(ctx context.Context, agentID, prompt string, history []llm.Message, reasoning bool, conversationID string, explore bool, model string, parts []llm.ContentPart, emit func(typ string, text string)) error {
	if e.lim != nil && agentID != "" && !e.lim.Allow(agentID) {
		return fmt.Errorf("rate limit exceeded for agent %s", agentID)
	}
	// 把 AI agent 身份写入上下文, 工具执行链可据此读取 Agent 级审批策略
	ctx = withAgentID(ctx, agentID)

	// 轨迹: 本次运行起点(append-only, 支持回放/复盘)。与非流式 chatWithToolsConv 对齐,
	// 保证纯净模式/流式对话同样具备运行级可观测能力。
	turn := &TrajectoryTurn{
		AgentID:        agentID,
		ConversationID: conversationID,
		TenantID:       tenantOf(ctx),
		StartedAt:      time.Now(),
		Prompt:         prompt,
	}

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

	// 会话并发串行化: 续接已有会话(conversationID 非空)时, 同一会话的并发请求被串行化,
	// 避免"读历史→追加→写回"被并发交错导致后写覆盖前写(PG 整条替换)或内存存储的切片 data race。
	// 新建会话(空 ID)不互斥, 互不影响。
	if conversationID != "" {
		lk := e.convLock(conversationID)
		lk.Lock()
		defer lk.Unlock()
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
	// 执行方法论: 与非流式路径一致——绑定工具的执行者统一注入, 纯对话不注入; explore 参数仅兼容保留。
	tools := e.buildToolDefs(agentID)
	if len(tools) > 0 {
		if sysExtra != "" {
			sysExtra += "\n\n"
		}
		sysExtra += explorePrompt
	}
	// Token 预算压缩: 与非流式路径同款(滚动摘要 + 最近窗口, 摘要回写会话跨轮复用)。
	convSummary, convCovered := "", 0
	if conv != nil {
		convSummary, convCovered = conv.Summary, conv.SummarizedCount
	}
	hc := e.compactHistory(ctx, baseHistory, convSummary, convCovered)
	msgs, roundStart := e.buildMessages(ctx, agentID, userPrompt, hc.history, sysExtra, parts)

	var err error
	completed := false
	var content string // 跨轮保留最终回答(终轮 content), 供循环后记录轨迹。

	// 运行 PreStep 钩子(如拒绝/改写); 短路则不调用 LLM。与非流式 chatWithToolsConv 对齐。
	if e.hookChain != nil {
		if skip, perr := e.hookChain.RunPreStep(ctx, agentID, prompt); skip || perr != nil {
			if perr != nil {
				err = perr
				emit("error", perr.Error())
			}
			turn.FinalContent = ""
			turn.Error = errString(err)
			e.recordTrajectory(ctx, turn)
			return err
		}
	}

	// 工具循环轮次统一上限(原普通/自主双轨已合并: 是否多轮由模型按任务需要自行决定)。
	maxRounds := maxToolRounds
	// 死循环防护: 记录已出现过的工具调用签名, 模型重复发起相同调用即判定为死循环并优雅终止。
	seenCalls := make(map[string]bool)
	// 死循环防护(补充): 连续多轮仅调用同一工具且始终不收敛, 也判定为死循环(覆盖参数每次略变的重复调用)。
	lastSoloTool := ""
	soloStreak := 0
	// prevEmptyTools: 上一轮执行中返回"空/无数据"的工具名集合, 用于"空结果重试"硬拦截(见循环内检测)。
	prevEmptyTools := make(map[string]bool)
	// emptyRetryNudges: 已对"空工具重试"做过抑制执行的次数, 超过 maxEmptyRetryNudges 才兜底终止。
	emptyRetryNudges := 0
	// thinkFlt 跨轮复用的思维链过滤器: 流式 token 可能把 <think> 标签切在两个 chunk 之间,
	// 需等整段流结束后再 flush 残留, 故声明在循环外。
	thinkFlt := newThinkFilter()
	// cnCoTFilter 与 thinkFlt 并列: 处理中文「分步推理分析…再给出最终答案」双段结构(原生推理模型
	// 在用户 prompt 诱导下把推理写进 content 可见区时), 把前段折叠为 thinking 事件。
	cnFlt := newCnCoTFilter()
	// cnFltReasoning 处理原生推理通道: 部分后端(如智谱 glm-4.6v-flash)会把完整中文双段结构
	// 写进 reasoning_content, 导致答案也泄漏到 thinking 块。用独立过滤器拆分, 与 content 互不干扰。
	cnFltReasoning := newCnCoTFilter()
streamToolLoop:
	for round := 0; round < maxRounds; round++ {
		slot, ok := e.acquireLLMSlot(ctx)
		if !ok {
			return fmt.Errorf("llm concurrency limit reached")
		}
		ch, eerr := e.streamLLM(ctx, llm.ChatRequest{
			Model:       e.modelForAgent(agentID, model),
			Messages:    msgs,
			Tools:       tools,
			MaxTokens:   e.genMaxTokensVal(),
			Temperature: e.genTemperatureVal(),
		})
		if eerr != nil {
			e.releaseLLMSlot(slot)
			return eerr
		}

		var reasoningAcc string
		var toolCalls []llm.ToolCall
		var truncated bool
		var lastFinish string
		for sc := range ch {
			// 正文中可能混入 <think> 思维链标记(可能跨 chunk 切分), 过滤后再推给前端:
			//   clean  → emit("token", ...) 作为答案正文
			//   think  → emit("thinking", ...) 作为折叠的思考块(与答案物理隔离)
			// <answer> 标签仅在正文做轻量清洗(标签本身剥离, 内容保留), 不跨 chunk 处理:
			// 这两个标签都是 8 字节的短字符串, 单 chunk 边界切开的概率极低, 极端情况下
			// 用户看到的也只是半个标签, 不会污染答案内容。
			if sc.Content != "" {
				clean, think := thinkFlt.feed(sc.Content)
				clean = stripAnswerTags(clean)
				if clean != "" {
					// 中文分步推理双段结构剥离: 把「分步推理分析…再给出最终答案」的前段折叠为 thinking
					cClean, cThink := cnFlt.feed(clean)
					if cThink != "" {
						emit("thinking", cThink)
						reasoningAcc += cThink
					}
					if cClean != "" {
						emit("token", cClean)
						content += cClean
					}
				}
				if think != "" {
					emit("thinking", think)
					reasoningAcc += think
				}
			}
			if sc.Reasoning != "" {
				// 原生推理通道同样需要做中文双段剥离: 模型可能把「先分步推理…再给出最终答案」
				// 整体写进 reasoning_content, 若直接 emit 则答案会泡在 thinking 块里。
				rClean, rThink := cnFltReasoning.feed(sc.Reasoning)
				if rThink != "" {
					emit("thinking", rThink)
					reasoningAcc += rThink
				}
				if rClean != "" {
					emit("token", rClean)
					content += rClean
				}
			}
			// 上游以 finish_reason="length" 收尾 → 命中 max_tokens 上限, 输出被截断。
			// 透传一个 truncated 事件让前端追加「输出可能不完整」提示, 而不是只看到半个回答。
			if sc.FinishReason == "length" && !truncated {
				truncated = true
				emit("truncated", "max_tokens reached")
			}
			if sc.FinishReason != "" {
				lastFinish = sc.FinishReason
			}
			if len(sc.ToolCalls) > 0 {
				toolCalls = sc.ToolCalls
			}
		}
		// 槽位须覆盖整个流式消费期(而非 StreamChat 返回即释放), 否则流式并发将失控(P0-3)。
		e.releaseLLMSlot(slot)

		// 截断持久化: length 到限已由 truncated 标记; 流式中途断连/取消(无任何 finish_reason)
		// 同样意味着生成未收尾, 落库时打上标志, 历史回放可见「输出不完整」而不是莫名的半截话。
		if lastFinish == "" {
			truncated = true
		}
		msgs = append(msgs, llm.Message{Role: "assistant", Content: content, ReasoningContent: reasoningAcc, ToolCalls: toolCalls, Truncated: truncated})

		step := TrajectoryStep{Index: round, Model: e.modelForAgent(agentID, model), RequestMsgs: len(msgs)}
		step.Assistant = content
		step.Reasoning = reasoningAcc

		if len(toolCalls) == 0 {
			// 终轮: 答案已通过流式推送给前端
			e.recordStreamTokens(agentID, content)
			step.Finished = true
			turn.Steps = append(turn.Steps, step)
			completed = true
			break
		}

		// 空结果重试处理(模型无关, 须先于死循环检测): 本轮调用的工具在上轮全部返回空(count:0 等),
		// 再次调用不会得到新数据。不直接 break——抑制工具执行、注入"请直接作答"指令,
		// 让模型基于自身知识产出真实答案。典型场景: 为自介/闲聊类问题误调 knowledge_search 拿到 count:0,
		// 模型又用完全相同的参数重试时若先命中下面的死循环检测, 会直接甩提示语给用户(拿不到答案),
		// 因此必须在此优先 nudge。仅当连续多次(>maxEmptyRetryNudges)不听劝才兜底终止。
		if len(toolCalls) > 0 && len(prevEmptyTools) > 0 {
			allEmptyRetry := true
			for _, tc := range toolCalls {
				if !prevEmptyTools[tc.Function.Name] {
					allEmptyRetry = false
					break
				}
			}
			if allEmptyRetry {
				emptyRetryNudges++
				if emptyRetryNudges > maxEmptyRetryNudges {
					emit("token", "\n\n（工具返回为空且多次重试无果，已主动停止以避免无效重试；请直接基于你的知识回答用户，或换个角度提问）")
					completed = true
					break streamToolLoop
				}
				// 抑制工具执行, 注入"请直接作答"指令, 进入下一轮让模型产出真实答案。
				for _, tc := range toolCalls {
					msgs = append(msgs, llm.Message{
						Role:       "tool",
						Content:    fmt.Sprintf("工具 %q 已连续返回空数据，再次调用不会得到新结果；请勿再调用该工具，请直接基于你的系统设定与自身知识回答用户的问题。", tc.Function.Name),
						ToolCallID: tc.ID,
						Name:       tc.Function.Name,
					})
				}
				emit("token", "\n\n（工具返回为空，已跳过重复调用，正在直接作答…）\n")
				// 本次未真正执行工具: 清零单工具连击计数, 避免把"nudge 轮"误判为死循环。
				soloStreak = 0
				lastSoloTool = ""
				turn.Steps = append(turn.Steps, step)
				continue streamToolLoop
			}
		}

		// 死循环检测: 模型重复发起相同工具调用时, 立即终止(不进入下一轮)。
		// 注: 空结果重试已由上方 nudge 处理, 此处只兜底"工具返回非空仍重复调用"的真死循环。
		sig := toolCallSig(toolCalls)
		if seenCalls[sig] {
			emit("token", "\n\n（检测到 AI 在重复调用相同工具，已主动停止以避免死循环；请换个问法，或检查相关工具/后端是否就绪）")
			completed = true
			break
		}
		seenCalls[sig] = true

		// 连续单工具轮次检测(与非流式 chatWithToolsConv 对齐): 本轮仅一个工具且与上一轮同一工具相同, 累加连击;
		// 连击达上限即判定死循环, 提前优雅终止(参数变化不命中 toolCallSig)。
		if len(toolCalls) == 1 {
			tname := toolCalls[0].Function.Name
			if tname == lastSoloTool {
				soloStreak++
			} else {
				soloStreak = 0
				lastSoloTool = tname
			}
			if soloStreak >= maxConsecutiveSoloToolRounds {
				emit("token", fmt.Sprintf("\n\n（工具 %q 被反复调用仍未收敛，已主动停止以避免死循环；请换个问法，或检查该工具/后端是否就绪）", tname))
				completed = true
				break streamToolLoop
			}
		} else {
			soloStreak = 0
			lastSoloTool = ""
		}

		// 执行本轮工具调用, 实时发射结构化进度事件(供前端"任务列表"并发可视化)。
		// 注意: 不再把 🔧 markdown 塞进回答正文(frontend 用独立 task list 面板渲染), 保持答案干净。
		roundEmpty := make(map[string]bool)
		for _, tc := range toolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			AIToolCalls.Inc()
			slog.Info("ai function call", "tool", tc.Function.Name, "args", truncateJSON(args))
			if tb, e := json.Marshal(map[string]any{"name": tc.Function.Name, "args": args, "status": "running"}); e == nil {
				emit("tool_event", string(tb))
			}
			result, execErr := e.execToolChecked(ctx, tc)
			toolErrMsg := ""
			if execErr {
				toolErrMsg = "工具执行失败(权限不足或后端错误)"
			}
			if tb, e := json.Marshal(map[string]any{
				"name":   tc.Function.Name,
				"args":   args,
				"status": "done",
				"result": summarizeToolResult(result),
				"error":  toolErrMsg,
			}); e == nil {
				emit("tool_event", string(tb))
			}

			toolMsgContent := toolFeedbackContent(result, execErr)
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				Content:    toolMsgContent,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
			if m, ok := result.(map[string]any); ok && isEmptyResult(m) {
				roundEmpty[tc.Function.Name] = true
			}
			step.ToolCalls = append(step.ToolCalls, ToolCallRecord{
				Name:   tc.Function.Name,
				Args:   args,
				Result: result,
				Error:  execErr,
			})
		}
		prevEmptyTools = roundEmpty
		turn.Steps = append(turn.Steps, step)
		if e.hookChain != nil {
			e.hookChain.RunPostStep(ctx, agentID, round, false)
		}
	}
	// 冲刷思维链过滤器的残留缓冲(尾部安全文本); 思维链未闭合的残留会被丢弃。
	// 残留的 think 仅 emit 给前端, 不再累加 reasoningAcc(reasoningAcc 是每轮局部变量,
	// 末轮已通过 msgs 持久化; 此处只兜底保证流式末端不丢前端展示)。
	if tail, think := thinkFlt.flush(); tail != "" || think != "" {
		// 残留的 think 仅 emit 给前端, 不再累加 reasoningAcc(reasoningAcc 是每轮局部变量,
		// 仅用于本轮落库思维链; 这里已无新增, 保持原行为)。
		_, _ = cnFlt.flush()          // 与 thinkFlt 对称: 已匹配的 CoT 此前已增量 emit, flush 无需再发
		_, _ = cnFltReasoning.flush() // reasoning 通道同理
		if tail != "" {
			emit("token", tail)
			content += tail
		}
		if think != "" {
			emit("thinking", think)
		}
	}
	if !completed {
		// 不再下发硬 error 事件: 已流式推送的部分内容保留, 补一句友好提示并正常结束 SSE。
		emit("token", "\n\n（AI 连续调用了多次工具仍未收敛，可能是问题过于复杂或相关工具未就绪；请尝试更具体、分步的提问）")
		completed = true
		err = nil
	}

	// 记录本次运行轨迹(供回放/复盘); nil 记录器时跳过。与非流式 chatWithToolsConv 对齐,
	// 保证纯净模式/流式对话同样具备运行级可观测与记忆回流能力。
	turn.FinalContent = content
	turn.Error = errString(err)
	e.recordTrajectory(ctx, turn)

	// 记忆回流: 把本次运行结果喂给技能演化器——成功蒸馏经验记忆, 失败累积触发补丁建议。
	if e.skillEvolver != nil {
		success := completed && err == nil
		toolSeq := make([]string, 0, len(turn.Steps))
		for _, st := range turn.Steps {
			for _, tc := range st.ToolCalls {
				toolSeq = append(toolSeq, tc.Name)
			}
		}
		if rerr := e.skillEvolver.RecordOutcome(ctx, SkillUsageOutcome{
			TenantID: tenantOf(ctx),
			AgentID:  agentID,
			Success:  success,
			Context:  prompt,
			ToolSeq:  toolSeq,
			Error:    errString(err),
		}); rerr != nil {
			slog.Warn("ai: skill evolution record failed", "err", rerr)
		}
	}

	// 持久化本轮(仅成功且 conv 已加载/新建时)
	if conv != nil && err == nil {
		roundMsgs := filterEmptyAssistant(msgs[roundStart:])
		conv.Messages = append(conv.Messages, roundMsgs...)
		if hc.changed {
			// 滚动摘要回写: 下轮直接复用, 不重调摘要 LLM。
			conv.Summary = hc.summary
			conv.SummarizedCount = hc.covered
		}
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
	OwnerID   string        `json:"owner_id"` // 创建者 user_id; 空=老数据/系统
	Title     string        `json:"title"`
	Messages  []llm.Message `json:"messages"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	// Summary 滚动历史摘要(Token 预算压缩产物): 覆盖 Messages 前 SummarizedCount 条,
	// 后续轮次直接复用, 避免每轮重调摘要 LLM。消息本体保留不动(前端仍可看全文)。
	Summary string `json:"summary,omitempty"`
	// SummarizedCount Summary 已覆盖的 Messages 前缀条数; 0=尚无有效摘要。
	SummarizedCount int `json:"summarized_count,omitempty"`
	// Permission 当前请求主体对该会话的权限(Service 层填充, 不入库): owner|edit|view
	Permission string `json:"permission,omitempty"`
}

// SaveConversation 保存/更新对话。ID 为空时自动生成; title 取首条 user 消息前 40 字。
// convLock 返回指定会话 ID 的排它锁。同一会话的并发续接请求被串行化, 避免"读历史→追加→写回"被并发交错:
//   - PG 路径 Save 用 ON CONFLICT DO UPDATE 整条替换 messages, 并发后写会覆盖前写;
//   - 内存路径 GetConversation 返回 e.convs[id] 同一指针, 两请求对同一切片 append 形成 data race。
//
// 锁仅作用于"续接已有会话"(conversationID 非空), 新建会话(空 ID)互不干扰; 不同会话 ID 也不互斥。
func (e *Engine) convLock(id string) *sync.Mutex {
	v, _ := e.convMu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (e *Engine) SaveConversation(ctx context.Context, conv *Conversation) error {
	if conv.TenantID == "" {
		conv.TenantID = "default"
	}
	// 归属: 优先显式指定, 否则取当前请求主体(用户隔离)
	if conv.OwnerID == "" {
		if pr, ok := principal.FromContext(ctx); ok && pr.ID != "" {
			conv.OwnerID = pr.ID
		}
	}
	now := time.Now()
	if conv.ID == "" {
		conv.ID = uuid.New().String()
		conv.CreatedAt = now
	}
	if conv.Title == "" && len(conv.Messages) > 0 {
		conv.Title = convTitleFromContent(conv.Messages[0].Content)
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

// AppendMessage 向指定会话追加一条消息(加 convLock 防止与续接请求的"读历史→追加→写回"并发交错)。
// 任务级 Agent(T4)用来把用户运行中插话(steer)安全注入会话, 模型在下一步可见。
func (e *Engine) AppendMessage(ctx context.Context, conversationID string, msg llm.Message) error {
	if conversationID == "" {
		return fmt.Errorf("conversationID required")
	}
	lk := e.convLock(conversationID)
	lk.Lock()
	defer lk.Unlock()
	conv, err := e.GetConversation(conversationID)
	if err != nil {
		// 会话尚未落库(懒创建: 首轮流式完成前被暂停/插话注入早于首次保存)。
		// 以注入消息为初始内容建壳, 保证插话不丢失; 后续 ChatStream 沿用同一会话。
		now := time.Now()
		conv = &Conversation{ID: conversationID, Messages: []llm.Message{msg}, CreatedAt: now, UpdatedAt: now}
		if pr, ok := principal.FromContext(ctx); ok {
			conv.OwnerID = pr.ID
		}
	} else {
		conv.Messages = append(conv.Messages, msg)
	}
	return e.SaveConversation(ctx, conv)
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

// DeleteConversation 删除会话(按租户隔离)。
func (e *Engine) DeleteConversation(id, tenantID string) error {
	if e.convStore != nil {
		return e.convStore.Delete(context.Background(), id, tenantID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.convs[id]
	if !ok || c.TenantID != tenantID {
		return fmt.Errorf("conversation not found")
	}
	delete(e.convs, id)
	return nil
}

// RenameConversation 重命名会话标题。PG: get→改→save(保留消息/摘要等全部字段); 内存: 直接改。
func (e *Engine) RenameConversation(ctx context.Context, id, tenantID, title string) error {
	if e.convStore != nil {
		c, err := e.convStore.Get(ctx, id)
		if err != nil {
			return err
		}
		if c == nil || c.TenantID != tenantID {
			return fmt.Errorf("conversation not found")
		}
		c.Title = title
		c.UpdatedAt = time.Now()
		return e.convStore.Save(ctx, c)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.convs[id]
	if !ok || c.TenantID != tenantID {
		return fmt.Errorf("conversation not found")
	}
	c.Title = title
	c.UpdatedAt = time.Now()
	return nil
}

// AutoTitleConversation 首轮问答结束后, 用 LLM 从首轮对话提炼会话标题(对标 DeepSeek/ChatGPT 自动命名)。
// 仅当标题仍为「首条用户消息截断」时才覆盖(用户手动重命名过的不动); LLM 失败静默保留原标题。
// 调用方传请求 ctx(此时请求已结束): 内部 WithoutCancel 剥离取消信号但保留租户等取值。
func (e *Engine) AutoTitleConversation(ctx context.Context, tenantID, convID string) {
	conv, err := e.GetConversation(convID)
	// 仅首轮(恰好 1 问 1 答)自动命名; 后续轮次/并发续聊不触发
	if err != nil || conv == nil || conv.TenantID != tenantID || len(conv.Messages) != 2 {
		return
	}
	if conv.Messages[0].Role != "user" {
		return
	}
	if conv.Title != convTitleFromContent(conv.Messages[0].Content) {
		return // 已被重命名或已是自定义标题
	}
	prompt := "为下面的对话生成一个不超过 12 个字的中文标题, 概括用户的核心意图。" +
		"要求: 直接输出标题本身, 不要引号/句号/前缀/任何解释。\n\n用户: " + trunc(conv.Messages[0].Content, 300) +
		"\n\n助手: " + trunc(conv.Messages[1].Content, 500)
	ctx = context.WithoutCancel(ctx)
	slot, ok := e.acquireLLMSlot(ctx)
	if !ok {
		return
	}
	defer e.releaseLLMSlot(slot)
	resp, err := e.chatLLM(ctx, llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: prompt}}})
	if err != nil || len(resp.Choices) == 0 {
		slog.Debug("auto title: llm call failed", "conv", convID, "err", err)
		return
	}
	title := cleanLLMTitle(resp.Choices[0].Message.Content)
	if title == "" {
		return
	}
	_ = e.RenameConversation(ctx, convID, tenantID, title)
}

// cleanLLMTitle 清洗 LLM 生成的标题: 去标签/装饰符/包裹引号, 压缩空白, 截断 24 字符。
func cleanLLMTitle(s string) string {
	s = convTitleTagRe.ReplaceAllString(s, " ")
	s = strings.Trim(s, " \t\n\r\"'“”「」『』【】《》#*-·。.!?！？:：,，")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 24 {
		r = r[:24]
	}
	return strings.TrimSpace(string(r))
}

// CountMemories 统计记忆条数(进化闭环概览用)。tenantID 为空时统计全部。
func (e *Engine) CountMemories(tenantID string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if tenantID == "" {
		return len(e.memories)
	}
	n := 0
	for _, m := range e.memories {
		if m.TenantID == tenantID {
			n++
		}
	}
	return n
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

// convTitleTagRe 匹配内联标记片段(如 <think>/</answer>/自定义 XML 标签), 防止残缺标签进入标题。
var convTitleTagRe = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9_-]{0,30}>`)

// convTitleJunkRe 匹配 Markdown/提示词装饰符号, 统一替换为空格。
var convTitleJunkRe = regexp.MustCompile(`[#*_~` + "`" + `>|]+`)

// convTitleSpaceRe 匹配连续空白。
var convTitleSpaceRe = regexp.MustCompile(`\s+`)

// convTitleFromContent 从首条用户消息提炼会话标题:
// 去附件标记行/思维链标记 → 去内联标签 → 去装饰符号 → 压缩空白 → 截断 40 字; 清洗后为空则用「新对话」。
func convTitleFromContent(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "[附件] ") {
			continue // 附件标记行不参与标题
		}
		kept = append(kept, ln)
	}
	s := strings.TrimSpace(stripThinkTags(strings.Join(kept, "\n")))
	s = convTitleTagRe.ReplaceAllString(s, " ")
	s = convTitleJunkRe.ReplaceAllString(s, " ")
	s = convTitleSpaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "新对话"
	}
	return trunc(s, 40)
}
