package agentic

import (
	"testing"
	"time"
)

func TestTTLCacheSetGet(t *testing.T) {
	c := newTTLCache(time.Minute, 16)
	c.set("a", "v1")
	if v, ok := c.get("a"); !ok || v.(string) != "v1" {
		t.Fatalf("expected v1, got %v ok=%v", v, ok)
	}
	if _, ok := c.get("missing"); ok {
		t.Fatalf("expected cache miss for unknown key")
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	c := newTTLCache(time.Nanosecond, 16)
	c.set("a", "v1")
	time.Sleep(2 * time.Millisecond)
	if _, ok := c.get("a"); ok {
		t.Fatalf("expected entry to be expired")
	}
}

func TestTTLCacheEviction(t *testing.T) {
	c := newTTLCache(time.Minute, 2)
	c.set("a", 1)
	c.set("b", 2)
	c.set("c", 3) // 命中容量上限, 应驱逐至少一条
	if len(c.m) > 2 {
		t.Fatalf("expected <=2 keys after eviction, got %d", len(c.m))
	}
}

func TestCanonicalArgsKeyOrderIndependent(t *testing.T) {
	a := map[string]any{"x": "1", "y": "2"}
	b := map[string]any{"y": "2", "x": "1"}
	if canonicalArgsKey(a) != canonicalArgsKey(b) {
		t.Fatalf("param order must not affect key: %q vs %q", canonicalArgsKey(a), canonicalArgsKey(b))
	}
	if canonicalArgsKey(map[string]any{}) != "∅" {
		t.Fatalf("empty args should map to sentinel key, got %q", canonicalArgsKey(map[string]any{}))
	}
}

func TestSearchCacheKeyDistinguishesMode(t *testing.T) {
	ts := &MCPToolSet{}
	kVec := ts.searchCacheKey("t1", "q", "vector", 5, 500)
	kIL := ts.searchCacheKey("t1", "q", "ilike", 5, 500)
	if kVec == kIL {
		t.Fatalf("vector vs ilike must produce distinct keys")
	}
	if ts.searchCacheKey("t1", "q", "vector", 5, 500) != kVec {
		t.Fatalf("key must be stable for identical inputs")
	}
}
