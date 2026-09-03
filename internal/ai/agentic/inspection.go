package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/llm"
)

// —— 巡检周期(简化 cron) ——
const (
	ScheduleHourly  = "@hourly"
	ScheduleEvery6h = "@every_6h"
	ScheduleDaily   = "@daily"
	ScheduleWeekly  = "@weekly"
)

// InspectionTemplate 内置巡检模板目录(与岗位模板同构, 平台交付即开箱可用)。
type InspectionTemplate struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Category        string `json:"category"`
	Description     string `json:"description"`
	Prompt          string `json:"prompt"`     // 注入 Agent 系统提示的巡检指令
	ScopeHint       string `json:"scope_hint"` // 建议的巡检范围(给用户参考)
	DefaultSchedule string `json:"default_schedule"`
}

// Inspection 一条用户可管理的巡检任务(启停/改周期/改范围)。
type Inspection struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Name       string     `json:"name"`
	TemplateID string     `json:"template_id,omitempty"`
	AgentID    string     `json:"agent_id,omitempty"` // 指定执行 Agent; 空=引擎默认(全工具)
	TeamID     string     `json:"team_id,omitempty"`  // 指定执行团队(多 Agent 协作编排); 优先级高于 AgentID
	Prompt     string     `json:"prompt"`             // 巡检指令(覆盖/补充模板)
	Scope      string     `json:"scope"`              // 巡检范围描述(如 全部主机 / 某 CMDB 类型)
	Schedule   string     `json:"schedule"`           // @hourly/@daily/...
	Enabled    bool       `json:"enabled"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
	NextRunAt  time.Time  `json:"next_run_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Escalation 一条由巡检发现转出的 ITSM 工单记录(幂等: 同一发现只转一次)。
type Escalation struct {
	FindingIdx  int       `json:"finding_idx"`            // 对应 findings JSON 数组中的下标
	Level       string    `json:"level"`                  // high | medium | low
	Title       string    `json:"title"`                  // 发现标题
	TicketID    string    `json:"ticket_id"`              // ITSM 工单 ID
	TicketTitle string    `json:"ticket_title,omitempty"` // 工单标题
	CreatedAt   time.Time `json:"created_at"`
}

// InspectionRun 一次巡检执行记录(只读追加, 便于追溯与复盘)。
type InspectionRun struct {
	ID           string     `json:"id"`
	InspectionID string     `json:"inspection_id"`
	TenantID     string     `json:"tenant_id"`
	TriggeredBy  string     `json:"triggered_by"`       // scheduled | manual
	Status       string     `json:"status"`             // running | success | failed
	Summary      string     `json:"summary"`            // 巡检报告正文
	Findings     string     `json:"findings,omitempty"` // 结构化发现(JSON 字符串)
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	// Escalations 已转工单的发现记录(通过 /runs/{id}/escalate 追加)。
	Escalations []Escalation `json:"escalations,omitempty"`
	// Experiment 对比实验元数据(策略优化 A/B), 非实验运行时为 nil。
	Experiment *ExperimentMeta `json:"experiment,omitempty"`
}

// InspectionFinding 巡检发现的通用结构(findings JSON 数组元素)。
// 与前端 parseFindings 及巡检指令要求的输出契约对齐。
type InspectionFinding struct {
	Level      string `json:"level"`                // high | medium | low
	Title      string `json:"title"`                // 发现标题
	Detail     string `json:"detail,omitempty"`     // 详情
	Suggestion string `json:"suggestion,omitempty"` // 处置建议
}

