package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/internal/ai/eval"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/internal/objectstore"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/llm"
	redisclient "github.com/opsorigin/core/pkg/redis"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	cfg           *config.Config
	Engine        *agentic.Engine
	Registry      *mcp.Registry
	MCPManager    *mcp.Manager // 外部 MCP server 接入与工具 proxy 管理
	limiter       ratelimit.RateLimiter
	ragFn         func(ctx context.Context, query, agentID string) string // RAG上下文注入(按 Agent 绑定分类过滤)
	feedbackStore agentic.FeedbackStore                                   // 点赞/点踩反馈持久化(P2-Eval)
	db            *sql.DB                                                 // 统计/管理查询直连(会话删除、进化闭环等)
	objStore      *objectstore.Client                                     // 对话产物/文件归档(MinIO), 未配置为 nil
	userGroupsFn  UserGroupsFn                                            // 用户组查询(会话共享按组匹配), nil=不支持组共享

	// llmClient 供"非对话式"结构化生成场景直连模型(如 AI 生成报表看板),
	// 不走 Engine 的对话/工具循环。sysconfig 改配置时随 UpdateLLMConfig 一起热替换。
	llmMu     sync.RWMutex
	llmClient *llm.Client
}

func NewService(cfg *config.Config, llmClient *llm.Client, redisClient *redisclient.Client, db *sql.DB) *Service {
	reg := mcp.NewRegistry()

	var lim ratelimit.RateLimiter
	if redisClient != nil {
		lim = ratelimit.NewRedisLimiter(redisClient.Conn())
		slog.Info("ai rate limiter: using redis distributed backend")
	} else {
		lim = ratelimit.New()
		slog.Info("ai rate limiter: using in-memory backend")
	}

	var convStore agentic.ConversationStore
	if db != nil {
		convStore = agentic.NewPGConvStore(db)
		slog.Info("ai conversation store: using postgres backend")
	} else {
		slog.Info("ai conversation store: using in-memory backend")
	}

	var feedbackStore agentic.FeedbackStore
	if db != nil {
		feedbackStore = agentic.NewPGFeedbackStore(db)
		slog.Info("ai feedback store: using postgres backend")
	} else {
		feedbackStore = agentic.NewMemFeedbackStore()
		slog.Info("ai feedback store: using in-memory backend")
	}

	eng := agentic.NewEngine(reg, lim, llmClient, convStore)

	// 提示词外置化: 将配置中的默认 system prompt 注入引擎(环境变量可覆盖内置常量)。
	eng.SetDefaultSystemPrompt(cfg.LLM.DefaultSystemPrompt)

	if db != nil {
		eng.SetTeamConvStore(agentic.NewPGTeamConvStore(db))
		slog.Info("ai team conversation store: using postgres backend")
	}

	svc := &Service{
		cfg:           cfg,
		Engine:        eng,
		Registry:      reg,
		MCPManager:    mcp.NewManager(reg),
		limiter:       lim,
		feedbackStore: feedbackStore,
		llmClient:     llmClient,
		db:            db,
	}
	svc.initEvolutionDDL(context.Background())
	svc.initArtifactDDL(context.Background())
	svc.initWorkspaceDDL(context.Background())
	svc.initMemoryDDL(context.Background())
	return svc
}

// SetObjectStore 注入对象存储(对话产物归档; 未注入则产物仅存元数据)。
func (s *Service) SetObjectStore(os *objectstore.Client) { s.objStore = os }

// LLMClient 返回当前生效的 LLM 客户端(可能为 nil，表示尚未配置模型)。
func (s *Service) LLMClient() *llm.Client {
	s.llmMu.RLock()
	defer s.llmMu.RUnlock()
	return s.llmClient
}

// LLMConfigHook 在 sysconfig 修改 LLM 设置后被调用(热生效)。nil 表示未设置。
var LLMConfigHook func(cfg llm.Config)

