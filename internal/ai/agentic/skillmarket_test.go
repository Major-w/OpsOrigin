package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	ratings  map[string]int // key: tenant|pkgID|user -> score
	favs     map[string]bool
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
		ratings:  map[string]int{},
		favs:     map[string]bool{},
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
func (m *memStore) GetSkillPackageByNameVersion(_ context.Context, _, name, version string) (*SkillPackage, error) {
	for _, p := range m.skills {
		if p.Name == name && p.Version == version {
			cp := *p
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *memStore) ListSkillPackageVersions(_ context.Context, _, name string) ([]*SkillPackage, error) {
	out := make([]*SkillPackage, 0)
	for _, p := range m.skills {
		if p.Name == name {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (m *memStore) RateSkill(_ context.Context, tenantID, pkgID, user string, score int) error {
	if _, ok := m.skills[pkgID]; !ok {
		return fmt.Errorf("package not found: %s", pkgID)
	}
	m.ratings[tenantID+"|"+pkgID+"|"+user] = score
	// 重算均值写回
	var sum, cnt int
	for k, sc := range m.ratings {
		if strings.HasPrefix(k, tenantID+"|"+pkgID+"|") {
			sum += sc
			cnt++
		}
	}
	if p, ok := m.skills[pkgID]; ok && cnt > 0 {
		p.RatingAvg = float64(sum) / float64(cnt)
		p.RatingCount = cnt
	}
	return nil
}
func (m *memStore) GetSkillRating(_ context.Context, tenantID, pkgID, user string) (float64, int, int, error) {
	var sum, cnt, my int
	for k, sc := range m.ratings {
		if strings.HasPrefix(k, tenantID+"|"+pkgID+"|") {
			sum += sc
			cnt++
		}
	}
	if sc, ok := m.ratings[tenantID+"|"+pkgID+"|"+user]; ok {
		my = sc
	}
	avg := 0.0
	if cnt > 0 {
		avg = float64(sum) / float64(cnt)
	}
	return avg, cnt, my, nil
}
func (m *memStore) ToggleFavorite(_ context.Context, tenantID, pkgID, user string) (bool, error) {
	key := tenantID + "|" + pkgID + "|" + user
	if m.favs[key] {
		delete(m.favs, key)
		return false, nil
	}
	m.favs[key] = true
	return true, nil
}
func (m *memStore) IsFavorite(_ context.Context, tenantID, pkgID, user string) (bool, error) {
	return m.favs[tenantID+"|"+pkgID+"|"+user], nil
}
func (m *memStore) ListFavoriteIDs(_ context.Context, tenantID, user string) ([]string, error) {
	prefix := tenantID + "|"
	suffix := "|" + user
	var out []string
	for k := range m.favs {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, suffix) {
			// 去掉 tenant| 前缀与 |user 后缀得到 pkgID
			mid := k[len(prefix) : len(k)-len(suffix)]
			out = append(out, mid)
		}
	}
	return out, nil
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

// TestSkillMarketRatingFavoriteVersions 验证评分聚合 / 收藏切换 / 同名版本列表。
func TestSkillMarketRatingFavoriteVersions(t *testing.T) {
	ctx := context.Background()
	tenant := "default"
	e := newTestEngine(t)
	e.SetStore(newMemStore())

	// 预置并发布一个技能包 v1.0.0
	depTool := &CustomTool{TenantID: tenant, Name: "ops_ping", Type: "http", Enabled: true, HTTPURL: "https://e/p"}
	_ = e.CreateCustomTool(ctx, depTool)
	ag := &model.AgentInstance{Name: "巡检工", TenantID: tenant, Tools: []string{"ops_ping"}}
	_ = e.CreateAgent(ctx, ag)
	pkg, err := e.PublishSkill(ctx, tenant, ag.ID, SkillPackageMeta{Version: "1.0.0"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// 评分: 三个用户打分 5/3/4 → 均值 4, 计数 3
	for u, s := range map[string]int{"alice": 5, "bob": 3, "carol": 4} {
		if err := e.RateSkill(ctx, tenant, pkg.ID, u, s); err != nil {
			t.Fatalf("rate %s: %v", u, err)
		}
	}
	avg, cnt, my, err := e.GetSkillRating(ctx, tenant, pkg.ID, "alice")
	if err != nil {
		t.Fatalf("get rating: %v", err)
	}
	if cnt != 3 || my != 5 {
		t.Fatalf("rating aggregate wrong: cnt=%d my=%d", cnt, my)
	}
	if avg < 3.9 || avg > 4.1 {
		t.Fatalf("rating avg should be ~4, got %v", avg)
	}
	got, _ := e.GetSkillPackage(ctx, tenant, pkg.ID)
	if got.RatingAvg != avg || got.RatingCount != 3 {
		t.Fatalf("rating fields not persisted on package: avg=%v cnt=%d", got.RatingAvg, got.RatingCount)
	}

	// 非法评分应报错
	if err := e.RateSkill(ctx, tenant, pkg.ID, "dave", 9); err == nil {
		t.Fatalf("expected error for out-of-range score")
	}

	// 收藏: 切换两次 → 先 true 后 false
	f1, _ := e.ToggleFavorite(ctx, tenant, pkg.ID, "alice")
	if !f1 {
		t.Fatalf("first favorite should be true")
	}
	// 同用户再次打分应更新而非新增(计数仍为 3)
	_ = e.RateSkill(ctx, tenant, pkg.ID, "alice", 2)
	_, cnt2, my2, _ := e.GetSkillRating(ctx, tenant, pkg.ID, "alice")
	if cnt2 != 3 || my2 != 2 {
		t.Fatalf("re-rate should update not append: cnt=%d my=%d", cnt2, my2)
	}
	f2, _ := e.ToggleFavorite(ctx, tenant, pkg.ID, "alice")
	if f2 {
		t.Fatalf("second favorite should be false (toggled off)")
	}
	// alice 取消后列表应为空
	favs := e.ListFavorites(ctx, tenant, "alice")
	if len(favs) != 0 {
		t.Fatalf("alice should have 0 favorites after toggle off, got %d", len(favs))
	}
	// bob 收藏后列表含 1
	_, _ = e.ToggleFavorite(ctx, tenant, pkg.ID, "bob")
	favs2 := e.ListFavorites(ctx, tenant, "bob")
	if len(favs2) != 1 {
		t.Fatalf("bob should have 1 favorite, got %d", len(favs2))
	}

	// 版本列表: 发布同名 v2.0.0, 应列出 2 个版本
	v2, err := e.PublishSkill(ctx, tenant, ag.ID, SkillPackageMeta{Version: "2.0.0"})
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	vs := e.ListVersions(ctx, tenant, pkg.ID)
	if len(vs) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(vs))
	}
	// 再发同名同版本 v2.0.0 应覆盖而非新增(仍为 2 个)
	if _, err := e.PublishSkill(ctx, tenant, ag.ID, SkillPackageMeta{Version: "2.0.0"}); err != nil {
		t.Fatalf("republish v2: %v", err)
	}
	if vs2 := e.ListVersions(ctx, tenant, v2.ID); len(vs2) != 2 {
		t.Fatalf("republish same version should not add row, got %d", len(vs2))
	}
}
