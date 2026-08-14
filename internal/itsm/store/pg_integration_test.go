//go:build integration

package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/opsorigin/core/internal/itsm/model"
)

// 集成测试: ITSM PGStore 工单完整存取与状态流转。
// 验证 PG 层真实落库正确性(工单/事件/状态更新), 而非内存 mock。
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN), 未配置时跳过。
func TestPGStoreTicketLifecycle(t *testing.T) {
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

	tenant := "default"
	now := time.Now()
	ticket := &model.Ticket{
		ID: uuid.NewString(), TenantID: tenant, Title: "集成测试-磁盘扩容",
		RiskLevel: model.RiskLow, Status: model.StatusPendingReview,
		CreatedBy: "integration-bot", CreatedByType: "manual",
		TraceID: "trace-itsm-integ", CreatedAt: now, UpdatedAt: now,
		CIIDs:  []string{"ci-web-01"},
		Labels: map[string]string{"env": "test"},
		OnHold: false,
	}
	t.Cleanup(func() { _ = s.DeleteComment(ctx, 0, ticket.ID, tenant) })

	// 1. 创建 → 读取
	if err := s.Create(ctx, ticket); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	got, err := s.Get(ctx, ticket.ID, tenant)
	if err != nil {
		t.Fatalf("get ticket: %v", err)
	}
	if got == nil || got.Title != ticket.Title || got.Status != model.StatusPendingReview {
		t.Fatalf("ticket roundtrip mismatch: %+v", got)
	}
	if got.TraceID != "trace-itsm-integ" {
		t.Fatalf("trace_id 未落库: %s", got.TraceID)
	}

	// 2. 状态流转: 审批通过 → 执行中 → 成功(每次基于最新落库状态, 避免覆盖前序字段)
	refresh := func() *model.Ticket {
		cur, err := s.Get(ctx, ticket.ID, tenant)
		if err != nil {
			t.Fatalf("get ticket: %v", err)
		}
		return cur
	}

	approved := time.Now()
	step := refresh()
	step.Status = model.StatusApproved
	step.ApprovedBy = "reviewer-1"
	step.ApprovedAt = &approved
	step.UpdatedAt = time.Now()
	if err := s.Update(ctx, step); err != nil {
		t.Fatalf("update ticket: %v", err)
	}

	executed := time.Now()
	step = refresh()
	step.Status = model.StatusExecuting
	step.ExecutedAt = &executed
	step.UpdatedAt = time.Now()
	if err := s.Update(ctx, step); err != nil {
		t.Fatalf("update to executing: %v", err)
	}

	completed := time.Now()
	step = refresh()
	step.Status = model.StatusSucceeded
	step.CompletedBy = "integration-bot"
	step.CompletedAt = &completed
	step.UpdatedAt = time.Now()
	if err := s.Update(ctx, step); err != nil {
		t.Fatalf("update to succeeded: %v", err)
	}

	final, err := s.Get(ctx, ticket.ID, tenant)
	if err != nil {
		t.Fatalf("get final ticket: %v", err)
	}
	if final.Status != model.StatusSucceeded {
		t.Fatalf("status 流转未落库, want %s got %s", model.StatusSucceeded, final.Status)
	}
	if final.ApprovedBy != "reviewer-1" {
		t.Fatalf("approved_by 未落库: %s", final.ApprovedBy)
	}
	t.Logf("ticket lifecycle ok: %s -> %s", final.Title, final.Status)

	// 3. 事件(审计轨迹)落库
	ev := &model.TicketEvent{
		TicketID:  ticket.ID,
		ToStatus:  model.StatusApproved,
		ChangedBy: "reviewer-1",
		Comment:   "pending_review -> approved",
		CreatedAt: time.Now(),
	}
	if err := s.AddEvent(ctx, ev); err != nil {
		t.Fatalf("add event: %v", err)
	}
	events, err := s.GetEvents(ctx, ticket.ID, tenant)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) < 1 {
		t.Fatal("事件应落库")
	}

	// 4. 列表查询
	list, err := s.List(ctx, tenant, model.StatusSucceeded)
	if err != nil {
		t.Fatalf("list tickets: %v", err)
	}
	found := false
	for _, tk := range list {
		if tk.ID == ticket.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("按状态列表应包含该工单")
	}

	// 5. 统计
	stats, err := s.GetStats(ctx, tenant)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	t.Logf("itsm stats by_creator=%v", stats.ByCreator)
}
