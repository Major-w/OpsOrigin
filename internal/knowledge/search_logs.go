package knowledge

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// —— AI 知识库检索可观测(P1-2)与零结果留痕(P1-4) ——
//
// 此前 RAG 检索是纯黑盒: 只有 knowledge_unindexed_articles 一个指标(衡量"还没进索引的文章数"),
// 检索本身没有延迟/命中/零结果/各路贡献的任何记录。后果是
//   1. 无法回答"知识库到底有没有被用上、回答质量差是检索不到还是模型不会用";
//   2. 零结果查询(用户真实问过、但知识库里没有的问题)完全留不下痕迹, 知识补全只能靠猜。
//
// 本文件提供 knowledge_search_logs 的读写:
//   - Record: 由 AI 检索链路(agentic 工具集)每次召回后写入一条留痕(失败只告警, 绝不影响检索);
//   - List/Stats: 供管理员查看「检索可观测」页(零结果查询 TOP、延迟、各路贡献、按天趋势)。
//
// 表结构由 pgStore.init 的自愈 DDL 建立(与知识库其他表同源), 本文件只做 DML。

// SearchLogRetention 检索留痕保留时长: 留痕是运行态观测数据, 不做长期归档。
const SearchLogRetention = 30 * 24 * time.Hour

// searchLogQueryMaxRunes 单条查询文本的落库上限(防御超长 prompt 直接把表撑爆)。
const searchLogQueryMaxRunes = 500

// SearchLog 单次 AI 知识库检索的留痕记录。
type SearchLog struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
	// Mode 检索路径: rrf(块级双路融合) | fallback(块级全空回退文章级) | cache(检索结果缓存命中)。
	Mode       string    `json:"mode"`
	Categories []string  `json:"categories,omitempty"`
	VecHits    int       `json:"vec_hits"`  // 块级向量路命中块数(融合前)
	KwHits     int       `json:"kw_hits"`   // 块级关键词路命中块数(tsvector+ILIKE 合并, 融合前)
	HitCount   int       `json:"hit_count"` // 融合后返回的文章数(即最终喂给模型的知识条数)
	ZeroResult bool      `json:"zero_result"`
	LatencyMS  float64   `json:"latency_ms"`
	TopScore   float64   `json:"top_score"` // 首条结果的融合分(0 = 无结果)
	AgentID    string    `json:"agent_id,omitempty"`
	UserID     string    `json:"user_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// SearchLogStore 检索留痕读写(仅 Postgres)。
type SearchLogStore struct {
	db *sql.DB
}

// NewSearchLogStore 构造留痕存储; db 为 nil 时返回 nil(内存模式, 留痕整体关闭)。
func NewSearchLogStore(db *sql.DB) *SearchLogStore {
	if db == nil {
		return nil
	}
	return &SearchLogStore{db: db}
}

// Record 写入一条检索留痕(best-effort: 失败只告警, 由调用方决定是否忽略)。
// 留痕属观测数据, 单独设 3s 超时, 避免它把一次检索的响应时间拖长。
func (s *SearchLogStore) Record(ctx context.Context, rec *SearchLog) error {
	if s == nil || s.db == nil || rec == nil {
		return nil
	}
	q := strings.TrimSpace(rec.Query)
	if q == "" {
		return nil
	}
	if r := []rune(q); len(r) > searchLogQueryMaxRunes {
		q = string(r[:searchLogQueryMaxRunes])
	}
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	ectx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ectx, `
		INSERT INTO knowledge_search_logs
			(id, tenant_id, query, mode, categories, vec_hits, kw_hits, hit_count,
			 zero_result, latency_ms, top_score, agent_id, user_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		rec.ID, rec.TenantID, q, rec.Mode, strings.Join(rec.Categories, ","),
		rec.VecHits, rec.KwHits, rec.HitCount, rec.ZeroResult,
		rec.LatencyMS, rec.TopScore, rec.AgentID, rec.UserID, rec.CreatedAt)
	return err
}

// SearchLogFilter 留痕列表过滤条件。
type SearchLogFilter struct {
	ZeroOnly bool   // 只看零结果查询(知识盲区)
	Keyword  string // 查询文本模糊过滤
	Mode     string // 按检索路径过滤
	Limit    int
	Offset   int
}

