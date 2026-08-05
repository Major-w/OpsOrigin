package synthetic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

func TestProbeUnsupportedProtocol(t *testing.T) {
	p := NewProber()
	res := p.Probe(context.Background(), &model.ProbeTask{Protocol: model.ProbeProtocol("bogus"), Address: "127.0.0.1"})
	if res.Success {
		t.Fatal("expected failure for unsupported protocol")
	}
	if res.ErrorMessage == "" {
		t.Error("expected non-empty error message")
	}
}

func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	p := NewProber()
	res := p.Probe(context.Background(), &model.ProbeTask{
		Protocol: model.ProbeHTTP, Address: u.Hostname(), Port: port, Timeout: 3 * time.Second,
	})
	if !res.Success {
		t.Fatalf("expected HTTP probe success, got error: %s", res.ErrorMessage)
	}
}
