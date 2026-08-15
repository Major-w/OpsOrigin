package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/model"
)

// TestSkillOverlayComposePriority 验证 overlay 按优先级合并指令(数值大者后应用),
// 同优先级按 id 字典序稳定; 合并后标注来源, 便于追溯。
func TestSkillOverlayComposePriority(t *testing.T) {
	r := NewSkillOverlayRegistry()
	base := model.AgentSkill{Name: "db-troubleshoot", Description: "数据库故障处置", Instructions: "1. 收集日志"}

	// 同优先级, 期望按 id 字典序: mysql < oracle < pg
	mustApply(t, r, &SkillOverlay{ID: "pg", TargetSkill: "db-troubleshoot", Priority: 10, Instructions: "PostgreSQL: 查 pg_stat_activity"})
	mustApply(t, r, &SkillOverlay{ID: "oracle", TargetSkill: "db-troubleshoot", Priority: 10, Instructions: "Oracle: 查 v$session"})
	mustApply(t, r, &SkillOverlay{ID: "mysql", TargetSkill: "db-troubleshoot", Priority: 10, Instructions: "MySQL: 查 processlist"})

	eff := r.Compose(base)
	// 期望顺序: base -> mysql -> oracle -> pg(优先级相同, id 升序)
	idxBase := strings.Index(eff.Instructions, "收集日志")
	idxMySQL := strings.Index(eff.Instructions, "MySQL")
	idxOracle := strings.Index(eff.Instructions, "Oracle")
	idxPG := strings.Index(eff.Instructions, "PostgreSQL")
	if !(idxBase < idxMySQL && idxMySQL < idxOracle && idxOracle < idxPG) {
		t.Fatalf("overlay order wrong: %q", eff.Instructions)
	}
	// 每个 patch 标注来源 id
	for _, id := range []string{"mysql", "oracle", "pg"} {
		if !strings.Contains(eff.Instructions, "[overlay "+id+"]") {
			t.Errorf("missing overlay source marker for %s in %q", id, eff.Instructions)
		}
	}
	if !strings.Contains(eff.Description, "overlays:") {
		t.Errorf("description should annotate active overlays, got %q", eff.Description)
	}
}

// TestSkillOverlayReversible 验证移除 overlay 即还原到 base(Harness overlay 可逆 effect)。
func TestSkillOverlayReversible(t *testing.T) {
	r := NewSkillOverlayRegistry()
	base := model.AgentSkill{Name: "db-troubleshoot", Instructions: "base step"}

	if err := r.Apply(&SkillOverlay{ID: "o1", TargetSkill: "db-troubleshoot", Instructions: "scenario A"}); err != nil {
		t.Fatal(err)
	}
	if got := r.Compose(base).Instructions; !strings.Contains(got, "scenario A") {
		t.Fatalf("overlay should be applied, got %q", got)
	}
	// 移除 -> 还原
	if !r.Remove("o1") {
		t.Fatal("remove should report true")
	}
	if got := r.Compose(base).Instructions; got != "base step" {
		t.Fatalf("after remove, should equal base, got %q", got)
	}
	// 移除不存在的 id 返回 false, 不影响状态
	if r.Remove("nope") {
		t.Fatal("removing unknown id should be false")
	}
}

// TestSkillOverlayValidation 验证 fail-closed: 缺 id / 缺 target / 无任何补丁均拒绝。
func TestSkillOverlayValidation(t *testing.T) {
	r := NewSkillOverlayRegistry()
	cases := []*SkillOverlay{
		{TargetSkill: "x", Instructions: "p"}, // 缺 id
		{ID: "o", Instructions: "p"},          // 缺 target
		{ID: "o", TargetSkill: "x"},           // 无任何补丁内容
	}
	for i, c := range cases {
		if err := r.Apply(c); err == nil {
			t.Errorf("case %d should be rejected", i)
		}
	}
	// nil 安全
	if err := r.Apply(nil); err == nil {
		t.Error("nil overlay should be rejected")
	}
	// 合法者通过
	if err := r.Apply(&SkillOverlay{ID: "ok", TargetSkill: "x", Instructions: "p"}); err != nil {
		t.Errorf("valid overlay rejected: %v", err)
	}
}

