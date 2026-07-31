package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/pkg/llm"
)

// —— 编排增强默认值（均可在 AgentTeam 上覆盖）——

const (
	defaultMemberTimeoutSec = 90  // 单成员作答硬超时
	defaultMemberRetries    = 1   // 单成员失败重试次数
	defaultDebateRounds     = 2   // 辩论模式默认往返轮数
	supervisorVerifyRounds  = 1   // 主管审核最多触发的修订轮数(防止无限循环)
)

// RunTeamChat 执行一次多智能体团队对话。
//
// 编排规则:
//   - 团队级限流: 进入即 e.lim.Allow(team.ID); 超限返回错误(由 service 转友好提示)。
//   - 默认 5 分钟整体超时, 防止某成员挂死拖垮整次对话。
//   - 成员级超时 + 重试: 每个成员的作答有独立硬超时(默认 90s)与失败重试(默认 1 次、指数退避),
//     单成员反复失败才被跳过, 其余成员继续; 全部失败才返回错误。
//   - 成员工具调用(agentic): 若团队/成员开启 EnableTools, 成员在作答时可经 Function Calling 调用
//     CMDB 查询 / 知识库检索 / 受管脚本等工具(RBAC + 限流 + 结果截断复用单智能体能力),
//     工具调用过程通过 SSE 的 tool_call / tool_result 事件实时透出。
//   - 主管(supervisor)模式: 主管先将任务拆解为子任务 1:1 映射给其余成员并行执行, 最后汇总者定稿。
//   - 辩论(debate)模式: 成员按 DebateRounds 轮往返, 每轮可见其余成员上一轮观点并予以反驳/补充。
//   - 主管审核(SupervisorVerify): 成员产出后由主管审核, 若判定需修订则指定某成员补充重做一轮再定稿。
//   - 无汇总者(CanFinalize)时回退到 Members[0] 作为定稿人。
//
// emit 用于把 SSE 事件推给上层(service 负责 flush 到 HTTP 响应); 并发安全由调用方保证(加锁)。
func (e *Engine) RunTeamChat(
	ctx context.Context,
	team *model.AgentTeam,
	message string,
	history []llm.Message,
	emit func(ev model.TeamStreamEvent),
) (final string, interactions []string, err error) {
	if team == nil {
		return "", nil, fmt.Errorf("team is nil")
	}
	if message == "" {
		return "", nil, fmt.Errorf("message is required")
	}
	// 团队级限流
	if e.lim != nil && !e.lim.Allow(team.ID) {
		AILLMRateLimitHits.Inc()
		return "", nil, fmt.Errorf("rate limit exceeded for team %s", team.ID)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	finalizer := pickFinalizer(team)
	supervisor := pickSupervisor(team)

	// workers = 除汇总者外的所有成员; 若等于空(全员都是汇总者), 退化成直接让汇总者作答
	var workers []model.TeamMember
	for _, m := range team.Members {
		if m.AgentID == finalizer.AgentID {
			continue
		}
		workers = append(workers, m)
	}
	if len(workers) == 0 {
		workers = team.Members
	}

	var (
		mu         sync.Mutex
		workerOuts = map[string]string{} // agentID -> 最终产出
	)

	// runTurn 执行单个成员的单次作答(含工具循环 + 超时 + 重试)。
	// 调用方负责发射 agent_start / agent_end; 本函数仅发射 token / tool_call / tool_result。
	runTurn := func(m model.TeamMember, prompt string) (string, error) {
		sys := e.resolveMemberSysPrompt(team, m)
		return e.runMemberWithRetry(ctx, m, team, sys, prompt, history, emit)
	}

	switch team.Mode {
	case model.TeamModeParallel, model.TeamModeSupervisor:
		// supervisor 模式: 先由主管拆解子任务(1:1 映射给 workers)
		var subs []string
		if team.Mode == model.TeamModeSupervisor && supervisor.AgentID != finalizer.AgentID {
			subs = e.supervisorPlan(ctx, team, supervisor, message, workers, emit)
		}
		var wg sync.WaitGroup
		for i, m := range workers {
			wg.Add(1)
			go func(m model.TeamMember, idx int) {
				defer wg.Done()
				sub := ""
				if idx < len(subs) {
					sub = subs[idx]
				}
				emit(model.TeamStreamEvent{Type: "agent_start", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role})
				content, werr := runTurn(m, buildMemberPrompt(m, message, sub, 1, nil))
				if werr != nil {
					emit(model.TeamStreamEvent{Type: "error", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Error: werr.Error()})
					slog.Error("team worker failed", "team", team.ID, "agent", m.AgentID, "error", werr)
					return
				}
				mu.Lock()
				workerOuts[m.AgentID] = content
				mu.Unlock()
				emit(model.TeamStreamEvent{Type: "agent_end", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: content})
			}(m, i)
		}
		wg.Wait()

	case model.TeamModeSequential:
		// 顺序接力: 成员依次作答, 后一位能看到前一位的产出。
		for _, m := range workers {
			emit(model.TeamStreamEvent{Type: "agent_start", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role})
			prior := map[string]string{}
			for _, pm := range workers {
				if pm.AgentID == m.AgentID {
					break
				}
				if c := workerOuts[pm.AgentID]; c != "" {
					prior[pm.AgentName] = c
				}
			}
			content, werr := runTurn(m, buildMemberPrompt(m, message, "", 1, prior))
			if werr != nil {
				emit(model.TeamStreamEvent{Type: "error", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Error: werr.Error()})
				slog.Error("team worker failed", "team", team.ID, "agent", m.AgentID, "error", werr)
				continue
			}
			mu.Lock()
			workerOuts[m.AgentID] = content
			mu.Unlock()
			emit(model.TeamStreamEvent{Type: "agent_end", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: content})
		}

	case model.TeamModeDebate:
		// 辩论: 成员按 DebateRounds 轮往返, 每轮可见其余成员上一轮观点并予以反驳/补充。
		latest := map[string]string{}
		rounds := debateRounds(team)
		for r := 1; r <= rounds; r++ {
			for _, m := range workers {
				if r == 1 {
					emit(model.TeamStreamEvent{Type: "agent_start", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role})
				}
				prior := map[string]string{}
				for k, v := range latest {
					if k != m.AgentID {
						prior[memberNameByID(team, k)] = v
					}
				}
				content, werr := runTurn(m, buildMemberPrompt(m, message, "", r, prior))
				if werr != nil {
					emit(model.TeamStreamEvent{Type: "error", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Error: werr.Error()})
					slog.Error("team debater failed", "team", team.ID, "agent", m.AgentID, "round", r, "error", werr)
					continue
				}
				mu.Lock()
				latest[m.AgentID] = content
				mu.Unlock()
			}
		}
		mu.Lock()
		for k, v := range latest {
			workerOuts[k] = v
		}
		mu.Unlock()
		for _, m := range workers {
			if c := workerOuts[m.AgentID]; c != "" {
				emit(model.TeamStreamEvent{Type: "agent_end", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: c})
			}
		}

	default:
		// 未知模式按并行处理
		var wg sync.WaitGroup
		for _, m := range workers {
			wg.Add(1)
			go func(m model.TeamMember) {
				defer wg.Done()
				emit(model.TeamStreamEvent{Type: "agent_start", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role})
				content, werr := runTurn(m, buildMemberPrompt(m, message, "", 1, nil))
				if werr != nil {
					emit(model.TeamStreamEvent{Type: "error", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Error: werr.Error()})
					return
				}
				mu.Lock()
				workerOuts[m.AgentID] = content
				mu.Unlock()
				emit(model.TeamStreamEvent{Type: "agent_end", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: content})
			}(m)
		}
		wg.Wait()
	}

	if len(workerOuts) == 0 {
		return "", nil, fmt.Errorf("all team members failed")
	}

	// 主管审核回路: 可选, 由主管评估成员产出, 必要时指定某成员补充重做一轮。
	if team.SupervisorVerify && supervisor.AgentID != finalizer.AgentID {
		revise, instr, verr := e.supervisorReview(ctx, team, supervisor, workerOuts, emit)
		if verr != nil {
			// 审核失败不致命, 直接定稿
			slog.Warn("supervisor review failed, finalizing without revision", "team", team.ID, "err", verr)
		} else if revise != nil {
			emit(model.TeamStreamEvent{Type: "agent_start", AgentID: revise.AgentID, AgentName: revise.AgentName, Role: revise.Role, Content: "修订中: " + instr})
			revisedPrompt := fmt.Sprintf("【修订指令】%s\n\n【用户原始问题】\n%s", instr, message)
			c, rerr := e.runMemberWithRetry(ctx, *revise, team, e.resolveMemberSysPrompt(team, *revise), revisedPrompt, history, emit)
			if rerr == nil {
				mu.Lock()
				workerOuts[revise.AgentID] = c
				mu.Unlock()
			} else {
				slog.Warn("supervisor-requested revision failed", "agent", revise.AgentID, "err", rerr)
			}
			emit(model.TeamStreamEvent{Type: "agent_end", AgentID: revise.AgentID, AgentName: revise.AgentName, Role: revise.Role, Content: workerOuts[revise.AgentID]})
		}
	}

	// 汇总者定稿
	var sb strings.Builder
	for _, m := range workers {
		c := workerOuts[m.AgentID]
		if c == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("【成员 %s（%s）的产出】\n%s\n\n", m.AgentName, m.Role, c))
		interactions = append(interactions, fmt.Sprintf("%s: %s", m.AgentName, c))
	}

	finSys := e.resolveMemberSysPrompt(team, finalizer)
	finPrompt := "以下是团队各成员的产出, 请综合并给出面向用户的最终答复(直接、专业、可操作, 不要重复罗列成员原话):\n\n" +
		sb.String() + "\n【用户原始问题】\n" + message

	emit(model.TeamStreamEvent{Type: "agent_start", AgentID: finalizer.AgentID, AgentName: finalizer.AgentName, Role: finalizer.Role})
	finContent, ferr := e.runMemberWithRetry(ctx, finalizer, team, finSys, finPrompt, history, emit)
	if ferr != nil {
		emit(model.TeamStreamEvent{Type: "error", AgentID: finalizer.AgentID, AgentName: finalizer.AgentName, Role: finalizer.Role, Error: ferr.Error()})
		// 汇总失败: 回退为成员产出拼接
		final = strings.TrimSpace(sb.String())
		emit(model.TeamStreamEvent{Type: "final", Content: final, Done: true})
		emit(model.TeamStreamEvent{Type: "agent_end", AgentID: finalizer.AgentID, AgentName: finalizer.AgentName, Role: finalizer.Role, Content: final})
		return final, interactions, nil
	}
	final = finContent
	emit(model.TeamStreamEvent{Type: "agent_end", AgentID: finalizer.AgentID, AgentName: finalizer.AgentName, Role: finalizer.Role, Content: final})
	emit(model.TeamStreamEvent{Type: "final", Content: final, Done: true})
	return final, interactions, nil
}

