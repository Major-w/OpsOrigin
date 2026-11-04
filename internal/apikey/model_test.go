package apikey

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestKeyStatus(t *testing.T) {
	now := time.Now()
	base := Key{ID: "k1", TenantID: "t1", UserID: "u1"}

	// 无任何时间约束 → active
	if s := base.Status(now); s != "active" {
		t.Errorf("plain key: expected active, got %s", s)
	}

	// 已吊销 → revoked
	rev := now
	rk := base
	rk.RevokedAt = &rev
	if s := rk.Status(now); s != "revoked" {
		t.Errorf("revoked: expected revoked, got %s", s)
	}

	// 已过期（过期时间在过去）→ expired
	exp := now.Add(-time.Hour)
	ek := base
	ek.ExpiresAt = &exp
	if s := ek.Status(now); s != "expired" {
		t.Errorf("expired: expected expired, got %s", s)
	}

	// 未来过期 → 仍 active
	future := now.Add(time.Hour)
	fk := base
	fk.ExpiresAt = &future
	if s := fk.Status(now); s != "active" {
		t.Errorf("future expiry: expected active, got %s", s)
	}

	// revoked 优先于 expired（同时设置两个时间，状态应为 revoked）
	both := base
	both.RevokedAt = &rev
	both.ExpiresAt = &future
	if s := both.Status(now); s != "revoked" {
		t.Errorf("revoked beats expired: expected revoked, got %s", s)
	}
}

func TestNewRawKey(t *testing.T) {
	k1, err := newRawKey()
	if err != nil {
		t.Fatalf("newRawKey: unexpected error: %v", err)
	}
	k2, err := newRawKey()
	if err != nil {
		t.Fatalf("newRawKey: unexpected error: %v", err)
	}

	if !strings.HasPrefix(k1, KeyPrefix) {
		t.Errorf("expected prefix %q, got %q", KeyPrefix, k1)
	}
	if k1 == k2 {
		t.Error("expected two distinct keys, got identical")
	}

	// 32 字节随机 → base64url（无填充）固定 43 字符
	randPart := strings.TrimPrefix(k1, KeyPrefix)
	if len(randPart) != 43 {
		t.Errorf("expected 43-char random part, got %d (%q)", len(randPart), randPart)
	}
	// 无填充字符 '='
	if strings.Contains(randPart, "=") {
		t.Errorf("expected unpadded base64url, got %q", randPart)
	}
}

func TestHashKey(t *testing.T) {
	raw := "osk_some_plain_text_key"
	got := hashKey(raw)
	sum := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("hashKey mismatch:\n got  %s\n want %s", got, want)
	}
	// 确定性
	if hashKey(raw) != got {
		t.Error("hashKey should be deterministic")
	}
	// 不同输入 → 不同摘要
	if hashKey("different") == got {
		t.Error("different inputs should hash differently")
	}
}

func TestDisplayPrefix(t *testing.T) {
	// 长明文：截取到 KeyPrefix(4) + 8 = 12 字符
	raw := KeyPrefix + strings.Repeat("a", 50)
	got := displayPrefix(raw)
	want := KeyPrefix + strings.Repeat("a", 8)
	if got != want {
		t.Errorf("displayPrefix long: got %q, want %q", got, want)
	}
	// 短明文：不足长度直接原样返回
	short := KeyPrefix + "abc"
	if displayPrefix(short) != short {
		t.Errorf("displayPrefix short: expected unchanged %q, got %q", short, displayPrefix(short))
	}
}
