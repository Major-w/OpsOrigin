// 主动进化: 定期从互联网数据源(RSS/URL)抓取内容, 经 LLM 提炼后沉淀为本地知识库文章。
// LLM 未配置或数据源为空时优雅降级(记录跳过, 不影响主流程)。
package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/opsorigin/core/pkg/httputil"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/llm"
)

// EvolutionSource 主动进化数据源。
type EvolutionSource struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"` // rss | url | keyword
	Source         string     `json:"source"`
	Enabled        bool       `json:"enabled"`
	IntervalHours  int        `json:"interval_hours"`  // 独立执行频率(小时), 0=继承全局
	TargetCategory string     `json:"target_category"` // 目标分类, 空=继承全局
	MinScore       float64    `json:"min_score"`       // 提炼最低分(0-1), 0=继承全局
	LastRunAt      *time.Time `json:"last_run_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// EvolutionPolicy 主动进化全局策略。
type EvolutionPolicy struct {
	Enabled        bool     `json:"enabled"`         // 总开关
	IntervalHours  int      `json:"interval_hours"`  // 默认执行频率(小时)
	TargetCategory string   `json:"target_category"` // 默认目标分类
	MinScore       float64  `json:"min_score"`       // 默认最低提炼分(0-1)
	Blacklist      []string `json:"blacklist"`       // 关键词黑名单(命中任一则丢弃)
	UpdatedAt      string   `json:"updated_at"`
}

// DefaultEvolutionPolicy 默认全局策略。
func DefaultEvolutionPolicy() EvolutionPolicy {
	return EvolutionPolicy{
		Enabled:        true,
		IntervalHours:  6,
		TargetCategory: "知识获取",
		MinScore:       0.5,
		Blacklist:      []string{},
	}
}

// KnowledgeCreateHook 由 cmd/core 注入: 将提炼的知识写入本地知识库(nil=未注入)。
var KnowledgeCreateHook func(ctx context.Context, title, content, category string, tags []string) error

// initEvolutionDDL 幂等建表(数据源配置 + 全局策略)。
func (s *Service) initEvolutionDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_evolution_sources (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			source TEXT NOT NULL,
			enabled BOOL NOT NULL DEFAULT true,
			interval_hours INT NOT NULL DEFAULT 0,
			target_category TEXT NOT NULL DEFAULT '',
			min_score DOUBLE PRECISION NOT NULL DEFAULT 0,
			last_run_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai evolution sources DDL failed", "err", err)
	}
	// 存量库幂等补列
	for _, stmt := range []string{
		`ALTER TABLE ai_evolution_sources ADD COLUMN IF NOT EXISTS interval_hours INT NOT NULL DEFAULT 0`,
		`ALTER TABLE ai_evolution_sources ADD COLUMN IF NOT EXISTS target_category TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ai_evolution_sources ADD COLUMN IF NOT EXISTS min_score DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE ai_evolution_sources ADD COLUMN IF NOT EXISTS last_run_at TIMESTAMPTZ`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			slog.Warn("ai evolution sources alter failed", "err", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_evolution_policy (
			tenant_id TEXT PRIMARY KEY,
			enabled BOOL NOT NULL DEFAULT true,
			interval_hours INT NOT NULL DEFAULT 6,
			target_category TEXT NOT NULL DEFAULT '知识获取',
			min_score DOUBLE PRECISION NOT NULL DEFAULT 0.5,
			blacklist JSONB NOT NULL DEFAULT '[]',
			updated_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai evolution policy DDL failed", "err", err)
	}
}

func (s *Service) listSources(ctx context.Context, tenantID string) []EvolutionSource {
	if s.db == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, source, enabled, interval_hours, target_category, min_score, last_run_at, created_at
		 FROM ai_evolution_sources WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		slog.Warn("ai evolution sources list failed", "err", err)
		return nil
	}
	defer rows.Close()
	out := []EvolutionSource{}
	for rows.Next() {
		var it EvolutionSource
		var last sql.NullTime
		if err := rows.Scan(&it.ID, &it.Name, &it.Type, &it.Source, &it.Enabled, &it.IntervalHours, &it.TargetCategory, &it.MinScore, &last, &it.CreatedAt); err != nil {
			continue
		}
		if last.Valid {
			t := last.Time
			it.LastRunAt = &t
		}
		out = append(out, it)
	}
	return out
}

// getPolicy 读取全局策略(不存在则落默认值)。
func (s *Service) getPolicy(ctx context.Context, tenantID string) EvolutionPolicy {
	def := DefaultEvolutionPolicy()
	if s.db == nil {
		return def
	}
	var black []byte
	var enabled bool
	var interval int
	var category string
	var score float64
	var updated time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled, interval_hours, target_category, min_score, blacklist, updated_at
		 FROM ai_evolution_policy WHERE tenant_id=$1`, tenantID).
		Scan(&enabled, &interval, &category, &score, &black, &updated)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Warn("ai evolution policy get failed", "err", err)
		}
		return def
	}
	def.Enabled = enabled
	def.IntervalHours = interval
	def.TargetCategory = category
	def.MinScore = score
	def.UpdatedAt = updated.Format(time.RFC3339)
	if len(black) > 0 {
		_ = json.Unmarshal(black, &def.Blacklist)
	}
	return def
}

