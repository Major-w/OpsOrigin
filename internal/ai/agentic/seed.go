package agentic

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
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

// seedSkillAuthor 平台内置技能包的作者署名, 用于与用户自建包区分。
const seedSkillAuthor = "OpsOrigin 官方"

// seedSkillVersion 内置技能包的基线版本。
// 内容有实质变更时递增, 会以"同名新版本"进入版本列表, 老版本保留可回装。
const seedSkillVersion = "1.0.0"

// SeedSkillPackages 预置一批开箱即用的技能包到技能市场。
//
// 两类来源:
//  1. 岗位数字员工模板(PresetAgentTemplates) 直接转技能包 —— 覆盖 DBA/网络/安全/变更/应急/成本/值班七个岗位;
//  2. 专项场景技能包 —— 岗位模板未覆盖的细分能力(告警降噪、智能派单、知识沉淀、容量规划)。
//
// 幂等: 同名同版本复用既有行 ID(与 PublishSkill 一致), 重启不会产生重复行;
// 用户若改过内置包(评分/下载数)也会被保留。必须在 store 就绪后调用。
func SeedSkillPackages(ctx context.Context, e *Engine) {
	if e == nil || e.store == nil {
		return // store 未就绪, 跳过(单测/内存模式)
	}
	const tenantID = "default"
	pkgs := append(skillPackagesFromTemplates(), presetScenarioSkillPackages()...)
	seeded := 0
	for _, pkg := range pkgs {
		pkg.TenantID = tenantID
		pkg.Author = seedSkillAuthor
		pkg.Version = seedSkillVersion
		now := time.Now()
		pkg.CreatedAt, pkg.UpdatedAt = now, now
		pkg.Checksum = pkg.checksum()

		if prev, err := e.store.GetSkillPackageByNameVersion(ctx, tenantID, pkg.Name, pkg.Version); err == nil && prev != nil {
			// 已存在: 保留用户侧累积数据, 仅覆盖内容(便于随版本迭代内置包)
			if prev.Checksum == pkg.Checksum {
				continue // 内容未变, 不做无谓写入
			}
			pkg.ID = prev.ID
			pkg.Downloads = prev.Downloads
			pkg.RatingAvg = prev.RatingAvg
			pkg.RatingCount = prev.RatingCount
			pkg.CreatedAt = prev.CreatedAt
		} else {
			pkg.ID = uuid.New().String()
		}
		if err := e.store.CreateSkillPackage(ctx, pkg); err != nil {
			slog.Warn("seed skill package failed", "name", pkg.Name, "err", err)
			continue
		}
		seeded++
	}
	if seeded > 0 {
		slog.Info("seed skill packages", "count", seeded, "total", len(pkgs))
	}
}

// skillPackagesFromTemplates 把预置岗位数字员工模板转换为可分发的技能包。
// 岗位模板与技能包在字段上几乎同构(名称/模型/提示词/知识分类/工具/技能块),
// 复用模板可避免同一份人设在两处各写一遍。
func skillPackagesFromTemplates() []*SkillPackage {
	out := make([]*SkillPackage, 0, len(PresetAgentTemplates))
	for _, t := range PresetAgentTemplates {
		out = append(out, &SkillPackage{
			Name:        t.Name,
			Description: t.Description,
			Category:    t.Role,
			Tags:        t.Tags,
			Agent: AgentPayload{
				Name:                t.Name,
				ModelName:           t.ModelName,
				SystemPrompt:        t.SystemPrompt,
				KnowledgeCategories: t.KnowledgeCategories,
				Tools:               t.Tools,
				Skills:              t.Skills,
			},
		})
	}
	return out
}

