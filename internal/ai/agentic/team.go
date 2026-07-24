package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/pkg/llm"
)

// RunTeamChat 执行一次多智能体团队对话。
//
// 编排规则:
//   - 团队级限流: 进入即 e.lim.Allow(team.ID); 超限返回错误(由 service 转友好提示)。
//   - 默认 5 分钟整体超时, 防止某成员挂死拖垮整次对话。
//   - 单成员失败: 发射 error 事件并跳过该成员, 其余成员继续; 全部失败才返回错误。
//   - 无汇总者(CanFinalize)时回退到 Members[0] 作为定稿人。
//   - 主管(supervisor 模式): 由 Members[0] 先将任务拆解为子任务, 1:1 映射给其余成员并行执行, 最后汇总者定稿。
//
// emit 用于把 SSE 事件推给上层(service 负责 flush 到 HTTP 响应)。
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
		mu        sync.Mutex
		workerOuts = map[string]string{} // agentID -> 产出
	)

	runWorker := func(m model.TeamMember, subTask string) {
		sys := e.resolveMemberSysPrompt(team, m)
		prompt := message
		if subTask != "" {
			prompt = "【你负责的子任务】\n" + subTask + "\n\n【用户原始问题】\n" + message
		}
		emit(model.TeamStreamEvent{Type: "agent_start", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role})
		content, werr := e.streamMember(ctx, m, sys, prompt, history, emit)
		if werr != nil {
			emit(model.TeamStreamEvent{Type: "error", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Error: werr.Error()})
			slog.Error("team worker failed", "team", team.ID, "agent", m.AgentID, "error", werr)
			return
		}
		mu.Lock()
		workerOuts[m.AgentID] = content
		mu.Unlock()
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
				runWorker(m, sub)
			}(m, i)
		}
		wg.Wait()

	case model.TeamModeSequential, model.TeamModeDebate:
		// 顺序 / 辩论: 成员依次作答, 后一位能看到前一位的产出(作为 assistant 消息累积)
		acc := append([]llm.Message{}, history...)
		for _, m := range workers {
			runWorker(m, "")
			mu.Lock()
			c := workerOuts[m.AgentID]
			mu.Unlock()
			if c != "" {
				acc = append(acc, llm.Message{Role: "assistant", Content: c})
			}
		}

	default:
		// 未知模式按并行处理
		var wg sync.WaitGroup
		for _, m := range workers {
			wg.Add(1)
			go func(m model.TeamMember) { defer wg.Done(); runWorker(m, "") }(m)
		}
		wg.Wait()
	}

	if len(workerOuts) == 0 {
		return "", nil, fmt.Errorf("all team members failed")
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
	finContent, ferr := e.streamMember(ctx, finalizer, finSys, finPrompt, history, emit)
	if ferr != nil {
		emit(model.TeamStreamEvent{Type: "error", AgentID: finalizer.AgentID, AgentName: finalizer.AgentName, Role: finalizer.Role, Error: ferr.Error()})
		// 汇总失败: 回退为成员产出拼接
		final = strings.TrimSpace(sb.String())
		emit(model.TeamStreamEvent{Type: "final", Content: final, Done: true})
		return final, interactions, nil
	}
	final = finContent
	emit(model.TeamStreamEvent{Type: "final", Content: final, Done: true})
	return final, interactions, nil
}

// streamMember 以流式方式调用某成员的 LLM, 逐 token 通过 emit 推送, 返回完整内容。
// 不负责发射 agent_start(由调用方发射, 以便汇总者的 start 与 worker 统一)。
func (e *Engine) streamMember(ctx context.Context, m model.TeamMember, sysPrompt, prompt string, history []llm.Message, emit func(model.TeamStreamEvent)) (string, error) {
	if !e.acquireLLMSlot(ctx) {
		return "", fmt.Errorf("llm concurrency limit reached")
	}
	defer e.releaseLLMSlot()

	msgs := []llm.Message{{Role: "system", Content: sysPrompt}}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: prompt})

	ch, err := e.llmClient.StreamChat(ctx, llm.ChatRequest{
		Messages:    msgs,
		MaxTokens:   e.genMaxTokensVal(),
		Temperature: e.genTemperatureVal(),
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for c := range ch {
		if c.Content != "" {
			b.WriteString(c.Content)
			emit(model.TeamStreamEvent{Type: "token", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: c.Content})
		}
	}
	content := b.String()
	if content == "" {
		return "", fmt.Errorf("llm: empty response from member %s", m.AgentID)
	}
	e.recordTeamTokens(m, content)
	emit(model.TeamStreamEvent{Type: "agent_end", AgentID: m.AgentID, AgentName: m.AgentName, Role: m.Role, Content: content})
	return content, nil
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
	plan, perr := e.streamMember(ctx, sup, planSys, planPrompt, nil, emit)
	if perr != nil {
		emit(model.TeamStreamEvent{Type: "error", AgentID: sup.AgentID, AgentName: sup.AgentName, Role: sup.Role, Error: perr.Error()})
		return nil // 拆解失败则 workers 各自拿到原始问题
	}
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

// recordTeamTokens 估算并记录团队成员的 token 消耗(无真实 usage 时回退到按角色记账)。
func (e *Engine) recordTeamTokens(m model.TeamMember, content string) {
	est := len([]rune(content)) / 4 // 粗略估算: 4 字符 ≈ 1 token
	if est < 1 {
		est = 1
	}
	e.tokenMu.Lock()
	e.tokenRecs = append(e.tokenRecs, model.TokenRecord{
		Time:       time.Now(),
		AgentID:    m.AgentID,
		Model:      "team:" + m.Role,
		Prompt:     0,
		Completion: est,
		Total:      est,
	})
	if len(e.tokenRecs) > 2000 {
		e.tokenRecs = e.tokenRecs[len(e.tokenRecs)-2000:]
	}
	e.tokenMu.Unlock()
}
