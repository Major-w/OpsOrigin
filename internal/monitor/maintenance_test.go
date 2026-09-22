package monitor

import (
	"testing"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

func cloneWindow(w *model.MaintenanceWindow) *model.MaintenanceWindow {
	c := *w
	return &c
}

// TestIsInMaintenance_WindowAndMatch 锁定维护期静默匹配逻辑:
// 窗口内 + 命中可选维度(空维度=全匹配)才静默; 窗口外/禁用/维度不命中则不静默。
func TestIsInMaintenance_WindowAndMatch(t *testing.T) {
	now := time.Now()
	base := &model.MaintenanceWindow{
		ID: "m1", TenantID: "t1", Name: "win",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		Enabled: true,
	}

	cases := []struct {
		name  string
		w     *model.MaintenanceWindow
		alert *model.AlertEvent
		want  bool
	}{
		{"in-window no match", cloneWindow(base), &model.AlertEvent{}, true},
		{"disabled", func() *model.MaintenanceWindow { w := cloneWindow(base); w.Enabled = false; return w }(),
			&model.AlertEvent{}, false},
		{"outside before", func() *model.MaintenanceWindow {
			w := cloneWindow(base); w.StartAt = now.Add(time.Hour); w.EndAt = now.Add(2 * time.Hour); return w
		}(), &model.AlertEvent{}, false},
		{"match instance hit", func() *model.MaintenanceWindow {
			w := cloneWindow(base); w.Match.Instances = []string{"host-a"}; return w
		}(), &model.AlertEvent{Labels: map[string]string{"instance": "host-a"}}, true},
		{"match instance miss", func() *model.MaintenanceWindow {
			w := cloneWindow(base); w.Match.Instances = []string{"host-a"}; return w
		}(), &model.AlertEvent{Labels: map[string]string{"instance": "host-b"}}, false},
		{"match severity hit", func() *model.MaintenanceWindow {
			w := cloneWindow(base); w.Match.Severities = []string{"critical"}; return w
		}(), &model.AlertEvent{Severity: model.AlertCritical}, true},
		{"match ci_id hit", func() *model.MaintenanceWindow {
			w := cloneWindow(base); w.Match.CIIDs = []string{"ci-1"}; return w
		}(), &model.AlertEvent{SourceCIID: "ci-1"}, true},
		{"match source miss", func() *model.MaintenanceWindow {
			w := cloneWindow(base); w.Match.Sources = []string{"zabbix"}; return w
		}(), &model.AlertEvent{Labels: map[string]string{"source": "prometheus"}}, false},
	}

	for _, c := range cases {
		s := &Service{maintenances: []*model.MaintenanceWindow{c.w}}
		if got := s.isInMaintenance(c.alert); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
