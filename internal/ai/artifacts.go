// 对话产物归档: 将 AI 对话中生成的文档/报告保存为文件, 二进制落对象存储(MinIO),
// 元数据(含 object_key)落库, 支持列表/下载/删除。未配置对象存储时降级为元数据留存。
package ai

import (
	"context"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ChatArtifact 对话产物元数据。
type ChatArtifact struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	FileName       string    `json:"file_name"`
	FileType       string    `json:"file_type"`
	FileSize       int64     `json:"file_size"`
	ObjectKey      string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

// initArtifactDDL 幂等建表。
func (s *Service) initArtifactDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_chat_artifacts (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_type TEXT NOT NULL DEFAULT 'text/markdown',
			file_size BIGINT NOT NULL DEFAULT 0,
			object_key TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai chat artifacts DDL failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		ALTER TABLE ai_chat_artifacts ADD COLUMN IF NOT EXISTS object_key TEXT NOT NULL DEFAULT ''`); err != nil {
		slog.Warn("ai chat artifacts alter failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_artifacts_conv ON ai_chat_artifacts(conversation_id, tenant_id)`); err != nil {
		slog.Warn("ai chat artifacts index failed", "err", err)
	}
}

// artifactKey 生成对象存储 key: chat/<conversation>/<uuid><ext>。
func artifactKey(convID, fileName string) string {
	ext := filepath.Ext(fileName)
	if ext == "" {
		ext = ".md"
	}
	return "chat/" + convID + "/" + uuid.New().String() + ext
}

// artifactContentType 按文件名推断 Content-Type。
func artifactContentType(fileName string, fallback string) string {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".txt", ".log":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".html":
		return "text/html"
	case ".pdf":
		return "application/pdf"
	default:
		if fallback != "" {
			return fallback
		}
		return "application/octet-stream"
	}
}

// handleSaveArtifact 保存对话产物(文本内容 → 对象存储 + 元数据)。
func (s *Service) handleSaveArtifact(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		MessageID string `json:"message_id"`
		FileName  string `json:"file_name"`
		Content   string `json:"content"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "content required")
		return
	}
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" {
		fileName = "ai-" + time.Now().Format("20060102-150405") + ".md"
	}
	contentType := artifactContentType(fileName, "text/markdown")
	key := artifactKey(convID, fileName)

	// 落对象存储(未配置则跳过, 仅存元数据)
	content := []byte(req.Content)
	if s.objStore != nil {
		if err := s.objStore.Put(r.Context(), key, strings.NewReader(req.Content), int64(len(content)), contentType); err != nil {
			slog.Warn("ai artifact put failed, fallback to metadata-only", "err", err)
			key = ""
		}
	}

	art := ChatArtifact{
		ID: uuid.New().String(), TenantID: tenantID, ConversationID: convID,
		MessageID: req.MessageID, FileName: fileName, FileType: contentType,
		FileSize: int64(len(content)), ObjectKey: key, CreatedAt: time.Now(),
	}
	if s.db != nil {
		if _, err := s.db.ExecContext(r.Context(),
			`INSERT INTO ai_chat_artifacts (id, tenant_id, conversation_id, message_id, file_name, file_type, file_size, object_key, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			art.ID, tenantID, convID, req.MessageID, fileName, contentType, art.FileSize, key, art.CreatedAt); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, art)
}

// handleListArtifacts 产物列表。
func (s *Service) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteJSON(w, http.StatusOK, []ChatArtifact{})
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, tenant_id, conversation_id, message_id, file_name, file_type, file_size, object_key, created_at
		 FROM ai_chat_artifacts WHERE conversation_id=$1 AND tenant_id=$2 ORDER BY created_at DESC`, convID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []ChatArtifact{}
	for rows.Next() {
		var a ChatArtifact
		if err := rows.Scan(&a.ID, &a.TenantID, &a.ConversationID, &a.MessageID, &a.FileName, &a.FileType, &a.FileSize, &a.ObjectKey, &a.CreatedAt); err != nil {
			continue
		}
		out = append(out, a)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// handleDownloadArtifact 产物下载(对象存储 → 流式)。
func (s *Service) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	aid := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	var a ChatArtifact
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, tenant_id, conversation_id, message_id, file_name, file_type, file_size, object_key, created_at
		 FROM ai_chat_artifacts WHERE id=$1 AND conversation_id=$2 AND tenant_id=$3`, aid, convID, tenantID).
		Scan(&a.ID, &a.TenantID, &a.ConversationID, &a.MessageID, &a.FileName, &a.FileType, &a.FileSize, &a.ObjectKey, &a.CreatedAt)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if a.ObjectKey == "" || s.objStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "object store disabled or artifact not stored")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+a.FileName+"\"")
	w.Header().Set("Content-Type", a.FileType)
	w.Header().Set("Content-Length", strconv.FormatInt(a.FileSize, 10))
	if err := s.objStore.Get(r.Context(), w, a.ObjectKey); err != nil {
		slog.Warn("ai artifact get failed", "id", aid, "err", err)
	}
}

// handleDeleteArtifact 删除产物(先删对象防孤儿)。
func (s *Service) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	aid := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	var key string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT object_key FROM ai_chat_artifacts WHERE id=$1 AND conversation_id=$2 AND tenant_id=$3`, aid, convID, tenantID).Scan(&key)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if key != "" && s.objStore != nil {
		if err := s.objStore.Remove(r.Context(), key); err != nil {
			slog.Warn("ai artifact remove failed", "id", aid, "err", err)
		}
	}
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM ai_chat_artifacts WHERE id=$1 AND conversation_id=$2 AND tenant_id=$3`, aid, convID, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
