package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// Audience 服务对象: 限定哪些用户 / 用户组可以在自助服务门户看到该目录项。
// 两个列表都为空 = 全员可见(不设限)。命中任一即可见(并集语义)。
type Audience struct {
	UserIDs  []string `json:"user_ids,omitempty"`
	GroupIDs []string `json:"group_ids,omitempty"`
}

// IsEmpty 未配置任何服务对象 → 全员可见。
func (a Audience) IsEmpty() bool { return len(a.UserIDs) == 0 && len(a.GroupIDs) == 0 }

// Allows 判断给定用户(及其所属用户组)是否在服务对象范围内。
func (a Audience) Allows(userID string, groupIDs []string) bool {
	if a.IsEmpty() {
		return true
	}
	for _, u := range a.UserIDs {
		if u != "" && u == userID {
			return true
		}
	}
	for _, g := range a.GroupIDs {
		for _, ug := range groupIDs {
			if g != "" && g == ug {
				return true
			}
		}
	}
	return false
}

// ServiceItem 服务目录项（支持多级分类：parent_id 为空=一级分类）
type ServiceItem struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Category    string    `json:"category,omitempty"`   // 继承的一级分类(change/incident/problem/request/release)，用于统计/分组
	ParentID    string    `json:"parent_id"`            // 父分类 ID；空字符串=一级分类
	Level       int       `json:"level"`                // 层级：1=一级分类
	SortOrder   int       `json:"sort_order"`           // 同级排序(升序)
	ProcessID   string    `json:"process_id,omitempty"` // 关联流程(申请时自动套用)
	FormScope   string    `json:"form_scope,omitempty"` // 关联表单作用域(如 ticket:default / catalog:<id> 自定义)
	Enabled     bool      `json:"enabled"`              // 上线/下线: 仅上线项出现在自助服务门户
	Audience    Audience  `json:"audience"`             // 服务对象(可见范围)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GroupResolver 返回某用户所属的用户组 ID 列表(由 cmd/core 注入 IAM 实现,
// 避免 catalog 反向依赖 iam 包)。未注入时按「无组」处理。
type GroupResolver func(ctx context.Context, tenantID, userID string) ([]string, error)

type Store struct {
	db     *sql.DB
	groups GroupResolver
}

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	s.seed()
	return s
}

// SetGroupResolver 注入用户组解析器(服务对象按组授权时需要)。
func (s *Store) SetGroupResolver(r GroupResolver) { s.groups = r }

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_catalog (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, description TEXT DEFAULT '',
		category TEXT DEFAULT '', process_id TEXT DEFAULT '',
		form_scope TEXT DEFAULT '',
		enabled BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS form_scope TEXT DEFAULT ''`)
	// 服务对象(可见范围): {user_ids:[], group_ids:[]}, 空对象=全员可见
	s.db.Exec(`ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS audience JSONB DEFAULT '{}'::jsonb`)
	// 多级分类：父分类 / 层级 / 同级排序（空 parent_id = 一级分类）
	s.db.Exec(`ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS parent_id TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS level INTEGER DEFAULT 1`)
	s.db.Exec(`ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS sort_order INTEGER DEFAULT 0`)
}

const catalogCols = `id,tenant_id,name,description,category,parent_id,level,sort_order,process_id,form_scope,enabled,audience,created_at,updated_at`

func marshalAudience(a Audience) []byte {
	b, err := json.Marshal(a)
	if err != nil || len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func unmarshalAudience(raw []byte, a *Audience) {
	if len(raw) == 0 || string(raw) == "null" {
		*a = Audience{}
		return
	}
	if err := json.Unmarshal(raw, a); err != nil {
		*a = Audience{}
	}
}

func (s *Store) seed() {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM itsm_catalog`).Scan(&count)
	if count > 0 {
		return
	}
	now := time.Now()
	// ITIL 最佳实践 5 大类一级分类（文件夹型，无流程模板）
	topCats := []struct {
		key  string
		name string
		desc string
	}{
		{"incident", "事件管理", "故障报修与中断恢复"},
		{"problem", "问题管理", "根因分析与重复故障治理"},
		{"change", "变更管理", "标准与紧急变更实施"},
		{"request", "服务请求", "常规服务开通与权限申请"},
		{"release", "发布管理", "版本发布与上线部署"},
	}
	catIDs := make(map[string]string)
	for i, c := range topCats {
		id := uuid.New().String()
		catIDs[c.key] = id
		s.insertRow(ServiceItem{
			ID: id, TenantID: "default", Name: c.name, Description: c.desc,
			Category: c.key, ParentID: "", Level: 1, SortOrder: i + 1,
			Enabled: true, Audience: Audience{}, CreatedAt: now, UpdatedAt: now,
		})
	}
	// 示例服务：挂在对应一级分类下（二级），上线后即在门户可见
	samples := []ServiceItem{
		{Name: "服务器重启", Description: "重启指定服务器或虚拟机", Category: "change", ParentID: catIDs["change"], Level: 2, SortOrder: 1},
		{Name: "数据库配置修改", Description: "修改数据库参数或执行 DDL", Category: "change", ParentID: catIDs["change"], Level: 2, SortOrder: 2},
		{Name: "SSL 证书续期", Description: "续费或更新 SSL 证书", Category: "change", ParentID: catIDs["change"], Level: 2, SortOrder: 3},
		{Name: "DNS 记录变更", Description: "添加/修改/删除 DNS 记录", Category: "change", ParentID: catIDs["change"], Level: 2, SortOrder: 4},
		{Name: "防火墙规则变更", Description: "修改防火墙白名单或策略", Category: "change", ParentID: catIDs["change"], Level: 2, SortOrder: 5},
		{Name: "账号权限申请", Description: "申请系统或数据库访问权限", Category: "request", ParentID: catIDs["request"], Level: 2, SortOrder: 1},
	}
	for _, it := range samples {
		it.ID = uuid.New().String()
		it.TenantID = "default"
		it.Enabled = true
		it.CreatedAt = now
		it.UpdatedAt = now
		s.insertRow(it)
	}
}

