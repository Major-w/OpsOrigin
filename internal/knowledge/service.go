package knowledge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/internal/objectstore"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/docextract"
	"github.com/opsorigin/core/pkg/httputil"
)

// ErrNotFound 附件/资源不存在(与 ITSM store.ErrNotFound 同语义, 包内自用)。
var ErrNotFound = errors.New("knowledge: not found")

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

	// —— 文章附件 ——
	AddAttachment(ctx context.Context, a *ArticleAttachment) error
	ListAttachments(ctx context.Context, articleID, tenantID string) ([]*ArticleAttachment, error)
	GetAttachment(ctx context.Context, aid int64, articleID, tenantID string) (*ArticleAttachment, error)
	DeleteAttachment(ctx context.Context, aid int64, articleID, tenantID string) error

	// —— P3-3 状态机: 流转 + 评审轨迹 ——
	Transition(ctx context.Context, id, tenantID, to, note, actor string) (*Article, error)
	ListReviews(ctx context.Context, articleID, tenantID string, limit int) ([]*ArticleReview, error)

	// —— P3-1 创建去重 ——
	FindDuplicates(ctx context.Context, tenantID, title, content, excludeID string, limit int) ([]*DuplicateCandidate, error)
}

// ArticleAttachment 知识文章附件元数据。二进制在对象存储(MinIO/COS/OSS), ObjectKey 为空时回退 Data(bytea)。
type ArticleAttachment struct {
	ID         int64     `json:"id"`
	ArticleID  string    `json:"article_id"`
	TenantID   string    `json:"tenant_id"`
	FileName   string    `json:"file_name"`
	FileSize   int64     `json:"file_size"`
	FileType   string    `json:"file_type"`
	UploadedBy string    `json:"uploaded_by"`
	ObjectKey  string    `json:"object_key"`
	Data       []byte    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	// —— 解析入库状态(上传后自动抽文本→切块→embedding, 使附件内容可被 AI 检索) ——
	Indexed    bool   `json:"indexed"`               // 是否已成功解析入库
	IndexError string `json:"index_error,omitempty"` // 失败原因(空=成功或未尝试)
	ChunkCount int    `json:"chunk_count"`           // 产出知识块数
}

type MemoryStore struct {
	mu          sync.RWMutex
	articles    map[string]*Article
	attachments map[int64]*ArticleAttachment
	nextAttID   int64
	reviews     []*ArticleReview // 状态流转/评审轨迹(P3-3, 内存模式)
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{articles: make(map[string]*Article), attachments: make(map[int64]*ArticleAttachment)}
}

func (s *MemoryStore) Create(ctx context.Context, a *Article) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = uuid.New().String()
	now := time.Now()
	a.CreatedAt = now
	a.UpdatedAt = now
	a.normalizeVisibility()
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
	a.normalizeVisibility()
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

// —— 附件: 内存实现 ——

func (s *MemoryStore) AddAttachment(ctx context.Context, a *ArticleAttachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAttID++
	a.ID = s.nextAttID
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	s.attachments[a.ID] = a
	return nil
}

