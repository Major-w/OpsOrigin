// Package changeassess 变更影响评估的变更请求持久化层。
//
// 影响分析计算本身复用 CMDB 既有能力（GET /api/v1/cmdb/impact/{id}，
// 基于真实 CI 关系图 BFS 下游影响）。本包只负责「变更请求」本身的增删改查，
// 让变更影响评估页面从 mock 切换为真实数据。
package changeassess

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// ChangeRequest 一条变更请求。
type ChangeRequest struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Title       string    `json:"title"`
	Type        string    `json:"type"`     // infrastructure | application | network | database
	Status      string    `json:"status"`   // draft | assessing | approved | rejected
	SourceCI    string    `json:"source_ci"` // 受变更的源 CI id
	RiskScore   int       `json:"risk_score"`
	ImpactScope string    `json:"impact_scope"` // JSON 数组：受影响业务名（可选，评估后回填）
	ProcessID   string    `json:"process_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	return s
}

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS cmdb_change_requests (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		title TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'infrastructure',
		status TEXT NOT NULL DEFAULT 'draft', source_ci TEXT NOT NULL DEFAULT '',
		risk_score INT DEFAULT 0, impact_scope TEXT DEFAULT '[]',
		process_id TEXT DEFAULT '',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

const cols = `id,tenant_id,title,type,status,source_ci,risk_score,impact_scope,process_id,created_at,updated_at`

func (s *Store) List(ctx context.Context, tenantID string) ([]ChangeRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM cmdb_change_requests WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeRequest
	for rows.Next() {
		var c ChangeRequest
		var scope []byte
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Title, &c.Type, &c.Status, &c.SourceCI, &c.RiskScore, &scope, &c.ProcessID, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if len(scope) > 0 {
			var arr []string
			if json.Unmarshal(scope, &arr) == nil {
				c.ImpactScope = string(scope)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, c *ChangeRequest) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.TenantID == "" {
		c.TenantID = "default"
	}
	if c.Status == "" {
		c.Status = "draft"
	}
	if c.ImpactScope == "" {
		c.ImpactScope = "[]"
	}
	now := time.Now()
	c.CreatedAt = now
	c.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO cmdb_change_requests(id,tenant_id,title,type,status,source_ci,risk_score,impact_scope,process_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		c.ID, c.TenantID, c.Title, c.Type, c.Status, c.SourceCI, c.RiskScore, c.ImpactScope, c.ProcessID, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) Update(ctx context.Context, c *ChangeRequest) error {
	c.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE cmdb_change_requests SET title=$1,type=$2,status=$3,source_ci=$4,risk_score=$5,impact_scope=$6,process_id=$7,updated_at=$8 WHERE id=$9 AND tenant_id=$10`,
		c.Title, c.Type, c.Status, c.SourceCI, c.RiskScore, c.ImpactScope, c.ProcessID, c.UpdatedAt, c.ID, c.TenantID)
	return err
}

func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cmdb_change_requests WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// RegisterRoutes 注册变更请求 CRUD 路由。
func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cmdb/change-requests", s.handleList)
	mux.HandleFunc("POST /api/v1/cmdb/change-requests", s.handleCreate)
	mux.HandleFunc("PUT /api/v1/cmdb/change-requests/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/cmdb/change-requests/{id}", s.handleDelete)
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
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, httputil.EmptySliceIfNil(list))
}

func (s *Store) handleCreate(w http.ResponseWriter, r *http.Request) {
	var c ChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	c.TenantID = tenantOf(r)
	if err := s.Create(r.Context(), &c); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, c)
}

func (s *Store) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var c ChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	c.ID = id
	c.TenantID = tenantOf(r)
	if err := s.Update(r.Context(), &c); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, c)
}

func (s *Store) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Delete(r.Context(), id, tenantOf(r)); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 204, nil)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
