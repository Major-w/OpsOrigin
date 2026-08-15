package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/model"
)

// ============================================================================
// 记忆回流驱动技能演化 (S1, 参考 DeepSeek Harness 的 dsh-memory-evolve)
//
// 痛点: 长期经验记忆(ExperienceMemory) 与 技能市场(SkillPackage) 此前完全割裂——
// 记忆不会回流驱动技能迭代。本模块把两者打通:
//   - 一次成功的 agent 运行 → 蒸馏成经验记忆(后续注入 prompt, 越用越聪明);
//   - 一个技能(agent) 反复失败 → 自动基于历史记忆提炼处置要点, 建议并发布新的
//     补丁版本(把学到的经验沉淀进 system prompt)。这是通用 harness 补不齐的
//     运维领域护城河: 同类故障处置经验在技能包里持续自我进化。
//
// 设计: SkillEvolver 经 SkillEvolutionBackend 与引擎解耦(可注入、nil 安全),
// 单测用 fake backend 替换, 不依赖具体存储。
// ============================================================================

// SkillEvolutionBackend 技能演化所需的后端能力, 由 *Engine 满足, 便于单测替换。
type SkillEvolutionBackend interface {
	// GetAgent 取数字员工配置(演化补丁基于其 system prompt 迭代)。
	GetAgent(agentID string) (*model.AgentInstance, bool)
	// AddMemory 把一次成功运行蒸馏为经验记忆(回流长期记忆)。
	AddMemory(m *ExperienceMemory) (*ExperienceMemory, error)
	// RetrieveMemories 检索该 agent 的历史经验(提炼补丁处置要点用)。
	RetrieveMemories(tenantID, agentID, query string) []*ExperienceMemory
	// PublishSkillVersion 发布一个新版本技能包(含经验沉淀的 system prompt)。
	PublishSkillVersion(ctx context.Context, tenantID, agentID string, patch SkillPatchSuggestion) (*SkillPackage, error)
}

// SkillUsageOutcome 一次 agent 运行的技能使用结果——记忆回流/驱动演化的输入。
type SkillUsageOutcome struct {
	TenantID string
	AgentID  string
	Success  bool
	Context  string   // 运行上下文/问题描述(成功时用于蒸馏经验)
	ToolSeq  []string // 本次使用的工具序列
	Error    string
}

// SkillPatchSuggestion 技能补丁建议(由失败归因 + 历史记忆提炼)。
type SkillPatchSuggestion struct {
	AgentID               string
	FromVersion           string
	ToVersion             string
	Reason                string // 为何建议打补丁
	LearnedResolution     string // 从历史记忆提炼的处置要点
	SuggestedSystemPrompt string // 含经验沉淀的新 system prompt
}

// SkillEvolver 记忆回流驱动技能演化的引擎(可注入, nil 安全)。
type SkillEvolver interface {
	// RecordOutcome 回流一次运行结果(成功蒸馏记忆, 失败累积计数)。
	RecordOutcome(ctx context.Context, o SkillUsageOutcome) error
	// SuggestPatch 基于失败归因 + 历史记忆, 返回补丁建议; 未达阈值返回 (nil, nil)。
	SuggestPatch(ctx context.Context, tenantID, agentID string) (*SkillPatchSuggestion, error)
	// ApplyPatch 发布补丁为新版本技能包, 并重置失败计数。
	ApplyPatch(ctx context.Context, tenantID, agentID string, patch SkillPatchSuggestion) (*SkillPackage, error)
}

// skillFailureThreshold 触发补丁建议的累积失败次数阈值。
const skillFailureThreshold = 3

// agentEvoState 单个 agent 的运行结果累计与版本跟踪。
type agentEvoState struct {
	Success        int
	Failure        int
	CurrentVersion string // 最近发布的补丁版本(默认 "1.0.0")
	LastContext    string
	LastToolSeq    []string
}

// MemSkillEvolver 内存版技能演化器(nil 后端安全)。
type MemSkillEvolver struct {
	mu      sync.Mutex
	states  map[string]*agentEvoState // key = tenantID + "/" + agentID
	backend SkillEvolutionBackend
}

// NewMemSkillEvolver 构造内存技能演化器。backend 可为 nil(仅计数, 不做记忆/发布)。
func NewMemSkillEvolver(backend SkillEvolutionBackend) *MemSkillEvolver {
	return &MemSkillEvolver{
		states:  make(map[string]*agentEvoState),
		backend: backend,
	}
}

func evoKey(tenantID, agentID string) string { return tenantID + "/" + agentID }

