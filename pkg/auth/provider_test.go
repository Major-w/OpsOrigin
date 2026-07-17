package auth

import (
	"testing"
	"time"
)

func TestGenerateAndValidateToken(t *testing.T) {
	p := NewProvider("test-secret-key-for-unit-testing", nil, nil)

	token, err := p.GenerateToken("user-01", "tenant-01", "admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	claims, err := p.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}

	if claims.UserID != "user-01" {
		t.Errorf("expected user-01, got %s", claims.UserID)
	}
	if claims.TenantID != "tenant-01" {
		t.Errorf("expected tenant-01, got %s", claims.TenantID)
	}
	if claims.Role != "admin" {
		t.Errorf("expected admin, got %s", claims.Role)
	}
}

func TestInvalidToken(t *testing.T) {
	p := NewProvider("secret", nil, nil)

	_, err := p.ValidateToken("invalid-token")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestTamperedToken(t *testing.T) {
	p := NewProvider("secret", nil, nil)
	token, err := p.GenerateToken("user-01", "tenant-01", "admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	tampered := token + "x"
	_, err = p.ValidateToken(tampered)
	if err == nil {
		t.Error("expected error for tampered token")
	}
}

func TestExpiredToken(t *testing.T) {
	p := NewProvider("secret", nil, nil)
	token, err := p.GenerateToken("user-01", "tenant-01", "admin", -1*time.Second)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	_, err = p.ValidateToken(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestLogin(t *testing.T) {
	p := NewProvider("secret", nil, nil)
	token, err := p.Login("admin", "password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	claims, err := p.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate login token: %v", err)
	}
	if claims.UserID != "admin" {
		t.Errorf("expected admin, got %s", claims.UserID)
	}
}
