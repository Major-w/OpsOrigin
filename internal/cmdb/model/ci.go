package model

import "time"

type CIStatus string

const (
	CIStatusActive   CIStatus = "active"
	CIStatusInactive CIStatus = "inactive"
	CIStatusDeleted  CIStatus = "deleted"
)

type ChannelWeight int

const (
	WeightHuman        ChannelWeight = 100
	WeightITSM         ChannelWeight = 80
	WeightAutoScript   ChannelWeight = 50
	WeightDiscovery    ChannelWeight = 30
)

type CIModelType struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	ParentID    string            `json:"parent_id,omitempty"`
	Schema      map[string]AttrDef `json:"schema"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type AttrDef struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
}

type CIModel struct {
	ID          string            `json:"id"`
	TypeID      string            `json:"type_id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Status      CIStatus          `json:"status"`
	Attributes  map[string]any    `json:"attributes"`
	Labels      map[string]string `json:"labels,omitempty"`
	Channel     ChannelWeight     `json:"channel"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type CIRelation struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Properties   map[string]any `json:"properties,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type AttributeChange struct {
	CIID        string        `json:"ci_id"`
	AttrName    string        `json:"attr_name"`
	OldValue    any           `json:"old_value"`
	NewValue    any           `json:"new_value"`
	Channel     ChannelWeight `json:"channel"`
	ChannelDesc string        `json:"channel_desc"`
	ChangedAt   time.Time     `json:"changed_at"`
}
