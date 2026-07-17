package types

type IdentityType string

const (
	IdentityHuman   IdentityType = "HUMAN"
	IdentityAIAgent IdentityType = "AI_AGENT"
)

type Identity struct {
	Type     IdentityType `json:"type"`
	ID       string       `json:"id"`
	TenantID string       `json:"tenant_id"`
	TraceID  string       `json:"trace_id"`
}
