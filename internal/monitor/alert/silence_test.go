package alert

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/monitor/model"
)

// TestIngest_SilenceFuncSuppressesNotify 锁定聚合器静默接线:
// 注入 silenceFunc 返回 true 时, 告警被标记 IsSuppressed 且通知回调不被调用(仍保留活跃记录)。
func TestIngest_SilenceFuncSuppressesNotify(t *testing.T) {
	a := NewAggregator(nil)
	notified := false
	a.OnNotify(func(alert *model.AlertEvent) { notified = true })
	a.SetSilenceFunc(func(alert *model.AlertEvent) bool { return true })

	if _, err := a.Ingest(context.Background(), &model.AlertEvent{
		TenantID: "t1", RuleName: "r", Severity: model.AlertWarning, Title: "x",
	}); err != nil {
		t.Fatal(err)
	}

	alerts := a.GetActiveAlerts()
	if len(alerts) != 1 || !alerts[0].IsSuppressed {
		t.Fatalf("expected exactly one suppressed active alert")
	}
	if notified {
		t.Fatalf("notify must not be called when silenced")
	}
}

// TestIngest_NoSilenceFuncNotifies 反向: 未注入 silenceFunc 时正常通知。
func TestIngest_NoSilenceFuncNotifies(t *testing.T) {
	a := NewAggregator(nil)
	notified := false
	a.OnNotify(func(alert *model.AlertEvent) { notified = true })

	if _, err := a.Ingest(context.Background(), &model.AlertEvent{
		TenantID: "t1", RuleName: "r", Severity: model.AlertWarning, Title: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if !notified {
		t.Fatalf("expected notify when no silence func")
	}
}
