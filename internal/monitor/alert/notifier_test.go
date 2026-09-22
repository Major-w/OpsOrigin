package alert

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

func TestDetectWebhookFormat(t *testing.T) {
	cases := map[string]string{
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc": "wecom",
		"https://oapi.dingtalk.com/robot/send?access_token=xyz":    "dingtalk",
		"https://example.com/hooks/alert":                          "json",
	}
	for u, want := range cases {
		if got := DetectWebhookFormat(u); got != want {
			t.Fatalf("DetectWebhookFormat(%q)=%q want %q", u, got, want)
		}
	}
}

func TestMaskWebhookURL(t *testing.T) {
	got := MaskWebhookURL("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret")
	if got != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=***" {
		t.Fatalf("MaskWebhookURL=%q", got)
	}
	if MaskWebhookURL("") != "" {
		t.Fatalf("empty should mask to empty")
	}
}

func TestWebhookNotifier_Delivers(t *testing.T) {
	var gotBody map[string]any
	gotCh := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST got %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		gotCh <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL)
	n.Notify(&model.AlertEvent{
		TenantID:    "t1",
		SourceCIID:  "ci-1",
		RuleName:    "cpu_high",
		Severity:    model.AlertCritical,
		Title:       "CPU 过高",
		Description: "cpu > 90%",
		TriggeredAt: time.Now(),
		Labels:      map[string]string{"host": "h1"},
	})

	select {
	case <-gotCh:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not delivered within timeout")
	}
	if gotBody["rule_name"] != "cpu_high" {
		t.Fatalf("unexpected payload: %v", gotBody)
	}
	if gotBody["severity"] != "critical" {
		t.Fatalf("unexpected severity: %v", gotBody)
	}
}

func TestWebhookNotifier_EmptyURLNoop(t *testing.T) {
	// 空 URL 不应 panic 或阻塞(异步 no-op 直接返回)。
	n := NewWebhookNotifier("")
	n.Notify(&model.AlertEvent{Title: "x", TriggeredAt: time.Now()})
}

func TestBuildWebhookPayload_Wecom(t *testing.T) {
	p := buildWebhookPayload(&model.AlertEvent{
		Title: "t", Severity: model.AlertWarning, SourceCIID: "c", RuleName: "r", TriggeredAt: time.Now(),
	}, "wecom")
	m := p.(map[string]any)
	if m["msgtype"] != "text" {
		t.Fatalf("wecom should be text msgtype, got %v", m["msgtype"])
	}
}
