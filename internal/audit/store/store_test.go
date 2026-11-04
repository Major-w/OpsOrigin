package store

import (
	"context"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/audit/model"
)

func mkEvent(tenant, trace, etype, actorID, action string) *model.AuditEvent {
	return &model.AuditEvent{
		TenantID:  tenant,
		TraceID:   trace,
		EventType: etype,
		Actor:     model.AuditActor{Type: "user", ID: actorID, DisplayName: "Name " + actorID},
		Target:    model.AuditTarget{Type: "ci", ID: "c1", Name: "server"},
		Action:    action,
		Success:   true,
	}
}

func TestMemoryStore_RecordQuery(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Record(ctx, mkEvent("t1", "tr1", "login", "u1", "登录"))
	_ = s.Record(ctx, mkEvent("t1", "tr1", "logout", "u2", "登出"))
	_ = s.Record(ctx, mkEvent("t2", "tr2", "login", "u3", "登录"))

	// 全量
	all, total, err := s.Query(ctx, model.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("all: total=%d len=%d want 3/3", total, len(all))
	}
	// 时间倒序：最后插入的(t2)应在最前
	if all[0].TenantID != "t2" {
		t.Errorf("expected newest-first ordering, got first tenant=%s", all[0].TenantID)
	}

	// 按租户过滤
	t1ev, total, _ := s.Query(ctx, model.AuditFilter{TenantID: "t1"})
	if total != 2 || len(t1ev) != 2 {
		t.Fatalf("tenant t1: total=%d len=%d want 2/2", total, len(t1ev))
	}
	for _, e := range t1ev {
		if e.TenantID != "t1" {
			t.Errorf("tenant filter leaked event with tenant=%s", e.TenantID)
		}
	}

	// 按事件类型过滤
	_, total, _ = s.Query(ctx, model.AuditFilter{EventType: "login"})
	if total != 2 {
		t.Errorf("login filter: total=%d want 2", total)
	}

	// 关键词（action 模糊，大小写不敏感）
	_, total, _ = s.Query(ctx, model.AuditFilter{Keyword: "登"})
	if total != 3 {
		t.Errorf("keyword 登: total=%d want 3", total)
	}

	// 成功状态过滤（全部成功）
	fail := false
	_, total, _ = s.Query(ctx, model.AuditFilter{Success: &fail})
	if total != 0 {
		t.Errorf("success=false filter: total=%d want 0", total)
	}

	// 分页
	page, total, _ := s.Query(ctx, model.AuditFilter{Limit: 2})
	if len(page) != 2 || total != 3 {
		t.Errorf("paging: len=%d total=%d want 2/3", len(page), total)
	}

	// offset 越界返回空
	over, total, _ := s.Query(ctx, model.AuditFilter{Offset: 100})
	if len(over) != 0 || total != 3 {
		t.Errorf("offset overflow: len=%d total=%d want 0/3", len(over), total)
	}
}

func TestMatchFilter(t *testing.T) {
	now := time.Now()
	e := &model.AuditEvent{
		TenantID:  "t1",
		TraceID:   "tr1",
		EventType: "login",
		Actor:     model.AuditActor{Type: "user", ID: "u1", DisplayName: "Alice"},
		Target:    model.AuditTarget{Type: "ci", ID: "c1", Name: "server"},
		Action:    "reboot",
		Success:   true,
		Timestamp: now,
	}

	ptr := func(b bool) *bool { return &b }

	cases := []struct {
		name string
		f    model.AuditFilter
		want bool
	}{
		{"empty", model.AuditFilter{}, true},
		{"tenant match", model.AuditFilter{TenantID: "t1"}, true},
		{"tenant mismatch", model.AuditFilter{TenantID: "t2"}, false},
		{"trace match", model.AuditFilter{TraceID: "tr1"}, true},
		{"trace mismatch", model.AuditFilter{TraceID: "x"}, false},
		{"event match", model.AuditFilter{EventType: "login"}, true},
		{"event mismatch", model.AuditFilter{EventType: "logout"}, false},
		{"actor type match", model.AuditFilter{ActorType: "user"}, true},
		{"actor type mismatch", model.AuditFilter{ActorType: "system"}, false},
		{"actor id fuzzy (case-insensitive)", model.AuditFilter{ActorID: "U1"}, true},
		{"actor id via displayname", model.AuditFilter{ActorID: "alic"}, true},
		{"actor id no match", model.AuditFilter{ActorID: "zzz"}, false},
		{"target type match", model.AuditFilter{TargetType: "ci"}, true},
		{"target type mismatch", model.AuditFilter{TargetType: "host"}, false},
		{"keyword action (case-insensitive)", model.AuditFilter{Keyword: "REBOOT"}, true},
		{"keyword target name", model.AuditFilter{Keyword: "serve"}, true},
		{"keyword no match", model.AuditFilter{Keyword: "xyz"}, false},
		{"success true", model.AuditFilter{Success: ptr(true)}, true},
		{"success false", model.AuditFilter{Success: ptr(false)}, false},
		{"from future excludes", model.AuditFilter{From: now.Add(time.Hour)}, false},
		{"from past includes", model.AuditFilter{From: now.Add(-time.Hour)}, true},
		{"to past excludes", model.AuditFilter{To: now.Add(-time.Hour)}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchFilter(e, tc.f); got != tc.want {
				t.Errorf("matchFilter=%v want %v", got, tc.want)
			}
		})
	}
}

func TestContainsFold(t *testing.T) {
	if !containsFold("Hello", "ell") {
		t.Error("expected substring match")
	}
	if !containsFold("Hello", "HELLO") {
		t.Error("expected case-insensitive match")
	}
	if !containsFold("anything", "") {
		t.Error("expected empty needle to match")
	}
	if containsFold("Hello", "xyz") {
		t.Error("expected no match")
	}
}
