package rls

import (
	"strings"
	"testing"
)

func TestEnforceSelect_NoWhere(t *testing.T) {
	e := New()
	q, err := e.Enforce("SELECT * FROM cmdb_ci", "tenant-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q, "tenant_id = 'tenant-01'") {
		t.Errorf("expected tenant filter, got: %s", q)
	}
}

func TestEnforceSelect_WithWhere(t *testing.T) {
	e := New()
	q, err := e.Enforce("SELECT * FROM cmdb_ci WHERE status = 'active'", "tenant-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q, "AND tenant_id = 'tenant-01'") {
		t.Errorf("expected AND tenant filter, got: %s", q)
	}
}

func TestEnforceSelect_AlreadyHasTenant(t *testing.T) {
	e := New()
	q, err := e.Enforce("SELECT * FROM cmdb_ci WHERE tenant_id = 'tenant-01' AND status = 'active'", "tenant-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(q, "tenant_id") != 1 {
		t.Errorf("expected no duplicate tenant_id, got: %s", q)
	}
}

func TestEnforceUpdate_NoWhere(t *testing.T) {
	e := New()
	_, err := e.Enforce("UPDATE cmdb_ci SET name = 'test'", "tenant-01")
	if err == nil {
		t.Error("expected error for UPDATE without WHERE")
	}
}

func TestEnforceUpdate_WithWhere(t *testing.T) {
	e := New()
	q, err := e.Enforce("UPDATE cmdb_ci SET name = 'test' WHERE id = 'abc'", "tenant-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q, "tenant_id = 'tenant-01'") {
		t.Errorf("expected tenant filter, got: %s", q)
	}
}

func TestEnforceDelete_NoWhere(t *testing.T) {
	e := New()
	_, err := e.Enforce("DELETE FROM cmdb_ci", "tenant-01")
	if err == nil {
		t.Error("expected error for DELETE without WHERE")
	}
}

func TestEnforceInsert(t *testing.T) {
	e := New()
	q, err := e.Enforce("INSERT INTO cmdb_ci (name) VALUES ('test')", "tenant-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q, "'tenant-01'") {
		t.Errorf("expected tenant value in INSERT, got: %s", q)
	}
}

func TestEnforce_EmptyTenant(t *testing.T) {
	e := New()
	_, err := e.Enforce("SELECT * FROM cmdb_ci", "")
	if err == nil {
		t.Error("expected error for empty tenant")
	}
}
