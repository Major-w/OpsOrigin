package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, a *Article) error
	Get(ctx context.Context, id, tenantID string) (*Article, error)
	List(ctx context.Context, tenantID, category string) ([]*Article, error)
	Update(ctx context.Context, a *Article) error
	IncrementViews(ctx context.Context, id, tenantID string) error
	Delete(ctx context.Context, id, tenantID string) error

	// —— P1 内容管理 ——
	ListCategories(ctx context.Context, tenantID string) ([]*Category, error)
	CreateCategory(ctx context.Context, c *Category) error
	UpdateCategory(ctx context.Context, c *Category) error
	DeleteCategory(ctx context.Context, id, tenantID string) error
	ListVersions(ctx context.Context, articleID, tenantID string) ([]*ArticleVersion, error)
	RollbackToVersion(ctx context.Context, articleID, tenantID string, version int) error
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
	if !ok || a.TenantID != tenantID {
		return nil, nil
	}
	return a, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID, category string) ([]*Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Article
	for _, a := range s.articles {
		if a.TenantID != tenantID {
			continue
		}
		if category != "" && a.Category != category {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, a *Article) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.articles[a.ID]
	if !ok || existing.TenantID != a.TenantID {
		return nil
	}
	a.UpdatedAt = time.Now()
	s.articles[a.ID] = a
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.articles[id]
	if !ok || a.TenantID != tenantID {
		return nil
	}
	delete(s.articles, a.ID)
	return nil
}

func (s *MemoryStore) IncrementViews(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.articles[id]; ok && a.TenantID == tenantID {
		a.Views++
	}
	return nil
}

// —— P1 内容管理: 内存实现(测试/轻量场景) ——

func (s *MemoryStore) ListCategories(ctx context.Context, tenantID string) ([]*Category, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 从文章去重收集分类(无独立元数据时的降级)
	seen := map[string]*Category{}
	for _, a := range s.articles {
		if a.TenantID != tenantID || a.Category == "" {
			continue
		}
		if _, ok := seen[a.Category]; !ok {
			seen[a.Category] = &Category{Name: a.Category, TenantID: tenantID}
		}
	}
	out := make([]*Category, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}
	return out, nil
}

func (s *MemoryStore) CreateCategory(ctx context.Context, c *Category) error { return nil }
func (s *MemoryStore) UpdateCategory(ctx context.Context, c *Category) error { return nil }
func (s *MemoryStore) DeleteCategory(ctx context.Context, id, tenantID string) error {
	return nil
}

func (s *MemoryStore) ListVersions(ctx context.Context, articleID, tenantID string) ([]*ArticleVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if a, ok := s.articles[articleID]; ok && a.TenantID == tenantID && a.Version > 1 {
		return []*ArticleVersion{{
			ArticleID: articleID, TenantID: tenantID, Version: a.Version - 1,
			Title: a.Title, Content: a.Content, Category: a.Category,
		}}, nil
	}
	return nil, nil
}

func (s *MemoryStore) RollbackToVersion(ctx context.Context, articleID, tenantID string, version int) error {
	return nil
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
	mux.HandleFunc("POST /api/v1/knowledge", s.handleCreate)
	mux.HandleFunc("GET /api/v1/knowledge", s.handleList)
	mux.HandleFunc("GET /api/v1/knowledge/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/knowledge/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/knowledge/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/v1/knowledge/reindex", s.handleReindex)
	// —— P1 内容管理 ——
	mux.HandleFunc("GET /api/v1/knowledge/categories", s.handleListCategories)
	mux.HandleFunc("POST /api/v1/knowledge/categories", s.handleCreateCategory)
	mux.HandleFunc("PUT /api/v1/knowledge/categories/{id}", s.handleUpdateCategory)
	mux.HandleFunc("DELETE /api/v1/knowledge/categories/{id}", s.handleDeleteCategory)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/versions", s.handleListVersions)
	mux.HandleFunc("POST /api/v1/knowledge/{id}/versions/{version}/rollback", s.handleRollbackVersion)
	mux.HandleFunc("POST /api/v1/knowledge/import", s.handleImport)
}

// SetEmbedder 注入向量生成器,使新建/更新文章自动写入 embedding 以支持语义检索。
func (s *Service) SetEmbedder(e Embedder) {
	if ps, ok := s.store.(*pgStore); ok {
		ps.SetEmbedder(e)
	}
}

// ReindexAll 为存量文章补算 embedding(语义检索开启后的幂等回填, 仅处理 embedding IS NULL 的行)。
func (s *Service) ReindexAll(ctx context.Context) (int, error) {
	ps, ok := s.store.(*pgStore)
	if !ok {
		return 0, fmt.Errorf("reindex only supported with postgres store")
	}
	if !ps.VectorEnabled() {
		return 0, fmt.Errorf("vector search not enabled (pgvector unavailable)")
	}
	return ps.ReindexAll(ctx)
}

// StartReindexLoop 启动知识库增量索引自愈后台循环(embedding 失败文章带退避重试 + 未索引指标)。
// 仅 pgStore 且在向量检索可用时生效;传入 ctx 取消即停止。
func (s *Service) StartReindexLoop(ctx context.Context, interval time.Duration) {
	if ps, ok := s.store.(*pgStore); ok {
		ps.StartReindexLoop(ctx, interval)
	}
}

// handleReindex 为存量文章补算 embedding(开启语义检索后的一次性回填)。
func (s *Service) handleReindex(w http.ResponseWriter, r *http.Request) {
	ps, ok := s.store.(*pgStore)
	if !ok {
		writeError(w, http.StatusBadRequest, "reindex only supported with postgres store")
		return
	}
	if !ps.VectorEnabled() {
		writeError(w, http.StatusBadRequest, "vector search not enabled (pgvector unavailable)")
		return
	}
	n, err := ps.ReindexAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "reindexed": n})
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
	if articles == nil {
		articles = []*Article{}
	}
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
	s.store.IncrementViews(r.Context(), id, tenantID)
	a.Views++
	writeJSON(w, http.StatusOK, a)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, _ := s.store.Get(r.Context(), id, tenantID)
	if existing == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var a Article
	json.NewDecoder(r.Body).Decode(&a)
	a.ID = existing.ID
	a.TenantID = existing.TenantID
	a.CreatedAt = existing.CreatedAt
	a.Views = existing.Views
	a.Version = existing.Version // 版本递增由 store 内部处理
	if a.Status == "" {
		a.Status = existing.Status // 未显式传状态时不降级(如 published → draft)
	}
	s.store.Update(r.Context(), &a)
	writeJSON(w, http.StatusOK, a)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.Delete(r.Context(), id, tenantFromCtx(r.Context()))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— P1 内容管理: 分类元数据 ———

