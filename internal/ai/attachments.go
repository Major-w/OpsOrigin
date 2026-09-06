// AI 对话附件: 用户在对话中上传的图片/文件, 二进制落对象存储(MinIO), 元数据落库。
// 与 chat artifacts(产物归档)独立: 附件是输入侧(用户上传), 产物是输出侧(AI 生成)。
package ai

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/principal"
)

// maxChatAttachmentSize 单附件上限 20MB(对话场景图片/日志/文档足够)。
const maxChatAttachmentSize = 20 << 20

// ChatAttachment 对话附件元数据。
type ChatAttachment struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	OwnerID   string    `json:"owner_id,omitempty"`
	FileName  string    `json:"file_name"`
	FileType  string    `json:"file_type"`
	FileSize  int64     `json:"file_size"`
	ObjectKey string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// initAttachmentDDL 幂等建表。
func (s *Service) initAttachmentDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_chat_attachments (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			owner_id TEXT NOT NULL DEFAULT '',
			file_name TEXT NOT NULL,
			file_type TEXT NOT NULL DEFAULT 'application/octet-stream',
			file_size BIGINT NOT NULL DEFAULT 0,
			object_key TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai chat attachments DDL failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_attachments_tenant ON ai_chat_attachments(tenant_id, owner_id)`); err != nil {
		slog.Warn("ai chat attachments index failed", "err", err)
	}
}

// attachmentContentType 按扩展名推断展示用 Content-Type(图片供前端缩略/预览判断)。
func attachmentContentType(fileName string, fallback string) string {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".log":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	default:
		if fallback != "" {
			return fallback
		}
		return "application/octet-stream"
	}
}

// handleUploadAttachment 上传对话附件(multipart file 字段 → 对象存储 + 元数据落库)。
func (s *Service) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.objStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "object store not configured")
		return
	}
	if err := r.ParseMultipartForm(maxChatAttachmentSize); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "file field required")
		return
	}
	defer f.Close()
	if header.Size > maxChatAttachmentSize {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, "file too large (max 20MB)")
		return
	}
	fileName := filepath.Base(header.Filename)
	if fileName == "" || fileName == "." {
		fileName = "attachment-" + time.Now().Format("20060102-150405")
	}
	tenantID := auth.TenantFromContext(r.Context())
	ownerID := ""
	if pr, ok := principal.FromContext(r.Context()); ok {
		ownerID = pr.ID
	}
	contentType := attachmentContentType(fileName, header.Header.Get("Content-Type"))
	key := "chat-attach/" + uuid.New().String() + filepath.Ext(fileName)
	if err := s.objStore.Put(r.Context(), key, f, header.Size, contentType); err != nil {
		slog.Warn("ai attachment put failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "store object failed")
		return
	}
	att := ChatAttachment{
		ID: uuid.New().String(), TenantID: tenantID, OwnerID: ownerID,
		FileName: fileName, FileType: contentType, FileSize: header.Size,
		ObjectKey: key, CreatedAt: time.Now(),
	}
	if s.db != nil {
		if _, err := s.db.ExecContext(r.Context(),
			`INSERT INTO ai_chat_attachments (id, tenant_id, owner_id, file_name, file_type, file_size, object_key, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			att.ID, tenantID, ownerID, fileName, contentType, header.Size, key, att.CreatedAt); err != nil {
			slog.Warn("ai attachment insert failed", "err", err)
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, att)
}

// handleDownloadAttachment 附件下载/预览(inline: 图片可直接 <img>/新标签打开)。
func (s *Service) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	var a ChatAttachment
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, tenant_id, owner_id, file_name, file_type, file_size, object_key, created_at
		 FROM ai_chat_attachments WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.OwnerID, &a.FileName, &a.FileType, &a.FileSize, &a.ObjectKey, &a.CreatedAt)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if a.ObjectKey == "" || s.objStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "object store disabled or attachment not stored")
		return
	}
	w.Header().Set("Content-Type", a.FileType)
	w.Header().Set("Content-Length", strconv.FormatInt(a.FileSize, 10))
	// inline: 图片/文本浏览器直接预览; 非浏览器(下载)由前端加 download 属性落盘。
	w.Header().Set("Content-Disposition", "inline; filename=\""+strings.ReplaceAll(a.FileName, "\"", "")+"\"")
	if err := s.objStore.Get(r.Context(), w, a.ObjectKey); err != nil {
		slog.Warn("ai attachment get failed", "id", id, "err", err)
	}
}

// 附件元数据在对话消息中的文本标记(持久化进会话 user 消息, 前端加载时解析回 chip 渲染)。
// 格式: 每行一条 "[附件] 文件名 (类型, 大小)", 追加在用户正文之后。
const attachmentMarker = "[附件] "

// FormatAttachmentNote 把附件元数据格式化为追加到 prompt 的标记文本(供模型感知 + 会话持久化)。
func FormatAttachmentNote(att []ChatAttachment) string {
	if len(att) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n")
	for i, a := range att {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(attachmentMarker + a.FileName + " (" + a.FileType + ", " + formatSizeShort(a.FileSize) + ")")
	}
	return b.String()
}

// formatSizeShort 人类可读文件大小。
func formatSizeShort(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}