func (s *MemoryStore) ListAttachments(ctx context.Context, articleID, tenantID string) ([]*ArticleAttachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ArticleAttachment, 0)
	for _, a := range s.attachments {
		if a.ArticleID == articleID && a.TenantID == tenantID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *MemoryStore) GetAttachment(ctx context.Context, aid int64, articleID, tenantID string) (*ArticleAttachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if a, ok := s.attachments[aid]; ok && a.ArticleID == articleID && a.TenantID == tenantID {
		return a, nil
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) DeleteAttachment(ctx context.Context, aid int64, articleID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.attachments[aid]; ok && a.ArticleID == articleID && a.TenantID == tenantID {
		delete(s.attachments, aid)
		return nil
	}
	return ErrNotFound
}

type Service struct {
	store    Store
	social   *SocialStore
	objStore *objectstore.Client // 附件二进制落对象存储; nil = 回退数据库 bytea
	gen      Generator           // AI 辅助编写注入的 LLM 生成器; nil = 未启用
	logs     *SearchLogStore     // AI 检索留痕(P1-2/P1-4); nil = 内存模式, 留痕关闭
	ragEval  RAGEvaluator        // RAG 效果评估器(P1-1); nil = 未接 AI 引擎, 评估入口返回 501
	// retrievalPreview 检索测试台(迭代一 · 批次 4); nil = 未接 AI 引擎, 预览入口返回 501。
	retrievalPreview RetrievalPreviewer
	// knowledgeSearch 用户级知识搜索(面向普通用户的查询入口); nil = 未接 AI 引擎, 端点返回 501。
	knowledgeSearch KnowledgeSearcher
	// categoryResolver 按 Agent 解析知识范围(与 AI 引擎的知识库绑定同源), 供评估复现线上检索范围。
	categoryResolver func(agentID string) []string
	// cacheInvalidator 知识内容/可见性变更后失效 AI 检索缓存的回调(由 cmd/core 注入)。
	// 检索结果有 5 分钟 TTL 缓存, 不清则下架/收紧可见性的文章仍会在窗口内被召回。
	cacheInvalidator func()
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db)
	}
	return &Service{store: st, logs: NewSearchLogStore(db)}
}

// SetCacheInvalidator 注册 AI 检索缓存失效回调。
// 由装配层把 agentic 侧的两条缓存(MCPToolSet.searchCache 与引擎级 toolCache)清空动作接进来;
// knowledge 包不反向依赖 agentic, 故用函数回调解耦。
func (s *Service) SetCacheInvalidator(fn func()) { s.cacheInvalidator = fn }

// invalidateSearchCache 在知识库写操作后通知上游清缓存。回调未注入时静默跳过。
func (s *Service) invalidateSearchCache() {
	if s.cacheInvalidator != nil {
		s.cacheInvalidator()
	}
}

// SearchLogs 返回检索留痕存储(供 AI 检索链路注入写入, 见 agentic.MCPToolSet.SetSearchLogStore)。
// 内存模式下为 nil, 调用方需容忍。
func (s *Service) SearchLogs() *SearchLogStore { return s.logs }

// SetObjectStore 注入对象存储(MinIO/COS/OSS)。注入后附件二进制进对象存储, DB 只存 object_key。
func (s *Service) SetObjectStore(os *objectstore.Client) { s.objStore = os }

// CreateArticle 创建一篇知识库文章(供内部模块如 ITSM 工单案例沉淀复用)。
// 未指定 ID/Status/Author 时自动补全, 默认直接发布。
// ListDisposalCases 返回该租户 tags 含 disposal-case 的文章(处置案例, 供 AI 训练集导出)。
// 供 cmd/core 注入到 AI 引擎的 DisposalCaseProvider 回调。
func (s *Service) ListDisposalCases(ctx context.Context, tenantID string) ([]struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}, error) {
	articles, err := s.store.List(ctx, tenantID, "")
	if err != nil {
		return nil, err
	}
	out := make([]struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}, 0, len(articles))
	for _, a := range articles {
		hasTag := false
		for _, t := range a.Tags {
			if t == "disposal-case" {
				hasTag = true
				break
			}
		}
		if !hasTag {
			continue
		}
		out = append(out, struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}{ID: a.ID, Title: a.Title, Content: a.Content})
	}
	return out, nil
}

func (s *Service) CreateArticle(ctx context.Context, a *Article) error {
	if a == nil {
		return fmt.Errorf("article is nil")
	}
	if a.Title == "" {
		return fmt.Errorf("article title required")
	}
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.TenantID == "" {
		a.TenantID = "default"
	}
	if a.Status == "" {
		a.Status = "published"
	}
	if a.Author == "" {
		a.Author = "system"
	}
	if a.ViewPermission == "" {
		a.ViewPermission = "all"
	}
	if a.EditPermission == "" {
		a.EditPermission = "owner"
	}
	if err := s.store.Create(ctx, a); err != nil {
		return err
	}
	// 新知识立即可被检索: 清掉 AI 侧缓存, 否则同一提问 5 分钟内仍拿旧结果(看不到新文章)。
	s.invalidateSearchCache()
	return nil
}

