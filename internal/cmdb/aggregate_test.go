package cmdb

import (
	"encoding/json"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestApplyAggregateOp_Count(t *testing.T) {
	related := []*model.CIModel{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	if got := applyAggregateOp("count", "", related); got != 3 {
		t.Errorf("count = %v, want 3", got)
	}
	if got := applyAggregateOp("count", "", nil); got != 0 {
		t.Errorf("count empty = %v, want 0", got)
	}
}

func TestApplyAggregateOp_SumAndAvg(t *testing.T) {
	related := []*model.CIModel{
		{ID: "a", Attributes: map[string]any{"memory_gb": 32.0}},
		{ID: "b", Attributes: map[string]any{"memory_gb": 64.0}},
		{ID: "c", Attributes: map[string]any{"memory_gb": 96.0}},
	}
	if got := applyAggregateOp("sum", "memory_gb", related); got != 192.0 {
		t.Errorf("sum = %v, want 192", got)
	}
	if got := applyAggregateOp("avg", "memory_gb", related); got != 64.0 {
		t.Errorf("avg = %v, want 64", got)
	}
}

func TestApplyAggregateOp_MissingTargetIgnored(t *testing.T) {
	related := []*model.CIModel{
		{ID: "a", Attributes: map[string]any{"x": 10.0}},
		{ID: "b", Attributes: map[string]any{}}, // 无目标属性
	}
	if got := applyAggregateOp("sum", "memory_gb", related); got != 0.0 {
		t.Errorf("缺失目标属性应忽略: %v", got)
	}
	if got := applyAggregateOp("avg", "memory_gb", related); got != 0.0 {
		t.Errorf("avg 空集应 0: %v", got)
	}
}

func TestApplyAggregateOp_UnknownOp(t *testing.T) {
	related := []*model.CIModel{{ID: "a"}}
	if got := applyAggregateOp("max", "", related); got != nil {
		t.Errorf("未知 op 应返回 nil: %v", got)
	}
}

func TestAggregateDefInSchema(t *testing.T) {
	// 模型 schema 可声明聚合字段, 序列化往返保持
	def := model.AttrDef{
		Type: "int",
		Aggregate: &model.AttrAggregate{
			Op: "count", Relation: "hosts", Direction: "in",
		},
	}
	b, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back model.AttrDef
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Aggregate == nil || back.Aggregate.Op != "count" || back.Aggregate.Relation != "hosts" || back.Aggregate.Direction != "in" {
		t.Errorf("聚合定义往返丢失: %+v", back.Aggregate)
	}
}
