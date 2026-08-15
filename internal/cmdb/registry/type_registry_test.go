package registry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// ---- 内存 fake catalog：实现 TypeCatalog 窄接口，供注册表单测 ----

type fakeCatalog struct {
	types []*model.CIModelType
}

func (f *fakeCatalog) ListCIModelTypes(_ context.Context, tenantID string, limit, offset int) ([]*model.CIModelType, error) {
	out := make([]*model.CIModelType, 0, len(f.types))
	for _, t := range f.types {
		if t.TenantID == tenantID {
			out = append(out, t)
		}
	}
	if offset >= len(out) {
		return []*model.CIModelType{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (f *fakeCatalog) GetCIModelType(_ context.Context, id, tenantID string) (*model.CIModelType, error) {
	for _, t := range f.types {
		if t.ID == id && t.TenantID == tenantID {
			cp := *t
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeCatalog) GetCIModelTypeByName(_ context.Context, name, tenantID string) (*model.CIModelType, error) {
	for _, t := range f.types {
		if t.Name == name && t.TenantID == tenantID {
			cp := *t
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeCatalog) CreateCIModelType(_ context.Context, mt *model.CIModelType) error {
	for _, t := range f.types {
		if t.ID == mt.ID && t.TenantID == mt.TenantID {
			return fmt.Errorf("duplicate model type id %s", mt.ID)
		}
	}
	cp := *mt
	f.types = append(f.types, &cp)
	return nil
}

func (f *fakeCatalog) UpdateCIModelType(_ context.Context, mt *model.CIModelType) error {
	for i, t := range f.types {
		if t.ID == mt.ID && t.TenantID == mt.TenantID {
			cp := *mt
			f.types[i] = &cp
			return nil
		}
	}
	return fmt.Errorf("model type %s not found", mt.ID)
}

// ---- 种子构造 ----

const testTenant = "tenant-test"

func newType(id, name string, abstract bool, parentID, category string, schema map[string]model.AttrDef, confidence float64) *model.CIModelType {
	now := time.Now()
	return &model.CIModelType{
		ID:         id,
		TenantID:   testTenant,
		Name:       name,
		Abstract:   abstract,
		ParentID:   parentID,
		Category:   category,
		Schema:     schema,
		Confidence: confidence,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// 两个 abstract 父类 + 一个 mysql leaf，模拟方案 C 种子。
func seedBasicCatalog() *fakeCatalog {
	return &fakeCatalog{types: []*model.CIModelType{
		newType(model.ModelTypeDatabase, "数据库", true, "", model.ModelCategoryDatabase, map[string]model.AttrDef{
			"port":     {Type: "int", Required: true, Description: "监听端口"},
			"instance": {Type: "string", Description: "实例名"},
		}, 1.0),
		newType(model.ModelTypeMiddleware, "中间件", true, "", model.ModelCategoryMiddleware, map[string]model.AttrDef{
			"port": {Type: "int", Required: true, Description: "监听端口"},
		}, 1.0),
		newType("mysql", "MySQL", false, model.ModelTypeDatabase, model.ModelCategoryDatabase, map[string]model.AttrDef{
			"version": {Type: "string", Description: "版本"},
		}, 0.9),
	}}
}

// ---- 测试：相似度去重（mysql_8 → mysql 合并）----

func TestProposeType_MergeSimilar(t *testing.T) {
	cat := seedBasicCatalog()
	reg := New(cat)

	// 提案 mysql_8：归一化后 == mysql，应合并到已有 mysql 而非新建。
	id, status, err := reg.ProposeType(context.Background(), AITypeProposal{
		TenantID:   testTenant,
		Name:       "mysql_8",
		Confidence: 0.3,
		Evidence:   []string{"banner=MySQL 8.0"},
	})
	if err != nil {
		t.Fatalf("ProposeType error: %v", err)
	}
	if status != ProposalMerged {
		t.Fatalf("expected ProposalMerged, got %s", status)
	}
	if id != "mysql" {
		t.Fatalf("expected merged into mysql, got %s", id)
	}

	// 合并后 mysql 证据应增长，置信度应小幅提升（+0.05）。
	mt, err := cat.GetCIModelType(context.Background(), "mysql", testTenant)
	if err != nil {
		t.Fatalf("GetModelType error: %v", err)
	}
	if mt == nil {
		t.Fatal("mysql type should still exist")
	}
	found := false
	for _, e := range mt.Evidence {
		if e == "banner=MySQL 8.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged evidence missing, got %v", mt.Evidence)
	}
	if mt.Confidence < 0.9+mergeConfidenceBoost-1e-9 {
		t.Fatalf("expected confidence boosted to ~%.3f, got %.3f", 0.9+mergeConfidenceBoost, mt.Confidence)
	}
}

// ---- 测试：全新提案（无相似）创建记录并 pending/active 门控 ----

func TestProposeType_NewAndActivationGate(t *testing.T) {
	cat := seedBasicCatalog()
	reg := New(cat)

	// 低置信 → pending
	idLow, statusLow, err := reg.ProposeType(context.Background(), AITypeProposal{
		TenantID:   testTenant,
		Name:       "cassandra",
		Confidence: 0.3,
	})
	if err != nil {
		t.Fatalf("ProposeType(low) error: %v", err)
	}
	if statusLow != ProposalPending {
		t.Fatalf("expected ProposalPending, got %s", statusLow)
	}
	if idLow != "cassandra" {
		t.Fatalf("expected id cassandra, got %s", idLow)
	}

	// 高置信 → active，置信度被钳制到 autoActivateThreshold 之上
	idHigh, statusHigh, err := reg.ProposeType(context.Background(), AITypeProposal{
		TenantID:   testTenant,
		Name:       "tidb",
		Confidence: 0.95,
	})
	if err != nil {
		t.Fatalf("ProposeType(high) error: %v", err)
	}
	if statusHigh != ProposalActive {
		t.Fatalf("expected ProposalActive, got %s", statusHigh)
	}
	mt, _ := cat.GetCIModelType(context.Background(), idHigh, testTenant)
	if mt == nil || mt.Confidence < autoActivateThreshold {
		t.Fatalf("expected active confidence >= %.2f, got %v", autoActivateThreshold, mt)
	}
}

// ---- 测试：ResolveType 三分支 ----

func TestResolveType_ResolvedLeaf(t *testing.T) {
	cat := seedBasicCatalog()
	reg := New(cat)

	res, err := reg.ResolveType(context.Background(), testTenant, map[string]any{"engine": "mysql"})
	if err != nil {
		t.Fatalf("ResolveType error: %v", err)
	}
	if res.Kind != ResolvedLeaf {
		t.Fatalf("expected ResolvedLeaf, got %s", res.Kind)
	}
	if res.TypeID != "mysql" {
		t.Fatalf("expected mysql, got %s", res.TypeID)
	}
	if res.Confidence != resolveLeafConfidence {
		t.Fatalf("expected %.2f, got %.2f", resolveLeafConfidence, res.Confidence)
	}
}

func TestResolveType_AttachedToParent(t *testing.T) {
	// 仅父类，无精确 leaf：9200 归 elasticsearch 家族但库内无 es leaf → 挂 middleware
	cat := &fakeCatalog{types: []*model.CIModelType{
		newType(model.ModelTypeDatabase, "数据库", true, "", model.ModelCategoryDatabase, nil, 1.0),
		newType(model.ModelTypeMiddleware, "中间件", true, "", model.ModelCategoryMiddleware, nil, 1.0),
	}}
	reg := New(cat)

	res, err := reg.ResolveType(context.Background(), testTenant, map[string]any{"port": 9200})
	if err != nil {
		t.Fatalf("ResolveType error: %v", err)
	}
	if res.Kind != AttachedToParent {
		t.Fatalf("expected AttachedToParent, got %s", res.Kind)
	}
	if res.TypeID != model.ModelTypeMiddleware {
		t.Fatalf("expected %s, got %s", model.ModelTypeMiddleware, res.TypeID)
	}
}

func TestResolveType_ProposedNew(t *testing.T) {
	cat := seedBasicCatalog()
	reg := New(cat)

	// cassandra 不在任何 hint 中，且无家族命中 → 全新提案
	res, err := reg.ResolveType(context.Background(), testTenant, map[string]any{"engine": "cassandra"})
	if err != nil {
		t.Fatalf("ResolveType error: %v", err)
	}
	if res.Kind != ProposedNew {
		t.Fatalf("expected ProposedNew, got %s", res.Kind)
	}
	if res.ProposalID == "" {
		t.Fatal("expected non-empty ProposalID")
	}
	// 提案应已落库
	mt, _ := cat.GetCIModelType(context.Background(), res.ProposalID, testTenant)
	if mt == nil {
		t.Fatal("proposed type should be persisted")
	}
	if mt.Source != string(model.SourceAIProposed) {
		t.Fatalf("expected source ai_proposed, got %s", mt.Source)
	}
}

// ---- 测试：EffectiveSchema 继承合并 ----

func TestEffectiveSchema_Inheritance(t *testing.T) {
	cat := &fakeCatalog{types: []*model.CIModelType{
		newType(model.ModelTypeMiddleware, "中间件", true, "", model.ModelCategoryMiddleware, map[string]model.AttrDef{
			"port": {Type: "int", Required: true, Description: "父类端口"},
		}, 1.0),
		newType("redis", "Redis", false, model.ModelTypeMiddleware, model.ModelCategoryMiddleware, map[string]model.AttrDef{
			"mode": {Type: "string", Description: "子类模式"},
		}, 0.9),
	}}
	reg := New(cat)

	sch, err := reg.EffectiveSchema(context.Background(), testTenant, "redis")
	if err != nil {
		t.Fatalf("EffectiveSchema error: %v", err)
	}
	if _, ok := sch["port"]; !ok {
		t.Fatal("expected inherited port from parent")
	}
	if _, ok := sch["mode"]; !ok {
		t.Fatal("expected own mode field")
	}
	if len(sch) != 2 {
		t.Fatalf("expected 2 effective attrs (port+mode), got %d: %v", len(sch), sch)
	}
}

// ---- 测试：PromoteType 证据累积达阈值 ----

func TestPromoteType_AccumulatesToActive(t *testing.T) {
	cat := seedBasicCatalog()
	reg := New(cat)

	id, status, err := reg.ProposeType(context.Background(), AITypeProposal{
		TenantID:   testTenant,
		Name:       "cassandra",
		Confidence: 0.3,
		Evidence:   []string{"seed=ev"},
	})
	if err != nil {
		t.Fatalf("ProposeType error: %v", err)
	}
	if status != ProposalPending {
		t.Fatalf("expected pending start, got %s", status)
	}

	// 0.3 + 0.15*4 = 0.9 ≥ 0.8
	for i := 0; i < 4; i++ {
		if err := reg.PromoteType(context.Background(), testTenant, id, []string{fmt.Sprintf("ev-%d", i)}); err != nil {
			t.Fatalf("PromoteType #%d error: %v", i, err)
		}
	}

	mt, _ := cat.GetCIModelType(context.Background(), id, testTenant)
	if mt == nil {
		t.Fatal("cassandra should exist")
	}
	if mt.Confidence < autoActivateThreshold {
		t.Fatalf("expected confidence >= %.2f after promotions, got %.3f", autoActivateThreshold, mt.Confidence)
	}
	// 证据应累积：seed + 4 次 distinct
	if len(mt.Evidence) != 5 {
		t.Fatalf("expected 5 evidence entries, got %d: %v", len(mt.Evidence), mt.Evidence)
	}
}

// ---- 纯函数单测：归一化与相似度 ----

func TestNormalizeTypeID(t *testing.T) {
	cases := map[string]string{
		"MySQL 8.0":         "mysql",
		"mysql_8":           "mysql",
		"PostgreSQL-15":     "postgresql",
		"redis_cluster":     "redis",
		"pg":                "postgresql",
		"MongoDB":           "mongodb",
		"ElasticSearch_7.9": "elasticsearch",
		"  ZooKeeper ":      "zookeeper",
	}
	for in, want := range cases {
		if got := normalizeTypeID(in); got != want {
			t.Fatalf("normalizeTypeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSimilar(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"mysql", "mysql", true},
		{"mysql", "mysql8", true}, // 归一化后相等
		{"redis", "redi", true},   // 编辑距离 1，长度差 1
		{"redis", "redisx", true}, // 编辑距离 1，长度差 1
		{"mysql", "oracle", false},
		{"mongodb", "redis", false},
	}
	for _, c := range cases {
		if got := similar(c.a, c.b); got != c.want {
			t.Fatalf("similar(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
