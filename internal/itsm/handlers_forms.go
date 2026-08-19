package itsm

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

// handleFulfillCatalog 从服务目录发起申请: 套用目录项配置的流程与表单作用域创建工单。
func (s *Service) handleFulfillCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.CreateTicketRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		httputil.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.CreatedBy == "" {
		req.CreatedBy = "portal"
	}
	tk, err := s.CreateTicketFromCatalog(r.Context(), id, tenantID, req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "disabled") {
			httputil.WriteError(w, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, tk)
}

// handleListFormScopes 列出已配置的作用域；若带 scope 查询参数则返回该作用域的 schema（无则 schema 为 null）。
func (s *Service) handleListFormScopes(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	scope := r.URL.Query().Get("scope")
	if scope != "" {
		schema, err := s.store.GetFormSchema(r.Context(), tenantID, scope)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.expandSchemaDicts(r.Context(), tenantID, schema)
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"scope": scope, "schema": schema})
		return
	}
	scopes, err := s.store.ListFormScopes(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if scopes == nil {
		scopes = []string{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"scopes": scopes})
}

// handleUpsertFormSchema 按 scope 写入/更新一张表单 schema（管理端可配）。
func (s *Service) handleUpsertFormSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope  string          `json:"scope"`
		Schema form.FormSchema `json:"schema"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Scope == "" {
		req.Scope = req.Schema.Scope
	}
	if req.Scope == "" {
		httputil.WriteError(w, http.StatusBadRequest, "scope is required")
		return
	}
	req.Schema.Scope = req.Scope
	if req.Schema.Version < 1 {
		req.Schema.Version = 1
	}
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.UpsertFormSchema(r.Context(), tenantID, req.Scope, req.Schema); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "scope": req.Scope})
}

// expandSchemaDicts 展开 schema 中的 dict_ref 引用: 从数据字典拉取字段定义替换引用字段。
func (s *Service) expandSchemaDicts(ctx context.Context, tenantID string, schema *form.FormSchema) {
	if schema == nil {
		return
	}
	needExpand := false
	for _, f := range schema.Fields {
		if f.DictRef != "" {
			needExpand = true
			break
		}
	}
	if !needExpand {
		return
	}
	dicts := map[string]*form.FieldDict{}
	if list, err := s.store.ListFieldDicts(ctx, tenantID); err == nil {
		for _, d := range list {
			dicts[d.ID] = d
		}
	}
	form.ExpandDictRefs(schema, dicts)
}

func (s *Service) handleListFieldDicts(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	dicts, err := s.store.ListFieldDicts(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if dicts == nil {
		dicts = []*form.FieldDict{}
	}
	httputil.WriteJSON(w, http.StatusOK, dicts)
}

func (s *Service) handleUpsertFieldDict(w http.ResponseWriter, r *http.Request) {
	var dict form.FieldDict
	if err := httputil.BindJSON(r, &dict); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if dict.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.UpsertFieldDict(r.Context(), tenantID, &dict); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dict)
}

func (s *Service) handleDeleteFieldDict(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeleteFieldDict(r.Context(), tenantID, id); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "dict not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleFormOptions 解析表单字段的动态选项来源（cmdb:<type> / iam:user|role|group / cmdb:types）。
// 不带 source 参数时返回本租户可用的 source 清单，供表单配置界面下拉选择。
// keyword 用于前端下拉搜索时做服务端过滤，limit 限制返回条数（默认 200）。
// 带 parent 参数（可为空串）时进入层级模式：返回该父节点下的子选项，供 cascader 级联逐级展开。
func (s *Service) handleFormOptions(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.formEngine == nil || s.formEngine.SourceResolver() == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"options": []form.FieldOption{}, "sources": []string{}})
		return
	}
	resolver := s.formEngine.SourceResolver()
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" {
		sources := []string{}
		if c, ok := resolver.(sourceCataloger); ok {
			sources = c.SourceCatalog(r.Context(), tenantID)
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"sources": sources})
		return
	}
	// 层级模式：parent 参数存在即按树取子节点（parent 为空取根层级）
	if r.URL.Query().Has("parent") {
		tr, ok := resolver.(form.TreeSourceResolver)
		if !ok {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{"source": source, "options": []form.FieldOption{}})
			return
		}
		parent := strings.TrimSpace(r.URL.Query().Get("parent"))
		children, err := tr.ResolveTree(r.Context(), tenantID, source, parent)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, fmt.Sprintf("解析选项来源失败: %v", err))
			return
		}
		if children == nil {
			children = []form.FieldOption{}
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"source": source, "parent": parent, "options": children})
		return
	}
	opts, err := resolver.Resolve(r.Context(), tenantID, source)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, fmt.Sprintf("解析选项来源失败: %v", err))
		return
	}
	if kw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword"))); kw != "" {
		filtered := make([]form.FieldOption, 0, len(opts))
		for _, o := range opts {
			if strings.Contains(strings.ToLower(o.Label), kw) || strings.Contains(strings.ToLower(o.Value), kw) {
				filtered = append(filtered, o)
			}
		}
		opts = filtered
	}
	limit := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	if len(opts) > limit {
		opts = opts[:limit]
	}
	if opts == nil {
		opts = []form.FieldOption{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"source": source, "options": opts})
}

// handleUpdateCustomFields 按作用域更新工单自定义字段（流程步骤级表单回写）。
// 若给定 scope 存在 schema，则先注入默认值并校验，再合并进 ticket.custom_fields；无 schema 直接合并（向后兼容）。
func (s *Service) handleUpdateCustomFields(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		Scope  string         `json:"scope"`
		Fields map[string]any `json:"fields"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if ticket.CustomFields == nil {
		ticket.CustomFields = map[string]any{}
	}
	if req.Fields == nil {
		req.Fields = map[string]any{}
	}
	if req.Scope != "" && s.formEngine != nil {
		if schema, serr := s.formEngine.ResolveSchema(r.Context(), tenantID, req.Scope); serr == nil && schema != nil {
			form.MergeDefaults(schema, req.Fields)
			if verr := s.formEngine.ValidateValues(r.Context(), tenantID, schema, req.Fields); verr != nil {
				httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("表单校验失败: %v", verr))
				return
			}
		}
	}
	for k, v := range req.Fields {
		ticket.CustomFields[k] = v
	}
	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 步骤表单保存后推进流程: 若当前步骤下游是网关(表达式条件), 基于刚填写的表单字段选路;
	// 下游若是 service 步骤则自动执行脚本。无流程绑定时静默跳过。
	if ticket.ProcessID != "" && s.processStore != nil {
		s.advanceProcess(r.Context(), ticket, "success")
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "custom_fields": ticket.CustomFields})
}
