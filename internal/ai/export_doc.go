// 服务端确定性文档导出(md → docx/pptx): 前端浏览器转换(见 web/src/lib/doc-export.ts)
// 的 fallback 通道——内容超保险丝(2MB)或前端转换失败时走这里。
// 转换引擎 = pandoc 单二进制(同时支持 docx/pptx writer), 不引入容器/沙箱;
// 未安装 pandoc 时返回 501, 前端提示降级。纯内容转换, 无任意代码执行面。
package ai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/httputil"
)

const (
	maxExportContentBytes = 20 << 20 // 服务端通道上限 20MB(前端保险丝 2MB 的兜底空间)
	exportTimeout         = 90 * time.Second
)

var exportContentTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// handleExportDoc POST /api/v1/ai/export  body: {format: "docx"|"pptx", title: string, content: markdown}
func (s *Service) handleExportDoc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format  string `json:"format"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		return
	}
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	ct, ok := exportContentTypes[req.Format]
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "format 仅支持 docx/pptx")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "content 为空")
		return
	}
	if len(req.Content) > maxExportContentBytes {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, "内容超过服务端导出上限(20MB)")
		return
	}

	pandoc, err := exec.LookPath("pandoc")
	if err != nil {
		httputil.WriteError(w, http.StatusNotImplemented, "服务端未安装 pandoc, 服务端导出不可用(部署需 apk add pandoc)")
		return
	}

	dir, err := os.MkdirTemp("", "ai-export-*")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "创建临时目录失败")
		return
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "input.md")
	if err := os.WriteFile(src, []byte(req.Content), 0o600); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "写入临时文件失败")
		return
	}
	out := filepath.Join(dir, "output."+req.Format)

	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	// -- standalone: 产出独立文件; media-dir 隔离临时目录防止越界写
	cmd := exec.CommandContext(ctx, pandoc, "--standalone", "--media-dir", dir, "-f", "markdown", "-t", req.Format, "-o", out, src)
	if outb, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("ai server export pandoc failed", "format", req.Format, "err", err, "out", firstLine(string(outb)))
		if ctx.Err() != nil {
			httputil.WriteError(w, http.StatusGatewayTimeout, "转换超时")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "pandoc 转换失败: "+firstLine(string(outb)))
		return
	}

	data, err := os.ReadFile(out)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "读取转换产物失败")
		return
	}

	name := sanitizeExportName(req.Title, "export") + "." + req.Format
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Content-Disposition", `attachment; filename="export.`+req.Format+`"; filename*=UTF-8''`+url.PathEscape(name))
	_, _ = w.Write(data)
}

func sanitizeExportName(title, fallback string) string {
	t := strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', '#', '\n', '\r':
			return '_'
		}
		return r
	}, strings.TrimSpace(title))
	if t == "" {
		return fallback
	}
	if len(t) > 80 {
		t = t[:80]
	}
	return t
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}
