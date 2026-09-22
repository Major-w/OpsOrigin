package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/docextract"
	"github.com/opsorigin/core/pkg/httputil"
)

// —— 附件解析入库 ——
// 背景: 此前上传的附件只是"挂在文章上的文件柜", 内容不切片、不嵌入, AI 问答完全看不到。
// 本文件把附件二进制抽成纯文本 → chunkMarkdown 切块 → 逐块 embedding 写入 knowledge_chunks
// (source='attachment', attachment_id=附件ID), 与正文块同表同结构, 因此复用现有检索 SQL 即可被召回。

const (
	// 超过此大小的附件不做解析入库(纯存储/下载), 避免大文件阻塞上传请求。
	attachmentIndexMaxBytes = 20 << 20
	// 抽取文本截断上限, 防超长文档拖垮 embedding 调用。
	attachmentIndexMaxText = 2 << 20
)

// parseableExt 可抽取文本并入库的扩展名集合。
var parseableExt = map[string]bool{
	".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
	".md": true, ".markdown": true, ".txt": true, ".text": true, ".log": true,
	".csv": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".sql": true, ".html": true, ".htm": true, ".xml": true,
	".go": true, ".py": true, ".js": true, ".ts": true, ".java": true, ".sh": true,
}

// mimeByExt 按扩展名推断 MIME。浏览器上传常给 application/octet-stream,
// 直接存库会让下载/预览拿到错误类型, 故上传时校正一次。
var mimeByExt = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".doc":  "application/msword",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".xls":  "application/vnd.ms-excel",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".ppt":  "application/vnd.ms-powerpoint",
	".md":   "text/markdown; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".yaml": "text/yaml; charset=utf-8",
	".yml":  "text/yaml; charset=utf-8",
	".xml":  "application/xml; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".zip":  "application/zip",
	".gz":   "application/gzip",
	".tar":  "application/x-tar",
}

// normalizeFileType 用扩展名校正 MIME: 仅当浏览器给的类型为空或泛化(octet-stream)时覆盖,
// 有明确类型时保留(避免把 text/plain 之类被误改)。
func normalizeFileType(given, name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if m, ok := mimeByExt[ext]; ok {
		if given == "" || given == "application/octet-stream" || given == "application/octetstream" {
			return m
		}
		return given
	}
	if given == "" {
		return "application/octet-stream"
	}
	return given
}

// extractAttachmentText 按扩展名把附件二进制抽成纯文本。
// 返回 (text, err); err 非 nil 表示不支持或抽取失败(调用方记录为 index_error, 不影响存储)。
func extractAttachmentText(name string, data []byte) (string, error) {
	return docextract.ExtractText(name, data)
}

// handleListArticleChunks 列出文章正文切块(分块可视化: 看文章被切成哪几段、每段多大)。
func (s *Service) handleListArticleChunks(w http.ResponseWriter, r *http.Request) {
	articleID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	idx, ok := s.store.(attachmentIndexer)
	if !ok {
		httputil.WriteJSON(w, http.StatusOK, []ChunkRef{})
		return
	}
	list, err := idx.ListArticleChunks(r.Context(), tenantID, articleID, 200)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}

// handleGetChunk 按块 ID 取知识块内容(引用溯源: 跳到原文后定位并高亮该片段)。
func (s *Service) handleGetChunk(w http.ResponseWriter, r *http.Request) {
	articleID := r.PathValue("id")
	chunkID := r.PathValue("chunk")
	tenantID := auth.TenantFromContext(r.Context())
	idx, ok := s.store.(attachmentIndexer)
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "chunk not found")
		return
	}
	c, err := idx.GetChunk(r.Context(), tenantID, articleID, chunkID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c == nil {
		httputil.WriteError(w, http.StatusNotFound, "chunk not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, c)
}

// —— 下载 / 预览响应头 ——

// inlineDangerousExt 内联会导致同源脚本执行(存储型 XSS)的类型, 一律降级为纯文本渲染。
var inlineDangerousExt = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".svg": true, ".mhtml": true,
}

// previewHeaders 生成下载/预览响应头。
// 规则: 默认 inline; ?download=1 强制 attachment; html/htm/svg 强制 text/plain 防 XSS。
func previewHeaders(ct, ext, fileName string, forceDownload bool) (contentType, disposition string) {
	contentType = ct
	if inlineDangerousExt[ext] {
		contentType = "text/plain; charset=utf-8"
	}
	if forceDownload {
		disposition = "attachment; " + contentDispositionFileName(fileName)
		return contentType, disposition
	}
	disposition = "inline; " + contentDispositionFileName(fileName)
	return contentType, disposition
}

// contentDispositionFileName 生成 RFC 6266 / RFC 5987 双写文件名:
// filename= 给老浏览器(仅 ASCII), filename*= 给现代浏览器(UTF-8 百分号编码)。
// 此前直接裸拼中文导致非 ASCII 文件名乱码。
func contentDispositionFileName(name string) string {
	ascii := asciiFileNameFallback(name)
	return `filename="` + ascii + `"; filename*=UTF-8''` + percentEncodeUTF8(name)
}

// asciiFileNameFallback 把非 ASCII 字符替换成 '_', 并转义引号/反斜杠/控制字符。
func asciiFileNameFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\' || r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		case r < 0x80:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "attachment"
	}
	return out
}

// percentEncodeUTF8 按 RFC 5987 attr-char 规则百分号编码 UTF-8 字节。
func percentEncodeUTF8(name string) string {
	const hexDigit = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '!' || c == '#' || c == '$' || c == '&' || c == '+' || c == '-' ||
			c == '.' || c == '^' || c == '_' || c == '`' || c == '|' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigit[c>>4])
		b.WriteByte(hexDigit[c&0xf])
	}
	return b.String()
}

