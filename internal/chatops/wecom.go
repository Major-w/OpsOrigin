package chatops

// 企业微信自建应用适配器(P3-2b)。
//
// 与飞书侧的对应关系:
//   - 事件订阅 URL 验证(GET echostr) ↔ 飞书 url_verification
//   - 加密回调(AES-256-CBC + msg_signature SHA1) ↔ 飞书 Encrypt Key
//   - message/send(文本) / template_card(审批卡片) ↔ 飞书 im/v1/messages
//
// 企微回调默认 XML 格式(明文模式不安全且企微要求回调解密, 故只实现加密模式),
// 解密后为业务 XML。群聊(chatID 前缀 wr/wj)走 appchat/send, 用户单聊走 message/send。

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/pkg/httputil"
)

const wecomBase = "https://qyapi.weixin.qq.com"

// wecomSettings 企微自建应用凭据(存 chatops_channels.settings)。
type wecomSettings struct {
	CorpID     string `json:"corp_id"`
	CorpSecret string `json:"corp_secret"`
	AgentID    int    `json:"agent_id"`
	// Token / EncodingAESKey 回调验签与解密(企微后台「接收消息」设置页获取)。
	Token          string `json:"token"`
	EncodingAESKey string `json:"encoding_aes_key"`
	// DefaultAgentID 该渠道 IM 问答绑定的 AI Agent(空=引擎默认)。
	DefaultAgentID string `json:"default_agent_id,omitempty"`
}

type wecomPlatform struct {
	http *http.Client

	mu     sync.Mutex
	tokens map[string]wecomToken // corp_id+"\x00"+secret → access_token
}

type wecomToken struct {
	token  string
	expire time.Time
}

func newWecomPlatform() *wecomPlatform {
	return &wecomPlatform{
		http:   &http.Client{Timeout: 15 * time.Second},
		tokens: map[string]wecomToken{},
	}
}

func (w *wecomPlatform) Name() string { return PlatformWecom }

// ---- 回调加解密(企微官方加解密协议) ----

// wecomAESKey EncodingAESKey(43 字符 Base64) → 32 字节 AES 密钥。
func wecomAESKey(encodingAESKey string) ([]byte, error) {
	if len(encodingAESKey) != 43 {
		return nil, fmt.Errorf("encoding_aes_key 长度须为 43, 实际 %d", len(encodingAESKey))
	}
	return base64.StdEncoding.DecodeString(encodingAESKey + "=")
}

// wecomVerifySignature 校验 msg_signature = sha1(sort(token, timestamp, nonce, encrypt))。
func wecomVerifySignature(token, timestamp, nonce, encrypt, signature string) bool {
	if token == "" {
		return false
	}
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", h) == signature
}

// wecomDecrypt 解密回调密文: 明文 = random(16B) + msg_len(4B BE) + msg + corp_id。
func wecomDecrypt(encryptStr string, aesKey []byte) ([]byte, error) {
	cipherText, err := base64.StdEncoding.DecodeString(encryptStr)
	if err != nil {
		return nil, fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	if len(cipherText) < aes.BlockSize || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度非法")
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	iv := aesKey[:aes.BlockSize]
	plain := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, cipherText)
	// PKCS7 去填充
	pad := int(plain[len(plain)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, fmt.Errorf("密文填充非法")
	}
	plain = plain[:len(plain)-pad]
	if len(plain) < 20 {
		return nil, fmt.Errorf("解密后内容过短")
	}
	msgLen := binary.BigEndian.Uint32(plain[16:20])
	if int(msgLen)+20 > len(plain) {
		return nil, fmt.Errorf("消息长度字段非法")
	}
	return plain[20 : 20+msgLen], nil
}

// ---- 回调 XML 结构 ----

// wecomCallbackEnvelope 回调外层(XML): Encrypt 字段携带密文。
type wecomCallbackEnvelope struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	Encrypt    string   `xml:"Encrypt"`
	AgentID    string   `xml:"AgentID"`
}

