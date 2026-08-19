package ai

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
)

// handleListMemories 返回该租户的 Agent 长期记忆(按 agent 过滤可选, 创建时间倒序)。
func (s *Service) handleListMemories(w http.ResponseWriter, r *http.Request) {
	filter := agentic.MemoryFilter{}
	if a := r.URL.Query().Get("agent_id"); a != "" {
		filter.AgentID = a
	}
	if c := r.URL.Query().Get("category"); c != "" {
		filter.Category = c
	}
	writeJSON(w, http.StatusOK, s.Engine.ListMemories(tenantFromCtx(r.Context()), filter))
}

// handleAddMemory 手动记录一条 Agent 长期记忆(P0-1)。
// body: {"agent_id":"...","category":"数据库","problem":"...","resolution":"...","outcome":"succeeded"}。
// 需 ai:tool:exec 权限。
func (s *Service) handleAddMemory(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req agentic.ExperienceMemory
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	req.TenantID = tenantFromCtx(r.Context())
	// 手动记录默认来源: 对话要点(与反馈回流/工单沉淀区分)
	if req.Source == "" {
		req.Source = agentic.MemorySourceConversation
	}
	m, err := s.Engine.AddMemory(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// handleDeleteMemory 删除一条记忆。
func (s *Service) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if !s.Engine.DeleteMemory(tenantFromCtx(r.Context()), r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
