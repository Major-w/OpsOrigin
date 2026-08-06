package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// 提示词库 (Prompt Library)
//
// —— 与技能包(SkillPackage)的职责边界 ——
//
//	提示词 = 一段可复用的「文本」。人设、约束、输出格式、排查话术, 支持 {{变量}} 占位,
//	         与运行环境无关, 不携带工具/模型/知识库绑定, 复制粘贴即可用。
//	技能包 = 一个可移植的「运行单元」。Agent 完整配置(模型 + 系统提示词 + 知识库分类 +
//	         工具白名单 + 技能指令) + 依赖的自定义 MCP 工具定义, 安装即上岗。
//
//	依赖方向单向: 技能包内嵌提示词文本, 提示词不感知技能包。
//	选型口径: 只想复用一段话术 → 提示词; 想把整个数字员工搬到另一套环境 → 技能包。
//
// 存储: ai_prompts 表, 整对象 JSONB(与技能包/巡检同构), 结构演进无需改表。
// ============================================================================

// PromptTemplate 提示词库中的一条可复用提示词。
type PromptTemplate struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Scene       string    `json:"scene,omitempty"` // 场景: 通用/故障排查/变更评审/告警降噪/知识沉淀/容量规划
	Description string    `json:"description,omitempty"`
	Content     string    `json:"content"`
	Variables   []string  `json:"variables,omitempty"` // 由 Content 解析得出, 只读(写入时忽略入参)
	Builtin     bool      `json:"builtin"`             // 预置提示词: 可编辑内容/可另存副本, 不可删除
	IsDefault   bool      `json:"is_default"`          // 全局默认系统提示词(租户内唯一), 保存即热生效
	Usage       int       `json:"usage"`               // 被引用次数(渲染/插入 Agent 时自增)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DefaultSystemPromptID 是全局默认系统提示词的固定 ID(租户内唯一, 便于幂等 seed 与前端定位)。
const DefaultSystemPromptID = "prompt-default-system"

// promptVarRe 匹配 {{ 变量名 }} 占位, 变量名允许中英文/数字/下划线。
var promptVarRe = regexp.MustCompile(`\{\{\s*([\p{Han}A-Za-z0-9_]+)\s*\}\}`)

