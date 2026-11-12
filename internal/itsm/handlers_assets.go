package itsm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/opsorigin/core/internal/itsm/assets"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// 决策表资产 / 动作模板的 HTTP 端点。
//
// 权限点复用 itsm:read / itsm:write，不新增权限常量；所有路径在 pkg/auth/rbac.go
// PathPermissionMapping 显式登记（避免漏映射导致只认证不鉴权的 fail-open）。

func (s *Service) assetStoreOrFail(w http.ResponseWriter) assets.Store {
	if s.assetStore == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "asset store not configured")
		return nil
	}
	return s.assetStore
}

func writeAssetErr(w http.ResponseWriter, err error) {
	if errors.Is(err, assets.ErrNotFound) {
		httputil.WriteError(w, http.StatusNotFound, "资产不存在")
		return
	}
	httputil.WriteError(w, http.StatusInternalServerError, err.Error())
}

// assetUsage 扫描「独立表单 schema + 流程级主表单 + 流程级决策绑定」，统计每个
// 决策表资产的引用位置。与 form-dicts 的 used_by 同范式（配置数量级小，逐个查可接受）。
//
// 流程侧三处引用必须一并扫描：漏扫时资产可被删除，而引用解析对缺失资产是容错设计
// （退回空表 / 空绑定），定级/派单结果会静默改变 —— 正是 409 要防的场景。
func (s *Service) assetUsage(r *http.Request, tenantID string) map[string][]string {
	usage := map[string][]string{}
	seen := map[string]bool{}
	add := func(assetID, where string) {
		key := assetID + "\x00" + where
		if seen[key] {
			return
		}
		seen[key] = true
		usage[assetID] = append(usage[assetID], where)
	}
	if scopes, err := s.store.ListFormScopes(r.Context(), tenantID); err == nil {
		for _, scope := range scopes {
			schema, err := s.store.GetFormSchema(r.Context(), tenantID, scope)
			if err != nil || schema == nil {
				continue
			}
			for _, dt := range schema.DecisionTables {
				if dt.AssetRef != nil && dt.AssetRef.AssetID != "" {
					add(dt.AssetRef.AssetID, scope)
				}
			}
		}
	}
	fromProcess, _ := s.processAssetUsage(r, tenantID)
	for id, list := range fromProcess {
		for _, where := range list {
			add(id, where)
		}
	}
	return usage
}

// templateUsage 动作模板的反向引用（流程 service 节点的 template_ref）。
func (s *Service) templateUsage(r *http.Request, tenantID string) map[string][]string {
	_, usage := s.processAssetUsage(r, tenantID)
	return usage
}

// processAssetUsage 一次扫描全部流程定义，同时收集决策表资产与动作模板的引用位置。
//
// process.Service 有 List，但 ProcessEngine 最小接口不含它 —— 鸭子类型可选能力，
// 与 stepVoter 同范式：测试里的 fake engine 无 List 时自动跳过，不影响既有行为。
func (s *Service) processAssetUsage(r *http.Request, tenantID string) (map[string][]string, map[string][]string) {
	decisions := map[string][]string{}
	templates := map[string][]string{}
	seenD, seenT := map[string]bool{}, map[string]bool{}
	addD := func(assetID, where string) {
		key := assetID + "\x00" + where
		if seenD[key] {
			return
		}
		seenD[key] = true
		decisions[assetID] = append(decisions[assetID], where)
	}
	addT := func(templateID, where string) {
		key := templateID + "\x00" + where
		if seenT[key] {
			return
		}
		seenT[key] = true
		templates[templateID] = append(templates[templateID], where)
	}
	type processLister interface {
		List(ctx context.Context, tenantID string) ([]*process.Process, error)
	}
	lister, ok := s.processStore.(processLister)
	if !ok {
		return decisions, templates
	}
	procs, err := lister.List(r.Context(), tenantID)
	if err != nil {
		return decisions, templates
	}
	for _, p := range procs {
		if p == nil {
			continue
		}
		if len(p.Form) > 0 {
			var fs form.FormSchema
			if json.Unmarshal(p.Form, &fs) == nil {
				for _, dt := range fs.DecisionTables {
					if dt.AssetRef != nil && dt.AssetRef.AssetID != "" {
						addD(dt.AssetRef.AssetID, "流程「"+p.Name+"」主表单")
					}
				}
			}
		}
		for _, b := range p.DecisionBindings {
			if b.AssetID != "" {
				addD(b.AssetID, "流程「"+p.Name+"」决策绑定")
			}
		}
		for _, st := range p.Steps {
			if st.TemplateRef != nil && st.TemplateRef.TemplateID != "" {
				addT(st.TemplateRef.TemplateID, "流程「"+p.Name+"」步骤「"+st.Name+"」")
			}
		}
	}
	return decisions, templates
}

