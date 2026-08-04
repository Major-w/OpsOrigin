package itsm

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/itsm/form"
)

// fakeCI 实现 CIProvider，只为验证层级树组装与缓存行为。
type fakeCI struct {
	nodes     []cmdb.CITypeNode
	treeCalls int
}

func (f *fakeCI) ListCIsByTypeName(_ context.Context, _, typeName, _ string, _ int) ([]cmdb.CIRef, error) {
	return []cmdb.CIRef{{ID: "ci-1", Name: "web-01", Type: typeName}}, nil
}

func (f *fakeCI) ListCITypeNames(_ context.Context, _ string) ([]string, error) {
	names := make([]string, 0, len(f.nodes))
	for _, n := range f.nodes {
		names = append(names, n.Name)
	}
	return names, nil
}

func (f *fakeCI) ListCITypeTree(_ context.Context, _ string) ([]cmdb.CITypeNode, error) {
	f.treeCalls++
	return f.nodes, nil
}

func findOpt(opts []form.FieldOption, value string) *form.FieldOption {
	for i := range opts {
		if opts[i].Value == value {
			return &opts[i]
		}
	}
	return nil
}

func TestResolveTreeBuildsTypeHierarchy(t *testing.T) {
	ci := &fakeCI{nodes: []cmdb.CITypeNode{
		{ID: "biz", Name: "业务系统"},
		{ID: "app", Name: "应用", ParentID: "biz"},
		{ID: "mysql", Name: "MySQL 实例", ParentID: "app"},
		{ID: "infra", Name: "基础设施"},
	}}
	r := NewCMDBIAMResolver(ci, nil, nil).(form.TreeSourceResolver)
	ctx := context.Background()

	roots, err := r.ResolveTree(ctx, "t1", SourceCMDBTypes, "")
	if err != nil {
		t.Fatalf("根层级解析失败: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("应有 2 个根节点，实际 %d: %+v", len(roots), roots)
	}
	biz := findOpt(roots, "biz")
	if biz == nil || biz.Label != "业务系统" || len(biz.Children) != 1 {
		t.Fatalf("业务系统子树异常: %+v", biz)
	}
	if biz.Children[0].Value != "app" || len(biz.Children[0].Children) != 1 {
		t.Fatalf("第三级未挂上: %+v", biz.Children[0])
	}

	// 指定 parent 时只返回该子树
	kids, err := r.ResolveTree(ctx, "t1", SourceCMDBTypes, "app")
	if err != nil || len(kids) != 1 || kids[0].Value != "mysql" {
		t.Fatalf("按 parent 取子节点异常: %v / %+v", err, kids)
	}

	// 叶子节点无子级
	leaf, err := r.ResolveTree(ctx, "t1", SourceCMDBTypes, "mysql")
	if err != nil || len(leaf) != 0 {
		t.Fatalf("叶子节点应无子级: %v / %+v", err, leaf)
	}
}

func TestResolveTreeCachesByParent(t *testing.T) {
	ci := &fakeCI{nodes: []cmdb.CITypeNode{{ID: "biz", Name: "业务系统"}}}
	r := NewCMDBIAMResolver(ci, nil, nil).(form.TreeSourceResolver)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := r.ResolveTree(ctx, "t1", SourceCMDBTypes, ""); err != nil {
			t.Fatalf("第 %d 次解析失败: %v", i, err)
		}
	}
	if ci.treeCalls != 1 {
		t.Fatalf("同一 parent 应命中缓存只打一次后端，实际 %d 次", ci.treeCalls)
	}
}

func TestResolveTreeOrphanPromotedToRoot(t *testing.T) {
	// 父节点被删/跨租户引用：子树上提到根，避免整批类型凭空消失
	ci := &fakeCI{nodes: []cmdb.CITypeNode{
		{ID: "app", Name: "应用", ParentID: "ghost"},
		{ID: "mysql", Name: "MySQL 实例", ParentID: "app"},
	}}
	r := NewCMDBIAMResolver(ci, nil, nil).(form.TreeSourceResolver)

	roots, err := r.ResolveTree(context.Background(), "t1", SourceCMDBTypes, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(roots) != 1 || roots[0].Value != "app" || len(roots[0].Children) != 1 {
		t.Fatalf("孤儿节点未上提到根: %+v", roots)
	}
}

func TestResolveTreeSurvivesCycle(t *testing.T) {
	// 脏数据环路 a→b→a：visited + 深度上限兜底，不能栈溢出或死循环
	ci := &fakeCI{nodes: []cmdb.CITypeNode{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a"},
	}}
	r := NewCMDBIAMResolver(ci, nil, nil).(form.TreeSourceResolver)

	roots, err := r.ResolveTree(context.Background(), "t1", SourceCMDBTypes, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("全环路数据无根节点，实际: %+v", roots)
	}
	// 从环内任一节点进入也应能收敛
	sub, err := r.ResolveTree(context.Background(), "t1", SourceCMDBTypes, "a")
	if err != nil {
		t.Fatalf("按 parent 解析失败: %v", err)
	}
	if len(sub) != 1 || sub[0].Value != "b" {
		t.Fatalf("环内子节点解析异常: %+v", sub)
	}
}

func TestResolveTreeFlatSourceFallback(t *testing.T) {
	ci := &fakeCI{nodes: []cmdb.CITypeNode{{ID: "host", Name: "host"}}}
	r := NewCMDBIAMResolver(ci, nil, nil).(form.TreeSourceResolver)
	ctx := context.Background()

	// 无层级语义的来源：根层级给全量
	roots, err := r.ResolveTree(ctx, "t1", "cmdb:host", "")
	if err != nil || len(roots) != 1 || roots[0].Value != "ci-1" {
		t.Fatalf("平铺来源根层级异常: %v / %+v", err, roots)
	}
	// 子层级为空，前端据此判定为叶子
	kids, err := r.ResolveTree(ctx, "t1", "cmdb:host", "ci-1")
	if err != nil || len(kids) != 0 {
		t.Fatalf("平铺来源不应有子层级: %v / %+v", err, kids)
	}
}

func TestSourceCatalogIncludesTypeTree(t *testing.T) {
	ci := &fakeCI{nodes: []cmdb.CITypeNode{{ID: "host", Name: "host"}}}
	c, ok := NewCMDBIAMResolver(ci, nil, nil).(sourceCataloger)
	if !ok {
		t.Fatal("解析器应实现 sourceCataloger")
	}
	var hasTypes, hasHost bool
	for _, s := range c.SourceCatalog(context.Background(), "t1") {
		if s == SourceCMDBTypes {
			hasTypes = true
		}
		if s == "cmdb:host" {
			hasHost = true
		}
	}
	if !hasTypes || !hasHost {
		t.Fatalf("目录应同时含层级来源与具体类型（types=%v host=%v）", hasTypes, hasHost)
	}
}