func (s *Service) SetRAGFunc(fn func(ctx context.Context, query, agentID string) string) {
	s.ragFn = fn
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/ai/agents", s.handleCreateAgent)
	mux.HandleFunc("GET /api/v1/ai/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/v1/ai/agents/{id}", s.handleGetAgent)
	mux.HandleFunc("PUT /api/v1/ai/agents/{id}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/v1/ai/agents/{id}", s.handleDeleteAgent)
	mux.HandleFunc("GET /api/v1/ai/tools", s.handleListTools)
	mux.HandleFunc("GET /api/v1/ai/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("POST /api/v1/ai/tools/{name}/execute", s.handleExecuteTool)
	// 自定义 MCP 工具注册 (PRD 5.2.2)
	mux.HandleFunc("POST /api/v1/ai/tools/custom", s.handleCreateCustomTool)
	mux.HandleFunc("GET /api/v1/ai/tools/custom", s.handleListCustomTools)
	mux.HandleFunc("PUT /api/v1/ai/tools/custom/{id}", s.handleUpdateCustomTool)
	mux.HandleFunc("DELETE /api/v1/ai/tools/custom/{id}", s.handleDeleteCustomTool)
	mux.HandleFunc("POST /api/v1/ai/plans", s.handleExecutePlan)
	mux.HandleFunc("GET /api/v1/ai/plans", s.handleListPlans)
	mux.HandleFunc("GET /api/v1/ai/rate-limit/{agent_id}", s.handleGetRateLimit)
	mux.HandleFunc("GET /api/v1/ai/token-stats", s.handleTokenStats)
	mux.HandleFunc("POST /api/v1/ai/chat", s.handleChat)
	mux.HandleFunc("POST /api/v1/ai/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/v1/ai/conversations", s.handleListConversations)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("POST /api/v1/ai/conversations", s.handleSaveConversation)
	mux.HandleFunc("DELETE /api/v1/ai/conversations/{id}", s.handleDeleteConversation)
	// —— 会话共享(用户隔离: 默认私有, 共享后可见) ——
	mux.HandleFunc("POST /api/v1/ai/conversations/{id}/shares", s.handleAddShare)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}/shares", s.handleListShares)
	mux.HandleFunc("DELETE /api/v1/ai/conversations/{id}/shares/{sid}", s.handleDeleteShare)
	mux.HandleFunc("GET /api/v1/ai/evolution/stats", s.handleEvolutionStats)
	// —— 主动进化: 策略配置 + 数据源管理 + 手动触发 ——
	mux.HandleFunc("GET /api/v1/ai/evolution/policy", s.handleEvolutionPolicy)
	mux.HandleFunc("PUT /api/v1/ai/evolution/policy", s.handleEvolutionPolicy)
	mux.HandleFunc("GET /api/v1/ai/evolution/sources", s.handleEvolutionSources)
	mux.HandleFunc("POST /api/v1/ai/evolution/sources", s.handleEvolutionSources)
	mux.HandleFunc("PUT /api/v1/ai/evolution/sources/{id}", s.handleUpdateEvolutionSource)
	mux.HandleFunc("DELETE /api/v1/ai/evolution/sources/{id}", s.handleDeleteEvolutionSource)
	mux.HandleFunc("POST /api/v1/ai/evolution/fetch", s.handleEvolutionFetch)
	// —— 对话产物归档(MinIO) ——
	mux.HandleFunc("POST /api/v1/ai/conversations/{id}/artifacts", s.handleSaveArtifact)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}/artifacts", s.handleListArtifacts)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}/artifacts/{aid}", s.handleDownloadArtifact)
	mux.HandleFunc("DELETE /api/v1/ai/conversations/{id}/artifacts/{aid}", s.handleDeleteArtifact)
	// —— 会话工作区(工具产物自动沉淀) ——
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}/workspace", s.handleListWorkspace)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}/workspace/{fid}", s.handleDownloadWorkspace)
	mux.HandleFunc("DELETE /api/v1/ai/conversations/{id}/workspace/{fid}", s.handleDeleteWorkspace)
	// —— 长期记忆(持久化) ——
	mux.HandleFunc("GET /api/v1/ai/memories", s.handleListMemories)
	mux.HandleFunc("POST /api/v1/ai/memories", s.handleAddMemory)
	mux.HandleFunc("DELETE /api/v1/ai/memories/{id}", s.handleDeleteMemory)
	mux.HandleFunc("POST /api/v1/ai/feedback", s.handleSubmitFeedback)
	mux.HandleFunc("GET /api/v1/ai/feedback", s.handleListFeedback)
	mux.HandleFunc("GET /api/v1/ai/feedback/stats", s.handleFeedbackStats)
	mux.HandleFunc("GET /api/v1/ai/eval/cases", s.handleEvalCases)

	// —— Feature 1: 多智能体团队 ——
	mux.HandleFunc("POST /api/v1/ai/teams", s.handleCreateTeam)
	mux.HandleFunc("GET /api/v1/ai/teams", s.handleListTeams)
	mux.HandleFunc("GET /api/v1/ai/teams/{id}", s.handleGetTeam)
	mux.HandleFunc("PUT /api/v1/ai/teams/{id}", s.handleUpdateTeam)
	mux.HandleFunc("DELETE /api/v1/ai/teams/{id}", s.handleDeleteTeam)
	mux.HandleFunc("POST /api/v1/ai/teams/{id}/chat", s.handleTeamChat)

	// —— P0: 岗位数字员工模板(开箱即用的 Agent 矩阵) ——
	mux.HandleFunc("GET /api/v1/ai/agent-templates", s.handleListAgentTemplates)
	mux.HandleFunc("GET /api/v1/ai/agent-templates/{id}", s.handleGetAgentTemplate)
	mux.HandleFunc("POST /api/v1/ai/agent-templates/{id}/instantiate", s.handleInstantiateAgentTemplate)

	// —— P0: 主动巡检 Agent ——
	mux.HandleFunc("GET /api/v1/ai/inspection-templates", s.handleListInspectionTemplates)
	mux.HandleFunc("GET /api/v1/ai/inspections", s.handleListInspections)
	mux.HandleFunc("POST /api/v1/ai/inspections", s.handleCreateInspection)
	mux.HandleFunc("GET /api/v1/ai/inspections/{id}", s.handleGetInspection)
	mux.HandleFunc("PUT /api/v1/ai/inspections/{id}", s.handleUpdateInspection)
	mux.HandleFunc("DELETE /api/v1/ai/inspections/{id}", s.handleDeleteInspection)
	mux.HandleFunc("POST /api/v1/ai/inspections/{id}/run", s.handleRunInspection)
	mux.HandleFunc("POST /api/v1/ai/inspections/{id}/experiment", s.handleRunInspectionExperiment)
	mux.HandleFunc("GET /api/v1/ai/inspections/{id}/runs", s.handleListInspectionRuns)
	mux.HandleFunc("POST /api/v1/ai/inspections/{id}/runs/{runID}/escalate", s.handleEscalateInspectionRun)

	// —— 场景包 (AI 场景层封装: 岗位 + 巡检 + 推荐工单一键启用) ——
	mux.HandleFunc("GET /api/v1/ai/scene-templates", s.handleListSceneTemplates)
	mux.HandleFunc("GET /api/v1/ai/scene-templates/{id}", s.handleGetSceneTemplate)
	mux.HandleFunc("POST /api/v1/ai/scene-templates/{id}/instantiate", s.handleInstantiateScene)

	// —— 数字员工运营效果 (巡检健康 + 发现/转单/处置漏斗) ——
	mux.HandleFunc("GET /api/v1/ai/workforce-metrics", s.handleWorkforceMetrics)

	// —— 反馈驱动微调 (L3: 数据集 -> 微调任务 -> 产物注册模型池) ——
	mux.HandleFunc("POST /api/v1/ai/finetune/tasks", s.handleCreateFinetuneTask)
	mux.HandleFunc("GET /api/v1/ai/finetune/tasks", s.handleListFinetuneTasks)
	mux.HandleFunc("GET /api/v1/ai/finetune/tasks/{id}", s.handleGetFinetuneTask)

	// —— 故障仿真演练 (L3-2: 注入 -> Agent 处置评估 -> 报告/回滚) ——
	mux.HandleFunc("GET /api/v1/ai/faultsim/scenarios", s.handleListFaultScenarios)
	mux.HandleFunc("POST /api/v1/ai/faultsim/injections", s.handleStartFaultInjection)
	mux.HandleFunc("GET /api/v1/ai/faultsim/injections", s.handleListFaultInjections)
	mux.HandleFunc("POST /api/v1/ai/faultsim/injections/{id}/recover", s.handleRecoverFaultInjection)

	// —— 自动修复执行 (L1: 巡检发现 -> 分级处置 blocked/auto/review) ——
	mux.HandleFunc("POST /api/v1/ai/inspections/{id}/runs/{runID}/remediate/generate", s.handleGenerateRemediations)
	mux.HandleFunc("GET /api/v1/ai/inspections/{id}/runs/{runID}/remediate", s.handleListRemediations)
	mux.HandleFunc("POST /api/v1/ai/remediate/{actionID}/execute", s.handleExecuteRemediation)

	// —— Agent 长期记忆 (P0-1: 经验跨会话复用) ——
	mux.HandleFunc("GET /api/v1/ai/memory", s.handleListMemories)
	mux.HandleFunc("POST /api/v1/ai/memory", s.handleAddMemory)
	mux.HandleFunc("DELETE /api/v1/ai/memory/{id}", s.handleDeleteMemory)

	// —— 技能市场 (L4: MCP 打包共享, 数字员工 + 依赖工具打包成市场形态) ——
	mux.HandleFunc("POST /api/v1/ai/skills/publish", s.handlePublishSkill)
	mux.HandleFunc("GET /api/v1/ai/skills", s.handleListSkills)
	mux.HandleFunc("GET /api/v1/ai/skills/{id}", s.handleGetSkill)
	mux.HandleFunc("DELETE /api/v1/ai/skills/{id}", s.handleDeleteSkill)
	mux.HandleFunc("POST /api/v1/ai/skills/{id}/install", s.handleInstallSkill)
	mux.HandleFunc("GET /api/v1/ai/skills/{id}/export", s.handleExportSkill)
	mux.HandleFunc("POST /api/v1/ai/skills/import", s.handleImportSkill)
	// 评分 / 收藏 / 版本列表(用户维度, 复用 user_id 上下文, 无需特殊权限)
	mux.HandleFunc("POST /api/v1/ai/skills/{id}/rate", s.handleRateSkill)
	mux.HandleFunc("GET /api/v1/ai/skills/{id}/rating", s.handleGetSkillRating)
	mux.HandleFunc("POST /api/v1/ai/skills/{id}/favorite", s.handleToggleFavorite)
	mux.HandleFunc("GET /api/v1/ai/skills/{id}/favorite", s.handleIsFavorite)
	mux.HandleFunc("GET /api/v1/ai/skills/favorites", s.handleListFavorites)
	mux.HandleFunc("GET /api/v1/ai/skills/{id}/versions", s.handleListVersions)

	// —— 提示词库 (可复用文本片段; 与技能包的边界见 agentic/prompt.go 顶部注释) ——
	mux.HandleFunc("GET /api/v1/ai/prompts", s.handleListPrompts)
	mux.HandleFunc("POST /api/v1/ai/prompts", s.handleCreatePrompt)
	mux.HandleFunc("GET /api/v1/ai/prompts/{id}", s.handleGetPrompt)
	mux.HandleFunc("PUT /api/v1/ai/prompts/{id}", s.handleUpdatePrompt)
	mux.HandleFunc("DELETE /api/v1/ai/prompts/{id}", s.handleDeletePrompt)
	mux.HandleFunc("POST /api/v1/ai/prompts/{id}/render", s.handleRenderPrompt)

	// —— AI 定义工作空间 (P1: 自然语言 -> 受约束的报表看板草稿) ——
	// 只产草稿不落库；用户确认后走 POST /report/dashboards，权限归属与人工搭建一致。
	mux.HandleFunc("POST /api/v1/ai/workspace/generate", s.handleGenerateWorkspace)
	mux.HandleFunc("GET /api/v1/ai/workspace/suggestions", s.handleWorkspaceSuggestions)

	// —— 训练数据集导出 (P3-1 反馈驱动微调) ——
	mux.HandleFunc("GET /api/v1/ai/dataset", s.handleExportDataset)

	// —— 多模型路由池 (P3: provider 状态) ——
	mux.HandleFunc("GET /api/v1/ai/models", s.handleListModels)

	// —— 敏感操作审批策略 (PRD 5.2.5) ——
	mux.HandleFunc("GET /api/v1/ai/approval-policy", s.handleGetApprovalPolicy)
	mux.HandleFunc("PUT /api/v1/ai/approval-policy", s.handlePutApprovalPolicy)
	mux.HandleFunc("GET /api/v1/ai/approvals/pending", s.handleListPendingApprovals)
	mux.HandleFunc("POST /api/v1/ai/approvals/{id}/respond", s.handleRespondApproval)
}

