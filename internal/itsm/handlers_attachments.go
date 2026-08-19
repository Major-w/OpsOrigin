package itsm

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	uploadedBy := r.FormValue("uploaded_by")
	if uploadedBy == "" {
		uploadedBy = "anonymous"
	}

	att := &model.Attachment{
		TicketID:   id,
		TenantID:   tenantID,
		FileName:   header.Filename,
		FileSize:   header.Size,
		FileType:   header.Header.Get("Content-Type"),
		UploadedBy: uploadedBy,
		Data:       data,
	}
	// 对象存储已配置: 二进制进对象存储, DB 只存 object_key; 否则回退数据库 bytea。
	if s.objStore != nil {
		key := fmt.Sprintf("itsm/%s/%s", tenantID, uuid.NewString())
		if err := s.objStore.Put(r.Context(), key, bytes.NewReader(data), int64(len(data)), header.Header.Get("Content-Type")); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "failed to store attachment: "+err.Error())
			return
		}
		att.ObjectKey = key
		att.Data = nil
	}
	if err := s.store.AddAttachment(r.Context(), att); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to save attachment")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, att)
}

func (s *Service) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	atts, err := s.store.ListAttachments(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if atts == nil {
		atts = []*model.Attachment{}
	}
	httputil.WriteJSON(w, http.StatusOK, atts)
}

func (s *Service) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aidStr := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())

	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}

	att, err := s.store.GetAttachment(r.Context(), aid, id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=\""+att.FileName+"\"")
	if att.FileType != "" {
		w.Header().Set("Content-Type", att.FileType)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(att.FileSize, 10))
	// 对象存储模式: 从对象存储流式输出; 否则输出数据库 bytea。
	if att.ObjectKey != "" && s.objStore != nil {
		if err := s.objStore.Get(r.Context(), w, att.ObjectKey); err != nil {
			slog.Warn("attachment object read failed", "key", att.ObjectKey, "err", err)
			return
		}
		return
	}
	w.Write(att.Data)
}

func (s *Service) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aidStr := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())

	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}

	// 先取元数据: 对象存储模式下需一并删除对象, 避免孤儿文件。
	att, err := s.store.GetAttachment(r.Context(), aid, id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if att.ObjectKey != "" && s.objStore != nil {
		if err := s.objStore.Remove(r.Context(), att.ObjectKey); err != nil {
			slog.Warn("attachment object remove failed", "key", att.ObjectKey, "err", err)
		}
	}
	if err := s.store.DeleteAttachment(r.Context(), aid, id, tenantID); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
