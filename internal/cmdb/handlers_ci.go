package cmdb

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"

	"github.com/opsorigin/core/pkg/httputil"
)

// handleCreateCI godoc
// @Summary 创建 CI
// @Description 创建一个新的配置项
// @Tags CMDB
// @Accept json
// @Produce json
// @Param ci body object true "CI 信息"
// @Success 201 {object} model.CIModel
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/cmdb/ci [post]
// @Security BearerAuth
func (s *Service) handleCreateCI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		TypeID      string            `json:"type_id"`
		Name        string            `json:"name"`
		Description string            `json:"description,omitempty"`
		Attributes  map[string]any    `json:"attributes"`
		Labels      map[string]string `json:"labels,omitempty"`
		Channel     int               `json:"channel"`
	}

	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ci := &model.CIModel{
		TypeID:      req.TypeID,
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		Status:      model.CIStatusActive,
		Attributes:  req.Attributes,
		Labels:      req.Labels,
		Channel:     resolveChannel(req.Channel),
	}

	traceID := traceIDFromCtx(ctx)
	if s.pg != nil {
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, ""); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.pg.CreateCI(ctx, ci, traceID); err != nil {
			slog.Error("create ci failed", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// 内存回退:跳过 schema 校验(无 modelType 表)
		s.mem.createCI(ci)
	}

	if s.onCICreated != nil {
		s.onCICreated(ctx, ci.ID, tenantID)
	}

	// P2-5 订阅分发：CI 创建事件
	s.dispatchCIEvent(ctx, tenantID, "ci_created", ci)

	httputil.WriteJSON(w, http.StatusCreated, ci)
}

// handleBatchCreateCI 批量创建 CI(F4-B 决策 B: 异步化)。
// 请求体为 CI 数组; 每条经最小校验后提交到 ingest 缓冲(202 立即返回),
// 由后台 flush 批量落库, 绝不阻塞调用方。缓冲满时该条计入 ingest_dropped_total。
func (s *Service) handleBatchCreateCI(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())

	var reqs []struct {
		TypeID      string            `json:"type_id"`
		Name        string            `json:"name"`
		Description string            `json:"description,omitempty"`
		Attributes  map[string]any    `json:"attributes"`
		Labels      map[string]string `json:"labels,omitempty"`
		Channel     int               `json:"channel"`
	}
	if err := httputil.BindJSON(r, &reqs); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(reqs) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "empty batch")
		return
	}

	accepted := 0
	for i := range reqs {
		req := &reqs[i]
		if req.Name == "" {
			continue
		}
		ci := &model.CIModel{
			TypeID:      req.TypeID,
			TenantID:    tenantID,
			Name:        req.Name,
			Description: req.Description,
			Status:      model.CIStatusActive,
			Attributes:  req.Attributes,
			Labels:      req.Labels,
			Channel:     resolveChannel(req.Channel),
		}
		if !s.ing.Submit(ci) {
			// 缓冲满: 丢弃(计入 dropped 指标), 不阻塞
			continue
		}
		accepted++
	}

	httputil.WriteJSON(w, http.StatusAccepted, map[string]any{
		"accepted": accepted,
		"total":    len(reqs),
	})
}

