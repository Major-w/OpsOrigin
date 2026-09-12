package ai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/llm"
)

// awaitClarify 阻塞等待并发生成的追问结果, 最长 10s; 超时/失败返回 nil(不影响主回答)。
// 非流式场景: 响应本就等完整回答, 多等片刻不影响观感。
func awaitClarify(ch chan *ClarificationResult) *ClarificationResult {
	return awaitClarifyWithin(ch, 10*time.Second)
}

// awaitClarifyWithin 同 awaitClarify, 但自定义等待上限。
// 流式场景须用较短上限(主回答已流完, 再等会让前端的「思考中」状态僵硬)。
func awaitClarifyWithin(ch chan *ClarificationResult, d time.Duration) *ClarificationResult {
	if ch == nil {
		return nil
	}
	select {
	case res := <-ch:
		return res
	case <-time.After(d):
		return nil
	}
}

// buildClarifyMap 把追问结果转为非流式响应附带字段; 无内容时返回 nil(不污染响应体)。
func buildClarifyMap(res *ClarificationResult) map[string]any {
	if res == nil {
		return nil
	}
	m := map[string]any{}
	if res.NeedClarify && strings.TrimSpace(res.Question) != "" {
		m["clarification"] = res.Question
	}
	if len(res.Suggestions) > 0 {
		m["suggestions"] = res.Suggestions
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// ClarificationResult 模糊意图判定结果: 是否需要追问 + 追问问题 + 继续追问建议。
type ClarificationResult struct {
	NeedClarify bool     `json:"need_clarify"`
	Question    string   `json:"question"`
	Suggestions []string `json:"suggestions"`
}

const clarifySystemPrompt = "你是运维对话助手的「意图理解」模块。用户刚发送了一条消息，请判断这条消息是否足够清晰、能否直接给出高质量回答。\n" +
	"输出严格为单个 JSON 对象，不要包含任何解释或 Markdown 代码围栏：\n" +
	"{\n" +
	"  \"need_clarify\": true/false,        // 用户意图模糊/信息不足时为 true\n" +
	"  \"question\": \"如果用户意图模糊，用一句中文追问用户还缺少哪些关键信息（如对象、环境、现象、期望、时间、范围）；意图清晰则为空字符串\",\n" +
	"  \"suggestions\": [\"1-3 条用户可继续追问的问题，帮助用户把话题推向深入或落地，简洁、可点击\"]\n" +
	"}\n" +
	"判定 need_clarify=true 的典型情况：问题过短、缺少具体对象/环境/现象/期望、只有笼统的「怎么办/异常了/慢/报错」而无上下文。\n" +
	"suggestions 无论是否模糊都应给出，帮助用户深入，例如「提供具体的报错日志或错误码」「说明发生的环境与主机」「描述你期望的结果」。\n" +
	"每条 suggestion 不超过 20 个汉字，不要以问号结尾的冗余表述。"

// shouldGenerateClarify 启发式门控: 仅在疑似模糊/简短的提问上触发额外的追问生成,
// 避免对信息充分的长问题也额外消耗一次 LLM 调用与界面打扰。
func shouldGenerateClarify(prompt string) bool {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return false
	}
	r := []rune(p)
	// 极短(≤10 字)通常信息不足, 必追问
	if len(r) <= 10 {
		return true
	}
	// 含模糊意图词 → 追问建议有价值(去掉「错误/失败/慢/卡」等也常出现在精确指令里的词, 降低误触发)
	vagueMarkers := []string{
		"怎么办", "如何处理", "怎么处理", "怎么弄", "怎么搞", "怎么解决", "求解",
		"不清楚", "不太明白", "啥意思", "啥", "帮我看看", "帮我查", "有问题",
		"异常了", "挂了", "崩了", "连不上", "无法", "不能", "不动了", "没反应",
		"为啥", "怎样", "如何做", "求方案",
	}
	for _, w := range vagueMarkers {
		if strings.Contains(p, w) {
			return true
		}
	}
	return false
}

// generateClarify 调用 LLM 一次性产出模糊判定 + 追问问题 + 继续追问建议。
// 返回 (nil, nil) 表示未启用 LLM 或生成失败(调用方应静默忽略, 不影响主回答)。
func (s *Service) generateClarify(ctx context.Context, prompt string) (*ClarificationResult, error) {
	client := s.LLMClient()
	if client == nil {
		return nil, nil
	}
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: clarifySystemPrompt},
			{Role: "user", Content: "用户消息：" + prompt},
		},
		// 推理模型(如 glm-4.6v-flash)会先消耗 token 输出 reasoning_content,
		// max_tokens 给太小会导致 content 为空; 这里给足余量, JSON 不限制超长。
		MaxTokens:   1024,
		Temperature: 0.3,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, nil
	}
	return parseClarification(resp.Choices[0].Message.Content)
}

// parseClarification 从模型输出中稳健提取 JSON(容忍模型偶尔夹杂的注释/代码围栏/前后文)。
func parseClarification(raw string) (*ClarificationResult, error) {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, nil
	}
	jsonStr := raw[start : end+1]
	var res ClarificationResult
	if err := json.Unmarshal([]byte(jsonStr), &res); err != nil {
		return nil, err
	}
	// 清洗: 空字符串 question 视为无需追问; 去重/去空 suggestion
	if strings.TrimSpace(res.Question) == "" {
		res.NeedClarify = false
	}
	res.Suggestions = cleanSuggestions(res.Suggestions)
	return &res, nil
}

func cleanSuggestions(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(s, "。"), "？"))
		s = strings.TrimPrefix(s, "-")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
