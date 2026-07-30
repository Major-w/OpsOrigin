package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
		landing = proc.Advancer(start.ID, "success")
		if landing == nil {
			landing = proc.Advancer(start.ID, "always")
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
// 记录当前步骤结果并落到下一步；返回新落点步骤（nil 表示流程结束）。
// 若工单未绑定流程实例，返回 (nil,nil,nil) 静默跳过——使无流程工单不受影响。
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
	next := proc.Advancer(inst.CurrentStep, condition)
	if next == nil {
		next = proc.Advancer(inst.CurrentStep, "always")
	}
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
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, nil, err
	}
	return inst, next, nil
}

// GetInstance 返回工单当前流程实例（供前端/AI 观察流程进度）。
func (s *Service) GetInstance(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	return s.store.GetInstanceByTicket(ctx, ticketID, tenantID)
}
