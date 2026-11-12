package agentic

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// PGTrajectoryRecorder 生产轨迹记录器: append-only 落 ai_trajectory_turns。
// 与 deploy/migrations/041_ai_agent_governance.sql 双写自愈 DDL(无迁移 runner)。
//
// 安全约束:
//   - 所有查询强制带 tenant_id, 端点侧不得绕过;
//   - 落盘前统一 FinalizeTrajectory 脱敏(凭据键置 [REDACTED]、长文本截断);
//   - 默认保留 30 天, 低频清理, 避免工具原始结果无限堆积。
type PGTrajectoryRecorder struct {
	db            *sql.DB
	retentionDays int

	purgeMu   sync.Mutex
	lastPurge time.Time
}

var _ TrajectoryRecorder = (*PGTrajectoryRecorder)(nil)

func NewPGTrajectoryRecorder(db *sql.DB) *PGTrajectoryRecorder {
	r := &PGTrajectoryRecorder{db: db, retentionDays: 30}
	r.init()
	return r
}

func (r *PGTrajectoryRecorder) init() {
	if _, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_trajectory_turns (
			id              TEXT PRIMARY KEY,
			tenant_id       TEXT NOT NULL DEFAULT 'default',
			conversation_id TEXT NOT NULL DEFAULT '',
			agent_id        TEXT NOT NULL DEFAULT '',
			user_id         TEXT NOT NULL DEFAULT '',
			trace_id        TEXT NOT NULL DEFAULT '',
			started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			data            JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		slog.Warn("ai trajectory store init DDL failed", "err", err)
	}
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_trajectory_tenant_conv ON ai_trajectory_turns(tenant_id, conversation_id, started_at DESC)`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_trajectory_tenant_created ON ai_trajectory_turns(tenant_id, created_at DESC)`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_trajectory_trace ON ai_trajectory_turns(tenant_id, trace_id)`)
}

// RecordTurn 写入一条完整轨迹; 重复 ID 幂等(回放/重试不产生重复记录)。
func (r *PGTrajectoryRecorder) RecordTurn(ctx context.Context, t *TrajectoryTurn) error {
	if t == nil {
		return nil
	}
	FinalizeTrajectory(t)
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO ai_trajectory_turns(id,tenant_id,conversation_id,agent_id,user_id,trace_id,started_at,data,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (id) DO NOTHING`,
		t.ID, t.TenantID, t.ConversationID, t.AgentID, t.UserID, t.TraceID, t.StartedAt, b, time.Now().UTC())
	if err != nil {
		return err
	}
	r.maybePurge(ctx)
	return nil
}

// ListTurns 按租户拉取轨迹; conversationID 为空时拉该租户全部。参数全部走 $N。
func (r *PGTrajectoryRecorder) ListTurns(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]*TrajectoryTurn, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	query := `SELECT data FROM ai_trajectory_turns WHERE tenant_id=$1`
	args := []any{tenantID}
	if conversationID != "" {
		query += ` AND conversation_id=$` + strconv.Itoa(len(args)+1)
		args = append(args, conversationID)
	}
	query += ` ORDER BY started_at DESC`
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query += ` LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	if offset > 0 {
		query += ` OFFSET $` + strconv.Itoa(len(args)+1)
		args = append(args, offset)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*TrajectoryTurn, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		t := &TrajectoryTurn{}
		if err := json.Unmarshal(raw, t); err != nil {
			slog.Warn("ai trajectory decode failed", "err", err)
			continue
		}
		if t.TenantID != tenantID { // 双保险: 拒绝任何越租户数据
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTurn 按租户 + turn ID 取单条; 不存在或跨租户返回 (nil, nil)。
func (r *PGTrajectoryRecorder) GetTurn(ctx context.Context, tenantID, turnID string) (*TrajectoryTurn, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	var raw []byte
	err := r.db.QueryRowContext(ctx,
		`SELECT data FROM ai_trajectory_turns WHERE tenant_id=$1 AND id=$2`, tenantID, turnID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t := &TrajectoryTurn{}
	if err := json.Unmarshal(raw, t); err != nil {
		return nil, err
	}
	if t.TenantID != tenantID {
		return nil, nil
	}
	return t, nil
}

// maybePurge 每小时最多清理一次过期轨迹, 避免在每次对话热路径上执行 DELETE。
func (r *PGTrajectoryRecorder) maybePurge(ctx context.Context) {
	r.purgeMu.Lock()
	if time.Since(r.lastPurge) < time.Hour {
		r.purgeMu.Unlock()
		return
	}
	r.lastPurge = time.Now()
	r.purgeMu.Unlock()
	if _, err := r.db.ExecContext(ctx, `DELETE FROM ai_trajectory_turns WHERE created_at < NOW() - make_interval(days => $1)`, r.retentionDays); err != nil {
		slog.Warn("ai trajectory retention purge failed", "err", err)
	}
}
