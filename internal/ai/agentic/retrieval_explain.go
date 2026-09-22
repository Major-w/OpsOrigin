package agentic

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// —— 检索测试台: 可解释检索(迭代一 · 批次 4) ——
//
// 目标: 把「这一次检索到底做了什么」完整摊开 —— 每条召回路径各命中什么、RRF 与 rerank 的分数
// 是怎么加出来的、ACL 挡掉多少候选、最终进 prompt 的是哪几段文字。
//
// 铁律: 必须跑在**真实检索路径**上, 不得另写一套 —— 否则测的是"另一套检索", 对线上零解释力
// (与 RAGEvaluator 同理由)。做法是把 searchKnowledge 的核心逻辑提取为本文件的
// searchKnowledgeExplained, 由 searchKnowledge 以默认参数委托: 生产与测试台共用同一份代码,
// 不存在"测试台和线上不一致"的空间。

// kbKeywordNoDistanceExpr 关键词路(tsvector / ILIKE)的占位距离。
//
// knowledgeHit.distance 的契约是「余弦距离, 越小越近; -1 = 本条没有向量距离」(见 tools.go
// 结构体注释), rrfMergeChunksExplained / rerankBonus / FusionItem 三处都用 `>= 0` 判定
// 是否参与相似度加成。关键词路根本没有向量距离, 只能回填 -1。
//
// ⚠️ 这里曾写成 `1.0`: 结果纯关键词命中的文章被当作"距离 1.0"参与 rerank,
// 凭空拿到 0.6×(1−1.0/2)=0.3 的相似度加成(与真正的向量命中同量级), 把只靠 ILIKE
// 命中的文章顶到向量召回之前。排序表面正常, 但"相似度"这一项被凭空算进去了 ——
// 属于静默的结果失真, 且与 RAGEval 的指标口径互相矛盾(那边记 -1)。
const kbKeywordNoDistanceExpr = `-1.0 AS distance`

// RetrieveOptions 一次检索的可选参数。
//
// 生产路径只用到 Categories 一项, 其余字段专供检索测试台/评估等诊断场景。
type RetrieveOptions struct {
	// Categories 限定知识分类(空 = 全库)。
	Categories []string
	// Mode 限定召回路径: ""/"rrf"(默认, 双路 + 兜底) | "vector" | "keyword" | "fallback"。
	// 值非法时退回 rrf —— 参数拼错不该静默换成另一套召回逻辑。
	Mode string
	// BypassCache 跳过检索结果缓存。测试台必须开: 缓存命中会把真实链路整段隐藏。
	BypassCache bool
	// Scope 覆盖本次检索的身份(nil = 用 ctx 里的提问者)。测试台「以指定身份检索」专用;
	// 只能覆盖**用户与组**, 租户永远取自 ctx —— 防止借测试台跨租户取数。
	Scope *VisibilityScope
	// ComputeACL 额外计算 ACL 过滤量(多两条 count 查询)。仅测试台开启。
	ComputeACL bool
	// NoLog 不写检索留痕/指标。测试台必须开: 诊断请求不该污染生产留痕与零结果率。
	NoLog bool
}

// RetrievalTraceItem 单路召回的一条命中。
//
// 只回传命中窗口(preview)而非块全文: 块正文动辄 600 token, 一次三栏对比会把响应体撑到几 MB。
type RetrievalTraceItem struct {
	Rank       int     `json:"rank"`
	ArticleID  string  `json:"article_id"`
	Title      string  `json:"title"`
	ChunkID    string  `json:"chunk_id"`
	Heading    string  `json:"heading"`
	Source     string  `json:"source"`
	ChunkIndex int     `json:"chunk_index"`
	Distance   float64 `json:"distance"` // 余弦距离(越小越近); 关键词路恒为 -1
	Preview    string  `json:"preview"`
}

// RouteTrace 一路召回的轨迹。Count=0 且 Skipped 非空 = 该路本次未执行(含原因)。
type RouteTrace struct {
	Route   string               `json:"route"`
	Count   int                  `json:"count"`
	Skipped string               `json:"skipped,omitempty"`
	Items   []RetrievalTraceItem `json:"items"`
}

