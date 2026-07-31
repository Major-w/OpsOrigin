package agentic

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
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

// embedderIface 向量生成器,供知识库语义检索使用(由 llm.Client 实现)。
type embedderIface interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// tenantOf 从请求上下文解析当前租户; 缺省回退 "default"。
// 使 AI 工具仅在请求者所属租户范围内查询 CMDB/工单/脚本/主机/知识库, 避免越租户访问。
func tenantOf(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

// MCPToolSet 向 AI 引擎注册运维 MCP 工具（CMDB查询、工单创建、脚本执行、知识库检索）。
type MCPToolSet struct {
	db       *sql.DB
	exec     ScriptExecutor
	embedder embedderIface
	// approvedScripts 免于人工审批、允许 AI 直接执行的脚本白名单(script_id 或 script_name)。
	// 为空时(默认)所有 AI 发起的脚本执行都须经人工审批(建变更单), 以安全优先(P0-2)。
	approvedScripts map[string]bool

	// 向量检索缓存: embedCache 缓存 query->embedding(去重 LLM embedding 调用),
	// searchCache 缓存 (tenant,query,mode,limit,snippet)->结果(语义检索结果复用)。
	embedCache  *ttlCache
	searchCache *ttlCache
}

func NewMCPToolSet(db *sql.DB) *MCPToolSet {
	return &MCPToolSet{
		db:          db,
		embedCache:  newTTLCache(embedCacheTTL, 1024),
		searchCache: newTTLCache(searchCacheTTL, 1024),
	}
}

// SetScriptExecutor 注入脚本执行器（一般为 automation/script 服务）。
func (ts *MCPToolSet) SetScriptExecutor(e ScriptExecutor) { ts.exec = e }

// SetEmbedder 注入向量生成器,使知识库检索启用语义向量匹配(无则回退 ILIKE)。
func (ts *MCPToolSet) SetEmbedder(e embedderIface) { ts.embedder = e }

// SetApprovedScripts 设置 AI 可直接执行的脚本白名单(免人工审批)。
// 传入空切片则恢复为"全部须经审批"的安全默认(P0-2)。
func (ts *MCPToolSet) SetApprovedScripts(ids []string) {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			m[id] = true
		}
	}
	ts.approvedScripts = m
}

