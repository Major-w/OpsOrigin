package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AlertTriageRequest 告警第一道防线入参(由监控告警/Webhook 触发)。
type AlertTriageRequest struct {
	AlertID  string `json:"alert_id"`
	Title    string `json:"title"`
	Summary  string `json:"summary,omitempty"`
	Severity string `json:"severity,omitempty"` // critical | warning | info
	Source   string `json:"source,omitempty"`   // prometheus/zabbix/自定义
	TargetCI string `json:"target_ci,omitempty"`
}

// AlertTriageResult 告警分级结论(供回写告警系统 / 展示给值班人员)。
type AlertTriageResult struct {
	AlertID       string    `json:"alert_id"`
	Title         string    `json:"title"`
	Hypothesis    string    `json:"hypothesis"`    // 根因假设
	Confidence    float64   `json:"confidence"`    // 0-1
	Severity      string    `json:"severity"`      // 分级结论 critical/warning/info
	Suggestions   []string  `json:"suggestions"`   // 建议处置动作
	Escalate      bool      `json:"escalate"`      // 是否需升级人工
	Investigation string    `json:"investigation"` // 调查过程摘要
	Raw           string    `json:"raw,omitempty"` // LLM 原始输出(解析失败时兜底)
	CreatedAt     time.Time `json:"created_at"`
}

// alertTriageSystemPrompt 值班 SRE 系统提示(第一道防线角色)。
const alertTriageSystemPrompt = `你是值班 SRE, 对每条告警做第一道分级处置。你有权通过工具查询 CMDB 配置、监控指标与近期变更, 以核实根因。
要求输出严格的 JSON(不要任何多余文字):
{"hypothesis":"根因假设", "confidence":0.0, "severity":"critical|warning|info", "suggestions":["动作1","动作2"], "escalate":true|false, "investigation":"调查过程简述"}`

// RunAlertTriage 对单条告警执行第一道防线: 根因假设 + 分级 + 建议动作。
// 复用 ChatWithTools(带 CMDB/监控工具), agentID 用逻辑 ID "alert-triage"。
func (e *Engine) RunAlertTriage(ctx context.Context, req AlertTriageRequest) (*AlertTriageResult, error) {
	prompt := fmt.Sprintf(`%s

告警信息:
- 告警ID: %s
- 标题: %s
- 摘要: %s
- 原始等级: %s
- 来源: %s
- 关联CI: %s
`, alertTriageSystemPrompt, req.AlertID, req.Title, req.Summary, req.Severity, req.Source, req.TargetCI)

	out, _, _, err := e.ChatWithTools(ctx, "alert-triage", prompt, nil, false, "", "")
	res := &AlertTriageResult{
		AlertID:   req.AlertID,
		Title:     req.Title,
		Raw:       out,
		CreatedAt: time.Now(),
	}
	if err != nil {
		res.Investigation = "告警分级执行失败: " + err.Error()
		res.Escalate = true
		return res, err
	}
	parseAlertTriageJSON(out, res)
	return res, nil
}

// parseAlertTriageJSON 容错解析 LLM 输出的 JSON(允许被 markdown 代码块包裹/前后噪声)。
func parseAlertTriageJSON(out string, res *AlertTriageResult) {
	clean := out
	// 去掉 ```json ... ``` 包裹
	if i := strings.Index(clean, "```"); i >= 0 {
		clean = clean[i:]
		clean = strings.TrimPrefix(clean, "```json")
		clean = strings.TrimPrefix(clean, "```")
		if j := strings.Index(clean, "```"); j >= 0 {
			clean = clean[:j]
		}
	}
	// 截取首个 { 到最后一个 }
	start, end := strings.Index(clean, "{"), strings.LastIndex(clean, "}")
	if start >= 0 && end > start {
		clean = clean[start : end+1]
	}
	var parsed struct {
		Hypothesis    string   `json:"hypothesis"`
		Confidence    float64  `json:"confidence"`
		Severity      string   `json:"severity"`
		Suggestions   []string `json:"suggestions"`
		Escalate      bool     `json:"escalate"`
		Investigation string   `json:"investigation"`
	}
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		// 解析失败: 保留原始输出, 标记升级人工。
		res.Investigation = "分级结论解析失败, 请人工查看原始输出"
		res.Escalate = true
		return
	}
	res.Hypothesis = parsed.Hypothesis
	res.Confidence = parsed.Confidence
	res.Severity = parsed.Severity
	res.Suggestions = parsed.Suggestions
	res.Escalate = parsed.Escalate
	res.Investigation = parsed.Investigation
}
