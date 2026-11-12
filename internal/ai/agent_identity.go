package ai

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// 数字员工的独立身份与资产可见性(P0 治理层)。
//
// 与会话隔离(conv_share.go)是两套独立语义但复用同一范式:
//   - 会话: 默认按提问者隔离, 存量无主会话退化为全租户可见;
//   - 数字员工: 是**资产**, 新建默认 private, 存量无主实例退化为 public(兼容老数据)。
//
// visibility=public/shared/private 是「谁能看到」; scope.go 的 view_permission 是
// 「检索时按提问者身份过滤知识」, 两者同名不同义, 勿混用。

// AgentShare 数字员工共享条目。
type AgentShare struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	AgentID    string    `json:"agent_id"`
	TargetType string    `json:"target_type"` // user | group
	TargetID   string    `json:"target_id"`
	Permission string    `json:"permission"` // view | edit
	SharedBy   string    `json:"shared_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// initAgentIdentityDDL 与 deploy/migrations/041_ai_agent_governance.sql 双写自愈 DDL。
func (s *Service) initAgentIdentityDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	// 装配顺序兜底: PGStore.migrate 也可能尚未执行(NewService 早于 main.go 的 SetStore)。
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ai_agents (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, model_name TEXT DEFAULT 'deepseek-chat',
		system_prompt TEXT DEFAULT '', status TEXT DEFAULT 'active',
		extra JSONB DEFAULT '{}', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(),
		owner_id TEXT NOT NULL DEFAULT '', visibility TEXT NOT NULL DEFAULT 'public'
	)`); err != nil {
		slog.Warn("ai agent identity DDL: agents table", "err", err)
		return
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE ai_agents ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT ''`); err != nil {
		slog.Warn("ai agent identity DDL: owner_id column", "err", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE ai_agents ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public'`); err != nil {
		slog.Warn("ai agent identity DDL: visibility column", "err", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_agent_shares (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
			agent_id TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL,
			permission TEXT NOT NULL DEFAULT 'view', shared_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (tenant_id, agent_id, target_type, target_id)
		)`); err != nil {
		slog.Warn("ai agent identity DDL: shares table", "err", err)
		return
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_agent_shares_agent ON ai_agent_shares(tenant_id, agent_id)`)
}

