package process

// 审批委托/代理人（对标 ServiceNow Delegate）：
// principal 在生效期内把自己的审批权委托给 delegate。采用「追加式」语义——
// VoteStep 解析名册时把「在期委托」的 delegate 追加进名册，原审批人(principal)仍可投票；
// 表决判定（会签/或签/percent/count）基于扩大后的名册。存储异常时降级为原名册，不阻断审批。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/httputil"
)

// ApprovalDelegate 一条委托关系。
// StartAt/EndAt 为本地时区时间戳；EndAt 为零值表示长期有效(由 ActiveDelegates 过滤)。
type ApprovalDelegate struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Principal string    `json:"principal"` // 委托人（原审批人，用户名）
	Delegate  string    `json:"delegate"`  // 代理人（用户名）
	StartAt   time.Time `json:"start_at"`
	EndAt     time.Time `json:"end_at"` // 零值=长期有效
	Enabled   bool      `json:"enabled"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// DelegationStore 委托关系持久化能力（由 PGStore 实现）。
type DelegationStore interface {
	CreateDelegate(ctx context.Context, tenantID string, d *ApprovalDelegate) error
	ListDelegates(ctx context.Context, tenantID string) ([]*ApprovalDelegate, error)
	DeleteDelegate(ctx context.Context, tenantID, id string) error
	// ActiveDelegates 返回当前时刻生效（enabled 且 now∈[start_at,end_at)）的全部委托。
	ActiveDelegates(ctx context.Context, tenantID string, now time.Time) ([]*ApprovalDelegate, error)
}

// SetDelegationStore 注入委托存储（nil=关闭委托能力，VoteStep 按原名册表决）。
func (s *Service) SetDelegationStore(st DelegationStore) {
	s.delegStore = st
}

// applyDelegations 追加式应用在期委托：名册中出现且存在在期委托的 principal，
// 其 delegate 追加进名册（去重，保持先原名册后代理人的稳定次序）。
// 委托链（A 委托 B、B 委托 C）只展开一层，防环且避免表决名册被链式放大。
// 存储异常/未注入时返回原名册（fail-open 到旧行为，不阻断审批）。
func (s *Service) applyDelegations(ctx context.Context, tenantID string, roster []string) []string {
	if s.delegStore == nil || len(roster) == 0 {
		return roster
	}
	ds, err := s.delegStore.ActiveDelegates(ctx, tenantID, time.Now())
	if err != nil {
		return roster // 存储抖动不阻断审批
	}
	if len(ds) == 0 {
		return roster
	}
	byPrincipal := map[string][]string{}
	for _, d := range ds {
		if d.Delegate == "" || d.Principal == "" {
			continue
		}
		byPrincipal[d.Principal] = append(byPrincipal[d.Principal], d.Delegate)
	}
	seen := make(map[string]struct{}, len(roster)*2)
	for _, a := range roster {
		seen[a] = struct{}{}
	}
	out := roster
	for _, a := range roster {
		for _, dg := range byPrincipal[a] {
			if _, dup := seen[dg]; dup {
				continue
			}
			seen[dg] = struct{}{}
			out = append(out, dg)
		}
	}
	return out
}

// ---- HTTP handlers（挂在 process Service 路由上）----

func (s *Service) handleListDelegates(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		tenantID = "default"
	}
	list, err := s.delegStoreList(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

func (s *Service) handleCreateDelegate(w http.ResponseWriter, r *http.Request) {
	var d ApprovalDelegate
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	d.Principal = strings.TrimSpace(d.Principal)
	d.Delegate = strings.TrimSpace(d.Delegate)
	if d.Principal == "" || d.Delegate == "" {
		httputil.WriteError(w, http.StatusBadRequest, "principal 与 delegate 均为必填")
		return
	}
	if d.Principal == d.Delegate {
		httputil.WriteError(w, http.StatusBadRequest, "不能把审批权委托给自己")
		return
	}
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		tenantID = "default"
	}
	if err := s.delegStoreCreate(r.Context(), tenantID, &d); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, d)
}

func (s *Service) handleDeleteDelegate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		tenantID = "default"
	}
	if err := s.delegStoreDelete(r.Context(), tenantID, id); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// delegStore* 三件套兜底 nil store（未注入时委托能力关闭，接口返回空集）。
func (s *Service) delegStoreList(ctx context.Context, tenantID string) ([]*ApprovalDelegate, error) {
	if s.delegStore == nil {
		return []*ApprovalDelegate{}, nil
	}
	return s.delegStore.ListDelegates(ctx, tenantID)
}

func (s *Service) delegStoreCreate(ctx context.Context, tenantID string, d *ApprovalDelegate) error {
	if s.delegStore == nil {
		return fmt.Errorf("delegation store not configured")
	}
	return s.delegStore.CreateDelegate(ctx, tenantID, d)
}

func (s *Service) delegStoreDelete(ctx context.Context, tenantID, id string) error {
	if s.delegStore == nil {
		return fmt.Errorf("delegation store not configured")
	}
	return s.delegStore.DeleteDelegate(ctx, tenantID, id)
}

// migrateDelegates 建立审批委托表（幂等 DDL，与 deploy/migrations 双写）。
func (s *PGStore) migrateDelegates() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_approval_delegates (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		principal TEXT NOT NULL,
		delegate TEXT NOT NULL,
		start_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		end_at TIMESTAMPTZ,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		note TEXT DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_itsm_delegates_tenant ON itsm_approval_delegates(tenant_id)`)
}

// ---- DelegationStore 的 PGStore 实现 ----

func (s *PGStore) CreateDelegate(ctx context.Context, tenantID string, d *ApprovalDelegate) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	d.ID = uuid.New().String()
	d.TenantID = tenantID
	d.CreatedAt = time.Now()
	endAt := any(d.EndAt)
	if d.EndAt.IsZero() {
		endAt = nil // 零值落 NULL=长期有效
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO itsm_approval_delegates(id,tenant_id,principal,delegate,start_at,end_at,enabled,note,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		d.ID, d.TenantID, d.Principal, d.Delegate, d.StartAt, endAt, d.Enabled, d.Note, d.CreatedAt)
	return err
}

func (s *PGStore) ListDelegates(ctx context.Context, tenantID string) ([]*ApprovalDelegate, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,principal,delegate,start_at,end_at,enabled,note,created_at
		 FROM itsm_approval_delegates WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDelegates(rows)
}

func (s *PGStore) DeleteDelegate(ctx context.Context, tenantID, id string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM itsm_approval_delegates WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ActiveDelegates 返回当前时刻生效的委托：enabled 且 now∈[start_at, end_at)。
// end_at 为 NULL 表示长期有效。
func (s *PGStore) ActiveDelegates(ctx context.Context, tenantID string, now time.Time) ([]*ApprovalDelegate, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,principal,delegate,start_at,end_at,enabled,note,created_at
		 FROM itsm_approval_delegates
		 WHERE tenant_id=$1 AND enabled AND start_at<=$2 AND (end_at IS NULL OR end_at>$2)
		 ORDER BY created_at`, tenantID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDelegates(rows)
}

func scanDelegates(rows *sql.Rows) ([]*ApprovalDelegate, error) {
	var out []*ApprovalDelegate
	for rows.Next() {
		var d ApprovalDelegate
		var endAt sql.NullTime
		var note sql.NullString
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Principal, &d.Delegate, &d.StartAt, &endAt, &d.Enabled, &note, &d.CreatedAt); err != nil {
			return nil, err
		}
		if endAt.Valid {
			d.EndAt = endAt.Time
		}
		d.Note = note.String
		out = append(out, &d)
	}
	return out, rows.Err()
}
