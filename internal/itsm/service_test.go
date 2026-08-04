package itsm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/itsm/catalog"
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
	return doMethod(t, s, http.MethodPost, path, body)
}

func callPut(t *testing.T, s *Service, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doMethod(t, s, http.MethodPut, path, body)
}

func doMethod(t *testing.T, s *Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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

// 子任务: 创建/列表/状态流转(完成时间戳)/删除
func TestSubtaskLifecycle(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)

	// 创建两个子任务
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks", `{"title":"备份数据库","assignee":"alice","created_by":"u1"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create subtask: status %d body=%s", w.Code, w.Body.String())
	}
	var st1 model.Subtask
	if err := json.Unmarshal(w.Body.Bytes(), &st1); err != nil {
		t.Fatal(err)
	}
	if st1.Status != model.SubtaskPending {
		t.Fatalf("new subtask should be pending, got %s", st1.Status)
	}
	callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks", `{"title":"重启应用","assignee":"bob","created_by":"u1"}`)

	// 列表
	{
		rw := doMethod(t, s, http.MethodGet, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks", "")
		if rw.Code != http.StatusOK {
			t.Fatalf("list subtasks: status %d", rw.Code)
		}
		var list []*model.Subtask
		if err := json.Unmarshal(rw.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 {
			t.Fatalf("want 2 subtasks, got %d", len(list))
		}
	}

	// 状态流转 done → 记录完成时间戳
	rw := doMethod(t, s, http.MethodPut, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks/"+strconv.FormatInt(st1.ID, 10), `{"status":"done"}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("update subtask: status %d body=%s", rw.Code, rw.Body.String())
	}
	var stUpdated model.Subtask
	if err := json.Unmarshal(rw.Body.Bytes(), &stUpdated); err != nil {
		t.Fatal(err)
	}
	if stUpdated.Status != model.SubtaskDone || stUpdated.CompletedAt == nil {
		t.Fatalf("done subtask should record completed_at, got %+v", stUpdated)
	}

	// 删除
	rw = doMethod(t, s, http.MethodDelete, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks/"+strconv.FormatInt(st1.ID, 10), "")
	if rw.Code != http.StatusOK {
		t.Fatalf("delete subtask: status %d body=%s", rw.Code, rw.Body.String())
	}
	// 删除后只剩 1 条
	{
		rw := doMethod(t, s, http.MethodGet, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks", "")
		var list []*model.Subtask
		json.Unmarshal(rw.Body.Bytes(), &list)
		if len(list) != 1 {
			t.Fatalf("after delete want 1 subtask, got %d", len(list))
		}
	}
}

// --- SN 补全: 撤回/重开/挂起恢复/催办/关联/通用更新 ---

func TestTicketWithdrawAndReopenLifecycle(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)

	// 撤回: draft → withdrawn(终态)
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/withdraw", `{"withdrawn_by":"admin","reason":"不需要了"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", w.Code, w.Body.String())
	}
	var wd model.Ticket
	json.Unmarshal(w.Body.Bytes(), &wd)
	if wd.Status != model.StatusWithdrawn {
		t.Fatalf("withdraw status = %s", wd.Status)
	}
	// 终态不能再撤回
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/withdraw", `{"withdrawn_by":"admin"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("withdraw terminal should 409, got %d", w.Code)
	}

	// 另一张: submit → complete → close → reopen → execute
	tk2 := createTicket(t, s, model.RiskLow)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk2.ID+"/submit", `{"submitted_by":"admin"}`)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk2.ID+"/execute", `{"executed_by":"admin"}`)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk2.ID+"/complete", `{"completed_by":"admin"}`)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk2.ID+"/close", `{"closed_by":"admin","satisfaction":5}`)

	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk2.ID+"/reopen", `{"reopened_by":"admin","reason":"用户反馈未解决"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("reopen: %d %s", w.Code, w.Body.String())
	}
	var ro model.Ticket
	json.Unmarshal(w.Body.Bytes(), &ro)
	if ro.Status != model.StatusReopened {
		t.Fatalf("reopen status = %s", ro.Status)
	}
	// reopened → executing 可继续
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk2.ID+"/execute", `{"executed_by":"admin"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("execute after reopen: %d %s", w.Code, w.Body.String())
	}
}