func (s *Service) Start(ctx context.Context) error {
	return s.Engine.Start()
}

// handleChat godoc
// @Summary AI 聊天
// @Description 发送消息给 AI 助手，支持流式输出
// @Tags AI
// @Accept json
// @Produce json
// @Param chat body object true "聊天请求"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/ai/chat [post]
// @Security BearerAuth
// handleChat 处理 AI 助手聊天请求。
// req.stream=true 时返回 SSE(text/event-stream)流式输出; 否则返回 JSON。
func (s *Service) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt         string        `json:"prompt"`
		AgentID        string        `json:"agent_id"`
		History        []llm.Message `json:"history"`
		Stream         bool          `json:"stream"`
		Reasoning      bool          `json:"reasoning"`
		Explore        bool          `json:"explore"` // 自主模式: AI 自主规划-执行-总结
		Model          string        `json:"model"`   // 可选运行级模型覆盖(按会话/请求指定); 空=回退 agent/引擎默认
		ConversationID string        `json:"conversation_id"`
		MessageID      string        `json:"message_id"` // 前端为本次 assistant 回复生成的稳定 ID, 反馈时用于精确关联
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Prompt == "" {
		httputil.WriteError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// 会话权限: 续接已有会话时, 非属主且无 edit 共享 → 拒绝(用户隔离); view 共享只读不能继续对话。
	if req.ConversationID != "" {
		tenantID := tenantFromCtx(r.Context())
		userID := currentUserID(r.Context())
		if !s.requireConvWrite(r.Context(), tenantID, req.ConversationID, userID, s.userGroups(r.Context(), userID), w) {
			return
		}
	}

	// P0-1: 清洗客户端提交的对话历史, 丢弃 system/tool 角色与注入的 tool_calls, 防止提示注入绕过 RBAC。
	req.History = agentic.SanitizeHistory(req.History)

	// RAG: 从知识库检索相关内容注入到 system prompt(按 Agent 绑定分类过滤, 空=全部)
	if s.ragFn != nil {
		ragCtx := s.ragFn(r.Context(), req.Prompt, req.AgentID)
		if ragCtx != "" {
			req.History = append([]llm.Message{{Role: "system", Content: "你是运维AI助手。参考知识库文档回答问题：\n" + ragCtx}}, req.History...)
		}
	}

	// F4-D1: 会话计数入口(逻辑并发, 与物理 LLM 槽位解耦)
	agentic.AIConcurrentSessions.Inc()
	defer agentic.AIConcurrentSessions.Dec()

	if req.Stream {
		s.handleChatStream(w, r, &req)
		return
	}

	if req.Reasoning && !req.Stream {
		thinking, answer, convID, err := s.Engine.ChatReasoning(r.Context(), req.AgentID, req.Prompt, req.History, req.ConversationID, req.Model)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, chatFriendlyError(err.Error()))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":         answer,
			"thinking":        thinking,
			"conversation_id": convID,
			"message_id":      req.MessageID,
			"citations":       agentic.ExtractCitations(answer),
		})
		return
	}

	out, convID, err := s.Engine.Chat(r.Context(), req.AgentID, req.Prompt, req.History, req.ConversationID, req.Model)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, chatFriendlyError(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content":         out,
		"conversation_id": convID,
		"message_id":      req.MessageID,
		"citations":       agentic.ExtractCitations(out),
	})
}