// PresetInspectionTemplates 平台预置巡检目录。
var PresetInspectionTemplates = []*InspectionTemplate{
	{
		ID:              "insp-config-drift",
		Name:            "配置漂移巡检",
		Category:        "配置合规",
		Description:     "检测关键资产的配置与基线/上次记录是否发生非预期偏离。",
		DefaultSchedule: ScheduleDaily,
		ScopeHint:       "全部主机 / 关键中间件实例",
		Prompt: "你是主动巡检 Agent, 正在执行「配置漂移巡检」:\n" +
			"- 通过 cmdb_query 获取资产当前配置属性, 与安全基线/知识库(knowledge_search)中的标准配置比对;\n" +
			"- 识别非预期的配置变化(新增端口、权限放宽、版本回退等), 标注 高/中/低 风险;\n" +
			"- 说明漂移可能带来的后果与回滚/加固建议。",
	},
	{
		ID:              "insp-capacity",
		Name:            "容量水位巡检",
		Category:        "容量",
		Description:     "对主机/数据库/中间件的容量与增长趋势做每日体检, 提前预警。",
		DefaultSchedule: ScheduleDaily,
		ScopeHint:       "全部主机 / 数据库实例",
		Prompt: "你是主动巡检 Agent, 正在执行「容量水位巡检」:\n" +
			"- 通过 cmdb_query 盘点主机/数据库/中间件资产及其容量属性;\n" +
			"- 结合知识库中的容量阈值, 标记已超阈值或 7 日内将达阈值的资源, 标注 高/中/低;\n" +
			"- 给出扩容/降配/清理的量化建议与预估。",
	},
	{
		ID:              "insp-security-baseline",
		Name:            "安全基线巡检",
		Category:        "安全",
		Description:     "周期性核查资产是否符合安全基线(暴露面/弱口令风险/补丁)。",
		DefaultSchedule: ScheduleWeekly,
		ScopeHint:       "全部对外暴露资产 / 核心业务系统",
		Prompt: "你是主动巡检 Agent, 正在执行「安全基线巡检」:\n" +
			"- 通过 cmdb_query 梳理资产与暴露面, 对照知识库(knowledge_search)中的安全基线;\n" +
			"- 检查不必要的公网暴露、权限过大、补丁缺失等, 标注 高/中/低;\n" +
			"- 映射到合规要求并给出整改建议, 具体到资产实例。",
	},
	{
		ID:              "insp-cert-expiry",
		Name:            "证书到期巡检",
		Category:        "安全",
		Description:     "扫描 TLS/域名证书有效期, 提前提醒续期避免业务中断。",
		DefaultSchedule: ScheduleWeekly,
		ScopeHint:       "全部含证书的域名 / 负载均衡 / 网关",
		Prompt: "你是主动巡检 Agent, 正在执行「证书到期巡检」:\n" +
			"- 通过 cmdb_query 识别承载证书的域名/网关/负载均衡资产, 读取证书有效期属性;\n" +
			"  (若 cmdb 无该属性, 明确说明, 并建议使用 knowledge_search 中的续期流程)\n" +
			"- 标记 30 天内到期(高)、90 天内到期(中)的证书, 给出续期责任人与步骤。",
	},
	{
		ID:              "insp-host-health",
		Name:            "主机健康巡检",
		Category:        "健康",
		Description:     "每日体检主机运行状态、Agent 在线率与核心服务可用性。",
		DefaultSchedule: ScheduleDaily,
		ScopeHint:       "全部受管主机",
		Prompt: "你是主动巡检 Agent, 正在执行「主机健康巡检」:\n" +
			"- 通过 cmdb_query 盘点受管主机及其状态/在线情况;\n" +
			"- 结合 script_executions 历史判断近期执行成功率, 标记离线/异常/高频失败主机(高/中/低);\n" +
			"- 给出是否需要人工介入的判断与建议动作。",
	},
	{
		ID:              "insp-kb-freshness",
		Name:            "知识库时效巡检",
		Category:        "知识",
		Description:     "检查知识库文档时效, 标记长期未更新或可能过期的运维手册。",
		DefaultSchedule: ScheduleWeekly,
		ScopeHint:       "全部知识库文章",
		Prompt: "你是主动巡检 Agent, 正在执行「知识库时效巡检」:\n" +
			"- 通过 knowledge_search 检索运维手册类文章, 评估其时效与适用性;\n" +
			"- 标记超过 365 天未更新、或内容与当前架构明显脱节的文章, 标注 中/低 风险;\n" +
			"- 给出需复核/补充的文章清单与建议负责人。",
	},
}

// ListInspectionTemplates 返回内置巡检模板目录。
func (e *Engine) ListInspectionTemplates() []*InspectionTemplate {
	out := make([]*InspectionTemplate, 0, len(PresetInspectionTemplates))
	for _, t := range PresetInspectionTemplates {
		out = append(out, t)
	}
	return out
}

