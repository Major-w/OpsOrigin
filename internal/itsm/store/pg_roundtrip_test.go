//go:build integration

package store

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/opsorigin/core/internal/itsm/model"
)

// TestPGStoreTicketFieldRoundTrip 是 memStore/PGStore 字段覆盖事故的端到端守卫：
// 构造「全字段都有值」的工单，写入 PG 再读回，逐字段断言无丢失。
// 任何「model 加字段没同步 SQL / Scan 列错位」都会在这里红。
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN), 未配置时跳过。
func TestPGStoreTicketFieldRoundTrip(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set, skipping itsm pg integration test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("pg not available, skipping: %v", err)
	}
	ctx := context.Background()
	s := NewPGStore(db)
	if err := s.Init(ctx); err != nil {
		t.Fatalf("init itsm tables: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	deadline := now.Add(4 * time.Hour)
	sched := now.Add(24 * time.Hour)
	approved := now.Add(1 * time.Hour)
	holdAt := now.Add(-30 * time.Minute)
	holdUntil := now.Add(2 * time.Hour)
	breached := now.Add(30 * time.Minute)
	assigned := now.Add(10 * time.Minute)

	tk := &model.Ticket{
		TenantID:           "rt-roundtrip", // 独立租户, 不污染其它测试
		Title:              "roundtrip",
		Description:        "desc",
		RiskLevel:          model.RiskCritical,
		Status:             model.StatusExecuting,
		ChangePlan:         "cp",
		RollbackPlan:       "rb",
		CreatedBy:          "u1",
		CreatedByType:      "manual",
		ApprovedBy:         "u2",
		RejectReason:       "",
		CompletedBy:        "u3",
		RolledBackBy:       "u4",
		RollbackReason:     "why",
		CIIDs:              []string{"ci-1", "ci-2"},
		WebhookURL:         "http://example.com/hook",
		Labels:             map[string]string{"k": "v"},
		CustomFields:       map[string]any{"f1": 1.0, "f2": "x"},
		ProcessID:          "proc-1",
		CatalogID:          "cat-1",
		TraceID:            "trace-1",
		ScheduledAt:        &sched,
		DeadlineAt:         &deadline,
		ApprovedAt:         &approved,
		ExecutedAt:         &approved,
		CompletedAt:        &approved,
		RolledBackAt:       &approved,
		Assignee:           "u5",
		AssignedAt:         &assigned,
		SLAWarningNotified: true,
		SLABreached:        true,
		SLABreachedAt:      &breached,
		SLAEscalationLevel: 3,
		Satisfaction:       4,
		OnHold:             true,
		HoldReason:         "waiting",
		HoldUntilAt:        &holdUntil,
		HoldAt:             &holdAt,
		HoldSeconds:        1800,
		ApprovalChain: &model.ApprovalChain{
			Mode:      "parallel",
			Approvers: []string{"a1", "a2", "a3"},
		},
		ApprovalStatus: &model.ApprovalStatus{
			CurrentIndex: 1,
			FinalResult:  "pending",
			Votes: []model.ApprovalVote{
				{Approver: "a1", Approved: true, Comment: "ok", VotedAt: approved},
			},
		},
		RelatedTicketIDs: []string{"tk-9"},
	}
	tk.ID = "rt-roundtrip-" + tk.TenantID // 自填 ID, 避开 Create 不生成 ID 的约定差异
	if err := s.Create(ctx, tk); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.db.Exec(`DELETE FROM itsm_tickets WHERE id = $1`, tk.ID)

	got, err := s.Get(ctx, tk.ID, tk.TenantID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if diffs := diffTickets(tk, got); len(diffs) > 0 {
		for _, d := range diffs {
			t.Errorf("字段丢失/不一致: %s", d)
		}
	}
}

// diffTickets 逐字段比较; time 字段统一 UTC+截断微秒(PG timestamptz 精度)。
func diffTickets(want, got *model.Ticket) []string {
	var diffs []string
	norm := func(v reflect.Value) reflect.Value {
		switch v.Kind() {
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(time.Time{}) {
				tm := v.Interface().(time.Time).UTC().Truncate(time.Microsecond)
				return reflect.ValueOf(tm)
			}
		case reflect.Ptr:
			if !v.IsNil() && v.Type().Elem() == reflect.TypeOf(time.Time{}) {
				tm := v.Interface().(*time.Time).UTC().Truncate(time.Microsecond)
				return reflect.ValueOf(&tm)
			}
		}
		return v
	}
	wv, gv := reflect.ValueOf(*want), reflect.ValueOf(*got)
	typ := wv.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if name == "CreatedAt" || name == "UpdatedAt" {
			continue // 由存储层生成
		}
		w, g := norm(wv.Field(i)), norm(gv.Field(i))
		if !reflect.DeepEqual(w.Interface(), g.Interface()) {
			diffs = append(diffs, name+" want="+formatVal(w)+" got="+formatVal(g))
		}
	}
	return diffs
}

func formatVal(v reflect.Value) string {
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		return "&" + formatVal(v.Elem())
	}
	switch val := v.Interface().(type) {
	case time.Time:
		return val.Format(time.RFC3339Nano)
	default:
		return reflect.TypeOf(v.Interface()).String()
	}
}
