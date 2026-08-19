package agentmgr

import (
	"log/slog"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/validate"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleListProxies(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	proxies, err := s.store.ListProxies(r.Context(), tenantID)
	if err != nil {
		slog.Warn("list proxies failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if proxies == nil {
		proxies = []*ProxyInfo{}
	}
	httputil.WriteJSON(w, http.StatusOK, proxies)
}

func (s *Service) handleRegisterProxy(w http.ResponseWriter, r *http.Request) {
	var req RegisterProxyRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.HostName("Proxy 名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	proxy := &ProxyInfo{
		TenantID: auth.TenantFromContext(r.Context()),
		Name:     req.Name,
		Address:  req.Address,
		Port:     req.Port,
	}
	if err := s.store.RegisterProxy(r.Context(), proxy); err != nil {
		slog.Warn("register proxy failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, proxy)
}

func (s *Service) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	proxy, err := s.store.GetProxy(r.Context(), id, tenantID)
	if err != nil || proxy == nil {
		httputil.WriteError(w, http.StatusNotFound, "proxy not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, proxy)
}

func (s *Service) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	proxy, err := s.store.GetProxy(r.Context(), id, tenantID)
	if err != nil || proxy == nil {
		httputil.WriteError(w, http.StatusNotFound, "proxy not found")
		return
	}
	var req UpdateProxyRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != "" {
		proxy.Name = req.Name
	}
	if req.Address != "" {
		proxy.Address = req.Address
	}
	if req.Port != 0 {
		proxy.Port = req.Port
	}
	if req.Status != "" {
		proxy.Status = req.Status
	}
	if err := s.store.UpdateProxy(r.Context(), proxy); err != nil {
		slog.Warn("update proxy failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, proxy)
}

func (s *Service) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeleteProxy(r.Context(), id, tenantID); err != nil {
		slog.Warn("delete proxy failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
