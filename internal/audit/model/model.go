package model

import "time"

type AuditEvent struct {
	ID        string      `json:"id"`
	TraceID   string      `json:"trace_id"`
	TenantID  string      `json:"tenant_id"`
	EventType string      `json:"event_type"`
	Actor     AuditActor  `json:"actor"`
	Target    AuditTarget `json:"target"`
	Action    string      `json:"action"`
	Request   any         `json:"request,omitempty"`
	Response  any         `json:"response,omitempty"`
	Success   bool        `json:"success"`
	Error     string      `json:"error,omitempty"`
	IPAddress string      `json:"ip_address"`
	UserAgent string      `json:"user_agent,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

type AuditActor struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	TicketID    string `json:"ticket_id,omitempty"`
	TokenHash   string `json:"token_hash,omitempty"`
}

type AuditTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type AuditFilter struct {
	TenantID   string    `json:"tenant_id,omitempty"`
	TraceID    string    `json:"trace_id,omitempty"`
	EventType  string    `json:"event_type,omitempty"`
	ActorType  string    `json:"actor_type,omitempty"`
	ActorID    string    `json:"actor_id,omitempty"` // actor_id 模糊搜索(操作主体)
	TargetType string    `json:"target_type,omitempty"`
	Keyword    string    `json:"keyword,omitempty"` // action/target_name/trace_id 全局模糊搜索
	Success    *bool     `json:"success,omitempty"` // nil=不过滤
	From       time.Time `json:"from,omitempty"`
	To         time.Time `json:"to,omitempty"`
	Limit      int       `json:"limit,omitempty"`
	Offset     int       `json:"offset,omitempty"`
}
