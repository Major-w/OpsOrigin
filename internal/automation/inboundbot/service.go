// Package inboundbot 提供多渠道机器人入站受理: 企业微信(WeCom)与钉钉(DingTalk)
// 回调机器人收到用户消息后, 平台通过 webhook 接收并路由——
// 消息命中工单 ID → 作为回复追加评论; 未命中 → 创建新工单(渠道标记 wecom/dingtalk)。
//
// 设计: 不依赖 internal/itsm, 处理动作由 cmd/core 注入回调(避免 import 环);
// 各渠道独立 Enable, 未配置则不挂载对应 webhook。签名校验与加解密均用标准库实现, 零三方依赖。
package inboundbot

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Config 多渠道机器人入站配置(由 cmd/core 从环境变量或入站配置存储装配)。
type Config struct {
	WeCom    WeComConfig
	DingTalk DingTalkConfig
	Feishu   FeishuConfig
}

// FeishuConfig 飞书(Lark)事件订阅回调配置。
type FeishuConfig struct {
	Enable     bool   // 总开关
	Token      string // Verification Token：用于 URL 验证与签名校验(HMAC key)
	Secret     string // App Secret：参与签名计算(sign = base64(HMAC-SHA256(timestamp+"\n"+secret, key=Token)))
	EncryptKey string // 可选：数据加密模式 EncodingAESKey(43 字符)，留空=明文模式
	TenantID   string // 工单归属租户, 默认 default
	ProcessID  string // 可选: 机器人提单默认流程模板 ID
}

// WeComConfig 企业微信回调配置(自建应用消息回调模式)。
type WeComConfig struct {
	Enable    bool
	Token     string // 企业微信后台配置的 Token
	AESKey    string // EncodingAESKey(43 字符)
	CorpID    string // 企业 ID, 即消息密文里的 receiveid
	AgentID   string // 应用 AgentId(仅日志/回写用, 非必填)
	TenantID  string // 工单归属租户, 默认 default
	ProcessID string // 可选: 机器人提单默认流程模板 ID
}

// DingTalkConfig 钉钉回调配置(事件订阅 HTTP 回调模式, 非加密/加密皆可)。
type DingTalkConfig struct {
	Enable    bool
	Token     string // 钉钉回调 Token(URL 验证与消息验签共用)
	AESKey    string // 可选: 数据加密模式 EncodingAESKey(43 字符); 留空=非加密模式
	TenantID  string
	ProcessID string
}

// Handlers 机器人消息处理回调(由 cmd/core 注入)。
type Handlers struct {
	// ReplyToTicket 消息命中工单 ID → 追加评论(作者带渠道前缀)。
	ReplyToTicket func(ctx context.Context, tenantID, ticketID, author, body string) error
	// CreateTicket 未命中 → 创建工单, channel 为 wecom/dingtalk, 返回工单 ID。
	CreateTicket func(ctx context.Context, tenantID, channel, from, subject, body string) (string, error)
}

// Service 多渠道机器人入站服务。
type Service struct {
	cfg         Config
	cfgProvider func() Config // 运行时配置提供者(由 cmd/core 注入, 从入站配置存储读取, 使 UI 改动实时生效)
	handlers    Handlers
}

// New 构造服务并补全缺省租户。
func New(cfg Config) *Service {
	if cfg.WeCom.TenantID == "" {
		cfg.WeCom.TenantID = "default"
	}
	if cfg.DingTalk.TenantID == "" {
		cfg.DingTalk.TenantID = "default"
	}
	return &Service{cfg: cfg}
}

// SetHandlers 注入处理回调(建单/回复)。
func (s *Service) SetHandlers(h Handlers) { s.handlers = h }

// SetConfigProvider 注入运行时配置提供者(从入站配置存储读取),使 UI 改动实时生效。
// 未注入时回退到构造时传入的静态 Config(兼容纯环境变量装配场景)。
func (s *Service) SetConfigProvider(p func() Config) { s.cfgProvider = p }

// CurrentConfig 返回当前生效配置:优先运行时提供者,否则静态 Config。
func (s *Service) CurrentConfig() Config {
	if s.cfgProvider != nil {
		return s.cfgProvider()
	}
	return s.cfg
}

