package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MemoryPersist 记忆持久化回调(由 cmd/core 注入 DB 实现, nil=仅内存态)。
// AddMemory 新增/更新成功后调用, 保证"重启不丢"。
type MemoryPersist func(ctx context.Context, m *ExperienceMemory) error

// MemoryLoad 记忆全量加载回调(由 cmd/core 注入, 启动时灌入内存 map)。
type MemoryLoad func(ctx context.Context, tenantID string) ([]*ExperienceMemory, error)

// 记忆来源: disposal_case(工单沉淀) | manual(手动) | conversation(对话要点)
// | feedback_up(点赞回流) | feedback_down(点踩反例)。
const (
	MemorySourceDisposalCase = "disposal_case"
	MemorySourceManual       = "manual"
	MemorySourceConversation = "conversation"
	MemorySourceFeedbackUp   = "feedback_up"
	MemorySourceFeedbackDown = "feedback_down"
)

// ExperienceMemory Agent 长期记忆条目(P0-1): 一条"问题特征 + 处置方案 + 结果"的经验,
// 跨会话复用 —— 下次遇到同类问题时直接注入 system prompt, 让 Agent 越用越聪明。
//
// 沉淀来源:
//   - 自动: 巡检发现转工单并处置成功(succeeded)后, 经 ITSM 完成回调(SetMemorySink)生成;
//   - 手动: 运维人员在数字员工页记录经验(API POST)。
//
// 检索: 对话/巡检时按问题关键词匹配该 Agent 的记忆, top-K 注入 system prompt,
// 命中后 HitCount+1 并刷新 LastHitAt。
type ExperienceMemory struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	AgentID    string     `json:"agent_id,omitempty"` // 空=全 Agent 通用经验
	Category   string     `json:"category,omitempty"` // 问题类型(如 "数据库"/"网络"/"容量")
	Keywords   []string   `json:"keywords"`           // 问题特征关键词(检索匹配用)
	Problem    string     `json:"problem"`            // 问题描述(简短)
	Resolution string     `json:"resolution"`         // 处置方案(可操作步骤)
	Outcome    string     `json:"outcome"`            // 处置结果: succeeded | false_positive
	Source     string     `json:"source,omitempty"`   // disposal_case(工单沉淀) | manual
	HitCount   int        `json:"hit_count"`          // 命中复用次数
	CreatedAt  time.Time  `json:"created_at"`
	LastHitAt  *time.Time `json:"last_hit_at,omitempty"`
}

// MemoryFilter 记忆查询过滤条件。
type MemoryFilter struct {
	AgentID  string
	Category string
}

// memoryTopK 每次检索注入 system prompt 的经验条数上限。
const memoryTopK = 3