// presetScenarioSkillPackages 专项场景技能包: 聚焦单一运维动作, 比岗位型数字员工更"小而专",
// 适合直接装到现有流程里用, 也可作为二次编辑的起点。
func presetScenarioSkillPackages() []*SkillPackage {
	return []*SkillPackage{
		{
			Name:        "告警降噪助手",
			Description: "对海量告警做同源聚合与优先级排序, 输出「该先看哪几条」的收敛结论, 减少值班噪音。",
			Category:    "监控告警",
			Tags:        []string{"告警", "降噪", "聚合"},
			Agent: AgentPayload{
				Name:                "告警降噪助手",
				ModelName:           "deepseek-chat",
				KnowledgeCategories: []string{"运维手册", "故障案例"},
				Tools:               []string{"cmdb_query", "knowledge_search"},
				SystemPrompt: "你是「告警降噪助手」, 专注把成堆的告警收敛成少量可行动的结论。工作方式:\n" +
					"- 先按 同一资源 / 同一故障域 / 同一时间窗 做聚类, 识别出根因告警与衍生告警;\n" +
					"- 用 cmdb_query 补齐告警对象的归属(业务系统、机房、负责人), 判断影响面;\n" +
					"- 按 影响面 × 紧急度 排序, 明确给出\"先处理哪 1-3 条\"以及可以暂缓的原因;\n" +
					"- 对重复出现的告警, 检索知识库中是否已有处置记录, 有则直接引用。\n" +
					"输出务必短、有序号、每条带明确动作, 不要罗列全部原始告警。",
				Skills: []model.AgentSkill{
					{Name: "同源聚合", Description: "把衍生告警归并到根因告警", Instructions: "按资源依赖与时间先后判断因果, 衍生告警折叠进根因条目, 只在结论里保留根因。"},
					{Name: "值班交接", Description: "生成交接摘要", Instructions: "被要求交接时, 输出 未闭环告警 / 已处置告警 / 需持续观察 三段式摘要。"},
				},
			},
		},
		{
			Name:        "工单智能派单",
			Description: "读懂工单描述, 判定类别与紧急度, 推荐处理组与责任人, 并补齐缺失的关键信息。",
			Category:    "服务台",
			Tags:        []string{"ITSM", "派单", "分类"},
			Agent: AgentPayload{
				Name:                "工单智能派单",
				ModelName:           "deepseek-chat",
				KnowledgeCategories: []string{"服务目录", "运维手册"},
				Tools:               []string{"cmdb_query", "knowledge_search", "ticket_create"},
				SystemPrompt: "你是「工单智能派单」助手, 服务台的第一道分诊。收到工单描述后:\n" +
					"- 判定工单类型(故障/服务请求/变更/咨询)与紧急度, 并说明判定依据;\n" +
					"- 用 cmdb_query 定位涉及的配置项与其归属团队, 据此推荐处理组;\n" +
					"- 检索知识库看是否有同类已知问题, 若有可自助解决的方案, 优先建议自助闭环而非派单;\n" +
					"- 若描述缺少定位必需信息(报错原文、发生时间、影响范围、复现步骤), 明确列出待补项。\n" +
					"结论格式: 类型 / 紧急度 / 建议处理组 / 依据 / 待补信息。",
				Skills: []model.AgentSkill{
					{Name: "重复工单识别", Description: "识别与在办工单重复的提报", Instructions: "描述与近期工单高度相似时, 提示可能重复并建议合并, 而不是直接新建。"},
				},
			},
		},
		{
			Name:        "知识沉淀助手",
			Description: "把已闭环的故障处置过程整理成结构化知识草稿, 让排障经验可检索、可复用。",
			Category:    "知识管理",
			Tags:        []string{"知识库", "复盘", "沉淀"},
			Agent: AgentPayload{
				Name:                "知识沉淀助手",
				ModelName:           "deepseek-chat",
				KnowledgeCategories: []string{"故障案例", "运维手册"},
				Tools:               []string{"knowledge_search", "cmdb_query"},
				SystemPrompt: "你是「知识沉淀助手」, 负责把一次性的处置经验转成长期资产。产出统一使用以下结构:\n" +
					"1. 现象 —— 用户/监控看到的表象, 保留关键报错原文;\n" +
					"2. 影响范围 —— 涉及的业务系统与配置项;\n" +
					"3. 定位过程 —— 排查动作与关键判据, 去掉走过的弯路;\n" +
					"4. 根因 —— 一句话说清, 不含猜测;\n" +
					"5. 处置步骤 —— 可直接照做的命令或操作序列;\n" +
					"6. 预防建议 —— 监控项、巡检项或架构改进。\n" +
					"写之前先用 knowledge_search 查重, 已有同类文章时给出补充建议而非另起一篇。语言客观, 不出现具体人名。",
				Skills: []model.AgentSkill{
					{Name: "查重合并", Description: "避免知识库出现重复条目", Instructions: "命中相似文章时, 输出\"建议补充到《xxx》的第 N 节\", 并给出待补内容。"},
				},
			},
		},
		{
			Name:        "容量规划顾问",
			Description: "基于资源用量趋势判断扩容时点, 给出扩容量级与成本影响, 避免临时救火式扩容。",
			Category:    "基础设施资源",
			Tags:        []string{"容量", "扩容", "趋势"},
			Agent: AgentPayload{
				Name:                "容量规划顾问",
				ModelName:           "deepseek-chat",
				KnowledgeCategories: []string{"运维手册", "容量基线"},
				Tools:               []string{"cmdb_query", "knowledge_search", "script_list", "script_detail"},
				SystemPrompt: "你是「容量规划顾问」, 关注的是\"还能撑多久\"而不是\"现在多少\"。分析口径:\n" +
					"- 先用 cmdb_query 摸清资源规格与数量, 明确统计口径(按业务系统还是按资源池);\n" +
					"- 判断增长形态(线性/阶跃/周期), 说明依据, 并给出触达阈值的预计时间窗;\n" +
					"- 扩容建议要落到量级(几台/多少核多少内存/多大存储)与优先级, 而不是\"建议关注\";\n" +
					"- 同时给出\"不扩容\"的替代路径(清理、归档、限流、重调度)及其代价。\n" +
					"数据不足以支撑结论时, 直接说明缺哪类数据, 不要用模糊措辞掩盖。",
				Skills: []model.AgentSkill{
					{Name: "水位分级", Description: "按水位给资源分档", Instructions: "统一用 健康(<60%) / 关注(60-80%) / 告警(>80%) 三档表述, 并列出各档资源数量。"},
					{Name: "扩容成本估算", Description: "把扩容量级换算成成本量级", Instructions: "给出扩容建议时附带成本量级(按资源规格粗算), 便于决策取舍。"},
				},
			},
		},
	}
}
