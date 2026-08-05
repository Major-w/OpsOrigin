package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNotifyEmptyURL(t *testing.T) {
	c := New()
	if err := c.Notify(context.Background(), "", "ticket.created", "T1", "t1", "open", "high", "u1"); err != nil {
		t.Fatalf("empty url should be a no-op, got %v", err)
	}
}

func TestNotifySuccess(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := New()
	if err := c.Notify(context.Background(), srv.URL, "ticket.created", "T1", "t1", "open", "high", "u1"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("expected 1 delivery, got %d", hits)
	}
}

func TestNotifyRetryOn500(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Notify(ctx, srv.URL, "ticket.created", "T1", "t1", "open", "high", "u1"); err == nil {
		t.Fatal("expected error after retries")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 3 {
		t.Errorf("expected 3 attempts, got %d", hits)
	}
}
