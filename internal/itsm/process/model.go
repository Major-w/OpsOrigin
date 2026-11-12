package process

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/itsm/form"
)

// StepType — BPMN 风格节点类型
type StepType string

const (
	StepStart    StepType = "start"    // 开始事件
	StepEnd      StepType = "end"      // 结束事件
	StepUser     StepType = "user"     // 人工任务（审批/执行）
	StepService  StepType = "service"  // 服务任务（自动执行脚本）
	StepGateway  StepType = "gateway"  // 排他网关（条件分支）
	StepTimer    StepType = "timer"    // 定时器（SLA超时触发）
	StepNotify   StepType = "notify"   // 通知任务
	StepParallel StepType = "parallel" // 并行网关（发散）
	StepJoin     StepType = "join"     // 汇聚网关（多入一出, 与 parallel 配对: 等待上游分支汇合后继续）
	StepDispatch StepType = "dispatch" // 派单节点（先派单再处理: 解析受派人 → 写工单 assignee → 继续流转）
	StepSubflow  StepType = "subflow"  // 子流程
	StepWait     StepType = "wait"     // 等待条件（Wait for Condition: 挂起轮询, 条件成立自动继续）
)

// 人工任务表决方式（对齐 ServiceNow Ask for Approval 的 5 种表决语义）。
const (
	ApprovalCountersign  = "countersign"   // 会签：全部审批人通过才继续（缺省，等价 all）
	ApprovalOrSign       = "orsign"        // 或签：任一审批人通过即可继续（等价 anyone）
	ApprovalRespondedAny = "responded_any" // 全员表态后任一通过：所有名册成员都投过票且至少一人通过
	ApprovalPercent      = "percent"       // 按比例：通过人数 ≥ 名册的 threshold%（默认 50）
	ApprovalCount        = "count"         // 按数量：通过人数 ≥ threshold 人（默认 1）
)

// voteLabel 表决语义的人类可读描述（dry-run 报告与画布标签共用）。
func voteLabel(t string, threshold float64) string {
	switch t {
	case ApprovalOrSign:
		return "或签（任一通过即继续）"
	case ApprovalRespondedAny:
		return "全员表态后任一通过即继续"
	case ApprovalPercent:
		if threshold <= 0 {
			threshold = 50
		}
		return fmt.Sprintf("比例表决（通过 ≥ %.0f%%）", threshold)
	case ApprovalCount:
		if threshold <= 0 {
			threshold = 1
		}
		return fmt.Sprintf("数量表决（通过 ≥ %.0f 人）", threshold)
	default:
		return "会签（全部通过才继续）"
	}
}

// DAGDependency  — 上游依赖
type Dependency struct {
	NodeID    string `json:"nodeId"`
	Condition string `json:"condition"` // success / failure / always
}

// DecisionBinding 流程级决策表资产绑定（流程引擎一等公民入口）。
//
// 运行期每次构建变量上下文时按已发布版本求值，输出写入 decision.<alias>.<输出列>
// 命名空间，供网关 Condition（如 decision.route.target == "L2"）、派单 AssigneeVar
// （decision.route.owner）与动作/脚本参数 ${decision.route.*} 直接引用，
// 不必再绕道工单自定义表单字段。
//
// Inputs 把决策表条件列映射到流程变量表达式（${...} 点路径，见 resolveRefValue）；
// 未显式映射的条件列按同名从 custom_fields / 变量上下文顶层取值。
type DecisionBinding struct {
	AssetID string            `json:"asset_id"`
	Version int               `json:"version,omitempty"` // 0 = 跟随最新已发布版本
	Alias   string            `json:"alias"`             // decision.<alias> 命名空间
	Inputs  map[string]string `json:"inputs,omitempty"`  // 条件列 key -> ${...} 变量表达式
}

// ActionTemplateRef 服务步骤对动作模板资产的引用。
// version=0 跟随最新已发布版本；>0 锁定到不可变快照。运行期由 ActionTemplateResolver
// 解析，模板为权威来源，Step.Inputs 仅作同名覆盖；解析失败 fail-closed 走 failure 分支。
type ActionTemplateRef struct {
	TemplateID string `json:"template_id"`
	Version    int    `json:"version,omitempty"`
}

