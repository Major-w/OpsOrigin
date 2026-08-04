package agentic

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/ai/model"
)

// memStore 是 Store 接口的进程内实现, 仅用于技能市场单测, 避免依赖真实 Postgres。
// 技能包 / 自定义工具走真实内存映射; 其余方法为最小实现。
type memStore struct {
	agents   map[string]*AgentRecord
	teams    map[string]*TeamRecord
	customs  map[string]*CustomTool
	skills   map[string]*SkillPackage
	tokens   []model.TokenRecord
	insps    map[string]*Inspection
	inspRuns map[string][]*InspectionRun
}

func newMemStore() *memStore {
	return &memStore{
		agents:   map[string]*AgentRecord{},
		teams:    map[string]*TeamRecord{},
		customs:  map[string]*CustomTool{},
		skills:   map[string]*SkillPackage{},
		insps:    map[string]*Inspection{},
		inspRuns: map[string][]*InspectionRun{},
	}
}

func (m *memStore) CreateAgent(_ context.Context, a *AgentRecord) error {
	m.agents[a.ID] = a
	return nil
}
func (m *memStore) GetAgent(_ context.Context, id, _ string) (*AgentRecord, error) {
	if a, ok := m.agents[id]; ok {
		return a, nil
	}
	return nil, nil
}
func (m *memStore) ListAgents(_ context.Context, _ string) ([]*AgentRecord, error) {
	out := make([]*AgentRecord, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, a)
	}
	return out, nil
}
func (m *memStore) UpdateAgent(_ context.Context, a *AgentRecord) error {
	m.agents[a.ID] = a
	return nil
}
func (m *memStore) DeleteAgent(_ context.Context, id, _ string) error {
	delete(m.agents, id)
	return nil
}

func (m *memStore) CreateTeam(_ context.Context, t *TeamRecord) error {
	m.teams[t.ID] = t
	return nil
}
func (m *memStore) GetTeam(_ context.Context, id, _ string) (*TeamRecord, error) {
	if t, ok := m.teams[id]; ok {
		return t, nil
	}
	return nil, nil
}
func (m *memStore) ListTeams(_ context.Context, _ string) ([]*TeamRecord, error) {
	out := make([]*TeamRecord, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, t)
	}
	return out, nil
}
func (m *memStore) UpdateTeam(_ context.Context, t *TeamRecord) error {
	m.teams[t.ID] = t
	return nil
}
func (m *memStore) DeleteTeam(_ context.Context, id, _ string) error {
	delete(m.teams, id)
	return nil
}

func (m *memStore) TokenRecordsSince(_ context.Context, _ time.Time) ([]model.TokenRecord, error) {
	return m.tokens, nil
}

// SaveTokenRecord 写入 token 用量记录。
func (m *memStore) SaveTokenRecord(_ context.Context, r *model.TokenRecord) error {
	m.tokens = append(m.tokens, *r)
	return nil
}

func (m *memStore) CreateCustomTool(_ context.Context, t *CustomTool) error {
	m.customs[t.ID] = t
	return nil
}
func (m *memStore) UpdateCustomTool(_ context.Context, t *CustomTool) error {
	m.customs[t.ID] = t
	return nil
}
func (m *memStore) DeleteCustomTool(_ context.Context, id, _ string) error {
	delete(m.customs, id)
	return nil
}
func (m *memStore) ListCustomTools(_ context.Context, _ string) ([]*CustomTool, error) {
	out := make([]*CustomTool, 0, len(m.customs))
	for _, t := range m.customs {
		out = append(out, t)
	}
	return out, nil
}

