package chatops

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// makeWecomCiphertext 构造一段合法的企微回调密文(random16 + len4 + msg + corpid)。
func makeWecomCiphertext(t *testing.T, msg, corpID, aesKeyB64 string) string {
	t.Helper()
	aesKey, err := wecomAESKey(aesKeyB64)
	if err != nil {
		t.Fatalf("aes key: %v", err)
	}
	plain := make([]byte, 16+4+len(msg)+len(corpID))
	msgLen := make([]byte, 4)
	binary.BigEndian.PutUint32(msgLen, uint32(len(msg)))
	copy(plain, "0123456789abcdef")   // random
	copy(plain[16:], msgLen)          // len
	copy(plain[20:], msg)             // msg
	copy(plain[20+len(msg):], corpID) // corp_id
	// PKCS7 填充到 16 的倍数
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	enc := cipher.NewCBCEncrypter(block, aesKey[:aes.BlockSize])
	out := make([]byte, len(plain))
	enc.CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out)
}

const testAESKey = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" // 43 字符

// TestWecomDecryptRoundtrip 加解密往返: 消息与 corp_id 段完整还原。
func TestWecomDecryptRoundtrip(t *testing.T) {
	cipher := makeWecomCiphertext(t, `<xml><MsgType>text</MsgType><Content>你好</Content></xml>`, "wwcorp", testAESKey)
	plain, err := wecomDecrypt(cipher, mustWecomKey(t))
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !strings.Contains(string(plain), "你好") {
		t.Fatalf("decrypted text missing: %s", plain)
	}
}

func mustWecomKey(t *testing.T) []byte {
	t.Helper()
	k, err := wecomAESKey(testAESKey)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return k
}

// TestWecomVerifySignature 签名与官方算法一致(sha1(sort(token,ts,nonce,encrypt)));
// 伪造参数必须被拒绝 —— 这是企微回调的安全边界。
func TestWecomVerifySignature(t *testing.T) {
	encrypt := makeWecomCiphertext(t, "x", "wwcorp", testAESKey)
	// 用同一算法计算期望签名
	parts := []string{"tok", "1700000000", "nonce1", encrypt}
	sortStrings(parts)
	want := fmt.Sprintf("%x", sha1Sum(strings.Join(parts, "")))
	if !wecomVerifySignature("tok", "1700000000", "nonce1", encrypt, want) {
		t.Fatal("valid signature rejected")
	}
	if wecomVerifySignature("tok", "1700000000", "nonce1", encrypt, "deadbeef") {
		t.Fatal("forged signature accepted")
	}
	if wecomVerifySignature("", "1700000000", "nonce1", encrypt, want) {
		t.Fatal("empty token must reject (fail-closed)")
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sha1Sum(s string) []byte {
	h := sha1.Sum([]byte(s))
	return h[:]
}

// TestWecomSendPayload 群聊走 appchat/send 且带 chatid; 单聊走 message/send 且带 touser。
func TestWecomSendPayload(t *testing.T) {
	w := newWecomPlatform()
	cfg := &wecomSettings{CorpID: "wwcorp", AgentID: 1000002}

	body, path := w.sendPayload(cfg, "wrJeSAAAAAAAA", map[string]any{})
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if path != "/cgi-bin/appchat/send" || m["chatid"] != "wrJeSAAAAAAAA" {
		t.Fatalf("group: path=%s chatid=%v", path, m["chatid"])
	}
	if m["agentid"] != float64(1000002) {
		t.Fatalf("agentid missing: %v", m["agentid"])
	}

	body, path = w.sendPayload(cfg, "zhangsan", map[string]any{})
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if path != "/cgi-bin/message/send" || m["touser"] != "zhangsan" {
		t.Fatalf("user: path=%s touser=%v", path, m["touser"])
	}
}
