package ai

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
)

// handleListAgentTemplates 返回平台预置的岗位数字员工目录。
// 只读目录, 不含租户数据, 无需额外权限。
func (s *Service) handleListAgentTemplates(w http.ResponseWriter, r *http.Request) {
	items := s.Engine.ListAgentTemplates()
	if items == nil {
		items = []*agentic.AgentTemplate{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleGetAgentTemplate 返回单个岗位模板详情(含完整 system prompt, 供前端预览)。
func (s *Service) handleGetAgentTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Engine.GetAgentTemplate(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent template not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleInstantiateAgentTemplate 将岗位模板一键实例化为租户内可用的 Agent。
// 创建 Agent 属于配置变更, 复用 AI 工具执行权限做闸门。
func (s *Service) handleInstantiateAgentTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	// body 可选: 不传则使用模板默认名称。
	_ = json.NewDecoder(r.Body).Decode(&req)

	agent, err := s.Engine.InstantiateAgentTemplate(tenantFromCtx(r.Context()), r.PathValue("id"), req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}
