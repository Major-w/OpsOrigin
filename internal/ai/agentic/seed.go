package agentic

import (
	"context"
	"time"

	"github.com/opsorigin/core/internal/ai/model"
)

// SeedPresetTeams 在引擎启动时预置平台交付团队(Feature 1 默认团队)。
// 按团队名称幂等: 若已存在同名团队则跳过, 避免进程重启/重复 NewEngine 时重复创建。
func SeedPresetTeams(e *Engine) {
	const presetName = "平台交付团队"
	for _, t := range e.ListTeams("default") {
		if t.Name == presetName {
			return // 已存在, 跳过
		}
	}

	now := time.Now()
	team := &model.AgentTeam{
		TenantID:     "default",
		Name:         presetName,
		Description:  "面向平台交付的 5 人虚拟团队: 架构设计为主管负责拆解, 实施顾问为汇总者负责定稿, 其余成员并行产出。",
		Mode:         model.TeamModeSupervisor,
		SystemPrompt: "你是一个跨职能交付团队的协作中枢, 团队成员各司其职, 由汇总者整合产出形成面向客户的最终答复。",
		CreatedAt:    now,
		UpdatedAt:    now,
		Members: []model.TeamMember{
			{
				AgentID:      "seed-architect",
				AgentName:    "架构设计",
				Role:         model.TeamRoleSupervisor,
				CanFinalize:  false,
				Persona:      "资深系统架构师, 擅长把模糊需求拆解为清晰、可执行的子任务",
				SystemPrompt: "你是团队主管(架构设计)。你的职责是把用户的需求拆解为若干独立子任务, 并明确指派给对应成员。你不直接产出最终方案, 只做任务分解与调度。",
			},
			{
				AgentID:      "seed-req",
				AgentName:    "需求分析",
				Role:         model.TeamRoleMember,
				CanFinalize:  false,
				Persona:      "敏锐的需求分析师, 善于挖掘隐含诉求与边界条件",
				SystemPrompt: "你是需求分析师。请从业务视角梳理用户诉求, 明确目标、约束、验收标准与潜在风险, 输出结构化的需求理解。",
			},
			{
				AgentID:      "seed-impl",
				AgentName:    "实施顾问",
				Role:         model.TeamRoleFinalizer,
				CanFinalize:  true,
				Persona:      "沉稳的实施顾问, 能把多方输入收敛为可落地的交付方案",
				SystemPrompt: "你是实施顾问, 也是团队的汇总者。你会收到其他成员的产出, 负责综合、裁定并给出面向客户的最终可落地方案。你的输出应直接、专业、可操作。",
			},
			{
				AgentID:      "seed-dev",
				AgentName:    "集成开发",
				Role:         model.TeamRoleMember,
				CanFinalize:  false,
				Persona:      "务实的集成开发工程师, 关注接口、数据流与实现成本",
				SystemPrompt: "你是集成开发工程师。请从技术实现角度评估方案的可行性, 说明关键技术选型、集成点与需要注意的坑点。",
			},
			{
				AgentID:      "seed-ops",
				AgentName:    "售后支持",
				Role:         model.TeamRoleMember,
				CanFinalize:  false,
				Persona:      "贴心的售后支持, 站在客户成功角度补齐使用与保障细节",
				SystemPrompt: "你是售后支持。请从客户成功与持续运营角度, 补充上线后的支撑、培训、监控与回滚保障等落地细节。",
			},
		},
	}

	if err := e.CreateTeam(context.Background(), team); err != nil {
		// 预置失败不应阻断引擎启动
		// (e.g. 并发竞态导致已存在), 仅记录。
		_ = err
	}
}
