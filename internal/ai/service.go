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
	ragFn         func(ctx context.Context, query, agentID string, categories []string) string // RAG上下文注入(按 Agent 绑定分类过滤, categories 为前端显式选择的范围)
	citeVerifyFn  func(ctx context.Context, tenant string, ids []string) map[string]bool       // 引用校验: 过滤模型幻觉/已删除文章的 [^id], 未注入时跳过校验
	feedbackStore agentic.FeedbackStore                                                        // 点赞/点踩反馈持久化(P2-Eval)
	db            *sql.DB                                                                      // 统计/管理查询直连(会话删除、进化闭环等)
	objStore      *objectstore.Client                                                          // 对话产物/文件归档(MinIO), 未配置为 nil
	userGroupsFn  UserGroupsFn                                                                 // 用户组查询(会话共享按组匹配), nil=不支持组共享

	// 任务级 Agent(T1): 后台 Runner 的取消句柄与事件总线。taskCancels 生命周期由 tasks.go 管理。
	taskMu      sync.Mutex
	taskCancels map[string]context.CancelFunc
	taskBus     *taskEventBus
	// 任务级 Agent(T3): taskLive=Runner 持有的活体任务指针(工具 handler 与 Runner 同 goroutine
	// 执行, 直接改写活体避免存储副本分叉); taskApprovals=等待/最近批复的审批单; taskPlanning=规划轮去重。
	taskLive      map[string]*AITask
	taskApprovals map[string]*taskApproval
	taskPlanning  map[string]bool
	// 任务级 Agent(T4): taskSteers=运行中插话(steer)队列, Runner 在步骤边界安全注入会话;
	// taskPaused=暂停标记(取消 in-flight 时区分「暂停」与「取消」, 决定终态 paused/cancelled)。
	taskSteers map[string][]steerEntry
	taskPaused map[string]bool
	// 任务级 Agent(T6): 多任务并行调度——taskRunning=并发槽占位(startTask 即置位, 规避
	// go runTask 注册 cancel 的竞态窗口); taskQueue=等待队列(仅存 ID, 排序取元数据)。
	taskRunning map[string]bool
	taskQueue   []string
	// knowledgeCreate 任务产物沉淀知识库的回调(T2, main.go 注入; nil=未接入)
	knowledgeCreate func(ctx context.Context, title, content, category, author string) (articleID string, err error)

	// llmClient 供"非对话式"结构化生成场景直连模型(如 AI 生成报表看板),
	// 不走 Engine 的对话/工具循环。sysconfig 改配置时随 UpdateLLMConfig 一起热替换。
	llmMu     sync.RWMutex
	llmClient *llm.Client
}

func NewService(cfg *config.Config, llmClient *llm.Client, redisClient *redisclient.Client, db *sql.DB) *Service {
	reg := mcp.NewRegistry()

	// 载入配置中的用户自定义 LLM 厂商预设(同名覆盖内置项), 供 /ai/llm-presets 查询。
	SetCustomPresets(cfg.LLM.Presets)

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
		taskCancels:   map[string]context.CancelFunc{},
		taskBus:       newTaskEventBus(),
		taskLive:      map[string]*AITask{},
		taskApprovals: map[string]*taskApproval{},
		taskPlanning:  map[string]bool{},
		taskSteers:    map[string][]steerEntry{},
		taskPaused:    map[string]bool{},
		taskRunning:   map[string]bool{},
		taskQueue:     []string{},
	}
	svc.initEvolutionDDL(context.Background())
	svc.initArtifactDDL(context.Background())
	svc.initAttachmentDDL(context.Background())
	svc.initWorkspaceDDL(context.Background())
	svc.initMemoryDDL(context.Background())
	svc.initTaskDDL(context.Background())
	svc.registerTaskTools()
	svc.recoverTaskQueue() // T6: 重启后从 DB 重建等待队列(内存队列丢失兜底)
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

func (s *Service) SetRAGFunc(fn func(ctx context.Context, query, agentID string, categories []string) string) {
	s.ragFn = fn
}

// SetCitationVerifier 注入引用校验回调(main.go 装配 MCPToolSet, 避免 ai→knowledge 直接依赖)。
// 校验失败的引用 ID 不下发给前端, 防止「引用来源」链接到不存在/已删除的知识库文章。
func (s *Service) SetCitationVerifier(fn func(ctx context.Context, tenant string, ids []string) map[string]bool) {
	s.citeVerifyFn = fn
}

