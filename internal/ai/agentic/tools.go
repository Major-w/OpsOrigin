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
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/knowledge"
	"github.com/opsorigin/core/internal/objectstore"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
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

	// cfg 全局配置指针(image_generate 等需要从 sysconfig 读 LLM provider key/base_url 的工具用)。
	// 由 main.go 在 NewMCPToolSet 后注入; sysconfig 更新通过同一指针共享, 无需再同步。
	cfg *config.Config

	// objStore 对象存储(image_generate 生成的图片持久化到 MinIO, 避免智谱 CDN URL 24h 过期)。
	// nil 时优雅降级: 直接返回上游 CDN URL(与未接入 MinIO 时行为一致)。
	objStore *objectstore.Client

	// sandboxRoot 代码编写类工具(coding_*)的目录沙箱根: 只能操作此目录子树, 防止越权读写本仓库外文件。
	// 空=未配置, coding 写/执行类工具将直接拒绝(只读类亦拒绝, fail-closed)。
	sandboxRoot string
	// codingApproval 代码编写工具(写/执行类)的交互式人工审批器; 未注入时写/执行类一律拒绝(fail-closed)。
	// 由 engine.CodingApprovalChecker 注入, 复用同一套 SSE ask 事件 + 超时 fail-closed 机制。
	codingApproval func(ctx context.Context, tool, agentID string, args map[string]any, summary string) (approved bool, reason string)

	// searchLogs 知识库检索留痕(P1-2/P1-4): 每次召回后写一条 knowledge_search_logs。
	// 由 main.go 注入知识库 Service 的留痕存储; nil(未注入/内存模式)时只统计指标不落库。
	searchLogs *knowledge.SearchLogStore

	// scopeResolver 可见性范围解析器(权限感知检索)。由 main.go 注入 iam 适配器。
	// nil 时 resolveScope 返回零值 scope → 谓词退化为「仅公开」, 即改造前的行为(fail-closed)。
	scopeResolver ScopeResolver

	// 反馈闭环(检索降权): penaltyProvider 提供「租户→文章净踩数」映射。
	// nil(未注入)时检索不做反馈降权。缓存与取数逻辑见 retrieval_feedback.go。
	penaltyMu       sync.Mutex
	penaltyProvider ArticlePenaltyProvider
	penaltyCache    map[string]penaltyCacheEntry
}

