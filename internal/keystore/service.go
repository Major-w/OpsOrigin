package keystore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, key *KeyEntry) error
	Get(ctx context.Context, id, tenantID string) (*KeyEntry, error)
	List(ctx context.Context, tenantID string, keyType KeyType) ([]*KeyEntry, error)
	Update(ctx context.Context, key *KeyEntry) error
	Delete(ctx context.Context, id, tenantID string) error
	BatchDelete(ctx context.Context, tenantID string, ids []string) error
}

type MemoryStore struct {
	mu   sync.RWMutex
	keys map[string]*KeyEntry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{keys: make(map[string]*KeyEntry)}
}

func (s *MemoryStore) Create(ctx context.Context, key *KeyEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key.ID = uuid.New().String()
	key.CreatedAt = time.Now()
	key.UpdatedAt = time.Now()
	s.keys[key.ID] = key
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*KeyEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[id]
	if !ok || k.TenantID != tenantID {
		return nil, nil
	}
	return k, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string, keyType KeyType) ([]*KeyEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*KeyEntry
	for _, k := range s.keys {
		if k.TenantID != tenantID {
			continue
		}
		if keyType != "" && k.Type != keyType {
			continue
		}
		result = append(result, k)
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, key *KeyEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.keys[key.ID]
	if !ok || existing.TenantID != key.TenantID {
		return nil
	}
	key.UpdatedAt = time.Now()
	s.keys[key.ID] = key
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok || k.TenantID != tenantID {
		return nil
	}
	delete(s.keys, k.ID)
	return nil
}

func (s *MemoryStore) BatchDelete(ctx context.Context, tenantID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		k, ok := s.keys[id]
		if !ok || k.TenantID != tenantID {
			continue
		}
		delete(s.keys, id)
	}
	return nil
}

func computeFingerprint(content string) string {
	h := sha256.Sum256([]byte(content))
	return "SHA256:" + hex.EncodeToString(h[:])[:16]
}

func estimateSize(content string) int {
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return len(content)
	}
	return len(decoded)
}

type Service struct {
	store Store
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db)
	}
	return &Service{store: st}
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/keys", s.handleCreate)
	mux.HandleFunc("GET /api/v1/keys", s.handleList)
	mux.HandleFunc("GET /api/v1/keys/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/keys/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/keys/{id}", s.handleDelete)
	mux.HandleFunc("DELETE /api/v1/keys", s.handleBatchDelete)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var key KeyEntry
	if err := json.NewDecoder(r.Body).Decode(&key); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	key.TenantID = tenantFromCtx(r.Context())
	
	if key.Type == KeyTypeSSHPrivate || key.Type == KeyTypeSSHPublic {
		key.Fingerprint = computeFingerprint(key.Content)
		key.Size = estimateSize(key.Content)
	} else {
		key.Fingerprint = ""
		key.Size = len(key.Content)
	}
	
	if key.Type == "" {
		key.Type = KeyTypeSSHPrivate
	}
	if key.Name == "" {
		if key.Type == KeyTypeSSHPrivate || key.Type == KeyTypeSSHPublic {
			key.Name = "key-" + key.Fingerprint
		} else {
			key.Name = string(key.Type) + "-" + uuid.New().String()[:8]
		}
	}
	if err := s.store.Create(r.Context(), &key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, key)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	keyType := KeyType(r.URL.Query().Get("type"))
	keys, err := s.store.List(r.Context(), tenantID, keyType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []*KeyEntry{}
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	key, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || key == nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	writeJSON(w, http.StatusOK, key)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	key, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || key == nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	var req struct {
		Name     string  `json:"name,omitempty"`
		Type     KeyType `json:"type,omitempty"`
		Content  string  `json:"content,omitempty"`
		Username string  `json:"username,omitempty"`
		Host     string  `json:"host,omitempty"`
		Port     string  `json:"port,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != "" { key.Name = req.Name }
	if req.Type != "" { key.Type = req.Type }
	if req.Username != "" { key.Username = req.Username }
	if req.Host != "" { key.Host = req.Host }
	if req.Port != "" { key.Port = req.Port }
	if req.Content != "" {
		key.Content = req.Content
		if key.Type == KeyTypeSSHPrivate || key.Type == KeyTypeSSHPublic {
			key.Fingerprint = computeFingerprint(key.Content)
			key.Size = estimateSize(key.Content)
		} else {
			key.Fingerprint = ""
			key.Size = len(key.Content)
		}
	}
	if err := s.store.Update(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, key)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	s.store.Delete(r.Context(), id, tenantID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleBatchDelete(w http.ResponseWriter, r *http.Request) {
	var req struct { IDs []string `json:"ids"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.BatchDelete(r.Context(), tenantID, req.IDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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