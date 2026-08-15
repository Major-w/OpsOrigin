package agentic

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/opsorigin/core/internal/ai/model"
)

// ============================================================================
// 技能 overlay 组合 (D, 参考 DeepSeek Harness 的 Seam overlay: 按 id 覆盖补丁、可逆 effect)
//
// 痛点: 同一技能(如"数据库故障处置")在 MySQL / PostgreSQL / Oracle 等不同场景下
// 只有少数指令差异, 却要复制出 N 份完整技能包——复用度低、易漂移。
//
// 借鉴 Harness 的 "Bundle → Profile → home patch → CLI overlay" 思路:
//   - 维护一份 base 技能;
//   - 各场景的差异以 SkillOverlay(按 id 标识的覆盖补丁)叠加;
//   - 渲染时按优先级合并指令, 移除某个 overlay 即还原到 base(可逆、无副作用);
//   - 与 Harness 一致: 补丁以 id 为单位, 谁加的谁负责移除, 不破坏 base 本身。
//
// 安全边界:
//   - 仅修改技能的 Instructions/Description 文本(渲染进 system prompt), 不改动工具权限;
//   - ToolAdds/ToolRemoves 为"声明式"提示(composable 审计用), 引擎**不会**据此自动
//     扩大 agent 既有的 Tools 白名单, 避免 overlay 越权授予高危工具(如 script_run)。
//   - 验证 fail-closed: 非法 overlay(ID/目标技能/补丁任一缺失)直接拒绝, 不影响既有渲染。
// ============================================================================

// SkillOverlay 针对某个 base 技能的覆盖补丁(按 id 标识, 可移除即还原)。
type SkillOverlay struct {
	ID          string `json:"id"`                    // 唯一 id(覆盖补丁身份; 移除时按此 id 撤销)
	TargetSkill string `json:"target_skill"`          // 目标 base 技能名(对应 AgentSkill.Name)
	Description string `json:"description,omitempty"` // 该 overlay 的用途说明(审计/可读)
	Priority    int    `json:"priority"`              // 数值越大越晚应用(后写覆盖); 同优先级按 ID 字典序稳定
	// Instructions 追加到 base.Instructions 的补丁指令(场景差异步骤/注意事项)。
	Instructions string `json:"instructions,omitempty"`
	// ToolAdds/ToolRemoves: 声明式——激活该场景时本技能预期挂载/禁用的工具。
	// 仅作渲染提示与审计, 引擎不据此自动改动 agent 的 Tools 白名单。
	ToolAdds    []string `json:"tool_adds,omitempty"`
	ToolRemoves []string `json:"tool_removes,omitempty"`
}

// SkillOverlayRegistry overlay 注册表(并发安全、nil 安全)。
// 按 TargetSkill 分组持有 overlay 列表, 提供合并/移除/列举能力。
type SkillOverlayRegistry struct {
	mu      sync.RWMutex
	bySkill map[string][]*SkillOverlay // key = base 技能名
}

// NewSkillOverlayRegistry 构造空注册表。
func NewSkillOverlayRegistry() *SkillOverlayRegistry {
	return &SkillOverlayRegistry{bySkill: make(map[string][]*SkillOverlay)}
}

// validate 校验 overlay 合法性(返回首个不通过原因)。
func (o *SkillOverlay) validate() error {
	if strings.TrimSpace(o.ID) == "" {
		return fmt.Errorf("overlay id is required")
	}
	if strings.TrimSpace(o.TargetSkill) == "" {
		return fmt.Errorf("overlay target_skill is required")
	}
	hasPatch := strings.TrimSpace(o.Instructions) != "" ||
		len(o.ToolAdds) > 0 || len(o.ToolRemoves) > 0
	if !hasPatch {
		return fmt.Errorf("overlay must carry at least one of: instructions / tool_adds / tool_removes")
	}
	return nil
}

