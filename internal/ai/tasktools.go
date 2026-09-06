// 任务产物工具(T2): 让模型在任务执行中自主产出文档/报告。
//
//   - doc_write / doc_read / doc_list: 任务工作区读写, 产物落对象存储(MinIO, key 前缀 chat/<taskID>/),
//     元数据复用 ai_chat_artifacts 表(conversation_id=任务会话), 右栏产物面板据此展示。
//   - doc_to_knowledge: 把产物沉淀为知识库文章草稿(knowledge.Service 由 main.go 注入, 避免模块耦合)。
//
// 作用域安全: 工具仅在任务执行上下文可用 —— Task Runner 调 ChatStream 前用 withTaskScope 把 taskID
// 注入 ctx, 引擎原样透传到工具 handler; 普通对话无此注入, 工具返回明确的"仅任务内可用"提示。
// 租户隔离: handler 内解析租户并强制带入查询条件, 禁止跨租户读写。
package ai

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/mcp"
)

// taskToolCtxKey Runner→工具 的任务作用域注入 key。
type taskToolCtxKey struct{}

func withTaskScope(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, taskToolCtxKey{}, taskID)
}

func taskIDFromScope(ctx context.Context) string {
	s, _ := ctx.Value(taskToolCtxKey{}).(string)
	return s
}