// validCitations 过滤无效引用: verifier 未注入或查询失败时放行原列表(宁可多展示也不误杀)。
// 引用串可能带类型前缀(kb:/ci:/mon:): 仅 kb 类型(知识库文章)需要走 verifier 存在性校验,
// ci(CMDB 配置项)/mon(监控) 类型无法用知识库 verifier 校验, 直接保留(其存在性由前端跳转时再核验)。
func (s *Service) validCitations(ctx context.Context, tenantID string, ids []string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, 0, len(ids))
	var kbRaw []string
	for _, id := range ids {
		// 带类型前缀且非 kb → 直接保留(免校验)
		if i := strings.Index(id, ":"); i > 0 {
			switch id[:i] {
			case "ci", "mon":
				out = append(out, id)
				continue
			case "kb":
				kbRaw = append(kbRaw, id[i+1:])
				continue
			}
		}
		// 无前缀 或 未知前缀 → 视为 kb 原文, 走校验
		kbRaw = append(kbRaw, id)
	}
	if len(kbRaw) == 0 {
		return out
	}
	if s.citeVerifyFn == nil {
		out = append(out, kbRaw...)
		return out
	}
	ok := s.citeVerifyFn(ctx, tenantID, kbRaw)
	for _, raw := range kbRaw {
		if ok[raw] {
			out = append(out, raw) // 保持 kb 无前缀, 兼容旧前端
		}
	}
	return out
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
	mux.HandleFunc("GET /api/v1/ai/llm-presets", s.handleListLLMPresets)
	mux.HandleFunc("GET /api/v1/ai/conversations", s.handleListConversations)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("POST /api/v1/ai/conversations", s.handleSaveConversation)
	mux.HandleFunc("DELETE /api/v1/ai/conversations/{id}", s.handleDeleteConversation)
	mux.HandleFunc("PATCH /api/v1/ai/conversations/{id}", s.handleRenameConversation)
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
	// —— 服务端文档导出(前端保险丝 fallback, pandoc 确定性转换) ——
	mux.HandleFunc("POST /api/v1/ai/export", s.handleExportDoc)
	// —— AI 重编演示文稿(LLM 提炼 markdown → 分页大纲 JSON, 前端 pptxgenjs 渲染) ——
	mux.HandleFunc("POST /api/v1/ai/presentation", s.handleGeneratePresentation)
	// —— 对话附件(用户上传, MinIO) ——
	mux.HandleFunc("POST /api/v1/ai/attachments", s.handleUploadAttachment)
	mux.HandleFunc("GET /api/v1/ai/attachments/{id}", s.handleDownloadAttachment)
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

	// 任务级 Agent(T1): 目标驱动、后台执行、SSE 进度流
	s.registerTaskRoutes(mux)

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

	// —— 自动修复执行 (L1: 巡检发现 -> 分级处置 blocked/auto/review + 验证闭环) ——
	mux.HandleFunc("POST /api/v1/ai/inspections/{id}/runs/{runID}/remediate/generate", s.handleGenerateRemediations)
	mux.HandleFunc("GET /api/v1/ai/inspections/{id}/runs/{runID}/remediate", s.handleListRemediations)
	mux.HandleFunc("POST /api/v1/ai/remediate/{actionID}/execute", s.handleExecuteRemediation)

	// —— 可复用剧本 (runbook: 成功且验证通过的修复自动沉淀, 同因直接复用) ——
	mux.HandleFunc("GET /api/v1/ai/runbooks", s.handleListRunbooks)
	mux.HandleFunc("GET /api/v1/ai/runbooks/{id}", s.handleGetRunbook)
	mux.HandleFunc("POST /api/v1/ai/runbooks/{id}/execute", s.handleExecuteRunbook)

	// —— 自治分级 (notify/suggest/act_with_approval/autonomous) ——
	mux.HandleFunc("GET /api/v1/ai/autonomy", s.handleGetAutonomy)
	mux.HandleFunc("PUT /api/v1/ai/autonomy", s.handleSetAutonomy)

	// —— 告警第一道防线 (告警 → agent 自动分级) ——
	mux.HandleFunc("POST /api/v1/ai/alerts/triage", s.handleAlertTriage)

	// —— RCA 基准 (根因结论记录 + 人工标注准确率) ——
	mux.HandleFunc("POST /api/v1/ai/rca-records", s.handleRecordRCA)
	mux.HandleFunc("GET /api/v1/ai/rca-records", s.handleListRCARecords)
	mux.HandleFunc("POST /api/v1/ai/rca-records/{id}/label", s.handleLabelRCA)

	// —— 事故复盘沉淀 (复盘学习, 回灌记忆) ——
	mux.HandleFunc("POST /api/v1/ai/postmortems", s.handleCreatePostmortem)
	mux.HandleFunc("GET /api/v1/ai/postmortems", s.handleListPostmortems)
	mux.HandleFunc("POST /api/v1/ai/postmortems/{id}/publish", s.handlePublishPostmortem)

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
// chatRequest AI 聊天请求。流式/非流式共用同一结构(流式由 handleChatStream 接管)。
type chatRequest struct {
	Prompt              string        `json:"prompt"`
	AgentID             string        `json:"agent_id"`
	History             []llm.Message `json:"history"`
	Stream              bool          `json:"stream"`
	Reasoning           bool          `json:"reasoning"`
	Explore             bool          `json:"explore"` // 自主模式: AI 自主规划-执行-总结
	Model               string        `json:"model"`   // 可选运行级模型覆盖(按会话/请求指定); 空=回退 agent/引擎默认
	ConversationID      string        `json:"conversation_id"`
	MessageID           string        `json:"message_id"`           // 前端为本次 assistant 回复生成的稳定 ID, 反馈时用于精确关联
	KnowledgeCategories []string      `json:"knowledge_categories"` // 前端显式选择的知识范围(空=沿用 Agent 绑定/全部)
	Attachments         []struct {
		ID   string `json:"id"`
		Name string `json:"name,omitempty"`
		Type string `json:"type,omitempty"`
	} `json:"attachments"` // 本轮随消息上传的附件(图片/文档), 后端解析为多模态片段
	Parts []llm.ContentPart `json:"-"` // 已解析的多模态片段(服务端内部传递, 不接受客户端输入)
}

