package sla

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// Policy SLA 策略定义
type Policy struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	Name            string `json:"name"`
	RiskLevel       string `json:"risk_level"`           // low/medium/high/critical
	ProcessID       string `json:"process_id,omitempty"` // 绑定流程: 非空时按流程优先匹配 SLA
	ResponseHours   int    `json:"response_hours"`
	ResolutionHours int    `json:"resolution_hours"`
	EscalationHours int    `json:"escalation_hours"`
	EscalationTo    string `json:"escalation_to,omitempty"` // 升级通知角色/负责人
	// 多级升级策略: 按时间阈值逐级升级
	EscalationLevels []EscalationLevel `json:"escalation_levels,omitempty"`
	Enabled          bool              `json:"enabled"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// EscalationLevel 单级升级配置
type EscalationLevel struct {
	Level        int    `json:"level"`         // 升级级别 (1, 2, 3...)
	Hours        int    `json:"hours"`         // 触发小时数(从创建算起)
	NotifyTo     string `json:"notify_to"`     // 通知对象(角色/用户)
	NotifyMethod string `json:"notify_method"` // 通知方式: email/webhook/both
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	s.seed()
	return s
}

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_sla_policies (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, risk_level TEXT NOT NULL,
		process_id TEXT DEFAULT '',
		response_hours INT DEFAULT 0, resolution_hours INT DEFAULT 0,
		escalation_hours INT DEFAULT 0, escalation_to TEXT DEFAULT '',
		escalation_levels JSONB DEFAULT '[]'::jsonb,
		enabled BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`ALTER TABLE itsm_sla_policies ADD COLUMN IF NOT EXISTS process_id TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE itsm_sla_policies ADD COLUMN IF NOT EXISTS escalation_levels JSONB DEFAULT '[]'::jsonb`)
}

func (s *Store) seed() {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM itsm_sla_policies`).Scan(&count)
	if count > 0 {
		return
	}
	policies := []Policy{
		{RiskLevel: "low", ResponseHours: 72, ResolutionHours: 168, EscalationHours: 96, Name: "低风险 SLA"},
		{RiskLevel: "medium", ResponseHours: 48, ResolutionHours: 96, EscalationHours: 72, Name: "中风险 SLA"},
		{RiskLevel: "high", ResponseHours: 24, ResolutionHours: 48, EscalationHours: 36, Name: "高风险 SLA"},
		{RiskLevel: "critical", ResponseHours: 4, ResolutionHours: 8, EscalationHours: 2, Name: "紧急 SLA"},
	}
	for _, p := range policies {
		p.ID = uuid.New().String()
		p.TenantID = "default"
		p.Enabled = true
		p.CreatedAt = time.Now()
		p.UpdatedAt = p.CreatedAt
		s.db.Exec(`INSERT INTO itsm_sla_policies(id,tenant_id,name,risk_level,process_id,response_hours,resolution_hours,escalation_hours,escalation_to,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			p.ID, p.TenantID, p.Name, p.RiskLevel, p.ProcessID, p.ResponseHours, p.ResolutionHours, p.EscalationHours, p.EscalationTo, p.Enabled, p.CreatedAt, p.UpdatedAt)
	}
}

const slaCols = `id,tenant_id,name,risk_level,process_id,response_hours,resolution_hours,escalation_hours,escalation_to,escalation_levels,enabled,created_at,updated_at`

func (s *Store) List(ctx context.Context, tenantID string) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+slaCols+` FROM itsm_sla_policies WHERE tenant_id=$1 ORDER BY risk_level, process_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		var p Policy
		var escalationJSON []byte
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.RiskLevel, &p.ProcessID, &p.ResponseHours, &p.ResolutionHours, &p.EscalationHours, &p.EscalationTo, &escalationJSON, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if len(escalationJSON) > 0 {
			json.Unmarshal(escalationJSON, &p.EscalationLevels)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Update(ctx context.Context, p *Policy) error {
	p.UpdatedAt = time.Now()
	escalationJSON, _ := json.Marshal(p.EscalationLevels)
	_, err := s.db.ExecContext(ctx, `UPDATE itsm_sla_policies SET name=$1,process_id=$2,response_hours=$3,resolution_hours=$4,escalation_hours=$5,escalation_to=$6,escalation_levels=$7,enabled=$8,updated_at=$9 WHERE id=$10 AND tenant_id=$11`,
		p.Name, p.ProcessID, p.ResponseHours, p.ResolutionHours, p.EscalationHours, p.EscalationTo, escalationJSON, p.Enabled, p.UpdatedAt, p.ID, p.TenantID)
	return err
}

// Create 新建一条 SLA 策略（自定义/风险皆可）。自动生成 ID 与时间戳。
func (s *Store) Create(ctx context.Context, p *Policy) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if p.RiskLevel == "" {
		p.RiskLevel = "custom"
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	escalationJSON, _ := json.Marshal(p.EscalationLevels)
	_, err := s.db.ExecContext(ctx, `INSERT INTO itsm_sla_policies(id,tenant_id,name,risk_level,process_id,response_hours,resolution_hours,escalation_hours,escalation_to,escalation_levels,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		p.ID, p.TenantID, p.Name, p.RiskLevel, p.ProcessID, p.ResponseHours, p.ResolutionHours, p.EscalationHours, p.EscalationTo, escalationJSON, p.Enabled, p.CreatedAt, p.UpdatedAt)
	return err
}

// Delete 删除一条 SLA 策略（按 ID + 租户）。
func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_sla_policies WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *Store) GetByRisk(ctx context.Context, tenantID, riskLevel string) (*Policy, error) {
	var p Policy
	var escalationJSON []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+slaCols+` FROM itsm_sla_policies WHERE tenant_id=$1 AND risk_level=$2 AND enabled=true AND process_id=''`, tenantID, riskLevel).
		Scan(&p.ID, &p.TenantID, &p.Name, &p.RiskLevel, &p.ProcessID, &p.ResponseHours, &p.ResolutionHours, &p.EscalationHours, &p.EscalationTo, &escalationJSON, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if len(escalationJSON) > 0 {
		json.Unmarshal(escalationJSON, &p.EscalationLevels)
	}
	return &p, err
}

// GetByProcessID 按流程绑定查询 SLA 策略(流程级 SLA, 优先级高于风险级)。
func (s *Store) GetByProcessID(ctx context.Context, tenantID, processID string) (*Policy, error) {
	if processID == "" {
		return nil, nil
	}
	var p Policy
	var escalationJSON []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+slaCols+` FROM itsm_sla_policies WHERE tenant_id=$1 AND process_id=$2 AND enabled=true LIMIT 1`, tenantID, processID).
		Scan(&p.ID, &p.TenantID, &p.Name, &p.RiskLevel, &p.ProcessID, &p.ResponseHours, &p.ResolutionHours, &p.EscalationHours, &p.EscalationTo, &escalationJSON, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if len(escalationJSON) > 0 {
		json.Unmarshal(escalationJSON, &p.EscalationLevels)
	}
	return &p, err
}

// RegisterRoutes registers SLA HTTP endpoints
func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/sla/policies", s.handleList)
	mux.HandleFunc("POST /api/v1/itsm/sla/policies", s.handleCreate)
	mux.HandleFunc("PUT /api/v1/itsm/sla/policies/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/itsm/sla/policies/{id}", s.handleDelete)
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	policies, err := s.List(r.Context(), tenant)
	if err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 200, policies)
}

func (s *Store) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	var p Policy
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	p.ID = id
	p.TenantID = tenant
	if err := s.Update(r.Context(), &p); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 200, p)
}

func (s *Store) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	var p Policy
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	p.TenantID = tenant
	if err := s.Create(r.Context(), &p); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 201, p)
}

func (s *Store) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	if err := s.Delete(r.Context(), id, tenant); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 204, nil)
}
