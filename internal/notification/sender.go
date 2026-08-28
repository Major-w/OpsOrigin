package notification

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Sender 定义通知发送器的统一接口。每种 ChannelType 对应一个 Sender 实现。
type Sender interface {
	// Send 通过渠道发送一条通知消息。
	Send(ch *Channel, title, content string) error
	// Validate 校验渠道配置是否完整可用。
	Validate(config map[string]string) error
	// RequiredFields 返回该渠道必需的配置字段名列表,供前端表单生成。
	RequiredFields() []FieldSpec
}

// FieldSpec 描述一个渠道配置字段的元信息,用于前端动态渲染表单。
type FieldSpec struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"` // 是否为敏感字段(密码/token)
}

// Senders 返回类型到 Sender 的映射,供 Service 注入使用。
func Senders() map[ChannelType]Sender {
	return map[ChannelType]Sender{
		ChannelEmail:    &EmailSender{},
		ChannelDingTalk: &DingTalkSender{},
		ChannelWeCom:    &WeComSender{},
		ChannelFeishu:   &FeishuSender{},
		ChannelWebhook:  &WebhookSender{},
		ChannelScript:   &ScriptSender{},
	}
}

// ---------- EmailSender (SMTP) ----------

type EmailSender struct{}

func (s *EmailSender) RequiredFields() []FieldSpec {
	return []FieldSpec{
		{Name: "smtp_host", Label: "SMTP 主机", Placeholder: "smtp.example.com", Required: true},
		{Name: "smtp_port", Label: "SMTP 端口", Placeholder: "465 (SSL) 或 25", Required: true},
		{Name: "username", Label: "用户名", Placeholder: "user@example.com", Required: true},
		{Name: "password", Label: "密码", Placeholder: "邮箱密码或授权码", Required: true, Secret: true},
		{Name: "from", Label: "发件人", Placeholder: "noreply@example.com", Required: true},
		{Name: "to", Label: "收件人(逗号分隔)", Placeholder: "ops@example.com,oncall@example.com", Required: true},
	}
}

func (s *EmailSender) Validate(cfg map[string]string) error {
	for _, f := range s.RequiredFields() {
		if f.Required && cfg[f.Name] == "" {
			return fmt.Errorf("missing required field: %s", f.Name)
		}
	}
	return nil
}

func (s *EmailSender) Send(ch *Channel, title, content string) error {
	cfg := ch.Config
	host := cfg["smtp_host"]
	port := cfg["smtp_port"]
	if port == "" {
		port = "25"
	}
	addr := host + ":" + port
	auth := smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	to := strings.Split(cfg["to"], ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	// 过滤空收件人
	filtered := make([]string, 0, len(to))
	for _, t := range to {
		if t != "" {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) == 0 {
		return errors.New("no valid recipients")
	}
	msg := []byte(buildEmailMessage(cfg["from"], filtered, title, content))
	return smtp.SendMail(addr, auth, cfg["from"], filtered, msg)
}

func buildEmailMessage(from string, to []string, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(body)
	return b.String()
}

// ---------- DingTalkSender (钉钉机器人) ----------

type DingTalkSender struct{}

func (s *DingTalkSender) RequiredFields() []FieldSpec {
	return []FieldSpec{
		{Name: "webhook_url", Label: "Webhook URL", Placeholder: "https://oapi.dingtalk.com/robot/send?access_token=xxx", Required: true},
		{Name: "secret", Label: "加签密钥(可选)", Placeholder: "SEC... 开头的加签密钥"},
	}
}

func (s *DingTalkSender) Validate(cfg map[string]string) error {
	if cfg["webhook_url"] == "" {
		return errors.New("missing required field: webhook_url")
	}
	return nil
}

func (s *DingTalkSender) Send(ch *Channel, title, content string) error {
	url := ch.Config["webhook_url"]
	secret := ch.Config["secret"]
	if secret != "" {
		// 钉钉加签:timestamp + "\n" + secret 的 HMAC-SHA256,base64 编码后作为 sign 参数
		ts := time.Now().UnixMilli()
		stringToSign := fmt.Sprintf("%d\n%s", ts, secret)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stringToSign))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		// URL 中需对 sign 进行转义
		url = fmt.Sprintf("%s&timestamp=%d&sign=%s", url, ts, urlEncode(sign))
	}
	text := fmt.Sprintf("【%s】\n%s", title, content)
	body, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
	return postJSON(url, body)
}

// ---------- WeComSender (企业微信群机器人) ----------

type WeComSender struct{}

func (s *WeComSender) RequiredFields() []FieldSpec {
	return []FieldSpec{
		{Name: "webhook_url", Label: "Webhook URL", Placeholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx", Required: true},
	}
}

func (s *WeComSender) Validate(cfg map[string]string) error {
	if cfg["webhook_url"] == "" {
		return errors.New("missing required field: webhook_url")
	}
	return nil
}

func (s *WeComSender) Send(ch *Channel, title, content string) error {
	url := ch.Config["webhook_url"]
	// 企业微信 markdown 格式,标题加粗
	text := fmt.Sprintf("**%s**\n%s", title, content)
	body, _ := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": text},
	})
	return postJSON(url, body)
}

// ---------- FeishuSender (飞书自定义机器人) ----------

type FeishuSender struct{}

