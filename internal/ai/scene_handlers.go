package ai

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
)

// handleListSceneTemplates 返回平台预置的场景包目录(只读, 无需额外权限)。
func (s *Service) handleListSceneTemplates(w http.ResponseWriter, r *http.Request) {
	items := s.Engine.ListSceneTemplates()
	if items == nil {
		items = []*agentic.SceneTemplate{}
	}
	httputil.WriteJSON(w, http.StatusOK, items)
}

// handleGetSceneTemplate 返回单个场景包详情(含 recommended catalog 元数据)。
func (s *Service) handleGetSceneTemplate(w http.ResponseWriter, r *http.Request) {
	scene, ok := s.Engine.GetSceneTemplate(r.PathValue("id"))
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "scene template not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, scene)
}

// handleInstantiateScene 一键启用场景包(创建岗位 Agent + 关联巡检 + 场景额外巡检)。
// body 可选: {"name": "...", "auto_create_tickets": true}
//   - name 覆盖默认名(同名自动追加序号)
//   - auto_create_tickets=true 时将场景推荐工单类型真实落库到 itsm_catalog(幂等同名跳过)
//
// 需 ai:tool:exec 权限(场景启用属于配置变更)。
func (s *Service) handleInstantiateScene(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Name              string `json:"name"`
		AutoCreateTickets bool   `json:"auto_create_tickets"`
	}
	_ = httputil.BindJSON(r, &req)

	result, err := s.Engine.InstantiateScene(auth.TenantFromContext(r.Context()), r.PathValue("id"), req.Name, req.AutoCreateTickets)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, result)
}