// —— 服务层: 附件索引编排 ——

// attachmentIndexer 具备附件块索引能力的存储(仅 pgStore 实现; MemoryStore 无检索需求)。
type attachmentIndexer interface {
	syncAttachmentChunks(ctx context.Context, tenantID, articleID string, attID int64, title, content string) (chunkIndexResult, error)
	SetAttachmentIndexState(ctx context.Context, tenantID string, attID int64, indexed bool, errMsg string, chunkCount int)
	DeleteAttachmentChunks(ctx context.Context, tenantID string, attID int64) error
	ListAttachmentChunks(ctx context.Context, tenantID string, attID int64, limit int) ([]ChunkRef, error)
	ListArticleChunks(ctx context.Context, tenantID, articleID string, limit int) ([]ChunkRef, error)
	GetChunk(ctx context.Context, tenantID, articleID, chunkID string) (*ChunkRef, error)
}

// indexAttachment 抽取附件文本 → 切块 → embedding 落库, 并回写附件上的索引状态。
// 失败不影响附件本身(仍可下载), 只把原因记到 index_error 供前端提示"未入库"。
func (s *Service) indexAttachment(ctx context.Context, att *ArticleAttachment, data []byte) {
	idx, ok := s.store.(attachmentIndexer)
	if !ok {
		return // MemoryStore: 无向量检索, 跳过
	}
	reason := func(err error) {
		idx.SetAttachmentIndexState(ctx, att.TenantID, att.ID, false, err.Error(), 0)
		att.Indexed = false
		att.IndexError = err.Error()
		att.ChunkCount = 0
	}
	if len(data) > attachmentIndexMaxBytes {
		reason(fmt.Errorf("文件超过 %d MB, 跳过解析入库(仍可下载)", attachmentIndexMaxBytes>>20))
		return
	}
	text, err := extractAttachmentText(att.FileName, data)
	if err != nil {
		reason(err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		reason(fmt.Errorf("未抽取到文本内容"))
		return
	}
	if len(text) > attachmentIndexMaxText {
		text = text[:attachmentIndexMaxText]
	}
	// 首行作为文档标题参与切块 heading, 便于检索结果溯源。
	title := att.FileName
	heading := "# " + title
	content := heading + "\n\n" + text
	res, err := idx.syncAttachmentChunks(ctx, att.TenantID, att.ArticleID, att.ID, title, content)
	if err != nil {
		reason(err)
		return
	}
	if res.missing {
		slog.Warn("knowledge: attachment chunks missing embedding", "att", att.ID, "chunks", res.count)
	}
	// 截断不是失败(内容仍可检索), 但要让用户看见, 否则会以为整篇都入库了。
	note := ""
	if res.truncated {
		note = fmt.Sprintf("文档过长, 仅前 %d 段入库", chunkMaxBlocks)
		slog.Warn("knowledge: attachment exceeded chunk limit, tail content dropped",
			"att", att.ID, "file", att.FileName, "limit", chunkMaxBlocks)
	}
	idx.SetAttachmentIndexState(ctx, att.TenantID, att.ID, true, note, res.count)
	att.Indexed = true
	att.IndexError = note
	att.ChunkCount = res.count
	// 附件内容刚入库, 清 AI 侧检索缓存让它立即可被引用(否则要等 5 分钟 TTL)。
	s.invalidateSearchCache()
	slog.Info("knowledge: attachment indexed", "att", att.ID, "file", att.FileName, "chunks", res.count, "truncated", res.truncated)
}

// loadAttachmentBytes 取附件二进制: 对象存储优先, 否则回退 DB bytea。
func (s *Service) loadAttachmentBytes(ctx context.Context, att *ArticleAttachment) ([]byte, error) {
	if att.ObjectKey != "" && s.objStore != nil {
		var buf bytes.Buffer
		if err := s.objStore.Get(ctx, &buf, att.ObjectKey); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	if len(att.Data) > 0 {
		return att.Data, nil
	}
	return nil, fmt.Errorf("附件二进制不可用")
}

// handleReindexAttachment 手动重建单个附件的知识索引(上传失败/更换解析器后重试)。
func (s *Service) handleReindexAttachment(w http.ResponseWriter, r *http.Request) {
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
		if err == ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := s.loadAttachmentBytes(r.Context(), att)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "无法读取附件内容: "+err.Error())
		return
	}
	s.indexAttachment(r.Context(), att, data)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"id":          att.ID,
		"indexed":     att.Indexed,
		"index_error": att.IndexError,
		"chunk_count": att.ChunkCount,
	})
}

// handleAttachmentText 返回附件抽取出的纯文本(供前端在线预览 Office/PDF 内容, 不落库)。
func (s *Service) handleAttachmentText(w http.ResponseWriter, r *http.Request) {
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
		if err == ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "attachment not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := s.loadAttachmentBytes(r.Context(), att)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "无法读取附件内容: "+err.Error())
		return
	}
	text, err := extractAttachmentText(att.FileName, data)
	if err != nil {
		httputil.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if len(text) > 200000 { // 预览不必给全文
		text = text[:200000]
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"file_name": att.FileName,
		"text":      text,
	})
}

// handleAttachmentChunks 列出附件抽取出的知识块(溯源/调试用)。
func (s *Service) handleAttachmentChunks(w http.ResponseWriter, r *http.Request) {
	aidStr := r.PathValue("aid")
	tenantID := auth.TenantFromContext(r.Context())
	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}
	idx, ok := s.store.(attachmentIndexer)
	if !ok {
		httputil.WriteJSON(w, http.StatusOK, []ChunkRef{})
		return
	}
	list, err := idx.ListAttachmentChunks(r.Context(), tenantID, aid, 50)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, list)
}
