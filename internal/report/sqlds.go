package report

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SQLSpec 描述如何把一张(组)物理表映射成可查询数据集。
//
// 安全边界(务必遵守):
//   - Table / Where / DimExprs / MetricExprs 全部由业务模块的 Go 代码写死，
//     绝不允许来自 HTTP 请求，因此可以安全地拼进 SQL。
//   - 用户可控的只有"字段名"(必须命中白名单 map)与"值"(一律走占位符参数)。
//   - 租户条件由构建器强制注入，业务方无法遗漏。
type SQLSpec struct {
	// Table 是 FROM 子句内容，可为表名或带 JOIN 的片段。必须代码写死。
	Table string
	// TenantColumn 租户列，留空默认 tenant_id；显式设为 SQLNoTenant 表示该表无租户列。
	TenantColumn string
	// DimExprs 维度字段名 → SQL 表达式(留空则用字段名本身)。
	DimExprs map[string]string
	// MetricExprs 指标字段名 → 被聚合的 SQL 表达式(count 可留空)。
	MetricExprs map[string]string
	// Where 附加的固定条件(不带 WHERE 关键字)，如软删除过滤。
	Where string
}

// SQLNoTenant 用于声明该表没有租户列(如全局字典表)。
const SQLNoTenant = "-"

// granularityExpr 时间粒度 → date_trunc 单位。值来自常量白名单，非用户输入。
var granularityExpr = map[Granularity]string{
	GranHour:    "hour",
	GranDay:     "day",
	GranWeek:    "week",
	GranMonth:   "month",
	GranQuarter: "quarter",
	GranYear:    "year",
}

// castSuffix 依据字段声明类型给参数加显式类型转换，避免 PG 推断不出占位符类型。
func castSuffix(t FieldType) string {
	switch t {
	case FieldTime:
		return "::timestamptz"
	case FieldNumber:
		return "::numeric"
	case FieldBool:
		return "::boolean"
	default:
		return ""
	}
}

// SQLResolver 基于 SQLSpec 构建的通用解析器。
type SQLResolver struct {
	db   *sql.DB
	spec SQLSpec
}

// NewSQLResolver 创建 SQL 解析器。
func NewSQLResolver(db *sql.DB, spec SQLSpec) *SQLResolver {
	if spec.TenantColumn == "" {
		spec.TenantColumn = "tenant_id"
	}
	return &SQLResolver{db: db, spec: spec}
}

// NewSQLDataset 声明一个由 SQL 表支撑的数据集(最常用的入口)。
func NewSQLDataset(db *sql.DB, ds *Dataset, spec SQLSpec) *Dataset {
	ds.Resolver = NewSQLResolver(db, spec)
	return ds
}

// Spec 暴露只读的映射声明，供装配期自检(如与物理表结构比对)使用。
func (r *SQLResolver) Spec() SQLSpec { return r.spec }

// dimExpr 取维度的 SQL 表达式，未声明则回落到字段名本身。
// 字段名已在 Dataset.Validate 中被 ^[a-z][a-z0-9_]*$ 约束，可安全拼接。
func (r *SQLResolver) dimExpr(name string) string {
	if e, ok := r.spec.DimExprs[name]; ok && e != "" {
		return e
	}
	return name
}

// metricExpr 生成聚合表达式。
func (r *SQLResolver) metricExpr(m Metric) (string, error) {
	target := r.spec.MetricExprs[m.Name]
	switch m.Agg {
	case AggCount:
		if target == "" {
			return "COUNT(*)", nil
		}
		return fmt.Sprintf("COUNT(%s)", target), nil
	case AggCountDistinct:
		if target == "" {
			return "", fmt.Errorf("report: metric %q with count_distinct requires an expression", m.Name)
		}
		return fmt.Sprintf("COUNT(DISTINCT %s)", target), nil
	case AggSum, AggAvg, AggMin, AggMax:
		if target == "" {
			return "", fmt.Errorf("report: metric %q with %s requires an expression", m.Name, m.Agg)
		}
		return fmt.Sprintf("%s(%s)", strings.ToUpper(string(m.Agg)), target), nil
	default:
		return "", fmt.Errorf("report: unsupported aggregation %q", m.Agg)
	}
}