// RecordOutcome 回流一次运行结果。
func (m *MemSkillEvolver) RecordOutcome(_ context.Context, o SkillUsageOutcome) error {
	if o.AgentID == "" {
		return nil
	}
	m.mu.Lock()
	st := m.states[evoKey(o.TenantID, o.AgentID)]
	if st == nil {
		st = &agentEvoState{CurrentVersion: "1.0.0"}
		m.states[evoKey(o.TenantID, o.AgentID)] = st
	}
	if o.Success {
		st.Success++
	} else {
		st.Failure++
	}
	if o.Context != "" {
		st.LastContext = o.Context
	}
	if len(o.ToolSeq) > 0 {
		st.LastToolSeq = o.ToolSeq
	}
	m.mu.Unlock()

	// 成功运行 → 蒸馏为经验记忆(回流长期记忆, 后续注入 prompt)。
	if o.Success && o.Context != "" && m.backend != nil {
		if _, err := m.backend.AddMemory(&ExperienceMemory{
			ID:         uuid.New().String(),
			TenantID:   o.TenantID,
			AgentID:    o.AgentID,
			Category:   "skill_evolution",
			Keywords:   defaultKeywords(o.Context),
			Problem:    o.Context,
			Resolution: strings.Join(o.ToolSeq, " → "),
			Outcome:    "succeeded",
			Source:     "skill_run",
			CreatedAt:  time.Now(),
		}); err != nil {
			slog.Warn("ai: evolve distill memory failed", "err", err)
		}
	}
	return nil
}

// SuggestPatch 基于失败归因 + 历史记忆提炼补丁建议; 未达阈值返回 (nil, nil)。
func (m *MemSkillEvolver) SuggestPatch(_ context.Context, tenantID, agentID string) (*SkillPatchSuggestion, error) {
	if m.backend == nil {
		return nil, fmt.Errorf("skill evolver backend not configured")
	}
	ag, ok := m.backend.GetAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}
	m.mu.Lock()
	st := m.states[evoKey(tenantID, agentID)]
	m.mu.Unlock()
	if st == nil || st.Failure < skillFailureThreshold {
		return nil, nil
	}

	// 从历史记忆提炼处置要点(同类问题已沉淀的处置)。
	learned := ""
	mems := m.backend.RetrieveMemories(tenantID, agentID, st.LastContext)
	for _, mem := range mems {
		learned += "- " + mem.Problem + " → " + mem.Resolution + "\n"
	}
	if learned == "" {
		seq := strings.Join(st.LastToolSeq, " → ")
		if seq == "" {
			seq = "(无工具序列)"
		}
		learned = "（暂无历史经验，建议人工复盘本次失败序列：" + seq + "）"
	}

	fromVer := st.CurrentVersion
	toVer := bumpVersion(fromVer)
	reason := fmt.Sprintf("技能近 %d 次运行失败 %d 次，建议基于历史经验沉淀补丁版本",
		st.Success+st.Failure, st.Failure)

	suggested := ag.SystemPrompt
	if suggested != "" {
		suggested += "\n\n"
	}
	suggested += "【经验沉淀-自动补丁】\n" + learned

	return &SkillPatchSuggestion{
		AgentID:               agentID,
		FromVersion:           fromVer,
		ToVersion:             toVer,
		Reason:                reason,
		LearnedResolution:     learned,
		SuggestedSystemPrompt: suggested,
	}, nil
}

// ApplyPatch 发布补丁为新版本技能包, 并重置失败计数(避免反复触发)。
func (m *MemSkillEvolver) ApplyPatch(ctx context.Context, tenantID, agentID string, patch SkillPatchSuggestion) (*SkillPackage, error) {
	if m.backend == nil {
		return nil, fmt.Errorf("skill evolver backend not configured")
	}
	pkg, err := m.backend.PublishSkillVersion(ctx, tenantID, agentID, patch)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	st := m.states[evoKey(tenantID, agentID)]
	if st == nil {
		st = &agentEvoState{CurrentVersion: "1.0.0"}
		m.states[evoKey(tenantID, agentID)] = st
	}
	st.CurrentVersion = patch.ToVersion
	st.Failure = 0 // 补丁发布后重置, 避免同一失败持续触发
	m.mu.Unlock()
	return pkg, nil
}

// bumpVersion 对语义化版本末位 +1 (如 "1.0.0" → "1.0.1"); 非法输入回落 "1.0.1"。
func bumpVersion(v string) string {
	if v == "" {
		return "1.0.1"
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 {
		return "1.0.1"
	}
	last := parts[len(parts)-1]
	n := 0
	if _, err := fmt.Sscanf(last, "%d", &n); err != nil {
		return v + ".1"
	}
	parts[len(parts)-1] = fmt.Sprintf("%d", n+1)
	return strings.Join(parts, ".")
}
