package script

import "time"

type ScriptType string

const (
	ScriptTypeShell   ScriptType = "shell"
	ScriptTypePython  ScriptType = "python"
	ScriptTypeAnsible ScriptType = "ansible"
	ScriptTypeBat     ScriptType = "bat"
)

type ParamDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

// Script 脚本定义(当前版本)
type Script struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Type        ScriptType        `json:"type"`
	Content     string            `json:"content"`
	Description string            `json:"description,omitempty"`
	InputParams []ParamDef        `json:"input_params"`
	OutputParams []ParamDef       `json:"output_params"`
	Timeout     int               `json:"timeout"`
	RunAs       string            `json:"run_as,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Version     int               `json:"version"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ScriptVersion 脚本历史版本快照
type ScriptVersion struct {
	ID         string    `json:"id"`
	ScriptID   string    `json:"script_id"`
	TenantID   string    `json:"tenant_id"`
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	Type       ScriptType `json:"type"`
	Content    string    `json:"content"`
	InputParams []ParamDef `json:"input_params"`
	OutputParams []ParamDef `json:"output_params"`
	Timeout    int       `json:"timeout"`
	RunAs      string    `json:"run_as,omitempty"`
	ChangeNote string    `json:"change_note,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ExecuteRequest 脚本执行请求
type ExecuteRequest struct {
	AgentID  string            `json:"agent_id,omitempty"`
	TenantID string            `json:"tenant_id,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Timeout  int               `json:"timeout,omitempty"`
	RunAs    string            `json:"run_as,omitempty"`
}

// ExecutionResult 脚本执行结果
type ExecutionResult struct {
	ID         string    `json:"id"`
	ScriptID   string    `json:"script_id"`
	TenantID   string    `json:"tenant_id"`
	Status     string    `json:"status"` // running / success / failed / timeout
	Output     string    `json:"output"`
	Error      string    `json:"error,omitempty"`
	ExitCode   int       `json:"exit_code"`
	Duration   int64     `json:"duration_ms"`
	Params     map[string]string `json:"params,omitempty"`
	RunAs      string    `json:"run_as,omitempty"`
	StartedAt time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// DiffResult 版本比对结果
type DiffResult struct {
	FromVersion int      `json:"from_version"`
	ToVersion   int      `json:"to_version"`
	Changes     []DiffEntry `json:"changes"`
}

type DiffEntry struct {
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}