// GetInspectionTemplate 按 ID 查模板。
func (e *Engine) GetInspectionTemplate(id string) (*InspectionTemplate, bool) {
	for _, t := range PresetInspectionTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// nextRunAt 根据周期计算下一次运行时间(从 from 起算)。未知周期回退每日。
func (e *Engine) nextRunAt(schedule string, from time.Time) time.Time {
	switch schedule {
	case ScheduleHourly:
		return from.Add(time.Hour)
	case ScheduleEvery6h:
		return from.Add(6 * time.Hour)
	case ScheduleWeekly:
		return from.Add(7 * 24 * time.Hour)
	case ScheduleDaily, "":
		return from.Add(24 * time.Hour)
	default:
		return from.Add(24 * time.Hour)
	}
}

// CreateInspection 创建巡检任务。模板 ID 可预填巡检指令; 默认每日, 启用即排程。
func (e *Engine) CreateInspection(ctx context.Context, in *Inspection) (*Inspection, error) {
	if in == nil {
		return nil, fmt.Errorf("inspection is nil")
	}
	if in.Name == "" {
		return nil, fmt.Errorf("inspection name required")
	}
	if in.ID == "" {
		in.ID = uuid.New().String()
	}
	if in.TenantID == "" {
		in.TenantID = "default"
	}
	now := time.Now()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.Schedule == "" {
		in.Schedule = ScheduleDaily
	}
	if in.Prompt == "" && in.TemplateID != "" {
		if t, ok := e.GetInspectionTemplate(in.TemplateID); ok {
			in.Prompt = t.Prompt
		}
	}
	if in.Enabled {
		in.NextRunAt = e.nextRunAt(in.Schedule, now)
	}
	e.mu.Lock()
	e.inspections[in.ID] = in
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.CreateInspection(ctx, in); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// GetInspection 按 ID 查巡检任务。
func (e *Engine) GetInspection(id string) (*Inspection, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ins, ok := e.inspections[id]
	return ins, ok
}

// ListInspections 列出租户下巡检任务(tenant 空=全部)。
func (e *Engine) ListInspections(tenantID string) []*Inspection {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*Inspection
	for _, ins := range e.inspections {
		if tenantID != "" && ins.TenantID != tenantID {
			continue
		}
		out = append(out, ins)
	}
	return out
}

// UpdateInspection 更新巡检任务(名称/指令/范围/周期/启停/执行 Agent)。
// 更新后按当前启用与周期重排下一次运行时间。
func (e *Engine) UpdateInspection(ctx context.Context, in *Inspection) (*Inspection, error) {
	if in.ID == "" {
		return nil, fmt.Errorf("inspection id required")
	}
	e.mu.Lock()
	existing, ok := e.inspections[in.ID]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("inspection not found: %s", in.ID)
	}
	if in.Name != "" {
		existing.Name = in.Name
	}
	if in.TemplateID != "" {
		existing.TemplateID = in.TemplateID
	}
	if in.AgentID != "" {
		existing.AgentID = in.AgentID
	}
	if in.TeamID != "" {
		existing.TeamID = in.TeamID
	}
	if in.Prompt != "" {
		existing.Prompt = in.Prompt
	}
	if in.Scope != "" {
		existing.Scope = in.Scope
	}
	if in.Schedule != "" {
		existing.Schedule = in.Schedule
	}
	existing.Enabled = in.Enabled
	existing.UpdatedAt = time.Now()
	if existing.Enabled {
		existing.NextRunAt = e.nextRunAt(existing.Schedule, existing.UpdatedAt)
	} else {
		existing.NextRunAt = time.Time{}
	}
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.UpdateInspection(ctx, existing); err != nil {
			return nil, err
		}
	}
	return existing, nil
}

// DeleteInspection 删除巡检任务及其运行记录。
func (e *Engine) DeleteInspection(ctx context.Context, id, tenantID string) error {
	e.mu.Lock()
	delete(e.inspections, id)
	delete(e.inspectionRuns, id)
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.DeleteInspection(ctx, id, tenantID); err != nil {
			return err
		}
	}
	return nil
}

