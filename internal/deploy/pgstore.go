package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	return s
}

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS deploy_apps (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, code TEXT NOT NULL, type TEXT DEFAULT 'go',
		status TEXT DEFAULT 'enabled', repo_url TEXT DEFAULT '', branch TEXT DEFAULT '',
		build_cmd TEXT DEFAULT '', artifact_path TEXT DEFAULT '', deploy_script TEXT DEFAULT '',
		health_check_url TEXT DEFAULT '', target_port INT DEFAULT 8080,
		envs JSONB DEFAULT '[]', current_versions JSONB DEFAULT '{}',
		description TEXT DEFAULT '', owner TEXT DEFAULT '',
		labels JSONB DEFAULT '{}', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS deploy_releases (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		app_id TEXT NOT NULL, app_name TEXT DEFAULT '', app_code TEXT DEFAULT '',
		title TEXT NOT NULL, version TEXT NOT NULL, env TEXT DEFAULT 'dev',
		strategy TEXT DEFAULT 'rolling', status TEXT DEFAULT 'draft',
		commit_id TEXT DEFAULT '', commit_msg TEXT DEFAULT '', changelog TEXT DEFAULT '',
		approvers JSONB DEFAULT '[]', approved_by TEXT DEFAULT '', rejected_by TEXT DEFAULT '',
		reject_reason TEXT DEFAULT '', operator TEXT DEFAULT '',
		steps JSONB DEFAULT '[]', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(),
		approved_at TIMESTAMPTZ, deployed_at TIMESTAMPTZ
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS deploy_environments (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, type TEXT DEFAULT 'dev',
		hosts JSONB DEFAULT '[]', config JSONB DEFAULT '{}',
		agent_count INT DEFAULT 0, status TEXT DEFAULT 'active',
		description TEXT DEFAULT '', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS deploy_history (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		release_id TEXT NOT NULL, app_id TEXT NOT NULL, app_name TEXT DEFAULT '',
		env TEXT DEFAULT 'dev', version TEXT DEFAULT '', status TEXT DEFAULT 'succeeded',
		operator TEXT DEFAULT '', output TEXT DEFAULT '', created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

// ======= Apps =======

func (s *Store) ListApps(ctx context.Context, tenantID string) ([]DeployApp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,name,code,type,status,repo_url,branch,build_cmd,artifact_path,deploy_script,health_check_url,target_port,envs,current_versions,description,owner,labels,created_at,updated_at FROM deploy_apps WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanApps(rows)
}

func (s *Store) CreateApp(ctx context.Context, a *DeployApp) error {
	a.ID = uuid.New().String(); a.CreatedAt = time.Now(); a.UpdatedAt = a.CreatedAt
	if a.TenantID == "" { a.TenantID = "default" }
	if a.Code == "" { a.Code = a.Name }
	envs, _ := json.Marshal(a.Envs); vers, _ := json.Marshal(a.CurrentVersions); lbls, _ := json.Marshal(a.Labels)
	_, err := s.db.ExecContext(ctx, `INSERT INTO deploy_apps VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21) ON CONFLICT(id) DO NOTHING`,
		a.ID,a.TenantID,a.Name,a.Code,a.Type,a.Status,a.RepoURL,a.Branch,a.BuildCmd,a.ArtifactPath,a.DeployScript,a.HealthCheckURL,a.TargetPort,envs,vers,a.Description,a.Owner,lbls,a.CreatedAt,a.UpdatedAt)
	return err
}

func (s *Store) UpdateApp(ctx context.Context, a *DeployApp) error {
	a.UpdatedAt = time.Now()
	envs, _ := json.Marshal(a.Envs); vers, _ := json.Marshal(a.CurrentVersions); lbls, _ := json.Marshal(a.Labels)
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_apps SET name=$1,code=$2,type=$3,status=$4,repo_url=$5,branch=$6,build_cmd=$7,artifact_path=$8,deploy_script=$9,health_check_url=$10,target_port=$11,envs=$12,current_versions=$13,description=$14,owner=$15,labels=$16,updated_at=$17 WHERE id=$18 AND tenant_id=$19`,
		a.Name,a.Code,a.Type,a.Status,a.RepoURL,a.Branch,a.BuildCmd,a.ArtifactPath,a.DeployScript,a.HealthCheckURL,a.TargetPort,envs,vers,a.Description,a.Owner,lbls,a.UpdatedAt,a.ID,a.TenantID)
	return err
}

func (s *Store) DeleteApp(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deploy_apps WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ======= Releases =======

func (s *Store) ListReleases(ctx context.Context, tenantID string) ([]ReleaseTicket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,app_id,app_name,app_code,title,version,env,strategy,status,commit_id,commit_msg,changelog,approvers,approved_by,rejected_by,reject_reason,operator,steps,created_at,updated_at,approved_at,deployed_at FROM deploy_releases WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanReleases(rows)
}

func (s *Store) CreateRelease(ctx context.Context, r *ReleaseTicket) error {
	r.ID = uuid.New().String(); r.CreatedAt = time.Now(); r.UpdatedAt = r.CreatedAt
	if r.TenantID == "" { r.TenantID = "default" }
	if r.Status == "" { r.Status = "draft" }
	approvers, _ := json.Marshal(r.Approvers); steps, _ := json.Marshal(r.Steps)
	_, err := s.db.ExecContext(ctx, `INSERT INTO deploy_releases VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23) ON CONFLICT(id) DO NOTHING`,
		r.ID,r.TenantID,r.AppID,r.AppName,r.AppCode,r.Title,r.Version,r.Env,r.Strategy,r.Status,r.CommitID,r.CommitMsg,r.Changelog,approvers,r.ApprovedBy,r.RejectedBy,r.RejectReason,r.Operator,steps,r.CreatedAt,r.UpdatedAt,r.ApprovedAt,r.DeployedAt)
	return err
}

func (s *Store) UpdateRelease(ctx context.Context, r *ReleaseTicket) error {
	r.UpdatedAt = time.Now()
	approvers, _ := json.Marshal(r.Approvers); steps, _ := json.Marshal(r.Steps)
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_releases SET app_id=$1,title=$2,version=$3,env=$4,strategy=$5,status=$6,approvers=$7,approved_by=$8,rejected_by=$9,reject_reason=$10,operator=$11,steps=$12,updated_at=$13,approved_at=$14,deployed_at=$15 WHERE id=$16 AND tenant_id=$17`,
		r.AppID,r.Title,r.Version,r.Env,r.Strategy,r.Status,approvers,r.ApprovedBy,r.RejectedBy,r.RejectReason,r.Operator,steps,r.UpdatedAt,r.ApprovedAt,r.DeployedAt,r.ID,r.TenantID)
	return err
}

func (s *Store) DeleteRelease(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deploy_releases WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ======= Environments =======

func (s *Store) ListEnvs(ctx context.Context, tenantID string) ([]DeployEnvironment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,name,type,hosts,config,agent_count,status,description,created_at,updated_at FROM deploy_environments WHERE tenant_id=$1`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []DeployEnvironment
	for rows.Next() {
		var e DeployEnvironment; var hosts, cfg []byte
		rows.Scan(&e.ID,&e.TenantID,&e.Name,&e.Type,&hosts,&cfg,&e.AgentCount,&e.Status,&e.Description,&e.CreatedAt,&e.UpdatedAt)
		json.Unmarshal(hosts, &e.Hosts); json.Unmarshal(cfg, &e.Config)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CreateEnv(ctx context.Context, e *DeployEnvironment) error {
	e.ID = uuid.New().String(); e.CreatedAt = time.Now(); e.UpdatedAt = e.CreatedAt
	if e.TenantID == "" { e.TenantID = "default" }
	hosts, _ := json.Marshal(e.Hosts); cfg, _ := json.Marshal(e.Config)
	_, err := s.db.ExecContext(ctx, `INSERT INTO deploy_environments VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		e.ID,e.TenantID,e.Name,e.Type,hosts,cfg,e.AgentCount,e.Status,e.Description,e.CreatedAt,e.UpdatedAt)
	return err
}

func (s *Store) UpdateEnv(ctx context.Context, e *DeployEnvironment) error {
	e.UpdatedAt = time.Now()
	hosts, _ := json.Marshal(e.Hosts); cfg, _ := json.Marshal(e.Config)
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_environments SET name=$1,type=$2,hosts=$3,config=$4,agent_count=$5,status=$6,description=$7,updated_at=$8 WHERE id=$9 AND tenant_id=$10`,
		e.Name,e.Type,hosts,cfg,e.AgentCount,e.Status,e.Description,e.UpdatedAt,e.ID,e.TenantID)
	return err
}

func (s *Store) DeleteEnv(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deploy_environments WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ======= History =======

func (s *Store) ListHistory(ctx context.Context, tenantID string) ([]ReleaseHistory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,release_id,app_id,app_name,env,version,status,operator,output,created_at FROM deploy_history WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []ReleaseHistory
	for rows.Next() {
		var h ReleaseHistory
		rows.Scan(&h.ID,&h.TenantID,&h.ReleaseID,&h.AppID,&h.AppName,&h.Env,&h.Version,&h.Status,&h.Operator,&h.Output,&h.CreatedAt)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) CreateHistory(ctx context.Context, h *ReleaseHistory) error {
	h.ID = uuid.New().String(); h.CreatedAt = time.Now()
	if h.TenantID == "" { h.TenantID = "default" }
	_, err := s.db.ExecContext(ctx, `INSERT INTO deploy_history VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		h.ID,h.TenantID,h.ReleaseID,h.AppID,h.AppName,h.Env,h.Version,h.Status,h.Operator,h.Output,h.CreatedAt)
	return err
}

// helpers
func scanApps(rows *sql.Rows) ([]DeployApp, error) {
	var out []DeployApp
	for rows.Next() {
		var a DeployApp; var envs, vers, lbls []byte
		if err := rows.Scan(&a.ID,&a.TenantID,&a.Name,&a.Code,&a.Type,&a.Status,&a.RepoURL,&a.Branch,&a.BuildCmd,&a.ArtifactPath,&a.DeployScript,&a.HealthCheckURL,&a.TargetPort,&envs,&vers,&a.Description,&a.Owner,&lbls,&a.CreatedAt,&a.UpdatedAt); err != nil { return nil, err }
		json.Unmarshal(envs, &a.Envs); json.Unmarshal(vers, &a.CurrentVersions); json.Unmarshal(lbls, &a.Labels)
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanReleases(rows *sql.Rows) ([]ReleaseTicket, error) {
	var out []ReleaseTicket
	for rows.Next() {
		var r ReleaseTicket; var approvers, steps []byte
		if err := rows.Scan(&r.ID,&r.TenantID,&r.AppID,&r.AppName,&r.AppCode,&r.Title,&r.Version,&r.Env,&r.Strategy,&r.Status,&r.CommitID,&r.CommitMsg,&r.Changelog,&approvers,&r.ApprovedBy,&r.RejectedBy,&r.RejectReason,&r.Operator,&steps,&r.CreatedAt,&r.UpdatedAt,&r.ApprovedAt,&r.DeployedAt); err != nil { return nil, err }
		json.Unmarshal(approvers, &r.Approvers); json.Unmarshal(steps, &r.Steps)
		out = append(out, r)
	}
	return out, rows.Err()
}
