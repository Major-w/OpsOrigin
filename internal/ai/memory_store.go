// 长期记忆持久化: ai_memories 表 + 引擎 MemoryPersist/Load 回调实现。
// CRUD 端点在 memory_handlers.go(已有), 这里只负责落库与启动加载。
package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/ai/agentic"
)

// initMemoryDDL 幂等建表(记忆持久化)。
func (s *Service) initMemoryDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_memories (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			keywords JSONB NOT NULL DEFAULT '[]',
			problem TEXT NOT NULL,
			resolution TEXT NOT NULL,
			outcome TEXT NOT NULL DEFAULT 'succeeded',
			source TEXT NOT NULL DEFAULT 'manual',
			hit_count INT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL,
			last_hit_at TIMESTAMPTZ
		)`); err != nil {
		slog.Warn("ai memories DDL failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_memories_uniq ON ai_memories(tenant_id, agent_id, problem)`); err != nil {
		slog.Warn("ai memories unique index failed", "err", err)
	}
}

// PersistMemory 引擎 MemoryPersist 回调: 记忆落库(upsert)。
func (s *Service) PersistMemory(ctx context.Context, m *agentic.ExperienceMemory) error {
	if s.db == nil {
		return nil
	}
	kw, _ := json.Marshal(m.Keywords)
	if m.Keywords == nil {
		kw = []byte("[]")
	}
	var lastHit *time.Time
	if m.LastHitAt != nil {
		t := *m.LastHitAt
		lastHit = &t
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_memories (id, tenant_id, agent_id, category, keywords, problem, resolution, outcome, source, hit_count, created_at, last_hit_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (tenant_id, agent_id, problem) DO UPDATE SET
			resolution=EXCLUDED.resolution, category=EXCLUDED.category,
			keywords=EXCLUDED.keywords, outcome=EXCLUDED.outcome,
			source=EXCLUDED.source, last_hit_at=EXCLUDED.last_hit_at`,
		m.ID, m.TenantID, m.AgentID, m.Category, kw, m.Problem, m.Resolution,
		m.Outcome, m.Source, m.HitCount, m.CreatedAt, lastHit)
	return err
}

// LoadMemoriesFromDB 引擎 MemoryLoad 回调: 全量读取记忆。
func (s *Service) LoadMemoriesFromDB(ctx context.Context, tenantID string) ([]*agentic.ExperienceMemory, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, agent_id, category, keywords, problem, resolution, outcome, source, hit_count, created_at, last_hit_at
		 FROM ai_memories WHERE tenant_id=$1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*agentic.ExperienceMemory{}
	for rows.Next() {
		var m agentic.ExperienceMemory
		var kw []byte
		var last sql.NullTime
		if err := rows.Scan(&m.ID, &m.TenantID, &m.AgentID, &m.Category, &kw, &m.Problem, &m.Resolution,
			&m.Outcome, &m.Source, &m.HitCount, &m.CreatedAt, &last); err != nil {
			continue
		}
		if last.Valid {
			t := last.Time
			m.LastHitAt = &t
		}
		if len(kw) > 0 {
			_ = json.Unmarshal(kw, &m.Keywords)
		}
		out = append(out, &m)
	}
	return out, nil
}
