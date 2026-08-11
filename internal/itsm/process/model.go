package process

import (
	"encoding/json"
	"strings"
	"time"
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
	StepParallel StepType = "parallel" // 并行网关
	StepSubflow  StepType = "subflow"  // 子流程
)

// 人工任务表决方式。
const (
	ApprovalCountersign = "countersign" // 会签：全部审批人通过才继续（缺省）
	ApprovalOrSign      = "orsign"      // 或签：任一审批人通过即可继续
)

// DAGDependency  — 上游依赖
type Dependency struct {
	NodeID    string `json:"nodeId"`
	Condition string `json:"condition"` // success / failure / always
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
	//   orsign(或签)           — 任一审批人通过即可继续。
	ApprovalType string `json:"approval_type,omitempty"`
	// ApproverField 从表单字段动态取审批人：非空时取工单自定义字段 <key> 的值追加到审批人集合，
	// 支持字符串(单人/逗号分隔)与字符串数组两种取值形态，便于「谁提单谁的主管审批」这类场景。
	ApproverField string `json:"approver_field,omitempty"`
	// 服务任务
	ScriptID string         `json:"script_id,omitempty"`
	Inputs   map[string]any `json:"inputs,omitempty"`
	// 动作库: 非空时 service 步骤优先执行预置动作(action_params 为动作参数, 复用 Inputs 亦可)。
	// 支持动作: http(请求外发) / notify(通知分发) / create_ticket(创建 ITSM 工单) / script(脚本, 等价 ScriptID)。
	ActionType string `json:"action_type,omitempty"`
	// 网关
	Condition string `json:"condition,omitempty"`
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
	// DAG 依赖（替代旧的 on_success/on_failure）
	DependsOn []Dependency `json:"depends_on,omitempty"`
	// 画布位置
	PositionX *float64 `json:"position_x,omitempty"`
	PositionY *float64 `json:"position_y,omitempty"`
	// 表单作用域：工单流转到本步骤时渲染的自定义表单 scope（如 process:<pid>:step:<step_id>）。
	// 空表示本步骤不挂动态表单。
	FormScope string `json:"form_scope,omitempty"`
	// Actions 本节点允许展示的操作按钮 key（如 submit/approve/execute/complete/close...）。
	// 非空时工单在该节点只显示这些按钮；空/缺省表示沿用默认（全部显示），保持向后兼容。
	Actions []string `json:"actions,omitempty"`
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
	// SlaPolicyID 关联 SLA 策略(id); 运行期工单启动时按 process_id 命中 GetByProcessID, 优先级高于风险级。
	SlaPolicyID string `json:"sla_policy_id,omitempty"`
	// NotifyConfig 流程级通知策略(JSON): { channels:[...], on_create:bool, on_complete:bool, template:string }。
	NotifyConfig json.RawMessage `json:"notify_config,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
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