func (m *memStore) CreateSkillPackage(_ context.Context, p *SkillPackage) error {
	cp := *p
	m.skills[p.ID] = &cp
	return nil
}
func (m *memStore) GetSkillPackage(_ context.Context, _, id string) (*SkillPackage, error) {
	if p, ok := m.skills[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}
func (m *memStore) ListSkillPackages(_ context.Context, _ string) ([]*SkillPackage, error) {
	out := make([]*SkillPackage, 0, len(m.skills))
	for _, p := range m.skills {
		cp := *p
		out = append(out, &cp)
	}
	return out, nil
}
func (m *memStore) DeleteSkillPackage(_ context.Context, _, id string) error {
	delete(m.skills, id)
	return nil
}
func (m *memStore) IncrSkillDownloads(_ context.Context, _, id string) error {
	if p, ok := m.skills[id]; ok {
		p.Downloads++
	}
	return nil
}

func (m *memStore) CreateInspection(_ context.Context, in *Inspection) error {
	m.insps[in.ID] = in
	return nil
}
func (m *memStore) GetInspection(_ context.Context, id, _ string) (*Inspection, error) {
	if in, ok := m.insps[id]; ok {
		return in, nil
	}
	return nil, nil
}
func (m *memStore) ListInspections(_ context.Context, _ string) ([]*Inspection, error) {
	out := make([]*Inspection, 0, len(m.insps))
	for _, in := range m.insps {
		out = append(out, in)
	}
	return out, nil
}
func (m *memStore) UpdateInspection(_ context.Context, in *Inspection) error {
	m.insps[in.ID] = in
	return nil
}
func (m *memStore) DeleteInspection(_ context.Context, id, _ string) error {
	delete(m.insps, id)
	return nil
}
func (m *memStore) CreateInspectionRun(_ context.Context, run *InspectionRun) error {
	m.inspRuns[run.ID] = append(m.inspRuns[run.ID], run)
	return nil
}
func (m *memStore) UpdateInspectionRun(_ context.Context, run *InspectionRun) error {
	m.inspRuns[run.ID] = append(m.inspRuns[run.ID], run)
	return nil
}
func (m *memStore) ListInspectionRuns(_ context.Context, id, _ string) ([]*InspectionRun, error) {
	return m.inspRuns[id], nil
}

// TestSkillMarketFlow 端到端验证技能市场全链路:
// 发布 → 列出 → 读取 → 安装(重建依赖自定义工具 + 新 Agent) → 导出 → 导入 → 删除。
func TestSkillMarketFlow(t *testing.T) {
	ctx := context.Background()
	tenant := "default"

	e := newTestEngine(t)
	e.SetStore(newMemStore())

	// 1) 预置一个自定义工具, 供 Agent 依赖
	depTool := &CustomTool{
		TenantID:    tenant,
		Name:        "ops_ip_lookup",
		Description: "查询 IP 归属",
		Type:        "http",
		Enabled:     true,
		HTTPMethod:  "GET",
		HTTPURL:     "https://example.com/ip/{ip}",
		TimeoutSec:  10,
		Parameters: []CustomToolParam{
			{Name: "ip", Type: "string", Description: "目标 IP", Required: true},
		},
	}
	if err := e.CreateCustomTool(ctx, depTool); err != nil {
		t.Fatalf("create custom tool: %v", err)
	}

	// 2) 创建一个使用上述工具的数字员工
	ag := &model.AgentInstance{
		Name:                "网络巡检员",
		ModelName:           "gpt-4o",
		SystemPrompt:        "你是网络巡检助手, 负责 IP 归属核查。",
		TenantID:            tenant,
		KnowledgeCategories: []string{"network"},
		Tools:               []string{"ops_ip_lookup"},
		Skills: []model.AgentSkill{
			{Name: "ip_audit", Description: "IP 归属审计", Instructions: "对给定的 IP 列表逐一核查归属。"},
		},
	}
	if err := e.CreateAgent(ctx, ag); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// 3) 发布技能包
	pkg, err := e.PublishSkill(ctx, tenant, ag.ID, SkillPackageMeta{
		Author:      "ops-team",
		Description: "网络 IP 巡检技能",
		Category:    "network",
		Tags:        []string{"巡检", "网络"},
	})
	if err != nil {
		t.Fatalf("publish skill: %v", err)
	}
	if pkg.ID == "" || pkg.Checksum == "" {
		t.Fatalf("published package missing id/checksum")
	}
	if len(pkg.Tools) != 1 || pkg.Tools[0].Name != "ops_ip_lookup" {
		t.Fatalf("published package should bundle 1 dependent custom tool, got %d", len(pkg.Tools))
	}
	if len(pkg.Agent.Skills) != 1 {
		t.Fatalf("published package should carry agent skills, got %d", len(pkg.Agent.Skills))
	}

	// 4) 列出
	list := e.ListSkillPackages(ctx, tenant, "", "")
	if len(list) != 1 {
		t.Fatalf("expected 1 package, got %d", len(list))
	}
	// 按分类过滤
	filtered := e.ListSkillPackages(ctx, tenant, "network", "")
	if len(filtered) != 1 {
		t.Fatalf("category filter should keep 1, got %d", len(filtered))
	}
	none := e.ListSkillPackages(ctx, tenant, "db", "")
	if len(none) != 0 {
		t.Fatalf("category filter db should drop to 0, got %d", len(none))
	}

	// 5) 读取
	got, err := e.GetSkillPackage(ctx, tenant, pkg.ID)
	if err != nil || got == nil {
		t.Fatalf("get skill: %v", err)
	}

	// 6) 安装(应幂等重建自定义工具 + 新建 Agent)
	installed, err := e.InstallSkill(ctx, tenant, pkg.ID, "")
	if err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if installed.Name != "网络巡检员 (市场)" {
		t.Fatalf("installed agent name mismatch: %q", installed.Name)
	}
	// 依赖工具仍存在(幂等: 未重复创建)
	toolsAfter := e.ListCustomTools(tenant)
	if len(toolsAfter) != 1 {
		t.Fatalf("custom tool should remain 1 after idempotent install, got %d", len(toolsAfter))
	}
	// 下载计数 +1
	dl, _ := e.GetSkillPackage(ctx, tenant, pkg.ID)
	if dl.Downloads != 1 {
		t.Fatalf("downloads should be 1, got %d", dl.Downloads)
	}

	// 7) 导出
	data, err := e.ExportSkill(ctx, tenant, pkg.ID)
	if err != nil {
		t.Fatalf("export skill: %v", err)
	}
	var exported SkillPackage
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("exported json invalid: %v", err)
	}
	if exported.Checksum != pkg.Checksum {
		t.Fatalf("exported checksum mismatch")
	}

	// 8) 导入(应生成新 ID 归到当前租户)
	imported, err := e.ImportSkill(ctx, tenant, data)
	if err != nil {
		t.Fatalf("import skill: %v", err)
	}
	if imported.ID == pkg.ID {
		t.Fatalf("imported package should have new id")
	}
	if imported.Downloads != 0 {
		t.Fatalf("imported package downloads should reset to 0, got %d", imported.Downloads)
	}
	all := e.ListSkillPackages(ctx, tenant, "", "")
	if len(all) != 2 {
		t.Fatalf("expected 2 packages after import, got %d", len(all))
	}

	// 9) 删除
	if err := e.DeleteSkillPackage(ctx, tenant, pkg.ID); err != nil {
		t.Fatalf("delete skill: %v", err)
	}
	all2 := e.ListSkillPackages(ctx, tenant, "", "")
	if len(all2) != 1 {
		t.Fatalf("expected 1 package after delete, got %d", len(all2))
	}
}

// TestSkillMarketImportBadData 校验导入时的非法数据(缺 agent name / 非法 JSON)。
func TestSkillMarketImportBadData(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	e.SetStore(newMemStore())

	if _, err := e.ImportSkill(ctx, "default", []byte("not-json")); err == nil {
		t.Fatalf("expected error for invalid json")
	}
	if _, err := e.ImportSkill(ctx, "default", []byte(`{"version":"1.0.0"}`)); err == nil {
		t.Fatalf("expected error for missing agent name")
	}
}
