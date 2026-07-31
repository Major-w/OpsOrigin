package agentic

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/command"
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

	// externalApproval 外部审批通道(engine 层注入)。
	// 当脚本不在 approvedScripts 白名单内时, 优先询问外部审批器:
	//   - 返回 (true, _)  视为已获批准, 直接执行;
	//   - 返回 (false, reason) 且 reason 非空, 视为被拒绝, 返回 rejected 而非建单;
	//   - 返回 (false, "")      视为外部未介入, 回退到"建 ITSM 变更单"兜底。
	externalApproval func(ctx context.Context, scriptID, agentID string, args map[string]any) (approved bool, reason string)

	// categoriesResolver 按 Agent 解析其绑定的知识库分类(PRD 5.2.6)。
	// 由 main.go 注入 engine.AgentKnowledgeCategories; nil 表示不启用绑定过滤(检索全部)。
	categoriesResolver func(agentID string) []string

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

// SetExternalApproval 注入外部审批通道(engine 层交互式审批)。
// 未注入时保持原有行为: 白名单外脚本一律建 ITSM 变更单等待离线审批。
func (ts *MCPToolSet) SetExternalApproval(fn func(ctx context.Context, scriptID, agentID string, args map[string]any) (approved bool, reason string)) {
	ts.externalApproval = fn
}

// SetKnowledgeCategoriesResolver 注入按 Agent 解析知识库分类绑定的函数。
// 未注入时 RAG 检索不按分类过滤(绑定全部, 兼容旧行为)。
func (ts *MCPToolSet) SetKnowledgeCategoriesResolver(fn func(agentID string) []string) {
	ts.categoriesResolver = fn
}