// List 分页列出留痕(按时间倒序), 返回记录与总数。
func (s *SearchLogStore) List(ctx context.Context, tenant string, f SearchLogFilter) ([]*SearchLog, int, error) {
	if s == nil || s.db == nil {
		return []*SearchLog{}, 0, nil
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{"tenant_id = $1"}
	args := []any{tenant}
	if f.ZeroOnly {
		where = append(where, "zero_result = true")
	}
	if f.Keyword != "" {
		args = append(args, "%"+f.Keyword+"%")
		where = append(where, "query ILIKE $"+strconv.Itoa(len(args)))
	}
	if f.Mode != "" {
		args = append(args, f.Mode)
		where = append(where, "mode = $"+strconv.Itoa(len(args)))
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM knowledge_search_logs`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, query, mode, categories, vec_hits, kw_hits, hit_count,
		       zero_result, latency_ms, top_score, agent_id, user_id, created_at
		FROM knowledge_search_logs`+clause+`
		ORDER BY created_at DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]*SearchLog, 0)
	for rows.Next() {
		rec := &SearchLog{}
		var cats, agent, user sql.NullString
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.Query, &rec.Mode, &cats,
			&rec.VecHits, &rec.KwHits, &rec.HitCount, &rec.ZeroResult,
			&rec.LatencyMS, &rec.TopScore, &agent, &user, &rec.CreatedAt); err != nil {
			return nil, 0, err
		}
		rec.AgentID, rec.UserID = agent.String, user.String
		if cats.String != "" {
			rec.Categories = strings.Split(cats.String, ",")
		}
		out = append(out, rec)
	}
	return out, total, rows.Err()
}

// SearchQueryStat 按查询文本聚合的统计(TOP 命中 / TOP 零结果)。
type SearchQueryStat struct {
	Query   string    `json:"query"`
	Count   int64     `json:"count"`
	AvgHits float64   `json:"avg_hits"`
	LastAt  time.Time `json:"last_at"`
}

// SearchDailyStat 按天聚合的检索趋势。
type SearchDailyStat struct {
	Day          string  `json:"day"`
	Total        int64   `json:"total"`
	Zero         int64   `json:"zero"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

// SearchStats 检索质量概览。
type SearchStats struct {
	Days         int     `json:"days"`
	Total        int64   `json:"total"`
	ZeroResults  int64   `json:"zero_results"`
	ZeroRate     float64 `json:"zero_rate"`     // 零结果率 = 零结果检索 / 总检索
	AvgHitCount  float64 `json:"avg_hit_count"` // 平均召回文章数
	AvgLatencyMS float64 `json:"avg_latency_ms"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
	// ModeStats 检索路径分布(rrf/fallback/cache)。
	ModeStats map[string]int64 `json:"mode_stats"`
	// VectorRoutes / KeywordRoutes / FallbackRoutes 各路贡献: 该路有命中的检索次数。
	// 用于判断关键词路是否其实在兜底(向量路全空), 以及 fallback 是否长期在用(说明分块索引没建好)。
	VectorRoutes   int64 `json:"vector_routes"`
	KeywordRoutes  int64 `json:"keyword_routes"`
	FallbackRoutes int64 `json:"fallback_routes"`
	// UnindexedArticles 当前仍未完成索引的文章数(知识库侧指标, 与检索留痕一起看才完整)。
	UnindexedArticles int64 `json:"unindexed_articles"`
	// TopQueries 命中查询 TOP: 知识库真正被用上的问题。
	TopQueries []SearchQueryStat `json:"top_queries"`
	// ZeroQueries 零结果查询 TOP: 知识盲区清单, 直接对应「该补什么知识」。
	ZeroQueries []SearchQueryStat `json:"zero_queries"`
	// Series 按天趋势(含零结果与平均延迟)。
	Series []SearchDailyStat `json:"series"`
}

// Stats 聚合最近 days 天的检索质量统计; days <= 0 时默认 7 天, 上限 90 天。
func (s *SearchLogStore) Stats(ctx context.Context, tenant string, days int) (*SearchStats, error) {
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	out := &SearchStats{
		Days:        days,
		ModeStats:   map[string]int64{},
		TopQueries:  []SearchQueryStat{},
		ZeroQueries: []SearchQueryStat{},
		Series:      []SearchDailyStat{},
	}
	if s == nil || s.db == nil {
		return out, nil
	}
	since := time.Now().AddDate(0, 0, -days)

	var avgHits, avgLatency, p95 float64
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE zero_result),
		       COALESCE(avg(hit_count), 0),
		       COALESCE(avg(latency_ms), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)
		FROM knowledge_search_logs WHERE tenant_id=$1 AND created_at >= $2`,
		tenant, since).Scan(&out.Total, &out.ZeroResults, &avgHits, &avgLatency, &p95); err != nil {
		return nil, err
	}
	out.AvgHitCount, out.AvgLatencyMS, out.P95LatencyMS = avgHits, avgLatency, p95
	if out.Total > 0 {
		out.ZeroRate = float64(out.ZeroResults) / float64(out.Total)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE vec_hits > 0),
		       count(*) FILTER (WHERE kw_hits > 0),
		       count(*) FILTER (WHERE mode = 'fallback')
		FROM knowledge_search_logs WHERE tenant_id=$1 AND created_at >= $2`,
		tenant, since).Scan(&out.VectorRoutes, &out.KeywordRoutes, &out.FallbackRoutes); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT mode, count(*) FROM knowledge_search_logs
		 WHERE tenant_id=$1 AND created_at >= $2 GROUP BY mode`, tenant, since)
	if err == nil {
		for rows.Next() {
			var mode string
			var n int64
			if err := rows.Scan(&mode, &n); err == nil {
				out.ModeStats[mode] = n
			}
		}
		rows.Close()
	}

	if out.TopQueries, err = s.topQueries(ctx, tenant, since, false); err != nil {
		return nil, err
	}
	if out.ZeroQueries, err = s.topQueries(ctx, tenant, since, true); err != nil {
		return nil, err
	}
	if out.Series, err = s.daily(ctx, tenant, since); err != nil {
		return nil, err
	}
	return out, nil
}

// topQueries 聚合出现频次最高的查询; zero=true 时只看零结果查询。
func (s *SearchLogStore) topQueries(ctx context.Context, tenant string, since time.Time, zero bool) ([]SearchQueryStat, error) {
	cond := "AND zero_result = false"
	if zero {
		cond = "AND zero_result = true"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT query, count(*) AS c, COALESCE(avg(hit_count), 0) AS avg_hits, max(created_at) AS last_at
		FROM knowledge_search_logs
		WHERE tenant_id=$1 AND created_at >= $2 `+cond+`
		GROUP BY query ORDER BY c DESC, last_at DESC LIMIT 10`, tenant, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SearchQueryStat, 0, 10)
	for rows.Next() {
		var st SearchQueryStat
		if err := rows.Scan(&st.Query, &st.Count, &st.AvgHits, &st.LastAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// daily 按天聚合检索量与零结果数, 供前端折线趋势使用。
func (s *SearchLogStore) daily(ctx context.Context, tenant string, since time.Time) ([]SearchDailyStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT to_char(date_trunc('day', created_at), 'YYYY-MM-DD') AS d,
		       count(*), count(*) FILTER (WHERE zero_result), COALESCE(avg(latency_ms), 0)
		FROM knowledge_search_logs
		WHERE tenant_id=$1 AND created_at >= $2
		GROUP BY 1 ORDER BY 1`, tenant, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SearchDailyStat, 0)
	for rows.Next() {
		var st SearchDailyStat
		if err := rows.Scan(&st.Day, &st.Total, &st.Zero, &st.AvgLatencyMS); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Prune 删除 before 之前的留痕, 返回删除条数(由后台循环周期性调用)。
func (s *SearchLogStore) Prune(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM knowledge_search_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// —— HTTP: 检索可观测(管理员视图) ——

// handleSearchLogs 列出检索留痕。
// GET /api/v1/knowledge/search-logs?zero_only=1&mode=rrf&q=&limit=&offset=
func (s *Service) handleSearchLogs(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": []*SearchLog{}, "total": 0})
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, total, err := s.logs.List(r.Context(), auth.TenantFromContext(r.Context()), SearchLogFilter{
		ZeroOnly: q.Get("zero_only") == "1" || q.Get("zero_only") == "true",
		Keyword:  strings.TrimSpace(q.Get("q")),
		Mode:     strings.TrimSpace(q.Get("mode")),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// handleSearchStats 检索质量概览(零结果率/延迟/各路贡献/零结果 TOP/按天趋势)。
// GET /api/v1/knowledge/search-stats?days=7
func (s *Service) handleSearchStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	stats, err := s.logs.Stats(r.Context(), auth.TenantFromContext(r.Context()), days)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 未索引文章数(与检索质量同页展示): 索引缺口大 → 检索命中率必然低, 两个数一起看才有解释力。
	if ps, ok := s.store.(*pgStore); ok {
		stats.UnindexedArticles = ps.countUnindexedArticles(r.Context())
	}
	httputil.WriteJSON(w, http.StatusOK, stats)
}

// StartSearchLogPruneLoop 周期性清理过期检索留痕(保留 SearchLogRetention)。
// 留痕是运行态观测数据, 无长期价值, 不清理会让表无限膨胀。
func (s *Service) StartSearchLogPruneLoop(ctx context.Context, interval time.Duration) {
	if s.logs == nil {
		return
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	go func() {
		// 启动时先清一次: 服务重启前累积的过期数据不必等一个周期。
		s.pruneSearchLogs(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pruneSearchLogs(ctx)
			}
		}
	}()
}

func (s *Service) pruneSearchLogs(ctx context.Context) {
	if s.logs == nil {
		return
	}
	n, err := s.logs.Prune(ctx, time.Now().Add(-SearchLogRetention))
	if err != nil {
		slog.Warn("knowledge: prune search logs failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("knowledge: pruned search logs", "rows", n)
	}
}
