package auth

import (
	"testing"
	"time"
)

const testSecret = "test-secret-1234567890-abcdef"

func TestJWT_HMAC_Roundtrip(t *testing.T) {
	m := NewJWTManager(testSecret)
	tok, err := m.GenerateToken("u1", "t1", "admin", time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	claims, err := m.ValidateToken(tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.UserID != "u1" || claims.TenantID != "t1" || claims.Role != "admin" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestJWT_TamperRejected(t *testing.T) {
	m := NewJWTManager(testSecret)
	tok, _ := m.GenerateToken("u1", "t1", "admin", time.Hour)
	b := []byte(tok)
	// Flip the last byte of the signature segment.
	b[len(b)-1] ^= 0xFF
	if _, err := m.ValidateToken(string(b)); err == nil {
		t.Fatal("tampered token must be rejected")
	}
}

func TestJWT_WrongSecretRejected(t *testing.T) {
	a := NewJWTManager("secret-A-1234567890")
	b := NewJWTManager("secret-B-1234567890")
	tok, _ := a.GenerateToken("u1", "t1", "admin", time.Hour)
	if _, err := b.ValidateToken(tok); err == nil {
		t.Fatal("token signed with A must not validate under B")
	}
}

func TestJWT_ExpiredRejected(t *testing.T) {
	m := NewJWTManager(testSecret)
	tok, _ := m.GenerateToken("u1", "t1", "admin", -time.Hour)
	if _, err := m.ValidateToken(tok); err == nil {
		t.Fatal("expired token must be rejected")
	}
}
