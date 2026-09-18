// AI 通用文本文件生成工具 (file_generate)。
//
// 覆盖 pptx/docx/xlsx/pdf 之外的常见纯文本文档类型: csv / markdown / txt / json / html /
// xml / yaml / sql / log / ini / conf / 源码等。模型给出文件名(含扩展名)与内容, 工具按扩展名
// 推断 MIME, 字节直存对象存储(MinIO)并返回可下载 URL, 闭环"生成任意文本文件"诉求。
//
// 与 pptx_generate 等同构: 纯 Go, 零外部依赖, 仅做字节落地与 MIME 推断。
package agentic

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// textMimeByExt 常见纯文本扩展名 → MIME。未知扩展名回退 text/plain。
var textMimeByExt = map[string]string{
	"csv":        "text/csv; charset=utf-8",
	"tsv":        "text/tab-separated-values; charset=utf-8",
	"md":         "text/markdown; charset=utf-8",
	"markdown":   "text/markdown; charset=utf-8",
	"txt":        "text/plain; charset=utf-8",
	"text":       "text/plain; charset=utf-8",
	"json":       "application/json; charset=utf-8",
	"jsonl":      "application/x-ndjson; charset=utf-8",
	"ndjson":     "application/x-ndjson; charset=utf-8",
	"html":       "text/html; charset=utf-8",
	"htm":        "text/html; charset=utf-8",
	"xml":        "application/xml; charset=utf-8",
	"yaml":       "text/yaml; charset=utf-8",
	"yml":        "text/yaml; charset=utf-8",
	"toml":       "application/toml; charset=utf-8",
	"sql":        "application/sql; charset=utf-8",
	"log":        "text/plain; charset=utf-8",
	"ini":        "text/plain; charset=utf-8",
	"conf":       "text/plain; charset=utf-8",
	"cfg":        "text/plain; charset=utf-8",
	"env":        "text/plain; charset=utf-8",
	"sh":         "text/x-shellscript; charset=utf-8",
	"bash":       "text/x-shellscript; charset=utf-8",
	"zsh":        "text/x-shellscript; charset=utf-8",
	"py":         "text/x-python; charset=utf-8",
	"js":         "text/javascript; charset=utf-8",
	"mjs":        "text/javascript; charset=utf-8",
	"cjs":        "text/javascript; charset=utf-8",
	"ts":         "text/typescript; charset=utf-8",
	"tsx":        "text/typescript; charset=utf-8",
	"jsx":        "text/jsx; charset=utf-8",
	"go":         "text/x-go; charset=utf-8",
	"java":       "text/x-java; charset=utf-8",
	"c":          "text/x-c; charset=utf-8",
	"h":          "text/x-c; charset=utf-8",
	"cpp":        "text/x-c++; charset=utf-8",
	"hpp":        "text/x-c++; charset=utf-8",
	"css":        "text/css; charset=utf-8",
	"scss":       "text/css; charset=utf-8",
	"less":       "text/css; charset=utf-8",
	"rs":         "text/x-rust; charset=utf-8",
	"rb":         "text/x-ruby; charset=utf-8",
	"php":        "text/x-php; charset=utf-8",
	"vue":        "text/x-vue; charset=utf-8",
	"r":          "text/x-r; charset=utf-8",
	"pl":         "text/x-perl; charset=utf-8",
	"lua":        "text/x-lua; charset=utf-8",
	"dockerfile": "text/plain; charset=utf-8",
	"gitignore":  "text/plain; charset=utf-8",
	"lock":       "text/plain; charset=utf-8",
	"properties": "text/plain; charset=utf-8",
	"rst":        "text/plain; charset=utf-8",
	"tex":        "text/plain; charset=utf-8",
	"adoc":       "text/plain; charset=utf-8",
}

// fileGenerate 实现 MCP 工具 file_generate。
func (ts *MCPToolSet) fileGenerate(ctx context.Context, args map[string]any) (any, error) {
	fileName, _ := args["file_name"].(string)
	fileName = strings.TrimSpace(fileName)
	content, _ := args["content"].(string)
	contentType, _ := args["content_type"].(string)
	contentType = strings.TrimSpace(contentType)

	if fileName == "" {
		return nil, fmt.Errorf("file_generate: file_name 必填(需含扩展名, 如 report.md)")
	}
	if content == "" {
		// 允许空文件, 但记一条 warning 便于排查模型漏传。
		slog.Warn("file_generate: content 为空, 将生成 0 字节文件", "file", fileName)
	}

	ext := strings.ToLower(strings.TrimPrefix(path.Ext(fileName), "."))
	ct := contentType
	if ct == "" {
		if m, ok := textMimeByExt[ext]; ok {
			ct = m
		} else {
			ct = "text/plain; charset=utf-8"
		}
	}

	data := []byte(content)
	key := "ai-files/" + uuid.NewString() + "-" + sanitizeFileName(fileName)
	if ts.objStore == nil {
		return nil, fmt.Errorf("file_generate: 未配置对象存储(MinIO), 无法生成可下载的文件")
	}
	if err := ts.objStore.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return nil, fmt.Errorf("file_generate: 对象存储写入失败: %w", err)
	}
	u, err := ts.objStore.PresignedURL(key, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("file_generate: 预签名失败: %w", err)
	}

	slog.Info("file_generate ok", "file", fileName, "ext", ext, "bytes", len(data), "ct", ct)
	return map[string]any{
		"url":          u.String(),
		"file_name":    fileName,
		"size_bytes":   len(data),
		"content_type": ct,
	}, nil
}

// sanitizeFileName 仅保留文件名中的安全字符, 避免对象存储 key 注入。
func sanitizeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "file"
	}
	return out
}
