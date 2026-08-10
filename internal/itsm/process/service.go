package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/opsorigin/core/pkg/httputil"
)

func NewService(db *sql.DB) *Service {
	return &Service{store: NewPGStore(db)}
}

// ScriptExecutor 执行脚本(与 automation/workflow 的接口签名一致, 由 automation/script.Service 实现,
// 避免 import 环)。StepService 步骤携带 ScriptID 时, 流程引擎调用它执行自动化脚本。
type ScriptExecutor interface {
	Execute(scriptID string, params map[string]string, timeout int, runAs string, agentID string, tenantID string) (output string, exitCode int, err error)
}

// ActionExecutor 执行预置动作(动作库)。与 ScriptExecutor 并列: StepService 步骤携带
// ActionType 时优先走动作执行器(如 http / notify / create_ticket), 由 cmd/core 注入实现,
// 避免 process 包反向依赖自动化/通知/ITSM 具体实现。
type ActionExecutor interface {
	// ExecuteAction 执行一个动作。params 为动作参数(action_params), exprCtx 为表达式上下文
	// (含 custom_fields 等表单字段), 供参数模板替换。返回结构化结果供 StepResults 记录。
	ExecuteAction(ctx context.Context, actionType string, params map[string]any, tenantID, ticketID string, exprCtx map[string]any) (map[string]any, error)
}

type Service struct {
	store Store
	Name  string
	// scriptExec 由 cmd/core 注入: StepService 步骤执行脚本(nil=跳过脚本, 服务步骤按 success 直通)。
	scriptExec ScriptExecutor
	// actionExec 由 cmd/core 注入: StepService 步骤执行预置动作(nil=未配置动作库, 动作步骤按 success 直通)。
	actionExec ActionExecutor
	// notifier 由 cmd/core 注入: OLA 节点超时升级通知(nil=不通知)。
	notifier func(ctx context.Context, tenantID, event, title, content string) error
	// subflowLoader 加载子流程定义(默认走 store.Get; 测试可注入 fake 避免依赖 DB)。
	subflowLoader func(ctx context.Context, id, tenantID string) (*Process, error)
}

// SetScriptExecutor 注入脚本执行器, 使流程的 service 步骤真正驱动自动化脚本执行。
func (s *Service) SetScriptExecutor(e ScriptExecutor) {
	s.scriptExec = e
}

// SetActionExecutor 注入动作执行器, 使流程的 service 步骤能执行预置动作(http/notify/create_ticket 等)。
func (s *Service) SetActionExecutor(e ActionExecutor) {
	s.actionExec = e
}

// SetNotifier 注入通知回调(OLA 节点超时升级用)。
func (s *Service) SetNotifier(f func(ctx context.Context, tenantID, event, title, content string) error) {
	s.notifier = f
}

// SetSubflowLoader 注入子流程定义加载器(默认 s.store.Get; 测试用)。
func (s *Service) SetSubflowLoader(f func(ctx context.Context, id, tenantID string) (*Process, error)) {
	s.subflowLoader = f
}

func (s *Service) loadSubflow(ctx context.Context, id, tenantID string) (*Process, error) {
	if s.subflowLoader != nil {
		return s.subflowLoader(ctx, id, tenantID)
	}
	return s.store.Get(ctx, id, tenantID)
}