// KeywordTrace 关键词路的两个子查询必须分开呈现: tsvector 与 ILIKE 是两条独立 SQL
// (合成一条 OR 会让优化器放弃索引), 命中集并不相同, 合并展示会看不到"中文只走了 ILIKE"这类事实。
type KeywordTrace struct {
	TSVector RouteTrace `json:"tsvector"`
	ILIKE    RouteTrace `json:"ilike"`
}

// FusionItem 融合后单篇文章的分数分解(测试台「融合明细」栏)。
//
// 分数口径与 rrfMergeChunksExplained 逐字对应: 最终分 = RRF 名次分 + rerankBonus,
// 而 bonus = 0.6×相似度 + 0.25×关键词命中 + 0.2×标题命中。分解出来才能回答
// "这篇为什么排第一" —— 是名次贡献还是质量分贡献。
type FusionItem struct {
	Rank         int     `json:"rank"`
	ArticleID    string  `json:"article_id"`
	Title        string  `json:"title"`
	ChunkID      string  `json:"chunk_id"`
	RRFScore     float64 `json:"rrf_score"`
	SimPart      float64 `json:"sim_part"`      // 0.6 × 相似度(无向量命中为 0)
	KwPart       float64 `json:"kw_part"`       // 关键词命中加成
	TitlePart    float64 `json:"title_part"`    // 标题命中加成
	FeedbackPart float64 `json:"feedback_part"` // 反馈降权(≤0; 未启用闭环恒为 0)
	FinalScore   float64 `json:"final_score"`   // RRFScore + 三部分加成 + 反馈降权
	BestDist     float64 `json:"best_distance"`
	KwHit        bool    `json:"kw_hit"`
	TitleHit     bool    `json:"title_hit"`
	MatchCount   int     `json:"match_count"`
}

// RetrievalExplain 一次检索的完整归因。
type RetrievalExplain struct {
	Query     string  `json:"query"`
	Mode      string  `json:"mode"`
	Route     string  `json:"route"` // rrf | fallback | cache
	LatencyMS float64 `json:"latency_ms"`
	// Scoped 谓词是否按身份生成(false = 退化为「仅公开」, 即未认证/未解析出身份)。
	// 测试台必须把这个事实显式告诉使用者, 否则「查不到团队文档」会被误判为检索缺陷。
	Scoped   bool             `json:"scoped"`
	ScopeKey string           `json:"scope_key"` // 可见范围短哈希(不含明文身份, 可安全展示)
	Vector   RouteTrace       `json:"vector"`
	Keyword  KeywordTrace     `json:"keyword"`
	Fusion   []FusionItem     `json:"fusion"`
	Final    []map[string]any `json:"final"`
	// FilteredByACL 仅 ComputeACL 模式计算: 摘掉可见性约束后的候选数 − 实际候选数。
	FilteredByACL int             `json:"filtered_by_acl"`
	Degraded      map[string]bool `json:"degraded"`
}

// NormalizeRetrieveMode 收敛检索模式。未知值一律退回 rrf。
func NormalizeRetrieveMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "vector":
		return "vector"
	case "keyword":
		return "keyword"
	case "fallback":
		return "fallback"
	default:
		return "rrf"
	}
}

