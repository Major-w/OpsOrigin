package knowledge

import (
	"context"
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
)

// —— 检索测试台(迭代一 · 批次 4) ——
//
// 为什么需要它: 检索是"沉默"的 —— 一次提问没命中, 用户只看到「知识库里没有相关内容」。
// 但真实原因可能是四件完全不同的事:
//  1. 库里确实没有 → 要补内容;
//  2. 有内容但被分块切碎了 / 标题结构没进块 → 要调分块策略;
//  3. 向量模型对这类中文问法不敏感 → 要换模型或加中文分词;
//  4. 内容存在且能召回, 只是当前提问者无权看见 → 要调权限/归属。
//
// 四者的处置方式互斥, 而线上只给一个空结果。检索测试台把一次检索完整摊开:
// 每条召回路径各命中什么、RRF 与 rerank 怎么合成、ACL 挡掉多少候选、最终进 prompt 的是哪几段文字。
//
// 实现必须跑在**真实检索路径**上(agentic.searchKnowledgeExplained), 否则测的是"另一套检索",
// 对线上零解释力 —— 与 RAGEvaluator 同一条理由。

// RetrievalPreviewRequest 测试台请求。
type RetrievalPreviewRequest struct {
	Query      string   `json:"query"`
	K          int      `json:"k"`          // top-k, 1..20, 默认 5
	Categories []string `json:"categories"` // 可选, 限定知识分类
	// Mode 限定召回路径: ""/"rrf"(默认, 双路+兜底) | "vector" | "keyword" | "fallback"。
	// 非法值由实现层退回 rrf。
	Mode string `json:"mode"`
	// ScopeUserID 以指定用户身份检索(专门用于验证 ACL: 同一句话对不同人应该召回不同结果)。
	// 空 = 用请求者本人身份。属敏感能力(可窥探他人的可见范围), 故端点收敛到 knowledge:manage。
	ScopeUserID string `json:"scope_user_id"`
}

// RetrievalRouteHit 单路召回的一条命中。
type RetrievalRouteHit struct {
	Rank       int     `json:"rank"`
	ArticleID  string  `json:"article_id"`
	Title      string  `json:"title"`
	ChunkID    string  `json:"chunk_id"`
	Heading    string  `json:"heading"`
	Source     string  `json:"source"`
	ChunkIndex int     `json:"chunk_index"`
	Distance   float64 `json:"distance"`
	Preview    string  `json:"preview"`
}

// RetrievalRoute 一路召回的轨迹(Count=0 且 Skipped 非空 = 该路本次未执行, Skipped 说明原因)。
type RetrievalRoute struct {
	Route   string              `json:"route"`
	Count   int                 `json:"count"`
	Skipped string              `json:"skipped,omitempty"`
	Items   []RetrievalRouteHit `json:"items"`
}

// RetrievalKeyword 关键词路的两个子查询分开呈现: tsvector 与 ILIKE 是两条独立 SQL
// (合成一条 OR 会让优化器放弃索引), 命中集不同 —— 合并展示就看不到「中文其实只靠 ILIKE」。
type RetrievalKeyword struct {
	TSVector RetrievalRoute `json:"tsvector"`
	ILIKE    RetrievalRoute `json:"ilike"`
}

// RetrievalFusionRow 融合后单篇文章的分数分解。
type RetrievalFusionRow struct {
	Rank      int     `json:"rank"`
	ArticleID string  `json:"article_id"`
	Title     string  `json:"title"`
	ChunkID   string  `json:"chunk_id"`
	RRFScore  float64 `json:"rrf_score"`
	SimPart   float64 `json:"sim_part"`
	KwPart    float64 `json:"kw_part"`
	TitlePart float64 `json:"title_part"`
	// FeedbackPart 反馈降权(≤0): 用户点踩落到该文章的净踩计票扣分; 未启用闭环恒为 0。
	FeedbackPart float64 `json:"feedback_part"`
	FinalScore   float64 `json:"final_score"`
	BestDist     float64 `json:"best_distance"`
	KwHit        bool    `json:"kw_hit"`
	TitleHit     bool    `json:"title_hit"`
	MatchCount   int     `json:"match_count"`
}