// RegisterRoutes 把各渠道 webhook 挂到 mux(始终挂载, 渠道启用态在请求时按当前配置判定)。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/inbound/wecom", s.handleWeComVerify)
	mux.HandleFunc("POST /api/v1/inbound/wecom", s.handleWeComMessage)
	mux.HandleFunc("POST /api/v1/inbound/dingtalk", s.handleDingTalk)
	mux.HandleFunc("GET /api/v1/inbound/dingtalk", s.handleDingTalk)
	mux.HandleFunc("GET /api/v1/inbound/feishu", s.handleFeishuVerify)
	mux.HandleFunc("POST /api/v1/inbound/feishu", s.handleFeishuMessage)
}

// ticketIDRe 匹配工单 UUID(ID 形态), 与 inboundmail 保持一致。
var ticketIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// route 单条消息路由: 命中工单 ID → 回复评论; 否则 → 建单(标题取自正文前 60 字)。
func (s *Service) route(ctx context.Context, channel, tenantID, from, subject, body string) {
	if s.handlers.ReplyToTicket == nil && s.handlers.CreateTicket == nil {
		return
	}
	ticketID := ticketIDRe.FindString(subject + " " + body)
	if ticketID != "" && s.handlers.ReplyToTicket != nil {
		if err := s.handlers.ReplyToTicket(ctx, tenantID, ticketID, channel+":"+from, body); err != nil {
			slog.Warn("inbound bot: reply failed", "channel", channel, "ticket", ticketID, "error", err)
			return
		}
		slog.Info("inbound bot: replied", "channel", channel, "ticket", ticketID, "from", from)
		return
	}
	if s.handlers.CreateTicket != nil {
		title := subject
		if title == "" {
			title = body
		}
		if r := []rune(title); len(r) > 60 {
			title = string(r[:60]) + "…"
		}
		id, err := s.handlers.CreateTicket(ctx, tenantID, channel, from, title, body)
		if err != nil {
			slog.Warn("inbound bot: create failed", "channel", channel, "error", err)
			return
		}
		slog.Info("inbound bot: created ticket", "channel", channel, "ticket", id, "from", from)
	}
}

// ---------------------------------------------------------------------------
// 企业微信(WeCom)回调: 签名校验 + AES-256-CBC(PKCS7) 加解密
// ---------------------------------------------------------------------------

func wecomSignature(token, timestamp, nonce, echo string) string {
	arr := []string{token, timestamp, nonce, echo}
	sort.Strings(arr)
	h := sha1.Sum([]byte(strings.Join(arr, "")))
	return hex.EncodeToString(h[:])
}

// wecomDecodeKey 把 43 字符 EncodingAESKey 解码为 32 字节 AES 密钥。
func wecomDecodeKey(aesKey string) ([]byte, error) {
	if len(aesKey) != 43 {
		return nil, fmt.Errorf("EncodingAESKey must be 43 chars, got %d", len(aesKey))
	}
	b, err := base64.StdEncoding.DecodeString(aesKey + "=")
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("decoded key length %d, want 32", len(b))
	}
	return b, nil
}

// wecomDecrypt 解密企业微信消息体: base64 → AES-CBC 去 PKCS7 → 去 16 随机 + 4 长度头 → 取明文。
func wecomDecrypt(key []byte, encrypted string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	plain, err := aesCBCDecrypt(key, raw)
	if err != nil {
		return "", err
	}
	// 结构: random(16) + msgLen(4, 大端) + msg + receiveid
	if len(plain) < 20 {
		return "", fmt.Errorf("plain too short")
	}
	msgLen := int(binary.BigEndian.Uint32(plain[16:20]))
	if 20+msgLen > len(plain) {
		return "", fmt.Errorf("bad msg len")
	}
	return string(plain[20 : 20+msgLen]), nil
}

