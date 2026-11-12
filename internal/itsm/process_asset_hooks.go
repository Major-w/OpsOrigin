package itsm

import (
	"context"
	"fmt"

	"github.com/opsorigin/core/internal/itsm/assets"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/process"
)

// 流程引擎的两个运行期解析器实现（分层单向）：process 只认接口，
// itsm.Service 负责读 assets.Store 已发布版本，由 cmd/core 装配注入。

var _ process.DecisionEvaluator = (*Service)(nil)
var _ process.ActionTemplateResolver = (*Service)(nil)

// EvaluateDecision 按决策表资产已发布版本求值，返回仅含输出列的 map。
//
// 合成虚拟 schema 让 form.ApplyDataRules 的输出列可写入；只回传输出列，
// 避免把输入字段混进 decision.<alias> 命名空间。资产未发布/版本缺失返回 error，
// 由 process.applyDecisionBindings 按容错跳过该绑定（不阻断工单流转）。
func (s *Service) EvaluateDecision(ctx context.Context, tenantID, assetID string, version int, inputs map[string]any) (map[string]any, []string, error) {
	if s.assetStore == nil {
		return nil, nil, fmt.Errorf("决策表资产存储未装配")
	}
	table, err := s.assetStore.ResolveDecisionTable(ctx, tenantID, assetID, version)
	if err != nil {
		return nil, nil, err
	}
	if table == nil {
		return nil, nil, fmt.Errorf("决策表资产 %s 未发布或版本不存在", assetID)
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	computed, fired := form.ApplyDataRules(assets.SyntheticSchema(*table), inputs)
	out := make(map[string]any, len(table.OutputKeys))
	for _, k := range table.OutputKeys {
		if v, ok := computed[k]; ok {
			out[k] = v
		}
	}
	return out, fired, nil
}

// ResolveActionTemplate 按已发布版本解析动作模板，返回动作类型与默认参数副本。
// 模板停用 / 未发布 / 版本缺失由 Store 返回 error，流程引擎 fail-closed 走 failure。
func (s *Service) ResolveActionTemplate(ctx context.Context, tenantID, templateID string, version int) (string, map[string]any, error) {
	if s.assetStore == nil {
		return "", nil, fmt.Errorf("动作模板存储未装配")
	}
	v, err := s.assetStore.ResolveActionTemplate(ctx, tenantID, templateID, version)
	if err != nil {
		return "", nil, err
	}
	if v == nil {
		return "", nil, fmt.Errorf("动作模板 %s 版本不存在", templateID)
	}
	inputs := make(map[string]any, len(v.Inputs))
	for k, val := range v.Inputs {
		inputs[k] = val
	}
	return v.ActionType, inputs, nil
}