// categoriesFor 解析 Agent 绑定的知识库分类(可能为 nil=全部)。
func (ts *MCPToolSet) categoriesFor(agentID string) []string {
	if ts.categoriesResolver != nil {
		return ts.categoriesResolver(agentID)
	}
	return nil
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

	// 审批闸门: 未列入白名单的脚本须经审批。
	// 优先级: ① 白名单直通 → ② 外部交互式审批(engine, 支持用户即时批准/拒绝) → ③ 建 ITSM 变更单(离线兜底)。
	approved := ts.isScriptApproved(scriptID, args)
	if !approved && ts.externalApproval != nil {
		a, reason := ts.externalApproval(ctx, scriptID, agentID, args)
		if a {
			approved = true
		} else if reason != "" {
			return map[string]any{
				"script_id": scriptID,
				"agent_id":  agentID,
				"status":    "rejected",
				"message":   reason,
			}, nil
		}
		// reason=="" 表示外部未介入 → 落到兜底建单
	}
	if !approved {
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
	result := map[string]any{
		"script_id": scriptID, "agent_id": agentID, "exit_code": exitCode,
		"status": "success", "output": output,
	}
	// 输出为合法 JSON 时附加结构化字段, 供 LLM 直接分析数值/对象而非面对文本墙
	if parsed, ok := tryParseJSONOutput(output); ok {
		result["output_parsed"] = parsed
	}
	return result, nil
}

// scriptList 枚举当前租户下已审批(approved)的脚本, 供 LLM 决策前"看见"脚本库。
// 安全红线: 只返回 approved 状态的脚本(draft/pending/rejected 对 AI 不可见)。
// 列表为摘要(含输入参数 name/type/required), 完整 Schema 走 script_detail。
func (ts *MCPToolSet) scriptList(ctx context.Context, args map[string]any) (any, error) {
	if ts.db == nil {
		return nil, fmt.Errorf("script store not wired")
	}
	typ, _ := args["type"].(string)
	keyword, _ := args["keyword"].(string)
	label, _ := args["label"].(string)

	q := `SELECT id, name, type, description, input_params, labels, version FROM automation_scripts WHERE tenant_id=$1 AND approval_status='approved'`
	params := []any{tenantOf(ctx)}
	idx := 2
	if typ != "" {
		q += fmt.Sprintf(` AND type=$%d`, idx)
		params = append(params, typ)
		idx++
	}
	if keyword != "" {
		q += fmt.Sprintf(` AND (name ILIKE $%d OR description ILIKE $%d)`, idx, idx+1)
		params = append(params, "%"+keyword+"%", "%"+keyword+"%")
		idx += 2
	}
	if label != "" {
		// labels 为 JSON 对象文本, 用 ILIKE 粗匹配标签键值
		q += fmt.Sprintf(` AND labels::text ILIKE $%d`, idx)
		params = append(params, "%"+label+"%")
		idx++
	}
	q += fmt.Sprintf(` ORDER BY name LIMIT $%d`, idx)
	params = append(params, 50)

	rows, err := ts.db.QueryContext(ctx, q, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, name, stype string
		var desc, inp, lbl sql.NullString
		var ver int
		if err := rows.Scan(&id, &name, &stype, &desc, &inp, &lbl, &ver); err != nil {
			return nil, err
		}
		// 摘要参数列表: 只保留 name/type/required, 详情交给 script_detail
		var params2 []map[string]any
		if inp.Valid && inp.String != "" && inp.String != "[]" {
			var defs []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			}
			if json.Unmarshal([]byte(inp.String), &defs) == nil {
				for _, d := range defs {
					params2 = append(params2, map[string]any{"name": d.Name, "type": d.Type, "required": d.Required})
				}
			}
		}
		labelsMap := map[string]string{}
		if lbl.Valid && lbl.String != "" && lbl.String != "{}" {
			_ = json.Unmarshal([]byte(lbl.String), &labelsMap)
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "type": stype, "description": desc.String,
			"version": ver, "input_params": params2, "labels": labelsMap,
		})
	}
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// scriptDetail 返回单个已审批脚本的完整定义(内容/输入输出参数 Schema/超时/运行身份/标签),
// 供 LLM 决定如何传参调用 script_run。
func (ts *MCPToolSet) scriptDetail(ctx context.Context, args map[string]any) (any, error) {
	if ts.db == nil {
		return nil, fmt.Errorf("script store not wired")
	}
	scriptID, _ := args["script_id"].(string)
	if scriptID == "" {
		name, _ := args["script_name"].(string)
		if name != "" {
			ts.db.QueryRowContext(ctx,
				`SELECT id FROM automation_scripts WHERE tenant_id=$2 AND approval_status='approved' AND name ILIKE $1 LIMIT 1`,
				"%"+name+"%", tenantOf(ctx)).Scan(&scriptID)
		}
	}
	if scriptID == "" {
		return nil, fmt.Errorf("script_id or script_name required")
	}

	var id, name, stype string
	var content, desc, inp, outp, runAs, lbl sql.NullString
	var timeout, ver int
	err := ts.db.QueryRowContext(ctx,
		`SELECT id, name, type, content, description, input_params, output_params, timeout, run_as, labels, version
		 FROM automation_scripts WHERE tenant_id=$1 AND id=$2 AND approval_status='approved'`,
		tenantOf(ctx), scriptID).
		Scan(&id, &name, &stype, &content, &desc, &inp, &outp, &timeout, &runAs, &lbl, &ver)
	if err == sql.ErrNoRows {
		return map[string]any{
			"script_id": scriptID, "status": "not_found",
			"message": "script not found or not approved (only approved scripts are visible to AI)",
		}, nil
	}
	if err != nil {
		return nil, err
	}

	inputParams := []map[string]any{}
	if inp.Valid && inp.String != "" && inp.String != "[]" {
		var defs []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Required    bool   `json:"required"`
			Description string `json:"description"`
			Default     string `json:"default"`
		}
		if json.Unmarshal([]byte(inp.String), &defs) == nil {
			for _, d := range defs {
				inputParams = append(inputParams, map[string]any{
					"name": d.Name, "type": d.Type, "required": d.Required,
					"description": d.Description, "default": d.Default,
				})
			}
		}
	}
	outputParams := []map[string]any{}
	if outp.Valid && outp.String != "" && outp.String != "[]" {
		var defs []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Description string `json:"description"`
		}
		if json.Unmarshal([]byte(outp.String), &defs) == nil {
			for _, d := range defs {
				outputParams = append(outputParams, map[string]any{"name": d.Name, "type": d.Type, "description": d.Description})
			}
		}
	}
	labelsMap := map[string]string{}
	if lbl.Valid && lbl.String != "" && lbl.String != "{}" {
		_ = json.Unmarshal([]byte(lbl.String), &labelsMap)
	}

	return map[string]any{
		"id": id, "name": name, "type": stype, "version": ver,
		"description": desc.String, "content": content.String, "labels": labelsMap,
		"timeout": timeout, "run_as": runAs.String,
		"input_params": inputParams, "output_params": outputParams,
	}, nil
}

