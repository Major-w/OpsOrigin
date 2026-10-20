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
)

// TestPGStoreNullableDescriptionScan 直击「可空列直扫裸 string」的线上故障形态：
// 用原生 SQL 写入 description 为 NULL 的工单（Create 永远写字符串，走不到这条路径），
// 再调用两个后台巡检用的查询。修复前这里会报
// `sql: Scan error on column index 3, name "description": converting NULL to string is unsupported`，
// 导致 SLA 巡检与 OLA 超时升级整体失效。
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN)，未配置时跳过。
func TestPGStoreNullableDescriptionScan(t *testing.T) {
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

	const tenant = "default"
	now := time.Now()
	activeID := uuid.NewString() // 在途 + 有 deadline：进 ListActiveTicketsWithDeadline
	holdID := uuid.NewString()   // 挂起到期：进 ListOnHoldTicketsPastDue
	t.Cleanup(func() {
		db.ExecContext(ctx, `DELETE FROM itsm_tickets WHERE id IN ($1, $2)`, activeID, holdID)
	})

	// description 显式写 NULL；其余 NOT NULL 列（title/risk_level/status/created_by/
	// created_by_type/trace_id/created_at/updated_at）必须给值。
	if _, err := db.ExecContext(ctx, `
		INSERT INTO itsm_tickets (id, tenant_id, title, description, risk_level, status,
			created_by, created_by_type, trace_id,
			deadline_at, created_at, updated_at, on_hold, hold_until_at)
		VALUES ($1, $2, 'NULL描述-在途', NULL, 'medium', 'approved',
			'tester', 'user', $5, $3, $4, $4, false, NULL)`,
		activeID, tenant, now.Add(2*time.Hour), now, activeID); err != nil {
		t.Fatalf("insert active ticket with NULL description: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO itsm_tickets (id, tenant_id, title, description, risk_level, status,
			created_by, created_by_type, trace_id,
			created_at, updated_at, on_hold, hold_reason, hold_until_at)
		VALUES ($1, $2, 'NULL描述-挂起到期', NULL, 'low', 'executing',
			'tester', 'user', $5, $3, $3, true, '等变更窗口', $4)`,
		holdID, tenant, now.Add(-3*time.Hour), now.Add(-time.Hour), holdID); err != nil {
		t.Fatalf("insert on-hold ticket with NULL description: %v", err)
	}

	t.Run("ListActiveTicketsWithDeadline", func(t *testing.T) {
		tickets, err := s.ListActiveTicketsWithDeadline(ctx, tenant)
		if err != nil {
			t.Fatalf("含 NULL description 的在途工单导致查询失败: %v", err)
		}
		found := false
		for _, tk := range tickets {
			if tk.ID == activeID {
				found = true
				if tk.Description != "" {
					t.Errorf("NULL description 应回填为空串，实际 %q", tk.Description)
				}
				if tk.DeadlineAt == nil {
					t.Error("deadline_at 应被读出，实际为 nil")
				}
			}
		}
		if !found {
			t.Error("未在结果中找到构造的 NULL description 工单")
		}
	})

	t.Run("ListOnHoldTicketsPastDue", func(t *testing.T) {
		tickets, err := s.ListOnHoldTicketsPastDue(ctx, tenant)
		if err != nil {
			t.Fatalf("含 NULL description 的挂起工单导致查询失败: %v", err)
		}
		found := false
		for _, tk := range tickets {
			if tk.ID == holdID {
				found = true
				if tk.Description != "" {
					t.Errorf("NULL description 应回填为空串，实际 %q", tk.Description)
				}
				if tk.HoldReason != "等变更窗口" {
					t.Errorf("hold_reason 期望 %q，实际 %q", "等变更窗口", tk.HoldReason)
				}
			}
		}
		if !found {
			t.Error("未在结果中找到构造的挂起到期工单")
		}
	})
}