// parsePromptVariables 从提示词正文解析出去重后的占位变量(保持首次出现顺序)。
func parsePromptVariables(content string) []string {
	matches := promptVarRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		v := m[1]
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// renderPromptContent 用 vars 替换正文中的 {{变量}}; 未提供值的占位原样保留(便于人工补全)。
func renderPromptContent(content string, vars map[string]string) string {
	if len(vars) == 0 {
		return content
	}
	return promptVarRe.ReplaceAllStringFunc(content, func(raw string) string {
		m := promptVarRe.FindStringSubmatch(raw)
		if len(m) < 2 {
			return raw
		}
		if v, ok := vars[m[1]]; ok {
			return v
		}
		return raw
	})
}

// ListPrompts 返回该租户的提示词库(可按 scene 过滤)。
// 排序: 默认系统提示词置顶 → 预置 → 自建, 组内按更新时间倒序。
func (e *Engine) ListPrompts(ctx context.Context, tenantID, scene string) []*PromptTemplate {
	if e.store == nil {
		return []*PromptTemplate{}
	}
	list, err := e.store.ListPromptTemplates(ctx, tenantID)
	if err != nil {
		slog.Error("list prompt templates failed", "err", err)
		return []*PromptTemplate{}
	}
	out := make([]*PromptTemplate, 0, len(list))
	for _, p := range list {
		if scene != "" && p.Scene != scene {
			continue
		}
		p.Variables = parsePromptVariables(p.Content)
		out = append(out, p)
	}
	rank := func(p *PromptTemplate) int {
		switch {
		case p.IsDefault:
			return 0
		case p.Builtin:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

// GetPrompt 读取单条提示词。
func (e *Engine) GetPrompt(ctx context.Context, tenantID, id string) (*PromptTemplate, error) {
	if e.store == nil {
		return nil, fmt.Errorf("store not configured")
	}
	p, err := e.store.GetPromptTemplate(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("prompt not found: %s", id)
	}
	p.Variables = parsePromptVariables(p.Content)
	return p, nil
}

// SavePrompt 新建或更新一条提示词(ID 为空则新建)。
//
// 约束:
//   - Name/Content 必填;
//   - Builtin 标记只由 seed 赋予, 前端改不动(更新时沿用既有值, 新建恒为 false);
//   - IsDefault 租户内唯一: 置为默认时自动摘掉旧默认, 并热生效到引擎兜底提示词。
func (e *Engine) SavePrompt(ctx context.Context, tenantID string, in *PromptTemplate) (*PromptTemplate, error) {
	if e.store == nil {
		return nil, fmt.Errorf("store not configured")
	}
	name := strings.TrimSpace(in.Name)
	content := strings.TrimSpace(in.Content)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}

	now := time.Now()
	p := &PromptTemplate{
		ID:          in.ID,
		TenantID:    tenantID,
		Name:        name,
		Scene:       strings.TrimSpace(in.Scene),
		Description: strings.TrimSpace(in.Description),
		Content:     content,
		IsDefault:   in.IsDefault,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if p.ID == "" {
		p.ID = uuid.New().String()
	} else if old, err := e.store.GetPromptTemplate(ctx, tenantID, p.ID); err == nil && old != nil {
		// 更新: 保留不可由前端改写的字段
		p.Builtin = old.Builtin
		p.Usage = old.Usage
		p.CreatedAt = old.CreatedAt
		// 默认提示词不允许被降级为普通提示词(否则租户将无兜底), 只能被另一条顶替
		if old.IsDefault {
			p.IsDefault = true
		}
	}

	// 默认唯一性: 摘掉其它条目的默认标记
	if p.IsDefault {
		if others, err := e.store.ListPromptTemplates(ctx, tenantID); err == nil {
			for _, o := range others {
				if o.ID == p.ID || !o.IsDefault {
					continue
				}
				o.IsDefault = false
				o.UpdatedAt = now
				if err := e.store.CreatePromptTemplate(ctx, o); err != nil {
					slog.Warn("demote previous default prompt failed", "id", o.ID, "err", err)
				}
			}
		}
	}

	if err := e.store.CreatePromptTemplate(ctx, p); err != nil {
		return nil, err
	}
	if p.IsDefault {
		e.SetDefaultSystemPrompt(p.Content) // 热生效, 无需重启 Core
	}
	p.Variables = parsePromptVariables(p.Content)
	return p, nil
}

// DeletePrompt 删除一条提示词。预置与默认提示词受保护, 拒绝删除。
func (e *Engine) DeletePrompt(ctx context.Context, tenantID, id string) error {
	if e.store == nil {
		return fmt.Errorf("store not configured")
	}
	p, err := e.store.GetPromptTemplate(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("prompt not found: %s", id)
	}
	if p.IsDefault {
		return fmt.Errorf("默认系统提示词不可删除, 请先将其它提示词设为默认")
	}
	if p.Builtin {
		return fmt.Errorf("预置提示词不可删除, 可另存副本后修改")
	}
	return e.store.DeletePromptTemplate(ctx, tenantID, id)
}

// RenderPrompt 用变量渲染提示词正文并累计引用次数。
// 未提供值的占位原样保留, 由调用方(前端预览/Agent 表单)决定是否补全。
func (e *Engine) RenderPrompt(ctx context.Context, tenantID, id string, vars map[string]string) (string, error) {
	p, err := e.GetPrompt(ctx, tenantID, id)
	if err != nil {
		return "", err
	}
	if err := e.store.IncrPromptUsage(ctx, tenantID, id); err != nil {
		slog.Warn("incr prompt usage failed", "id", id, "err", err)
	}
	return renderPromptContent(p.Content, vars), nil
}

// ——— 预置提示词 seed ———

// SeedPromptTemplates 幂等注入预置提示词, 并把默认系统提示词回灌到引擎兜底值。
// 幂等策略: 按固定 ID 判断存在性; 已存在则只补齐缺失的元数据, 不覆盖用户改过的正文。
func SeedPromptTemplates(ctx context.Context, e *Engine) {
	if e == nil || e.store == nil {
		return
	}
	seeded, skipped := 0, 0
	for _, p := range presetPrompts() {
		existing, err := e.store.GetPromptTemplate(ctx, p.TenantID, p.ID)
		if err != nil {
			slog.Warn("seed prompt: lookup failed", "id", p.ID, "err", err)
			continue
		}
		if existing != nil {
			skipped++
			if existing.IsDefault {
				e.SetDefaultSystemPrompt(existing.Content) // 用户改过的默认提示词优先生效
			}
			continue
		}
		if err := e.store.CreatePromptTemplate(ctx, p); err != nil {
			slog.Warn("seed prompt failed", "id", p.ID, "err", err)
			continue
		}
		seeded++
		if p.IsDefault {
			e.SetDefaultSystemPrompt(p.Content)
		}
	}
	if seeded > 0 || skipped > 0 {
		slog.Info("prompt templates seeded", "created", seeded, "existing", skipped)
	}
}

// presetPrompts 平台随包提供的提示词。ID 固定以保证幂等。
func presetPrompts() []*PromptTemplate {
	now := time.Now()
	mk := func(id, name, scene, desc, content string, isDefault bool) *PromptTemplate {
		return &PromptTemplate{
			ID: id, TenantID: "default", Name: name, Scene: scene,
			Description: desc, Content: content,
			Builtin: true, IsDefault: isDefault,
			CreatedAt: now, UpdatedAt: now,
		}
	}
	return []*PromptTemplate{
		mk(DefaultSystemPromptID, "默认系统提示词", "通用",
			"所有未单独配置 system_prompt 的 Agent 与对话的兜底人设, 保存后立即热生效。",
			DefaultSystemPrompt, true),

		mk("prompt-troubleshoot", "故障排查（分层定位）", "故障排查",
			"按「现象→影响面→分层定位→结论」输出, 适合一线值班快速收敛。",
			`你是一位资深 SRE, 负责快速定位线上故障。

现象: {{现象}}
涉及系统: {{系统}}

请严格按以下结构回答:
1. 影响面判定 — 受影响的服务、用户比例、是否已触发 SLA 告警;
2. 分层定位 — 依次排查 应用层 / 中间件 / 数据库 / 网络 / 主机资源, 每层给出「要看的指标或命令」与「异常判据」;
3. 最可能根因 — 给出 Top 3 假设并按可能性排序, 说明支撑证据;
4. 处置建议 — 区分「立即止血」与「根治」, 止血步骤必须可回滚。

约束: 不臆造未提供的监控数据; 缺数据时明确写出「需要补充: xxx」。`, false),

		mk("prompt-change-review", "变更评审（风险前置）", "变更评审",
			"对变更单做风险评估与回滚校验, 输出可直接贴进工单的评审意见。",
			`你是变更管理评审员, 对以下变更做风险前置评估。

变更内容: {{变更内容}}
影响范围: {{影响范围}}
计划窗口: {{窗口}}

输出:
1. 风险等级(低/中/高)及判定理由;
2. 关键风险点清单, 每条标注「触发条件 + 影响 + 缓解措施」;
3. 回滚方案检查 — 回滚步骤是否完整、耗时是否可接受、是否有数据不可逆操作;
4. 放行结论 — 同意 / 补充材料后再审 / 驳回, 并列出前置条件。

约束: 涉及数据删除或不可逆操作时风险等级不得低于「高」。`, false),

		mk("prompt-alert-triage", "告警降噪（聚合归并）", "告警降噪",
			"把成片告警归并为少量事件, 抑制抖动与衍生告警。",
			`你是告警治理专家。以下是一个时间窗内的原始告警:

{{告警列表}}

请完成:
1. 归并 — 按「同一根因」把告警聚成若干事件, 每个事件给出标题、首发时间、涉及实例数;
2. 抑制 — 标出属于衍生告警(由其它事件引发)与抖动告警(短时自愈)的条目并说明依据;
3. 定级 — 每个事件给出建议级别(P1~P4)与理由;
4. 建议动作 — 每个事件给出下一步(自动修复 / 转工单 / 观察)。

约束: 归并必须给出可验证依据(时间相关性、拓扑上下游、指标同向), 不做无根据合并。`, false),

		mk("prompt-knowledge-distill", "知识沉淀（事件转文档）", "知识沉淀",
			"把一次处置过程整理成结构化知识库文章, 供后续检索复用。",
			`把下面这次处置过程整理成一篇可检索的知识库文章。

处置记录: {{处置记录}}

输出 Markdown, 结构固定:
# 标题（一句话概括问题, 含关键组件名）
## 适用场景
## 现象特征（便于检索的报错关键字、指标形态）
## 根因
## 处置步骤（编号, 每步含命令或操作 + 预期结果）
## 验证方法
## 预防措施
## 相关配置项（涉及的主机 / 服务 / 中间件）

约束: 命令必须标注适用系统版本; 隐去密钥、令牌、内网域名等敏感信息。`, false),

		mk("prompt-capacity", "容量规划（趋势外推）", "容量规划",
			"基于历史指标给出扩容窗口与配额建议, 输出结论先行。",
			`你是容量规划顾问, 基于以下指标做扩容研判。

资源对象: {{资源对象}}
历史指标: {{指标数据}}

输出:
1. 结论先行 — 是否需要扩容、建议时间窗、建议规格;
2. 趋势判断 — 增长形态(线性/阶跃/周期), 触及水位线的预计时间;
3. 风险 — 若不扩容, 最早何时出现容量事件及影响;
4. 成本权衡 — 给出「保守 / 均衡 / 激进」三档方案的资源量对比。

约束: 数据不足以外推时明确说明所需最小观测窗口, 不给出无依据的具体日期。`, false),
	}
}
