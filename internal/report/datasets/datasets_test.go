package datasets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/report"
)

// TestBuiltinDatasetsValidate 保证内置声明本身合法(字段命名、聚合、时间字段等)。
func TestBuiltinDatasetsValidate(t *testing.T) {
	reg := report.NewRegistry()
	for _, ds := range builtin(nil) {
		if err := reg.Register(ds); err != nil {
			t.Fatalf("dataset %q invalid: %v", ds.Name, err)
		}
	}
	if got := len(reg.Names()); got != 5 {
		t.Fatalf("expected 5 builtin datasets, got %d", got)
	}
}

// TestBuiltinDatasetsBuildSQL 对每个数据集跑一遍典型查询，确认能编译出参数化 SQL。
func TestBuiltinDatasetsBuildSQL(t *testing.T) {
	for _, ds := range builtin(nil) {
		r, ok := ds.Resolver.(*report.SQLResolver)
		if !ok {
			t.Fatalf("dataset %q should use SQLResolver", ds.Name)
		}
		// 取第一个可分组维度 + 全部指标，按天聚合
		var dim string
		for _, d := range ds.Dimensions {
			if d.Groupable && d.Type != report.FieldTime {
				dim = d.Name
				break
			}
		}
		mets := make([]string, 0, len(ds.Metrics))
		for _, m := range ds.Metrics {
			mets = append(mets, m.Name)
		}
		q := report.Query{
			Dataset:     ds.Name,
			Metrics:     mets,
			Granularity: report.GranDay,
			TimeRange:   &report.TimeRange{Last: "7d"},
		}
		if dim != "" {
			q.Dimensions = []string{dim}
		}
		if err := q.Normalize(ds); err != nil {
			t.Fatalf("dataset %q normalize failed: %v", ds.Name, err)
		}
		sqlText, args, err := r.BuildSQL("tenant-x", &q, ds)
		if err != nil {
			t.Fatalf("dataset %q build sql failed: %v", ds.Name, err)
		}
		if !strings.Contains(sqlText, "FROM "+r.Spec().Table) {
			t.Fatalf("dataset %q should query its own table, got: %s", ds.Name, sqlText)
		}
		if len(args) == 0 || args[0] != "tenant-x" {
			t.Fatalf("dataset %q must bind tenant first, args=%v", ds.Name, args)
		}
	}
}

// —— 与物理表结构比对：防止数据集声明与迁移脚本漂移 ——

var (
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s*\((.*?)\n\s*\);`)
	identRe       = regexp.MustCompile(`[a-z_][a-z0-9_]*`)
)

// sqlKeywords 出现在指标表达式里的 SQL 关键字，不参与列名校验。
var sqlKeywords = map[string]bool{
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"not": true, "and": true, "or": true, "null": true, "is": true,
	"distinct": true, "coalesce": true, "nullif": true, "cast": true,
	"extract": true, "epoch": true, "from": true, "interval": true,
}

// loadSchema 解析迁移脚本，得到 表名 → 列名集合。
func loadSchema(t *testing.T) map[string]map[string]bool {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "deploy", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("migrations dir unavailable: %v", err)
	}
	schema := make(map[string]map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range createTableRe.FindAllStringSubmatch(string(raw), -1) {
			table := m[1]
			cols, ok := schema[table]
			if !ok {
				cols = make(map[string]bool)
				schema[table] = cols
			}
			for _, line := range strings.Split(m[2], "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "--") {
					continue
				}
				upper := strings.ToUpper(line)
				// 跳过表级约束行
				if strings.HasPrefix(upper, "PRIMARY KEY") || strings.HasPrefix(upper, "FOREIGN KEY") ||
					strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "CONSTRAINT") ||
					strings.HasPrefix(upper, "CHECK") {
					continue
				}
				name := identRe.FindString(line)
				if name != "" {
					cols[name] = true
				}
			}
		}
		// ALTER TABLE ... ADD COLUMN 追加的列同样纳入
		for _, m := range regexp.MustCompile(`(?i)ALTER\s+TABLE\s+([a-z_][a-z0-9_]*)\s+ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`).
			FindAllStringSubmatch(string(raw), -1) {
			table, col := strings.ToLower(m[1]), strings.ToLower(m[2])
			if schema[table] == nil {
				schema[table] = make(map[string]bool)
			}
			schema[table][col] = true
		}
	}
	return schema
}

// TestBuiltinDatasetsMatchSchema 逐个校验维度名与指标表达式引用的列在物理表中真实存在。
// 这是防止"数据集声明 ↔ 迁移脚本"漂移的护栏：改表结构却忘改数据集时会在这里失败。
func TestBuiltinDatasetsMatchSchema(t *testing.T) {
	schema := loadSchema(t)
	if len(schema) == 0 {
		t.Skip("no schema parsed")
	}

	for _, ds := range builtin(nil) {
		r := ds.Resolver.(*report.SQLResolver)
		spec := r.Spec()
		cols, ok := schema[spec.Table]
		if !ok {
			t.Errorf("dataset %q: table %q not found in migrations", ds.Name, spec.Table)
			continue
		}
		if spec.TenantColumn != report.SQLNoTenant && !cols[spec.TenantColumn] {
			t.Errorf("dataset %q: tenant column %q missing on table %q", ds.Name, spec.TenantColumn, spec.Table)
		}
		for _, d := range ds.Dimensions {
			if _, overridden := spec.DimExprs[d.Name]; overridden {
				continue
			}
			if !cols[d.Name] {
				t.Errorf("dataset %q: dimension %q is not a column of %q", ds.Name, d.Name, spec.Table)
			}
		}
		if ds.TimeField != "" && !cols[ds.TimeField] {
			if _, overridden := spec.DimExprs[ds.TimeField]; !overridden {
				t.Errorf("dataset %q: time field %q is not a column of %q", ds.Name, ds.TimeField, spec.Table)
			}
		}
		for metric, expr := range spec.MetricExprs {
			for _, id := range identRe.FindAllString(strings.ToLower(expr), -1) {
				if sqlKeywords[id] || cols[id] {
					continue
				}
				t.Errorf("dataset %q metric %q: identifier %q in expression %q is not a column of %q",
					ds.Name, metric, id, expr, spec.Table)
			}
		}
	}
}