// ListInspectionRuns 列出某巡检任务的运行记录(按开始时间倒序)。
func (e *Engine) ListInspectionRuns(inspectionID string) []*InspectionRun {
	e.mu.RLock()
	defer e.mu.RUnlock()
	runs := e.inspectionRuns[inspectionID]
	out := make([]*InspectionRun, len(runs))
	copy(out, runs)
	// 倒序: 最新在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ExperimentMeta 一次对比实验(策略优化)的覆盖参数。
// 仅影响该次运行, 不改动巡检任务本身的 prompt/scope。
type ExperimentMeta struct {
	PromptOverride string `json:"prompt_override,omitempty"` // 覆盖巡检指令(空=沿用任务 prompt)
	ScopeOverride  string `json:"scope_override,omitempty"`  // 覆盖巡检范围(空=沿用任务 scope)
	CreatedBy      string `json:"created_by,omitempty"`      // 发起实验的人(用于审计)
}

// RunInspection 立即执行一次巡检(手动或定时触发)。
// 通过 chatWithToolsConv 让 Agent 携带巡检指令作为 system 提示并可使用全部运维工具,
// 最终报告与结构化发现写回运行记录。无 LLM 时记录失败状态(不阻塞)。
func (e *Engine) RunInspection(ctx context.Context, id, triggeredBy string) (*InspectionRun, error) {
	return e.runInspection(ctx, id, triggeredBy, nil)
}

// RunInspectionExperiment 执行一次对比实验: 用覆盖的 prompt/scope 跑同一巡检任务,
// 运行记录带 experiment 标记, 便于与基线运行对比(策略优化 A/B)。
func (e *Engine) RunInspectionExperiment(ctx context.Context, id string, meta *ExperimentMeta) (*InspectionRun, error) {
	return e.runInspection(ctx, id, "experiment", meta)
}

func (e *Engine) runInspection(ctx context.Context, id, triggeredBy string, experiment *ExperimentMeta) (*InspectionRun, error) {
	e.mu.RLock()
	ins, ok := e.inspections[id]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("inspection not found: %s", id)
	}

	run := &InspectionRun{
		ID:           uuid.New().String(),
		InspectionID: id,
		TenantID:     ins.TenantID,
		TriggeredBy:  triggeredBy,
		Status:       "running",
		StartedAt:    time.Now(),
	}
	if experiment != nil {
		run.Experiment = experiment
	}
	e.mu.Lock()
	e.inspectionRuns[id] = append(e.inspectionRuns[id], run)
	e.mu.Unlock()

	// 实验模式下用覆盖参数, 不改动任务本身定义
	prompt := ins.Prompt
	scope := ins.Scope
	if experiment != nil {
		if experiment.PromptOverride != "" {
			prompt = experiment.PromptOverride
		}
		if experiment.ScopeOverride != "" {
			scope = experiment.ScopeOverride
		}
	}

	systemPrompt := prompt
	if systemPrompt == "" {
		systemPrompt = "你是一名主动巡检运维助手, 负责发现潜在风险并给出处置建议。"
	}
	// P0-1 长期记忆: 巡检时检索该 Agent 的历史处置经验注入 system prompt(同类问题参考复用)
	if mems := e.RetrieveMemories(ins.TenantID, ins.AgentID, orDefault(scope, "巡检")); len(mems) > 0 {
		systemPrompt += "\n\n" + memoriesPrompt(mems)
	}
	userPrompt := fmt.Sprintf("请对以下范围执行一次主动巡检, 并输出结构化报告:\n范围: %s\n\n要求:\n"+
		"1. 使用 cmdb_query / knowledge_search 等工具获取真实数据, 引用你实际查到的内容;\n"+
		"2. 明确指出发现的异常、风险及严重程度(高/中/低);\n"+
		"3. 给出可操作的处置建议;\n"+
		"4. 在文末用 ```json 代码块输出发现清单, 字段: [{\"level\":\"high|medium|low\",\"title\":\"...\",\"detail\":\"...\",\"suggestion\":\"...\"}]。",
		orDefault(scope, "全部受管资产"))

	history := []llm.Message{{Role: "system", Content: systemPrompt}}

	// 团队级巡检(P2-3): 绑定团队时走多 Agent 协作编排(各成员分头巡检, 汇总者定稿),
	// 否则回退到单 Agent 执行。TeamID 优先级高于 AgentID。
	var summary string
	var err error
	if ins.TeamID != "" {
		team, ok := e.GetTeam(ins.TeamID)
		if !ok {
			run.Status = "failed"
			run.Error = "team not found: " + ins.TeamID
			finishTime := time.Now()
			run.FinishedAt = &finishTime
			return run, nil
		}
		summary, _, err = e.RunTeamChat(ctx, team, userPrompt, history, nil, "")
	} else {
		summary, _, _, _, err = e.chatWithToolsConv(ctx, ins.AgentID, userPrompt, history, false, "", "", false)
	}

	now := time.Now()
	run.FinishedAt = &now
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
	} else {
		run.Status = "success"
		run.Summary = summary
		if f := extractJSONFindings(summary); f != "" {
			run.Findings = f
		}
	}

	e.mu.Lock()
	ins.LastRunAt = &now
	ins.LastStatus = run.Status
	if ins.Enabled {
		ins.NextRunAt = e.nextRunAt(ins.Schedule, now)
	} else {
		ins.NextRunAt = time.Time{}
	}
	e.mu.Unlock()

	if e.store != nil {
		_ = e.store.UpdateInspection(ctx, ins)
		_ = e.store.CreateInspectionRun(ctx, run) // upsert
	}

	// 巡检闭环通知: 完成/失败后推送平台通知(未装配回调则跳过)。
	if e.InspectionNotifyFunc != nil {
		statusLabel := "完成"
		if run.Status == "failed" {
			statusLabel = "失败"
		}
		content := fmt.Sprintf("巡检「%s」执行%s。", ins.Name, statusLabel)
		if run.Status == "success" {
			if n, err := countFindings(run.Findings); err == nil && n > 0 {
				content += fmt.Sprintf(" 本次发现 %d 项风险, 可在巡检历史查看并转工单处置。", n)
			}
		}
		if run.Error != "" {
			content += " 错误: " + run.Error
		}
		_ = e.InspectionNotifyFunc(ctx, ins.TenantID, "ai.inspection_finished", "巡检"+statusLabel+": "+ins.Name, content)
	}
	return run, nil
}

