package agentic

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// TestPGStoreInspectionRestore 真实库验证 loadFromStore 的恢复链路:
// ListInspections 与 ListInspectionRuns 是否一致返回(定位重启后 runs 丢失问题)。
func TestPGStoreInspectionRestore(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set, skipping real-db test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	s := NewPGStore(db)
	ctx := context.Background()

	inspections, err := s.ListInspections(ctx, "")
	if err != nil {
		t.Fatalf("ListInspections: %v", err)
	}
	t.Logf("ListInspections(\"\") -> %d rows", len(inspections))
	for _, in := range inspections {
		t.Logf("  inspection id=%s tenant=%q name=%s", in.ID, in.TenantID, in.Name)
		runs, rerr := s.ListInspectionRuns(ctx, in.ID, in.TenantID)
		if rerr != nil {
			t.Fatalf("ListInspectionRuns(%s,%q): %v", in.ID, in.TenantID, rerr)
		}
		t.Logf("  -> runs: %d", len(runs))
		for _, r := range runs {
			t.Logf("     run id=%s status=%s findings_len=%d", r.ID, r.Status, len(r.Findings))
		}
	// 再按空 tenant 试一次
	runs2, rerr2 := s.ListInspectionRuns(ctx, in.ID, "")
	if rerr2 != nil {
		t.Fatalf("ListInspectionRuns(%s,\"\"): %v", in.ID, rerr2)
	}
	t.Logf("  -> runs(tenant=\"\"): %d", len(runs2))

	// 直接 SELECT run 并尝试 Unmarshal, 定位 json 解析是否被吞
	var body []byte
	if err := db.QueryRowContext(ctx,
		`SELECT run FROM ai_inspection_runs WHERE inspection_id=$1 AND tenant_id=$2 LIMIT 1`,
		in.ID, in.TenantID).Scan(&body); err != nil {
		t.Fatalf("direct SELECT run: %v", err)
	}
	t.Logf("  direct run bytes: %d", len(body))
	r := &InspectionRun{}
	if err := json.Unmarshal(body, r); err != nil {
		t.Fatalf("json.Unmarshal run: %v\nraw: %s", err, string(body))
	}
	t.Logf("  unmarshal OK: run id=%s status=%s started=%s", r.ID, r.Status, r.StartedAt)
}
}