// EnsureCategory 确保分类存在: 不存在则创建, 返回分类 ID(供外部模块如主动进化路由使用)。
func (s *Service) EnsureCategory(ctx context.Context, tenantID, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("category name required")
	}
	if tenantID == "" {
		tenantID = "default"
	}
	items, err := s.store.ListCategories(ctx, tenantID)
	if err == nil {
		for _, c := range items {
			if c.Name == name {
				return c.ID, nil
			}
		}
	}
	c := &Category{
		ID: uuid.New().String(), TenantID: tenantID, Name: name, CreatedAt: time.Now(),
	}
	if err := s.store.CreateCategory(ctx, c); err != nil {
		return "", err
	}
	return c.ID, nil
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
	// P1 文件导入: 后端解析 .md/.txt/.json/.pdf/.docx → 建文章
	mux.HandleFunc("POST /api/v1/knowledge/import-file", s.handleImportFile)
	// AI 辅助编写知识: 一次性生成 Markdown 回填编辑器(由 / 斜杠命令或工具栏按钮触发)
	mux.HandleFunc("POST /api/v1/knowledge/ai-write", s.handleAIWrite)
	// —— 文章附件 ——
	mux.HandleFunc("POST /api/v1/knowledge/{id}/attachments", s.handleUploadAttachment)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/attachments", s.handleListAttachments)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/attachments/{aid}", s.handleDownloadAttachment)
	mux.HandleFunc("DELETE /api/v1/knowledge/{id}/attachments/{aid}", s.handleDeleteAttachment)
	// —— 附件解析入库 / 在线预览 ——
	mux.HandleFunc("POST /api/v1/knowledge/{id}/attachments/{aid}/reindex", s.handleReindexAttachment)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/attachments/{aid}/text", s.handleAttachmentText)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/attachments/{aid}/chunks", s.handleAttachmentChunks)
	// 网页 URL 抓取入库(带 SSRF 防护)
	mux.HandleFunc("POST /api/v1/knowledge/import-url", s.handleImportURL)
	// 引用溯源: 按块 ID 取原文片段(挂在文章下, 同时校验归属; 放在 /knowledge/{id}/... 下
	// 避免与 /knowledge/{id}/versions 等既有路由在 Go 1.22 ServeMux 下冲突)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/chunks", s.handleListArticleChunks)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/chunks/{chunk}", s.handleGetChunk)
	// 用户级知识搜索: 与 AI 同一条检索路径(可见性谓词按请求者身份生成), 零结果也进留痕。
	// 字面量段优先于 /knowledge/{id} 通配, 不会被当成文章 ID。
	mux.HandleFunc("GET /api/v1/knowledge/search", s.handleKnowledgeSearch)
	// —— 检索可观测(P1-2/P1-4) ——
	// 字面量段优先于 /knowledge/{id} 通配, 故 search-logs / search-stats 不会被当成文章 ID。
	mux.HandleFunc("GET /api/v1/knowledge/search-logs", s.handleSearchLogs)
	mux.HandleFunc("GET /api/v1/knowledge/search-stats", s.handleSearchStats)
	// —— 生命周期(P3-1/P3-3) ——
	mux.HandleFunc("POST /api/v1/knowledge/{id}/transition", s.handleTransition)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/reviews", s.handleListReviews)
	mux.HandleFunc("GET /api/v1/knowledge/duplicates", s.handleCheckDuplicates)
	mux.HandleFunc("POST /api/v1/knowledge/duplicates", s.handleCheckDuplicates)
	// —— RAG 效果评估(P1-1) ——
	mux.HandleFunc("POST /api/v1/knowledge/rag-eval", s.handleRAGEval)
	// —— 检索测试台(迭代一 · 批次 4) ——
	// 与 /knowledge/duplicates 同形(字面段), Go 1.22 ServeMux 下字面段优先于 {id} 通配, 无歧义。
	mux.HandleFunc("POST /api/v1/knowledge/retrieval-preview", s.handleRetrievalPreview)
}

