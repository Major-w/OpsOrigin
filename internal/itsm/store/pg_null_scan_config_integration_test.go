//go:build integration

package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// TestPGStoreNullableConfigColumnsScan 守卫配置类表（form_dicts / ticket_templates /
// automation_rules / ticket_buttons）的可空文本列裸扫回归：这些列的建表都是
// `TEXT DEFAULT ”` 无 NOT NULL，历史行或异常行若为 NULL，直扫裸 string 会让整条
// 查询以 `converting NULL to string is unsupported` 失败。修复后读取侧统一走
// sql.NullString + nullableStr() 回填。
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN)，未配置时跳过。
func TestPGStoreNullableConfigColumnsScan(t *testing.T) {
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

	t.Run("FieldDict NULL description", func(t *testing.T) {
		id := uuid.NewString()
		t.Cleanup(func() { db.ExecContext(ctx, `DELETE FROM itsm_form_dicts WHERE id=$1`, id) })
		if _, err := db.ExecContext(ctx,
			`INSERT INTO itsm_form_dicts (id, tenant_id, name, description, fields) VALUES ($1,$2,$3,NULL,NULL)`,
			id, tenant, "dict-null"); err != nil {
			t.Fatalf("insert dict NULL description: %v", err)
		}
		d, err := s.GetFieldDict(ctx, tenant, id)
		if err != nil {
			t.Fatalf("GetFieldDict 含 NULL description 失败: %v", err)
		}
		if d.Description != "" {
			t.Errorf("NULL description 应回填为空串, 实际 %q", d.Description)
		}
		all, err := s.ListFieldDicts(ctx, tenant)
		if err != nil {
			t.Fatalf("ListFieldDicts 含 NULL description 失败: %v", err)
		}
		found := false
		for _, x := range all {
			if x.ID == id {
				found = true
				if x.Description != "" {
					t.Errorf("List NULL description 应回填为空串, 实际 %q", x.Description)
				}
			}
		}
		if !found {
			t.Error("ListFieldDicts 未返回构造的 NULL description 字典")
		}
	})

	t.Run("TicketTemplate NULL text columns", func(t *testing.T) {
		id := uuid.NewString()
		t.Cleanup(func() { db.ExecContext(ctx, `DELETE FROM itsm_ticket_templates WHERE id=$1`, id) })
		if _, err := db.ExecContext(ctx,
			`INSERT INTO itsm_ticket_templates (id, tenant_id, name, remark, category, form_scope, process_id, catalog_id, content, created_by)
			 VALUES ($1,$2,$3,NULL,NULL,NULL,NULL,NULL,NULL,NULL)`,
			id, tenant, "tpl-null"); err != nil {
			t.Fatalf("insert template NULL cols: %v", err)
		}
		tmpl, err := s.GetTicketTemplate(ctx, tenant, id)
		if err != nil {
			t.Fatalf("GetTicketTemplate 含 NULL 列失败: %v", err)
		}
		if tmpl.Remark != "" || tmpl.Category != "" || tmpl.FormScope != "" ||
			tmpl.ProcessID != "" || tmpl.CatalogID != "" || tmpl.CreatedBy != "" {
			t.Errorf("NULL 文本列应回填为空串, 实际 remark=%q category=%q formScope=%q processID=%q catalogID=%q createdBy=%q",
				tmpl.Remark, tmpl.Category, tmpl.FormScope, tmpl.ProcessID, tmpl.CatalogID, tmpl.CreatedBy)
		}
		all, err := s.ListTicketTemplates(ctx, tenant)
		if err != nil {
			t.Fatalf("ListTicketTemplates 含 NULL 列失败: %v", err)
		}
		found := false
		for _, x := range all {
			if x.ID == id {
				found = true
				if x.Remark != "" || x.CreatedBy != "" {
					t.Errorf("List NULL 列应回填为空串, 实际 remark=%q createdBy=%q", x.Remark, x.CreatedBy)
				}
			}
		}
		if !found {
			t.Error("ListTicketTemplates 未返回构造的 NULL 列模板")
		}
	})

	t.Run("AutomationRule NULL created_by", func(t *testing.T) {
		id := uuid.NewString()
		t.Cleanup(func() { db.ExecContext(ctx, `DELETE FROM itsm_automation_rules WHERE id=$1`, id) })
		if _, err := db.ExecContext(ctx,
			`INSERT INTO itsm_automation_rules (id, tenant_id, name, created_by) VALUES ($1,$2,$3,NULL)`,
			id, tenant, "rule-null"); err != nil {
			t.Fatalf("insert automation NULL created_by: %v", err)
		}
		r, err := s.GetAutomationRule(ctx, tenant, id)
		if err != nil {
			t.Fatalf("GetAutomationRule 含 NULL created_by 失败: %v", err)
		}
		if r.CreatedBy != "" {
			t.Errorf("NULL created_by 应回填为空串, 实际 %q", r.CreatedBy)
		}
		all, err := s.ListAutomationRules(ctx, tenant)
		if err != nil {
			t.Fatalf("ListAutomationRules 含 NULL created_by 失败: %v", err)
		}
		found := false
		for _, x := range all {
			if x.ID == id {
				found = true
				if x.CreatedBy != "" {
					t.Errorf("List NULL created_by 应回填为空串, 实际 %q", x.CreatedBy)
				}
			}
		}
		if !found {
			t.Error("ListAutomationRules 未返回构造的 NULL created_by 规则")
		}
	})

	t.Run("TicketButton NULL confirm_text/created_by", func(t *testing.T) {
		id := uuid.NewString()
		t.Cleanup(func() { db.ExecContext(ctx, `DELETE FROM itsm_ticket_buttons WHERE id=$1`, id) })
		if _, err := db.ExecContext(ctx,
			`INSERT INTO itsm_ticket_buttons (id, tenant_id, name, confirm_text, created_by) VALUES ($1,$2,$3,NULL,NULL)`,
			id, tenant, "btn-null"); err != nil {
			t.Fatalf("insert button NULL confirm_text/created_by: %v", err)
		}
		b, err := s.GetTicketButton(ctx, tenant, id)
		if err != nil {
			t.Fatalf("GetTicketButton 含 NULL 列失败: %v", err)
		}
		if b.ConfirmText != "" || b.CreatedBy != "" {
			t.Errorf("NULL 列应回填为空串, 实际 confirmText=%q createdBy=%q", b.ConfirmText, b.CreatedBy)
		}
		all, err := s.ListTicketButtons(ctx, tenant)
		if err != nil {
			t.Fatalf("ListTicketButtons 含 NULL 列失败: %v", err)
		}
		found := false
		for _, x := range all {
			if x.ID == id {
				found = true
				if x.ConfirmText != "" || x.CreatedBy != "" {
					t.Errorf("List NULL 列应回填为空串, 实际 confirmText=%q createdBy=%q", x.ConfirmText, x.CreatedBy)
				}
			}
		}
		if !found {
			t.Error("ListTicketButtons 未返回构造的 NULL 列按钮")
		}
	})
}
