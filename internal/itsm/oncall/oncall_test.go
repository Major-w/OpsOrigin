package oncall

import (
	"context"
	"testing"
	"time"
)

func TestRotationCurrentOncall(t *testing.T) {
	now := time.Now()
	r := &Rotation{
		ID:        "r1",
		TenantID:  "t1",
		Name:      "一线值班",
		Members:   []string{"user:a", "user:b", "user:c"},
		CycleDays: 1,
		CreatedAt: now,
	}
	if got := r.CurrentOncall(now); got != "user:a" {
		t.Fatalf("created now -> expect user:a, got %s", got)
	}
	r.CreatedAt = now.Add(-48 * time.Hour) // 48h / 1d cycle / 3 members -> idx 2
	if got := r.CurrentOncall(now); got != "user:c" {
		t.Fatalf("48h daily/3 -> expect user:c, got %s", got)
	}
}

func TestRotationWeekly(t *testing.T) {
	now := time.Now()
	r := &Rotation{
		ID:        "r2",
		TenantID:  "t1",
		Name:      "周值班",
		Members:   []string{"user:x", "user:y"},
		CycleDays: 7,
		CreatedAt: now.Add(-7 * 24 * time.Hour), // 1 week -> idx 1
	}
	if got := r.CurrentOncall(now); got != "user:y" {
		t.Fatalf("1 week weekly/2 -> expect user:y, got %s", got)
	}
}

func TestRotationSingleMember(t *testing.T) {
	now := time.Now()
	r := &Rotation{ID: "r3", TenantID: "t1", Members: []string{"agent:oncall"}, CycleDays: 1, CreatedAt: now.Add(-999 * time.Hour)}
	if got := r.CurrentOncall(now); got != "agent:oncall" {
		t.Fatalf("single member always oncall, got %s", got)
	}
}

func TestRotationEmptyMembers(t *testing.T) {
	r := &Rotation{ID: "r4", TenantID: "t1", CycleDays: 1, CreatedAt: time.Now()}
	if got := r.CurrentOncall(time.Now()); got != "" {
		t.Fatalf("empty members -> empty, got %s", got)
	}
}

func TestHandleAlertRoutesToAgent(t *testing.T) {
	svc := NewService(nil)
	rot := &Rotation{
		TenantID:  "t1",
		Name:      "AI 值班",
		Members:   []string{"agent:oncall-agent"},
		CycleDays: 1,
		Labels:    map[string]string{"component": "database"},
	}
	_ = svc.store.AddRotation(context.Background(), rot)

	var tookOver []string
	svc.SetAgentHandler(func(ctx context.Context, tenantID, agentID, title, desc string) error {
		tookOver = append(tookOver, agentID)
		return nil
	})

	// 不匹配 label 的告警不路由
	svc.HandleAlert(context.Background(), &Alert{ID: "1", TenantID: "t1", Title: "web down", Labels: map[string]string{"component": "web"}})
	if len(tookOver) != 0 {
		t.Fatalf("unmatched label alert should not route, got %v", tookOver)
	}
	// 匹配的告警路由到 agent
	svc.HandleAlert(context.Background(), &Alert{ID: "2", TenantID: "t1", Title: "db down", Description: "conn failed", Labels: map[string]string{"component": "database"}})
	if len(tookOver) != 1 || tookOver[0] != "oncall-agent" {
		t.Fatalf("expected oncall-agent takeover, got %v", tookOver)
	}
}

func TestHandleAlertNoAgentHandler(t *testing.T) {
	svc := NewService(nil)
	_ = svc.store.AddRotation(context.Background(), &Rotation{
		Name: "人工值班", Members: []string{"user:a"}, CycleDays: 1, Labels: nil,
	})
	// 无 agent handler + user 成员: 不 panic, 静默路由
	svc.HandleAlert(context.Background(), &Alert{ID: "1", TenantID: "t1", Title: "cpu"})
}

func TestIsAgentMember(t *testing.T) {
	if !isAgentMember("agent:a1") {
		t.Fatal("agent:a1 should be agent member")
	}
	if isAgentMember("user:u1") || isAgentMember("agent:") {
		t.Fatal("user member / bare agent: should not be agent")
	}
}

func TestLabelsMatch(t *testing.T) {
	if !labelsMatch(nil, map[string]string{"a": "b"}) {
		t.Fatal("nil required matches all")
	}
	if !labelsMatch(map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}) {
		t.Fatal("subset match should pass")
	}
	if labelsMatch(map[string]string{"a": "1"}, map[string]string{"a": "2"}) {
		t.Fatal("value mismatch should fail")
	}
}
