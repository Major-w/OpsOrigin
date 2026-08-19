package itsm

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleAddSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.CreateSubtaskRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		httputil.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.CreatedBy == "" {
		req.CreatedBy = getUserID(r, "")
	}

	// 随主单关闭默认开启, 兼容既有级联行为; 显式传 false 则该子任务独立于主单生命周期。
	closeWithParent := true
	if req.CloseWithParent != nil {
		closeWithParent = *req.CloseWithParent
	}
	st := &model.Subtask{
		TicketID:        id,
		TenantID:        tenantID,
		Title:           req.Title,
		Description:     req.Description,
		Assignee:        req.Assignee,
		Status:          model.SubtaskPending,
		DueAt:           req.DueAt,
		CreatedBy:       req.CreatedBy,
		CloseWithParent: closeWithParent,
	}
	// 主单字段同步: 按 sync_fields 从主工单拷贝字段值到子任务 meta(系统字段/自定义字段均可)。
	if len(req.SyncFields) > 0 {
		parent, err := s.store.Get(r.Context(), id, tenantID)
		if err == nil && parent != nil {
			meta := map[string]any{}
			for _, key := range req.SyncFields {
				fieldKey := strings.TrimPrefix(key, "custom_fields.")
				if v, ok := parent.CustomFields[fieldKey]; ok {
					meta[key] = v
					continue
				}
				switch fieldKey {
				case "risk_level":
					meta[key] = string(parent.RiskLevel)
				case "title":
					meta[key] = parent.Title
				case "description":
					meta[key] = parent.Description
				case "assignee":
					meta[key] = parent.Assignee
				case "status":
					meta[key] = string(parent.Status)
				case "deadline_at":
					if parent.DeadlineAt != nil {
						meta[key] = parent.DeadlineAt.Format(time.RFC3339)
					}
				default:
					if v, ok := parent.CustomFields[fieldKey]; ok {
						meta[key] = v
					}
				}
			}
			st.Meta = meta
		}
	}
	if err := s.store.CreateSubtask(r.Context(), st); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create subtask")
		return
	}
	// 派发即通知: 子任务有指派人时向其站内信箱投递待办。
	if st.Assignee != "" {
		if parent, perr := s.store.Get(r.Context(), id, tenantID); perr == nil && parent != nil {
			s.notifySubtaskInbox(r.Context(), parent, st, msgLevelInfo,
				"子任务待处理: "+st.Title,
				fmt.Sprintf("来自工单「%s」, 派发人: %s%s", parent.Title, st.CreatedBy, optionalComment(st.Description)),
				st.CreatedBy, st.Assignee)
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, st)
}

// handleGetSubtask 获取子任务详情。
// 派发语义: 指派人首次查看即认领——记录 viewed_at, 并把 pending 自动推进为 in_progress。
func (s *Service) handleGetSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sidStr := r.PathValue("sid")
	tenantID := auth.TenantFromContext(r.Context())

	sid, err := strconv.ParseInt(sidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid subtask id")
		return
	}
	st, err := s.store.GetSubtask(r.Context(), sid, id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "subtask not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	viewer := actorOf(r, "")
	// 仅指派人本人的首次查看触发认领; 旁观者(创建人/管理员)查看不改状态。
	if st.Assignee != "" && strings.EqualFold(viewer, st.Assignee) && st.ViewedAt == nil {
		now := time.Now()
		updated := *st
		updated.ViewedAt = &now
		if updated.Status == model.SubtaskPending {
			updated.Status = model.SubtaskInProgress
			updated.StartedAt = &now
		}
		if err := s.store.UpdateSubtask(r.Context(), &updated); err == nil {
			st = &updated
			if parent, perr := s.store.Get(r.Context(), id, tenantID); perr == nil && parent != nil {
				s.notifySubtaskInbox(r.Context(), parent, st, msgLevelInfo,
					"子任务已受理: "+st.Title,
					fmt.Sprintf("处理人 %s 已查看并开始处理", viewer),
					viewer, st.CreatedBy)
			}
		}
	}
	httputil.WriteJSON(w, http.StatusOK, st)
}

func (s *Service) handleListSubtasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	subtasks, err := s.store.ListSubtasks(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subtasks == nil {
		subtasks = []*model.Subtask{}
	}
	httputil.WriteJSON(w, http.StatusOK, subtasks)
}

func (s *Service) handleUpdateSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sidStr := r.PathValue("sid")
	tenantID := auth.TenantFromContext(r.Context())

	sid, err := strconv.ParseInt(sidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid subtask id")
		return
	}

	var req model.UpdateSubtaskRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 先取原记录, 未传字段保持原值
	cur, err := s.store.GetSubtask(r.Context(), sid, id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "subtask not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 值拷贝后再更新: 避免直接改 store 返回的内部引用, 使 store 能判断"从非 done 变 done"以记录完成时间
	updated := *cur
	if req.Title != "" {
		updated.Title = req.Title
	}
	if req.Description != "" {
		updated.Description = req.Description
	}
	if req.Assignee != "" {
		updated.Assignee = req.Assignee
	}
	if req.Status != "" {
		switch req.Status {
		case model.SubtaskPending, model.SubtaskInProgress, model.SubtaskDone, model.SubtaskBlocked:
			updated.Status = req.Status
		default:
			httputil.WriteError(w, http.StatusBadRequest, "invalid status")
			return
		}
	}
	if req.DueAt != nil {
		updated.DueAt = req.DueAt
	}
	// 处理情况: 指针语义, 不传保持原值, 传空串表示清空。
	if req.Detail != nil {
		updated.Detail = *req.Detail
	}
	if req.CloseWithParent != nil {
		updated.CloseWithParent = *req.CloseWithParent
	}
	operator := actorOf(r, req.UpdatedBy)
	updated.UpdatedBy = operator
	// 处理人动手即视为已接收, 补记查看时间(避免只走更新未走详情接口的场景漏记)。
	if updated.ViewedAt == nil && updated.Assignee != "" && strings.EqualFold(operator, updated.Assignee) {
		now := time.Now()
		updated.ViewedAt = &now
	}

	if err := s.store.UpdateSubtask(r.Context(), &updated); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "subtask not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 状态流转知会派发人: 完成/受阻是主单负责人需要感知的节点。
	if req.Status != "" && req.Status != cur.Status {
		if parent, perr := s.store.Get(r.Context(), id, tenantID); perr == nil && parent != nil {
			switch updated.Status {
			case model.SubtaskDone:
				s.notifySubtaskInbox(r.Context(), parent, &updated, msgLevelInfo,
					"子任务已完成: "+updated.Title,
					fmt.Sprintf("处理人: %s%s", operator, optionalComment(updated.Detail)),
					operator, updated.CreatedBy, parent.Assignee)
			case model.SubtaskBlocked:
				s.notifySubtaskInbox(r.Context(), parent, &updated, msgLevelWarning,
					"子任务受阻: "+updated.Title,
					fmt.Sprintf("处理人: %s%s", operator, optionalComment(updated.Detail)),
					operator, updated.CreatedBy, parent.Assignee)
			}
		}
	}
	httputil.WriteJSON(w, http.StatusOK, &updated)
}

func (s *Service) handleDeleteSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sidStr := r.PathValue("sid")
	tenantID := auth.TenantFromContext(r.Context())

	sid, err := strconv.ParseInt(sidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid subtask id")
		return
	}

	if err := s.store.DeleteSubtask(r.Context(), sid, id, tenantID); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "subtask not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
