package cmdb

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/pkg/validate"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleCreateModelType(w http.ResponseWriter, r *http.Request) {
	var mt model.CIModelType
	if err := httputil.BindJSON(r, &mt); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.Name("模型名称", mt.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	mt.TenantID = auth.TenantFromContext(r.Context())

	// 模型继承：禁止自引用（读取侧有环检测兜底，此处阻断明显错误）。
	// 注意：新建时 mt.ID 与 mt.ParentID 均为空，不能误判为"继承自身"（历史 bug：新建必失败）。
	if mt.ID != "" && mt.ParentID == mt.ID {
		httputil.WriteError(w, http.StatusBadRequest, "模型不能继承自身")
		return
	}
	if mt.Category == "" {
		mt.Category = model.ModelCategoryPlatform
	}

	if err := s.pg.CreateCIModelType(r.Context(), &mt); err != nil {
		slog.Error("create model type failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, mt)
}

// handleFieldUsage 字段消费度统计(以流定库/MVD 的执行依据):
// 扫描保存查询与 ITSM 表单 schema 对模型字段的引用, 产出每字段的
// 消费来源与次数; 无任何消费的字段标 unused(僵尸字段, 可考虑下线)。
func (s *Service) handleFieldUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(ctx)

	mt, err := s.pg.GetCIModelType(ctx, id, tenantID)
	if err != nil || mt == nil {
		httputil.WriteError(w, http.StatusNotFound, "model type not found")
		return
	}

	// 收集消费引用: 保存查询 attrs + ITSM 表单字段 key
	saved := map[string]int{}
	savedQueries, err := s.pg.ListSavedQueries(ctx, tenantID)
	if err == nil {
		for _, q := range savedQueries {
			if attrs, ok := q.Query["attrs"].(map[string]any); ok {
				for k := range attrs {
					saved[k]++
				}
			}
		}
	}
	form := map[string]int{}
	formFields, err := s.pg.ListFormSchemaFields(ctx, tenantID)
	if err == nil {
		for _, f := range formFields {
			if k, ok := f["key"].(string); ok && k != "" {
				form[k]++
			}
		}
	}

	fields := make([]fieldUsage, 0, len(mt.Schema))
	for name, def := range mt.Schema {
		u := fieldUsage{Field: name, Type: def.Type, Required: def.Required,
			SavedQueries: saved[name], ITSMForms: form[name]}
		u.Total = u.SavedQueries + u.ITSMForms
		u.Unused = u.Total == 0
		fields = append(fields, u)
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Unused != fields[j].Unused {
			return !fields[i].Unused // unused 排后
		}
		return fields[i].Total > fields[j].Total
	})

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"model_type_id": id, "model_name": mt.Name,
		"fields": fields, "unused_count": countUnused(fields),
	})
}

// handleCIFieldUsage 实例级字段/关系使用统计（删除保护用）：
//   - fields:    该模型各 schema 字段被多少条 CI 实例实际使用（attributes 含该 key）
//   - relations: 该模型全部 CI 参与的关系按 relation_type 的条数
//
// 前端删除属性字段/关系定义前先查此接口，有实例使用则拦截并提示。
func (s *Service) handleCIFieldUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(ctx)

	mt, err := s.pg.GetCIModelType(ctx, id, tenantID)
	if err != nil || mt == nil {
		httputil.WriteError(w, http.StatusNotFound, "model type not found")
		return
	}

	fields := make(map[string]int, len(mt.Schema))
	for name := range mt.Schema {
		n, cerr := s.pg.CountCIByAttr(ctx, tenantID, id, name)
		if cerr != nil {
			slog.Warn("count ci by attr failed", "field", name, "err", cerr)
			continue
		}
		fields[name] = n
	}

	rels, rerr := s.pg.CountRelationTypesForModel(ctx, tenantID, id)
	if rerr != nil {
		slog.Warn("count relation types for model failed", "err", rerr)
		rels = map[string]int{}
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"model_type_id": id, "model_name": mt.Name,
		"fields": fields, "relations": rels,
	})
}

