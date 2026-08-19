package cmdb

import (
	"context"
	"fmt"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// ============================================================
// 实体调解合并（IRE: 多源数据打架的正解）
// 云 API 的 i-xxx、发现的 Web-01、手工 Web-Prd-01 常指同一实体,
// 识别疑似重复 → 按渠道置信度(AttributeMeta.Confidence)合并 → 保留高置信来源值。
// ============================================================

// ReconcileCandidate 一组疑似同一实体的 CI。
type ReconcileCandidate struct {
	Key     string                 `json:"key"`     // 分组键(ip 或 name)
	Reason  string                 `json:"reason"`  // 重复 IP / 重复名称
	Members []ReconcileMember      `json:"members"` // 组内成员(按置信度降序)
	Attrs   map[string]interface{} `json:"-"`       // 内部: 各组 IP 快照(避免重复解析)
}

// ReconcileMember 候选组中的一个 CI。
type ReconcileMember struct {
	CIID       string                    `json:"ci_id"`
	Name       string                    `json:"name"`
	TypeID     string                    `json:"type_id"`
	Source     model.SourceKind          `json:"source"`
	Confidence float64                   `json:"confidence"`
	UpdatedAt  string                    `json:"updated_at"`
	Attrs      map[string]any            `json:"-"`
	Meta       map[string]model.AttrMeta `json:"-"`
}

// ciIP 从 CI 属性中提取首个 IP(兼容多键)。
func ciIP(attrs map[string]any) string {
	for _, k := range []string{"ip", "ip_address", "management_ip", "private_ip"} {
		if v, ok := attrs[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// avgConfidence 计算 CI 的平均属性置信度(用于候选排序)。
func avgConfidence(meta map[string]model.AttrMeta) float64 {
	if len(meta) == 0 {
		return 0
	}
	var sum float64
	for _, m := range meta {
		sum += m.Confidence
	}
	return sum / float64(len(meta))
}

// groupCandidates 对 CI 列表执行疑似重复分组(纯函数, 便于单测):
// 同 IP(非空)或同类型同名称的 CI 归为一组, 组内按平均置信度降序。
func groupCandidates(cis []*model.CIModel) []ReconcileCandidate {
	type member struct {
		ci   *model.CIModel
		ip   string
		conf float64
	}
	byIP := map[string][]member{}
	byNameType := map[string][]member{}
	for _, ci := range cis {
		m := member{ci: ci, ip: ciIP(ci.Attributes), conf: avgConfidence(ci.AttributeMeta)}
		if m.ip != "" {
			byIP[m.ip] = append(byIP[m.ip], m)
		}
		key := ci.TypeID + "|" + ci.Name
		byNameType[key] = append(byNameType[key], m)
	}

	var out []ReconcileCandidate
	seen := map[string]bool{} // key 去重(同组同时命中 IP 与 name 只报一次)

	addGroup := func(key, reason string, members []member) {
		if len(members) < 2 || seen[key] {
			return
		}
		seen[key] = true
		sort.Slice(members, func(i, j int) bool { return members[i].conf > members[j].conf })
		can := ReconcileCandidate{Key: key, Reason: reason}
		for _, m := range members {
			can.Members = append(can.Members, ReconcileMember{
				CIID: m.ci.ID, Name: m.ci.Name, TypeID: m.ci.TypeID,
				Source: sourceOf(m.ci), Confidence: m.conf,
				UpdatedAt: m.ci.UpdatedAt.Format("2006-01-02 15:04"),
				Attrs:     m.ci.Attributes, Meta: m.ci.AttributeMeta,
			})
		}
		out = append(out, can)
	}

	for ip, ms := range byIP {
		addGroup("ip:"+ip, "重复 IP "+ip, ms)
	}
	for nk, ms := range byNameType {
		addGroup("name:"+nk, "重复名称(同类型)", ms)
	}
	return out
}

// findReconcileCandidates 扫描租户全部 CI 并分组疑似重复(服务层入口)。
func (s *Service) findReconcileCandidates(ctx context.Context, tenantID string) ([]ReconcileCandidate, error) {
	if s.pg == nil {
		return nil, nil
	}
	cis, err := s.pg.ListCIPaginated(ctx, tenantID, "", "", "", 10000, 0)
	if err != nil {
		return nil, err
	}
	return groupCandidates(cis), nil
}

// sourceOf 从 CI 元数据推导来源(最高置信字段的来源, 兜底 channel)。
func sourceOf(ci *model.CIModel) model.SourceKind {
	var best model.SourceKind
	var bestConf float64
	for _, m := range ci.AttributeMeta {
		if m.Confidence > bestConf {
			best = m.Source
			bestConf = m.Confidence
		}
	}
	if best == "" {
		return model.SourceFromChannelWeight(ci.Channel)
	}
	return best
}

// mergeReconcile 合并疑似同实体: 以 keep 为主体, 逐字段按置信度取高者;
// keep 缺失字段从 merge CI 补齐; 合并后删除被合并 CI, 写审计。
// 返回本次合并写入的字段清单。
func (s *Service) mergeReconcile(ctx context.Context, tenantID, keepID string, mergeIDs []string) ([]string, error) {
	if s.pg == nil {
		return nil, fmt.Errorf("reconcile merge requires pg backend")
	}
	keep, err := s.pg.GetCI(ctx, keepID, tenantID)
	if err != nil || keep == nil {
		return nil, fmt.Errorf("keep ci %s not found", keepID)
	}
	if keep.Attributes == nil {
		keep.Attributes = map[string]any{}
	}
	if keep.AttributeMeta == nil {
		keep.AttributeMeta = map[string]model.AttrMeta{}
	}

	var updated []string
	for _, mid := range mergeIDs {
		if mid == keepID {
			continue
		}
		merge, err := s.pg.GetCI(ctx, mid, tenantID)
		if err != nil || merge == nil {
			continue
		}
		// 逐字段: merge 置信度 > keep 置信度 → 采纳 merge 值; keep 缺失 → 补齐。
		for k, v := range merge.Attributes {
			keepMeta, keepHas := keep.AttributeMeta[k]
			mergeMeta, mergeHas := merge.AttributeMeta[k]
			if !keepHas || (mergeHas && mergeMeta.Confidence > keepMeta.Confidence) {
				if keep.Attributes[k] == nil && v == nil {
					continue
				}
				keep.Attributes[k] = v
				if mergeHas {
					keep.AttributeMeta[k] = mergeMeta
				}
				updated = append(updated, k)
			}
		}
		// 删除被合并 CI(含订阅事件)。
		before := merge
		if err := s.pg.DeleteCI(ctx, mid, tenantID, "reconcile-merge"); err != nil {
			slog.Warn("reconcile: delete merged ci failed", "id", mid, "error", err)
			continue
		}
		s.dispatchCIEvent(ctx, tenantID, "ci_deleted", before)
	}

	if len(updated) > 0 {
		if err := s.pg.UpdateCI(ctx, keep, model.WeightHuman, "reconcile-merge"); err != nil {
			return nil, err
		}
		s.dispatchCIEvent(ctx, tenantID, "ci_updated", keep)
	}
	return updated, nil
}

// handleReconcileCandidates 列出疑似重复组。
func (s *Service) handleReconcileCandidates(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	cands, err := s.findReconcileCandidates(r.Context(), tenantID)
	if err != nil {
		slog.Error("reconcile candidates failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cands == nil {
		cands = []ReconcileCandidate{}
	}
	httputil.WriteJSON(w, http.StatusOK, cands)
}

// handleReconcileMerge 合并一组疑似重复 CI。
func (s *Service) handleReconcileMerge(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		KeepID   string   `json:"keep_id"`
		MergeIDs []string `json:"merge_ids"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.KeepID == "" || len(req.MergeIDs) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "keep_id and merge_ids are required")
		return
	}
	updated, err := s.mergeReconcile(r.Context(), tenantID, req.KeepID, req.MergeIDs)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"kept_id": req.KeepID, "merged_count": len(req.MergeIDs), "updated_fields": updated,
	})
}

// trim 去掉首尾空白(JSON 里 source 兼容展示)。
func trim(s string) string { return strings.TrimSpace(s) }
