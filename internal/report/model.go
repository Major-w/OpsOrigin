// Package report 提供跨模块统一的报表底座。
//
// 设计要点:
//   - 数据集注册表: 各业务模块通过 Register 声明自己可对外查询的"数据集"
//     (维度 + 指标 + 解析器)，report 包本身不 import 任何业务模块，避免循环依赖。
//   - 白名单驱动: 查询只能引用数据集显式声明的维度/指标/过滤字段，
//     绝不开放裸 SQL，从源头杜绝注入与越权取数。
//   - 租户隔离: 解析器统一接收 tenantID，由数据集实现强制下推到查询条件。
package report

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TimeBucketColumn 是按时间粒度聚合时自动产生的时间桶列名。
const TimeBucketColumn = "time_bucket"

// 查询规模上限，防止单次查询拖垮数据库。
const (
	DefaultLimit = 200
	MaxLimit     = 5000
)

// FieldType 描述字段数据类型，供前端选择合适的筛选控件与格式化方式。
type FieldType string

const (
	FieldString FieldType = "string"
	FieldNumber FieldType = "number"
	FieldTime   FieldType = "time"
	FieldBool   FieldType = "bool"
)

// Dimension 是可用于分组与过滤的维度字段。
type Dimension struct {
	Name  string    `json:"name"`  // 对外字段名，查询中引用
	Label string    `json:"label"` // 展示名
	Type  FieldType `json:"type"`
	// Enum 为有限枚举时的候选值，前端可直接渲染成下拉框。
	Enum []string `json:"enum,omitempty"`
	// Filterable 为 false 时该维度只能分组、不能作为过滤条件。
	Filterable bool `json:"filterable"`
	// Groupable 为 false 时该维度只能过滤、不能分组(如自由文本字段)。
	Groupable bool `json:"groupable"`
}

// Aggregation 指标聚合方式白名单。
type Aggregation string

const (
	AggCount         Aggregation = "count"
	AggCountDistinct Aggregation = "count_distinct"
	AggSum           Aggregation = "sum"
	AggAvg           Aggregation = "avg"
	AggMin           Aggregation = "min"
	AggMax           Aggregation = "max"
)

// validAggregations 限定聚合函数集合，超出即拒绝。
var validAggregations = map[Aggregation]bool{
	AggCount: true, AggCountDistinct: true, AggSum: true,
	AggAvg: true, AggMin: true, AggMax: true,
}

// Metric 是可聚合的指标。
type Metric struct {
	Name  string      `json:"name"`
	Label string      `json:"label"`
	Agg   Aggregation `json:"agg"`
	// Unit 展示单位(如 "个" "小时" "%")，纯前端提示。
	Unit string `json:"unit,omitempty"`
	// Decimals 建议保留小数位，前端格式化用。
	Decimals int `json:"decimals,omitempty"`
}

// FilterOp 过滤操作符白名单。
type FilterOp string

const (
	OpEq       FilterOp = "eq"
	OpNe       FilterOp = "ne"
	OpIn       FilterOp = "in"
	OpNotIn    FilterOp = "not_in"
	OpGt       FilterOp = "gt"
	OpGte      FilterOp = "gte"
	OpLt       FilterOp = "lt"
	OpLte      FilterOp = "lte"
	OpContains FilterOp = "contains"
	OpBetween  FilterOp = "between"
	OpIsNull   FilterOp = "is_null"
	OpNotNull  FilterOp = "not_null"
)

var validOps = map[FilterOp]bool{
	OpEq: true, OpNe: true, OpIn: true, OpNotIn: true,
	OpGt: true, OpGte: true, OpLt: true, OpLte: true,
	OpContains: true, OpBetween: true, OpIsNull: true, OpNotNull: true,
}

// Filter 是一条过滤条件。in/not_in/between 的 Value 需为数组。
type Filter struct {
	Field string   `json:"field"`
	Op    FilterOp `json:"op"`
	Value any      `json:"value,omitempty"`
}

// ValueList 把 Value 归一化成切片，便于 in/not_in/between 处理。
func (f Filter) ValueList() []any {
	switch v := f.Value.(type) {
	case nil:
		return nil
	case []any:
		return v
	case []string:
		out := make([]any, 0, len(v))
		for _, s := range v {
			out = append(out, s)
		}
		return out
	default:
		return []any{v}
	}
}

// Granularity 时间聚合粒度。
type Granularity string

const (
	GranNone    Granularity = ""
	GranHour    Granularity = "hour"
	GranDay     Granularity = "day"
	GranWeek    Granularity = "week"
	GranMonth   Granularity = "month"
	GranQuarter Granularity = "quarter"
	GranYear    Granularity = "year"
)

