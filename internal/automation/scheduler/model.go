package scheduler

import "time"

type TaskType string

const (
	TaskTypeScript   TaskType = "script"
	TaskTypeWorkflow TaskType = "workflow"
)

type ScheduledTask struct {
	ID       string            `json:"id"`
	TenantID string            `json:"tenant_id"`
	Name     string            `json:"name"`
	CronExpr string            `json:"cron_expr"`
	TaskType TaskType          `json:"task_type"`
	TargetID string            `json:"target_id"`
	AgentIDs []string          `json:"agent_ids,omitempty"`
	Inputs   map[string]any    `json:"inputs,omitempty"`
	Enabled  bool              `json:"enabled"`
	Labels   map[string]string `json:"labels,omitempty"`
	// 重试配置
	MaxRetries     int        `json:"max_retries,omitempty"`      // 最大重试次数(0=不重试)
	RetryDelaySecs int        `json:"retry_delay_secs,omitempty"` // 重试间隔(秒,默认60)
	RetryCount     int        `json:"retry_count,omitempty"`      // 当前已重试次数(内部使用)
	LastError      string     `json:"last_error,omitempty"`       // 最近一次错误
	AlertOnFailure bool       `json:"alert_on_failure,omitempty"` // 失败时发送告警通知
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