// savePolicy 写全局策略。
func (s *Service) savePolicy(ctx context.Context, tenantID string, p EvolutionPolicy) error {
	if s.db == nil {
		return fmt.Errorf("db unavailable")
	}
	black, _ := json.Marshal(p.Blacklist)
	if p.Blacklist == nil {
		black = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_evolution_policy (tenant_id, enabled, interval_hours, target_category, min_score, blacklist, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tenant_id) DO UPDATE SET
			enabled=EXCLUDED.enabled, interval_hours=EXCLUDED.interval_hours,
			target_category=EXCLUDED.target_category, min_score=EXCLUDED.min_score,
			blacklist=EXCLUDED.blacklist, updated_at=EXCLUDED.updated_at`,
		tenantID, p.Enabled, p.IntervalHours, p.TargetCategory, p.MinScore, black, time.Now())
	return err
}

// handleEvolutionPolicy GET/PUT 全局策略。
func (s *Service) handleEvolutionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	if r.Method == http.MethodGet {
		httputil.WriteJSON(w, http.StatusOK, s.getPolicy(r.Context(), tenantID))
		return
	}
	var req struct {
		Enabled        *bool    `json:"enabled"`
		IntervalHours  *int     `json:"interval_hours"`
		TargetCategory string   `json:"target_category"`
		MinScore       *float64 `json:"min_score"`
		Blacklist      []string `json:"blacklist"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	cur := s.getPolicy(r.Context(), tenantID)
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.IntervalHours != nil && *req.IntervalHours > 0 {
		cur.IntervalHours = *req.IntervalHours
	}
	if req.TargetCategory != "" {
		cur.TargetCategory = req.TargetCategory
	}
	if req.MinScore != nil {
		cur.MinScore = *req.MinScore
	}
	if req.Blacklist != nil {
		cur.Blacklist = req.Blacklist
	}
	if err := s.savePolicy(r.Context(), tenantID, cur); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, s.getPolicy(r.Context(), tenantID))
}

// handleEvolutionSources CRUD。
func (s *Service) handleEvolutionSources(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		httputil.WriteJSON(w, http.StatusOK, s.listSources(r.Context(), tenantFromCtx(r.Context())))
		return
	}
	// POST 创建
	var req struct {
		Name           string   `json:"name"`
		Type           string   `json:"type"`
		Source         string   `json:"source"`
		Enabled        *bool    `json:"enabled"`
		IntervalHours  *int     `json:"interval_hours"`
		TargetCategory string   `json:"target_category"`
		MinScore       *float64 `json:"min_score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" || req.Source == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and source required")
		return
	}
	if req.Type != "rss" && req.Type != "url" && req.Type != "keyword" {
		httputil.WriteError(w, http.StatusBadRequest, "type must be rss|url|keyword")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	it := EvolutionSource{
		ID: uuid.New().String(), Name: req.Name, Type: req.Type,
		Source: req.Source, Enabled: enabled, CreatedAt: time.Now(),
	}
	if req.IntervalHours != nil {
		it.IntervalHours = *req.IntervalHours
	}
	it.TargetCategory = req.TargetCategory
	if req.MinScore != nil {
		it.MinScore = *req.MinScore
	}
	if s.db != nil {
		if _, err := s.db.ExecContext(r.Context(),
			`INSERT INTO ai_evolution_sources (id, tenant_id, name, type, source, enabled, interval_hours, target_category, min_score, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			it.ID, tenantFromCtx(r.Context()), it.Name, it.Type, it.Source, it.Enabled, it.IntervalHours, it.TargetCategory, it.MinScore, it.CreatedAt); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, it)
}

