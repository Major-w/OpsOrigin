// AI 对话多模态输入: 用户上传的图片/文档 → 模型可理解的 content parts。
//
// 设计要点:
//  1. 会话持久化只存"附件引用"(attachment://<id> 或 attachment://<id>#<name>), 不存 base64 二进制 ——
//     否则一张 2MB 图片会把 ai_conversations.messages 文本撑大 2.7MB, 历史回放/列表查询都会拖垮。
//  2. 每次请求发送前由本文件把引用解析为 data: URL(图片) 或正文文本(纯文本类文档),
//     因此历史消息回放时图片依然"可见"。
//  3. 附件按 tenant_id 隔离读取(越权读取直接跳过), 与 handleDownloadAttachment 同一约束。
package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/docextract"
	"github.com/opsorigin/core/pkg/llm"
)

// AttachmentRef 前端随对话提交的附件引用(上传成功后由 POST /ai/attachments 返回)。
type AttachmentRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"` // MIME, 前端可省略(后端以库内记录为准)
}

// attachmentScheme 内部引用协议: 持久化态 URL 形如 attachment://<id>#<filename>。
const attachmentScheme = "attachment://"

// maxInlineImageBytes 单图内联上限 8MB: 超过则不下发(模型侧也有限制, 且 base64 会再膨胀 33%)。
const maxInlineImageBytes = 8 << 20

// maxInlineTextBytes 文本类附件注入正文的上限 256KB(超出截断, 避免挤爆上下文窗口)。
const maxInlineTextBytes = 256 << 10

// imageExts 可作为图片内联下发的扩展名。
var imageExts = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

// textExts 可作为文本直接注入正文的扩展名(日志/配置/文档片段等, 运维场景高频)。
var textExts = map[string]bool{
	".txt": true, ".log": true, ".md": true, ".csv": true, ".json": true,
	".yaml": true, ".yml": true, ".ini": true, ".conf": true, ".xml": true, ".sql": true,
}

// BuildContentParts 把本次请求的附件引用转为"持久化态"多模态片段:
// 只写 attachment://<id>#<name> 引用, 不读对象存储 —— 真正的二进制解析推迟到发送前由 ResolvePartURL 完成。
// 这样会话表里存的是几十字节的引用而非 MB 级 base64(历史回放/列表查询不会被撑垮)。
// 返回 nil 表示无有效附件(调用方退回纯文本)。
func (s *Service) BuildContentParts(ctx context.Context, tenantID, prompt string, refs []AttachmentRef) []llm.ContentPart {
	if len(refs) == 0 {
		return nil
	}
	parts := []llm.ContentPart{{Type: "text", Text: prompt}}
	n := 0
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		att, ok := s.loadAttachment(ctx, tenantID, ref.ID)
		if !ok {
			slog.Warn("ai multimodal attachment skipped", "id", ref.ID, "reason", "not found or forbidden")
			continue
		}
		parts = append(parts, placeholderPart(att))
		n++
	}
	if n == 0 {
		return nil // 附件全部无效: 退回纯文本, 不让模型收到空片段数组
	}
	return parts
}

// placeholderPart 生成持久化态片段(仅引用, 不含二进制)。
func placeholderPart(att ChatAttachment) llm.ContentPart {
	ext := strings.ToLower(filepath.Ext(att.FileName))
	ref := attachmentScheme + att.ID + "#" + att.FileName
	if _, ok := imageExts[ext]; ok {
		return llm.ContentPart{Type: "image_url", ImageURL: &llm.ImageURLPart{URL: ref}}
	}
	// 文本类/其它类型统一走 file_url: 由 ResolvePartURL 在发送前决定"注入正文"还是"占位提示"。
	return llm.ContentPart{Type: "file_url", FileURL: &llm.FileURLPart{URL: ref, Name: att.FileName}}
}

// ResolvePartURL 发送前解析 attachment://<id>#<name> 引用 → 模型可消费的片段。
// 以 SetPartResolver 形式注入 Engine, 由 chatLLM/streamLLM 在真正下发前调用;
// 每次请求都重新读取对象存储, 因此历史回放时附件依然"可见"(前提: 附件记录未被清理)。
func (s *Service) ResolvePartURL(ctx context.Context, url string) (llm.ContentPart, bool) {
	if !strings.HasPrefix(url, attachmentScheme) {
		return llm.ContentPart{}, false
	}
	raw := strings.TrimPrefix(url, attachmentScheme)
	name := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		name, raw = raw[i+1:], raw[:i]
	}
	tenantID := auth.TenantFromContext(ctx)
	att, ok := s.loadAttachment(ctx, tenantID, raw)
	if !ok {
		slog.Warn("ai multimodal resolve skipped", "url", url, "reason", "attachment not found")
		return llm.ContentPart{Type: "text", Text: "[附件已不可读，可能已被清理]"}, true
	}
	if name != "" {
		att.FileName = name
	}
	sub := s.partForAttachment(ctx, att)
	if len(sub) == 0 {
		return llm.ContentPart{}, false
	}
	// 多片段(理论上仅 1 个)合并为单片段: 契约要求一对一替换。
	if len(sub) == 1 {
		return sub[0], true
	}
	var b strings.Builder
	for _, p := range sub {
		b.WriteString(p.Text)
	}
	return llm.ContentPart{Type: "text", Text: b.String()}, true
}

