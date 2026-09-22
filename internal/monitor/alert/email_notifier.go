package alert

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

// EmailConfig 邮件告警投递配置。密码等敏感字段不落库, 仅 env 或内存热更新。
type EmailConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Pass     string `json:"pass"`
	From     string `json:"from"`
	To       string `json:"to"` // 逗号分隔的多个收件人
	TLS      bool   `json:"tls"`
	Insecure bool   `json:"insecure_skip_verify"`
}

// EmailConfigFromEnv 从环境变量读取 SMTP 配置(国内客户常用企业邮箱, 字段独立可配)。
func EmailConfigFromEnv() EmailConfig {
	port, _ := strconv.Atoi(os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_PORT"))
	if port == 0 {
		port = 25
	}
	tlsOn := strings.EqualFold(os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_TLS"), "true") ||
		strings.EqualFold(os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_TLS"), "1")
	return EmailConfig{
		Host:     os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_HOST"),
		Port:     port,
		User:     os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_USER"),
		Pass:     os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_PASS"),
		From:     os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_FROM"),
		To:       os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_TO"),
		TLS:      tlsOn,
		Insecure: strings.EqualFold(os.Getenv("OPSORIGIN_CORE_ALERT_SMTP_INSECURE"), "true"),
	}
}

// EmailNotifier 把新告警投递到 SMTP 邮件。地址/收件人支持热更新 SetConfig; 配置不全时不投递(安全退化)。
type EmailNotifier struct {
	mu     sync.RWMutex
	cfg    EmailConfig
	usable bool
}

func NewEmailNotifier(cfg EmailConfig) *EmailNotifier {
	n := &EmailNotifier{cfg: cfg}
	n.refreshUsable()
	return n
}

func (n *EmailNotifier) refreshUsable() {
	n.usable = n.cfg.Host != "" && n.cfg.From != "" && strings.TrimSpace(n.cfg.To) != ""
}

// Enabled 配置是否可用(可用才注入告警链)。
func (n *EmailNotifier) Enabled() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.usable
}

// Config 返回当前配置(密码脱敏由调用方处理)。
func (n *EmailNotifier) Config() EmailConfig {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.cfg
}

// SetConfig 热更新配置(空 to/from 即停用)。密码空表示沿用已配置值(避免保存时误清空)。
func (n *EmailNotifier) SetConfig(cfg EmailConfig) {
	n.mu.Lock()
	if cfg.Pass == "" && n.cfg.Pass != "" {
		cfg.Pass = n.cfg.Pass
	}
	n.cfg = cfg
	n.refreshUsable()
	n.mu.Unlock()
}

// Notify 符合 Aggregator OnNotify / Service.AddAlertNotifier 签名, 异步投递不阻塞主链路。
func (n *EmailNotifier) Notify(a *model.AlertEvent) {
	n.mu.RLock()
	usable := n.usable
	n.mu.RUnlock()
	if !usable {
		return
	}
	alert := *a // 拷贝, 避免异步读取竞态
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := n.send(ctx, &alert); err != nil {
			slog.Warn("email alert deliver failed", "err", err, "ci", alert.SourceCIID)
		}
	}()
}

func (n *EmailNotifier) send(ctx context.Context, a *model.AlertEvent) error {
	cfg := n.Config()
	to := splitRecipients(cfg.To)
	subject := "[" + severityCn(a.Severity) + "] " + titleOf(a)
	html := buildEmailHTML(a)
	msg := buildMIMEMessage(cfg.From, to, subject, html)

	client, err := dialSMTP(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Quit() }()

	if cfg.User != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// dialSMTP 按端口/TLS 开关选择直连 TLS 或明文+STARTTLS。
func dialSMTP(cfg EmailConfig) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.Insecure}
	if cfg.Port == 465 || cfg.TLS {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 8 * time.Second}, "tcp", addr, tlsCfg)
		if err != nil {
			return nil, err
		}
		return smtp.NewClient(conn, cfg.Host)
	}
	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(tlsCfg); err != nil {
			return nil, err
		}
	}
	return client, nil
}

func splitRecipients(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func titleOf(a *model.AlertEvent) string {
	if a.Title != "" {
		return a.Title
	}
	return a.RuleName
}

func buildMIMEMessage(from string, to []string, subject, htmlBody string) []byte {
	var buf bytes.Buffer
	buf.WriteString("From: " + from + "\r\n")
	buf.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	buf.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: base64\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(base64.StdEncoding.EncodeToString([]byte(htmlBody)))
	return buf.Bytes()
}

func buildEmailHTML(a *model.AlertEvent) string {
	title := titleOf(a)
	sev := severityCn(a.Severity)
	color := "#d4380d"
	if a.Severity == model.AlertWarning {
		color = "#d46b08"
	} else if a.Severity == model.AlertInfo {
		color = "#1677ff"
	}
	rows := [][2]string{
		{"严重级别", sev},
		{"规则", a.RuleName},
		{"来源 CI", a.SourceCIID},
		{"触发时间", a.TriggeredAt.Format("2006-01-02 15:04:05")},
	}
	var sb strings.Builder
	sb.WriteString("<table cellpadding='6' cellspacing='0' style='font-family:-apple-system,Segoe UI,Roboto,sans-serif;font-size:13px;color:#1f2329'>")
	for _, r := range rows {
		sb.WriteString("<tr><td style='color:#8a8f99;width:90px'>" + r[0] + "</td><td>" + escapeHTML(r[1]) + "</td></tr>")
	}
	if a.Description != "" {
		sb.WriteString("<tr><td style='color:#8a8f99;vertical-align:top'>详情</td><td>" + escapeHTML(a.Description) + "</td></tr>")
	}
	sb.WriteString("</table>")
	return "<div style='border-left:4px solid " + color + ";padding:4px 0 4px 12px'>" +
		"<div style='font-size:15px;font-weight:600;color:" + color + ";margin-bottom:8px'>" + escapeHTML(title) + "</div>" +
		sb.String() + "</div>"
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
