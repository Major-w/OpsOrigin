package model

import "time"

type AgentInstance struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Name         string    `json:"name"`
	ModelName    string    `json:"model_name"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ExecutionPlan struct {
	ID          string    `json:"id"`
	TicketID    string    `json:"ticket_id"`
	AgentID     string    `json:"agent_id"`
	TenantID    string    `json:"tenant_id"`
	Steps       []Step    `json:"steps"`
	RiskLevel   string    `json:"risk_level"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Step struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Input     any    `json:"input"`
	Output    any    `json:"output,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

type ToolCall struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	ToolName    string    `json:"tool_name"`
	Input       any       `json:"input"`
	Output      any       `json:"output,omitempty"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	Elapsed     time.Duration `json:"elapsed_ms"`
	Timestamp   time.Time `json:"timestamp"`
}

type ThinkLog struct {
	AgentID     string    `json:"agent_id"`
	TraceID     string    `json:"trace_id"`
	Reasoning   string    `json:"reasoning"`
	TokenUsage  TokenUsage `json:"token_usage"`
}

type TokenUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

// TokenRecord 单次调用的 token 消耗记录。
type TokenRecord struct {
	Time       time.Time `json:"time"`
	AgentID    string    `json:"agent_id,omitempty"`
	Model      string    `json:"model"`
	Prompt     int       `json:"prompt_tokens"`
	Completion int       `json:"completion_tokens"`
	Total      int       `json:"total_tokens"`
}

// TokenStats 聚合统计。
type TokenStats struct {
	Period    string        `json:"period"`
	TotalCalls int          `json:"total_calls"`
	Prompt     int          `json:"prompt_tokens"`
	Completion int          `json:"completion_tokens"`
	Total      int          `json:"total_tokens"`
	ByHour     []HourBucket `json:"by_hour,omitempty"`
	ByDay      []DayBucket  `json:"by_day,omitempty"`
}

type HourBucket struct {
	Hour  string `json:"hour"`
	Total int    `json:"tokens"`
	Calls int    `json:"calls"`
}

type DayBucket struct {
	Date  string `json:"date"`
	Total int    `json:"tokens"`
	Calls int    `json:"calls"`
}
