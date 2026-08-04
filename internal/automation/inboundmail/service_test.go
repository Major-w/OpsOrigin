package inboundmail

import (
	"context"
	"testing"
)

func TestExtractTicketID(t *testing.T) {
	const uuid = "3f2c1b4a-5d6e-4f7a-8b9c-0d1e2f3a4b5c"
	cases := []struct {
		subject, body string
		want          string
	}{
		{"[工单] " + uuid + " 服务器重启", "请处理", uuid},
		{"服务器故障", "ticket_id: " + uuid + "\n请重启", uuid},
		{"普通邮件", "无工单引用", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		got := ExtractTicketID(c.subject, c.body)
		if got != c.want {
			t.Fatalf("ExtractTicketID(%q,%q) = %q, want %q", c.subject, c.body, got, c.want)
		}
	}
}

func TestProcessMessageRoutesToReply(t *testing.T) {
	const uuid = "3f2c1b4a-5d6e-4f7a-8b9c-0d1e2f3a4b5c"
	var repliedTicket, repliedAuthor, repliedBody string
	s := New(Config{Enable: true, TenantID: "default"})
	s.SetHandlers(Handlers{
		ReplyToTicket: func(ctx context.Context, tenantID, ticketID, author, body string) error {
			repliedTicket, repliedAuthor, repliedBody = ticketID, author, body
			return nil
		},
		CreateTicket: func(ctx context.Context, tenantID, from, subject, body string) (string, error) {
			t.Fatal("should not create ticket when ticket id present")
			return "", nil
		},
	})
	if err := s.processMessage(context.Background(), "user@example.com", "RE: [工单] "+uuid, "已处理"); err != nil {
		t.Fatal(err)
	}
	if repliedTicket != uuid || repliedAuthor != "user@example.com" || repliedBody != "已处理" {
		t.Fatalf("reply got %q/%q/%q", repliedTicket, repliedAuthor, repliedBody)
	}
}

func TestProcessMessageCreatesTicket(t *testing.T) {
	var createdFrom, createdSubject, createdBody string
	s := New(Config{Enable: true, TenantID: "default"})
	s.SetHandlers(Handlers{
		ReplyToTicket: func(ctx context.Context, tenantID, ticketID, author, body string) error {
			t.Fatal("should not reply when no ticket id")
			return nil
		},
		CreateTicket: func(ctx context.Context, tenantID, from, subject, body string) (string, error) {
			createdFrom, createdSubject, createdBody = from, subject, body
			return "tk-1", nil
		},
	})
	if err := s.processMessage(context.Background(), "ops@example.com", "磁盘空间不足", "请扩容 /data"); err != nil {
		t.Fatal(err)
	}
	if createdFrom != "ops@example.com" || createdSubject != "磁盘空间不足" || createdBody != "请扩容 /data" {
		t.Fatalf("create got %q/%q/%q", createdFrom, createdSubject, createdBody)
	}
}

func TestDecodeQuotedPrintable(t *testing.T) {
	in := "=E6=9C=8D=E5=8A=A1=E5=99=A8=0A=E6=95=85=E9=9A=9C" // "服务器\n故障" (=0A 硬换行)
	got := decodeQuotedPrintable(in)
	if got != "服务器\n故障" {
		t.Fatalf("decode = %q", got)
	}
	// 软换行(=CRLF)应被剥掉
	if got := decodeQuotedPrintable("ab=\r\ncd"); got != "abcd" {
		t.Fatalf("soft line break decode = %q", got)
	}
}
