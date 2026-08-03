package agentic

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/opsorigin/core/internal/ai/model"
)

// SceneTemplate 一个开箱即用的业务场景包(岗位 + 巡检 + 关联知识 + 推荐服务目录)。
//
// 设计目标: 按业务场景把"数字员工 + 主动巡检 + 处置标准"打包, 让用户一键启用一整套
// 数字员工能力, 而不是从零搭建岗位 Agent / 巡检任务 / 处置流程。
//
// 岗位 Agent 与关联巡检通过 InstantiateScene 真实创建(持久化), 知识分类仅作推荐
// (供前端展示, 不自动写入租户的知识分类体系), 服务目录项仅作元数据展示
// (建议的标准化工单类型, 不自动建到 itsm_catalog)。
type SceneTemplate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`            // 安全合规 / 容量规划 / 故障定位 ...
	Description string   `json:"description"`
	Icon        string   `json:"icon"`                // lucide 图标名
	// AgentTemplateID 场景对应的岗位模板 ID(必填, InstantiateScene 据此创建数字员工)。
	AgentTemplateID string `json:"agent_template_id"`
	// InspectionTemplateIDs 场景内额外要创建的巡检模板 ID 列表(空=仅依赖岗位模板自带巡检)。
	InspectionTemplateIDs []string `json:"inspection_template_ids"`
	// KnowledgeCategories 推荐绑定的知识库分类(前端展示, 不自动建)。
	KnowledgeCategories []string `json:"knowledge_categories,omitempty"`
	// SuggestedCatalog 场景推荐的标准化工单类型(仅元数据展示, 不自动落库)。
	SuggestedCatalog []SceneCatalogItem `json:"suggested_catalog,omitempty"`
	Tags             []string            `json:"tags,omitempty"`
}

// SceneCatalogItem 场景推荐的标准化工单类型(前端展示场景包时一并列出,
// 不自动建到 itsm.catalog: 真实落库留给用户在 ITSM 模块启用场景后自行挑选)。
type SceneCatalogItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"` // incident/problem/change/request
}

// PresetSceneTemplates 平台预置的场景包目录。
// 与 AgentTemplate/InspectionTemplate 同构, 按业务场景聚合"数字员工 + 主动巡检"。
var PresetSceneTemplates = []*SceneTemplate{
	{
		ID:               "scene-security",
		Name:             "安全合规",
		Category:         "安全",
		Description:      "安全合规官 + 安全基线巡检 + 推荐的标准合规处置工单类型, 让安全合规一键开箱即用。",
		Icon:             "Shield",
		AgentTemplateID:  "tpl-security",
		InspectionTemplateIDs: []string{"insp-security-baseline"},
		KnowledgeCategories:   []string{"安全", "合规", "运维手册"},
		SuggestedCatalog: []SceneCatalogItem{
			{Name: "安全漏洞修复", Description: "对发现的安全漏洞或配置弱点进行修复", Category: "incident"},
			{Name: "合规整改", Description: "对照等保/合规要求做整改", Category: "problem"},
			{Name: "安全基线核查", Description: "对核心资产做安全基线核查", Category: "change"},
		},
		Tags: []string{"安全", "合规", "基线"},
	},
	{
		ID:               "scene-capacity",
		Name:             "容量规划",
		Category:         "容量",
		Description:      "成本优化分析师 + 容量水位巡检 + 推荐的扩容/降配/回收工单类型, 提前预警容量瓶颈并量化降本空间。",
		Icon:             "TrendingDown",
		AgentTemplateID:  "tpl-cost",
		InspectionTemplateIDs: []string{"insp-capacity"},
		KnowledgeCategories:   []string{"成本", "运维手册"},
		SuggestedCatalog: []SceneCatalogItem{
			{Name: "主机扩容", Description: "对资源紧张的服务器/虚拟机做扩容", Category: "change"},
			{Name: "数据库扩容", Description: "对容量告警的数据库做扩容或归档", Category: "change"},
			{Name: "资源回收", Description: "回收长期闲置或规格错配的资源", Category: "change"},
		},
		Tags: []string{"容量", "成本", "FinOps"},
	},
	{
		ID:               "scene-incident",
		Name:             "故障定位",
		Category:         "应急",
		Description:      "故障应急指挥官 + 主机健康巡检 + 推荐的故障应急/复盘工单类型, 让故障定位与止血动作标准化。",
		Icon:             "Siren",
		AgentTemplateID:  "tpl-sre",
		InspectionTemplateIDs: []string{"insp-host-health"},
		KnowledgeCategories:   []string{"故障", "运维手册"},
		SuggestedCatalog: []SceneCatalogItem{
			{Name: "故障应急响应", Description: "故障发生时的紧急止血与恢复", Category: "incident"},
			{Name: "应急扩容", Description: "故障期间紧急扩容承载能力", Category: "change"},
			{Name: "故障复盘", Description: "故障恢复后做根因分析并输出复盘", Category: "problem"},
		},
		Tags: []string{"故障", "应急", "SRE"},
	},
}

