package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestDetectDrifts_SameValuesNoDrift(t *testing.T) {
	existing := &model.CIModel{ID: "a", TenantID: "default", Attributes: map[string]any{"os": "ubuntu", "ip": "1.2.3.4"}}
	discovered := &model.CIModel{Attributes: map[string]any{"os": "ubuntu", "ip": "1.2.3.4"}}
	if drifts := detectDrifts(existing, discovered); len(drifts) != 0 {
		t.Fatalf("相同值不应产生漂移: %v", drifts)
	}
}

func TestDetectDrifts_ValueChangeReported(t *testing.T) {
	existing := &model.CIModel{ID: "a", TenantID: "default", Attributes: map[string]any{"os": "ubuntu-20.04", "version": "1.0"}}
	discovered := &model.CIModel{Attributes: map[string]any{"os": "ubuntu-22.04", "version": "1.0", "kernel": "6.8"}}
	drifts := detectDrifts(existing, discovered)
	if len(drifts) != 1 {
		t.Fatalf("应只有 os 字段漂移, got %d: %v", len(drifts), drifts)
	}
	if drifts[0].Field != "os" || drifts[0].Expected != "ubuntu-20.04" || drifts[0].Actual != "ubuntu-22.04" {
		t.Errorf("漂移详情不符: %+v", drifts[0])
	}
	if drifts[0].CIID != "a" || drifts[0].TenantID != "default" {
		t.Errorf("漂移应携带 CI 上下文: %+v", drifts[0])
	}
}

func TestDetectDrifts_NewFieldNotDrift(t *testing.T) {
	existing := &model.CIModel{ID: "a", TenantID: "default", Attributes: map[string]any{"os": "ubuntu"}}
	discovered := &model.CIModel{Attributes: map[string]any{"os": "ubuntu", "new_field": "x"}}
	if drifts := detectDrifts(existing, discovered); len(drifts) != 0 {
		t.Fatalf("新字段不应算漂移(采集合并处理): %v", drifts)
	}
}

func TestDetectDrifts_TypeInsensitiveCompare(t *testing.T) {
	existing := &model.CIModel{ID: "a", TenantID: "default", Attributes: map[string]any{"port": 8080}}
	discovered := &model.CIModel{Attributes: map[string]any{"port": "8080"}}
	if drifts := detectDrifts(existing, discovered); len(drifts) != 0 {
		t.Fatalf("8080 与 \"8080\" 应视为相同: %v", drifts)
	}
}