// argHolder 负责收集参数并生成 $N 占位符，保证任何值都不会拼进 SQL 文本。
type argHolder struct{ args []any }

func (a *argHolder) add(v any) string {
	a.args = append(a.args, v)
	return "$" + strconv.Itoa(len(a.args))
}

// BuildSQL 把归一化后的查询编译成参数化 SQL。导出以便单测直接验证生成结果。
func (r *SQLResolver) BuildSQL(tenantID string, q *Query, ds *Dataset) (string, []any, error) {
	if r.spec.Table == "" {
		return "", nil, fmt.Errorf("report: dataset %q has empty table spec", ds.Name)
	}
	h := &argHolder{}

	// ---- SELECT ----
	selects := make([]string, 0, len(q.Dimensions)+len(q.Metrics)+1)
	groupBy := make([]string, 0, len(q.Dimensions)+1)
	// 结果列名 → SELECT 中的序号(1-based)，ORDER BY/GROUP BY 用序号，杜绝标识符注入。
	ordinal := make(map[string]int, len(q.Dimensions)+len(q.Metrics)+1)

	if q.Granularity != GranNone {
		field, err := q.TimeField(ds)
		if err != nil {
			return "", nil, err
		}
		unit, ok := granularityExpr[q.Granularity]
		if !ok {
			return "", nil, fmt.Errorf("report: unsupported granularity %q", q.Granularity)
		}
		selects = append(selects, fmt.Sprintf("date_trunc('%s', %s) AS %s", unit, r.dimExpr(field), TimeBucketColumn))
		ordinal[TimeBucketColumn] = len(selects)
		groupBy = append(groupBy, strconv.Itoa(len(selects)))
	}
	for _, name := range q.Dimensions {
		selects = append(selects, fmt.Sprintf("%s AS %s", r.dimExpr(name), name))
		ordinal[name] = len(selects)
		groupBy = append(groupBy, strconv.Itoa(len(selects)))
	}
	for _, name := range q.Metrics {
		m, ok := ds.Met(name)
		if !ok {
			return "", nil, fmt.Errorf("report: unknown metric %q", name)
		}
		expr, err := r.metricExpr(m)
		if err != nil {
			return "", nil, err
		}
		selects = append(selects, fmt.Sprintf("%s AS %s", expr, name))
		ordinal[name] = len(selects)
	}

	// ---- WHERE ----
	var where []string
	if r.spec.TenantColumn != SQLNoTenant {
		where = append(where, fmt.Sprintf("%s = %s", r.spec.TenantColumn, h.add(tenantID)))
	}
	if strings.TrimSpace(r.spec.Where) != "" {
		where = append(where, "("+r.spec.Where+")")
	}
	if q.TimeRange != nil {
		from, to, ok, err := q.TimeRange.Resolve(time.Now())
		if err != nil {
			return "", nil, err
		}
		if ok {
			field := q.TimeRange.Field
			if field == "" {
				field = ds.TimeField
			}
			expr := r.dimExpr(field)
			if !from.IsZero() {
				where = append(where, fmt.Sprintf("%s >= %s::timestamptz", expr, h.add(from)))
			}
			if !to.IsZero() {
				where = append(where, fmt.Sprintf("%s <= %s::timestamptz", expr, h.add(to)))
			}
		}
	}
	for _, f := range q.Filters {
		dim, ok := ds.Dim(f.Field)
		if !ok {
			return "", nil, fmt.Errorf("report: unknown filter field %q", f.Field)
		}
		clause, err := r.buildFilter(h, dim, f)
		if err != nil {
			return "", nil, err
		}
		if clause != "" {
			where = append(where, clause)
		}
	}

	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(selects, ", "))
	b.WriteString(" FROM ")
	b.WriteString(r.spec.Table)
	if len(where) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(where, " AND "))
	}
	if len(groupBy) > 0 {
		b.WriteString(" GROUP BY ")
		b.WriteString(strings.Join(groupBy, ", "))
	}
	if len(q.OrderBy) > 0 {
		orders := make([]string, 0, len(q.OrderBy))
		for _, o := range q.OrderBy {
			pos, ok := ordinal[o.Field]
			if !ok {
				return "", nil, fmt.Errorf("report: order by field %q is not selected", o.Field)
			}
			dir := "ASC"
			if o.Desc {
				dir = "DESC"
			}
			orders = append(orders, fmt.Sprintf("%d %s", pos, dir))
		}
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(orders, ", "))
	}
	b.WriteString(" LIMIT " + h.add(q.Limit))
	return b.String(), h.args, nil
}

