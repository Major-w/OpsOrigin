package cmdb

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleListHistory(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	changeType := r.URL.Query().Get("change_type")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}

	versions, total, err := s.pg.ListAllCIHistory(r.Context(), tenantID, changeType, limit, offset)
	if err != nil {
		slog.Error("list history failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, map[string]any{"items": versions, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleListCIVersions(w http.ResponseWriter, r *http.Request) {
	ciID := r.PathValue("ciId")
	tenantID := auth.TenantFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}

	versions, total, err := s.pg.ListCIVersions(r.Context(), ciID, tenantID, limit, offset)
	if err != nil {
		slog.Error("list ci versions failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, map[string]any{"items": versions, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleGetVersionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, err := s.pg.GetCIVersion(r.Context(), id)
	if err != nil {
		slog.Error("get version detail failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if version == nil {
		httputil.WriteError(w, 404, "version not found")
		return
	}
	httputil.WriteJSON(w, 200, version)
}
