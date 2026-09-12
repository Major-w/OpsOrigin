package cmdb

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// handleCaptureSnapshot 捕获一份 CMDB 基线快照（全量 CI + 关系）。
// POST /api/v1/cmdb/snapshots?name=xxx
func (s *Service) handleCaptureSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "snapshot requires postgres backend")
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "基线快照-" + time.Now().Format("20060102-150405")
	}

	cis, err := s.pg.ListCI(ctx, tenantID, "")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "list ci: "+err.Error())
		return
	}
	rels, err := s.pg.ListRelations(ctx, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "list relations: "+err.Error())
		return
	}

	data := pg.SnapshotData{}
	for _, ci := range cis {
		attrs := ci.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		data.CIs = append(data.CIs, pg.SnapshotCI{
			ID:     ci.ID,
			Name:   ci.Name,
			TypeID: ci.TypeID,
			Status: string(ci.Status),
			Attrs:  attrs,
		})
	}
	for _, rel := range rels {
		data.Rels = append(data.Rels, pg.SnapshotRel{
			SourceID:     rel.SourceID,
			TargetID:     rel.TargetID,
			RelationType: rel.RelationType,
		})
	}

	snap := &pg.Snapshot{
		TenantID: tenantID,
		Name:     name,
		CICount:  len(data.CIs),
		RelCount: len(data.Rels),
		Data:     data,
	}
	if err := s.pg.CreateSnapshot(ctx, snap); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, snap)
}

// handleListSnapshots GET /api/v1/cmdb/snapshots
func (s *Service) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "snapshot requires postgres backend")
		return
	}
	list, err := s.pg.ListSnapshots(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

// handleGetSnapshot GET /api/v1/cmdb/snapshots/{id}
func (s *Service) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "snapshot requires postgres backend")
		return
	}
	snap, err := s.pg.GetSnapshot(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snap == nil {
		httputil.WriteError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, snap)
}