// @Summary 获取 CI 详情
// @Description 根据 ID 获取配置项详情
// @Tags CMDB
// @Accept json
// @Produce json
// @Param id path string true "CI ID"
// @Success 200 {object} model.CIModel
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/cmdb/ci/{id} [get]
// @Security BearerAuth
// handleChainQuery 跨模型关联查询(we-cmdb 综合查询借鉴):
// 从起点模型出发, 沿关系链逐跳展开, 返回分层结果。
// 请求: {start_type, path:[{relation, direction}], max_depth}
// 响应: {layers:[{depth, type_id, cis:[...]}]}
func (s *Service) handleChainQuery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		StartType string `json:"start_type"`
		Path      []struct {
			Relation  string `json:"relation"`
			Direction string `json:"direction"` // out(默认) | in
		} `json:"path"`
		MaxDepth int `json:"max_depth"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.StartType == "" {
		httputil.WriteError(w, http.StatusBadRequest, "start_type is required")
		return
	}
	if req.MaxDepth <= 0 || req.MaxDepth > 5 {
		req.MaxDepth = 3
	}
	if s.pg == nil {
		httputil.WriteError(w, http.StatusBadRequest, "requires pg backend")
		return
	}

	rels, err := s.pg.ListRelations(ctx, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// relation → 邻接表(源→目标 与 目标→源)
	type edge struct{ from, to string }
	adjOut := map[string][]string{} // from → tos
	adjIn := map[string][]string{}  // to → froms
	for _, rel := range rels {
		adjOut[rel.SourceID] = append(adjOut[rel.SourceID], rel.TargetID)
		adjIn[rel.TargetID] = append(adjIn[rel.TargetID], rel.SourceID)
	}

	// 起点 CI
	start, err := s.pg.ListCI(ctx, tenantID, req.StartType)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type layer struct {
		Depth  int              `json:"depth"`
		TypeID string           `json:"type_id"`
		CIs    []*model.CIModel `json:"cis"`
	}
	layers := []layer{{Depth: 0, TypeID: req.StartType, CIs: start}}

	current := map[string]bool{}
	for _, ci := range start {
		current[ci.ID] = true
	}

	for depth := 1; depth <= req.MaxDepth; depth++ {
		step := req.Path[depth-1]
		nextIDs := map[string]bool{}
		for id := range current {
			if step.Direction == "in" {
				for _, n := range adjIn[id] {
					nextIDs[n] = true
				}
			} else {
				for _, n := range adjOut[id] {
					nextIDs[n] = true
				}
			}
		}
		if len(nextIDs) == 0 {
			break
		}
		var next []*model.CIModel
		typeByID := map[string]string{}
		for id := range nextIDs {
			ci, err := s.pg.GetCI(ctx, id, tenantID)
			if err == nil && ci != nil {
				next = append(next, ci)
				typeByID[id] = ci.TypeID
			}
		}
		// 层类型取多数(简化为首个)
		layerType := req.StartType
		for id := range nextIDs {
			if t, ok := typeByID[id]; ok {
				layerType = t
				break
			}
		}
		layers = append(layers, layer{Depth: depth, TypeID: layerType, CIs: next})
		current = nextIDs
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"layers": layers, "total_layers": len(layers)})
}

func (s *Service) handleGetCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	if s.pg == nil {
		ci := s.mem.getCI(id, tenantID)
		if ci == nil {
			httputil.WriteError(w, http.StatusNotFound, "ci not found")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, ci)
		return
	}
	ci, err := s.pg.GetCI(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ci == nil {
		httputil.WriteError(w, http.StatusNotFound, "ci not found")
		return
	}
	// P2-4 字段级规则权限
	if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, ci.TypeID); rerr == nil {
		applyFieldRules([]*model.CIModel{ci}, rules, subjectFromRequest(r))
	}
	// 聚合字段: 模型声明 Aggregate 时按关系链实时计算(count/sum/avg)。
	s.computeAggregates(r.Context(), tenantID, ci)
	httputil.WriteJSON(w, http.StatusOK, ci)
}

func (s *Service) handleUpdateCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Status      string            `json:"status"`
		Attributes  map[string]any    `json:"attributes"`
		Labels      map[string]string `json:"labels,omitempty"`
		Channel     int               `json:"channel"`
	}

	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var existing *model.CIModel
	if s.pg != nil {
		var err error
		existing, err = s.pg.GetCI(ctx, id, tenantID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// PG 不可用时才回退到内存存储,避免每次都多查一次
		existing = s.mem.getCI(id, tenantID)
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "ci not found")
		return
	}

	if req.Attributes == nil {
		req.Attributes = existing.Attributes
	}
	if req.Labels == nil {
		req.Labels = existing.Labels
	}
	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.Status == "" {
		req.Status = string(existing.Status)
	}

	ci := &model.CIModel{
		ID:          id,
		TypeID:      existing.TypeID,
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		Status:      model.CIStatus(req.Status),
		Attributes:  req.Attributes,
		Labels:      req.Labels,
	}

	channel := resolveChannel(req.Channel)
	traceID := traceIDFromCtx(ctx)

	if s.pg != nil {
		// 更新路径同样校验(必填/类型/唯一/引用), exclude 自身避免自撞。
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, id); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		// P2-6 写拦截: 角色 update 策略下目标 CI 必须满足条件
		if err := s.checkDataPolicyWrite(ctx, tenantID, ci.TypeID, ci, "update"); err != nil {
			httputil.WriteError(w, http.StatusForbidden, err.Error())
			return
		}
		if err := s.pg.UpdateCI(ctx, ci, channel, traceID); err != nil {
			if errors.Is(err, pg.ErrAntiFlapping) {
				httputil.WriteError(w, http.StatusConflict, err.Error())
				return
			}
			slog.Error("update ci failed", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		s.mem.updateCI(ci)
	}

	// P2-5 订阅分发：CI 更新事件
	s.dispatchCIEvent(ctx, tenantID, "ci_updated", ci)

	httputil.WriteJSON(w, http.StatusOK, ci)
}

func (s *Service) handleDeleteCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	traceID := traceIDFromCtx(r.Context())

	if s.pg == nil {
		if !s.mem.deleteCI(id, tenantID) {
			httputil.WriteError(w, http.StatusNotFound, "ci not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// 删除前取快照，用于订阅分发（P2-5）
	before, _ := s.pg.GetCI(r.Context(), id, tenantID)
	// P2-6 写拦截: 角色 delete 策略下目标 CI 必须满足条件
	if before != nil {
		if err := s.checkDataPolicyWrite(r.Context(), tenantID, before.TypeID, before, "delete"); err != nil {
			httputil.WriteError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if err := s.pg.DeleteCI(r.Context(), id, tenantID, traceID); err != nil {
		if err.Error() == "ci not found" {
			httputil.WriteError(w, http.StatusNotFound, "ci not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if before != nil {
		s.dispatchCIEvent(r.Context(), tenantID, "ci_deleted", before)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListCI godoc
// @Summary 获取 CI 列表
// @Description 获取配置项列表，支持分页、排序、过滤
// @Tags CMDB
// @Accept json
// @Produce json
// @Param type_id query string false "CI 类型 ID"
// @Param sort_by query string false "排序字段"
// @Param sort_order query string false "排序方向 (asc/desc)"
// @Param limit query int false "每页数量"
// @Param offset query int false "偏移量"
// @Param archived query string false "归档过滤 (true/all/空)"
// @Success 200 {array} model.CIModel
// @Failure 500 {object} map[string]string
// @Router /api/v1/cmdb/ci [get]
// @Security BearerAuth
func (s *Service) handleListCI(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	typeID := r.URL.Query().Get("type_id")
	sortBy := r.URL.Query().Get("sort_by")
	sortOrder := r.URL.Query().Get("sort_order")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	// archived 过滤：true=仅归档 / all=含归档 / 其它(含空)=排除归档（默认）
	archivedMode := r.URL.Query().Get("archived")
	if archivedMode == "true" {
		archivedMode = "archived"
	}

	if s.pg == nil {
		cis := s.mem.listCIs(tenantID, typeID)
		if cis == nil {
			cis = []*model.CIModel{}
		}
		// 内存回退：按 archivedMode 过滤（status 字段）
		if archivedMode == "archived" {
			cis = filterByStatus(cis, model.CIStatusArchived)
		} else if archivedMode != "all" {
			cis = filterExcludeStatus(cis, model.CIStatusArchived)
		}
		httputil.WriteJSON(w, http.StatusOK, cis)
		return
	}
	cis, err := s.pg.ListCIFiltered(r.Context(), tenantID, typeID, archivedMode, sortBy, sortOrder, limit, offset)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	// P2-4 字段级规则权限
	if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, typeID); rerr == nil {
		applyFieldRules(cis, rules, subjectFromRequest(r))
	}
	// P2-6 行级数据权限: 角色策略过滤可见范围
	cis = filterByDataPolicy(cis, s.loadDataPolicies(r.Context(), tenantID), roleFromContext(r.Context()))
	httputil.WriteJSON(w, http.StatusOK, cis)
}

func (s *Service) handleCIAudit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	if s.pg == nil {
		httputil.WriteJSON(w, http.StatusOK, []*model.AttributeChange{})
		return
	}
	changes, err := s.pg.GetCIAudit(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if changes == nil {
		changes = []*model.AttributeChange{}
	}
	httputil.WriteJSON(w, http.StatusOK, changes)
}

func (s *Service) handleSearchCI(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	q := r.URL.Query().Get("q")
	typeID := r.URL.Query().Get("type_id")
	// archived 过滤：true=仅归档 / all=含归档 / 其它(含空)=排除归档（与列表保持一致）
	archivedMode := r.URL.Query().Get("archived")
	if archivedMode == "true" {
		archivedMode = "archived"
	}

	if s.pg == nil {
		cis := s.mem.listCIs(tenantID, typeID)
		if q != "" {
			filtered := make([]*model.CIModel, 0)
			for _, ci := range cis {
				if strings.Contains(ci.Name, q) || strings.Contains(ci.Description, q) || strings.Contains(ci.TypeID, q) {
					filtered = append(filtered, ci)
				}
			}
			cis = filtered
		}
		if archivedMode == "archived" {
			cis = filterByStatus(cis, model.CIStatusArchived)
		} else if archivedMode != "all" {
			cis = filterExcludeStatus(cis, model.CIStatusArchived)
		}
		if cis == nil {
			cis = []*model.CIModel{}
		}
		httputil.WriteJSON(w, http.StatusOK, cis)
		return
	}
	if q != "" {
		// Push the free-text filter down to SQL instead of loading the
		// entire CI table and filtering in Go — required for acceptable
		// performance as the dataset grows (see report issue 3.3).
		cis, err := s.pg.ListCIByQuery(r.Context(), tenantID, typeID, q)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if archivedMode == "archived" {
			cis = filterByStatus(cis, model.CIStatusArchived)
		} else if archivedMode != "all" {
			cis = filterExcludeStatus(cis, model.CIStatusArchived)
		}
		if cis == nil {
			cis = []*model.CIModel{}
		}
		// P2-4 字段级规则权限
		if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, typeID); rerr == nil {
			applyFieldRules(cis, rules, subjectFromRequest(r))
		}
		httputil.WriteJSON(w, http.StatusOK, cis)
		return
	}
	cis, err := s.pg.ListCI(r.Context(), tenantID, typeID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if archivedMode == "archived" {
		cis = filterByStatus(cis, model.CIStatusArchived)
	} else if archivedMode != "all" {
		cis = filterExcludeStatus(cis, model.CIStatusArchived)
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	// P2-4 字段级规则权限
	if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, typeID); rerr == nil {
		applyFieldRules(cis, rules, subjectFromRequest(r))
	}
	// P2-6 行级数据权限: 角色策略过滤可见范围(搜索与列表一致)
	cis = filterByDataPolicy(cis, s.loadDataPolicies(r.Context(), tenantID), roleFromContext(r.Context()))
	httputil.WriteJSON(w, http.StatusOK, cis)
}

// handleBatchUpdateCI 批量更新多个 CI 的属性(合并写), 逐个过 validateAttributes
// (唯一/引用/类型校验)。返回成功数与逐条失败明细, 不因单条失败中断整体。
func (s *Service) handleBatchUpdateCI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		IDs   []string       `json:"ids"`
		Attrs map[string]any `json:"attrs"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 || len(req.Attrs) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "ids and attrs are required")
		return
	}

	traceID := traceIDFromCtx(ctx)
	type failure struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	var failed []failure
	updated := 0

	for _, id := range req.IDs {
		var ci *model.CIModel
		if s.pg != nil {
			var err error
			ci, err = s.pg.GetCI(ctx, id, tenantID)
			if err != nil {
				failed = append(failed, failure{id, err.Error()})
				continue
			}
		} else {
			ci = s.mem.getCI(id, tenantID)
		}
		if ci == nil {
			failed = append(failed, failure{id, "ci not found"})
			continue
		}
		// 合并属性(批量只改给定字段, 不动其余)
		merged := map[string]any{}
		for k, v := range ci.Attributes {
			merged[k] = v
		}
		for k, v := range req.Attrs {
			merged[k] = v
		}
		ci.Attributes = merged

		if s.pg != nil {
			if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, id); err != nil {
				failed = append(failed, failure{id, err.Error()})
				continue
			}
			if err := s.pg.UpdateCI(ctx, ci, model.WeightHuman, traceID); err != nil {
				failed = append(failed, failure{id, err.Error()})
				continue
			}
		} else {
			s.mem.updateCI(ci)
		}
		updated++
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"updated": updated, "failed": failed})
}

