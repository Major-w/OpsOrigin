package notification

import (
	"strings"
	"testing"
)

func TestSenders_AllChannelsRegistered(t *testing.T) {
	senders := Senders()
	for _, ct := range []ChannelType{ChannelEmail, ChannelDingTalk, ChannelWeCom, ChannelFeishu, ChannelWebhook} {
		if _, ok := senders[ct]; !ok {
			t.Errorf("渠道 %s 未注册 sender", ct)
		}
	}
}

func TestEmailSender_Validate(t *testing.T) {
	s := &EmailSender{}
	if err := s.Validate(nil); err == nil {
		t.Fatal("空配置应校验失败")
	}
	cfg := map[string]string{
		"smtp_host": "smtp.example.com", "smtp_port": "465",
		"username": "u", "password": "p", "from": "noreply@x.com", "to": "ops@x.com",
	}
	if err := s.Validate(cfg); err != nil {
		t.Fatalf("完整配置应通过: %v", err)
	}
	// 缺失必填字段
	delete(cfg, "smtp_host")
	err := s.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "smtp_host") {
		t.Fatalf("缺 smtp_host 应报错: %v", err)
	}
}

func TestSender_RequiredFieldsMarked(t *testing.T) {
	for ct, s := range Senders() {
		for _, f := range s.RequiredFields() {
			if f.Name == "" || f.Label == "" {
				t.Errorf("渠道 %s 字段定义不完整: %+v", ct, f)
			}
		}
	}
}

func TestBuildEmailMessage(t *testing.T) {
	msg := buildEmailMessage("noreply@x.com", []string{"a@x.com", "b@x.com"}, "告警", "磁盘使用率 95%")
	for _, want := range []string{
		"From: noreply@x.com",
		"To: a@x.com, b@x.com",
		"Subject: 告警",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"磁盘使用率 95%",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("邮件消息应包含 %q, got: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "\r\n\r\n") {
		t.Error("header 与 body 之间应有空行分隔")
	}
}

func TestChannelTypesAndEvents(t *testing.T) {
	// 全部渠道类型均已注册 sender, 保证前端可配
	senders := Senders()
	for _, ct := range []ChannelType{ChannelEmail, ChannelDingTalk, ChannelWeCom, ChannelFeishu, ChannelWebhook} {
		if _, ok := senders[ct]; !ok {
			t.Errorf("渠道 %s 无 sender", ct)
		}
	}
}
