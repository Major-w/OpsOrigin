package agentmgr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ent"
	entAgentHost "github.com/opsorigin/core/internal/ent/agenthost"
	entAgentProxy "github.com/opsorigin/core/internal/ent/agentproxy"
	entAgentTask "github.com/opsorigin/core/internal/ent/agenttask"
)

type pgStore struct {
	db        *sql.DB
	entClient *ent.Client
}

func newPGStore(db *sql.DB, entClient *ent.Client) *pgStore {
	s := &pgStore{db: db, entClient: entClient}
	s.init(context.Background())
	return s
}

func (s *pgStore) init(ctx context.Context) {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_hosts (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, fingerprint TEXT,
			name TEXT, type TEXT,
			address TEXT, port INTEGER, os_type TEXT, os_version TEXT,
			auth_type TEXT, ssh_user TEXT, status TEXT DEFAULT 'offline',
			proxy_id TEXT, install_method TEXT, version TEXT, cpu TEXT, memory TEXT,
			disk TEXT, hostname TEXT, labels TEXT DEFAULT '{}', environment TEXT,
			capabilities TEXT DEFAULT '[]', ips TEXT DEFAULT '[]',
			channels TEXT DEFAULT '[]',
			last_seen TIMESTAMPTZ, last_heartbeat TIMESTAMPTZ, first_seen TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_hosts_tenant ON agent_hosts(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_hosts_fingerprint ON agent_hosts(fingerprint)`,
		`CREATE TABLE IF NOT EXISTS agent_proxies (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, address TEXT,
			port INTEGER, status TEXT DEFAULT 'offline', agent_count INTEGER,
			version TEXT, last_seen TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_proxies_tenant ON agent_proxies(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS agent_tasks (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, agent_id TEXT,
			action TEXT, script TEXT, params TEXT DEFAULT '{}', timeout INTEGER,
			run_as TEXT, status TEXT DEFAULT 'pending', output TEXT,
			exit_code INTEGER, duration_ms BIGINT DEFAULT 0, error TEXT,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_agent ON agent_tasks(agent_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_tenant ON agent_tasks(tenant_id)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("agentmgr pg store init DDL failed", "err", err)
			return
		}
	}
}

// ——— Agent CRUD (ent 链式调用) ———

func (s *pgStore) Register(ctx context.Context, h *AgentHost) error {
	h.Fingerprint = GenerateFingerprint(h.TenantID, h.Address)
	ch := channelFromType(h.Type)
	existing, _ := s.GetByFingerprint(ctx, h.Fingerprint, h.TenantID)
	isUpdate := existing != nil

	if isUpdate {
		h.ID = existing.ID
		if h.FirstSeen == nil {
			h.FirstSeen = existing.FirstSeen
		}
		if h.CreatedAt.IsZero() {
			h.CreatedAt = existing.CreatedAt
		}
		if ch != "" {
			h.Channels = mergeChannels(existing.Channels, ch)
		}
	}
	if h.ID == "" {
		h.ID = uuid.New().String()
	}
	if len(h.Channels) == 0 {
		h.Channels = []string{ch}
	}
	if h.TenantID == "" {
		h.TenantID = "default"
	}
	now := time.Now()
	if h.CreatedAt.IsZero() {
		h.CreatedAt = now
	}
	h.UpdatedAt = now
	if h.Status == "" {
		h.Status = "offline"
	}

	if isUpdate {
		_, err := s.entClient.AgentHost.UpdateOneID(h.ID).
			SetTenantID(h.TenantID).
			SetFingerprint(h.Fingerprint).
			SetName(h.Name).
			SetType(string(h.Type)).
			SetAddress(h.Address).
			SetPort(h.Port).
			SetOsType(h.OSType).
			SetOsVersion(h.OSVersion).
			SetAuthType(h.AuthType).
			SetSSHUser(h.SSHUser).
			SetStatus(string(h.Status)).
			SetProxyID(h.ProxyID).
			SetInstallMethod(string(h.InstallMethod)).
			SetVersion(h.Version).
			SetCPU(h.CPU).
			SetMemory(h.Memory).
			SetDisk(h.Disk).
			SetHostname(h.Hostname).
			SetLabels(h.Labels).
			SetEnvironment(h.Environment).
			SetCapabilities(h.Capabilities).
			SetIps([]string{}).
			SetChannels(h.Channels).
			SetNillableLastSeen(h.LastSeen).
			SetNillableLastHeartbeat(h.LastHeartbeat).
			SetNillableFirstSeen(h.FirstSeen).
			SetUpdatedAt(h.UpdatedAt).
			Save(ctx)
		return err
	}

	err := s.entClient.AgentHost.Create().
		SetID(h.ID).
		SetTenantID(h.TenantID).
		SetFingerprint(h.Fingerprint).
		SetName(h.Name).
		SetType(string(h.Type)).
		SetAddress(h.Address).
		SetPort(h.Port).
		SetOsType(h.OSType).
		SetOsVersion(h.OSVersion).
		SetAuthType(h.AuthType).
		SetSSHUser(h.SSHUser).
		SetStatus(string(h.Status)).
		SetProxyID(h.ProxyID).
		SetInstallMethod(string(h.InstallMethod)).
		SetVersion(h.Version).
		SetCPU(h.CPU).
		SetMemory(h.Memory).
		SetDisk(h.Disk).
		SetHostname(h.Hostname).
		SetLabels(h.Labels).
		SetEnvironment(h.Environment).
		SetCapabilities(h.Capabilities).
		SetIps([]string{}).
		SetChannels(h.Channels).
		SetNillableLastSeen(h.LastSeen).
		SetNillableLastHeartbeat(h.LastHeartbeat).
		SetNillableFirstSeen(h.FirstSeen).
		SetCreatedAt(h.CreatedAt).
		SetUpdatedAt(h.UpdatedAt).
		Exec(ctx)
	return err
}

func (s *pgStore) GetByFingerprint(ctx context.Context, fp, tenantID string) (*AgentHost, error) {
	row, err := s.entClient.AgentHost.Query().
		Where(
			entAgentHost.FingerprintEQ(fp),
			entAgentHost.TenantIDEQ(tenantID),
		).First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return entRowToAgentHost(row), nil
}

func (s *pgStore) Get(ctx context.Context, id, tenantID string) (*AgentHost, error) {
	row, err := s.entClient.AgentHost.Query().
		Where(
			entAgentHost.IDEQ(id),
			entAgentHost.TenantIDEQ(tenantID),
		).First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return entRowToAgentHost(row), nil
}

func (s *pgStore) List(ctx context.Context, tenantID string) ([]*AgentHost, error) {
	rows, err := s.entClient.AgentHost.Query().
		Where(entAgentHost.TenantIDEQ(tenantID)).
		Order(ent.Asc(entAgentHost.FieldName)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*AgentHost, 0, len(rows))
	for _, row := range rows {
		out = append(out, entRowToAgentHost(row))
	}
	return out, nil
}

func (s *pgStore) Update(ctx context.Context, h *AgentHost) error {
	h.UpdatedAt = time.Now()
	_, err := s.entClient.AgentHost.Update().
		Where(
			entAgentHost.IDEQ(h.ID),
			entAgentHost.TenantIDEQ(h.TenantID),
		).
		SetName(h.Name).
		SetType(string(h.Type)).
		SetAddress(h.Address).
		SetPort(h.Port).
		SetOsType(h.OSType).
		SetOsVersion(h.OSVersion).
		SetAuthType(h.AuthType).
		SetSSHUser(h.SSHUser).
		SetStatus(string(h.Status)).
		SetProxyID(h.ProxyID).
		SetInstallMethod(string(h.InstallMethod)).
		SetVersion(h.Version).
		SetCPU(h.CPU).
		SetMemory(h.Memory).
		SetDisk(h.Disk).
		SetHostname(h.Hostname).
		SetLabels(h.Labels).
		SetEnvironment(h.Environment).
		SetCapabilities(h.Capabilities).
		SetNillableLastSeen(h.LastSeen).
		SetNillableLastHeartbeat(h.LastHeartbeat).
		SetNillableFirstSeen(h.FirstSeen).
		SetUpdatedAt(h.UpdatedAt).
		Save(ctx)
	return err
}

func (s *pgStore) Delete(ctx context.Context, id, tenantID string) error {
	tx, err := s.entClient.Tx(ctx)
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		_, _ = tx.AgentTask.Delete().Where(entAgentTask.AgentIDEQ(id)).Exec(ctx)
		_, err2 := tx.AgentHost.Delete().Where(
			entAgentHost.IDEQ(id),
			entAgentHost.TenantIDEQ(tenantID),
		).Exec(ctx)
		if err2 != nil {
			_ = tx.Rollback()
			return err2
		}
		return tx.Commit()
	}
	_, err = s.entClient.AgentHost.Delete().Where(
		entAgentHost.IDEQ(id),
		entAgentHost.TenantIDEQ(tenantID),
	).Exec(ctx)
	return err
}

// heartbeatBatch bundles heartbeat data for bulk upsert.
type heartbeatBatch struct {
	ID, TenantID, Version, OS, OSVersion, Hostname, CPU, Memory, IPs, Capabilities string
	LastSeen, LastHeartbeat                                                     time.Time
}

// BulkHeartbeat bulk-upserts agent heartbeat records in a single INSERT … ON CONFLICT statement.
func (s *pgStore) BulkHeartbeat(ctx context.Context, batch []heartbeatBatch) error {
	if len(batch) == 0 {
		return nil
	}
	vals := make([]string, 0, len(batch))
	args := make([]any, 0, len(batch)*17)
	now := time.Now()
	for i, hb := range batch {
		base := i * 17
		vals = append(vals, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7,
			base+8, base+9, base+10, base+11, base+12, base+13, base+14, base+15, base+16, base+17))
		args = append(args,
			hb.ID, hb.TenantID,
			hb.Hostname, // name 兜底用 hostname,纯隧道注册的 Agent 首次落库也有可读名称
			string(AgentTypeAgent), // 经 gRPC/隧道注册的统一标记为 agent 类型,避免 UI 兜底显示 "Agentless"
			hb.Version, hb.OS, hb.OSVersion,
			hb.Hostname, hb.CPU, hb.Memory,
			hb.IPs, hb.Capabilities, // JSON 文本,与 ent 序列化格式一致
			string(AgentStatusOnline),
			hb.LastSeen, hb.LastHeartbeat,
			now, now,
		)
	}
	query := `INSERT INTO agent_hosts(id,tenant_id,name,type,version,os_type,os_version,hostname,cpu,memory,ips,capabilities,status,last_seen,last_heartbeat,created_at,updated_at)
VALUES ` + strings.Join(vals, ",") + `
ON CONFLICT(id) DO UPDATE SET
  last_seen=EXCLUDED.last_seen,
  last_heartbeat=EXCLUDED.last_heartbeat,
  status='online',
  type=COALESCE(NULLIF(agent_hosts.type,''),'agent'),
  hostname=COALESCE(NULLIF(EXCLUDED.hostname,''), agent_hosts.hostname),
  name=COALESCE(NULLIF(EXCLUDED.name,''), agent_hosts.name),
  version=COALESCE(NULLIF(EXCLUDED.version,''), agent_hosts.version),
  os_type=COALESCE(NULLIF(EXCLUDED.os_type,''), agent_hosts.os_type),
  os_version=COALESCE(NULLIF(EXCLUDED.os_version,''), agent_hosts.os_version),
  cpu=COALESCE(NULLIF(EXCLUDED.cpu,''), agent_hosts.cpu),
  memory=COALESCE(NULLIF(EXCLUDED.memory,''), agent_hosts.memory),
  ips=COALESCE(NULLIF(EXCLUDED.ips,''), agent_hosts.ips),
  capabilities=COALESCE(NULLIF(EXCLUDED.capabilities,''), agent_hosts.capabilities),
  updated_at=NOW()`
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *pgStore) Heartbeat(ctx context.Context, id, tenantID string, req *HeartbeatRequest) error {
	now := time.Now()
	_, err := s.entClient.AgentHost.Update().
		Where(
			entAgentHost.IDEQ(id),
			entAgentHost.TenantIDEQ(tenantID),
		).
		SetStatus(string(AgentStatusOnline)).
		SetType(string(AgentTypeAgent)).
		SetVersion(req.AgentVersion).
		SetCPU(req.CPU).
		SetMemory(req.Memory).
		SetOsType(req.OS).
		SetOsVersion(req.OSVersion).
		SetCapabilities(req.Capabilities).
		SetIps(req.IPs).
		SetNillableLastSeen(&now).
		SetUpdatedAt(now).
		Save(ctx)
	return err
}

func (s *pgStore) MarkOfflineAgents(ctx context.Context, tenantID string, threshold time.Duration) (int, error) {
	cutoff := time.Now().Add(-threshold)
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_hosts SET status='offline',updated_at=$3 WHERE tenant_id=$1 AND status='online' AND last_seen < $2`,
		tenantID, cutoff, time.Now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ——— Proxy CRUD (ent 链式调用) ———

func (s *pgStore) RegisterProxy(ctx context.Context, p *ProxyInfo) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if p.Status == "" {
		p.Status = "offline"
	}

	existing, err := s.entClient.AgentProxy.Query().
		Where(entAgentProxy.IDEQ(p.ID), entAgentProxy.TenantIDEQ(p.TenantID)).
		First(ctx)
	isUpdate := err == nil && existing != nil

	if isUpdate {
		_, err = s.entClient.AgentProxy.UpdateOneID(p.ID).
			SetTenantID(p.TenantID).
			SetName(p.Name).
			SetAddress(p.Address).
			SetPort(p.Port).
			SetStatus(p.Status).
			SetAgentCount(p.AgentCount).
			SetVersion(p.Version).
			SetNillableLastSeen(p.LastSeen).
			Save(ctx)
		return err
	}

	err = s.entClient.AgentProxy.Create().
		SetID(p.ID).
		SetTenantID(p.TenantID).
		SetName(p.Name).
		SetAddress(p.Address).
		SetPort(p.Port).
		SetStatus(p.Status).
		SetAgentCount(p.AgentCount).
		SetVersion(p.Version).
		SetNillableLastSeen(p.LastSeen).
		SetCreatedAt(p.CreatedAt).
		Exec(ctx)
	return err
}

func (s *pgStore) GetProxy(ctx context.Context, id, tenantID string) (*ProxyInfo, error) {
	row, err := s.entClient.AgentProxy.Query().
		Where(
			entAgentProxy.IDEQ(id),
			entAgentProxy.TenantIDEQ(tenantID),
		).First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return entRowToProxy(row), nil
}

func (s *pgStore) ListProxies(ctx context.Context, tenantID string) ([]*ProxyInfo, error) {
	rows, err := s.entClient.AgentProxy.Query().
		Where(entAgentProxy.TenantIDEQ(tenantID)).
		Order(ent.Asc(entAgentProxy.FieldName)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ProxyInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, entRowToProxy(row))
	}
	return out, nil
}

func (s *pgStore) UpdateProxy(ctx context.Context, p *ProxyInfo) error {
	_, err := s.entClient.AgentProxy.Update().
		Where(
			entAgentProxy.IDEQ(p.ID),
			entAgentProxy.TenantIDEQ(p.TenantID),
		).
		SetName(p.Name).
		SetAddress(p.Address).
		SetPort(p.Port).
		SetStatus(p.Status).
		SetAgentCount(p.AgentCount).
		SetVersion(p.Version).
		SetNillableLastSeen(p.LastSeen).
		Save(ctx)
	return err
}

func (s *pgStore) DeleteProxy(ctx context.Context, id, tenantID string) error {
	_, err := s.entClient.AgentProxy.Delete().Where(
		entAgentProxy.IDEQ(id),
		entAgentProxy.TenantIDEQ(tenantID),
	).Exec(ctx)
	return err
}

// ——— Tasks (ent 链式调用) ———

func (s *pgStore) CreateTask(ctx context.Context, agentID string, req *TaskRequest) error {
	if req.TaskID == "" {
		req.TaskID = uuid.New().String()
	}
	tenantID := "default"
	now := time.Now()
	err := s.entClient.AgentTask.Create().
		SetID(req.TaskID).
		SetTenantID(tenantID).
		SetAgentID(agentID).
		SetAction(req.Action).
		SetScript(req.Script).
		SetParams(req.Params).
		SetTimeout(req.Timeout).
		SetRunAs(req.RunAs).
		SetStatus(string(TaskStatusPending)).
		SetFileID(req.FileID).
		SetFileURL(req.FileURL).
		SetFilePath(req.FilePath).
		SetExpectedChecksum(req.ExpectedChecksum).
		SetChecksumAlgo(req.ChecksumAlgo).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Exec(ctx)
	return err
}

func (s *pgStore) ListTasks(ctx context.Context, agentID string) ([]*TaskInfo, error) {
	rows, err := s.entClient.AgentTask.Query().
		Where(entAgentTask.AgentIDEQ(agentID)).
		Order(ent.Desc(entAgentTask.FieldCreatedAt)).
		Limit(100).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*TaskInfo, 0, len(rows))
	for _, row := range rows {
		ti := &TaskInfo{
			TaskRequest: TaskRequest{
				TaskID:  row.ID,
				Action:  row.Action,
				Script:  row.Script,
				Params:  row.Params,
				Timeout: row.Timeout,
				RunAs:   row.RunAs,
			},
			Status:    TaskStatus(row.Status),
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		}
		if row.Output != "" || row.ExitCode != 0 || row.DurationMs != 0 || row.Error != "" {
			ti.Response = &TaskResponse{
				TaskID:   row.ID,
				Output:   row.Output,
				ExitCode: row.ExitCode,
				Duration: row.DurationMs,
				Error:    row.Error,
			}
		}
		out = append(out, ti)
	}
	return out, nil
}

func (s *pgStore) CompleteTask(ctx context.Context, agentID, taskID string, resp *TaskResponse) error {
	st := string(TaskStatusCompleted)
	if !resp.Success {
		st = string(TaskStatusFailed)
	}
	now := time.Now()
	_, err := s.entClient.AgentTask.Update().
		Where(
			entAgentTask.IDEQ(taskID),
			entAgentTask.AgentIDEQ(agentID),
		).
		SetStatus(st).
		SetOutput(resp.Output).
		SetExitCode(resp.ExitCode).
		SetDurationMs(resp.Duration).
		SetError(resp.Error).
		SetUpdatedAt(now).
		Save(ctx)
	return err
}

// ——— Helper: ent row → domain model ———

func entRowToAgentHost(row *ent.AgentHost) *AgentHost {
	h := &AgentHost{
		ID:            row.ID,
		TenantID:      row.TenantID,
		Fingerprint:   row.Fingerprint,
		Channels:      row.Channels,
		Name:          row.Name,
		Type:          AgentType(row.Type),
		Address:       row.Address,
		Port:          row.Port,
		OSType:        row.OsType,
		OSVersion:     row.OsVersion,
		AuthType:      row.AuthType,
		SSHUser:       row.SSHUser,
		Status:        AgentStatus(row.Status),
		ProxyID:       row.ProxyID,
		InstallMethod: InstallMethod(row.InstallMethod),
		Version:       row.Version,
		CPU:           row.CPU,
		Memory:        row.Memory,
		Disk:          row.Disk,
		Hostname:      row.Hostname,
		Labels:        row.Labels,
		Environment:   row.Environment,
		IPs:           row.Ips,
		Capabilities:  row.Capabilities,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if !row.LastSeen.IsZero() {
		h.LastSeen = &row.LastSeen
	}
	if !row.LastHeartbeat.IsZero() {
		h.LastHeartbeat = &row.LastHeartbeat
	}
	if !row.FirstSeen.IsZero() {
		h.FirstSeen = &row.FirstSeen
	}
	return h
}

func entRowToProxy(row *ent.AgentProxy) *ProxyInfo {
	p := &ProxyInfo{
		ID:         row.ID,
		TenantID:   row.TenantID,
		Name:       row.Name,
		Address:    row.Address,
		Port:       row.Port,
		Status:     row.Status,
		AgentCount: row.AgentCount,
		Version:    row.Version,
		CreatedAt:  row.CreatedAt,
	}
	if !row.LastSeen.IsZero() {
		p.LastSeen = &row.LastSeen
	}
	return p
}

func toJSON(v any) []byte {
	if v == nil {
		return []byte("null")
	}
	b, _ := json.Marshal(v)
	return b
}