// searchKnowledgeExplained 是生产检索的唯一实现: 双路召回 + RRF 融合 + 文章聚合,
// 同时按需回传完整归因。searchKnowledge 是它的默认参数包装。
//
// limits: limit = 返回文章数; snippetLen = 单块回传窗口(rune)。
func (ts *MCPToolSet) searchKnowledgeExplained(ctx context.Context, query string, limit, snippetLen int, opts RetrieveOptions) ([]map[string]any, *RetrievalExplain, error) {
	started := time.Now()
	if limit <= 0 {
		limit = 5
	}
	mode := NormalizeRetrieveMode(opts.Mode)
	tenant := tenantOf(ctx)
	// 可见性范围: 按提问者身份解析。零值(未认证/解析失败) → 谓词退化为「仅公开」, fail-closed。
	scope := ts.resolveScope(ctx)
	if opts.Scope != nil {
		// 租户只信 ctx: 覆盖身份是为了验证 ACL, 不是为了跨租户取数。
		scope = *opts.Scope
		scope.TenantID = tenant
		if scope.IsZero() {
			scope = VisibilityScope{} // 覆盖成空身份 → 退回零值而非半残值
		}
	}

	ex := &RetrievalExplain{
		Query: query, Mode: mode, Scoped: !scope.IsZero(), ScopeKey: scopeCacheKey(scope),
		// 降级标记在真库上可能被本次调用翻转, 故在返回前统一重算(finalizeExplain)。
		Degraded: map[string]bool{},
	}

	// 分类过滤子句: 三条召回 SQL 的参数位不同, 故各自定义(向量路 $5, 关键词路 $4)。
	// 无论是否限定分类, 都必须带上可见性谓词 —— 否则草稿/过期/越权文章会被召回。
	vecCat, kwCat := "", ""
	var catArg []string
	if len(opts.Categories) > 0 {
		vecCat = " AND a.category = ANY($5)"
		kwCat = " AND a.category = ANY($4)"
		catArg = opts.Categories
	}

	// 缓存键必须带 scopeKey: 谓词已按身份变化, 不带则同租户不同用户互相复用结果(=越权泄漏)。
	cacheKey := ts.searchCacheKey(tenant, query, mode, scopeCacheKey(scope), limit, snippetLen, opts.Categories)
	if !opts.BypassCache {
		if v, ok := ts.searchCache.get(cacheKey); ok {
			AIKnowledgeCacheHits.Inc()
			items := v.([]map[string]any)
			ex.Route = "cache"
			ex.Final = items
			ts.recordSearch(ctx, searchTrace{mode: "cache", query: query, tenant: tenant, categories: opts.Categories, started: started, resultCount: len(items), topScore: topScoreOf(items)}, opts.NoLog)
			return items, finalizeExplain(ex, started), nil
		}
	}

	wantVector := mode == "rrf" || mode == "vector"
	wantKeyword := mode == "rrf" || mode == "keyword"
	// fallback 仅在 rrf 模式(双路全空)或显式 fallback 模式下执行。
	wantFallback := mode == "rrf" || mode == "fallback"

	topN := limit * 3 // 召回上限: 每路取 3 倍, 融合后截断到 limit
	var vecHits, kwHits []knowledgeHit

	// —— 路 1: 块级向量检索(带维度过滤) ——
	switch {
	case !wantVector:
		ex.Vector = RouteTrace{Route: "vector", Skipped: "本次仅运行 " + mode + " 路径"}
	case ts.embedder == nil:
		ex.Vector = RouteTrace{Route: "vector", Skipped: "未配置 embedding 模型"}
	default:
		qv, embedErr := ts.cachedEmbed(ctx, query)
		if embedErr != nil || len(qv) == 0 {
			reason := "查询向量化失败"
			if embedErr != nil {
				reason += ": " + embedErr.Error()
			}
			ex.Vector = RouteTrace{Route: "vector", Skipped: reason}
		} else {
			lit := vectorLiteral(qv)
			// 排序表达式必须与 HNSW 索引的表达式完全一致才会走索引; 见 vectorOrderExpr。
			order := vectorOrderExpr(len(qv))
			// $1 查询向量, $2 召回数, $3 租户, $4 向量维度, [$5 分类], 其后为 scope 参数(见 newScopeArgs)
			qArgs := []any{lit, topN, tenant, len(qv)}
			if catArg != nil {
				qArgs = append(qArgs, catArg)
			}
			sa := newScopeArgs(scope, qArgs...)
			if rows, qerr := ts.db.QueryContext(ctx,
				`SELECT c.article_id, a.title, c.heading, c.content,
				        c.id AS chunk_id, COALESCE(c.source,'article') AS source,
				        c.chunk_index, `+order+` AS distance
				 FROM knowledge_chunks c
				 JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
				 WHERE c.tenant_id=$3 AND c.embedding IS NOT NULL
				   AND vector_dims(c.embedding) = $4`+vecCat+sa.Predicate()+`
				 ORDER BY `+order+` LIMIT $2`,
				sa.Args()...); qerr == nil {
				vecHits, _ = scanChunkHits(rows)
			} else {
				// halfvec 不可用(pgvector < 0.7)或老库缺列: 一次性降级为原生 vector 表达式。
				if !kbHalfvecUnavailable.Swap(true) {
					kbHalfvecWarnOnce.Do(func() {
						slog.Warn("knowledge: halfvec expression unavailable, falling back to native vector distance", "err", qerr)
					})
					// 立即用降级表达式重试一次, 避免本次检索直接丢掉向量路
					if rows, rerr := ts.db.QueryContext(ctx,
						`SELECT c.article_id, a.title, c.heading, c.content,
						        c.id AS chunk_id, COALESCE(c.source,'article') AS source,
						        c.chunk_index, c.embedding <=> $1::vector AS distance
						 FROM knowledge_chunks c
						 JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
						 WHERE c.tenant_id=$3 AND c.embedding IS NOT NULL
						   AND vector_dims(c.embedding) = $4`+vecCat+sa.Predicate()+`
						 ORDER BY c.embedding <=> $1::vector LIMIT $2`,
						sa.Args()...); rerr == nil {
						vecHits, _ = scanChunkHits(rows)
					} else {
						slog.Warn("knowledge: chunk vector search failed", "err", rerr)
						ex.Vector = RouteTrace{Route: "vector", Skipped: "向量检索失败: " + rerr.Error()}
						break
					}
				} else {
					slog.Warn("knowledge: chunk vector search failed", "err", qerr)
					ex.Vector = RouteTrace{Route: "vector", Skipped: "向量检索失败: " + qerr.Error()}
					break
				}
			}
			ex.Vector = RouteTrace{Route: "vector", Count: len(vecHits), Items: traceItems(vecHits, query, snippetLen)}
		}
	}

	// —— 路 2: 块级关键词检索 ——
	// 拆成 tsvector 与 ILIKE 两条独立查询: 合并成一条 OR 会让优化器放弃索引退化成全表扫描。
	if !wantKeyword {
		ex.Keyword = KeywordTrace{
			TSVector: RouteTrace{Route: "tsvector", Skipped: "本次仅运行 " + mode + " 路径"},
			ILIKE:    RouteTrace{Route: "ilike", Skipped: "本次仅运行 " + mode + " 路径"},
		}
	} else {
		// 2a tsvector(英文/带空格词元, GIN 索引)
		if kbTSVUnavailable.Load() {
			ex.Keyword.TSVector = RouteTrace{Route: "tsvector", Skipped: "tsvector 不可用(库缺 content_tsv 列或扩展), 已降级为 ILIKE"}
		} else {
			// $1 查询文本, $2 召回数, $3 租户, [$4 分类], 其后为 scope 参数(见 newScopeArgs)
			tArgs := []any{query, topN, tenant}
			if catArg != nil {
				tArgs = append(tArgs, catArg)
			}
			tsa := newScopeArgs(scope, tArgs...)
			if rows, qerr := ts.db.QueryContext(ctx,
				`SELECT c.article_id, a.title, c.heading, c.content,
				        c.id AS chunk_id, COALESCE(c.source,'article') AS source,
				        c.chunk_index, `+kbKeywordNoDistanceExpr+`
				 FROM knowledge_chunks c
				 JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
				 WHERE c.tenant_id=$3 AND c.content_tsv @@ plainto_tsquery('simple', $1)`+kwCat+tsa.Predicate()+`
				 ORDER BY ts_rank(c.content_tsv, plainto_tsquery('simple', $1)) DESC LIMIT $2`,
				tsa.Args()...); qerr == nil {
				if tsv, serr := scanChunkHits(rows); serr == nil {
					kwHits = append(kwHits, tsv...)
					ex.Keyword.TSVector = RouteTrace{Route: "tsvector", Count: len(tsv), Items: traceItems(tsv, query, snippetLen)}
				} else {
					ex.Keyword.TSVector = RouteTrace{Route: "tsvector", Skipped: "结果扫描失败: " + serr.Error()}
				}
			} else {
				// 老库无 content_tsv 列(或未装扩展): 只告警一次并永久降级到 ILIKE, 避免每次检索都打日志。
				kbTSVUnavailable.Store(true)
				kbTSVWarnOnce.Do(func() {
					slog.Warn("knowledge: tsvector keyword search unavailable, falling back to ILIKE", "err", qerr)
				})
				ex.Keyword.TSVector = RouteTrace{Route: "tsvector", Skipped: "tsvector 检索失败: " + qerr.Error()}
			}
		}
		// 2b ILIKE(中文子串匹配; pg_trgm 索引可用时走 GIN 扫描而非全表)
		iArgs := []any{"%" + query + "%", topN, tenant}
		if catArg != nil {
			iArgs = append(iArgs, catArg)
		}
		isa := newScopeArgs(scope, iArgs...)
		if rows, qerr := ts.db.QueryContext(ctx,
			`SELECT c.article_id, a.title, c.heading, c.content,
			        c.id AS chunk_id, COALESCE(c.source,'article') AS source,
			        c.chunk_index, `+kbKeywordNoDistanceExpr+`
			 FROM knowledge_chunks c
			 JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
			 WHERE c.tenant_id=$3 AND (a.title ILIKE $1 OR c.content ILIKE $1)`+kwCat+isa.Predicate()+` LIMIT $2`,
			isa.Args()...); qerr == nil {
			if like, serr := scanChunkHits(rows); serr == nil {
				kwHits = append(kwHits, like...)
				ex.Keyword.ILIKE = RouteTrace{Route: "ilike", Count: len(like), Items: traceItems(like, query, snippetLen)}
			} else {
				ex.Keyword.ILIKE = RouteTrace{Route: "ilike", Skipped: "结果扫描失败: " + serr.Error()}
			}
		} else {
			slog.Warn("knowledge: chunk keyword search failed", "err", qerr)
			ex.Keyword.ILIKE = RouteTrace{Route: "ilike", Skipped: "关键词检索失败: " + qerr.Error()}
		}
	}

	// —— 全路全空: 回退文章级 ILIKE(兼容未升级/未 reindex 的存量数据) ——
	if wantFallback && len(vecHits) == 0 && len(kwHits) == 0 {
		if items, err := ts.searchKnowledgeFallback(ctx, query, limit, snippetLen, opts.Categories); err == nil {
			if !opts.BypassCache {
				ts.searchCache.set(cacheKey, items)
			}
			ts.recordSearch(ctx, searchTrace{mode: "fallback", query: query, tenant: tenant, categories: opts.Categories, started: started, resultCount: len(items), topScore: topScoreOf(items)}, opts.NoLog)
			ex.Route = "fallback"
			ex.Final = items
			return items, finalizeExplain(ex, started), nil
		}
	}

	items, fusion := ts.rrfMergeChunksExplained(ctx, vecHits, kwHits, limit, query, snippetLen, ts.articlePenaltiesFor(ctx, tenant))
	if !opts.BypassCache {
		ts.searchCache.set(cacheKey, items)
	}
	ts.recordSearch(ctx, searchTrace{
		mode: mode, query: query, tenant: tenant, categories: opts.Categories, started: started,
		vecHits: len(vecHits), kwHits: len(kwHits),
		resultCount: len(items), topScore: topScoreOf(items),
	}, opts.NoLog)
	ex.Route = "rrf"
	ex.Fusion = fusion
	ex.Final = items
	if opts.ComputeACL {
		ex.FilteredByACL = ts.aclFilteredCandidates(ctx, query, tenant, scope, opts.Categories, topN)
	}
	return items, finalizeExplain(ex, started), nil
}

