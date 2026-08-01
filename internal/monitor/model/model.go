package model

import "time"

type TargetStatus string

const (
	TargetUp   TargetStatus = "up"
	TargetDown TargetStatus = "down"
	TargetUnknown TargetStatus = "unknown"
)

type ProbeProtocol string

const (
	ProbeICMP   ProbeProtocol = "icmp"
	ProbeTCP    ProbeProtocol = "tcp"
	ProbeUDP    ProbeProtocol = "udp"
	ProbeHTTP   ProbeProtocol = "http"
	ProbeDNS    ProbeProtocol = "dns"
	ProbeSSL    ProbeProtocol = "ssl"
)

type MonitorTarget struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	CIID        string            `json:"ci_id"`
	Name        string            `json:"name"`
	Address     string            `json:"address"`
	Port        int               `json:"port,omitempty"`
	Probes      []ProbeProtocol   `json:"probes"`
	Interval    time.Duration     `json:"interval"`
	Timeout     time.Duration     `json:"timeout"`
	Labels      map[string]string `json:"labels,omitempty"`
	Status      TargetStatus      `json:"status"`
	LastChecked *time.Time        `json:"last_checked,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

type ProbeTask struct {
	ID        string         `json:"id"`
	TargetID  string         `json:"target_id"`
	TenantID  string         `json:"tenant_id"`
	Protocol  ProbeProtocol  `json:"protocol"`
	Address   string         `json:"address"`
	Port      int            `json:"port,omitempty"`
	Timeout   time.Duration  `json:"timeout"`
	Result    *ProbeResult   `json:"result,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type ProbeResult struct {
	Success      bool          `json:"success"`
	Latency      time.Duration `json:"latency_ms"`
	StatusCode   int           `json:"status_code,omitempty"`
	ErrorMessage string        `json:"error_message,omitempty"`
	CheckedAt    time.Time     `json:"checked_at"`
}

type AlertSeverity string

const (
	AlertInfo     AlertSeverity = "info"
	AlertWarning  AlertSeverity = "warning"
	AlertCritical AlertSeverity = "critical"
)

type AlertEvent struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	TargetID    string            `json:"target_id"`
	SourceCIID  string            `json:"source_ci_id"`
	RuleName    string            `json:"rule_name"`
	Severity    AlertSeverity     `json:"severity"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	IsSuppressed bool             `json:"is_suppressed"`
	RootCauseCI string            `json:"root_cause_ci,omitempty"`
	Fingerprint string            `json:"fingerprint"`
	TriggeredAt time.Time         `json:"triggered_at"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
}

type AlertRule struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	MetricName  string            `json:"metric_name"`
	Operator    string            `json:"operator"`
	Threshold   float64           `json:"threshold"`
	Duration    time.Duration     `json:"duration"`
	Severity    AlertSeverity     `json:"severity"`
	Labels      map[string]string `json:"labels,omitempty"`
	Enabled     bool              `json:"enabled"`

	// Type 规则类型(阶段 4 PromQL 化):
	//   - "threshold"(默认): 基于 MetricName+Operator+Threshold 的单指标阈值;
	//   - "promql": 基于 Expr 的 PromQL 表达式(查询 VM, 命中非空即告警;
	//     含 no_data 检测: 如 absent(cpu.usage) 表达式命中即"数据断流"告警)。
	Type string `json:"type,omitempty"`
	// Expr PromQL 表达式(Type=promql 时使用)。如 avg(cpu.usage) > 90 / absent(cpu.usage)。
	Expr string `json:"expr,omitempty"`
	// EvalIntervalSec 评估周期(秒, 默认 60)。PromQL 规则按此周期查询 VM。
	EvalIntervalSec int `json:"eval_interval_sec,omitempty"`
}
