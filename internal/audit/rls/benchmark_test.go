package rls

import (
	"testing"
)

// BenchmarkEnforceSelect 基准: 多租户 SELECT 注入(含 WHERE 分支)。
func BenchmarkEnforceSelect(b *testing.B) {
	e := New()
	queries := []string{
		"SELECT * FROM cmdb_ci",
		"SELECT id, name FROM cmdb_ci WHERE status = 'active'",
		"SELECT * FROM cmdb_relation WHERE tenant_id = 'tenant-01'",
		"UPDATE cmdb_ci SET status = 'down' WHERE name = 'web-01'",
		"DELETE FROM cmdb_ci WHERE id = 'x'",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Enforce(queries[i%len(queries)], "tenant-01"); err != nil {
			b.Fatalf("enforce: %v", err)
		}
	}
}