// finalizeExplain 补齐耗时与降级标记。
//
// 降级标记必须在返回前**重算**而非进入时快照: halfvec/tsvector 的不可用标志是进程级竞态变量,
// 本次调用本身就可能把它从 false 翻成 true, 快照会漏报"本次就是降级的那一次"。
func finalizeExplain(ex *RetrievalExplain, started time.Time) *RetrievalExplain {
	ex.LatencyMS = float64(time.Since(started).Microseconds()) / 1000
	if ex.Degraded == nil {
		ex.Degraded = map[string]bool{}
	}
	ex.Degraded["halfvec"] = kbHalfvecUnavailable.Load()
	ex.Degraded["tsvector"] = kbTSVUnavailable.Load()
	if ex.Final == nil {
		ex.Final = []map[string]any{}
	}
	if ex.Fusion == nil {
		ex.Fusion = []FusionItem{}
	}
	return ex
}

// traceItems 把一路命中转成可展示的轨迹项(带命中窗口预览)。
func traceItems(hits []knowledgeHit, query string, snippetLen int) []RetrievalTraceItem {
	out := make([]RetrievalTraceItem, 0, len(hits))
	win := kbChunkContextRunes
	if snippetLen > win {
		win = snippetLen
	}
	for i, h := range hits {
		out = append(out, RetrievalTraceItem{
			Rank: i + 1, ArticleID: h.articleID, Title: h.title, ChunkID: h.chunkID,
			Heading: h.heading, Source: h.source, ChunkIndex: h.chunkIndex,
			Distance: round3(h.distance), Preview: contextWindow(h.content, query, win),
		})
	}
	return out
}

