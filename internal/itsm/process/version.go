package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// ProcessVersion 流程定义历史版本快照。
// 每次 Update 前把「更新之前」的定义整体归档一份, 用于版本列表 / 回看 / 版本间差异对比。
// 与 ProcessInstance.ProcessSnapshot 的区别: 后者是「实例视角」的版本隔离(在途实例按启动时定义推进),
// 本表是「定义视角」的演进轨迹(谁在什么时候把流程改成了什么样)。
type ProcessVersion struct {
	ID          string            `json:"id"`
	ProcessID   string            `json:"process_id"`
	TenantID    string            `json:"tenant_id"`
	Version     int               `json:"version"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"`
	Steps       []Step            `json:"steps"`
	Labels      map[string]string `json:"labels,omitempty"`
	Enabled     bool              `json:"enabled"`

	Form         json.RawMessage   `json:"form,omitempty"`
	SlaPolicyID  string            `json:"sla_policy_id,omitempty"`
	NotifyConfig json.RawMessage   `json:"notify_config,omitempty"`
	NumberRule   *TicketNumberRule `json:"number_rule,omitempty"`

	// CreatedBy 归档时的操作人(取自 X-User-ID / X-Principal-ID 可信头), 空表示未知。
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SnapshotOf 由当前流程定义构造一份版本快照。
func SnapshotOf(p *Process, actor string) *ProcessVersion {
	if p == nil {
		return nil
	}
	return &ProcessVersion{
		ProcessID:    p.ID,
		TenantID:     p.TenantID,
		Version:      p.Version,
		Name:         p.Name,
		Description:  p.Description,
		Category:     p.Category,
		Steps:        p.Steps,
		Labels:       p.Labels,
		Enabled:      p.Enabled,
		Form:         p.Form,
		SlaPolicyID:  p.SlaPolicyID,
		NotifyConfig: p.NotifyConfig,
		NumberRule:   p.NumberRule,
		CreatedBy:    actor,
	}
}

func (s *PGStore) migrateVersions() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_process_versions (
		id TEXT PRIMARY KEY,
		process_id TEXT NOT NULL,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		version INT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		description TEXT DEFAULT '',
		category TEXT DEFAULT '',
		steps JSONB DEFAULT '[]'::jsonb,
		labels JSONB DEFAULT '{}'::jsonb,
		enabled BOOLEAN DEFAULT TRUE,
		form JSONB DEFAULT '{}'::jsonb,
		sla_policy_id TEXT DEFAULT '',
		notify_config JSONB DEFAULT '{}'::jsonb,
		number_rule JSONB,
		created_by TEXT DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	s.db.Exec(`ALTER TABLE itsm_process_versions ADD COLUMN IF NOT EXISTS number_rule JSONB`)
	// 同一流程同一版本只保留一份快照(重复归档幂等覆盖)
	s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_itsm_pv_unique ON itsm_process_versions(tenant_id, process_id, version)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_itsm_pv_process ON itsm_process_versions(tenant_id, process_id, version DESC)`)
}

const versionCols = `id,process_id,tenant_id,version,name,description,category,steps,labels,enabled,form,sla_policy_id,notify_config,number_rule,created_by,created_at`

// SaveVersion 归档一份版本快照; 同 (tenant, process, version) 已存在则覆盖。
func (s *PGStore) SaveVersion(ctx context.Context, v *ProcessVersion) error {
	if s.db == nil {
		return nil
	}
	if v == nil {
		return nil
	}
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	if v.TenantID == "" {
		v.TenantID = "default"
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	stepsJSON, _ := json.Marshal(v.Steps)
	labelsJSON, _ := json.Marshal(v.Labels)
	formJSON := v.Form
	if len(formJSON) == 0 {
		formJSON = []byte("{}")
	}
	notifyJSON := v.NotifyConfig
	if len(notifyJSON) == 0 {
		notifyJSON = []byte("{}")
	}
	var nrJSON []byte
	if v.NumberRule != nil {
		nrJSON, _ = json.Marshal(v.NumberRule)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO itsm_process_versions(`+versionCols+`)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 ON CONFLICT (tenant_id, process_id, version) DO UPDATE SET
		   name=EXCLUDED.name, description=EXCLUDED.description, category=EXCLUDED.category,
		   steps=EXCLUDED.steps, labels=EXCLUDED.labels, enabled=EXCLUDED.enabled,
		   form=EXCLUDED.form, sla_policy_id=EXCLUDED.sla_policy_id, notify_config=EXCLUDED.notify_config,
		   number_rule=EXCLUDED.number_rule,
		   created_by=EXCLUDED.created_by, created_at=EXCLUDED.created_at`,
		v.ID, v.ProcessID, v.TenantID, v.Version, v.Name, v.Description, v.Category,
		stepsJSON, labelsJSON, v.Enabled, formJSON, v.SlaPolicyID, notifyJSON, nrJSON, v.CreatedBy, v.CreatedAt)
	return err
}

