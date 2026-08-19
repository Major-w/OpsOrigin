// Package ola 提供 OLA（运营级别协议）独立管理：定义流程步骤的处理时限与超时升级策略。
//
// 与 SLA（面向风险/流程整体的响应解决时限）不同，OLA 聚焦「单个流程步骤」的处理时限——
// 当定时器步骤在 duration_hours 内未完成时先发升级通知给 escalate_to，再自动推进。
// 流程定时器步骤的 OlaEscalateTo 字段可与本策略联动：此处维护独立策略，供 UI 统一配置。
package ola

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// Policy OLA 策略定义。
type Policy struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	Name          string    `json:"name"`
	ProcessID     string    `json:"process_id"`     // 关联流程（可选，限定策略适用范围）
	StepID        string    `json:"step_id"`        // 关联流程步骤（可选，指定某定时器步骤）
	DurationHours int       `json:"duration_hours"` // OLA 处理时限（小时）
	EscalateTo    string    `json:"escalate_to"`    // 超时升级对象（角色/负责人/群组）
	NotifyChannel string    `json:"notify_channel"` // 通知渠道：email/wecom/dingtalk/feishu
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	s.seed()
	return s
}

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_ola_policies (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, process_id TEXT DEFAULT '', step_id TEXT DEFAULT '',
		duration_hours INT DEFAULT 24, escalate_to TEXT DEFAULT '',
		notify_channel TEXT DEFAULT 'email', enabled BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

func (s *Store) seed() {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM itsm_ola_policies`).Scan(&count)
	if count > 0 {
		return
	}
	seeds := []Policy{
		{Name: "一线处理时限", DurationHours: 4, EscalateTo: "一线组长", NotifyChannel: "wecom", Enabled: true},
		{Name: "二线升级时限", DurationHours: 8, EscalateTo: "二线值班", NotifyChannel: "dingtalk", Enabled: true},
	}
	for _, p := range seeds {
		p.ID = uuid.New().String()
		p.TenantID = "default"
		p.CreatedAt = time.Now()
		p.UpdatedAt = p.CreatedAt
		s.db.Exec(`INSERT INTO itsm_ola_policies(id,tenant_id,name,process_id,step_id,duration_hours,escalate_to,notify_channel,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			p.ID, p.TenantID, p.Name, p.ProcessID, p.StepID, p.DurationHours, p.EscalateTo, p.NotifyChannel, p.Enabled, p.CreatedAt, p.UpdatedAt)
	}
}

const cols = `id,tenant_id,name,process_id,step_id,duration_hours,escalate_to,notify_channel,enabled,created_at,updated_at`

func (s *Store) List(ctx context.Context, tenantID string) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM itsm_ola_policies WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.ProcessID, &p.StepID, &p.DurationHours, &p.EscalateTo, &p.NotifyChannel, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, p *Policy) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO itsm_ola_policies(id,tenant_id,name,process_id,step_id,duration_hours,escalate_to,notify_channel,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.ID, p.TenantID, p.Name, p.ProcessID, p.StepID, p.DurationHours, p.EscalateTo, p.NotifyChannel, p.Enabled, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) Update(ctx context.Context, p *Policy) error {
	p.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE itsm_ola_policies SET name=$1,process_id=$2,step_id=$3,duration_hours=$4,escalate_to=$5,notify_channel=$6,enabled=$7,updated_at=$8 WHERE id=$9 AND tenant_id=$10`,
		p.Name, p.ProcessID, p.StepID, p.DurationHours, p.EscalateTo, p.NotifyChannel, p.Enabled, p.UpdatedAt, p.ID, p.TenantID)
	return err
}

func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_ola_policies WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/ola/policies", s.handleList)
	mux.HandleFunc("POST /api/v1/itsm/ola/policies", s.handleCreate)
	mux.HandleFunc("PUT /api/v1/itsm/ola/policies/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/itsm/ola/policies/{id}", s.handleDelete)
}

func tenantOf(r *http.Request) string {
	t := r.Header.Get("X-Tenant-ID")
	if t == "" {
		return "default"
	}
	return t
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.List(r.Context(), tenantOf(r))
	if err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 200, httputil.EmptySliceIfNil(list))
}

func (s *Store) handleCreate(w http.ResponseWriter, r *http.Request) {
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		httputil.WriteJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	p.TenantID = tenantOf(r)
	if err := s.Create(r.Context(), &p); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 201, p)
}

func (s *Store) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		httputil.WriteJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	p.ID = id
	p.TenantID = tenantOf(r)
	if err := s.Update(r.Context(), &p); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 200, p)
}

func (s *Store) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Delete(r.Context(), id, tenantOf(r)); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 204, nil)
}