// handleChatStream SSE 流式输出。
func (s *Service) handleChatStream(w http.ResponseWriter, r *http.Request, req *struct {
	Prompt         string        `json:"prompt"`
	AgentID        string        `json:"agent_id"`
	History        []llm.Message `json:"history"`
	Stream         bool          `json:"stream"`
	Reasoning      bool          `json:"reasoning"`
	Explore        bool          `json:"explore"`
	Model          string        `json:"model"`
	ConversationID string        `json:"conversation_id"`
	MessageID      string        `json:"message_id"`
}) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httputil.WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 注入审批通知回调: 工具执行触发敏感操作审批时, 通过本 SSE 连接下发 ask 事件, 前端弹窗确认。
	ctx := agentic.WithApprovalNotify(r.Context(), func(pa *agentic.PendingApproval) {
		payload, _ := json.Marshal(map[string]any{
			"id":       pa.ID,
			"tool":     pa.Tool,
			"summary":  pa.Summary,
			"args":     pa.Args,
			"deadline": pa.Deadline.Format(time.RFC3339),
			"agent_id": pa.AgentID,
		})
		fmt.Fprintf(w, "event: ask\ndata: %s\n\n", payload)
		flusher.Flush()
	})

	ch, convID, err := s.Engine.ChatStream(ctx, req.AgentID, req.Prompt, req.History, req.Reasoning, req.ConversationID, req.Explore, req.Model)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	var full strings.Builder
	for c := range ch {
		switch c.Type {
		case "thinking":
			// 思考过程: 通过命名事件下发, 前端可通过 event:thinking 独立渲染/折叠
			b, _ := json.Marshal(c.Content)
			fmt.Fprintf(w, "event: thinking\ndata: %s\n\n", b)
		case "error":
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", c.Content)
		case "tool":
			// 工具标注走默认 data 流, 与 token 同轨(保持向前兼容)
			b, _ := json.Marshal(c.Content)
			fmt.Fprintf(w, "data: %s\n\n", b)
			full.WriteString(c.Content)
		default:
			// "token" 或未指定: 标准回答 token
			full.WriteString(c.Content)
			b, _ := json.Marshal(c.Content)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		flusher.Flush()
	}
	if convID != "" {
		fmt.Fprintf(w, "event: conversation\ndata: %s\n\n", convID)
		flusher.Flush()
	}
	// 引用来源: 从完整回答中解析 [^<id>] 标注并下发, 前端可据此展示知识库出处。
	if cites := agentic.ExtractCitations(full.String()); len(cites) > 0 {
		if cb, err := json.Marshal(cites); err == nil {
			fmt.Fprintf(w, "event: citations\ndata: %s\n\n", cb)
			flusher.Flush()
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// UpdateLLMConfig 热更新 LLM 配置(sysconfig 修改 LLM 设置后调用)。
// 同时热更新多模型路由池(附加 provider 取自 cfg.LLM.Providers), 不停机生效。
func (s *Service) UpdateLLMConfig(cfg llm.Config) {
	var extra []agentic.ProviderConfig
	if s.cfg != nil {
		for _, p := range s.cfg.LLM.Providers {
			extra = append(extra, agentic.ProviderConfig{
				Name:    p.Name,
				APIKey:  p.APIKey,
				Model:   p.Model,
				BaseURL: p.BaseURL,
				Weight:  p.Weight,
			})
		}
	}
	s.Engine.HotUpdateRouter(cfg, extra)
	// 结构化生成走的是独立客户端，这里一并换掉，避免改完配置后看板生成还在用旧 key。
	s.llmMu.Lock()
	s.llmClient = llm.NewClient(cfg)
	s.llmMu.Unlock()
}

// ——— 对话持久 ———

func (s *Service) handleSaveConversation(w http.ResponseWriter, r *http.Request) {
	var conv agentic.Conversation
	if err := json.NewDecoder(r.Body).Decode(&conv); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	conv.TenantID = tenantFromCtx(r.Context())
	if err := s.Engine.SaveConversation(r.Context(), &conv); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, conv)
}

// handleDeleteConversation 删除会话(属主可删)。
func (s *Service) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	userID := currentUserID(r.Context())
	if s.convAccess(r.Context(), tenantID, id, userID, s.userGroups(r.Context(), userID)) != "owner" {
		httputil.WriteError(w, http.StatusForbidden, "only the owner can delete this conversation")
		return
	}
	if err := s.Engine.DeleteConversation(id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	// 连带清理共享记录(防孤儿)
	if s.db != nil {
		if _, err := s.db.ExecContext(r.Context(),
			`DELETE FROM ai_conversation_shares WHERE conversation_id=$1`, id); err != nil {
			slog.Warn("conversation shares cleanup failed", "id", id, "err", err)
		}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— 进化闭环概览 ———

// KnowledgeStatsHook 由 cmd/core 注入: 返回知识库侧闭环统计(处置案例数)。nil=未注入。
var KnowledgeStatsHook func(ctx context.Context) (disposalCases int)

// handleEvolutionStats 进化闭环统计: 反馈(点赞/点踩) + 长期记忆 + 处置案例沉淀。
func (s *Service) handleEvolutionStats(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	stats := struct {
		FeedbackUp    int `json:"feedback_up"`
		FeedbackDown  int `json:"feedback_down"`
		Memories      int `json:"memories"`
		DisposalCases int `json:"disposal_cases"`
	}{Memories: s.Engine.CountMemories(tenantID)}
	if s.db != nil {
		_ = s.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FILTER (WHERE rating='up'), COUNT(*) FILTER (WHERE rating='down') FROM ai_message_feedback WHERE tenant_id=$1`,
			tenantID).Scan(&stats.FeedbackUp, &stats.FeedbackDown)
	}
	if KnowledgeStatsHook != nil {
		stats.DisposalCases = KnowledgeStatsHook(r.Context())
	}
	httputil.WriteJSON(w, http.StatusOK, stats)
}

// handleCreateAgent godoc
// @Summary 创建 AI Agent
// @Description 创建一个新的 AI Agent 实例
// @Tags AI
// @Accept json
// @Produce json
// @Param agent body object true "Agent 配置"
// @Success 201 {object} model.AgentInstance
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/ai/agents [post]
// @Security BearerAuth
func (s *Service) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                string             `json:"name"`
		ModelName           string             `json:"model_name"`
		SystemPrompt        string             `json:"system_prompt"`
		KnowledgeCategories []string           `json:"knowledge_categories"`
		Tools               []string           `json:"tools"`
		Skills              []model.AgentSkill `json:"skills"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	agent := &model.AgentInstance{
		Name:                req.Name,
		ModelName:           req.ModelName,
		SystemPrompt:        req.SystemPrompt,
		KnowledgeCategories: req.KnowledgeCategories,
		Tools:               req.Tools,
		Skills:              req.Skills,
		TenantID:            tenantFromCtx(r.Context()),
	}

	if err := s.Engine.CreateAgent(r.Context(), agent); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, agent)
}

func (s *Service) handleListAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	all := s.Engine.ListAgents()
	filtered := make([]*model.AgentInstance, 0)
	for _, a := range all {
		if a.TenantID == tenantID {
			filtered = append(filtered, a)
		}
	}
	if filtered == nil {
		filtered = []*model.AgentInstance{}
	}
	httputil.WriteJSON(w, http.StatusOK, filtered)
}

func (s *Service) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	agent, ok := s.Engine.GetAgent(id)
	if !ok || agent.TenantID != tenantFromCtx(r.Context()) {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, agent)
}

func (s *Service) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := s.Registry.List()
	if tools == nil {
		tools = []mcp.ToolDefinition{}
	}
	httputil.WriteJSON(w, http.StatusOK, tools)
}

// StartExternalMCP 从环境变量加载外部 MCP server 配置并接入。
// 应在 AI 服务初始化、内置工具注册完成后调用(Manager 会把远端工具
// proxy 进 s.Registry,与内置工具共享同一注册表)。
// 无配置时安全空转,不影响既有功能。
func (s *Service) StartExternalMCP(ctx context.Context) {
	cfgs, err := mcp.LoadServersFromEnv()
	if err != nil {
		slog.Error("load external mcp servers failed", "error", err)
		return
	}
	if len(cfgs) == 0 {
		slog.Info("no external mcp servers configured, skip")
		return
	}
	if err := s.MCPManager.Start(ctx, cfgs); err != nil {
		slog.Error("start external mcp manager failed", "error", err)
	}
}

// handleMCPServers 返回外部 MCP server 的运行时状态(连接/工具数/错误)。
func (s *Service) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.MCPManager.Status())
}