// aclFilteredCandidates 计算「本次可见性约束挡掉了多少候选块」。
//
// 口径: 用与关键词路完全相同的匹配条件跑两次 count(仅取前 topN 个候选, 避免大库全表计数):
//   - 带可见性谓词 → 提问者真正能看到的候选数;
//   - 摘掉可见性谓词(保留状态/过期约束) → 该查询在库里的全部候选数。
//
// 两者之差就是 ACL 的代价。这是测试台最能一眼看懂的数字: 它把"检索不到"从
// 「向量模型不行」和「权限挡住了」这两种完全不同的解释里区分开来。
//
// 只做 best-effort: 任何一次查询失败都返回 0, 绝不让诊断信息本身成为失败原因。
func (ts *MCPToolSet) aclFilteredCandidates(ctx context.Context, query, tenant string, scope VisibilityScope, categories []string, topN int) int {
	if ts.db == nil {
		return 0
	}
	if topN <= 0 {
		topN = 15
	}
	catClause := ""
	var catArg []string
	if len(categories) > 0 {
		catClause = " AND a.category = ANY($4)"
		catArg = categories
	}
	count := func(predicate string, args []any) int {
		rows, err := ts.db.QueryContext(ctx,
			`SELECT count(*) FROM (
			   SELECT 1 FROM knowledge_chunks c
			   JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
			   WHERE c.tenant_id=$3 AND (a.title ILIKE $1 OR c.content ILIKE $1)`+catClause+predicate+` LIMIT $2
			 ) t`, args...)
		if err != nil {
			slog.Warn("knowledge: acl candidate count failed", "err", err)
			return 0
		}
		defer rows.Close()
		var n int
		if rows.Next() {
			if err := rows.Scan(&n); err != nil {
				return 0
			}
		}
		return n
	}

	base := []any{"%" + query + "%", topN, tenant}
	if catArg != nil {
		base = append(base, catArg)
	}
	// 不带可见性: 只保留状态/过期约束 → 库里该查询的全部候选。
	withoutACL := count(kbVisibleStatusPredicate, base)
	sa := newScopeArgs(scope, base...)
	withACL := count(sa.Predicate(), sa.Args())
	if diff := withoutACL - withACL; diff > 0 {
		return diff
	}
	return 0
}