func (s *Service) handleGetModelType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	tenantID := auth.TenantFromContext(r.Context())
	mt, err := s.ResolveModelType(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mt == nil {
		httputil.WriteError(w, http.StatusNotFound, "model type not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, mt)
}

func (s *Service) handleListModelTypes(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())

	// 获取分页参数（默认 500：内置模型 122+，避免默认值截断模型清单）
	limit := 500
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	types, err := s.pg.ListCIModelTypes(r.Context(), tenantID, limit, offset)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 合并继承属性并默认分类，保证列表/详情展示完整属性集
	for _, mt := range types {
		if resolved, rerr := s.ResolveModelType(r.Context(), mt.ID, tenantID); rerr == nil && resolved != nil {
			mt.Schema = resolved.Schema
		}
		if mt.Category == "" {
			mt.Category = model.ModelCategoryPlatform
		}
	}

	// 获取总数
	total, err := s.pg.CountCIModelTypes(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if types == nil {
		types = []*model.CIModelType{}
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"items":  types,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Service) handleUpdateModelType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	// 获取现有模型类型
	existing, err := s.pg.GetCIModelType(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "model type not found")
		return
	}

	// 父模型保护：有子模型继承时禁止修改（结构由子模型依赖，防破坏继承链）
	children, err := s.pg.CountModelChildren(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if children > 0 {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("父模型受保护，存在 %d 个子模型，不可修改", children))
		return
	}

	var mt model.CIModelType
	if err := httputil.BindJSON(r, &mt); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 保留不可更新的字段
	mt.ID = existing.ID
	mt.TenantID = existing.TenantID
	mt.CreatedAt = existing.CreatedAt
	mt.Version = existing.Version + 1

	// 模型版本留痕: 更新前把旧状态存快照(可追溯/回滚)。
	if err := s.pg.SaveModelTypeVersion(r.Context(), &model.ModelTypeVersion{
		ModelTypeID: existing.ID, TenantID: existing.TenantID, Version: existing.Version,
		Name: existing.Name, Description: existing.Description, ParentID: existing.ParentID,
		Category: existing.Category, Icon: existing.Icon, Abstract: existing.Abstract,
		Schema: existing.Schema, Operator: "admin", CreatedAt: time.Now(),
	}); err != nil {
		slog.Warn("save model type version failed", "error", err) // 版本留痕失败不阻断更新
	}

	if err := s.pg.UpdateCIModelType(r.Context(), &mt); err != nil {
		slog.Error("update model type failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, mt)
}

// handleListModelTypeVersions 模型版本历史(新→旧, 含 schema 快照供前端 diff)。
func (s *Service) handleListModelTypeVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	versions, err := s.pg.ListModelTypeVersions(r.Context(), id, tenantID, 100)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []*model.ModelTypeVersion{}
	}
	httputil.WriteJSON(w, http.StatusOK, versions)
}

// handleRollbackModelType 回滚模型到指定版本(恢复 schema/名称/分类等)。
func (s *Service) handleRollbackModelType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	versionStr := r.PathValue("version")
	tenantID := auth.TenantFromContext(ctx)

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid version")
		return
	}
	snap, err := s.pg.GetModelTypeVersion(ctx, id, tenantID, version)
	if err != nil || snap == nil {
		httputil.WriteError(w, http.StatusNotFound, "version not found")
		return
	}
	current, err := s.pg.GetCIModelType(ctx, id, tenantID)
	if err != nil || current == nil {
		httputil.WriteError(w, http.StatusNotFound, "model type not found")
		return
	}

	// 当前状态先留痕(回滚本身也是一次变更)。
	if err := s.pg.SaveModelTypeVersion(ctx, &model.ModelTypeVersion{
		ModelTypeID: current.ID, TenantID: current.TenantID, Version: current.Version,
		Name: current.Name, Description: current.Description, ParentID: current.ParentID,
		Category: current.Category, Icon: current.Icon, Abstract: current.Abstract,
		Schema: current.Schema, Operator: "rollback", CreatedAt: time.Now(),
	}); err != nil {
		slog.Warn("save pre-rollback version failed", "error", err)
	}

	current.Name = snap.Name
	current.Description = snap.Description
	current.ParentID = snap.ParentID
	current.Category = snap.Category
	current.Icon = snap.Icon
	current.Abstract = snap.Abstract
	current.Schema = snap.Schema
	current.Version = snap.Version + 1

	if err := s.pg.UpdateCIModelType(ctx, current); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "model_type_id": id, "version": current.Version})
}

