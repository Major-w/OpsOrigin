package chatops

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/pkg/httputil"
)

// feishuSettings 飞书渠道凭据(存 chatops_channels.settings)。
type feishuSettings struct {
	AppID             string `json:"app_id"`
	AppSecret         string `json:"app_secret"`
	VerificationToken string `json:"verification_token"`
	EncryptKey        string `json:"encrypt_key"` // 空=明文模式
	// DefaultAgentID 该渠道 IM 问答绑定的 AI Agent(空=引擎默认)。
	DefaultAgentID string `json:"default_agent_id,omitempty"`
}

// feishuPlatform 飞书自建应用适配器(事件订阅 v2 + im API)。
type feishuPlatform struct {
	http *http.Client

	mu     sync.Mutex
	tokens map[string]feishuToken // app_id → tenant_access_token
	seenMu sync.Mutex
	seen   map[string]time.Time // event_id 幂等去重
}

type feishuToken struct {
	token  string
	expire time.Time
}

const feishuBase = "https://open.feishu.cn"

func newFeishuPlatform() *feishuPlatform {
	return &feishuPlatform{
		http:   &http.Client{Timeout: 15 * time.Second},
		tokens: map[string]feishuToken{},
		seen:   map[string]time.Time{},
	}
}

func (f *feishuPlatform) Name() string { return PlatformFeishu }

// ---- 飞书事件结构与响应 ----

type feishuEventBody struct {
	// v2.0 事件订阅
	Schema string          `json:"schema"`
	Header feishuEvtHeader `json:"header"`
	Event  json.RawMessage `json:"event"`
	// url_verification
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Type      string `json:"type"` // url_verification
	// v1.0 旧格式(兼容判定, 直接拒绝并提示升级)
	Encrypt string `json:"encrypt"`
}

type feishuEvtHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	TenantKey  string `json:"tenant_key"`
}

type feishuMsgEvent struct {
	Sender struct {
		SenderID struct {
			OpenID string `json:"open_id"`
		} `json:"sender_id"`
	} `json:"sender"`
	Message struct {
		ChatID      string `json:"chat_id"`
		MessageType string `json:"message_type"`
		MessageID   string `json:"message_id"`
		Content     string `json:"content"` // JSON 字符串: {"text":"..."}
		ChatType    string `json:"chat_type"`
	} `json:"message"`
}

// ---- 验签与解密 ----

// decryptEvent AES-256-CBC 解密 encrypt 字段(Encrypt Key 模式), 返回解密后的 JSON。
func (f *feishuPlatform) decryptEvent(encryptB64, key string) ([]byte, error) {
	cipherText, err := base64.StdEncoding.DecodeString(encryptB64)
	if err != nil {
		return nil, fmt.Errorf("encrypt 字段 base64 解码失败: %w", err)
	}
	if len(cipherText) < aes.BlockSize || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypt 字段长度非法")
	}
	h := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(h[:])
	if err != nil {
		return nil, err
	}
	iv, body := cipherText[:aes.BlockSize], cipherText[aes.BlockSize:]
	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, body)
	// PKCS7 去填充
	pad := int(plain[len(plain)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, fmt.Errorf("encrypt 字段填充非法")
	}
	return plain[:len(plain)-pad], nil
}

// verifyToken 校验事件 token(明文模式 header.token / 旧格式 token; 加密模式解密后仍带 token)。
func (f *feishuPlatform) verifyToken(header, cfgToken string) bool {
	if cfgToken == "" {
		return false // 未配置 verification_token 一律拒绝(防伪造)
	}
	return header == cfgToken
}

// dedupeCheck event_id 幂等: 首次见返回 true; 重复/过期清理后返回 false。
func (f *feishuPlatform) dedupeCheck(eventID string) bool {
	if eventID == "" {
		return true
	}
	f.seenMu.Lock()
	defer f.seenMu.Unlock()
	now := time.Now()
	if t, ok := f.seen[eventID]; ok && now.Sub(t) < 30*time.Minute {
		return false
	}
	// 顺带清理过期键, 防长期膨胀
	if len(f.seen) > 4096 {
		for k, v := range f.seen {
			if now.Sub(v) >= 30*time.Minute {
				delete(f.seen, k)
			}
		}
	}
	f.seen[eventID] = now
	return true
}

// ---- webhook 主入口 ----

