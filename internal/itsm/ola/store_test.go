package ola

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// 验证 Create 对空租户的默认值兜底 (OLA 此前同样存在空值落库风险)。
func TestCreate_DefaultsFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	defer db.Close()
	s := &Store{db: db}

	mock.ExpectExec("INSERT INTO itsm_ola_policies").
		WithArgs(
			sqlmock.AnyArg(), // id
			"default",        // tenant_id 空 -> default
			"一线处理时限",         // name
			"",               // process_id
			"",               // step_id
			4,                // duration_hours
			"一线组长",           // escalate_to
			"wecom",          // notify_channel
			true,             // enabled
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	p := &Policy{Name: "一线处理时限", DurationHours: 4, EscalateTo: "一线组长", NotifyChannel: "wecom", Enabled: true}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create err = %v", err)
	}
	if p.TenantID != "default" {
		t.Errorf("tenant_id = %q, want %q", p.TenantID, "default")
	}
	if p.ID == "" {
		t.Error("id should be auto-generated")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock: %v", err)
	}
}
