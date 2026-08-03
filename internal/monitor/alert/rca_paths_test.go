package alert

import (
	"context"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

func newAlert(ci, rule, title string, sev model.AlertSeverity, t time.Time) *model.AlertEvent {
	return &model.AlertEvent{
		SourceCIID:  ci,
		RuleName:    rule,
		Title:       title,
		Severity:    sev,
		TriggeredAt: t,
	}
}

// TestRCAPathsChain 拓扑链 db -> app -> web, 告警依次触发:
// db 最早触发且是共同祖先 -> 根因 db, 路径 [db, app, web]。
func TestRCAPathsChain(t *testing.T) {
	topo := &memTopology{parents: map[string]string{"app": "db", "web": "app"}}
	a := NewAggregator(topo)
	base := time.Now().Add(-10 * time.Minute)
	alerts := []*model.AlertEvent{
		newAlert("web", "probe.fail", "web 不可达", model.AlertWarning, base.Add(3*time.Minute)),
		newAlert("app", "probe.fail", "app 慢", model.AlertWarning, base.Add(2*time.Minute)),
		newAlert("db", "db.conn", "db 连接数爆满", model.AlertCritical, base),
	}
	paths := a.AnalyzeRootCausePaths(context.Background(), alerts)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	p := paths[0]
	if p.RootCIID != "db" {
		t.Fatalf("root = %s, want db", p.RootCIID)
	}
	if p.AffectedCount != 3 {
		t.Fatalf("affected = %d, want 3", p.AffectedCount)
	}
	if len(p.Path) != 3 || p.Path[0] != "db" || p.Path[2] != "web" {
		t.Fatalf("path = %v, want [db app web]", p.Path)
	}
	if p.RootSeverity != "critical" {
		t.Fatalf("root severity = %s, want critical", p.RootSeverity)
	}
}

// TestRCAPathsIndependent 无拓扑关系(兄弟)的告警各自成路径。
func TestRCAPathsIndependent(t *testing.T) {
	topo := &memTopology{parents: map[string]string{"h1": "app", "h2": "app"}}
	a := NewAggregator(topo)
	base := time.Now().Add(-5 * time.Minute)
	alerts := []*model.AlertEvent{
		newAlert("h1", "probe.fail", "h1 不可达", model.AlertWarning, base),
		newAlert("h2", "probe.fail", "h2 不可达", model.AlertWarning, base.Add(time.Minute)),
	}
	paths := a.AnalyzeRootCausePaths(context.Background(), alerts)
	if len(paths) != 2 {
		t.Fatalf("expected 2 independent paths, got %d", len(paths))
	}
}

// TestRCAPathsNoTopology 无拓扑时同 rule 族 + 30 分钟窗口近似传播。
func TestRCAPathsNoTopology(t *testing.T) {
	a := NewAggregator(nil)
	base := time.Now().Add(-3 * time.Minute)
	alerts := []*model.AlertEvent{
		newAlert("h1", "probe.timeout", "h1 超时", model.AlertWarning, base),
		newAlert("h2", "probe.fail", "h2 失败", model.AlertWarning, base.Add(30*time.Second)),
	}
	paths := a.AnalyzeRootCausePaths(context.Background(), alerts)
	if len(paths) != 1 {
		t.Fatalf("expected 1 approximated path, got %d", len(paths))
	}
	if paths[0].Confidence != 0.5 {
		t.Fatalf("confidence = %v, want 0.5 (approximated)", paths[0].Confidence)
	}
}
