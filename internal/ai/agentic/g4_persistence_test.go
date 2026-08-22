package agentic

import (
	"context"
	"sync"
	"testing"
)

// TestG4PersistenceRoundTrip 验证 G4 持久化闭环:
// 引擎 A(轻量构造)写入闭环产物 → 引擎 B(轻量构造)restoreClosedLoopFromStore 恢复。
func TestG4PersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMemStore()

	// 引擎 A: 轻量构造(不 NewEngine, 避开 LLM/工具注册/预置团队开销)
	eA := &Engine{
		mu:             sync.RWMutex{},
		remediations:   map[string]*RemediationAction{},
		runbooks:       map[string]*Runbook{},
		rcaRecords:     map[string]*RCARecord{},
		postmortems:    map[string]*Postmortem{},
		inspectionRuns: map[string][]*InspectionRun{},
	}
	eA.SetStore(m)
	eA.SetAutonomy(AutonomyAutonomous)

	run := &InspectionRun{
		ID:       "run-1",
		TenantID: "default",
		Findings: `[{"level":"low","title":"磁盘使用率超 90%","detail":"d","suggestion":"清理 /var/log 旧日志"}]`,
	}
	eA.inspectionRuns[run.ID] = append(eA.inspectionRuns[run.ID], run)

	acts, err := eA.GenerateRemediations(ctx, "default", "run-1")
	if err != nil || len(acts) != 1 {
		t.Fatalf("generate remediations: %v (n=%d)", err, len(acts))
	}
	if _, err := eA.ExecuteRemediation(ctx, "default", acts[0].ID); err != nil {
		t.Fatalf("execute remediation: %v", err)
	}
	cur, _ := eA.GetRemediation(acts[0].ID)
	if cur.VerifyStatus != RemediationVerified {
		t.Fatalf("expected verified, got %s", cur.VerifyStatus)
	}
	if len(eA.runbooks) != 1 {
		t.Fatalf("expected 1 runbook, got %d", len(eA.runbooks))
	}
	rec := eA.RecordRCA("default", &RCARecord{IncidentID: "inc-1", Title: "磁盘", Hypothesis: "日志膨胀", Confidence: 0.8})
	if _, err := eA.LabelRCA("default", rec.ID, true, "ok"); err != nil {
		t.Fatalf("label rca: %v", err)
	}
	pm := eA.CreatePostmortem("default", &Postmortem{IncidentID: "inc-1", Title: "磁盘复盘", RootCause: "日志未轮转"})
	if _, err := eA.PublishPostmortem("default", pm.ID, false); err != nil {
		t.Fatalf("publish postmortem: %v", err)
	}

	// memStore 真实收到数据(写时落库生效)
	if len(m.remediations) != 1 || len(m.runbooks) != 1 || len(m.rcaRecords) != 1 || len(m.postmortems) != 1 {
		t.Fatalf("memStore writes missing: rem=%d rb=%d rca=%d pm=%d",
			len(m.remediations), len(m.runbooks), len(m.rcaRecords), len(m.postmortems))
	}
	if m.autonomy["default"] != AutonomyAutonomous {
		t.Fatalf("memStore autonomy missing: %v", m.autonomy["default"])
	}

	// 引擎 B: 轻量构造, 同 store 恢复
	eB := &Engine{
		mu:           sync.RWMutex{},
		remediations: map[string]*RemediationAction{},
		runbooks:     map[string]*Runbook{},
		rcaRecords:   map[string]*RCARecord{},
		postmortems:  map[string]*Postmortem{},
	}
	eB.SetStore(m)
	eB.mu.Lock()
	eB.restoreClosedLoopFromStore(ctx)
	eB.mu.Unlock()

	if len(eB.remediations) != 1 || len(eB.runbooks) != 1 || len(eB.rcaRecords) != 1 || len(eB.postmortems) != 1 {
		t.Fatalf("B restore counts wrong: rem=%d rb=%d rca=%d pm=%d",
			len(eB.remediations), len(eB.runbooks), len(eB.rcaRecords), len(eB.postmortems))
	}
	if eB.GetAutonomy() != AutonomyAutonomous {
		t.Fatalf("B: autonomy not restored, got %s", eB.GetAutonomy())
	}
	// 修复后的 runbook UseCount: 生成时命中 runbook → 应 >=1 且恢复不丢
	for id := range eA.runbooks {
		if eB.runbooks[id].UseCount < 1 {
			t.Fatalf("runbook use_count should >=1 after restore, got %d", eB.runbooks[id].UseCount)
		}
	}
	// 恢复后的修复动作保留验证结果(verify 状态已落库)
	for _, a := range eB.remediations {
		if a.VerifyStatus != RemediationVerified {
			t.Fatalf("restored remediation verify_status lost: %s", a.VerifyStatus)
		}
	}
}
