// Package aiworkspace 让 AI 以「受约束的 schema 填空器」身份生成报表看板。
//
// 设计要点:
//   - AI 不生成 SQL、不生成代码，只在统一报表模块(internal/report)已注册的
//     数据集白名单内填 Dashboard JSON，产出物与人工搭建的看板完全同构。
//   - 强制 function calling: 用 create_dashboard 工具的 JSON Schema 约束输出形态，
//     数据集名直接以 enum 下发，从源头降低幻觉。
//   - 二次兜底: 模型仍可能编造字段，因此本包对返回结果做"白名单修复"
//     (剥离未知维度/指标、纠正图表类型、夹紧布局、去重 ID)，
//     最后再交给 report.Dashboard.Validate 做终审，双保险。
//   - 只产草稿: 本包不落库。草稿交前端预览，用户确认后走既有
//     POST /report/dashboards，权限与归属和人工搭建完全一致。
package aiworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/report"
	"github.com/opsorigin/core/pkg/llm"
)

// toolName 是约束模型输出的函数名。
const toolName = "create_dashboard"

// 生成规模上限，避免模型一口气吐出几十个图表拖垮预览。
const (
	maxWidgets     = 8
	gridColumns    = 12
	defaultWidgetW = 6
	defaultWidgetH = 8
	maxWidgetH     = 24
)

// chartTypes 是前端 ReportRenderer 实际支持的图表类型白名单。
var chartTypes = map[string]bool{
	"line": true, "bar": true, "pie": true, "number": true, "table": true,
}

// granularities 与 report 包保持一致的时间粒度白名单。
var granularities = map[string]bool{
	"": true, "hour": true, "day": true, "week": true,
	"month": true, "quarter": true, "year": true,
}

// LLMClient 抽出最小依赖面，便于单测注入假客户端。
type LLMClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

// Generator 把自然语言需求翻译成受约束的看板草稿。
type Generator struct {
	client LLMClient
	model  string
}

// New 构造生成器。model 留空表示使用 LLM 客户端的默认模型。
func New(client LLMClient, model string) *Generator {
	return &Generator{client: client, model: model}
}

// Draft 是一次生成的产物：看板草稿 + 模型自述 + 修复过程中的告警。
// Warnings 会原样透给前端，让用户知道"哪些地方被平台纠正过"，避免黑箱。
type Draft struct {
	Dashboard   report.Dashboard `json:"dashboard"`
	Explanation string           `json:"explanation,omitempty"`
	Warnings    []string         `json:"warnings,omitempty"`
}

// Generate 依据数据集目录与用户描述生成看板草稿。
func (g *Generator) Generate(ctx context.Context, reg *report.Registry, prompt string) (*Draft, error) {
	if g == nil || g.client == nil {
		return nil, fmt.Errorf("aiworkspace: llm client not configured")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("aiworkspace: prompt is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("aiworkspace: nil registry")
	}
	datasets := reg.List()
	if len(datasets) == 0 {
		return nil, fmt.Errorf("aiworkspace: no dataset registered")
	}

	sys, err := buildSystemPrompt(datasets)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Chat(ctx, llm.ChatRequest{
		Model: g.model,
		Messages: []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: prompt},
		},
		Tools:       []llm.ToolDef{buildTool(datasets)},
		ToolChoice:  llm.ForceTool(toolName),
		Temperature: 0.2,
		MaxTokens:   4096,
	})
	if err != nil {
		return nil, fmt.Errorf("aiworkspace: llm call failed: %w", err)
	}
	raw, err := extractPayload(resp)
	if err != nil {
		return nil, err
	}
	return sanitize(reg, raw, prompt)
}

