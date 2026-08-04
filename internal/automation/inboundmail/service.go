// Package inboundmail 提供邮件入站能力: IMAP 轮询收件箱,
// 按主题/正文中的工单 ID 路由——命中工单则作为回复追加评论, 未命中则创建新工单。
//
// 设计: 不依赖 internal/itsm, 处理动作由 cmd/core 注入回调(避免 import 环);
// 未配置 IMAP(Enable=false)时服务不启动, 平台无邮件入站配置也能正常运行。
package inboundmail

import (
	"context"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// Config 邮件入站配置(由 cmd/core 从环境变量装配)。
type Config struct {
	Enable   bool   // 总开关, 未开启则不启动轮询
	Host     string // IMAP 服务器
	Port     string // 默认 993
	Username string
	Password string
	Mailbox  string // 收件箱名, 默认 INBOX
	TenantID string // 工单归属租户, 默认 default
	PollSecs int    // 轮询间隔秒, 默认 60
}

// Handlers 邮件处理回调(由 cmd/core 注入)。
type Handlers struct {
	// ReplyToTicket 邮件命中工单 ID → 追加评论(作者为发件人)。
	ReplyToTicket func(ctx context.Context, tenantID, ticketID, author, body string) error
	// CreateTicket 未命中 → 创建工单, 返回工单 ID。
	CreateTicket func(ctx context.Context, tenantID, from, subject, body string) (string, error)
}

// Service 邮件入站轮询服务。
type Service struct {
	cfg      Config
	handlers Handlers
}

func New(cfg Config) *Service {
	if cfg.Port == "" {
		cfg.Port = "993"
	}
	if cfg.Mailbox == "" {
		cfg.Mailbox = "INBOX"
	}
	if cfg.TenantID == "" {
		cfg.TenantID = "default"
	}
	if cfg.PollSecs <= 0 {
		cfg.PollSecs = 60
	}
	return &Service{cfg: cfg}
}

// SetHandlers 注入处理回调(建单/回复)。
func (s *Service) SetHandlers(h Handlers) { s.handlers = h }

// Start 启动轮询 goroutine。未启用或回调未注入时直接返回。
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enable {
		slog.Info("inbound mail: disabled, skip")
		return
	}
	if s.handlers.ReplyToTicket == nil && s.handlers.CreateTicket == nil {
		slog.Warn("inbound mail: handlers not injected, skip")
		return
	}
	go func() {
		ticker := time.NewTicker(time.Duration(s.cfg.PollSecs) * time.Second)
		defer ticker.Stop()
		slog.Info("inbound mail: polling started", "host", s.cfg.Host, "mailbox", s.cfg.Mailbox)
		// 启动即拉一次
		if err := s.pollOnce(ctx); err != nil {
			slog.Warn("inbound mail: first poll failed", "error", err)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.pollOnce(ctx); err != nil {
					slog.Warn("inbound mail: poll failed", "error", err)
				}
			}
		}
	}()
}

// ticketIDRe 匹配工单 UUID(ID 形态)。
var ticketIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// ExtractTicketID 从主题与正文中提取工单 ID(第一个匹配), 未命中返回空串。
func ExtractTicketID(subject, body string) string {
	if m := ticketIDRe.FindString(subject); m != "" {
		return m
	}
	if m := ticketIDRe.FindString(body); m != "" {
		return m
	}
	return ""
}