// scriptExecutions 查询脚本执行历史(最近 20 条), 供 AI 对比"上次跑的结果"、判断失败模式。
// 可按 script_id / status 过滤; 输出截断至 2000 字符防止撑爆上下文。
func (ts *MCPToolSet) scriptExecutions(ctx context.Context, args map[string]any) (any, error) {
	if ts.db == nil {
		return nil, fmt.Errorf("script store not wired")
	}
	scriptID, _ := args["script_id"].(string)
	status, _ := args["status"].(string)
	limit := 20
	if n, ok := args["limit"].(float64); ok && n > 0 && int(n) <= 50 {
		limit = int(n)
	}

	q := `SELECT e.id, e.script_id, COALESCE(s.name,''), e.status, e.exit_code, e.duration_ms, LEFT(e.output,$2), COALESCE(e.error,''), e.started_at
	      FROM automation_script_executions e LEFT JOIN automation_scripts s ON s.id = e.script_id
	      WHERE e.tenant_id=$1`
	params := []any{tenantOf(ctx), 2000}
	idx := 3
	if scriptID != "" {
		q += fmt.Sprintf(` AND e.script_id=$%d`, idx)
		params = append(params, scriptID)
		idx++
	}
	if status != "" {
		q += fmt.Sprintf(` AND e.status=$%d`, idx)
		params = append(params, status)
		idx++
	}
	q += fmt.Sprintf(` ORDER BY e.started_at DESC LIMIT $%d`, idx)
	params = append(params, limit)

	rows, err := ts.db.QueryContext(ctx, q, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, sid, sname, st, errStr string
		var exitCode, dur int64
		var output sql.NullString
		var startedAt time.Time
		if err := rows.Scan(&id, &sid, &sname, &st, &exitCode, &dur, &output, &errStr, &startedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "script_id": sid, "script_name": sname, "status": st,
			"exit_code": exitCode, "duration_ms": dur, "output": output.String,
			"error": errStr, "started_at": startedAt.Format(time.RFC3339),
		})
	}
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// tryParseJSONOutput 尝试把脚本输出解析为结构化 JSON(供 AI 直接读取, 而非面对文本墙)。
// 仅当输出是合法非空 JSON 时返回 (value, true)。
func tryParseJSONOutput(output string) (any, bool) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil || v == nil {
		return nil, false
	}
	return v, true
}

