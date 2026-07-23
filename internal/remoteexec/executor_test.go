package remoteexec

import (
	"strings"
	"testing"
	"time"
)

// TestCacheKeyNoPlaintext verifies the connection-pool cache key never
// contains the raw password (P0 fix: cacheKey hashes the password).
func TestCacheKeyNoPlaintext(t *testing.T) {
	p := NewPool()
	key := p.cacheKey("host1", 22, Credential{User: "alice", Password: "s3cr3t-pw"})
	if strings.Contains(key, "s3cr3t-pw") {
		t.Fatalf("cache key leaks plaintext password: %q", key)
	}
	if !strings.Contains(key, "alice") {
		t.Fatalf("cache key should include the user: %q", key)
	}
}

// TestPoolExhausted verifies the connection cap guard rejects new
// dials once the pool is at its limit (report issue 6.1).
func TestPoolExhausted(t *testing.T) {
	p := NewPool()
	p.maxConns = 0 // force the guard to trip immediately
	_, err := p.GetClient("127.0.0.1", 22, Credential{User: "u", Password: "p"}, time.Second)
	if err == nil {
		t.Fatal("expected pool exhausted error, got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("expected 'exhausted' in error, got %v", err)
	}
}
