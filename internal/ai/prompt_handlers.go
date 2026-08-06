package ai

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
)

// ============================================================================
// 提示词库 REST 端点
//
// 读接口对所有已认证用户开放(提示词是团队共享资产, 一线值班需要随时取用);
// 写接口需 ai:tool:exec 权限(改动默认提示词会影响全租户 Agent 行为)。
// ============================================================================

// handleListPrompts 列出租户提示词库, 可按 ?scene= 过滤。
func (s *Service) handleListPrompts(w http.ResponseWriter, r *http.Request) {
	prompts := s.Engine.ListPrompts(r.Context(), tenantFromCtx(r.Context()), r.URL.Query().Get("scene"))
	if prompts == nil {
		prompts = []*agentic.PromptTemplate{}
	}
	writeJSON(w, http.StatusOK, prompts)
}

// handleGetPrompt 返回单条提示词详情(含解析出的占位变量)。
func (s *Service) handleGetPrompt(w http.ResponseWriter, r *http.Request) {
	p, err := s.Engine.GetPrompt(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// promptRequest 是新建/更新提示词的入参。
// Builtin/Usage/Variables 均为服务端派生, 前端传了也会被忽略。
type promptRequest struct {
	Name        string `json:"name"`
	Scene       string `json:"scene"`
	Description string `json:"description"`
	Content     string `json:"content"`
	IsDefault   bool   `json:"is_default"`
}

// handleCreatePrompt 新建一条提示词。需 ai:tool:exec 权限。
func (s *Service) handleCreatePrompt(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req promptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := s.Engine.SavePrompt(r.Context(), tenantFromCtx(r.Context()), &agentic.PromptTemplate{
		Name: req.Name, Scene: req.Scene, Description: req.Description,
		Content: req.Content, IsDefault: req.IsDefault,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// handleUpdatePrompt 更新一条提示词(含把它设为默认系统提示词, 保存即热生效)。
func (s *Service) handleUpdatePrompt(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req promptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := s.Engine.SavePrompt(r.Context(), tenantFromCtx(r.Context()), &agentic.PromptTemplate{
		ID: r.PathValue("id"), Name: req.Name, Scene: req.Scene,
		Description: req.Description, Content: req.Content, IsDefault: req.IsDefault,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleDeletePrompt 删除一条提示词。预置与默认提示词受保护(引擎层拒绝)。
func (s *Service) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	if err := s.Engine.DeletePrompt(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleRenderPrompt 用变量渲染提示词并累计引用次数, 返回可直接粘进 Agent 的成品文本。
// body: {"variables": {"现象": "接口 5xx 突增"}}
func (s *Service) handleRenderPrompt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Variables map[string]string `json:"variables"`
	}
	// 允许空 body: 不带变量时等价于取原文并计一次引用
	_ = json.NewDecoder(r.Body).Decode(&req)
	text, err := s.Engine.RenderPrompt(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"), req.Variables)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": text})
}