// partForAttachment 按附件类型生成片段: 图片内联 / 文本注入 / 其它占位。
func (s *Service) partForAttachment(ctx context.Context, att ChatAttachment) []llm.ContentPart {
	ext := strings.ToLower(filepath.Ext(att.FileName))
	if mime, ok := imageExts[ext]; ok {
		data, err := s.readObject(ctx, att)
		if err != nil {
			slog.Warn("ai multimodal read image failed", "id", att.ID, "err", err)
			return []llm.ContentPart{{Type: "text", Text: fmt.Sprintf("[图片 %s 读取失败]", att.FileName)}}
		}
		if len(data) > maxInlineImageBytes {
			return []llm.ContentPart{{Type: "text", Text: fmt.Sprintf("[图片 %s 超过 %dMB 上限，已跳过]", att.FileName, maxInlineImageBytes>>20)}}
		}
		return []llm.ContentPart{{
			Type:     "image_url",
			ImageURL: &llm.ImageURLPart{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)},
		}}
	}
	if textExts[ext] {
		data, err := s.readObject(ctx, att)
		if err != nil {
			return []llm.ContentPart{{Type: "text", Text: fmt.Sprintf("[文件 %s 读取失败]", att.FileName)}}
		}
		body := string(data)
		if len(body) > maxInlineTextBytes {
			body = body[:maxInlineTextBytes] + "\n...(已截断)"
		}
		return []llm.ContentPart{{
			Type: "text",
			Text: fmt.Sprintf("\n\n【附件 %s 内容】\n%s", att.FileName, body),
		}}
	}
	// PDF / Office(OOXML) / HTML: 服务端抽成纯文本后注入正文。
	// 此前这里只回一句"暂不支持", 等于上传了也白传 —— 与知识库附件解析入库共用 pkg/docextract,
	// 保证同一个文件在"对话"和"知识库"两条链路上抽出的内容一致。
	if docextract.Parseable(att.FileName) {
		data, err := s.readObject(ctx, att)
		if err != nil {
			return []llm.ContentPart{{Type: "text", Text: fmt.Sprintf("[文件 %s 读取失败]", att.FileName)}}
		}
		body, err := docextract.ExtractText(att.FileName, data)
		if err != nil {
			slog.Warn("ai multimodal extract failed", "id", att.ID, "file", att.FileName, "err", err)
			return []llm.ContentPart{{
				Type: "text",
				Text: fmt.Sprintf("\n\n[用户上传了附件 %s（%s，%s）。服务端解析失败: %s]",
					att.FileName, att.FileType, formatSizeShort(att.FileSize), err.Error()),
			}}
		}
		if len(body) > maxInlineTextBytes {
			body = body[:maxInlineTextBytes] + "\n...(已截断)"
		}
		return []llm.ContentPart{{
			Type: "text",
			Text: fmt.Sprintf("\n\n【附件 %s 内容】\n%s", att.FileName, body),
		}}
	}
	// 其余二进制(压缩包/音视频等): 模型侧无通用内联协议, 明确告知, 避免模型"假装看懂"。
	return []llm.ContentPart{{
		Type: "text",
		Text: fmt.Sprintf("\n\n[用户上传了附件 %s（%s，%s）。当前模型暂不支持直接解析该格式，请告知用户改用图片、文本或 PDF/Office 文档上传]",
			att.FileName, att.FileType, formatSizeShort(att.FileSize)),
	}}
}

// loadAttachment 按租户隔离读取附件元数据。
func (s *Service) loadAttachment(ctx context.Context, tenantID, id string) (ChatAttachment, bool) {
	if s.db == nil || id == "" {
		return ChatAttachment{}, false
	}
	var a ChatAttachment
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, owner_id, file_name, file_type, file_size, object_key, created_at
		 FROM ai_chat_attachments WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.OwnerID, &a.FileName, &a.FileType, &a.FileSize, &a.ObjectKey, &a.CreatedAt)
	if err != nil || a.ObjectKey == "" {
		return ChatAttachment{}, false
	}
	return a, true
}

// readObject 从对象存储读取附件字节(带上限保护)。
func (s *Service) readObject(ctx context.Context, att ChatAttachment) ([]byte, error) {
	if s.objStore == nil {
		return nil, fmt.Errorf("object store not configured")
	}
	var buf bytes.Buffer
	// 比实际需要多读 1 字节以便识别超限(读超限时 Go 不会报错, 需自行判断长度)。
	limit := maxInlineImageBytes
	if att.FileSize > int64(limit) {
		limit = int(att.FileSize)
	}
	if err := s.objStore.Get(ctx, &limitedWriter{w: &buf, limit: limit}, att.ObjectKey); err != nil && buf.Len() == 0 {
		return nil, err
	}
	return buf.Bytes(), nil
}

// limitedWriter 限长写入: 达到上限后静默丢弃剩余字节(防止超大附件把内存打爆)。
type limitedWriter struct {
	w     io.Writer
	limit int
	n     int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remain := lw.limit - lw.n
	if remain <= 0 {
		return len(p), nil // 丢弃但报告成功, 不中断读取
	}
	if len(p) > remain {
		p = p[:remain]
	}
	n, err := lw.w.Write(p)
	lw.n += n
	return n, err
}