// handleBatchDeleteCI 批量删除 CI(含订阅事件分发), 返回成功数与失败明细。
func (s *Service) handleBatchDeleteCI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "ids are required")
		return
	}

	traceID := traceIDFromCtx(ctx)
	type failure struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	var failed []failure
	deleted := 0

	for _, id := range req.IDs {
		if s.pg == nil {
			if !s.mem.deleteCI(id, tenantID) {
				failed = append(failed, failure{id, "ci not found"})
			} else {
				deleted++
			}
			continue
		}
		before, _ := s.pg.GetCI(ctx, id, tenantID)
		if before == nil {
			failed = append(failed, failure{id, "ci not found"})
			continue
		}
		if err := s.pg.DeleteCI(ctx, id, tenantID, traceID); err != nil {
			failed = append(failed, failure{id, err.Error()})
			continue
		}
		s.dispatchCIEvent(ctx, tenantID, "ci_deleted", before)
		deleted++
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "failed": failed})
}

// handleExportCI 导出某类型全部 CI 为 CSV 附件（表头: id,name,description,status + 模型 schema 属性列）。
func (s *Service) handleExportCI(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	typeID := r.URL.Query().Get("type_id")
	if typeID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "type_id is required")
		return
	}
	mt, err := s.ResolveModelType(r.Context(), typeID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	attrCols := []string{}
	if mt != nil {
		for k := range mt.Schema {
			attrCols = append(attrCols, k)
		}
		sort.Strings(attrCols)
	}
	cols := append([]string{"id", "name", "description", "status"}, attrCols...)

	var cis []*model.CIModel
	if s.pg != nil {
		cis, err = s.pg.ListCI(r.Context(), tenantID, typeID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		cis = s.mem.listCIs(tenantID, typeID)
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	if err := cw.Write(cols); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, ci := range cis {
		row := []string{ci.ID, ci.Name, ci.Description, string(ci.Status)}
		for _, c := range attrCols {
			row = append(row, toCSVValue(ci.Attributes[c]))
		}
		if err := cw.Write(row); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fname := fmt.Sprintf("cmdb_%s_%s.csv", typeID, time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fname))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// handleImportCI 从上传的 CSV 批量导入 CI（支持按 id 列 upsert 更新，否则新建）。
func (s *Service) handleImportCI(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	typeID := r.URL.Query().Get("type_id")
	if typeID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "type_id is required")
		return
	}
	mt, err := s.ResolveModelType(r.Context(), typeID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid csv: "+err.Error())
		return
	}
	if len(records) < 2 {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"created": 0, "updated": 0, "failed": []any{}})
		return
	}

	header := records[0]
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	if _, ok := idx["name"]; !ok {
		httputil.WriteError(w, http.StatusBadRequest, "csv must contain a 'name' column")
		return
	}
	get := func(row []string, col string) string {
		if i, ok := idx[col]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}

	var toCreate []*model.CIModel
	updated := 0
	var failed []map[string]any
	traceID := traceIDFromCtx(r.Context())

	for ri, row := range records[1:] {
		lineNo := ri + 2
		recordFailed := func(msg string) {
			failed = append(failed, map[string]any{"row": lineNo, "error": msg})
		}
		name := get(row, "name")
		if name == "" {
			recordFailed("name is empty")
			continue
		}
		attrs := map[string]any{}
		if mt != nil {
			loopBroken := false
			for k, def := range mt.Schema {
				raw := get(row, k)
				if raw == "" {
					continue
				}
				v, cerr := fromCSVValue(raw, def.Type)
				if cerr != nil {
					recordFailed(fmt.Sprintf("attr %s: %v", k, cerr))
					loopBroken = true
					break
				}
				attrs[k] = v
			}
			if loopBroken {
				continue
			}
			for k, def := range mt.Schema {
				if def.Required {
					if _, ok := attrs[k]; !ok {
						recordFailed(fmt.Sprintf("required attr %s missing", k))
						loopBroken = true
						break
					}
				}
			}
			if loopBroken {
				continue
			}
		}

		// upsert: 含 id 且该 id 存在则更新，否则新建
		if idVal := get(row, "id"); idVal != "" && s.pg != nil {
			if existing, gerr := s.pg.GetCI(r.Context(), idVal, tenantID); gerr == nil && existing != nil {
				existing.Name = name
				if d := get(row, "description"); d != "" {
					existing.Description = d
				}
				if st := get(row, "status"); st != "" {
					existing.Status = model.CIStatus(st)
				}
				for k, v := range attrs {
					existing.Attributes[k] = v
				}
				existing.Channel = model.WeightHuman
				if uerr := s.pg.UpdateCI(r.Context(), existing, model.WeightHuman, traceID); uerr != nil {
					recordFailed(uerr.Error())
				} else {
					updated++
				}
				continue
			}
		}

		ci := &model.CIModel{
			TypeID:      typeID,
			TenantID:    tenantID,
			Name:        name,
			Description: get(row, "description"),
			Status:      model.CIStatusActive,
			Attributes:  attrs,
			Channel:     model.WeightHuman,
		}
		if st := get(row, "status"); st != "" {
			ci.Status = model.CIStatus(st)
		}
		toCreate = append(toCreate, ci)
	}

	created := 0
	if len(toCreate) > 0 {
		if s.pg != nil {
			if berr := s.pg.BulkCreateCI(r.Context(), toCreate, traceID); berr != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "bulk create failed: "+berr.Error())
				return
			}
			created = len(toCreate)
		} else {
			for _, ci := range toCreate {
				s.mem.createCI(ci)
			}
			created = len(toCreate)
		}
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"created": created,
		"updated": updated,
		"failed":  failed,
	})
}