// buildSystemPrompt 把数据集目录序列化进系统提示词。
// 目录本身就是"可用字段的唯一真相"，杜绝模型凭空造字段。
func buildSystemPrompt(datasets []*report.Dataset) (string, error) {
	catalog, err := json.MarshalIndent(datasets, "", "  ")
	if err != nil {
		return "", fmt.Errorf("aiworkspace: marshal catalog: %w", err)
	}
	var b strings.Builder
	b.WriteString(`你是 OpsOrigin 运维平台的报表看板生成助手。用户用自然语言描述想看的数据，你必须调用 ` + toolName + ` 工具输出一个看板定义。

硬性约束（违反会被平台直接丢弃）：
1. 只能使用下方「数据集目录」中出现过的 dataset name、dimension name、metric name，严禁编造任何字段。
2. query.dimensions 只能取该数据集中 groupable 为 true 的维度；query.metrics 只能取该数据集声明的指标名。
3. filters[].field 只能取 filterable 为 true 的维度；op 仅限 eq/ne/in/not_in/gt/gte/lt/lte/contains/between/is_null/not_null；in/not_in 的 value 必须是数组，between 必须是长度为 2 的数组。
4. 时间趋势：设置 granularity（hour/day/week/month）并配合 timeRange.last（形如 "24h" "7d" "30d" "4w"）；数据集没有 timeField 时不要设置 granularity 和 timeRange。
5. 图表类型选择：趋势用 line；分类对比/排行用 bar；占比构成用 pie（只带 1 个维度 1 个指标）；单一总量用 number（不要带 dimensions 和 granularity）；明细清单用 table。
6. layout 采用 12 列栅格：x 取 0~11，w 取 1~12，h 建议 6~10，widget 之间不要重叠（同一行两个图各占 w=6）。
7. 输出 2~6 个 widget，覆盖用户关心的核心视角，不要堆砌无关图表。
8. widget id 仅用小写字母、数字、下划线，形如 w_ticket_trend，同一看板内唯一。
9. name 与 title 用简洁中文；explanation 用一两句中文说明这个看板回答了什么问题。

数据集目录（JSON，唯一可用字段来源）：
`)
	b.Write(catalog)
	return b.String(), nil
}

// buildTool 生成 create_dashboard 的 JSON Schema，把数据集名做成 enum 下发。
func buildTool(datasets []*report.Dataset) llm.ToolDef {
	names := make([]string, 0, len(datasets))
	hints := make([]string, 0, len(datasets))
	for _, ds := range datasets {
		names = append(names, ds.Name)
		dims := make([]string, 0, len(ds.Dimensions))
		for _, d := range ds.Dimensions {
			if d.Groupable {
				dims = append(dims, d.Name)
			}
		}
		mets := make([]string, 0, len(ds.Metrics))
		for _, m := range ds.Metrics {
			mets = append(mets, m.Name)
		}
		hints = append(hints, fmt.Sprintf("%s(%s): 维度=[%s] 指标=[%s]",
			ds.Name, ds.Label, strings.Join(dims, ","), strings.Join(mets, ",")))
	}

	querySchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dataset": map[string]any{
				"type":        "string",
				"enum":        names,
				"description": "数据集名，必须来自枚举。" + strings.Join(hints, "; "),
			},
			"dimensions": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "分组维度名，必须是该数据集 groupable 的维度",
			},
			"metrics": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "指标名，必须是该数据集声明的指标",
			},
			"filters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"field": map[string]any{"type": "string"},
						"op": map[string]any{
							"type": "string",
							"enum": []string{"eq", "ne", "in", "not_in", "gt", "gte", "lt", "lte", "contains", "between", "is_null", "not_null"},
						},
						"value": map[string]any{"description": "标量或数组，取决于 op"},
					},
					"required": []string{"field", "op"},
				},
			},
			"timeRange": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string", "description": "留空则用数据集默认时间字段"},
					"last":  map[string]any{"type": "string", "description": `相对区间，如 "24h" "7d" "30d" "4w"`},
				},
			},
			"granularity": map[string]any{
				"type": "string",
				"enum": []string{"", "hour", "day", "week", "month", "quarter", "year"},
			},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 5000},
		},
		"required": []string{"dataset"},
	}

	widgetSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    map[string]any{"type": "string", "description": "小写字母/数字/下划线，如 w_ticket_trend"},
			"title": map[string]any{"type": "string", "description": "中文卡片标题"},
			"type": map[string]any{
				"type": "string",
				"enum": []string{"line", "bar", "pie", "number", "table"},
			},
			"query": querySchema,
			"layout": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "minimum": 0, "maximum": 11},
					"y": map[string]any{"type": "integer", "minimum": 0},
					"w": map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
					"h": map[string]any{"type": "integer", "minimum": 1, "maximum": 24},
				},
			},
		},
		"required": []string{"id", "title", "type", "query"},
	}

	return llm.ToolDef{
		Type: "function",
		Function: llm.ToolFunc{
			Name:        toolName,
			Description: "根据用户需求创建一个运维报表看板，所有字段必须来自已注册数据集白名单",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "看板名称（中文）"},
					"description": map[string]any{"type": "string", "description": "看板简介（中文，可选）"},
					"explanation": map[string]any{"type": "string", "description": "一两句话说明这个看板回答了什么问题"},
					"widgets": map[string]any{
						"type":     "array",
						"minItems": 1,
						"maxItems": maxWidgets,
						"items":    widgetSchema,
					},
				},
				"required": []string{"name", "widgets"},
			},
		},
	}
}