// scriptGenerate AI 生成脚本并落库为 draft(需人工审批后才可执行)。
// 安全红线(生成层):
//   - 高危命令硬拦截: 命中沙箱禁止规则直接拒绝, 不落库;
//   - 语法校验: shell 用 bash/sh -n, python 用 py 编译(只解析不执行);
//   - 落库状态恒为 draft, AI 的 script_list/script_detail/script_run 均不可见/不可执行, 人工审批后才 approved;
//   - dry_run=true 时仅校验不落库(安全预览)。
func (ts *MCPToolSet) scriptGenerate(ctx context.Context, args map[string]any) (any, error) {
	name, _ := args["name"].(string)
	stype, _ := args["type"].(string)
	content, _ := args["content"].(string)
	desc, _ := args["description"].(string)
	if name == "" || content == "" {
		return nil, fmt.Errorf("name and content are required")
	}
	if stype != "shell" && stype != "python" && stype != "bat" {
		return nil, fmt.Errorf("type must be one of: shell, python, bat")
	}
	if len(content) > 200_000 {
		return nil, fmt.Errorf("script content too large (max 200KB)")
	}

	dry, _ := args["dry_run"].(bool)

	// —— ① 高危命令硬拦截 ——
	blocked := command.BlockedInSandbox(content)
	if len(blocked) > 0 {
		items := make([]map[string]any, 0, len(blocked))
		for _, m := range blocked {
			items = append(items, map[string]any{
				"rule_id": m.RuleID, "level": m.Level, "description": m.Desc,
				"line": m.Line, "match": m.Match,
			})
		}
		return map[string]any{
			"status": "blocked", "name": name,
			"message": "script blocked: contains commands forbidden in sandbox (escape/destructive to host); no script saved",
			"violations": items,
		}, nil
	}

	// —— ② 参数 Schema 合法性 ——
	inputParams := parseParamDefs(args["input_params"], true)
	outputParams := parseParamDefs(args["output_params"], false)
	for _, p := range inputParams {
		if p.Name == "" {
			return nil, fmt.Errorf("input_params entries must have a non-empty name")
		}
	}

	// —— ③ 语法校验(只解析, 不执行) ——
	syntaxOK, syntaxNote := syntaxCheck(stype, content)
	if !syntaxOK {
		return map[string]any{
			"status": "syntax_error", "name": name,
			"message": "script syntax check failed; fix the script and retry: " + syntaxNote,
			"syntax_error": syntaxNote,
		}, nil
	}

	// —— ④ 名称查重(同租户) ——
	var existingID string
	ts.db.QueryRowContext(ctx, `SELECT id FROM automation_scripts WHERE tenant_id=$1 AND name=$2 LIMIT 1`,
		tenantOf(ctx), name).Scan(&existingID)
	if existingID != "" && !dry {
		return map[string]any{
			"status": "name_conflict", "name": name, "existing_script_id": existingID,
			"message": "a script with this name already exists; choose a different name or update the existing script",
		}, nil
	}

	timeout := 300
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = int(t)
	}
	runAs, _ := args["run_as"].(string)

	if dry {
		return map[string]any{
			"status": "validated", "name": name,
			"message": "dry-run: script passed safety scan and syntax check; not saved",
			"checks": map[string]any{"sandbox_scan": "passed", "syntax": syntaxNote},
		}, nil
	}

	// —— ⑤ 落库为 draft(人工审批后才可执行) ——
	if ts.db == nil {
		return nil, fmt.Errorf("script store not wired")
	}
	scriptID := uuid.New().String()
	labelsJSON := "{}"
	if labels, ok := args["labels"].(map[string]any); ok && len(labels) > 0 {
		if b, err := json.Marshal(labels); err == nil {
			labelsJSON = string(b)
		}
	}
	ipJSON, _ := json.Marshal(inputParams)
	opJSON, _ := json.Marshal(outputParams)
	now := time.Now()

	_, err := ts.db.ExecContext(ctx,
		`INSERT INTO automation_scripts
		 (id, tenant_id, name, type, content, description, input_params, output_params, timeout, run_as, labels, version, public, approval_status, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,false,'draft',$12,$12)`,
		scriptID, tenantOf(ctx), name, stype, content, desc,
		string(ipJSON), string(opJSON), timeout, runAs, labelsJSON, now)
	if err != nil {
		return nil, fmt.Errorf("save generated script: %w", err)
	}
	slog.Info("ai generated script saved as draft", "script_id", scriptID, "name", name)

	return map[string]any{
		"status": "draft_created", "script_id": scriptID, "name": name,
		"message": "script saved as draft; it requires human approval before it can be executed (approved scripts become visible to script_list/script_run)",
		"checks":  map[string]any{"sandbox_scan": "passed", "syntax": syntaxNote},
	}, nil
}

// paramDef 与 automation/script 的 ParamDef 对应的本地解析结构。
type paramDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

