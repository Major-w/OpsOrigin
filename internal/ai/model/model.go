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
	ID        string    `json:"id"`
	TicketID  string    `json:"ticket_id"`
	AgentID   string    `json:"agent_id"`
	TenantID  string    `json:"tenant_id"`
	Steps     []Step    `json:"steps"`
	RiskLevel string    `json:"risk_level"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Step struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Input  any    `json:"input"`
	Output any    `json:"output,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
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
	Period     string       `json:"period"`
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

// ---- 多智能体团队（Feature 1）----

// TeamMode 团队编排模式。
const (
	TeamModeSupervisor = "supervisor" // 主管拆解任务、汇总者定稿
	TeamModeParallel   = "parallel"   // 成员并行作答，汇总者定稿
	TeamModeSequential = "sequential" // 成员顺序接力作答
	TeamModeDebate     = "debate"     // 成员两两辩论，汇总者裁定
)

// TeamRole 团队成员角色。
const (
	TeamRoleSupervisor = "supervisor" // 主管：负责拆解 / 调度
	TeamRoleMember     = "member"     // 普通成员：执行子任务
	TeamRoleFinalizer  = "finalizer"  // 汇总者：整合成员产出，给出最终答复
)

// TeamMember 团队中的一名成员（绑定一个底层 Agent 实例）。
type TeamMember struct {
	AgentID      string `json:"agent_id"`
	AgentName    string `json:"agent_name"`
	Role         string `json:"role"`         // supervisor / member / finalizer
	CanFinalize  bool   `json:"can_finalize"` // 该成员是否拥有定稿权
	Weight       int    `json:"weight,omitempty"`
	Persona      string `json:"persona,omitempty"`       // 角色画像（如「严谨的架构师」）
	SystemPrompt string `json:"system_prompt,omitempty"` // 成员级覆盖提示词
}

// AgentTeam 一个多智能体团队定义。
type AgentTeam struct {
	ID           string       `json:"id"`
	TenantID     string       `json:"tenant_id"`
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Mode         string       `json:"mode"` // supervisor / parallel / sequential / debate
	Members      []TeamMember `json:"members"`
	SystemPrompt string       `json:"system_prompt,omitempty"` // 团队级 persona 前缀
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// TeamChatRequest 团队对话请求。
type TeamChatRequest struct {
	TeamID    string `json:"team_id"`
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
	Stream    bool   `json:"stream,omitempty"`
}

// TeamChatResponse 团队对话（非流式）的响应。
type TeamChatResponse struct {
	SessionID    string   `json:"session_id,omitempty"`
	FinalAnswer  string   `json:"final_answer"`
	InterActions []string `json:"interactions,omitempty"` // 各成员产出片段，便于审计
}

// TeamStreamEvent SSE 流式事件。
type TeamStreamEvent struct {
	Type      string `json:"type"` // agent_start | token | agent_end | final | error
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Error     string `json:"error,omitempty"`
}