// runMemberWithRetry 在成员级超时与重试边界内执行一次完整作答(含可能的多轮工具调用)。
func (e *Engine) runMemberWithRetry(ctx context.Context, m model.TeamMember, team *model.AgentTeam, sys, prompt string, history []llm.Message, emit func(model.TeamStreamEvent)) (string, error) {
	retries := memberRetries(team)
	var lastErr error
	var content string
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			slog.Warn("team member retry", "agent", m.AgentID, "attempt", attempt)
			emit(model.TeamStreamEvent{Type: "error", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Error: fmt.Sprintf("成员 %s 第 %d 次尝试失败, 重试中", m.AgentName, attempt)})
		}
		mctx, cancel := context.WithTimeout(ctx, memberTimeout(team))
		content, lastErr = e.runOneMemberRound(mctx, m, team, sys, prompt, history, emit)
		cancel()
		if lastErr == nil {
			return content, nil
		}
	}
	return "", lastErr
}

// runOneMemberRound 执行单个成员的单轮作答: 若开启工具则走 Function Calling 循环,
// 否则仅做一次普通补全。逐 token 经 emit 推送(此处以整段内容作为一个 token 事件, 保证工具过程可观测)。
// 不发射 agent_start / agent_end(由调用方负责)。
func (e *Engine) runOneMemberRound(ctx context.Context, m model.TeamMember, team *model.AgentTeam, sys, prompt string, history []llm.Message, emit func(model.TeamStreamEvent)) (string, error) {
	client := e.llmClient.Load()
	if client == nil {
		return "", fmt.Errorf("llm client not configured")
	}
	useTools := useTools(m, team)
	modelName := e.modelForMember(m)

	msgs := []llm.Message{{Role: "system", Content: sys}}
	hist := history
	if len(hist) > historyKeepRecent {
		hist = hist[len(hist)-historyKeepRecent:]
	}
	msgs = append(msgs, hist...)
	msgs = append(msgs, llm.Message{Role: "user", Content: prompt})

	tools := e.buildToolDefs()
	var content string
	for round := 0; round < maxToolRounds; round++ {
		slot, ok := e.acquireLLMSlot(ctx)
		if !ok {
			return "", fmt.Errorf("llm concurrency limit reached")
		}
		req := llm.ChatRequest{
			Model:       modelName,
			Messages:    msgs,
			MaxTokens:   e.genMaxTokensVal(),
			Temperature: e.genTemperatureVal(),
		}
		if useTools {
			req.Tools = tools
		}
		resp, cerr := client.Chat(ctx, req)
		e.releaseLLMSlot(slot)
		if cerr != nil {
			return "", cerr
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("llm: empty response from member %s", m.AgentID)
		}
		e.recordTeamUsage(m, resp)
		choice := resp.Choices[0].Message
		msgs = append(msgs, choice)

		if len(choice.ToolCalls) == 0 {
			content = choice.Content
			break
		}
		// 执行每个工具调用(经 execToolChecked 做 RBAC / 限流 / 截断)
		for _, tc := range choice.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			emit(model.TeamStreamEvent{Type: "tool_call", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments})
			AIToolCalls.Inc()
			result, _ := e.execToolChecked(ctx, tc)
			resJSON, _ := json.Marshal(result)
			emit(model.TeamStreamEvent{Type: "tool_result", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, ToolName: tc.Function.Name, ToolResult: string(resJSON)})
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				Content:    string(resJSON),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}
	if content == "" {
		return "", fmt.Errorf("member %s did not produce a final answer (possible tool loop)", m.AgentID)
	}
	emit(model.TeamStreamEvent{Type: "token", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: content})
	return content, nil
}