// Step  — 流程步骤（前端 JSON 对齐 Web 版 WorkflowStep）
type Step struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        StepType `json:"type"`
	Description string   `json:"description,omitempty"`
	// 人工任务
	Approvers   []string `json:"approvers,omitempty"`
	ApprovalMsg string   `json:"approval_msg,omitempty"`
	// ApprovalType 多审批人时的表决方式：
	//   countersign(会签, 缺省) — 全部审批人通过才继续；
	//   orsign(或签)           — 任一审批人通过即可继续；
	//   responded_any          — 全员表态后任一通过即继续；
	//   percent                — 通过人数 ≥ 名册的 ApprovalThreshold%（默认 50）；
	//   count                  — 通过人数 ≥ ApprovalThreshold 人（默认 1）。
	ApprovalType string `json:"approval_type,omitempty"`
	// ApprovalThreshold percent/count 表决的阈值（percent: 0-100 的百分比；count: 人数）。
	// 对其他表决方式无意义；<=0 时按默认值（percent=50 / count=1）。
	ApprovalThreshold float64 `json:"approval_threshold,omitempty"`
	// ApproverField 从表单字段动态取审批人：非空时取工单自定义字段 <key> 的值追加到审批人集合，
	// 支持字符串(单人/逗号分隔)与字符串数组两种取值形态，便于「谁提单谁的主管审批」这类场景。
	ApproverField string `json:"approver_field,omitempty"`
	// 人工任务审批时间语义（对齐 ServiceNow Ask for Approval 的 due-date 语义）:
	//   TimeoutDuration  审批时限(如 24h / 3d, 复用 parseStepDuration 解析), 非空时启用超时策略;
	//   TimeoutAction    到期处置: auto_approve(自动通过) / auto_reject(自动驳回) / notify_only(仅催办);
	//   ReminderInterval 催办间隔(如 24h): 停留期间每过一个间隔向审批人催办一次, 空=不周期催办。
	// 超时处置由 StartTimerChecker 扫描循环驱动(见 timer.go checkUserStepTimeout),
	// auto_* 动作经 ApprovalTimeoutHandler 回调由 itsm 侧完成工单状态迁移与流程推进。
	TimeoutDuration  string `json:"timeout_duration,omitempty"`
	TimeoutAction    string `json:"timeout_action,omitempty"`
	ReminderInterval string `json:"reminder_interval,omitempty"`
	// 工作日历（P3-2）: 配置服务时间窗口名称/ID（servicehours.Window）后,
	// 催办间隔与超时时限按业务时间计算（跳过非工作时段与节假日, 如 8x5 日历下
	// "24h" 实际为 3 个工作日）; 留空=自然时间（向后兼容）。由 main 装配解析器。
	Calendar string `json:"calendar,omitempty"`
	// 服务任务
	ScriptID string         `json:"script_id,omitempty"`
	Inputs   map[string]any `json:"inputs,omitempty"`
	// 动作库: 非空时 service 步骤优先执行预置动作(action_params 为动作参数, 复用 Inputs 亦可)。
	// 支持动作: http(请求外发) / notify(通知分发) / create_ticket(创建 ITSM 工单) / script(脚本, 等价 ScriptID)。
	ActionType string `json:"action_type,omitempty"`
	// TemplateRef 非空时运行期以动作模板为准：模板提供 action_type + 默认 inputs，
	// 本步骤 Inputs 中同名键覆盖模板值，ActionType 字段被忽略。模板未发布/被删除/
	// 解析器未装配时 fail-closed 走 failure 分支，避免静默用空参数执行动作。
	TemplateRef *ActionTemplateRef `json:"template_ref,omitempty"`
	// 服务任务失败重试策略（对齐 SN REST Step 内置重试）:
	//   RetryCount    失败后最多重试次数（0=不重试, 上限 5）；
	//   RetryInterval 重试间隔(如 10s / 1m), 空则立即重试。仅对动作/脚本执行失败生效,
	//                 全部重试耗尽仍失败才走 failure 分支。
	RetryCount    int    `json:"retry_count,omitempty"`
	RetryInterval string `json:"retry_interval,omitempty"`
	// 网关
	Condition string `json:"condition,omitempty"`
	// wait 节点复用 Condition 字段承载等待条件表达式（与网关同一套 EvaluateCondition 求值）:
	// 变量上下文含 ticket.* / custom_fields.* / steps.*，如 `ticket.status == "resolved"`。
	// 引擎每轮扫描重评条件, 成立后沿 success 自动推进; 留空的条件永不满足(视为配置错误, 仅记录)。
	// 汇聚网关(join)模式:
	//   all(缺省) — 全部上游分支到达(或终结)后才放行;
	//   any       — 任一上游分支到达即放行(其余分支的到达记录保留仅作审计)。
	JoinMode string `json:"join_mode,omitempty"`
	// 派单节点(dispatch):
	//   Assignee   静态受派人, 支持 ${...} 变量占位(如 "${custom_fields.owner}")与逗号分隔多候选(取首个非空);
	//   AssigneeVar 受派人变量点路径(如 custom_fields.owner / steps.s1.output.assignee / ticket.created_by),
	//              非空时优先于 Assignee;
	//   DispatchMode auto(缺省)=引擎自动派单后沿 success 继续; manual=停在派单点等待人工指派
	//              (工单既有「指派」操作完成后由引擎续跑)。
	Assignee     string `json:"assignee,omitempty"`
	AssigneeVar  string `json:"assignee_var,omitempty"`
	DispatchMode string `json:"dispatch_mode,omitempty"`
	// 定时器
	Duration string `json:"duration,omitempty"` // 1h / 30m / 3d
	Deadline string `json:"deadline,omitempty"` // SLA 截止描述
	// OLA 节点时限升级对象: 定时器步骤到期(超时)时先发升级通知再自动推进, 空则不升级。
	OlaEscalateTo string `json:"ola_escalate_to,omitempty"`
	// 通知
	NotifyChannel string `json:"notify_channel,omitempty"`
	NotifyTitle   string `json:"notify_title,omitempty"`
	NotifyBody    string `json:"notify_body,omitempty"`
	// 子流程
	SubflowID string `json:"subflow_id,omitempty"`
	// SubflowParams 子流程显式传参（对标 SN Activity 的 input variables）:
	// key=参数名(子流程内以 params.<key> 引用, 如脚本 inputs 写 "${params.host}"),
	// value=变量表达式(如 "${custom_fields.owner}", 经父流程变量上下文 ${...} 替换)。
	// 注入为子流程执行上下文的 params 命名空间(叠加式, 父上下文其余变量仍可用, 向后兼容);
	// 求值发生在子流程每次启动/恢复时。未配置时保持旧行为(全量继承父上下文)。
	SubflowParams map[string]string `json:"subflow_params,omitempty"`
	// ForEach For Each 循环(对标 SN For Each Activity): 非空时对表达式求值结果逐项执行
	// 本子流程, 每项向子流程上下文注入 item(当前元素)与 item_index(下标)。
	// 表达式如 "${steps.query.output.items}" 或 "${custom_fields.hosts}"(逗号分隔串也归一化为列表);
	// 仅支持全自动子流程——子流程含人工/定时等待点时报错, 不做跨迭代暂停恢复。
	ForEach string `json:"for_each,omitempty"`
	// DAG 依赖（替代旧的 on_success/on_failure）
	DependsOn []Dependency `json:"depends_on,omitempty"`
	// 画布位置
	PositionX *float64 `json:"position_x,omitempty"`
	PositionY *float64 `json:"position_y,omitempty"`
	// 表单作用域：工单流转到本步骤时渲染的自定义表单 scope（如 process:<pid>:step:<step_id>）。
	// 空表示本步骤不挂动态表单。
	FormScope string `json:"form_scope,omitempty"`
	// FormFields 节点字段权限（StepFieldPerm）：声明「流程主表单的某字段在本节点的状态」
	// （hidden/readonly/disabled/editable，editable+required 为本节点必填）。
	// 未声明字段沿用主表单字段自身定义。steps 为 JSONB 整体存储，本字段随定义自动持久化。
	// 运行期与主表单 BehaviorRules 的叠加语义见 form.EffectiveStatesForStep（节点权限为上限）。
	FormFields []form.StepFieldPerm `json:"form_fields,omitempty"`
	// Actions 本节点允许展示的操作按钮 key（如 submit/approve/execute/complete/close...）。
	// 非空时工单在该节点只显示这些按钮；空/缺省表示沿用默认（全部显示），保持向后兼容。
	Actions []string `json:"actions,omitempty"`
	// Stage 阶段归属（阶段管理）：同一阶段名的节点聚合为一个阶段，运行期按
	// 定义中首次出现顺序推导阶段进度（见 stage.go StageProgress）；空表示不参与阶段视图。
	Stage string `json:"stage,omitempty"`
}

