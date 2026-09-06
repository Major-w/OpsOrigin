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

	// KnowledgeCategories 绑定的知识库分类列表(PRD 5.2.6)。
	// 非空时, 该 Agent 的 RAG 检索(系统提示注入与 knowledge_search 工具)仅在这些分类内进行;
	// 为空表示绑定全部知识库(默认行为, 兼容旧配置)。
	KnowledgeCategories []string `json:"knowledge_categories,omitempty"`

	// Tools 该 Agent 允许使用的 MCP 工具白名单(如 ["cmdb_query","knowledge_search"])。
	// 非空时, 未列入的工具对模型不可见(不可调用);
	// 为空表示「未关联任何工具」, 引擎不向模型暴露任何工具(避免无关联却误调用)。
	Tools []string `json:"tools,omitempty"`

	// Skills 该 Agent 具备的可复用能力定义(PRD 5.2.2)。
	// 每个 Skill 以"能力指令"形式注入 system prompt, 使模型了解并遵循该技能的使用方式。
	Skills []AgentSkill `json:"skills,omitempty"`
}

// AgentSkill 一个 Agent 的可复用能力定义: 名称 + 描述 + 执行指令。
// 渲染到 system prompt 后, 模型在相关场景下会遵循该指令完成特定工作。
type AgentSkill struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions,omitempty"` // 触发条件与执行步骤说明
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

	// EnableTools 允许该成员在作答时调用工具(Function Calling): 查询 CMDB / 检索知识库 /
	// 执行受管脚本等。为空时回退到团队级 AgentTeam.EnableTools。
	EnableTools bool `json:"enable_tools,omitempty"`
	// ModelName 该成员使用的模型覆盖; 非空时覆盖引擎/团队的默认模型(实现 per-member 模型路由)。
	ModelName string `json:"model_name,omitempty"`
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

	// —— 编排增强配置（均为可选, 缺省回退到引擎内置默认值）——

	// EnableTools 团队级开关: 成员是否可调用工具(Function Calling)。
	// 若某成员显式设置 TeamMember.EnableTools=true, 则不论此处如何都生效。
	EnableTools bool `json:"enable_tools,omitempty"`
	// MemberTimeoutSec 单个成员作答的硬超时(秒)。默认 90s, 防止某成员挂死拖垮整次对话。
	MemberTimeoutSec int `json:"member_timeout_sec,omitempty"`
	// MemberRetries 单个成员失败后的重试次数(指数退避)。默认 1 次。
	MemberRetries int `json:"member_retries,omitempty"`
	// DebateRounds 辩论模式(debate)下的往返轮数。默认 2 轮。
	DebateRounds int `json:"debate_rounds,omitempty"`
	// SupervisorVerify 是否启用主管审核回路: 成员产出后由主管(supervisor)审核,
	// 若判定需修订则指定某成员补充重做一轮, 再交汇总者定稿。默认关闭。
	SupervisorVerify bool `json:"supervisor_verify,omitempty"`
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
	Type      string `json:"type"` // agent_start | token | agent_end | final | error | tool_call | tool_result | supervisor_review
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Error     string `json:"error,omitempty"`

	// —— 增强事件附加上下文 ——
	Round      int    `json:"round,omitempty"`       // 辩论/多轮中的轮次(从 1 开始)
	ToolName   string `json:"tool_name,omitempty"`   // tool_call / tool_result 事件: 工具名
	ToolArgs   string `json:"tool_args,omitempty"`   // tool_call 事件: 入参 JSON
	ToolResult string `json:"tool_result,omitempty"` // tool_result 事件: 出参 JSON(已截断)
}
