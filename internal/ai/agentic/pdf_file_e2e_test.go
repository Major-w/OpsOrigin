package agentic

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/opsorigin/core/internal/objectstore"
)

// newTestObjStore 连接本地 MinIO(docker-compose 默认)。不可用时返回 nil, 用例跳过。
func newTestObjStore(t *testing.T) *objectstore.Client {
	c, err := objectstore.New("localhost:9000", "opsorigin", "opsorigin123", "opsorigin", "us-east-1", false)
	if err != nil {
		t.Skipf("跳过: MinIO 不可用 (%v)", err)
	}
	return c
}

// TestPDFGenerateEndToEnd 端到端: 调 pdfGenerate → 落 MinIO → 预签名 URL 可下载且是合法 PDF。
func TestPDFGenerateEndToEnd(t *testing.T) {
	os_ := newTestObjStore(t)
	ts := &MCPToolSet{}
	ts.SetObjectStore(os_)

	out, err := ts.pdfGenerate(context.Background(), map[string]any{
		"title": "运维助手端到端 PDF 校验",
		"blocks": []any{
			map[string]any{"type": "paragraph", "text": "中文正文：告警自动拉起 / 根因分析 / 复盘报告。"},
			map[string]any{"type": "bullet", "text": "中英混排 English 与数字 123。"},
		},
		"file_name": "e2e-校验.pdf",
	})
	if err != nil {
		t.Fatalf("pdfGenerate: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("返回类型不是 map: %T", out)
	}
	url, _ := m["url"].(string)
	if url == "" {
		t.Fatalf("未返回 url: %v", m)
	}
	t.Logf("pdf url=%s file=%v pages=%v", url, m["file_name"], m["page_count"])

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("下载状态码 %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}
	if len(body) < 5 || string(body[:5]) != "%PDF-" {
		t.Fatalf("下载内容不是 PDF: %q", string(body[:min(20, len(body))]))
	}
	t.Logf("下载成功, %d 字节, 头部=%q", len(body), string(body[:8]))
}

// TestFileGenerateEndToEnd 端到端: 调 fileGenerate(csv) → 落 MinIO → 预签名 URL 可下载且内容一致。
func TestFileGenerateEndToEnd(t *testing.T) {
	os_ := newTestObjStore(t)
	ts := &MCPToolSet{}
	ts.SetObjectStore(os_)

	content := "姓名,部门,工单\n张三,运维,12\n李四,研发,7\n"
	out, err := ts.fileGenerate(context.Background(), map[string]any{
		"file_name": "e2e-工单.csv",
		"content":   content,
	})
	if err != nil {
		t.Fatalf("fileGenerate: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("返回类型不是 map: %T", out)
	}
	url, _ := m["url"].(string)
	if url == "" {
		t.Fatalf("未返回 url: %v", m)
	}
	t.Logf("file url=%s ct=%v size=%v", url, m["content_type"], m["size_bytes"])

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != content {
		t.Fatalf("下载内容与原文不一致:\n got=%q\nwant=%q", string(body), content)
	}
	t.Logf("下载成功: %q", string(body))
}
