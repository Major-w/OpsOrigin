package process

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/opsorigin/core/pkg/httputil"
)

func NewService(db *sql.DB) *Service {
	st := NewPGStore(db)
	return &Service{store: st, delegStore: st}
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
	// ticketVars 由 cmd/core 注入(itsm 实现): 构建节点变量上下文(ticket.* / custom_fields.*),
	// 是自动节点变量链路的工单侧数据源(nil=变量仅含步骤输出与当次 output)。
	ticketVars TicketVarProvider
	// ticketAssigner 由 cmd/core 注入(itsm 实现): 派单节点 auto 模式落库通道(nil=自动派单按 failure 处理)。
	ticketAssigner TicketAssigner
	// notifier 由 cmd/core 注入: OLA 节点超时升级通知(nil=不通知)。
	notifier func(ctx context.Context, tenantID, event, title, content string) error
	// approvalTimeoutHandler 由 cmd/core 注入(itsm 实现): 人工审批节点超时到期后的工单侧处置
	// (状态迁移/事件/通知/流程推进)。action 为 auto_approve / auto_reject; nil=超时仅记录不处置。
	approvalTimeoutHandler func(ctx context.Context, tenantID, ticketID, stepID, action string) error
	// advancedSyncer 由 cmd/core 注入(itsm 实现): 引擎侧自主推进(timer 到期/wait 条件成立)后,
	// 把落点步骤类型映射回工单状态并记事件广播。nil=不回写工单状态(历史行为)。
	advancedSyncer func(ctx context.Context, tenantID, ticketID string) error
	// subflowLoader 加载子流程定义(默认走 store.Get; 测试可注入 fake 避免依赖 DB)。
	subflowLoader func(ctx context.Context, id, tenantID string) (*Process, error)
	// delegStore 审批委托存储(由 cmd/core 装配 PGStore 自身; nil=委托能力关闭, 按原名册表决)。
	delegStore DelegationStore
	// calendarResolver 工作日历解析器(P3-2, main 注入 servicehours 实现): 审批
	// 催办/超时按业务时间计算。nil 或解析失败时回退自然时间。
	calendarResolver CalendarResolver
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

// SetApprovalTimeoutHandler 注入人工审批节点超时处置回调(itsm 实现):
// 到期自动通过/自动驳回时由其完成工单状态迁移、事件、通知与流程推进。
func (s *Service) SetApprovalTimeoutHandler(f func(ctx context.Context, tenantID, ticketID, stepID, action string) error) {
	s.approvalTimeoutHandler = f
}

// SetProcessAdvancedSyncer 注入引擎自主推进后的工单状态同步回调(itsm 实现)。
func (s *Service) SetProcessAdvancedSyncer(f func(ctx context.Context, tenantID, ticketID string) error) {
	s.advancedSyncer = f
}

// syncAfterEngineAdvance 引擎侧推进成功后回写工单状态(注入了 syncer 才生效)。
func (s *Service) syncAfterEngineAdvance(ctx context.Context, tenantID, ticketID string) {
	if s.advancedSyncer == nil {
		return
	}
	if err := s.advancedSyncer(ctx, tenantID, ticketID); err != nil {
		slog.Warn("itsm process: sync ticket status after engine advance failed", "ticket_id", ticketID, "error", err)
	}
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
// 表决方式由 Step.ApprovalType 决定（对齐 ServiceNow 5 种语义）:
//   - countersign(会签, 缺省): 名册内全部通过才达成;
//   - orsign(或签): 任一人通过即达成;
//   - responded_any: 全员都投过票且至少一人通过;
//   - percent: 通过人数 ≥ 名册的 threshold%（默认 50）;
//   - count: 通过人数 ≥ threshold 人（默认 1）。
//
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
	// 审批委托（对标 SN Delegate, 追加式）：在期代理人与原审批人一同进入表决名册
	roster = s.applyDelegations(ctx, tenantID, roster)
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
	return voteSettled(step.ApprovalType, step.ApprovalThreshold, roster, approvals), nil
}

// voteSettled 按表决语义判定投票名册是否已达成通过条件（纯函数, 便于单测）。
// roster 为最终生效名册, approvals 为当前投票快照（未投票的成员无条目）。
func voteSettled(approvalType string, threshold float64, roster []string, approvals map[string]any) bool {
	approvedN, votedN := 0, 0
	for _, a := range roster {
		if v, ok := approvals[a].(bool); ok {
			votedN++
			if v {
				approvedN++
			}
		}
	}
	total := len(roster)
	switch approvalType {
	case ApprovalOrSign:
		// 或签：任一人投了通过即达成
		return approvedN > 0
	case ApprovalRespondedAny:
		// 全员表态后任一通过: 名册内所有人都投过票, 且至少一人通过
		return votedN >= total && approvedN > 0
	case ApprovalPercent:
		// 比例表决: 通过人数/名册 ≥ threshold%（默认 50, 向上取整语义按浮点比较）
		if threshold <= 0 {
			threshold = 50
		}
		if threshold > 100 {
			threshold = 100
		}
		return total > 0 && float64(approvedN) >= float64(total)*threshold/100
	case ApprovalCount:
		// 数量表决: 通过人数 ≥ threshold（默认 1, 上限名册人数）
		if threshold <= 0 {
			threshold = 1
		}
		if threshold > float64(total) {
			threshold = float64(total)
		}
		return approvedN >= int(threshold)
	default:
		// 会签（缺省）：名册内全部投通过才达成
		return approvedN >= total
	}
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
	mux.HandleFunc("POST /api/v1/itsm/processes/{id}/versions/{version}/restore", s.handleRestoreVersion)
	mux.HandleFunc("POST /api/v1/itsm/processes/{id}/dry-run", s.handleDryRun)
	// 审批委托（代理人）管理
	mux.HandleFunc("GET /api/v1/itsm/approval-delegates", s.handleListDelegates)
	mux.HandleFunc("POST /api/v1/itsm/approval-delegates", s.handleCreateDelegate)
	mux.HandleFunc("DELETE /api/v1/itsm/approval-delegates/{id}", s.handleDeleteDelegate)
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
		httputil.WriteError(w, 404, "not found")
		return
	}
	xmlData, err := ToBPMNXML(p)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
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
		httputil.WriteError(w, 400, "invalid body")
		return
	}
	p, err := FromBPMNXML(string(body))
	if err != nil {
		httputil.WriteError(w, 400, err.Error())
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
		httputil.WriteError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process imported from bpmn", "id", p.ID, "name", p.Name, "steps", len(p.Steps))
	httputil.WriteJSON(w, 201, p)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	processes, err := s.store.List(r.Context(), tenant)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, processes)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var p Process
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteError(w, 400, "invalid body")
		return
	}
	if p.Name == "" {
		httputil.WriteError(w, 400, "name required")
		return
	}
	p.TenantID = r.Header.Get("X-Tenant-ID")
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if err := s.checkNumberRuleConflict(&p); err != nil {
		httputil.WriteError(w, 409, err.Error())
		return
	}
	if err := s.store.Create(context.Background(), &p); err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process created", "id", p.ID, "name", p.Name)
	httputil.WriteJSON(w, 201, p)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	p, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	if p == nil {
		httputil.WriteError(w, 404, "not found")
		return
	}
	httputil.WriteJSON(w, 200, p)
}

