// 主动进化: 定期从互联网数据源(RSS/URL)抓取内容, 经 LLM 提炼后沉淀为本地知识库文章。
// LLM 未配置或数据源为空时优雅降级(记录跳过, 不影响主流程)。
package ai

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
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
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // rss | url | keyword
	Source    string    `json:"source"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// KnowledgeCreateHook 由 cmd/core 注入: 将提炼的知识写入本地知识库(nil=未注入)。
var KnowledgeCreateHook func(ctx context.Context, title, content, category string, tags []string) error

// initEvolutionDDL 幂等建表(数据源配置)。
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
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai evolution sources DDL failed", "err", err)
	}
}

func (s *Service) listSources(ctx context.Context, tenantID string) []EvolutionSource {
	if s.db == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, source, enabled, created_at FROM ai_evolution_sources WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		slog.Warn("ai evolution sources list failed", "err", err)
		return nil
	}
	defer rows.Close()
	out := []EvolutionSource{}
	for rows.Next() {
		var it EvolutionSource
		if err := rows.Scan(&it.ID, &it.Name, &it.Type, &it.Source, &it.Enabled, &it.CreatedAt); err != nil {
			continue
		}
		out = append(out, it)
	}
	return out
}

// handleEvolutionSources CRUD。
func (s *Service) handleEvolutionSources(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.listSources(r.Context(), tenantFromCtx(r.Context())))
		return
	}
	// POST 创建
	var req struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Source  string `json:"source"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" || req.Source == "" {
		writeError(w, http.StatusBadRequest, "name and source required")
		return
	}
	if req.Type != "rss" && req.Type != "url" && req.Type != "keyword" {
		writeError(w, http.StatusBadRequest, "type must be rss|url|keyword")
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
	if s.db != nil {
		if _, err := s.db.ExecContext(r.Context(),
			`INSERT INTO ai_evolution_sources (id, tenant_id, name, type, source, enabled, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			it.ID, tenantFromCtx(r.Context()), it.Name, it.Type, it.Source, it.Enabled, it.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, it)
}

func (s *Service) handleDeleteEvolutionSource(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusNotFound, "db unavailable")
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM ai_evolution_sources WHERE id=$1 AND tenant_id=$2`, id, tenantFromCtx(r.Context())); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleEvolutionFetch 手动触发一次主动进化。
func (s *Service) handleEvolutionFetch(w http.ResponseWriter, r *http.Request) {
	sources := s.listSources(r.Context(), tenantFromCtx(r.Context()))
	results := []map[string]any{}
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		summary, err := s.fetchAndAnalyze(r.Context(), src)
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
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// fetchAndAnalyze 抓取单个数据源 → LLM 提炼 → 沉淀知识库。
func (s *Service) fetchAndAnalyze(ctx context.Context, src EvolutionSource) (string, error) {
	lc := s.LLMClient()
	if lc == nil {
		return "", fmt.Errorf("LLM 未配置, 跳过提炼(请先在 模型 页配置 API Key)")
	}
	if src.Type == "keyword" {
		return "", fmt.Errorf("keyword 类型暂不支持(需配置检索 API), 请使用 rss/url")
	}
	raw, err := fetchSourceContent(ctx, src)
	if err != nil {
		return "", err
	}
	articles := summarizeArticles(ctx, lc, raw)
	if len(articles) == 0 {
		return "", fmt.Errorf("LLM 未提炼出有效知识")
	}
	created := 0
	for _, a := range articles {
		if KnowledgeCreateHook == nil {
			return "", fmt.Errorf("knowledge writer hook 未注入")
		}
		if err := KnowledgeCreateHook(ctx, a.Title, a.Content, "知识获取", []string{"evolution", src.Name}); err != nil {
			slog.Warn("evolution knowledge create failed", "title", a.Title, "err", err)
			continue
		}
		created++
	}
	return fmt.Sprintf("提炼并沉淀 %d 篇知识", created), nil
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
	Title   string `json:"title"`
	Content string `json:"content"`
}

// summarizeArticles 用 LLM 从原始文本提炼 1-3 条运维知识。
func summarizeArticles(ctx context.Context, lc *llm.Client, raw string) []evolvedArticle {
	prompt := `你是运维知识提炼助手。从下面的内容中提炼出 1~3 条最有价值的运维知识。
要求: 只输出 JSON 数组, 格式 [{"title":"简短标题","content":"Markdown 正文(含背景/步骤/注意事项)"}], 不要任何额外说明。
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
		// 解析失败: 整体兜底为一条
		return []evolvedArticle{{Title: "互联网知识获取", Content: text}}
	}
	out := []evolvedArticle{}
	for _, a := range arts {
		if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Content) == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// StartEvolutionLoop 定时主动进化循环(默认 6h; LLM 未配置时跳过)。
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

func (s *Service) runEvolutionPass(ctx context.Context) {
	if s.LLMClient() == nil {
		slog.Info("evolution loop skip: llm not configured")
		return
	}
	sources := s.listSources(ctx, "default")
	enabled := 0
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		enabled++
		summary, err := s.fetchAndAnalyze(ctx, src)
		if err != nil {
			slog.Warn("evolution pass source failed", "name", src.Name, "err", err)
			continue
		}
		slog.Info("evolution pass source done", "name", src.Name, "summary", summary)
	}
	slog.Info("evolution pass finished", "enabled_sources", enabled)
}
