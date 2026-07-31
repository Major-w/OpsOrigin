package agentic

import (
	"context"
	"testing"
)

func TestMemFeedbackStoreSaveListStats(t *testing.T) {
	s := NewMemFeedbackStore()
	ctx := context.Background()
	if err := s.Save(ctx, &Feedback{TenantID: "t1", MessageID: "m1", Rating: "up"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, &Feedback{TenantID: "t1", MessageID: "m2", Rating: "down"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	up, down, err := s.Stats(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if up != 1 || down != 1 {
		t.Fatalf("want up=1 down=1, got %d/%d", up, down)
	}
}

func TestMemFeedbackStoreUpsert(t *testing.T) {
	s := NewMemFeedbackStore()
	ctx := context.Background()
	if err := s.Save(ctx, &Feedback{TenantID: "t1", MessageID: "m1", Rating: "up"}); err != nil {
		t.Fatal(err)
	}
	// 同一 message_id 改为 down, 应更新而非新增
	if err := s.Save(ctx, &Feedback{TenantID: "t1", MessageID: "m1", Rating: "down", Comment: "bad"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx, "t1", "")
	if len(list) != 1 {
		t.Fatalf("upsert should keep 1 record, got %d", len(list))
	}
	if list[0].Rating != "down" || list[0].Comment != "bad" {
		t.Fatalf("not updated: %#v", list[0])
	}
	up, down, _ := s.Stats(ctx, "t1")
	if up != 0 || down != 1 {
		t.Fatalf("want up=0 down=1, got %d/%d", up, down)
	}
}

func TestMemFeedbackStoreConversationFallback(t *testing.T) {
	s := NewMemFeedbackStore()
	ctx := context.Background()
	// 无 message_id, 退化按 conversation_id
	if err := s.Save(ctx, &Feedback{TenantID: "t1", ConversationID: "c1", Rating: "up"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, &Feedback{TenantID: "t1", ConversationID: "c1", Rating: "up"}); err != nil {
		t.Fatal(err)
	}
	// 同一 conv 应 upsert 为 1 条
	all, _ := s.List(ctx, "t1", "")
	if len(all) != 1 {
		t.Fatalf("conversation fallback should upsert to 1, got %d", len(all))
	}
	byConv, _ := s.List(ctx, "t1", "c1")
	if len(byConv) != 1 {
		t.Fatalf("want 1 for c1, got %d", len(byConv))
	}
	none, _ := s.List(ctx, "t1", "c2")
	if len(none) != 0 {
		t.Fatalf("want 0 for c2, got %d", len(none))
	}
}

func TestMemFeedbackStoreTenantIsolation(t *testing.T) {
	s := NewMemFeedbackStore()
	ctx := context.Background()
	_ = s.Save(ctx, &Feedback{TenantID: "t1", MessageID: "m1", Rating: "up"})
	_ = s.Save(ctx, &Feedback{TenantID: "t2", MessageID: "m1", Rating: "up"})
	list1, _ := s.List(ctx, "t1", "")
	if len(list1) != 1 {
		t.Fatalf("t1 should see only its own feedback, got %d", len(list1))
	}
}
