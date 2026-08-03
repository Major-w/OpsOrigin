package alert

import (
	"context"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

// memTopology 内存拓扑: parent -> children; 支持 GetParentCI / IsAncestorOf。
type memTopology struct {
	parents map[string]string // ciID -> parentID
}

func (m *memTopology) GetParentCI(_ context.Context, ciID string) (string, error) {
	return m.parents[ciID], nil
}

func (m *memTopology) IsAncestorOf(_ context.Context, ancestorID, descendantID string) (bool, error) {
	cur := descendantID
	for cur != "" {
		if cur == ancestorID {
			return true, nil
		}
		cur = m.parents[cur]
	}
	return false, nil
}

func newTestAlert(ci, rule, sev string, labels map[string]string) *model.AlertEvent {
	return &model.AlertEvent{
		TenantID:   "t1",
		SourceCIID: ci,
		RuleName:   rule,
		Severity:   model.AlertSeverity(sev),
		Title:      rule + " on " + ci,
		Labels:     labels,
	}
}

// TestClusterSameRuleSameCI 同 rule 同 CI 的重复告警应归入同一簇(第二次通知被抑制)。
func TestClusterSameRuleSameCI(t *testing.T) {
	topo := &memTopology{parents: map[string]string{}}
	a := NewAggregator(topo)

	notified := 0
	a.OnNotify(func(_ *model.AlertEvent) { notified++ })

	// 第一条: 新簇, 通知
	al1 := newTestAlert("ci-1", "cpu_high", "warning", map[string]string{"component": "web"})
	_, _ = a.Ingest(context.Background(), al1)
	// 第二条: 同 rule 同 CI -> 归簇, 抑制通知
	al2 := newTestAlert("ci-1", "cpu_high", "warning", map[string]string{"component": "web"})
	_, _ = a.Ingest(context.Background(), al2)

	if notified != 1 {
		t.Fatalf("expected 1 notify (second clustered), got %d", notified)
	}
	if al1.ClusterID == "" || al2.ClusterID == "" {
		t.Fatal("expected both alerts clustered")
	}
	if al1.ClusterID != al2.ClusterID {
		t.Fatalf("expected same cluster, got %s vs %s", al1.ClusterID, al2.ClusterID)
	}
}

// TestClusterLabelsTopology 不同 CI 但同 rule 族 + labels 共享 + 同拓扑域(祖先/后代) -> 聚类。
func TestClusterLabelsTopology(t *testing.T) {
	// 拓扑: app -> node-1 -> node-2(父子链, node-1 是 node-2 祖先)
	topo := &memTopology{parents: map[string]string{"node-1": "app", "node-2": "node-1"}}
	a := NewAggregator(topo)

	notified := 0
	a.OnNotify(func(_ *model.AlertEvent) { notified++ })

	al1 := newTestAlert("node-1", "probe_failure", "warning", map[string]string{"component": "db", "tier": "core"})
	_, _ = a.Ingest(context.Background(), al1)
	// node-2 同族 probe_failure + labels 共享 component/tier + 同父 app -> 聚入 al1 簇
	al2 := newTestAlert("node-2", "probe_failure", "warning", map[string]string{"component": "db", "tier": "core"})
	_, _ = a.Ingest(context.Background(), al2)

	if notified != 1 {
		t.Fatalf("expected 1 notify (node-2 clustered), got %d", notified)
	}
	if al1.ClusterID != al2.ClusterID {
		t.Fatalf("expected same cluster via topology, got %s vs %s", al1.ClusterID, al2.ClusterID)
	}
}

// TestClusterDifferentDomain 不同拓扑域的相似告警不聚类。
func TestClusterDifferentDomain(t *testing.T) {
	// 两个独立 app 域
	topo := &memTopology{parents: map[string]string{"node-1": "app-a", "node-2": "app-b"}}
	a := NewAggregator(topo)

	notified := 0
	a.OnNotify(func(_ *model.AlertEvent) { notified++ })

	al1 := newTestAlert("node-1", "probe_failure", "warning", map[string]string{"component": "db"})
	_, _ = a.Ingest(context.Background(), al1)
	al2 := newTestAlert("node-2", "probe_failure", "warning", map[string]string{"component": "db"})
	_, _ = a.Ingest(context.Background(), al2)

	if notified != 2 {
		t.Fatalf("expected 2 notifies (different domains), got %d", notified)
	}
}

// TestAnalyzeRootCause 根因分析: 祖先 CI 的告警(更早触发 + 作为后代祖先)应排最前。
func TestAnalyzeRootCause(t *testing.T) {
	topo := &memTopology{parents: map[string]string{"app": "", "node-1": "app", "node-2": "node-1"}}
	a := NewAggregator(topo)

	now := time.Now()
	alerts := []*model.AlertEvent{
		{SourceCIID: "node-2", RuleName: "mem_high", Severity: model.AlertWarning, Title: "mem", TriggeredAt: now.Add(-2 * time.Minute)},
		{SourceCIID: "app", RuleName: "latency_high", Severity: model.AlertCritical, Title: "latency", TriggeredAt: now.Add(-5 * time.Minute)},
		{SourceCIID: "node-1", RuleName: "cpu_high", Severity: model.AlertWarning, Title: "cpu", TriggeredAt: now.Add(-3 * time.Minute)},
	}

	entries := a.AnalyzeRootCause(context.Background(), alerts, 5)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// app: critical(2.0) + 是 node-1/node-2 祖先(+1.0) + 触发最早(时间分高) -> 应排第一
	if entries[0].CIID != "app" {
		t.Fatalf("expected root cause app first, got %s (score %.2f)", entries[0].CIID, entries[0].Score)
	}
	if entries[0].Score < entries[1].Score {
		t.Fatalf("app score %.2f should exceed next %.2f", entries[0].Score, entries[1].Score)
	}
}

// TestClusterStats 聚类统计: GetStats 含 clusters 数。
func TestClusterStats(t *testing.T) {
	topo := &memTopology{parents: map[string]string{}}
	a := NewAggregator(topo)
	_, _ = a.Ingest(context.Background(), newTestAlert("ci-1", "cpu_high", "warning", nil))
	_, _ = a.Ingest(context.Background(), newTestAlert("ci-2", "mem_high", "warning", nil))
	stats := a.GetStats()
	if stats["active_alerts"] != 2 {
		t.Fatalf("expected 2 active alerts, got %v", stats["active_alerts"])
	}
	if stats["clusters"] != 2 {
		t.Fatalf("expected 2 clusters, got %v", stats["clusters"])
	}
}
