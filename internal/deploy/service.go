package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

func NewService(db *sql.DB) *Service { return &Service{store: NewStore(db)} }

type Service struct{ store *Store }

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// Apps
	mux.HandleFunc("GET /api/v1/deploy/apps", s.handleListApps)
	mux.HandleFunc("POST /api/v1/deploy/apps", s.handleCreateApp)
	mux.HandleFunc("PUT /api/v1/deploy/apps/{id}", s.handleUpdateApp)
	mux.HandleFunc("DELETE /api/v1/deploy/apps/{id}", s.handleDeleteApp)
	// Releases
	mux.HandleFunc("GET /api/v1/deploy/releases", s.handleListReleases)
	mux.HandleFunc("POST /api/v1/deploy/releases", s.handleCreateRelease)
	mux.HandleFunc("PUT /api/v1/deploy/releases/{id}", s.handleUpdateRelease)
	mux.HandleFunc("DELETE /api/v1/deploy/releases/{id}", s.handleDeleteRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/approve", s.handleApproveRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/reject", s.handleRejectRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/deploy", s.handleDeployRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/rollback", s.handleRollbackRelease)
	// Environments
	mux.HandleFunc("GET /api/v1/deploy/environments", s.handleListEnvs)
	mux.HandleFunc("POST /api/v1/deploy/environments", s.handleCreateEnv)
	mux.HandleFunc("PUT /api/v1/deploy/environments/{id}", s.handleUpdateEnv)
	mux.HandleFunc("DELETE /api/v1/deploy/environments/{id}", s.handleDeleteEnv)
	// History
	mux.HandleFunc("GET /api/v1/deploy/history", s.handleListHistory)
}

func (s *Service) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, _ := s.store.ListApps(r.Context(), tenant(r))
	writeJSON(w, 200, apps)
}
func (s *Service) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var a DeployApp; json.NewDecoder(r.Body).Decode(&a); a.TenantID = tenant(r)
	s.store.CreateApp(r.Context(), &a); writeJSON(w, 201, a)
}
func (s *Service) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id"); var a DeployApp; json.NewDecoder(r.Body).Decode(&a)
	a.ID = id; a.TenantID = tenant(r)
	s.store.UpdateApp(r.Context(), &a); writeJSON(w, 200, a)
}
func (s *Service) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id"); s.store.DeleteApp(r.Context(), id, tenant(r))
	writeJSON(w, 200, map[string]string{"ok": "deleted"})
}

func (s *Service) handleListReleases(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.store.ListReleases(r.Context(), tenant(r))
	writeJSON(w, 200, rel)
}
func (s *Service) handleCreateRelease(w http.ResponseWriter, r *http.Request) {
	var r2 ReleaseTicket; json.NewDecoder(r.Body).Decode(&r2); r2.TenantID = tenant(r)
	if r2.AppName == "" {
		var a DeployApp
		apps, _ := s.store.ListApps(r.Context(), tenant(r))
		for _, app := range apps { if app.ID == r2.AppID { a = app; break } }
		r2.AppName = a.Name; r2.AppCode = a.Code
	}
	s.store.CreateRelease(r.Context(), &r2)
	writeJSON(w, 201, r2)
}
func (s *Service) handleUpdateRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id"); var r2 ReleaseTicket; json.NewDecoder(r.Body).Decode(&r2)
	r2.ID = id; r2.TenantID = tenant(r)
	s.store.UpdateRelease(r.Context(), &r2); writeJSON(w, 200, r2)
}
func (s *Service) handleDeleteRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id"); s.store.DeleteRelease(r.Context(), id, tenant(r))
	writeJSON(w, 200, map[string]string{"ok": "deleted"})
}

func (s *Service) handleApproveRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rels, _ := s.store.ListReleases(r.Context(), tenant(r))
	for i := range rels { if rels[i].ID == id {
		rels[i].Status = "approved"
		now := time.Now(); rels[i].ApprovedAt = &now
		s.store.UpdateRelease(r.Context(), &rels[i])
		writeJSON(w, 200, &rels[i]); return
	}}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}
