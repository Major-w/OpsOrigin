// 会话工作区: 工具执行产物自动沉淀区(轻量伪沙箱)。引擎在工具执行后经
// WorkspaceSink 回调写入, 前端对话区可查看/下载/删除。文本内容直接落库。
package ai

import (
	"context"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WorkspaceFile 会话工作区文件。
type WorkspaceFile struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	ConversationID string    `json:"conversation_id"`
	ToolName       string    `json:"tool_name"`
	FileName       string    `json:"file_name"`
	FileType       string    `json:"file_type"`
	FileSize       int64     `json:"file_size"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

// initWorkspaceDDL 幂等建表。
func (s *Service) initWorkspaceDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_workspace_files (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			file_name TEXT NOT NULL,
			file_type TEXT NOT NULL DEFAULT 'application/json',
			file_size BIGINT NOT NULL DEFAULT 0,
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai workspace DDL failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_ai_workspace_conv ON ai_workspace_files(conversation_id, tenant_id)`); err != nil {
		slog.Warn("ai workspace index failed", "err", err)
	}
}

// SaveWorkspaceFile 引擎 WorkspaceSink 回调: 工具产物写入会话工作区。
func (s *Service) SaveWorkspaceFile(ctx context.Context, conversationID, toolName, fileName string, content []byte) error {
	if s.db == nil {
		return nil
	}
	text := string(content)
	ft := "application/json"
	if strings.HasSuffix(fileName, ".txt") {
		ft = "text/plain"
	} else if strings.HasSuffix(fileName, ".md") {
		ft = "text/markdown"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_workspace_files (id, tenant_id, conversation_id, tool_name, file_name, file_type, file_size, content, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		uuid.New().String(), "default", conversationID, toolName, fileName, ft, int64(len(text)), text, time.Now())
	if err != nil {
		slog.Warn("ai workspace insert failed", "conv", conversationID, "tool", toolName, "err", err)
	}
	return err
}

// handleListWorkspace 工作区文件列表(含内容预览)。
func (s *Service) handleListWorkspace(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteJSON(w, http.StatusOK, []WorkspaceFile{})
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, tenant_id, conversation_id, tool_name, file_name, file_type, file_size, content, created_at
		 FROM ai_workspace_files WHERE conversation_id=$1 AND tenant_id=$2 ORDER BY created_at DESC`, convID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []WorkspaceFile{}
	for rows.Next() {
		var f WorkspaceFile
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ConversationID, &f.ToolName, &f.FileName, &f.FileType, &f.FileSize, &f.Content, &f.CreatedAt); err != nil {
			continue
		}
		out = append(out, f)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// handleDownloadWorkspace 工作区文件下载(文本内容直接返回)。
func (s *Service) handleDownloadWorkspace(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	fid := r.PathValue("fid")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	var f WorkspaceFile
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, tenant_id, conversation_id, tool_name, file_name, file_type, file_size, content, created_at
		 FROM ai_workspace_files WHERE id=$1 AND conversation_id=$2 AND tenant_id=$3`, fid, convID, tenantID).
		Scan(&f.ID, &f.TenantID, &f.ConversationID, &f.ToolName, &f.FileName, &f.FileType, &f.FileSize, &f.Content, &f.CreatedAt)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "workspace file not found")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+f.FileName+"\"")
	w.Header().Set("Content-Type", f.FileType)
	w.Header().Set("Content-Length", strconv.FormatInt(f.FileSize, 10))
	_, _ = w.Write([]byte(f.Content))
}

// handleDeleteWorkspace 删除工作区文件。
func (s *Service) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	fid := r.PathValue("fid")
	tenantID := auth.TenantFromContext(r.Context())
	if s.db == nil {
		httputil.WriteError(w, http.StatusNotFound, "db unavailable")
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM ai_workspace_files WHERE id=$1 AND conversation_id=$2 AND tenant_id=$3`, fid, convID, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
