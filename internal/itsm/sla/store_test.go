package sla

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// 验证 Create 对空租户/空风险级的默认值兜底,以及 ID/时间戳自动填充。
// 这正是此前 ITSM 反复踩的「空值落库」坑的回归守护。
func TestCreate_DefaultsFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	defer db.Close()
	s := &Store{db: db}

	mock.ExpectExec("INSERT INTO itsm_sla_policies").
		WithArgs(
			sqlmock.AnyArg(), // id (uuid 自动生成)
			"default",        // tenant_id 空 -> default
			"测试策略",           // name
			"custom",         // risk_level 空 -> custom
			"",               // process_id
			0, 0, 0,          // response/resolution/escalation hours
			"",               // escalation_to
			sqlmock.AnyArg(), // escalation_levels json
			"",               // service_window_id（未绑定服务窗口）
			true,             // enabled
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	p := &Policy{Name: "测试策略", Enabled: true}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create err = %v", err)
	}
	if p.TenantID != "default" {
		t.Errorf("tenant_id = %q, want %q", p.TenantID, "default")
	}
	if p.RiskLevel != "custom" {
		t.Errorf("risk_level = %q, want %q", p.RiskLevel, "custom")
	}
	if p.ID == "" {
		t.Error("id should be auto-generated")
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at should be set")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock: %v", err)
	}
}

// 验证显式传入的值不被默认值覆盖。
func TestCreate_KeepsExplicitValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	defer db.Close()
	s := &Store{db: db}

	mock.ExpectExec("INSERT INTO itsm_sla_policies").
		WithArgs(
			sqlmock.AnyArg(),
			"tenantA", // 显式租户
			"高风险策略",
			"high", // 显式风险级
			"proc-1",
			24, 48, 36,
			"值班经理",
			sqlmock.AnyArg(), // escalation_levels json
			"",               // service_window_id（未绑定服务窗口）
			true,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	p := &Policy{
		Name: "高风险策略", TenantID: "tenantA", RiskLevel: "high",
		ProcessID: "proc-1", ResponseHours: 24, ResolutionHours: 48,
		EscalationHours: 36, EscalationTo: "值班经理", Enabled: true,
	}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create err = %v", err)
	}
	if p.TenantID != "tenantA" || p.RiskLevel != "high" {
		t.Errorf("explicit values overwritten: tenant=%q risk=%q", p.TenantID, p.RiskLevel)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock: %v", err)
	}
}