// taskTenantOf 与 agentic.tenantOf 同约定: 中间件写入的字符串 "tenant_id", 缺省 "default"。
func taskTenantOf(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

// fileNameSanitizer 产物文件名白名单: 中文/字母/数字/._- , 禁路径分隔与 ..
var fileNameSanitizer = regexp.MustCompile(`[^\p{Han}a-zA-Z0-9._\-]+`)

func sanitizeArtifactName(name string) string {
	name = fileNameSanitizer.ReplaceAllString(strings.TrimSpace(name), "-")
	name = strings.Trim(name, "-.")
	if name == "" || name == ".." || strings.Contains(name, "..") {
		name = "doc-" + time.Now().Format("20060102-150405") + ".md"
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

// SetKnowledgeCreator 注入知识库文章创建回调(main.go 装配 knowledge.Service, 避免 ai→knowledge 直接依赖)。
// 未注入时 doc_to_knowledge 返回"未接入"提示。
func (s *Service) SetKnowledgeCreator(fn func(ctx context.Context, title, content, category, author string) (articleID string, err error)) {
	s.knowledgeCreate = fn
}

// registerTaskTools 把任务产物工具注册进全局 Registry。
// handler 闭包引用 Service, 依赖(objStore/knowledgeCreate)在调用时才读取, 支持后续注入。
func (s *Service) registerTaskTools() {
	reg := s.Registry
	reg.Register(mcp.ToolDefinition{
		Name: "doc_write",
		Description: "在当前任务工作区创建/更新一份文档产物(markdown/代码/csv 等), 用户可在任务工作台「产物」面板查看与下载。" +
			"需要向用户交付文档、报告、清单时必须调用本工具落盘, 而不是只在回复里输出正文。Args: file_name(string), content(string), note(string,optional 内容摘要)",
		Parameters: map[string]mcp.ParamSchema{
			"file_name": {Type: "string", Required: true, Description: "文件名(含扩展名, 如 report.md / inventory.csv), 禁路径分隔符"},
			"content":   {Type: "string", Required: true, Description: "文件全文内容(UTF-8 文本)"},
			"note":      {Type: "string", Description: "一句话内容摘要, 展示在产物列表"},
		},
		Handler: s.toolDocWrite,
		Risk:    "write",
	})
	reg.Register(mcp.ToolDefinition{
		Name:        "doc_read",
		Description: "读取当前任务工作区中一份产物文件的全文内容(如需引用自己先前步骤产出的文档)。Args: file_name(string)",
		Parameters: map[string]mcp.ParamSchema{
			"file_name": {Type: "string", Required: true},
		},
		Handler: s.toolDocRead,
		Risk:    "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name:        "doc_list",
		Description: "列出当前任务工作区已有的全部产物文件(名称/类型/大小/摘要)。多步任务开始新步骤前建议先调用, 了解已有产出避免重复。",
		Parameters:  map[string]mcp.ParamSchema{},
		Handler:     s.toolDocList,
		Risk:        "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name:        "task_update_step",
		Description: "更新任务步骤清单(checklist)的状态或备注, 向用户实时展示进度。当前步骤完成后无需手动调用(系统自动勾选); 需要提前标记某步骤跳过/补充说明时使用。Args: step_index(number, 1 开始的步骤序号), status(string, optional: done|skipped|pending), note(string, optional 补充说明)",
		Parameters: map[string]mcp.ParamSchema{
			"step_index": {Type: "number", Required: true, Description: "步骤序号(从 1 开始)"},
			"status":     {Type: "string", Description: "目标状态: done|skipped|pending", Enum: []string{"done", "skipped", "pending"}},
			"note":       {Type: "string", Description: "补充说明(进展/发现/跳过原因)"},
		},
		Handler: s.toolTaskUpdateStep,
		Risk:    "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name:        "task_confirm",
		Description: "向用户发起中途确认, 任务暂停直到用户批复。遇到方案歧义、需要用户拍板的选择(如: 采用哪种方案/是否继续高危操作/需求不明确)时主动调用, 不要自作主张。Args: question(string 具体问题), context(string,optional 背景/选项说明)",
		Parameters: map[string]mcp.ParamSchema{
			"question": {Type: "string", Required: true, Description: "向用户确认的具体问题"},
			"context":  {Type: "string", Description: "补充背景、可选方案及影响"},
		},
		Handler: s.toolTaskConfirm,
		Risk:    "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "doc_to_knowledge",
		Description: "把当前任务工作区的一份产物沉淀为知识库文章草稿(默认 draft 状态, 由人工审核发布)。" +
			"任务产出了有长期复用价值的文档(方案/手册/复盘)时调用。Args: file_name(string), title(string,optional), category(string,optional)",
		Parameters: map[string]mcp.ParamSchema{
			"file_name": {Type: "string", Required: true},
			"title":     {Type: "string", Description: "文章标题, 缺省用文件名"},
			"category":  {Type: "string", Description: "知识库分类名, 缺省为空"},
		},
		Handler: s.toolDocToKnowledge,
		Risk:    "write",
	})
	slog.Info("ai task doc tools registered", "tools", "doc_write,doc_read,doc_list,doc_to_knowledge,task_update_step,task_confirm")
}

// taskArtifactRow 产物元数据(读写共用)。
type taskArtifactRow struct {
	ID        string
	FileName  string
	FileType  string
	FileSize  int64
	ObjectKey string
	CreatedAt time.Time
}

// resolveTaskScope 校验工具调用处于任务上下文内, 返回任务与租户。
func (s *Service) resolveTaskScope(ctx context.Context) (task *AITask, tenantID string, errMsg string) {
	tenantID = taskTenantOf(ctx)
	taskID := taskIDFromScope(ctx)
	if taskID == "" {
		return nil, tenantID, "该工具仅在任务执行中可用(doc_write/doc_read/doc_list/doc_to_knowledge 属任务工作区能力), 普通对话请直接在回复中输出内容"
	}
	task = s.getTask(ctx, taskID, tenantID)
	if task == nil {
		return nil, tenantID, "任务不存在或已失效"
	}
	return task, tenantID, ""
}

// saveTaskArtifact 落盘: 内容 → 对象存储(chat/<taskID>/), 元数据 → ai_chat_artifacts。
// message_id 编码 "task:<taskID>" 以区分手动保存的对话产物。
func (s *Service) saveTaskArtifact(ctx context.Context, task *AITask, tenantID, fileName, content, note string) (*taskArtifactRow, error) {
	if s.objStore == nil {
		return nil, fmt.Errorf("对象存储未配置, 无法保存产物文件")
	}
	contentType := artifactContentType(fileName, "text/markdown")
	key := artifactKey(task.ID, fileName)
	if err := s.objStore.Put(ctx, key, strings.NewReader(content), int64(len(content)), contentType); err != nil {
		return nil, fmt.Errorf("写入对象存储失败: %w", err)
	}
	row := &taskArtifactRow{
		ID: uuid.New().String(), FileName: fileName, FileType: contentType,
		FileSize: int64(len(content)), ObjectKey: key, CreatedAt: time.Now(),
	}
	if s.db != nil {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO ai_chat_artifacts (id, tenant_id, conversation_id, message_id, file_name, file_type, file_size, object_key, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			row.ID, tenantID, task.ConversationID, "task:"+task.ID, fileName, contentType, row.FileSize, key, row.CreatedAt); err != nil {
			// 元数据落库失败: 对象已成孤儿, 回滚删除
			_ = s.objStore.Remove(ctx, key)
			return nil, fmt.Errorf("保存产物元数据失败: %w", err)
		}
	}
	if note != "" {
		s.emitTask(task.ID, "artifact", map[string]any{"file_name": fileName, "note": note, "size": row.FileSize})
	}
	slog.Info("ai task doc saved", "task_id", task.ID, "file", fileName, "bytes", row.FileSize)
	return row, nil
}

// listTaskArtifactRows 按任务会话列产物(同 file_name 取最新一份, 模型视角是一份可覆盖的工作区文件)。
func (s *Service) listTaskArtifactRows(ctx context.Context, task *AITask, tenantID string) ([]taskArtifactRow, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未配置")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, file_name, file_type, file_size, object_key, created_at
		 FROM ai_chat_artifacts WHERE conversation_id=$1 AND tenant_id=$2 ORDER BY created_at ASC`,
		task.ConversationID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]taskArtifactRow{}
	for rows.Next() {
		var r taskArtifactRow
		if err := rows.Scan(&r.ID, &r.FileName, &r.FileType, &r.FileSize, &r.ObjectKey, &r.CreatedAt); err != nil {
			continue
		}
		latest[r.FileName] = r // ASC 遍历, 后写覆盖前写
	}
	out := make([]taskArtifactRow, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) toolDocWrite(ctx context.Context, input map[string]any) (any, error) {
	task, tenantID, errMsg := s.resolveTaskScope(ctx)
	if errMsg != "" {
		return map[string]any{"error": errMsg, "retry": false}, nil
	}
	fileName := sanitizeArtifactName(strVal(input, "file_name"))
	content := strVal(input, "content")
	if strings.TrimSpace(content) == "" {
		return map[string]any{"error": "content 不能为空", "retry": false}, nil
	}
	if len(content) > 512*1024 {
		return map[string]any{"error": fmt.Sprintf("content 超过 512KB 上限(当前 %d KB), 请拆分为多个文件", len(content)/1024), "retry": false}, nil
	}
	row, err := s.saveTaskArtifact(ctx, task, tenantID, fileName, content, strVal(input, "note"))
	if err != nil {
		return map[string]any{"error": err.Error(), "retry": false}, nil
	}
	return map[string]any{
		"status": "saved", "file_name": row.FileName, "file_size": row.FileSize,
		"message": "产物已保存到任务工作区, 用户可在「产物」面板查看与下载",
	}, nil
}

func (s *Service) toolDocRead(ctx context.Context, input map[string]any) (any, error) {
	task, tenantID, errMsg := s.resolveTaskScope(ctx)
	if errMsg != "" {
		return map[string]any{"error": errMsg, "retry": false}, nil
	}
	fileName := sanitizeArtifactName(strVal(input, "file_name"))
	rows, err := s.listTaskArtifactRows(ctx, task, tenantID)
	if err != nil {
		return map[string]any{"error": "读取产物列表失败: " + err.Error(), "retry": false}, nil
	}
	var row *taskArtifactRow
	for i := range rows {
		if rows[i].FileName == fileName {
			row = &rows[i]
		}
	}
	if row == nil {
		return map[string]any{"error": "文件不存在: " + fileName + "; 可先用 doc_list 查看已有产物", "retry": false}, nil
	}
	if row.FileSize > 1024*1024 {
		return map[string]any{"error": "文件超过 1MB, 过大不支持整读", "retry": false}, nil
	}
	var buf strings.Builder
	if err := s.objStore.Get(ctx, writerFunc(buf.WriteString), row.ObjectKey); err != nil {
		return map[string]any{"error": "读取文件内容失败: " + err.Error(), "retry": false}, nil
	}
	return map[string]any{"file_name": row.FileName, "content": buf.String()}, nil
}

func (s *Service) toolDocList(ctx context.Context, _ map[string]any) (any, error) {
	task, tenantID, errMsg := s.resolveTaskScope(ctx)
	if errMsg != "" {
		return map[string]any{"error": errMsg, "retry": false}, nil
	}
	rows, err := s.listTaskArtifactRows(ctx, task, tenantID)
	if err != nil {
		return map[string]any{"error": "读取产物列表失败: " + err.Error(), "retry": false}, nil
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"file_name": r.FileName, "file_type": r.FileType, "file_size": r.FileSize,
			"created_at": r.CreatedAt.Format(time.RFC3339),
		})
	}
	return map[string]any{"count": len(items), "items": items}, nil
}

func (s *Service) toolDocToKnowledge(ctx context.Context, input map[string]any) (any, error) {
	task, tenantID, errMsg := s.resolveTaskScope(ctx)
	if errMsg != "" {
		return map[string]any{"error": errMsg, "retry": false}, nil
	}
	if s.knowledgeCreate == nil {
		return map[string]any{"error": "知识库模块未接入, 暂不支持沉淀文章", "retry": false}, nil
	}
	fileName := sanitizeArtifactName(strVal(input, "file_name"))
	rows, err := s.listTaskArtifactRows(ctx, task, tenantID)
	if err != nil {
		return map[string]any{"error": "读取产物列表失败: " + err.Error(), "retry": false}, nil
	}
	var row *taskArtifactRow
	for i := range rows {
		if rows[i].FileName == fileName {
			row = &rows[i]
		}
	}
	if row == nil {
		return map[string]any{"error": "文件不存在: " + fileName, "retry": false}, nil
	}
	var buf strings.Builder
	if err := s.objStore.Get(ctx, writerFunc(buf.WriteString), row.ObjectKey); err != nil {
		return map[string]any{"error": "读取文件内容失败: " + err.Error(), "retry": false}, nil
	}
	title := strings.TrimSpace(strVal(input, "title"))
	if title == "" {
		title = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	articleID, err := s.knowledgeCreate(ctx, title, buf.String(), strings.TrimSpace(strVal(input, "category")),
		"AI任务:"+task.Title)
	if err != nil {
		return map[string]any{"error": "创建知识库草稿失败: " + err.Error(), "retry": false}, nil
	}
	return map[string]any{
		"status": "draft_created", "article_id": articleID, "title": title,
		"message": "已创建知识库文章草稿, 请用户在知识库中审核后发布",
	}, nil
}

// toolTaskUpdateStep 更新活体任务的步骤状态/备注(与 Runner 同 goroutine, 直改指针无副本分叉)。
func (s *Service) toolTaskUpdateStep(ctx context.Context, input map[string]any) (any, error) {
	taskID := taskIDFromScope(ctx)
	if taskID == "" {
		return map[string]any{"error": "该工具仅在任务执行中可用", "retry": false}, nil
	}
	s.taskMu.Lock()
	live := s.taskLive[taskID]
	s.taskMu.Unlock()
	if live == nil {
		return map[string]any{"error": "任务不在执行中, 无法更新步骤", "retry": false}, nil
	}
	// step_index 兼容 number/string 传入
	idx := -1
	switch v := input["step_index"].(type) {
	case float64:
		idx = int(v) - 1
	case string:
		fmt.Sscanf(strings.TrimSpace(v), "%d", &idx)
		idx--
	}
	if idx < 0 || idx >= len(live.Steps) {
		return map[string]any{"error": fmt.Sprintf("step_index 无效(有效范围 1~%d)", len(live.Steps)), "retry": false}, nil
	}
	status := strVal(input, "status")
	if status != "" && status != "done" && status != "skipped" && status != "pending" {
		return map[string]any{"error": "status 仅支持 done|skipped|pending", "retry": false}, nil
	}
	note := strVal(input, "note")
	if status == "" && note == "" {
		return map[string]any{"error": "status 与 note 至少提供一项", "retry": false}, nil
	}
	step := &live.Steps[idx]
	if status != "" && step.Status != "running" { // running 步骤的状态由系统管理, 防模型自证完成
		step.Status = status
	}
	if note != "" {
		step.Note = truncateNote(note)
	}
	s.saveTask(persistCtx(), live)
	s.emitTask(taskID, "step", map[string]any{"index": idx, "step": *step})
	return map[string]any{"status": "updated", "step_index": idx + 1, "step_status": step.Status, "note": step.Note}, nil
}

// toolTaskConfirm 模型主动发起中途确认: 复用审批排队通道, 阻塞等用户批复。
func (s *Service) toolTaskConfirm(ctx context.Context, input map[string]any) (any, error) {
	task, _, errMsg := s.resolveTaskScope(ctx)
	if errMsg != "" {
		return map[string]any{"error": errMsg, "retry": false}, nil
	}
	question := strings.TrimSpace(strVal(input, "question"))
	if question == "" {
		return map[string]any{"error": "question 不能为空", "retry": false}, nil
	}
	if bg := strVal(input, "context"); bg != "" {
		question += "\n背景: " + bg
	}
	return s.requestTaskConfirm(ctx, task, question, input)
}

// strVal 安全取 map 字符串值(模型可能传非字符串类型)。
func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		default:
			return fmt.Sprintf("%v", t)
		}
	}
	return ""
}

// writerFunc 让 objStore.Get(io.Writer) 的内容汇入 strings.Builder。
type writerFunc func(p string) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(string(p)) }

// 编译期确认 writerFunc 满足 objStore.Get 的 io.Writer 参数
var _ io.Writer = writerFunc(nil)