// buildFilter 生成单条过滤子句，所有用户值均通过占位符传入。
func (r *SQLResolver) buildFilter(h *argHolder, dim Dimension, f Filter) (string, error) {
	expr := r.dimExpr(f.Field)
	cast := castSuffix(dim.Type)
	switch f.Op {
	case OpIsNull:
		return fmt.Sprintf("%s IS NULL", expr), nil
	case OpNotNull:
		return fmt.Sprintf("%s IS NOT NULL", expr), nil
	case OpContains:
		return fmt.Sprintf("%s ILIKE %s", expr, h.add("%"+fmt.Sprint(f.Value)+"%")), nil
	case OpIn, OpNotIn:
		vals := f.ValueList()
		if len(vals) == 0 {
			return "", fmt.Errorf("report: empty value list for %q", f.Field)
		}
		phs := make([]string, 0, len(vals))
		for _, v := range vals {
			phs = append(phs, h.add(v)+cast)
		}
		op := "IN"
		if f.Op == OpNotIn {
			op = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", expr, op, strings.Join(phs, ", ")), nil
	case OpBetween:
		vals := f.ValueList()
		if len(vals) != 2 {
			return "", fmt.Errorf("report: between requires 2 values for %q", f.Field)
		}
		return fmt.Sprintf("%s BETWEEN %s%s AND %s%s", expr, h.add(vals[0]), cast, h.add(vals[1]), cast), nil
	default:
		ops := map[FilterOp]string{OpEq: "=", OpNe: "<>", OpGt: ">", OpGte: ">=", OpLt: "<", OpLte: "<="}
		sqlOp, ok := ops[f.Op]
		if !ok {
			return "", fmt.Errorf("report: unsupported operator %q", f.Op)
		}
		return fmt.Sprintf("%s %s %s%s", expr, sqlOp, h.add(f.Value), cast), nil
	}
}

// Resolve 执行查询并把结果规整成统一 Result。
func (r *SQLResolver) Resolve(ctx context.Context, tenantID string, q *Query, ds *Dataset) (*Result, error) {
	if r.db == nil {
		return nil, fmt.Errorf("report: dataset %q has no database connection", ds.Name)
	}
	query, args, err := r.BuildSQL(tenantID, q, ds)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("report: query dataset %q failed: %w", ds.Name, err)
	}
	defer rows.Close()

	cols := q.Columns(ds)
	out := make([][]any, 0, 64)
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("report: scan dataset %q failed: %w", ds.Name, err)
		}
		row := make([]any, len(cols))
		for i, v := range raw {
			row[i] = normalizeValue(v, cols[i].Role == RoleMetric)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("report: iterate dataset %q failed: %w", ds.Name, err)
	}
	return &Result{Columns: cols, Rows: out}, nil
}

// normalizeValue 把驱动返回的原始值规整成 JSON 友好类型。
// lib/pq 对 NUMERIC/时间等类型会返回 []byte，需要显式转换。
func normalizeValue(v any, numeric bool) any {
	switch x := v.(type) {
	case nil:
		if numeric {
			return 0
		}
		return nil
	case []byte:
		s := string(x)
		if numeric {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
		return s
	case time.Time:
		return x.Format(time.RFC3339)
	case string:
		if numeric {
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f
			}
		}
		return x
	default:
		return v
	}
}
