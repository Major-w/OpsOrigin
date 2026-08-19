package cmdb

import (
	"context"
	"fmt"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"log/slog"
	"net/http"
	"strings"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/pkg/principal"
)

// ============================================================
// P2-6 行级数据权限(we-cmdb 细粒度数据权限借鉴)
// 角色对某模型类型的 CI, 仅可访问/操作满足属性条件的行。
// 读取过滤 + 写入拦截, 与租户 RLS / 字段规则(FieldRule) 平行。
// ============================================================

// roleFromContext 取主体角色(principal.Role; 无主体时空串=不应用角色策略)。
func roleFromContext(ctx context.Context) string {
	pr, ok := principal.FromContext(ctx)
	if !ok {
		return ""
	}
	return pr.Role
}

// matchPolicyCondition 判断 CI 是否满足全部条件(AND)。
func matchPolicyCondition(ci *model.CIModel, cond []model.PolicyCondition) bool {
	for _, c := range cond {
		v, ok := ci.Attributes[c.Attr]
		if !ok {
			return false
		}
		s := fmt.Sprint(v)
		switch c.Op {
		case "eq":
			if s != c.Value {
				return false
			}
		case "in":
			if !containsStr(strings.Split(c.Value, ","), s) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// filterByDataPolicy 按主体的角色策略过滤 CI 列表(查询/搜索后调用)。
// 返回过滤后的列表; 无匹配策略时不拦截(策略只收窄可见范围)。
func filterByDataPolicy(cis []*model.CIModel, policies []*model.DataPolicy, role string) []*model.CIModel {
	if role == "" || len(policies) == 0 || len(cis) == 0 {
		return cis
	}
	// 该角色对 (model_type_id) 的 query 策略(多个 AND 语义? 取全部, 满足任一即可见)
	type pol struct {
		cond []model.PolicyCondition
	}
	byType := map[string][]pol{}
	for _, p := range policies {
		if p.Role == role && (p.Action == "" || p.Action == "query") {
			byType[p.ModelTypeID] = append(byType[p.ModelTypeID], pol{cond: p.Condition})
		}
	}
	if len(byType) == 0 {
		return cis
	}
	out := make([]*model.CIModel, 0, len(cis))
	for _, ci := range cis {
		policiesFor := byType[ci.TypeID]
		if len(policiesFor) == 0 {
			out = append(out, ci) // 该类型无策略 → 不拦截
			continue
		}
		visible := false
		for _, p := range policiesFor {
			if matchPolicyCondition(ci, p.cond) {
				visible = true
				break
			}
		}
		if visible {
			out = append(out, ci)
		}
	}
	return out
}

// checkDataPolicyWrite 写操作拦截: 主体角色对目标 CI 类型的 update/delete 策略,
// 若目标 CI 不满足条件则拒绝。
func (s *Service) checkDataPolicyWrite(ctx context.Context, tenantID, ciTypeID string, ci *model.CIModel, action string) error {
	role := roleFromContext(ctx)
	if role == "" || s.pg == nil {
		return nil
	}
	policies, err := s.pg.ListDataPolicies(ctx, tenantID, role, ciTypeID)
	if err != nil {
		return nil // 策略加载失败不阻断(安全网由 RLS 兜底)
	}
	var matched *model.DataPolicy
	for _, p := range policies {
		if p.Action == action {
			matched = p
			break
		}
	}
	if matched == nil {
		return nil // 该角色无此动作策略 → 不额外拦截
	}
	if !matchPolicyCondition(ci, matched.Condition) {
		return fmt.Errorf("data policy: role %q 无权对不满足条件的 CI 执行 %s(条件 %v)", role, action, matched.Condition)
	}
	return nil
}

// loadDataPoliciesFor 加载租户全部策略(供查询过滤)。
func (s *Service) loadDataPolicies(ctx context.Context, tenantID string) []*model.DataPolicy {
	if s.pg == nil {
		return nil
	}
	policies, err := s.pg.ListDataPolicies(ctx, tenantID, "", "")
	if err != nil {
		slog.Warn("load data policies failed", "error", err)
		return nil
	}
	return policies
}

// handleListDataPolicies 数据权限规则列表。
func (s *Service) handleListDataPolicies(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, http.StatusOK, []*model.DataPolicy{})
		return
	}
	policies, err := s.pg.ListDataPolicies(r.Context(), tenantID, r.URL.Query().Get("role"), r.URL.Query().Get("model_type_id"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if policies == nil {
		policies = []*model.DataPolicy{}
	}
	httputil.WriteJSON(w, http.StatusOK, policies)
}

// handleSaveDataPolicy 新建/更新(按 role+model+action upsert)。
func (s *Service) handleSaveDataPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var p model.DataPolicy
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if p.Role == "" || p.ModelTypeID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "role and model_type_id are required")
		return
	}
	if p.Action == "" {
		p.Action = "query"
	}
	p.TenantID = tenantID
	if err := s.pg.SaveDataPolicy(r.Context(), &p); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, p)
}

// handleDeleteDataPolicy 删除规则。
func (s *Service) handleDeleteDataPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.DeleteDataPolicy(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