// rawTimeRange 单独定义，兼容模型可能吐出的字符串时间与 snake_case。
type rawTimeRange struct {
	Field string     `json:"field"`
	Last  string     `json:"last"`
	From  *time.Time `json:"from"`
	To    *time.Time `json:"to"`
}

type rawQuery struct {
	Dataset        string           `json:"dataset"`
	Dimensions     []string         `json:"dimensions"`
	Metrics        []string         `json:"metrics"`
	Filters        []report.Filter  `json:"filters"`
	TimeRange      *rawTimeRange    `json:"timeRange"`
	TimeRangeSnake *rawTimeRange    `json:"time_range"`
	Granularity    string           `json:"granularity"`
	OrderBy        []report.OrderBy `json:"orderBy"`
	OrderBySnake   []report.OrderBy `json:"order_by"`
	Limit          int              `json:"limit"`
}

type rawWidget struct {
	ID     string        `json:"id"`
	Title  string        `json:"title"`
	Type   string        `json:"type"`
	Query  rawQuery      `json:"query"`
	Layout report.Layout `json:"layout"`
}

type rawDashboard struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Explanation string      `json:"explanation"`
	Widgets     []rawWidget `json:"widgets"`
}

// extractPayload 从模型响应里取出工具调用参数。
// 部分兼容后端不支持 tool_choice 强制，会退化成把 JSON 写进 content，这里一并兜底。
func extractPayload(resp *llm.ChatResponse) (*rawDashboard, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return nil, fmt.Errorf("aiworkspace: empty response from model")
	}
	msg := resp.Choices[0].Message
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name != toolName && tc.Function.Name != "" {
			continue
		}
		var out rawDashboard
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &out); err != nil {
			return nil, fmt.Errorf("aiworkspace: invalid tool arguments: %w", err)
		}
		return &out, nil
	}
	if s := extractJSONObject(msg.Content); s != "" {
		var out rawDashboard
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return &out, nil
		}
	}
	return nil, fmt.Errorf("aiworkspace: model did not return a dashboard definition")
}

