package model

import "time"

type OutboxEventType string

const (
	EventCICreated       OutboxEventType = "ci.created"
	EventCIUpdated       OutboxEventType = "ci.updated"
	EventCIDeleted       OutboxEventType = "ci.deleted"
	EventRelationCreated OutboxEventType = "relation.created"
	EventRelationDeleted OutboxEventType = "relation.deleted"
)

type OutboxRecord struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	EventType   OutboxEventType `json:"event_type"`
	EntityType  string          `json:"entity_type"`
	EntityID    string          `json:"entity_id"`
	Payload     string          `json:"payload"`
	TraceID     string          `json:"trace_id"`
	CreatedAt   time.Time       `json:"created_at"`
	ProcessedAt *time.Time      `json:"processed_at,omitempty"`
}
