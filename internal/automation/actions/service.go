// Package actions 提供流程引擎动作库(action library)的执行器实现。
//
// 动作库让流程的 service 步骤在"执行脚本"之外, 还能执行预置动作:
//   - http:          外发 HTTP 请求(支持 ${field} 模板替换 body/url/headers)
//   - notify:        经通知中心按 Policy 分发事件
//   - create_ticket: 创建 ITSM 工单(经注入闭包, 避免反向依赖 internal/itsm)
//   - script:        执行自动化脚本(等价于 Step.ScriptID 路径)
//
// 实现方(本包)不直接依赖 internal/itsm / internal/notification 的具体类型,
// 依赖通过构造期闭包注入, 由 cmd/core 装配, 保证无 import 环。
package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 动作类型常量(与前端流程建模器 action_type 下拉对齐)。
const (
	ActionHTTP        = "http"
	ActionNotify      = "notify"
	ActionCreateTicket = "create_ticket"
	ActionScript      = "script"
)

// ScriptExecutor 执行脚本(与 automation/script.Service.Execute 签名一致)。
type ScriptExecutor interface {
	Execute(scriptID string, params map[string]string, timeout int, runAs string, agentID string, tenantID string) (output string, exitCode int, err error)
}

// Executor 动作库执行器。字段由 cmd/core 装配注入。
type Executor struct {
	scriptExec ScriptExecutor
	// notifier 经通知中心分发事件(与 ITSM Notifier 同签名)。
	notifier func(ctx context.Context, tenantID, event, title, content string) error
	// createTicket 创建 ITSM 工单, 返回工单 ID。params 为动作参数(title/description/risk_level 等)。
	createTicket func(ctx context.Context, tenantID string, params map[string]any) (string, error)
	// httpClient 外发 HTTP 请求客户端(可注入测试桩)。
	httpClient *http.Client
}