func (s *Service) handleRejectRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct { Reason string `json:"reason"` }
	json.NewDecoder(r.Body).Decode(&body)
	rels, _ := s.store.ListReleases(r.Context(), tenant(r))
	for i := range rels { if rels[i].ID == id {
		rels[i].Status = "rejected"; rels[i].RejectReason = body.Reason
		s.store.UpdateRelease(r.Context(), &rels[i])
		writeJSON(w, 200, &rels[i]); return
	}}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}
func (s *Service) handleDeployRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rels, _ := s.store.ListReleases(r.Context(), tenant(r))
	for i := range rels { if rels[i].ID == id {
		rels[i].Status = "deploying"
		now := time.Now(); rels[i].DeployedAt = &now
		s.store.UpdateRelease(r.Context(), &rels[i])
		// 模拟部署步骤
		go s.simulateDeploy(&rels[i])
		writeJSON(w, 200, &rels[i]); return
	}}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}
func (s *Service) handleRollbackRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rels, _ := s.store.ListReleases(r.Context(), tenant(r))
	for i := range rels { if rels[i].ID == id {
		rels[i].Status = "rolled_back"
		s.store.UpdateRelease(r.Context(), &rels[i])
		s.historyFor(&rels[i], "rolled_back", "manual rollback")
		writeJSON(w, 200, &rels[i]); return
	}}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func (s *Service) simulateDeploy(rel *ReleaseTicket) {
	steps := []string{"build", "test", "deploy", "health_check"}
	for i, name := range steps {
		rel.Steps = append(rel.Steps, DeployStep{ID: name, Name: name, Status: "running"})
		s.store.UpdateRelease(context.Background(), rel)
		time.Sleep(2 * time.Second)
		rel.Steps[i].Status = "succeeded"
		s.store.UpdateRelease(context.Background(), rel)
	}
	rel.Status = "succeeded"
	s.store.UpdateRelease(context.Background(), rel)
	s.historyFor(rel, "succeeded", "")
	slog.Info("deploy completed", "release", rel.ID, "app", rel.AppName)
}

func (s *Service) historyFor(rel *ReleaseTicket, status, output string) {
	s.store.CreateHistory(context.Background(), &ReleaseHistory{
		ReleaseID: rel.ID, AppID: rel.AppID, AppName: rel.AppName,
		Env: rel.Env, Version: rel.Version, Status: status,
		Operator: rel.Operator, Output: output, TenantID: rel.TenantID,
	})
}

func (s *Service) handleListEnvs(w http.ResponseWriter, r *http.Request) {
	envs, _ := s.store.ListEnvs(r.Context(), tenant(r))
	if len(envs) == 0 {
		// seed default envs
		for _, t := range []DeployEnv{"dev", "test", "staging", "prod"} {
			e := DeployEnvironment{Name: t, Type: t, Status: "active", TenantID: tenant(r)}
			s.store.CreateEnv(r.Context(), &e)
			envs = append(envs, e)
		}
	}
	writeJSON(w, 200, envs)
}
func (s *Service) handleCreateEnv(w http.ResponseWriter, r *http.Request) {
	var e DeployEnvironment; json.NewDecoder(r.Body).Decode(&e); e.TenantID = tenant(r)
	s.store.CreateEnv(r.Context(), &e); writeJSON(w, 201, e)
}
func (s *Service) handleUpdateEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id"); var e DeployEnvironment; json.NewDecoder(r.Body).Decode(&e)
	e.ID = id; e.TenantID = tenant(r)
	s.store.UpdateEnv(r.Context(), &e); writeJSON(w, 200, e)
}
func (s *Service) handleDeleteEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id"); s.store.DeleteEnv(r.Context(), id, tenant(r))
	writeJSON(w, 200, map[string]string{"ok": "deleted"})
}

func (s *Service) handleListHistory(w http.ResponseWriter, r *http.Request) {
	h, _ := s.store.ListHistory(r.Context(), tenant(r))
	writeJSON(w, 200, h)
}

func tenant(r *http.Request) string {
	t := r.Header.Get("X-Tenant-ID")
	if t == "" { t = "default" }
	return t
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
