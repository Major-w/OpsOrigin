package knowledge

import (
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
)

// aiWriteRequest AI 辅助编写知识请求体(由编辑器 / 斜杠命令或工具栏按钮提交)。
type aiWriteRequest struct {
	Topic    string `json:"topic"`    // 主题/标题(必填)
	Outline  string `json:"outline"`  // 大纲要点(可选, 多行)
	Tone     string `json:"tone"`     // 语气: professional/formal/friendly/concise
	Length   string `json:"length"`   // 篇幅: short/medium/long
	Category string `json:"category"` // 目标分类(可选, 仅用于提示模型受众)
}

const aiWriteSystemPrompt = "你是一名资深运维技术文档工程师，擅长把零散的需求写成结构清晰、可直接发布到企业知识库的技术文章。\n" +
	"要求：\n" +
	"1. 使用标准 Markdown 输出，包含恰当的标题层级（# / ## / ###）、列表、代码块（标注语言）、表格与加粗重点。\n" +
	"2. 内容面向运维/研发/服务台等内部读者，强调可操作性与踩坑提示，避免空洞口号。\n" +
	"3. 不要输出任何前言、解释或与文章无关的客套话，直接给出正文。\n" +
	"4. 不要用代码围栏包裹整篇内容，直接输出 Markdown 正文。\n" +
	"5. 若用户提供了分类，请贴合该分类的受众与术语习惯。"

func buildAIWriteUserPrompt(req aiWriteRequest) string {
	var b strings.Builder
	b.WriteString("请围绕以下主题撰写一篇知识库文章。\n\n")
	b.WriteString("主题：" + strings.TrimSpace(req.Topic) + "\n")
	if req.Category != "" {
		b.WriteString("目标分类：" + req.Category + "\n")
	}
	switch req.Tone {
	case "formal":
		b.WriteString("语气：正式、严谨（适合规范/SOP 类文档）。\n")
	case "friendly":
		b.WriteString("语气：亲切、易懂（适合新人上手/科普类）。\n")
	case "concise":
		b.WriteString("语气：精简、要点化（适合速查/FAQ 类）。\n")
	default:
		b.WriteString("语气：专业、务实（默认）。\n")
	}
	switch req.Length {
	case "short":
		b.WriteString("篇幅：短（约 300 字，聚焦核心结论与步骤）。\n")
	case "long":
		b.WriteString("篇幅：长（约 1200 字以上，含背景、步骤、示例、注意事项）。\n")
	default:
		b.WriteString("篇幅：中（约 600-800 字，结构完整）。\n")
	}
	if strings.TrimSpace(req.Outline) != "" {
		b.WriteString("\n请务必覆盖以下大纲要点（可调整顺序与合并）：\n")
		for _, line := range strings.Split(req.Outline, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			b.WriteString("- " + line + "\n")
		}
	}
	return b.String()
}

// handleAIWrite 调用注入的 LLM 生成器一次性产出 Markdown 正文,回填知识编辑器。
// 不污染任何会话上下文;未注入生成器时返回 503(LLM 未配置)。
func (s *Service) handleAIWrite(w http.ResponseWriter, r *http.Request) {
	if s.gen == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "AI 编写未启用（未配置 LLM）")
		return
	}
	var req aiWriteRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Topic) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "topic 不能为空")
		return
	}
	// 生成本身不依赖租户数据(路由已由鉴权中间件保护), 无需读取租户。
	user := buildAIWriteUserPrompt(req)
	content, err := s.gen.Generate(r.Context(), aiWriteSystemPrompt, user)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "AI 生成失败："+err.Error())
		return
	}
	content = stripMarkdownFence(content)
	if strings.TrimSpace(content) == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "AI 生成内容为空")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"content": content})
}

// stripMarkdownFence 去掉模型偶尔输出的整篇 ```markdown ... ``` 围栏, 返回纯正文。
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// 去掉首行 ```lang 与末尾 ```
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