// SetEmbedder 注入向量生成器,使新建/更新文章自动写入 embedding 以支持语义检索。
func (s *Service) SetEmbedder(e Embedder) {
	if ps, ok := s.store.(*pgStore); ok {
		ps.SetEmbedder(e)
	}
}

// SetGenerator 注入 LLM 生成器,启用「AI 辅助编写知识」(由编辑器 / 斜杠命令或工具栏按钮触发)。
func (s *Service) SetGenerator(g Generator) { s.gen = g }

// Generator 由 LLM 客户端适配注入,用于 AI 辅助编写知识(一次性生成,不污染会话上下文)。
type Generator interface {
	Generate(ctx context.Context, system, user string) (string, error)
}

// SetSocialStore 注入社交存储,使文章更新时能为关注者生成更新通知。
func (s *Service) SetSocialStore(soc *SocialStore) {
	s.social = soc
}

// ReindexAll 为存量文章补算 embedding(语义检索开启后的幂等回填, 仅处理还没有块的文档)。
func (s *Service) ReindexAll(ctx context.Context) (int, error) {
	ps, ok := s.store.(*pgStore)
	if !ok {
		return 0, fmt.Errorf("reindex only supported with postgres store")
	}
	if !ps.VectorEnabled() {
		return 0, fmt.Errorf("vector search not enabled (pgvector unavailable)")
	}
	return ps.ReindexAll(ctx, false)
}

// StartReindexLoop 启动知识库增量索引自愈后台循环(embedding 失败文章带退避重试 + 未索引指标)。
// 仅 pgStore 且在向量检索可用时生效;传入 ctx 取消即停止。
func (s *Service) StartReindexLoop(ctx context.Context, interval time.Duration) {
	if ps, ok := s.store.(*pgStore); ok {
		ps.StartReindexLoop(ctx, interval)
	}
	// 检索留痕清理与索引自愈同生命周期启动(留痕是运行态数据, 保留 30 天)。
	s.StartSearchLogPruneLoop(ctx, 6*time.Hour)
	// 到期自动下架(P3-3): 有效期已过的文章从 published 归档, 不再被 AI 引用。
	s.StartExpiryLoop(ctx, archiveExpiredInterval)
}

// handleReindex 重建知识库分块索引。
//   - 默认(幂等): 只为还没有块的文档补建;
//   - ?force=1: 全部文章重建 —— 更换 embedding 模型后必须走这个, 否则旧维度块被检索过滤后
//     这些文章会静默失去语义检索能力;
//   - ?attachments=1: 连同附件抽取的块一起重建。
func (s *Service) handleReindex(w http.ResponseWriter, r *http.Request) {
	ps, ok := s.store.(*pgStore)
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "reindex only supported with postgres store")
		return
	}
	if !ps.VectorEnabled() {
		httputil.WriteError(w, http.StatusBadRequest, "vector search not enabled (pgvector unavailable)")
		return
	}
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	n, err := ps.ReindexAll(r.Context(), force)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	attN := 0
	if r.URL.Query().Get("attachments") == "1" {
		attN = s.reindexAllAttachments(r.Context())
	}
	// 重建索引会改变块与向量, 缓存里的旧召回结果必须作废。
	s.invalidateSearchCache()
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "reindexed": n, "force": force, "attachments": attN,
	})
}