// isNativeReasoner 判断模型是否原生支持 reasoning_content 推理字段。
//
// 背景(关键): 引擎对非原生推理模型始终给 prompt 追加
// "<think>先进行分步推理分析</think><answer>再给出最终答案</answer>" 的格式要求(与是否开深度思考开关无关),
// 这样即使用户仅用"先分步推理分析再给答案"类指令而未开开关, 思考也能被 thinkFilter 分流折叠。
// 这对不支持原生推理字段的模型是必要的(prompt 伪思维链), 但对原生推理模型是"双重指令"——
// 模型既通过 reasoning_content 输出思维链, 又把同样的推理文本以"分步推理分析:"写进 content,
// 导致前端看到"思考过程与答案混在一起"。
//
// 在非流式路径(ChatReasoning)中, 原生推理模型不再注入 <think> prompt, 让它只走
// reasoning_content 通道, content 保持只有最终答案。
//
// 注意: 流式路径(ChatStream)对**非原生**推理模型(forceCoTPrompt := !isNativeReasoner(model))注入
// <think> prompt, 确保 thinkFilter 能把模型可能写进 content 的思维链物理分流; 原生推理模型不注入
// (避免双重指令), 但其 content 里由用户 prompt 诱导的「分步推理分析…再给出最终答案」双段由 cnCoTFilter
// 在流式循环里折叠为 event:thinking。reasoning_content 通道的捕获与二者都不冲突。
//
// 未识别的模型一律按"非原生"处理(保留原行为, 最保守)。
func isNativeReasoner(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	// 常见原生推理模型标识。注意刻意收紧: 过宽的前缀(如 "glm-4")会把同代非推理模型
	// (glm-4-flash / glm-4-air)也算进来, 反而丢掉本该有的 CoT。漏判的代价远小于误判——
	// 漏判只是回到"加 <think> prompt"的原行为, 误判则会让本可展示的思维链消失。
	nativeKeys := []string{
		"glm-4.5", "glm-4.6", "glm-5", "glm-zero", // 智谱思考系列(glm-4.6v-flash 等)
		"deepseek-r1", "deepseek-reasoner", // DeepSeek 推理系列
		"qwq",      // 阿里 QwQ 推理
		"o1", "o3", // OpenAI 推理系列
		"reasoner", "reasoning", // 通用兜底关键字
	}
	for _, k := range nativeKeys {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

func (s *Service) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Prompt == "" {
		httputil.WriteError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// 会话权限: 续接已有会话时, 非属主且无 edit 共享 → 拒绝(用户隔离); view 共享只读不能继续对话。
	// 若引用的会话不存在(如前端复用了已删除/重建前的旧 conversation_id),视为新会话,
	// 交由引擎以该 ID 新建(归属由 OwnerID 保障),不再拦截 —— 否则陈旧 ID 会让每次对话都吃 403。
	if req.ConversationID != "" {
		if _, gerr := s.Engine.GetConversation(req.ConversationID); gerr == nil {
			tenantID := auth.TenantFromContext(r.Context())
			userID := currentUserID(r.Context())
			if !s.requireConvWrite(r.Context(), tenantID, req.ConversationID, userID, s.userGroups(r.Context(), userID), w) {
				return
			}
		}
	}

	// P0-1: 清洗客户端提交的对话历史, 丢弃 system/tool 角色与注入的 tool_calls, 防止提示注入绕过 RBAC。
	req.History = agentic.SanitizeHistory(req.History)

	// 多模态输入: 把本轮上传的附件转为"引用态"片段(attachment://<id>), 会话表里只存引用不存 base64。
	// 历史消息中的同名引用由 Engine.resolvePartsForSend 在每次发送前统一解析(回放时附件依然可见)。
	tenantID := auth.TenantFromContext(r.Context())
	var parts []llm.ContentPart
	if len(req.Attachments) > 0 {
		refs := make([]AttachmentRef, 0, len(req.Attachments))
		for _, a := range req.Attachments {
			refs = append(refs, AttachmentRef{ID: a.ID, Name: a.Name, Type: a.Type})
		}
		parts = s.BuildContentParts(r.Context(), tenantID, req.Prompt, refs)
	}

	// RAG: 从知识库检索相关内容注入到 system prompt(按 Agent 绑定分类过滤, 空=全部)
	if s.ragFn != nil {
		ragCtx := s.ragFn(r.Context(), req.Prompt, req.AgentID, req.KnowledgeCategories)
		if ragCtx != "" {
			req.History = append([]llm.Message{{Role: "system", Content: "你是运维AI助手。参考知识库文档回答问题：\n" + ragCtx}}, req.History...)
		}
	}

	// F4-D1: 会话计数入口(逻辑并发, 与物理 LLM 槽位解耦)
	agentic.AIConcurrentSessions.Inc()
	defer agentic.AIConcurrentSessions.Dec()

	// Q1: 模糊意图追问 + 继续追问建议(与主回答并发生成, 不阻塞首 token)。
	// 仅当 prompt 疑似模糊/简短时才触发额外一次 LLM 调用, 明确长问不打扰。
	clarifyCh := make(chan *ClarificationResult, 1)
	if shouldGenerateClarify(req.Prompt) {
		go func() {
			defer func() { _ = recover() }()
			res, _ := s.generateClarify(r.Context(), req.Prompt)
			clarifyCh <- res
		}()
	} else {
		clarifyCh <- nil
	}

	if req.Stream {
		s.handleChatStream(w, r, &req, clarifyCh)
		return
	}

	if req.Reasoning && !req.Stream {
		thinking, answer, convID, err := s.Engine.ChatReasoning(r.Context(), req.AgentID, req.Prompt, req.History, req.ConversationID, req.Model, req.Explore, isNativeReasoner(req.Model), parts...)
		if err != nil {
			slog.Warn("ai chat reasoning failed", "model", req.Model, "conversation_id", req.ConversationID, "err", err.Error())
			httputil.WriteError(w, chatErrorStatus(err), chatFriendlyError(err.Error()))
			return
		}
		answer = stripEmptyCitationSection(answer)
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"content":         answer,
			"thinking":        thinking,
			"conversation_id": convID,
			"message_id":      req.MessageID,
			"citations":       s.validCitations(r.Context(), tenantID, agentic.ExtractCitations(answer)),
		}
		if extra := buildClarifyMap(awaitClarify(clarifyCh)); extra != nil {
			for k, v := range extra {
				payload[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode(payload)
		if convID != "" {
			go s.Engine.AutoTitleConversation(r.Context(), tenantID, convID)
		}
		return
	}

	out, convID, err := s.Engine.Chat(r.Context(), req.AgentID, req.Prompt, req.History, req.ConversationID, req.Model, req.Explore, parts...)
	if err != nil {
		slog.Warn("ai chat failed", "model", req.Model, "conversation_id", req.ConversationID, "err", err.Error())
		httputil.WriteError(w, chatErrorStatus(err), chatFriendlyError(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"content":         out,
		"conversation_id": convID,
		"message_id":      req.MessageID,
		"citations":       s.validCitations(r.Context(), tenantID, agentic.ExtractCitations(out)),
	}
	if extra := buildClarifyMap(awaitClarify(clarifyCh)); extra != nil {
		for k, v := range extra {
			payload[k] = v
		}
	}
	_ = json.NewEncoder(w).Encode(payload)
	if convID != "" {
		go s.Engine.AutoTitleConversation(r.Context(), tenantID, convID)
	}
}

// handleChatStream SSE 流式输出。clarifyCh 用于在主回答流结束后下发模糊意图追问与继续追问建议。
func (s *Service) handleChatStream(w http.ResponseWriter, r *http.Request, req *chatRequest, clarifyCh chan *ClarificationResult) {
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

	// 多模态: 把本轮上传的附件转为"引用态"片段(attachment://<id>), 与历史消息中的引用一并由
	// Engine.resolvePartsForSend 在发送前解析(会话持久化只存引用, 不存 base64)。
	if len(req.Attachments) > 0 {
		refs := make([]AttachmentRef, 0, len(req.Attachments))
		for _, a := range req.Attachments {
			refs = append(refs, AttachmentRef{ID: a.ID, Name: a.Name, Type: a.Type})
		}
		req.Parts = s.BuildContentParts(r.Context(), auth.TenantFromContext(r.Context()), req.Prompt, refs)
	}

	// RAG: 流式对话同样注入知识库检索(普通对话此前无 RAG; 仅当 Agent 绑定或前端显式选择范围时检索)。
	// 经 history 的 system 消息注入, Engine.ChatStream 内部会从历史提取 sysExtra 还原(agentic/engine.go:2712)。
	if s.ragFn != nil {
		if ragCtx := s.ragFn(r.Context(), req.Prompt, req.AgentID, req.KnowledgeCategories); ragCtx != "" {
			req.History = append([]llm.Message{{Role: "system", Content: "你是运维AI助手。参考知识库文档回答问题：\n" + ragCtx}}, req.History...)
		}
	}

	slog.Info("ai chat stream start",
		"model", req.Model,
		"conversation_id", req.ConversationID,
		"agent_id", req.AgentID,
		"reasoning", req.Reasoning,
		"prompt_len", len(req.Prompt),
		"attachments", len(req.Attachments))

	// 非原生推理模型始终注入 <think>...</think><answer>...</answer> 格式提示(与是否开启"深度思考"开关无关):
	//   - 引擎的 thinkFilter 会在流式过程中把 <think> 块单独抽成 event: thinking,
	//     推给前端 ThinkingBlock 折叠展示, 与答案物理隔离。
	//   - 之前"原生推理模型只走 reasoning_content"的策略在 glm-4.6v-flash 等
	//     实际未启用 thinking 参数的模型上会失效: 模型把推理和答案一起写进 content,
	//     导致"思考过程与答案混在一起"。统一注入 prompt 是更稳的兜底。
	// 非原生推理模型始终注入 <think>...</think> 格式: 确保 thinkFilter 能把模型可能写进 content 的
	// 思维链物理分流; 原生推理模型(isNativeReasoner)走 reasoning_content 通道, 不注入(避免双重指令),
	// 其 content 里由用户 prompt 诱导的「分步推理分析…再给出最终答案」双段由 cnCoTFilter 负责折叠。
	forceCoTPrompt := !isNativeReasoner(req.Model)
	ch, convID, err := s.Engine.ChatStream(ctx, req.AgentID, req.Prompt, req.History, forceCoTPrompt, req.ConversationID, req.Explore, req.Model, req.Parts...)
	if err != nil {
		// 记录原始错误(模型/路由/解析等)便于排查; UI 走 chatFriendlyError 文案。
		slog.Warn("ai chat stream init failed",
			"model", req.Model, "conversation_id", req.ConversationID, "err", err.Error())
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", chatFriendlyError(err.Error()))
		flusher.Flush()
		return
	}

	var full strings.Builder
	var sentError bool
	for c := range ch {
		switch c.Type {
		case "thinking":
			// 思考过程: 通过命名事件下发, 前端可通过 event:thinking 独立渲染/折叠
			b, _ := json.Marshal(c.Content)
			fmt.Fprintf(w, "event: thinking\ndata: %s\n\n", b)
		case "error":
			sentError = true
			// apiError 携带 Status/Body/RetryAfter; 此处透传状态码便于后端日志直接命中 4xx/5xx/限流。
			slog.Warn("ai chat stream upstream error",
				"model", req.Model, "conversation_id", req.ConversationID, "err", c.Content)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", chatFriendlyError(c.Content))
		case "truncated":
			// 上游 finish_reason=length → max_tokens 到限, 正文被截断。
			// 单独事件而非 error: 内容是真实存在且可能有用的, 不应像失败那样用红色错误块。
			slog.Info("ai chat stream truncated by max_tokens",
				"model", req.Model, "conversation_id", req.ConversationID)
			fmt.Fprintf(w, "event: truncated\ndata: %s\n\n",
				"输出已触达 max_tokens 上限, 后续内容未生成。可在「设置 → 模型」调大该模型的最大生成长度后重试。")
		case "tool":
			// 兼容旧客户端: 工具标注走默认 data 流(已不再由引擎产生, 保留分支避免遗漏)
			b, _ := json.Marshal(c.Content)
			fmt.Fprintf(w, "data: %s\n\n", b)
			full.WriteString(c.Content)
		case "tool_event":
			// 结构化工具事件: 前端据此渲染"任务列表"(并发任务可视化), 不污染回答正文。
			fmt.Fprintf(w, "event: tool\ndata: %s\n\n", c.Content)
		default:
			// "token" 或未指定: 标准回答 token
			full.WriteString(c.Content)
			b, _ := json.Marshal(c.Content)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		flusher.Flush()
	}

	// Silent-fail 守卫: 流正常关闭但 0 token 且未发过 error —— 多见于推理模型 thinking 后静默、
	// max_tokens 给的不够返回空、或上游异常结束连接。补一个明确错误, 不让用户看到空气泡。
	if !sentError && full.Len() == 0 {
		slog.Warn("ai chat stream empty content",
			"model", req.Model, "conversation_id", req.ConversationID,
			"reasoning", req.Reasoning, "history_len", len(req.History))
		fmt.Fprintf(w, "event: error\ndata: %s\n\n",
			"模型未返回任何内容：可能是 max_tokens 过小、推理模型思考后静默，或上游异常中断连接。\n请重试或切换其他模型。")
		flusher.Flush()
	}
	if convID != "" {
		fmt.Fprintf(w, "event: conversation\ndata: %s\n\n", convID)
		flusher.Flush()
	}
	// 引用来源: 从完整回答中解析 [^<id>] 标注, 经知识库存在性校验后下发(幻觉/已删除 ID 不下发),
	// 前端可据此展示知识库出处。
	if cites := s.validCitations(r.Context(), auth.TenantFromContext(r.Context()), agentic.ExtractCitations(full.String())); len(cites) > 0 {
		if cb, err := json.Marshal(cites); err == nil {
			fmt.Fprintf(w, "event: citations\ndata: %s\n\n", cb)
			flusher.Flush()
		}
	}
	// Q1: 模糊意图追问 + 继续追问建议(与主回答并发生成, 此处收敛下发)。
	// clarify 事件: 用户问题模糊时 AI 反问缺哪些信息; suggestions 事件: 可点击的继续追问建议。
	if res := awaitClarifyWithin(clarifyCh, 4*time.Second); res != nil {
		if res.NeedClarify && strings.TrimSpace(res.Question) != "" {
			if b, err := json.Marshal(map[string]any{"question": res.Question}); err == nil {
				fmt.Fprintf(w, "event: clarify\ndata: %s\n\n", b)
				flusher.Flush()
			}
		}
		if len(res.Suggestions) > 0 {
			if b, err := json.Marshal(res.Suggestions); err == nil {
				fmt.Fprintf(w, "event: suggestions\ndata: %s\n\n", b)
				flusher.Flush()
			}
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	// 首轮新会话: 异步用 LLM 生成会话标题(对标 DeepSeek/ChatGPT 自动命名, 失败静默保留截断标题)
	if convID != "" {
		go s.Engine.AutoTitleConversation(r.Context(), auth.TenantFromContext(r.Context()), convID)
	}
}

// UpdateLLMConfig 热更新 LLM 配置(sysconfig 修改 LLM 设置后调用)。
// 同时热更新多模型路由池(附加 provider 取自 cfg.LLM.Providers), 不停机生效。
// 模型列表(Providers)中的 Type/Visibility/CreatedBy/Primary 元数据一并透传给路由,
// 由路由按权重+健康+类型+可见性做 auto 选型; 带 Primary 标记的 provider 作为兜底 default。
func (s *Service) UpdateLLMConfig(cfg llm.Config) {
	var extra []agentic.ProviderConfig
	if s.cfg != nil {
		for _, p := range s.cfg.LLM.Providers {
			extra = append(extra, agentic.ProviderConfig{
				Name:       p.Name,
				APIKey:     p.APIKey,
				Model:      p.Model,
				BaseURL:    p.BaseURL,
				Weight:     p.Weight,
				Types:      p.Types,
				Type:       p.Type,
				Visibility: p.Visibility,
				CreatedBy:  p.CreatedBy,
				Primary:    p.Primary,
			})
		}
	}
	// 主配置留空时回退到池内权重最高的可用 provider, 使「仅用模型池」也能驱动
	// 报表/演进/知识库 embedding 等直连主客户端的场景(与路由池的 default 解析保持一致)。
	cfg = agentic.ResolvePrimary(cfg, extra)
	s.Engine.HotUpdateRouter(cfg, extra)
	// 结构化生成走的是独立客户端，这里一并换掉，避免改完配置后看板生成还在用旧 key。
	s.llmMu.Lock()
	s.llmClient = llm.NewClient(cfg)
	s.llmMu.Unlock()
}

// ——— 对话持久 ———

func (s *Service) handleSaveConversation(w http.ResponseWriter, r *http.Request) {
	var conv agentic.Conversation
	if err := httputil.BindJSON(r, &conv); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	conv.TenantID = auth.TenantFromContext(r.Context())
	if err := s.Engine.SaveConversation(r.Context(), &conv); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, conv)
}

// handleDeleteConversation 删除会话(属主可删)。
func (s *Service) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
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
	tenantID := auth.TenantFromContext(r.Context())
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
	if err := httputil.BindJSON(r, &req); err != nil {
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
		TenantID:            auth.TenantFromContext(r.Context()),
	}

	if err := s.Engine.CreateAgent(r.Context(), agent); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, agent)
}

func (s *Service) handleListAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
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
	if !ok || agent.TenantID != auth.TenantFromContext(r.Context()) {
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
	if err := httputil.BindJSON(r, &input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := auth.TenantFromContext(r.Context())
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
	if err := httputil.BindJSON(r, &t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCustomTool(&t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.TenantID = auth.TenantFromContext(r.Context())
	if err := s.Engine.CreateCustomTool(r.Context(), &t); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, t)
}

func (s *Service) handleListCustomTools(w http.ResponseWriter, r *http.Request) {
	items := s.Engine.ListCustomTools(auth.TenantFromContext(r.Context()))
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
	if err := httputil.BindJSON(r, &t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCustomTool(&t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.ID = id
	t.TenantID = auth.TenantFromContext(r.Context())
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
	if err := s.Engine.DeleteCustomTool(r.Context(), id, auth.TenantFromContext(r.Context())); err != nil {
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
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	plan := &model.ExecutionPlan{
		ID:        uuid.New().String(),
		AgentID:   req.AgentID,
		TenantID:  auth.TenantFromContext(r.Context()),
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
	tenantID := auth.TenantFromContext(r.Context())
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
	if err := httputil.BindJSON(r, &req); err != nil {
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
	tenantID := auth.TenantFromContext(r.Context())
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
	if err := httputil.BindJSON(r, &req); err != nil {
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
	if err := httputil.BindJSON(r, &req); err != nil {
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
	tenant := auth.TenantFromContext(r.Context())
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
	tenant := auth.TenantFromContext(r.Context())
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
	tenant := auth.TenantFromContext(r.Context())
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
	if err := httputil.BindJSON(r, &req); err != nil {
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
	if err := httputil.BindJSON(r, &ag); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ag.ID = id
	ag.TenantID = auth.TenantFromContext(r.Context())
	if err := s.Engine.UpdateAgent(r.Context(), &ag); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ag)
}

func (s *Service) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := auth.TenantFromContext(r.Context())
	if err := s.Engine.DeleteAgent(r.Context(), id, tenant); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— Feature 1: 多智能体团队 ———

func (s *Service) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var team model.AgentTeam
	if err := httputil.BindJSON(r, &team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team.TenantID = auth.TenantFromContext(r.Context())
	if err := s.Engine.CreateTeam(r.Context(), &team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, team)
}

func (s *Service) handleListTeams(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
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
	if team.TenantID != auth.TenantFromContext(r.Context()) {
		httputil.WriteError(w, http.StatusNotFound, "team not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, team)
}

func (s *Service) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var team model.AgentTeam
	if err := httputil.BindJSON(r, &team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team.ID = id
	team.TenantID = auth.TenantFromContext(r.Context())
	if err := s.Engine.UpdateTeam(r.Context(), &team); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, team)
}

func (s *Service) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := auth.TenantFromContext(r.Context())
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
	if err := httputil.BindJSON(r, &req); err != nil {
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
	if team.TenantID != auth.TenantFromContext(r.Context()) {
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
	tenantID := auth.TenantFromContext(r.Context())

	// RAG 上下文注入: 对用户消息检索知识库, 注入到团队成员/汇总者/主管的系统提示词中。
	// 团队场景无单一 Agent, 检索全部知识库(绑定过滤按成员 Agent 在工具链内生效)。
	var ragCtx string
	if s.ragFn != nil {
		ragCtx = s.ragFn(r.Context(), req.Message, "", nil)
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
	if strings.Contains(raw, "no healthy llm provider") || strings.Contains(raw, "no available llm") {
		return "当前没有可用的模型服务：模型池内所有模型均不可达或不可用。请到「智能体工作台 → 模型」核对各模型的 API Key / Base URL / 健康状态，或检查主 LLM 配置"
	}
	if strings.Contains(raw, "401") || strings.Contains(raw, "Auth") || strings.Contains(raw, "auth") {
		return "LLM API Key 无效或未配置，请在「设置 → 模型」中填写正确的 API Key"
	}
	if strings.Contains(raw, "429") || strings.Contains(raw, "访问量过大") || strings.Contains(raw, "使用量已超出") || strings.Contains(raw, "rate limit") {
		return "模型请求频率过高或当前繁忙，请稍候重试"
	}
	if strings.Contains(raw, "404") || strings.Contains(raw, "model not found") || strings.Contains(raw, "模型不存在") || strings.Contains(raw, "not exist") || strings.Contains(raw, "invalid model") {
		return "模型不存在或不可用：请在「智能体工作台 → 模型」核对模型 ID / Base URL，或改选 auto 自动路由"
	}
	if strings.Contains(raw, "余额") || strings.Contains(raw, "balance") || strings.Contains(raw, "insufficient") || strings.Contains(raw, "quota") {
		return "模型服务商账户异常或额度不足，请到对应平台检查余额 / 配额状态"
	}
	if strings.Contains(raw, "500") || strings.Contains(raw, "502") || strings.Contains(raw, "503") || strings.Contains(raw, "upstream") || strings.Contains(raw, "internal server") {
		return "模型服务商侧服务异常(HTTP 5xx)，请稍后重试或切换其他模型"
	}
	if strings.Contains(raw, "timeout") || strings.Contains(raw, "deadline") {
		return "LLM 响应超时，请检查网络或增加超时时间"
	}
	if strings.Contains(raw, "connection refused") {
		return "LLM 服务连接失败，请检查 Base URL 和网络"
	}
	return raw
}

// chatErrorStatus 将底层 LLM 错误映射为合适的 HTTP 状态码。
// 限流(429)返回 429 而非 502, 让前端按限流策略退避; 其余底层故障按 502 网关错误处理。
func chatErrorStatus(err error) int {
	if llm.IsRateLimited(err) || strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "访问量过大") || strings.Contains(err.Error(), "使用量已超出") {
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

// emptyCitationSectionRE 匹配回答尾部孤立的 "## 引用来源" 段(无实际引用标注时由模型生成)。
var emptyCitationSectionRE = regexp.MustCompile(`(?s)\n+[ \t]*## 引用来源.*$`)

// stripEmptyCitationSection 当回答未实际引用任何资料时, 删除模型可能附带的空"## 引用来源"占位段, 保持输出整洁。
func stripEmptyCitationSection(text string) string {
	if len(agentic.ExtractCitations(text)) > 0 {
		return text
	}
	return emptyCitationSectionRE.ReplaceAllString(text, "")
}