// cmdbQuery 查询 CMDB 资产
func (ts *MCPToolSet) cmdbQuery(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		query = "%"
	}
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, type_id, name, attributes FROM cmdb_ci WHERE tenant_id=$2 AND (name ILIKE $1 OR type_id ILIKE $1) LIMIT 20`,
		"%"+query+"%", tenantOf(ctx))
	if err != nil {
		return nil, err
	}
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
	if title == "" {
		title = "AI Assistant Ticket"
	}
	if risk == "" {
		risk = "low"
	}
	id := uuid.NewString()
	now := time.Now()
	_, err := ts.db.ExecContext(ctx,
		`INSERT INTO itsm_tickets(id,tenant_id,title,description,risk_level,status,created_by,created_by_type,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,'draft','ai_assistant','ai',$6,$6)`,
		id, tenantOf(ctx), title, desc, risk, now)
	if err != nil {
		slog.Warn("ai ticket create failed", "err", err)
		return map[string]any{"error": err.Error()}, nil
	}
	return map[string]any{"id": id, "status": "draft", "title": title}, nil
}

// scriptRun 在目标 Agent 主机上真实执行脚本并同步等待结果。
//
// 安全约束(P0-2):
//   - 目标 Agent 必须显式指定(agent_id 或 agent_name); 严禁"自动挑选任意在线 Agent"在随机主机上执行。
//   - 默认(白名单为空)所有 AI 发起的脚本执行都须经人工审批: 自动建 ITSM 变更单并返回
//     pending_approval, 而非直接执行。仅白名单内的脚本可免审批直执行。
//   - dry_run=true 时仅回显执行计划, 不真正执行(安全预览)。
func (ts *MCPToolSet) scriptRun(ctx context.Context, args map[string]any) (any, error) {
	scriptID, _ := args["script_id"].(string)
	if scriptID == "" {
		// search by name
		name, _ := args["script_name"].(string)
		if name != "" {
			var sid string
			ts.db.QueryRowContext(ctx, `SELECT id FROM automation_scripts WHERE tenant_id=$2 AND name ILIKE $1 LIMIT 1`, "%"+name+"%", tenantOf(ctx)).Scan(&sid)
			scriptID = sid
		}
	}
	if scriptID == "" {
		return nil, fmt.Errorf("script_id or script_name required")
	}

	// 解析目标 Agent: 必须显式指定, 禁止自动挑选任意在线 Agent 在随机主机上执行。
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		if an, _ := args["agent_name"].(string); an != "" && ts.db != nil {
			ts.db.QueryRowContext(ctx,
				`SELECT id FROM agent_hosts WHERE tenant_id=$2 AND name ILIKE $1 ORDER BY (status='online') DESC LIMIT 1`,
				"%"+an+"%", tenantOf(ctx)).Scan(&agentID)
		}
	}
	if agentID == "" {
		return map[string]any{
			"script_id": scriptID,
			"status":    "error",
			"error":     "target host not specified: provide explicit agent_id or agent_name (auto-selection of any online agent is disabled for safety)",
		}, nil
	}

	// dry_run: 仅解析并回显执行计划, 不真正执行。
	if dry, _ := args["dry_run"].(bool); dry {
		return map[string]any{
			"script_id": scriptID,
			"agent_id":  agentID,
			"status":    "dry_run",
			"message":   "dry-run: script would be executed on the target host (no execution performed)",
			"params":    args["params"],
			"timeout":   args["timeout"],
			"run_as":    args["run_as"],
		}, nil
	}

	// 审批闸门: 未列入白名单的脚本须经人工审批(建变更单), 默认全部需要审批(安全优先)。
	if !ts.isScriptApproved(scriptID, args) {
		ticketID, terr := ts.createApprovalTicket(ctx, scriptID, agentID)
		if terr != nil {
			return map[string]any{"script_id": scriptID, "agent_id": agentID, "status": "error", "error": "script requires approval but ticket creation failed: " + terr.Error()}, nil
		}
		return map[string]any{
			"script_id": scriptID,
			"agent_id":  agentID,
			"status":    "pending_approval",
			"ticket_id": ticketID,
			"message":   "script execution requires human approval; an ITSM change ticket has been created, execute after approval",
		}, nil
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

	output, exitCode, err := ts.exec.Execute(scriptID, params, timeout, runAs, agentID, tenantOf(ctx))
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

// isScriptApproved 判断脚本是否免于人工审批(在白名单内)。白名单为空时默认全部需要审批。
func (ts *MCPToolSet) isScriptApproved(scriptID string, args map[string]any) bool {
	if len(ts.approvedScripts) == 0 {
		return false
	}
	if ts.approvedScripts[scriptID] {
		return true
	}
	if id, _ := args["script_id"].(string); id != "" && ts.approvedScripts[id] {
		return true
	}
	if name, _ := args["script_name"].(string); name != "" && ts.approvedScripts[name] {
		return true
	}
	return false
}

// createApprovalTicket 为待审批的 AI 脚本执行创建一张 draft 状态的 ITSM 变更单。
func (ts *MCPToolSet) createApprovalTicket(ctx context.Context, scriptID, agentID string) (string, error) {
	id := uuid.NewString()
	now := time.Now()
	_, err := ts.db.ExecContext(ctx,
		`INSERT INTO itsm_tickets(id,tenant_id,title,description,risk_level,status,created_by,created_by_type,created_at,updated_at)
		 VALUES($1,$2,$3,$4,'high','draft','ai_assistant','ai',$5,$5)`,
		id, tenantOf(ctx), "AI 脚本执行审批: "+scriptID,
		fmt.Sprintf("AI assistant requested execution of script %s on host %s; requires human approval before run.", scriptID, agentID), now)
	if err != nil {
		return "", err
	}
	return id, nil
}

// knowledgeSearch 知识库 RAG 检索
func (ts *MCPToolSet) knowledgeSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query required")
	}
	items, err := ts.searchKnowledge(ctx, query, 5, 500)
	if err != nil {
		return nil, err
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// searchKnowledge 优先用向量语义检索,失败或无 embedding 时回退 ILIKE 关键词匹配。
// 结果经 ttlCache 缓存: embedding 去重(query->向量) + 检索结果复用(tenant+query+mode+分页)。
func (ts *MCPToolSet) searchKnowledge(ctx context.Context, query string, limit, snippetLen int) ([]map[string]any, error) {
	tenant := tenantOf(ctx)

	// —— 向量语义检索 ——
	if ts.embedder != nil {
		// embedding 去重: 优先从 embedCache 取已算好的向量, 避免重复调用 LLM embedding 接口。
		qv, embedErr := ts.cachedEmbed(ctx, query)
		if embedErr == nil && len(qv) > 0 {
			lit := vectorLiteral(qv)
			vkey := ts.searchCacheKey(tenant, query, "vector", limit, snippetLen)
			if v, ok := ts.searchCache.get(vkey); ok {
				AIKnowledgeCacheHits.Inc()
				return v.([]map[string]any), nil
			}
			rows, qerr := ts.db.QueryContext(ctx,
				`SELECT id, title, LEFT(content, $2) AS snippet FROM knowledge_articles
				 WHERE tenant_id=$4 AND embedding IS NOT NULL
				 ORDER BY embedding <=> $1::vector LIMIT $3`,
				lit, snippetLen, limit, tenant)
			if qerr == nil {
				items, serr := scanKnowledgeRows(rows)
				if serr != nil {
					return nil, serr
				}
				ts.searchCache.set(vkey, items)
				return items, nil
			}
			slog.Warn("knowledge: vector search failed, falling back to ILIKE", "err", qerr)
		}
	}

	// —— 回退:ILIKE 关键词匹配 ——
	ikey := ts.searchCacheKey(tenant, query, "ilike", limit, snippetLen)
	if v, ok := ts.searchCache.get(ikey); ok {
		AIKnowledgeCacheHits.Inc()
		return v.([]map[string]any), nil
	}
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, title, LEFT(content, $2) AS snippet FROM knowledge_articles WHERE tenant_id=$4 AND (title ILIKE $1 OR content ILIKE $1) LIMIT $3`,
		"%"+query+"%", snippetLen, limit, tenant)
	if err != nil {
		return nil, err
	}
	items, err := scanKnowledgeRows(rows)
	if err != nil {
		return nil, err
	}
	ts.searchCache.set(ikey, items)
	return items, nil
}

