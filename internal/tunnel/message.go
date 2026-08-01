package tunnel

import "encoding/json"

type MessageType string

const (
	MsgRegister      MessageType = "register"
	MsgRegisterResp  MessageType = "register_resp"
	MsgHeartbeat     MessageType = "heartbeat"
	MsgCommand       MessageType = "command"
	MsgCommandResp   MessageType = "command_resp"
	MsgAgentCommand  MessageType = "agent_command"
	MsgAgentResp     MessageType = "agent_resp"
	// MsgMetrics Agent→Proxy→Core 的指标/资产批量上报(经 Proxy 中转路径, §4.5)。
	MsgMetrics MessageType = "metrics"
)

type TunnelMessage struct {
	MessageID string          `json:"message_id"`
	TraceID   string          `json:"trace_id"`
	ProxyID   string          `json:"proxy_id"`
	Type      MessageType     `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type RegisterRequest struct {
	ProxyID      string   `json:"proxy_id"`
	Version      string   `json:"version"`
	Hostname     string   `json:"hostname"`
	Capabilities []string `json:"capabilities,omitempty"`
	Token        string   `json:"token,omitempty"`
	// boot_id 用于服务端克隆冲突检测,force_new 在 RESET 后重注册时置 true。
	BootID   string `json:"boot_id,omitempty"`
	ForceNew bool   `json:"force_new,omitempty"`
}

type RegisterResponse struct {
	Accepted    bool   `json:"accepted"`
	SessionID   string `json:"session_id"`
	Error       string `json:"error,omitempty"`
	// 克隆冲突:Server 要求 Agent 清空本地身份(追加随机盐)后以新 host_id 重注册。
	Reset       bool   `json:"reset,omitempty"`
	ResetReason string `json:"reset_reason,omitempty"`
}

type Heartbeat struct {
	Timestamp   int64 `json:"timestamp"`
	ActiveCount int32 `json:"active_count"`
}

type CommandRequest struct {
	CommandID     string `json:"command_id"`
	TargetID      string `json:"target_id"`
	Action        string `json:"action"`
	Payload       []byte `json:"payload,omitempty"`
	TimeoutSecond int32  `json:"timeout_seconds"`
}

type CommandResponse struct {
	CommandID string `json:"command_id"`
	Success   bool   `json:"success"`
	Output    []byte `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type AgentCommand struct {
	CommandID string `json:"command_id"`
	AgentID   string `json:"agent_id"`
	Action    string `json:"action"`
	Payload   []byte `json:"payload,omitempty"`
}

type AgentResponse struct {
	CommandID string `json:"command_id"`
	AgentID   string `json:"agent_id"`
	Success   bool   `json:"success"`
	Output    []byte `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

// TunnelMetricPoint Agent 上报的单个指标点(JSON 隧道 / Proxy 中转路径用)。
// 与 pb.MetricPoint 字段对齐, 由 Proxy 透传时转换。
type TunnelMetricPoint struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Unit   string            `json:"unit,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// MetricsMessage Agent→Proxy→Core 的指标/资产批量上报负载。
// 字段与 pb.MetricBatch 对齐: task_type 区分 cmdb(低频)/monitor(高频),
// 由 Proxy 透传为 gRPC MetricBatch, Core 侧据此分流存储。
type MetricsMessage struct {
	TaskID      string              `json:"task_id"`
	TaskType    string              `json:"task_type,omitempty"` // cmdb | monitor
	Timestamp   int64               `json:"timestamp,omitempty"` // unix ms
	IntervalSec int64               `json:"interval_sec,omitempty"`
	Points      []TunnelMetricPoint `json:"points"`
}

func MarshalMessage(msgType MessageType, payload any, proxyID, traceID string) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(TunnelMessage{
		Type:    msgType,
		Payload: p,
		ProxyID: proxyID,
		TraceID: traceID,
	})
}

func UnmarshalMessage(data []byte) (*TunnelMessage, error) {
	var msg TunnelMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
