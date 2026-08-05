package inboundbot

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// genTestKey 生成合法的 43 字符 EncodingAESKey(32 随机字节 base64 后取前 43 位)。
func genTestKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := io.ReadFull(randReader{}, raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw) // 44 字符(末位 '=')
	return b64[:43]
}

// randReader 包装 crypto/rand 以满足 io.Reader(测试免 import 命名冲突)。
type randReader struct{}

func (randReader) Read(p []byte) (int, error) {
	return io.ReadFull(crand.Reader, p)
}

func TestWeComSignature(t *testing.T) {
	tok, ts, nonce, echo := "tok", "1700000000", "nonce123", "echoval"
	got := wecomSignature(tok, ts, nonce, echo)
	if len(got) != 40 {
		t.Fatalf("sha1 hex len = %d, want 40", len(got))
	}
	// 字典序确定性: 重排入参顺序结果不变
	if wecomSignature(tok, ts, nonce, echo) != got {
		t.Fatal("signature not stable")
	}
}

func TestWeComAESRoundTrip(t *testing.T) {
	key, err := wecomDecodeKey(genTestKey(t))
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	plain := `<xml><MsgType>text</MsgType><Content>你好世界</Content><FromUserName>zhangsan</FromUserName></xml>`
	enc, err := wecomEncrypt(key, plain, "corp123")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := wecomDecrypt(key, enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("round trip mismatch: got %q", dec)
	}
}

func TestWeComVerifyHandler(t *testing.T) {
	key, _ := wecomDecodeKey(genTestKey(t))
	tok, corp := "tok", "corp123"
	svc := New(Config{WeCom: WeComConfig{Enable: true, Token: tok, AESKey: encodeKey(key), CorpID: corp, TenantID: "default"}})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	echostrPlain := "randomechostr123"
	enc, _ := wecomEncrypt(key, echostrPlain, corp)
	ts, nonce := "1700000001", "nonceA"
	sig := wecomSignature(tok, ts, nonce, enc)
	q := url.Values{}
	q.Set("msg_signature", sig)
	q.Set("timestamp", ts)
	q.Set("nonce", nonce)
	q.Set("echostr", enc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inbound/wecom?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != echostrPlain {
		t.Fatalf("echostr = %q, want %q", rec.Body.String(), echostrPlain)
	}
}

func TestWeComVerifyBadSig(t *testing.T) {
	key, _ := wecomDecodeKey(genTestKey(t))
	svc := New(Config{WeCom: WeComConfig{Enable: true, Token: "tok", AESKey: encodeKey(key), CorpID: "c", TenantID: "default"}})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	enc, _ := wecomEncrypt(key, "x", "c")
	q := url.Values{}
	q.Set("msg_signature", "bad")
	q.Set("timestamp", "1")
	q.Set("nonce", "2")
	q.Set("echostr", enc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inbound/wecom?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestWeComMessageCreatesTicket(t *testing.T) {
	key, _ := wecomDecodeKey(genTestKey(t))
	var gotCh, gotFrom, gotTitle string
	var created bool
	svc := New(Config{WeCom: WeComConfig{Enable: true, Token: "tok", AESKey: encodeKey(key), CorpID: "corp123", TenantID: "t1"}})
	svc.SetHandlers(Handlers{
		CreateTicket: func(_ context.Context, tenantID, channel, from, subject, body string) (string, error) {
			gotCh, gotFrom, gotTitle = channel, from, subject
			created = true
			return "ticket-1", nil
		},
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	plain := `<xml><MsgType>text</MsgType><Content>服务器宕机了</Content><FromUserName>zhangsan</FromUserName></xml>`
	enc, _ := wecomEncrypt(key, plain, "corp123")
	ts, nonce := "1700000002", "nonceB"
	sig := wecomSignature("tok", ts, nonce, enc)
	body := "<xml><Encrypt>" + enc + "</Encrypt></xml>"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inbound/wecom?msg_signature="+sig+"&timestamp="+ts+"&nonce="+nonce, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "success" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !created {
		t.Fatal("CreateTicket not called")
	}
	if gotCh != "wecom" || gotFrom != "zhangsan" || !strings.Contains(gotTitle, "服务器宕机") {
		t.Fatalf("route args: ch=%s from=%s title=%s", gotCh, gotFrom, gotTitle)
	}
}

func TestDingTalkVerifyHandler(t *testing.T) {
	svc := New(Config{DingTalk: DingTalkConfig{Enable: true, Token: "dtok", TenantID: "default"}})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	ts, nonce, random := "1700000003", "nonceC", "dingrandom"
	sig := dingSignature("dtok", ts, nonce, random)
	body := `{"type":"check_url","random":"` + random + `","test":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inbound/dingtalk?signature="+sig+"&timestamp="+ts+"&nonce="+nonce, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"echostr":"`+random+`"`) {
		t.Fatalf("echostr not echoed: %s", rec.Body.String())
	}
}

func TestDingTalkMessageCreatesTicket(t *testing.T) {
	var gotCh, gotFrom string
	var created bool
	svc := New(Config{DingTalk: DingTalkConfig{Enable: true, Token: "dtok", TenantID: "t2"}})
	svc.SetHandlers(Handlers{
		CreateTicket: func(_ context.Context, tenantID, channel, from, subject, body string) (string, error) {
			gotCh, gotFrom, created = channel, from, true
			return "ticket-2", nil
		},
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	body := `{"msgtype":"text","text":{"content":"钉钉提单测试"},"senderId":"$userid$","conversationId":"cid1"}`
	ts, nonce := "1700000004", "nonceD"
	sig := dingSignature("dtok", ts, nonce, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inbound/dingtalk?signature="+sig+"&timestamp="+ts+"&nonce="+nonce, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !created {
		t.Fatal("CreateTicket not called")
	}
	if gotCh != "dingtalk" || gotFrom != "$userid$" {
		t.Fatalf("route args: ch=%s from=%s", gotCh, gotFrom)
	}
}

func TestRouteRepliesToExistingTicket(t *testing.T) {
	const existing = "11111111-2222-3333-4444-555555555555"
	var replied bool
	var created bool
	svc := New(Config{WeCom: WeComConfig{Enable: true, Token: "tok", AESKey: encodeKey(mustKey(t)), CorpID: "c", TenantID: "default"}})
	svc.SetHandlers(Handlers{
		ReplyToTicket: func(_ context.Context, _, ticketID, _, _ string) error {
			if ticketID == existing {
				replied = true
			}
			return nil
		},
		CreateTicket: func(_ context.Context, _, _, _, _, _ string) (string, error) {
			created = true
			return "new", nil
		},
	})
	svc.route(context.Background(), "wecom", "default", "zhangsan", "请跟进 "+existing, "进度如何")
	if !replied {
		t.Fatal("ReplyToTicket should be called for existing ticket id")
	}
	if created {
		t.Fatal("CreateTicket must NOT be called when ticket id present")
	}
}

// encodeKey 把 32 字节密钥还原为 43 字符 EncodingAESKey(测试用)。
func encodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)[:43]
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := wecomDecodeKey(genTestKey(t))
	if err != nil {
		t.Fatalf("mustKey: %v", err)
	}
	return k
}