func (s *Service) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, _ := s.store.ListCategories(r.Context(), tenantFromCtx(r.Context()))
	if cats == nil {
		cats = []*Category{}
	}
	writeJSON(w, http.StatusOK, cats)
}

func (s *Service) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var c Category
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if c.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	c.TenantID = tenantFromCtx(r.Context())
	if err := s.store.CreateCategory(r.Context(), &c); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Service) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var c Category
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c.ID = r.PathValue("id")
	c.TenantID = tenantFromCtx(r.Context())
	if err := s.store.UpdateCategory(r.Context(), &c); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Service) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	// 先查分类名, 若有文章引用则把文章分类清空(避免悬挂分类)
	if cats, err := s.store.ListCategories(r.Context(), tenantID); err == nil {
		var name string
		for _, c := range cats {
			if c.ID == id {
				name = c.Name
			}
		}
		if name != "" {
			if articles, err := s.store.List(r.Context(), tenantID, name); err == nil {
				for _, a := range articles {
					a.Category = ""
					s.store.Update(r.Context(), a)
				}
			}
		}
	}
	s.store.DeleteCategory(r.Context(), id, tenantID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— P1 内容管理: 文章版本 ———

func (s *Service) handleListVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	versions, _ := s.store.ListVersions(r.Context(), id, tenantID)
	if versions == nil {
		versions = []*ArticleVersion{}
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Service) handleRollbackVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version <= 0 {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.RollbackToVersion(r.Context(), id, tenantID, version); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rolled_back", "version": r.PathValue("version")})
}

// handleImport 批量导入文章。Body: {"items":[{title,content,category,tags,status}]}。
// 单条失败不中断整体, 返回 imported 数量与错误列表。
func (s *Service) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []Article `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items is empty")
		return
	}
	if len(req.Items) > 500 {
		writeError(w, http.StatusBadRequest, "too many items (max 500)")
		return
	}
	tenantID := tenantFromCtx(r.Context())
	imported := 0
	var errs []string
	for _, item := range req.Items {
		if item.Title == "" || item.Content == "" {
			errs = append(errs, "skip: title/content required")
			continue
		}
		item.ID = ""
		item.TenantID = tenantID
		if item.Status == "" {
			item.Status = "draft"
		}
		if err := s.store.Create(r.Context(), &item); err != nil {
			errs = append(errs, item.Title+": "+err.Error())
			continue
		}
		imported++
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "errors": errs})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