// insertRow 全字段写入（含多级分类字段）。
func (s *Store) insertRow(it ServiceItem) {
	s.db.Exec(`INSERT INTO itsm_catalog(`+catalogCols+`) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		it.ID, it.TenantID, it.Name, it.Description, it.Category, it.ParentID, it.Level, it.SortOrder,
		it.ProcessID, it.FormScope, it.Enabled, marshalAudience(it.Audience), it.CreatedAt, it.UpdatedAt)
}

func (s *Store) List(ctx context.Context, tenantID string) ([]ServiceItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+catalogCols+` FROM itsm_catalog WHERE tenant_id=$1 ORDER BY level, sort_order, name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceItem
	for rows.Next() {
		var it ServiceItem
		var audRaw []byte
		if err := rows.Scan(&it.ID, &it.TenantID, &it.Name, &it.Description, &it.Category, &it.ParentID, &it.Level, &it.SortOrder, &it.ProcessID, &it.FormScope, &it.Enabled, &audRaw, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		unmarshalAudience(audRaw, &it.Audience)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Get 按 ID 读取单个目录项(供"从目录发起申请"套用流程/表单)。
func (s *Store) Get(ctx context.Context, id, tenantID string) (*ServiceItem, error) {
	var it ServiceItem
	var audRaw []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+catalogCols+` FROM itsm_catalog WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&it.ID, &it.TenantID, &it.Name, &it.Description, &it.Category, &it.ParentID, &it.Level, &it.SortOrder, &it.ProcessID, &it.FormScope, &it.Enabled, &audRaw, &it.CreatedAt, &it.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	unmarshalAudience(audRaw, &it.Audience)
	return &it, nil
}

func (s *Store) Create(ctx context.Context, it *ServiceItem) error {
	it.ID = uuid.New().String()
	it.CreatedAt = time.Now()
	it.UpdatedAt = it.CreatedAt
	if it.TenantID == "" {
		it.TenantID = "default"
	}
	if it.Level == 0 {
		it.Level = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO itsm_catalog(`+catalogCols+`) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		it.ID, it.TenantID, it.Name, it.Description, it.Category, it.ParentID, it.Level, it.SortOrder,
		it.ProcessID, it.FormScope, it.Enabled, marshalAudience(it.Audience), it.CreatedAt, it.UpdatedAt)
	return err
}

func (s *Store) Update(ctx context.Context, it *ServiceItem) error {
	it.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE itsm_catalog SET name=$1,description=$2,category=$3,parent_id=$4,level=$5,sort_order=$6,process_id=$7,form_scope=$8,enabled=$9,audience=$10,updated_at=$11 WHERE id=$12 AND tenant_id=$13`,
		it.Name, it.Description, it.Category, it.ParentID, it.Level, it.SortOrder, it.ProcessID, it.FormScope, it.Enabled, marshalAudience(it.Audience), it.UpdatedAt, it.ID, it.TenantID)
	return err
}

func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_catalog WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ListVisible 返回「已上线 且 当前用户在服务对象范围内」的目录项(自助服务门户视角)。
func (s *Store) ListVisible(ctx context.Context, tenantID, userID string) ([]ServiceItem, error) {
	all, err := s.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var groupIDs []string
	if s.groups != nil && userID != "" {
		if gs, err := s.groups(ctx, tenantID, userID); err == nil {
			groupIDs = gs
		}
	}
	out := make([]ServiceItem, 0, len(all))
	for _, it := range all {
		if !it.Enabled {
			continue // 下线项不出现在门户
		}
		if !it.Audience.Allows(userID, groupIDs) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/catalog", s.handleList)
	mux.HandleFunc("GET /api/v1/itsm/catalog/visible", s.handleListVisible)
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

func (s *Store) handleListVisible(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	items, err := s.ListVisible(r.Context(), tenant, auth.UserIDFromContext(r.Context()))
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