// handleCollectReport 自动采集上报端点(供 Agent / 采集器调用)。
// 固定格式：按 模型ID-字段ID 定位字段写入、按 模型ID-关系ID 建立关系，
// 与人工建实例的 handleCreateCI 解耦，使采集端只需记住「模型ID-字段ID」映射。
//
// 请求体(单条)：
//
//	{
//	  "model_id": "server",
//	  "identify": { "field_id": "ip", "value": "10.0.0.1" },  // 定位/新建实例的键
//	  "name": "web-01",                                       // 新建时使用的展示名(可选, 默认取 identify value)
//	  "attrs": { "cpu": 32, "mem_gb": 128 },                  // field_id -> 值
//	  "relations": [{ "relation_id": "hosts", "target": "app-1" }] // relation_id + 目标 CI ID
//	}
//
// 批量：{ "reports": [ ... ] }。
// handleCollectReport 自动采集上报端点(供 Agent / 采集器调用)。
// 固定格式：按 模型ID-字段ID 定位字段写入、按 模型ID-关系ID 建立关系，
// 与人工建实例的 handleCreateCI 解耦，使采集端只需记住「模型ID-字段ID」映射。
//
// 请求体(单条)：
//
//	{
//	  "model_id": "server",
//	  "identify": { "field_id": "ip", "value": "10.0.0.1" },  // 定位/新建实例的键
//	  "name": "web-01",                                       // 新建时使用的展示名(可选, 默认取 identify value)
//	  "attrs": { "cpu": 32, "mem_gb": 128 },                  // field_id -> 值
//	  "relations": [{ "relation_id": "hosts", "target": "app-1" }] // relation_id + 目标 CI ID
//	}
//
// 批量：{ "reports": [ ... ] }。
func (s *Service) handleCollectReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var body struct {
		Reports []collectReportItem `json:"reports"`
	}
	// 兼容单条直接传(无 reports 包裹)
	var single collectReportItem
	if err := httputil.BindJSON(r, &single); err == nil && single.ModelID != "" {
		body.Reports = []collectReportItem{single}
	} else {
		if err := httputil.BindJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if len(body.Reports) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "empty reports")
		return
	}

	results, ok := s.applyCollectReports(ctx, tenantID, body.Reports)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"accepted": len(body.Reports),
		"ok":       ok,
		"results":  results,
	})
}