// reindexAllAttachments 重建全部附件的知识块(换解析器/换 embedding 模型后使用)。
// 逐个抽取需要读二进制(对象存储或 DB), 成本较高, 故设上限避免一次请求打满。
func (s *Service) reindexAllAttachments(ctx context.Context) int {
	atts, err := psListAttachments(ctx, s.store)
	if err != nil {
		slog.Warn("knowledge: list attachments for reindex failed", "err", err)
		return 0
	}
	n := 0
	for _, att := range atts {
		if n >= reindexAttachmentMax {
			break
		}
		data, err := s.loadAttachmentBytes(ctx, att)
		if err != nil {
			slog.Warn("knowledge: reindex attachment load failed", "att", att.ID, "err", err)
			continue
		}
		s.indexAttachment(ctx, att, data)
		n++
	}
	return n
}

// reindexAttachmentMax 单次全量重建的附件数量上限。
const reindexAttachmentMax = 200

// psListAttachments 列出全部文章下的附件(全量重建用)。
// 附件按文章组织, 故先取文章清单再逐个列附件; 仅在管理员触发的全量重建路径调用, 不做性能优化。
func psListAttachments(ctx context.Context, st Store) ([]*ArticleAttachment, error) {
	ps, ok := st.(*pgStore)
	if !ok {
		return nil, fmt.Errorf("attachments only supported with postgres store")
	}
	rows, err := ps.db.QueryContext(ctx, `SELECT id, tenant_id FROM knowledge_articles`)
	if err != nil {
		return nil, err
	}
	var ids []struct{ id, tenant string }
	for rows.Next() {
		var id, tenant string
		if err := rows.Scan(&id, &tenant); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, struct{ id, tenant string }{id, tenant})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []*ArticleAttachment
	for _, p := range ids {
		list, lerr := ps.ListAttachments(ctx, p.id, p.tenant)
		if lerr != nil {
			slog.Warn("knowledge: list attachments failed", "article", p.id, "err", lerr)
			continue
		}
		out = append(out, list...)
		if len(out) >= reindexAttachmentMax {
			break
		}
	}
	return out, nil
}

