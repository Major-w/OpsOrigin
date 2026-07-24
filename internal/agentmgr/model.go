package agentmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type AgentType string

const (
	AgentTypeAgent     AgentType = "agent"
	AgentTypeAgentless AgentType = "agentless"
	AgentTypeSandbox   AgentType = "sandbox"
)

type InstallMethod string

const (
	InstallAuto   InstallMethod = "auto"
	InstallManual InstallMethod = "manual"
)

type AgentStatus string

const (
	AgentStatusOnline  AgentStatus = "online"
	AgentStatusOffline AgentStatus = "offline"
	AgentStatusUnknown AgentStatus = "unknown"
)

// 离线判定阈值:超过该时长未收到心跳则标记为 offline
const OfflineThreshold = 90 * time.Second

type AgentHost struct {
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	Fingerprint   string            `json:"fingerprint,omitempty"`
	Channels      []string          `json:"channels,omitempty"`       // 可用通道: ["agent","ssh"] 等 // SHA256(tenant:address), 用于去重
	Name          string            `json:"name"`
	Type          AgentType         `json:"type"`
	Address       string            `json:"address"`
	Port          int               `json:"port"`
	OSType        string            `json:"os_type,omitempty"`
	OSVersion     string            `json:"os_version,omitempty"`
	AuthType      string            `json:"auth_type,omitempty"`
	SSHUser       string            `json:"ssh_user,omitempty"`
	SSHPassword   string            `json:"-"`
	SSHKey        string            `json:"-"`
	Status        AgentStatus       `json:"status"`
	ProxyID       string            `json:"proxy_id,omitempty"`
	InstallMethod InstallMethod     `json:"install_method"`
	Version       string            `json:"version,omitempty"`
	CPU           string            `json:"cpu,omitempty"`
	Memory        string            `json:"memory,omitempty"`
	Disk          string            `json:"disk,omitempty"`
	Hostname      string            `json:"hostname,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Environment   string            `json:"environment,omitempty"` // sandbox, production
	LastSeen      *time.Time        `json:"last_seen,omitempty"`
	LastHeartbeat *time.Time        `json:"last_heartbeat,omitempty"`
	FirstSeen     *time.Time        `json:"first_seen,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type RegisterAgentRequest struct {
	Name           string            `json:"name"`
	Type           AgentType         `json:"type"`
	Address        string            `json:"address"`
	Port           int               `json:"port,omitempty"`
	OSType         string            `json:"os_type,omitempty"`
	AuthType       string            `json:"auth_type,omitempty"`
	SSHUser        string            `json:"ssh_user,omitempty"`
	SSHPassword    string            `json:"ssh_password,omitempty"`
	SSHKey         string            `json:"ssh_key,omitempty"`
	CredentialID   string            `json:"credential_id,omitempty"`
	CredentialType string            `json:"credential_type,omitempty"`
	InstallMethod InstallMethod     `json:"install_method,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	// AsProxy 勾选时将该主机注册为 Proxy 而非 AgentHost
	AsProxy bool `json:"as_proxy,omitempty"`
	ProxyID string `json:"proxy_id,omitempty"` // 通过 Proxy 连接时指定 Proxy ID,空表示直连
}

// UpdateAgentRequest 支持更多字段的更新请求
type UpdateAgentRequest struct {
	Name     string            `json:"name,omitempty"`
	Address  string            `json:"address,omitempty"`
	Port     int               `json:"port,omitempty"`
	SSHUser  string            `json:"ssh_user,omitempty"`
	AuthType string            `json:"auth_type,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type HeartbeatRequest struct {
	AgentVersion string   `json:"agent_version,omitempty"`
	OS           string   `json:"os,omitempty"`
	OSVersion    string   `json:"os_version,omitempty"`
	Hostname     string   `json:"hostname,omitempty"`
	CPU          string   `json:"cpu,omitempty"`
	Memory       string   `json:"memory,omitempty"`
	Disk         string   `json:"disk,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	IPs          []string `json:"ips,omitempty"`
}

type BatchInstallRequest struct {
	Addresses      []string `json:"addresses"`
	Username       string   `json:"username"`
	Password       string   `json:"password,omitempty"`
	SSHKey         string   `json:"ssh_key,omitempty"`
	CredentialID   string   `json:"credential_id,omitempty"`
	CredentialType string   `json:"credential_type,omitempty"`
	Port           int      `json:"port,omitempty"`
	// AsProxy 勾选时将目标主机注册为 Proxy 节点
	AsProxy bool `json:"as_proxy,omitempty"`
	ProxyID string `json:"proxy_id,omitempty"` // 通过 Proxy 连接时指定 Proxy ID,空表示直连
}

// TaskStatus 任务执行状态
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

type TaskRequest struct {
	TaskID  string         `json:"task_id"`
	Action  string         `json:"action"`
	Script  string         `json:"script,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Timeout int            `json:"timeout,omitempty"`
	RunAs  string          `json:"run_as,omitempty"` // 以指定系统用户身份执行(Agent 端可 honor)

	// 文件分发(§5)相关字段。Agent 拉取 download-tasks 时,这些字段直接映射
	// 到 Agent 端 FileDownloadRequest(file_url/file_path/expected_checksum/checksum_algo),
	// 无需额外转换。FileURL 由 Server 在签发下载任务时填充(含短期令牌)。
	FileID          string `json:"file_id,omitempty"`          // 中心存储 object key,用于令牌绑定与文件寻址
	FileURL         string `json:"file_url,omitempty"`         // 完整下载地址(含短期令牌),拓扑感知(直连 Server 或经 Proxy)
	FilePath        string `json:"file_path,omitempty"`        // Agent 本地落盘路径
	ExpectedChecksum string `json:"expected_checksum,omitempty"` // 非空时 Agent 下载完成后强制校验(§5.3)
	ChecksumAlgo   string `json:"checksum_algo,omitempty"`   // "sha256"(默认) / "md5"
}

type TaskResponse struct {
	TaskID   string `json:"task_id"`
	Success  bool   `json:"success"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
}

// TaskInfo 任务完整信息(含状态)
type TaskInfo struct {
	TaskRequest
	Status    TaskStatus   `json:"status"`
	Response  *TaskResponse `json:"response,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type CircuitBreakerConfig struct {
	AgentID             string     `json:"agent_id"`
	MaxHeartbeatsPerMin int        `json:"max_heartbeats_per_min"`
	MaxReqPerMin        int        `json:"max_req_per_min"`
	AutoRestart         bool       `json:"auto_restart"`
	MaxConsecutiveFails int        `json:"max_consecutive_fails"`
	BreakerOpen         bool       `json:"breaker_open"`
	FailCount           int        `json:"fail_count"`
	LastFail            *time.Time `json:"last_fail,omitempty"`
	ConfigLimit         int        `json:"config_limit,omitempty"`
}

// RegisterProxyRequest Proxy 注册请求
type RegisterProxyRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// UpdateProxyRequest Proxy 更新请求
type UpdateProxyRequest struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
	Status  string `json:"status,omitempty"`
}

type ProxyInfo struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Name       string     `json:"name"`
	Address    string     `json:"address"`
	Port       int        `json:"port"`
	Status     string     `json:"status"`
	AgentCount int        `json:"agent_count"`
	Version    string     `json:"version,omitempty"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// GenerateFingerprint 基于租户+地址生成唯一标识，用于跨渠道去重(Agent/Agentless/Proxy)。
func GenerateFingerprint(tenantID, address string) string {
	h := sha256.Sum256([]byte(tenantID + ":" + address))
	return hex.EncodeToString(h[:])[:16]
}
