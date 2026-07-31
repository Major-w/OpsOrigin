package knowledge

import (
	"github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// KnowledgeUnindexedArticles 当前仍未生成 embedding(embedding IS NULL)的知识库文章数,
// 即尚未进入语义检索、仅靠 ILIKE 兜底的文章数。由后台重索引循环周期性刷新(P3-1)。
var KnowledgeUnindexedArticles = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "knowledge_unindexed_articles",
	Help: "Number of knowledge-base articles still awaiting embedding (embedding IS NULL), i.e. not yet semantically searchable (ILIKE fallback only)",
})

func init() {
	metrics.Registry.MustRegister(KnowledgeUnindexedArticles)
}