func scanVersion(sc interface{ Scan(...any) error }) (*ProcessVersion, error) {
	var stepsJSON, labelsJSON, formJSON, notifyJSON, nrJSON []byte
	v := &ProcessVersion{}
	if err := sc.Scan(&v.ID, &v.ProcessID, &v.TenantID, &v.Version, &v.Name, &v.Description, &v.Category,
		&stepsJSON, &labelsJSON, &v.Enabled, &formJSON, &v.SlaPolicyID, &notifyJSON, &nrJSON, &v.CreatedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	json.Unmarshal(stepsJSON, &v.Steps)
	json.Unmarshal(labelsJSON, &v.Labels)
	if len(formJSON) > 0 && string(formJSON) != "null" {
		v.Form = formJSON
	}
	if len(notifyJSON) > 0 && string(notifyJSON) != "null" {
		v.NotifyConfig = notifyJSON
	}
	if len(nrJSON) > 0 && string(nrJSON) != "null" {
		nr := &TicketNumberRule{}
		if json.Unmarshal(nrJSON, nr) == nil {
			v.NumberRule = nr
		}
	}
	return v, nil
}

// ListVersions 按版本倒序返回某流程的历史快照。
func (s *PGStore) ListVersions(ctx context.Context, processID, tenantID string) ([]*ProcessVersion, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+versionCols+` FROM itsm_process_versions WHERE process_id=$1 AND tenant_id=$2 ORDER BY version DESC`,
		processID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProcessVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetVersion 读取指定版本快照; 不存在返回 (nil, nil)。
func (s *PGStore) GetVersion(ctx context.Context, processID, tenantID string, version int) (*ProcessVersion, error) {
	if s.db == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+versionCols+` FROM itsm_process_versions WHERE process_id=$1 AND tenant_id=$2 AND version=$3`,
		processID, tenantID, version)
	v, err := scanVersion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// ---- HTTP ----

func (s *Service) handleListVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantOf(r)
	list, err := s.store.ListVersions(r.Context(), id, tenant)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	// 把「当前生效版本」也拼进列表头部, 前端一屏即可对比当前 vs 历史
	if cur, err := s.store.Get(r.Context(), id, tenant); err == nil && cur != nil {
		exists := false
		for _, v := range list {
			if v.Version == cur.Version {
				exists = true
				break
			}
		}
		if !exists {
			snap := SnapshotOf(cur, "")
			snap.ID = "current"
			snap.CreatedAt = cur.UpdatedAt
			list = append([]*ProcessVersion{snap}, list...)
		}
	}
	httputil.WriteJSON(w, 200, list)
}

func (s *Service) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantOf(r)
	num, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		httputil.WriteError(w, 400, "invalid version")
		return
	}
	v, err := s.store.GetVersion(r.Context(), id, tenant, num)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	if v == nil {
		// 回退到当前定义(当前版本尚未归档时)
		if cur, err := s.store.Get(r.Context(), id, tenant); err == nil && cur != nil && cur.Version == num {
			snap := SnapshotOf(cur, "")
			snap.ID = "current"
			snap.CreatedAt = cur.UpdatedAt
			httputil.WriteJSON(w, 200, snap)
			return
		}
		httputil.WriteError(w, 404, "version not found")
		return
	}
	httputil.WriteJSON(w, 200, v)
}

// handleRestoreVersion POST /api/v1/itsm/processes/{id}/versions/{version}/restore
// 把历史版本快照恢复为当前定义（SN 语义：产生一个新版本，而非把版本号拨回去，
// 从而保留完整审计轨迹）。恢复前先把当前定义归档为快照，防止误恢复丢失最新编辑。
func (s *Service) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantOf(r)
	num, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		httputil.WriteError(w, 400, "invalid version")
		return
	}
	cur, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	if cur == nil {
		httputil.WriteError(w, 404, "not found")
		return
	}
	if cur.Version == num {
		httputil.WriteError(w, 400, "该版本已是当前生效版本，无需回滚")
		return
	}
	v, err := s.store.GetVersion(r.Context(), id, tenant, num)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	if v == nil {
		httputil.WriteError(w, 404, "version not found")
		return
	}
	// 先归档当前定义（幂等覆盖），再以快照内容覆盖当前定义并版本号 +1
	if snap := SnapshotOf(cur, actorOf(r)); snap != nil {
		if err := s.store.SaveVersion(r.Context(), snap); err != nil {
			slog.Warn("itsm process restore: archive current failed", "id", id, "version", cur.Version, "err", err)
		}
	}
	next := *cur
	next.Name = v.Name
	next.Description = v.Description
	next.Category = v.Category
	next.Steps = v.Steps
	next.Labels = v.Labels
	next.Enabled = v.Enabled
	next.Form = v.Form
	next.SlaPolicyID = v.SlaPolicyID
	next.NotifyConfig = v.NotifyConfig
	next.NumberRule = v.NumberRule
	next.Version = cur.Version + 1
	// 回滚后的编号规则同样要过格式唯一性校验（历史版本可能与新流程撞型）
	if err := s.checkNumberRuleConflict(&next); err != nil {
		httputil.WriteError(w, 409, "回滚被拒绝："+err.Error())
		return
	}
	if err := s.store.Update(r.Context(), &next); err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process version restored", "id", id, "from_version", num, "to_version", next.Version, "actor", actorOf(r))
	httputil.WriteJSON(w, 200, &next)
}

func tenantOf(r *http.Request) string {
	t := r.Header.Get("X-Tenant-ID")
	if t == "" {
		return "default"
	}
	return t
}

func actorOf(r *http.Request) string {
	if v := r.Header.Get("X-User-ID"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Principal-ID"); v != "" {
		return v
	}
	return ""
}