// TestSkillOverlayIdempotentReplace 验证同 id 覆盖(幂等替换)而非重复追加。
func TestSkillOverlayIdempotentReplace(t *testing.T) {
	r := NewSkillOverlayRegistry()
	base := model.AgentSkill{Name: "s", Instructions: "base"}
	_ = r.Apply(&SkillOverlay{ID: "o1", TargetSkill: "s", Instructions: "v1"})
	_ = r.Apply(&SkillOverlay{ID: "o1", TargetSkill: "s", Instructions: "v2"}) // 同 id 替换
	eff := r.Compose(base)
	if strings.Contains(eff.Instructions, "v1") {
		t.Fatalf("old version should be replaced, got %q", eff.Instructions)
	}
	if !strings.Contains(eff.Instructions, "v2") {
		t.Fatalf("new version should be present, got %q", eff.Instructions)
	}
	if n := len(r.List("s")); n != 1 {
		t.Fatalf("replace should keep single entry, got %d", n)
	}
}

// TestRenderSkillsWithOverlay 验证引擎 renderSkills 把 active overlay 合并进 system prompt(端到端)。
func TestRenderSkillsWithOverlay(t *testing.T) {
	e := newTestEngine(t)
	ag := &model.AgentInstance{
		Name:     "ops-agent",
		TenantID: "default",
		Skills: []model.AgentSkill{
			{Name: "db-troubleshoot", Description: "数据库故障处置", Instructions: "1. 收集日志"},
		},
	}
	if err := e.CreateAgent(context.Background(), ag); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplySkillOverlay(&SkillOverlay{
		ID: "mysql", TargetSkill: "db-troubleshoot", Priority: 10,
		Instructions: "MySQL: 查 processlist",
	}); err != nil {
		t.Fatal(err)
	}

	rendered := e.renderSkills(ag.ID)
	if !strings.Contains(rendered, "db-troubleshoot") {
		t.Errorf("base skill name missing: %q", rendered)
	}
	if !strings.Contains(rendered, "收集日志") {
		t.Errorf("base instructions missing: %q", rendered)
	}
	if !strings.Contains(rendered, "MySQL: 查 processlist") {
		t.Errorf("overlay patch missing in render: %q", rendered)
	}
	if !strings.Contains(rendered, "[overlay mysql]") {
		t.Errorf("overlay source marker missing: %q", rendered)
	}

	// 移除 overlay 后渲染应还原(不再含场景补丁)
	e.RemoveSkillOverlay("mysql")
	rendered2 := e.renderSkills(ag.ID)
	if strings.Contains(rendered2, "MySQL: 查 processlist") {
		t.Errorf("after removal, overlay patch should be gone: %q", rendered2)
	}
	if !strings.Contains(rendered2, "收集日志") {
		t.Errorf("base should remain after removal: %q", rendered2)
	}
}

// TestSkillOverlayComposeTools 验证声明式工具集(base + adds - removes), 仅用于审计提示。
func TestSkillOverlayComposeTools(t *testing.T) {
	r := NewSkillOverlayRegistry()
	base := []string{"cmdb_query", "ticket_create"}
	_ = r.Apply(&SkillOverlay{
		ID: "o1", TargetSkill: "s", ToolAdds: []string{"script_run", "cmdb_query"}, ToolRemoves: []string{"ticket_create"},
	})
	got := r.ComposeTools("s", base)
	// 期望去重 + 新增 + 移除: cmdb_query, script_run
	if len(got) != 2 || !contains(got, "cmdb_query") || !contains(got, "script_run") || contains(got, "ticket_create") {
		t.Fatalf("compose tools wrong: %#v", got)
	}
	// 无 overlay 时原样返回
	if got := r.ComposeTools("other", base); len(got) != 2 {
		t.Fatalf("no-overlay should return base, got %#v", got)
	}
}

func mustApply(t *testing.T, r *SkillOverlayRegistry, o *SkillOverlay) {
	t.Helper()
	if err := r.Apply(o); err != nil {
		t.Fatalf("apply %s failed: %v", o.ID, err)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
