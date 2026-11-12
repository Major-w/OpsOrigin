package assets

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/itsm/form"
)

// Store 决策表资产与动作模板的持久化能力。
// PGStore 用于生产（自愈 DDL + 事务发布），MemoryStore 用于无 DB 的本地/测试场景。
// 所有方法都强制 tenant_id 隔离，调用方从请求 ctx 取租户。
type Store interface {
	Init(ctx context.Context) error

	ListDecisionTables(ctx context.Context, tenantID string) ([]*DecisionTableAsset, error)
	GetDecisionTable(ctx context.Context, tenantID, id string) (*DecisionTableAsset, error)
	UpsertDecisionTable(ctx context.Context, tenantID string, a *DecisionTableAsset) error
	DeleteDecisionTable(ctx context.Context, tenantID, id string) error
	// Publish 以给定内容发布一个新版本（回滚时内容取历史快照），事务内递增版本号。
	Publish(ctx context.Context, tenantID, id string, table form.DecisionTable, note, createdBy string) (*DecisionTableVersion, error)
	ListVersions(ctx context.Context, tenantID, assetID string) ([]*DecisionTableVersion, error)
	GetVersion(ctx context.Context, tenantID, assetID string, version int) (*DecisionTableVersion, error)
	// ResolveDecisionTable 实现 form.DecisionTableResolver：version=0 跟随最新已发布版本。
	ResolveDecisionTable(ctx context.Context, tenantID, assetID string, version int) (*form.DecisionTable, error)

	ListActionTemplates(ctx context.Context, tenantID string) ([]*ActionTemplate, error)
	GetActionTemplate(ctx context.Context, tenantID, id string) (*ActionTemplate, error)
	UpsertActionTemplate(ctx context.Context, tenantID string, t *ActionTemplate) error
	DeleteActionTemplate(ctx context.Context, tenantID, id string) error
	// PublishActionTemplate 以模板当前草稿内容追加一个不可变版本快照。
	PublishActionTemplate(ctx context.Context, tenantID, id, note, createdBy string) (*ActionTemplateVersion, error)
	ListActionTemplateVersions(ctx context.Context, tenantID, templateID string) ([]*ActionTemplateVersion, error)
	GetActionTemplateVersion(ctx context.Context, tenantID, templateID string, version int) (*ActionTemplateVersion, error)
	// ResolveActionTemplate 供流程引擎运行期解析：version=0 跟随最新已发布版本。
	// 模板停用 / 未发布 / 版本缺失一律返回 error，调用方 fail-closed。
	ResolveActionTemplate(ctx context.Context, tenantID, templateID string, version int) (*ActionTemplateVersion, error)
}

// --- MemoryStore ---

type MemoryStore struct {
	mu               sync.RWMutex
	decisions        map[string]map[string]*DecisionTableAsset
	versions         map[string]map[string][]*DecisionTableVersion
	templates        map[string]map[string]*ActionTemplate
	templateVersions map[string]map[string][]*ActionTemplateVersion
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		decisions:        map[string]map[string]*DecisionTableAsset{},
		versions:         map[string]map[string][]*DecisionTableVersion{},
		templates:        map[string]map[string]*ActionTemplate{},
		templateVersions: map[string]map[string][]*ActionTemplateVersion{},
	}
}

func (m *MemoryStore) Init(context.Context) error { return nil }

