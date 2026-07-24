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
)

type pgStore struct {
	db *sql.DB
}

func newPGStore(db *sql.DB) *pgStore {
	s := &pgStore{db: db}
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

func toJSON(v any) []byte {
	if v == nil { return []byte("null") }
	b, _ := json.Marshal(v)
	return b
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil { return sql.NullTime{} }
	return sql.NullTime{Time: *t, Valid: true}
}

// ——— Agent CRUD ———

func (s *pgStore) Register(ctx context.Context, h *AgentHost) error {
	h.Fingerprint = GenerateFingerprint(h.TenantID, h.Address)
	ch := channelFromType(h.Type)
	if existing, _ := s.GetByFingerprint(ctx, h.Fingerprint, h.TenantID); existing != nil {
		h.ID = existing.ID
		if h.FirstSeen == nil { h.FirstSeen = existing.FirstSeen }
		if h.CreatedAt.IsZero() { h.CreatedAt = existing.CreatedAt }
		if ch != "" { h.Channels = mergeChannels(existing.Channels, ch) }
	}
	if h.ID == "" { h.ID = uuid.New().String() }
	if len(h.Channels) == 0 { h.Channels = []string{ch} }
	if h.TenantID == "" { h.TenantID = "default" }
	now := time.Now()
	if h.CreatedAt.IsZero() { h.CreatedAt = now }
	h.UpdatedAt = now
	if h.Status == "" { h.Status = "offline" }
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_hosts(id,tenant_id,fingerprint,name,type,address,port,os_type,os_version,auth_type,ssh_user,status,proxy_id,install_method,version,cpu,memory,disk,hostname,labels,environment,capabilities,ips,channels,last_seen,last_heartbeat,first_seen,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
		ON CONFLICT(id) DO UPDATE SET fingerprint=$3,name=$4,type=$5,address=$6,port=$7,os_type=$8,os_version=$9,auth_type=$10,ssh_user=$11,status=$12,proxy_id=$13,install_method=$14,version=$15,cpu=$16,memory=$17,disk=$18,hostname=$19,labels=$20,environment=$21,capabilities=$22,ips=$23,channels=$24,last_seen=$25,last_heartbeat=$26,updated_at=$30`,
		h.ID, h.TenantID, h.Fingerprint, h.Name, string(h.Type), h.Address, h.Port, h.OSType, h.OSVersion, h.AuthType, h.SSHUser, string(h.Status), h.ProxyID, string(h.InstallMethod), h.Version, h.CPU, h.Memory, h.Disk, h.Hostname, toJSON(h.Labels), h.Environment, toJSON(h.Capabilities), toJSON([]string{}), toJSON(h.Channels), nullTime(h.LastSeen), nullTime(h.LastHeartbeat), nullTime(h.FirstSeen), h.CreatedAt, h.UpdatedAt)
	return err
}

func (s *pgStore) GetByFingerprint(ctx context.Context, fp, tenantID string) (*AgentHost, error) {
	h := &AgentHost{}
	var caps, ips, labels, channels []byte
	var typ, st, im string
	var lastSeen, lastHb, firstSeen sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,fingerprint,name,type,address,port,os_type,os_version,auth_type,ssh_user,status,proxy_id,install_method,version,cpu,memory,disk,hostname,labels,environment,capabilities,ips,channels,last_seen,last_heartbeat,first_seen,created_at,updated_at
		FROM agent_hosts WHERE fingerprint=$1 AND tenant_id=$2`, fp, tenantID).
		Scan(&h.ID, &h.TenantID, &h.Fingerprint, &h.Name, &typ, &h.Address, &h.Port, &h.OSType, &h.OSVersion, &h.AuthType, &h.SSHUser, &st, &h.ProxyID, &im, &h.Version, &h.CPU, &h.Memory, &h.Disk, &h.Hostname, &labels, &h.Environment, &caps, &ips, &channels, &lastSeen, &lastHb, &firstSeen, &h.CreatedAt, &h.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	h.Type, h.Status, h.InstallMethod = AgentType(typ), AgentStatus(st), InstallMethod(im)
	_ = json.Unmarshal(caps, &h.Capabilities)
	_ = json.Unmarshal(labels, &h.Labels)
	_ = json.Unmarshal(channels, &h.Channels)
	if lastSeen.Valid { h.LastSeen = &lastSeen.Time }
	if lastHb.Valid { h.LastHeartbeat = &lastHb.Time }
	if firstSeen.Valid { h.FirstSeen = &firstSeen.Time }
	return h, nil
}

func (s *pgStore) Get(ctx context.Context, id, tenantID string) (*AgentHost, error) {
	h := &AgentHost{}
	var caps, ips, labels []byte
	var typ, st, im string
	var lastSeen, lastHb, firstSeen sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,type,address,port,os_type,os_version,auth_type,ssh_user,status,proxy_id,install_method,version,cpu,memory,disk,hostname,labels,environment,capabilities,ips,last_seen,last_heartbeat,first_seen,created_at,updated_at
		FROM agent_hosts WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&h.ID, &h.TenantID, &h.Name, &typ, &h.Address, &h.Port, &h.OSType, &h.OSVersion, &h.AuthType, &h.SSHUser, &st, &h.ProxyID, &im, &h.Version, &h.CPU, &h.Memory, &h.Disk, &h.Hostname, &labels, &h.Environment, &caps, &ips, &lastSeen, &lastHb, &firstSeen, &h.CreatedAt, &h.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	h.Type, h.Status, h.InstallMethod = AgentType(typ), AgentStatus(st), InstallMethod(im)
	_ = json.Unmarshal(caps, &h.Capabilities)
	_ = json.Unmarshal(labels, &h.Labels)
	if lastSeen.Valid { h.LastSeen = &lastSeen.Time }
	if lastHb.Valid { h.LastHeartbeat = &lastHb.Time }
	if firstSeen.Valid { h.FirstSeen = &firstSeen.Time }
	return h, nil
}

func (s *pgStore) List(ctx context.Context, tenantID string) ([]*AgentHost, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,type,address,port,os_type,os_version,auth_type,ssh_user,status,proxy_id,install_method,version,cpu,memory,disk,hostname,labels,environment,capabilities,ips,last_seen,last_heartbeat,first_seen,created_at,updated_at
		FROM agent_hosts WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*AgentHost
	for rows.Next() {
		h := &AgentHost{}
		var caps, ips, labels []byte
		var typ, st, im string
		var lastSeen, lastHb, firstSeen sql.NullTime
		if err := rows.Scan(&h.ID, &h.TenantID, &h.Name, &typ, &h.Address, &h.Port, &h.OSType, &h.OSVersion, &h.AuthType, &h.SSHUser, &st, &h.ProxyID, &im, &h.Version, &h.CPU, &h.Memory, &h.Disk, &h.Hostname, &labels, &h.Environment, &caps, &ips, &lastSeen, &lastHb, &firstSeen, &h.CreatedAt, &h.UpdatedAt); err != nil { return nil, err }
		h.Type, h.Status, h.InstallMethod = AgentType(typ), AgentStatus(st), InstallMethod(im)
		_ = json.Unmarshal(caps, &h.Capabilities)
		_ = json.Unmarshal(labels, &h.Labels)
		if lastSeen.Valid { h.LastSeen = &lastSeen.Time }
		if lastHb.Valid { h.LastHeartbeat = &lastHb.Time }
		if firstSeen.Valid { h.FirstSeen = &firstSeen.Time }
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *pgStore) Update(ctx context.Context, h *AgentHost) error {
	h.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_hosts SET name=$2,type=$3,address=$4,port=$5,os_type=$6,os_version=$7,auth_type=$8,ssh_user=$9,status=$10,proxy_id=$11,install_method=$12,version=$13,cpu=$14,memory=$15,disk=$16,hostname=$17,labels=$18,environment=$19,capabilities=$20,last_seen=$21,last_heartbeat=$22,first_seen=$23,updated_at=$24 WHERE id=$1 AND tenant_id=$25`,
		h.ID, h.Name, string(h.Type), h.Address, h.Port, h.OSType, h.OSVersion, h.AuthType, h.SSHUser, string(h.Status), h.ProxyID, string(h.InstallMethod), h.Version, h.CPU, h.Memory, h.Disk, h.Hostname, toJSON(h.Labels), h.Environment, toJSON(h.Capabilities), nullTime(h.LastSeen), nullTime(h.LastHeartbeat), nullTime(h.FirstSeen), h.UpdatedAt, h.TenantID)
	return err
}

func (s *pgStore) Delete(ctx context.Context, id, tenantID string) error {
	tx, _ := s.db.BeginTx(ctx, nil)
	if tx != nil {
		defer tx.Rollback()
		tx.ExecContext(ctx, `DELETE FROM agent_tasks WHERE agent_id=$1`, id)
		tx.ExecContext(ctx, `DELETE FROM agent_hosts WHERE id=$1 AND tenant_id=$2`, id, tenantID)
		return tx.Commit()
	}
	return s.db.QueryRowContext(ctx, `DELETE FROM agent_hosts WHERE id=$1 AND tenant_id=$2`, id, tenantID).Err()
}

// heartbeatBatch bundles heartbeat data for bulk upsert.
type heartbeatBatch struct {
	ID, TenantID, Version, OS, OSVersion, Hostname, CPU, Memory string
	LastSeen, LastHeartbeat                                     time.Time
}

// BulkHeartbeat bulk-upserts agent heartbeat records in a single INSERT … ON CONFLICT statement.
func (s *pgStore) BulkHeartbeat(ctx context.Context, batch []heartbeatBatch) error {
	if len(batch) == 0 {
		return nil
	}
	// Build VALUES ($1,$2,…), ($N+1,$N+2,…), …
	// Template: placeholder idx = row*12 + col (0-based)
	vals := make([]string, 0, len(batch))
	args := make([]any, 0, len(batch)*12)
	for i, hb := range batch {
		base := i * 12
		vals = append(vals, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6,
			base+7, base+8, base+9, base+10, base+11, base+12))
		args = append(args,
			hb.ID, hb.TenantID, hb.Version, hb.OS, hb.OSVersion,
			hb.Hostname, hb.CPU, hb.Memory,
			hb.LastSeen, hb.LastHeartbeat,
			time.Now(), // last_seen (duplicate for ON CONFLICT UPDATE)
			time.Now(), // updated_at
		)
	}
	query := `INSERT INTO agent_hosts(id,tenant_id,version,os_type,os_version,hostname,cpu,memory,last_seen,last_heartbeat,updated_at)
VALUES ` + strings.Join(vals, ",") + `
ON CONFLICT(id) DO UPDATE SET
  last_seen=EXCLUDED.last_seen,
  last_heartbeat=EXCLUDED.last_heartbeat,
  status='online',
  updated_at=NOW()`
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *pgStore) Heartbeat(ctx context.Context, id, tenantID string, req *HeartbeatRequest) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_hosts SET status='online',version=$3,cpu=$4,memory=$5,os_type=$6,os_version=$7,capabilities=$8,ips=$9,last_seen=$10,updated_at=$11 WHERE id=$1 AND tenant_id=$2`,
		id, tenantID, req.AgentVersion, req.CPU, req.Memory, req.OS, req.OSVersion, toJSON(req.Capabilities), toJSON(req.IPs), now, now)
	return err
}

func (s *pgStore) MarkOfflineAgents(ctx context.Context, tenantID string, threshold time.Duration) (int, error) {
	cutoff := time.Now().Add(-threshold)
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_hosts SET status='offline',updated_at=$3 WHERE tenant_id=$1 AND status='online' AND last_seen < $2`,
		tenantID, cutoff, time.Now())
	if err != nil { return 0, err }
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ——— Proxy CRUD ———

func (s *pgStore) RegisterProxy(ctx context.Context, p *ProxyInfo) error {
	if p.ID == "" { p.ID = uuid.New().String() }
	if p.TenantID == "" { p.TenantID = "default" }
	if p.CreatedAt.IsZero() { p.CreatedAt = time.Now() }
	if p.Status == "" { p.Status = "offline" }
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_proxies(id,tenant_id,name,address,port,status,agent_count,version,last_seen,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET name=$3,address=$4,port=$5,status=$6,agent_count=$7,version=$8,last_seen=$9`,
		p.ID, p.TenantID, p.Name, p.Address, p.Port, p.Status, p.AgentCount, p.Version, nullTime(p.LastSeen), p.CreatedAt)
	return err
}

func (s *pgStore) GetProxy(ctx context.Context, id, tenantID string) (*ProxyInfo, error) {
	p := &ProxyInfo{}
	var lastSeen sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,address,port,status,agent_count,version,last_seen,created_at FROM agent_proxies WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&p.ID, &p.TenantID, &p.Name, &p.Address, &p.Port, &p.Status, &p.AgentCount, &p.Version, &lastSeen, &p.CreatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	if lastSeen.Valid { p.LastSeen = &lastSeen.Time }
	return p, nil
}

func (s *pgStore) ListProxies(ctx context.Context, tenantID string) ([]*ProxyInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,address,port,status,agent_count,version,last_seen,created_at FROM agent_proxies WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*ProxyInfo
	for rows.Next() {
		p := &ProxyInfo{}
		var lastSeen sql.NullTime
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.Address, &p.Port, &p.Status, &p.AgentCount, &p.Version, &lastSeen, &p.CreatedAt); err != nil { return nil, err }
		if lastSeen.Valid { p.LastSeen = &lastSeen.Time }
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdateProxy(ctx context.Context, p *ProxyInfo) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_proxies SET name=$2,address=$3,port=$4,status=$5,agent_count=$6,version=$7,last_seen=$8 WHERE id=$1 AND tenant_id=$9`,
		p.ID, p.Name, p.Address, p.Port, p.Status, p.AgentCount, p.Version, nullTime(p.LastSeen), p.TenantID)
	return err
}

func (s *pgStore) DeleteProxy(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_proxies WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ——— Tasks ———

func (s *pgStore) CreateTask(ctx context.Context, agentID string, req *TaskRequest) error {
	if req.TaskID == "" { req.TaskID = uuid.New().String() }
	tenantID := "default"
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_tasks(id,tenant_id,agent_id,action,script,params,timeout,run_as,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		req.TaskID, tenantID, agentID, req.Action, req.Script, toJSON(req.Params), req.Timeout, req.RunAs, string(TaskStatusPending), now, now)
	return err
}

func (s *pgStore) ListTasks(ctx context.Context, agentID string) ([]*TaskInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,action,script,params,timeout,run_as,status,output,exit_code,duration_ms,error,created_at,updated_at
		FROM agent_tasks WHERE agent_id=$1 ORDER BY created_at DESC LIMIT 100`, agentID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*TaskInfo
	for rows.Next() {
		ti := &TaskInfo{}
		var params []byte
		var action, scr, st, rAs, outStr, errStr sql.NullString
		var to, exitCode, dur sql.NullInt64
		if err := rows.Scan(&ti.TaskID, &action, &scr, &params, &to, &rAs, &st, &outStr, &exitCode, &dur, &errStr, &ti.CreatedAt, &ti.UpdatedAt); err != nil {
			return nil, err
		}
		if action.Valid { ti.Action = action.String }
		if scr.Valid { ti.Script = scr.String }
		if rAs.Valid { ti.RunAs = rAs.String }
		if st.Valid { ti.Status = TaskStatus(st.String) }
		if to.Valid { ti.Timeout = int(to.Int64) }
		_ = json.Unmarshal(params, &ti.Params)
		if outStr.Valid || exitCode.Valid || dur.Valid || errStr.Valid {
			ti.Response = &TaskResponse{}
			if outStr.Valid { ti.Response.Output = outStr.String }
			if exitCode.Valid { ti.Response.ExitCode = int(exitCode.Int64) }
			if dur.Valid { ti.Response.Duration = dur.Int64 }
			if errStr.Valid { ti.Response.Error = errStr.String }
		}
		out = append(out, ti)
	}
	return out, rows.Err()
}

func (s *pgStore) CompleteTask(ctx context.Context, agentID, taskID string, resp *TaskResponse) error {
	st := string(TaskStatusCompleted)
	if !resp.Success { st = string(TaskStatusFailed) }
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_tasks SET status=$3,output=$4,exit_code=$5,duration_ms=$6,error=$7,updated_at=$8 WHERE id=$1 AND agent_id=$2`,
		taskID, agentID, st, resp.Output, resp.ExitCode, resp.Duration, resp.Error, now)
	return err
}
