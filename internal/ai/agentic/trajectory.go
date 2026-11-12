package agentic

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TrajectoryStep 记录一次 LLM 轮次(对应 DeepSeek Harness 的 step):
// 一次模型请求 + 其产生的工具调用。设计为只读快照, 记录后不可变。
//
// OTel 对齐(P0 治理层): 每个 step 是一个 span, 名字遵循 OTel GenAI 语义约定
// (gen_ai.chat / execute_tool.<name>); TraceID 在 turn 级生成, SpanID 在 step 级生成。
type TrajectoryStep struct {
	Index       int              `json:"index"`
	Model       string           `json:"model"`
	RequestMsgs int              `json:"request_msgs"` // 本次请求发给 LLM 的消息条数
	Assistant   string           `json:"assistant,omitempty"`
	Reasoning   string           `json:"reasoning,omitempty"`
	ToolCalls   []ToolCallRecord `json:"tool_calls,omitempty"`
	Finished    bool             `json:"finished"` // true=本轮模型不再请求工具(产出最终答案)

	SpanID        string         `json:"span_id,omitempty"`
	OperationName string         `json:"operation_name,omitempty"` // gen_ai.chat
	StartedAt     time.Time      `json:"started_at,omitempty"`
	DurationMs    int64          `json:"duration_ms,omitempty"`
	Status        string         `json:"status,omitempty"` // UNSET / OK / ERROR
	Attributes    map[string]any `json:"attributes,omitempty"`
}

// ToolCallRecord 单次工具调用的入参与结果(对应 OTel execute_tool.* span)。
type ToolCallRecord struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  bool           `json:"error,omitempty"` // true=工具执行返回错误

	SpanID     string         `json:"span_id,omitempty"`
	StartedAt  time.Time      `json:"started_at,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Status     string         `json:"status,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// TrajectoryTurn 一次用户请求触发的完整 agent 运行轨迹(turn), 由若干 step 组成。
// 采用 append-only 语义: 记录后不可变, 支持只读回放/复盘。
//
// 注: 可执行回放 / fork 需要全量 messages 上下文快照, 当前只记录 RequestMsgs 条数,
// 未实现(也不假装支持)。本次 P0 交付的是「可追溯的只读证据链」。
type TrajectoryTurn struct {
	ID             string           `json:"id,omitempty"`
	AgentID        string           `json:"agent_id"`
	ConversationID string           `json:"conversation_id"`
	TenantID       string           `json:"tenant_id"`
	UserID         string           `json:"user_id,omitempty"`
	TraceID        string           `json:"trace_id,omitempty"` // W3C Trace Context 32 hex
	RootSpanID     string           `json:"root_span_id,omitempty"`
	StartedAt      time.Time        `json:"started_at"`
	EndedAt        time.Time        `json:"ended_at,omitempty"`
	Prompt         string           `json:"prompt"`
	Steps          []TrajectoryStep `json:"steps"`
	FinalContent   string           `json:"final_content,omitempty"`
	Error          string           `json:"error,omitempty"`
	Attributes     map[string]any   `json:"attributes,omitempty"` // gen_ai.* 属性
}

// TrajectoryRecorder 轨迹记录器接口。
//
// P0 治理层: GetTurns 已从接口移除 —— 它只按 conversationID 取数、无 tenant 过滤,
// 直接接 API 会跨租户越权。生产端点一律走带 tenantID 的 ListTurns / GetTurn。
type TrajectoryRecorder interface {
	// RecordTurn 追加一次完整运行轨迹(按 ConversationID 归集, 顺序追加)。
	RecordTurn(ctx context.Context, t *TrajectoryTurn) error
	// ListTurns 按租户拉取轨迹; conversationID 为空时拉该租户全部。
	ListTurns(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]*TrajectoryTurn, error)
	// GetTurn 按租户 + turn ID 取单条轨迹; 跨租户一律返回 (nil, nil)。
	GetTurn(ctx context.Context, tenantID, turnID string) (*TrajectoryTurn, error)
}

// ——— 轨迹规范化 / 脱敏 / OTel 导出 ———

// sensitiveKeyParts 命中即整键脱敏, 避免把凭据写进审计表。
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "api_key", "apikey",
	"authorization", "credential", "private_key", "cookie", "access_key",
}

const maxTrajectoryText = 4096

func isSensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(lk, p) {
			return true
		}
	}
	return false
}

// redactValue 递归脱敏: 敏感键置为 [REDACTED], 长文本截断, 其余原样保留结构。
func redactValue(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > maxTrajectoryText {
			return x[:maxTrajectoryText] + "...(truncated)"
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if isSensitiveKey(k) {
				out[k] = "[REDACTED]"
			} else {
				out[k] = redactValue(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactValue(val)
		}
		return out
	default:
		return v
	}
}

func clipText(s string) string {
	b, _ := json.Marshal(redactValue(s))
	var out string
	_ = json.Unmarshal(b, &out)
	return out
}

// FinalizeTrajectory 在落盘前补全 OTel 标识、时间、属性并脱敏。
// 由 Engine.recordTrajectory 统一调用, 两条主循环无需各写一遍。
func FinalizeTrajectory(t *TrajectoryTurn) {
	if t == nil {
		return
	}
	now := time.Now().UTC()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.TraceID == "" {
		t.TraceID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if t.RootSpanID == "" {
		t.RootSpanID = strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	}
	if t.StartedAt.IsZero() {
		t.StartedAt = now
	}
	if t.EndedAt.IsZero() {
		t.EndedAt = now
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	status := "OK"
	if t.Error != "" {
		status = "ERROR"
	}
	if t.Attributes == nil {
		t.Attributes = map[string]any{}
	}
	t.Attributes["gen_ai.operation.name"] = "chat"
	t.Attributes["gen_ai.agent.id"] = t.AgentID
	t.Attributes["gen_ai.conversation.id"] = t.ConversationID
	t.Attributes["opsorigin.trace.status"] = status
	t.Prompt = clipText(t.Prompt)
	t.FinalContent = clipText(t.FinalContent)
	t.Error = clipText(t.Error)
	for i := range t.Steps {
		st := &t.Steps[i]
		if st.SpanID == "" {
			st.SpanID = strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
		}
		if st.OperationName == "" {
			st.OperationName = "gen_ai.chat"
		}
		if st.StartedAt.IsZero() {
			st.StartedAt = t.StartedAt
		}
		if st.Status == "" {
			st.Status = "UNSET"
		}
		if st.Attributes == nil {
			st.Attributes = map[string]any{}
		}
		st.Attributes["gen_ai.operation.name"] = "chat"
		st.Attributes["gen_ai.request.model"] = st.Model
		st.Attributes["gen_ai.usage.input_messages"] = st.RequestMsgs
		st.Assistant = clipText(st.Assistant)
		st.Reasoning = clipText(st.Reasoning)
		for j := range st.ToolCalls {
			tc := &st.ToolCalls[j]
			if tc.SpanID == "" {
				tc.SpanID = strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
			}
			if tc.Status == "" {
				tc.Status = "OK"
				if tc.Error {
					tc.Status = "ERROR"
				}
			}
			if tc.Attributes == nil {
				tc.Attributes = map[string]any{}
			}
			tc.Attributes["gen_ai.operation.name"] = "execute_tool"
			tc.Attributes["gen_ai.tool.name"] = tc.Name
			tc.Attributes["gen_ai.tool.call.id"] = tc.SpanID
			tc.Args, _ = redactValue(tc.Args).(map[string]any)
			tc.Result = redactValue(tc.Result)
		}
	}
}

// ToOTelJSON 把一条轨迹转成 OTel GenAI 语义约定的 span 集合(供导出/对接观测后端)。
// 结构: resourceSpans[].scopeSpans[].spans[], 字段名与 OTLP/JSON 一致。
func ToOTelJSON(t *TrajectoryTurn) map[string]any {
	if t == nil {
		return map[string]any{"resourceSpans": []any{}}
	}
	attrs := func(m map[string]any) []map[string]any {
		out := make([]map[string]any, 0, len(m))
		for k, v := range m {
			out = append(out, map[string]any{"key": k, "value": map[string]any{"stringValue": valueToString(v)}})
		}
		return out
	}
	rootAttrs := attrs(t.Attributes)
	nano := func(ts time.Time) int64 {
		if ts.IsZero() {
			return 0
		}
		return ts.UnixNano()
	}
	spans := []map[string]any{
		{
			"traceId":           t.TraceID,
			"spanId":            t.RootSpanID,
			"name":              "gen_ai.chat",
			"kind":              "SPAN_KIND_INTERNAL",
			"startTimeUnixNano": nano(t.StartedAt),
			"endTimeUnixNano":   nano(t.EndedAt),
			"attributes":        rootAttrs,
			"status":            map[string]any{"code": t.Attributes["opsorigin.trace.status"]},
		},
	}
	for _, st := range t.Steps {
		spans = append(spans, map[string]any{
			"traceId":           t.TraceID,
			"spanId":            st.SpanID,
			"parentSpanId":      t.RootSpanID,
			"name":              st.OperationName,
			"kind":              "SPAN_KIND_CLIENT",
			"startTimeUnixNano": nano(st.StartedAt),
			"endTimeUnixNano":   nano(st.StartedAt.Add(time.Duration(st.DurationMs) * time.Millisecond)),
			"attributes":        attrs(st.Attributes),
			"status":            map[string]any{"code": st.Status},
		})
		for _, tc := range st.ToolCalls {
			spans = append(spans, map[string]any{
				"traceId":           t.TraceID,
				"spanId":            tc.SpanID,
				"parentSpanId":      st.SpanID,
				"name":              "execute_tool." + tc.Name,
				"kind":              "SPAN_KIND_CLIENT",
				"startTimeUnixNano": nano(tc.StartedAt),
				"endTimeUnixNano":   nano(tc.StartedAt.Add(time.Duration(tc.DurationMs) * time.Millisecond)),
				"attributes":        attrs(tc.Attributes),
				"status":            map[string]any{"code": tc.Status},
			})
		}
	}
	return map[string]any{
		"resourceSpans": []any{map[string]any{
			"resource": map[string]any{"attributes": []map[string]any{
				{"key": "service.name", "value": map[string]any{"stringValue": "opsorigin.ai"}},
				{"key": "tenant.id", "value": map[string]any{"stringValue": t.TenantID}},
			}},
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "opsorigin.ai.agent", "version": "1.0.0"},
				"spans": spans,
			}},
		}},
	}
}

func valueToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// ——— 内存实现(本地/测试) ———

// MemTrajectoryRecorder 进程内轨迹存储。nil 安全: 未注入记录器时引擎不记录。
type MemTrajectoryRecorder struct {
	mu   sync.Mutex
	data map[string][]*TrajectoryTurn
}

// NewMemTrajectoryRecorder 构造内存轨迹记录器。
func NewMemTrajectoryRecorder() *MemTrajectoryRecorder {
	return &MemTrajectoryRecorder{data: make(map[string][]*TrajectoryTurn)}
}

// RecordTurn 追加一次轨迹; nil 入参被忽略。
func (r *MemTrajectoryRecorder) RecordTurn(_ context.Context, t *TrajectoryTurn) error {
	if t == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[t.ConversationID] = append(r.data[t.ConversationID], t)
	return nil
}

// GetTurns 仅用于本地/测试: 按会话返回轨迹副本。
// 生产端点禁止使用(无 tenant 过滤), 必须走 ListTurns / GetTurn。
func (r *MemTrajectoryRecorder) GetTurns(_ context.Context, conversationID string) ([]*TrajectoryTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.data[conversationID]
	cp := make([]*TrajectoryTurn, len(out))
	copy(cp, out)
	return cp, nil
}

// ListTurns 按租户过滤; conversationID 非空时再按会话收敛。
func (r *MemTrajectoryRecorder) ListTurns(_ context.Context, tenantID, conversationID string, limit, offset int) ([]*TrajectoryTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*TrajectoryTurn, 0)
	for _, turns := range r.data {
		for _, t := range turns {
			if t.TenantID != tenantID {
				continue
			}
			if conversationID != "" && t.ConversationID != conversationID {
				continue
			}
			all = append(all, t)
		}
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(all) {
		return []*TrajectoryTurn{}, nil
	}
	all = all[offset:]
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]*TrajectoryTurn, len(all))
	copy(out, all)
	return out, nil
}

// GetTurn 按租户 + ID 取单条; 跨租户返回 (nil, nil)。
func (r *MemTrajectoryRecorder) GetTurn(_ context.Context, tenantID, turnID string) (*TrajectoryTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, turns := range r.data {
		for _, t := range turns {
			if t.ID == turnID && t.TenantID == tenantID {
				return t, nil
			}
		}
	}
	return nil, nil
}
