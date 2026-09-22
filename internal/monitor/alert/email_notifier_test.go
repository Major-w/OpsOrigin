package alert

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/monitor/model"
)

func TestEmailConfigFromEnv(t *testing.T) {
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_HOST", "smtp.example.com")
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_PORT", "465")
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_USER", "ops@ex.com")
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_PASS", "secret")
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_FROM", "ops@ex.com")
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_TO", "a@ex.com, b@ex.com ")
	os.Setenv("OPSORIGIN_CORE_ALERT_SMTP_TLS", "true")
	defer func() {
		for _, k := range []string{"OPSORIGIN_CORE_ALERT_SMTP_HOST", "OPSORIGIN_CORE_ALERT_SMTP_PORT", "OPSORIGIN_CORE_ALERT_SMTP_USER", "OPSORIGIN_CORE_ALERT_SMTP_PASS", "OPSORIGIN_CORE_ALERT_SMTP_FROM", "OPSORIGIN_CORE_ALERT_SMTP_TO", "OPSORIGIN_CORE_ALERT_SMTP_TLS"} {
			os.Unsetenv(k)
		}
	}()
	cfg := EmailConfigFromEnv()
	if cfg.Host != "smtp.example.com" || cfg.Port != 465 {
		t.Fatalf("host/port parse wrong: %+v", cfg)
	}
	if !cfg.TLS {
		t.Fatal("tls should be true")
	}
	if len(splitRecipients(cfg.To)) != 2 {
		t.Fatalf("expected 2 recipients, got %v", splitRecipients(cfg.To))
	}
}

func TestEmailConfigFromEnv_DefaultPort(t *testing.T) {
	os.Unsetenv("OPSORIGIN_CORE_ALERT_SMTP_PORT")
	if cfg := EmailConfigFromEnv(); cfg.Port != 25 {
		t.Fatalf("default port should be 25, got %d", cfg.Port)
	}
}

func TestEmailNotifierEnabled(t *testing.T) {
	// 缺 to/from -> 不可用
	n := NewEmailNotifier(EmailConfig{Host: "h", Port: 25})
	if n.Enabled() {
		t.Fatal("should be disabled without from/to")
	}
	// 齐全 -> 可用
	n2 := NewEmailNotifier(EmailConfig{Host: "h", Port: 25, From: "a@b.com", To: "c@d.com"})
	if !n2.Enabled() {
		t.Fatal("should be enabled with from/to")
	}
}

func TestSplitRecipients(t *testing.T) {
	got := splitRecipients(" a@x.com , b@x.com,, c@x.com ")
	if len(got) != 3 || got[0] != "a@x.com" || got[2] != "c@x.com" {
		t.Fatalf("split wrong: %v", got)
	}
	if len(splitRecipients("")) != 0 {
		t.Fatal("empty should yield 0")
	}
}

func TestBuildMIME(t *testing.T) {
	msg := buildMIMEMessage("ops@ex.com", []string{"a@ex.com", "b@ex.com"}, "测试主题 [严重]", "<p>hi</p>")
	s := string(msg)
	if !strings.Contains(s, "From: ops@ex.com") {
		t.Fatal("missing From")
	}
	if !strings.Contains(s, "To: a@ex.com, b@ex.com") {
		t.Fatal("missing To list")
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Fatal("missing base64 encoding header")
	}
	// base64 段应可还原为 HTML
	idx := strings.Index(s, "\r\n\r\n")
	b64 := strings.TrimSpace(s[idx+4:])
	dec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if !strings.Contains(string(dec), "<p>hi</p>") {
		t.Fatalf("decoded html wrong: %s", dec)
	}
}

func TestBuildEmailHTML_Escapes(t *testing.T) {
	a := &model.AlertEvent{
		Title:       "CPU 高",
		RuleName:    "cpu_high",
		SourceCIID:  "ci-1",
		Severity:    model.AlertCritical,
		Description: "<script>alert(1)</script> & \"x\"",
	}
	html := buildEmailHTML(a)
	if strings.Contains(html, "<script>") {
		t.Fatal("description not escaped, XSS risk")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag, got: %s", html)
	}
	if !strings.Contains(html, "ci-1") {
		t.Fatal("missing source ci")
	}
}
