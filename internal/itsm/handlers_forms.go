package itsm

import (
	"context"
	"encoding/json"
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
		// MemoryStore 返回内部指针，dict_ref / asset_ref 的展开都是就地改写；
		// 不先深拷贝的话，GET 一次就会把展开快照写回存储，保存态不再是纯引用，
		// 资产发布新版本后跟随版表单会永久停在旧快照（PGStore 每次新建对象，故只在内存态暴露）。
		if schema != nil {
			if raw, mErr := json.Marshal(schema); mErr == nil {
				var cloned form.FormSchema
				if uErr := json.Unmarshal(raw, &cloned); uErr == nil {
					schema = &cloned
				}
			}
		}
		s.expandSchemaDicts(r.Context(), tenantID, schema)
		s.expandSchemaDecisionRefs(r.Context(), tenantID, schema)
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
	// 决策表配置校验：拦截「列未声明 / 空列 / 无行」等运行期静默失效的错配。
	// 资产引用表在展开态校验（用已发布版本的真实列），确保资产的列也在本表单字段中声明；
	// 校验后立刻归一回纯引用，避免把版本快照固化进 schema。
	s.expandSchemaDecisionRefs(r.Context(), tenantID, &req.Schema)
	warns, verr := form.ValidateDecisionTables(&req.Schema)
	if verr != nil {
		httputil.WriteError(w, http.StatusBadRequest, verr.Error())
		return
	}
	form.NormalizeDecisionRefs(&req.Schema)
	if err := s.store.UpsertFormSchema(r.Context(), tenantID, req.Scope, req.Schema); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"status": "ok", "scope": req.Scope}
	if len(warns) > 0 {
		resp["warnings"] = warns
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
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

// expandSchemaDecisionRefs 把 schema 中的「决策表资产引用」展开成已发布版本内容，
// 但保留 AssetRef 标记（引用式展开，与 dict_ref 同范式）。
//
// GET schema 时调用：前端运行期 applyDataRules 直接拿到完整行矩阵，实时推导隐藏字段，
// 与后端提交时的求值结果一致；设计器保存后由 NormalizeDecisionRefs 归一回纯引用，
// 不会把版本快照固化进表单，资产发布新版本后所有跟随版表单立即生效。
func (s *Service) expandSchemaDecisionRefs(ctx context.Context, tenantID string, schema *form.FormSchema) {
	if schema == nil || !form.HasDecisionRefs(schema) {
		return
	}
	if s.formEngine == nil || s.formEngine.DecisionTableResolver() == nil {
		return
	}
	expanded, err := form.ExpandDecisionRefs(ctx, tenantID, schema, s.formEngine.DecisionTableResolver())
	if err != nil || expanded == nil || expanded == schema {
		return
	}
	*schema = *expanded
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
	// used_by：扫描全部表单 schema，统计每个字典被哪些 scope 引用。
	// 让用户在删除/改动字典前能看到影响面（N 为表单 scope 数，量级小，逐个查可接受）。
	usage := map[string][]string{}
	if scopes, err := s.store.ListFormScopes(r.Context(), tenantID); err == nil {
		for _, scope := range scopes {
			schema, err := s.store.GetFormSchema(r.Context(), tenantID, scope)
			if err != nil || schema == nil {
				continue
			}
			for _, f := range schema.Fields {
				if f.DictRef != "" {
					usage[f.DictRef] = append(usage[f.DictRef], scope)
				}
			}
		}
	}
	out := make([]dictWithUsage, 0, len(dicts))
	for _, d := range dicts {
		out = append(out, dictWithUsage{FieldDict: d, UsedBy: usage[d.ID]})
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// dictWithUsage 字典 + 引用它的表单 scope 列表（GET /itsm/form-dicts 响应）。
type dictWithUsage struct {
	*form.FieldDict
	UsedBy []string `json:"used_by,omitempty"`
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
	// 字段白名单校验：key 必填且不重复；选择类字段必须带选项。
	seenKeys := map[string]bool{}
	for i, f := range dict.Fields {
		if f.Key == "" {
			httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("fields[%d]: key is required", i))
			return
		}
		if seenKeys[f.Key] {
			httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("fields[%d]: duplicate key %q", i, f.Key))
			return
		}
		seenKeys[f.Key] = true
		switch f.Type {
		case form.TypeSelect, form.TypeMultiSelect, form.TypeCascader:
			if len(f.Options) == 0 {
				httputil.WriteError(w, http.StatusBadRequest,
					fmt.Sprintf("fields[%d] (%s): type %s requires options", i, f.Key, f.Type))
				return
			}
		}
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
// 参数化级联: source 串支持 ${field_key} 占位符（如 cmdb:机柜?room=${room}），
// 前端把当前表单值以 p_<key>=<value> 查询参数传入，此处展开后过滤。
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
	// 参数化级联: 收集 p_<key> 查询参数; source 含占位符或带了 p_ 参数即走参数化解析。
	params := map[string]string{}
	for k, vs := range r.URL.Query() {
		if strings.HasPrefix(k, "p_") && len(vs) > 0 {
			params[strings.TrimPrefix(k, "p_")] = vs[0]
		}
	}
	if len(params) > 0 || SourcePlaceholderKeys(source) != nil {
		opts, missing, err := resolveWithOptions(r.Context(), resolver, tenantID, source, params)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, fmt.Sprintf("解析选项来源失败: %v", err))
			return
		}
		if opts == nil {
			opts = []form.FieldOption{}
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"source": source, "options": opts, "missing_params": missing})
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

// parameterizedResolver 支持表单值参数化解析的选项来源（表单级联, 由 cmdbIAMResolver 实现）。
type parameterizedResolver interface {
	ResolveWithParams(ctx context.Context, tenantID, source string, params map[string]string) ([]form.FieldOption, error)
}

// resolveWithOptions 统一入口: 解析器支持参数化则走参数化路径, 否则退化为普通解析。
// 返回 missing(级联前置字段未就绪的键列表, 供前端提示)。
func resolveWithOptions(ctx context.Context, r form.SourceResolver, tenantID, source string, params map[string]string) ([]form.FieldOption, []string, error) {
	if pr, ok := r.(parameterizedResolver); ok {
		opts, err := pr.ResolveWithParams(ctx, tenantID, source, params)
		_, missing := ExpandSourceParams(source, params)
		return opts, missing, err
	}
	opts, err := r.Resolve(ctx, tenantID, source)
	return opts, nil, err
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
	// 流程主表单 + 节点权限感知校验（优先路径）：主表单模式下前端传空 scope，
	// 此处是节点保存唯一的服务端校验入口——按「流程主表单 + 当前节点字段权限」
	// 校验（ValidateValuesFullForStep：节点权限投影 + 行为规则联动 + cap），
	// 与前端运行期「applyStepFieldPerms 投影 → effectiveFieldStates 联动」完全镜像。
	// 无流程 / 无主表单 / 解析失败时回退下方 scope 路径或直接合并（保守放行，与创建路径策略一致）。
	stepValidated := false
	if ticket.ProcessID != "" && s.processStore != nil && s.formEngine != nil {
		if proc, perr := s.processStore.Get(r.Context(), ticket.ProcessID, tenantID); perr == nil && proc != nil && len(proc.Form) > 0 {
			var fs form.FormSchema
			if jerr := json.Unmarshal(proc.Form, &fs); jerr == nil && len(fs.Fields) > 0 {
				var perms []form.StepFieldPerm
				if inst, ierr := s.processStore.GetInstance(r.Context(), ticket.ID, tenantID); ierr == nil && inst != nil && inst.CurrentStep != "" {
					for i := range proc.Steps {
						if proc.Steps[i].ID == inst.CurrentStep {
							perms = proc.Steps[i].FormFields
							break
						}
					}
				}
				form.MergeDefaultsAs(&fs, req.Fields, auth.UserIDFromContext(r.Context()))
				_, computed, verr := s.formEngine.ValidateValuesFullForStep(r.Context(), tenantID, &fs, req.Fields, perms)
				if verr != nil {
					httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("表单校验失败: %v", verr))
					return
				}
				// 计算字段/联动派生值合并回写（服务端落定, 与前端展示一致）
				for k, v := range computed {
					req.Fields[k] = v
				}
				stepValidated = true
			}
		}
	}
	if !stepValidated && req.Scope != "" && s.formEngine != nil {
		if schema, serr := s.formEngine.ResolveSchema(r.Context(), tenantID, req.Scope); serr == nil && schema != nil {
			form.MergeDefaultsAs(schema, req.Fields, auth.UserIDFromContext(r.Context()))
			_, computed, verr := s.formEngine.ValidateValuesFull(r.Context(), tenantID, schema, req.Fields)
			if verr != nil {
				httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("表单校验失败: %v", verr))
				return
			}
			// 计算字段/联动派生值合并回写（服务端落定, 与前端展示一致）
			for k, v := range computed {
				req.Fields[k] = v
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

// handleFormCIAttr 数据联动 ci_attr 回填端点：按「模型类型名 + CI 名称」取单个属性。
// 前端在用户选中来源字段（如门店）后实时调用，把 CMDB 属性（如负责人）回填进表单；
// 后端提交校验时也会经 ciAttrResolver 再次落定（前后端一致）。
// GET /api/v1/itsm/form/ci-attr?type_name=&name=&attr=
func (s *Service) handleFormCIAttr(w http.ResponseWriter, r *http.Request) {
	if s.formEngine == nil || s.formEngine.CIAttrResolver() == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "ci_attr resolver not configured")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	q := r.URL.Query()
	typeName := strings.TrimSpace(q.Get("type_name"))
	name := strings.TrimSpace(q.Get("name"))
	attr := strings.TrimSpace(q.Get("attr"))
	if typeName == "" || name == "" || attr == "" {
		httputil.WriteError(w, http.StatusBadRequest, "type_name/name/attr 均不能为空")
		return
	}
	v, found, err := s.formEngine.CIAttrResolver().ResolveCIAttr(r.Context(), tenantID, typeName, name, attr)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"found": found}
	if found {
		resp["value"] = v
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// POST /api/v1/itsm/form/rules-preview
// 表单设计器「试算」：用当前草稿 schema + 样例值跑一遍数据联动/决策表推导，
// 返回推导后的完整值、触发规则（含决策表命中行号）与配置校验提示。
// 与提交时走同一条 ApplyDataRulesFull 通道（含 ci_attr / submitter_attr 实时解析）。
func (s *Service) handleFormRulesPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schema form.FormSchema `json:"schema"`
		Values map[string]any  `json:"values"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	warns, verr := form.ValidateDecisionTables(&req.Schema)
	if verr != nil {
		// 草稿允许未配完：校验错误也作为提示返回，不阻断试算。
		warns = append([]string{verr.Error()}, warns...)
	}
	tenantID := auth.TenantFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	values := req.Values
	if values == nil {
		values = map[string]any{}
	}
	var computed map[string]any
	var fired []string
	if s.formEngine != nil {
		computed, fired = s.formEngine.PreviewRules(r.Context(), tenantID, &req.Schema, values)
	} else {
		computed, fired = form.ApplyDataRules(&req.Schema, values)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"values":   computed,
		"fired":    fired,
		"warnings": warns,
	})
}

// handleFormSubmitterAttr 数据联动 submitter_attr 回填端点：取当前登录用户（提单人）
// 的单个属性（如 department=部门/门店）。前端打开表单时实时调用回填隐藏字段；
// 后端提交校验时也会经 submitterAttrResolver 再次落定（前后端一致）。
// GET /api/v1/itsm/form/submitter-attr?attr=department
func (s *Service) handleFormSubmitterAttr(w http.ResponseWriter, r *http.Request) {
	if s.formEngine == nil || s.formEngine.SubmitterAttrResolver() == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "submitter_attr resolver not configured")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	attr := strings.TrimSpace(r.URL.Query().Get("attr"))
	if attr == "" {
		httputil.WriteError(w, http.StatusBadRequest, "attr 不能为空")
		return
	}
	v, found, err := s.formEngine.SubmitterAttrResolver().ResolveSubmitterAttr(r.Context(), tenantID, attr)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"found": found}
	if found {
		resp["value"] = v
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}
