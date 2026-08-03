package agentic

import (
	"context"
	"fmt"
	"log/slog"

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
// withInspection 为 true 且模板声明了关联巡检时, 自动为该数字员工创建对应的巡检任务
// (命名 "<Agent名> · <巡检模板名>", 同名已存在则跳过), 让"数字员工 + 主动巡检"开箱成闭环。
// 复用既有的 CreateAgent 持久化链路, 实例化后即可在 /ai 聊天或加入团队直接使用。
func (e *Engine) InstantiateAgentTemplate(tenantID, templateID, overrideName string, withInspection bool) (*model.AgentInstance, *Inspection, error) {
	t, ok := e.GetAgentTemplate(templateID)
	if !ok {
		return nil, nil, fmt.Errorf("agent template not found: %s", templateID)
	}

	name := t.Name
	if overrideName != "" {
		name = overrideName
	}
	name = e.uniqueAgentName(tenantID, name)

	agent := &model.AgentInstance{
		Name:                name,
		ModelName:           t.ModelName,
		SystemPrompt:        t.SystemPrompt,
		Status:              "active",
		TenantID:            tenantID,
		KnowledgeCategories: append([]string(nil), t.KnowledgeCategories...),
		Tools:               append([]string(nil), t.Tools...),
		Skills:              t.Skills,
	}
	if err := e.CreateAgent(context.Background(), agent); err != nil {
		return nil, nil, err
	}

	// 关联巡检：模板声明了巡检模板且未显式关闭时自动创建
	var insp *Inspection
	if withInspection && t.InspectionTemplateID != "" {
		if created, err := e.autoCreateInspection(tenantID, agent.ID, name, t.InspectionTemplateID); err != nil {
			// 巡检创建失败不阻断上岗(Agent 已落库), 记录告警
			slog.Warn("auto create inspection failed on instantiate",
				"agent_id", agent.ID, "template", t.InspectionTemplateID, "error", err)
		} else if created != nil {
			insp = created
		}
	}
	return agent, insp, nil
}

// autoCreateInspection 为数字员工创建关联巡检任务(同名已存在则复用, 保证幂等)。
func (e *Engine) autoCreateInspection(tenantID, agentID, agentName, inspectionTemplateID string) (*Inspection, error) {
	it, ok := e.GetInspectionTemplate(inspectionTemplateID)
	if !ok {
		return nil, nil
	}
	inspectionName := fmt.Sprintf("%s · %s", agentName, it.Name)

	e.mu.RLock()
	for _, in := range e.inspections {
		if in.TenantID == tenantID && in.Name == inspectionName {
			e.mu.RUnlock()
			return in, nil
		}
	}
	e.mu.RUnlock()

	insp := &Inspection{
		TenantID:   tenantID,
		Name:       inspectionName,
		TemplateID: it.ID,
		AgentID:    agentID,
		Prompt:     it.Prompt,
		Scope:      it.ScopeHint,
		Schedule:   it.DefaultSchedule,
		Enabled:    true,
	}
	created, err := e.CreateInspection(context.Background(), insp)
	if err != nil {
		return nil, err
	}
	slog.Info("auto created inspection for digital employee",
		"agent_id", agentID, "inspection_id", created.ID, "name", inspectionName)
	return created, nil
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
