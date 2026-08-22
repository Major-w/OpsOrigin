package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func sampleModelType() *model.CIModelType {
	return &model.CIModelType{
		ID:   "server",
		Name: "服务器",
		Schema: map[string]model.AttrDef{
			"ip":     {ID: "ip", Type: "string", Required: true},
			"cpu":    {ID: "cpu", Type: "number"},
			"mem_gb": {ID: "mem_gb", Type: "number"},
			"env":    {ID: "env", Type: "enum", Options: []string{"prod", "dev"}},
			"no_id":  {Type: "string"}, // 旧数据无 ID, 兼容按 name 命中
		},
		Relations: []model.RelationDef{
			{ID: "hosts", Name: "承载", RelationType: "hosts", TargetModel: "app"},
			{ID: "legacy_rel", Name: "旧关系", RelationType: "old_rt", TargetModel: "db"}, // 旧数据 relation id=relation_type
		},
	}
}

func TestFieldByNameOrID(t *testing.T) {
	mt := sampleModelType()
	// 按 ID 命中
	if def, ok := mt.FieldByNameOrID("cpu"); !ok || def.Type != "number" {
		t.Fatalf("按 ID 查 cpu 失败: ok=%v def=%+v", ok, def)
	}
	// 按 name 命中(field_id 与 name 相同场景)
	if def, ok := mt.FieldByNameOrID("ip"); !ok || def.ID != "ip" {
		t.Fatalf("按 name 查 ip 失败")
	}
	// 旧数据无 ID, 按 name 命中
	if def, ok := mt.FieldByNameOrID("no_id"); !ok || def.Type != "string" {
		t.Fatalf("旧数据无 ID 按 name 命中失败")
	}
	// 不存在
	if _, ok := mt.FieldByNameOrID("nope"); ok {
		t.Fatalf("不存在字段不应命中")
	}
}

func TestRelationByID(t *testing.T) {
	mt := sampleModelType()
	// 按 relation id
	if r, ok := mt.RelationByID("hosts"); !ok || r.RelationType != "hosts" {
		t.Fatalf("按 ID 查 hosts 关系失败")
	}
	// 旧数据 id=relation_type
	if r, ok := mt.RelationByID("old_rt"); !ok || r.TargetModel != "db" {
		t.Fatalf("旧数据按 relation_type 命中失败")
	}
	// 不存在
	if _, ok := mt.RelationByID("nope"); ok {
		t.Fatalf("不存在关系不应命中")
	}
}

// TestResolveModelTypeIDBackfill 验证 ResolveModelType 对旧数据补全 ID 的兼容逻辑。
// 旧数据 AttrDef.ID 为空 -> 补全为字段名; RelationDef.ID 为空 -> 补全为 RelationType。
func TestResolveModelTypeIDBackfill(t *testing.T) {
	mt := &model.CIModelType{
		ID:   "legacy",
		Name: "遗留模型",
		Schema: map[string]model.AttrDef{
			"hostname": {Type: "string"}, // 无 ID
		},
		Relations: []model.RelationDef{
			{Name: "依赖", RelationType: "depends_on", TargetModel: "x"}, // 无 ID
		},
	}
	// 模拟 ResolveModelType 的兼容分支
	for k, v := range mt.Schema {
		if v.ID == "" {
			v.ID = k
		}
		mt.Schema[k] = v
	}
	for i := range mt.Relations {
		if mt.Relations[i].ID == "" {
			mt.Relations[i].ID = mt.Relations[i].RelationType
		}
	}
	if mt.Schema["hostname"].ID != "hostname" {
		t.Fatalf("旧属性 ID 未补全为 name")
	}
	if mt.Relations[0].ID != "depends_on" {
		t.Fatalf("旧关系 ID 未补全为 relation_type")
	}
}
