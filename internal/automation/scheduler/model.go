package scheduler

import "time"

type TaskType string

const (
	TaskTypeScript   TaskType = "script"
	TaskTypeWorkflow TaskType = "workflow"
)

type ScheduledTask struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	CronExpr    string            `json:"cron_expr"`
	TaskType    TaskType          `json:"task_type"`
	TargetID    string            `json:"target_id"`
	AgentIDs    []string          `json:"agent_ids,omitempty"`
	Inputs      map[string]any    `json:"inputs,omitempty"`
	Enabled     bool              `json:"enabled"`
	Labels      map[string]string `json:"labels,omitempty"`
	LastRunAt   *time.Time        `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time        `json:"next_run_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}