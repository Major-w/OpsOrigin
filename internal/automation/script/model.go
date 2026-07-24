package script

import "time"

type ScriptType string

const (
	ScriptTypeShell  ScriptType = "shell"
	ScriptTypePython ScriptType = "python"
	ScriptTypeBat    ScriptType = "bat"
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
	// 公共脚本 & 审批
	Public         bool   `json:"public"`                     // 是否标记为公共脚本（需审批）
	ApprovalStatus string `json:"approval_status,omitempty"`  // "draft"|"pending"|"approved"|"rejected"
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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
	// 审批快照
	Public         bool   `json:"public"`
	ApprovalStatus string `json:"approval_status,omitempty"`
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
	ID           string            `json:"id"`
	ScriptID     string            `json:"script_id"`
	TenantID     string            `json:"tenant_id"`
	Status       string            `json:"status"`
	Output       string            `json:"output"`
	Error        string            `json:"error,omitempty"`
	ExitCode     int               `json:"exit_code"`
	Duration     int64             `json:"duration_ms"`
	Params       map[string]string `json:"params,omitempty"`
	ParsedOutput map[string]any    `json:"parsed_output,omitempty"`
	RunAs        string            `json:"run_as,omitempty"`
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   *time.Time        `json:"finished_at,omitempty"`
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

// CheckRequest 语法检查请求
type CheckRequest struct {
	Type    ScriptType `json:"type"`
	Content string     `json:"content"`
}

// CheckResult 语法检查结果
type CheckResult struct {
	Valid  bool         `json:"valid"`
	Errors []CheckError `json:"errors"`
}

// CheckError 单条语法错误
type CheckError struct {
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}
