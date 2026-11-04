package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
		process_snapshot JSONB DEFAULT '[]',
		started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		finished_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	s.db.Exec(`ALTER TABLE itsm_process_instances ADD COLUMN IF NOT EXISTS process_snapshot JSONB DEFAULT '[]'`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_process_instances_ticket ON itsm_process_instances(ticket_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_process_instances_tenant ON itsm_process_instances(tenant_id)`)
}

func (s *PGStore) CreateInstance(ctx context.Context, inst *ProcessInstance) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
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
	snapshotJSON, _ := json.Marshal(inst.ProcessSnapshot)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO itsm_process_instances(id,process_id,ticket_id,tenant_id,current_step,status,step_results,process_snapshot,started_at,finished_at,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		inst.ID, inst.ProcessID, inst.TicketID, inst.TenantID, inst.CurrentStep, inst.Status,
		resultsJSON, snapshotJSON, inst.StartedAt, inst.FinishedAt, inst.CreatedAt, inst.UpdatedAt)
	return err
}

func (s *PGStore) ListRunningInstances(ctx context.Context) ([]*ProcessInstance, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,process_id,ticket_id,tenant_id,current_step,status,step_results,process_snapshot,started_at,finished_at,created_at,updated_at
		 FROM itsm_process_instances WHERE status='running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProcessInstance
	for rows.Next() {
		var resultsJSON, snapshotJSON []byte
		inst := &ProcessInstance{}
		if err := rows.Scan(&inst.ID, &inst.ProcessID, &inst.TicketID, &inst.TenantID, &inst.CurrentStep, &inst.Status,
			&resultsJSON, &snapshotJSON, &inst.StartedAt, &inst.FinishedAt, &inst.CreatedAt, &inst.UpdatedAt); err != nil {
			return nil, err
		}
		if len(resultsJSON) > 0 {
			json.Unmarshal(resultsJSON, &inst.StepResults)
		}
		if len(snapshotJSON) > 0 {
			json.Unmarshal(snapshotJSON, &inst.ProcessSnapshot)
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

func (s *PGStore) GetInstanceByTicket(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error) {
	if s.db == nil {
		return nil, nil
	}
	var resultsJSON, snapshotJSON []byte
	inst := &ProcessInstance{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,process_id,ticket_id,tenant_id,current_step,status,step_results,process_snapshot,started_at,finished_at,created_at,updated_at
		 FROM itsm_process_instances WHERE ticket_id=$1 AND tenant_id=$2 ORDER BY created_at DESC LIMIT 1`,
		ticketID, tenantID).
		Scan(&inst.ID, &inst.ProcessID, &inst.TicketID, &inst.TenantID, &inst.CurrentStep, &inst.Status,
			&resultsJSON, &snapshotJSON, &inst.StartedAt, &inst.FinishedAt, &inst.CreatedAt, &inst.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(resultsJSON) > 0 {
		json.Unmarshal(resultsJSON, &inst.StepResults)
	}
	if len(snapshotJSON) > 0 {
		json.Unmarshal(snapshotJSON, &inst.ProcessSnapshot)
	}
	return inst, nil
}

func (s *PGStore) UpdateInstance(ctx context.Context, inst *ProcessInstance) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	inst.UpdatedAt = time.Now()
	resultsJSON, _ := json.Marshal(inst.StepResults)
	snapshotJSON, _ := json.Marshal(inst.ProcessSnapshot)
	res, err := s.db.ExecContext(ctx,
		`UPDATE itsm_process_instances SET current_step=$1,status=$2,step_results=$3,process_snapshot=$4,finished_at=$5,updated_at=$6
		 WHERE id=$7 AND tenant_id=$8`,
		inst.CurrentStep, inst.Status, resultsJSON, snapshotJSON, inst.FinishedAt, inst.UpdatedAt, inst.ID, inst.TenantID)
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
		ProcessID:       processID,
		TicketID:        ticketID,
		TenantID:        tenantID,
		Status:          "running",
		StepResults:     map[string]any{},
		ProcessSnapshot: append([]Step(nil), proc.Steps...), // 版本快照: 在途实例按创建时定义推进
		StartedAt:       time.Now(),
	}
	var landing *Step
	if start := proc.StartStep(); start != nil {
		inst.CurrentStep = start.ID
		landing = proc.AdvancerExpr(start.ID, "success", nil)
	}
	// 首个落点若是 service 步骤, 自动执行脚本并继续推进, 直到停在人工/定时步骤或流程结束。
	if landing != nil {
		varCtx := s.buildVarCtx(ctx, inst, nil)
		landing, err = s.runAutomated(ctx, proc, inst, landing, varCtx)
		if err != nil {
			slog.Warn("itsm process auto-run on start failed", "process_id", processID, "error", err)
		}
		// start 直连 join 的防御场景: 登记到达并判定(start 视为第一个到达者)。
		if landing != nil && landing.Type == StepJoin {
			arrived := markJoinArrival(inst, landing.ID, "start")
			if joinPass(proc, inst, landing, arrived) {
				if nxt := passJoin(proc, inst, landing, varCtx); nxt != nil {
					landing, err = s.runAutomated(ctx, proc, inst, nxt, varCtx)
					if err != nil {
						slog.Warn("itsm process auto-run on start failed", "process_id", processID, "error", err)
					}
				} else {
					landing = nil
				}
			}
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
	// 版本隔离: 实例存在定义快照时用快照(在途实例按创建时定义推进), 否则用当前定义。
	proc, err := resolveInstanceProcess(ctx, s.store, inst, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if proc == nil {
		return inst, nil, fmt.Errorf("process %s not found", inst.ProcessID)
	}
	if inst.StepResults == nil {
		inst.StepResults = map[string]any{}
	}
	// 记录当前步骤结果; 保留 timer 的 started_at 与子流程暂停上下文(subflow_next/subflow_id),
	// 避免被后续操作覆盖导致定时器/子流程无法恢复。
	stepRec := map[string]any{
		"condition": condition,
		"output":    output,
		"at":        time.Now(),
	}
	if existing, ok := inst.StepResults[inst.CurrentStep].(map[string]any); ok {
		for _, keep := range []string{"started_at", "subflow_next", "subflow_id"} {
			if v, ok := existing[keep]; ok {
				stepRec[keep] = v
			}
		}
	}
	inst.StepResults[inst.CurrentStep] = stepRec
	// 变量上下文: 在记录完当前步骤结果后构建, 使 steps.<step_id> 已包含本次 output ——
	// 网关条件与下游动作/脚本均可引用 ${steps.<id>.output} / ${ticket.*} / ${custom_fields.*}。
	varCtx := s.buildVarCtx(ctx, inst, output)
	next := proc.AdvancerExpr(inst.CurrentStep, condition, varCtx)
	if next == nil {
		inst.Status = "completed"
		now := time.Now()
		inst.FinishedAt = &now
	} else {
		landing, rerr := s.runAutomated(ctx, proc, inst, next, varCtx)
		if rerr != nil {
			slog.Warn("itsm process auto-run failed", "ticket_id", ticketID, "error", rerr)
		}
		// 汇聚网关: 分支推进到达 join —— 登记到达(以刚完成的步骤为到达者)并判定放行。
		if landing != nil && landing.Type == StepJoin {
			arrived := markJoinArrival(inst, landing.ID, inst.CurrentStep)
			if joinPass(proc, inst, landing, arrived) {
				if nxt := passJoin(proc, inst, landing, varCtx); nxt != nil {
					landing, rerr = s.runAutomated(ctx, proc, inst, nxt, varCtx)
					if rerr != nil {
						slog.Warn("itsm process auto-run failed", "ticket_id", ticketID, "error", rerr)
					}
				} else {
					landing = nil // join 后无下游, 流程结束
				}
			}
			// 未放行: 工单停在 join(状态映射 executing), 等最后一个上游分支汇入时放行。
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

// resolveInstanceProcess 返回实例应使用的流程定义: 有快照(版本隔离)用快照, 否则取当前定义。
func resolveInstanceProcess(ctx context.Context, store Store, inst *ProcessInstance, tenantID string) (*Process, error) {
	if len(inst.ProcessSnapshot) > 0 {
		return &Process{ID: inst.ProcessID, TenantID: inst.TenantID, Steps: inst.ProcessSnapshot}, nil
	}
	return store.Get(ctx, inst.ProcessID, tenantID)
}

// maxAutoSteps 防止异常流程定义导致自动执行死循环。
const maxAutoSteps = 20

// maxSubflowDepth 子流程递归深度上限(防环)。
const maxSubflowDepth = 4

// runAutomated 从落点步骤开始自动执行自动化步骤, 直到停在需要人工/定时的步骤、
// 流程结束或无可达下游。支持: 脚本执行(service)、定时器记录(timer)、并行分支(parallel)、子流程(subflow)。
func (s *Service) runAutomated(ctx context.Context, proc *Process, inst *ProcessInstance, first *Step, exprCtx map[string]any) (*Step, error) {
	return s.runAutomatedDepth(ctx, proc, inst, first, exprCtx, 0)
}

func (s *Service) runAutomatedDepth(ctx context.Context, proc *Process, inst *ProcessInstance, first *Step, exprCtx map[string]any, depth int) (*Step, error) {
	if depth > maxSubflowDepth {
		return nil, fmt.Errorf("process %s: subflow nesting exceeds %d", proc.ID, maxSubflowDepth)
	}
	// 变量上下文惰性兜底: 直接调用(如测试/StartInstance 未构建时)也能在同轮自动链内
	// 形成「上游输出 → 下游参数」的变量链路。
	if exprCtx == nil {
		exprCtx = map[string]any{}
	}
	if _, ok := exprCtx["steps"]; !ok {
		exprCtx["steps"] = map[string]any{}
	}
	cur := first
	for i := 0; i < maxAutoSteps; i++ {
		if cur == nil || cur.Type == StepEnd {
			return cur, nil
		}
		if cur.Type == StepUser {
			recordStepEntered(inst, cur, time.Now())
			return cur, nil // 等待人工审批
		}
		if cur.Type == StepDispatch {
			// 派单节点: auto 模式解析受派人→落库→继续; manual 模式停等人工指派(assign 后续跑)。
			return s.runDispatchStep(ctx, proc, inst, cur, exprCtx)
		}
		if cur.Type == StepTimer {
			recordStepEntered(inst, cur, time.Now())
			return cur, nil // 等待定时器到期(由 StartTimerChecker 推进)
		}
		if cur.Type == StepWait {
			// 等待条件节点: 挂起轮询(StartTimerChecker 每轮重评条件, 成立后自动推进)。
			recordStepEntered(inst, cur, time.Now())
			return cur, nil
		}
		if cur.Type == StepService && cur.ActionType != "" && s.actionExec != nil {
			// 动作库: 执行预置动作(http/notify/create_ticket 等), 结果写 StepResults, 失败走 failure 分支。
			// 参数先在引擎侧按变量上下文替换(支持 steps.<id>.output 等点路径), 再交动作执行器
			// (执行器内部 tpl 仍兼容 custom_fields.xxx, 已替换的占位符不会二次展开)。
			params := substMap(cur.Inputs, exprCtx)
			if params == nil {
				params = map[string]any{}
			}
			// 失败重试（对齐 SN REST Step 内置重试）: 按策略重试后仍失败才走 failure 分支。
			attempts := serviceRetryAttempts(cur)
			retryDelay := serviceRetryDelay(cur)
			var result map[string]any
			var execErr error
			used := 0
			for i := 0; i < attempts; i++ {
				used = i + 1
				if i > 0 {
					slog.Warn("itsm process action retry", "action", cur.ActionType, "ticket_id", inst.TicketID, "attempt", i+1, "of", attempts)
					sleepRetryInterval(ctx, retryDelay)
				}
				result, execErr = s.actionExec.ExecuteAction(ctx, cur.ActionType, params, inst.TenantID, inst.TicketID, exprCtx)
				if execErr == nil {
					break
				}
			}
			rec := map[string]any{"action": cur.ActionType, "at": time.Now(), "attempts": used}
			if execErr != nil {
				rec["error"] = execErr.Error()
				slog.Warn("itsm process action failed", "action", cur.ActionType, "ticket_id", inst.TicketID, "error", execErr)
			} else {
				rec["result"] = result
			}
			inst.StepResults[cur.ID] = rec
			cond := "success"
			if execErr != nil {
				cond = "failure"
			}
			injectStepResult(exprCtx, cur.ID, rec, cond)
			nxt := proc.AdvancerExpr(cur.ID, cond, exprCtx)
			if nxt == nil {
				return cur, nil
			}
			cur = nxt
			continue
		}
		if cur.Type == StepService && cur.ScriptID != "" && s.scriptExec != nil {
			// 自动节点变量链路: Inputs 中的 ${ticket.*} / ${custom_fields.*} / ${steps.<id>.output}
			// 在下发前按变量上下文替换, 使脚本参数能引用工单字段与上游节点产出。
			params := condStringMap(substMap(cur.Inputs, exprCtx))
			runAs, _ := cur.Inputs["run_as"].(string)
			attempts := serviceRetryAttempts(cur)
			retryDelay := serviceRetryDelay(cur)
			var output string
			var exitCode int
			var execErr error
			used := 0
			for i := 0; i < attempts; i++ {
				used = i + 1
				if i > 0 {
					slog.Warn("itsm process script retry", "script_id", cur.ScriptID, "ticket_id", inst.TicketID, "attempt", i+1, "of", attempts)
					sleepRetryInterval(ctx, retryDelay)
				}
				output, exitCode, execErr = s.scriptExec.Execute(cur.ScriptID, params, 0, runAs, "", inst.TenantID)
				if execErr == nil && exitCode == 0 {
					break
				}
			}
			inst.StepResults[cur.ID] = map[string]any{
				"output":    output,
				"exit_code": exitCode,
				"attempts":  used,
				"at":        time.Now(),
			}
			cond := "success"
			if execErr != nil || exitCode != 0 {
				cond = "failure"
				slog.Warn("itsm process script failed", "script_id", cur.ScriptID, "exit_code", exitCode, "error", execErr)
			}
			injectStepResult(exprCtx, cur.ID, inst.StepResults[cur.ID].(map[string]any), cond)
			nxt := proc.AdvancerExpr(cur.ID, cond, exprCtx)
			if nxt == nil {
				return cur, nil
			}
			cur = nxt
			continue
		}
		if cur.Type == StepParallel {
			landing, err := s.runParallel(ctx, proc, inst, cur, exprCtx, depth)
			if err != nil {
				return nil, err
			}
			if landing == nil {
				return nil, nil // 全部分支自动执行完, 流程结束
			}
			cur = landing
			continue
		}
		// 汇聚网关: 不可自动执行, 停在此处由上层(parallel 分支/Advance)登记到达并判定放行。
		if cur.Type == StepJoin {
			return cur, nil
		}
		if cur.Type == StepSubflow && cur.SubflowID != "" {
			landing, err := s.runSubflow(ctx, proc, inst, cur, exprCtx, depth)
			if err != nil {
				return nil, err
			}
			if landing == nil {
				// 子流程完成 → 父流程沿 success 继续
				nxt := proc.AdvancerExpr(cur.ID, "success", exprCtx)
				if nxt == nil {
					return cur, nil
				}
				cur = nxt
				continue
			}
			// 子流程暂停(停在人工/定时等待点): 返回父流程 subflow 步骤本身, 由下次 Advance 恢复
			return cur, nil
		}
		// 通知节点: 标题/正文经变量上下文替换后真实下发(此前为直通空转)。
		// 通知失败不阻断业务流转 —— 记录错误后仍按 success 继续(与 OLA 升级通知同策略)。
		if cur.Type == StepNotify && s.notifier != nil {
			event := cur.NotifyChannel
			if event == "" {
				event = "workflow.notify"
			}
			rec := map[string]any{"notify": event, "at": time.Now()}
			if err := s.notifier(ctx, inst.TenantID, event,
				subst(cur.NotifyTitle, exprCtx), subst(cur.NotifyBody, exprCtx)); err != nil {
				rec["error"] = err.Error()
				slog.Warn("itsm process notify step failed", "ticket_id", inst.TicketID, "step", cur.ID, "error", err)
			} else {
				rec["sent"] = true
			}
			inst.StepResults[cur.ID] = rec
			nxt := proc.AdvancerExpr(cur.ID, "success", exprCtx)
			if nxt == nil {
				return cur, nil
			}
			cur = nxt
			continue
		}
		// 无脚本的 service / notify / gateway / start 等: 按 success 直通
		// (gateway 的下游若为表达式条件, AdvancerExpr 会基于表单字段上下文求值选路)
		nxt := proc.AdvancerExpr(cur.ID, "success", exprCtx)
		if nxt == nil {
			return cur, nil
		}
		cur = nxt
	}
	return cur, fmt.Errorf("process %s: automated steps exceeded %d", proc.ID, maxAutoSteps)
}

// runParallel 执行并行网关: 枚举其所有下游分支端点, 顺序执行每个分支的自动步骤链。
// 务实语义(文档标注): 自动分支按顺序执行; 分支内遇人工/定时等待点时整个并行暂停于该等待点,
// 工单状态映射为等待点; 全部分支自动执行完(无等待点)则流程结束。
// 分支链终止于汇聚网关(join)或直接终结(end/无下游)时, 以分支端点 ID 登记到达;
// 全部分支走完后对 join 做 all/any 判定, 放行则从 join 下游继续。
// 返回 nil 表示并行完成, 否则返回等待点步骤。
func (s *Service) runParallel(ctx context.Context, proc *Process, inst *ProcessInstance, par *Step, exprCtx map[string]any, depth int) (*Step, error) {
	endpoints := proc.FanOut(par.ID)
	if len(endpoints) == 0 {
		nxt := proc.AdvancerExpr(par.ID, "success", exprCtx)
		if nxt == nil {
			return nil, nil
		}
		return nxt, nil
	}
	var firstWait *Step
	var joinHit *Step
	for _, ep := range endpoints {
		landing, err := s.runAutomatedDepth(ctx, proc, inst, ep, exprCtx, depth+1)
		if err != nil {
			return nil, err
		}
		if landing != nil && landing.Type == StepJoin {
			markJoinArrival(inst, landing.ID, ep.ID)
			if joinHit == nil {
				joinHit = landing
			}
			continue
		}
		if landing == nil {
			continue // 分支到 end / 无下游
		}
		if firstWait == nil && (landing.Type == StepUser || landing.Type == StepTimer) {
			firstWait = landing
		}
	}
	// 分支全部走完: 对已到达的 join 判定放行(all/any)。
	if joinHit != nil {
		arrived, _ := toStringSlice(inst.StepResults[joinHit.ID].(map[string]any)["arrived_by"])
		if joinPass(proc, inst, joinHit, arrived) {
			nxt := passJoin(proc, inst, joinHit, exprCtx)
			if nxt == nil {
				return nil, nil // join 后无下游, 流程结束
			}
			return s.runAutomatedDepth(ctx, proc, inst, nxt, exprCtx, depth+1)
		}
		// all 模式下仍有上游未汇入(停在其他等待点): 保持既有暂停语义, join 登记保留,
		// 待该分支 Advance 推进到 join 时补登记并放行。
	}
	if firstWait != nil {
		return firstWait, nil
	}
	// 全部分支自动执行完且无等待点 → 并行完成
	return nil, nil
}

// runSubflow 执行子流程步骤: 加载子流程定义并从其 start(或上次暂停点)开始自动执行。
// 子流程停在人工/定时等待点时, 记录 subflow_next 到父流程该步骤的 StepResults 并返回暂停步骤;
// 子流程完成(到 end)时返回 nil, 父流程继续。
// 显式传参(Step.SubflowParams): 启动/恢复时基于父上下文求值并注入子流程上下文 params 命名空间;
// For Each(Step.ForEach): 非空时对列表逐项执行子流程(仅自动子流程, 见 runSubflowForEach)。
func (s *Service) runSubflow(ctx context.Context, proc *Process, inst *ProcessInstance, sf *Step, exprCtx map[string]any, depth int) (*Step, error) {
	subID := sf.SubflowID
	rec, _ := inst.StepResults[sf.ID].(map[string]any)

	var sub *Process
	var err error
	// 先尝试恢复已暂停的子流程(存在 subflow_next 且能加载定义)
	if rec != nil {
		if nid, ok := rec["subflow_next"].(string); ok && nid != "" {
			if sub, err = s.loadSubflow(ctx, subID, inst.TenantID); err != nil {
				return nil, err
			}
			if sub != nil {
				if startFrom := sub.StepByID(nid); startFrom != nil {
					return s.resumeSubflow(ctx, sub, inst, sf, startFrom, s.subflowContext(sf, exprCtx), depth)
				}
			}
		}
	}
	if sub == nil {
		if sub, err = s.loadSubflow(ctx, subID, inst.TenantID); err != nil {
			return nil, err
		}
	}
	if sub == nil {
		return nil, fmt.Errorf("subflow %s not found", subID)
	}
	start := sub.StartStep()
	if start == nil {
		return nil, fmt.Errorf("subflow %s has no start step", subID)
	}
	// For Each 循环: 对列表逐项执行子流程(恢复场景不会走到这里——暂停分支已提前 return)
	if sf.ForEach != "" {
		return s.runSubflowForEach(ctx, sub, inst, sf, exprCtx, depth)
	}
	return s.resumeSubflow(ctx, sub, inst, sf, start, s.subflowContext(sf, exprCtx), depth)
}

// resolveRefValue 求值子流程传参/For Each 表达式: 纯 "${path}" 引用形式按点路径取**原始值**
// (保留数组/对象形态, 供 For Each 逐项遍历与结构化传参); 混排或其它形态回退 ${} 字符串替换。
func resolveRefValue(expr string, exprCtx map[string]any) any {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "${") && strings.HasSuffix(expr, "}") {
		inner := expr[2 : len(expr)-1]
		if !strings.Contains(inner, "${") && !strings.Contains(inner, "}") {
			return lookupVar(exprCtx, inner)
		}
	}
	return substValue(expr, exprCtx)
}

// subflowParams 求值子流程传参表: 每个值经父上下文变量求值(纯引用保留原始形态);
// 未配置返回 nil。
func (s *Service) subflowParams(sf *Step, exprCtx map[string]any) map[string]any {
	if len(sf.SubflowParams) == 0 {
		return nil
	}
	out := make(map[string]any, len(sf.SubflowParams))
	for k, v := range sf.SubflowParams {
		out[k] = resolveRefValue(v, exprCtx)
	}
	return out
}

// subflowContext 构建子流程执行上下文: 复制父上下文(未配置传参=全量继承, 旧行为),
// 配置了显式传参时叠加 params 命名空间(父变量仍可引用, 叠加式兼容)。
func (s *Service) subflowContext(sf *Step, exprCtx map[string]any) map[string]any {
	params := s.subflowParams(sf, exprCtx)
	if len(params) == 0 {
		return exprCtx // 未配置: 直接沿用父上下文, 与旧行为完全一致
	}
	sub := make(map[string]any, len(exprCtx)+1)
	for k, v := range exprCtx {
		sub[k] = v
	}
	sub["params"] = params
	return sub
}

// runSubflowForEach For Each 循环: 对 ForEach 表达式求值出的列表逐项执行子流程,
// 每项注入 item / item_index 到子流程上下文(在显式传参构建的上下文之上)。
// 仅支持全自动子流程: 任一迭代停在人工/定时等待点即报错(已完成迭代的结果保留在
// StepResults[sf.ID].for_each 中供审计), 不做跨迭代暂停恢复(务实取舍)。
// 空列表视为循环零次, 直接完成并放行父流程。
func (s *Service) runSubflowForEach(ctx context.Context, sub *Process, inst *ProcessInstance, sf *Step, exprCtx map[string]any, depth int) (*Step, error) {
	raw := resolveRefValue(sf.ForEach, exprCtx)
	items := forEachItems(raw)
	results := make([]any, 0, len(items))
	for i, item := range items {
		subCtx := s.subflowContext(sf, exprCtx)
		subCtx["item"] = item
		subCtx["item_index"] = i
		start := sub.StartStep()
		if start == nil {
			return nil, fmt.Errorf("subflow %s has no start step", sf.SubflowID)
		}
		landing, err := s.resumeSubflow(ctx, sub, inst, sf, start, subCtx, depth+1)
		if err != nil {
			return nil, err
		}
		if landing != nil {
			return nil, fmt.Errorf("子流程 %s 含人工/定时等待点, 不支持 For Each 循环(仅支持全自动子流程)", sf.SubflowID)
		}
		results = append(results, map[string]any{"item": item, "item_index": i, "status": "done"})
	}
	rec, _ := inst.StepResults[sf.ID].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	rec["for_each"] = results
	rec["items_total"] = len(items)
	rec["at"] = time.Now().Format(time.RFC3339)
	inst.StepResults[sf.ID] = rec
	return nil, nil
}

// forEachItems 把 ForEach 求值结果归一化为列表:
// 数组直接用; 逗号分隔字符串拆分为列表; 标量包成单元素列表(防御)。
func forEachItems(raw any) []any {
	switch t := raw.(type) {
	case nil:
		return nil
	case string:
		var out []any
		for _, p := range strings.Split(t, ",") {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		return t
	case []string:
		out := make([]any, 0, len(t))
		for _, s := range t {
			out = append(out, s)
		}
		return out
	default:
		return []any{raw}
	}
}

// resumeSubflow 从子流程的指定步骤开始执行其自动链, 处理暂停/完成标记。
// 若 startFrom 是等待点(user/timer), 表示上次暂停处已由本次工单操作推进, 沿 success 继续。
func (s *Service) resumeSubflow(ctx context.Context, sub *Process, inst *ProcessInstance, sf *Step, startFrom *Step, exprCtx map[string]any, depth int) (*Step, error) {
	if startFrom.Type == StepUser || startFrom.Type == StepTimer {
		nxt := sub.AdvancerExpr(startFrom.ID, "success", exprCtx)
		if nxt == nil {
			// 子流程在此等待点后无下游 → 视为完成
			if rec, ok := inst.StepResults[sf.ID].(map[string]any); ok {
				delete(rec, "subflow_next")
				inst.StepResults[sf.ID] = rec
			}
			return nil, nil
		}
		startFrom = nxt
	}
	landing, err := s.runAutomatedDepth(ctx, sub, inst, startFrom, exprCtx, depth+1)
	if err != nil {
		return nil, err
	}
	if landing == nil || landing.Type == StepEnd {
		// 子流程完成: 清除暂停标记
		if rec, ok := inst.StepResults[sf.ID].(map[string]any); ok {
			delete(rec, "subflow_next")
			inst.StepResults[sf.ID] = rec
		}
		return nil, nil
	}
	// 子流程停在等待点: 记录恢复点
	rec, _ := inst.StepResults[sf.ID].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	rec["subflow_id"] = sf.SubflowID
	rec["subflow_next"] = landing.ID
	rec["at"] = time.Now().Format(time.RFC3339)
	inst.StepResults[sf.ID] = rec
	return landing, nil
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

// serviceRetryAttempts 归一化 service 步骤的执行次数（1=不重试, 上限 6=1 次执行+5 次重试）。
func serviceRetryAttempts(st *Step) int {
	if st == nil || st.RetryCount <= 0 {
		return 1
	}
	if st.RetryCount > 5 {
		return 6
	}
	return st.RetryCount + 1
}

// serviceRetryDelay 返回重试间隔（配置为空或无法解析时立即重试）。
func serviceRetryDelay(st *Step) time.Duration {
	if st == nil || st.RetryInterval == "" {
		return 0
	}
	if d, ok := parseStepDuration(st.RetryInterval); ok && d > 0 {
		return d
	}
	return 0
}

// sleepRetryInterval 在重试之间等待, ctx 取消时提前返回。
func sleepRetryInterval(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// GetInstance 返回工单当前流程实例（供前端/AI 观察流程进度）。
func (s *Service) GetInstance(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	return s.store.GetInstanceByTicket(ctx, ticketID, tenantID)
}