var validGranularity = map[Granularity]bool{
	GranNone: true, GranHour: true, GranDay: true, GranWeek: true,
	GranMonth: true, GranQuarter: true, GranYear: true,
}

// relativeRe 匹配相对时间表达式，如 7d / 24h / 30m / 4w。
var relativeRe = regexp.MustCompile(`^(\d{1,5})([mhdw])$`)

// TimeRange 时间范围。Last 为相对区间(优先级低于显式 From/To)。
type TimeRange struct {
	Field string     `json:"field,omitempty"` // 留空则用数据集默认时间字段
	From  *time.Time `json:"from,omitempty"`
	To    *time.Time `json:"to,omitempty"`
	Last  string     `json:"last,omitempty"` // "7d" "24h" "30m" "4w"
}

// Resolve 计算实际的起止时间。now 由调用方传入以便测试。
// 返回 ok=false 表示未指定任何时间约束。
func (t *TimeRange) Resolve(now time.Time) (from, to time.Time, ok bool, err error) {
	if t == nil {
		return time.Time{}, time.Time{}, false, nil
	}
	if t.From != nil || t.To != nil {
		from = time.Time{}
		to = now
		if t.From != nil {
			from = *t.From
		}
		if t.To != nil {
			to = *t.To
		}
		if t.From != nil && t.To != nil && to.Before(from) {
			return time.Time{}, time.Time{}, false, fmt.Errorf("time range: to (%s) is before from (%s)", to.Format(time.RFC3339), from.Format(time.RFC3339))
		}
		return from, to, true, nil
	}
	if t.Last == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	m := relativeRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(t.Last)))
	if m == nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("invalid relative time range %q (expect like 7d/24h/30m/4w)", t.Last)
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return time.Time{}, time.Time{}, false, fmt.Errorf("relative time range must be positive: %q", t.Last)
	}
	var d time.Duration
	switch m[2] {
	case "m":
		d = time.Duration(n) * time.Minute
	case "h":
		d = time.Duration(n) * time.Hour
	case "d":
		d = time.Duration(n) * 24 * time.Hour
	case "w":
		d = time.Duration(n) * 7 * 24 * time.Hour
	}
	return now.Add(-d), now, true, nil
}

// OrderBy 排序项，字段必须来自本次查询选中的维度/指标(或时间桶列)。
type OrderBy struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Query 是一次统一报表查询请求。
type Query struct {
	Dataset     string      `json:"dataset"`
	Dimensions  []string    `json:"dimensions,omitempty"`
	Metrics     []string    `json:"metrics,omitempty"`
	Filters     []Filter    `json:"filters,omitempty"`
	TimeRange   *TimeRange  `json:"timeRange,omitempty"`
	Granularity Granularity `json:"granularity,omitempty"`
	OrderBy     []OrderBy   `json:"orderBy,omitempty"`
	Limit       int         `json:"limit,omitempty"`
}

// ColumnRole 区分结果列是维度还是指标，前端据此决定画图时的轴归属。
type ColumnRole string

const (
	RoleDimension ColumnRole = "dimension"
	RoleMetric    ColumnRole = "metric"
)

// Column 描述结果集的一列。
type Column struct {
	Name     string     `json:"name"`
	Label    string     `json:"label"`
	Type     FieldType  `json:"type"`
	Role     ColumnRole `json:"role"`
	Unit     string     `json:"unit,omitempty"`
	Decimals int        `json:"decimals,omitempty"`
}