// VoteStep 记录人工步骤的审批投票, 返回本步骤是否已达成通过条件。
// 表决方式由 Step.ApprovalType 决定: 会签(缺省)要求名册内全部通过, 或签只要任一人通过。
// extraApprovers 是调用方按 Step.ApproverField 从工单表单解析出的动态审批人, 与静态名单合并。
// 名册为空的步骤直接返回 allApproved=true(不影响原单审批路径)。
// 投票存于 StepResults[stepID].approvals = {approver: bool}。
func (s *Service) VoteStep(ctx context.Context, tenantID, ticketID, stepID, approver string, approved bool, extraApprovers []string) (bool, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	inst, err := s.store.GetInstanceByTicket(ctx, ticketID, tenantID)
	if err != nil {
		return false, err
	}
	if inst == nil {
		return false, fmt.Errorf("process instance not found for ticket %s", ticketID)
	}
	proc, err := s.store.Get(ctx, inst.ProcessID, tenantID)
	if err != nil {
		return false, err
	}
	if proc == nil {
		return false, fmt.Errorf("process %s not found", inst.ProcessID)
	}
	step := proc.StepByID(stepID)
	if step == nil {
		return false, fmt.Errorf("step %s not found", stepID)
	}
	roster := step.ApproverRoster(extraApprovers)
	if len(roster) == 0 {
		return true, nil // 未指定审批人的步骤不参与表决
	}
	if inst.StepResults == nil {
		inst.StepResults = map[string]any{}
	}
	rec, _ := inst.StepResults[stepID].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	approvals, _ := rec["approvals"].(map[string]any)
	if approvals == nil {
		approvals = map[string]any{}
	}
	approvals[approver] = approved
	rec["approvals"] = approvals
	inst.StepResults[stepID] = rec
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return false, err
	}
	if step.ApprovalType == ApprovalOrSign {
		// 或签：名册内任一人投了通过即达成，其余人无需再表态
		for _, a := range roster {
			if v, ok := approvals[a].(bool); ok && v {
				return true, nil
			}
		}
		return false, nil
	}
	// 会签（缺省）：名册内全部投通过才达成
	for _, a := range roster {
		v, ok := approvals[a].(bool)
		if !ok || !v {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) Get(ctx context.Context, id, tenantID string) (*Process, error) {
	return s.store.Get(ctx, id, tenantID)
}

func (s *Service) List(ctx context.Context, tenantID string) ([]*Process, error) {
	return s.store.List(ctx, tenantID)
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/processes", s.handleList)
	mux.HandleFunc("POST /api/v1/itsm/processes", s.handleCreate)
	mux.HandleFunc("GET /api/v1/itsm/processes/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/itsm/processes/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/itsm/processes/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/v1/itsm/processes/{id}/bpmn", s.handleExportBPMN)
	mux.HandleFunc("POST /api/v1/itsm/processes/import-bpmn", s.handleImportBPMN)
	mux.HandleFunc("GET /api/v1/itsm/processes/{id}/versions", s.handleListVersions)
	mux.HandleFunc("GET /api/v1/itsm/processes/{id}/versions/{version}", s.handleGetVersion)
}

// handleExportBPMN 导出流程为 BPMN 2.0 XML(兼容 Camunda/Flowable/bpmn-js 建模器)。
func (s *Service) handleExportBPMN(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	p, err := s.store.Get(r.Context(), id, tenant)
	if err != nil || p == nil {
		writeError(w, 404, "not found")
		return
	}
	xmlData, err := ToBPMNXML(p)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(200)
	w.Write([]byte(xmlData))
}

// handleImportBPMN 从 BPMN 2.0 XML 导入并创建流程定义。
func (s *Service) handleImportBPMN(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	p, err := FromBPMNXML(string(body))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p.TenantID = r.Header.Get("X-Tenant-ID")
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if p.Name == "" {
		p.Name = "导入流程"
	}
	if err := s.store.Create(context.Background(), p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process imported from bpmn", "id", p.ID, "name", p.Name, "steps", len(p.Steps))
	writeJSON(w, 201, p)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	processes, err := s.store.List(r.Context(), tenant)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, processes)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var p Process
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if p.Name == "" {
		writeError(w, 400, "name required")
		return
	}
	p.TenantID = r.Header.Get("X-Tenant-ID")
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if err := s.store.Create(context.Background(), &p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process created", "id", p.ID, "name", p.Name)
	writeJSON(w, 201, p)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	p, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if p == nil {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, p)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	existing, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if existing == nil {
		writeError(w, 404, "not found")
		return
	}
	var p Process
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	p.ID = id
	p.TenantID = tenant
	p.CreatedAt = existing.CreatedAt
	// 覆盖写之前先把「旧定义」归档成历史版本快照(版本列表/差异对比的数据来源)。
	// 归档失败不阻断保存: 版本轨迹是审计增强, 不应让用户的编辑丢失。
	if snap := SnapshotOf(existing, actorOf(r)); snap != nil {
		if err := s.store.SaveVersion(r.Context(), snap); err != nil {
			slog.Warn("itsm process version snapshot failed", "id", id, "version", existing.Version, "err", err)
		}
	}
	if err := s.store.Update(r.Context(), &p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process updated", "id", id, "version", p.Version)
	writeJSON(w, 200, p)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	if err := s.store.Delete(r.Context(), id, tenant); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