// New 创建空执行器, 依赖由后续 SetXxx 注入。
func New() *Executor {
	return &Executor{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// SetScriptExecutor 注入脚本执行器(script 动作)。
func (e *Executor) SetScriptExecutor(s ScriptExecutor) { e.scriptExec = s }

// SetNotifier 注入通知分发回调(notify 动作)。
func (e *Executor) SetNotifier(f func(ctx context.Context, tenantID, event, title, content string) error) {
	e.notifier = f
}

// SetCreateTicket 注入建单回调(create_ticket 动作)。
func (e *Executor) SetCreateTicket(f func(ctx context.Context, tenantID string, params map[string]any) (string, error)) {
	e.createTicket = f
}

// SetHTTPClient 注入自定义 HTTP 客户端(测试用)。
func (e *Executor) SetHTTPClient(c *http.Client) { e.httpClient = c }

// ExecuteAction 执行单个动作, 返回结构化结果(写入 StepResults)。
func (e *Executor) ExecuteAction(ctx context.Context, actionType string, params map[string]any, tenantID, ticketID string, exprCtx map[string]any) (map[string]any, error) {
	if actionType == "" {
		return nil, fmt.Errorf("action type is empty")
	}
	switch actionType {
	case ActionHTTP:
		return e.doHTTP(ctx, params, exprCtx)
	case ActionNotify:
		return e.doNotify(ctx, tenantID, params, exprCtx)
	case ActionCreateTicket:
		return e.doCreateTicket(ctx, tenantID, params, exprCtx)
	case ActionScript:
		return e.doScript(ctx, tenantID, params, exprCtx)
	default:
		return nil, fmt.Errorf("unknown action type %q", actionType)
	}
}

// --- http ---

func (e *Executor) doHTTP(ctx context.Context, params map[string]any, exprCtx map[string]any) (map[string]any, error) {
	method := strOr(params, "method", "GET")
	url := tplOr(params, "url", exprCtx)
	if url == "" {
		return nil, fmt.Errorf("http action requires url")
	}
	body := tplOr(params, "body", exprCtx)
	headers := map[string]string{}
	if h, ok := params["headers"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = tpl(fmt.Sprint(v), exprCtx)
		}
	}
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, fmt.Errorf("http action: %w", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http action %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	out := map[string]any{
		"status_code": resp.StatusCode,
		"body":        string(respBody),
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("http action %s %s returned %d", method, url, resp.StatusCode)
	}
	return out, nil
}

// --- notify ---

func (e *Executor) doNotify(ctx context.Context, tenantID string, params map[string]any, exprCtx map[string]any) (map[string]any, error) {
	if e.notifier == nil {
		return nil, fmt.Errorf("notify action: notifier not configured")
	}
	event := strOr(params, "event", "")
	if event == "" {
		return nil, fmt.Errorf("notify action requires event")
	}
	title := tplOr(params, "title", exprCtx)
	content := tplOr(params, "content", exprCtx)
	if err := e.notifier(ctx, tenantID, event, title, content); err != nil {
		return nil, fmt.Errorf("notify action: %w", err)
	}
	return map[string]any{"event": event, "sent": true}, nil
}

// --- create_ticket ---

func (e *Executor) doCreateTicket(ctx context.Context, tenantID string, params map[string]any, exprCtx map[string]any) (map[string]any, error) {
	if e.createTicket == nil {
		return nil, fmt.Errorf("create_ticket action: ticket creator not configured")
	}
	resolved := map[string]any{}
	for k, v := range params {
		resolved[k] = tpl(fmt.Sprint(v), exprCtx)
	}
	id, err := e.createTicket(ctx, tenantID, resolved)
	if err != nil {
		return nil, fmt.Errorf("create_ticket action: %w", err)
	}
	return map[string]any{"ticket_id": id}, nil
}

// --- script ---

func (e *Executor) doScript(ctx context.Context, tenantID string, params map[string]any, exprCtx map[string]any) (map[string]any, error) {
	if e.scriptExec == nil {
		return nil, fmt.Errorf("script action: script executor not configured")
	}
	scriptID := strOr(params, "script_id", "")
	if scriptID == "" {
		return nil, fmt.Errorf("script action requires script_id")
	}
	strParams := map[string]string{}
	for k, v := range params {
		if k == "script_id" {
			continue
		}
		strParams[k] = tpl(fmt.Sprint(v), exprCtx)
	}
	output, exitCode, err := e.scriptExec.Execute(scriptID, strParams, 0, "", "", tenantID)
	if err != nil {
		return map[string]any{"exit_code": exitCode, "output": output}, err
	}
	if exitCode != 0 {
		return map[string]any{"exit_code": exitCode, "output": output}, fmt.Errorf("script %s exited with code %d", scriptID, exitCode)
	}
	return map[string]any{"exit_code": 0, "output": output}, nil
}

// --- 模板与取值辅助 ---

func strOr(params map[string]any, key, def string) string {
	if v, ok := params[key].(string); ok && v != "" {
		return v
	}
	return def
}

func tplOr(params map[string]any, key string, exprCtx map[string]any) string {
	return tpl(strOr(params, key, ""), exprCtx)
}

// tpl 将字符串中的 ${field} 或 ${custom_fields.field} 按表达式上下文替换。
// 无匹配占位符原样保留, 不报错(容错: 动态值缺失时动作仍能执行)。
func tpl(s string, exprCtx map[string]any) string {
	if !strings.Contains(s, "${") || exprCtx == nil {
		return s
	}
	var out strings.Builder
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			out.WriteString(s)
			break
		}
		out.WriteString(s[:start])
		rest := s[start+2:]
		end := strings.Index(rest, "}")
		if end < 0 {
			out.WriteString(s[start:])
			break
		}
		name := rest[:end]
		out.WriteString(fmt.Sprint(lookup(exprCtx, name)))
		s = rest[end+1:]
	}
	return out.String()
}

// lookup 从表达式上下文取值: 支持 "custom_fields.xxx" 点路径, 顶层直查。
func lookup(exprCtx map[string]any, name string) any {
	if v, ok := exprCtx[name]; ok {
		return v
	}
	if strings.HasPrefix(name, "custom_fields.") {
		if cf, ok := exprCtx["custom_fields"].(map[string]any); ok {
			if v, ok := cf[strings.TrimPrefix(name, "custom_fields.")]; ok {
				return v
			}
		}
	}
	return ""
}

// JSON 输出辅助(供测试断言使用)。
func marshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
