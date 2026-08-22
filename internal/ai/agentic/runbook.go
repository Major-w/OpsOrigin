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

// Runbook 沉淀的可复用修复剧本(由成功且验证通过的修复动作抽取)。
// 后续同因发现直接匹配执行, 降低 LLM 生成成本并提升可审计性。
type Runbook struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`               // 剧本名(取自修复标题)
	TitlePrefix string    `json:"title_prefix"`       // 匹配键: finding 标题前缀
	Steps       []string  `json:"steps"`              // 处置步骤(由 suggestion 拆行)
	Rollback    string    `json:"rollback,omitempty"` // 回滚说明
	RiskLevel   string    `json:"risk_level"`         // high|medium|low
	SourceRunID string    `json:"source_run_id"`      // 沉淀来源巡检 run
	UseCount    int       `json:"use_count"`          // 复用次数
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// runbookStepsFromSuggestion 将处置指令拆为步骤列表(按行/分号拆分, 去空)。
func runbookStepsFromSuggestion(s string) []string {
	var steps []string
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ';' }) {
		line = strings.TrimSpace(line)
		if line != "" {
			steps = append(steps, line)
		}
	}
	return steps
}

// runbookNormalizeTitle 归一化标题: 连续数字/百分比序列替换为 #(告警标题常带数字, 如"超 90%"/"超 95%"应视为同类)。
func runbookNormalizeTitle(title string) string {
	var b strings.Builder
	inDigit := false
	for _, r := range title {
		if (r >= '0' && r <= '9') || r == '.' || r == '%' {
			if !inDigit {
				b.WriteByte('#')
				inDigit = true
			}
			continue
		}
		inDigit = false
		b.WriteRune(r)
	}
	return b.String()
}

// runbookTitlePrefix 从修复标题抽匹配前缀: 归一化数字后取前 12 字符, 去尾部标点。
func runbookTitlePrefix(title string) string {
	title = runbookNormalizeTitle(strings.TrimSpace(title))
	runes := []rune(title)
	if len(runes) > 12 {
		title = string(runes[:12])
	}
	return strings.TrimRight(title, "。.！!？?：:；; ")
}

// CreateRunbookFromRemediation 将一条「成功且验证通过」的修复动作沉淀为 runbook(幂等: 同 title 已存在则复用并更新)。
func (e *Engine) CreateRunbookFromRemediation(ctx context.Context, a *RemediationAction) (*Runbook, bool) {
	if a == nil || a.Status != RemediationSucceeded || a.VerifyStatus != RemediationVerified {
		return nil, false
	}
	prefix := runbookTitlePrefix(a.Title)
	if prefix == "" {
		return nil, false
	}
	e.mu.Lock()
	// 幂等: 同租户同前缀已存在 → 更新步骤与风险级, 返回复用
	for _, rb := range e.runbooks {
		if rb.TenantID == a.TenantID && rb.TitlePrefix == prefix {
			rb.Steps = runbookStepsFromSuggestion(a.Suggestion)
			rb.Rollback = a.Suggestion // 原文留作回滚参考
			rb.RiskLevel = a.Level
			rb.UpdatedAt = time.Now()
			cp := *rb
			e.mu.Unlock()
			if e.store != nil {
				if err := e.store.SaveRunbook(ctx, &cp); err != nil {
					slog.Warn("persist runbook reuse failed", "id", rb.ID, "err", err)
				}
			}
			return rb, true
		}
	}
	now := time.Now()
	rb := &Runbook{
		ID:          uuid.New().String(),
		TenantID:    a.TenantID,
		Name:        a.Title,
		TitlePrefix: prefix,
		Steps:       runbookStepsFromSuggestion(a.Suggestion),
		Rollback:    a.Suggestion,
		RiskLevel:   a.Level,
		SourceRunID: a.RunID,
		UseCount:    1, // 由修复动作沉淀而来, 创建即计首次使用
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if e.runbooks == nil {
		e.runbooks = map[string]*Runbook{}
	}
	e.runbooks[rb.ID] = rb
	slog.Info("runbook created", "runbook", rb.Name, "title_prefix", prefix)
	cp := *rb
	e.mu.Unlock()
	if e.store != nil {
		if err := e.store.SaveRunbook(ctx, &cp); err != nil {
			slog.Warn("persist runbook failed", "id", rb.ID, "err", err)
		}
	}
	return rb, false
}

// MatchRunbook 按标题前缀匹配可复用剧本(数字归一化, 最精确前缀优先)。
func (e *Engine) MatchRunbook(tenantID, findingTitle string) *Runbook {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.matchRunbookLocked(tenantID, findingTitle)
}

// matchRunbookLocked 内部匹配(调用方须已持有 e.mu; 写锁或读锁皆可)。
// 抽此版本是为了让 GenerateRemediations 在持有写锁时复用匹配逻辑而不死锁(避免 RLock 嵌套)。
func (e *Engine) matchRunbookLocked(tenantID, findingTitle string) *Runbook {
	var best *Runbook
	norm := runbookNormalizeTitle(findingTitle)
	for _, rb := range e.runbooks {
		if rb.TenantID != tenantID {
			continue
		}
		if strings.HasPrefix(norm, rb.TitlePrefix) {
			if best == nil || len(rb.TitlePrefix) > len(best.TitlePrefix) {
				best = rb
			}
		}
	}
	return best
}

// ListRunbooks 返回租户 runbook 列表(按更新时间倒序)。
func (e *Engine) ListRunbooks(tenantID string) []*Runbook {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*Runbook
	for _, rb := range e.runbooks {
		if rb.TenantID == tenantID {
			cp := *rb
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// GetRunbook 按 ID 查询。
func (e *Engine) GetRunbook(id string) (*Runbook, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rb, ok := e.runbooks[id]
	if !ok {
		return nil, false
	}
	cp := *rb
	return &cp, true
}

// ExecuteRunbook 按剧本执行处置(复用修复执行器), 成功后累计 UseCount。
func (e *Engine) ExecuteRunbook(ctx context.Context, tenantID, runbookID string) (*Runbook, error) {
	rb, ok := e.GetRunbook(runbookID)
	if !ok || rb.TenantID != tenantID {
		return nil, fmt.Errorf("runbook not found: %s", runbookID)
	}
	action := &RemediationAction{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		RunID:      "runbook:" + rb.ID,
		Level:      rb.RiskLevel,
		Title:      rb.Name,
		Suggestion: strings.Join(rb.Steps, "; "),
		Risk:       classifyRemediationRisk(rb.RiskLevel, strings.Join(rb.Steps, " ")),
		Status:     RemediationPending,
		CreatedAt:  time.Now(),
	}
	e.mu.Lock()
	if e.remediations == nil {
		e.remediations = map[string]*RemediationAction{}
	}
	e.remediations[action.ID] = action
	e.mu.Unlock()

	_, err := e.ExecuteRemediation(ctx, tenantID, action.ID)
	if err != nil {
		return rb, err
	}
	e.mu.Lock()
	cur := e.runbooks[runbookID]
	if cur != nil {
		// 修正: 原 rb 是 GetRunbook 的浅拷贝, 必须写回真实 map 条目, 否则内存累计丢失。
		cur.UseCount++
		cur.UpdatedAt = time.Now()
		cp := *cur
		e.mu.Unlock()
		if e.store != nil {
			if err := e.store.SaveRunbook(ctx, &cp); err != nil {
				slog.Warn("persist runbook failed", "id", runbookID, "err", err)
			}
		}
	} else {
		e.mu.Unlock()
	}
	return rb, nil
}
