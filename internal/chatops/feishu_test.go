package chatops

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// encryptForTest 与飞书加密格式一致: AES-256-CBC(sha256(key)) + PKCS7 + base64。
func encryptForTest(t *testing.T, plaintext []byte, key string) string {
	t.Helper()
	h := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(h[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(append(iv, out...))
}

func TestDecryptEvent(t *testing.T) {
	f := newFeishuPlatform()
	plain := []byte(`{"schema":"2.0","header":{"event_id":"e1","token":"tk"}}`)
	enc := encryptForTest(t, plain, "my-encrypt-key")

	got, err := f.decryptEvent(enc, "my-encrypt-key")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var body feishuEventBody
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Header.EventID != "e1" || body.Header.Token != "tk" {
		t.Fatalf("roundtrip mismatch: %+v", body.Header)
	}
	// 错误 key 必须失败(而非返回垃圾)
	if _, err := f.decryptEvent(enc, "wrong-key"); err == nil {
		t.Fatal("wrong key should fail")
	}
}

func TestParseTextContentStripsMention(t *testing.T) {
	f := newFeishuPlatform()
	out, err := f.parseTextContent(`{"text":"@_user_1 查看主机 cpu"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "查看主机 cpu" {
		t.Fatalf("got %q", out)
	}
}

func TestDedupeCheck(t *testing.T) {
	f := newFeishuPlatform()
	if !f.dedupeCheck("evt-1") {
		t.Fatal("first sight should pass")
	}
	if f.dedupeCheck("evt-1") {
		t.Fatal("second sight should be deduped")
	}
	// 过期后可再次通过
	f.seenMu.Lock()
	f.seen["evt-1"] = time.Now().Add(-31 * time.Minute)
	f.seenMu.Unlock()
	if !f.dedupeCheck("evt-1") {
		t.Fatal("expired event should pass again")
	}
}

func TestVerifyToken(t *testing.T) {
	f := newFeishuPlatform()
	if f.verifyToken("tk", "") {
		t.Fatal("empty config token must reject")
	}
	if f.verifyToken("bad", "tk") {
		t.Fatal("mismatch must reject")
	}
	if !f.verifyToken("tk", "tk") {
		t.Fatal("match should pass")
	}
}

func TestURLVerification(t *testing.T) {
	f := newFeishuPlatform()
	ch := &Channel{Platform: PlatformFeishu, Settings: json.RawMessage(`{"verification_token":"tk"}`)}
	body := `{"type":"url_verification","token":"tk","challenge":"echo-me"}`
	req := httptest.NewRequest("POST", "/cb", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	if err := f.HandleWebhook(w, req, ch, nil); err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["challenge"] != "echo-me" {
		t.Fatalf("challenge mismatch: %v", out)
	}
	// token 错 → 401
	req2 := httptest.NewRequest("POST", "/cb", bytes.NewBufferString(`{"type":"url_verification","token":"bad","challenge":"x"}`))
	w2 := httptest.NewRecorder()
	_ = f.HandleWebhook(w2, req2, ch, nil)
	if w2.Code != 401 {
		t.Fatalf("expect 401, got %d", w2.Code)
	}
}
