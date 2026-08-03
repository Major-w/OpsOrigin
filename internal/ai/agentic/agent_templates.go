package agentic

import (
	"context"
	"fmt"

	"github.com/opsorigin/core/internal/ai/model"
)

// ListAgentTemplates 返回平台预置的岗位数字员工目录(开箱即用)。
func (e *Engine) ListAgentTemplates() []*AgentTemplate {
	out := make([]*AgentTemplate, 0, len(PresetAgentTemplates))
	for _, t := range PresetAgentTemplates {
		out = append(out, t)
	}
	return out
}

// GetAgentTemplate 按 ID 查询单个岗位模板。
func (e *Engine) GetAgentTemplate(id string) (*AgentTemplate, bool) {
	for _, t := range PresetAgentTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// InstantiateAgentTemplate 将岗位模板一键实例化为租户内一个真实可用的 Agent。
// overrideName 非空时作为 Agent 名称(覆盖模板默认名); 同名自动追加序号避免冲突。
// 复用既有的 CreateAgent 持久化链路, 实例化后即可在 /ai 聊天或加入团队直接使用。
func (e *Engine) InstantiateAgentTemplate(tenantID, templateID, overrideName string) (*model.AgentInstance, error) {
	t, ok := e.GetAgentTemplate(templateID)
	if !ok {
		return nil, fmt.Errorf("agent template not found: %s", templateID)
	}

	name := t.Name
	if overrideName != "" {
		name = overrideName
	}
	name = e.uniqueAgentName(tenantID, name)

	agent := &model.AgentInstance{
		Name:               name,
		ModelName:          t.ModelName,
		SystemPrompt:       t.SystemPrompt,
		Status:             "active",
		TenantID:           tenantID,
		KnowledgeCategories: append([]string(nil), t.KnowledgeCategories...),
		Tools:               append([]string(nil), t.Tools...),
		Skills:              t.Skills,
	}
	if err := e.CreateAgent(context.Background(), agent); err != nil {
		return nil, err
	}
	return agent, nil
}

// uniqueAgentName 在现有 Agent 名称集合中生成不冲突的名称(追加 (2)/(3) ...)。
func (e *Engine) uniqueAgentName(tenantID, base string) string {
	_ = tenantID // 当前 Agent 在内存中不分租户, 全局去重即可
	e.mu.RLock()
	existing := make(map[string]bool, len(e.agents))
	for _, a := range e.agents {
		existing[a.Name] = true
	}
	e.mu.RUnlock()
	if !existing[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s (%d)", base, i)
		if !existing[cand] {
			return cand
		}
	}
	return base
}