// parseParamDefs 把 args 中的参数定义数组解析为 paramDef 列表(容错非标准输入)。
func parseParamDefs(raw any, ensureType bool) []paramDef {
	var out []paramDef
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				d := paramDef{
					Name:        str(m["name"]),
					Type:        str(m["type"]),
					Description: str(m["description"]),
					Default:     str(m["default"]),
				}
				if b, ok := m["required"].(bool); ok {
					d.Required = b
				}
				if d.Type == "" && ensureType {
					d.Type = "string"
				}
				out = append(out, d)
			}
		}
	}
	return out
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// syntaxCheck 对脚本做语法级校验(只解析, 不执行)。返回 (是否通过, 说明)。
func syntaxCheck(stype, content string) (bool, string) {
	switch stype {
	case "shell":
		bin := "bash"
		if _, err := exec.LookPath(bin); err != nil {
			bin = "sh"
		}
		cmd := exec.Command(bin, "-n")
		cmd.Stdin = strings.NewReader(content)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return false, fmt.Sprintf("%s -n: %s", bin, strings.TrimSpace(stderr.String()))
		}
		return true, bin + " -n passed"
	case "python":
		if _, err := exec.LookPath("python3"); err != nil {
			return true, "python3 not available, syntax check skipped"
		}
		cmd := exec.Command("python3", "-c", "import sys; compile(sys.stdin.read(), '<generated>', 'exec')")
		cmd.Stdin = strings.NewReader(content)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return false, strings.TrimSpace(stderr.String())
		}
		return true, "python3 compile passed"
	default:
		return true, "no syntax check for type " + stype
	}
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
	// 按发起会话的 Agent 绑定分类过滤(PRD 5.2.6); 未绑定/非 Agent 会话时检索全部。
	categories := ts.categoriesFor(agentIDFrom(ctx))
	items, err := ts.searchKnowledge(ctx, query, 5, 500, categories)
	if err != nil {
		return nil, err
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

// searchKnowledge 块级双路召回 + RRF 融合 + 文章聚合(2026-07-31 P0 增强):
//   - 路 1: 块级向量检索(knowledge_chunks.embedding, 标题感知分块后每块独立向量);
//   - 路 2: 块级关键词检索(标题/块内容 ILIKE);
//   - 双路按 RRF(Reciprocal Rank Fusion) 融合, 命中块聚合回文章;
//   - 双路全空时回退文章级 ILIKE(兼容未升级/未 reindex 的存量数据)。
// categories 非空时仅在这些分类内检索(Agent 知识库绑定, PRD 5.2.6)。
// 结果经 ttlCache 缓存: embedding 去重(query->向量) + 检索结果复用。
func (ts *MCPToolSet) searchKnowledge(ctx context.Context, query string, limit, snippetLen int, categories []string) ([]map[string]any, error) {
	tenant := tenantOf(ctx)
	// 分类过滤子句(参数位固定在 $5); 空分类不追加参数。
	catClause := ""
	var catArg []string
	if len(categories) > 0 {
		catClause = " AND a.category = ANY($5)"
		catArg = categories
	}

	cacheKey := ts.searchCacheKey(tenant, query, "rrf", limit, snippetLen, categories)
	if v, ok := ts.searchCache.get(cacheKey); ok {
		AIKnowledgeCacheHits.Inc()
		return v.([]map[string]any), nil
	}

	topN := limit * 3 // 召回上限: 每路取 3 倍, RRF 融合后截断到 limit
	var vecHits, kwHits []knowledgeHit

	// —— 路 1: 块级向量检索 ——
	if ts.embedder != nil {
		qv, embedErr := ts.cachedEmbed(ctx, query)
		if embedErr == nil && len(qv) > 0 {
			lit := vectorLiteral(qv)
			qArgs := []any{lit, snippetLen, topN, tenant}
			if catArg != nil {
				qArgs = append(qArgs, catArg)
			}
			if rows, qerr := ts.db.QueryContext(ctx,
				`SELECT c.article_id, a.title, c.heading, LEFT(c.content, $2) AS snippet
				 FROM knowledge_chunks c
				 JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
				 WHERE c.tenant_id=$4 AND c.embedding IS NOT NULL`+catClause+`
				 ORDER BY c.embedding <=> $1::vector LIMIT $3`,
				qArgs...); qerr == nil {
				vecHits, _ = scanChunkHits(rows)
			} else {
				slog.Warn("knowledge: chunk vector search failed", "err", qerr)
			}
		}
	}

	// —— 路 2: 块级关键词检索(标题命中同样计入) ——
	iArgs := []any{"%" + query + "%", snippetLen, topN, tenant}
	if catArg != nil {
		iArgs = append(iArgs, catArg)
	}
	if rows, qerr := ts.db.QueryContext(ctx,
		`SELECT c.article_id, a.title, c.heading, LEFT(c.content, $2) AS snippet
		 FROM knowledge_chunks c
		 JOIN knowledge_articles a ON a.id = c.article_id AND a.tenant_id = c.tenant_id
		 WHERE c.tenant_id=$4 AND (a.title ILIKE $1 OR c.content ILIKE $1)`+catClause+` LIMIT $3`,
		iArgs...); qerr == nil {
		kwHits, _ = scanChunkHits(rows)
	} else {
		slog.Warn("knowledge: chunk keyword search failed", "err", qerr)
	}

	// —— 双路全空: 回退文章级 ILIKE(兼容未升级/未 reindex 的存量数据) ——
	if len(vecHits) == 0 && len(kwHits) == 0 {
		if items, err := ts.searchKnowledgeFallback(ctx, query, limit, snippetLen, categories); err == nil {
			ts.searchCache.set(cacheKey, items)
			return items, nil
		}
	}

	items := rrfMergeChunks(vecHits, kwHits, limit)
	ts.searchCache.set(cacheKey, items)
	return items, nil
}

// knowledgeHit 是块级检索的单条命中(双路召回的统一中间结构)。
type knowledgeHit struct {
	articleID string
	title     string
	heading   string
	snippet   string
}

// scanChunkHits 扫描块级检索结果(article_id/title/heading/snippet)。
func scanChunkHits(rows *sql.Rows) ([]knowledgeHit, error) {
	defer rows.Close()
	var hits []knowledgeHit
	for rows.Next() {
		var h knowledgeHit
		var heading sql.NullString
		if err := rows.Scan(&h.articleID, &h.title, &heading, &h.snippet); err != nil {
			return nil, err
		}
		h.heading = heading.String
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// rrfMergeChunks 用 RRF 融合双路召回并按文章聚合, 返回 top-limit 篇文章。
// 每篇含 id/title/snippet(首命中块)与 matches(全部命中块, 含 heading 供引用溯源)。
func rrfMergeChunks(vec, kw []knowledgeHit, limit int) []map[string]any {
	const rrfK = 60
	type articleAgg struct {
		id      string
		title   string
		score   float64
		matches []map[string]any
		snippet string
	}
	agg := map[string]*articleAgg{}
	seenHeading := map[string]map[string]bool{} // articleID -> heading 去重

	addRanked := func(list []knowledgeHit) {
		for i, h := range list {
			a, ok := agg[h.articleID]
			if !ok {
				a = &articleAgg{id: h.articleID, title: h.title}
				agg[h.articleID] = a
				seenHeading[h.articleID] = map[string]bool{}
			}
			a.score += 1.0 / (float64(rrfK) + float64(i+1))
			if a.snippet == "" {
				a.snippet = h.snippet
			}
			// 同一文章内按 heading 去重合并命中块
			if !seenHeading[h.articleID][h.heading+"|"+h.snippet] {
				seenHeading[h.articleID][h.heading+"|"+h.snippet] = true
				a.matches = append(a.matches, map[string]any{"heading": h.heading, "snippet": h.snippet})
			}
		}
	}
	addRanked(vec)
	addRanked(kw)

	// 按 RRF 分数排序, 取 top-limit
	list := make([]*articleAgg, 0, len(agg))
	for _, a := range agg {
		list = append(list, a)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > limit {
		list = list[:limit]
	}

	items := make([]map[string]any, 0, len(list))
	for _, a := range list {
		items = append(items, map[string]any{
			"id": a.id, "title": a.title, "snippet": a.snippet,
			"matches": a.matches, "score": round3(a.score),
		})
	}
	return items
}

// searchKnowledgeFallback 文章级 ILIKE 检索(块级双路全空时的保底, 兼容未 reindex 存量)。
func (ts *MCPToolSet) searchKnowledgeFallback(ctx context.Context, query string, limit, snippetLen int, categories []string) ([]map[string]any, error) {
	tenant := tenantOf(ctx)
	catClause := ""
	var catArg []string
	if len(categories) > 0 {
		catClause = " AND category = ANY($5)"
		catArg = categories
	}
	args := []any{"%" + query + "%", snippetLen, limit, tenant}
	if catArg != nil {
		args = append(args, catArg)
	}
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, title, LEFT(content, $2) AS snippet FROM knowledge_articles
		 WHERE tenant_id=$4 AND (title ILIKE $1 OR content ILIKE $1)`+catClause+` LIMIT $3`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, title, snippet string
		if err := rows.Scan(&id, &title, &snippet); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "title": title, "snippet": snippet, "matches": []map[string]any{}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, nil
}

// round3 保留 3 位小数的 RRF 分数。
func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// scanKnowledgeRows 已废弃: 检索已升级为块级(scanChunkHits), 文章级扫描由 searchKnowledgeFallback 内联处理。

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

// searchCacheKey 生成检索结果缓存键, 含租户/查询/检索模式/分页参数/分类绑定, 避免跨租户与跨参数串味。
func (ts *MCPToolSet) searchCacheKey(tenant, query, mode string, limit, snippetLen int, categories []string) string {
	k := tenant + "\x1f" + mode + "\x1f" + query + "\x1f" + strconv.Itoa(limit) + "\x1f" + strconv.Itoa(snippetLen)
	if len(categories) > 0 {
		k += "\x1f" + strings.Join(categories, ",")
	}
	return k
}

// Register 将所有运维工具注册到 MCP Registry
func (ts *MCPToolSet) Register(reg *mcp.Registry) {
	reg.Register(mcp.ToolDefinition{Name: "cmdb_query", Description: "Query CMDB assets by name or type. Args: query(string)", Handler: ts.cmdbQuery})
	reg.Register(mcp.ToolDefinition{Name: "ticket_create", Description: "Create an ITSM ticket. Args: title(string), description(string), risk_level(string)", Handler: ts.ticketCreate})
	reg.Register(mcp.ToolDefinition{Name: "script_run", Description: "Execute an automation script on a managed host and return its output. Args: script_id(string) or script_name(string); agent_id(string) or agent_name(string) REQUIRED target host (auto-selection of any online agent is disabled for safety); dry_run(bool) optional safe preview without execution; params(object) optional; timeout(number) seconds; run_as(string) optional. Scripts not on the approved allowlist require human approval and return status=pending_approval with a ticket_id.", Handler: ts.scriptRun})
	reg.Register(mcp.ToolDefinition{Name: "script_list", Description: "List approved automation scripts available to you (enumerate the script library). Args: type(string) optional filter by script type; keyword(string) optional match name/description; label(string) optional match labels. Returns id/name/type/description/version/input_params summary — call script_detail for full parameter schema before running.", Handler: ts.scriptList})
	reg.Register(mcp.ToolDefinition{Name: "script_detail", Description: "Get the full definition of one approved automation script: content, input_params schema (name/type/required/description/default), output_params, timeout, run_as, labels. Args: script_id(string) or script_name(string). Use before script_run to build correct params.", Handler: ts.scriptDetail})
	reg.Register(mcp.ToolDefinition{Name: "script_executions", Description: "Query recent execution history of automation scripts (latest 20). Args: script_id(string) optional filter by script; status(string) optional filter like success/failed; limit(number) optional 1-50. Returns id/script_name/status/exit_code/duration_ms/output(truncated)/error/started_at. Use to compare previous runs or diagnose failures.", Handler: ts.scriptExecutions})
	reg.Register(mcp.ToolDefinition{Name: "script_generate", Description: "Generate an automation script and save it as DRAFT (requires human approval before it can run). Args: name(string) REQUIRED; type(string) REQUIRED one of shell|python|bat; content(string) REQUIRED the script body; description(string) optional; input_params(array) optional [{name,type,required,description,default}]; output_params(array) optional; timeout(number) optional seconds; run_as(string) optional; labels(object) optional; dry_run(bool) optional if true only validate, do not save. Safety: blocked scripts (rm -rf /, dd/mkfs, shutdown, curl|bash etc.) are rejected without saving; syntax is checked before saving.", Handler: ts.scriptGenerate})
	reg.Register(mcp.ToolDefinition{Name: "knowledge_search", Description: "Search knowledge base articles for relevant documentation (RAG, semantic vector search). Args: query(string). Each returned item has an 'id' field — when you base your answer on an article, cite it inline as [^<id>] and list it under '## 引用来源'.", Handler: ts.knowledgeSearch})
}

// RAGContext 为给定查询检索知识库上下文(用于注入 LLM system prompt)。
// agentID 非空时按该 Agent 绑定的知识库分类过滤检索(PRD 5.2.6), 空则检索全部。
func (ts *MCPToolSet) RAGContext(ctx context.Context, query, agentID string) string {
	if ts.db == nil || query == "" {
		return ""
	}
	categories := ts.categoriesFor(agentID)
	items, err := ts.searchKnowledge(ctx, query, 3, 300, categories)
	if err != nil || len(items) == 0 {
		return ""
	}
	var parts []string
	for _, it := range items {
		id, _ := it["id"].(string)
		title, _ := it["title"].(string)
		// 在标题行附带文章 ID, 使模型能以 [^<id>] 引用(实现 RAG grounding)。
		heading := title
		if id != "" {
			heading = fmt.Sprintf("[%s] %s", id, title)
		}
		// 块级命中(matches)优先: 带标题路径的段落级片段, 引用更精确。
		if matches, ok := it["matches"].([]map[string]any); ok && len(matches) > 0 {
			for _, m := range matches {
				ch, _ := m["heading"].(string)
				snip, _ := m["snippet"].(string)
				sec := heading
				if ch != "" {
					sec = fmt.Sprintf("%s / %s", heading, ch)
				}
				parts = append(parts, fmt.Sprintf("## %s\n%s", sec, snip))
			}
			continue
		}
		snippet, _ := it["snippet"].(string)
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
