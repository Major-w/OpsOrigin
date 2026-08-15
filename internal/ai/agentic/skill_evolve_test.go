package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/model"
)

// fakeEvoBackend 实现 SkillEvolutionBackend, 用于隔离测试演化逻辑。
type fakeEvoBackend struct {
	agents    map[string]*model.AgentInstance
	memories  []*ExperienceMemory
	published []*SkillPackage
}

func newFakeEvoBackend() *fakeEvoBackend {
	return &fakeEvoBackend{
		agents: map[string]*model.AgentInstance{
			"agent-1": {
				ID:           "agent-1",
				Name:         "db-healer",
				SystemPrompt: "你是数据库修复助手。",
				TenantID:     "t1",
				Tools:        []string{"cmdb_query", "script_run"},
			},
		},
	}
}

func (f *fakeEvoBackend) GetAgent(id string) (*model.AgentInstance, bool) {
	a, ok := f.agents[id]
	return a, ok
}

func (f *fakeEvoBackend) AddMemory(m *ExperienceMemory) (*ExperienceMemory, error) {
	f.memories = append(f.memories, m)
	return m, nil
}

func (f *fakeEvoBackend) RetrieveMemories(tenantID, agentID, _ string) []*ExperienceMemory {
	var out []*ExperienceMemory
	for _, m := range f.memories {
		if m.TenantID != tenantID {
			continue
		}
		if m.AgentID != "" && m.AgentID != agentID {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeEvoBackend) PublishSkillVersion(_ context.Context, tenantID, _ string, patch SkillPatchSuggestion) (*SkillPackage, error) {
	p := &SkillPackage{
		ID:       "pkg-" + patch.ToVersion,
		TenantID: tenantID,
		Name:     "db-healer",
		Version:  patch.ToVersion,
		Agent:    AgentPayload{SystemPrompt: patch.SuggestedSystemPrompt},
	}
	f.published = append(f.published, p)
	return p, nil
}

func TestEvolverDistillOnSuccess(t *testing.T) {
	b := newFakeEvoBackend()
	ev := NewMemSkillEvolver(b)
	if err := ev.RecordOutcome(context.Background(), SkillUsageOutcome{
		TenantID: "t1", AgentID: "agent-1", Success: true,
		Context: "MySQL 主从延迟高", ToolSeq: []string{"cmdb_query", "script_run"},
	}); err != nil {
		t.Fatalf("RecordOutcome error: %v", err)
	}
	if len(b.memories) != 1 {
		t.Fatalf("expected 1 distilled memory, got %d", len(b.memories))
	}
	m := b.memories[0]
	if m.Outcome != "succeeded" {
		t.Errorf("outcome = %q, want succeeded", m.Outcome)
	}
	if m.Resolution != "cmdb_query → script_run" {
		t.Errorf("resolution = %q", m.Resolution)
	}
	if m.AgentID != "agent-1" {
		t.Errorf("memory agent = %q", m.AgentID)
	}
}

func TestEvolverNoPatchBelowThreshold(t *testing.T) {
	b := newFakeEvoBackend()
	ev := NewMemSkillEvolver(b)
	for i := 0; i < skillFailureThreshold-1; i++ {
		_ = ev.RecordOutcome(context.Background(), SkillUsageOutcome{
			TenantID: "t1", AgentID: "agent-1", Success: false, Context: "fail",
		})
	}
	patch, err := ev.SuggestPatch(context.Background(), "t1", "agent-1")
	if err != nil {
		t.Fatalf("SuggestPatch error: %v", err)
	}
	if patch != nil {
		t.Fatalf("expected nil patch below threshold, got %+v", patch)
	}
}

func TestEvolverSuggestPatchAboveThreshold(t *testing.T) {
	b := newFakeEvoBackend()
	// 预置一条历史经验(同类问题已沉淀的处置)
	_, _ = b.AddMemory(&ExperienceMemory{
		TenantID: "t1", AgentID: "agent-1", Category: "skill_evolution",
		Keywords: []string{"mysql", "延迟"}, Problem: "MySQL 主从延迟高",
		Resolution: "调大 relay_log 缓冲并重启 SQL 线程", Outcome: "succeeded",
	})
	ev := NewMemSkillEvolver(b)
	for i := 0; i < skillFailureThreshold; i++ {
		_ = ev.RecordOutcome(context.Background(), SkillUsageOutcome{
			TenantID: "t1", AgentID: "agent-1", Success: false,
			Context: "MySQL 主从延迟高", ToolSeq: []string{"script_run"},
		})
	}
	patch, err := ev.SuggestPatch(context.Background(), "t1", "agent-1")
	if err != nil {
		t.Fatalf("SuggestPatch error: %v", err)
	}
	if patch == nil {
		t.Fatal("expected patch above threshold, got nil")
	}
	if patch.ToVersion != "1.0.1" {
		t.Errorf("toVersion = %q, want 1.0.1", patch.ToVersion)
	}
	if !strings.Contains(patch.LearnedResolution, "relay_log") {
		t.Errorf("learned resolution missing historical fix: %q", patch.LearnedResolution)
	}
	if !strings.Contains(patch.SuggestedSystemPrompt, "经验沉淀") {
		t.Errorf("suggested prompt missing evolution marker: %q", patch.SuggestedSystemPrompt)
	}
	if !strings.HasPrefix(patch.SuggestedSystemPrompt, "你是数据库修复助手。") {
		t.Errorf("suggested prompt should keep original system prompt prefix")
	}
}

func TestEvolverApplyPatchAndReset(t *testing.T) {
	b := newFakeEvoBackend()
	ev := NewMemSkillEvolver(b)
	for i := 0; i < skillFailureThreshold; i++ {
		_ = ev.RecordOutcome(context.Background(), SkillUsageOutcome{
			TenantID: "t1", AgentID: "agent-1", Success: false, Context: "fail",
		})
	}
	patch, err := ev.SuggestPatch(context.Background(), "t1", "agent-1")
	if err != nil || patch == nil {
		t.Fatalf("suggest patch failed: err=%v patch=%v", err, patch)
	}
	pkg, err := ev.ApplyPatch(context.Background(), "t1", "agent-1", *patch)
	if err != nil {
		t.Fatalf("ApplyPatch error: %v", err)
	}
	if pkg.Version != "1.0.1" {
		t.Errorf("published version = %q, want 1.0.1", pkg.Version)
	}
	if len(b.published) != 1 {
		t.Fatalf("expected 1 published package, got %d", len(b.published))
	}
	// 补丁发布后失败计数应重置, 不应再触发补丁
	_ = ev.RecordOutcome(context.Background(), SkillUsageOutcome{
		TenantID: "t1", AgentID: "agent-1", Success: false, Context: "fail",
	})
	if p2, _ := ev.SuggestPatch(context.Background(), "t1", "agent-1"); p2 != nil {
		t.Fatalf("failure count should reset after ApplyPatch, got patch %+v", p2)
	}
}

func TestEvolverNilBackend(t *testing.T) {
	ev := NewMemSkillEvolver(nil)
	// 不应 panic
	if err := ev.RecordOutcome(context.Background(), SkillUsageOutcome{
		TenantID: "t1", AgentID: "agent-1", Success: true, Context: "ctx",
	}); err != nil {
		t.Fatalf("RecordOutcome(nil backend) error: %v", err)
	}
	if _, err := ev.SuggestPatch(context.Background(), "t1", "agent-1"); err == nil {
		t.Fatal("SuggestPatch with nil backend should error")
	}
}
