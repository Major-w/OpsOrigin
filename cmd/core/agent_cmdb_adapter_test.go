package main

import (
	"testing"

	"github.com/opsorigin/core/internal/agentmgr"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// TestHostToCI 验证 AgentHost → CMDB host CI 的映射正确性（来源必须标 agent，而非 discovery 兜底）。
func TestHostToCI(t *testing.T) {
	h := &agentmgr.AgentHost{
		ID:        "agent-123",
		TenantID:  "default",
		Hostname:  "web-01",
		OSType:    "linux",
		OSVersion: "Ubuntu 22.04",
		CPU:       "8 Cores (Intel)",
		Memory:    "16 GB",
		Disk:      "100G total / 40G used",
		Address:   "10.0.0.5",
		Labels:    map[string]string{"env": "prod"},
	}
	ci := hostToCI(h, "default")

	if ci.TypeID != "host" {
		t.Fatalf("TypeID=%q, want host", ci.TypeID)
	}
	if ci.Attributes["resource_id"] != "host:agent-123" {
		t.Fatalf("resource_id=%v, want host:agent-123", ci.Attributes["resource_id"])
	}
	if ci.Status != model.CIStatusActive {
		t.Fatalf("Status=%q, want active", ci.Status)
	}
	if ci.Name != "web-01" {
		t.Fatalf("Name=%q, want web-01", ci.Name)
	}
	// 每个属性都应显式标 source=agent、confidence=0.9（而非兜底 discovery）。
	want := model.DefaultConfidence(model.SourceAgent)
	for k, m := range ci.AttributeMeta {
		if m.Source != model.SourceAgent {
			t.Errorf("attr %s source=%s, want agent", k, m.Source)
		}
		if m.Confidence != want {
			t.Errorf("attr %s confidence=%v, want %v", k, m.Confidence, want)
		}
	}
	for _, must := range []string{"hostname", "os", "cpu", "memory", "ip", "agent_id"} {
		if _, ok := ci.AttributeMeta[must]; !ok {
			t.Errorf("attr %s missing in AttributeMeta", must)
		}
	}
}

// TestHostToCINameFallback 验证名称兜底：无 hostname 时用 IP，再无则用 agentID。
func TestHostToCINameFallback(t *testing.T) {
	h := &agentmgr.AgentHost{ID: "agent-9", Address: "1.2.3.4"}
	if got := hostToCI(h, "default").Name; got != "1.2.3.4" {
		t.Fatalf("Name=%q, want 1.2.3.4", got)
	}
	h2 := &agentmgr.AgentHost{ID: "agent-0"}
	if got := hostToCI(h2, "default").Name; got != "agent-0" {
		t.Fatalf("Name=%q, want agent-0", got)
	}
}
