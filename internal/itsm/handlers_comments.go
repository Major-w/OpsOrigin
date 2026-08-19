package itsm

import (
	"context"
	"net/http"
	"strconv"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleAddComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req struct {
		Author   string   `json:"author"`
		Content  string   `json:"content"`
		Mentions []string `json:"mentions,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Author == "" || req.Content == "" {
		httputil.WriteError(w, http.StatusBadRequest, "author and content are required")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	comment := &model.Comment{
		TicketID: id,
		TenantID: tenantID,
		Author:   req.Author,
		Content:  req.Content,
		// 显式 mentions 优先, 正文里的 @xxx 作为兜底 —— 保证任何入口
		// (页面 / 邮件入站 / 机器人)@ 到的人都能收到站内消息。
		Mentions: extractMentions(req.Content, req.Mentions),
	}
	if err := s.store.AddComment(r.Context(), comment); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to add comment")
		return
	}

	if len(comment.Mentions) > 0 {
		s.notifyTicketInbox(r.Context(), ticket, msgCategoryMention, msgLevelInfo,
			req.Author+" 在工单中提到了你",
			truncate(req.Content, 140), req.Author, comment.Mentions...)
	}
	// 评论本身也知会负责人与申请人(被 @ 的人已在上一条收到, sendInbox 会去重触发者本人)。
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryTicket, msgLevelInfo,
		"工单有新评论: "+ticket.Title,
		truncate(req.Content, 140), req.Author, assigneeAndOwner(ticket)...)

	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusCreated, comment)
}

// AddTicketComment 以指定作者追加评论(供邮件入站等外部通道调用)。
func (s *Service) AddTicketComment(ctx context.Context, tenantID, ticketID, author, body string) error {
	tk, err := s.store.Get(ctx, ticketID, tenantID)
	if err != nil {
		return err
	}
	c := &model.Comment{TicketID: ticketID, TenantID: tenantID, Author: author, Content: body}
	if err := s.store.AddComment(ctx, c); err != nil {
		return err
	}
	s.broadcastTicketUpdate(tk)
	return nil
}

func (s *Service) handleListComments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	comments, err := s.store.ListComments(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	httputil.WriteJSON(w, http.StatusOK, comments)
}

func (s *Service) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cidStr := r.PathValue("cid")
	tenantID := auth.TenantFromContext(r.Context())

	cid, err := strconv.ParseInt(cidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid comment id")
		return
	}

	if err := s.store.DeleteComment(r.Context(), cid, id, tenantID); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "comment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleEditComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cidStr := r.PathValue("cid")
	tenantID := auth.TenantFromContext(r.Context())

	cid, err := strconv.ParseInt(cidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid comment id")
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := httputil.BindJSON(r, &body); err != nil || body.Content == "" {
		httputil.WriteError(w, http.StatusBadRequest, "content is required")
		return
	}

	if err := s.store.EditComment(r.Context(), cid, id, tenantID, body.Content); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "comment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
