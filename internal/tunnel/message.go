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
}

type RegisterResponse struct {
	Accepted  bool   `json:"accepted"`
	SessionID string `json:"session_id"`
	Error     string `json:"error,omitempty"`
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