// supervisorReview 由主管评估各成员产出, 返回需修订的成员与修订指令(无则 revise=nil)。
func (e *Engine) supervisorReview(ctx context.Context, team *model.AgentTeam, sup model.TeamMember, workerOuts map[string]string, emit func(model.TeamStreamEvent)) (revise *model.TeamMember, instruction string, err error) {
	var sb strings.Builder
	for _, m := range team.Members {
		if m.AgentID == sup.AgentID {
			continue
		}
		c := workerOuts[m.AgentID]
		if c == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("【成员 %s 的产出】\n%s\n\n", m.AgentName, c))
	}
	sys := e.resolveMemberSysPrompt(team, sup) +
		"\n\n你是团队的审核主管。请评估上述成员产出是否完整、无矛盾、可直接用于面向用户的最终答复。"
	prompt := "请严格按以下格式输出:\nVERDICT: APPROVED\n或\nVERDICT: REVISE [成员名] [修订指令]\n\n" + sb.String()

	content, werr := e.runMemberWithRetry(ctx, sup, team, sys, prompt, nil, emit)
	if werr != nil {
		return nil, "", werr
	}
	emit(model.TeamStreamEvent{Type: "supervisor_review", AgentID: sup.AgentID, AgentName: sup.AgentName, Role: sup.Role, Content: content})

	if idx := strings.Index(content, "REVISE"); idx >= 0 {
		after := strings.TrimSpace(content[idx+len("REVISE"):])
		name := after
		instr := ""
		if sp := strings.IndexAny(after, " \t"); sp >= 0 {
			name = after[:sp]
			instr = strings.TrimSpace(after[sp+1:])
		}
		name = strings.Trim(name, "[] ")
		for i := range team.Members {
			if team.Members[i].AgentName == name {
				return &team.Members[i], instr, nil
			}
		}
		slog.Warn("supervisor review referenced unknown member", "name", name)
	}
	return nil, "", nil
}

