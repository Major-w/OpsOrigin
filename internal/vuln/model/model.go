package model

import "time"

// Severity 漏洞严重级别。
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Rank 用于 quick 扫描(仅取 rank>=3 的 critical/high)。
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// ScanStatus 扫描状态。
type ScanStatus string

const (
	ScanStatusRunning   ScanStatus = "running"
	ScanStatusCompleted ScanStatus = "completed"
	ScanStatusFailed    ScanStatus = "failed"
)

// ScanType 扫描类型。
type ScanType string

const (
	ScanTypeFull  ScanType = "full"
	ScanTypeQuick ScanType = "quick"
	ScanTypeCVE  ScanType = "cve"
	ScanTypeAgent ScanType = "agent"
)

// Scan 一次漏洞扫描记录。
type Scan struct {
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	Name          string            `json:"name"`
	Type          ScanType          `json:"type"`
	DemoMode      bool              `json:"demo_mode"`
	Status        ScanStatus        `json:"status"`
	SeverityCounts map[string]int  `json:"severity_counts"`
	TotalFindings int               `json:"total_findings"`
	CreatedAt     time.Time         `json:"created_at"`
	CompletedAt   *time.Time       `json:"completed_at,omitempty"`
}

// Finding 一条漏洞发现项。
type Finding struct {
	ID          string    `json:"id"`
	ScanID      string    `json:"scan_id"`
	TenantID    string    `json:"tenant_id"`
	CVE         string    `json:"cve"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Severity    Severity  `json:"severity"`
	Affected    string    `json:"affected_package"`
	Installed   string    `json:"installed_version"`
	Fixed       string    `json:"fixed_version"`
	Reference   string    `json:"reference"`
	Rank        int       `json:"rank"`
	CreatedAt   time.Time `json:"created_at"`
}

// VulnStats 聚合统计。
type VulnStats struct {
	TotalScans    int            `json:"total_scans"`
	TotalFindings int            `json:"total_findings"`
	BySeverity   map[string]int `json:"by_severity"`
	ByStatus     map[string]int `json:"by_status"`
}