// extractJSONObject 从可能带 ```json 围栏的文本里抠出第一个 JSON 对象。
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// sanitize 做白名单修复 + 终审，是 AI 输出进入系统前的最后一道闸门。
func sanitize(reg *report.Registry, raw *rawDashboard, prompt string) (*Draft, error) {
	warnings := make([]string, 0, 4)

	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = truncate(prompt, 30)
		warnings = append(warnings, "模型未给出看板名称，已用需求描述兜底")
	}

	widgets := make([]report.Widget, 0, len(raw.Widgets))
	usedIDs := make(map[string]bool, len(raw.Widgets))
	for i, rw := range raw.Widgets {
		if len(widgets) >= maxWidgets {
			warnings = append(warnings, fmt.Sprintf("图表数量超过 %d 个，多余部分已丢弃", maxWidgets))
			break
		}
		w, ws, ok := sanitizeWidget(reg, rw, i, usedIDs)
		warnings = append(warnings, ws...)
		if !ok {
			continue
		}
		usedIDs[w.ID] = true
		widgets = append(widgets, w)
	}
	if len(widgets) == 0 {
		return nil, fmt.Errorf("aiworkspace: 生成结果中没有任何可用图表，请换个说法或指定具体的数据范围")
	}
	autoLayout(widgets)

	d := report.Dashboard{
		Name:        truncate(name, 80),
		Description: truncate(strings.TrimSpace(raw.Description), 200),
		Widgets:     widgets,
	}
	// 终审：走和人工搭建看板完全一致的校验路径，确保草稿一定能存下去。
	if err := d.Validate(reg); err != nil {
		return nil, fmt.Errorf("aiworkspace: 生成的看板未通过校验: %w", err)
	}
	return &Draft{
		Dashboard:   d,
		Explanation: strings.TrimSpace(raw.Explanation),
		Warnings:    warnings,
	}, nil
}

