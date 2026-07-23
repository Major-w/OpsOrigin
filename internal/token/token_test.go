package token

import (
	"errors"
	"testing"
	"time"
)

func TestGenerateVerifyRoundTrip(t *testing.T) {
	secret := "super-secret-shared-key"
	tok, err := Generate(secret, "file-123", "agent-9", 5*time.Minute)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	claims, err := Verify(secret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.FileID != "file-123" || claims.AgentID != "agent-9" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	tok, _ := Generate("right-secret", "f", "a", time.Minute)
	if _, err := Verify("wrong-secret", tok); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong secret must be invalid, got %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	tok, _ := Generate("secret", "f", "a", time.Minute)
	// flip last char of signature
	b := []byte(tok)
	b[len(b)-1] ^= 0xFF
	if _, err := Verify("secret", string(b)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered token must be invalid, got %v", err)
	}
}

func TestTTLCappedAtTenMinutes(t *testing.T) {
	tok, _ := Generate("secret", "f", "a", 24*time.Hour)
	claims, err := Verify("secret", tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Exp-claims.Iat > 600 {
		t.Fatalf("TTL exceeded 10min cap: %d", claims.Exp-claims.Iat)
	}
}

func TestExpired(t *testing.T) {
	// 用 1 秒 ttl 生成,睡眠越过过期再校验。
	tok, _ := Generate("secret", "f", "a", 1*time.Second)
	time.Sleep(1100 * time.Millisecond)
	if _, err := Verify("secret", tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired token must return ErrExpired, got %v", err)
	}
}
