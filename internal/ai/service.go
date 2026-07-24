package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/llm"
	redisclient "github.com/opsorigin/core/pkg/redis"
)

type Service struct {
	cfg      *config.Config
	engine   *agentic.Engine
	registry *mcp.Registry
	limiter  ratelimit.RateLimiter
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

	eng := agentic.NewEngine(reg, lim, llmClient, convStore)

	return &Service{
		cfg:      cfg,
		engine:   eng,
		registry: reg,
		limiter:  lim,
	}
}

// LLMConfigHook 在 sysconfig 修改 LLM 设置后被调用(热生效)。nil 表示未设置。
var LLMConfigHook func(cfg llm.Config)

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/ai/agents", s.handleCreateAgent)
	mux.HandleFunc("GET /api/v1/ai/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/v1/ai/agents/{id}", s.handleGetAgent)
	mux.HandleFunc("PUT /api/v1/ai/agents/{id}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/v1/ai/agents/{id}", s.handleDeleteAgent)
	mux.HandleFunc("GET /api/v1/ai/tools", s.handleListTools)
	mux.HandleFunc("POST /api/v1/ai/tools/{name}/execute", s.handleExecuteTool)
	mux.HandleFunc("POST /api/v1/ai/plans", s.handleExecutePlan)
	mux.HandleFunc("GET /api/v1/ai/plans", s.handleListPlans)
	mux.HandleFunc("GET /api/v1/ai/rate-limit/{agent_id}", s.handleGetRateLimit)
	mux.HandleFunc("GET /api/v1/ai/token-stats", s.handleTokenStats)
	mux.HandleFunc("POST /api/v1/ai/chat", s.handleChat)
	mux.HandleFunc("GET /api/v1/ai/conversations", s.handleListConversations)
	mux.HandleFunc("GET /api/v1/ai/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("POST /api/v1/ai/conversations", s.handleSaveConversation)

	// —— Feature 1: 多智能体团队 ——
	mux.HandleFunc("POST /api/v1/ai/teams", s.handleCreateTeam)
	mux.HandleFunc("GET /api/v1/ai/teams", s.handleListTeams)
	mux.HandleFunc("GET /api/v1/ai/teams/{id}", s.handleGetTeam)
	mux.HandleFunc("PUT /api/v1/ai/teams/{id}", s.handleUpdateTeam)
	mux.HandleFunc("DELETE /api/v1/ai/teams/{id}", s.handleDeleteTeam)
	mux.HandleFunc("POST /api/v1/ai/teams/{id}/chat", s.handleTeamChat)
}

func (s *Service) Start(ctx context.Context) error {
	return s.engine.Start()
}

// handleChat 处理 AI 助手聊天请求。
// req.stream=true 时返回 SSE(text/event-stream)流式输出; 否则返回 JSON。
func (s *Service) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt    string        `json:"prompt"`
		AgentID   string        `json:"agent_id"`
		History   []llm.Message `json:"history"`
		Stream    bool          `json:"stream"`
		Reasoning bool          `json:"reasoning"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// F4-D1: 会话计数移到入口, 与物理 LLM 槽位解耦 ——
	// 逻辑并发(如 100 路 SSE)全周期计入, 峰值=逻辑会话数而非物理槽位数。
	agentic.AIConcurrentSessions.Inc()
	defer agentic.AIConcurrentSessions.Dec()

	if req.Stream {
		s.handleChatStream(w, r, &req)
		return
	}

	if req.Reasoning && !req.Stream {
		thinking, answer, err := s.engine.ChatReasoning(r.Context(), req.AgentID, req.Prompt, req.History)
		if err != nil {
			writeError(w, http.StatusBadGateway, chatFriendlyError(err.Error()))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"content": answer, "thinking": thinking})
		return
	}

	out, err := s.engine.Chat(r.Context(), req.AgentID, req.Prompt, req.History)
	if err != nil {
		writeError(w, http.StatusBadGateway, chatFriendlyError(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"content": out})
}

// handleChatStream SSE 流式输出。
func (s *Service) handleChatStream(w http.ResponseWriter, r *http.Request, req *struct {
	Prompt    string        `json:"prompt"`
	AgentID   string        `json:"agent_id"`
	History   []llm.Message `json:"history"`
	Stream    bool          `json:"stream"`
	Reasoning bool          `json:"reasoning"`
}) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, err := s.engine.ChatStream(r.Context(), req.AgentID, req.Prompt, req.History)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	for token := range ch {
		// SSE data 字段中的换行需转义; 简单实现: 用 JSON 字符串包裹 token。
		b, _ := json.Marshal(token)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// UpdateLLMConfig 热更新 LLM 配置(sysconfig 修改 LLM 设置后调用)。
func (s *Service) UpdateLLMConfig(cfg llm.Config) {
	s.engine.UpdateClient(llm.NewClient(cfg))
}

// ——— 对话持久 ———

func (s *Service) handleSaveConversation(w http.ResponseWriter, r *http.Request) {
	var conv agentic.Conversation
	if err := json.NewDecoder(r.Body).Decode(&conv); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	conv.TenantID = tenantFromCtx(r.Context())
	if err := s.engine.SaveConversation(r.Context(), &conv); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (s *Service) handleListConversations(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	agentID := r.URL.Query().Get("agent_id")
	convs := s.engine.LoadConversations(tenantID, agentID, 50)
	writeJSON(w, http.StatusOK, convs)
}

func (s *Service) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conv, err := s.engine.GetConversation(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (s *Service) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		ModelName    string `json:"model_name"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	agent := &model.AgentInstance{
		Name:         req.Name,
		ModelName:    req.ModelName,
		SystemPrompt: req.SystemPrompt,
		TenantID:     tenantFromCtx(r.Context()),
	}

	if err := s.engine.CreateAgent(r.Context(), agent); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