// pollOnce 连接 IMAP, 拉取未读邮件并处理, 处理后标记已读。
func (s *Service) pollOnce(ctx context.Context) error {
	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)
	c, err := client.DialTLS(addr, nil)
	if err != nil {
		// 非 TLS 兜底(内网邮箱)
		if c2, err2 := client.Dial(addr); err2 == nil {
			c = c2
			err = nil
		}
	}
	if err != nil {
		return err
	}
	defer c.Logout()
	if err := c.Login(s.cfg.Username, s.cfg.Password); err != nil {
		return err
	}
	if _, err := c.Select(s.cfg.Mailbox, true); err != nil {
		return err
	}

	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	ids, err := c.Search(criteria)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	seqset := new(imap.SeqSet)
	seqset.AddNum(ids...)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, section.FetchItem(), imap.FetchFlags}
	msgs := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(seqset, items, msgs) }()

	processed := 0
	for msg := range msgs {
		envelope := msg.Envelope
		subject := ""
		from := ""
		if envelope != nil {
			subject = envelope.Subject
			if len(envelope.From) > 0 {
				from = envelope.From[0].Address()
			}
		}
		body := readBody(msg, section)
		if err := s.processMessage(ctx, from, subject, body); err != nil {
			slog.Warn("inbound mail: process failed", "subject", subject, "error", err)
			continue
		}
		processed++
	}
	if err := <-done; err != nil {
		return err
	}
	// 标记已读(处理过的)
	if processed > 0 {
		if err := c.Store(seqset, imap.AddFlags, []interface{}{imap.SeenFlag}, nil); err != nil {
			slog.Warn("inbound mail: mark seen failed", "error", err)
		}
	}
	slog.Info("inbound mail: poll done", "processed", processed)
	return nil
}

// processMessage 单封邮件路由: 命中工单 ID → 回复评论; 否则 → 建单。
func (s *Service) processMessage(ctx context.Context, from, subject, body string) error {
	ticketID := ExtractTicketID(subject, body)
	if ticketID != "" && s.handlers.ReplyToTicket != nil {
		if err := s.handlers.ReplyToTicket(ctx, s.cfg.TenantID, ticketID, from, body); err != nil {
			return err
		}
		slog.Info("inbound mail: replied to ticket", "ticket_id", ticketID, "from", from)
		return nil
	}
	if s.handlers.CreateTicket != nil {
		id, err := s.handlers.CreateTicket(ctx, s.cfg.TenantID, from, subject, body)
		if err != nil {
			return err
		}
		slog.Info("inbound mail: created ticket", "ticket_id", id, "from", from, "subject", subject)
		return nil
	}
	return nil
}

// readBody 从邮件消息提取纯文本正文(优先 text/plain 部分, multipart 递归)。
func readBody(msg *imap.Message, section *imap.BodySectionName) string {
	r := msg.GetBody(section)
	if r == nil {
		return ""
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	ct := "text/plain"
	if msg.BodyStructure != nil && msg.BodyStructure.MIMEType != "" {
		ct = msg.BodyStructure.MIMEType + "/" + msg.BodyStructure.MIMESubType
	}
	mediaType, params, _ := mime.ParseMediaType(ct)
	if mediaType == "text/plain" {
		return decodeQuotedPrintable(string(raw))
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		if boundary := params["boundary"]; boundary != "" {
			mr := multipart.NewReader(strings.NewReader(string(raw)), boundary)
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				partCT := part.Header.Get("Content-Type")
				if strings.HasPrefix(partCT, "text/plain") {
					b, _ := io.ReadAll(part)
					return decodeQuotedPrintable(string(b))
				}
			}
		}
	}
	// 兜底: 尝试 mail 解析后取 body
	if m, err := mail.ReadMessage(strings.NewReader(string(raw))); err == nil {
		b, _ := io.ReadAll(m.Body)
		return decodeQuotedPrintable(string(b))
	}
	return string(raw)
}

// decodeQuotedPrintable 粗解 quoted-printable(逐行软换行与 =XX 转义)。
func decodeQuotedPrintable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '=' && i+2 < len(s) {
			if isHex(s[i+1]) && isHex(s[i+2]) {
				b.WriteByte(fromHex(s[i+1], s[i+2]))
				i += 2
				continue
			}
			if s[i+1] == '\n' {
				i++
				continue
			}
			if s[i+1] == '\r' && i+2 < len(s) && s[i+2] == '\n' {
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func fromHex(h, l byte) byte {
	hv := h - '0'
	if h >= 'a' {
		hv = h - 'a' + 10
	} else if h >= 'A' {
		hv = h - 'A' + 10
	}
	lv := l - '0'
	if l >= 'a' {
		lv = l - 'a' + 10
	} else if l >= 'A' {
		lv = l - 'A' + 10
	}
	return hv<<4 | lv
}

var _ = textproto.MIMEHeader{}
