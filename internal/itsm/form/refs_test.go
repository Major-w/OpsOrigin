package form

import (
	"context"
	"testing"
)

type stubDecisionResolver struct {
	table   *DecisionTable
	err     error
	lastID  string
	lastVer int
	calls   int
}

func (r *stubDecisionResolver) ResolveDecisionTable(_ context.Context, _, assetID string, version int) (*DecisionTable, error) {
	r.calls++
	r.lastID, r.lastVer = assetID, version
	return r.table, r.err
}

func publishedSnapshot(level string) *DecisionTable {
	return &DecisionTable{
		Name:          "事件分级矩阵",
		ConditionKeys: []string{"env", "impact"},
		OutputKeys:    []string{"level"},
		Rows: []DecisionRow{
			{Cells: map[string]string{"env": "prod", "impact": "global"}, Outputs: map[string]string{"level": level}},
		},
	}
}

func refSchema() *FormSchema {
	return &FormSchema{
		Version: 1,
		Scope:   "test:ref",
		Fields: []FieldDef{
			{Key: "env", Label: "环境", Type: TypeText},
			{Key: "impact", Label: "影响范围", Type: TypeText},
			{Key: "level", Label: "级别", Type: TypeText},
			{Key: "inline_out", Label: "内嵌输出", Type: TypeText},
		},
		DecisionTables: []DecisionTable{
			{ID: "dt-ref", AssetRef: &DecisionTableAssetRef{AssetID: "asset-1"}},
			{
				ID:            "dt-inline",
				ConditionKeys: []string{"env"},
				OutputKeys:    []string{"inline_out"},
				Rows:          []DecisionRow{{Cells: map[string]string{"env": "test"}, Outputs: map[string]string{"inline_out": "ok"}}},
			},
		},
	}
}

// 纯引用态：按已发布版本展开，保留 AssetRef 与表单内的 ID，且不修改入参 schema。
func TestExpandDecisionRefsResolvesSnapshot(t *testing.T) {
	resolver := &stubDecisionResolver{table: publishedSnapshot("P1")}
	schema := refSchema()

	got, err := ExpandDecisionRefs(context.Background(), "tenant-a", schema, resolver)
	if err != nil {
		t.Fatalf("展开不应报错: %v", err)
	}
	if resolver.calls != 1 || resolver.lastID != "asset-1" || resolver.lastVer != 0 {
		t.Fatalf("解析器参数错误: calls=%d id=%s version=%d", resolver.calls, resolver.lastID, resolver.lastVer)
	}
	ref := got.DecisionTables[0]
	if ref.AssetRef == nil || ref.AssetRef.AssetID != "asset-1" {
		t.Fatalf("展开后必须保留引用标记，实际 %+v", ref.AssetRef)
	}
	if ref.ID != "dt-ref" || len(ref.ConditionKeys) != 2 || len(ref.Rows) != 1 {
		t.Fatalf("展开内容不正确: %+v", ref)
	}
	if ref.Rows[0].Outputs["level"] != "P1" {
		t.Fatalf("输出列未按资产快照填充: %+v", ref.Rows[0].Outputs)
	}
	// 内嵌表原样保留
	if got.DecisionTables[1].AssetRef != nil || got.DecisionTables[1].Rows[0].Outputs["inline_out"] != "ok" {
		t.Fatalf("内嵌决策表被误改: %+v", got.DecisionTables[1])
	}
	// 入参不可变（浅拷贝保护）
	if len(schema.DecisionTables[0].Rows) != 0 {
		t.Fatal("ExpandDecisionRefs 修改了入参 schema")
	}
}

// 资产缺失/未发布/解析失败：容错为空表，保留引用标记，决策表不产生输出但不报错。
func TestExpandDecisionRefsMissingAssetTolerated(t *testing.T) {
	schema := refSchema()
	got, err := ExpandDecisionRefs(context.Background(), "tenant-a", schema, &stubDecisionResolver{})
	if err != nil {
		t.Fatalf("缺失资产不应报错: %v", err)
	}
	ref := got.DecisionTables[0]
	if ref.AssetRef == nil || len(ref.Rows) != 0 || len(ref.ConditionKeys) != 0 {
		t.Fatalf("缺失资产应退化为带标记的空表，实际 %+v", ref)
	}
	if !HasDecisionRefs(got) {
		t.Fatal("退化的引用表仍应被识别为引用态")
	}
	out, _ := ApplyDataRules(got, map[string]any{"env": "prod"})
	if _, ok := out["level"]; ok {
		t.Fatal("资产缺失时引用表不应产生输出")
	}
}

// 保存态归一：展开后回传的内容快照必须剥掉，只留 ID + 引用标记；内嵌表不受影响。
func TestNormalizeDecisionRefsStripsSnapshot(t *testing.T) {
	schema, err := ExpandDecisionRefs(context.Background(), "tenant-a", refSchema(),
		&stubDecisionResolver{table: publishedSnapshot("P1")})
	if err != nil {
		t.Fatal(err)
	}
	NormalizeDecisionRefs(schema)
	ref := schema.DecisionTables[0]
	if ref.AssetRef == nil || ref.AssetRef.AssetID != "asset-1" || ref.ID != "dt-ref" {
		t.Fatalf("归一后引用标记/ID 丢失: %+v", ref)
	}
	if len(ref.Rows) != 0 || len(ref.ConditionKeys) != 0 || len(ref.OutputKeys) != 0 || ref.Name != "" {
		t.Fatalf("归一后仍残留内容快照: %+v", ref)
	}
	if schema.DecisionTables[1].Rows[0].Outputs["inline_out"] != "ok" {
		t.Fatal("内嵌决策表被误归一")
	}
}

// 往返语义：发布新版本后，跟随最新版（version=0）的表单无需改 schema 即拿到新内容；
// 锁定版本（version>0）则把版本号透传给解析器。
func TestDecisionRefRoundTripFollowsLatest(t *testing.T) {
	schema := refSchema()
	resolver := &stubDecisionResolver{table: publishedSnapshot("P1")}
	expanded, err := ExpandDecisionRefs(context.Background(), "tenant-a", schema, resolver)
	if err != nil {
		t.Fatal(err)
	}
	NormalizeDecisionRefs(expanded) // 模拟保存

	resolver.table = publishedSnapshot("P0") // 资产发布新版本
	expanded2, err := ExpandDecisionRefs(context.Background(), "tenant-a", expanded, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if got := expanded2.DecisionTables[0].Rows[0].Outputs["level"]; got != "P0" {
		t.Fatalf("跟随最新版应拿到 P0，实际 %s", got)
	}
}

func TestDecisionRefLockedVersionPassedThrough(t *testing.T) {
	schema := refSchema()
	schema.DecisionTables[0].AssetRef.Version = 3
	resolver := &stubDecisionResolver{table: publishedSnapshot("P2")}
	if _, err := ExpandDecisionRefs(context.Background(), "tenant-a", schema, resolver); err != nil {
		t.Fatal(err)
	}
	if resolver.lastVer != 3 {
		t.Fatalf("锁定版本号未透传，实际 %d", resolver.lastVer)
	}
}

// 纯引用态（尚未展开）不应被 ValidateDecisionTables 判为「空列 / 无行」而阻断保存。
func TestValidateDecisionTablesRefOnly(t *testing.T) {
	schema := refSchema()
	if _, err := ValidateDecisionTables(schema); err != nil {
		t.Fatalf("纯引用态应允许保存，实际报错: %v", err)
	}
}
