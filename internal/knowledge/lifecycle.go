package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// —— 文章生命周期: 状态机(P3-3) + 创建去重(P3-1) ——
//
// 修复前知识库文章只有 draft/published 两态与一个 expires_at 字段:
//   - 没有评审环节: 谁都能直接把草稿置为 published, 也没地方记录驳回原因;
//   - 状态是自由字符串: 前端传什么就存什么, 没有白名单;
//   - expires_at 到期后无人处理: 过期知识既不会下架, 也不会从检索里消失
//     (检索侧的 kbRetrievablePredicate 只是不召回, 库里的状态仍是"已发布", 管理端看着很迷惑);
//   - 创建不查重: 同一个人/不同人重复录入同一篇知识, 检索时同一主题被切成多篇抢排名。

// 文章状态取值(P3-3)。
const (
	StatusDraft     = "draft"     // 草稿: 仅作者可见, 不参与检索
	StatusInReview  = "in_review" // 待评审: 已提交, 等评审人通过或驳回
	StatusPublished = "published" // 已发布: 参与 AI 检索
	StatusArchived  = "archived"  // 已归档: 下架保留, 不参与检索
)

// ArticleStatuses 状态白名单(顺序即生命周期顺序, 供前端渲染)。
var ArticleStatuses = []string{StatusDraft, StatusInReview, StatusPublished, StatusArchived}

// articleTransitions 合法状态迁移表。
//
// 设计取向: 「不可跳跃到未知态」+「保留现网已有的反向操作」。
// published → draft 是现网编辑器已有的「撤回到草稿」能力, 不能因为引入评审流程而破坏它;
// 因此本表保证的是「状态取值受控 + 迁移路径可枚举 + 可观测」, 而非严格单向闭环。
var articleTransitions = map[string][]string{
	StatusDraft:     {StatusInReview, StatusPublished, StatusArchived},
	StatusInReview:  {StatusPublished, StatusDraft, StatusArchived},
	StatusPublished: {StatusDraft, StatusInReview, StatusArchived},
	StatusArchived:  {StatusDraft, StatusPublished},
}

// ErrInvalidStatus / ErrInvalidTransition 供 handler 区分「参数错」与「内部错」。
var (
	ErrInvalidStatus     = errors.New("knowledge: unknown article status")
	ErrInvalidTransition = errors.New("knowledge: illegal status transition")
)

func validArticleStatus(s string) bool {
	for _, v := range ArticleStatuses {
		if v == s {
			return true
		}
	}
	return false
}

func canTransition(from, to string) bool {
	for _, v := range articleTransitions[from] {
		if v == to {
			return true
		}
	}
	return false
}

// ArticleReview 一次状态流转记录(评审轨迹): 谁在什么时候把文章从哪个状态改到哪个状态、备注是什么。
// 驳回原因留在这里, 而不是文章表上的一个可变字段 —— 评审是可追溯的历史, 不是当前状态的一部分。
type ArticleReview struct {
	ID        string    `json:"id"`
	ArticleID string    `json:"article_id"`
	TenantID  string    `json:"tenant_id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Note      string    `json:"note,omitempty"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

// DuplicateCandidate 创建前的潜在重复文章(P3-1)。
type DuplicateCandidate struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Category   string    `json:"category,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	Similarity float64   `json:"similarity"` // 标题相似度 0~1; 正文完全一致时置 0.99
	Reason     string    `json:"reason"`     // title_similar | title_same | content_same
}

// —— Service: 去重检查 ——

// CheckDuplicate 检查同一租户内是否已有同名/同内容文章(P3-1)。
// 只在创建入口调用: 内部调用方(如 ITSM 工单沉淀)可能有意重复沉淀, 由调用方决定是否强制创建。
func (s *Service) CheckDuplicate(ctx context.Context, tenantID, title, content, excludeID string) ([]*DuplicateCandidate, error) {
	if strings.TrimSpace(title) == "" {
		return []*DuplicateCandidate{}, nil
	}
	dups, err := s.store.FindDuplicates(ctx, tenantID, title, content, excludeID, 5)
	if err != nil {
		return nil, err
	}
	if dups == nil {
		dups = []*DuplicateCandidate{}
	}
	return dups, nil
}

// —— Service: 状态流转 ——

// TransitionArticle 校验并执行一次状态流转, 同时写入评审轨迹。
//
// 说明: 流转不改正文, 因此不会触发重新分块与 embedding。
// 从 published 转出后, 该文章不再被 AI 检索召回(见 kbRetrievablePredicate),
// 但知识块保留 —— 重新发布时无需重新索引。
func (s *Service) TransitionArticle(ctx context.Context, id, tenantID, to, note, actor string) (*Article, error) {
	if !validArticleStatus(to) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, to)
	}
	a, err := s.store.Transition(ctx, id, tenantID, to, note, actor)
	if err != nil {
		return nil, err
	}
	// 状态变了, 可召回集合就变了(如 published→archived 后不应再被引用)。
	// 检索结果有 5 分钟 TTL 缓存, 不清则下架内容仍会在窗口内进入模型上下文。
	s.invalidateSearchCache()
	// 发布时通知关注者(与编辑保存同一条通路)。
	if to == StatusPublished && s.social != nil {
		s.social.NotifyFollowersOnUpdate(ctx, id)
	}
	return a, nil
}

// —— HTTP ——

// handleTransition 状态流转(提交评审 / 通过发布 / 驳回 / 归档 / 恢复)。
// POST /api/v1/knowledge/{id}/transition  body: {"to":"published","note":"..."}
func (s *Service) handleTransition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		To   string `json:"to"`
		Note string `json:"note"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	to := strings.TrimSpace(req.To)
	note := strings.TrimSpace(req.Note)
	// 备注是评审信息, 截断防止超长文本污染轨迹表。
	if r := []rune(note); len(r) > 500 {
		note = string(r[:500])
	}
	a, err := s.TransitionArticle(r.Context(), id, tenantID, to, note, reviewActor(r.Context()))
	switch {
	case errors.Is(err, ErrInvalidStatus):
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, ErrInvalidTransition):
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, "not found")
		return
	case err != nil:
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"article": a,
		// 语义提示: 非 published 状态的文章不再被 AI 检索召回。
		"retrievable": a.Status == StatusPublished,
	})
}