// sanitizeWidget 逐个字段做白名单修复；无法救回的 widget 直接丢弃并给出原因。
func sanitizeWidget(reg *report.Registry, rw rawWidget, idx int, used map[string]bool) (report.Widget, []string, bool) {
	var warns []string
	title := strings.TrimSpace(rw.Title)
	if title == "" {
		title = fmt.Sprintf("图表 %d", idx+1)
	}

	ds, err := reg.Get(strings.TrimSpace(rw.Query.Dataset))
	if err != nil {
		return report.Widget{}, []string{fmt.Sprintf("图表「%s」引用了不存在的数据集 %q，已丢弃", title, rw.Query.Dataset)}, false
	}

	chart := strings.ToLower(strings.TrimSpace(rw.Type))
	if !chartTypes[chart] {
		warns = append(warns, fmt.Sprintf("图表「%s」的类型 %q 不受支持，已回退为柱状图", title, rw.Type))
		chart = "bar"
	}

	q := report.Query{Dataset: ds.Name, Limit: rw.Query.Limit}

	// 维度：未声明 / 不可分组的一律剥离
	for _, name := range rw.Query.Dimensions {
		name = strings.TrimSpace(name)
		dim, ok := ds.Dim(name)
		if !ok {
			warns = append(warns, fmt.Sprintf("图表「%s」引用了未知维度 %q，已忽略", title, name))
			continue
		}
		if !dim.Groupable {
			warns = append(warns, fmt.Sprintf("图表「%s」的维度 %q 不支持分组，已忽略", title, name))
			continue
		}
		q.Dimensions = append(q.Dimensions, name)
	}
	// 指标：未声明的剥离；一个不剩时由 Normalize 回落到数据集首个指标
	for _, name := range rw.Query.Metrics {
		name = strings.TrimSpace(name)
		if _, ok := ds.Met(name); !ok {
			warns = append(warns, fmt.Sprintf("图表「%s」引用了未知指标 %q，已忽略", title, name))
			continue
		}
		q.Metrics = append(q.Metrics, name)
	}
	// 过滤条件：字段/操作符双白名单，值形态不符的直接丢该条而不是整图报废
	for _, f := range rw.Query.Filters {
		f.Field = strings.TrimSpace(f.Field)
		dim, ok := ds.Dim(f.Field)
		if !ok || !dim.Filterable {
			warns = append(warns, fmt.Sprintf("图表「%s」的过滤字段 %q 不可用，已忽略", title, f.Field))
			continue
		}
		if f.Op == report.OpContains && dim.Type != report.FieldString {
			warns = append(warns, fmt.Sprintf("图表「%s」对非文本字段 %q 使用了模糊匹配，已忽略", title, f.Field))
			continue
		}
		switch f.Op {
		case report.OpIn, report.OpNotIn:
			if len(f.ValueList()) == 0 {
				warns = append(warns, fmt.Sprintf("图表「%s」的过滤条件 %q 缺少候选值，已忽略", title, f.Field))
				continue
			}
		case report.OpBetween:
			if len(f.ValueList()) != 2 {
				warns = append(warns, fmt.Sprintf("图表「%s」的区间过滤 %q 取值不合法，已忽略", title, f.Field))
				continue
			}
		case report.OpIsNull, report.OpNotNull:
			f.Value = nil
		default:
			if f.Value == nil {
				warns = append(warns, fmt.Sprintf("图表「%s」的过滤条件 %q 缺少取值，已忽略", title, f.Field))
				continue
			}
		}
		q.Filters = append(q.Filters, f)
	}

	// 时间粒度与区间：数据集没有时间字段时一律清空，避免整图校验失败
	gran := strings.ToLower(strings.TrimSpace(rw.Query.Granularity))
	if !granularities[gran] {
		warns = append(warns, fmt.Sprintf("图表「%s」的时间粒度 %q 不受支持，已清除", title, rw.Query.Granularity))
		gran = ""
	}
	tr := rw.Query.TimeRange
	if tr == nil {
		tr = rw.Query.TimeRangeSnake
	}
	if tr != nil {
		field := strings.TrimSpace(tr.Field)
		if field != "" {
			if dim, ok := ds.Dim(field); !ok || dim.Type != report.FieldTime {
				warns = append(warns, fmt.Sprintf("图表「%s」的时间字段 %q 不可用，已回退为数据集默认时间字段", title, field))
				field = ""
			}
		}
		if field == "" && ds.TimeField == "" {
			warns = append(warns, fmt.Sprintf("数据集 %s 不支持按时间筛选，图表「%s」的时间范围已清除", ds.Name, title))
			tr = nil
		} else {
			rng := &report.TimeRange{Field: field, From: tr.From, To: tr.To, Last: strings.TrimSpace(tr.Last)}
			if _, _, _, err := rng.Resolve(time.Now()); err != nil {
				warns = append(warns, fmt.Sprintf("图表「%s」的时间范围 %q 无法解析，已清除", title, tr.Last))
				rng = nil
			}
			if rng != nil && rng.From == nil && rng.To == nil && rng.Last == "" {
				rng = nil
			}
			q.TimeRange = rng
		}
	}
	if gran != "" && ds.TimeField == "" && (q.TimeRange == nil || q.TimeRange.Field == "") {
		warns = append(warns, fmt.Sprintf("数据集 %s 无时间字段，图表「%s」的时间粒度已清除", ds.Name, title))
		gran = ""
	}
	q.Granularity = report.Granularity(gran)

	// 图表语义修复：让产出物在 ReportRenderer 里一定"画得出来"
	switch chart {
	case "number":
		if len(q.Dimensions) > 0 || q.Granularity != report.GranNone {
			warns = append(warns, fmt.Sprintf("指标卡「%s」不支持分组，已改为展示总量", title))
		}
		q.Dimensions = nil
		q.Granularity = report.GranNone
		if len(q.Metrics) > 1 {
			q.Metrics = q.Metrics[:1]
		}
	case "pie":
		if q.Granularity != report.GranNone {
			q.Granularity = report.GranNone
		}
		if len(q.Dimensions) > 1 {
			warns = append(warns, fmt.Sprintf("饼图「%s」仅支持单维度，已保留 %q", title, q.Dimensions[0]))
			q.Dimensions = q.Dimensions[:1]
		}
		if len(q.Metrics) > 1 {
			q.Metrics = q.Metrics[:1]
		}
	case "line", "bar":
		// 趋势/对比图至少要有一个分组口径，否则只有一根孤零零的柱子
		if len(q.Dimensions) == 0 && q.Granularity == report.GranNone {
			if ds.TimeField != "" {
				q.Granularity = report.Granularity("day")
				if q.TimeRange == nil {
					q.TimeRange = &report.TimeRange{Last: "30d"}
				}
				warns = append(warns, fmt.Sprintf("图表「%s」缺少分组口径，已按天聚合近 30 天", title))
			} else if dim, ok := firstGroupable(ds); ok {
				q.Dimensions = []string{dim}
				warns = append(warns, fmt.Sprintf("图表「%s」缺少分组口径，已按 %q 分组", title, dim))
			}
		}
	}

	// 排序：交给 Normalize 兜底，这里只透传模型显式给出的合法项
	orders := rw.Query.OrderBy
	if len(orders) == 0 {
		orders = rw.Query.OrderBySnake
	}
	allowed := make(map[string]bool, len(q.Dimensions)+len(q.Metrics)+1)
	for _, d := range q.Dimensions {
		allowed[d] = true
	}
	for _, m := range q.Metrics {
		allowed[m] = true
	}
	if q.Granularity != report.GranNone {
		allowed[report.TimeBucketColumn] = true
	}
	for _, o := range orders {
		if allowed[strings.TrimSpace(o.Field)] {
			q.OrderBy = append(q.OrderBy, o)
		}
	}

	w := report.Widget{
		ID:     uniqueID(rw.ID, idx, used),
		Title:  truncate(title, 60),
		Type:   chart,
		Query:  q,
		Layout: clampLayout(rw.Layout),
	}
	// 单图终审：用带占位布局的副本跑一遍 Normalize，救不回来的直接丢弃，不污染整个看板。
	// 用副本是因为此刻布局可能还是"待自动排版"的空值，而 Validate 要求 w/h > 0。
	probe := w
	if probe.Layout.W <= 0 || probe.Layout.H <= 0 {
		probe.Layout = report.Layout{W: defaultWidgetW, H: defaultWidgetH}
	}
	if err := probe.Validate(reg); err != nil {
		warns = append(warns, fmt.Sprintf("图表「%s」未通过校验，已丢弃：%v", title, err))
		return report.Widget{}, warns, false
	}
	w.Query = probe.Query // 回收 Normalize 补齐的排序与 limit
	return w, warns, true
}