// wecomCallbackXML 解密后的业务消息。
type wecomCallbackXML struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        int64    `xml:"MsgId"`
	// ChatId 群聊消息的群 ID(wr/wj 前缀); 单聊为空, 用 FromUserName 作会话键。
	ChatId string `xml:"ChatId"`
	// Event 事件类型(template_card_event 等)。
	Event     string `xml:"Event"`
	TaskID    string `xml:"TaskId"`
	ButtonKey string `xml:"ButtonKey"`
	// Approval 组件不做, 审批动作用 ButtonKey 承载。
}

// ---- webhook 主入口 ----

func (w *wecomPlatform) HandleWebhook(wr http.ResponseWriter, r *http.Request, ch *Channel, d Dispatcher) error {
	var cfg wecomSettings
	if err := json.Unmarshal(ch.Settings, &cfg); err != nil {
		httputil.WriteError(wr, http.StatusInternalServerError, "channel settings invalid")
		return nil
	}
	aesKey, err := wecomAESKey(cfg.EncodingAESKey)
	if err != nil {
		httputil.WriteError(wr, http.StatusInternalServerError, "encoding_aes_key invalid")
		return nil
	}

	// URL 验证(企微后台保存回调配置时): GET echostr, 校验签名后回显解密明文
	if r.Method == http.MethodGet {
		if !w.verifyQuery(r, cfg.Token, aesKey, wr, true) {
			return nil
		}
		return nil // verifyQuery 内已写响应
	}

	// 普通回调: POST, query 带 msg_signature/timestamp/nonce, body 为 XML envelope
	q := r.URL.Query()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httputil.WriteError(wr, http.StatusBadRequest, "read body failed")
		return nil
	}
	var env wecomCallbackEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil || env.Encrypt == "" {
		// 非目标格式, 200 吞掉防重试风暴
		_, _ = wr.Write([]byte("success"))
		return nil
	}
	if !wecomVerifySignature(cfg.Token, q.Get("timestamp"), q.Get("nonce"), env.Encrypt, q.Get("msg_signature")) {
		httputil.WriteError(wr, http.StatusUnauthorized, "signature mismatch")
		return nil
	}
	plain, err := wecomDecrypt(env.Encrypt, aesKey)
	if err != nil {
		httputil.WriteError(wr, http.StatusBadRequest, "decrypt failed")
		return nil
	}
	var cb wecomCallbackXML
	if err := xml.Unmarshal(plain, &cb); err != nil {
		_, _ = wr.Write([]byte("success"))
		return nil
	}

	switch {
	case cb.MsgType == "text":
		chatID := cb.ChatId
		if chatID == "" {
			chatID = cb.FromUserName // 单聊: 会话键=成员 userid
		}
		if strings.TrimSpace(cb.Content) == "" || chatID == "" {
			break
		}
		msg := &InboundMessage{
			Platform: PlatformWecom,
			ChatID:   chatID,
			UserID:   cb.FromUserName,
			Text:     strings.TrimSpace(cb.Content),
			EventID:  fmt.Sprintf("wecom-%d", cb.MsgID),
		}
		reply := d.OnUserMessage(r.Context(), ch, msg)
		if reply != "" {
			_ = w.SendText(r.Context(), ch, chatID, reply)
		}
	case cb.Event == "template_card_event" && cb.ButtonKey != "":
		// 审批卡片按钮: ButtonKey = "<approval_id>:<action>"
		parts := strings.SplitN(cb.ButtonKey, ":", 2)
		action := ""
		if len(parts) == 2 {
			action = parts[1]
		}
		tip := d.OnApprovalAction(r.Context(), ch, parts[0], action, cb.FromUserName, "")
		if tip != "" {
			_ = w.SendText(r.Context(), ch, cb.FromUserName, tip)
		}
	}
	// 企微要求 5s 内响应; 均返回 success(已通过 API 异步回复, 不走响应体回消息)
	_, _ = wr.Write([]byte("success"))
	return nil
}