// handleListReviews 列出文章的状态流转/评审轨迹(最新在前)。
// GET /api/v1/knowledge/{id}/reviews?limit=20
func (s *Service) handleListReviews(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.store.ListReviews(r.Context(), id, auth.TenantFromContext(r.Context()), limit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*ArticleReview{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleCheckDuplicates 创建前预检重复(编辑器在提交前调用, 提前提示而不是等创建失败)。
//   - GET  /api/v1/knowledge/duplicates?title=&content=&exclude_id=  (短内容/快捷查询)
//   - POST /api/v1/knowledge/duplicates  body: {title, content, exclude_id}  (正文很长时避免 URL 超限)
func (s *Service) handleCheckDuplicates(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var title, content, excludeID string
	if r.Method == http.MethodPost {
		var req struct {
			Title     string `json:"title"`
			Content   string `json:"content"`
			ExcludeID string `json:"exclude_id"`
		}
		if err := httputil.BindJSON(r, &req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		title, content, excludeID = req.Title, req.Content, req.ExcludeID
	} else {
		q := r.URL.Query()
		title, content, excludeID = q.Get("title"), q.Get("content"), q.Get("exclude_id")
	}
	dups, err := s.CheckDuplicate(r.Context(), tenantID, title, content, excludeID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"duplicates": dups})
}

// duplicateConflictBody 构造 409 响应体: 让前端能直接把候选列表渲染进「发现相似文章」对话框。
func duplicateConflictBody(dups []*DuplicateCandidate) map[string]any {
	return map[string]any{
		"error":      "duplicate article: similar article already exists",
		"duplicates": dups,
		"hint":       "确认仍需创建时, 请在请求体带 force_duplicate=true 或在 URL 上带 ?force=1 重试",
	}
}

// reviewActor 取流转操作人: 优先登录用户, 其次 API Key 绑定的用户, 最后回落 "system"。
func reviewActor(ctx context.Context) string {
	if u := auth.UserIDFromContext(ctx); u != "" {
		return u
	}
	if k := auth.APIKeyIDFromContext(ctx); k != "" {
		return "apikey:" + k
	}
	return "system"
}

// —— 到期自动下架(P3-3) ——

// archiveExpiredInterval 到期扫描周期: 知识有效期以"天"为粒度, 十分钟一次的扫描足够及时。
const archiveExpiredInterval = 10 * time.Minute

// StartExpiryLoop 启动到期自动下架循环: 把已过 expires_at 但仍处于 published 的文章归档。
// 归档而非删除: 知识可能仍有参考价值, 由人决定是续期、修订还是删除。
func (s *Service) StartExpiryLoop(ctx context.Context, interval time.Duration) {
	ps, ok := s.store.(*pgStore)
	if !ok {
		return
	}
	if interval <= 0 {
		interval = archiveExpiredInterval
	}
	go func() {
		s.archiveExpired(ps, ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.archiveExpired(ps, ctx)
			}
		}
	}()
}

func (s *Service) archiveExpired(ps *pgStore, ctx context.Context) {
	n, err := ps.ArchiveExpired(ctx)
	if err != nil {
		slog.Warn("knowledge: archive expired articles failed", "err", err)
		return
	}
	if n > 0 {
		KnowledgeExpiredArchived.Add(float64(n))
		// 到期下架同样要失效检索缓存: 否则这批文章在 TTL 内仍会被 AI 引用。
		s.invalidateSearchCache()
		slog.Info("knowledge: archived expired articles", "count", n)
	}
}

// ArchiveExpired 把已过期但仍为 published 的文章置为 archived, 返回处理条数。
// 顺带写一条评审轨迹(from=published, to=archived, actor=system), 让管理端能看到"为什么被下架"。
func (s *pgStore) ArchiveExpired(ctx context.Context) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id FROM knowledge_articles
		WHERE status = $1 AND expires_at IS NOT NULL AND expires_at <= now() LIMIT 500`, StatusPublished)
	if err != nil {
		return 0, err
	}
	type rec struct{ id, tenant string }
	var expired []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.tenant); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(expired) == 0 {
		return 0, nil
	}
	var n int64
	for _, e := range expired {
		if _, err := s.Transition(ctx, e.id, e.tenant, StatusArchived,
			"知识有效期已过, 系统自动下架", "system"); err != nil {
			slog.Warn("knowledge: auto-archive failed", "article", e.id, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// —— pgStore: 状态流转与评审轨迹 ——

func (s *pgStore) Transition(ctx context.Context, id, tenantID, to, note, actor string) (*Article, error) {
	a, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNotFound
	}
	if !validArticleStatus(to) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, to)
	}
	if a.Status == to {
		return a, nil // 幂等: 重复提交同一目标状态不报错、也不产生噪声轨迹
	}
	if !canTransition(a.Status, to) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.Status, to)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_articles SET status=$1, updated_at=now() WHERE id=$2 AND tenant_id=$3`,
		to, id, tenantID); err != nil {
		return nil, err
	}
	if err := s.addReview(ctx, &ArticleReview{
		ArticleID: id, TenantID: tenantID, From: a.Status, To: to, Note: note, Actor: actor,
	}); err != nil {
		// 轨迹写入失败不回滚状态: 状态是主数据, 轨迹是审计增强, 只告警。
		slog.Warn("knowledge: write review trail failed", "article", id, "err", err)
	}
	a.Status = to
	a.UpdatedAt = time.Now()
	return a, nil
}