// validateOwnership 校验归属信息与可见范围是否自洽。
//
// team 可见却没有归属团队时, 该文章对**任何**提问者都不成立(数组重叠匹配 '{}' && anything = false),
// 即静默地对所有人不可见。与其让作者以为"发给团队了"实际谁也没看到, 不如在写入时直接报错。
// 注意调用时机: 必须在 normalizeVisibility 之后 —— 留空的 view_permission 语义是 all,
// 若先校验会把空串当成 team 之外的合法值而漏掉 team 场景。
func validateOwnership(a *Article) error {
	if a.ViewPermission == ViewPermissionTeam && len(a.OwnerGroupIDs) == 0 {
		return errors.New("团队可见必须至少指定一个归属团队")
	}
	return nil
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	// 内嵌 Article + force_duplicate: 一次解包拿到正文与「已知重复仍要创建」的显式确认。
	var req struct {
		Article
		ForceDuplicate bool `json:"force_duplicate"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a := req.Article
	// 状态白名单(P3-3): 此前状态是自由字符串, 前端传什么就存什么。
	if a.Status != "" && !validArticleStatus(a.Status) {
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("unknown status %q, allowed: %s", a.Status, strings.Join(ArticleStatuses, "|")))
		return
	}
	a.TenantID = auth.TenantFromContext(r.Context())
	// 归属: 作者 = 当前登录用户(owner 可见性的唯一判定依据, 库中为空则该分支永不成立)。
	a.AuthorID = auth.UserIDFromContext(r.Context())
	// 归属归一 + 校验必须在写库前完成: store 层也会归一, 但那已经在 INSERT 时机上,
	// 报错不如在 handler 直接拒绝(前端表单同样把归属团队标为必填)。
	a.normalizeVisibility()
	if err := validateOwnership(&a); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 创建去重(P3-1): 命中相似文章时返回 409 + 候选清单, 由调用方决定是否强制创建。
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	if !force && !req.ForceDuplicate {
		dups, derr := s.CheckDuplicate(r.Context(), a.TenantID, a.Title, a.Content, "")
		if derr != nil {
			slog.Warn("knowledge create duplicate check failed", "err", derr)
		} else if len(dups) > 0 {
			httputil.WriteJSON(w, http.StatusConflict, duplicateConflictBody(dups))
			return
		}
	}
	// 分类路由闭环: 目标分类不存在时自动创建(与主动进化沉淀一致)
	if a.Category != "" {
		if _, err := s.EnsureCategory(r.Context(), a.TenantID, a.Category); err != nil {
			slog.Warn("knowledge create ensure category failed", "category", a.Category, "err", err)
		}
	}
	if err := s.store.Create(r.Context(), &a); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 创建即可能被检索(状态为 published 时), 必须让 AI 侧缓存失效。
	s.invalidateSearchCache()
	httputil.WriteJSON(w, http.StatusCreated, a)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	category := r.URL.Query().Get("category")
	articles, _ := s.store.List(r.Context(), tenantID, category)
	if articles == nil {
		articles = []*Article{}
	}
	httputil.WriteJSON(w, http.StatusOK, articles)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	a, _ := s.store.Get(r.Context(), id, tenantID)
	if a == nil {
		httputil.WriteError(w, http.StatusNotFound, "article not found")
		return
	}
	s.store.IncrementViews(r.Context(), id, tenantID)
	a.Views++
	httputil.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, _ := s.store.Get(r.Context(), id, tenantID)
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	var a Article
	httputil.BindJSON(r, &a)
	a.ID = existing.ID
	a.TenantID = existing.TenantID
	a.CreatedAt = existing.CreatedAt
	a.Views = existing.Views
	a.Version = existing.Version // 版本递增由 store 内部处理
	if a.Status == "" {
		a.Status = existing.Status // 未显式传状态时不降级(如 published → draft)
	} else if a.Status != existing.Status {
		// 状态机(P3-3): 编辑态改状态同样要过白名单与合法迁移校验,
		// 否则 POST /transition 只是"建议", 前端一处 PUT 就能把状态改成任意值。
		if !validArticleStatus(a.Status) {
			httputil.WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("unknown status %q, allowed: %s", a.Status, strings.Join(ArticleStatuses, "|")))
			return
		}
		if !canTransition(existing.Status, a.Status) {
			httputil.WriteError(w, http.StatusConflict,
				fmt.Sprintf("illegal status transition: %s -> %s", existing.Status, a.Status))
			return
		}
		// 状态流转同时留一条评审轨迹, 保证「谁改的状态」可追溯。
		if _, err := s.TransitionArticle(r.Context(), id, existing.TenantID, a.Status, "编辑保存时变更状态", reviewActor(r.Context())); err != nil {
			slog.Warn("knowledge update transition failed", "article", id, "to", a.Status, "err", err)
		}
	}
	// 权限字段: 未显式传时保留原值
	if a.ViewPermission == "" {
		a.ViewPermission = existing.ViewPermission
	}
	if a.EditPermission == "" {
		a.EditPermission = existing.EditPermission
	}
	// 作者不可通过编辑接口改写(否则可把他人 owner 文章改到自己名下)。未传则保留。
	if strings.TrimSpace(a.AuthorID) == "" {
		a.AuthorID = existing.AuthorID
	}
	// 归属团队: nil = 请求体未带该字段 → 保留原值; 显式 [] = 清空。
	if a.OwnerGroupIDs == nil {
		a.OwnerGroupIDs = existing.OwnerGroupIDs
	}
	a.normalizeVisibility()
	if err := validateOwnership(&a); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// ExpiresAt: 前端传 null/undefined 表示清除有效期; 传 existing 值表示保留
	if a.ExpiresAt == nil && r.Body != nil {
		// 如果请求体中没有 expires_at 字段, 保留原值
		a.ExpiresAt = existing.ExpiresAt
	}
	s.store.Update(r.Context(), &a)
	// 正文/状态/可见性任一变化都会改变召回结果, 清 AI 侧缓存避免 TTL 内继续返回旧内容。
	s.invalidateSearchCache()
	// 文章更新后, 通知关注者(关注更新)
	if s.social != nil {
		s.social.NotifyFollowersOnUpdate(r.Context(), id)
	}
	httputil.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.Delete(r.Context(), id, auth.TenantFromContext(r.Context()))
	// 删除后必须立即失效: 否则被删文章的内容仍会从缓存里进入模型上下文。
	s.invalidateSearchCache()
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— P1 内容管理: 分类元数据 ———

func (s *Service) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, _ := s.store.ListCategories(r.Context(), auth.TenantFromContext(r.Context()))
	if cats == nil {
		cats = []*Category{}
	}
	httputil.WriteJSON(w, http.StatusOK, cats)
}

func (s *Service) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var c Category
	if err := httputil.BindJSON(r, &c); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if c.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	c.TenantID = auth.TenantFromContext(r.Context())
	if err := s.store.CreateCategory(r.Context(), &c); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, c)
}

func (s *Service) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var c Category
	if err := httputil.BindJSON(r, &c); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c.ID = r.PathValue("id")
	c.TenantID = auth.TenantFromContext(r.Context())
	if err := s.store.UpdateCategory(r.Context(), &c); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
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
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— P1 内容管理: 文章版本 ———

func (s *Service) handleListVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	versions, _ := s.store.ListVersions(r.Context(), id, tenantID)
	if versions == nil {
		versions = []*ArticleVersion{}
	}
	httputil.WriteJSON(w, http.StatusOK, versions)
}

func (s *Service) handleRollbackVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version <= 0 {
		httputil.WriteError(w, http.StatusBadRequest, "invalid version")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.RollbackToVersion(r.Context(), id, tenantID, version); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "rolled_back", "version": r.PathValue("version")})
}

// handleImport 批量导入文章。Body: {"items":[{title,content,category,tags,status}]}。
// 单条失败不中断整体, 返回 imported 数量与错误列表。
func (s *Service) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []Article `json:"items"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Items) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "items is empty")
		return
	}
	if len(req.Items) > 500 {
		httputil.WriteError(w, http.StatusBadRequest, "too many items (max 500)")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
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
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"imported": imported, "errors": errs})
}

