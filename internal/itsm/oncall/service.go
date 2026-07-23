package oncall

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	AddShift(ctx context.Context, s *Shift) error
	ListShifts(ctx context.Context, tenantID string) ([]*Shift, error)
	AddRotation(ctx context.Context, r *Rotation) error
	ListRotations(ctx context.Context, tenantID string) ([]*Rotation, error)
	DeleteRotation(ctx context.Context, id, tenantID string) error
}

type MemoryStore struct {
	mu        sync.RWMutex
	shifts    map[string]*Shift
	rotations map[string]*Rotation
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		shifts:    make(map[string]*Shift),
		rotations: make(map[string]*Rotation),
	}
}

func (s *MemoryStore) AddShift(ctx context.Context, shift *Shift) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	shift.ID = uuid.New().String()
	shift.CreatedAt = time.Now()
	s.shifts[shift.ID] = shift
	return nil
}

func (s *MemoryStore) ListShifts(ctx context.Context, tenantID string) ([]*Shift, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Shift
	for _, sh := range s.shifts {
		if sh.TenantID == tenantID {
			result = append(result, sh)
		}
	}
	return result, nil
}

func (s *MemoryStore) AddRotation(ctx context.Context, r *Rotation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = uuid.New().String()
	now := time.Now()
	r.CreatedAt = now
	r.UpdatedAt = now
	s.rotations[r.ID] = r
	return nil
}

func (s *MemoryStore) ListRotations(ctx context.Context, tenantID string) ([]*Rotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Rotation
	for _, r := range s.rotations {
		if r.TenantID == tenantID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s *MemoryStore) DeleteRotation(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rotations[id]
	if !ok || r.TenantID != tenantID {
		return nil
	}
	delete(s.rotations, id)
	return nil
}

type Service struct {
	store Store
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil { st = newPGStore(db) }
	return &Service{store: st}
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/oncall", s.handleListShifts)
	mux.HandleFunc("POST /api/v1/oncall", s.handleAddShift)
	mux.HandleFunc("GET /api/v1/oncall/rotations", s.handleListRotations)
	mux.HandleFunc("POST /api/v1/oncall/rotations", s.handleCreateRotation)
	mux.HandleFunc("DELETE /api/v1/oncall/rotations/{id}", s.handleDeleteRotation)
}

func (s *Service) handleListShifts(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	shifts, _ := s.store.ListShifts(r.Context(), tenantID)
	if shifts == nil {
		shifts = []*Shift{}
	}
	writeJSON(w, http.StatusOK, shifts)
}

func (s *Service) handleAddShift(w http.ResponseWriter, r *http.Request) {
	var shift Shift
	json.NewDecoder(r.Body).Decode(&shift)
	shift.TenantID = tenantFromCtx(r.Context())
	s.store.AddShift(r.Context(), &shift)
	writeJSON(w, http.StatusCreated, shift)
}

func (s *Service) handleListRotations(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	rotations, _ := s.store.ListRotations(r.Context(), tenantID)
	if rotations == nil {
		rotations = []*Rotation{}
	}
	writeJSON(w, http.StatusOK, rotations)
}

func (s *Service) handleCreateRotation(w http.ResponseWriter, r *http.Request) {
	var rot Rotation
	json.NewDecoder(r.Body).Decode(&rot)
	rot.TenantID = tenantFromCtx(r.Context())
	s.store.AddRotation(r.Context(), &rot)
	writeJSON(w, http.StatusCreated, rot)
}

func (s *Service) handleDeleteRotation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.DeleteRotation(r.Context(), id, tenantFromCtx(r.Context()))
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