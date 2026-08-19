package cmdb

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// handleArchiveCI 归档一条 CI：状态置为 archived，不物理删除，可随时恢复。
func (s *Service) handleArchiveCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	if s.pg == nil {
		if !s.mem.setCIStatus(tenantID, id, model.CIStatusArchived) {
			httputil.WriteError(w, 404, "ci not found")
			return
		}
		httputil.WriteJSON(w, 200, map[string]string{"status": "archived"})
		return
	}
	if err := s.pg.UpdateCIStatus(r.Context(), id, tenantID, model.CIStatusArchived, "archive"); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// P2-5 订阅分发：归档事件
	if ci, err := s.pg.GetCI(r.Context(), id, tenantID); err == nil && ci != nil {
		s.dispatchCIEvent(r.Context(), tenantID, "ci_archived", ci)
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "archived"})
}

// handleRestoreCI 恢复一条已归档 CI：状态置回 active。
func (s *Service) handleRestoreCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	if s.pg == nil {
		if !s.mem.setCIStatus(tenantID, id, model.CIStatusActive) {
			httputil.WriteError(w, 404, "ci not found")
			return
		}
		httputil.WriteJSON(w, 200, map[string]string{"status": "restored"})
		return
	}
	if err := s.pg.UpdateCIStatus(r.Context(), id, tenantID, model.CIStatusActive, "restore"); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "restored"})
}

// ============================================================
// 内存回退模式下的辅助函数（PG 不可用时 Service.mem 使用）
// ============================================================

func filterByStatus(cis []*model.CIModel, status model.CIStatus) []*model.CIModel {
	out := make([]*model.CIModel, 0, len(cis))
	for _, c := range cis {
		if c.Status == status {
			out = append(out, c)
		}
	}
	return out
}

func filterExcludeStatus(cis []*model.CIModel, status model.CIStatus) []*model.CIModel {
	out := make([]*model.CIModel, 0, len(cis))
	for _, c := range cis {
		if c.Status != status {
			out = append(out, c)
		}
	}
	return out
}
