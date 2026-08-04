package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/opsorigin/core/pkg/httputil"
)

func NewService(db *sql.DB) *Service {
	return &Service{store: NewPGStore(db)}
}

// ScriptExecutor 执行脚本(与 automation/workflow 的接口签名一致, 由 automation/script.Service 实现,
// 避免 import 环)。StepService 步骤携带 ScriptID 时, 流程引擎调用它执行自动化脚本。
type ScriptExecutor interface {
	Execute(scriptID string, params map[string]string, timeout int, runAs string, agentID string, tenantID string) (output string, exitCode int, err error)
}

type Service struct {
	store Store
	Name  string
	// scriptExec 由 cmd/core 注入: StepService 步骤执行脚本(nil=跳过脚本, 服务步骤按 success 直通)。
	scriptExec ScriptExecutor
}

// SetScriptExecutor 注入脚本执行器, 使流程的 service 步骤真正驱动自动化脚本执行。
func (s *Service) SetScriptExecutor(e ScriptExecutor) {
	s.scriptExec = e
}

func (s *Service) Get(ctx context.Context, id, tenantID string) (*Process, error) {
	return s.store.Get(ctx, id, tenantID)
}

func (s *Service) List(ctx context.Context, tenantID string) ([]*Process, error) {
	return s.store.List(ctx, tenantID)
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/processes", s.handleList)
	mux.HandleFunc("POST /api/v1/itsm/processes", s.handleCreate)
	mux.HandleFunc("GET /api/v1/itsm/processes/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/itsm/processes/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/itsm/processes/{id}", s.handleDelete)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	processes, err := s.store.List(r.Context(), tenant)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, processes)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var p Process
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if p.Name == "" {
		writeError(w, 400, "name required")
		return
	}
	p.TenantID = r.Header.Get("X-Tenant-ID")
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if err := s.store.Create(context.Background(), &p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process created", "id", p.ID, "name", p.Name)
	writeJSON(w, 201, p)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	p, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if p == nil {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, p)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	existing, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if existing == nil {
		writeError(w, 404, "not found")
		return
	}
	var p Process
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	p.ID = id
	p.TenantID = tenant
	p.CreatedAt = existing.CreatedAt
	if err := s.store.Update(r.Context(), &p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process updated", "id", id)
	writeJSON(w, 200, p)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	if err := s.store.Delete(r.Context(), id, tenant); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
