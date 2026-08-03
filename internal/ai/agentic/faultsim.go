package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FaultScenario 预置故障场景库(L3-2 仿真验证飞轮): 描述一类可注入的故障,
// 供演练时选择"注入什么故障 -> 观察 Agent 是否识别并处置 -> 生成评估报告"。
//
// 本实现为仿真注入(记录故障事件 + 触发 Agent 巡检评估), 不真实破坏目标系统;
// 接入真实故障注入后端(如混沌工程平台)时, 用同一模型承载注入与恢复语义。
type FaultScenario struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`               // network_delay / network_loss / process_kill / disk_full / cpu_high
	Severity string         `json:"severity"`           // critical / major / minor
	Desc     string         `json:"description"`        // 故障描述
	Keywords []string       `json:"keywords,omitempty"` // 评估匹配关键词(Agent 输出包含即视为识别)
	Recovery string         `json:"recovery"`           // 恢复方式
	Params   map[string]any `json:"params,omitempty"`   // 注入参数说明(透传给注入后端)
}

// PresetFaultScenarios 预置故障场景库。
var PresetFaultScenarios = []*FaultScenario{
	{
		ID: "fault-network-delay", Name: "网络延迟激增", Type: "network_delay",
		Severity: "major", Desc: "目标主机到核心网关延迟从 2ms 飙升至 800ms, 应用响应变慢。",
		Keywords: []string{"延迟", "latency", "慢", "响应", "网络"},
		Recovery: "恢复路由策略 / 重启网络服务 / 切换备用链路",
		Params:   map[string]any{"delay_ms": 800, "duration": "10m"},
	},
	{
		ID: "fault-network-loss", Name: "网络丢包", Type: "network_loss",
		Severity: "critical", Desc: "目标主机 30% 丢包, 业务链路中断风险。",
		Keywords: []string{"丢包", "loss", "不可达", "超时", "网络"},
		Recovery: "排查交换机端口 / 检查光模块 / 切换链路",
		Params:   map[string]any{"loss_pct": 30, "duration": "10m"},
	},
	{
		ID: "fault-process-kill", Name: "关键进程崩溃", Type: "process_kill",
		Severity: "critical", Desc: "目标主机核心服务进程被终止, 服务不可用。",
		Keywords: []string{"进程", "process", "崩溃", "退出", "重启", "不可用"},
		Recovery: "拉起进程 / 检查 crash 日志 / 回滚版本",
		Params:   map[string]any{"process": "core", "signal": "SIGKILL"},
	},
	{
		ID: "fault-disk-full", Name: "磁盘空间耗尽", Type: "disk_full",
		Severity: "major", Desc: "目标主机磁盘使用率超过 95%, 日志/数据库写入受阻。",
		Keywords: []string{"磁盘", "disk", "空间", "使用率", "inode"},
		Recovery: "清理日志 / 扩容磁盘 / 归档旧数据",
		Params:   map[string]any{"usage_pct": 95},
	},
	{
		ID: "fault-cpu-high", Name: "CPU 过载", Type: "cpu_high",
		Severity: "major", Desc: "目标主机 CPU 使用率持续 90%+, 业务吞吐下降。",
		Keywords: []string{"cpu", "负载", "load", "过载", "资源"},
		Recovery: "定位高消耗进程 / 扩容 / 限流降级",
		Params:   map[string]any{"cpu_pct": 90, "duration": "15m"},
	},
}

// FaultInjectionStatus 演练状态常量。
const (
	FaultInjectionInjecting = "injecting"
	FaultInjectionRecovered = "recovered"
	FaultInjectionFailed    = "failed"
)

// FaultInjectionRun 一次故障仿真演练记录。
type FaultInjectionRun struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	ScenarioID    string     `json:"scenario_id"`
	ScenarioName  string     `json:"scenario_name"`
	TargetID      string     `json:"target_id,omitempty"`
	TargetName    string     `json:"target_name,omitempty"`
	InspectionID  string     `json:"inspection_id,omitempty"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	RecoveredAt   *time.Time `json:"recovered_at,omitempty"`
	AgentResponded bool      `json:"agent_responded"`
	AgentSummary  string     `json:"agent_summary,omitempty"`
	Findings      int        `json:"findings"`
	MatchedScenario bool     `json:"matched_scenario"`
	Score         int        `json:"score"`
	Report        string     `json:"report,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// ListFaultScenarios 返回预置故障场景库。
func (e *Engine) ListFaultScenarios() []*FaultScenario {
	out := make([]*FaultScenario, 0, len(PresetFaultScenarios))
	for _, s := range PresetFaultScenarios {
		out = append(out, s)
	}
	return out
}

// StartFaultInjection 启动一次故障仿真演练: 注入故障(仿真记录)并触发绑定的巡检任务,
// 评估 Agent 对故障的识别与处置响应, 生成演练报告与评分(飞轮闭环)。
// inspectionID 为空时跳过 Agent 评估(仅记录注入, 可后续手动 recover)。
func (e *Engine) StartFaultInjection(ctx context.Context, tenantID, scenarioID, targetID, targetName, inspectionID string) (*FaultInjectionRun, error) {
	var sc *FaultScenario
	for _, s := range PresetFaultScenarios {
		if s.ID == scenarioID {
			sc = s
			break
		}
	}
	if sc == nil {
		return nil, fmt.Errorf("fault scenario not found: %s", scenarioID)
	}
	run := &FaultInjectionRun{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		ScenarioID:   sc.ID,
		ScenarioName: sc.Name,
		TargetID:     targetID,
		TargetName:   targetName,
		InspectionID: inspectionID,
		Status:       FaultInjectionInjecting,
		StartedAt:    time.Now(),
	}
	e.mu.Lock()
	if e.faultInjections == nil {
		e.faultInjections = map[string]*FaultInjectionRun{}
	}
	e.faultInjections[run.ID] = run
	e.mu.Unlock()

	if inspectionID != "" {
		go e.executeFaultEvaluation(ctx, run.ID)
	}
	slog.Info("fault injection started", "run", run.ID, "scenario", sc.ID, "target", targetID)
	return run, nil
}

// executeFaultEvaluation 执行演练评估: 触发巡检 -> 分析结果 -> 生成报告评分。
func (e *Engine) executeFaultEvaluation(ctx context.Context, runID string) {
	e.mu.RLock()
	run := e.faultInjections[runID]
	e.mu.RUnlock()
	if run == nil {
		return
	}
	inspRun, err := e.RunInspection(ctx, run.InspectionID, "faultsim")
	if err != nil {
		e.finishFaultInjection(runID, "failed", "触发巡检失败: "+err.Error())
		return
	}
	e.evaluateInjection(runID, inspRun)
}

// evaluateInjection 基于巡检运行记录评估演练: 解析 findings + 关键词匹配 + 评分。
func (e *Engine) evaluateInjection(runID string, inspRun *InspectionRun) {
	e.mu.RLock()
	run := e.faultInjections[runID]
	e.mu.RUnlock()
	if run == nil {
		return
	}

	var sc *FaultScenario
	for _, s := range PresetFaultScenarios {
		if s.ID == run.ScenarioID {
			sc = s
			break
		}
	}

	summary := inspRun.Summary
	findings := parseFindings(inspRun.Findings)
	run.Findings = len(findings)
	run.AgentSummary = truncateStr(summary, 400)
	run.AgentResponded = inspRun.Status == "success" && summary != ""

	// 关键词匹配: Agent 输出(findings title/detail + summary)是否识别出注入故障
	matched := false
	if sc != nil {
		hay := strings.ToLower(summary)
		for _, f := range findings {
			hay += " " + strings.ToLower(f.Title) + " " + strings.ToLower(f.Detail)
		}
		for _, kw := range sc.Keywords {
			if strings.Contains(hay, strings.ToLower(kw)) {
				matched = true
				break
			}
		}
	}
	run.MatchedScenario = matched

	// 评分: 基础 40 + 响应 20 + 发现 10/条(上限 20) + 命中 20
	score := 40
	if run.AgentResponded {
		score += 20
	}
	score += minInt(run.Findings*10, 20)
	if matched {
		score += 20
	}
	run.Score = score

	verdict := "未识别"
	if matched {
		verdict = "已识别"
	}
	run.Report = fmt.Sprintf(
		"仿真演练: 注入「%s」到 %s。Agent 巡检%s, 发现 %d 项异常, %s注入故障(%d 分)。\n",
		run.ScenarioName, orDefault(run.TargetName, run.TargetID),
		map[bool]string{true: "已响应", false: "未响应"}[run.AgentResponded],
		run.Findings, verdict, run.Score,
	)
	if run.AgentSummary != "" {
		run.Report += "\nAgent 摘要: " + truncateStr(run.AgentSummary, 300) + "\n"
	}
	if sc != nil {
		run.Report += "\n建议处置: " + sc.Recovery + "\n"
	}

	// 演练保持 injecting(等待人工 recover 回滚) —— 语义上故障仍存在
	slog.Info("fault injection evaluated", "run", runID, "score", run.Score, "matched", matched, "findings", run.Findings)
}

// RecoverFaultInjection 恢复/回滚一次演练(仿真解除注入), 记录恢复时间。
func (e *Engine) RecoverFaultInjection(id string) (*FaultInjectionRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	run, ok := e.faultInjections[id]
	if !ok {
		return nil, fmt.Errorf("fault injection not found: %s", id)
	}
	now := time.Now()
	run.Status = FaultInjectionRecovered
	run.RecoveredAt = &now
	slog.Info("fault injection recovered", "run", id)
	return run, nil
}

// finishFaultInjection 演练异常结束(巡检触发失败等)。
func (e *Engine) finishFaultInjection(id, status, errMsg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if run, ok := e.faultInjections[id]; ok {
		run.Status = status
		run.Error = errMsg
	}
}

// ListFaultInjections 返回该租户的演练记录(倒序)。
func (e *Engine) ListFaultInjections(tenantID string) []*FaultInjectionRun {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*FaultInjectionRun
	for _, r := range e.faultInjections {
		if r.TenantID != tenantID {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// parseFindings 解析运行记录中的 findings JSON 字符串(与 EscalateInspectionRun 同解析)。
func parseFindings(raw string) []InspectionFinding {
	var out []InspectionFinding
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// minInt 返回两整数较小值。
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
