package itsm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/risk"
	"github.com/opsorigin/core/internal/itsm/store"
)

func newTestService() *Service {
	return &Service{
		store: store.NewMemoryStore(),
		risk:  risk.New(),
	}
}

func createTicket(t *testing.T, s *Service, riskLevel model.RiskLevel) model.Ticket {
	t.Helper()
	tk, err := s.CreateTicket(context.Background(), model.CreateTicketRequest{
		Title:         "测试工单",
		CreatedBy:     "u1",
		CreatedByType: "user",
		RiskLevel:     riskLevel,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	return *tk
}

func callPost(t *testing.T, s *Service, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func decodeTicket(t *testing.T, w *httptest.ResponseRecorder) model.Ticket {
	t.Helper()
	var tk model.Ticket
	if err := json.Unmarshal(w.Body.Bytes(), &tk); err != nil {
		t.Fatalf("decode ticket: %v body=%s", err, w.Body.String())
	}
	return tk
}

// P0-1 + P0-2: 低/中风险工单 draft → submit → executing → complete → succeeded → close → closed
func TestClosedLoopLowRisk(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)
	if tk.Status != model.StatusDraft {
		t.Fatalf("new low-risk ticket should be draft, got %s", tk.Status)
	}
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/submit", `{"submitted_by":"u1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("submit: status %d body=%s", w.Code, w.Body.String())
	}
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusExecuting {
		t.Fatalf("after submit: want executing got %s", tk.Status)
	}
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/complete", `{"completed_by":"u1"}`)
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusSucceeded {
		t.Fatalf("after complete: want succeeded got %s", tk.Status)
	}
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/close", `{"closed_by":"u1","satisfaction":5}`)
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusClosed {
		t.Fatalf("after close: want closed got %s", tk.Status)
	}
	if tk.Satisfaction != 5 {
		t.Fatalf("satisfaction want 5 got %d", tk.Satisfaction)
	}
	if !model.IsTerminal(tk.Status) {
		t.Fatalf("closed should be terminal")
	}
}

// P0-1 + P0-2: 高风险工单 draft →(自动) pending_review → approve → executing → complete → close
func TestClosedLoopHighRisk(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskHigh)
	if tk.Status != model.StatusPendingReview {
		t.Fatalf("high-risk new ticket should auto go pending_review, got %s", tk.Status)
	}
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/approve", `{"approved_by":"u1"}`)
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusApproved {
		t.Fatalf("after approve: want approved got %s", tk.Status)
	}
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/execute", `{"executed_by":"u1"}`)
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusExecuting {
		t.Fatalf("after execute: want executing got %s", tk.Status)
	}
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/complete", `{"completed_by":"u1"}`)
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusSucceeded {
		t.Fatalf("after complete: want succeeded got %s", tk.Status)
	}
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/close", `{"closed_by":"u1"}`)
	tk = decodeTicket(t, w)
	if tk.Status != model.StatusClosed {
		t.Fatalf("after close: want closed got %s", tk.Status)
	}
}

// draft 不能直接 approve(低风险应 submit 直接 executing), 校验入口不再死锁
func TestDraftCannotApproveDirectly(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/approve", `{"approved_by":"u1"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("approve from draft should be 409, got %d", w.Code)
	}
}

// P0-5 + P0-6: 指派不改变状态, 且创建/指派各触发一次通知
func TestAssignKeepsStatusAndNotifies(t *testing.T) {
	cap := &capturingNotifier{}
	s := newTestService()
	s.notify = cap
	tk := createTicket(t, s, model.RiskLow)
	if len(cap.events) != 1 || cap.events[0] != evTicketCreated {
		t.Fatalf("create should notify ticket.created, got %v", cap.events)
	}
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/submit", `{"submitted_by":"u1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("submit status %d", w.Code)
	}
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/assign", `{"assignee":"alice","assigned_by":"u1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("assign status %d body=%s", w.Code, w.Body.String())
	}
	tk2 := decodeTicket(t, w)
	if tk2.Assignee != "alice" {
		t.Fatalf("assignee want alice got %q", tk2.Assignee)
	}
	if tk2.Status != model.StatusExecuting {
		t.Fatalf("assign should not change status, got %s", tk2.Status)
	}
	if len(cap.events) == 0 || cap.events[len(cap.events)-1] != evTicketAssigned {
		t.Fatalf("assign should notify ticket.assigned, got %v", cap.events)
	}
}

// P0-3: 流程推进的落点步骤类型应回写工单状态(流程真正驱动工单)
func TestAdvanceProcessDrivesStatus(t *testing.T) {
	s := newTestService()
	s.processStore = &fakeProc{advanceNext: &process.Step{Type: process.StepEnd}}
	tk := createTicket(t, s, model.RiskLow)
	tk.ProcessID = "p1"
	ctx := context.Background()
	if err := s.store.Update(ctx, &tk); err != nil {
		t.Fatal(err)
	}
	s.advanceProcess(ctx, &tk, "success")
	if tk.Status != model.StatusSucceeded {
		t.Fatalf("advanceProcess should map StepEnd->succeeded, got %s", tk.Status)
	}
}

// P0-5 + P0-7: SLA 检查遍历全租户(此处 default), 违约时触发通知
func TestCheckSLAAllTenantsAndNotify(t *testing.T) {
	cap := &capturingNotifier{}
	s := newTestService()
	s.notify = cap
	tk := createTicket(t, s, model.RiskLow) // draft, 带未来 deadline
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	tk.DeadlineAt = &past
	if err := s.store.Update(ctx, &tk); err != nil {
		t.Fatal(err)
	}
	s.checkSLA(ctx)
	found := false
	for _, e := range cap.events {
		if e == evSLABreached {
			found = true
		}
	}
	if !found {
		t.Fatalf("SLA breach should notify ticket.sla_breached, events=%v", cap.events)
	}
}

// P0-4: ListActiveTicketsWithDeadline 必须保留 process_id(止血数据损坏)
func TestListActiveTicketsPreservesProcessID(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)
	tk.ProcessID = "proc-1"
	tk.Status = model.StatusExecuting
	now := time.Now().Add(time.Hour)
	tk.DeadlineAt = &now
	if err := s.store.Update(context.Background(), &tk); err != nil {
		t.Fatal(err)
	}
	list, err := s.store.ListActiveTicketsWithDeadline(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range list {
		if x.ID == tk.ID {
			found = true
			if x.ProcessID != "proc-1" {
				t.Fatalf("ProcessID lost across ListActiveTicketsWithDeadline: got %q", x.ProcessID)
			}
		}
	}
	if !found {
		t.Fatal("ticket not in active list")
	}
}

// --- fakes ---

type capturingNotifier struct {
	events []string
}

func (c *capturingNotifier) NotifyEvent(ctx context.Context, tenantID, event, title, content string) error {
	c.events = append(c.events, event)
	return nil
}

type fakeProc struct {
	advanceNext *process.Step
}

func (f *fakeProc) Get(ctx context.Context, id, tenantID string) (*process.Process, error) {
	return &process.Process{}, nil
}
func (f *fakeProc) StartInstance(ctx context.Context, tenantID, processID, ticketID string) (*process.ProcessInstance, *process.Step, error) {
	return &process.ProcessInstance{}, nil, nil
}
func (f *fakeProc) Advance(ctx context.Context, tenantID, ticketID, condition string, output any) (*process.ProcessInstance, *process.Step, error) {
	return &process.ProcessInstance{}, f.advanceNext, nil
}
func (f *fakeProc) GetInstance(ctx context.Context, ticketID, tenantID string) (*process.ProcessInstance, error) {
	return &process.ProcessInstance{}, nil
}
