package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, t *ScheduledTask) error
	Get(ctx context.Context, id, tenantID string) (*ScheduledTask, error)
	List(ctx context.Context, tenantID string) ([]*ScheduledTask, error)
	Update(ctx context.Context, t *ScheduledTask) error
	Delete(ctx context.Context, id, tenantID string) error
}

type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*ScheduledTask
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tasks: make(map[string]*ScheduledTask)}
}

func (s *MemoryStore) Create(ctx context.Context, t *ScheduledTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = uuid.New().String()
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	s.tasks[t.ID] = t
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok || t.TenantID != tenantID {
		return nil, nil
	}
	return t, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string) ([]*ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ScheduledTask
	for _, t := range s.tasks {
		if t.TenantID == tenantID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, t *ScheduledTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[t.ID]
	if !ok || existing.TenantID != t.TenantID {
		return nil
	}
	t.UpdatedAt = time.Now()
	s.tasks[t.ID] = t
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.TenantID != tenantID {
		return nil
	}
	delete(s.tasks, t.ID)
	return nil
}

type Service struct {
	store Store
}

func NewService() *Service {
	return &Service{store: NewMemoryStore()}
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/schedules", s.handleCreate)
	mux.HandleFunc("GET /api/v1/schedules", s.handleList)
	mux.HandleFunc("GET /api/v1/schedules/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/schedules/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/schedules/{id}", s.handleDelete)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var task ScheduledTask
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task.TenantID = tenantFromCtx(r.Context())
	task.Enabled = true
	if err := s.store.Create(r.Context(), &task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tasks, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*ScheduledTask{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	task, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || task == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	var task ScheduledTask
	json.NewDecoder(r.Body).Decode(&task)
	task.ID = existing.ID
	task.TenantID = existing.TenantID
	task.CreatedAt = existing.CreatedAt
	s.store.Update(r.Context(), &task)
	writeJSON(w, http.StatusOK, task)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	s.store.Delete(r.Context(), id, tenantID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}