func (m *MemoryStore) ListDecisionTables(_ context.Context, tenantID string) ([]*DecisionTableAsset, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*DecisionTableAsset, 0, len(m.decisions[tenantID]))
	for _, a := range m.decisions[tenantID] {
		out = append(out, cloneDecision(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemoryStore) GetDecisionTable(_ context.Context, tenantID, id string) (*DecisionTableAsset, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a := m.decisions[tenantID][id]
	if a == nil {
		return nil, ErrNotFound
	}
	return cloneDecision(a), nil
}

func (m *MemoryStore) UpsertDecisionTable(_ context.Context, tenantID string, a *DecisionTableAsset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.decisions[tenantID] == nil {
		m.decisions[tenantID] = map[string]*DecisionTableAsset{}
	}
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.TenantID = tenantID
	a.UpdatedAt = now
	if a.Status == "" {
		a.Status = StatusDraft
	}
	m.decisions[tenantID][a.ID] = cloneDecision(a)
	return nil
}

func (m *MemoryStore) DeleteDecisionTable(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.decisions[tenantID][id] == nil {
		return ErrNotFound
	}
	delete(m.decisions[tenantID], id)
	if m.versions[tenantID] != nil {
		delete(m.versions[tenantID], id)
	}
	return nil
}

func (m *MemoryStore) Publish(_ context.Context, tenantID, id string, table form.DecisionTable, note, createdBy string) (*DecisionTableVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.decisions[tenantID][id]
	if a == nil {
		return nil, ErrNotFound
	}
	a.Table = table
	a.Status = StatusPublished
	a.Version++
	a.UpdatedAt = time.Now()
	v := &DecisionTableVersion{
		ID: uuid.New().String(), TenantID: tenantID, AssetID: id,
		Version: a.Version, Table: table, Note: note, CreatedBy: createdBy, CreatedAt: time.Now(),
	}
	if m.versions[tenantID] == nil {
		m.versions[tenantID] = map[string][]*DecisionTableVersion{}
	}
	m.versions[tenantID][id] = append(m.versions[tenantID][id], v)
	return v, nil
}

func (m *MemoryStore) ListVersions(_ context.Context, tenantID, assetID string) ([]*DecisionTableVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]*DecisionTableVersion{}, m.versions[tenantID][assetID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (m *MemoryStore) GetVersion(_ context.Context, tenantID, assetID string, version int) (*DecisionTableVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.versions[tenantID][assetID] {
		if v.Version == version {
			cp := *v
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) ResolveDecisionTable(ctx context.Context, tenantID, assetID string, version int) (*form.DecisionTable, error) {
	a, err := m.GetDecisionTable(ctx, tenantID, assetID)
	if err != nil {
		return nil, nil // 资产不存在：容错按空表处理
	}
	if a.Status != StatusPublished || a.Version < 1 {
		return nil, nil // 未发布：引用不生效
	}
	if version == 0 {
		version = a.Version
	}
	v, err := m.GetVersion(ctx, tenantID, assetID, version)
	if err != nil || v == nil {
		return nil, nil
	}
	t := v.Table
	t.Name = a.Name
	return &t, nil
}

// --- 动作模板（草稿 + 不可变版本）---

func (m *MemoryStore) ListActionTemplates(_ context.Context, tenantID string) ([]*ActionTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*ActionTemplate, 0, len(m.templates[tenantID]))
	for _, t := range m.templates[tenantID] {
		out = append(out, cloneTemplate(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemoryStore) GetActionTemplate(_ context.Context, tenantID, id string) (*ActionTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := m.templates[tenantID][id]
	if t == nil {
		return nil, ErrNotFound
	}
	return cloneTemplate(t), nil
}

func (m *MemoryStore) UpsertActionTemplate(_ context.Context, tenantID string, t *ActionTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.templates[tenantID] == nil {
		m.templates[tenantID] = map[string]*ActionTemplate{}
	}
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.TenantID = tenantID
	t.UpdatedAt = time.Now()
	if t.Inputs == nil {
		t.Inputs = map[string]any{}
	}
	// 状态与版本由发布动作管理，客户端传值一律忽略；保存草稿不改已发布版本。
	if existing := m.templates[tenantID][t.ID]; existing != nil {
		t.Status, t.Version = existing.Status, existing.Version
	} else {
		t.Status, t.Version = StatusDraft, 0
	}
	m.templates[tenantID][t.ID] = cloneTemplate(t)
	return nil
}

func (m *MemoryStore) DeleteActionTemplate(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.templates[tenantID][id] == nil {
		return ErrNotFound
	}
	delete(m.templates[tenantID], id)
	if m.templateVersions[tenantID] != nil {
		delete(m.templateVersions[tenantID], id)
	}
	return nil
}

func (m *MemoryStore) PublishActionTemplate(_ context.Context, tenantID, id, note, createdBy string) (*ActionTemplateVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.templates[tenantID][id]
	if t == nil {
		return nil, ErrNotFound
	}
	if !IsBuiltinActionType(t.ActionType) {
		return nil, fmt.Errorf("action_type %q 不在内置动作白名单内", t.ActionType)
	}
	t.Version++
	t.Status = StatusPublished
	t.UpdatedAt = time.Now()
	v := &ActionTemplateVersion{
		ID: uuid.New().String(), TenantID: tenantID, TemplateID: id,
		Version: t.Version, ActionType: t.ActionType, Inputs: cloneTemplate(t).Inputs,
		Note: note, CreatedBy: createdBy, CreatedAt: time.Now(),
	}
	if m.templateVersions[tenantID] == nil {
		m.templateVersions[tenantID] = map[string][]*ActionTemplateVersion{}
	}
	m.templateVersions[tenantID][id] = append(m.templateVersions[tenantID][id], v)
	return cloneTemplateVersion(v), nil
}

func (m *MemoryStore) ListActionTemplateVersions(_ context.Context, tenantID, templateID string) ([]*ActionTemplateVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*ActionTemplateVersion, 0, len(m.templateVersions[tenantID][templateID]))
	for _, v := range m.templateVersions[tenantID][templateID] {
		out = append(out, cloneTemplateVersion(v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (m *MemoryStore) GetActionTemplateVersion(_ context.Context, tenantID, templateID string, version int) (*ActionTemplateVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.templateVersions[tenantID][templateID] {
		if v.Version == version {
			return cloneTemplateVersion(v), nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) ResolveActionTemplate(ctx context.Context, tenantID, templateID string, version int) (*ActionTemplateVersion, error) {
	t, err := m.GetActionTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !t.Enabled {
		return nil, fmt.Errorf("动作模板「%s」已停用", t.Name)
	}
	if t.Status != StatusPublished || t.Version < 1 {
		return nil, fmt.Errorf("动作模板「%s」尚未发布", t.Name)
	}
	if version == 0 {
		version = t.Version
	}
	return m.GetActionTemplateVersion(ctx, tenantID, templateID, version)
}

func cloneDecision(a *DecisionTableAsset) *DecisionTableAsset {
	cp := *a
	cp.UsedBy = append([]string{}, a.UsedBy...)
	return &cp
}

func cloneTemplate(t *ActionTemplate) *ActionTemplate {
	cp := *t
	cp.Inputs = map[string]any{}
	for k, v := range t.Inputs {
		cp.Inputs[k] = v
	}
	cp.UsedBy = append([]string{}, t.UsedBy...)
	return &cp
}

func cloneTemplateVersion(v *ActionTemplateVersion) *ActionTemplateVersion {
	cp := *v
	cp.Inputs = map[string]any{}
	for k, val := range v.Inputs {
		cp.Inputs[k] = val
	}
	return &cp
}
