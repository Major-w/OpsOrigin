package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// ServiceItem 服务目录项
type ServiceItem struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Category    string    `json:"category,omitempty"`   // change/incident/problem/request
	ProcessID   string    `json:"process_id,omitempty"` // 关联流程
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	s.seed()
	return s
}

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_catalog (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, description TEXT DEFAULT '',
		category TEXT DEFAULT '', process_id TEXT DEFAULT '',
		enabled BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

func (s *Store) seed() {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM itsm_catalog`).Scan(&count)
	if count > 0 {
		return
	}
	items := []ServiceItem{
		{Name: "服务器重启", Description: "重启指定服务器或虚拟机", Category: "change"},
		{Name: "数据库配置修改", Description: "修改数据库参数或执行 DDL", Category: "change"},
		{Name: "SSL 证书续期", Description: "续费或更新 SSL 证书", Category: "change"},
		{Name: "DNS 记录变更", Description: "添加/修改/删除 DNS 记录", Category: "change"},
		{Name: "防火墙规则变更", Description: "修改防火墙白名单或策略", Category: "change"},
		{Name: "账号权限申请", Description: "申请系统或数据库访问权限", Category: "request"},
	}
	for _, it := range items {
		it.ID = uuid.New().String()
		it.TenantID = "default"
		it.Enabled = true
		it.CreatedAt = time.Now()
		it.UpdatedAt = it.CreatedAt
		s.db.Exec(`INSERT INTO itsm_catalog(id,tenant_id,name,description,category,process_id,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			it.ID, it.TenantID, it.Name, it.Description, it.Category, it.ProcessID, it.Enabled, it.CreatedAt, it.UpdatedAt)
	}
}

func (s *Store) List(ctx context.Context, tenantID string) ([]ServiceItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,name,description,category,process_id,enabled,created_at,updated_at FROM itsm_catalog WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceItem
	for rows.Next() {
		var it ServiceItem
		if err := rows.Scan(&it.ID, &it.TenantID, &it.Name, &it.Description, &it.Category, &it.ProcessID, &it.Enabled, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, it *ServiceItem) error {
	it.ID = uuid.New().String()
	it.CreatedAt = time.Now()
	it.UpdatedAt = it.CreatedAt
	if it.TenantID == "" {
		it.TenantID = "default"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO itsm_catalog(id,tenant_id,name,description,category,process_id,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		it.ID, it.TenantID, it.Name, it.Description, it.Category, it.ProcessID, it.Enabled, it.CreatedAt, it.UpdatedAt)
	return err
}

func (s *Store) Update(ctx context.Context, it *ServiceItem) error {
	it.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE itsm_catalog SET name=$1,description=$2,category=$3,process_id=$4,enabled=$5,updated_at=$6 WHERE id=$7 AND tenant_id=$8`,
		it.Name, it.Description, it.Category, it.ProcessID, it.Enabled, it.UpdatedAt, it.ID, it.TenantID)
	return err
}

func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_catalog WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/catalog", s.handleList)
	mux.HandleFunc("POST /api/v1/itsm/catalog", s.handleCreate)
	mux.HandleFunc("PUT /api/v1/itsm/catalog/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/itsm/catalog/{id}", s.handleDelete)
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	items, err := s.List(r.Context(), tenant)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, items)
}

func (s *Store) handleCreate(w http.ResponseWriter, r *http.Request) {
	var it ServiceItem
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	it.TenantID = r.Header.Get("X-Tenant-ID")
	if it.TenantID == "" {
		it.TenantID = "default"
	}
	if err := s.Create(r.Context(), &it); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, it)
}

func (s *Store) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	var it ServiceItem
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	it.ID = id
	it.TenantID = tenant
	if err := s.Update(r.Context(), &it); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, it)
}

func (s *Store) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	if err := s.Delete(r.Context(), id, tenant); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
