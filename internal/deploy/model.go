package deploy

import "time"

type AppType = string
type AppStatus = string
type DeployEnv = string
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
	Hosts       []string          `json:"hosts"`
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
