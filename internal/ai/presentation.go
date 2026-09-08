// AI 演示文稿编排(POST /api/v1/ai/presentation):
// 将一份 markdown 文档交给 LLM 重写为"适合直接演讲"的分页大纲(标题 + 每页要点),
// 前端拿到 JSON 用 pptxgenjs 渲染成 .pptx —— 取代旧的"按标题机械分页、正文整段转储"。
// 纯 LLM 文本编排, 无文件/代码执行面。
package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/llm"
)

const (
	// 输入太长先截断(演示大纲不需要全文, 保留标题与主体论述即可)
	presentationMaxInputRunes = 60000
	presentationTimeout       = 90 * time.Second
)

type presentationSlide struct {
	Title   string   `json:"title"`
	Bullets []string `json:"bullets"`
}

type presentationOutline struct {
	Title  string              `json:"title"`
	Slides []presentationSlide `json:"slides"`
}

// stripCodeFence 去除模型输出外层 ```json ... ``` 围栏(存在时)。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			s = s[3:]
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}
	return strings.TrimSpace(s)
}

// handleGeneratePresentation body: {title, content(markdown)}
func (s *Service) handleGeneratePresentation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		httputil.WriteError(w, http.StatusBadRequest, "content 为空, 无法生成演示文稿")
		return
	}
	content = truncateRunes(content, presentationMaxInputRunes)

	const sysPrompt = `你是资深演示文稿设计师。用户会提供一份 Markdown 文档, 请把它重写成一份**适合直接演讲的 PPT 大纲**——不是原样转储, 而是提炼、重组、分层。

硬性要求:
1. 只输出一个 JSON 对象, 不要 Markdown 代码围栏、不要任何解释或注释:
{"title":"演示标题","slides":[{"title":"页面标题","bullets":["要点1","要点2","要点3"]}]}
2. slides[0] 为封面页: title=演示标题, bullets 放开场钩子与讲者话术要点(2-4 条)。
3. 中间按文档逻辑结构分节, 每节 1-2 页; 总页数控制在 6-12 页。
4. 每页 bullets 3-6 条, 每条 8-24 个汉字, 讲人话、可念出来, 严禁贴原文大段或整段照抄。
5. 关键数字、对比、结论要单独成条强调; 只使用文档中出现的数据与事实, 不要编造。`
	msg := llm.Message{
		Role:    "user",
		Content: "文档标题: " + req.Title + "\n\n文档内容(markdown):\n" + content,
	}

	ctx, cancel := context.WithTimeout(r.Context(), presentationTimeout)
	defer cancel()
	resp, err := s.LLMClient().Chat(ctx, llm.ChatRequest{
		Messages:    []llm.Message{{Role: "system", Content: sysPrompt}, msg},
		MaxTokens:   5000,
		Temperature: 0.4,
		Stream:      false,
	})
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "演示大纲生成失败: "+err.Error())
		return
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		httputil.WriteError(w, http.StatusBadGateway, "模型未返回内容(可能 max_tokens 不足或推理中断), 请重试")
		return
	}
	out := stripCodeFence(resp.Choices[0].Message.Content)
	var outline presentationOutline
	if err := json.Unmarshal([]byte(out), &outline); err != nil || len(outline.Slides) == 0 {
		httputil.WriteError(w, http.StatusUnprocessableEntity, "模型输出不是有效的演示大纲, 请重试")
		return
	}
	// 结构清洗: 防超长/空页/空条
	clean := presentationOutline{
		Title:  truncateRunes(strings.TrimSpace(outline.Title), 80),
		Slides: []presentationSlide{},
	}
	for _, sl := range outline.Slides {
		if len(clean.Slides) >= 30 {
			break
		}
		t := truncateRunes(strings.TrimSpace(sl.Title), 60)
		var bs []string
		for _, b := range sl.Bullets {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			bs = append(bs, truncateRunes(b, 200))
			if len(bs) >= 12 {
				break
			}
		}
		if t == "" && len(bs) == 0 {
			continue
		}
		clean.Slides = append(clean.Slides, presentationSlide{Title: t, Bullets: bs})
	}
	if len(clean.Slides) == 0 {
		httputil.WriteError(w, http.StatusUnprocessableEntity, "演示大纲为空, 请重试")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, clean)
}
