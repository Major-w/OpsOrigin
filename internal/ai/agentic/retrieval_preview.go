package agentic

import (
	"context"
	"fmt"
	"strings"

	"github.com/opsorigin/core/internal/knowledge"
	"github.com/opsorigin/core/pkg/auth"
)

// PreviewRetrieval 实现 knowledge.RetrievalPreviewer: 检索测试台(迭代一 · 批次 4)。
//
// 与 RAGEvaluator 同一范式 —— knowledge 模块不反向依赖 ai 引擎, 由 cmd/core 注入。
// 全部计算都落在 searchKnowledgeExplained(生产同一条路径)上, 这里只做:
//  1. 参数收敛与身份解析;
//  2. 内部归因结构 → API DTO 的映射;
//  3. 与线上逐字一致的 prompt 预览。
func (ts *MCPToolSet) PreviewRetrieval(ctx context.Context, req knowledge.RetrievalPreviewRequest) (*knowledge.RetrievalPreviewReport, error) {
	req = knowledge.NormalizeRetrievalPreviewRequest(req)
	if req.Query == "" {
		return nil, fmt.Errorf("query required")
	}
	if ts.db == nil {
		return nil, fmt.Errorf("knowledge retrieval unavailable: no database")
	}
	tenant := tenantOf(ctx)

	opts := RetrieveOptions{
		Categories: req.Categories,
		Mode:       req.Mode,
		// 诊断必须绕缓存: 缓存命中会把整条归因链路隐藏成「命中 cache」, 测试台就失去意义。
		BypassCache: true,
		// 人工探测不进指标与留痕 —— 否则「线上零结果率」里会混进一堆并非真实用户提出的查询。
		NoLog:      true,
		ComputeACL: true,
	}
	// 「以指定身份检索」: 直接向解析器要该用户的可见范围, 而不是改写 ctx 里的身份
	// (ctx 中的主体是类型化的 principal, 覆写会牵动整条中间件链)。
	// 租户恒取自 ctx —— 该能力用于验证 ACL, 不是跨租户取数的后门。
	if uid := req.ScopeUserID; uid != "" && uid != strings.TrimSpace(auth.UserIDFromContext(ctx)) {
		if ts.scopeResolver == nil {
			return nil, fmt.Errorf("scope resolver not wired: cannot resolve visibility for the given user")
		}
		scope := ts.scopeResolver.ResolveScope(ctx, tenant, uid)
		scope.TenantID = tenant
		opts.Scope = &scope
	}

	// k 由请求决定(1..20); snippetLen 与工具检索一致(1200), 保证展示窗口与模型看到的一致。
	items, ex, err := ts.searchKnowledgeExplained(ctx, req.Query, req.K, kbChunkContextRunes, opts)
	if err != nil {
		return nil, err
	}
	// prompt 预览: 用与 RAGContext 完全相同的参数再跑一次(limit=3/snippet=600),
	// 否则「预览里看到的」与「模型真正读到的」对不上, 预览本身就成了误导。
	prompt, promptItems := ts.PreviewRetrievalPrompt(ctx, req.Query, opts)

	rep := &knowledge.RetrievalPreviewReport{
		Query:         req.Query,
		Mode:          ex.Mode,
		Route:         ex.Route,
		LatencyMS:     ex.LatencyMS,
		Scoped:        ex.Scoped,
		ScopeKey:      ex.ScopeKey,
		Vector:        toRetrievalRoute(ex.Vector),
		Keyword:       knowledge.RetrievalKeyword{TSVector: toRetrievalRoute(ex.Keyword.TSVector), ILIKE: toRetrievalRoute(ex.Keyword.ILIKE)},
		Fusion:        toFusionRows(ex.Fusion),
		Items:         items,
		FilteredByACL: ex.FilteredByACL,
		Degraded:      ex.Degraded,
		PromptPreview: prompt,
		PromptItems:   promptItems,
	}
	rep.Warnings = previewWarnings(rep)
	return rep, nil
}

