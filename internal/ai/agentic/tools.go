package agentic

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
)

// ScriptExecutor 复用 automation/script 服务的执行入口，
// 使 AI 的 script_run 工具真正把脚本经 Agent 通道下发到目标主机执行，
// 而非返回"已排队"的假状态。签名与 script.Service.Execute 对齐（鸭子类型）。
type ScriptExecutor interface {
	Execute(scriptID string, params map[string]string, timeout int, runAs, agentID, tenantID string) (output string, exitCode int, err error)
}

// MCPToolSet 向 AI 引擎注册运维 MCP 工具（CMDB查询、工单创建、脚本执行、知识库检索）。
type MCPToolSet struct {
	db   *sql.DB
	exec ScriptExecutor
}

func NewMCPToolSet(db *sql.DB) *MCPToolSet { return &MCPToolSet{db: db} }

// SetScriptExecutor 注入脚本执行器（一般为 automation/script 服务）。
func (ts *MCPToolSet) SetScriptExecutor(e ScriptExecutor) { ts.exec = e }

// cmdbQuery 查询 CMDB 资产
func (ts *MCPToolSet) cmdbQuery(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" { query = "%" }
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, type_id, name, attributes FROM cmdb_ci WHERE tenant_id='default' AND (name ILIKE $1 OR type_id ILIKE $1) LIMIT 20`,
		"%"+query+"%")
	if err != nil { return nil, err }
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, typeID, name string
		var attrs []byte
		rows.Scan(&id, &typeID, &name, &attrs)
		items = append(items, map[string]any{"id": id, "type": typeID, "name": name, "attrs": string(attrs)})
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// ticketCreate 创建 ITSM 工单
func (ts *MCPToolSet) ticketCreate(ctx context.Context, args map[string]any) (any, error) {
	title, _ := args["title"].(string)
	desc, _ := args["description"].(string)
	risk, _ := args["risk_level"].(string)
	if title == "" { title = "AI Assistant Ticket" }
	if risk == "" { risk = "low" }
	id := uuid.NewString()
	now := time.Now()
	_, err := ts.db.ExecContext(ctx,
		`INSERT INTO itsm_tickets(id,tenant_id,title,description,risk_level,status,created_by,created_by_type,created_at,updated_at)
		 VALUES($1,'default',$2,$3,$4,'draft','ai_assistant','ai',$5,$5)`,
		id, title, desc, risk, now)
	if err != nil { slog.Warn("ai ticket create failed", "err", err); return map[string]any{"error": err.Error()}, nil }
	return map[string]any{"id": id, "status": "draft", "title": title}, nil
}

// scriptRun 在目标 Agent 主机上真实执行脚本并同步等待结果。
// 目标 Agent 解析优先级：agent_id > agent_name(按名模糊匹配) > 任一在线 Agent。
func (ts *MCPToolSet) scriptRun(ctx context.Context, args map[string]any) (any, error) {
	scriptID, _ := args["script_id"].(string)
	if scriptID == "" {
		// search by name
		name, _ := args["script_name"].(string)
		if name != "" {
			var sid string
			ts.db.QueryRowContext(ctx, `SELECT id FROM automation_scripts WHERE tenant_id='default' AND name ILIKE $1 LIMIT 1`, "%"+name+"%").Scan(&sid)
			scriptID = sid
		}
	}
	if scriptID == "" { return nil, fmt.Errorf("script_id or script_name required") }

	// 解析目标 Agent
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		if an, _ := args["agent_name"].(string); an != "" && ts.db != nil {
			ts.db.QueryRowContext(ctx,
				`SELECT id FROM agent_hosts WHERE tenant_id='default' AND name ILIKE $1 ORDER BY (status='online') DESC LIMIT 1`,
				"%"+an+"%").Scan(&agentID)
		}
	}
	if agentID == "" && ts.db != nil {
		ts.db.QueryRowContext(ctx,
			`SELECT id FROM agent_hosts WHERE tenant_id='default' AND status='online' ORDER BY last_heartbeat DESC NULLS LAST LIMIT 1`).Scan(&agentID)
	}
	if agentID == "" {
		return map[string]any{"script_id": scriptID, "status": "error", "error": "no target agent: provide agent_id/agent_name or ensure an online agent exists"}, nil
	}

	if ts.exec == nil {
		return map[string]any{"script_id": scriptID, "agent_id": agentID, "status": "error", "error": "script executor not wired"}, nil
	}

	// 解析入参
	params := map[string]string{}
	if raw, ok := args["params"].(map[string]any); ok {
		for k, v := range raw {
			params[k] = fmt.Sprintf("%v", v)
		}
	}
	timeout := 300
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = int(t)
	}
	runAs, _ := args["run_as"].(string)

	output, exitCode, err := ts.exec.Execute(scriptID, params, timeout, runAs, agentID, "default")
	if err != nil {
		return map[string]any{
			"script_id": scriptID, "agent_id": agentID, "exit_code": exitCode,
			"status": "failed", "error": err.Error(), "output": output,
		}, nil
	}
	return map[string]any{
		"script_id": scriptID, "agent_id": agentID, "exit_code": exitCode,
		"status": "success", "output": output,
	}, nil
}

// knowledgeSearch 知识库 RAG 检索
func (ts *MCPToolSet) knowledgeSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" { return nil, fmt.Errorf("query required") }
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, title, LEFT(content, 500) as snippet FROM knowledge_articles WHERE tenant_id='default' AND (title ILIKE $1 OR content ILIKE $1) LIMIT 5`,
		"%"+query+"%")
	if err != nil { return nil, err }
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, title, snippet string
		rows.Scan(&id, &title, &snippet)
		items = append(items, map[string]any{"id": id, "title": title, "snippet": snippet})
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// Register 将所有运维工具注册到 MCP Registry
func (ts *MCPToolSet) Register(reg *mcp.Registry) {
	reg.Register(mcp.ToolDefinition{Name: "cmdb_query", Description: "Query CMDB assets by name or type. Args: query(string)", Handler: ts.cmdbQuery})
	reg.Register(mcp.ToolDefinition{Name: "ticket_create", Description: "Create an ITSM ticket. Args: title(string), description(string), risk_level(string)", Handler: ts.ticketCreate})
	reg.Register(mcp.ToolDefinition{Name: "script_run", Description: "Execute an automation script on a managed host and return its output. Args: script_id(string) or script_name(string); agent_id(string) or agent_name(string) target host (defaults to any online agent); params(object) optional; timeout(number) seconds; run_as(string) optional", Handler: ts.scriptRun})
	reg.Register(mcp.ToolDefinition{Name: "knowledge_search", Description: "Search knowledge base articles for relevant documentation (RAG). Args: query(string)", Handler: ts.knowledgeSearch})
}

// RAGContext 为给定查询检索知识库上下文（用于注入 LLM system prompt）
func (ts *MCPToolSet) RAGContext(ctx context.Context, query string) string {
	if ts.db == nil || query == "" { return "" }
	rows, err := ts.db.QueryContext(ctx,
		`SELECT title, LEFT(content, 300) FROM knowledge_articles WHERE tenant_id='default' AND (title ILIKE $1 OR content ILIKE $1) LIMIT 3`,
		"%"+query+"%")
	if err != nil { return "" }
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var title, snippet string
		rows.Scan(&title, &snippet)
		parts = append(parts, fmt.Sprintf("## %s\n%s", title, snippet))
	}
	if len(parts) == 0 { return "" }
	return "\n\n--- 知识库相关文档 ---\n" + strings.Join(parts, "\n\n")
}