// buildMemberPrompt 合成某成员本轮的 user 提示词: 子任务 + (辩论轮次下的)其余成员观点 + 用户原始问题。
func buildMemberPrompt(m model.TeamMember, message, subTask string, round int, prior map[string]string) string {
	var b strings.Builder
	if subTask != "" {
		b.WriteString("【你负责的子任务】\n" + subTask + "\n\n")
	}
	if round > 1 && len(prior) > 0 {
		b.WriteString("【其他成员在此前轮次的观点, 请据此补充、反驳或修正你的立场】\n")
		for name, out := range prior {
			b.WriteString(fmt.Sprintf("- %s: %s\n", name, out))
		}
		b.WriteString("\n")
	}
	b.WriteString("【用户原始问题】\n" + message)
	return b.String()
}

// memberNameByID 按 agentID 查找成员名(用于辩论 prior 标注)。
func memberNameByID(team *model.AgentTeam, agentID string) string {
	for _, m := range team.Members {
		if m.AgentID == agentID {
			if m.AgentName != "" {
				return m.AgentName
			}
			return m.AgentID
		}
	}
	return agentID
}

// supervisorPlan 由主管将任务拆解为 N 个子任务(与 workers 数对齐)。
func (e *Engine) supervisorPlan(ctx context.Context, team *model.AgentTeam, sup model.TeamMember, message string, workers []model.TeamMember, emit func(model.TeamStreamEvent)) []string {
	planSys := e.resolveMemberSysPrompt(team, sup)
	memberNames := make([]string, 0, len(workers))
	for _, w := range workers {
		memberNames = append(memberNames, w.AgentName)
	}
	planPrompt := fmt.Sprintf(
		"你是团队「%s」的主管。请将用户的任务拆解为 %d 个相互独立的子任务, 每个子任务由一位成员负责。\n"+
			"成员名单(按编号对应): %s。\n"+
			"请严格按如下格式输出, 每行一个子任务:\n1. [成员名] 子任务描述\n2. [成员名] 子任务描述\n...\n"+
			"只输出编号清单, 不要多余解释。\n\n用户任务:\n%s",
		team.Name, len(workers), strings.Join(memberNames, ", "), message,
	)

	emit(model.TeamStreamEvent{Type: "agent_start", AgentID: sup.AgentID, AgentName: sup.AgentName, Role: sup.Role})
	plan, perr := e.runOneMemberRound(ctx, sup, team, planSys, planPrompt, nil, emit)
	if perr != nil {
		emit(model.TeamStreamEvent{Type: "error", AgentID: sup.AgentID, AgentName: sup.AgentName, Role: sup.Role, Error: perr.Error()})
		emit(model.TeamStreamEvent{Type: "agent_end", AgentID: sup.AgentID, AgentName: sup.AgentName, Role: sup.Role})
		return nil // 拆解失败则 workers 各自拿到原始问题
	}
	emit(model.TeamStreamEvent{Type: "agent_end", AgentID: sup.AgentID, AgentName: sup.AgentName, Role: sup.Role, Content: plan})
	return parseNumbered(plan)
}