// ApproverRoster 返回本步骤最终生效的审批人名册：静态配置的 Approvers 与
// 由 ApproverField 从工单表单动态解析出的 extra 合并去重（保持先静态后动态的稳定次序）。
func (s *Step) ApproverRoster(extra []string) []string {
	seen := make(map[string]struct{}, len(s.Approvers)+len(extra))
	out := make([]string, 0, len(s.Approvers)+len(extra))
	for _, group := range [][]string{s.Approvers, extra} {
		for _, a := range group {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			if _, dup := seen[a]; dup {
				continue
			}
			seen[a] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}

// ApproversFromValue 把工单自定义字段的取值归一化为审批人列表。
// 兼容三种落库形态：字符串(可逗号分隔多人)、字符串数组、任意数组(逐个字符串化)。
func ApproversFromValue(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		parts := strings.Split(t, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}

// Process — ITSM 流程定义
type Process struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"` // 变更/事件/问题/请求
	Steps       []Step            `json:"steps"`
	Labels      map[string]string `json:"labels,omitempty"`
	Enabled     bool              `json:"enabled"`
	Version     int               `json:"version"`
	// Form 流程级主表单 Schema(工单创建时采集的自定义字段), JSON 结构对齐 internal/itsm/form.FormSchema。
	// 与画布中「人工任务」节点的 form_schema 互补: 前者是工单入口表单, 后者是流转到该步骤时的补充表单。
	Form json.RawMessage `json:"form,omitempty"`
	// DecisionBindings 流程级决策表资产绑定：运行期求值结果注入 decision.<alias>
	// 变量命名空间，供分支条件 / 派单 / 动作参数直接引用（与表单侧 custom_fields 链路并存）。
	DecisionBindings []DecisionBinding `json:"decision_bindings,omitempty"`
	// SlaPolicyID 关联 SLA 策略(id); 运行期工单启动时按 process_id 命中 GetByProcessID, 优先级高于风险级。
	SlaPolicyID string `json:"sla_policy_id,omitempty"`
	// NotifyConfig 流程级通知策略(JSON): { channels:[...], on_create:bool, on_complete:bool, template:string }。
	NotifyConfig json.RawMessage `json:"notify_config,omitempty"`
	// NumberRule 工单编号组成规则：流程创建的工单按此规则生成人类可读工单号。
	// 为空或未启用时回退全局默认 TK-YYYY-NNNN。
	NumberRule *TicketNumberRule `json:"number_rule,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// TicketNumberRule — 工单编号组成规则（流程设计"高级设置"可自定义）。
//
// 组成结构： [Prefix][Separator][DateSegment][Separator][Sequence]
//   - Prefix     固定前缀（业务标识），如变更="CHG"、事件="INC"、请求="REQ"
//   - DateFormat 日期片段：""(无) / "YYYY" / "YYYYMM" / "YYYYMMDD"
//   - Separator  分隔符，默认 "-"
//   - SeqWidth   序列最小宽度（不足左侧补零），默认 4
//   - SeqScope   序号重置粒度："year"(按年,默认) / "month"(按月) / "global"(全局)
//
// 序号统一走全局自增计数器（按租户 + 重置粒度维度），保证租户内唯一连续；
// 不同流程共享同一序号池（如 CHG-2026-0045、INC-2026-0046 交替）属正常行为。
type TicketNumberRule struct {
	Enabled    bool   `json:"enabled"`
	Prefix     string `json:"prefix"`
	DateFormat string `json:"date_format"`
	Separator  string `json:"separator"`
	SeqWidth   int    `json:"seq_width"`
	SeqScope   string `json:"seq_scope"`
}

// EffectiveSeparator 返回有效分隔符（空则默认 "-"）。
func (r *TicketNumberRule) EffectiveSeparator() string {
	if r == nil || r.Separator == "" {
		return "-"
	}
	return r.Separator
}

// EffectiveSeqWidth 返回有效序列宽度（<=0 则默认 4）。
func (r *TicketNumberRule) EffectiveSeqWidth() int {
	if r == nil || r.SeqWidth <= 0 {
		return 4
	}
	return r.SeqWidth
}

// ProcessInstance — 工作流运行实例（工单关联流程后自动创建）
type ProcessInstance struct {
	ID          string         `json:"id"`
	ProcessID   string         `json:"process_id"`
	TicketID    string         `json:"ticket_id"`
	TenantID    string         `json:"tenant_id"`
	CurrentStep string         `json:"current_step"`           // 当前步骤 ID
	Status      string         `json:"status"`                 // running / waiting / completed / terminated
	StepResults map[string]any `json:"step_results,omitempty"` // step_id → {status, output}
	// ProcessSnapshot 实例启动时的流程定义快照: 流程版本演进(更新定义)后,
	// 在途实例仍按创建时的定义推进(版本隔离)。空表示使用当前定义。
	ProcessSnapshot []Step     `json:"process_snapshot,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
