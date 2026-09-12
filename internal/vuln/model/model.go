package model

import "time"

// Severity 漏洞严重级别。
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityUnknown  Severity = "unknown"
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
// 语义约定:completed 表示「扫描链路成功执行完毕」,与是否发现漏洞无关;
// 链路失败(二进制缺失/DB 不可用/解析失败/超时)一律 failed,绝不落成 completed 0 条,
// 否则无法区分「扫了但没漏洞」与「压根没扫成」(静默假阴性)。
type ScanStatus string

const (
	ScanStatusRunning   ScanStatus = "running"
	ScanStatusCompleted ScanStatus = "completed"
	ScanStatusFailed    ScanStatus = "failed"
	// ScanStatusPartial 部分目标失败:有可用结果,但覆盖率不完整,结论需打折。
	ScanStatusPartial ScanStatus = "partial"
)

// ScanType 扫描类型。
type ScanType string

const (
	ScanTypeFull  ScanType = "full"
	ScanTypeQuick ScanType = "quick"
	ScanTypeCVE   ScanType = "cve"
	ScanTypeAgent ScanType = "agent"
	ScanTypeImage ScanType = "image"
)

// Schedule 执行频率。
type Schedule string

const (
	ScheduleOnce   Schedule = "once"
	ScheduleDaily  Schedule = "daily"
	ScheduleWeekly Schedule = "weekly"
)

// Valid 校验频率取值。
func (s Schedule) Valid() bool {
	switch s {
	case ScheduleOnce, ScheduleDaily, ScheduleWeekly:
		return true
	}
	return false
}

// Interval 返回调度间隔;once 返回 0。
func (s Schedule) Interval() time.Duration {
	switch s {
	case ScheduleDaily:
		return 24 * time.Hour
	case ScheduleWeekly:
		return 7 * 24 * time.Hour
	}
	return 0
}

// FindingStatus 漏洞处置状态。
// 生命周期:open --(人工)--> fixed/ignored/false_positive;
// ignored/false_positive 带到期时间(ignore_until),到期后再次扫出会自动回到 open。
type FindingStatus string

const (
	FindingOpen          FindingStatus = "open"
	FindingFixed         FindingStatus = "fixed"
	FindingIgnored       FindingStatus = "ignored"
	FindingFalsePositive FindingStatus = "false_positive"
)

// Valid 校验处置状态取值。
func (s FindingStatus) Valid() bool {
	switch s {
	case FindingOpen, FindingFixed, FindingIgnored, FindingFalsePositive:
		return true
	}
	return false
}

// Scan 一次漏洞扫描记录。计划任务(每天/每周)复用同一条记录,只刷新统计与执行时间。
type Scan struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	Name           string         `json:"name"`
	Type           ScanType       `json:"type"`
	Schedule       Schedule       `json:"schedule"`
	DemoMode       bool           `json:"demo_mode"`
	Status         ScanStatus     `json:"status"`
	SeverityCounts map[string]int `json:"severity_counts"`
	TotalFindings  int            `json:"total_findings"`
	// Targets 本轮实际尝试扫描的目标数(主机/镜像);FailedTargets 其中失败数。
	// 二者用于区分「0 目标可扫」与「扫了 N 台全部干净」——前者结论不可用。
	Targets       int `json:"targets"`
	FailedTargets int `json:"failed_targets"`
	// ErrorDetail 失败原因摘要(截断),status=failed/partial 时给出可读诊断。
	ErrorDetail string `json:"error_detail,omitempty"`
	// AgentID 指定的执行 Agent(image 扫描必填;agent 扫描可留空表示全部已绑定 Agent 的资产)。
	AgentID string `json:"agent_id,omitempty"`
	// DBUpdatedAt 本轮 Agent 端漏洞库的实际更新时间(由脚本回传),空表示未更新(用本地库)。
	DBUpdatedAt *time.Time `json:"db_updated_at,omitempty"`
	// Options 创建时的执行参数(JSON),供调度器按原参数重跑。
	// 用 json:"-" 刻意不外泄 —— 其中可能含仓库凭据,绝不能出现在 API 响应里。
	Options     string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	NextRunAt   *time.Time `json:"next_run_at,omitempty"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Finding 一条漏洞发现项。
// 以 (tenant_id, dedupe_key) 为唯一实例:同一漏洞在多次扫描间复用同一行,
// first_seen 记首次发现,last_seen 记最近一次仍然存在,occurrences 累计命中次数。
type Finding struct {
	ID          string   `json:"id"`
	ScanID      string   `json:"scan_id"`
	TenantID    string   `json:"tenant_id"`
	CVE         string   `json:"cve"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Severity    Severity `json:"severity"`
	CVSS        float64  `json:"cvss"`
	Affected    string   `json:"affected_package"`
	Installed   string   `json:"installed_version"`
	Fixed       string   `json:"fixed_version"`
	Reference   string   `json:"reference"`
	Remediation string   `json:"remediation"`
	Rank        int      `json:"rank"`
	// Host 漏洞所在资产(主机名或镜像全引用);为空表示扫描器未回传资产归属。
	Host      string        `json:"host"`
	DedupeKey string        `json:"dedupe_key"`
	Status    FindingStatus `json:"status"`
	// Note 处置说明(人工填写,记录忽略/误报/修复的依据)。
	Note string `json:"note"`
	// IgnoreUntil 忽略到期时间(nil = 永久忽略,仅对 ignored/false_positive 有意义)。
	IgnoreUntil *time.Time `json:"ignore_until,omitempty"`
	Occurrences int        `json:"occurrences"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Suppressed 判断该发现项当前是否被抑制(忽略/误报仍在有效期内)。
// 被抑制的项在复扫时不会重新打开为 open,避免噪音反复。
func (f *Finding) Suppressed(now time.Time) bool {
	if f.Status != FindingIgnored && f.Status != FindingFalsePositive {
		return false
	}
	return f.IgnoreUntil == nil || f.IgnoreUntil.After(now)
}

// VulnStats 聚合统计。Open* 系列只统计仍未关闭(last_seen 之后未被自动闭环)的项。
type VulnStats struct {
	TotalScans      int            `json:"total_scans"`
	TotalFindings   int            `json:"total_findings"`
	OpenFindings    int            `json:"open_findings"`
	BySeverity      map[string]int `json:"by_severity"`
	ByStatus        map[string]int `json:"by_status"`
	ByFindingStatus map[string]int `json:"by_finding_status"`
	// OpenBySeverity 仅未关闭项的级别分布(看板主口径)。
	OpenBySeverity map[string]int `json:"open_by_severity"`
	// ScannedTargets/FailedTargets 最近一轮扫描的覆盖率,用于提示结论可信度。
	ScannedTargets int `json:"scanned_targets"`
	FailedTargets  int `json:"failed_targets"`
}