// SearchForUI 实现 knowledge.KnowledgeSearcher: 用户级知识搜索(面向普通用户的查询入口)。
//
// 走生产检索路径 searchKnowledge(与 knowledge_search 工具同一函数):
//   - 可见性谓词按请求者身份生成(与 AI 链路同一 ScopeResolver), 天然权限安全;
//   - 命中与零结果都进留痕, 「零结果率」等可观测指标因此覆盖用户搜索;
//   - 走缓存(键已含 scope 哈希), 不会跨用户串味。
//
// 与测试台不同: 不做归因(BypassCache=false / NoLog=false / 无 ACL 计数),
// 用户要的是结果, 不是引擎内部。
func (ts *MCPToolSet) SearchForUI(ctx context.Context, query string, limit int, categories []string) ([]map[string]any, error) {
	if ts.db == nil {
		return nil, fmt.Errorf("knowledge retrieval unavailable: no database")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query required")
	}
	if limit <= 0 {
		limit = 10
	}
	// 展示窗口与 RAGContext 注入一致(600): 片段过长会把结果卡撑爆。
	return ts.searchKnowledge(ctx, query, limit, 600, categories)
}

// toRetrievalRoute 内部轨迹 → API DTO。
func toRetrievalRoute(r RouteTrace) knowledge.RetrievalRoute {
	out := knowledge.RetrievalRoute{Route: r.Route, Count: r.Count, Skipped: r.Skipped, Items: []knowledge.RetrievalRouteHit{}}
	for _, it := range r.Items {
		out.Items = append(out.Items, knowledge.RetrievalRouteHit{
			Rank: it.Rank, ArticleID: it.ArticleID, Title: it.Title, ChunkID: it.ChunkID,
			Heading: it.Heading, Source: it.Source, ChunkIndex: it.ChunkIndex,
			Distance: it.Distance, Preview: it.Preview,
		})
	}
	return out
}

// toFusionRows 内部融合明细 → API DTO。
func toFusionRows(rows []FusionItem) []knowledge.RetrievalFusionRow {
	out := make([]knowledge.RetrievalFusionRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, knowledge.RetrievalFusionRow{
			Rank: r.Rank, ArticleID: r.ArticleID, Title: r.Title, ChunkID: r.ChunkID,
			RRFScore: r.RRFScore, SimPart: r.SimPart, KwPart: r.KwPart, TitlePart: r.TitlePart,
			FeedbackPart: r.FeedbackPart,
			FinalScore:   r.FinalScore, BestDist: r.BestDist,
			KwHit: r.KwHit, TitleHit: r.TitleHit, MatchCount: r.MatchCount,
		})
	}
	return out
}

// previewWarnings 把"需要人工注意的事实"显式说出来。
//
// 测试台的价值不在展示数据, 而在避免误判: 一个空结果配上「ACL 过滤 3 篇」和
// 「未按身份过滤」是完全不同的结论, 但原始数据长得一样。这里把易被忽略的事实顶到最前面。
func previewWarnings(rep *knowledge.RetrievalPreviewReport) []string {
	out := []string{}
	if !rep.Scoped {
		out = append(out, "本次未按身份过滤(谓词退化为「仅公开」): 未认证或未解析出可见范围, 团队/个人私有文章一律不参与召回。")
	}
	if rep.FilteredByACL > 0 {
		out = append(out, fmt.Sprintf("有 %d 个候选块因可见性被过滤 —— 检索不到不等于库里没有。", rep.FilteredByACL))
	}
	if rep.Degraded["halfvec"] {
		out = append(out, "halfvec 表达式不可用, 向量路已降级为原生 vector 距离(不会走 HNSW 索引, 延迟会显著上升)。")
	}
	if rep.Degraded["tsvector"] {
		out = append(out, "tsvector 全文检索不可用, 英文/带空格词元的关键词召回已降级为 ILIKE。")
	}
	if len(rep.Items) == 0 {
		switch {
		case rep.FilteredByACL > 0:
			out = append(out, "零结果且存在 ACL 过滤: 优先核对可见范围/归属团队, 而不是急着补内容。")
		default:
			out = append(out, "零结果且未见 ACL 过滤: 更可能是内容缺失、分块把答案切碎, 或问法与文档用词差异过大。")
		}
	}
	return out
}