func (f *feishuPlatform) HandleWebhook(w http.ResponseWriter, r *http.Request, ch *Channel, d Dispatcher) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "read body failed")
		return nil
	}
	var body feishuEventBody
	if err := json.Unmarshal(raw, &body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid json")
		return nil
	}

	var cfg feishuSettings
	if err := json.Unmarshal(ch.Settings, &cfg); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "channel settings invalid")
		return nil
	}

	// 加密模式: 解密后覆盖 body 重新解析
	if body.Encrypt != "" {
		if cfg.EncryptKey == "" {
			httputil.WriteError(w, http.StatusBadRequest, "channel missing encrypt_key")
			return nil
		}
		plain, err := f.decryptEvent(body.Encrypt, cfg.EncryptKey)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "decrypt failed")
			return nil
		}
		body = feishuEventBody{}
		if err := json.Unmarshal(plain, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid decrypted json")
			return nil
		}
	}

	// url_verification 握手: 校验 token 后回显 challenge
	if body.Type == "url_verification" {
		if !f.verifyToken(body.Token, cfg.VerificationToken) {
			httputil.WriteError(w, http.StatusUnauthorized, "token mismatch")
			return nil
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"challenge": body.Challenge})
		return nil
	}

	// v2.0 事件: header.token 校验 + event_id 幂等
	if body.Header.EventID == "" {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"}) // 非目标事件, 200 吞掉防重试风暴
		return nil
	}
	if !f.verifyToken(body.Header.Token, cfg.VerificationToken) {
		httputil.WriteError(w, http.StatusUnauthorized, "token mismatch")
		return nil
	}
	if !f.dedupeCheck(body.Header.EventID) {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
		return nil
	}

	switch body.Header.EventType {
	case "im.message.receive_v1":
		var ev feishuMsgEvent
		if err := json.Unmarshal(body.Event, &ev); err != nil {
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
			return nil
		}
		// 仅处理文本消息(图片/文件类 MVP 不支持, 显式提示)
		if ev.Message.MessageType != "text" {
			_ = f.SendText(r.Context(), ch, ev.Message.ChatID, "暂仅支持文本消息, 请直接输入文字提问。")
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
			return nil
		}
		text, err := f.parseTextContent(ev.Message.Content)
		if err != nil || strings.TrimSpace(text) == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
			return nil
		}
		msg := &InboundMessage{
			Platform: PlatformFeishu,
			ChatID:   ev.Message.ChatID,
			UserID:   ev.Sender.SenderID.OpenID,
			Text:     text,
			EventID:  body.Header.EventID,
		}
		reply := d.OnUserMessage(r.Context(), ch, msg)
		if reply != "" {
			// 同步回复(飞书事件订阅要求 3s 内响应, 长问题由 dispatcher 内部限时, 超时回复降级提示)
			_ = f.SendText(r.Context(), ch, ev.Message.ChatID, reply)
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
	case "card.action.trigger":
		// 审批卡片按钮回调
		var cardEv feishuCardAction
		if err := json.Unmarshal(body.Event, &cardEv); err != nil {
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
			return nil
		}
		approvalID := cardEv.Action.Value["approval_id"]
		action := cardEv.Action.Value["action"] // approve / reject
		if approvalID == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
			return nil
		}
		tip := d.OnApprovalAction(r.Context(), ch, approvalID, action, cardEv.Operator.OpenID, "")
		// 飞书卡片回调响应体可直接更新卡片, MVP 返回 toast 提示
		toastType := "success"
		if action == "" {
			toastType = "error"
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"toast": map[string]string{"type": toastType, "content": tip},
		})
	default:
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"code": "0"})
	}
	return nil
}

type feishuCardAction struct {
	Operator struct {
		OpenID string `json:"open_id"`
	} `json:"operator"`
	Action struct {
		Value map[string]string `json:"value"`
	} `json:"action"`
}

// parseTextContent 解析 im.message.content: {"text":"@_user_1 你好"} → 剥离 @机器人 标记。
var atMentionRe = regexp.MustCompile(`@_user_\d+`)

func (f *feishuPlatform) parseTextContent(content string) (string, error) {
	var c struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &c); err != nil {
		return "", err
	}
	return strings.TrimSpace(atMentionRe.ReplaceAllString(c.Text, "")), nil
}

// ---- 消息发送 ----

// tenantAccessToken 获取并缓存 tenant_access_token(提前 60s 过期)。
func (f *feishuPlatform) tenantAccessToken(ctx context.Context, cfg *feishuSettings) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tokens[cfg.AppID]; ok && time.Now().Before(t.expire) {
		return t.token, nil
	}
	payload, _ := json.Marshal(map[string]string{"app_id": cfg.AppID, "app_secret": cfg.AppSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuBase+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", fmt.Errorf("获取 tenant_access_token 失败: code=%d msg=%s", out.Code, out.Msg)
	}
	f.tokens[cfg.AppID] = feishuToken{token: out.TenantAccessToken, expire: time.Now().Add(time.Duration(out.Expire-60) * time.Second)}
	return out.TenantAccessToken, nil
}

func (f *feishuPlatform) SendText(ctx context.Context, ch *Channel, chatID, text string) error {
	var cfg feishuSettings
	if err := json.Unmarshal(ch.Settings, &cfg); err != nil {
		return err
	}
	token, err := f.tenantAccessToken(ctx, &cfg)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    mustJSON(map[string]string{"text": feishuEscape(text)}),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		feishuBase+"/open-apis/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.Code != 0 {
		return fmt.Errorf("飞书发消息失败: code=%d msg=%s", out.Code, out.Msg)
	}
	return nil
}

func (f *feishuPlatform) SendApprovalCard(ctx context.Context, ch *Channel, chatID string, card *ApprovalCard) (string, error) {
	var cfg feishuSettings
	if err := json.Unmarshal(ch.Settings, &cfg); err != nil {
		return "", err
	}
	token, err := f.tenantAccessToken(ctx, &cfg)
	if err != nil {
		return "", err
	}
	cardJSON := map[string]any{
		"config": map[string]bool{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": "🔐 AI 操作待审批"},
			"template": "orange",
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]string{
					"tag":     "lark_md",
					"content": fmt.Sprintf("**工具**: %s\n**操作**: %s\n**参数**: %s", card.Tool, card.Summary, card.Args),
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":   "button",
						"text":  map[string]string{"tag": "plain_text", "content": "✅ 批准"},
						"type":  "primary",
						"value": map[string]string{"approval_id": card.ID, "action": "approve"},
					},
					map[string]any{
						"tag":   "button",
						"text":  map[string]string{"tag": "plain_text", "content": "❌ 拒绝"},
						"type":  "danger",
						"value": map[string]string{"approval_id": card.ID, "action": "reject"},
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    mustJSON(cardJSON),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		feishuBase+"/open-apis/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("飞书发卡片失败: code=%d msg=%s", out.Code, out.Msg)
	}
	return out.Data.MessageID, nil
}

// feishuEscape 飞书 text 消息的转义(@ 与链接控制字符)。
func feishuEscape(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func init() { registerPlatform(newFeishuPlatform()) }