// globalDefaultNumberKey 全局默认工单号格式键（TK-YYYY-NNNN，store 层回退规则的归一化形态）。
const globalDefaultNumberKey = "TK|-|YYYY|4"

// numberRuleFormatKey 归一化编号格式键：前缀|分隔符|日期片段|位宽。
// 两个启用规则的流程该键相同 ⇒ 各自独立计序会生成字面完全相同的工单号，
// 必须在保存流程时拦截（序号 scope 不同不影响：格式相同即可能撞号）。
func numberRuleFormatKey(r *TicketNumberRule) string {
	if r == nil || !r.Enabled {
		return ""
	}
	return r.Prefix + "|" + r.EffectiveSeparator() + "|" + r.DateFormat + "|" + strconv.Itoa(r.EffectiveSeqWidth())
}

// checkNumberRuleConflict 校验流程编号规则唯一性：
// ① 不得与全局默认 TK-YYYY-NNNN 撞型；② 不得与其他启用规则的流程格式重复。
// 仅校验格式本身（计序按流程隔离，但格式相同仍会产出相同字面工单号）。
func (s *Service) checkNumberRuleConflict(p *Process) error {
	key := numberRuleFormatKey(p.NumberRule)
	if key == "" {
		return nil
	}
	if key == globalDefaultNumberKey {
		return fmt.Errorf("工单编号格式与全局默认 TK-YYYY-NNNN 相同，请修改前缀或日期/位宽组合")
	}
	list, err := s.store.List(context.Background(), p.TenantID)
	if err != nil {
		return err
	}
	for _, other := range list {
		if other == nil || other.ID == p.ID {
			continue
		}
		if numberRuleFormatKey(other.NumberRule) == key {
			return fmt.Errorf("工单编号格式与流程「%s」重复（同格式会生成相同工单号），请调整前缀或日期/位宽组合", other.Name)
		}
	}
	return nil
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	existing, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, 404, "not found")
		return
	}
	var p Process
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteError(w, 400, "invalid body")
		return
	}
	p.ID = id
	p.TenantID = tenant
	p.CreatedAt = existing.CreatedAt
	// 编号格式唯一性：与全局默认/其他启用规则的流程撞型会生成相同工单号，保存时拦截。
	if err := s.checkNumberRuleConflict(&p); err != nil {
		httputil.WriteError(w, 409, err.Error())
		return
	}
	// 覆盖写之前先把「旧定义」归档成历史版本快照(版本列表/差异对比的数据来源)。
	// 归档失败不阻断保存: 版本轨迹是审计增强, 不应让用户的编辑丢失。
	if snap := SnapshotOf(existing, actorOf(r)); snap != nil {
		if err := s.store.SaveVersion(r.Context(), snap); err != nil {
			slog.Warn("itsm process version snapshot failed", "id", id, "version", existing.Version, "err", err)
		}
	}
	if err := s.store.Update(r.Context(), &p); err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	slog.Info("itsm process updated", "id", id, "version", p.Version)
	httputil.WriteJSON(w, 200, p)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" {
		tenant = "default"
	}
	if err := s.store.Delete(r.Context(), id, tenant); err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}