// handleDeleteSnapshot DELETE /api/v1/cmdb/snapshots/{id}
func (s *Service) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "snapshot requires postgres backend")
		return
	}
	if err := s.pg.DeleteSnapshot(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleCompareSnapshots 对比两份快照（或「快照 vs 当前实时」），给出配置漂移报告。
// POST /api/v1/cmdb/snapshots/compare  body: {base_id, target_id?}
func (s *Service) handleCompareSnapshots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "snapshot requires postgres backend")
		return
	}
	var req struct {
		BaseID   string `json:"base_id"`
		TargetID string `json:"target_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil || req.BaseID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "base_id is required")
		return
	}
	base, err := s.pg.GetSnapshot(ctx, req.BaseID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if base == nil {
		httputil.WriteError(w, http.StatusNotFound, "base snapshot not found")
		return
	}

	var targetName string
	var targetData pg.SnapshotData
	var targetIsLive bool
	if req.TargetID != "" {
		t, err := s.pg.GetSnapshot(ctx, req.TargetID, tenantID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if t == nil {
			httputil.WriteError(w, http.StatusNotFound, "target snapshot not found")
			return
		}
		targetName = t.Name
		targetData = t.Data
	} else {
		targetIsLive = true
		targetName = "当前实时"
		cis, e1 := s.pg.ListCI(ctx, tenantID, "")
		rels, e2 := s.pg.ListRelations(ctx, tenantID)
		if e1 != nil || e2 != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "collect live state failed")
			return
		}
		for _, ci := range cis {
			attrs := ci.Attributes
			if attrs == nil {
				attrs = map[string]any{}
			}
			targetData.CIs = append(targetData.CIs, pg.SnapshotCI{ID: ci.ID, Name: ci.Name, TypeID: ci.TypeID, Status: string(ci.Status), Attrs: attrs})
		}
		for _, rel := range rels {
			targetData.Rels = append(targetData.Rels, pg.SnapshotRel{SourceID: rel.SourceID, TargetID: rel.TargetID, RelationType: rel.RelationType})
		}
	}

	report := computeDrift(base.Data, targetData)
	report["base_name"] = base.Name
	report["target_name"] = targetName
	report["target_is_live"] = targetIsLive
	httputil.WriteJSON(w, http.StatusOK, report)
}

// computeDrift 对比两份 SnapshotData，返回新增/删除/变更 CI 与关系。
func computeDrift(base, target pg.SnapshotData) map[string]any {
	baseCIs := map[string]pg.SnapshotCI{}
	for _, c := range base.CIs {
		baseCIs[c.ID] = c
	}
	targetCIs := map[string]pg.SnapshotCI{}
	for _, c := range target.CIs {
		targetCIs[c.ID] = c
	}

	var ciAdded, ciRemoved []pg.SnapshotCI
	var ciChanged []map[string]any
	for id, tc := range targetCIs {
		if _, ok := baseCIs[id]; !ok {
			ciAdded = append(ciAdded, tc)
		}
	}
	for id, bc := range baseCIs {
		if _, ok := targetCIs[id]; !ok {
			ciRemoved = append(ciRemoved, bc)
		}
	}
	for id, tc := range targetCIs {
		bc, ok := baseCIs[id]
		if !ok {
			continue
		}
		var fields []map[string]any
		if bc.Name != tc.Name {
			fields = append(fields, map[string]any{"field": "name", "old": bc.Name, "new": tc.Name})
		}
		if bc.TypeID != tc.TypeID {
			fields = append(fields, map[string]any{"field": "type_id", "old": bc.TypeID, "new": tc.TypeID})
		}
		if bc.Status != tc.Status {
			fields = append(fields, map[string]any{"field": "status", "old": bc.Status, "new": tc.Status})
		}
		for k, v := range tc.Attrs {
			if !attrEqual(v, bc.Attrs[k]) {
				fields = append(fields, map[string]any{"field": k, "old": bc.Attrs[k], "new": v})
			}
		}
		for k := range bc.Attrs {
			if _, ok := tc.Attrs[k]; !ok {
				fields = append(fields, map[string]any{"field": k, "old": bc.Attrs[k], "new": nil})
			}
		}
		if len(fields) > 0 {
			ciChanged = append(ciChanged, map[string]any{"id": id, "name": tc.Name, "type_id": tc.TypeID, "fields": fields})
		}
	}

	relKey := func(r pg.SnapshotRel) string { return r.SourceID + ">" + r.TargetID + ":" + r.RelationType }
	baseRels := map[string]pg.SnapshotRel{}
	for _, rl := range base.Rels {
		baseRels[relKey(rl)] = rl
	}
	targetRels := map[string]pg.SnapshotRel{}
	for _, rl := range target.Rels {
		targetRels[relKey(rl)] = rl
	}
	var relAdded, relRemoved []pg.SnapshotRel
	for k, rl := range targetRels {
		if _, ok := baseRels[k]; !ok {
			relAdded = append(relAdded, rl)
		}
	}
	for k, rl := range baseRels {
		if _, ok := targetRels[k]; !ok {
			relRemoved = append(relRemoved, rl)
		}
	}

	return map[string]any{
		"ci_added":    ciAdded,
		"ci_removed":  ciRemoved,
		"ci_changed":  ciChanged,
		"rel_added":   relAdded,
		"rel_removed": relRemoved,
		"summary": map[string]int{
			"ci_added":    len(ciAdded),
			"ci_removed":  len(ciRemoved),
			"ci_changed":  len(ciChanged),
			"rel_added":   len(relAdded),
			"rel_removed": len(relRemoved),
		},
	}
}

// attrEqual 浅比较属性值（数值/字符串/嵌套 JSON 用规范化为字符串比较）。
func attrEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	as, ok1 := a.(string)
	bs, ok2 := b.(string)
	if ok1 && ok2 {
		return as == bs
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