func (s *FeishuSender) RequiredFields() []FieldSpec {
	return []FieldSpec{
		{Name: "webhook_url", Label: "Webhook URL", Placeholder: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx", Required: true},
		{Name: "secret", Label: "签名校验密钥(可选)", Placeholder: "启用签名校验时填入"},
	}
}

func (s *FeishuSender) Validate(cfg map[string]string) error {
	if cfg["webhook_url"] == "" {
		return errors.New("missing required field: webhook_url")
	}
	return nil
}

func (s *FeishuSender) Send(ch *Channel, title, content string) error {
	url := ch.Config["webhook_url"]
	secret := ch.Config["secret"]
	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": fmt.Sprintf("【%s】\n%s", title, content),
		},
	}
	if secret != "" {
		// 飞书签名:timestamp + "\n" + secret 的 HMAC-SHA256,再 HMAC-SHA256 一次
		ts := time.Now().Unix()
		stringToSign := fmt.Sprintf("%d\n%d", ts, ts)
		// 第一轮:用 timestamp 作 key 对 stringToSign 做 HMAC-SHA256
		mac1 := hmac.New(sha256.New, []byte(fmt.Sprintf("%d", ts)))
		mac1.Write([]byte(stringToSign))
		// 第二轮:用 secret 作 key 对第一轮结果做 HMAC-SHA256
		mac2 := hmac.New(sha256.New, []byte(secret))
		mac2.Write(mac1.Sum(nil))
		sign := hex.EncodeToString(mac2.Sum(nil))
		payload["timestamp"] = fmt.Sprintf("%d", ts)
		payload["sign"] = sign
	}
	body, _ := json.Marshal(payload)
	return postJSON(url, body)
}

// ---------- WebhookSender (通用 HTTP Webhook) ----------

type WebhookSender struct{}

func (s *WebhookSender) RequiredFields() []FieldSpec {
	return []FieldSpec{
		{Name: "url", Label: "Webhook URL", Placeholder: "https://example.com/hook", Required: true},
		{Name: "auth_header", Label: "Authorization 头(可选)", Placeholder: "Bearer xxx"},
	}
}

func (s *WebhookSender) Validate(cfg map[string]string) error {
	if cfg["url"] == "" {
		return errors.New("missing required field: url")
	}
	return nil
}

func (s *WebhookSender) Send(ch *Channel, title, content string) error {
	url := ch.Config["url"]
	payload := map[string]any{
		"title":   title,
		"content": content,
		"source":  "OpsOrigin",
		"time":    time.Now().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if ah := ch.Config["auth_header"]; ah != "" {
		req.Header.Set("Authorization", ah)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// ---------- ScriptSender (脚本执行) ----------

// ScriptExecutor 是脚本执行器的接口,由外部注入（复用 automation/script.Service 的 Execute 方法）。
type ScriptExecutor interface {
	Execute(scriptID string, params map[string]string, timeout int, runAs, agentID, tenantID string) (output string, exitCode int, err error)
}

// ScriptSender 通过执行自动化脚本来投递通知。
// 配置字段: script_id(必填), params(JSON 可选), timeout(秒,可选), agent_id(可选)。
type ScriptSender struct {
	executor ScriptExecutor
}

// SetExecutor 注入脚本执行器。
func (s *ScriptSender) SetExecutor(exec ScriptExecutor) {
	s.executor = exec
}

func (s *ScriptSender) RequiredFields() []FieldSpec {
	return []FieldSpec{
		{Name: "script_id", Label: "脚本", Placeholder: "从脚本库选择", Required: true},
		{Name: "params", Label: "执行参数 (JSON)", Placeholder: `{"key":"value"}`, Required: false},
		{Name: "timeout", Label: "超时 (秒)", Placeholder: "3600", Required: false},
		{Name: "agent_id", Label: "目标 Agent ID (留空=本机)", Placeholder: "可选", Required: false},
	}
}

func (s *ScriptSender) Validate(config map[string]string) error {
	if config["script_id"] == "" {
		return errors.New("script_id is required")
	}
	return nil
}

func (s *ScriptSender) Send(ch *Channel, title, content string) error {
	if s.executor == nil {
		return errors.New("script executor not configured")
	}
	scriptID := ch.Config["script_id"]
	if scriptID == "" {
		return errors.New("script_id not set in channel config")
	}
	timeout := 3600
	if t := ch.Config["timeout"]; t != "" {
		fmt.Sscanf(t, "%d", &timeout)
	}
	params := map[string]string{"title": title, "content": content}
	if p := ch.Config["params"]; p != "" {
		var extra map[string]string
		if err := json.Unmarshal([]byte(p), &extra); err == nil {
			for k, v := range extra {
				params[k] = v
			}
		}
	}
	agentID := ch.Config["agent_id"]
	_, _, err := s.executor.Execute(scriptID, params, timeout, "", agentID, "")
	return err
}

// ---------- 辅助函数 ----------

func postJSON(url string, body []byte) error {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("channel returned %d", resp.StatusCode)
	}
	return nil
}

// urlEncode 对字符串进行 URL 编码,用于钉钉 sign 参数。
func urlEncode(s string) string {
	// 替换 + 为 %20,与钉钉要求一致
	return strings.ReplaceAll(strings.ReplaceAll(s, "+", "%2B"), "/", "%2F")
}
