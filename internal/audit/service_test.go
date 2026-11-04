package audit

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/audit/model"
	"github.com/opsorigin/core/internal/audit/store"
	"github.com/opsorigin/core/pkg/principal"
)

func TestActorFromContext_NoPrincipal(t *testing.T) {
	// 无主体上下文：回落系统服务身份，永不空 actor。
	actor := ActorFromContext(context.Background())
	if actor.Type != string(principal.TypeService) {
		t.Errorf("type = %q, want %q", actor.Type, principal.TypeService)
	}
	if actor.ID != "system" {
		t.Errorf("id = %q, want %q", actor.ID, "system")
	}
	if actor.DisplayName != "系统" {
		t.Errorf("display_name = %q, want %q", actor.DisplayName, "系统")
	}
}

func TestActorFromContext_WithHuman(t *testing.T) {
	pr := principal.Principal{
		Type:        principal.TypeHuman,
		ID:          "u1",
		DisplayName: "张三",
		TenantID:    "t1",
		TicketID:    "T-100",
	}
	ctx := principal.NewContext(context.Background(), pr)
	actor := ActorFromContext(ctx)
	if actor.Type != string(principal.TypeHuman) {
		t.Errorf("type = %q, want %q", actor.Type, principal.TypeHuman)
	}
	if actor.ID != "u1" {
		t.Errorf("id = %q, want %q", actor.ID, "u1")
	}
	if actor.DisplayName != "张三" {
		t.Errorf("display_name = %q, want %q", actor.DisplayName, "张三")
	}
	if actor.TicketID != "T-100" {
		t.Errorf("ticket_id = %q, want %q", actor.TicketID, "T-100")
	}
}

func TestActorFromContext_EmptyIDFallsBack(t *testing.T) {
	// 主体存在但 ID 为空：视为无效主体，回落系统身份。
	pr := principal.Principal{Type: principal.TypeHuman, ID: "", DisplayName: "无名"}
	ctx := principal.NewContext(context.Background(), pr)
	actor := ActorFromContext(ctx)
	if actor.ID != "system" {
		t.Errorf("id = %q, want %q (empty principal id must fall back)", actor.ID, "system")
	}
}

func TestRecordEvent_FallbackActor(t *testing.T) {
	// 调用方未填 actor 时，RecordEvent 必须以服务端可信主体覆盖，
	// 防止伪造审计记录（handler 同款安全逻辑）。
	st := store.NewMemoryStore()
	svc := &Service{store: st, rls: nil}

	pr := principal.Principal{
		Type:        principal.TypeHuman,
		ID:          "u2",
		DisplayName: "李四",
		TenantID:    "t2",
	}
	ctx := principal.NewContext(context.Background(), pr)

	event := &model.AuditEvent{EventType: "config_change"} // Actor.ID 为空
	if err := svc.RecordEvent(ctx, event); err != nil {
		t.Fatalf("RecordEvent err = %v", err)
	}
	if event.Actor.ID != "u2" {
		t.Errorf("actor.id after record = %q, want %q (server must override empty actor)", event.Actor.ID, "u2")
	}
	if event.Actor.DisplayName != "李四" {
		t.Errorf("actor.display_name = %q, want %q", event.Actor.DisplayName, "李四")
	}

	// 调用方已填 actor 时不应被覆盖。
	st2 := store.NewMemoryStore()
	svc2 := &Service{store: st2}
	event2 := &model.AuditEvent{
		EventType: "login",
		Actor:     model.AuditActor{Type: "HUMAN", ID: "caller", DisplayName: "调用方"},
	}
	if err := svc2.RecordEvent(context.Background(), event2); err != nil {
		t.Fatalf("RecordEvent err = %v", err)
	}
	if event2.Actor.ID != "caller" {
		t.Errorf("actor.id = %q, want %q (existing actor must not be overwritten)", event2.Actor.ID, "caller")
	}
}