// handleImportFile 接收上传文件(.md/.txt/.json/.pdf/.docx), 服务端解析文本后批量建文章。
// multipart 字段: file(必填); 可选 form: category(统一归类)。
func (s *Service) handleImportFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "解析上传失败(请使用 multipart/form-data)")
		return
	}
	fh, ok := r.MultipartForm.File["file"]
	if !ok || len(fh) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	header := fh[0]
	f, err := header.Open()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "打开上传文件失败")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "读取上传文件失败")
		return
	}
	items, err := parseImportFile(header.Filename, data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	category := r.FormValue("category")
	tenantID := auth.TenantFromContext(r.Context())
	imported := 0
	var errs []string
	for _, it := range items {
		if strings.TrimSpace(it.Title) == "" || strings.TrimSpace(it.Content) == "" {
			errs = append(errs, "skip: title/content required")
			continue
		}
		it.ID = ""
		it.TenantID = tenantID
		if category != "" {
			it.Category = category
		}
		if it.Status == "" {
			it.Status = "draft"
		}
		if err := s.store.Create(r.Context(), &it); err != nil {
			errs = append(errs, it.Title+": "+err.Error())
			continue
		}
		imported++
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"imported": imported, "errors": errs})
}

// parseImportFile 按扩展名路由到对应解析器。
func parseImportFile(name string, data []byte) ([]Article, error) {
	ext := strings.ToLower(filepath.Ext(name))
	base := strings.TrimSuffix(name, ext)
	switch ext {
	case ".json":
		var items []Article
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("JSON 解析失败: %w", err)
		}
		return items, nil
	default:
		// PDF/Office/HTML/纯文本统一走共享抽取器(pkg/docextract), 与附件解析入库同一实现。
		text, err := docextract.ExtractText(name, data)
		if err != nil {
			return nil, err
		}
		return []Article{{Title: base, Content: text}}, nil
	}
}