func (s *pgStore) addReview(ctx context.Context, rev *ArticleReview) error {
	if rev.ID == "" {
		rev.ID = uuid.New().String()
	}
	if rev.CreatedAt.IsZero() {
		rev.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_article_reviews (id, article_id, tenant_id, from_status, to_status, note, actor, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		rev.ID, rev.ArticleID, rev.TenantID, rev.From, rev.To, rev.Note, rev.Actor, rev.CreatedAt)
	return err
}

func (s *pgStore) ListReviews(ctx context.Context, articleID, tenantID string, limit int) ([]*ArticleReview, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, article_id, tenant_id, from_status, to_status, note, actor, created_at
		FROM knowledge_article_reviews
		WHERE article_id=$1 AND tenant_id=$2
		ORDER BY created_at DESC LIMIT $3`, articleID, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ArticleReview, 0)
	for rows.Next() {
		rev := &ArticleReview{}
		if err := rows.Scan(&rev.ID, &rev.ArticleID, &rev.TenantID, &rev.From, &rev.To,
			&rev.Note, &rev.Actor, &rev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

// —— pgStore: 重复检测(P3-1) ——

// FindDuplicates 查找与给定标题/正文重复或高度相似的文章。
//
// 判据(命中任一即候选, 按相似度降序):
//  1. 归一化标题完全相同(与是否安装 pg_trgm 无关, 始终可用);
//  2. 正文 md5 完全相同(纯文本精确重复);
//  3. pg_trgm 可用时, 标题相似度 similarity() ≥ dupTitleThreshold(触发模糊近似检测, 如
//     "磁盘满处理" 与 "磁盘满了怎么办")。
func (s *pgStore) FindDuplicates(ctx context.Context, tenantID, title, content, excludeID string, limit int) ([]*DuplicateCandidate, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return []*DuplicateCandidate{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	if s.trgmEnabled {
		rows, err := s.db.QueryContext(ctx, `
			SELECT a.id, a.title, a.status, coalesce(a.category,''), a.updated_at,
			       similarity(a.title, $2) AS sim,
			       (lower(btrim(a.title)) = lower(btrim($2))) AS same_title,
			       (md5(coalesce(a.content,'')) = md5($3)) AS same_content
			FROM knowledge_articles a
			WHERE a.tenant_id=$1 AND a.id <> $4
			  AND (a.title % $2 OR lower(btrim(a.title)) = lower(btrim($2))
			       OR (coalesce($3,'') <> '' AND md5(coalesce(a.content,'')) = md5($3)))
			ORDER BY sim DESC, a.updated_at DESC LIMIT $5`,
			tenantID, title, content, excludeID, limit)
		if err == nil {
			return scanDuplicateCandidates(rows)
		}
		slog.Warn("knowledge: trgm duplicate check failed, falling back to exact title match", "err", err)
	}
	// 无 pg_trgm(或查询失败): 退化为归一化标题相同 + 正文完全一致, 保证去重能力不会整体失效。
	// 与上面保持同一列形状, 只是相似度由下面的 scanDuplicateCandidates 按判据补算。
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.title, a.status, coalesce(a.category,''), a.updated_at,
		       0.0 AS sim,
		       (lower(btrim(a.title)) = lower(btrim($2))) AS same_title,
		       (md5(coalesce(a.content,'')) = md5($3)) AS same_content
		FROM knowledge_articles a
		WHERE a.tenant_id=$1 AND a.id <> $4
		  AND (lower(btrim(a.title)) = lower(btrim($2))
		       OR (coalesce($3,'') <> '' AND md5(coalesce(a.content,'')) = md5($3)))
		ORDER BY a.updated_at DESC LIMIT $5`,
		tenantID, title, content, excludeID, limit)
	if err != nil {
		return nil, err
	}
	return scanDuplicateCandidates(rows)
}