func (s *Service) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	// P2-1: 工具执行端点鉴权 + 限流(此前为裸奔端点)。
	// 工具含 script_run(在受管主机执行脚本)等高风险操作,仅 operator/admin 可执行,
	// 且按租户维度令牌桶限流,防止滥用。
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}

	name := r.PathValue("name")

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := tenantFromCtx(r.Context())
	if s.limiter != nil && !s.limiter.Allow(tenantID+":tool:"+name) {
		httputil.WriteError(w, http.StatusTooManyRequests, "rate limit exceeded for tool execution")
		return
	}

	result, err := s.Registry.Execute(r.Context(), name, input)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"result": result})
}

// ——— 自定义 MCP 工具注册 (PRD 5.2.2) ———

// validateCustomTool 校验自定义工具定义的合法性。
func validateCustomTool(t *agentic.CustomTool) error {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if !regexpMustCompile(`^[a-z][a-z0-9_]{1,63}$`).MatchString(name) {
		return fmt.Errorf("tool name must match ^[a-z][a-z0-9_]{1,63}$ (e.g. weather_query)")
	}
	if t.Type != "http" && t.Type != "cli" {
		return fmt.Errorf("tool type must be http or cli")
	}
	if t.Type == "http" && strings.TrimSpace(t.HTTPURL) == "" {
		return fmt.Errorf("http_url is required for http tools")
	}
	if t.Type == "cli" && strings.TrimSpace(t.CLICommand) == "" {
		return fmt.Errorf("cli_command is required for cli tools")
	}
	for _, p := range t.Parameters {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("parameter name is required")
		}
		switch p.Type {
		case "string", "number", "boolean":
		default:
			return fmt.Errorf("parameter %q has invalid type %q (string|number|boolean)", p.Name, p.Type)
		}
	}
	return nil
}

func regexpMustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

func (s *Service) handleCreateCustomTool(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var t agentic.CustomTool
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCustomTool(&t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.TenantID = tenantFromCtx(r.Context())
	if err := s.Engine.CreateCustomTool(r.Context(), &t); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, t)
}

func (s *Service) handleListCustomTools(w http.ResponseWriter, r *http.Request) {
	items := s.Engine.ListCustomTools(tenantFromCtx(r.Context()))
	if items == nil {
		items = []*agentic.CustomTool{}
	}
	httputil.WriteJSON(w, http.StatusOK, items)
}

func (s *Service) handleUpdateCustomTool(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	id := r.PathValue("id")
	var t agentic.CustomTool
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCustomTool(&t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.ID = id
	t.TenantID = tenantFromCtx(r.Context())
	if err := s.Engine.UpdateCustomTool(r.Context(), &t); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) handleDeleteCustomTool(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	id := r.PathValue("id")
	if err := s.Engine.DeleteCustomTool(r.Context(), id, tenantFromCtx(r.Context())); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// authorize 检查当前请求的 RBAC 权限;不满足时写入错误响应并返回 false。
// 身份(role)由全局 auth/audit/rbac 中间件注入 context;此处做纵深防御 + 端点级权限约束。
func (s *Service) authorize(w http.ResponseWriter, r *http.Request, perm string) bool {
	role := auth.RoleFromContext(r.Context())
	if role == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !auth.HasPermission(role, perm) {
		httputil.WriteError(w, http.StatusForbidden, "forbidden: missing permission "+perm)
		return false
	}
	return true
}

func (s *Service) handleExecutePlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID   string       `json:"agent_id"`
		RiskLevel string       `json:"risk_level"`
		Steps     []model.Step `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	plan := &model.ExecutionPlan{
		ID:        uuid.New().String(),
		AgentID:   req.AgentID,
		TenantID:  tenantFromCtx(r.Context()),
		RiskLevel: req.RiskLevel,
		Steps:     req.Steps,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	if err := s.Engine.ExecutePlan(r.Context(), plan); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, plan)
}

func (s *Service) handleListPlans(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	all := s.Engine.ListPlans()
	filtered := make([]*model.ExecutionPlan, 0)
	for _, p := range all {
		if p.TenantID == tenantID {
			filtered = append(filtered, p)
		}
	}
	if filtered == nil {
		filtered = []*model.ExecutionPlan{}
	}
	httputil.WriteJSON(w, http.StatusOK, filtered)
}

func (s *Service) handleGetRateLimit(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id":   agentID,
		"state":      s.limiter.GetState(agentID),
		"call_count": s.limiter.GetCount(agentID),
		"limit":      ratelimit.DefaultLimit,
		"window_s":   ratelimit.DefaultWindow.Seconds(),
	})
}

// ——— 敏感操作审批策略 (PRD 5.2.5) ———

// handleGetApprovalPolicy 读取审批策略。?agent_id=xxx 时返回该 Agent 的覆盖策略(可能不存在),
// 不传时返回全局默认。
func (s *Service) handleGetApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	policy := s.Engine.GetApprovalPolicy(agentID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"policy":   policy,
	})
}

// handlePutApprovalPolicy 保存审批策略。body: {agent_id?, mode?, sensitive_tools?, ask_timeout_sec?}
// agent_id 为空或 "default" 表示全局; 字段缺省时保留原策略对应值。
func (s *Service) handlePutApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID        string   `json:"agent_id"`
		Mode           string   `json:"mode"`
		SensitiveTools []string `json:"sensitive_tools"`
		AskTimeoutSec  int      `json:"ask_timeout_sec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	agentID := req.AgentID
	key := agentID
	if key == "" {
		key = "default"
	}
	cur := s.Engine.GetApprovalPolicy(key)

	switch req.Mode {
	case string(agentic.ApprovalAlwaysAsk), string(agentic.ApprovalAlwaysAllow), string(agentic.ApprovalSensitiveOnly):
		cur.Mode = agentic.ApprovalPolicyMode(req.Mode)
	case "":
		// 保留原值
	default:
		httputil.WriteError(w, http.StatusBadRequest, "invalid mode: must be always_ask | always_allow | sensitive_only")
		return
	}
	if req.SensitiveTools != nil {
		cur.SensitiveTools = req.SensitiveTools
	}
	if req.AskTimeoutSec > 0 {
		cur.AskTimeoutSec = req.AskTimeoutSec
	}

	s.Engine.SetApprovalPolicy(key, cur)
	slog.Info("ai approval policy updated", "agent_id", key, "mode", cur.Mode)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"policy":   cur,
	})
}