// ListSceneTemplates 返回平台预置的场景包目录(只读, 无租户数据)。
func (e *Engine) ListSceneTemplates() []*SceneTemplate {
	out := make([]*SceneTemplate, 0, len(PresetSceneTemplates))
	for _, s := range PresetSceneTemplates {
		out = append(out, s)
	}
	return out
}

// GetSceneTemplate 按 ID 查询单个场景包详情。
func (e *Engine) GetSceneTemplate(id string) (*SceneTemplate, bool) {
	for _, s := range PresetSceneTemplates {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

// SceneInstantiateResult 一键启用场景的结果(用于 HTTP 响应一次性展示)。
type SceneInstantiateResult struct {
	Agent       *model.AgentInstance `json:"agent,omitempty"`       // 创建的数字员工
	Inspection  *Inspection          `json:"inspection,omitempty"`  // 岗位模板自带的关联巡检(若有)
	Inspections []*Inspection        `json:"inspections,omitempty"` // 场景额外创建的巡检(含 Inspection)
	Catalog     []SceneCatalogItem   `json:"catalog,omitempty"`     // 复述建议服务目录(元数据, 未落库)
}

// InstantiateScene 一键启用场景包: 创建数字员工 + 场景自带巡检 + 额外巡检。
// overrideName 非空时覆盖 Agent 默认名(同名自动追加序号避免冲突)。
// 复用既有的 InstantiateAgentTemplate(withInspection=true) 自动建关联巡检, 避免重复造轮子。
// 返回值: SceneInstantiateResult(agent + 巡检列表 + 建议服务目录)。
func (e *Engine) InstantiateScene(tenantID, sceneID, overrideName string) (*SceneInstantiateResult, error) {
	scene, ok := e.GetSceneTemplate(sceneID)
	if !ok {
		return nil, fmt.Errorf("scene template not found: %s", sceneID)
	}

	// 1. 创建岗位 Agent(自带关联巡检, 如 tpl-security → insp-security-baseline)
	agent, insp, err := e.InstantiateAgentTemplate(tenantID, scene.AgentTemplateID, overrideName, true)
	if err != nil {
		return nil, fmt.Errorf("create scene agent: %w", err)
	}

	result := &SceneInstantiateResult{
		Agent:       agent,
		Inspection:  insp,
		Inspections: []*Inspection{},
		Catalog:     scene.SuggestedCatalog,
	}

	// 2. 创建场景额外巡检(去重: 若岗位模板自带的关联巡检 ID 与场景额外列表重叠, 跳过)。
	sceneInspIDs := make(map[string]bool)
	if insp != nil && insp.TemplateID != "" {
		sceneInspIDs[insp.TemplateID] = true
	}
	for _, tplID := range scene.InspectionTemplateIDs {
		if sceneInspIDs[tplID] {
			continue
		}
		it, ok := e.GetInspectionTemplate(tplID)
		if !ok {
			slog.Warn("scene inspection template not found", "scene", sceneID, "template", tplID)
			continue
		}
		// 检查同名巡检已存在则跳过(幂等)
		sceneInspName := fmt.Sprintf("%s · %s", scene.Name, it.Name)
		e.mu.RLock()
		exists := false
		for _, in := range e.inspections {
			if in.TenantID == tenantID && in.Name == sceneInspName {
				exists = true
				break
			}
		}
		e.mu.RUnlock()
		if exists {
			continue
		}
		newInsp := &Inspection{
			TenantID:   tenantID,
			Name:       sceneInspName,
			TemplateID: it.ID,
			Prompt:     it.Prompt,
			Scope:      it.ScopeHint,
			Schedule:   it.DefaultSchedule,
			Enabled:    true,
		}
		created, cerr := e.CreateInspection(context.Background(), newInsp)
		if cerr != nil {
			slog.Warn("create scene inspection failed", "scene", sceneID, "template", tplID, "error", cerr)
			continue
		}
		result.Inspections = append(result.Inspections, created)
	}

	slog.Info("scene instantiated",
		"scene", sceneID, "tenant", tenantID,
		"agent_id", agentIDOf(agent), "auto_insp", inspIDOf(insp),
		"extra_insps", len(result.Inspections))
	return result, nil
}

// agentIDOf 安全取 Agent ID(避免 nil panic)
func agentIDOf(a *model.AgentInstance) string {
	if a == nil {
		return ""
	}
	return a.ID
}

// inspIDOf 安全取 Inspection ID
func inspIDOf(in *Inspection) string {
	if in == nil {
		return ""
	}
	return in.ID
}