// Apply 注册或更新一个 overlay(按 ID 幂等替换)。非法 overlay 返回 error(不写入)。
func (r *SkillOverlayRegistry) Apply(o *SkillOverlay) error {
	if o == nil {
		return fmt.Errorf("overlay is nil")
	}
	if err := o.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.TrimSpace(o.TargetSkill)
	list := r.bySkill[key]
	// 同 ID 已存在 → 原地替换(幂等)
	for i, ex := range list {
		if ex.ID == o.ID {
			list[i] = o
			return nil
		}
	}
	r.bySkill[key] = append(list, o)
	return nil
}

// Remove 按 id 移除某个 overlay; 返回是否实际移除。
func (r *SkillOverlayRegistry) Remove(overlayID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := false
	for key, list := range r.bySkill {
		kept := list[:0]
		for _, o := range list {
			if o.ID == overlayID {
				removed = true
				continue
			}
			kept = append(kept, o)
		}
		if len(kept) == 0 {
			delete(r.bySkill, key)
		} else {
			r.bySkill[key] = kept
		}
	}
	return removed
}

// Clear 移除某 base 技能下的全部 overlay。
func (r *SkillOverlayRegistry) Clear(skillName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bySkill, strings.TrimSpace(skillName))
}

// List 返回某 base 技能下、按 (Priority 升序, ID 字典序) 排好的 overlay 列表(nil 安全)。
func (r *SkillOverlayRegistry) List(skillName string) []*SkillOverlay {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := r.bySkill[strings.TrimSpace(skillName)]
	out := make([]*SkillOverlay, len(list))
	copy(out, list)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Compose 把某 base 技能与其 active overlay 合并为"生效技能定义"。
// 语义(对齐 Harness 按 id 覆盖补丁):
//   - Instructions: base + 按优先级顺序追加每个 overlay 的补丁(标注来源 id, 便于追溯);
//   - Description: base + 末尾标注 active overlay 的 id 列表;
//   - 移除全部 overlay 即还原到 base(可逆); base 为空时返回空技能。
func (r *SkillOverlayRegistry) Compose(base model.AgentSkill) model.AgentSkill {
	if strings.TrimSpace(base.Name) == "" {
		return base
	}
	ovs := r.List(base.Name)
	if len(ovs) == 0 {
		return base
	}
	eff := base
	ins := strings.TrimSpace(base.Instructions)
	var ids []string
	for _, ov := range ovs {
		ids = append(ids, ov.ID)
		patch := strings.TrimSpace(ov.Instructions)
		if patch == "" {
			continue
		}
		if ins != "" {
			ins += "\n\n"
		}
		ins += "[overlay " + ov.ID + "] " + patch
	}
	eff.Instructions = ins
	if d := strings.TrimSpace(base.Description); d != "" {
		eff.Description = d + " | overlays: " + strings.Join(ids, ", ")
	} else {
		eff.Description = "overlays: " + strings.Join(ids, ", ")
	}
	return eff
}

// ComposeTools 声明式计算某技能在当前 active overlay 下的预期工具集:
// baseTools + 所有 overlay 的 ToolAdds − 所有 overlay 的 ToolRemoves。
// 注意: 返回结果仅用于"场景预期/审计", 引擎渲染时**不会**据此扩大 agent 权限。
func (r *SkillOverlayRegistry) ComposeTools(skillName string, baseTools []string) []string {
	ovs := r.List(skillName)
	if len(ovs) == 0 {
		return baseTools
	}
	set := make(map[string]bool, len(baseTools))
	out := make([]string, 0, len(baseTools))
	for _, t := range baseTools {
		if !set[t] {
			set[t] = true
			out = append(out, t)
		}
	}
	for _, ov := range ovs {
		for _, add := range ov.ToolAdds {
			if !set[add] {
				set[add] = true
				out = append(out, add)
			}
		}
	}
	// 移除: 重新过滤
	if hasRemove(ovs) {
		rm := make(map[string]bool)
		for _, ov := range ovs {
			for _, t := range ov.ToolRemoves {
				rm[t] = true
			}
		}
		filtered := out[:0]
		for _, t := range out {
			if !rm[t] {
				filtered = append(filtered, t)
			}
		}
		out = filtered
	}
	return out
}

func hasRemove(ovs []*SkillOverlay) bool {
	for _, ov := range ovs {
		if len(ov.ToolRemoves) > 0 {
			return true
		}
	}
	return false
}