// agentAccess 计算当前用户对某个数字员工的权限: "" 不可见 / view 可使用 / edit 可改 / owner 属主。
// 管理员拥有治理级 owner 权限, 便于统一处置失控员工; 其余一律按属主 + visibility + 分享表裁决。
func (s *Service) agentAccess(ctx context.Context, tenantID, userID string, a *model.AgentInstance, groups []string) string {
	if a == nil || a.TenantID != tenantID {
		return ""
	}
	if auth.RoleFromContext(ctx) == auth.RoleAdmin {
		return "owner"
	}
	if a.OwnerID != "" && a.OwnerID == userID {
		return "owner"
	}
	vis := model.NormalizeVisibility(a.Visibility)
	if a.OwnerID == "" {
		// 存量无主实例: 一律按 public 兼容, 不因后来改了默认可见性而把它们藏起来。
		vis = model.AgentVisibilityPublic
	}
	switch vis {
	case model.AgentVisibilityPublic:
		return "view"
	case model.AgentVisibilityPrivate:
		return ""
	case model.AgentVisibilityShared:
		// 落到分享表匹配
	default:
		return ""
	}
	if userID == "" || s.db == nil {
		return ""
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_type, target_id, permission FROM ai_agent_shares
		 WHERE tenant_id=$1 AND agent_id=$2`, tenantID, a.ID)
	if err != nil {
		slog.Warn("ai agent share lookup failed", "err", err)
		return ""
	}
	defer rows.Close()
	best := ""
	for rows.Next() {
		var tt, tid, perm string
		if err := rows.Scan(&tt, &tid, &perm); err != nil {
			continue
		}
		matched := tt == "user" && tid == userID
		if tt == "group" {
			for _, g := range groups {
				if g == tid {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		if perm == "edit" {
			return "edit"
		}
		if best == "" {
			best = "view"
		}
	}
	return best
}

// filterVisibleAgents 列表过滤: 只返回当前用户可见的数字员工, 并附响应态 permission。
// 写入副本, 不污染 Engine 返回的内部指针。
func (s *Service) filterVisibleAgents(ctx context.Context, tenantID, userID string, all []*model.AgentInstance) []*model.AgentInstance {
	groups := s.userGroups(ctx, userID)
	out := make([]*model.AgentInstance, 0, len(all))
	for _, a := range all {
		if a.TenantID != tenantID {
			continue
		}
		perm := s.agentAccess(ctx, tenantID, userID, a, groups)
		if perm == "" {
			continue
		}
		cp := *a
		cp.Permission = perm
		out = append(out, &cp)
	}
	return out
}

// handleListAgentShares 共享列表(有任意可见权限者可读)。
func (s *Service) handleListAgentShares(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	a, ok := s.Engine.GetAgent(id)
	if !ok || a.TenantID != tenantID {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if s.agentAccess(r.Context(), tenantID, userID, a, s.userGroups(r.Context(), userID)) == "" {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if s.db == nil {
		httputil.WriteJSON(w, http.StatusOK, []AgentShare{})
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, tenant_id, agent_id, target_type, target_id, permission, shared_by, created_at
		 FROM ai_agent_shares WHERE tenant_id=$1 AND agent_id=$2 ORDER BY created_at DESC`, tenantID, id)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []AgentShare{}
	for rows.Next() {
		var sh AgentShare
		if err := rows.Scan(&sh.ID, &sh.TenantID, &sh.AgentID, &sh.TargetType, &sh.TargetID,
			&sh.Permission, &sh.SharedBy, &sh.CreatedAt); err != nil {
			continue
		}
		out = append(out, sh)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// handleAddAgentShare 共享数字员工给指定用户/组(仅属主可操作)。
func (s *Service) handleAddAgentShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	a, ok := s.Engine.GetAgent(id)
	if !ok || a.TenantID != tenantID {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if s.agentAccess(r.Context(), tenantID, userID, a, s.userGroups(r.Context(), userID)) != "owner" {
		httputil.WriteError(w, http.StatusForbidden, "only the owner can share this agent")
		return
	}
	var req struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Permission string `json:"permission"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.TargetID = strings.TrimSpace(req.TargetID)
	if req.TargetType != "user" && req.TargetType != "group" {
		httputil.WriteError(w, http.StatusBadRequest, "target_type must be user or group")
		return
	}
	if req.TargetID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "target_id required")
		return
	}
	if req.Permission != "edit" {
		req.Permission = "view"
	}
	if s.db == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "db unavailable")
		return
	}
	sh := AgentShare{
		ID: uuid.NewString(), TenantID: tenantID, AgentID: id,
		TargetType: req.TargetType, TargetID: req.TargetID,
		Permission: req.Permission, SharedBy: userID, CreatedAt: time.Now(),
	}
	if _, err := s.db.ExecContext(r.Context(),
		`INSERT INTO ai_agent_shares(id,tenant_id,agent_id,target_type,target_id,permission,shared_by,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (tenant_id, agent_id, target_type, target_id) DO UPDATE SET permission=$6`,
		sh.ID, sh.TenantID, sh.AgentID, sh.TargetType, sh.TargetID, sh.Permission, sh.SharedBy, sh.CreatedAt); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, sh)
}

// handleDeleteAgentShare 取消共享(仅属主可操作)。
func (s *Service) handleDeleteAgentShare(w http.ResponseWriter, r *http.Request) {
	id, sid := r.PathValue("id"), r.PathValue("sid")
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	a, ok := s.Engine.GetAgent(id)
	if !ok || a.TenantID != tenantID {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if s.agentAccess(r.Context(), tenantID, userID, a, s.userGroups(r.Context(), userID)) != "owner" {
		httputil.WriteError(w, http.StatusForbidden, "only the owner can unshare this agent")
		return
	}
	if s.db == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "db unavailable")
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM ai_agent_shares WHERE tenant_id=$1 AND agent_id=$2 AND id=$3`, tenantID, id, sid); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