// parseNumbered 从主管产出中提取 "1. xxx" / "1、xxx" 形式的编号行。
func parseNumbered(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 形如 "1." / "1、" / "1:" 开头
		if len(line) >= 2 && line[0] >= '0' && line[0] <= '9' {
			rest := line[1:]
			if len(rest) > 0 {
				r := []rune(rest)
				if r[0] == '.' || r[0] == '、' || r[0] == ':' || r[0] == '．' {
					out = append(out, strings.TrimSpace(string(r[1:])))
				}
			}
		}
	}
	return out
}

// pickFinalizer 选择拥有定稿权的成员; 无则返回 Members[0]。
func pickFinalizer(team *model.AgentTeam) model.TeamMember {
	for _, m := range team.Members {
		if m.CanFinalize {
			return m
		}
	}
	if len(team.Members) > 0 {
		return team.Members[0]
	}
	return model.TeamMember{}
}

// pickSupervisor 选择主管(role=supervisor); 无则返回 Members[0]。
func pickSupervisor(team *model.AgentTeam) model.TeamMember {
	for _, m := range team.Members {
		if m.Role == model.TeamRoleSupervisor {
			return m
		}
	}
	if len(team.Members) > 0 {
		return team.Members[0]
	}
	return model.TeamMember{}
}