// scanDuplicateCandidates 扫描候选行并按判据给出相似度与原因(两条 SQL 共用同一列形状)。
func scanDuplicateCandidates(rows *sql.Rows) ([]*DuplicateCandidate, error) {
	defer rows.Close()
	out := make([]*DuplicateCandidate, 0)
	for rows.Next() {
		c := &DuplicateCandidate{}
		var sim float64
		var sameTitle, sameContent bool
		if err := rows.Scan(&c.ID, &c.Title, &c.Status, &c.Category, &c.UpdatedAt,
			&sim, &sameTitle, &sameContent); err != nil {
			return nil, err
		}
		switch {
		case sameTitle:
			c.Similarity, c.Reason = 1, "title_same"
		case sameContent:
			c.Similarity, c.Reason = 0.99, "content_same"
		default:
			c.Similarity, c.Reason = math.Round(sim*1000)/1000, "title_similar"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// —— MemoryStore 实现(内存模式: 去重与流转按同一规则降级实现, 保证接口完整) ——

func (s *MemoryStore) FindDuplicates(ctx context.Context, tenantID, title, content, excludeID string, limit int) ([]*DuplicateCandidate, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return []*DuplicateCandidate{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*DuplicateCandidate, 0)
	for _, a := range s.articles {
		if a.TenantID != tenantID || a.ID == excludeID {
			continue
		}
		c := &DuplicateCandidate{ID: a.ID, Title: a.Title, Status: a.Status, Category: a.Category, UpdatedAt: a.UpdatedAt}
		switch {
		case strings.EqualFold(strings.TrimSpace(a.Title), title):
			c.Similarity, c.Reason = 1, "title_same"
		case content != "" && a.Content == content:
			c.Similarity, c.Reason = 0.99, "content_same"
		default:
			continue
		}
		out = append(out, c)
	}
	if len(out) > limit && limit > 0 {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) Transition(ctx context.Context, id, tenantID, to, note, actor string) (*Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.articles[id]
	if !ok || a.TenantID != tenantID {
		return nil, ErrNotFound
	}
	if !validArticleStatus(to) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, to)
	}
	if a.Status == to {
		return a, nil
	}
	if !canTransition(a.Status, to) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.Status, to)
	}
	from := a.Status
	a.Status = to
	a.UpdatedAt = time.Now()
	s.reviews = append(s.reviews, &ArticleReview{
		ID: uuid.New().String(), ArticleID: id, TenantID: tenantID,
		From: from, To: to, Note: note, Actor: actor, CreatedAt: a.UpdatedAt,
	})
	return a, nil
}

func (s *MemoryStore) ListReviews(ctx context.Context, articleID, tenantID string, limit int) ([]*ArticleReview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ArticleReview, 0)
	for i := len(s.reviews) - 1; i >= 0; i-- {
		rev := s.reviews[i]
		if rev.ArticleID != articleID || rev.TenantID != tenantID {
			continue
		}
		out = append(out, rev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
