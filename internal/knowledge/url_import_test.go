package knowledge

import (
	"net"
	"testing"
)

// TestIsBlockedIP 验证 SSRF 地址黑名单覆盖各类内网/本机/云元数据地址。
func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3",
		"10.0.0.1", "10.255.255.254",
		"172.16.0.1", "172.31.255.255",
		"192.168.1.1",
		"169.254.169.254", // 云厂商元数据服务
		"0.0.0.0",
		"100.64.0.1", // CGNAT
		"224.0.0.1",  // 组播
		"::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1",
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("应被拦截: %s", s)
		}
	}
	allowed := []string{"8.8.8.8", "114.114.114.114", "93.184.216.34", "2400:3200::1"}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("不应被拦截: %s", s)
		}
	}
}

// TestValidatePublicURLRejectsInternal 验证 URL 入口拦截: 非 http(s)、本机名、IP 字面量内网。
func TestValidatePublicURLRejectsInternal(t *testing.T) {
	cases := []string{
		"http://localhost/admin",
		"http://127.0.0.1:8080/healthz",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/internal",
		"http://192.168.0.10/x",
		"http://metadata.google.internal/",
		"http://foo.internal/",
		"file:///etc/passwd",
		"ftp://example.com/a",
		"http://",
		"not-a-url",
	}
	for _, u := range cases {
		if _, err := validatePublicURL(u); err == nil {
			t.Errorf("应被拒绝: %s", u)
		}
	}
}

// TestValidatePublicURLAcceptsPublicIP 公网 IP 字面量应放行(域名型需联网解析, 不在单测覆盖)。
func TestValidatePublicURLAcceptsPublicIP(t *testing.T) {
	if _, err := validatePublicURL("https://93.184.216.34/page"); err != nil {
		t.Errorf("公网 IP 不应被拒绝: %v", err)
	}
}

// TestContentDispositionFileName 验证下载头中文文件名按 RFC 5987 编码且带 ASCII 兜底。
func TestContentDispositionFileName(t *testing.T) {
	got := contentDispositionFileName(`运维 报告"v2.pdf`)
	if !contains(got, `filename="`) || !contains(got, `filename*=UTF-8''`) {
		t.Fatalf("缺少 RFC 5987 双写: %s", got)
	}
	if contains(got, "\n") || contains(got, "\r") {
		t.Errorf("文件名注入换行(响应头走私风险): %q", got)
	}
	// 取出 ASCII 兜底(filename="..." 段): 引号/反斜杠必须已被替换, 否则会截断 header。
	start := len(`filename="`)
	end := -1
	for i := start; i < len(got); i++ {
		if got[i] == '"' {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatalf("ASCII 兜底段未闭合: %s", got)
	}
	fallback := got[start:end]
	if contains(fallback, `"`) || contains(fallback, `\`) {
		t.Errorf("ASCII 兜底含引号/反斜杠: %q", fallback)
	}
	if !contains(got, "%E8%BF%90%E7%BB%B4") { // "运维" 的百分号编码
		t.Errorf("filename* 未按 RFC 5987 编码中文: %s", got)
	}
}

// TestPreviewHeadersDowngradesHTML 验证 html/svg 内联预览被降级为纯文本(防存储型 XSS)。
func TestPreviewHeadersDowngradesHTML(t *testing.T) {
	for _, tc := range []struct{ ext, ct string }{
		{".html", "text/html; charset=utf-8"},
		{".htm", "text/html"},
		{".svg", "image/svg+xml"},
	} {
		ct, disp := previewHeaders(tc.ct, tc.ext, "a"+tc.ext, false)
		if ct != "text/plain; charset=utf-8" {
			t.Errorf("%s 未降级: %s", tc.ext, ct)
		}
		if !contains(disp, "inline") {
			t.Errorf("%s 应为 inline: %s", tc.ext, disp)
		}
	}
	// PDF 保持原类型 + inline
	ct, disp := previewHeaders("application/pdf", ".pdf", "报告.pdf", false)
	if ct != "application/pdf" || !contains(disp, "inline") {
		t.Errorf("pdf 预览头错误: %s / %s", ct, disp)
	}
	// ?download=1 强制附件
	if _, disp := previewHeaders("application/pdf", ".pdf", "报告.pdf", true); !contains(disp, "attachment") {
		t.Errorf("强制下载未生效: %s", disp)
	}
}