// handleUpdateEvolutionSource 编辑数据源(策略覆盖字段)。
func (s *Service) handleUpdateEvolutionSource(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Enabled        *bool    `json:"enabled"`
		IntervalHours  *int     `json:"interval_hours"`
		TargetCategory *string  `json:"target_category"`
		MinScore       *float64 `json:"min_score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 读当前行判断 tenant 归属
	row := s.db.QueryRowContext(r.Context(),
		`SELECT interval_hours, target_category, min_score, enabled FROM ai_evolution_sources WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	var iv int
	var cat string
	var ms float64
	var en bool
	if err := row.Scan(&iv, &cat, &ms, &en); err != nil {
		httputil.WriteError(w, http.StatusNotFound, "source not found")
		return
	}
	if req.Enabled != nil {
		en = *req.Enabled
	}
	if req.IntervalHours != nil {
		iv = *req.IntervalHours
	}
	if req.TargetCategory != nil {
		cat = *req.TargetCategory
	}
	if req.MinScore != nil {
		ms = *req.MinScore
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE ai_evolution_sources SET enabled=$1, interval_hours=$2, target_category=$3, min_score=$4 WHERE id=$5 AND tenant_id=$6`,
		en, iv, cat, ms, id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Service) handleDeleteEvolutionSource(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM ai_evolution_sources WHERE id=$1 AND tenant_id=$2`, id, tenantFromCtx(r.Context())); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// hitBlacklist 命中任一黑名单关键词(大小写不敏感)。
func hitBlacklist(a evolvedArticle, black []string) bool {
	hay := strings.ToLower(a.Title + " " + a.Content)
	for _, k := range black {
		if k = strings.TrimSpace(k); k != "" && strings.Contains(hay, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// handleEvolutionFetch 手动触发一次主动进化。
func (s *Service) handleEvolutionFetch(w http.ResponseWriter, r *http.Request) {
	sources := s.listSources(r.Context(), tenantFromCtx(r.Context()))
	results := []map[string]any{}
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		summary, err := s.fetchAndAnalyze(r.Context(), tenantFromCtx(r.Context()), src)
		item := map[string]any{"name": src.Name, "type": src.Type, "source": src.Source}
		if err != nil {
			item["status"] = "error"
			item["message"] = err.Error()
		} else {
			item["status"] = "ok"
			item["message"] = summary
		}
		results = append(results, item)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// fetchAndAnalyze 抓取单个数据源 → LLM 提炼(打分过滤) → 沉淀知识库。
func (s *Service) fetchAndAnalyze(ctx context.Context, tenantID string, src EvolutionSource) (string, error) {
	lc := s.LLMClient()
	if lc == nil {
		return "", fmt.Errorf("LLM 未配置, 跳过提炼(请先在 模型 页配置 API Key)")
	}
	if src.Type == "keyword" {
		return "", fmt.Errorf("keyword 类型暂不支持(需配置检索 API), 请使用 rss/url")
	}
	policy := s.getPolicy(ctx, tenantID)
	raw, err := fetchSourceContent(ctx, src)
	if err != nil {
		return "", err
	}
	// 按源覆盖解析策略参数
	minScore := policy.MinScore
	if src.MinScore > 0 {
		minScore = src.MinScore
	}
	category := policy.TargetCategory
	if src.TargetCategory != "" {
		category = src.TargetCategory
	}
	articles := summarizeArticles(ctx, lc, raw)
	if len(articles) == 0 {
		return "", fmt.Errorf("LLM 未提炼出有效知识")
	}
	created, filtered := 0, 0
	for _, a := range articles {
		// 黑名单过滤(标题/正文命中任一关键词)
		if hitBlacklist(a, policy.Blacklist) {
			filtered++
			continue
		}
		// 分数过滤
		if minScore > 0 && a.Score > 0 && a.Score < minScore {
			filtered++
			continue
		}
		if KnowledgeCreateHook == nil {
			return "", fmt.Errorf("knowledge writer hook 未注入")
		}
		if err := KnowledgeCreateHook(ctx, a.Title, a.Content, category, []string{"evolution", src.Name}); err != nil {
			slog.Warn("evolution knowledge create failed", "title", a.Title, "err", err)
			continue
		}
		created++
	}
	msg := fmt.Sprintf("提炼 %d 篇, 沉淀 %d 篇", len(articles), created)
	if filtered > 0 {
		msg += fmt.Sprintf(", 过滤 %d 篇", filtered)
	}
	return msg, nil
}

// fetchSourceContent 抓取 rss/url 文本。
func fetchSourceContent(ctx context.Context, src EvolutionSource) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.Source, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "OpsOrigin-Evolution/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("抓取失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("抓取失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if src.Type == "rss" {
		return rssToText(body), nil
	}
	return stripHTML(body), nil
}

// rssToText 解析 RSS 2.0/Atom 提要为纯文本。
func rssToText(b []byte) string {
	var feed struct {
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
		Entries []struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			Sum   string `xml:"summary"`
		} `xml:"entry"` // Atom
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		return stripHTML(b)
	}
	var sb strings.Builder
	for i, it := range feed.Channel.Items {
		if i >= 6 {
			break
		}
		fmt.Fprintf(&sb, "## %s\n%s\n链接: %s\n\n", it.Title, stripHTML([]byte(it.Description)), it.Link)
	}
	for i, e := range feed.Entries {
		if i >= 6 {
			break
		}
		fmt.Fprintf(&sb, "## %s\n%s\n链接: %s\n\n", e.Title, stripHTML([]byte(e.Sum)), e.Link)
	}
	if sb.Len() == 0 {
		return stripHTML(b)
	}
	return sb.String()
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// stripHTML 去除 HTML 标签并压缩空白。
func stripHTML(b []byte) string {
	s := htmlTagRe.ReplaceAllString(string(b), " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 4000 {
		s = s[:4000]
	}
	return s
}

type evolvedArticle struct {
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// summarizeArticles 用 LLM 从原始文本提炼 1-3 条运维知识(带价值分 0-1)。
func summarizeArticles(ctx context.Context, lc *llm.Client, raw string) []evolvedArticle {
	prompt := `你是运维知识提炼助手。从下面的内容中提炼出 1~3 条最有价值的运维知识。
要求: 只输出 JSON 数组, 格式 [{"title":"简短标题","content":"Markdown 正文(含背景/步骤/注意事项)","score":0.0}],
score 为该条知识对运维人员的价值评分(0~1, 与运维场景相关性越高分越高), 不要任何额外说明。
内容:
` + raw
	resp, err := lc.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: prompt}},
		Stream:   false,
	})
	if err != nil || resp == nil || len(resp.Choices) == 0 {
		slog.Warn("evolution llm summarize failed", "err", err)
		return nil
	}
	text := resp.Choices[0].Message.Content
	text = strings.TrimSpace(strings.Trim(text, "`"))
	// 兼容带 ```json 围栏
	text = strings.TrimPrefix(text, "json")
	text = strings.TrimSpace(strings.Trim(text, "`"))
	var arts []evolvedArticle
	if err := json.Unmarshal([]byte(text), &arts); err != nil {
		// 解析失败: 整体兜底为一条, 分数按 1.0(不参与阈值过滤)
		return []evolvedArticle{{Title: "互联网知识获取", Content: text, Score: 1.0}}
	}
	out := []evolvedArticle{}
	for _, a := range arts {
		if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Content) == "" {
			continue
		}
		if a.Score < 0 || a.Score > 1 {
			a.Score = 1.0 // 非法分视为放行
		}
		out = append(out, a)
	}
	return out
}

