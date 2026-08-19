package ai

import (
	"github.com/opsorigin/core/pkg/httputil"
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
	httputil.WriteJSON(w, http.StatusOK, items)
}

// handleGetAgentTemplate 返回单个岗位模板详情(含完整 system prompt, 供前端预览)。
func (s *Service) handleGetAgentTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Engine.GetAgentTemplate(r.PathValue("id"))
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "agent template not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

// handleInstantiateAgentTemplate 将岗位模板一键实例化为租户内可用的 Agent。
// 创建 Agent 属于配置变更, 复用 AI 工具执行权限做闸门。
// body 可选: {"name":"...", "with_inspection": true|false}。
// with_inspection 默认开启(模板声明了关联巡检时自动创建巡检任务); 显式传 false 关闭。
func (s *Service) handleInstantiateAgentTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Name           string `json:"name"`
		WithInspection *bool  `json:"with_inspection"`
	}
	// body 可选: 不传则使用模板默认名称。
	_ = httputil.BindJSON(r, &req)

	withInspection := true
	if req.WithInspection != nil {
		withInspection = *req.WithInspection
	}

	agent, insp, err := s.Engine.InstantiateAgentTemplate(auth.TenantFromContext(r.Context()), r.PathValue("id"), req.Name, withInspection)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{
		"agent":      agent,
		"inspection": insp, // 自动创建的关联巡检任务(可能为 null)
	})
}
