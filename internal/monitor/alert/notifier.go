package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

// WebhookNotifier 把新告警投递到外部 Webhook。
// 支持三种目标格式: 通用 JSON / 企业微信机器人 / 钉钉机器人(按 URL 域名自动识别)。
// URL 为空时不投递(安全退化)。地址支持热更新 SetURL。
type WebhookNotifier struct {
	mu  sync.RWMutex
	url string
}

func NewWebhookNotifier(u string) *WebhookNotifier {
	return &WebhookNotifier{url: strings.TrimSpace(u)}
}

// SetURL 热更新投递地址(空串表示停用)。
func (n *WebhookNotifier) SetURL(u string) {
	n.mu.Lock()
	n.url = strings.TrimSpace(u)
	n.mu.Unlock()
}

// URL 返回当前投递地址(原始, 含密钥; 展示请走 MaskWebhookURL)。
func (n *WebhookNotifier) URL() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.url
}

// Notify 符合 Aggregator OnNotify / Service.AddAlertNotifier 签名。
// 异步投递避免阻塞告警主链路; 触发瞬间拷贝所需字段防竞态。
func (n *WebhookNotifier) Notify(alert *model.AlertEvent) {
	n.mu.RLock()
	u := n.url
	n.mu.RUnlock()
	if u == "" {
		return
	}
	payload := buildWebhookPayload(alert, DetectWebhookFormat(u))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		body, err := json.Marshal(payload)
		if err != nil {
			slog.Warn("webhook marshal failed", "err", err)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			slog.Warn("webhook build request failed", "err", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Warn("webhook deliver failed", "err", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			slog.Warn("webhook deliver unexpected status", "status", resp.StatusCode)
		}
	}()
}

// DetectWebhookFormat 按 URL 域名识别机器人格式, 兼容企业微信/钉钉群机器人。
func DetectWebhookFormat(u string) string {
	if strings.Contains(u, "qyapi.weixin.qq.com") {
		return "wecom"
	}
	if strings.Contains(u, "oapi.dingtalk.com") {
		return "dingtalk"
	}
	return "json"
}

// MaskWebhookURL 脱敏展示: 隐藏 query 中的密钥(企微/钉钉 key)。
func MaskWebhookURL(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	if parsed.RawQuery != "" {
		return fmt.Sprintf("%s://%s%s?key=***", parsed.Scheme, parsed.Host, parsed.Path)
	}
	return u
}

// buildWebhookPayload 构造投递体。企微/钉钉走其文本消息格式; 通用 JSON 保留完整结构。
func buildWebhookPayload(a *model.AlertEvent, format string) any {
	title := a.Title
	if title == "" {
		title = a.RuleName
	}
	text := fmt.Sprintf("[%s] %s\n源CI: %s\n规则: %s\n时间: %s",
		severityCn(a.Severity), title, a.SourceCIID, a.RuleName, a.TriggeredAt.Format(time.RFC3339))
	if a.Description != "" {
		text += "\n" + a.Description
	}
	switch format {
	case "wecom", "dingtalk":
		return map[string]any{
			"msgtype": "text",
			"text":    map[string]any{"content": text},
		}
	default:
		return map[string]any{
			"title":        title,
			"severity":     a.Severity,
			"source_ci_id": a.SourceCIID,
			"rule_name":    a.RuleName,
			"description":  a.Description,
			"labels":       a.Labels,
			"triggered_at": a.TriggeredAt,
			"fingerprint":  a.Fingerprint,
		}
	}
}

func severityCn(s model.AlertSeverity) string {
	switch s {
	case model.AlertCritical:
		return "严重"
	case model.AlertWarning:
		return "警告"
	default:
		return "提示"
	}
}
