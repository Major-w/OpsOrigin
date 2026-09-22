package monitor

import (
	"testing"

	"github.com/opsorigin/core/internal/monitor/model"
)

func TestNormalizeExternal_Alertmanager(t *testing.T) {
	p := externalIngestPayload{
		Alerts: []amAlert{{
			Status:      "firing",
			Labels:      map[string]string{"alertname": "HighCpu", "severity": "warning", "instance": "10.0.1.10"},
			Annotations: map[string]string{"summary": "CPU high"},
		}},
	}
	items := normalizeExternal(p)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	it := items[0]
	if it.status != "firing" || it.ruleName != "HighCpu" || it.sourceCIID != "10.0.1.10" || it.title != "CPU high" {
		t.Fatalf("unexpected item: %+v", it)
	}
	if normalizeSeverity(it.severity) != model.AlertWarning {
		t.Fatalf("severity should be warning, got %s", it.severity)
	}
}

func TestNormalizeExternal_Zabbix(t *testing.T) {
	p := externalIngestPayload{
		Host:     "db-prod-01",
		Trigger:  "CPU load is high",
		Severity: "High",
		Status:   "problem",
		Message:  "load high",
	}
	items := normalizeExternal(p)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	it := items[0]
	if it.sourceCIID != "db-prod-01" || it.ruleName != "CPU load is high" || it.status != "problem" {
		t.Fatalf("unexpected item: %+v", it)
	}
	if normalizeSeverity(it.severity) != model.AlertCritical {
		t.Fatalf("zabbix High should map to critical, got %s", it.severity)
	}
}

func TestNormalizeSeverity(t *testing.T) {
	cases := map[string]model.AlertSeverity{
		"critical": model.AlertCritical,
		"Disaster": model.AlertCritical,
		"high":     model.AlertCritical,
		"warning":  model.AlertWarning,
		"average":  model.AlertWarning,
		"info":     model.AlertInfo,
		"":         model.AlertWarning,
	}
	for in, want := range cases {
		if got := normalizeSeverity(in); got != want {
			t.Fatalf("normalizeSeverity(%q)=%s, want %s", in, got, want)
		}
	}
}
