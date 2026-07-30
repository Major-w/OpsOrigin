package deploy

import "time"

type AppType = string
type AppStatus = string
type DeployEnv = string
type DeployMode = string
type ReleaseStatus = string
type ReleaseStrategy = string

const (
	TypeJava      AppType = "java"
	TypeGo        AppType = "go"
	TypePython    AppType = "python"
	TypeNodeJS    AppType = "nodejs"
	TypeStatic    AppType = "static"
	TypeContainer AppType = "container"
)

// 部署模式：决定发布单在目标环境如何真正落地。
const (
	// ModeHost 主机环境：经被管主机 Agent 下发 deploy_script 在目标主机执行。
	ModeHost DeployMode = "host"
	// ModeContainer 容器环境：经 K8s 网关 Agent 下发 kubectl 指令到集群（set image / apply）。
	ModeContainer DeployMode = "container"
)

const (
	StatusEnabled  AppStatus = "enabled"
	StatusDisabled AppStatus = "disabled"
)

var AllEnvs = []DeployEnv{"dev", "test", "staging", "prod"}

type DeployApp struct {
	ID              string                `json:"id"`
	TenantID        string                `json:"tenant_id"`
	Name            string                `json:"name"`
	Code            string                `json:"code"`
	Type            AppType               `json:"type"`
	Status          AppStatus             `json:"status"`
	RepoURL         string                `json:"repo_url"`
	Branch          string                `json:"branch"`
	BuildCmd        string                `json:"build_cmd"`
	ArtifactPath    string                `json:"artifact_path"`
	DeployScript    string                `json:"deploy_script"`
	HealthCheckURL  string                `json:"health_check_url"`
	TargetPort      int                   `json:"target_port"`
	// 容器模式（ModeContainer）专用字段：经 K8s 网关 Agent 下发 kubectl 部署。
	ImageURL      string `json:"image_url"`      // 镜像仓库地址（不含 tag，tag 取发布单 version）
	Namespace     string `json:"namespace"`       // K8s 命名空间
	Workload      string `json:"workload"`        // 工作负载，如 deployment/myapp 或 statefulset/myapp
	ContainerName string `json:"container_name"`  // 容器名（set image 用），缺省取 code/name
	Replicas      int    `json:"replicas"`        // 期望副本数（manifest 模式可忽略）
	Manifest      string `json:"manifest"`        // 可选：原始 YAML，优先于 set image 直接 apply -f
	Envs            []DeployEnv           `json:"envs"`
	CurrentVersions map[DeployEnv]string  `json:"current_versions"`
	Description     string                `json:"description"`
	Owner           string                `json:"owner"`
	Labels          map[string]string     `json:"labels"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type ReleaseTicket struct {
	ID          string           `json:"id"`
	TenantID    string           `json:"tenant_id"`
	AppID       string           `json:"app_id"`
	AppName     string           `json:"app_name"`
	AppCode     string           `json:"app_code"`
	Title       string           `json:"title"`
	Version     string           `json:"version"`
	Env         DeployEnv        `json:"env"`
	Strategy    ReleaseStrategy  `json:"strategy"`
	Status      ReleaseStatus    `json:"status"`
	CommitID    string           `json:"commit_id"`
	CommitMsg   string           `json:"commit_msg"`
	Changelog   string           `json:"changelog"`
	Approvers   []string         `json:"approvers"`
	ApprovedBy  string           `json:"approved_by"`
	RejectedBy  string           `json:"rejected_by"`
	RejectReason string          `json:"reject_reason"`
	Operator    string           `json:"operator"`
	Steps       []DeployStep     `json:"steps"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	ApprovedAt  *time.Time       `json:"approved_at,omitempty"`
	DeployedAt  *time.Time       `json:"deployed_at,omitempty"`
}

type DeployStep struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`  // pending/running/succeeded/failed
	Output  string `json:"output"`
}

type DeployEnvironment struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Type        DeployEnv         `json:"type"`
	// Mode 决定该环境的部署落地方式：host（经主机 Agent 下发脚本）或 container（经 K8s 网关 Agent 下发 kubectl）。
	Mode        DeployMode        `json:"mode"`
	Hosts       []string          `json:"hosts"`
	// AgentIDs 是真正执行部署脚本的被管主机 Agent 列表（经 Tunnel 下发）。
	// 与 Hosts 不同：Hosts 仅作展示/记录，AgentIDs 决定脚本发往哪些 Agent。
	AgentIDs    []string          `json:"agent_ids"`
	Config      map[string]string `json:"config"`
	AgentCount  int               `json:"agent_count"`
	Status      string            `json:"status"`
	Description string            `json:"description"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type ReleaseHistory struct {
	ID          string        `json:"id"`
	TenantID    string        `json:"tenant_id"`
	ReleaseID   string        `json:"release_id"`
	AppID       string        `json:"app_id"`
	AppName     string        `json:"app_name"`
	Env         DeployEnv     `json:"env"`
	Version     string        `json:"version"`
	Status      ReleaseStatus `json:"status"`
	Operator    string        `json:"operator"`
	Output      string        `json:"output"`
	CreatedAt   time.Time     `json:"created_at"`
}