// StartEvolutionLoop 定时主动进化循环(主 tick 全局间隔; 每个源按自身频率调度)。
func (s *Service) StartEvolutionLoop(ctx context.Context, interval time.Duration) {
	if s.db == nil {
		return
	}
	go func() {
		s.runEvolutionPass(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runEvolutionPass(ctx)
			}
		}
	}()
}

// sourceDue 判断数据源是否到执行时间(按源 interval 或全局)。
func (s *Service) sourceDue(ctx context.Context, tenantID string, src EvolutionSource) bool {
	h := src.IntervalHours
	if h <= 0 {
		h = s.getPolicy(ctx, tenantID).IntervalHours
	}
	if h <= 0 {
		h = 6
	}
	if src.LastRunAt == nil {
		return true
	}
	return time.Since(*src.LastRunAt) >= time.Duration(h)*time.Hour
}

// markSourceRun 记录数据源最近执行时间。
func (s *Service) markSourceRun(ctx context.Context, tenantID, id string) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE ai_evolution_sources SET last_run_at=$1 WHERE id=$2 AND tenant_id=$3`, time.Now(), id, tenantID); err != nil {
		slog.Warn("ai evolution mark run failed", "err", err)
	}
}

func (s *Service) runEvolutionPass(ctx context.Context) {
	if s.LLMClient() == nil {
		slog.Info("evolution loop skip: llm not configured")
		return
	}
	const tenantID = "default"
	policy := s.getPolicy(ctx, tenantID)
	if !policy.Enabled {
		slog.Info("evolution loop skip: disabled by policy")
		return
	}
	sources := s.listSources(ctx, tenantID)
	enabled, ran := 0, 0
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		enabled++
		if !s.sourceDue(ctx, tenantID, src) {
			continue
		}
		ran++
		summary, err := s.fetchAndAnalyze(ctx, tenantID, src)
		if err != nil {
			slog.Warn("evolution pass source failed", "name", src.Name, "err", err)
		} else {
			slog.Info("evolution pass source done", "name", src.Name, "summary", summary)
		}
		s.markSourceRun(ctx, tenantID, src.ID)
	}
	slog.Info("evolution pass finished", "enabled_sources", enabled, "ran", ran)
}
