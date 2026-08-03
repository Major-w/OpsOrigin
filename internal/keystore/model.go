package keystore

import "time"

type KeyType string

const (
	KeyTypeSSHPrivate KeyType = "ssh_private"
	KeyTypeSSHPublic  KeyType = "ssh_public"
	KeyTypePassword   KeyType = "password"
	KeyTypeToken      KeyType = "token"
	KeyTypeDatabase   KeyType = "database"
)

type KeyEntry struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Type        KeyType           `json:"type"`
	Content     string            `json:"content"`
	Fingerprint string            `json:"fingerprint"`
	Size        int               `json:"size"`
	Algorithm   string            `json:"algorithm,omitempty"`
	Username    string            `json:"username,omitempty"`
	Host        string            `json:"host,omitempty"`
	Port        string            `json:"port,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}