// applyCollectReports 批量处理采集上报(按 模型ID-字段ID/关系ID 落库, 不存在则新建)。
// 抽离自 handleCollectReport, 供 HTTP 端点与 agentmgr 的 CMDBReportSink 共用同一条落库链路,
// 保证「自定义脚本采集 / 内置发现插件 / 直接 HTTP 上报」三路输出经统一规范写入 CMDB。
func (s *Service) applyCollectReports(ctx context.Context, tenantID string, reports []collectReportItem) ([]map[string]any, int) {
	traceID := traceIDFromCtx(ctx)
	results := make([]map[string]any, 0, len(reports))
	ok := 0

	for i, rep := range reports {
		ri := reportResultItem{Index: i}
		mt, err := s.ResolveModelType(ctx, rep.ModelID, tenantID)
		if err != nil {
			ri.Error = "resolve model failed: " + err.Error()
			results = append(results, toMap(ri))
			continue
		}
		if mt == nil {
			ri.Error = "model type not found: " + rep.ModelID
			results = append(results, toMap(ri))
			continue
		}
		if !mt.IsInstantiable() {
			ri.Error = "model type is abstract (not instantiable): " + rep.ModelID
			results = append(results, toMap(ri))
			continue
		}

		// 解析 attrs：field_id -> 值(按 FieldByNameOrID 回退到 name)
		attrs := map[string]any{}
		for key, val := range rep.Attrs {
			if _, ok := mt.FieldByNameOrID(key); !ok {
				ri.Error = "unknown field: " + key
				results = append(results, toMap(ri))
				continue
			}
			attrs[key] = val
		}
		if ri.Error != "" {
			continue
		}

		// 定位或新建 CI
		ciID := ""
		var existing *model.CIModel
		if rep.Identify != nil && rep.Identify.FieldID != "" {
			if _, fok := mt.FieldByNameOrID(rep.Identify.FieldID); !fok {
				ri.Error = "unknown identify field: " + rep.Identify.FieldID
				results = append(results, toMap(ri))
				continue
			}
			// 按 identify 字段值查已有 CI
			found, ferr := s.findCIByAttribute(ctx, tenantID, rep.ModelID, rep.Identify.FieldID, rep.Identify.Value)
			if ferr != nil {
				ri.Error = "find ci failed: " + ferr.Error()
				results = append(results, toMap(ri))
				continue
			}
			existing = found
		}

		if existing != nil {
			ciID = existing.ID
			// 合并属性后更新(保留未上报字段)
			merged := map[string]any{}
			for k, v := range existing.Attributes {
				merged[k] = v
			}
			for k, v := range attrs {
				merged[k] = v
			}
			if s.pg != nil {
				if uerr := s.UpdateCIAttributes(ctx, ciID, tenantID, merged); uerr != nil {
					ri.Error = "update ci failed: " + uerr.Error()
					results = append(results, toMap(ri))
					continue
				}
			} else if s.mem != nil {
				s.mem.updateCI(&model.CIModel{ID: ciID, TenantID: tenantID, Attributes: merged})
			}
			ri.Action = "updated"
		} else {
			name := rep.Name
			if name == "" && rep.Identify != nil {
				name = fmt.Sprintf("%v", rep.Identify.Value)
			}
			ci := &model.CIModel{
				TypeID:      rep.ModelID,
				TenantID:    tenantID,
				Name:        name,
				Description: rep.Description,
				Status:      model.CIStatusActive,
				Attributes:  attrs,
				Channel:     model.WeightAutoScript,
			}
			if s.pg != nil {
				if cerr := s.pg.CreateCI(ctx, ci, traceID); cerr != nil {
					ri.Error = "create ci failed: " + cerr.Error()
					results = append(results, toMap(ri))
					continue
				}
			} else if s.mem != nil {
				s.mem.createCI(ci)
			}
			ciID = ci.ID
			ri.Action = "created"
		}
		ri.CIID = ciID

		// 建立关系(按 relation_id 解析 RelationType)
		for _, rel := range rep.Relations {
			rdef, rok := mt.RelationByID(rel.RelationID)
			relType := rel.RelationType
			if rok {
				relType = rdef.RelationType
			}
			if relType == "" {
				ri.Error = "unknown relation: " + rel.RelationID
				break
			}
			if verr := s.validateRelationCardinality(ctx, tenantID, ciID, rel.Target, relType); verr != nil {
				ri.Error = "relation validate failed: " + verr.Error()
				break
			}
			relation := &model.CIRelation{
				TenantID:     tenantID,
				SourceID:     ciID,
				TargetID:     rel.Target,
				RelationType: relType,
				Properties:   rel.Properties,
			}
			if s.pg != nil {
				if crerr := s.pg.CreateRelation(ctx, relation, traceID); crerr != nil {
					ri.Error = "create relation failed: " + crerr.Error()
					break
				}
			} else if s.mem != nil {
				s.mem.createRelation(relation)
			}
		}
		if ri.Error == "" {
			ok++
		}
		results = append(results, toMap(ri))
	}
	return results, ok
}