// —— 文章附件: 上传/列表/下载/删除 ——

// handleUploadAttachment 上传附件。对象存储已配置时二进制进对象存储(key=kb/<article>/<uuid>),
// DB 只存 object_key; 否则回退数据库 bytea。
func (s *Service) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	uploadedBy := r.FormValue("uploaded_by")
	if uploadedBy == "" {
		uploadedBy = "anonymous"
	}
	// 浏览器给的 Content-Type 常是 application/octet-stream, 按扩展名校正后再落库,
	// 否则下载/预览会拿到错误类型(预览弹层据此分流渲染)。
	fileType := normalizeFileType(header.Header.Get("Content-Type"), header.Filename)
	att := &ArticleAttachment{
		ArticleID:  id,
		TenantID:   tenantID,
		FileName:   header.Filename,
		FileSize:   header.Size,
		FileType:   fileType,
		UploadedBy: uploadedBy,
		Data:       data,
	}
	if s.objStore != nil {
		// 对象 key 保留扩展名: 便于直接按 key 下载/预览, 也避免排障时无法辨认文件类型。
		key := fmt.Sprintf("kb/%s/%s%s", id, uuid.NewString(), strings.ToLower(filepath.Ext(header.Filename)))
		if err := s.objStore.Put(r.Context(), key, bytes.NewReader(data), int64(len(data)), fileType); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "failed to store attachment: "+err.Error())
			return
		}
		att.ObjectKey = key
		att.Data = nil
	}
	if err := s.store.AddAttachment(r.Context(), att); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to save attachment: "+err.Error())
		return
	}
	// 解析入库: 抽文本 → 切块 → embedding, 使附件内容可被 AI 检索问答。
	// 失败只记 index_error, 不影响附件存储与下载。
	s.indexAttachment(r.Context(), att, data)
	httputil.WriteJSON(w, http.StatusCreated, att)
}

func (s *Service) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	list, err := s.store.ListAttachments(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

func (s *Service) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aidStr := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())
	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}
	att, err := s.store.GetAttachment(r.Context(), aid, id, tenantID)
	if err != nil {
		if err == ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// —— 在线预览 ——
	// 默认 inline(浏览器直接打开); ?download=1 才强制下载。
	// 安全: html/htm/svg 内联等于同源执行脚本(存储型 XSS), 一律降级为纯文本渲染。
	ct := normalizeFileType(att.FileType, att.FileName)
	ext := strings.ToLower(filepath.Ext(att.FileName))
	forceDownload := r.URL.Query().Get("download") == "1"
	ct, disposition := previewHeaders(ct, ext, att.FileName, forceDownload)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", strconv.FormatInt(att.FileSize, 10))
	w.Header().Set("Cache-Control", "private, max-age=300")
	if att.ObjectKey != "" && s.objStore != nil {
		if err := s.objStore.Get(r.Context(), w, att.ObjectKey); err != nil {
			slog.Warn("kb attachment object read failed", "key", att.ObjectKey, "err", err)
			return
		}
		return
	}
	w.Write(att.Data)
}

func (s *Service) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aidStr := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())
	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}
	// 先取元数据: 对象存储模式下需一并删除对象, 避免孤儿文件。
	att, err := s.store.GetAttachment(r.Context(), aid, id, tenantID)
	if err != nil {
		if err == ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if att.ObjectKey != "" && s.objStore != nil {
		if err := s.objStore.Remove(r.Context(), att.ObjectKey); err != nil {
			slog.Warn("kb attachment object remove failed", "key", att.ObjectKey, "err", err)
		}
	}
	if err := s.store.DeleteAttachment(r.Context(), aid, id, tenantID); err != nil {
		if err == ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 附件块已被删除, 缓存里可能还留着它的召回结果 → 立即失效。
	s.invalidateSearchCache()
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