// countFindings 统计 findings JSON 数组中的发现条数(容错: 解析失败返回 0)。
func countFindings(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	var fs []InspectionFinding
	if err := json.Unmarshal([]byte(raw), &fs); err != nil {
		return 0, err
	}
	return len(fs), nil
}

// SetInspectionCallbacks 注入巡检闭环回调(由 cmd/core 装配时调用):
// createTicket 用于将巡检发现转 ITSM 工单; notify 用于巡检完成/失败通知。
// 任一传 nil 表示关闭对应能力。
func (e *Engine) SetInspectionCallbacks(
	createTicket func(ctx context.Context, tenantID, title, description string, labels map[string]string) (string, error),
	notify func(ctx context.Context, tenantID, event, title, content string) error,
) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.CreateTicketFunc = createTicket
	e.InspectionNotifyFunc = notify
}

// SetTicketMetricsProvider 注入数字员工运营效果所需的 ITSM 工单统计回调。
// p 返回: 总数、按状态分布、平均处置时长(分钟)、处置效果评估分布(confirmed/false_positive)。
func (e *Engine) SetTicketMetricsProvider(p func(ctx context.Context, tenantID string) (int, map[string]int, float64, map[string]int, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.TicketMetricsProvider = p
}

// GetInspectionRun 按运行 ID 查巡检记录(租户校验; tenantID 空=不限租户)。
func (e *Engine) GetInspectionRun(tenantID, runID string) (*InspectionRun, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runs := range e.inspectionRuns {
		for _, r := range runs {
			if r.ID == runID {
				if tenantID != "" && r.TenantID != tenantID {
					return nil, fmt.Errorf("inspection run not found: %s", runID)
				}
				return r, nil
			}
		}
	}
	return nil, fmt.Errorf("inspection run not found: %s", runID)
}

// EscalateInspectionRun 将某次巡检运行记录中达到指定级别(含)的发现批量转 ITSM 工单。
// severityLimit: "high"=仅高危, "medium"=高危+中危(默认), "low"=全部。
// 幂等: 已在 run.Escalations 记录的发现自动跳过; 每成功一条追加一条 Escalation 并持久化。
// 返回更新后的运行记录(含 escalations)。未装配 CreateTicketFunc 时报错。
func (e *Engine) EscalateInspectionRun(ctx context.Context, tenantID, runID, severityLimit string) (*InspectionRun, error) {
	run, err := e.GetInspectionRun(tenantID, runID)
	if err != nil {
		return nil, err
	}
	if e.CreateTicketFunc == nil {
		return nil, fmt.Errorf("escalation disabled: create ticket callback not wired")
	}
	if severityLimit == "" {
		severityLimit = "medium"
	}
	rank := func(lv string) int {
		switch lv {
		case "high":
			return 3
		case "medium":
			return 2
		case "low":
			return 1
		}
		return 0
	}
	limitRank := rank(severityLimit)
	if limitRank == 0 {
		return nil, fmt.Errorf("invalid severity limit: %s", severityLimit)
	}

	// 已转工单的发现下标集合(幂等去重)
	done := make(map[int]bool)
	for _, es := range run.Escalations {
		done[es.FindingIdx] = true
	}

	var findings []InspectionFinding
	if run.Findings != "" {
		_ = json.Unmarshal([]byte(run.Findings), &findings)
	}

	added := 0
	var firstErr error
	for i, f := range findings {
		if done[i] || rank(f.Level) < limitRank {
			continue
		}
		title := fmt.Sprintf("[AI巡检·%s] %s", strings.ToUpper(f.Level), orDefault(f.Title, "未命名发现"))
		desc := f.Detail
		if f.Suggestion != "" {
			if desc != "" {
				desc += "\n"
			}
			desc += "处置建议: " + f.Suggestion
		}
		ticketID, terr := e.CreateTicketFunc(ctx, run.TenantID, title, desc, map[string]string{
			"source":        "ai_inspection",
			"inspection_id": run.InspectionID,
			"run_id":        run.ID,
			"agent_id":      e.agentIDOfInspection(run.InspectionID), // P0-1: 记忆沉淀按巡检 Agent 绑定
		})
		if terr != nil {
			slog.Warn("escalate finding failed", "run_id", run.ID, "finding_idx", i, "error", terr)
			if firstErr == nil {
				firstErr = terr
			}
			continue
		}
		run.Escalations = append(run.Escalations, Escalation{
			FindingIdx:  i,
			Level:       f.Level,
			Title:       f.Title,
			TicketID:    ticketID,
			TicketTitle: title,
			CreatedAt:   time.Now(),
		})
		added++
	}

	if added > 0 && e.store != nil {
		if uerr := e.store.UpdateInspectionRun(ctx, run); uerr != nil {
			slog.Error("persist run escalations failed", "run_id", run.ID, "error", uerr)
		}
	}
	if added == 0 {
		if firstErr != nil {
			return run, fmt.Errorf("escalation failed: %v", firstErr)
		}
		return run, fmt.Errorf("no findings to escalate (level >= %s)", severityLimit)
	}
	return run, nil
}

// StartInspectionScheduler 启动进程内定时调度器(每 30s 扫描到期启用的巡检并异步执行)。
// 在 Engine.Start() 中调用一次; 进程退出由 inspectCancel 控制(当前无显式 Stop)。
func (e *Engine) StartInspectionScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	e.inspectCancel = cancel
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				e.runDueInspections(ctx)
			}
		}
	}()
	slog.Info("inspection scheduler started")
}