// ApplyCollectReports 是 agentmgr 侧 CMDBReportSink 接口的实现:
// 接收通用的 report 单条 map(由 agentmgr 把 Agent 脚本 stdout 解析后得到), 统一经 applyCollectReports 落库。
// 入参 reports 每条须可被 json 反序列化为 collectReportItem(model_id + identify/attrs/relations)。
func (s *Service) ApplyCollectReports(ctx context.Context, tenantID string, raw []map[string]any) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	items := make([]collectReportItem, 0, len(raw))
	for _, m := range raw {
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		var item collectReportItem
		if err := json.Unmarshal(b, &item); err != nil || item.ModelID == "" {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return 0, fmt.Errorf("no valid collect report items")
	}
	_, ok := s.applyCollectReports(ctx, tenantID, items)
	return ok, nil
}

// reportResultItem 是采集上报单条处理结果(供 applyCollectReports 统一返回)。
type reportResultItem struct {
	Index  int    `json:"index"`
	CIID   string `json:"ci_id,omitempty"`
	Action string `json:"action,omitempty"` // created | updated | skipped
	Error  string `json:"error,omitempty"`
}

// toMap 把 reportResultItem 转为 map(供 applyCollectReports 统一返回)。
func toMap(ri reportResultItem) map[string]any {
	m := map[string]any{"index": ri.Index}
	if ri.CIID != "" {
		m["ci_id"] = ri.CIID
	}
	if ri.Action != "" {
		m["action"] = ri.Action
	}
	if ri.Error != "" {
		m["error"] = ri.Error
	}
	return m
}

// collectReportItem 采集上报单条结构。
type collectReportItem struct {
	ModelID     string                  `json:"model_id"`
	Name        string                  `json:"name,omitempty"`
	Description string                  `json:"description,omitempty"`
	Identify    *collectReportIdentify  `json:"identify,omitempty"`
	Attrs       map[string]any          `json:"attrs,omitempty"`
	Relations   []collectReportRelation `json:"relations,omitempty"`
}

type collectReportIdentify struct {
	FieldID string `json:"field_id"`
	Value   any    `json:"value"`
}

type collectReportRelation struct {
	RelationID   string         `json:"relation_id"`
	RelationType string         `json:"relation_type,omitempty"` // 可选, 缺省按 relation_id 解析
	Target       string         `json:"target"`
	Properties   map[string]any `json:"properties,omitempty"`
}