func (s *Service) handleDeleteModelType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	// 内置模型 / 父模型保护：禁止删除
	existing, err := s.pg.GetCIModelType(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "model type not found")
		return
	}
	if existing.Builtin {
		httputil.WriteError(w, http.StatusBadRequest, "内置模型受保护，不可删除")
		return
	}
	children, err := s.pg.CountModelChildren(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if children > 0 {
		childNames, cerr := s.pg.ListModelChildren(r.Context(), id, tenantID)
		if cerr != nil {
			httputil.WriteError(w, http.StatusInternalServerError, cerr.Error())
			return
		}
		msg := fmt.Sprintf("该模型为父模型，还有 %d 个子模型在使用该模型", children)
		if len(childNames) > 0 {
			msg += "：「" + strings.Join(childNames, "」「") + "」，请先删除这些子模型"
		}
		httputil.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	// 检查是否有CI实例使用此模型类型
	count, err := s.pg.CountCIByTypeID(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count > 0 {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("该模型下还有 %d 条实例记录，请先删除这些实例后再删除模型", count))
		return
	}

	if err := s.pg.DeleteCIModelType(r.Context(), id, tenantID); err != nil {
		slog.Error("delete model type failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleExportModelTypes 导出模型定义为 JSON 数组（跳过内置模型：内置模型不可导出）。
func (s *Service) handleExportModelTypes(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	types, err := s.pg.ListCIModelTypes(r.Context(), tenantID, 1000, 0)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := map[string]bool{}
	if q := r.URL.Query().Get("ids"); q != "" {
		for _, x := range strings.Split(q, ",") {
			if t := strings.TrimSpace(x); t != "" {
				ids[t] = true
			}
		}
	}
	out := make([]map[string]any, 0, len(types))
	for _, mt := range types {
		if mt.Builtin {
			continue
		}
		if len(ids) > 0 && !ids[mt.ID] {
			continue
		}
		out = append(out, map[string]any{
			"id":           mt.ID,
			"name":         mt.Name,
			"description":  mt.Description,
			"parent_id":    mt.ParentID,
			"category":     mt.Category,
			"sub_category": mt.SubCategory,
			"icon":         mt.Icon,
			"show_in_menu": mt.ShowInMenu,
			"schema":       mt.Schema,
		})
	}
	w.Header().Set("Content-Disposition", `attachment; filename="cmdb-model-types.json"`)
	w.Header().Set("Content-Type", "application/json")
	httputil.WriteJSON(w, http.StatusOK, out)
}

// handleImportModelTypes 从 JSON 数组导入模型定义。
// 父子排序后创建，old id → new id 映射保证继承关系重建；重名/内置/无名跳过。
func (s *Service) handleImportModelTypes(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var raw []model.CIModelType
	if err := httputil.BindJSON(r, &raw); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	inSet := map[string]bool{}
	for _, d := range raw {
		if d.ID != "" {
			inSet[d.ID] = true
		}
	}
	type item struct {
		d           model.CIModelType
		parentFirst bool
	}
	var seq []item
	for _, d := range raw {
		parentFirst := d.ParentID == "" || !inSet[d.ParentID]
		seq = append(seq, item{d: d, parentFirst: parentFirst})
	}
	// 父在前：parentFirst=true 的排前面
	sort.SliceStable(seq, func(i, j int) bool {
		return seq[i].parentFirst && !seq[j].parentFirst
	})

	idMap := map[string]string{} // old id -> new id
	created, skipped := 0, 0
	for _, it := range seq {
		d := it.d
		if d.Builtin || d.Name == "" {
			skipped++
			continue
		}
		if existing, _ := s.pg.GetCIModelTypeByName(r.Context(), d.Name, tenantID); existing != nil {
			skipped++
			continue
		}
		parentNew := ""
		if d.ParentID != "" {
			if v, ok := idMap[d.ParentID]; ok {
				parentNew = v
			} else if p, _ := s.pg.GetCIModelType(r.Context(), d.ParentID, tenantID); p != nil {
				parentNew = d.ParentID // 父已在本租户
			}
		}
		mt := &model.CIModelType{
			Name:        d.Name,
			Description: d.Description,
			ParentID:    parentNew,
			Category:    d.Category,
			SubCategory: d.SubCategory,
			Icon:        d.Icon,
			ShowInMenu:  d.ShowInMenu,
			Schema:      d.Schema,
			TenantID:    tenantID,
		}
		if mt.Category == "" {
			mt.Category = model.ModelCategoryPlatform
		}
		if err := s.pg.CreateCIModelType(r.Context(), mt); err != nil {
			slog.Error("import model type failed", "error", err, "name", d.Name)
			skipped++
			continue
		}
		if d.ID != "" {
			idMap[d.ID] = mt.ID
		}
		created++
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"created": created, "skipped": skipped})
}