// scanKnowledgeRows 扫描知识库查询结果, 返回统一的 []map 结构(含 id/title/snippet)。
func scanKnowledgeRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, title, snippet string
		if err := rows.Scan(&id, &title, &snippet); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "title": title, "snippet": snippet})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// cachedEmbed 返回 query 的向量, 优先用 embedCache 去重(避免重复调用 LLM embedding 接口);
// 未命中则调用 embedder 并回填缓存。embedder 失败/返回空向量时返回错误, 交由调用方回退 ILIKE。
func (ts *MCPToolSet) cachedEmbed(ctx context.Context, query string) ([]float32, error) {
	if cv, ok := ts.embedCache.get(query); ok {
		return cv.([]float32), nil
	}
	qv, err := ts.embedder.Embed(ctx, query)
	if err != nil || len(qv) == 0 {
		return nil, err
	}
	ts.embedCache.set(query, qv)
	return qv, nil
}

// searchCacheKey 生成检索结果缓存键, 含租户/查询/检索模式/分页参数, 避免跨租户与跨参数串味。
func (ts *MCPToolSet) searchCacheKey(tenant, query, mode string, limit, snippetLen int) string {
	return tenant + "\x1f" + mode + "\x1f" + query + "\x1f" + strconv.Itoa(limit) + "\x1f" + strconv.Itoa(snippetLen)
}

// Register 将所有运维工具注册到 MCP Registry
func (ts *MCPToolSet) Register(reg *mcp.Registry) {
	reg.Register(mcp.ToolDefinition{Name: "cmdb_query", Description: "Query CMDB assets by name or type. Args: query(string)", Handler: ts.cmdbQuery})
	reg.Register(mcp.ToolDefinition{Name: "ticket_create", Description: "Create an ITSM ticket. Args: title(string), description(string), risk_level(string)", Handler: ts.ticketCreate})
	reg.Register(mcp.ToolDefinition{Name: "script_run", Description: "Execute an automation script on a managed host and return its output. Args: script_id(string) or script_name(string); agent_id(string) or agent_name(string) REQUIRED target host (auto-selection of any online agent is disabled for safety); dry_run(bool) optional safe preview without execution; params(object) optional; timeout(number) seconds; run_as(string) optional. Scripts not on the approved allowlist require human approval and return status=pending_approval with a ticket_id.", Handler: ts.scriptRun})
	reg.Register(mcp.ToolDefinition{Name: "knowledge_search", Description: "Search knowledge base articles for relevant documentation (RAG, semantic vector search). Args: query(string). Each returned item has an 'id' field — when you base your answer on an article, cite it inline as [^<id>] and list it under '## 引用来源'.", Handler: ts.knowledgeSearch})
}

// RAGContext 为给定查询检索知识库上下文（用于注入 LLM system prompt）
func (ts *MCPToolSet) RAGContext(ctx context.Context, query string) string {
	if ts.db == nil || query == "" {
		return ""
	}
	items, err := ts.searchKnowledge(ctx, query, 3, 300)
	if err != nil || len(items) == 0 {
		return ""
	}
	var parts []string
	for _, it := range items {
		id, _ := it["id"].(string)
		title, _ := it["title"].(string)
		snippet, _ := it["snippet"].(string)
		// 在标题行附带文章 ID, 使模型能以 [^<id>] 引用(实现 RAG grounding)。
		heading := title
		if id != "" {
			heading = fmt.Sprintf("[%s] %s", id, title)
		}
		parts = append(parts, fmt.Sprintf("## %s\n%s", heading, snippet))
	}
	return "\n\n--- 知识库相关文档 ---\n" + strings.Join(parts, "\n\n")
}

// vectorLiteral 将向量序列化为 pgvector 文本格式 '[0.1,0.2,...]'。
func vectorLiteral(v []float32) string {
	b := make([]string, len(v))
	for i, x := range v {
		b[i] = strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return "[" + strings.Join(b, ",") + "]"
}