// handleListPendingApprovals 列出当前租户挂起的审批请求。
func (s *Service) handleListPendingApprovals(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	items := s.Engine.Approvals().ListPending(tenantID)
	if items == nil {
		items = []*agentic.PendingApproval{}
	}
	httputil.WriteJSON(w, http.StatusOK, items)
}

// handleRespondApproval 对挂起的审批请求做出响应: {action: "approve"|"reject", comment?}
func (s *Service) handleRespondApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Action  string `json:"action"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Action != "approve" && req.Action != "reject" {
		httputil.WriteError(w, http.StatusBadRequest, "action must be approve or reject")
		return
	}
	by := auth.UserIDFromContext(r.Context())
	if by == "" {
		by = "human"
	}
	if !s.Engine.Approvals().Respond(id, req.Action, by, req.Comment) {
		httputil.WriteError(w, http.StatusNotFound, "approval request not found or already expired")
		return
	}
	slog.Info("ai approval responded", "id", id, "action", req.Action, "by", by)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "action": req.Action})
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

// userFromCtx 从请求上下文解析当前用户标识(供评分/收藏等按用户维度去重的操作)。
// 与 finetune_handlers.go 的取值约定保持一致(鉴权中间件注入 user_id)。
func userFromCtx(r *http.Request) string {
	if u, ok := r.Context().Value("user_id").(string); ok && u != "" {
		return u
	}
	return "anonymous"
}

// ——— 反馈(Eval 与反馈, P2) ———

// handleSubmitFeedback 接收用户对一条 AI 消息的点赞/点踩并落库(upsert)。
func (s *Service) handleSubmitFeedback(w http.ResponseWriter, r *http.Request) {
	if s.feedbackStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "feedback store not configured")
		return
	}
	var req struct {
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		Rating         string `json:"rating"` // "up" | "down"
		Comment        string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Rating != "up" && req.Rating != "down" {
		httputil.WriteError(w, http.StatusBadRequest, "rating must be 'up' or 'down'")
		return
	}
	if req.MessageID == "" && req.ConversationID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "message_id or conversation_id is required")
		return
	}
	tenant := tenantFromCtx(r.Context())
	f := &agentic.Feedback{
		TenantID:       tenant,
		ConversationID: req.ConversationID,
		MessageID:      req.MessageID,
		Rating:         req.Rating,
		Comment:        req.Comment,
	}
	if err := s.feedbackStore.Save(r.Context(), f); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to save feedback")
		return
	}
	// 反馈回流(非阻断): 点赞→正向经验 / 点踩→反例, 沉淀为长期记忆(自动落库)
	s.reflowFeedback(r.Context(), tenant, f)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": f.ID, "ok": true})
}

// reflowFeedback 把赞踩反馈回流为长期记忆: 取该会话最新一轮问答,
// 点赞沉淀正向经验(source=feedback_up), 点踩沉淀反例(source=feedback_down)。
// 失败仅记录日志, 不影响反馈主流程。
func (s *Service) reflowFeedback(ctx context.Context, tenant string, f *agentic.Feedback) {
	if f.ConversationID == "" || s.Engine == nil {
		return
	}
	conv, err := s.Engine.GetConversation(f.ConversationID)
	if err != nil || conv == nil {
		return
	}
	var lastUser, lastAssistant string
	for _, m := range conv.Messages {
		if m.Role == "user" {
			lastUser = m.Content
		}
		if m.Role == "assistant" {
			lastAssistant = m.Content
		}
	}
	if lastUser == "" || lastAssistant == "" {
		return
	}
	source := agentic.MemorySourceFeedbackUp
	outcome := "succeeded"
	if f.Rating == "down" {
		source = agentic.MemorySourceFeedbackDown
		outcome = "false_positive"
	}
	if _, err := s.Engine.AddMemory(&agentic.ExperienceMemory{
		TenantID:   tenant,
		Category:   "对话反馈",
		Problem:    truncateRunes(lastUser, 80),
		Resolution: truncateRunes(lastAssistant, 500),
		Outcome:    outcome,
		Source:     source,
	}); err != nil {
		slog.Warn("feedback reflow to memory failed", "err", err)
	}
}

// truncateRunes 按字符数截断(中文安全)。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// handleListFeedback 返回当前租户的反馈列表(可按 conversation_id 过滤)。
func (s *Service) handleListFeedback(w http.ResponseWriter, r *http.Request) {
	if s.feedbackStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "feedback store not configured")
		return
	}
	tenant := tenantFromCtx(r.Context())
	convID := r.URL.Query().Get("conversation_id")
	list, err := s.feedbackStore.List(r.Context(), tenant, convID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list feedback")
		return
	}
	if list == nil {
		list = []*agentic.Feedback{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"feedbacks": list})
}

// handleFeedbackStats 返回当前租户的反馈聚合统计(点赞/点踩计数)。
func (s *Service) handleFeedbackStats(w http.ResponseWriter, r *http.Request) {
	if s.feedbackStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "feedback store not configured")
		return
	}
	tenant := tenantFromCtx(r.Context())
	up, down, err := s.feedbackStore.Stats(r.Context(), tenant)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to compute feedback stats")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"up": up, "down": down, "total": up + down})
}

// handleEvalCases 返回内置运维评测集,供前端展示或离线评估对话质量。
func (s *Service) handleEvalCases(w http.ResponseWriter, r *http.Request) {
	cases := eval.DefaultEvalSet()
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"cases": cases})
}

func (s *Service) handleTokenStats(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	stats := s.Engine.GetTokenStats(period)
	httputil.WriteJSON(w, http.StatusOK, stats)
}

// handleUpdateSettings 运行时调整生成参数(温度/max_tokens)与物理 LLM 并发上限。
// 字段为 0 时保持引擎当前值(引擎内置默认: 温度 0.7 / 上限 1024 / 并发 32)。
func (s *Service) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxTokens   int     `json:"max_tokens"`
		Temperature float64 `json:"temperature"`
		MaxInflight int     `json:"max_inflight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MaxTokens > 0 {
		s.Engine.SetGenParams(req.MaxTokens, req.Temperature)
	} else if req.Temperature > 0 {
		s.Engine.SetGenParams(0, req.Temperature)
	}
	if req.MaxInflight > 0 {
		s.Engine.SetLLMMaxInflight(req.MaxInflight)
	}
	slog.Info("ai settings updated", "max_tokens", req.MaxTokens, "temperature", req.Temperature, "max_inflight", req.MaxInflight)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var ag model.AgentInstance
	if err := json.NewDecoder(r.Body).Decode(&ag); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ag.ID = id
	ag.TenantID = tenantFromCtx(r.Context())
	if err := s.Engine.UpdateAgent(r.Context(), &ag); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ag)
}

