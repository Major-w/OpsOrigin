package ai

import (
	"encoding/json"
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
	writeJSON(w, http.StatusOK, items)
}

// handleGetSceneTemplate 返回单个场景包详情(含 recommended catalog 元数据)。
func (s *Service) handleGetSceneTemplate(w http.ResponseWriter, r *http.Request) {
	scene, ok := s.Engine.GetSceneTemplate(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scene template not found")
		return
	}
	writeJSON(w, http.StatusOK, scene)
}

// handleInstantiateScene 一键启用场景包(创建岗位 Agent + 关联巡检 + 场景额外巡检)。
// body 可选: {"name": "..."} 覆盖默认名(同名自动追加序号)。
// 需 ai:tool:exec 权限(场景启用属于配置变更)。
func (s *Service) handleInstantiateScene(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	result, err := s.Engine.InstantiateScene(tenantFromCtx(r.Context()), r.PathValue("id"), req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}