// GET /api/v1/itsm/decision-tables
func (s *Service) handleListDecisionTables(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	list, err := as.ListDecisionTables(r.Context(), tenantID)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	if list == nil {
		list = []*assets.DecisionTableAsset{}
	}
	usage := s.assetUsage(r, tenantID)
	for _, a := range list {
		a.UsedBy = usage[a.ID]
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

// GET /api/v1/itsm/decision-tables/{id}
func (s *Service) handleGetDecisionTable(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	a, err := as.GetDecisionTable(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	a.UsedBy = s.assetUsage(r, tenantID)[a.ID]
	httputil.WriteJSON(w, http.StatusOK, a)
}

// POST /api/v1/itsm/decision-tables —— 新建或保存草稿（不改发布状态/版本）。
func (s *Service) handleUpsertDecisionTable(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var a assets.DecisionTableAsset
	if err := httputil.BindJSON(r, &a); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	// 资产内容不接受外部引用标记，避免自引用环。
	a.Table.AssetRef = nil
	warns, verr := assets.ValidateTable(a.Table)
	if verr != nil {
		httputil.WriteError(w, http.StatusBadRequest, verr.Error())
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if err := as.UpsertDecisionTable(r.Context(), tenantID, &a); err != nil {
		writeAssetErr(w, err)
		return
	}
	// 回读真实状态（Status/Version 由 store 管理，客户端传值一律忽略）。
	saved, err := as.GetDecisionTable(r.Context(), tenantID, a.ID)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	resp := map[string]any{"asset": saved}
	if len(warns) > 0 {
		resp["warnings"] = warns
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// DELETE /api/v1/itsm/decision-tables/{id}
// 被表单引用的资产禁止删除（引用失效会静默改变派单结果，fail-closed）。
func (s *Service) handleDeleteDecisionTable(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	if scopes := s.assetUsage(r, tenantID)[id]; len(scopes) > 0 {
		httputil.WriteError(w, http.StatusConflict,
			fmt.Sprintf("该决策表资产正被 %d 处配置引用（%s），请先移除引用再删除", len(scopes), strings.Join(scopes, "、")))
		return
	}
	if err := as.DeleteDecisionTable(r.Context(), tenantID, id); err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GET /api/v1/itsm/decision-tables/{id}/versions
func (s *Service) handleListDecisionVersions(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	list, err := as.ListVersions(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	if list == nil {
		list = []*assets.DecisionTableVersion{}
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

// POST /api/v1/itsm/decision-tables/{id}/publish
// body: { "note": "发布说明" }；以资产当前草稿内容发布一个新版本。
func (s *Service) handlePublishDecisionTable(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	a, err := as.GetDecisionTable(r.Context(), tenantID, id)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	if _, verr := assets.ValidateTable(a.Table); verr != nil {
		httputil.WriteError(w, http.StatusBadRequest, verr.Error())
		return
	}
	v, err := as.Publish(r.Context(), tenantID, id, a.Table, req.Note, getUserID(r, ""))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, v)
}

// POST /api/v1/itsm/decision-tables/{id}/rollback
// body: { "version": 2, "note": "..." }；回滚 = 以历史快照再发布一个新版本，
// 历史版本永不改写，跟随最新版的全部表单随之回退。
func (s *Service) handleRollbackDecisionTable(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var req struct {
		Version int    `json:"version"`
		Note    string `json:"note"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Version < 1 {
		httputil.WriteError(w, http.StatusBadRequest, "version 必须为正整数")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	if _, err := as.GetDecisionTable(r.Context(), tenantID, id); err != nil {
		writeAssetErr(w, err)
		return
	}
	v, err := as.GetVersion(r.Context(), tenantID, id, req.Version)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = fmt.Sprintf("回滚至版本 v%d", req.Version)
	}
	newV, err := as.Publish(r.Context(), tenantID, id, v.Table, note, getUserID(r, ""))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, newV)
}

// POST /api/v1/itsm/decision-tables/{id}/preview
// 独立试算：不依赖任何表单，用资产（草稿或指定版本）+ 样例值跑一次决策表推导，
// 返回命中行号与输出列。与提交时同一条 ApplyDataRulesFull 通道。
func (s *Service) handlePreviewDecisionTable(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var req struct {
		Version int                 `json:"version"`         // 0 = 当前草稿内容
		Table   *form.DecisionTable `json:"table,omitempty"` // 传了则优先（编辑态未保存草稿）
		Values  map[string]any      `json:"values"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	var table form.DecisionTable
	switch {
	case req.Table != nil:
		table = *req.Table
	case req.Version > 0:
		v, err := as.GetVersion(r.Context(), tenantID, id, req.Version)
		if err != nil {
			writeAssetErr(w, err)
			return
		}
		table = v.Table
	default:
		a, err := as.GetDecisionTable(r.Context(), tenantID, id)
		if err != nil {
			writeAssetErr(w, err)
			return
		}
		table = a.Table
	}
	table.AssetRef = nil
	warns, verr := assets.ValidateTable(table)
	if verr != nil {
		// 草稿允许未配完：配置错误作为提示返回，不阻断试算。
		warns = append([]string{verr.Error()}, warns...)
	}
	// 与流程级决策绑定共用同一套合成 schema，保证独立试算与运行期推导结果一致。
	schema := assets.SyntheticSchema(table)
	values := req.Values
	if values == nil {
		values = map[string]any{}
	}
	var computed map[string]any
	var fired []string
	if s.formEngine != nil {
		computed, fired = s.formEngine.PreviewRules(r.Context(), tenantID, schema, values)
	} else {
		computed, fired = form.ApplyDataRules(schema, values)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"values":   computed,
		"fired":    fired,
		"warnings": warns,
	})
}

// --- 动作模板 ---

// GET /api/v1/itsm/action-templates
func (s *Service) handleListActionTemplates(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	list, err := as.ListActionTemplates(r.Context(), tenantID)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	if list == nil {
		list = []*assets.ActionTemplate{}
	}
	usage := s.templateUsage(r, tenantID)
	for _, t := range list {
		t.UsedBy = usage[t.ID]
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

// POST /api/v1/itsm/action-templates —— 新建 / 编辑 / 启停。
func (s *Service) handleUpsertActionTemplate(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var t assets.ActionTemplate
	if err := httputil.BindJSON(r, &t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	t.ActionType = strings.TrimSpace(t.ActionType)
	if !assets.IsBuiltinActionType(t.ActionType) {
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("action_type 必须是内置动作之一: %s", strings.Join(assets.BuiltinActionTypes, " / ")))
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if err := as.UpsertActionTemplate(r.Context(), tenantID, &t); err != nil {
		writeAssetErr(w, err)
		return
	}
	saved, err := as.GetActionTemplate(r.Context(), tenantID, t.ID)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, saved)
}

// DELETE /api/v1/itsm/action-templates/{id}
// 被流程步骤引用的模板禁止删除（运行期解析 fail-closed 会让在途工单直接失败）。
func (s *Service) handleDeleteActionTemplate(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	if scopes := s.templateUsage(r, tenantID)[id]; len(scopes) > 0 {
		httputil.WriteError(w, http.StatusConflict,
			fmt.Sprintf("该动作模板正被 %d 处流程步骤引用（%s），请先移除引用再删除", len(scopes), strings.Join(scopes, "、")))
		return
	}
	if err := as.DeleteActionTemplate(r.Context(), tenantID, id); err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GET /api/v1/itsm/action-templates/{id}/versions
func (s *Service) handleListActionTemplateVersions(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	list, err := as.ListActionTemplateVersions(r.Context(), auth.TenantFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	if list == nil {
		list = []*assets.ActionTemplateVersion{}
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

// POST /api/v1/itsm/action-templates/{id}/publish
// 以模板当前草稿内容追加不可变版本；发布后 version=0 的流程引用立即切到该版本。
func (s *Service) handlePublishActionTemplate(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	v, err := as.PublishActionTemplate(r.Context(), tenantID, r.PathValue("id"),
		strings.TrimSpace(req.Note), getUserID(r, ""))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, v)
}

// POST /api/v1/itsm/action-templates/{id}/rollback
// 回滚 = 把历史快照写回草稿再发布一个新版本，历史版本永不改写。
func (s *Service) handleRollbackActionTemplate(w http.ResponseWriter, r *http.Request) {
	as := s.assetStoreOrFail(w)
	if as == nil {
		return
	}
	var req struct {
		Version int    `json:"version"`
		Note    string `json:"note"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Version < 1 {
		httputil.WriteError(w, http.StatusBadRequest, "version 必须为正整数")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	v, err := as.GetActionTemplateVersion(r.Context(), tenantID, id, req.Version)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	t, err := as.GetActionTemplate(r.Context(), tenantID, id)
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	t.ActionType, t.Inputs = v.ActionType, v.Inputs
	if err := as.UpsertActionTemplate(r.Context(), tenantID, t); err != nil {
		writeAssetErr(w, err)
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = fmt.Sprintf("回滚至版本 v%d", req.Version)
	}
	newV, err := as.PublishActionTemplate(r.Context(), tenantID, id, note, getUserID(r, ""))
	if err != nil {
		writeAssetErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, newV)
}