func (s *Service) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantFromCtx(r.Context())
	if err := s.Engine.DeleteAgent(r.Context(), id, tenant); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— Feature 1: 多智能体团队 ———

func (s *Service) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var team model.AgentTeam
	if err := json.NewDecoder(r.Body).Decode(&team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team.TenantID = tenantFromCtx(r.Context())
	if err := s.Engine.CreateTeam(r.Context(), &team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, team)
}

func (s *Service) handleListTeams(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	teams := s.Engine.ListTeams(tenantID)
	if teams == nil {
		teams = []*model.AgentTeam{}
	}
	httputil.WriteJSON(w, http.StatusOK, teams)
}

func (s *Service) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	team, ok := s.Engine.GetTeam(id)
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "team not found")
		return
	}
	if team.TenantID != tenantFromCtx(r.Context()) {
		httputil.WriteError(w, http.StatusNotFound, "team not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, team)
}

func (s *Service) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var team model.AgentTeam
	if err := json.NewDecoder(r.Body).Decode(&team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team.ID = id
	team.TenantID = tenantFromCtx(r.Context())
	if err := s.Engine.UpdateTeam(r.Context(), &team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, team)
}

func (s *Service) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantFromCtx(r.Context())
	if err := s.Engine.DeleteTeam(r.Context(), id, tenant); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleTeamChat 团队对话(SSE 流式)：逐成员发射 agent_start/token/agent_end,
// 最后发射 final。任成员失败发射 error 事件并继续, 全部失败才返回错误。
func (s *Service) handleTeamChat(w http.ResponseWriter, r *http.Request) {
	var req model.TeamChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TeamID == "" {
		req.TeamID = r.PathValue("id")
	}
	if req.Message == "" {
		httputil.WriteError(w, http.StatusBadRequest, "message is required")
		return
	}

	team, ok := s.Engine.GetTeam(req.TeamID)
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "team not found")
		return
	}
	if team.TenantID != tenantFromCtx(r.Context()) {
		httputil.WriteError(w, http.StatusNotFound, "team not found")
		return
	}

	// F4-D1: 逻辑会话计数在入口, 与物理 LLM 槽位解耦
	agentic.AIConcurrentSessions.Inc()
	defer agentic.AIConcurrentSessions.Dec()

	flusher, ok := w.(http.Flusher)
	if !ok {
		httputil.WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// emit 必须串行化: 并行团队模式下多个 worker goroutine 会并发调用 emit,
	// 而 http.ResponseWriter 并非并发安全; 不加锁会导致并发 Fprintf+Flush 破坏
	// 底层连接, 触发 server 取消请求 ctx, 使所有 worker 集体 ctx.Done()。
	var emitMu sync.Mutex
	emit := func(ev model.TeamStreamEvent) {
		b, _ := json.Marshal(ev)
		emitMu.Lock()
		defer emitMu.Unlock()
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// P2-2: 加载该团队的历史对话(跨轮记忆), 本轮结束后追加 user+assistant 一轮。
	history := s.Engine.LoadTeamHistory(team.ID, 20)
	tenantID := tenantFromCtx(r.Context())

	// RAG 上下文注入: 对用户消息检索知识库, 注入到团队成员/汇总者/主管的系统提示词中。
	// 团队场景无单一 Agent, 检索全部知识库(绑定过滤按成员 Agent 在工具链内生效)。
	var ragCtx string
	if s.ragFn != nil {
		ragCtx = s.ragFn(r.Context(), req.Message, "")
	}

	final, _, err := s.Engine.RunTeamChat(r.Context(), team, req.Message, history, emit, ragCtx)
	if err != nil {
		emit(model.TeamStreamEvent{Type: "error", Content: chatFriendlyError(err.Error())})
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	// 持久化本轮对话,供下一轮作为历史注入
	s.Engine.AppendTeamTurn(team.ID, tenantID, req.Message, final)

	// RunTeamChat 已发射 final 事件; 保险再发一次确保闭合
	_ = final
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// chatFriendlyError 将底层 LLM 错误转为用户友好的提示。
func chatFriendlyError(raw string) string {
	if strings.Contains(raw, "401") || strings.Contains(raw, "Auth") || strings.Contains(raw, "auth") {
		return "LLM API Key 无效或未配置，请在「设置 → 模型」中填写正确的 API Key"
	}
	if strings.Contains(raw, "rate limit") {
		return "请求频率过高，请稍后再试"
	}
	if strings.Contains(raw, "timeout") || strings.Contains(raw, "deadline") {
		return "LLM 响应超时，请检查网络或增加超时时间"
	}
	if strings.Contains(raw, "connection refused") {
		return "LLM 服务连接失败，请检查 Base URL 和网络"
	}
	return raw
}
