package itsm

import (
	"context"
	"encoding/json"

	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/process"
)

// 流程级主表单的决策表资产引用处理：实现 process.FormSchemaResolver，由 cmd/core
// 装配进 process.Service。process 包只认接口，itsm 负责 form/assets 具体实现（分层单向）。
var _ process.FormSchemaResolver = (*Service)(nil)

// ExpandProcessForm 读取路径：把流程主表单里的 asset_ref 展开成已发布版本内容。
//
// 工单页(tickets)与服务门户(portal)直接 parseFormSchema(proc.form)；若不展开，
// 前端 applyDataRules 遍历空 rows，决策表实时联动静默失效（后端提交时才补算）。
// 展开产物保留 AssetRef 标记，设计器回存时仍会被归一。
func (s *Service) ExpandProcessForm(ctx context.Context, tenantID string, raw json.RawMessage) (json.RawMessage, error) {
	return patchProcessFormTables(raw, func(schema *form.FormSchema) ([]form.DecisionTable, error) {
		s.expandSchemaDecisionRefs(ctx, tenantID, schema)
		return schema.DecisionTables, nil
	})
}

// NormalizeProcessForm 保存路径：按已发布版本校验列，再归一回纯引用标记。
//
// 与独立表单 PUT /itsm/forms 同标准：无论前端回传纯引用态还是 GET 后的展开态，
// 资产的列都必须在本流程主表单的 fields 中声明，否则运行期输出会被静默跳过。
// 归一避免把版本快照固化进流程定义——否则资产发布/回滚对该流程永久失效。
func (s *Service) NormalizeProcessForm(ctx context.Context, tenantID string, raw json.RawMessage) (json.RawMessage, error) {
	return patchProcessFormTables(raw, func(schema *form.FormSchema) ([]form.DecisionTable, error) {
		s.expandSchemaDecisionRefs(ctx, tenantID, schema)
		if _, verr := form.ValidateDecisionTables(schema); verr != nil {
			return nil, verr
		}
		form.NormalizeDecisionRefs(schema)
		return schema.DecisionTables, nil
	})
}

// patchProcessFormTables 只替换 form JSON 的 decision_tables 键，其余键用原始
// RawMessage 原样保留：流程主表单可能带设计器扩展字段，整体 Unmarshal→Marshal 会丢失。
// 非标准 schema / 无决策表时原样返回，存量流程零改动。
func patchProcessFormTables(raw json.RawMessage, patch func(*form.FormSchema) ([]form.DecisionTable, error)) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var schema form.FormSchema
	if err := json.Unmarshal(raw, &schema); err != nil || len(schema.DecisionTables) == 0 {
		return raw, nil
	}
	tables, err := patch(&schema)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(tables)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw, nil
	}
	obj["decision_tables"] = b
	return json.Marshal(obj)
}