func TestTicketHoldResumeRemindRelateUpdate(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)

	// 挂起(标记, 状态不变)
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/hold", `{"hold_by":"admin","reason":"等依赖系统恢复","hold_until_at":"2026-08-10T00:00:00Z"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("hold: %d %s", w.Code, w.Body.String())
	}
	var held model.Ticket
	json.Unmarshal(w.Body.Bytes(), &held)
	if !held.OnHold || held.HoldReason == "" {
		t.Fatalf("hold not set: %+v", held)
	}
	if held.Status != model.StatusDraft {
		t.Fatalf("hold should not change status: %s", held.Status)
	}
	// 恢复
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/resume", `{"resumed_by":"admin"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	var resumed model.Ticket
	json.Unmarshal(w.Body.Bytes(), &resumed)
	if resumed.OnHold {
		t.Fatal("resume should clear on_hold")
	}

	// 催办(200)
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/remind", `{"reminded_by":"admin","message":"请尽快处理"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("remind: %d %s", w.Code, w.Body.String())
	}

	// 关联(双向)
	tk2 := createTicket(t, s, model.RiskMedium)
	w = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/relate", `{"related_id":"`+tk2.ID+`","relate_by":"admin"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("relate: %d %s", w.Code, w.Body.String())
	}
	var rel model.Ticket
	json.Unmarshal(w.Body.Bytes(), &rel)
	if len(rel.RelatedTicketIDs) != 1 || rel.RelatedTicketIDs[0] != tk2.ID {
		t.Fatalf("relate not bidirectional: %+v", rel.RelatedTicketIDs)
	}
	tk2b, _ := s.store.Get(context.Background(), tk2.ID, "default")
	if len(tk2b.RelatedTicketIDs) != 1 || tk2b.RelatedTicketIDs[0] != tk.ID {
		t.Fatalf("reverse relate missing: %+v", tk2b.RelatedTicketIDs)
	}

	// 通用更新(PUT)
	w = callPut(t, s, "/api/v1/itsm/tickets/"+tk.ID, `{"title":"更新后的标题","assignee":"ops"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update ticket: %d %s", w.Code, w.Body.String())
	}
	var upd model.Ticket
	json.Unmarshal(w.Body.Bytes(), &upd)
	if upd.Title != "更新后的标题" || upd.Assignee != "ops" {
		t.Fatalf("update not applied: %+v", upd)
	}
}

// --- SN 补全: 主单关闭级联关闭子任务 ---

func TestSubtaskCascadeOnClose(t *testing.T) {
	s := newTestService()
	tk := createTicket(t, s, model.RiskLow)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/submit", `{"submitted_by":"admin"}`)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/execute", `{"executed_by":"admin"}`)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/complete", `{"completed_by":"admin"}`)
	callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/subtasks", `{"title":"子任务1","created_by":"admin","sync_fields":["risk_level"]}`)

	// 关闭主单 → 子任务级联 done
	w := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/close", `{"closed_by":"admin"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("close: %d %s", w.Code, w.Body.String())
	}
	subs, _ := s.store.ListSubtasks(context.Background(), tk.ID, "default")
	if len(subs) != 1 || subs[0].Status != model.SubtaskDone {
		t.Fatalf("subtask not cascaded: %+v", subs)
	}
	// 字段同步: meta 含 risk_level
	if subs[0].Meta == nil || subs[0].Meta["risk_level"] != "low" {
		t.Fatalf("sync_fields not copied: %+v", subs[0].Meta)
	}
}

// --- 产品化: 服务目录申请套用流程/表单 + 统计扩展 ---

type fakeCatalogGetter struct{ item *catalog.ServiceItem }

func (f *fakeCatalogGetter) Get(ctx context.Context, id, tenantID string) (*catalog.ServiceItem, error) {
	if f.item == nil {
		return nil, nil
	}
	it := *f.item
	return &it, nil
}

func TestCreateTicketFromCatalogAppliesScopeAndProcess(t *testing.T) {
	s := newTestService()
	s.processStore = &fakeProc{}
	cg := &fakeCatalogGetter{item: &catalog.ServiceItem{
		ID: "cat-1", Name: "服务器重启", ProcessID: "proc-9", Enabled: true,
	}}
	s.SetCatalogStore(cg)

	tk, err := s.CreateTicketFromCatalog(context.Background(), "cat-1", "default", model.CreateTicketRequest{
		Title:     "重启 prod-db-01",
		CreatedBy: "portal",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 套用目录配置: 表单 scope = catalog:cat-1, 流程 = proc-9, 来源标记 catalog
	stored, _ := s.store.Get(context.Background(), tk.ID, "default")
	if stored.ProcessID != "proc-9" {
		t.Fatalf("process not applied: %s", stored.ProcessID)
	}
	if stored.Labels["source"] != "catalog" {
		t.Fatalf("source label missing: %+v", stored.Labels)
	}
}

func TestFulfillEndpointDisabledItem(t *testing.T) {
	s := newTestService()
	s.SetCatalogStore(&fakeCatalogGetter{item: &catalog.ServiceItem{ID: "cat-2", Enabled: false}})
	w := callPost(t, s, "/api/v1/itsm/catalog/cat-2/fulfill", `{"title":"x","created_by":"portal"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled catalog should 404, got %d", w.Code)
	}
}

func TestGetStatsExtendedDimensions(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	mk := func(title, by, byType string, created time.Time) *model.Ticket {
		return &model.Ticket{
			ID: uuid.New().String(), TenantID: "default", Title: title,
			RiskLevel: model.RiskLow, Status: model.StatusExecuting,
			CreatedBy: by, CreatedByType: byType,
			CreatedAt: created, UpdatedAt: created,
		}
	}
	st.Create(ctx, mk("t1", "alice", "manual", now.AddDate(0, 0, -2)))
	st.Create(ctx, mk("t2", "alice", "manual", now.AddDate(0, 0, -2)))
	st.Create(ctx, mk("t3", "bob", "mail", now.AddDate(0, 0, -1)))

	stats, err := st.GetStats(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if stats.ByCreator["alice"] != 2 || stats.ByCreator["bob"] != 1 {
		t.Fatalf("by_creator = %+v", stats.ByCreator)
	}
	if stats.ByChannel["manual"] != 2 || stats.ByChannel["mail"] != 1 {
		t.Fatalf("by_channel = %+v", stats.ByChannel)
	}
	// MemoryStore.Create 会覆盖 CreatedAt 为 now, 故趋势至少 1 个数据点即可
	if len(stats.Trends) < 1 {
		t.Fatalf("trends empty")
	}
}
