package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, a *Article) error
	Get(ctx context.Context, id, tenantID string) (*Article, error)
	List(ctx context.Context, tenantID, category string) ([]*Article, error)
	Update(ctx context.Context, a *Article) error
	Delete(ctx context.Context, id, tenantID string) error
}

type MemoryStore struct {
	mu       sync.RWMutex
	articles map[string]*Article
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{articles: make(map[string]*Article)}
}

func (s *MemoryStore) Create(ctx context.Context, a *Article) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = uuid.New().String()
	now := time.Now()
	a.CreatedAt = now
	a.UpdatedAt = now
	s.articles[a.ID] = a
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.articles[id]
	if !ok || a.TenantID != tenantID { return nil, nil }
	return a, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID, category string) ([]*Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Article
	for _, a := range s.articles {
		if a.TenantID != tenantID { continue }
		if category != "" && a.Category != category { continue }
		result = append(result, a)
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, a *Article) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.articles[a.ID]
	if !ok || existing.TenantID != a.TenantID { return nil }
	a.UpdatedAt = time.Now()
	s.articles[a.ID] = a
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.articles[id]
	if !ok || a.TenantID != tenantID { return nil }
	delete(s.articles, a.ID)
	return nil
}

type Service struct {
	store Store
}

func NewService() *Service {
	return &Service{store: NewMemoryStore()}
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" { return t }
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/knowledge", s.handleCreate)
	mux.HandleFunc("GET /api/v1/knowledge", s.handleList)
	mux.HandleFunc("GET /api/v1/knowledge/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/knowledge/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/knowledge/{id}", s.handleDelete)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var a Article
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a.TenantID = tenantFromCtx(r.Context())
	s.store.Create(r.Context(), &a)
	writeJSON(w, http.StatusCreated, a)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	category := r.URL.Query().Get("category")
	articles, _ := s.store.List(r.Context(), tenantID, category)
	if articles == nil { articles = []*Article{} }
	writeJSON(w, http.StatusOK, articles)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	a, _ := s.store.Get(r.Context(), id, tenantID)
	if a == nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}
	a.Views++
	s.store.Update(r.Context(), a)
	writeJSON(w, http.StatusOK, a)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, _ := s.store.Get(r.Context(), id, tenantID)
	if existing == nil { writeError(w, http.StatusNotFound, "not found"); return }
	var a Article
	json.NewDecoder(r.Body).Decode(&a)
	a.ID = existing.ID; a.TenantID = existing.TenantID; a.CreatedAt = existing.CreatedAt; a.Views = existing.Views
	s.store.Update(r.Context(), &a)
	writeJSON(w, http.StatusOK, a)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.Delete(r.Context(), id, tenantFromCtx(r.Context()))
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