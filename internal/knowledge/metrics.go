package knowledge

import (
	"github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// KnowledgeUnindexedArticles 当前仍需重建索引的知识库文章数。
// 口径 = 正文非空且「完全没有正文块」或「有块但缺向量」(见 unindexedArticlePredicate),
// 即尚未进入语义检索、仅靠关键词兜底的文章数。由后台重索引循环周期性刷新(P3-1/P1-3)。
var KnowledgeUnindexedArticles = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "knowledge_unindexed_articles",
	Help: "Number of knowledge-base articles still awaiting embedding (no chunk, or chunk without vector), i.e. not yet semantically searchable (keyword fallback only)",
})

// —— 分块 embedding 成本可观测性(P2-1/P2-2) ——
// 内容哈希复用上线后, 「改一小节 = 整篇重算」的开销应显著下降。
// 通过 reused/computed 两个计数器的比值可以直接判断复用是否生效。

// KnowledgeEmbedReusedChunks 因内容未变化而复用旧向量的块数累计。
var KnowledgeEmbedReusedChunks = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "knowledge_embed_reused_chunks_total",
	Help: "Total number of knowledge chunks whose embedding was reused from the previous version (content hash unchanged)",
})

// KnowledgeEmbedComputedChunks 实际调用 embedding 接口的块数累计。
var KnowledgeEmbedComputedChunks = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "knowledge_embed_computed_chunks_total",
	Help: "Total number of knowledge chunks whose embedding was recomputed (new or changed content)",
})

// KnowledgeExpiredArchived 因超过知识有效期而被自动下架(归档)的文章数(P3-3)。
// 该值持续增长说明知识维护没跟上: 大量知识到期无人续期或修订。
var KnowledgeExpiredArchived = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "knowledge_expired_archived_total",
	Help: "Total number of knowledge articles automatically archived because their expiry date had passed",
})

func init() {
	metrics.Registry.MustRegister(
		KnowledgeUnindexedArticles,
		KnowledgeEmbedReusedChunks,
		KnowledgeEmbedComputedChunks,
		KnowledgeExpiredArchived,
	)
}
