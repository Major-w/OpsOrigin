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
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	Name            string    `json:"name"`
	RiskLevel       string    `json:"risk_level"` // low/medium/high/critical
	ResponseHours   int       `json:"response_hours"`
	ResolutionHours int       `json:"resolution_hours"`
	EscalationHours int       `json:"escalation_hours"`
	EscalationTo    string    `json:"escalation_to,omitempty"` // 升级通知角色
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
		response_hours INT DEFAULT 0, resolution_hours INT DEFAULT 0,
		escalation_hours INT DEFAULT 0, escalation_to TEXT DEFAULT '',
		enabled BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
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
		s.db.Exec(`INSERT INTO itsm_sla_policies(id,tenant_id,name,risk_level,response_hours,resolution_hours,escalation_hours,escalation_to,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			p.ID, p.TenantID, p.Name, p.RiskLevel, p.ResponseHours, p.ResolutionHours, p.EscalationHours, p.EscalationTo, p.Enabled, p.CreatedAt, p.UpdatedAt)
	}
}

func (s *Store) List(ctx context.Context, tenantID string) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,name,risk_level,response_hours,resolution_hours,escalation_hours,escalation_to,enabled,created_at,updated_at FROM itsm_sla_policies WHERE tenant_id=$1 ORDER BY risk_level`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.RiskLevel, &p.ResponseHours, &p.ResolutionHours, &p.EscalationHours, &p.EscalationTo, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Update(ctx context.Context, p *Policy) error {
	p.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE itsm_sla_policies SET name=$1,response_hours=$2,resolution_hours=$3,escalation_hours=$4,escalation_to=$5,enabled=$6,updated_at=$7 WHERE id=$8 AND tenant_id=$9`,
		p.Name, p.ResponseHours, p.ResolutionHours, p.EscalationHours, p.EscalationTo, p.Enabled, p.UpdatedAt, p.ID, p.TenantID)
	return err
}

func (s *Store) GetByRisk(ctx context.Context, tenantID, riskLevel string) (*Policy, error) {
	var p Policy
	err := s.db.QueryRowContext(ctx, `SELECT id,tenant_id,name,risk_level,response_hours,resolution_hours,escalation_hours,escalation_to,enabled,created_at,updated_at FROM itsm_sla_policies WHERE tenant_id=$1 AND risk_level=$2 AND enabled=true`, tenantID, riskLevel).
		Scan(&p.ID, &p.TenantID, &p.Name, &p.RiskLevel, &p.ResponseHours, &p.ResolutionHours, &p.EscalationHours, &p.EscalationTo, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

// RegisterRoutes registers SLA HTTP endpoints
func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/sla/policies", s.handleList)
	mux.HandleFunc("PUT /api/v1/itsm/sla/policies/{id}", s.handleUpdate)
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	policies, err := s.List(r.Context(), tenant)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, policies)
}

func (s *Store) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	p.ID = id
	p.TenantID = tenant
	if err := s.Update(r.Context(), &p); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