func NewMCPToolSet(db *sql.DB) *MCPToolSet {
	return &MCPToolSet{
		db:           db,
		embedCache:   newTTLCache(embedCacheTTL, 1024),
		searchCache:  newTTLCache(searchCacheTTL, 1024),
		penaltyCache: map[string]penaltyCacheEntry{},
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

// SetConfig 注入全局配置指针(image_generate 等需要从 sysconfig 读取 LLM provider key/base_url 的工具使用)。
// 同一指针在 sysconfig 更新时被原地修改字段, 故无需重复同步。
func (ts *MCPToolSet) SetConfig(cfg *config.Config) { ts.cfg = cfg }

// SetCodingSandbox 设置代码编写类工具的目录沙箱根(coding_* 工具只能操作此目录子树)。
// 未设置时coding 工具全部拒绝(fail-closed), 确保不会在任意目录读写文件。
func (ts *MCPToolSet) SetCodingSandbox(root string) { ts.sandboxRoot = root }

// SetCodingApproval 注入代码编写工具(写/执行类)的交互式人工审批器。
// 未注入时写/执行类工具直接拒绝(fail-closed), 只读类不受审批约束但仍受 sandboxRoot 限定。
func (ts *MCPToolSet) SetCodingApproval(fn func(ctx context.Context, tool, agentID string, args map[string]any, summary string) (approved bool, reason string)) {
	ts.codingApproval = fn
}

// SetObjectStore 注入对象存储(image_generate 生成的图片持久化到 MinIO, 避免 CDN URL 过期)。
// nil 安全: 未注入时 image_generate 直接返回上游 CDN URL。
func (ts *MCPToolSet) SetObjectStore(os *objectstore.Client) { ts.objStore = os }

// SetSearchLogStore 注入知识库检索留痕存储(P1-2/P1-4)。
// 未注入时检索照常工作, 只是不落 knowledge_search_logs(指标仍然统计)。
func (ts *MCPToolSet) SetSearchLogStore(s *knowledge.SearchLogStore) { ts.searchLogs = s }

// FlushSearchCache 清空知识库检索结果缓存。
//
// 必要性(可见性修复的配套): 检索结果按 (租户,查询,参数...) 缓存 5 分钟。文章一旦
// 转为草稿/归档、到期下架、删除或改动可见性, 同一查询的旧结果仍会被继续返回,
// 最长 5 分钟内受限内容照样进入模型上下文 —— 相当于把 fail-closed 的可见性过滤
// 打了个时间窗口。知识库发生写操作时调用本方法即可让缓存立即失效。
//
// 只清 searchCache: embedCache 是 query→向量 的映射, 与内容可见性无关, 保留其去重收益。
func (ts *MCPToolSet) FlushSearchCache() {
	if ts.searchCache != nil {
		ts.searchCache.clear()
	}
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
			"message":    "script blocked: contains commands forbidden in sandbox (escape/destructive to host); no script saved",
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
			"message":      "script syntax check failed; fix the script and retry: " + syntaxNote,
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
			"checks":  map[string]any{"sandbox_scan": "passed", "syntax": syntaxNote},
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
	// 回传窗口给足: 块约 600 token, 截到 500 字符会让 LLM 只看到开头三分之一。
	items, err := ts.searchKnowledge(ctx, query, 5, kbChunkContextRunes, categories)
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
//
// categories 非空时仅在这些分类内检索(Agent 知识库绑定, PRD 5.2.6)。
// 结果经 ttlCache 缓存: embedding 去重(query->向量) + 检索结果复用。
// ValidKnowledgeIDs 批量校验知识库文章 ID 在本租户内是否真实存在。
// 用于过滤模型在回答里标注的幻觉引用(编造/已删除文章的 [^id]), 避免前端引用来源链接到不存在的文章。
// 校验口径与检索一致(已发布/未过期/提问者可见), 避免草稿或受限文章被引用后仍被判定为有效来源。
//
// ⚠️ 这条路径必须与检索用同一个可见性谓词: 它决定模型回答里的 [^kb:id] 引用是否被判有效。
// 若这里放宽(例如只判存在), 受限文章的标题与 ID 会通过模型输出泄漏给提问者。
func (ts *MCPToolSet) ValidKnowledgeIDs(ctx context.Context, tenant string, ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	if ts.db == nil || tenant == "" || len(ids) == 0 {
		return out
	}
	sa := newScopeArgs(ts.resolveScope(ctx), tenant, ids)
	rows, err := ts.db.QueryContext(ctx,
		`SELECT a.id FROM knowledge_articles a WHERE a.tenant_id=$1 AND a.id = ANY($2)`+sa.Predicate(),
		sa.Args()...)
	if err != nil {
		slog.Warn("knowledge: validate citation ids failed", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out[id] = true
		}
	}
	return out
}

// 知识块回传上下文的容量预算(单位 rune)。
// 此前 SQL 用 LEFT(content,500) 硬截断: 块本身约 600 token, 只给 LLM 前 1/3 且从头截而非命中处开窗,
// 答案容易断章取义。现在回传完整块(超长时按命中位置开窗), 相邻块各补一段保证上下文完整。
const (
	kbChunkContextRunes       = 1200 // 单块回传上限
	kbNeighborRunes           = 200  // 相邻块各补充的长度
	kbRAGMaxMatchesPerArticle = 2    // 注入 prompt 时每篇文章最多取几个命中块
)

// 可见性谓词已迁到 scope.go: 常量 kbPublicOnlyPredicate(退化分支) + scopeArgs(按身份生成)。
// 见 scope.go「权限感知检索」段的完整说明。

// searchKnowledge 生产检索入口: searchKnowledgeExplained 的默认参数包装。
//
// 实现体见 retrieval_explain.go —— 检索测试台复用同一个函数, 保证「测试台看到的检索」
// 就是「线上跑的检索」。此处只保留入口, 避免出现第二份检索逻辑。
func (ts *MCPToolSet) searchKnowledge(ctx context.Context, query string, limit, snippetLen int, categories []string) ([]map[string]any, error) {
	items, _, err := ts.searchKnowledgeExplained(ctx, query, limit, snippetLen, RetrieveOptions{Categories: categories})
	return items, err
}

// searchTrace 一次检索的观测快照(指标 + 留痕共用)。
type searchTrace struct {
	mode        string
	query       string
	tenant      string
	categories  []string
	started     time.Time
	vecHits     int // 块级向量路命中块数(融合前)
	kwHits      int // 块级关键词路命中块数(tsvector+ILIKE 合并, 融合前)
	resultCount int // 融合后返回的文章数
	topScore    float64
}

// recordSearch 记录一次检索的指标与留痕(P1-2/P1-4)。
//
// 指标永远统计; 留痕落库为 best-effort —— 留痕是观测数据, 写失败只告警,
// 绝不能因为它让一次知识库检索(乃至整个对话)失败。
//
// noLog 为真时整段跳过(含指标): 检索测试台是人工探测, 混进指标会污染「线上零结果率」
// 这类基线, 也会让留痕里出现一堆并非真实用户提问的查询。
func (ts *MCPToolSet) recordSearch(ctx context.Context, tr searchTrace, noLog bool) {
	if noLog {
		return
	}
	elapsed := time.Since(tr.started)
	mode := tr.mode
	if mode == "" {
		mode = "rrf"
	}
	AIKnowledgeSearchTotal.WithLabelValues(mode).Inc()
	AIKnowledgeSearchLatency.WithLabelValues(mode).Observe(elapsed.Seconds())
	AIKnowledgeSearchHits.Observe(float64(tr.resultCount))
	if tr.vecHits > 0 {
		AIKnowledgeSearchRouteHits.WithLabelValues("vector").Inc()
	}
	if tr.kwHits > 0 {
		AIKnowledgeSearchRouteHits.WithLabelValues("keyword").Inc()
	}
	if mode == "fallback" {
		AIKnowledgeSearchRouteHits.WithLabelValues("fallback").Inc()
	}
	zero := tr.resultCount == 0
	if zero {
		AIKnowledgeSearchZeroResults.WithLabelValues(mode).Inc()
	}
	if ts.searchLogs == nil {
		return
	}
	rec := &knowledge.SearchLog{
		TenantID:   tr.tenant,
		Query:      tr.query,
		Mode:       mode,
		Categories: tr.categories,
		VecHits:    tr.vecHits,
		KwHits:     tr.kwHits,
		HitCount:   tr.resultCount,
		ZeroResult: zero,
		LatencyMS:  float64(elapsed.Microseconds()) / 1000,
		TopScore:   tr.topScore,
		AgentID:    agentIDFrom(ctx),
		UserID:     auth.UserIDFromContext(ctx),
	}
	if err := ts.searchLogs.Record(ctx, rec); err != nil {
		slog.Warn("knowledge: record search log failed", "err", err)
	}
}

// topScoreOf 取检索结果首条的融合分(无结果返回 0)。
func topScoreOf(items []map[string]any) float64 {
	if len(items) == 0 {
		return 0
	}
	switch v := items[0]["score"].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

// kbTSVUnavailable / kbTSVWarnOnce 记录 tsvector 检索是否可用(老库缺列时永久降级)。
var (
	kbTSVUnavailable atomic.Bool
	kbTSVWarnOnce    sync.Once
)

// kbHalfvecUnavailable / kbHalfvecWarnOnce 记录 halfvec 表达式检索是否可用
// (pgvector < 0.7 无 halfvec 类型时永久降级为原生 vector 距离表达式)。
var (
	kbHalfvecUnavailable atomic.Bool
	kbHalfvecWarnOnce    sync.Once
)

// vectorOrderExpr 返回向量检索的排序/距离表达式, 必须与 knowledge 侧建的 HNSW 索引表达式一致。
//
// 为什么用半精度表达式而非原生:
//  1. 原生 `USING hnsw (embedding vector_cosine_ops)` 要求列有确定维度, 而 embedding 列是
//     裸 vector(允许混维度) —— 直接建索引报 "column does not have dimensions";
//  2. 即便固化维度, 原生 HNSW 硬上限 2000 维, 而智谱 embedding-3 是 2048 维,
//     报 "column cannot have more than 2000 dimensions for hnsw index"。
//
// `embedding::halfvec(D)` 表达式索引上限 4000 维、索引体积减半, 且 <=2000 维也照常命中。
// 只要本表达式与索引表达式逐字相同(维度取查询向量长度, 与写入时的维度必然一致), 即走索引扫描。
func vectorOrderExpr(dim int) string {
	if kbHalfvecUnavailable.Load() {
		return "c.embedding <=> $1::vector"
	}
	return fmt.Sprintf("c.embedding::halfvec(%d) <=> $1::halfvec(%d)", dim, dim)
}

// knowledgeHit 是块级检索的单条命中(多路召回的统一中间结构)。
type knowledgeHit struct {
	articleID  string
	title      string
	heading    string
	content    string  // 完整块内容: 截断改在 Go 侧按命中位置开窗, 不再由 SQL LEFT() 硬截
	chunkID    string  // 命中块 ID: 供引用溯源跳原文并高亮(附件块同样带 ID)
	source     string  // article | attachment: 命中来自正文还是附件抽取
	chunkIndex int     // 块序号: 用于取相邻块补上下文
	distance   float64 // 余弦距离(0~2, 越小越近); 关键词路无距离为 -1
}

// scanChunkHits 扫描块级检索结果(article_id/title/heading/content/chunk_id/source/chunk_index/distance)。
func scanChunkHits(rows *sql.Rows) ([]knowledgeHit, error) {
	defer rows.Close()
	var hits []knowledgeHit
	for rows.Next() {
		var h knowledgeHit
		var heading sql.NullString
		var dist sql.NullFloat64
		if err := rows.Scan(&h.articleID, &h.title, &heading, &h.content, &h.chunkID, &h.source, &h.chunkIndex, &dist); err != nil {
			return nil, err
		}
		h.heading = heading.String
		h.distance = -1
		if dist.Valid {
			h.distance = dist.Float64
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// rrfMergeChunksExplained 融合多路召回并按文章聚合, 返回 top-limit 篇文章 + 分数分解明细。
// 排序不只用 RRF 名次: 叠加向量相似度、关键词命中与标题命中做轻量重排(rerank),
// 纯名次融合会把"多路都排在末尾但语义最贴合"的块压下去。
// 每篇含 id/title/snippet(首命中块)与 matches(全部命中块, 含 heading 供引用溯源),
// 首块额外附带相邻块上下文(context), 避免长流程文档回答残缺。
//
// 第二个返回值供检索测试台展示「分数怎么来的」, 生产调用方直接丢弃。
func (ts *MCPToolSet) rrfMergeChunksExplained(ctx context.Context, vec, kw []knowledgeHit, limit int, query string, snippetLen int, penalties map[string]int) ([]map[string]any, []FusionItem) {
	const rrfK = 60
	type articleAgg struct {
		id           string
		title        string
		score        float64
		matches      []map[string]any
		snippet      string
		chunkID      string  // 首个命中块的 ID(引用溯源锚点)
		anchorSource string  // 首块来源(正文/附件): 取相邻块时需按来源区分, 否则序号会撞车
		anchorIndex  int     // 首块序号, -1 表示未知
		bestDist     float64 // 最小余弦距离, -1 表示无向量命中
		kwHit        bool    // 关键词(tsvector/ILIKE)命中
		titleHit     bool    // 查询词命中文章标题
	}
	agg := map[string]*articleAgg{}
	seen := map[string]map[string]bool{} // articleID -> 去重键

	addRanked := func(list []knowledgeHit, isVec bool) {
		for i, h := range list {
			a, ok := agg[h.articleID]
			if !ok {
				a = &articleAgg{id: h.articleID, title: h.title, anchorIndex: -1, bestDist: -1}
				agg[h.articleID] = a
				seen[h.articleID] = map[string]bool{}
			}
			a.score += 1.0 / (float64(rrfK) + float64(i+1))
			if h.distance >= 0 && (a.bestDist < 0 || h.distance < a.bestDist) {
				a.bestDist = h.distance
			}
			if !isVec {
				a.kwHit = true
			}
			if containsFold(h.title, query) {
				a.titleHit = true
			}
			if a.snippet == "" {
				a.chunkID = h.chunkID // 首个(最高分)命中块作为溯源锚点
				a.anchorSource = h.source
				a.anchorIndex = h.chunkIndex
			}
			// 同一文章内按 heading+块ID 去重合并命中块
			key := h.heading + "|" + h.chunkID
			if seen[h.articleID][key] {
				continue
			}
			seen[h.articleID][key] = true
			win := kbChunkContextRunes
			if snippetLen > win {
				win = snippetLen
			}
			a.matches = append(a.matches, map[string]any{
				"heading": h.heading, "snippet": contextWindow(h.content, query, win),
				"chunk_id": h.chunkID, "source": h.source, "chunk_index": h.chunkIndex,
			})
			if a.snippet == "" {
				a.snippet = contextWindow(h.content, query, snippetLen)
			}
		}
	}
	addRanked(vec, true)
	addRanked(kw, false)

	// 按 RRF 分数排序, 取 top-limit
	list := make([]*articleAgg, 0, len(agg))
	for _, a := range agg {
		a.score += rerankBonus(a.bestDist, a.kwHit, a.titleHit) - feedbackPenalty(penalties[a.id])
		list = append(list, a)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > limit {
		list = list[:limit]
	}

	items := make([]map[string]any, 0, len(list))
	fusion := make([]FusionItem, 0, len(list))
	for i, a := range list {
		item := map[string]any{
			"id": a.id, "title": a.title, "snippet": a.snippet,
			"matches": a.matches, "score": round3(a.score),
			"chunk_id": a.chunkID, // 引用溯源: 前端可链到 /knowledge/{id}?chunk={chunk_id}
		}
		// 首块补相邻块上下文(small-to-big): 单块语义不完整时长流程文档会答残缺。
		if c := ts.anchorContext(ctx, a.id, a.anchorSource, a.anchorIndex, a.snippet, query); c != "" {
			item["context"] = c
		}
		items = append(items, item)

		// 分数分解: 最终分 = RRF 名次分 + rerankBonus − 反馈降权。把各部分拆开才能回答
		// 「这篇为什么排第一」—— 是靠双路名次, 还是靠相似度 / 关键词 / 标题的加成。
		bonus := rerankBonus(a.bestDist, a.kwHit, a.titleHit)
		fbPart := -feedbackPenalty(penalties[a.id])
		simPart := 0.0
		if a.bestDist >= 0 {
			if sim := 1 - a.bestDist/2; sim > 0 {
				simPart = 0.6 * sim
			}
		}
		kwPart, titlePart := 0.0, 0.0
		if a.kwHit {
			kwPart = 0.25
		}
		if a.titleHit {
			titlePart = 0.2
		}
		fusion = append(fusion, FusionItem{
			Rank: i + 1, ArticleID: a.id, Title: a.title, ChunkID: a.chunkID,
			RRFScore: round3(a.score - bonus - fbPart), SimPart: round3(simPart),
			KwPart: kwPart, TitlePart: titlePart, FeedbackPart: round3(fbPart),
			FinalScore: round3(a.score),
			BestDist:   round3(a.bestDist), KwHit: a.kwHit, TitleHit: a.titleHit,
			MatchCount: len(a.matches),
		})
	}
	return items, fusion
}

// rerankBonus 在 RRF 名次分之上叠加质量分: 向量相似度(权重最高) + 关键词命中 + 标题命中。
func rerankBonus(bestDist float64, kwHit, titleHit bool) float64 {
	b := 0.0
	if bestDist >= 0 {
		// 余弦距离 0~2 → 相似度 0~1
		sim := 1 - bestDist/2
		if sim < 0 {
			sim = 0
		}
		b += 0.6 * sim
	}
	if kwHit {
		b += 0.25
	}
	if titleHit {
		b += 0.2
	}
	return b
}

// anchorContext 取首命中块的前后相邻块拼成上下文, 保证回答有完整语境。
func (ts *MCPToolSet) anchorContext(ctx context.Context, articleID, source string, idx int, main, query string) string {
	if ts.db == nil || articleID == "" || idx < 0 {
		return ""
	}
	prev, next := ts.fetchNeighborChunks(ctx, tenantOf(ctx), articleID, source, idx)
	if prev == "" && next == "" {
		return main
	}
	var b strings.Builder
	if prev != "" {
		b.WriteString("（上文）…")
		b.WriteString(prev)
		b.WriteString("\n")
	}
	b.WriteString(main)
	if next != "" {
		b.WriteString("\n（下文）")
		b.WriteString(next)
		b.WriteString("…")
	}
	return b.String()
}

// fetchNeighborChunks 取目标块的前一块尾部与后一块头部。
// 必须按 source 过滤: 正文块与附件块的 chunk_index 各自从 0 编号, 混在一起会取错邻居。
func (ts *MCPToolSet) fetchNeighborChunks(ctx context.Context, tenant, articleID, source string, idx int) (prev, next string) {
	rows, err := ts.db.QueryContext(ctx,
		`SELECT chunk_index, content FROM knowledge_chunks
		 WHERE tenant_id=$1 AND article_id=$2 AND COALESCE(source,'article')=$3
		   AND chunk_index >= $4 AND chunk_index <= $5
		 ORDER BY chunk_index`, tenant, articleID, source, idx-1, idx+1)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	for rows.Next() {
		var ci int
		var content string
		if err := rows.Scan(&ci, &content); err != nil {
			return prev, next
		}
		switch {
		case ci < idx:
			prev = tailRunes(content, kbNeighborRunes)
		case ci > idx:
			next = headRunes(content, kbNeighborRunes)
		}
	}
	return prev, next
}

// contextWindow 按查询词命中位置开窗截取块内容。
// 从头硬截会把命中处之后的关键步骤丢掉, 这正是"检索到了却答不准"的主因之一。
func contextWindow(content, query string, maxRunes int) string {
	r := []rune(content)
	if maxRunes <= 0 || len(r) <= maxRunes {
		return content
	}
	at := indexFold(content, query)
	if at < 0 {
		return string(r[:maxRunes]) + "…"
	}
	// 字节下标换算成 rune 下标, 避免中文按字节切一半
	ri := utf8.RuneCountInString(content[:at])
	start := ri - maxRunes/3
	if start < 0 {
		start = 0
	}
	end := start + maxRunes
	if end > len(r) {
		end = len(r)
		if end-start < maxRunes {
			start = end - maxRunes
			if start < 0 {
				start = 0
			}
		}
	}
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// indexFold 大小写不敏感的子串定位; 整串找不到时退化为查询中最长的词。
func indexFold(s, sub string) int {
	ls := strings.ToLower(s)
	q := strings.TrimSpace(strings.ToLower(sub))
	if q == "" {
		return -1
	}
	if i := strings.Index(ls, q); i >= 0 {
		return i
	}
	best, bestLen := -1, 0
	for _, tok := range strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(tok) < 2 {
			continue
		}
		if i := strings.Index(ls, tok); i >= 0 && len(tok) > bestLen {
			best, bestLen = i, len(tok)
		}
	}
	return best
}

// containsFold 大小写不敏感的包含判断。
func containsFold(s, sub string) bool {
	return indexFold(s, sub) >= 0
}

func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// searchKnowledgeFallback 文章级 ILIKE 检索(块级双路全空时的保底, 兼容未 reindex 存量)。
// 同样按可见性谓词过滤, 保证保底路径不会绕过可见性/状态约束。
func (ts *MCPToolSet) searchKnowledgeFallback(ctx context.Context, query string, limit, snippetLen int, categories []string) ([]map[string]any, error) {
	tenant := tenantOf(ctx)
	catClause := ""
	var catArg []string
	if len(categories) > 0 {
		catClause = " AND a.category = ANY($5)"
		catArg = categories
	}
	// $1 ILIKE, $2 snippet 长度, $3 limit, $4 租户, [$5 分类], 其后为 scope 参数
	args := []any{"%" + query + "%", snippetLen, limit, tenant}
	if catArg != nil {
		args = append(args, catArg)
	}
	sa := newScopeArgs(ts.resolveScope(ctx), args...)
	rows, err := ts.db.QueryContext(ctx,
		`SELECT a.id, a.title, LEFT(a.content, $2) AS snippet FROM knowledge_articles a
		 WHERE a.tenant_id=$4 AND (a.title ILIKE $1 OR a.content ILIKE $1)`+catClause+sa.Predicate()+` LIMIT $3`,
		sa.Args()...)
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

// searchCacheKey 生成检索结果缓存键, 含租户/可见范围/查询/检索模式/分页参数/分类绑定,
// 避免跨租户、**跨用户**与跨参数串味。
//
// scopeKey 不可省: 可见性谓词已按提问者身份变化(见 scope.go), 键里不带 scope 会让
// 「A 检索到的团队私有内容」被 B 的相同查询直接复用 —— 越权泄漏且不留痕迹。
func (ts *MCPToolSet) searchCacheKey(tenant, query, mode, scopeKey string, limit, snippetLen int, categories []string) string {
	k := tenant + "\x1f" + scopeKey + "\x1f" + mode + "\x1f" + query + "\x1f" + strconv.Itoa(limit) + "\x1f" + strconv.Itoa(snippetLen)
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

	// 图像生成: 模型主动调用生成真实图片(智谱 cogview-3 / 3-flash); 返回的图片 URL 应在最终回答里
	// 以 markdown 图片语法 `![描述](url)` 输出, 前端会自动渲染(URL 必须以 http/https 开头)。
	// 选型: prompt 必填; size 默认 1024x1024; model 默认 cogview-3-flash (快/免费), 追求质量可填 cogview-3。
	reg.Register(mcp.ToolDefinition{
		Name: "image_generate",
		Description: "Generate an image from a text prompt using the configured image model (default 智谱 cogview-3-flash). " +
			"Args: prompt(string) REQUIRED detailed image description; size(string) optional one of 1024x1024|768x1344|1344x768|512x512 (default 1024x1024); " +
			"model(string) optional 'cogview-3-flash' (default, fast/free) or 'cogview-3' (higher quality). " +
			"Returns {url, model, size, prompt}. In your final reply, embed the image using markdown image syntax ![<description>](<url>) so the UI can render it. " +
			"Prefer this tool when the user explicitly asks for an image/diagram/picture; otherwise answer in text.",
		Parameters: map[string]mcp.ParamSchema{
			"prompt": {Type: "string", Description: "图片描述, 尽量具体(主体/场景/构图/色调)", Required: true},
			"size":   {Type: "string", Description: "输出尺寸, 默认 1024x1024", Required: false, Default: "1024x1024", Enum: []string{"1024x1024", "768x1344", "1344x768", "512x512"}},
			"model":  {Type: "string", Description: "模型 ID, 默认 cogview-3-flash(快速/免费); 追求质量选 cogview-3", Required: false, Default: "cogview-3-flash", Enum: []string{"cogview-3-flash", "cogview-3"}},
		},
		Handler: ts.imageGenerate,
	})

	// 文档生成: 模型主动调用生成真实可下载的 .pptx 演示文稿(纯 Go 手写 OOXML, 零外部依赖)。
	// 返回 {url, file_name, slide_count, title}; 模型在最终回复里用 markdown 链接 [file_name](url) 输出, 前端渲染下载。
	reg.Register(mcp.ToolDefinition{
		Name: "pptx_generate",
		Description: "Generate a downloadable PowerPoint (.pptx) presentation from structured content. " +
			"Args: title(string) REQUIRED deck title (shown on the cover slide); subtitle(string) optional subtitle on cover; " +
			"slides(array) REQUIRED list of content slides, each {title(string), bullets(array of string, optional)}; " +
			"file_name(string) optional output file name. " +
			"Returns {url, file_name, slide_count, title}. In your final reply, output a markdown download link [<file_name>](<url>) so the UI can render it. " +
			"Use this tool whenever the user asks for a PPT / slide deck / presentation; first plan the outline (title + per-slide title/bullets) then call it.",
		Parameters: map[string]mcp.ParamSchema{
			"title":     {Type: "string", Description: "演示文稿标题(封面页主标题)", Required: true},
			"subtitle":  {Type: "string", Description: "封面页副标题(可选)", Required: false},
			"slides":    {Type: "array", Description: "内容页列表, 每项 {title: 页标题, bullets: 要点数组(可选)}", Required: true},
			"file_name": {Type: "string", Description: "输出文件名(可选, 默认按时间戳命名)", Required: false},
		},
		Handler: ts.pptxGenerate,
	})

	// 文档生成: 生成真实可下载的 .docx Word 文档(纯 Go 手写 OOXML, 零外部依赖)。
	// 返回 {url, file_name, section_count, title}; 模型用 markdown 链接 [file_name](url) 输出。
	reg.Register(mcp.ToolDefinition{
		Name: "docx_generate",
		Description: "Generate a downloadable Word (.docx) document from structured content. " +
			"Args: title(string) REQUIRED document title (rendered as the title line); " +
			"sections(array) REQUIRED list of sections, each {heading(string), paragraphs(array of string, optional), bullets(array of string, optional)}; " +
			"file_name(string) optional output file name. " +
			"Returns {url, file_name, section_count, title}. In your final reply, output a markdown download link [<file_name>](<url>). " +
			"Use this tool whenever the user asks for a Word doc / report / proposal / 文档 / 方案 / 报告; first plan the outline (title + sections) then call it.",
		Parameters: map[string]mcp.ParamSchema{
			"title":     {Type: "string", Description: "文档主标题(标题行)", Required: true},
			"sections":  {Type: "array", Description: "章节列表, 每项 {heading: 章节标题, paragraphs: 段落数组(可选), bullets: 要点数组(可选)}", Required: true},
			"file_name": {Type: "string", Description: "输出文件名(可选)", Required: false},
		},
		Handler: ts.docxGenerate,
	})

	// 表格生成: 生成真实可下载的 .xlsx Excel 表格(纯 Go 手写 OOXML, 零外部依赖)。
	// 返回 {url, file_name, sheet_count}; 模型用 markdown 链接 [file_name](url) 输出。
	reg.Register(mcp.ToolDefinition{
		Name: "xlsx_generate",
		Description: "Generate a downloadable Excel (.xlsx) workbook from structured tabular data. " +
			"Args: sheets(array) REQUIRED list of sheets, each {name(string, optional), headers(array of string, optional), rows(array of array, optional)}; " +
			"or use top-level headers(array) + rows(array of array) for a single-sheet workbook; " +
			"file_name(string) optional output file name. " +
			"Returns {url, file_name, sheet_count}. In your final reply, output a markdown download link [<file_name>](<url>). " +
			"Use this tool whenever the user asks for a spreadsheet / table / 表格 / 台账 / 导出数据; build the headers + rows then call it.",
		Parameters: map[string]mcp.ParamSchema{
			"sheets":    {Type: "array", Description: "多工作表, 每项 {name: 表名(可选), headers: 表头数组(可选), rows: 二维行数据(可选)}", Required: false},
			"headers":   {Type: "array", Description: "单表便捷入口: 表头数组(与 rows 配合构造第一个 sheet)", Required: false},
			"rows":      {Type: "array", Description: "单表便捷入口: 二维行数据(每行为单元格数组)", Required: false},
			"file_name": {Type: "string", Description: "输出文件名(可选)", Required: false},
		},
		Handler: ts.xlsxGenerate,
	})

	// 文档生成: 生成真实可下载的 .pdf (纯 Go 手写 PDF, 内嵌 CJK 字体以支持中文, 零外部依赖)。
	// 返回 {url, file_name, page_count, title}; 模型用 markdown 链接 [file_name](url) 输出。
	reg.Register(mcp.ToolDefinition{
		Name: "pdf_generate",
		Description: "Generate a downloadable PDF document (with Chinese/CJK support) from structured content. " +
			"Args: title(string) optional document title (rendered as the H1 heading); " +
			"blocks(array) optional list of content blocks, each {type(string, one of heading|paragraph|bullet|pagebreak), level(number, heading level 1-3 optional), text(string)}; " +
			"content(string) optional fallback plain text (split by newline into paragraphs) when blocks is omitted; " +
			"file_name(string) optional output file name. " +
			"Returns {url, file_name, page_count, title}. In your final reply, output a markdown download link [<file_name>](<url>). " +
			"Use this tool whenever the user asks for a PDF / 报告(需 PDF) / 文档(要 PDF 格式); prefer pdf_generate over docx_generate only when PDF format is explicitly required.",
		Parameters: map[string]mcp.ParamSchema{
			"title":     {Type: "string", Description: "文档主标题(作为一级标题)", Required: false},
			"blocks":    {Type: "array", Description: "结构化内容块列表, 每项 {type: heading|paragraph|bullet|pagebreak, level: 标题级别(可选), text: 文本}", Required: false},
			"content":   {Type: "string", Description: "纯文本内容(按换行拆分段落), 不传 blocks 时使用", Required: false},
			"file_name": {Type: "string", Description: "输出文件名(可选, 默认按时间戳命名)", Required: false},
		},
		Handler: ts.pdfGenerate,
	})

	// 通用文本文件生成: 覆盖 csv/md/txt/json/html/xml/yaml/sql 等纯文本类型, 字节直存对象存储。
	// 返回 {url, file_name, size_bytes, content_type}; 模型用 markdown 链接 [file_name](url) 输出。
	reg.Register(mcp.ToolDefinition{
		Name: "file_generate",
		Description: "Generate a downloadable plain-text file of an arbitrary type (csv / markdown / txt / json / html / xml / yaml / sql / log / ini / source code, etc.). " +
			"Args: file_name(string) REQUIRED output file name WITH extension (e.g. report.md, data.csv, config.yaml); " +
			"content(string) REQUIRED file body; content_type(string) optional MIME override (inferred from extension by default). " +
			"Returns {url, file_name, size_bytes, content_type}. In your final reply, output a markdown download link [<file_name>](<url>). " +
			"Use this tool whenever the user needs a downloadable text file of a type not covered by pptx/docx/xlsx/pdf_generate.",
		Parameters: map[string]mcp.ParamSchema{
			"file_name":    {Type: "string", Description: "输出文件名(必填, 需含扩展名, 如 report.md)", Required: true},
			"content":      {Type: "string", Description: "文件正文内容", Required: true},
			"content_type": {Type: "string", Description: "MIME 类型(可选, 默认按扩展名推断)", Required: false},
		},
		Handler: ts.fileGenerate,
	})

	// 代码编写类工具(coding_*): 仅当沙箱根已配置才注册, 否则静默跳过, 避免暴露未隔离的文件操作。
	if ts.sandboxRoot != "" {
		ts.registerCodingTools(reg)
	}

	annotateBuiltinToolRisks(reg)
}

// builtinToolRisks 内置运维工具风险分级(T3): 任务级 Agent 按权限模式 × risk 判定执行策略。
// critical=不可逆/高危运维动作; write=产生外部写入; read=只读查询。未列入的自定义/外部 MCP
// 工具缺省按 write 处理(standard 模式下触发人工确认, 保守优先)。
var builtinToolRisks = map[string]string{
	"cmdb_query":        "read",
	"script_list":       "read",
	"script_detail":     "read",
	"script_executions": "read",
	"knowledge_search":  "read",
	"image_generate":    "write",
	"pptx_generate":     "write",
	"docx_generate":     "write",
	"xlsx_generate":     "write",
	"pdf_generate":      "write",
	"file_generate":     "write",
	"script_generate":   "write",
	"ticket_create":     "write",
	"script_run":        "critical",
}

// annotateBuiltinToolRisks 给已注册的内置工具补 Risk 标注(不改变 Handler/描述)。
func annotateBuiltinToolRisks(reg *mcp.Registry) {
	for name, risk := range builtinToolRisks {
		if d, ok := reg.Get(name); ok {
			d.Risk = risk
			reg.Register(d)
		}
	}
}

// RAGContext 为给定查询检索知识库上下文(用于注入 LLM system prompt)。
// agentID 非空时按该 Agent 绑定的知识库分类过滤检索(PRD 5.2.6), 空则检索全部。
// effectiveCategories 合并 Agent 绑定分类与前端显式选择的范围:
//   - extra 空: 沿用 Agent 绑定(原行为, 空=全部)
//   - base 空(普通对话无绑定): 直接采用前端选择的范围
//   - 两者皆非空: 取交集, 既尊重 Agent 绑定又不超出用户所选范围
func effectiveCategories(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	if len(base) == 0 {
		return extra
	}
	set := make(map[string]struct{}, len(base))
	for _, c := range base {
		set[c] = struct{}{}
	}
	out := make([]string, 0, len(extra))
	for _, c := range extra {
		if _, ok := set[c]; ok {
			out = append(out, c)
		}
	}
	return out
}

// 注入 prompt 的检索参数。抽成常量是因为检索测试台要给出「逐字一致」的 prompt 预览:
// 两边必须用同一组 limit/snippetLen, 各写一份迟早会漂移。
const (
	ragPromptLimit      = 3
	ragPromptSnippetLen = 600
)

// RAGContext 检索知识库相关内容, 注入到 system prompt。
// categories 为前端在对话中显式选择的知识范围(空=不额外限定, 沿用 Agent 绑定分类)。
func (ts *MCPToolSet) RAGContext(ctx context.Context, query, agentID string, categories []string) string {
	if ts.db == nil || query == "" {
		return ""
	}
	effective := effectiveCategories(ts.categoriesFor(agentID), categories)
	// 注入 prompt 的窗口比工具回传小(工具侧给 1200), 避免 system prompt 被知识块撑爆。
	items, err := ts.searchKnowledge(ctx, query, ragPromptLimit, ragPromptSnippetLen, effective)
	if err != nil || len(items) == 0 {
		return ""
	}
	return ragPromptFromItems(items)
}

// PreviewRetrievalPrompt 跑一次与 RAGContext **完全同参**的检索, 返回真实注入 system prompt 的文本
// 以及进 prompt 的那几篇文章 —— 检索测试台的「注入预览」用, 保证预览与线上逐字一致。
//
// 强制走 rrf 模式并绕过缓存: 预览要展示的是线上那条路径, 而不是测试台选的诊断路径。
func (ts *MCPToolSet) PreviewRetrievalPrompt(ctx context.Context, query string, opts RetrieveOptions) (string, []map[string]any) {
	if ts.db == nil || query == "" {
		return "", nil
	}
	o := opts
	o.Mode = ""
	o.ComputeACL = false
	o.BypassCache = true
	o.NoLog = true
	items, _, err := ts.searchKnowledgeExplained(ctx, query, ragPromptLimit, ragPromptSnippetLen, o)
	if err != nil || len(items) == 0 {
		return "", nil
	}
	return ragPromptFromItems(items), items
}

// ragPromptFromItems 把检索结果拼装成注入 system prompt 的片段。
//
// 独立成函数是为了让「测试台的 prompt 预览」与「线上真实注入」共用同一段拼装代码:
// 各写一份的话, 预览会随重构慢慢变成一句善意的谎言。
func ragPromptFromItems(items []map[string]any) string {
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
			if len(matches) > kbRAGMaxMatchesPerArticle {
				matches = matches[:kbRAGMaxMatchesPerArticle]
			}
			for i, m := range matches {
				ch, _ := m["heading"].(string)
				snip, _ := m["snippet"].(string)
				// 首块用带相邻块上下文的版本, 保证注入的片段语义完整。
				if i == 0 {
					if c, ok := it["context"].(string); ok && c != "" {
						snip = c
					}
				}
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
	if len(parts) == 0 {
		return ""
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