// AddMemory 记录一条长期记忆(幂等: 同 agent+problem 已存在则更新并返回原条)。
// 新增/更新成功后经 memoryPersist 回调落库(重启不丢)。
func (e *Engine) AddMemory(m *ExperienceMemory) (*ExperienceMemory, error) {
	if m == nil || strings.TrimSpace(m.Problem) == "" || strings.TrimSpace(m.Resolution) == "" {
		return nil, fmt.Errorf("memory problem and resolution are required")
	}
	if len(m.Keywords) == 0 {
		m.Keywords = defaultKeywords(m.Problem)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.memories == nil {
		e.memories = map[string]*ExperienceMemory{}
	}
	// 幂等: 同 agent+problem 更新
	for _, existing := range e.memories {
		if existing.TenantID == m.TenantID && existing.AgentID == m.AgentID &&
			existing.Problem == m.Problem {
			existing.Resolution = m.Resolution
			existing.Category = m.Category
			existing.Outcome = m.Outcome
			existing.Keywords = m.Keywords
			existing.Source = m.Source
			slog.Info("memory updated", "id", existing.ID, "agent", existing.AgentID)
			e.persistMemory(existing)
			return existing, nil
		}
	}
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	e.memories[m.ID] = m
	slog.Info("memory added", "id", m.ID, "agent", m.AgentID, "category", m.Category)
	e.persistMemory(m)
	return m, nil
}

// persistMemory 记忆落库回调(失败仅记录, 不阻断内存写入)。
func (e *Engine) persistMemory(m *ExperienceMemory) {
	if e.memoryPersist == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.memoryPersist(ctx, m); err != nil {
		slog.Warn("memory persist failed", "id", m.ID, "err", err)
	}
}

// SetMemoryPersist 注入记忆持久化回调(DB 落库)。
func (e *Engine) SetMemoryPersist(p MemoryPersist) { e.memoryPersist = p }

// LoadMemories 从 DB 全量加载记忆进内存(启动时调用, 幂等覆盖)。
func (e *Engine) LoadMemories(ctx context.Context, tenantID string, load MemoryLoad) error {
	if load == nil {
		return nil
	}
	items, err := load(ctx, tenantID)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.memories == nil {
		e.memories = map[string]*ExperienceMemory{}
	}
	for _, it := range items {
		if it == nil || it.ID == "" {
			continue
		}
		e.memories[it.ID] = it
	}
	slog.Info("memories loaded from db", "tenant", tenantID, "count", len(items))
	return nil
}

// defaultKeywords 从问题描述抽取默认关键词(英文词优先, 中文按 2-4 字滑窗)。
func defaultKeywords(problem string) []string {
	seen := map[string]bool{}
	var out []string
	words := strings.FieldsFunc(problem, func(r rune) bool {
		return r == ' ' || r == '，' || r == ',' || r == '。' || r == ':' || r == '：' || r == '；' || r == ';' || r == '\n' || r == '\t'
	})
	// 第一遍: 英文词(精确 token, 优先保留)
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" || !isASCIIWord(w) {
			continue
		}
		lw := strings.ToLower(w)
		if len(lw) >= 3 && !seen[lw] {
			seen[lw] = true
			out = append(out, lw)
		}
	}
	// 第二遍: 中文 2-4 字滑窗
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" || isASCIIWord(w) {
			continue
		}
		runes := []rune(w)
		for i := 0; i < len(runes); i++ {
			for l := 2; l <= 4 && i+l <= len(runes); l++ {
				seg := string(runes[i : i+l])
				if !seen[seg] {
					seen[seg] = true
					out = append(out, seg)
				}
			}
		}
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func isASCIIWord(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// ListMemories 返回该租户的记忆(按 agent 过滤可选, 创建时间倒序)。
func (e *Engine) ListMemories(tenantID string, filter MemoryFilter) []*ExperienceMemory {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*ExperienceMemory
	for _, m := range e.memories {
		if m.TenantID != tenantID {
			continue
		}
		if filter.AgentID != "" && m.AgentID != filter.AgentID {
			continue
		}
		if filter.Category != "" && m.Category != filter.Category {
			continue
		}
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// DeleteMemory 删除一条记忆。
func (e *Engine) DeleteMemory(tenantID, id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.memories[id]
	if !ok || m.TenantID != tenantID {
		return false
	}
	delete(e.memories, id)
	return true
}

// RetrieveMemories 检索该 Agent 可用记忆(全 Agent 通用 + 本 Agent), 按关键词重叠度取 top-K。
// 命中后异步累计 HitCount。
func (e *Engine) RetrieveMemories(tenantID, agentID, query string) []*ExperienceMemory {
	if query == "" {
		return nil
	}
	qTokens := tokenize(query)
	e.mu.RLock()
	var candidates []*ExperienceMemory
	for _, m := range e.memories {
		if m.TenantID != tenantID {
			continue
		}
		if m.AgentID != "" && m.AgentID != agentID {
			continue
		}
		score := 0
		for _, kw := range m.Keywords {
			for _, t := range qTokens {
				if kw == t || strings.Contains(t, kw) || strings.Contains(kw, t) {
					score++
					break
				}
			}
		}
		if score > 0 {
			cp := *m
			cp.HitCount = m.HitCount
			candidates = append(candidates, &cp)
		}
	}
	e.mu.RUnlock()
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].HitCount > candidates[j].HitCount })
	if len(candidates) > memoryTopK {
		candidates = candidates[:memoryTopK]
	}
	// 命中统计(异步)
	ids := make([]string, 0, len(candidates))
	for _, m := range candidates {
		ids = append(ids, m.ID)
	}
	go e.recordMemoryHits(ids)
	return candidates
}

// recordMemoryHits 累计命中次数。
func (e *Engine) recordMemoryHits(ids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for _, id := range ids {
		if m, ok := e.memories[id]; ok {
			m.HitCount++
			m.LastHitAt = &now
		}
	}
}

// memoriesPrompt 把检索到的记忆渲染为 system 注入片段。
func memoriesPrompt(ms []*ExperienceMemory) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【历史处置经验(来自本平台积累, 同类问题可优先参考)】\n")
	for _, m := range ms {
		outcome := "成功"
		if m.Outcome == "false_positive" {
			outcome = "误报"
		}
		fmt.Fprintf(&b, "- [%s] 问题: %s | 处置: %s | 结果: %s\n",
			orDefault(m.Category, "通用"), m.Problem, m.Resolution, outcome)
	}
	return b.String()
}

// tokenize 查询分词(复用 defaultKeywords 逻辑, 返回去重小写 token)。
func tokenize(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range defaultKeywords(q) {
		lw := strings.ToLower(w)
		if !seen[lw] {
			seen[lw] = true
			out = append(out, lw)
		}
	}
	return out
}
