package script

import (
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
)

// aiWriteRequest AI 辅助编写脚本请求体(由编辑器 / 斜杠命令或工具栏按钮提交)。
type aiWriteRequest struct {
	Requirement string `json:"requirement"` // 脚本要做什么(必填)
	Language    string `json:"language"`    // shell / python / bat
	Notes       string `json:"notes"`       // 关键要点(可选, 多行)
	Rigor       string `json:"rigor"`       // simple / standard / robust
	Existing    string `json:"existing"`    // 现有脚本内容(非空 = 在其基础上改写)
}

const aiScriptSystemPrompt = "你是一名资深运维自动化工程师，擅长编写生产可用的运维脚本。\n" +
	"要求：\n" +
	"1. 只输出可执行的代码本身，不要输出 Markdown 代码围栏，不要输出任何解释、前言或示例输出。\n" +
	"2. 在关键步骤前后加简短注释，说明该步做什么；注释语言用中文。\n" +
	"3. 变量与临时文件命名要能表达用途，重要路径与阈值抽成脚本顶部变量。\n" +
	"4. 不要写死主机名/IP/账号密码等环境相关信息，需要时用变量并在注释里说明如何传入。\n" +
	"5. 涉及删除/覆盖/重启等破坏性动作时，先检查后执行，并在注释里写明影响范围。\n" +
	"6. 对可能的失败分支给出明确提示，不要静默失败。"

func buildAIScriptUserPrompt(req aiWriteRequest) string {
	var b strings.Builder
	lang := req.Language
	if lang == "" {
		lang = "shell"
	}
	switch lang {
	case "python":
		b.WriteString("请编写一个 Python 脚本。\n")
	case "bat":
		b.WriteString("请编写一个 Windows 批处理（.bat）脚本。\n")
	default:
		b.WriteString("请编写一个 Shell（bash）脚本。\n")
	}
	b.WriteString("\n脚本功能：\n" + strings.TrimSpace(req.Requirement) + "\n")

	switch req.Rigor {
	case "simple":
		b.WriteString("\n实现风格：尽量简洁，用最少的代码完成需求，不必引入额外的参数校验与日志。\n")
	case "robust":
		b.WriteString("\n实现风格：要健壮 —— 包含必要的参数/环境校验、错误处理、幂等保护、失败时的清理或回滚提示。\n")
	default:
		b.WriteString("\n实现风格：标准 —— 包含基本的参数校验、关键步骤输出与退出码处理。\n")
	}

	if strings.TrimSpace(req.Notes) != "" {
		b.WriteString("\n必须满足的要点（逐条落实）：\n")
		for _, line := range strings.Split(req.Notes, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			b.WriteString("- " + line + "\n")
		}
	}

	if strings.TrimSpace(req.Existing) != "" {
		b.WriteString("\n这是编辑器里现有的脚本，请在它的基础上修改/补全，保持原有风格与结构：\n")
		b.WriteString("```\n" + req.Existing + "\n```\n")
	}
	return b.String()
}

// handleAIWrite 调用注入的 LLM 生成器一次性产出脚本代码。
// 返回纯代码文本(已剥离模型可能多写的代码围栏);未注入生成器时返回 503(LLM 未配置)。
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
	if strings.TrimSpace(req.Requirement) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "requirement 不能为空")
		return
	}
	// 防御: 现有脚本上下文过长时截断, 避免撑爆上下文窗口。
	if len(req.Existing) > 12000 {
		req.Existing = req.Existing[:12000]
	}

	content, err := s.gen.Generate(r.Context(), aiScriptSystemPrompt, buildAIScriptUserPrompt(req))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "AI 生成失败："+err.Error())
		return
	}
	content = stripCodeFence(content)
	if strings.TrimSpace(content) == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "AI 生成内容为空")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"content": content})
}

// stripCodeFence 去掉模型偶尔输出的 ```lang ... ``` 围栏, 只保留代码本身。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}
