package model

import (
	"encoding/json"
	"time"
)

// CIVersion represents a full snapshot of a CI at a point in time.
type CIVersion struct {
	ID         string         `json:"id"`
	CIID       string         `json:"ci_id"`
	TenantID   string         `json:"tenant_id"`
	Version    int            `json:"version"`
	Name       string         `json:"name"`
	TypeID     string         `json:"type_id"`
	Status     string         `json:"status"`
	Attributes map[string]any `json:"attributes"`
	Labels     map[string]string `json:"labels"`
	Operator   string         `json:"operator"`
	ChangeType string         `json:"change_type"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (v *CIVersion) MarshalAttributes() ([]byte, error) {
	return json.Marshal(v.Attributes)
}

func (v *CIVersion) MarshalLabels() ([]byte, error) {
	return json.Marshal(v.Labels)
}

func (v *CIVersion) UnmarshalAttributes(data []byte) error {
	return json.Unmarshal(data, &v.Attributes)
}

func (v *CIVersion) UnmarshalLabels(data []byte) error {
	return json.Unmarshal(data, &v.Labels)
}

// DiscoveryTask represents an auto-discovery task configuration.
type DiscoveryTask struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenant_id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Target     string         `json:"target"`
	Status     string         `json:"status"`
	Config     map[string]any `json:"config"`
	FoundCount int            `json:"found_count"`
	LastRunAt  *time.Time     `json:"last_run_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

func (t *DiscoveryTask) MarshalConfig() ([]byte, error) {
	return json.Marshal(t.Config)
}

func (t *DiscoveryTask) UnmarshalConfig(data []byte) error {
	return json.Unmarshal(data, &t.Config)
}

// DiscoveryResult represents a single CI discovered by a task.
type DiscoveryResult struct {
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	TenantID      string         `json:"tenant_id"`
	CIName        string         `json:"ci_name"`
	CIType        string         `json:"ci_type"`
	IPAddress     string         `json:"ip_address,omitempty"`
	Attributes    map[string]any `json:"attributes"`
	Source        string         `json:"source"`
	Synced        bool           `json:"synced"`
	SyncedCIID    string         `json:"synced_ci_id,omitempty"`
	DiscoveredAt  time.Time      `json:"discovered_at"`
}

func (r *DiscoveryResult) MarshalAttributes() ([]byte, error) {
	return json.Marshal(r.Attributes)
}

func (r *DiscoveryResult) UnmarshalAttributes(data []byte) error {
	return json.Unmarshal(data, &r.Attributes)
}
