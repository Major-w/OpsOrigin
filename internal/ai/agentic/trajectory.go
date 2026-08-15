package agentic

import (
	"context"
	"sync"
	"time"
)

// TrajectoryStep 记录一次 LLM 轮次(对应 DeepSeek Harness 的 step):
// 一次模型请求 + 其产生的工具调用。设计为只读快照, 记录后不可变。
type TrajectoryStep struct {
	Index       int              `json:"index"`
	Model       string           `json:"model"`
	RequestMsgs int              `json:"request_msgs"` // 本次请求发给 LLM 的消息条数
	Assistant   string           `json:"assistant,omitempty"`
	Reasoning   string           `json:"reasoning,omitempty"`
	ToolCalls   []ToolCallRecord `json:"tool_calls,omitempty"`
	Finished    bool             `json:"finished"` // true=本轮模型不再请求工具(产出最终答案)
}

// ToolCallRecord 单次工具调用的入参与结果(对应 Harness 的 tool/call + tool/result)。
type ToolCallRecord struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  bool           `json:"error,omitempty"` // true=工具执行返回错误
}

// TrajectoryTurn 一次用户请求触发的完整 agent 运行轨迹(turn), 由若干 step 组成。
// 采用 append-only 语义: 记录后不可变, 支持回放 / 分叉 / 复盘(参考 Harness 的追记式事件流)。
// 运维场景价值: 一次"自动重启把生产搞挂"可逐 step 追溯根因, 亦可喂给长期记忆做经验复用。
type TrajectoryTurn struct {
	AgentID        string           `json:"agent_id"`
	ConversationID string           `json:"conversation_id"`
	TenantID       string           `json:"tenant_id"`
	StartedAt      time.Time        `json:"started_at"`
	Prompt         string           `json:"prompt"`
	Steps          []TrajectoryStep `json:"steps"`
	FinalContent   string           `json:"final_content,omitempty"`
	Error          string           `json:"error,omitempty"`
}

// TrajectoryRecorder 轨迹记录器接口。内存实现用于本地/测试; 生产可接 PG(复用 ai_conversations 同库, 后续扩展)。
type TrajectoryRecorder interface {
	// RecordTurn 追加一次完整运行轨迹(按 ConversationID 归集, 顺序追加)。
	RecordTurn(ctx context.Context, t *TrajectoryTurn) error
	// GetTurns 按会话拉取全部轨迹(按发生顺序), 用于回放/复盘。
	GetTurns(ctx context.Context, conversationID string) ([]*TrajectoryTurn, error)
}

// MemTrajectoryRecorder 进程内轨迹存储。nil 安全: 未注入记录器时引擎不记录。
type MemTrajectoryRecorder struct {
	mu   sync.Mutex
	data map[string][]*TrajectoryTurn
}

// NewMemTrajectoryRecorder 构造内存轨迹记录器。
func NewMemTrajectoryRecorder() *MemTrajectoryRecorder {
	return &MemTrajectoryRecorder{data: make(map[string][]*TrajectoryTurn)}
}

// RecordTurn 追加一次轨迹; nil 入参或被忽略。
func (r *MemTrajectoryRecorder) RecordTurn(_ context.Context, t *TrajectoryTurn) error {
	if t == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[t.ConversationID] = append(r.data[t.ConversationID], t)
	return nil
}

// GetTurns 返回该会话的全部轨迹(副本, 避免外部修改内部状态)。
func (r *MemTrajectoryRecorder) GetTurns(_ context.Context, conversationID string) ([]*TrajectoryTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.data[conversationID]
	cp := make([]*TrajectoryTurn, len(out))
	copy(cp, out)
	return cp, nil
}