// verifyQuery 处理 GET 验证: 校验签名并回显解密后的 echostr。返回是否校验通过。
func (w *wecomPlatform) verifyQuery(r *http.Request, token string, aesKey []byte, wr http.ResponseWriter, echo bool) bool {
	q := r.URL.Query()
	echoStr := q.Get("echostr")
	if !wecomVerifySignature(token, q.Get("timestamp"), q.Get("nonce"), echoStr, q.Get("msg_signature")) {
		httputil.WriteError(wr, http.StatusUnauthorized, "signature mismatch")
		return false
	}
	plain, err := wecomDecrypt(echoStr, aesKey)
	if err != nil {
		httputil.WriteError(wr, http.StatusBadRequest, "decrypt failed")
		return false
	}
	wr.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = wr.Write(plain)
	return true
}

// ---- access_token 与消息发送 ----

// accessToken 获取并缓存应用 access_token(提前 300s 过期)。
func (w *wecomPlatform) accessToken(ctx context.Context, cfg *wecomSettings) (string, error) {
	key := cfg.CorpID + "\x00" + cfg.CorpSecret
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.tokens[key]; ok && time.Now().Before(t.expire) {
		return t.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s", wecomBase, cfg.CorpID, cfg.CorpSecret), nil)
	if err != nil {
		return "", err
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("获取 access_token 失败: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	w.tokens[key] = wecomToken{token: out.AccessToken, expire: time.Now().Add(time.Duration(out.ExpiresIn-300) * time.Second)}
	return out.AccessToken, nil
}

// isGroupChat 群聊会话 ID 识别: 企微群 ID 为 wr/wj 前缀(应用群/互联群)。
func isGroupChat(chatID string) bool {
	return strings.HasPrefix(chatID, "wr") || strings.HasPrefix(chatID, "wj")
}

func (w *wecomPlatform) SendText(ctx context.Context, ch *Channel, chatID, text string) error {
	var cfg wecomSettings
	if err := json.Unmarshal(ch.Settings, &cfg); err != nil {
		return err
	}
	token, err := w.accessToken(ctx, &cfg)
	if err != nil {
		return err
	}
	payload, path := w.sendPayload(&cfg, chatID, map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
	return w.post(ctx, token, path, payload)
}

func (w *wecomPlatform) SendApprovalCard(ctx context.Context, ch *Channel, chatID string, card *ApprovalCard) (string, error) {
	var cfg wecomSettings
	if err := json.Unmarshal(ch.Settings, &cfg); err != nil {
		return "", err
	}
	token, err := w.accessToken(ctx, &cfg)
	if err != nil {
		return "", err
	}
	cardBody := map[string]any{
		"msgtype": "template_card",
		"template_card": map[string]any{
			"card_type": "button_list",
			"source":    map[string]string{"icon_url": "", "desc": "OpsOrigin"},
			"main_title": map[string]string{
				"title": "🔐 AI 操作待审批",
			},
			"sub_title_text": fmt.Sprintf("工具: %s\n操作: %s\n参数: %s", card.Tool, card.Summary, card.Args),
			"button_list": []any{
				map[string]string{"text": "✅ 批准", "type": "primary", "key": card.ID + ":approve"},
				map[string]string{"text": "❌ 拒绝", "type": "danger", "key": card.ID + ":reject"},
			},
			"task_id": card.ID,
		},
	}
	payload, path := w.sendPayload(&cfg, chatID, cardBody)
	if err := w.post(ctx, token, path, payload); err != nil {
		return "", err
	}
	return card.ID, nil
}

// sendPayload 按单聊/群聊装配发送体与端点路径。
func (w *wecomPlatform) sendPayload(cfg *wecomSettings, chatID string, body map[string]any) (string, string) {
	body["agentid"] = cfg.AgentID
	if isGroupChat(chatID) {
		body["chatid"] = chatID
		return mustJSON(body), "/cgi-bin/appchat/send"
	}
	body["touser"] = chatID
	return mustJSON(body), "/cgi-bin/message/send"
}

func (w *wecomPlatform) post(ctx context.Context, token, path string, payload string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		wecomBase+path+"?access_token="+token, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("企微发消息失败: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	return nil
}

func init() { registerPlatform(newWecomPlatform()) }
