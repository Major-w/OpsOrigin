package agentic

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/opsorigin/core/pkg/llm"
)

// DatasetEntry 一条训练数据样本(JSONL 一行)。
// 供"人机反馈 → 微调数据集"闭环: 点赞样本作为正向偏好, 点踩样本作为负向偏好,
// 处置案例作为"问题→处置"指令样本。
type DatasetEntry struct {
	// Instruction 用户问题 / 巡检发现标题。
	Instruction string `json:"instruction"`
	// Output 模型回复 / 处置方案。
	Output string `json:"output"`
	// Category 样本类别: preference_up / preference_down / disposal_case。
	Category string `json:"category"`
	// Source 样本来源: feedback:<message_id> / knowledge:<article_id>。
	Source string `json:"source,omitempty"`
	// Meta 附加元数据(租户/时间等, 供筛选去重)。
	Meta map[string]any `json:"meta,omitempty"`
}

// DatasetSummary 数据集导出摘要(供前端展示)。
type DatasetSummary struct {
	Total          int `json:"total"`
	PreferenceUp   int `json:"preference_up"`
	PreferenceDown int `json:"preference_down"`
	DisposalCase   int `json:"disposal_cases"`
}

// DisposalCaseProvider 由 cmd/core 注入的知识库处置案例回调(nil=不导出处置案例)。
// 返回处置案例文章(标题 + 内容 + ID)。
type DisposalCaseProvider func(ctx context.Context, tenantID string) ([]DisposalCase, error)

// DisposalCase 一条处置案例(知识库中 tags 含 disposal-case 的文章)。
type DisposalCase struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// SetDisposalCaseProvider 注入处置案例提供回调(cmd/core 装配, 保持 ai↔knowledge 解耦)。
func (e *Engine) SetDisposalCaseProvider(p DisposalCaseProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disposalCaseProvider = p
}

// ExportDataset 导出该租户的训练数据集:
//  1. 反馈样本(由调用方传入): 点赞/点踩 + 对应对话上下文(instruction=首条 user 消息, output=assistant 回复)
//  2. 处置案例: 知识库中 AI 巡检工单沉淀的 [处置案例] 文章(经回调注入)
//
// 返回 JSONL 字符串与摘要。
func (e *Engine) ExportDataset(ctx context.Context, tenantID string, feedbacks []*Feedback) (string, *DatasetSummary, error) {
	summary := &DatasetSummary{}
	lines := make([]string, 0, 32)
	seen := map[string]bool{}

	// 1) 反馈样本(由 ai.Service 从 feedbackStore 查询后传入)
	for _, f := range feedbacks {
		if f == nil || f.MessageID == "" {
			continue
		}
		// 从对话历史取上下文
		instruction, output := e.feedbackContext(ctx, f)
		if instruction == "" || output == "" {
			continue
		}
		cat := "preference_up"
		if f.Rating == "down" {
			cat = "preference_down"
		}
		entry := DatasetEntry{
			Instruction: instruction,
			Output:      output,
			Category:    cat,
			Source:      "feedback:" + f.MessageID,
			Meta: map[string]any{
				"tenant_id":  f.TenantID,
				"rating":     f.Rating,
				"comment":    f.Comment,
				"created_at": f.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			},
		}
		key := "fb:" + f.MessageID
		if seen[key] {
			continue
		}
		seen[key] = true
		b, _ := json.Marshal(entry)
		lines = append(lines, string(b))
		if f.Rating == "down" {
			summary.PreferenceDown++
		} else {
			summary.PreferenceUp++
		}
	}

	// 2) 处置案例样本
	e.mu.RLock()
	provider := e.disposalCaseProvider
	e.mu.RUnlock()
	if provider != nil {
		cases, err := provider(ctx, tenantID)
		if err != nil {
			slog.Warn("export dataset: load disposal cases failed", "err", err)
		}
		for _, c := range cases {
			if c.Title == "" || c.Content == "" {
				continue
			}
			key := "kb:" + c.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			entry := DatasetEntry{
				Instruction: c.Title,
				Output:      c.Content,
				Category:    "disposal_case",
				Source:      "knowledge:" + c.ID,
				Meta: map[string]any{
					"tenant_id": tenantID,
				},
			}
			b, _ := json.Marshal(entry)
			lines = append(lines, string(b))
			summary.DisposalCase++
		}
	}

	summary.Total = len(lines)
	return strings.Join(lines, "\n"), summary, nil
}

// feedbackContext 从对话历史中提取反馈样本的 instruction/output。
// instruction = 第一条 user 消息(取前 400 字符); output = 该 feedback 对应 assistant 回复
// (MessageID 匹配优先, 否则取最后一条 assistant 回复)。
func (e *Engine) feedbackContext(ctx context.Context, f *Feedback) (string, string) {
	if f.ConversationID == "" {
		return "", ""
	}
	conv, err := e.GetConversation(f.ConversationID)
	if err != nil || conv == nil {
		return "", ""
	}
	// instruction: 首条 user 消息
	for _, m := range conv.Messages {
		if m.Role == "user" {
			return truncate(m.Content, 400), lastAssistant(conv.Messages)
		}
	}
	return "", ""
}

func lastAssistant(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return truncate(msgs[i].Content, 2000)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
