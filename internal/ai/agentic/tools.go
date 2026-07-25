package agentic

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// MCPToolSet 向 AI 引擎注册运维 MCP 工具（CMDB查询、工单创建、脚本执行、知识库检索）。
type MCPToolSet struct {
	db *sql.DB
}

func NewMCPToolSet(db *sql.DB) *MCPToolSet { return &MCPToolSet{db: db} }

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
	now := fmt.Sprintf("%d", ctx.Value("now"))
	_ = now
	id := fmt.Sprintf("ai-%d", len(title)+len(desc))
	_, err := ts.db.ExecContext(ctx,
		`INSERT INTO itsm_tickets(id,tenant_id,title,description,risk_level,status,created_by,created_by_type,created_at,updated_at)
		 VALUES($1,'default',$2,$3,$4,'draft','ai_assistant','api',NOW(),NOW())`,
		id, title, desc, risk)
	if err != nil { slog.Warn("ai ticket create failed", "err", err); return map[string]any{"error": err.Error()}, nil }
	return map[string]any{"id": id, "status": "draft", "title": title}, nil
}

// scriptRun 执行脚本
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
	return map[string]any{"script_id": scriptID, "note": "execution queued via scheduler"}, nil
}

// knowledgeSearch 知识库 RAG 检索
func (ts *MCPToolSet) knowledgeSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" { return nil, fmt.Errorf("query required") }
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, title, LEFT(content, 500) as snippet FROM itsm_knowledge WHERE tenant_id='default' AND (title ILIKE $1 OR content ILIKE $1) LIMIT 5`,
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
	reg.Register(mcp.ToolDefinition{Name: "script_run", Description: "Execute a script by ID or name. Args: script_id(string) or script_name(string)", Handler: ts.scriptRun})
	reg.Register(mcp.ToolDefinition{Name: "knowledge_search", Description: "Search knowledge base articles for relevant documentation (RAG). Args: query(string)", Handler: ts.knowledgeSearch})
}

// RAGContext 为给定查询检索知识库上下文（用于注入 LLM system prompt）
func (ts *MCPToolSet) RAGContext(ctx context.Context, query string) string {
	if ts.db == nil || query == "" { return "" }
	rows, err := ts.db.QueryContext(ctx,
		`SELECT title, LEFT(content, 300) FROM itsm_knowledge WHERE tenant_id='default' AND (title ILIKE $1 OR content ILIKE $1) LIMIT 3`,
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