// resolveMemberSysPrompt 合成某成员的系统提示词:
// 团队 persona 前缀 + 成员画像(persona) + 成员级提示词(覆盖)或默认成员提示词。
func (e *Engine) resolveMemberSysPrompt(team *model.AgentTeam, m model.TeamMember) string {
	var b strings.Builder
	if team.SystemPrompt != "" {
		b.WriteString(team.SystemPrompt)
		b.WriteString("\n\n")
	}
	if m.Persona != "" {
		b.WriteString(withPersonaPrefix(m.Persona))
		b.WriteString("\n\n")
	}
	if m.SystemPrompt != "" {
		b.WriteString(m.SystemPrompt)
	} else {
		b.WriteString(defaultMemberSysPrompt(team, m))
	}
	return b.String()
}

// withPersonaPrefix 把角色画像包装成系统提示前缀。
func withPersonaPrefix(persona string) string {
	return "你扮演的角色：" + persona + "。请始终以此身份与专业视角作答, 并在答复中体现该角色的判断口径。"
}

// defaultMemberSysPrompt 成员未显式配置 system_prompt 时的默认提示词。
func defaultMemberSysPrompt(team *model.AgentTeam, m model.TeamMember) string {
	roleLabel := "成员"
	switch m.Role {
	case model.TeamRoleSupervisor:
		roleLabel = "主管"
	case model.TeamRoleFinalizer:
		roleLabel = "汇总者"
	case model.TeamRoleMember:
		roleLabel = "成员"
	}
	name := m.AgentName
	if name == "" {
		name = m.AgentID
	}
	return fmt.Sprintf("你是团队「%s」的%s %s, 专注于你被赋予的职责。回答应专业、具体、可操作, 避免泛泛而谈。", team.Name, roleLabel, name)
}

// —— 编排增强辅助 ——

// modelForMember 解析成员实际使用的模型: 成员级 ModelName 优先, 否则回退到绑定 Agent 的模型, 再回退引擎默认。
func (e *Engine) modelForMember(m model.TeamMember) string {
	if m.ModelName != "" {
		return m.ModelName
	}
	return e.modelForAgent(m.AgentID)
}

// useTools 判断某成员本轮是否可调用工具(成员级或团队级任一开启即可)。
func useTools(m model.TeamMember, team *model.AgentTeam) bool {
	return m.EnableTools || team.EnableTools
}

// memberTimeout 单成员作答硬超时。
func memberTimeout(team *model.AgentTeam) time.Duration {
	if team.MemberTimeoutSec > 0 {
		return time.Duration(team.MemberTimeoutSec) * time.Second
	}
	return defaultMemberTimeoutSec * time.Second
}

// memberRetries 单成员失败重试次数。
func memberRetries(team *model.AgentTeam) int {
	if team.MemberRetries > 0 {
		return team.MemberRetries
	}
	return defaultMemberRetries
}

// debateRounds 辩论模式往返轮数。
func debateRounds(team *model.AgentTeam) int {
	if team.DebateRounds > 0 {
		return team.DebateRounds
	}
	return defaultDebateRounds
}

// recordTeamUsage 估算并记录团队成员的 token 消耗: 优先使用真实 usage, 缺失时按内容长度回退估算。
func (e *Engine) recordTeamUsage(m model.TeamMember, resp *llm.ChatResponse) {
	if resp != nil && resp.Usage != nil {
		e.recordTokens(m.AgentID, resp)
		return
	}
	est := 0
	if resp != nil && len(resp.Choices) > 0 {
		est = len([]rune(resp.Choices[0].Message.Content)) / 4
	}
	if est < 1 {
		est = 1
	}
	e.persistTokenRecord(model.TokenRecord{
		Time:       time.Now(),
		AgentID:    m.AgentID,
		Model:      "team:" + m.Role,
		Prompt:     0,
		Completion: est,
		Total:      est,
	})
}
