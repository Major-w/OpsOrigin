package db

import (
	"database/sql"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/opsorigin/core/internal/ent"
)

// NewEntClient 创建 ent Client，使用给定的 *sql.DB 作为底层 driver。
// Phase 1 禁用 AutoMigration，仅使用 ent CRUD。
func NewEntClient(drv *sql.DB) *ent.Client {
	drvWrap := entsql.OpenDB(dialect.Postgres, drv)
	return ent.NewClient(ent.Driver(drvWrap))
}