// Result 是统一的查询结果，行数据按 Columns 顺序排列。
type Result struct {
	Columns   []Column `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Truncated bool     `json:"truncated"` // 是否因 Limit 被截断
	ElapsedMs int64    `json:"elapsedMs"`
}

// Resolver 由数据集提供方实现，负责把归一化后的 Query 变成结果。
// 实现方必须强制把 tenantID 下推为查询条件。
type Resolver interface {
	Resolve(ctx context.Context, tenantID string, q *Query, ds *Dataset) (*Result, error)
}

// ResolverFunc 让普通函数满足 Resolver 接口。
type ResolverFunc func(ctx context.Context, tenantID string, q *Query, ds *Dataset) (*Result, error)

func (f ResolverFunc) Resolve(ctx context.Context, tenantID string, q *Query, ds *Dataset) (*Result, error) {
	return f(ctx, tenantID, q, ds)
}

// Dataset 是一个对外可查询的数据集声明。
type Dataset struct {
	Name        string      `json:"name"`  // 全局唯一，建议 "模块.实体"，如 itsm.tickets
	Label       string      `json:"label"` // 展示名
	Module      string      `json:"module"`
	Description string      `json:"description,omitempty"`
	Dimensions  []Dimension `json:"dimensions"`
	Metrics     []Metric    `json:"metrics"`
	// TimeField 默认时间字段名，必须同时出现在 Dimensions 中且类型为 time。
	TimeField string   `json:"timeField,omitempty"`
	Resolver  Resolver `json:"-"`
}

// Dim 按名查维度。
func (d *Dataset) Dim(name string) (Dimension, bool) {
	for _, dim := range d.Dimensions {
		if dim.Name == name {
			return dim, true
		}
	}
	return Dimension{}, false
}

// Met 按名查指标。
func (d *Dataset) Met(name string) (Metric, bool) {
	for _, m := range d.Metrics {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}

// datasetNameRe 限制数据集命名，避免奇怪字符流入前端路由与 SQL 别名。
var datasetNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// fieldNameRe 限制字段命名，白名单校验的第一道闸门。
var fieldNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate 在注册时做静态自检，尽早暴露数据集声明错误。
func (d *Dataset) Validate() error {
	if d == nil {
		return fmt.Errorf("report: nil dataset")
	}
	if !datasetNameRe.MatchString(d.Name) {
		return fmt.Errorf("report: invalid dataset name %q (expect like itsm.tickets)", d.Name)
	}
	if d.Resolver == nil {
		return fmt.Errorf("report: dataset %q has nil resolver", d.Name)
	}
	if len(d.Metrics) == 0 {
		return fmt.Errorf("report: dataset %q must declare at least one metric", d.Name)
	}
	seen := make(map[string]bool, len(d.Dimensions)+len(d.Metrics))
	for _, dim := range d.Dimensions {
		if !fieldNameRe.MatchString(dim.Name) {
			return fmt.Errorf("report: dataset %q invalid dimension name %q", d.Name, dim.Name)
		}
		if seen[dim.Name] {
			return fmt.Errorf("report: dataset %q duplicate field %q", d.Name, dim.Name)
		}
		seen[dim.Name] = true
	}
	for _, m := range d.Metrics {
		if !fieldNameRe.MatchString(m.Name) {
			return fmt.Errorf("report: dataset %q invalid metric name %q", d.Name, m.Name)
		}
		if seen[m.Name] {
			return fmt.Errorf("report: dataset %q duplicate field %q", d.Name, m.Name)
		}
		if !validAggregations[m.Agg] {
			return fmt.Errorf("report: dataset %q metric %q has invalid aggregation %q", d.Name, m.Name, m.Agg)
		}
		seen[m.Name] = true
	}
	if d.TimeField != "" {
		dim, ok := d.Dim(d.TimeField)
		if !ok {
			return fmt.Errorf("report: dataset %q timeField %q not declared as dimension", d.Name, d.TimeField)
		}
		if dim.Type != FieldTime {
			return fmt.Errorf("report: dataset %q timeField %q must be of type time", d.Name, d.TimeField)
		}
	}
	return nil
}

// Normalize 依据数据集白名单校验查询并补齐默认值。
// 这是所有取数路径的统一闸门：任何未声明的字段都会在此被拒绝。
func (q *Query) Normalize(ds *Dataset) error {
	if ds == nil {
		return fmt.Errorf("report: nil dataset")
	}
	q.Dataset = ds.Name

	// 维度白名单 + 去重
	dims := make([]string, 0, len(q.Dimensions))
	dimSeen := make(map[string]bool, len(q.Dimensions))
	for _, name := range q.Dimensions {
		dim, ok := ds.Dim(name)
		if !ok {
			return fmt.Errorf("report: unknown dimension %q in dataset %q", name, ds.Name)
		}
		if !dim.Groupable {
			return fmt.Errorf("report: dimension %q is not groupable", name)
		}
		if dimSeen[name] {
			continue
		}
		dimSeen[name] = true
		dims = append(dims, name)
	}
	q.Dimensions = dims

	// 指标白名单 + 去重；一个都没选则默认取第一个指标
	mets := make([]string, 0, len(q.Metrics))
	metSeen := make(map[string]bool, len(q.Metrics))
	for _, name := range q.Metrics {
		if _, ok := ds.Met(name); !ok {
			return fmt.Errorf("report: unknown metric %q in dataset %q", name, ds.Name)
		}
		if metSeen[name] {
			continue
		}
		metSeen[name] = true
		mets = append(mets, name)
	}
	if len(mets) == 0 {
		mets = append(mets, ds.Metrics[0].Name)
		metSeen[ds.Metrics[0].Name] = true
	}
	q.Metrics = mets

	// 过滤条件白名单
	for i, f := range q.Filters {
		dim, ok := ds.Dim(f.Field)
		if !ok {
			return fmt.Errorf("report: unknown filter field %q in dataset %q", f.Field, ds.Name)
		}
		if !dim.Filterable {
			return fmt.Errorf("report: field %q is not filterable", f.Field)
		}
		if !validOps[f.Op] {
			return fmt.Errorf("report: unsupported filter operator %q", f.Op)
		}
		if f.Op == OpContains && dim.Type != FieldString {
			return fmt.Errorf("report: operator contains only applies to string field, got %q", f.Field)
		}
		switch f.Op {
		case OpIn, OpNotIn:
			if len(f.ValueList()) == 0 {
				return fmt.Errorf("report: filter %q with op %q requires a non-empty value list", f.Field, f.Op)
			}
		case OpBetween:
			if len(f.ValueList()) != 2 {
				return fmt.Errorf("report: filter %q with op between requires exactly 2 values", f.Field)
			}
		case OpIsNull, OpNotNull:
			q.Filters[i].Value = nil
		default:
			if f.Value == nil {
				return fmt.Errorf("report: filter %q with op %q requires a value", f.Field, f.Op)
			}
		}
	}

	// 时间粒度：需要数据集提供可用的时间字段
	if !validGranularity[q.Granularity] {
		return fmt.Errorf("report: unsupported granularity %q", q.Granularity)
	}
	if q.Granularity != GranNone {
		if _, err := q.timeField(ds); err != nil {
			return err
		}
	}
	if q.TimeRange != nil {
		if q.TimeRange.Field != "" {
			dim, ok := ds.Dim(q.TimeRange.Field)
			if !ok || dim.Type != FieldTime {
				return fmt.Errorf("report: invalid time range field %q in dataset %q", q.TimeRange.Field, ds.Name)
			}
		} else if ds.TimeField == "" {
			return fmt.Errorf("report: dataset %q has no default time field, timeRange.field is required", ds.Name)
		}
		if _, _, _, err := q.TimeRange.Resolve(time.Now()); err != nil {
			return err
		}
	}

	// 排序字段必须在本次结果列内，避免排序成为越权取数的旁路
	allowed := make(map[string]bool, len(dims)+len(mets)+1)
	for _, d := range dims {
		allowed[d] = true
	}
	for _, m := range mets {
		allowed[m] = true
	}
	if q.Granularity != GranNone {
		allowed[TimeBucketColumn] = true
	}
	for _, o := range q.OrderBy {
		if !allowed[o.Field] {
			return fmt.Errorf("report: order by field %q must be one of the selected dimensions/metrics", o.Field)
		}
	}
	// 未指定排序时给一个稳定默认：有时间桶按时间升序，否则按首个指标降序
	if len(q.OrderBy) == 0 {
		if q.Granularity != GranNone {
			q.OrderBy = []OrderBy{{Field: TimeBucketColumn}}
		} else if len(dims) > 0 {
			q.OrderBy = []OrderBy{{Field: mets[0], Desc: true}}
		}
	}

	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	return nil
}

// timeField 返回本次查询实际使用的时间字段。
func (q *Query) timeField(ds *Dataset) (string, error) {
	name := ds.TimeField
	if q.TimeRange != nil && q.TimeRange.Field != "" {
		name = q.TimeRange.Field
	}
	if name == "" {
		return "", fmt.Errorf("report: dataset %q has no time field, cannot apply granularity", ds.Name)
	}
	dim, ok := ds.Dim(name)
	if !ok || dim.Type != FieldTime {
		return "", fmt.Errorf("report: time field %q unavailable in dataset %q", name, ds.Name)
	}
	return name, nil
}

// TimeField 对外暴露本次查询使用的时间字段(供 Resolver 实现使用)。
func (q *Query) TimeField(ds *Dataset) (string, error) { return q.timeField(ds) }

// Columns 依据归一化后的查询推导结果列定义，供 Resolver 复用。
func (q *Query) Columns(ds *Dataset) []Column {
	cols := make([]Column, 0, len(q.Dimensions)+len(q.Metrics)+1)
	if q.Granularity != GranNone {
		cols = append(cols, Column{
			Name:  TimeBucketColumn,
			Label: "时间",
			Type:  FieldTime,
			Role:  RoleDimension,
		})
	}
	for _, name := range q.Dimensions {
		dim, _ := ds.Dim(name)
		label := dim.Label
		if label == "" {
			label = name
		}
		cols = append(cols, Column{Name: name, Label: label, Type: dim.Type, Role: RoleDimension})
	}
	for _, name := range q.Metrics {
		m, _ := ds.Met(name)
		label := m.Label
		if label == "" {
			label = name
		}
		cols = append(cols, Column{
			Name:     name,
			Label:    label,
			Type:     FieldNumber,
			Role:     RoleMetric,
			Unit:     m.Unit,
			Decimals: m.Decimals,
		})
	}
	return cols
}
