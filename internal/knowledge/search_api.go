package knowledge

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
)

// —— 知识搜索 API(面向普通用户的查询入口) ——
//
// 现状问题: 真正的语义/关键词检索只存在于 AI 对话(knowledge_search 工具)内部,
// 列表页的搜索框只是前端子串过滤 —— 搜不到块级内容、无相关度、受已加载文章限制。
// 本端点把检索能力以「提问 → 命中片段 → 跳转原文」的形态开放给所有持有
// knowledge:read 的用户, 复用与 AI 完全相同的检索路径(scope 化召回 + RRF 融合)。
//
// 权限安全性: 检索实现按请求者身份生成可见性谓词(与 AI 链路同一 ScopeResolver),
// 用户只能搜到自己有权看的文章; 与 retrieval-preview 不同, 本端点不接受
// scope_user_id, 也不回传融合分解/prompt 等内部归因字段。

// KnowledgeSearcher 由 AI 检索链路的实现方注入(agentic 工具集)。
// 与 RAGEvaluator/RetrievalPreviewer 同一范式: knowledge 不反向依赖 ai 引擎。
// 实现必须走线上检索路径(含留痕与缓存), 使「零结果率」等可观测指标覆盖用户搜索。
type KnowledgeSearcher interface {
	SearchForUI(ctx context.Context, query string, limit int, categories []string) ([]map[string]any, error)
}

// SetKnowledgeSearcher 注入用户搜索实现(nil = 未接 AI 引擎, 端点返回 501,
// 前端回退为列表页本地过滤)。
func (s *Service) SetKnowledgeSearcher(x KnowledgeSearcher) { s.knowledgeSearch = x }

// KnowledgeSearchResult 用户搜索响应。
type KnowledgeSearchResult struct {
	Query  string           `json:"query"`
	Took   int              `json:"took_ms"`
	Items  []map[string]any `json:"items"`
	Total  int              `json:"total"`
	Source string           `json:"source"` // semantic(检索引擎) | none(未接入引擎)
}

const (
	kbSearchMaxQueryLen = 200
	kbSearchMaxLimit    = 20
)

// handleKnowledgeSearch GET /api/v1/knowledge/search?q=&limit=&cat=
func (s *Service) handleKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeSearch == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "knowledge search requires the AI retrieval engine")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httputil.WriteError(w, http.StatusBadRequest, "query required")
		return
	}
	if len([]rune(q)) > kbSearchMaxQueryLen {
		q = string([]rune(q)[:kbSearchMaxQueryLen])
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10
	}
	if limit > kbSearchMaxLimit {
		limit = kbSearchMaxLimit
	}
	var cats []string
	if c := strings.TrimSpace(r.URL.Query().Get("cat")); c != "" {
		cats = []string{c}
	}
	items, err := s.knowledgeSearch.SearchForUI(r.Context(), q, limit, cats)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	httputil.WriteJSON(w, http.StatusOK, KnowledgeSearchResult{
		Query: q, Items: items, Total: len(items), Source: "semantic",
	})
}
