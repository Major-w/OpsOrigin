package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// migrateInstances 建立流程实例表（工单绑定流程后自动创建实例，驱动流转）。
func (s *PGStore) migrateInstances() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_process_instances (
		id TEXT PRIMARY KEY,
		process_id TEXT NOT NULL,
		ticket_id TEXT NOT NULL,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		current_step TEXT DEFAULT '',
		status TEXT DEFAULT 'running',
		step_results JSONB DEFAULT '{}',
		started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		finished_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_process_instances_ticket ON itsm_process_instances(ticket_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_process_instances_tenant ON itsm_process_instances(tenant_id)`)
}

func (s *PGStore) CreateInstance(ctx context.Context, inst *ProcessInstance) error {
	if inst.ID == "" {
		inst.ID = uuid.NewString()
	}
	now := time.Now()
	if inst.StartedAt.IsZero() {
		inst.StartedAt = now
	}
	inst.CreatedAt = now
	inst.UpdatedAt = now
	if inst.TenantID == "" {
		inst.TenantID = "default"
	}
	if inst.Status == "" {
		inst.Status = "running"
	}
	resultsJSON, _ := json.Marshal(inst.StepResults)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO itsm_process_instances(id,process_id,ticket_id,tenant_id,current_step,status,step_results,started_at,finished_at,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		inst.ID, inst.ProcessID, inst.TicketID, inst.TenantID, inst.CurrentStep, inst.Status,
		resultsJSON, inst.StartedAt, inst.FinishedAt, inst.CreatedAt, inst.UpdatedAt)
	return err
}

func (s *PGStore) GetInstanceByTicket(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error) {
	var resultsJSON []byte
	inst := &ProcessInstance{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,process_id,ticket_id,tenant_id,current_step,status,step_results,started_at,finished_at,created_at,updated_at
		 FROM itsm_process_instances WHERE ticket_id=$1 AND tenant_id=$2 ORDER BY created_at DESC LIMIT 1`,
		ticketID, tenantID).
		Scan(&inst.ID, &inst.ProcessID, &inst.TicketID, &inst.TenantID, &inst.CurrentStep, &inst.Status,
			&resultsJSON, &inst.StartedAt, &inst.FinishedAt, &inst.CreatedAt, &inst.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(resultsJSON) > 0 {
		json.Unmarshal(resultsJSON, &inst.StepResults)
	}
	return inst, nil
}

func (s *PGStore) UpdateInstance(ctx context.Context, inst *ProcessInstance) error {
	inst.UpdatedAt = time.Now()
	resultsJSON, _ := json.Marshal(inst.StepResults)
	res, err := s.db.ExecContext(ctx,
		`UPDATE itsm_process_instances SET current_step=$1,status=$2,step_results=$3,finished_at=$4,updated_at=$5
		 WHERE id=$6 AND tenant_id=$7`,
		inst.CurrentStep, inst.Status, resultsJSON, inst.FinishedAt, inst.UpdatedAt, inst.ID, inst.TenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("process instance not found")
	}
	return nil
}

// StartInstance 为工单创建流程实例并推进到首个真实步骤（跳过 start 节点）。
// 返回实例与落点步骤——调用方（itsm）据落点步骤类型映射工单初始状态。
func (s *Service) StartInstance(ctx context.Context, tenantID, processID, ticketID string) (*ProcessInstance, *Step, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	proc, err := s.store.Get(ctx, processID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if proc == nil {
		return nil, nil, fmt.Errorf("process %s not found", processID)
	}
	if !proc.Enabled {
		return nil, nil, fmt.Errorf("process %s is disabled", processID)
	}
	inst := &ProcessInstance{
		ProcessID:   processID,
		TicketID:    ticketID,
		TenantID:    tenantID,
		Status:      "running",
		StepResults: map[string]any{},
		StartedAt:   time.Now(),
	}
	var landing *Step
	if start := proc.StartStep(); start != nil {
		inst.CurrentStep = start.ID
		landing = proc.AdvancerExpr(start.ID, "success", nil)
	}
	// 首个落点若是 service 步骤, 自动执行脚本并继续推进, 直到停在人工/定时步骤或流程结束。
	if landing != nil {
		landing, err = s.runAutomated(ctx, proc, inst, landing, nil)
		if err != nil {
			slog.Warn("itsm process auto-run on start failed", "process_id", processID, "error", err)
		}
	}
	if landing != nil {
		inst.CurrentStep = landing.ID
		if landing.Type == StepEnd {
			inst.Status = "completed"
			now := time.Now()
			inst.FinishedAt = &now
		}
	}
	if err := s.store.CreateInstance(ctx, inst); err != nil {
		return nil, nil, err
	}
	return inst, landing, nil
}

// Advance 按 condition(success/failure/always) 推进工单绑定的流程实例，
// 记录当前步骤结果并落到下一步；落点若是 service 步骤则自动执行脚本并继续推进。
// 返回新落点步骤（nil 表示流程结束）。若工单未绑定流程实例，返回 (nil,nil,nil) 静默跳过。
func (s *Service) Advance(ctx context.Context, tenantID, ticketID, condition string, output any) (*ProcessInstance, *Step, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	inst, err := s.store.GetInstanceByTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if inst == nil {
		return nil, nil, nil
	}
	if inst.Status == "completed" || inst.Status == "terminated" {
		return inst, nil, nil
	}
	proc, err := s.store.Get(ctx, inst.ProcessID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if proc == nil {
		return inst, nil, fmt.Errorf("process %s not found", inst.ProcessID)
	}
	if inst.StepResults == nil {
		inst.StepResults = map[string]any{}
	}
	inst.StepResults[inst.CurrentStep] = map[string]any{
		"condition": condition,
		"output":    output,
		"at":        time.Now(),
	}
	exprCtx := exprCtxFromOutput(output)
	next := proc.AdvancerExpr(inst.CurrentStep, condition, exprCtx)
	if next == nil {
		inst.Status = "completed"
		now := time.Now()
		inst.FinishedAt = &now
	} else {
		landing, rerr := s.runAutomated(ctx, proc, inst, next, exprCtx)
		if rerr != nil {
			slog.Warn("itsm process auto-run failed", "ticket_id", ticketID, "error", rerr)
		}
		next = landing
		if next == nil {
			inst.Status = "completed"
			now := time.Now()
			inst.FinishedAt = &now
		} else {
			inst.CurrentStep = next.ID
			if next.Type == StepEnd {
				inst.Status = "completed"
				now := time.Now()
				inst.FinishedAt = &now
			}
		}
	}
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, nil, err
	}
	return inst, next, nil
}

// exprCtxFromOutput 从 itsm 侧传入的 output(map) 中提取表达式求值上下文。
// 兼容旧调用(output 为 string): 返回 nil, 表达式分支退化为不命中。
func exprCtxFromOutput(output any) map[string]any {
	if m, ok := output.(map[string]any); ok {
		return m
	}
	return nil
}

// maxAutoSteps 防止异常流程定义导致自动执行死循环。
const maxAutoSteps = 20

// runAutomated 从落点步骤开始自动执行 service 步骤(调用脚本), 直到停在需要人工/定时的步骤、
// 流程结束或无可达下游。每个自动执行步骤的结果写入 inst.StepResults;
// 脚本失败时按 failure 条件选路(走失败/回滚分支), 无 failure 分支则 always 兜底。
func (s *Service) runAutomated(ctx context.Context, proc *Process, inst *ProcessInstance, first *Step, exprCtx map[string]any) (*Step, error) {
	cur := first
	for i := 0; i < maxAutoSteps; i++ {
		if cur == nil || cur.Type == StepEnd {
			return cur, nil
		}
		if cur.Type == StepUser || cur.Type == StepTimer {
			return cur, nil // 等待人工审批 / 定时器
		}
		if cur.Type == StepService && cur.ScriptID != "" && s.scriptExec != nil {
			params := condStringMap(cur.Inputs)
			runAs, _ := cur.Inputs["run_as"].(string)
			output, exitCode, execErr := s.scriptExec.Execute(cur.ScriptID, params, 0, runAs, "", inst.TenantID)
			inst.StepResults[cur.ID] = map[string]any{
				"output":    output,
				"exit_code": exitCode,
				"at":        time.Now(),
			}
			cond := "success"
			if execErr != nil || exitCode != 0 {
				cond = "failure"
				slog.Warn("itsm process script failed", "script_id", cur.ScriptID, "exit_code", exitCode, "error", execErr)
			}
			nxt := proc.AdvancerExpr(cur.ID, cond, exprCtx)
			if nxt == nil {
				return cur, nil
			}
			cur = nxt
			continue
		}
		// 无脚本的 service / notify / gateway / parallel / start 等: 按 success 直通
		// (gateway 的下游若为表达式条件, AdvancerExpr 会基于表单字段上下文求值选路)
		nxt := proc.AdvancerExpr(cur.ID, "success", exprCtx)
		if nxt == nil {
			return cur, nil
		}
		cur = nxt
	}
	return cur, fmt.Errorf("process %s: automated steps exceeded %d", proc.ID, maxAutoSteps)
}

// condStringMap 将步骤 inputs(any 值) 转换为脚本执行器所需的字符串参数表。
func condStringMap(inputs map[string]any) map[string]string {
	if len(inputs) == 0 {
		return nil
	}
	out := make(map[string]string, len(inputs))
	for k, v := range inputs {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// GetInstance 返回工单当前流程实例（供前端/AI 观察流程进度）。
func (s *Service) GetInstance(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	return s.store.GetInstanceByTicket(ctx, ticketID, tenantID)
}