// wecomEncrypt 加密(供 URL 验证回包或测试使用)。
func wecomEncrypt(key []byte, msg, receiveID string) (string, error) {
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	buf.Write(randBytes)
	_ = binary.Write(buf, binary.BigEndian, uint32(len(msg)))
	buf.WriteString(msg)
	buf.WriteString(receiveID)
	enc, err := aesCBCEncrypt(key, buf.Bytes())
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// handleWeComVerify GET 回调地址验证: 验签后 AES 解密 echostr 原样返回。
func (s *Service) handleWeComVerify(w http.ResponseWriter, r *http.Request) {
	cfg := s.CurrentConfig()
	if !cfg.WeCom.Enable {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	msgSig := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	echostr := q.Get("echostr")
	key, err := wecomDecodeKey(cfg.WeCom.AESKey)
	if err != nil {
		http.Error(w, "bad aeskey", http.StatusInternalServerError)
		return
	}
	if wecomSignature(cfg.WeCom.Token, timestamp, nonce, echostr) != msgSig {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	plain, err := wecomDecrypt(key, echostr)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(plain))
}

// handleWeComMessage POST 消息回调: 验签 → 解密 → 解析文本 → 路由。
func (s *Service) handleWeComMessage(w http.ResponseWriter, r *http.Request) {
	cfg := s.CurrentConfig()
	if !cfg.WeCom.Enable {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	msgSig := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	body, _ := io.ReadAll(r.Body)
	var env struct {
		XMLName xml.Name `xml:"xml"`
		Encrypt string   `xml:"Encrypt"`
	}
	if err := xml.Unmarshal(body, &env); err != nil || env.Encrypt == "" {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	key, err := wecomDecodeKey(cfg.WeCom.AESKey)
	if err != nil {
		http.Error(w, "bad aeskey", http.StatusInternalServerError)
		return
	}
	if wecomSignature(cfg.WeCom.Token, timestamp, nonce, env.Encrypt) != msgSig {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	plain, err := wecomDecrypt(key, env.Encrypt)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	var msg struct {
		XMLName  xml.Name `xml:"xml"`
		MsgType  string   `xml:"MsgType"`
		Content  string   `xml:"Content"`
		FromUser string   `xml:"FromUserName"`
	}
	if err := xml.Unmarshal([]byte(plain), &msg); err != nil {
		http.Error(w, "parse failed", http.StatusBadRequest)
		return
	}
	if msg.MsgType != "text" || strings.TrimSpace(msg.Content) == "" {
		writeText(w, "success")
		return
	}
	s.route(r.Context(), "wecom", cfg.WeCom.TenantID, msg.FromUser, msg.Content, msg.Content)
	writeText(w, "success")
}

// ---------------------------------------------------------------------------
// 钉钉(DingTalk)回调: sha256 字典序签名 + JSON 消息解析
// ---------------------------------------------------------------------------

func dingSignature(token, timestamp, nonce, payload string) string {
	arr := []string{token, timestamp, nonce, payload}
	sort.Strings(arr)
	h := sha256.Sum256([]byte(strings.Join(arr, "")))
	return hex.EncodeToString(h[:])
}

// handleDingTalk POST/GET 回调: 区分 URL 验证(check_url) 与消息回调。
func (s *Service) handleDingTalk(w http.ResponseWriter, r *http.Request) {
	cfg := s.CurrentConfig()
	if !cfg.DingTalk.Enable {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	signature := q.Get("signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	bodyBytes, _ := io.ReadAll(r.Body)

	var probe map[string]any
	_ = json.Unmarshal(bodyBytes, &probe)

	// URL 验证
	if t, _ := probe["type"].(string); t == "check_url" {
		random, _ := probe["random"].(string)
		if cfg.DingTalk.Token != "" {
			if dingSignature(cfg.DingTalk.Token, timestamp, nonce, random) != signature {
				http.Error(w, "invalid signature", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"signature": dingSignature(cfg.DingTalk.Token, timestamp, nonce, random),
			"timestamp": timestamp,
			"nonce":     nonce,
			"echostr":   random,
		})
		return
	}

	// 消息验签(非加密模式: 对原始 body 签名; 加密模式可在此扩展)
	if cfg.DingTalk.Token != "" {
		if dingSignature(cfg.DingTalk.Token, timestamp, nonce, string(bodyBytes)) != signature {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}

	var dm struct {
		MsgType        string `json:"msgtype"`
		Text           struct {
			Content string `json:"content"`
		} `json:"text"`
		SenderID       string `json:"senderId"`
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(bodyBytes, &dm); err != nil {
		writeText(w, "success")
		return
	}
	if dm.MsgType != "text" || strings.TrimSpace(dm.Text.Content) == "" {
		writeText(w, "success")
		return
	}
	s.route(r.Context(), "dingtalk", s.cfg.DingTalk.TenantID, dm.SenderID, dm.Text.Content, dm.Text.Content)
	writeText(w, "success")
}

// ---------------------------------------------------------------------------
// 飞书(Lark)回调: 验签(HMAC-SHA256) + 可选 AES 加密 + 事件路由
// ---------------------------------------------------------------------------

func feishuSignature(token, timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuDecrypt 解密飞书加密消息体(数据加密模式): EncodingAESKey(43 字符) → 32 字节 key,
// AES-256-CBC, IV = key[:16], PKCS7 去填充, 明文即 JSON(无 WeCom 式 16+4 头)。
func feishuDecrypt(key []byte, encrypted string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	plain, err := aesCBCDecrypt(key, raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func feishuDecodeKey(aesKey string) ([]byte, error) {
	if len(aesKey) != 43 {
		return nil, fmt.Errorf("EncodingAESKey must be 43 chars, got %d", len(aesKey))
	}
	b, err := base64.StdEncoding.DecodeString(aesKey + "=")
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("decoded key length %d, want 32", len(b))
	}
	return b, nil
}

// handleFeishuVerify GET URL 验证(v1.0): 校验 token 与 signature=base64(HMAC-SHA256(timestamp+"\n"+secret, key=Token)),
// 校验通过原样返回 challenge。
func (s *Service) handleFeishuVerify(w http.ResponseWriter, r *http.Request) {
	cfg := s.CurrentConfig()
	if !cfg.Feishu.Enable {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	token := q.Get("token")
	timestamp := q.Get("timestamp")
	sign := q.Get("signature")
	challenge := q.Get("challenge")
	if token != cfg.Feishu.Token {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if cfg.Feishu.Secret != "" {
		if feishuSignature(cfg.Feishu.Token, timestamp, cfg.Feishu.Secret) != sign {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}
	writeText(w, challenge)
}

// handleFeishuMessage POST 事件回调: 支持 v2.0 url_verification 与 event_callback(明文/加密模式)。
func (s *Service) handleFeishuMessage(w http.ResponseWriter, r *http.Request) {
	cfg := s.CurrentConfig()
	if !cfg.Feishu.Enable {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var env struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Token     string `json:"token"`
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
		Encrypt   string `json:"encrypt"`
		Event     struct {
			MsgType string `json:"msg_type"`
			Content string `json:"content"`
			Sender  struct {
				SenderID struct {
					OpenID     string `json:"open_id"`
					EmployeeID string `json:"employee_id"`
				} `json:"sender_id"`
			} `json:"sender"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		writeText(w, "success")
		return
	}
	// v2.0 URL 验证
	if env.Type == "url_verification" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"challenge": env.Challenge})
		return
	}
	// 签名校验(加密/明文模式通用)
	if cfg.Feishu.Secret != "" {
		if feishuSignature(cfg.Feishu.Token, env.Timestamp, cfg.Feishu.Secret) != env.Sign {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}
	// 解密(若启用加密模式)
	if env.Encrypt != "" {
		if cfg.Feishu.EncryptKey == "" {
			writeText(w, "success")
			return
		}
		key, err := feishuDecodeKey(cfg.Feishu.EncryptKey)
		if err != nil {
			writeText(w, "success")
			return
		}
		dec, err := feishuDecrypt(key, env.Encrypt)
		if err != nil {
			writeText(w, "success")
			return
		}
		var inner struct {
			Event struct {
				MsgType string `json:"msg_type"`
				Content string `json:"content"`
				Sender  struct {
					SenderID struct {
						OpenID     string `json:"open_id"`
						EmployeeID string `json:"employee_id"`
					} `json:"sender_id"`
				} `json:"sender"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(dec), &inner); err == nil {
			env.Event = inner.Event
		}
	}
	if env.Event.MsgType != "text" || env.Event.Content == "" {
		writeText(w, "success")
		return
	}
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(env.Event.Content), &content); err != nil || strings.TrimSpace(content.Text) == "" {
		writeText(w, "success")
		return
	}
	from := env.Event.Sender.SenderID.OpenID
	if from == "" {
		from = env.Event.Sender.SenderID.EmployeeID
	}
	s.route(r.Context(), "feishu", cfg.Feishu.TenantID, from, content.Text, content.Text)
	writeText(w, "success")
}

// ---------------------------------------------------------------------------
// 通用加解密 / 工具
// ---------------------------------------------------------------------------

func aesCBCDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not multiple of block size")
	}
	iv := key[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)
	plain := make([]byte, len(ciphertext))
	mode.CryptBlocks(plain, ciphertext)
	return pkcs7Unpad(plain)
}

func aesCBCEncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	iv := key[:aes.BlockSize]
	mode := cipher.NewCBCEncrypter(block, iv)
	out := make([]byte, len(padded))
	mode.CryptBlocks(out, padded)
	return out, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty")
	}
	pad := int(data[len(data)-1])
	if pad <= 0 || pad > len(data) {
		return nil, fmt.Errorf("bad padding")
	}
	return data[:len(data)-pad], nil
}

func writeText(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(s))
}