// runDueInspections 扫描并执行所有到期的启用中巡检(异步, 不阻塞调度循环)。
func (e *Engine) runDueInspections(ctx context.Context) {
	e.mu.RLock()
	var due []*Inspection
	now := time.Now()
	for _, ins := range e.inspections {
		if ins.Enabled && ins.NextRunAt.After(time.Time{}) && !ins.NextRunAt.After(now) {
			due = append(due, ins)
		}
	}
	e.mu.RUnlock()
	for _, ins := range due {
		go func(ins *Inspection) {
			if _, err := e.RunInspection(ctx, ins.ID, "scheduled"); err != nil {
				slog.Warn("scheduled inspection failed", "id", ins.ID, "err", err)
			}
		}(ins)
	}
}

// extractJSONFindings 从巡检报告正文中提取 ```json ... ``` 代码块内容(结构化发现)。
func extractJSONFindings(summary string) string {
	start := strings.Index(summary, "```json")
	if start < 0 {
		return ""
	}
	bodyStart := strings.Index(summary[start:], "\n")
	if bodyStart < 0 {
		return ""
	}
	bodyStart += start + 1
	end := strings.Index(summary[bodyStart:], "```")
	if end < 0 {
		return ""
	}
	raw := strings.TrimSpace(summary[bodyStart : bodyStart+end])
	// 校验确为合法 JSON 数组/对象
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return ""
	}
	return raw
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// agentIDOfInspection 返回巡检任务绑定的执行 Agent ID(供转单/记忆绑定)。
func (e *Engine) agentIDOfInspection(inspectionID string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if in, ok := e.inspections[inspectionID]; ok {
		return in.AgentID
	}
	return ""
}
