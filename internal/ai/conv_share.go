// 会话隔离与共享: 默认每个用户只能看到自己的对话; 共享给指定用户/组后,
// 目标可见并可配置 仅查看(view)/继续对话(edit)。多租户关闭(单租户)时同样按用户隔离。
package ai

import (
	"context"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/principal"
)

// ConvShare 会话共享条目。
type ConvShare struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	TargetType     string    `json:"target_type"` // user | group
	TargetID       string    `json:"target_id"`
	Permission     string    `json:"permission"` // view | edit
	SharedBy       string    `json:"shared_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// UserGroupsFn 由 cmd/core 注入: 返回用户所属用户组 ID 列表(供共享按组匹配)。
type UserGroupsFn func(ctx context.Context, userID string) []string

// userGroups 当前用户的组(未注入则空)。
func (s *Service) userGroups(ctx context.Context, userID string) []string {
	if s.userGroupsFn != nil && userID != "" {
		return s.userGroupsFn(ctx, userID)
	}
	return nil
}

// SetUserGroupsProvider 注入用户组查询(iam)。
func (s *Service) SetUserGroupsProvider(fn UserGroupsFn) { s.userGroupsFn = fn }

// currentUserID 从请求主体取用户 ID。
func currentUserID(ctx context.Context) string {
	if pr, ok := principal.FromContext(ctx); ok {
		return pr.ID
	}
	return ""
}

// convAccess 计算当前用户对会话的权限: "" 无权限 / view 仅查看 / edit 可继续对话 / owner 属主。
func (s *Service) convAccess(ctx context.Context, tenantID, convID, userID string, groups []string) string {
	conv, err := s.Engine.GetConversation(convID)
	if err != nil || conv == nil || conv.TenantID != tenantID {
		return ""
	}
	// 属主: 完整权限(历史数据 owner 为空时视为系统会话, 仅属主可见逻辑不适用, 返回空)
	if conv.OwnerID != "" {
		if conv.OwnerID == userID {
			return "owner"
		}
	} else {
		// 存量无主会话: 退化为全租户可见(兼容老数据), 权限 view
		return "view"
	}
	if userID == "" || s.db == nil {
		return ""
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_type, target_id, permission FROM ai_conversation_shares WHERE conversation_id=$1`, convID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	best := ""
	for rows.Next() {
		var tt, tid, perm string
		if err := rows.Scan(&tt, &tid, &perm); err != nil {
			continue
		}
		matched := false
		if tt == "user" && tid == userID {
			matched = true
		}
		if tt == "group" {
			for _, g := range groups {
				if g == tid {
					matched = true
					break
				}
			}
		}
		if matched {
			if perm == "edit" {
				return "edit"
			}
			if best == "" {
				best = "view"
			}
		}
	}
	return best
}

// filterVisible 列表可见性过滤: 只返回当前用户可见(属主或共享)的会话, 并附加 permission。
func (s *Service) filterVisible(ctx context.Context, tenantID, userID string, convs []*agentic.Conversation) []*agentic.Conversation {
	groups := s.userGroups(ctx, userID)
	out := make([]*agentic.Conversation, 0, len(convs))
	for _, c := range convs {
		perm := s.convAccess(ctx, tenantID, c.ID, userID, groups)
		if perm == "" {
			continue
		}
		copy := *c
		copy.Permission = perm
		out = append(out, &copy)
	}
	return out
}

// handleListConversations 会话列表(用户隔离: 仅返回自己的 + 共享给我的)。
func (s *Service) handleListConversations(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	agentID := r.URL.Query().Get("agent_id")
	userID := currentUserID(r.Context())
	convs := s.Engine.LoadConversations(tenantID, agentID, 200)
	httputil.WriteJSON(w, http.StatusOK, s.filterVisible(r.Context(), tenantID, userID, convs))
}