// RetrievalPreviewReport 一次检索的完整归因。
type RetrievalPreviewReport struct {
	Query     string  `json:"query"`
	Mode      string  `json:"mode"`
	Route     string  `json:"route"` // rrf | fallback | cache
	LatencyMS float64 `json:"latency_ms"`
	// Scoped 谓词是否按身份生成。false = 退化为「仅公开」, 必须显式告知:
	// 否则「查不到团队文档」会被误判成检索缺陷。
	Scoped        bool                 `json:"scoped"`
	ScopeKey      string               `json:"scope_key"` // 可见范围短哈希(不含明文身份)
	Vector        RetrievalRoute       `json:"vector"`
	Keyword       RetrievalKeyword     `json:"keyword"`
	Fusion        []RetrievalFusionRow `json:"fusion"`
	Items         []map[string]any     `json:"items"`
	FilteredByACL int                  `json:"filtered_by_acl"`
	Degraded      map[string]bool      `json:"degraded"`
	// PromptPreview 与线上 RAGContext 逐字一致的注入片段(同样 limit=3/snippet=600)。
	PromptPreview string           `json:"prompt_preview"`
	PromptItems   []map[string]any `json:"prompt_items"`
	Warnings      []string         `json:"warnings"`
}

// RetrievalPreviewer 由 AI 检索链路的实现方注入(agentic 工具集)。
//
// 用接口而非直接依赖, 是为了让 knowledge 模块不反向依赖 ai 引擎 —— 与 RAGEvaluator 同一范式。
// 检索测试台也不落库: 它是人工探测, 写进留痕会污染「线上零结果率」这类基线,
// 故实现方在内部强制 NoLog(见 agentic.RetrieveOptions)。
type RetrievalPreviewer interface {
	PreviewRetrieval(ctx context.Context, req RetrievalPreviewRequest) (*RetrievalPreviewReport, error)
}

// SetRetrievalPreviewer 注入检索测试台实现(nil = 未启用, 端点返回 501)。
func (s *Service) SetRetrievalPreviewer(p RetrievalPreviewer) { s.retrievalPreview = p }

// RetrievalPreviewEnabled 是否已接入测试台(供前端决定是否展示入口)。
func (s *Service) RetrievalPreviewEnabled() bool { return s.retrievalPreview != nil }

// NormalizeRetrievalPreviewRequest 收敛测试台参数(k 边界、去空白), 供实现方复用同一套边界。
func NormalizeRetrievalPreviewRequest(req RetrievalPreviewRequest) RetrievalPreviewRequest {
	req.Query = strings.TrimSpace(req.Query)
	req.ScopeUserID = strings.TrimSpace(req.ScopeUserID)
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	switch req.Mode {
	case "vector", "keyword", "fallback", "rrf":
	default:
		req.Mode = "rrf"
	}
	if req.K <= 0 {
		req.K = 5
	} else if req.K > 20 {
		req.K = 20
	}
	cats := make([]string, 0, len(req.Categories))
	for _, c := range req.Categories {
		if c = strings.TrimSpace(c); c != "" {
			cats = append(cats, c)
		}
	}
	req.Categories = cats
	return req
}

// handleRetrievalPreview 跑一次可解释检索。
// POST /api/v1/knowledge/retrieval-preview  body: {"query":"...","k":5,"mode":"rrf"}
func (s *Service) handleRetrievalPreview(w http.ResponseWriter, r *http.Request) {
	if s.retrievalPreview == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "retrieval preview requires the AI retrieval engine (vector search enabled)")
		return
	}
	var req RetrievalPreviewRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req = NormalizeRetrievalPreviewRequest(req)
	if req.Query == "" {
		httputil.WriteError(w, http.StatusBadRequest, "query required")
		return
	}
	report, err := s.retrievalPreview.PreviewRetrieval(r.Context(), req)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, report)
}
