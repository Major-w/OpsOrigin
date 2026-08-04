package process

import "time"

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
	// 服务任务
	ScriptID string         `json:"script_id,omitempty"`
	Inputs   map[string]any `json:"inputs,omitempty"`
	// 网关
	Condition string `json:"condition,omitempty"`
	// 定时器
	Duration string `json:"duration,omitempty"` // 1h / 30m / 3d
	Deadline string `json:"deadline,omitempty"` // SLA 截止描述
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
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
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