// requireConvWrite 校验当前用户对会话有写入(继续对话)权限; 失败返回 false 并已写响应。
func (s *Service) requireConvWrite(ctx context.Context, tenantID, convID, userID string, groups []string, w http.ResponseWriter) bool {
	perm := s.convAccess(ctx, tenantID, convID, userID, groups)
	if perm == "" || perm == "view" {
		httputil.WriteError(w, http.StatusForbidden, "no permission to continue this conversation (view-only or not shared)")
		return false
	}
	return true
}

// handleGetConversation 会话详情(可见性校验)。
func (s *Service) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	userID := currentUserID(r.Context())
	perm := s.convAccess(r.Context(), tenantID, id, userID, s.userGroups(r.Context(), userID))
	if perm == "" {
		httputil.WriteError(w, http.StatusNotFound, "conversation not found")
		return
	}
	conv, err := s.Engine.GetConversation(id)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	conv.Permission = perm
	httputil.WriteJSON(w, http.StatusOK, conv)
}

// ——— 共享管理 API ———

// handleAddShare 共享会话给指定用户/组(仅属主可操作)。
func (s *Service) handleAddShare(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	userID := currentUserID(r.Context())
	perm := s.convAccess(r.Context(), tenantID, convID, userID, s.userGroups(r.Context(), userID))
	if perm != "owner" {
		httputil.WriteError(w, http.StatusForbidden, "only the owner can share this conversation")
		return
	}
	var req struct {
		TargetType string `json:"target_type"` // user | group
		TargetID   string `json:"target_id"`
		Permission string `json:"permission"` // view | edit
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.TargetType != "user" && req.TargetType != "group" {
		httputil.WriteError(w, http.StatusBadRequest, "target_type must be user or group")
		return
	}
	if req.TargetID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "target_id required")
		return
	}
	if req.Permission != "view" && req.Permission != "edit" {
		req.Permission = "view"
	}
	if s.db == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "db unavailable")
		return
	}
	sh := ConvShare{
		ID: uuid.New().String(), ConversationID: convID,
		TargetType: req.TargetType, TargetID: req.TargetID,
		Permission: req.Permission, SharedBy: userID, CreatedAt: time.Now(),
	}
	if _, err := s.db.ExecContext(r.Context(),
		`INSERT INTO ai_conversation_shares (id, conversation_id, target_type, target_id, permission, shared_by, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (conversation_id, target_type, target_id) DO UPDATE SET permission=$5`,
		sh.ID, convID, sh.TargetType, sh.TargetID, sh.Permission, userID, sh.CreatedAt); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, sh)
}

// handleListShares 共享列表(属主或共享者可看)。
func (s *Service) handleListShares(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	userID := currentUserID(r.Context())
	if s.convAccess(r.Context(), tenantID, convID, userID, s.userGroups(r.Context(), userID)) == "" {
		httputil.WriteError(w, http.StatusNotFound, "conversation not found")
		return
	}
	if s.db == nil {
		httputil.WriteJSON(w, http.StatusOK, []ConvShare{})
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, conversation_id, target_type, target_id, permission, shared_by, created_at
		 FROM ai_conversation_shares WHERE conversation_id=$1 ORDER BY created_at DESC`, convID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []ConvShare{}
	for rows.Next() {
		var sh ConvShare
		if err := rows.Scan(&sh.ID, &sh.ConversationID, &sh.TargetType, &sh.TargetID, &sh.Permission, &sh.SharedBy, &sh.CreatedAt); err != nil {
			continue
		}
		out = append(out, sh)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// handleDeleteShare 取消共享(属主可操作)。
func (s *Service) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	sid := r.PathValue("sid")
	tenantID := auth.TenantFromContext(r.Context())
	userID := currentUserID(r.Context())
	if s.convAccess(r.Context(), tenantID, convID, userID, s.userGroups(r.Context(), userID)) != "owner" {
		httputil.WriteError(w, http.StatusForbidden, "only the owner can unshare this conversation")
		return
	}
	if s.db == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "db unavailable")
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM ai_conversation_shares WHERE id=$1 AND conversation_id=$2`, sid, convID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
