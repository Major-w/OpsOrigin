package workflow

import "time"

type StepType string

const (
	StepTypeScript    StepType = "script"
	StepTypeCondition StepType = "condition"
	StepTypeParallel  StepType = "parallel"
	StepTypeWait      StepType = "wait"
	StepTypeApproval  StepType = "approval"
	// 与前端画布对齐的扩展节点类型（DAG 模型）。执行器对未特化处理的类型走
	// 直通语义（沿成功边推进），确保 UI 建的工作流不再在首节点静默终止。
	StepTypeStart    StepType = "start"
	StepTypeEnd      StepType = "end"
	StepTypeNotify   StepType = "notify"
	StepTypeSubflow  StepType = "subflow"
	StepTypeLoop     StepType = "loop"
	StepTypeWebhook  StepType = "webhook"
	StepTypeComment  StepType = "comment"
	StepTypeFiledist StepType = "filedist"
)

// DAGDependency — 上游依赖边（前端画布模型）。表示"当 NodeID 以 Condition 结束时进入本节点"。
type DAGDependency struct {
	NodeID    string `json:"nodeId"`
	Condition string `json:"condition"` // success / failure / always
}

type WorkflowStep struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      StepType       `json:"type"`
	ScriptID  string         `json:"script_id,omitempty"`
	Inputs    map[string]any `json:"inputs,omitempty"`
	Condition string         `json:"condition,omitempty"`
	OnSuccess string         `json:"on_success,omitempty"`
	OnFailure string         `json:"on_failure,omitempty"`
	// DAG 依赖边（前端画布用 depends_on 表达连线，替代 on_success/on_failure）。
	// 保存时保留原样，执行前经 compileDAG 编译为 on_success/on_failure 供执行器消费。
	DependsOn     []DAGDependency `json:"depends_on,omitempty"`
	ParallelSteps []WorkflowStep  `json:"parallel_steps,omitempty"`
	WaitSeconds   int             `json:"wait_seconds,omitempty"`
	Timeout       int             `json:"timeout,omitempty"`
	Approvers     []string        `json:"approvers,omitempty"`
	ApprovalMsg   string          `json:"approval_msg,omitempty"`
	// 开始/结束
	EndStatus string `json:"end_status,omitempty"` // success/failure/timeout
	// 通知
	NotifyChannel string `json:"notify_channel,omitempty"`
	NotifyTitle   string `json:"notify_title,omitempty"`
	NotifyBody    string `json:"notify_body,omitempty"`
	// 子流程
	SubflowID   string `json:"subflow_id,omitempty"`
	SubflowName string `json:"subflow_name,omitempty"`
	// 循环
	LoopOver string `json:"loop_over,omitempty"`
	LoopMax  int    `json:"loop_max,omitempty"`
	// Webhook
	WebhookURL     string            `json:"webhook_url,omitempty"`
	WebhookMethod  string            `json:"webhook_method,omitempty"`
	WebhookHeaders map[string]string `json:"webhook_headers,omitempty"`
	WebhookBody    string            `json:"webhook_body,omitempty"`
	// 注释
	CommentText string `json:"comment_text,omitempty"`
	// 文件分发
	FiledistFileID      string   `json:"filedist_file_id,omitempty"`
	FiledistFileName    string   `json:"filedist_file_name,omitempty"`
	FiledistTargets     []string `json:"filedist_targets,omitempty"`
	FiledistDownloadDir string   `json:"filedist_download_dir,omitempty"`
	Position            struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position,omitempty"`
}

type Workflow struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Steps       []WorkflowStep    `json:"steps"`
	InputParams []ParamDef        `json:"input_params"`
	Labels      map[string]string `json:"labels,omitempty"`
	Timeout     int               `json:"timeout,omitempty"` // 工作流全局超时(秒),0=无限制
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type ParamDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

type ExecutionStatus string

const (
	ExecStatusPending   ExecutionStatus = "pending"
	ExecStatusRunning   ExecutionStatus = "running"
	ExecStatusSucceeded ExecutionStatus = "succeeded"
	ExecStatusFailed    ExecutionStatus = "failed"
	ExecStatusCancelled ExecutionStatus = "cancelled"
	ExecStatusWaiting   ExecutionStatus = "waiting"
)

type WorkflowExecution struct {
	ID            string                `json:"id"`
	WorkflowID    string                `json:"workflow_id"`
	TenantID      string                `json:"tenant_id"`
	Status        ExecutionStatus       `json:"status"`
	AgentIDs      []string              `json:"agent_ids"`
	Inputs        map[string]any        `json:"inputs,omitempty"`
	StepResults   map[string]StepResult `json:"step_results,omitempty"`
	StartedAt     *time.Time            `json:"started_at,omitempty"`
	FinishedAt    *time.Time            `json:"finished_at,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	PendingStepID string                `json:"pending_step_id,omitempty"`
	Context       map[string]any        `json:"-"`
}

type StepResult struct {
	StepID   string          `json:"step_id"`
	StepName string          `json:"step_name"`
	Status   ExecutionStatus `json:"status"`
	Output   map[string]any  `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	Duration int64           `json:"duration_ms"`
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

type StepApproval struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	ExecutionID string         `json:"execution_id"`
	StepID      string         `json:"step_id"`
	StepName    string         `json:"step_name"`
	Approvers   []string       `json:"approvers"`
	Message     string         `json:"message"`
	Status      ApprovalStatus `json:"status"`
	DecidedBy   string         `json:"decided_by,omitempty"`
	Comment     string         `json:"comment,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	DecidedAt   *time.Time     `json:"decided_at,omitempty"`
}
