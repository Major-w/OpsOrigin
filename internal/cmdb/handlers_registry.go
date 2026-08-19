package cmdb

import (
	"net/http"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/registry"

	"github.com/opsorigin/core/pkg/httputil"
)

// handleRegistryResolve 把 AI 特征解析为已有 leaf / 挂载 abstract 父类 / 新提案。
func (s *Service) handleRegistryResolve(w http.ResponseWriter, r *http.Request) {
	if s.typeRegistry == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "类型注册表未启用")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		Features map[string]any `json:"features"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if len(req.Features) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "features 不能为空")
		return
	}

	res, err := s.typeRegistry.ResolveType(ctx, tenantID, req.Features)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "类型解析失败: "+err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, res)
}

// handleRegistryPropose 提交新类型提案（相似度去重 + 审核门控）。
func (s *Service) handleRegistryPropose(w http.ResponseWriter, r *http.Request) {
	if s.typeRegistry == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "类型注册表未启用")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var prop registry.AITypeProposal
	if err := httputil.BindJSON(r, &prop); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	// 服务端托管的字段：租户来自上下文，状态/ID/时间戳由注册表内部生成。
	prop.TenantID = tenantID
	prop.Status = ""
	prop.ID = ""
	prop.CreatedAt = time.Time{}
	prop.UpdatedAt = time.Time{}

	id, status, err := s.typeRegistry.ProposeType(ctx, prop)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "提案提交失败: "+err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
}

// handleRegistryPromote 证据累积后晋升某提案类型（提升置信度，达阈值即视为激活）。
func (s *Service) handleRegistryPromote(w http.ResponseWriter, r *http.Request) {
	if s.typeRegistry == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "类型注册表未启用")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		ID       string   `json:"id"`
		Evidence []string `json:"evidence"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "id 不能为空")
		return
	}

	if err := s.typeRegistry.PromoteType(ctx, tenantID, req.ID, req.Evidence); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "晋升失败: "+err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": req.ID, "promoted": true})
}
