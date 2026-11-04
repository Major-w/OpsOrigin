package process

import (
	"context"
	"testing"
	"time"
)

// fakeDelegStore 内存委托存储（单测用）。
type fakeDelegStore struct{ items []*ApprovalDelegate }

func (f *fakeDelegStore) CreateDelegate(ctx context.Context, tenantID string, d *ApprovalDelegate) error {
	f.items = append(f.items, d)
	return nil
}
func (f *fakeDelegStore) ListDelegates(ctx context.Context, tenantID string) ([]*ApprovalDelegate, error) {
	return f.items, nil
}
func (f *fakeDelegStore) DeleteDelegate(ctx context.Context, tenantID, id string) error { return nil }
func (f *fakeDelegStore) ActiveDelegates(ctx context.Context, tenantID string, now time.Time) ([]*ApprovalDelegate, error) {
	var out []*ApprovalDelegate
	for _, d := range f.items {
		if !d.Enabled {
			continue
		}
		if now.Before(d.StartAt) || (!d.EndAt.IsZero() && !now.Before(d.EndAt)) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// TestApplyDelegations 追加式语义: 原名册保留、在期代理人追加、去重、一层展开。
func TestApplyDelegations(t *testing.T) {
	st := &fakeDelegStore{items: []*ApprovalDelegate{
		{Principal: "alice", Delegate: "bob", StartAt: time.Now().Add(-time.Hour), Enabled: true},
		{Principal: "carol", Delegate: "dave", StartAt: time.Now().Add(-time.Hour), Enabled: true},
		// 未生效: 开始时间在未来
		{Principal: "eve", Delegate: "frank", StartAt: time.Now().Add(time.Hour), Enabled: true},
		// 已过期
		{Principal: "gina", Delegate: "hank", StartAt: time.Now().Add(-2 * time.Hour), EndAt: time.Now().Add(-time.Hour), Enabled: true},
		// 禁用
		{Principal: "ivy", Delegate: "jack", StartAt: time.Now().Add(-time.Hour), Enabled: false},
	}}
	s := &Service{delegStore: st}

	roster := []string{"alice", "carol", "gina", "ivy", "eve"}
	got := s.applyDelegations(context.Background(), "default", roster)
	// alice→bob 与 carol→dave 在期追加；其余委托不生效
	want := []string{"alice", "carol", "gina", "ivy", "eve", "bob", "dave"}
	if len(got) != len(want) {
		t.Fatalf("roster = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roster = %v, want %v", got, want)
		}
	}

	// nil store: 返回原名册
	s2 := &Service{}
	if got2 := s2.applyDelegations(context.Background(), "default", roster); len(got2) != len(roster) {
		t.Fatalf("nil store should keep original roster, got %v", got2)
	}
}

// TestVoteStepWithDelegation 会签 + 委托: alice 委托 bob 后, 名册 = [alice, bob]，
// 只有 bob(代理人)通过不会签通过; alice+bob 都通过才放行。
func TestVoteStepWithDelegation(t *testing.T) {
	m := &memStore{procs: map[string]*Process{
		"p1": {ID: "p1", Steps: []Step{{ID: "approve", Type: StepUser, Approvers: []string{"alice"}}}},
	}, insts: map[string]*ProcessInstance{}}
	st := &fakeDelegStore{items: []*ApprovalDelegate{
		{Principal: "alice", Delegate: "bob", StartAt: time.Now().Add(-time.Hour), Enabled: true},
	}}
	s := &Service{store: m, delegStore: st}
	ctx := context.Background()
	m.CreateInstance(ctx, &ProcessInstance{ID: "i1", ProcessID: "p1", TicketID: "t1", TenantID: "default", Status: "running", StepResults: map[string]any{}})

	// bob(代理人)通过 → 名册 [alice,bob] 会签未达成
	settled, err := s.VoteStep(ctx, "default", "t1", "approve", "bob", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if settled {
		t.Fatal("only delegate approved should NOT settle countersign")
	}
	// alice(原审批人)也通过 → 达成
	settled, err = s.VoteStep(ctx, "default", "t1", "approve", "alice", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !settled {
		t.Fatal("principal + delegate both approved should settle countersign")
	}
}