func (s *Service) handleListAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	all := s.engine.ListAgents()
	filtered := make([]*model.AgentInstance, 0)
	for _, a := range all {
		if a.TenantID == tenantID {
			filtered = append(filtered, a)
		}
	}
	if filtered == nil {
		filtered = []*model.AgentInstance{}
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (s *Service) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	agent, ok := s.engine.GetAgent(id)
	if !ok || agent.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Service) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := s.registry.List()
	if tools == nil {
		tools = []mcp.ToolDefinition{}
	}
	writeJSON(w, http.StatusOK, tools)
}

func (s *Service) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := s.registry.Execute(r.Context(), name, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Service) handleExecutePlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID   string       `json:"agent_id"`
		RiskLevel string       `json:"risk_level"`
		Steps     []model.Step `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

	if err := s.engine.ExecutePlan(r.Context(), plan); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

func (s *Service) handleListPlans(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	all := s.engine.ListPlans()
	filtered := make([]*model.ExecutionPlan, 0)
	for _, p := range all {
		if p.TenantID == tenantID {
			filtered = append(filtered, p)
		}
	}
	if filtered == nil {
		filtered = []*model.ExecutionPlan{}
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (s *Service) handleGetRateLimit(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id":  agentID,
		"state":     s.limiter.GetState(agentID),
		"call_count": s.limiter.GetCount(agentID),
		"limit":     ratelimit.DefaultLimit,
		"window_s":  ratelimit.DefaultWindow.Seconds(),
	})
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Service) handleTokenStats(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" { period = "24h" }
	stats := s.engine.GetTokenStats(period)
	writeJSON(w, http.StatusOK, stats)
}

func (s *Service) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var ag model.AgentInstance
	if err := json.NewDecoder(r.Body).Decode(&ag); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ag.ID = id
	ag.TenantID = tenantFromCtx(r.Context())
	if err := s.engine.UpdateAgent(r.Context(), &ag); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ag)
}

func (s *Service) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantFromCtx(r.Context())
	if err := s.engine.DeleteAgent(r.Context(), id, tenant); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— Feature 1: 多智能体团队 ———

func (s *Service) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var team model.AgentTeam
	if err := json.NewDecoder(r.Body).Decode(&team); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team.TenantID = tenantFromCtx(r.Context())
	if err := s.engine.CreateTeam(r.Context(), &team); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (s *Service) handleListTeams(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	teams := s.engine.ListTeams(tenantID)
	if teams == nil {
		teams = []*model.AgentTeam{}
	}
	writeJSON(w, http.StatusOK, teams)
}

func (s *Service) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	team, ok := s.engine.GetTeam(id)
	if !ok {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	if team.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (s *Service) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var team model.AgentTeam
	if err := json.NewDecoder(r.Body).Decode(&team); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team.ID = id
	team.TenantID = tenantFromCtx(r.Context())
	if err := s.engine.UpdateTeam(r.Context(), &team); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (s *Service) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantFromCtx(r.Context())
	if err := s.engine.DeleteTeam(r.Context(), id, tenant); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleTeamChat 团队对话(SSE 流式)：逐成员发射 agent_start/token/agent_end,
// 最后发射 final。任成员失败发射 error 事件并继续, 全部失败才返回错误。
func (s *Service) handleTeamChat(w http.ResponseWriter, r *http.Request) {
	var req model.TeamChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TeamID == "" {
		req.TeamID = r.PathValue("id")
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	team, ok := s.engine.GetTeam(req.TeamID)
	if !ok {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	if team.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}

	// F4-D1: 逻辑会话计数在入口, 与物理 LLM 槽位解耦
	agentic.AIConcurrentSessions.Inc()
	defer agentic.AIConcurrentSessions.Dec()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
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

	final, _, err := s.engine.RunTeamChat(r.Context(), team, req.Message, nil, emit)
	if err != nil {
		emit(model.TeamStreamEvent{Type: "error", Content: chatFriendlyError(err.Error())})
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
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