// firstGroupable 取数据集第一个可分组维度，作为兜底分组口径。
func firstGroupable(ds *report.Dataset) (string, bool) {
	for _, d := range ds.Dimensions {
		if d.Groupable && d.Type != report.FieldTime {
			return d.Name, true
		}
	}
	return "", false
}

// uniqueID 归一化 widget id 并保证看板内唯一。
func uniqueID(raw string, idx int, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" || (id[0] == '-') {
		id = fmt.Sprintf("w_%d", idx+1)
	}
	base := id
	for i := 2; used[id]; i++ {
		id = fmt.Sprintf("%s_%d", base, i)
	}
	return id
}

// clampLayout 把布局夹进 12 列栅格；非法尺寸留 0 交给 autoLayout 自动排。
func clampLayout(l report.Layout) report.Layout {
	if l.W <= 0 || l.H <= 0 {
		return report.Layout{}
	}
	if l.W > gridColumns {
		l.W = gridColumns
	}
	if l.H > maxWidgetH {
		l.H = maxWidgetH
	}
	if l.X < 0 {
		l.X = 0
	}
	if l.Y < 0 {
		l.Y = 0
	}
	if l.X+l.W > gridColumns {
		l.X = gridColumns - l.W
	}
	return l
}

// autoLayout 为没有给出合法布局的 widget 自动排成两列瀑布流。
func autoLayout(ws []report.Widget) {
	row, col := 0, 0
	for i := range ws {
		if ws[i].Layout.W > 0 && ws[i].Layout.H > 0 {
			continue
		}
		ws[i].Layout = report.Layout{
			X: col * defaultWidgetW,
			Y: row * defaultWidgetH,
			W: defaultWidgetW,
			H: defaultWidgetH,
		}
		col++
		if col >= gridColumns/defaultWidgetW {
			col = 0
			row++
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
