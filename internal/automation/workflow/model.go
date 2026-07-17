package workflow

import "time"

type StepType string

const (
	StepTypeScript    StepType = "script"
	StepTypeCondition StepType = "condition"
	StepTypeParallel  StepType = "parallel"
	StepTypeWait      StepType = "wait"
	StepTypeApproval  StepType = "approval"
)

type WorkflowStep struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Type          StepType       `json:"type"`
	ScriptID      string         `json:"script_id,omitempty"`
	Inputs        map[string]any `json:"inputs,omitempty"`
	Condition     string         `json:"condition,omitempty"`
	OnSuccess     string         `json:"on_success,omitempty"`
	OnFailure     string         `json:"on_failure,omitempty"`
	ParallelSteps []WorkflowStep `json:"parallel_steps,omitempty"`
	WaitSeconds   int            `json:"wait_seconds,omitempty"`
	Timeout       int            `json:"timeout,omitempty"`
	Approvers     []string       `json:"approvers,omitempty"`
	ApprovalMsg   string         `json:"approval_msg,omitempty"`
	Position      struct {
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
