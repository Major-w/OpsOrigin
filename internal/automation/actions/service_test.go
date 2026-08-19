package actions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hook" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	e := New()
	out, err := e.ExecuteAction(context.Background(), ActionHTTP, map[string]any{
		"method": "POST",
		"url":    srv.URL + "/hook",
		"body":   `{"title":"${title}"}`,
	}, "default", "t1", map[string]any{"title": "发布 v1.2"})
	if err != nil {
		t.Fatal(err)
	}
	if out["status_code"] != 200 {
		t.Fatalf("status_code = %v", out["status_code"])
	}
	if !strings.Contains(out["body"].(string), `"ok":true`) {
		t.Fatalf("body = %v", out["body"])
	}
}

func TestHTTPActionTemplateReplace(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		got = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := New()
	_, err := e.ExecuteAction(context.Background(), ActionHTTP, map[string]any{
		"method": "POST",
		"url":    srv.URL,
		"body":   `{"env":"${custom_fields.env}","risk":"${risk_level}"}`,
	}, "default", "t1", map[string]any{
		"risk_level":    "high",
		"custom_fields": map[string]any{"env": "prod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"env":"prod"`) || !strings.Contains(got, `"risk":"high"`) {
		t.Fatalf("template not resolved: %s", got)
	}
}

func TestHTTPActionFailureStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	e := New()
	_, err := e.ExecuteAction(context.Background(), ActionHTTP, map[string]any{"url": srv.URL}, "default", "t1", nil)
	if err == nil {
		t.Fatal("expected error on 502")
	}
}

func TestNotifyAction(t *testing.T) {
	var gotEvent, gotTitle, gotContent string
	e := New()
	e.SetNotifier(func(ctx context.Context, tenantID, event, title, content string) error {
		gotEvent, gotTitle, gotContent = event, title, content
		return nil
	})
	out, err := e.ExecuteAction(context.Background(), ActionNotify, map[string]any{
		"event":   "ticket.sla_warning",
		"title":   "SLA 预警",
		"content": "工单 ${ticket_id} 即将超期",
	}, "default", "t1", map[string]any{"ticket_id": "TK-9"})
	if err != nil {
		t.Fatal(err)
	}
	if gotEvent != "ticket.sla_warning" || gotTitle != "SLA 预警" {
		t.Fatalf("notifier got %q / %q", gotEvent, gotTitle)
	}
	if !strings.Contains(gotContent, "TK-9") {
		t.Fatalf("template not resolved in content: %q", gotContent)
	}
	if out["sent"] != true {
		t.Fatalf("out = %v", out)
	}
}

func TestCreateTicketAction(t *testing.T) {
	e := New()
	e.SetCreateTicket(func(ctx context.Context, tenantID string, params map[string]any) (string, error) {
		if params["title"] != "发布" {
			t.Fatalf("title = %v", params["title"])
		}
		if params["app"] != "core" {
			t.Fatalf("app = %v", params["app"])
		}
		if tenantID != "default" {
			t.Fatalf("tenantID = %v", tenantID)
		}
		return "tk-123", nil
	})
	out, err := e.ExecuteAction(context.Background(), ActionCreateTicket, map[string]any{
		"title": "发布",
		"app":   "core",
	}, "default", "t1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["ticket_id"] != "tk-123" {
		t.Fatalf("ticket_id = %v", out["ticket_id"])
	}
}

func TestScriptAction(t *testing.T) {
	e := New()
	e.SetScriptExecutor(&fakeScript{})
	out, err := e.ExecuteAction(context.Background(), ActionScript, map[string]any{
		"script_id": "sc1",
		"arg":       "x",
	}, "default", "t1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["exit_code"] != 0 {
		t.Fatalf("exit_code = %v", out["exit_code"])
	}
}

func TestUnknownAction(t *testing.T) {
	e := New()
	if _, err := e.ExecuteAction(context.Background(), "explode", nil, "default", "t1", nil); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

type fakeScript struct{}

func (f *fakeScript) Execute(scriptID string, params map[string]string, timeout int, runAs string, agentID string, tenantID string) (string, int, error) {
	return "ok", 0, nil
}
